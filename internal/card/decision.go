package card

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// The decision card (TTP-192, 2026-10-02) is what a decision-model run prints
// instead of the token-stream card. Nothing was generated, so there is no
// Decode, no TTFT and no tok/s of output: the one figure that matters is how
// long a request takes from send to answer, and the card's job is to say that
// figure without letting a reader feel it is flattering.
//
// It is built once into a DecisionView and both renderings read the view — the
// text box here and the PNG in internal/card/png — so the two cannot word a
// figure differently (the rule the token card keeps by mirroring card.go field
// for field, kept here by sharing the strings).
//
// What the card refuses to do, each from a fact of the first engine:
//   - cold is a row of its own and never inside the warm figures: the first
//     request pays for everything that was not yet resident;
//   - short and long prompts are two rows: one 4.4k-token case dominates a
//     whole-suite p95, and a median over both describes neither;
//   - the timing word is printed beside the hero: every figure is the client's
//     send-to-last-byte time ("end to end", 2026-10-02: what an agent pays and
//     what curl reproduces), and the engine's own prompt + head time is a row
//     of its own, never the hero;
//   - no figure that is not on the tape — Cloudflare's published "Clef-flash
//     median 38.8 ms" carries no hardware and no prompt length, so it is never
//     printed beside ours.

// Decision row keys. The PNG picks its cells by key, so a row that is absent
// (no long prompt in the suite, no cache hit) leaves a gap in the layout
// rather than a "?".
const (
	DecisionRowEngine     = "engine"
	DecisionRowCold       = "cold"
	DecisionRowShort      = "short"
	DecisionRowLong       = "long"
	DecisionRowP95        = "p95"
	DecisionRowPrefill    = "prefill"
	DecisionRowThroughput = "throughput"
	DecisionRowRequests   = "requests"
	DecisionRowCache      = "cache"
	DecisionRowReference  = "reference"
)

// DecisionView is the decision card as strings.
type DecisionView struct {
	Hero DecisionHero
	// Rows are in card order. Engine, Cache and Reference are present only when
	// the tape has something to say; Short and Long only when the suite has a
	// prompt on that side or a figure for it.
	Rows []DecisionRow
	// Caveats is empty on a decision card: the one it had (client timing)
	// became the words "end to end" beside the hero. The field stays so the
	// JSON document keeps its shape.
	Caveats []Caveat
	// Note is RunSummary.Note, verbatim: the example tape says SYNTHETIC and
	// the card must show it.
	Note string
}

// DecisionHero is the card's one lit figure: "37.6 ms p50 · warm · end to end".
// The percentile, the phase and the clock are fixed words, not fields: the
// figure is the warm median of the client's send-to-last-byte time or it is
// nothing.
type DecisionHero struct {
	Value string // "37.6", or "?"
	Unit  string // "ms", empty when Value is "?"
}

// DecisionHeroCaption is the line under the hero figure on every surface.
const DecisionHeroCaption = "p50 · warm · end to end"

// DecisionRow is one labelled row. Parts[0] is the figure; the rest qualify it.
type DecisionRow struct {
	Key   string
	Label string
	Parts []string
}

// Line is the figure with its percentile: "37.6 ms p50", or "?" alone.
func (h DecisionHero) Line() string {
	if h.Unit == "" {
		return h.Value
	}
	return h.Value + " " + h.Unit + " p50"
}

// Value is the row's figure.
func (r DecisionRow) Value() string {
	if len(r.Parts) == 0 {
		return ""
	}
	return r.Parts[0]
}

// Detail is the row's qualifiers, joined.
func (r DecisionRow) Detail() string {
	if len(r.Parts) < 2 {
		return ""
	}
	return strings.Join(r.Parts[1:], " · ")
}

// Row returns the row with that key.
func (v DecisionView) Row(key string) (DecisionRow, bool) {
	for _, r := range v.Rows {
		if r.Key == key {
			return r, true
		}
	}
	return DecisionRow{}, false
}

// DecisionCard derives the decision card from s. A decision tape without a
// summary renders the all-unknown card rather than panicking.
func DecisionCard(s *tape.RunSummary) DecisionView {
	if s == nil {
		s = &tape.RunSummary{}
	}
	d := tape.DecisionSummary{}
	if s.Decision != nil {
		d = *s.Decision
	}

	v := DecisionView{Note: strings.TrimSpace(s.Note)}
	v.Hero = DecisionHero{
		Value: decisionMsNumber(d.WarmP50Ms),
		Unit:  "ms",
	}
	if v.Hero.Value == unknown {
		v.Hero.Unit = ""
	}

	short, long := splitCases(d.PerCase)
	row := func(key, label string, parts ...string) {
		v.Rows = append(v.Rows, DecisionRow{Key: key, Label: label, Parts: dropEmpty(parts...)})
	}

	// The engine's own prompt + head time, a breakdown of the hero and not a
	// second hero (2026-10-02): present only when every answered request
	// carried it.
	if d.EngineWarmP50Ms > 0 {
		row(DecisionRowEngine, "Engine", decisionMs(d.EngineWarmP50Ms)+" p50", "prompt + head")
	}
	row(DecisionRowCold, "Cold", decisionMs(d.ColdMs), "first request")
	if d.ShortWarmP50Ms > 0 || len(short) > 0 {
		detail := ""
		if len(short) > 0 {
			detail = fmt.Sprintf("%s under %s tok", plural(len(short), "case"), groupThousands(tape.DecisionLongPromptTokens))
		}
		row(DecisionRowShort, "Short", decisionMs(d.ShortWarmP50Ms)+" p50", detail)
	}
	if d.LongWarmP50Ms > 0 || len(long) > 0 {
		detail := ""
		switch {
		case len(long) == 1:
			detail = groupThousands(long[0].InputTokens) + " tok"
		case len(long) > 1:
			detail = fmt.Sprintf("%d cases", len(long))
		}
		row(DecisionRowLong, "Long", decisionMs(d.LongWarmP50Ms)+" p50", detail)
	}
	row(DecisionRowP95, "p95", decisionMs(d.WarmP95Ms), "whole suite")
	row(DecisionRowPrefill, "Prefill", decisionRate(d.PrefillPerSecond, "tok/s"), "median per request")
	row(DecisionRowThroughput, "Throughput", decisionRate(d.RequestsPerSecond, "req/s"), concurrencyWord(d.Concurrency))
	row(DecisionRowRequests, "Requests", strconv.Itoa(d.Requests), casesPasses(d), plural(d.Errors, "error"))

	// A cache hit means the engine did not pay for the whole prompt, so the
	// latency is not a prefill: said here rather than left to a reader of the
	// JSON (decision.go, DecisionServerTimings.CacheN).
	if d.CacheHits > 0 {
		row(DecisionRowCache, "Cache", plural(d.CacheHits, "hit"), "prompt partly reused")
	}
	// No Reference row without a reference file: the card prints no
	// correctness figure it did not compute.
	if r := d.Reference; r != nil {
		row(DecisionRowReference, "Reference",
			"max |Δp| "+strconv.FormatFloat(r.MaxAbsDeltaP, 'f', 3, 64),
			plural(r.TopFlips, "flip"),
			"Brier "+fmt.Sprintf("%+.4f", r.BrierDelta),
			"vs "+orUnknown(r.File))
	}

	return v
}

// decisionText renders the text card for a decision run: the token card's
// frame, header and identity block, then the decision rows. The memory, host
// and flags blocks are absent because a decision run reads none of them.
func decisionText(s *tape.RunSummary) string {
	v := DecisionCard(s)

	rows := field("Decision", speedLabelW, " · ", v.Hero.Line(), "warm", "end to end")
	for _, r := range v.Rows {
		rows = append(rows, field(r.Label, speedLabelW, " · ", r.Parts...)...)
	}

	sections := [][]string{
		{headerLine(s)},
		identitySection(s),
		rows,
	}
	// The caveat and the note share a block, wrapped word by word and never
	// truncated: a note cut mid-sentence on a synthetic tape is the one place
	// the cut would hide the word SYNTHETIC's sentence is about.
	var tail []string
	for _, c := range v.Caveats {
		tail = append(tail, wrapMarked("! ", c.Text)...)
	}
	if v.Note != "" {
		tail = append(tail, wrapMarked("", v.Note)...)
	}
	if len(tail) > 0 {
		sections = append(sections, tail)
	}
	sections = append(sections, []string{center(footerText, innerWidth)})
	return decisionFrame(sections)
}

// decisionFrame boxes sections the way Text does: one rule between sections,
// every line CardWidth columns. It repeats Text's loop rather than moving it,
// so the token card's code is not touched by this track.
func decisionFrame(sections [][]string) string {
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

// wrapMarked wraps text to the card's inner width with a first-line mark and
// a matching indent for the rest.
func wrapMarked(mark, text string) []string {
	lines := wrapJoin(strings.Fields(text), " ", innerWidth-Width(mark))
	out := make([]string, len(lines))
	for i, l := range lines {
		if i == 0 {
			out[i] = mark + l
			continue
		}
		out[i] = strings.Repeat(" ", Width(mark)) + l
	}
	return out
}

// ---------------------------------------------------------------- figures ---

// splitCases separates the suite's rows at the long-prompt line. A case with
// no recorded token count is on neither side: its length is unknown.
func splitCases(per []tape.DecisionCaseSummary) (short, long []tape.DecisionCaseSummary) {
	for _, c := range per {
		switch {
		case c.InputTokens >= tape.DecisionLongPromptTokens:
			long = append(long, c)
		case c.InputTokens > 0:
			short = append(short, c)
		}
	}
	return short, long
}

// decisionMsNumber is a latency without its unit: one decimal below 100, an
// integer above, thousands grouped. Zero is unobserved and prints "?".
func decisionMsNumber(v float64) string {
	switch {
	case v <= 0:
		return unknown
	case v < 100:
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return groupThousands(int(v + 0.5))
}

func decisionMs(v float64) string {
	n := decisionMsNumber(v)
	if n == unknown {
		return unknown
	}
	return n + " ms"
}

// decisionRate renders a per-second figure: an integer below 1,000 and "10.6k"
// above, one decimal. Zero is unobserved.
func decisionRate(v float64, unit string) string {
	switch {
	case v <= 0:
		return unknown
	case unit == "req/s":
		return formatRate(v) + " " + unit
	case v < 999.5:
		return strconv.FormatFloat(v, 'f', 0, 64) + " " + unit
	}
	return strconv.FormatFloat(v/1000, 'f', 1, 64) + "k " + unit
}

func concurrencyWord(c int) string {
	if c <= 0 {
		return ""
	}
	return "c" + strconv.Itoa(c)
}

func casesPasses(d tape.DecisionSummary) string {
	if d.Cases <= 0 || d.Repeats <= 0 {
		return ""
	}
	return fmt.Sprintf("%d %s × %d %s", d.Cases, pluralWord(d.Cases, "case"), d.Repeats, pluralWord(d.Repeats, "pass"))
}

func plural(n int, word string) string { return fmt.Sprintf("%d %s", n, pluralWord(n, word)) }

func pluralWord(n int, word string) string {
	if n == 1 {
		return word
	}
	if strings.HasSuffix(word, "ss") {
		return word + "es"
	}
	return word + "s"
}

// groupThousands renders 4386 as "4,386".
func groupThousands(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
