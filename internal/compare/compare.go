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

// numRow renders one numeric metric and computes the relative change.
func numRow(label string, a, b float64, f func(float64) string) Row {
	row := Row{Label: label, A: f(a), B: f(b)}
	if a != 0 {
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
		{"quant", a.Model.Quant, b.Model.Quant},
		{"build", a.Server.Build, b.Server.Build},
		{"commit", a.Server.Commit, b.Server.Commit},
		{"engine", string(a.Server.Kind), string(b.Server.Kind)},
		{"ctx", itoa(a.Server.CtxSize), itoa(b.Server.CtxSize)},
		{"streams", itoa(a.Concurrency), itoa(b.Concurrency)},
	}
	var out []Change
	for _, f := range fields {
		if f.a != f.b {
			out = append(out, Change{Label: f.label, A: orUnknown(f.a), B: orUnknown(f.b)})
		}
	}
	return out
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
