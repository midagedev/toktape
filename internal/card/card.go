package card

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// Version is the toktape version printed in the card header when the summary
// itself does not carry one. The CLI sets it at build time.
var Version = "dev"

// footerText is the card's last line (docs/toktape-spec.ko.md §4 item 11).
const footerText = "toktape · github.com/midagedev/toktape"

// Label gutters. Everything in a section lines up under the same column so the
// card reads as one fixed layout regardless of which fields were observed.
const (
	blockLabelW = 9  // MODEL / ENGINE / RIG / MEMORY / HOST / FLAGS
	speedLabelW = 14 // Decode / Prefill / Context / Prefix cache / Streams
	vramBarW    = 10 // cells in the VRAM bar
)

// Text renders the result card as a Unicode box.
//
// Every line is exactly CardWidth display columns wide, measured with East
// Asian width, and the output contains no ANSI escapes so it can be pasted
// into a Reddit code block verbatim. A nil summary renders the all-unknown
// card rather than panicking.
func Text(s *tape.RunSummary) string {
	if s == nil {
		s = &tape.RunSummary{}
	}

	sections := [][]string{
		{headerLine(s)},
		identitySection(s),
		speedSection(s),
		memorySection(s),
		hostSection(s),
		flagsSection(s),
	}
	if w := warningSection(s); len(w) > 0 {
		sections = append(sections, w)
	}
	sections = append(sections, []string{center(footerText, innerWidth)})

	var b strings.Builder
	b.WriteString("┌" + repeat('─', CardWidth-2) + "┐\n")
	for i, sec := range sections {
		if i > 0 {
			b.WriteString("├" + repeat('─', CardWidth-2) + "┤\n")
		}
		for _, line := range sec {
			b.WriteString("│ " + pad(line, innerWidth) + " │\n")
		}
	}
	b.WriteString("└" + repeat('─', CardWidth-2) + "┘\n")
	return b.String()
}

// Markdown wraps Text in a ```text fence, appends the llama-bench compatible
// table and closes with the folded Reproduce block, so the whole thing can be
// pasted as one comment: the figures first, then the same figures in the table
// the thread is already using, then the provenance that answers "paste your
// command" before anyone asks it.
func Markdown(s *tape.RunSummary) string {
	return "```text\n" + Text(s) + "```\n\n" + LlamaBenchTable(s) + "\n" + Reproduce(s)
}

// JSON renders the summary as indented JSON. Key order is the struct order of
// tape.RunSummary, which is the schema order; map keys are sorted by
// encoding/json, so the output is stable across runs.
func JSON(s *tape.RunSummary) ([]byte, error) {
	if s == nil {
		s = &tape.RunSummary{}
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("card: marshal run summary: %w", err)
	}
	return append(b, '\n'), nil
}

// ---------------------------------------------------------------- layout ---

// labelled puts label in the first line's gutter and indents the rest under it.
func labelled(label string, labelW int, lines []string) []string {
	if len(lines) == 0 {
		lines = []string{""}
	}
	avail := innerWidth - labelW
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		gutter := strings.Repeat(" ", labelW)
		if i == 0 {
			gutter = pad(label, labelW)
		}
		out = append(out, gutter+truncate(l, avail))
	}
	return out
}

// field joins parts with sep and wraps them under label at separator
// boundaries. A single part wider than the gutter-adjusted width is truncated
// with "…"; parts are never dropped.
func field(label string, labelW int, sep string, parts ...string) []string {
	return labelled(label, labelW, wrapJoin(parts, sep, innerWidth-labelW))
}

// wrapJoin lays parts out on as few lines of avail columns as possible,
// joining them with sep and breaking only between parts.
func wrapJoin(parts []string, sep string, avail int) []string {
	var lines []string
	cur := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if cur == "" {
			cur = truncate(p, avail)
			continue
		}
		if Width(cur)+Width(sep)+Width(p) <= avail {
			cur += sep + p
			continue
		}
		lines = append(lines, cur)
		cur = truncate(p, avail)
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// center left-pads s so it sits in the middle of w columns. pad fills the right.
func center(s string, w int) string {
	s = truncate(s, w)
	left := (w - Width(s)) / 2
	if left < 0 {
		left = 0
	}
	return strings.Repeat(" ", left) + s
}

// --------------------------------------------------------------- sections ---

func headerLine(s *tape.RunSummary) string {
	left := versionString(s)
	right := orUnknown(s.ID)
	if Width(left)+1+Width(right) > innerWidth {
		right = truncate(right, innerWidth-Width(left)-1)
	}
	gap := innerWidth - Width(left) - Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// versionString renders "toktape v0.1.0". The summary's own version wins over
// the package var so a replayed tape shows the version that recorded it.
func versionString(s *tape.RunSummary) string {
	v := s.ToktapeVersion
	if v == "" {
		v = Version
	}
	if v == "" {
		return "toktape " + unknown
	}
	// Only a numeric version gets the "v" prefix; "dev" stays "dev".
	if v[0] >= '0' && v[0] <= '9' {
		v = "v" + v
	}
	return "toktape " + v
}

func identitySection(s *tape.RunSummary) []string {
	var out []string
	// A shard set names its variant directory and says how many parts it has;
	// a single file is unchanged (TTP-32). shardsPart is empty for a single
	// file and wrapJoin drops it, so the existing goldens do not move.
	out = append(out, field("MODEL", blockLabelW, " · ",
		orUnknown(ModelLabel(s.Model)),
		shardsPart(s.Model),
		orUnknown(s.Model.Quant),
		formatGiB(s.Model.FileBytes),
	)...)

	engineParts := []string{engineString(s.Server), osString(s.Host)}
	if s.Host.Hostname != "" {
		engineParts = append(engineParts, s.Host.Hostname)
	}
	out = append(out, field("ENGINE", blockLabelW, " · ", engineParts...)...)

	rigParts := append(rigGPUs(s.Host.GPUs), orUnknown(s.Host.CPU), ramString(s.Host))
	out = append(out, field("RIG", blockLabelW, " · ", rigParts...)...)
	return out
}

func engineString(srv tape.ServerInfo) string {
	kind := string(srv.Kind)
	if kind == "" || srv.Kind == tape.ServerUnknown {
		kind = unknown
	}
	if kind == unknown && srv.Build == "" && srv.Commit == "" {
		return unknown
	}
	var b strings.Builder
	b.WriteString(kind)
	switch {
	case srv.Build != "" && srv.Commit != "":
		b.WriteString(" " + srv.Build + " (" + srv.Commit + ")")
	case srv.Build != "":
		b.WriteString(" " + srv.Build)
	case srv.Commit != "":
		b.WriteString(" " + bareCommit(srv))
	default:
		b.WriteString(" " + unknown)
	}
	return b.String()
}

// bareCommit renders a commit that has no build number beside it.
//
// The parentheses on "llama-server (abcdef12)" are what separates the hash
// from the build number it belongs to. ik_llama.cpp has no build number at all
// (TTP-33, 2026-09-13) — the hash is the whole version — so bracketing it
// would set apart a figure there is nothing to set it apart from, and the line
// reads "ik_llama.cpp 7b79b229" instead. Every other engine keeps the
// parentheses, because for them a missing build number is a gap, not a fact
// about the project, and the brackets are where that gap shows.
//
// internal/card/png/content.go carries the same rule: the two renderings of one
// summary must never disagree.
func bareCommit(srv tape.ServerInfo) string {
	if srv.Kind == tape.ServerIKLlama {
		return srv.Commit
	}
	return "(" + srv.Commit + ")"
}

func osString(h tape.HostInfo) string {
	s := strings.TrimSpace(h.OS + " " + h.Kernel)
	return orUnknown(s)
}

// rigGPUs collapses identical devices into "2× RTX 3090 24G".
func rigGPUs(gpus []tape.GPUInfo) []string {
	if len(gpus) == 0 {
		return []string{unknown}
	}
	type group struct {
		name string
		vram int64
		n    int
	}
	var order []*group
	seen := map[string]*group{}
	for _, g := range gpus {
		name := shortGPUName(g.Name)
		key := name + "|" + strconv.FormatInt(g.VRAMBytes, 10)
		e, ok := seen[key]
		if !ok {
			e = &group{name: name, vram: g.VRAMBytes}
			seen[key] = e
			order = append(order, e)
		}
		e.n++
	}
	out := make([]string, 0, len(order))
	for _, e := range order {
		label := orUnknown(e.name)
		if e.vram > 0 {
			label += fmt.Sprintf(" %.0fG", float64(e.vram)/gib)
		}
		if e.n > 1 {
			label = strconv.Itoa(e.n) + "× " + label
		}
		out = append(out, label)
	}
	return out
}

// shortGPUName drops the vendor prefixes nvidia-smi reports so the RIG line
// fits. It shortens, it never invents.
func shortGPUName(n string) string {
	n = strings.TrimSpace(n)
	for _, prefix := range []string{"NVIDIA GeForce ", "NVIDIA ", "GeForce "} {
		if strings.HasPrefix(n, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(n, prefix))
		}
	}
	return n
}

func ramString(h tape.HostInfo) string {
	// A bare "?" and not "? GB": the unit belongs to a number that was
	// measured, and "? GB" reads as a quantity of gigabytes nobody counted.
	if h.RAMBytes == 0 {
		return unknown
	}
	s := fmt.Sprintf("%.0f GB", float64(h.RAMBytes)/gib)
	// RAM speed is omitted when unknown rather than printed as "?": it is a
	// nice-to-have detail, not one of the argument-settling fields.
	if h.RAMSpeed != "" {
		s += " " + h.RAMSpeed
	}
	return s
}

func speedSection(s *tape.RunSummary) []string {
	t := s.Timings
	var out []string

	// Lesson 2: a short generation is a "sample", never a "decode" rate.
	decodeLabel := "Decode"
	if t.DecodeLabel == "sample" {
		decodeLabel = "Sample"
	}
	decodeParts := []string{formatRateUnit(t.PredictedPerSecond)}
	if bw := bandwidthString(s); bw != "" {
		decodeParts = append(decodeParts, bw)
	}
	out = append(out, field(decodeLabel, speedLabelW, " · ", decodeParts...)...)

	promptTotal := promptTokens(s)
	out = append(out, field("Prefill", speedLabelW, " · ",
		formatRateUnit(t.PromptPerSecond),
		"TTFT "+formatMs(t.TTFTMs),
		formatInt(promptTotal)+" prompt tokens",
	)...)

	if parts := draftParts(s); len(parts) > 0 {
		out = append(out, field("Draft", speedLabelW, " · ", parts...)...)
	}

	out = append(out, labelled("Context", speedLabelW, []string{
		contextString(s.Server.CtxSize, promptTotal, t.PredictedN, reasoningTokens(s)),
	})...)

	out = append(out, labelled("Prefix cache", speedLabelW, []string{cacheString(s.Cache)})...)

	if s.Concurrency > 1 {
		out = append(out, streamLines(s)...)
	}
	out = append(out, roundsLines(s)...)
	out = append(out, sweepLines(s)...)
	return out
}

// maxRoundsListed is how many rounds the Prompts row names before it says how
// many more there were. Eight is four lines at two a line: a prompts file of a
// hundred lines must not turn the card into a table.
const maxRoundsListed = 8

// roundsLines is the Prompts row of a multi-prompt run, or nil for a run of one
// round (TTP-31, 2026-09-13).
//
//	Prompts       6 rounds · 14.8 tok/s median (9.1–19.3)
//	              accepted 61% median (13–87%)
//	              1 sql-1 19.3 tok/s 87%  ·  2 sql-2 18.4 tok/s 83%
//
// The row exists because the same draft model is accepted 13 % on prose and
// 87 % on SQL: a single prompt's rate is not the rig's rate, and neither is the
// mean the Decode row prints, which a few long prose rounds pull around. The
// median with its range is what a reader compares against their own run, and
// the per-round list shows which prompt was which. The accepted clause is only
// printed when some round reported a draft, and the list's own percentages
// only for the rounds that did.
//
// It is the last row of the speed section so the Streams block above it keeps
// its meaning: N × the per-stream mean over every round.
func roundsLines(s *tape.RunSummary) []string {
	if s.Rounds <= 1 {
		return nil
	}
	avail := innerWidth - speedLabelW
	head := []string{fmt.Sprintf("%d rounds", s.Rounds)}
	if sp := s.Spread; sp != nil {
		if r := sp.PerStreamPredictedPerSecond; r.Median > 0 {
			head = append(head, fmt.Sprintf("%s median (%s–%s)",
				formatRateUnit(r.Median), formatRate(r.Min), formatRate(r.Max)))
		}
		if a := sp.DraftAcceptRate; a.Median != 0 || a.Min != 0 || a.Max != 0 {
			head = append(head, fmt.Sprintf("accepted %s median (%s–%s)",
				formatPct(a.Median), pctNumber(a.Min), formatPct(a.Max)))
		}
	}
	lines := wrapJoin(head, " · ", avail)

	var parts []string
	// pos is each round's 1-based place among the rounds sent with the same
	// speculative n_max, which names an unnamed round in a sweep.
	pos := map[int]int{}
	for i, p := range s.PerRound {
		if i == maxRoundsListed {
			parts = append(parts, fmt.Sprintf("… +%d more", len(s.PerRound)-maxRoundsListed))
			break
		}
		pos[p.SpecNMax]++
		parts = append(parts, roundPart(p, pos[p.SpecNMax]))
	}
	if len(parts) > 0 {
		lines = append(lines, wrapJoin(parts, "  ·  ", avail)...)
	}
	return labelled("Prompts", speedLabelW, lines)
}

// roundPart is one round of the Prompts list: "1 sql-1 19.3 tok/s 87%". The
// name is left out when the prompts line had none; the acceptance rate when the
// round reported no draft, and a round that drafted nothing says so rather than
// "0%", as the Draft row does.
//
// A round of a speculative n_max sweep (TTP-35) is labelled by the n_max it ran
// at instead of its run-order number: "3·sql-1 19.3 tok/s 87%", or "3·2" for
// the second unnamed prompt at that value, which is pos. In a sweep the
// run-order number counts every value's copy of the prompt set, and "9" says
// less than "5·sql-2".
func roundPart(p tape.RoundSummary, pos int) string {
	var fields []string
	switch {
	case p.SpecNMax > 0 && p.Name != "":
		fields = []string{strconv.Itoa(p.SpecNMax) + "·" + p.Name}
	case p.SpecNMax > 0:
		fields = []string{strconv.Itoa(p.SpecNMax) + "·" + strconv.Itoa(pos)}
	default:
		fields = []string{strconv.Itoa(p.Index + 1)}
		if p.Name != "" {
			fields = append(fields, p.Name)
		}
	}
	fields = append(fields, formatRateUnit(p.PerStreamPredictedPerSecond))
	if p.DraftN != nil {
		if *p.DraftN == 0 {
			fields = append(fields, "0 drafted")
		} else {
			accepted := 0
			if p.DraftNAccepted != nil {
				accepted = *p.DraftNAccepted
			}
			fields = append(fields, formatPct(float64(accepted)/float64(*p.DraftN)))
		}
	}
	return strings.Join(fields, " ")
}

// sweepLines is the Draft sweep row of a speculative n_max sweep, or nil when
// the run swept fewer than two values (TTP-35, 2026-09-13).
//
//	Draft sweep   n_max 3  14.8 tok/s median  61% accepted
//	              n_max 5  16.1 tok/s median  51% accepted · fastest
//
// The row answers the question the sweep was run to ask — which block size is
// fastest on this prompt mix — so each value gets the median over its own
// prompts, the figure the Prompts row uses for the run, and its acceptance.
// The columns are padded to the widest value so the rates can be read down
// the row. A value none of whose rounds reported a draft prints "?" accepted;
// "fastest" is only claimed when two values were measured and one was faster.
func sweepLines(s *tape.RunSummary) []string {
	groups := s.BySpecNMax
	if len(groups) < 2 {
		return nil
	}
	nmax := make([]string, len(groups))
	rate := make([]string, len(groups))
	pct := make([]string, len(groups))
	var nmaxW, rateW, pctW int
	for i, g := range groups {
		nmax[i] = strconv.Itoa(g.NMax)
		rate[i] = formatRate(g.Spread.PerStreamPredictedPerSecond.Median)
		pct[i] = unknown
		if a := g.Spread.DraftAcceptRate; a.Median != 0 || a.Min != 0 || a.Max != 0 {
			pct[i] = formatPct(a.Median)
		}
		nmaxW, rateW, pctW = max(nmaxW, Width(nmax[i])), max(rateW, Width(rate[i])), max(pctW, Width(pct[i]))
	}
	fastest := FastestSpecNMax(groups)
	lines := make([]string, len(groups))
	for i := range groups {
		lines[i] = "n_max " + padLeft(nmax[i], nmaxW) + "  " + padLeft(rate[i], rateW) +
			" tok/s median  " + padLeft(pct[i], pctW) + " accepted"
		if i == fastest {
			lines[i] += " · fastest"
		}
	}
	return labelled("Draft sweep", speedLabelW, lines)
}

// FastestSpecNMax is the index of the sweep group with the highest median
// per-stream rate, or -1 when there is no such single group: fewer than two
// groups measured a rate, or the highest rate is shared. The PNG's best-n_max
// clause uses it too, so the two renderings name the same value.
func FastestSpecNMax(groups []tape.SpecNMaxGroup) int {
	best, observed, tied := -1, 0, false
	for i, g := range groups {
		m := g.Spread.PerStreamPredictedPerSecond.Median
		if m <= 0 {
			continue
		}
		observed++
		switch {
		case best < 0 || m > groups[best].Spread.PerStreamPredictedPerSecond.Median:
			best, tied = i, false
		case m == groups[best].Spread.PerStreamPredictedPerSecond.Median:
			tied = true
		}
	}
	if observed < 2 || tied {
		return -1
	}
	return best
}

// padLeft right-aligns s in w display columns.
func padLeft(s string, w int) string {
	if d := w - Width(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

// pctNumber is formatPct without the sign, for the low end of a range whose
// high end carries it: "(13–87%)".
func pctNumber(ratio float64) string {
	return strings.TrimSuffix(formatPct(ratio), "%")
}

// draftParts is the Draft row, or nil when the run used no speculative
// decoding (TTP-30, 2026-09-13).
//
//	Draft         DSpark-0.6B-Q8_0.gguf · n_max 3 · 60% accepted (174/290)
//
// The row exists because a decode rate on its own does not say where the speed
// came from. Speculative decoding makes the rate a property of three things —
// the draft model, the block size it was allowed to guess in, and how often
// the target agreed — and a reader who wants the same number on their own box
// needs all three. The acceptance figure carries its own counts because a
// percentage over an unstated denominator is the kind of number this card
// exists to replace.
//
// The row's presence is decided by the reported figure, not by the flags: the
// server's timings object is what says a draft actually ran. A model name that
// is empty prints "?" like every other unobserved field — a draft cannot run
// without a model, so "" means nobody read the command line, never that the
// server had a default.
func draftParts(s *tape.RunSummary) []string {
	t := s.Timings
	if t.DraftN == nil {
		return nil
	}
	f := s.Server.Flags
	parts := []string{
		orUnknown(f.DraftModel),
		"n_max " + DraftNMax(s),
	}
	// Zero drafted is a reading, and it is not "0% accepted": there was no
	// denominator, so there is no rate to report.
	if *t.DraftN == 0 {
		return append(parts, "0 drafted")
	}
	accepted := 0
	if t.DraftNAccepted != nil {
		accepted = *t.DraftNAccepted
	}
	return append(parts, fmt.Sprintf("%s accepted (%d/%d)",
		formatPct(float64(accepted)/float64(*t.DraftN)), accepted, *t.DraftN))
}

// DraftNMax is the block size the Draft row names: the speculative.n_max
// values a --spec-n-max sweep's requests carried, "3,5", or the server's
// --draft-max when the run swept nothing. A sweep overrode the flag on every
// request, so printing the flag beside a sweep's acceptance would name a value
// that never ran (TTP-35, 2026-09-13). The PNG's draft clause uses it too.
func DraftNMax(s *tape.RunSummary) string {
	if len(s.SpecNMax) == 0 {
		return orUnknown(s.Server.Flags.DraftMax)
	}
	parts := make([]string, len(s.SpecNMax))
	for i, v := range s.SpecNMax {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

// contextString is the value of the Context row: the window the server was
// started with and how much of it this run used.
//
//	16384 (43 in / 96 out)
//	16384 (43 in / 96 out · 96 thinking)
//
// A thinking model's reasoning tokens are already inside the "out" figure —
// the server counts them in predicted_n, so every rate on the card includes
// them (TTP-20, 2026-09-13). The trailing clause says how many of them there
// were, because "96 out" for a run that spent the whole budget thinking is a
// different fact from 96 tokens of answer, and the reader cannot tell the two
// apart from the number alone. It is omitted when thinking is 0, which is both
// "not a thinking model" and "did not think" — neither is worth a clause.
func contextString(ctxSize, in, out, thinking int) string {
	used := fmt.Sprintf("%s in / %s out", formatInt(in), formatInt(out))
	if thinking > 0 {
		used += " · " + formatInt(thinking) + " thinking"
	}
	return fmt.Sprintf("%s (%s)", formatInt(ctxSize), used)
}

// reasoningTokens is how many of the run's generated tokens were a thinking
// model's reasoning_content. 0 for every model that does not think, and for a
// thinking model that answered without thinking.
func reasoningTokens(s *tape.RunSummary) int { return s.Timings.ReasoningN }

// promptTokens is the whole prompt the run sent, cached prefix included.
func promptTokens(s *tape.RunSummary) int {
	if s.Cache.PromptTotal > 0 {
		return s.Cache.PromptTotal
	}
	return s.Timings.PromptN + s.Timings.CacheN
}

// bandwidthString renders "≈ 91 GB/s, 9% of peak". Empty when the effective
// bandwidth was not derivable; the "≈" marks it as an estimate (spec §3.2 S6).
func bandwidthString(s *tape.RunSummary) string {
	if s.Timings.EffectiveBandwidthBytesPerSec <= 0 {
		return ""
	}
	out := "≈ " + formatGBs(s.Timings.EffectiveBandwidthBytesPerSec)
	var peak int64
	for _, g := range s.Host.GPUs {
		peak += g.PeakBandwidthBytesPerSec
	}
	if peak > 0 {
		ratio := float64(s.Timings.EffectiveBandwidthBytesPerSec) / float64(peak)
		out += ", " + formatPct(ratio) + " of peak"
	}
	return out
}

func cacheString(c tape.CacheSummary) string {
	label := string(c.Label)
	if label == "" {
		label = unknown
	}
	if c.PromptTotal <= 0 {
		return unknown + " · " + label
	}
	ratio := c.HitRatio
	if ratio == 0 && c.HitTokens > 0 {
		ratio = float64(c.HitTokens) / float64(c.PromptTotal)
	}
	return fmt.Sprintf("%s hit (%d/%d) · %s", formatPct(ratio), c.HitTokens, c.PromptTotal, label)
}

// streamLines renders the concurrent-run headline. It is two lines because the
// contract's single line is ~95 columns and none of its figures may be dropped.
func streamLines(s *tape.RunSummary) []string {
	a := s.Aggregate
	n := a.Streams
	if n == 0 {
		n = s.Concurrency
	}
	first := []string{fmt.Sprintf("%d × %s = %s aggregate",
		n, formatRateUnit(a.PerStreamPredictedPerSecond), formatRateUnit(a.AggregatePredictedPerSecond))}
	if a.StreamsFailed > 0 {
		first[0] += fmt.Sprintf(" · %d failed", a.StreamsFailed)
	}
	// A multi-round run is not an equation (lead, 2026-09-13): its aggregate
	// is weighted by how long each round decoded, so "4 × 14.4 = 50.6" would
	// print false arithmetic. The three figures are listed instead, and
	// failures count against every stream the run sent.
	if s.Rounds > 1 {
		parts := []string{
			fmt.Sprintf("%d streams per round", n),
			formatRateUnit(a.PerStreamPredictedPerSecond) + " each",
			formatRateUnit(a.AggregatePredictedPerSecond) + " aggregate",
		}
		if a.StreamsFailed > 0 {
			parts = append(parts, fmt.Sprintf("%d of %d failed", a.StreamsFailed, n*s.Rounds))
		}
		first = wrapJoin(parts, " · ", innerWidth-speedLabelW)
	}
	// The slots line wraps rather than truncating: on a server with fewer
	// slots than streams the queue note is the part that explains the
	// per-stream rate above it, so it is never the part that gets cut.
	second := wrapJoin([]string{
		fmt.Sprintf("TTFT p50 %s p95 %s", formatMs(a.TTFTp50Ms), formatMs(a.TTFTp95Ms)),
		"slots busy max " + formatInt(a.SlotsBusyMax),
		queuedString(s),
	}, " · ", innerWidth-speedLabelW)
	return labelled("Streams", speedLabelW, append(first, second...))
}

func memorySection(s *tape.RunSummary) []string {
	var lines []string

	if len(s.GPUsAtEnd) == 0 {
		lines = append(lines, "VRAM "+unknown)
	} else {
		total := map[int]int64{}
		for _, g := range s.Host.GPUs {
			total[g.Index] = g.VRAMBytes
		}
		for _, g := range s.GPUsAtEnd {
			lines = append(lines, vramLine(g, total[g.Index]))
		}
	}

	if p := s.Placement; p.VRAMWeightsBytes > 0 || p.VRAMKVBytes > 0 || p.VRAMComputeBytes > 0 {
		lines = append(lines, fmt.Sprintf("weights %s | kv %s | compute %s GiB",
			formatGiBNum(p.VRAMWeightsBytes), formatGiBNum(p.VRAMKVBytes), formatGiBNum(p.VRAMComputeBytes)))
	}

	// Lesson 3: RSS is not "loaded". Both of these lines need a /proc view to
	// mean anything; without one a zero would be a lie, so print "?".
	m := s.Memory
	hasProc := m.AtEnd.RSSBytes > 0
	if hasProc {
		lines = append(lines, fmt.Sprintf("Host RSS %s (file %s / anon %s)",
			formatGiB(m.AtEnd.RSSBytes), formatGiBNum(m.AtEnd.RSSFileBytes), formatGiBNum(m.AtEnd.RSSAnonBytes)))
	} else {
		lines = append(lines, "Host RSS "+unknown)
	}

	if s.Placement.NeverLoadedBytes > 0 {
		line := "Never loaded " + formatGiB(s.Placement.NeverLoadedBytes)
		if what := neverLoadedWhat(s.Placement); what != "" {
			line += " (" + what + ")"
		}
		lines = append(lines, line)
	}

	if hasProc {
		lines = append(lines, fmt.Sprintf("Page faults %s maj/token (%d during decode)",
			formatFloat1(m.MajFaultsPerToken), m.MajFaultsDecode))
	} else {
		lines = append(lines, "Page faults "+unknown)
	}

	return labelled("MEMORY", blockLabelW, lines)
}

func vramLine(g tape.GPUSample, total int64) string {
	name := "GPU" + strconv.Itoa(g.Index)
	if total <= 0 {
		return fmt.Sprintf("%s %s/%s GiB", name, formatGiBNum(g.UsedBytes), unknown)
	}
	return fmt.Sprintf("%s %s %s/%s GiB", name, vramBar(g.UsedBytes, total, vramBarW),
		formatGiBNum(g.UsedBytes), formatGiBNum(total))
}

func vramBar(used, total int64, w int) string {
	filled := 0
	if used > 0 && total > 0 {
		filled = int(math.Round(float64(used) / float64(total) * float64(w)))
	}
	if filled < 0 {
		filled = 0
	}
	if filled > w {
		filled = w
	}
	return "[" + repeat('█', filled) + repeat('░', w-filled) + "]"
}

// neverLoadedWhat names the tensor class behind the never-loaded bytes when the
// placement identified one; it is not assumed.
func neverLoadedWhat(p tape.PlacementSummary) string {
	for _, d := range p.Devices {
		if d.Classes[tape.ClassNGram] > 0 {
			return "ngram tables"
		}
	}
	return ""
}

func hostSection(s *tape.RunSummary) []string {
	parts := make([]string, 0, len(s.GPUsAtEnd)+2)
	throttled := unknown
	for _, g := range s.GPUsAtEnd {
		parts = append(parts, fmt.Sprintf("GPU%d %s %s", g.Index, tempString(g.TempC), powerString(g.PowerW)))
	}
	if len(s.GPUsAtEnd) > 0 {
		throttled = "no"
		for _, g := range s.GPUsAtEnd {
			if g.Throttled {
				throttled = "yes"
				break
			}
		}
	}
	parts = append(parts, "throttled: "+throttled)
	// Lesson 6: contention is always printed — but "no" is a claim, and a run
	// that could read neither the host load nor a GPU has not earned it. The
	// PNG card applies the same rule (internal/card/png contendedPill).
	parts = append(parts, "contended: "+contendedString(s.Contention))

	lines := wrapJoin(parts, " · ", innerWidth-blockLabelW)
	if s.Contention.Contended && len(s.Contention.Reasons) > 0 {
		lines = append(lines, wrapJoin(s.Contention.Reasons, " · ", innerWidth-blockLabelW)...)
	}
	return labelled("HOST", blockLabelW, lines)
}

// contendedString is the contention verdict, or "?" when nothing in it came
// from a reading.
//
// A card that says "contended no" about a run in which nobody looked at the
// host is worse than one that admits it did not look: the whole point of the
// card is that a figure on it was observed.
func contendedString(ci tape.ContentionInfo) string {
	if !contentionObserved(ci) {
		return unknown
	}
	return yesNo(ci.Contended)
}

// contentionObserved reports whether anything in ci came from a reading. It is
// character-for-character the PNG card's rule, because the two renderings of
// one summary must never disagree about what is known.
func contentionObserved(ci tape.ContentionInfo) bool {
	// A witness (TTP-36) is a reading even when every figure in it was quiet.
	return ci.Contended || ci.LoadAvg1 > 0 || ci.OtherGPUProcs > 0 || len(ci.Reasons) > 0 || len(ci.Witnesses) > 0
}

func tempString(c float64) string {
	if c <= 0 {
		return unknown + "°C"
	}
	return strconv.FormatFloat(c, 'f', 0, 64) + "°C"
}

func powerString(w float64) string {
	if w <= 0 {
		return unknown + " W"
	}
	return strconv.FormatFloat(w, 'f', 0, 64) + " W"
}

// flagsSection renders the flag line.
//
// The five argument-starters (-fa, -b, -ub, -ctk, -ctv) are always printed and
// show "?" when unobserved — they are the ones that end comment threads
// (docs/research/02-sharing-artifacts.md §5.2). The remaining flags are
// omitted when empty. Everything except the -ot group wraps without
// truncation; the -ot group, which can be arbitrarily long, is truncated with
// "…" so it never costs more than one extra line.
func flagsSection(s *tape.RunSummary) []string {
	base, ot := flagTokens(s.Server)
	avail := innerWidth - blockLabelW
	lines := wrapJoin(base, " ", avail)
	if ot != "" {
		last := lines[len(lines)-1]
		room := avail - Width(last) - 1
		switch {
		case Width(ot) <= room: // fits after the last flag
			lines[len(lines)-1] = last + " " + ot
		case Width(ot) <= avail: // fits whole on a line of its own
			lines = append(lines, ot)
		case room >= 12: // has to be cut; the tail of this line is roomier
			lines[len(lines)-1] = last + " " + truncate(ot, room)
		default:
			lines = append(lines, truncate(ot, avail))
		}
	}
	return labelled("FLAGS", blockLabelW, lines)
}

// flagTokens returns the flags in card order plus the -ot group separately.
//
// It takes the whole ServerInfo and not just the flags because the five
// always-printed ones distinguish "not observed" from "observed as absent",
// and only the argv says which of the two happened (see flagValue).
func flagTokens(srv tape.ServerInfo) (base []string, ot string) {
	f := srv.Flags
	read := argvObserved(srv)
	// -ngl is not one of the five: it is omitted when unset rather than
	// defaulted, because a card that prints it implies a placement decision.
	if f.NGL != "" {
		base = append(base, "-ngl "+f.NGL)
	}
	base = append(base,
		"-fa "+flagValue(f.FlashAttn, read),
		"-b "+flagValue(f.Batch, read),
		"-ub "+flagValue(f.UBatch, read),
		"-ctk "+flagValue(f.CacheTypeK, read),
		"-ctv "+flagValue(f.CacheTypeV, read),
	)
	if f.LoadMode != "" {
		base = append(base, "--load-mode "+f.LoadMode)
	}
	if f.CPUMoE != "" {
		// The field holds either a bare count or the verbatim flag.
		if strings.HasPrefix(f.CPUMoE, "-") {
			base = append(base, f.CPUMoE)
		} else {
			base = append(base, "-ncmoe "+f.CPUMoE)
		}
	}
	if f.Threads != "" {
		base = append(base, "-t "+f.Threads)
	}
	// Speculative decoding (TTP-30). These used to reach the card inside
	// Other, because the parser did not name them; now that it does, they are
	// printed here rather than dropped — a flag that was on the command line
	// belongs on the FLAGS line. The Draft row in the speed section repeats
	// the model and the block size because it needs them beside the acceptance
	// rate, but --draft-min and --draft-p-min have no other home at all.
	if f.DraftModel != "" {
		base = append(base, "-md "+f.DraftModel)
	}
	if f.DraftMax != "" {
		base = append(base, "--draft-max "+f.DraftMax)
	}
	if f.DraftMin != "" {
		base = append(base, "--draft-min "+f.DraftMin)
	}
	if f.DraftPMin != "" {
		base = append(base, "--draft-p-min "+f.DraftPMin)
	}
	base = append(base, f.Other...)

	parts := make([]string, 0, len(f.OverrideTens))
	for _, p := range f.OverrideTens {
		if p == "" {
			continue
		}
		parts = append(parts, "-ot "+p)
	}
	return base, strings.Join(parts, " ")
}

// answerCutWarning is the caveat for a run that thought until it ran out of
// budget and never answered.
//
// It is derived rather than recorded, because it is a reading of two figures
// the tape already carries and not an observation of its own. Without it the
// card shows a healthy decode rate beside an empty completion, and the reader
// blames the tool instead of --n-predict (TTP-20, 2026-09-13). A run that
// thought and then answered is not cut and gets no warning: the thinking
// clause on the Context row already says how much of the budget went where.
func answerCutWarning(s *tape.RunSummary) string {
	t := s.Timings
	if t.PredictedN <= 0 || t.ReasoningN < t.PredictedN {
		return ""
	}
	return fmt.Sprintf("answer cut: all %s predicted tokens were reasoning — raise --n-predict",
		formatInt(t.PredictedN))
}

func warningSection(s *tape.RunSummary) []string {
	var out []string
	warnings := s.Warnings
	if w := answerCutWarning(s); w != "" {
		// First: it explains an empty answer, which is the thing a reader is
		// looking at the card to understand.
		warnings = append([]string{w}, warnings...)
	}
	for _, w := range warnings {
		if strings.TrimSpace(w) == "" {
			continue
		}
		for i, l := range wrapJoin(strings.Fields(w), " ", innerWidth-2) {
			if i == 0 {
				out = append(out, "! "+l)
			} else {
				out = append(out, "  "+l)
			}
		}
	}
	return out
}
