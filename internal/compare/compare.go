// Package compare diffs two runs.
//
// It answers the question the card cannot: what changed between this run and
// the last one. Moving one flag and restarting the server is the whole
// workflow of the person this tool is for (docs/toktape-spec.ko.md §3.2 S1),
// and a side-by-side of the nine figures that settle arguments, plus the flag
// diff that caused them, is what makes a change legible.
//
// Diff is a pure function of two summaries and Text is a pure function of the
// report, so the whole package is testable against a fixture summary.
//
// The width contract is the card's: every line is at most card.CardWidth
// display columns, measured with card.Width, so a compare block pastes into
// the same Reddit code block as the card it came from.
package compare

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// Row is one metric on both sides.
type Row struct {
	// Label names the metric, e.g. "decode tok/s".
	Label string
	// A and B are the rendered figures, "?" when unobserved.
	A, B string
	// DeltaPct is B relative to A in percent. Valid only when HasDelta is
	// true: a zero A is not a baseline to be relative to, and a metric that
	// is not a number (contended) has no delta at all.
	DeltaPct float64
	HasDelta bool
}

// Change is one field that differs between the runs, rendered as text.
// Removed is true when B dropped something A had (a `-ot` rule), Added when B
// gained one; both are false for a plain A → B replacement.
type Change struct {
	Label   string
	A, B    string
	Added   bool
	Removed bool
}

// Report is the whole diff.
type Report struct {
	// IDA and IDB identify the runs.
	IDA, IDB string
	// Metrics are the nine figures, in card order.
	Metrics []Row
	// Flags are the server flags that differ.
	Flags []Change
	// Meta are the model, quantisation and build differences.
	Meta []Change
	// Notes qualify the whole comparison, one sentence each, printed under
	// the header before any figure (Tapes fills them; Diff has only the
	// summaries and leaves them empty).
	Notes []string
}

// Tapes is Diff for two recorded runs, with what only the records can say.
//
// A chat (tape.ModeChat) and a benchmark are not comparable, in either order:
// a chat's prompts are a person's conversation, growing turn by turn, and a
// benchmark's are a set the next run repeats (2026-09-24). That is an error,
// not a note, because every figure in the table would be over different
// text. Two chats compare, and a note says so when their first turns differ:
// the rates are then over two different conversations.
func Tapes(a, b *tape.Tape) (Report, error) {
	if a == nil {
		a = &tape.Tape{}
	}
	if b == nil {
		b = &tape.Tape{}
	}
	ca, cb := card.IsChat(&a.Summary), card.IsChat(&b.Summary)
	if ca != cb {
		chat, bench := "A", "B"
		if cb {
			chat, bench = "B", "A"
		}
		return Report{}, fmt.Errorf("%s is a chat and %s a benchmark run, so they are not comparable: a chat's prompts are a conversation, not a set another run repeats", chat, bench)
	}
	r := Diff(&a.Summary, &b.Summary)
	if ca && !sameOpening(a, b) {
		r.Notes = append(r.Notes, "different conversations: the rates are over different text")
	}
	return r, nil
}

// sameOpening reports whether two chats opened with the same messages: the
// first turn's prompt, which every later turn carries as its history. An
// empty first turn on either side is not the same opening — nothing observed
// is not a match.
func sameOpening(a, b *tape.Tape) bool {
	first := func(t *tape.Tape) []tape.Message {
		for _, r := range t.Requests {
			if r.Round == 0 {
				return r.Prompt.Messages
			}
		}
		return nil
	}
	ma, mb := first(a), first(b)
	if len(ma) == 0 || len(ma) != len(mb) {
		return false
	}
	for i := range ma {
		if ma[i] != mb[i] {
			return false
		}
	}
	return true
}

// Diff compares two run summaries. A nil summary is treated as an
// all-unknown run rather than a panic, so a half-readable pair of tapes still
// produces a report.
func Diff(a, b *tape.RunSummary) Report {
	if a == nil {
		a = &tape.RunSummary{}
	}
	if b == nil {
		b = &tape.RunSummary{}
	}
	r := Report{IDA: orUnknown(a.ID), IDB: orUnknown(b.ID)}
	r.Metrics = metrics(a, b)
	r.Flags = flagChanges(a.Server.Flags, b.Server.Flags)
	r.Meta = metaChanges(a, b)
	return r
}

// metrics builds the metric table. Every entry names how it is rendered and
// whether a relative delta means anything for it.
func metrics(a, b *tape.RunSummary) []Row {
	rows := []Row{
		numRow("TTFT", a.Timings.TTFTMs, b.Timings.TTFTMs, formatMs),
		numRow("prefill tok/s", a.Timings.PromptPerSecond, b.Timings.PromptPerSecond, formatRate),
		numRow("decode tok/s", a.Timings.PredictedPerSecond, b.Timings.PredictedPerSecond, formatRate),
	}
	// The acceptance rate sits directly under the rate it explains. A
	// speculative run's decode figure is a property of the draft model as much
	// as of the target, and the row above is already the net speed-up — its
	// delta is the whole answer to "did the draft help", so there is no second
	// speed-up row here (TTP-30, 2026-09-13).
	if a.Timings.DraftN != nil || b.Timings.DraftN != nil {
		rows = append(rows, draftAcceptedRow(a, b))
	}
	// A multi-prompt run's decode figure above is the mean over every round;
	// the median is the figure its card leads with, so it is set beside the
	// other run's here whenever one side has one (TTP-31, 2026-09-13).
	if a.Rounds > 1 || b.Rounds > 1 {
		rows = append(rows, medianRateRow(a, b))
	}
	// The aggregate is the server-wide view and only says something new when
	// at least one of the runs sent more than one stream.
	if a.Concurrency > 1 || b.Concurrency > 1 {
		rows = append(rows, numRow("aggregate tok/s",
			a.Aggregate.AggregatePredictedPerSecond,
			b.Aggregate.AggregatePredictedPerSecond, formatRate))
	}
	rows = append(rows,
		numRow("VRAM used", float64(vramUsed(a)), float64(vramUsed(b)), formatBytesGiB),
		numRow("host RSS", float64(a.Memory.AtEnd.RSSBytes), float64(b.Memory.AtEnd.RSSBytes), formatBytesGiB),
		// A page-fault rate of zero is a real measurement, not an unknown, so
		// it is rendered as "0.0" and still carries a delta when A was not 0.
		numRow("maj faults/token", a.Memory.MajFaultsPerToken, b.Memory.MajFaultsPerToken, formatFloat1),
		numRow("cache hit", a.Cache.HitRatio*100, b.Cache.HitRatio*100, formatPct),
		Row{
			Label: "contended",
			A:     yesNo(a.Contention.Contended),
			B:     yesNo(b.Contention.Contended),
		},
	)
	return rows
}

// draftAcceptedRow is the share of drafted tokens the target agreed with, on
// each side.
//
// It is built by hand rather than through numRow because the two "no figure"
// cases are different claims and only one of them is a number. A run that
// reported no draft prints "?" — nobody drafted anything — while a run that
// drafted and had nothing accepted really is 0 %, and flattening the two would
// make a draft model the target never agreed with look like a run that never
// used one. A rate over zero drafted tokens is no rate at all, so that prints
// "?" too: the denominator was never there.
func draftAcceptedRow(a, b *tape.RunSummary) Row {
	rate := func(s *tape.RunSummary) (float64, bool) {
		t := s.Timings
		if t.DraftN == nil || *t.DraftN == 0 {
			return 0, false
		}
		accepted := 0
		if t.DraftNAccepted != nil {
			accepted = *t.DraftNAccepted
		}
		return float64(accepted) / float64(*t.DraftN) * 100, true
	}
	av, aok := rate(a)
	bv, bok := rate(b)
	row := Row{Label: "draft accepted", A: "?", B: "?"}
	if aok {
		row.A = formatPct(av)
	}
	if bok {
		row.B = formatPct(bv)
	}
	// A delta needs both an observation to compare against and a baseline that
	// is not zero, like every other row here.
	if aok && bok && av != 0 {
		row.DeltaPct = (bv - av) / av * 100
		row.HasDelta = true
	}
	return row
}

// medianRateRow is the median per-stream rate over the prompt rounds of each
// side. A single-round run has no spread to take a median of, so it prints
// "?": its one rate is already the decode row above, and repeating it here
// would present one prompt as the median of several.
func medianRateRow(a, b *tape.RunSummary) Row {
	median := func(s *tape.RunSummary) (float64, bool) {
		if s.Rounds < 2 || s.Spread == nil || s.Spread.PerStreamPredictedPerSecond.Median <= 0 {
			return 0, false
		}
		return s.Spread.PerStreamPredictedPerSecond.Median, true
	}
	av, aok := median(a)
	bv, bok := median(b)
	row := Row{Label: "tok/s median", A: "?", B: "?"}
	if aok {
		row.A = formatRate(av)
	}
	if bok {
		row.B = formatRate(bv)
	}
	if aok && bok {
		row.DeltaPct = (bv - av) / av * 100
		row.HasDelta = true
	}
	return row
}

// roundsLabel names the rounds count: a chat's rounds are its turns. Only
// two chats reach here as a chat pair (Tapes refuses a mixed one).
func roundsLabel(a, b *tape.RunSummary) string {
	if card.IsChat(a) && card.IsChat(b) {
		return "turns"
	}
	return "prompts"
}

// promptsCount is the number of prompt rounds a run sent. A run recorded
// without a prompts file sent one (Rounds 0 and 1 are both a single round).
func promptsCount(s *tape.RunSummary) string {
	if s.Rounds <= 1 {
		return "1"
	}
	return strconv.Itoa(s.Rounds)
}

// numRow renders one numeric metric and computes the relative change.
//
// A side the formatter prints as "?" was never observed, so there is nothing
// to change from or to: no delta, not -100% (a formatter whose zero is a real
// measurement, like the page-fault rate, never prints "?" and keeps it).
func numRow(label string, a, b float64, f func(float64) string) Row {
	row := Row{Label: label, A: f(a), B: f(b)}
	if a != 0 && row.A != "?" && row.B != "?" {
		row.DeltaPct = (b - a) / a * 100
		row.HasDelta = true
	}
	return row
}

// vramUsed is the VRAM held on every device at the end of the run.
func vramUsed(s *tape.RunSummary) int64 {
	var sum int64
	for _, g := range s.GPUsAtEnd {
		sum += g.UsedBytes
	}
	return sum
}

// flagChanges diffs the flags the card always prints. The scalar flags are a
// straight A → B; -ot is a set, so each rule that appears on only one side is
// its own added or removed line.
func flagChanges(a, b tape.ServerFlags) []Change {
	scalars := []struct {
		label string
		a, b  string
	}{
		{"-ngl", a.NGL, b.NGL},
		{"-fa", a.FlashAttn, b.FlashAttn},
		{"-b", a.Batch, b.Batch},
		{"-ub", a.UBatch, b.UBatch},
		{"-ctk", a.CacheTypeK, b.CacheTypeK},
		{"-ctv", a.CacheTypeV, b.CacheTypeV},
		{"-t", a.Threads, b.Threads},
		{"--load-mode", a.LoadMode, b.LoadMode},
		{"-cmoe/-ncmoe", a.CPUMoE, b.CPUMoE},
		// The two draft thresholds have no other home: -md and --draft-max are
		// named in the run metadata above, but these reach the reader only
		// here and on the card's FLAGS line (TTP-30).
		{"--draft-min", a.DraftMin, b.DraftMin},
		{"--draft-p-min", a.DraftPMin, b.DraftPMin},
	}
	var out []Change
	for _, s := range scalars {
		if s.a != s.b {
			out = append(out, Change{Label: s.label, A: orUnknown(s.a), B: orUnknown(s.b)})
		}
	}
	out = append(out, setChanges("-ot", a.OverrideTens, b.OverrideTens)...)
	out = append(out, setChanges("other", a.Other, b.Other)...)
	return out
}

// setChanges reports the entries of b that a did not have and the entries of
// a that b dropped, in the order each side listed them.
func setChanges(label string, a, b []string) []Change {
	inA := make(map[string]bool, len(a))
	for _, v := range a {
		inA[v] = true
	}
	inB := make(map[string]bool, len(b))
	for _, v := range b {
		inB[v] = true
	}
	var out []Change
	for _, v := range a {
		if !inB[v] {
			out = append(out, Change{Label: label, A: v, Removed: true})
		}
	}
	for _, v := range b {
		if !inA[v] {
			out = append(out, Change{Label: label, B: v, Added: true})
		}
	}
	return out
}

// metaChanges diffs what the two runs were measuring. A decode rate that
// moved means nothing if the model or the build moved with it, so these lines
// come before the reader draws a conclusion.
func metaChanges(a, b *tape.RunSummary) []Change {
	fields := []struct {
		label string
		a, b  string
	}{
		{"model", a.Model.FileName, b.Model.FileName},
		// The directory is part of the model's identity when the file is
		// sharded (TTP-32): a hard-linked shard set with a different embedding
		// quant carries the same file names and differs only here.
		{"model dir", modelDir(a, b), modelDir(b, a)},
		{"quant", a.Model.Quant, b.Model.Quant},
		{"build", a.Server.Build, b.Server.Build},
		{"commit", a.Server.Commit, b.Server.Commit},
		// An engine that did not identify itself is unknown however the tape
		// spells it, "" or tape.ServerUnknown, and prints "?" (TTP-37).
		{"engine", engineKind(a.Server.Kind), engineKind(b.Server.Kind)},
		// Which draft produced the acceptance rate in the metric table, and
		// how many tokens it was allowed to guess at a time (TTP-30). A rate
		// that moved says nothing if the draft moved with it.
		{"draft model", a.Server.Flags.DraftModel, b.Server.Flags.DraftModel},
		{"draft n_max", a.Server.Flags.DraftMax, b.Server.Flags.DraftMax},
		// Which block sizes a --spec-n-max sweep ran (TTP-35): the per-value
		// lines of two sweeps compare only over the same values.
		{"spec n_max", specNMaxList(a), specNMaxList(b)},
		{"ctx", itoa(a.Server.CtxSize), itoa(b.Server.CtxSize)},
		{"streams", itoa(a.Concurrency), itoa(b.Concurrency)},
		// How many prompts the figures are over (TTP-31): a rate that moved
		// between one prompt and eight is a different measurement.
		{roundsLabel(a, b), promptsCount(a), promptsCount(b)},
	}
	var out []Change
	for _, f := range fields {
		if f.a != f.b {
			out = append(out, Change{Label: f.label, A: orUnknown(f.a), B: orUnknown(f.b)})
		}
	}
	return out
}

// specNMaxList is a sweep's values as the flag took them, "3,5", or "none" for
// a run that swept nothing. That is an observation, not an unknown: a run
// recorded without --spec-n-max sent no override, so "?" would be wrong.
func specNMaxList(s *tape.RunSummary) string {
	if len(s.SpecNMax) == 0 {
		return "none"
	}
	parts := make([]string, len(s.SpecNMax))
	for i, v := range s.SpecNMax {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

// modelDir is s's model directory, but only when the other run recorded one
// too.
//
// A directory that only one side has is a gap in that recording — an older
// tape, or a path the recorder could not read — and reporting it as
// "? → bartowski-Q4_K_M" would read as a change in the thing being measured
// when nothing about the model moved. With neither side carrying one the
// values are equal and the row never appears at all.
func modelDir(s, other *tape.RunSummary) string {
	if s.Model.Dir == "" || other.Model.Dir == "" {
		return ""
	}
	return s.Model.Dir
}

// engineKind is the engine's name for the run section, "" for one that did not
// identify itself so orUnknown prints it as "?" and two unknowns are equal.
func engineKind(k tape.ServerKind) string {
	if k == tape.ServerUnknown {
		return ""
	}
	return string(k)
}

// orUnknown is the card's rule: a value that was not observed prints "?".
func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "?"
	}
	return s
}

// Width is re-exported from card so callers measure a compare block the same
// way they measure a card.
const Width = card.CardWidth
