package card

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/bandwidth"
	"github.com/midagedev/toktape/internal/gpu"
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
	}
	// flagsSection is nil for a generic OpenAI-compatible server (TTP-99):
	// an empty section would still print its separator, so the block is
	// dropped from the list rather than rendered empty.
	if fs := flagsSection(s); len(fs) > 0 {
		sections = append(sections, fs)
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

// jsonCard is what `-o json` puts on stdout: the run summary, unchanged and in
// schema order, plus the card's derived caveats after it.
//
// The summary is embedded rather than nested, so every field a reader already
// parses stays at the top level with the same name and the same type and a
// tape.RunSummary still unmarshals from this document. The one new key is
// added at the end, where encoding/json puts the outer struct's own fields.
//
// It is "caveats" and not "warnings" because "warnings" is taken, by
// RunSummary.Warnings — the free text the recorder wrote — and the two are not
// the same list: the caveats are a superset, they carry codes, and a consumer
// that switched on an array of strings must not silently start receiving
// objects. That collision is worth removing at the schema end rather than
// here; see the report's note on renaming the recorder's field.
type jsonCard struct {
	tape.RunSummary
	// Caveats is every reason a figure above it might not mean what it looks
	// like, most serious first. Empty is the useful case: it is the card
	// saying, in the one field an agent has to read, that nothing about this
	// run disqualifies the number it came for.
	Caveats []Caveat `json:"caveats"`
}

// JSON renders the summary as indented JSON. Key order is the struct order of
// tape.RunSummary, which is the schema order, with the card's own derived
// caveats last; map keys are sorted by encoding/json, so the output is stable
// across runs.
func JSON(s *tape.RunSummary) ([]byte, error) {
	if s == nil {
		s = &tape.RunSummary{}
	}
	doc := jsonCard{RunSummary: *s, Caveats: Caveats(s)}
	if doc.Caveats == nil {
		// An empty array, never null: "this run has no caveats" is the answer
		// the field exists to give, and a consumer should not have to tell
		// null from [] to read it.
		doc.Caveats = []Caveat{}
	}
	b, err := json.MarshalIndent(doc, "", "  ")
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

// wrapJoinContinued is wrapJoin for a value whose parts are one bracketed
// group: a line that continues keeps the separator at its end, so the row
// says it is not finished (vision, 2026-09-19).
//
// wrapJoin drops the separator at the break, which is right for ENGINE,
// FLAGS and the Streams block — each of their continuation lines starts a
// complete clause, and a trailing mark on those would be noise. The Context
// row is the one value that is a parenthesised group, so its first line ends
// on an open bracket with nothing closing it and nothing saying more is
// coming: measured as reading like a broken line rather than a continued
// row, against the same row one character shorter, which fits and closes.
// Closing the bracket on the first line instead would move the endings
// clause outside the group, which is a different claim about what the
// parentheses hold.
//
// The mark is the separator's own non-space text, so the row cannot invent
// a punctuation the card does not use. A line with no room for it wraps
// unmarked rather than overflowing: the bracket is a reading aid and the
// column width is a contract.
func wrapJoinContinued(parts []string, sep string, avail int) []string {
	mark := strings.TrimRight(sep, " ")
	lines := wrapJoin(parts, sep, avail)
	for i := 0; i < len(lines)-1; i++ {
		if Width(lines[i])+Width(mark) <= avail {
			lines[i] += mark
		}
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
	// A generic OpenAI-compatible server names its protocol, not its engine
	// (TTP-99): the kind is what was observed, and the user's --engine text
	// is a claim printed with that word — never copied into Kind, Build or
	// Commit. Without a claim there is nothing to name after "openai".
	if srv.Kind == tape.ServerOpenAI {
		if claim := strings.TrimSpace(srv.EngineClaim); claim != "" {
			return "openai · claim: " + claim
		}
		return "openai · " + unknown
	}
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

	// Lesson 2: a short generation is a "sample", never a "decode" rate. The
	// predicate is isSample and nothing else, so the row label and the
	// short_generation caveat can never disagree (TTP-74).
	decodeLabel := "Decode"
	if isSample(s) {
		decodeLabel = "Sample"
	}
	out = append(out, field(decodeLabel, speedLabelW, " · ", decodeParts(s)...)...)

	promptTotal := PromptTokens(s)
	out = append(out, field("Prefill", speedLabelW, " · ", prefillParts(s, promptTotal)...)...)
	if line := probeLine(s); line != "" {
		out = append(out, labelled("", speedLabelW, []string{line})...)
	}

	if parts := draftParts(s); len(parts) > 0 {
		out = append(out, field("Draft", speedLabelW, " · ", parts...)...)
	}

	// The row wraps at its clause boundaries rather than truncating (lead,
	// 2026-09-19): the both spelling of the endings clause is 66 columns of
	// value against the 54 the gutter leaves, and a truncation there cuts the
	// second count — the sentence the clause exists to say. Wrapping is what
	// every other row that overflows already does (ENGINE, FLAGS, the Streams
	// block), and contextParts hands the units over so the clause moves whole
	// to the continuation line instead of breaking between its counts.
	out = append(out, labelled("Context", speedLabelW,
		wrapJoinContinued(contextParts(s.Server.CtxSize, promptTotal, t.PredictedN, reasoningTokens(s), s.Limit),
			" · ", innerWidth-speedLabelW))...)

	out = append(out, labelled("Prefix cache", speedLabelW, []string{cacheString(s.Cache)})...)

	// What the request asked for, which is worth a tenth of the rate above it
	// (TTP-55): greedy against server-default sampling against a thinking
	// model left to think are three different numbers for one engine. Absent
	// on a tape that did not record its endpoint, rather than guessed.
	if parts := samplingParts(s); len(parts) > 0 {
		out = append(out, field("Sampling", speedLabelW, " · ", parts...)...)
	}

	if s.Concurrency > 1 {
		out = append(out, streamLines(s)...)
	}
	out = append(out, roundsLines(s)...)
	out = append(out, sweepLines(s)...)
	return out
}

// decodeParts is the fields of the Decode row, and it leads with the same kind
// of figure the Prefill row leads with (2026-09-14). TTP-59 made Prefill lead
// with the box's aggregate under concurrency; leaving Decode on the per-stream
// mean would have left one card whose two rate rows mean different things by
// the same word — the ambiguity the user named as "개별 tps때문에 병렬세션에서
// 좀 애매하게 느껴진다". The right pane and the result modal already lead with
// the aggregate on both rates.
//
// The per-stream mean is kept beside it, and the Streams block below still
// carries the full arithmetic; nothing is dropped, only reordered.
func decodeParts(s *tape.RunSummary) []string {
	t := s.Timings
	var parts []string
	// An uncounted run prints "?" in place of the rate (TTP-99): the chunk
	// count beside it is not a token count, so no figure may stand where a
	// rate does. Bare "?" rather than "? tok/s" — the unit belongs to a
	// number that was measured.
	if t.PredictedNSource == "chunks" {
		parts = append(parts, unknown)
	} else if s.Concurrency > 1 && s.Aggregate.ConcurrentPredictedPerSecond > 0 {
		// TTP-138 (2026-09-19): when the tape carries the window in which
		// every stream decoded at once, that window's figure is the one this
		// row leads with — the place the whole-wall aggregate occupies and
		// under the same word. The whole-wall figure averages a ragged tail
		// into the rate: it stops being one concurrency the moment a stream
		// finishes, and the same box capped so the streams end together
		// reports the window's number.
		//
		// Both figures come from that window (lead, 2026-09-19). The
		// per-stream mean is NOT the same number whichever wall it is read
		// against: a stream that outlives the others gets more of the
		// machine, so its own mean sits above the rate it held while they
		// were running. Printing the window aggregate beside it would put
		// two spans in one row and hand the reader a product the card never
		// printed — which is the arithmetic `ragged_aggregate` disputes,
		// reappearing inside the row meant to settle it. With both from the
		// window, N x each == aggregate by construction. The whole-wall
		// figure the lead no longer prints is carried by that caveat.
		//
		// Zero here (one stream, a tape older than the field, streams that
		// never overlapped) takes the branch below, today's row exactly.
		parts = append(parts,
			formatRateUnit(s.Aggregate.ConcurrentPredictedPerSecond)+" aggregate",
			formatRateUnit(s.Aggregate.ConcurrentPerStreamPredictedPerSecond)+" each")
	} else if s.Concurrency > 1 && s.Aggregate.AggregatePredictedPerSecond > 0 {
		parts = append(parts,
			formatRateUnit(s.Aggregate.AggregatePredictedPerSecond)+" aggregate",
			formatRateUnit(t.PredictedPerSecond)+" each")
	} else {
		parts = append(parts, formatRateUnit(t.PredictedPerSecond))
	}
	// A client-timed run names its clock beside the headline (TTP-99): after
	// the rate, before the bandwidth, as its own part so it survives wrapping
	// beside the figure it qualifies.
	if t.Source == "client" {
		parts = append(parts, "client-timed")
	}
	if bw := bandwidthString(s); bw != "" {
		parts = append(parts, bw)
	}
	return parts
}

// prefillParts is the fields of the Prefill row.
//
// On a run of several streams the row leads with the box's aggregate rather
// than the per-stream mean (TTP-59, 2026-09-14). tape.TimingsSummary is the
// mean over the requests once Concurrency > 1 (internal/recorder/reduce.go),
// and the first figure on the row is what a reader takes for "what this box
// does": the code tape's card said 13.0 tok/s while the server was putting
// 24.2 through. The PNG card has led with the aggregate since TTP-28; this is
// the text card saying the same thing, with the per-stream mean kept beside it
// as the "each" figure, the way the right pane's decode rows are shaped.
//
// TTFT becomes the median over the streams there, which is the figure the
// Streams block below already prints — the single run-level TTFT of a
// concurrent run is a mean of times that started together and is not a
// reader's "how long until something appeared".
//
// The prompt-token count is the per-request total either way, unchanged: it
// says how long the prompt was, not how many the run sent.
// TTP-65 (2026-09-14) put two more things on the row, both of them the same
// defect: a prefill figure printed without the condition that decides what it
// measures.
//
// The four-stream ws tape printed "Prefill 5.6 tok/s · TTFT 11942 ms · 63
// prompt tokens". Four 63-token requests arrived at once, so the TTFT on that
// row is the time the request spent waiting for a slot PLUS the time the
// engine spent on the prompt, and the rate beside it is neither of those
// things. The honest prefill on that box is 60 to 130 tok/s.
//
// So on a concurrent run TTFT is split. The queue wait is the part of it the
// engine was not working, and the engine's own prefill time is prompt_ms out
// of the server's timings object — never send-to-first-token, which is what
// made 5.6 look like a prefill rate. Both are per-stream means over the same
// streams (internal/recorder/reduce.go), so the subtraction is between two
// figures of the same kind; the p50 stays beside them as its own clause rather
// than as the left side of an equation, because a median and a mean do not
// subtract.
//
// And a prompt under MinPrefillPromptTokens gets said so outright, next to the
// rate it disqualifies. The rate is still printed — it is what the server
// reported and dropping an observation is not this card's habit — but a reader
// who quotes it has been told, and an agent gets the same sentence under
// short_prompt_for_prefill in -o json.
func prefillParts(s *tape.RunSummary, promptTotal int) []string {
	t := s.Timings
	if s.Concurrency <= 1 {
		return dropEmpty(
			formatRateUnit(t.PromptPerSecond),
			promptTokensPart(s),
			"TTFT "+formatMs(t.TTFTMs),
		)
	}
	a := s.Aggregate
	return dropEmpty(
		formatRateUnit(a.AggregatePromptPerSecond)+" aggregate",
		formatRateUnit(t.PromptPerSecond)+" each",
		promptTokensPart(s),
		enginePrefillPart(s),
		queueWaitPart(s),
	)
}

// probeLine is the probe pass's own measurement, as an additional line under
// the Prefill row (TTP-137, 2026-09-19): the machine's prefill rate at the
// margin, on one stream, and the server's fixed cost per request — the two
// figures the two-point fit separates and no single rate on the row can
// show. The row's own figures stay untouched: they say what this run's
// prompts did at this run's concurrency, and this line says what the machine
// itself does, which is the number that does not move with the stream count
// — the same run measured 1182 tok/s on one stream and 69.3 each at four,
// and only the first of those is a fact about the box.
//
// Present only when the fit produced figures. A refused fit (both 0, the
// points kept) and no probe at all are each "not observed", and print
// nothing — never a "?" for a figure this row never asked about.
func probeLine(s *tape.RunSummary) string {
	p := s.Probe
	if p == nil || p.PrefillPerSecond <= 0 {
		return ""
	}
	// Led by "probe" because on a single-stream run the row above is also
	// one stream (vision, 2026-09-19), so "on one stream" separates nothing
	// and the row becomes two prefill rates a factor apart with no clause
	// between them — the defect this card exists to prevent. What differs is
	// where each figure came from: the row's is this run's prompts with the
	// per-request fixed cost inside it, the probe's is the machine's rate at
	// the margin with that cost named beside it.
	return fmt.Sprintf("probe %s on one stream · %s fixed",
		formatRateUnit(p.PrefillPerSecond), formatProbeMs(p.FixedMs))
}

// enginePrefillPart is the engine's own prompt-evaluation time: the server's
// prompt_ms, mean over the streams. "" when the server reported none.
func enginePrefillPart(s *tape.RunSummary) string {
	if s.Timings.PromptMs <= 0 {
		return ""
	}
	return "engine prefill " + formatMs(s.Timings.PromptMs)
}

// queueWaitPart is what is left of TTFT once the engine's prefill is taken
// out: how long the request sat before the engine started on it.
//
// "" unless both figures were observed and the subtraction is positive. A
// negative difference means the client's stopwatch and the server's disagree
// about the same window, and the repo's rule is to report nothing rather than
// clamp it to zero and call that a queue wait — the card would then print
// "queue 0 ms" for a run it had in fact failed to decompose.
func queueWaitPart(s *tape.RunSummary) string {
	t := s.Timings
	if t.TTFTMs <= 0 || t.PromptMs <= 0 {
		return ""
	}
	wait := t.TTFTMs - t.PromptMs
	if wait < 0 {
		return ""
	}
	// Zero is a reading here, not an absence: it says nothing queued.
	return "queue " + strconv.FormatFloat(wait, 'f', 0, 64) + " ms"
}

// dropEmpty is wrapJoin's rule applied before the parts are handed over, for
// rows built from clauses that are present only on some runs.
func dropEmpty(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
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
//
//	prefill 4k 129 · 16k 104 · 32k 71.0 tok/s
//	cache 4 repo-again 97% hit of 16k, 516 evaluated
//
// The prefill and cache lines (TTP-64, TTP-66, 2026-09-14) are what a prompts
// file of 4k, 16k and 32k prompts, with one of them sent twice, is run to
// measure. Each is printed only when some listed round carries its figures, so
// a tape recorded before the recorder kept them renders byte for byte as it
// did. They are lines of their own rather than clauses of each entry because
// the row has 54 columns: an entry that also said "pp 104 tok/s · 15875
// cached" no longer fits two to a line, and eight rounds would become eight
// lines. One line per measurement keeps the list at four lines, puts the
// prefill rates a reader compares side by side, and says the unit once.
//
// A round goes on one of the two lines, never both, and readRoundPrompt
// decides which. A prompt mostly served from the prefix cache, by the reading
// that labels a whole run "cached" (tape.CachedHitRatio), has a rate over the
// few hundred tokens the server re-evaluated at the end of a long context. That
// is not the prefill of a prompt that long, and next to the cold rounds it
// would read as one, so the cache line gives such a round its hit and its
// evaluated count and no rate. Every other round with prompt timings is a
// prefill entry labelled by its own prompt length, cached prefix included — a
// template's first tokens coming from the cache do not make a 16k prompt
// anything but a 16k prefill, and the run's Prefill row counts its prompt the
// same way. The prefill line names a round by length and not by its name,
// although the reader chose the name: length is the axis those rates are
// compared along, the list above already pairs each name with its number, and
// "3 monorepo 32k 71.0" would put the line back at one round a line. The cache
// line keeps the name, because which prompt came back is its whole point.
// A round too short to be a prefill measurement is not given a
// figure; the line counts it, so a missing round is explained rather than
// silently dropped. A round with no prompt timings is on neither line.
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

	var parts, prefill, cached []string
	short := 0
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
		switch r := readRoundPrompt(p); {
		case r.cached:
			cached = append(cached, roundCachePart(p, pos[p.SpecNMax], r))
		case r.rate <= 0:
		case r.short:
			short++
		default:
			prefill = append(prefill, promptLength(r.prompt)+" "+formatRate(r.rate))
		}
	}
	if len(parts) > 0 {
		lines = append(lines, wrapJoin(parts, "  ·  ", avail)...)
	}
	lines = append(lines, roundPrefillLines(prefill, short, avail)...)
	if len(cached) > 0 {
		cached[0] = "cache " + cached[0]
		lines = append(lines, wrapJoin(cached, "  ·  ", avail)...)
	}
	return labelled("Prompts", speedLabelW, lines)
}

// roundPrompt is what the Prompts row may say about one round's prompt,
// decided once by readRoundPrompt so the row and `card --explain` read the same
// verdict rather than two derivations of it.
//
// The counts are per stream. A round's PromptN and CacheN are summed over its
// streams, and every stream of a round sends the same prompt, so the sum over
// four streams is four prompts' worth: labelling it, or holding it against the
// floor, would call four 63-token requests one 252-token prefill.
type roundPrompt struct {
	prompt    int     // the whole prompt, cached prefix included
	evaluated int     // the tokens the server evaluated
	cachedN   int     // the tokens it took from the prefix cache
	hitRatio  float64 // cachedN / prompt; 0 when no prompt was counted
	rate      float64 // the server's own prompt tok/s; 0 = no prompt timings
	// short: evaluated is under tape.MinPrefillPromptTokens, the same test the
	// run's Prefill row makes (shortPromptCount), so the rate is not a prefill
	// measurement.
	short bool
	// cached: the prompt was mostly served from the prefix cache, by the
	// ratio that labels a whole run "cached".
	cached bool
}

func readRoundPrompt(p tape.RoundSummary) roundPrompt {
	streams := p.Streams
	if streams < 1 {
		streams = 1
	}
	perStream := func(n int) int {
		if n <= 0 {
			return 0
		}
		// Rounded, and never rounded down to 0, which is unknown: a count the
		// server reported is at least one token.
		return max(1, (n+streams/2)/streams)
	}
	r := roundPrompt{
		prompt:    perStream(p.PromptN + p.CacheN),
		evaluated: perStream(p.PromptN),
		cachedN:   perStream(p.CacheN),
		rate:      p.PromptPerSecond,
	}
	if total := p.PromptN + p.CacheN; total > 0 {
		r.hitRatio = float64(p.CacheN) / float64(total)
	}
	r.short = shortPromptCount(r.evaluated)
	// The run-level cached_prefill caveat asks the same question of the
	// whole run (cacheHitCached, TTP-88): one predicate, so a round and a
	// run cannot disagree about where "cached" starts.
	r.cached = cacheHitCached(p.CacheN, p.PromptN+p.CacheN)
	return r
}

// roundsCarryPrompt reports whether any round recorded a prompt figure: the
// condition for the Prompts row's prefill and cache lines to exist at all.
func roundsCarryPrompt(per []tape.RoundSummary) bool {
	for _, p := range per {
		if p.PromptN > 0 || p.CacheN > 0 || p.PromptPerSecond > 0 {
			return true
		}
	}
	return false
}

// roundPrefillLines is the Prompts row's prefill line: "prefill 4k 129 · 16k
// 104 tok/s", then how many rounds were too short to be given a figure. nil
// when there is neither.
//
// The count carries no rate on purpose. TTP-65 is a rate from a 63-token
// prompt that reached a reader as a prefill figure; a clause beside it would be
// one wrapJoin break away from the figure it qualifies, which is why the run's
// Prefill row joins its qualification to the count and why this line does not
// print the rate at all.
func roundPrefillLines(prefill []string, short, avail int) []string {
	if len(prefill) == 0 && short == 0 {
		return nil
	}
	parts := append([]string(nil), prefill...)
	if len(parts) > 0 {
		parts[len(parts)-1] += " tok/s"
	}
	if short > 0 {
		noun := "prompts"
		if short == 1 {
			noun = "prompt"
		}
		parts = append(parts, fmt.Sprintf("%d %s under %d tokens, not measured",
			short, noun, MinPrefillPromptTokens))
	}
	parts[0] = "prefill " + parts[0]
	return wrapJoin(parts, " · ", avail)
}

// roundCachePart is one round of the cache line: "4 repo-again 97% hit of 16k,
// 516 evaluated". The hit is the Prefix cache row's own "hit" percentage; the
// evaluated count is left out when the server reported none, rather than
// printed as "?".
func roundCachePart(p tape.RoundSummary, pos int, r roundPrompt) string {
	part := fmt.Sprintf("%s %s hit of %s", roundLabel(p, pos), formatPct(r.hitRatio), promptLength(r.prompt))
	if r.evaluated > 0 {
		part += ", " + formatInt(r.evaluated) + " evaluated"
	}
	return part
}

// promptLength is a prompt's token count the way a reader sizes one: 16391 is
// "16k". k is 1024, as it is in "a 16k context" for -c 16384, because the
// prompts a prefill sweep sends are cut to those sizes; one decimal under 10k
// ("4.5k") and none above, with a trailing ".0" dropped. Under 1024 the count
// is printed whole. It is derived from the round's own count and nothing else,
// so a label never names a size the server did not see.
func promptLength(n int) string {
	switch {
	case n <= 0:
		return unknown
	case n < 1024:
		return strconv.Itoa(n)
	}
	k := float64(n) / 1024
	if k < 10 {
		return strings.TrimSuffix(strconv.FormatFloat(k, 'f', 1, 64), ".0") + "k"
	}
	return strconv.FormatFloat(k, 'f', 0, 64) + "k"
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
	fields := []string{roundLabel(p, pos), formatRateUnit(p.PerStreamPredictedPerSecond)}
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

// roundLabel is how the Prompts row names a round — "1 sql-1", "3·sql-1",
// "3·2" — shared by the list, the cache line and `card --explain` so a round is
// called the same thing everywhere a reader looks for it.
func roundLabel(p tape.RoundSummary, pos int) string {
	switch {
	case p.SpecNMax > 0 && p.Name != "":
		return strconv.Itoa(p.SpecNMax) + "·" + p.Name
	case p.SpecNMax > 0:
		return strconv.Itoa(p.SpecNMax) + "·" + strconv.Itoa(pos)
	case p.Name != "":
		return strconv.Itoa(p.Index+1) + " " + p.Name
	default:
		return strconv.Itoa(p.Index + 1)
	}
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
	parts = append(parts, fmt.Sprintf("%s accepted (%d/%d)",
		formatPct(float64(accepted)/float64(*t.DraftN)), accepted, *t.DraftN))
	// The verify-step count and the batch (TTP-67). They live on this row and
	// not on the Decode row because they are properties of the draft, and
	// because the Decode row's bandwidth clause is already the figure they
	// explain: "≈ 108 GB/s from RAM per verify step" above, "156 verify steps
	// · 3.9 tokens a step" here.
	return append(parts, verifyParts(s)...)
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

// EndingsClause is the words, not just the counts: what a renderer says
// about how the answered streams ended — "4 of 4 hit the cap", "4 of 4 ran
// out of context", or both joined. "" when the tape cannot say (no endings
// observed) and when neither limit ended a stream.
//
// It lives here for the reason RaggedClause does (2026-09-19): the two
// renderers authored the same sentence twice and drifted. The counts were
// already shared — they come straight off tape.LimitSummary — but each
// renderer spelled them itself, which is exactly the shape that made the
// ragged clause look safe while the two cards said opposite things about one
// run. The PNG asks for EndingsClauseShort instead of respelling these
// figures; the two voices differ, the counts and the case selection cannot.
//
// The denominator is EndingsObserved in every spelling, and the two counts
// are disjoint (a stream that ran out of context did not reach the cap), so
// the "both" case names each against the same denominator rather than
// nesting one inside the other: the denominator rides the first count and
// the second stands alone — "3 of 4 hit the cap · 1 ran out of context" —
// because "1 of 4" beside "3 of 4" says the same 4 twice and the counts
// already sum to it. A lone exhausted count keeps its own denominator, or
// "4 ran out of context" would be a count with no whole to be a part of.
// Nothing is added when the tape cannot say, and no count is invented.
func EndingsClause(l tape.LimitSummary) string {
	if l.EndingsObserved <= 0 || (l.CappedStreams <= 0 && l.ContextExhaustedStreams <= 0) {
		return ""
	}
	var parts []string
	if l.CappedStreams > 0 {
		parts = append(parts, fmt.Sprintf("%s of %s hit the cap",
			formatInt(l.CappedStreams), formatInt(l.EndingsObserved)))
	}
	if l.ContextExhaustedStreams > 0 {
		if l.CappedStreams > 0 {
			parts = append(parts, fmt.Sprintf("%s ran out of context",
				formatInt(l.ContextExhaustedStreams)))
		} else {
			parts = append(parts, fmt.Sprintf("%s of %s ran out of context",
				formatInt(l.ContextExhaustedStreams), formatInt(l.EndingsObserved)))
		}
	}
	return strings.Join(parts, " · ")
}

// EndingsClauseShort is the same counts in the pill row's voice — "cap 4 of
// 4", "ctx full 4 of 4", "cap 3 of 4 · ctx 1" — for the renderer whose line
// will not hold the sentence. It lives beside the long form so the two
// cannot drift: that drift is what this pair was extracted to stop.
//
// The same denominator rule as the long form: "ctx 1" alone beside "cap 3 of
// 4" reads against the 4 that is already on the row, but a lone exhausted
// count must carry its own — hence "ctx full", the pill's "of", in the
// context-only spelling. Like the long form it is "" when the tape cannot
// say, so the ordinary card keeps its pill count.
func EndingsClauseShort(l tape.LimitSummary) string {
	if l.EndingsObserved <= 0 || (l.CappedStreams <= 0 && l.ContextExhaustedStreams <= 0) {
		return ""
	}
	var parts []string
	if l.CappedStreams > 0 {
		parts = append(parts, fmt.Sprintf("cap %s of %s",
			formatInt(l.CappedStreams), formatInt(l.EndingsObserved)))
	}
	if l.ContextExhaustedStreams > 0 {
		if l.CappedStreams > 0 {
			parts = append(parts, fmt.Sprintf("ctx %s", formatInt(l.ContextExhaustedStreams)))
		} else {
			parts = append(parts, fmt.Sprintf("ctx full %s of %s",
				formatInt(l.ContextExhaustedStreams), formatInt(l.EndingsObserved)))
		}
	}
	return strings.Join(parts, " · ")
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
//
//	TTP-135 (2026-09-19) added the cap clause: when the token cap ended
//	streams, the parens name how many against the endings observed — "4 of 4
//	hit the cap" — because a bare "1024 out" is exactly what a run that wrote
//
// 1024 tokens and stopped looks like, and a reader asking "was the whole run
// guillotined or one long-winded stream" needs the denominator. The clause
// grew its second and third spellings the same day the tape learned to tell
// the cap from the context running out (EndingsClause): "limit" with
// `truncated` set is a different problem with a different fix, and the row
// says which it was. Endings observed == 0 means the engine never said why
// streams stopped: nothing is added and nothing is guessed, and the row
// reads exactly as it did.
func contextString(ctxSize, in, out, thinking int, l tape.LimitSummary) string {
	return strings.Join(contextParts(ctxSize, in, out, thinking, l), " · ")
}

// contextParts is contextString's value as its wrap units (lead, 2026-09-19):
// the counts, the thinking clause, and the endings clause — each whole, never
// split by a line break, because the endings clause is one decided sentence
// and a break between its counts would print "3 of 4 hit the cap" and "1 ran
// out of context" as two findings. The opening paren rides the first part and
// the closing one the last, so a wrapped row closes where the value ends.
func contextParts(ctxSize, in, out, thinking int, l tape.LimitSummary) []string {
	parts := []string{fmt.Sprintf("%s in / %s out", formatInt(in), formatInt(out))}
	if thinking > 0 {
		parts = append(parts, formatInt(thinking)+" thinking")
	}
	if clause := EndingsClause(l); clause != "" {
		parts = append(parts, clause)
	}
	parts[0] = formatInt(ctxSize) + " (" + parts[0]
	parts[len(parts)-1] += ")"
	return parts
}

// reasoningTokens is how many of the run's generated tokens were a thinking
// model's reasoning_content. 0 for every model that does not think, and for a
// thinking model that answered without thinking.
func reasoningTokens(s *tape.RunSummary) int { return s.Timings.ReasoningN }

// PromptTokens is the whole prompt the run sent, cached prefix included.
func PromptTokens(s *tape.RunSummary) int {
	if s.Cache.PromptTotal > 0 {
		return s.Cache.PromptTotal
	}
	return s.Timings.PromptN + s.Timings.CacheN
}

// bandwidthString renders "≈ 91 GB/s, 9% of peak", or "≈ 112–206 GB/s,
// 15–27% of peak" above one stream on a sparse MoE, where concurrent streams
// share a forward pass and only their routed experts' overlap decides where
// in the range the traffic sat (lead, 2026-09-16). Empty when the effective
// bandwidth was not derivable; the "≈" marks it as an estimate (spec §3.2 S6).
//
// "of peak" is measured against the ceiling this run's placement allows, not
// against the sum of the GPUs' bandwidths (TTP-34, 2026-09-13): a layer split
// reads its devices one after another, and a partially offloaded model reads
// most of a token off the host bus. internal/bandwidth owns that arithmetic
// and omits the clause entirely when it is not derivable.
func bandwidthString(s *tape.RunSummary) string {
	// With a draft model the hardware never read weights once per token, so
	// bytes per accepted token is not a rate anything experienced (TTP-67,
	// 2026-09-14). The verify-step figure replaces it rather than joining it:
	// two RAM bandwidths on one card, differing by the acceptance rate and
	// with nothing on the card to say why, is a worse answer than one figure
	// that is true. The per-accepted-token figure stays in -o json as
	// timings.effective_bw_bps, where it is labelled by its own field name.
	if clause, ok := verifyRAM(s); ok {
		return clause
	}
	// On a placement split between host RAM and VRAM, one figure over all of
	// them is not a bandwidth against any ceiling that exists (TTP-56,
	// 2026-09-14). The ws run printed "≈ 155 GB/s" for a box whose host bus
	// tops out at 115.8 GB/s; the 155 is three buses' traffic added together
	// and divided by one second, and the host side of it — the side that was
	// actually the wall — was 65.5. So a mixed placement names the bus, and
	// the reader can put the figure next to a STREAM number themselves.
	if r, ok := bandwidth.RAM(s); ok {
		out := "≈ " + formatGBs(r.BytesPerSec) + " from RAM"
		// The percentage only when the CPU's share is provably the whole of
		// what it reads. A model whose router or shared expert might sit in
		// host RAM gives a figure that can only be low, and a ratio computed
		// from it would be a claim the tape cannot support (RAMSide.Exact).
		if r.Exact && r.OfPeak > 0 {
			out += ", " + formatPct(r.OfPeak) + " of peak"
		}
		return out
	}
	low, high, ok := bandwidth.CombinedRange(s)
	if !ok {
		return ""
	}
	out := "≈ " + formatGBsRange(low, high)
	if rlow, rhigh, ok := bandwidth.OfPeakRange(s); ok {
		out += ", " + formatPctRange(rlow, rhigh) + " of peak"
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

// streamsMultiply reports whether "n × per-stream" really is the aggregate,
// within the tolerance the card's own rounding needs: the figures are printed
// to one decimal, so a tenth either way is the printing and not the run. Above
// that the streams did not share a window — one finished while the others kept
// decoding — and the row says the three figures instead of an equation.
func streamsMultiply(a tape.AggregateTimings, n int) bool {
	product := float64(n) * a.PerStreamPredictedPerSecond
	if product <= 0 || a.AggregatePredictedPerSecond <= 0 {
		return true // nothing to contradict: the row prints what it has
	}
	return math.Abs(a.AggregatePredictedPerSecond-product)/product <= streamsIdentityTolerance
}

// streamsIdentityTolerance is how far the aggregate may sit from n × per-stream
// before the row stops multiplying. 2 % is tape.RateTolerance's reasoning one
// level up: the same slack the card allows between two clocks measuring one
// rate, allowed between two figures describing one run.
const streamsIdentityTolerance = 0.02

// streamLines renders the concurrent-run headline: what is true of the streams
// and nothing that is true of the rates.
//
// It used to open with "N × per-stream = aggregate", or — when that equation
// would have been false — with the same three figures listed. Both forms
// printed figures the Decode row above already carries, and on the hero card
// the whole first line said nothing the reader had not read four lines
// earlier: "144 tok/s aggregate · 42.1 tok/s each" up there and
// "4 streams · 42.1 tok/s each · 144 tok/s aggregate" down here, off the same
// two fields (TTP-110, user 2026-09-17: "중복정보 정리하자"). The rates are the
// Decode row's; a reader who wants the product can take it from there.
//
// What is left is what only this block knows: how many streams there were, how
// many failed, whether they actually overlapped, when each of them saw its
// first token, and how many slots the server had to give them.
//
// The overlap clause is what the equation was really for. streamsMultiply
// exists because on a ragged run — streams stopping at different times — the
// aggregate is the run's tokens over a window whose tail holds one stream, so
// N × per-stream is not it (the ExLlamaV3 two-stream take read 2 × 11.6
// against a 19.4 aggregate). Printing the figures and letting the arithmetic
// silently not work was never the reader's answer; the sentence is.
// TTP-108 (2026-09-19) is the fuller version of this clause, on the caveat
// lines as ragged_aggregate with the arithmetic named: RaggedAggregate asks
// the question for both.
func streamLines(s *tape.RunSummary) []string {
	a := s.Aggregate
	n := a.Streams
	if n == 0 {
		n = s.Concurrency
	}
	sent, label := n, fmt.Sprintf("%d streams", n)
	if s.Rounds > 1 {
		sent, label = n*s.Rounds, fmt.Sprintf("%d streams per round", n)
	}
	parts := []string{label}
	if a.StreamsFailed > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d failed", a.StreamsFailed, sent))
	}
	// The clause is RaggedAggregate's question, asked here and nowhere
	// re-derived (TTP-108, 2026-09-19): the same predicate raises
	// ragged_aggregate on the caveat lines, with the arithmetic named. A
	// multi-round run is not one window either (lead, 2026-09-13): its
	// aggregate is weighted by how long each round decoded. "per round" above
	// already says the streams were not all in one window, so the clause would
	// be saying it twice — and the predicate says so too.
	if clause := RaggedClause(s); clause != "" {
		parts = append(parts, clause)
	}
	// TTFT is here rather than on the Prefill row for a run of several streams
	// (TTP-110): what that row printed was a.TTFTp50Ms, a statistic over the
	// streams wearing a prefill label, and the same figure opened this block's
	// second line. It belongs beside its own p95. A single-stream run has no
	// Streams block, so the Prefill row still carries its TTFT.
	//
	// The pair is TTFTPercentiles' to give (TTP-97, 2026-09-19): percentiles
	// computed from two samples frequently render as the same string, which
	// reads as a bug, so alike renderings print the single figure instead.
	//
	// The line wraps rather than truncating: on a server with fewer slots than
	// streams the queue note is the part that explains the per-stream rate
	// above it, so it is never the part that gets cut.
	p50, p95, pair := TTFTPercentiles(s, formatMs)
	ttft := "TTFT " + p50
	if pair {
		ttft = fmt.Sprintf("TTFT p50 %s p95 %s", p50, p95)
	}
	parts = append(parts,
		ttft,
		"slots busy max "+formatInt(a.SlotsBusyMax),
		queuedString(s))
	return labelled("Streams", speedLabelW, wrapJoin(dropEmpty(parts...), " · ", innerWidth-speedLabelW))
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

	m := s.Memory
	hasProc := m.AtEnd.RSSBytes > 0

	// What is on the CPU, and how much of it is actually in RAM (TTP-63, user
	// 2026-09-14: "cpu 388g 찍혀있는데 이게 ram이랑 nvme랑 구분이 안되나?").
	// A CPU placement is llama.cpp's backend assignment, not a residency: on
	// the ws rig 388.1 GiB is placed on a 252 GB box, and the difference is
	// read from the model file on every touch — which is what the Page faults
	// line below counts. tape.Residency derives the split; it is never
	// re-derived here, so this line, the pane, the modal and the PNG print the
	// same figures.
	//
	// The word is "disk", not "NVMe": what was observed is that the pages are
	// not resident and come back from the file. The medium behind that file is
	// not in the tape, and naming it would be printing something nobody
	// measured.
	if r := tape.Residency(s.Placement, m.AtEnd); r.Placed > 0 {
		switch {
		case !r.Ok:
			lines = append(lines, "Host placed "+formatGiB(r.Placed))
		case r.Paged == 0:
			lines = append(lines, "Host placed "+formatGiB(r.Placed)+" (all in RAM)")
		default:
			lines = append(lines, fmt.Sprintf("Host placed %s (%s in RAM / %s on disk)",
				formatGiB(r.Placed), formatGiBNum(r.Resident), formatGiBNum(r.Paged)))
		}
	}

	// Lesson 3: RSS is not "loaded". Both of these lines need a /proc view to
	// mean anything; without one a zero would be a lie, so print "?".
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
		parts = append(parts, fmt.Sprintf("GPU%d %s %s", g.Index, tempString(g.TempC), powerString(g)))
	}
	if len(s.GPUsAtEnd) > 0 {
		throttled = "no"
		for _, g := range s.GPUsAtEnd {
			if GPUThrottled(g) {
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
	// The machine changed under the run, which is not contention: a perfectly
	// quiet box throttles too (TTP-57). Nothing when it did not change, and
	// nothing on a tape whose witnesses carry no operating point.
	lines = append(lines, conditionsLines(s)...)
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

// powerString is one GPU's power figure: the draw against the limit when both
// were read, the draw alone on a tape recorded before the limit was (2026-09-15).
// "281 of 300 W" is the figure the card owes a reader who is about to argue
// about a decode rate: it says the card was boosting into its own cap and not
// above it, which the bare draw cannot.
func powerString(g tape.GPUSample) string {
	draw := unknown
	if g.PowerW > 0 {
		draw = strconv.FormatFloat(g.PowerW, 'f', 0, 64)
	}
	if g.PowerLimitW <= 0 {
		return draw + " W"
	}
	return draw + " of " + strconv.FormatFloat(g.PowerLimitW, 'f', 0, 64) + " W"
}

// GPUThrottled is the card's throttle verdict for one GPU sample: whether the
// card was held below the hardware's own operating point. It is the text card's
// and the PNG's single verdict, so the two cannot disagree.
//
// The mask decides when the tape carries one, and it is deliberately narrower
// than the wide Throttled the tape and `-o json` keep: a card boosting into
// its own power cap sets sw power cap on every such run, which made
// "throttled: yes" true of every run and therefore of none.
// gpu.ThrottleHeldBelowSettingsMask is the bits that say the hardware stepped
// in. A tape with no mask — recorded before 2026-09-15 — keeps the stored
// g.Throttled verdict: re-deriving it would print "no" beside a "yes" the run
// recorded, and the wide verdict is the only one that tape has.
func GPUThrottled(g tape.GPUSample) bool {
	if g.ThrottleMask != 0 {
		return g.ThrottleMask&gpu.ThrottleHeldBelowSettingsMask != 0
	}
	return g.Throttled
}

// LlamaCPPFlags reports whether srv's flags are llama.cpp's. Everything that
// prints a flag branches on this one predicate: a self-declared engine's
// (tape.ServerKind.SelfDeclared) argv is its own — an ExLlamaV3 run's is the
// engine's (-gs, -mcs, ...), and rendering it through the llama.cpp token
// set would print five "?"-shaped holes where named flags should be and teach
// flags the run never had (2026-09-15; generalised to every named engine
// 2026-09-17, TTP-105). A generic OpenAI-compatible server (TTP-99) has no
// argv at all — nothing llama-shaped was ever run over it — so it is false
// there too. Every known kind keeps its flags — the question is what the
// argv means, not whether it was read.
func LlamaCPPFlags(srv tape.ServerInfo) bool {
	return srv.Kind.SpeaksLlamaProtocol() && !srv.Kind.SelfDeclared()
}

// flagsSection renders the flag line.
//
// The five argument-starters (-fa, -b, -ub, -ctk, -ctv) are always printed and
// show "?" when unobserved — they are the ones that end comment threads
// (docs/research/02-sharing-artifacts.md §5.2). The remaining flags are
// omitted when empty. Everything except the -ot group wraps without
// truncation; the -ot group, which can be arbitrarily long, is truncated with
// "…" so it never costs more than one extra line.
//
// An engine run prints its own argv instead, verbatim in Flags.Other: one
// element per string the engine reported, joined with single spaces the way it
// would be retyped. "?" when the engine named no arguments at all.
func flagsSection(s *tape.RunSummary) []string {
	// A generic OpenAI-compatible server's flags block is omitted entirely
	// (TTP-99): PID 0, no argv, zero flags — neither the llama.cpp row (five
	// "?" that teach flags the run never had) nor the engine's verbatim argv
	// (there is none) says anything true. Text drops the section; see below.
	if s.Server.Kind == tape.ServerOpenAI {
		return nil
	}
	if !LlamaCPPFlags(s.Server) {
		args := s.Server.Flags.Other
		if len(args) == 0 {
			args = []string{unknown}
		}
		return labelled("FLAGS", blockLabelW, wrapJoin(args, " ", innerWidth-blockLabelW))
	}
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
// blames the tool instead of the budget (TTP-20, 2026-09-13). A run that
// thought and then answered is not cut and gets no warning: the thinking
// clause on the Context row already says how much of the budget went where.
//
// The advice names the flag that would actually help, which is not the same
// flag in both cases (TTP-139, 2026-09-20). "raise --n-predict" was printed
// on every cut run, and on a default run it is wrong: naming --n-predict
// turns the clock off (see Options.limit's table), so on a run that had a
// clock the reader is being told to change the axis that did not end it —
// and a bigger cap under the same clock just hands the run to the clock
// mid-thought instead. What did not hold on a default run is the budget as
// a whole. On a run whose cap the user named there is no clock, the cap is
// the only thing that could have ended it, and raising it is exactly right.
//
// The rate itself is not what this warns about. A reasoning model's tokens
// are decode tokens at the decode rate — the server counts reasoning_content
// in predicted_n like any other token — so the figure beside this sentence
// is a measurement either way. What the run has no answer to show for it is
// the reader's problem, and the flags are how they fix it.
func answerCutWarning(s *tape.RunSummary) string {
	t := s.Timings
	if t.PredictedN <= 0 || t.ReasoningN < t.PredictedN {
		return ""
	}
	head := fmt.Sprintf("answer cut: all %s predicted tokens were reasoning", formatInt(t.PredictedN))
	if s.Limit.MaxTokensNamed {
		return head + " — raise --n-predict"
	}
	// --think-budget is chat-only: it caps the template's thinking, and a
	// raw /completion prompt has none for it to cap (samplingOptions rejects
	// the pair). Advising it there would be a flag the next run errors on.
	if s.Sampling.Endpoint == tape.EndpointCompletion {
		return head + " — the default budget did not hold the thinking; try --for"
	}
	return head + " — the default budget did not hold the thinking; try --for or --think-budget"
}

// warningSection is the card's last block before the footer: the caveats that
// apply to this run.
//
// It was a line per recorded warning until TTP-74 (2026-09-14). The list is now
// derived — every qualification the card makes, the recorder's own free text
// among them, in one place with one predicate each (caveat.go) — and it is
// printed as one line rather than as one line per entry. The hero tape alone
// raises three, and a card that answers "how fast is this rig" with a block of
// exclamation marks has spent its visual budget on the least interesting part
// of itself. The sentence printed is the most serious; the rest are named by
// the code that finds their sentence in `-o json`.
func warningSection(s *tape.RunSummary) []string {
	return caveatLines(s)
}
