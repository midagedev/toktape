package tui

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/midagedev/toktape/internal/tape"
)

// The emphasis contract (TTP-28, user 2026-09-13: "화면에 너무 많은 요소들이
// 강조되어 있다. tok/s가 팬 우상단 구석에 있어서 요소가 흩어져 헷갈렸어").
//
// A screen where the sparklines, the four section titles, three bars, the fault
// figure and the active header are all lit in the accent has no hierarchy: the
// eye lands nowhere. So the accent is spent on one figure per region — each
// tile's own decode rate, and the right pane's decode and aggregate rows — and
// everything else is demoted to the muted accent, to plain text or to dim.
//
// These two tests are the mechanical half of that contract. The first pins the
// figures: the only accent-coloured digits on a frame are those rates. The
// second pins everything else the accent can paint, because the loudest element
// on the screen the lead complained about was a sparkline, and a sparkline
// carries no digits for the first test to catch.

// accentShades are the accent hue at every lightness the theme carries, as the
// frames actually spell them (see styleHex). A figure painted in any of them
// reads as lit, so the digit gate treats them alike; the demoted shades exist
// for shapes — bars and sparklines — and not for numbers.
func accentShades() map[string]bool {
	th := ColourTheme()
	out := map[string]bool{}
	// graphSolo and graphRidge are the accent hue too: they carry a background
	// as well, and styleHex reads only the foreground, so they fold onto
	// accentMuted and accentMid here. They are listed so the set stays the
	// answer to "every style that paints the accent hue" and not a list that
	// happens to cover them (TTP-43b, 2026-09-13).
	for _, st := range []lipgloss.Style{th.accent, th.accentBold, th.accentHigh, th.accentMid, th.accentLow, th.accentMuted, th.graphRidge, th.graphSolo} {
		out[styleHex(st)] = true
	}
	return out
}

// rateFigure is what a stat line leads with: a decode rate, or the "?" that
// stands in for one that has not been measured yet.
//
// The sparkline that follows it on the same row (TTP-29) is not a figure: its
// runes are block elements and isFigureRune matches digits and the decimal
// point, so the digit gate below neither counts the graph nor has to make an
// exception for it. The one lit cell in it is the accent, and
// TestAccentIsReserved is where that is allowed for.
var rateFigure = regexp.MustCompile(`^(\?|[0-9]+(\.[0-9]+)?) tok/s`)

type emphasisFrame struct {
	name string
	m    Model
	at   time.Duration
	w, h int
}

// emphasisFrames are the frames both gates run over: the hero's four streams at
// the size the clip is rendered at, eight streams on the same screen, a larger
// terminal, the opening frame and the finished run.
func emphasisFrames(t *testing.T) []emphasisFrame {
	t.Helper()
	out := []emphasisFrame{
		{"n4-120x36", ModelAt(ExampleTapeN(4), midRun), midRun, 120, 36},
		{"n8-120x36", ModelAt(ExampleTapeN(8), midRun), midRun, 120, 36},
		{"n4-160x48", ModelAt(ExampleTapeN(4), midRun), midRun, 160, 48},
		{"n4-156x38", ModelAt(ExampleTapeN(4), midRun), midRun, 156, 38},
		{"n4-120x36-t0", ModelAt(ExampleTapeN(4), 0), 0, 120, 36},
		{"n4-120x36-done", ModelAt(ExampleTapeN(4), doneAt), doneAt, 120, 36},
	}
	for i := range out {
		out[i].m.Theme = ColourTheme()
		out[i].m.TapePath = "~/.toktape/runs/" + out[i].m.Summary.ID + ".tape"
	}
	return out
}

// TestOnlyTheRateIsAccent is the numeric half of the contract.
//
// A rate is recognised structurally rather than by value: it is the figure a
// tile's stat line leads with (the row under a "stream N/M" header), or the one
// the right pane's "decode" or "N streams … agg" row is built around. The test
// therefore keeps holding when the example's figures change, and still fails
// the moment a second figure is lit.
func TestOnlyTheRateIsAccent(t *testing.T) {
	for _, f := range emphasisFrames(t) {
		t.Run(f.name, func(t *testing.T) {
			rows := parseFrame(View(f.m, f.at, f.w, f.h), f.w, f.h)
			lit := 0
			for y, row := range rows {
				spans := rateSpans(rows, y)
				for _, rn := range accentRuns(row, isFigureRune) {
					if !within(spans, rn) {
						t.Errorf("the figure %q at row %d col %d is accent, and it is neither a tile's rate nor the right pane's decode or aggregate figure\n%s",
							rn.text, y, rn.from, rowContext(rows, y))
						continue
					}
					lit++
				}
			}
			// The gate has to be able to fail the other way too: a frame with
			// nothing lit would pass the loop above, and would mean the
			// hierarchy had been flattened rather than ordered.
			if want := visibleRates(f.m, f.at, f.w, f.h); lit < want {
				t.Errorf("%d accent rate figures on the frame, want at least %d — the emphasis is gone, not reordered", lit, want)
			}
		})
	}
}

// TestAccentIsReserved is the other half: the full accent, the shade the eye
// goes to first, paints the rate figures and the live chrome, and nothing else.
// The demoted shades are what sparklines, placement bars and section titles
// wear.
func TestAccentIsReserved(t *testing.T) {
	for _, f := range emphasisFrames(t) {
		t.Run(f.name, func(t *testing.T) {
			rows := parseFrame(View(f.m, f.at, f.w, f.h), f.w, f.h)
			for y, row := range rows {
				spans := rateSpans(rows, y)
				for _, rn := range accentRuns(row, func(r rune) bool { return r != ' ' }) {
					if row[rn.from].fg != styleHex(ColourTheme().accent) {
						continue // a demoted shade; the contract allows those
					}
					if within(spans, rn) || reservedChrome(rows, y, rn) || sparkWriteHead(rows, y, rn) {
						continue
					}
					t.Errorf("%q at row %d col %d wears the full accent, which is reserved for the rate figures, the brand, the cursor, the spinner, the prefill bar and one sparkline cell per stat line\n%s",
						rn.text, y, rn.from, rowContext(rows, y))
				}
			}
		})
	}
}

// visibleRates is how many accent rate figures a frame must carry: one per
// visible tile whose rate has been measured, plus the right pane's decode row,
// plus its aggregate row when the run has more than one stream. A figure that
// has not been measured prints "?" and carries no digits, so it is not counted.
func visibleRates(m Model, at time.Duration, w, h int) int {
	g := m.Grid.resolve(w-2-1-rightWidth(w)-2, h-chromeH)
	n := 0
	for i, s := range m.Streams {
		if i >= g.cells() {
			break
		}
		if streamRateFigure(s) != unknown {
			n++
		}
	}
	if _, cur, _ := m.decodeRateAt(at); cur > 0 || m.Done {
		n++
		if m.Summary.Concurrency > 1 {
			n++
		}
	}
	return n
}

// rateSpans are the column ranges on one row that the contract lights: the
// figure and the unit that names it.
func rateSpans(rows [][]pcell, y int) []span {
	var out []span
	for _, seg := range segments(rows[y]) {
		// A tile's stat line: the rate is the first thing inside the tile, and
		// the row above it is that tile's own header.
		if m := rateFigure.FindString(seg.text); m != "" {
			if y > 0 && strings.HasPrefix(segmentAt(rows[y-1], seg.from), "stream") {
				out = append(out, span{from: seg.from, to: seg.from + cond.StringWidth(m)})
			}
			continue
		}
		fields := strings.Fields(seg.text)
		switch {
		case len(fields) > 0 && (fields[0] == "decode" || fields[0] == "sample"):
			// "decode            17.4 tok/s"
			out = append(out, figureSpan(seg))
		case len(fields) > 1 && fields[1] == "streams" && strings.HasSuffix(strings.TrimRight(seg.text, " "), "agg"):
			// "4 streams      50.1 tok/s agg"
			out = append(out, figureSpan(seg))
		}
	}
	return out
}

// figureSpan locates "<figure> tok/s" inside a right-pane row and returns the
// columns it covers.
func figureSpan(seg span) span {
	at := strings.LastIndex(seg.text, " tok/s")
	if at < 0 {
		return span{}
	}
	start := strings.LastIndexAny(seg.text[:at], " ") + 1
	return span{
		from: seg.from + cond.StringWidth(seg.text[:start]),
		to:   seg.from + cond.StringWidth(seg.text[:at+len(" tok/s")]),
	}
}

// span is a range of display columns on one row, to exclusive. text is filled
// for the pane segments; a rate span carries only its bounds.
type span struct {
	from, to int
	text     string
}

func within(spans []span, rn run) bool {
	for _, s := range spans {
		if rn.from >= s.from && rn.to <= s.to {
			return true
		}
	}
	return false
}

// reservedChrome is the accent that is not a figure: the brand on the title
// bar, the breathing cursor of the stream that is talking, the
// prefill spinner and the evaluated part of a prompt-progress bar, and the tick
// on the footer of a finished run. Each is a single glyph or a word, and each
// says something no demoted shade could.
func reservedChrome(rows [][]pcell, y int, rn run) bool {
	switch {
	case rn.text == "toktape":
		return y == 0
	// The active stream's gutter ▏ was reserved here until the gutter went
	// (TTP-50, 2026-09-14). Removing the case tightens the gate: an accent ▏
	// anywhere now fails.
	case strings.Trim(rn.text, "▍") == "": // the cursor on the newest token
		return true
	case rn.text == "✓": // "tape saved", on the pane footer
		return true
	case strings.ContainsAny(rn.text, string(spinnerFrames)):
		return true
	case strings.Trim(rn.text, "█") == "" && strings.Contains(plainRow(rows[y]), prefillWord):
		// The evaluated share of a prompt-progress bar: the tile's one live
		// figure until a token arrives, and gone by the time the rate
		// replaces it.
		return true
	}
	return false
}

// sparkWriteHead is the one lit cell of a tile's rate graph: the newest
// sample, at the right-hand end of the sparkline on that tile's stat line.
//
// 2026-09-13 (TTP-29) — this is an addition to the accent contract, not a
// relaxation of it, and it is written as tightly as the thing it admits. One
// cell, exactly one rune, and only a sparkline rune (U+2581–U+2588: the cursor
// ▍ is outside that range and is covered by reservedChrome above). It must sit on a row whose tile header is directly
// above it, and nothing of the graph may follow it — the cell to its right is
// blank or the segment ends there. A second lit cell, a lit cell in the middle
// of the line, or a lit graph anywhere but a stat line all still fail.
//
// FAIL-first: before the write head was painted, no accent block rune appeared
// on a stat line at all, and with the footer's full-accent line this predicate
// admitted nothing (the footer was muted end to end, and it was thirty cells
// wide, not one).
//
// Why it earns the accent when no other shape does: the user asked for the
// newest sample to be the bright one ("스파크라인도 마지막 것만 밝게 하고 이전
// 것은 어둡게 하고"), and one cell of hue is how a graph says which end is now.
// The rest of the line stays in the muted accent the emphasis contract gives
// every shape.
func sparkWriteHead(rows [][]pcell, y int, rn run) bool {
	if rn.to-rn.from != 1 || len([]rune(rn.text)) != 1 {
		return false
	}
	r := []rune(rn.text)[0]
	if r < '▁' || r > '█' {
		return false
	}
	if y == 0 || !strings.HasPrefix(segmentAt(rows[y-1], rn.from), "stream") {
		return false
	}
	next := rn.to
	return next >= len(rows[y]) || rows[y][next].r == ' ' || rows[y][next].r == '│'
}

// run is a horizontal run of same-coloured cells on one row.
type run struct {
	from, to int
	text     string
}

// accentRuns returns the maximal runs of accent-coloured cells on a row whose
// runes all satisfy keep.
func accentRuns(row []pcell, keep func(rune) bool) []run {
	shades := accentShades()
	var out []run
	for x := 0; x < len(row); {
		c := row[x]
		if !shades[c.fg] || c.r == 0 || !keep(c.r) {
			x++
			continue
		}
		var b strings.Builder
		from := x
		for x < len(row) && row[x].fg == c.fg && row[x].r != 0 && keep(row[x].r) {
			b.WriteRune(row[x].r)
			x++
		}
		out = append(out, run{from: from, to: x, text: b.String()})
	}
	return out
}

// isFigureRune is what a printed number is made of.
func isFigureRune(r rune) bool { return (r >= '0' && r <= '9') || r == '.' }

// segments splits a row on the frame's borders and the tile rules, and returns
// each pane's own content with the display column its text starts at. The pane
// keeps a column of gutter on each side of a rule, so the leading blanks come
// off with the rule.
func segments(row []pcell) []span {
	var out []span
	start := 0
	flush := func(end int) {
		from := start
		for from < end && row[from].r == ' ' {
			from++
		}
		if from < end {
			out = append(out, span{from: from, to: end, text: textIn(row, from, end)})
		}
	}
	for x, c := range row {
		switch c.r {
		case '│', '├', '┤', '┌', '┐', '└', '┘':
			flush(x)
			start = x + 1
		}
	}
	flush(len(row))
	return out
}

// segmentAt is the text of the segment column x falls in.
func segmentAt(row []pcell, x int) string {
	for _, seg := range segments(row) {
		if x >= seg.from && x < seg.to {
			return seg.text
		}
	}
	return ""
}

// textIn is the row's text between two display columns.
func textIn(row []pcell, from, to int) string {
	var b strings.Builder
	for x := from; x < to && x < len(row); x++ {
		if row[x].r == 0 {
			continue
		}
		b.WriteRune(row[x].r)
	}
	return b.String()
}

// rowContext renders a row and its neighbours for a failure message.
func rowContext(rows [][]pcell, y int) string {
	var b strings.Builder
	for i := y - 1; i <= y+1; i++ {
		if i < 0 || i >= len(rows) {
			continue
		}
		fmt.Fprintf(&b, "  %2d | %s\n", i, strings.TrimRight(plainRow(rows[i]), " "))
	}
	return b.String()
}

func plainRow(row []pcell) string { return textIn(row, 0, len(row)) }

// The body-tone ladder and the write-head glow (TTP-28, user 2026-09-13:
// "토큰 내용 자체는 한 톤 내리는 게 맞겠어", then "방금 막 나온 토큰 정도만 조금
// 밝게 해서 속도감은 살리자").
//
// The two are one contract read from both ends. The body drops a tone so the
// header and the rate can be read against it, and the tokens that have just
// landed keep the header's tone for a moment so the drop does not also remove
// the sense that anything is happening.

// relLuminance is WCAG relative luminance, which is what "one tone down" has
// to mean if it is to be a number rather than an opinion.
func relLuminance(hex string) float64 {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return -1
	}
	chan_ := func(i int) float64 {
		var v int
		fmt.Sscanf(hex[i:i+2], "%x", &v)
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*chan_(0) + 0.7152*chan_(2) + 0.0722*chan_(4)
}

// TestBodyToneLadderDescends pins the five stops the body text moves through,
// measured off the theme's own hexes rather than off a frame: a palette edit
// that flattened two of them together would leave every frame test passing and
// the hierarchy gone.
func TestBodyToneLadderDescends(t *testing.T) {
	th := ColourTheme()
	ladder := []struct {
		name string
		st   lipgloss.Style
	}{
		{"text", th.text},
		{"textMid", th.textMid},
		{"textMuted", th.textMuted},
		{"dimMid", th.dimMid},
		{"dim", th.dim},
	}
	lum := map[string]float64{}
	for i, stop := range ladder {
		lum[stop.name] = relLuminance(styleHex(stop.st))
		if i == 0 {
			continue
		}
		if prev := ladder[i-1]; lum[stop.name] >= lum[prev.name] {
			t.Errorf("%s is %.4f and %s is %.4f; each stop must be darker than the one above it",
				stop.name, lum[stop.name], prev.name, lum[prev.name])
		}
	}
	// The two ratios the contract names. The body sits at roughly half the
	// header's luminance, and the reasoning text at or under 80 % of the body's.
	// 2026-09-13: re-pinned from 0.72 (band 0.68–0.76) after the user watched
	// the clip: the glow was visible on reasoning and invisible on answers, so
	// the answer step had to become as large as the reasoning step. FAIL-first:
	// the 0.72 palette fails this band.
	if got := lum["textMuted"] / lum["text"]; got < 0.44 || got > 0.54 {
		t.Errorf("the body is %.3f of the header's luminance, want about 0.49", got)
	}
	if got := lum["dim"] / lum["textMuted"]; got > 0.80 {
		t.Errorf("reasoning text is %.3f of the body's luminance, want 0.80 or less", got)
	}
	// And the rate, which is what the eye is meant to find first, is not found
	// by brightness: the accent is a hue at roughly the body's luminance. This
	// is asserted rather than assumed because it is the reason the emphasis
	// contract spends boldness and hue on the rate and not lightness.
	if acc := relLuminance(styleHex(th.accent)); acc > lum["text"] {
		t.Errorf("the accent is brighter than the header text (%.4f vs %.4f); the ladder above assumes it is not", acc, lum["text"])
	}
}

// TestFreshTokensGlowAtTheWriteHead is the glow contract, read off the bands
// the renderer actually paints with.
//
// The bands are checked rather than the pixels because a band is what the
// contract is about: a rune's shade is a function of how long ago its token
// arrived, and a frame can only show the answer, never the arithmetic. The
// frame is checked too, at the end, for the one thing a band cannot say — that
// a finished run has no glow anywhere on it.
func TestFreshTokensGlowAtTheWriteHead(t *testing.T) {
	const width = 48
	tp := ExampleTapeN(4)
	m := ModelAt(tp, midRun)
	rate := tp.Summary.Aggregate.PerStreamPredictedPerSecond
	// One or two tokens at the example's rate, plus the one that is arriving.
	maxFresh := int(math.Ceil(glowFresh.Seconds()*rate)) + 1

	live := 0
	for _, s := range m.Streams {
		if s.Done || len(s.Tokens) == 0 {
			continue
		}
		live++
		lines := streamTextLines(s, width)
		bands := bodyBands(s, lines, midRun)

		// (a) The freshest shade is one run, and it ends where the text ends:
		// the glow is a write head, not a highlight somewhere in the middle.
		var flat []tokenBand
		for i, bl := range lines {
			if bl.marker {
				continue
			}
			for _, sg := range bands[i] {
				for range sg.text {
					flat = append(flat, sg.band)
				}
			}
		}
		if len(flat) == 0 {
			t.Fatalf("stream %d is streaming and drew no text", s.Index)
		}
		if flat[len(flat)-1] != bandFresh {
			t.Errorf("stream %d: the last rune on screen is not in the freshest band; the newest token is %v old",
				s.Index, midRun-s.Tokens[len(s.Tokens)-1].T)
		}
		runs := 0
		for i, b := range flat {
			if b == bandFresh && (i == 0 || flat[i-1] != bandFresh) {
				runs++
			}
		}
		if runs != 1 {
			t.Errorf("stream %d: the freshest shade appears in %d separate runs, want one at the write head", s.Index, runs)
		}

		// (b) The glow covers the tokens of the last glowFresh and no more.
		fresh := 0
		for _, tk := range s.Tokens {
			if midRun-tk.T < glowFresh {
				fresh++
			}
		}
		if fresh > maxFresh {
			t.Errorf("stream %d: %d tokens are in the freshest band, want at most %d at %.1f tok/s",
				s.Index, fresh, maxFresh, rate)
		}
		if fresh == 0 {
			t.Errorf("stream %d: nothing is in the freshest band, so the tile carries no sense of speed", s.Index)
		}

		// (c) Nothing older than glowSettled is lit. The boundary is checked
		// from the tokens rather than from the bands, so the two derivations
		// have to agree.
		midStart, freshStart := bandStarts(s, midRun)
		off := 0
		for _, tk := range s.Tokens {
			age := midRun - tk.T
			lit := off >= midStart
			if want := age < glowSettled; lit != want {
				t.Errorf("stream %d: a token %v old is lit=%v at rune %d (mid band starts at %d)",
					s.Index, age.Round(time.Millisecond), lit, off, midStart)
				break
			}
			off += len([]rune(tk.Text))
		}
		if freshStart < midStart {
			t.Errorf("stream %d: the freshest band starts at %d, before the middle one at %d", s.Index, freshStart, midStart)
		}

		// (d) And the tail is short: a glow that ran past a line of wrapped
		// text would be a lit paragraph again.
		glowing := 0
		for _, b := range flat {
			if b != bandSettled {
				glowing++
			}
		}
		if glowing > width {
			t.Errorf("stream %d: %d runes are glowing, which is more than the %d-column line they are drawn on", s.Index, glowing, width)
		}
	}
	if live == 0 {
		t.Fatalf("no stream is still generating at %v; this test measured nothing", midRun)
	}

	// (e) A finished run does not shimmer. Neither glow shade may appear
	// anywhere on the done frame — that frame is what the card is laid over,
	// and a card that flickers is a card nobody trusts.
	done := ModelAt(tp, doneAt)
	done.Theme = ColourTheme()
	rows := parseFrame(View(done, doneAt, 120, 36), 120, 36)
	th := ColourTheme()
	for _, glow := range []struct {
		name string
		st   lipgloss.Style
	}{{"textMid", th.textMid}, {"dimMid", th.dimMid}} {
		hex := styleHex(glow.st)
		for y, row := range rows {
			for x, c := range row {
				if c.fg == hex && c.r != 0 && c.r != ' ' {
					t.Fatalf("the finished run still glows: %q at row %d col %d wears %s", c.r, y, x, glow.name)
				}
			}
		}
	}
}

// TestBodyTextSitsBelowTheHeaderTone is the body-tone contract read off the
// frame rather than off the bands (TTP-28, lead 2026-09-13).
//
// The band test above proves the renderer computes the right shade for each
// rune. This one proves the shade reaches the screen and nothing else on the
// tile side of the divider is wearing the header's tone: a tile body may use
// th.text only where a token has just landed, and everywhere else it sits at
// textMuted or below.
func TestBodyTextSitsBelowTheHeaderTone(t *testing.T) {
	const w, h = 120, 36
	tp := ExampleTapeN(4)
	m := ModelAt(tp, midRun)
	m.Theme = ColourTheme()
	rows := parseFrame(View(m, midRun, w, h), w, h)

	// Everything the glow is allowed to light, as text: the freshest band of
	// every stream on the frame. A run of header-toned cells in a tile body
	// has to come from here.
	fresh := map[string]bool{}
	for _, s := range m.Streams {
		lines := streamTextLines(s, 48)
		for i, bl := range bodyBands(s, lines, midRun) {
			if lines[i].marker || lines[i].reasoning {
				continue
			}
			for _, sg := range bl {
				if sg.band == bandFresh {
					for _, word := range strings.Fields(sg.text) {
						fresh[word] = true
					}
				}
			}
		}
	}
	if len(fresh) == 0 {
		t.Fatal("no stream has a freshest band at the golden instant; this test would pass vacuously")
	}

	rightStart := rightPaneStart(rows, w)
	hex := styleHex(ColourTheme().text)
	for y, row := range rows {
		if y == 0 {
			continue // the title bar, which names the model in the header tone
		}
		// A tile header row: "stream 3/4 · thinking". The header keeps th.text
		// by contract, and it is the tone the body is measured against.
		if strings.HasPrefix(segmentAt(row, 2), "stream ") {
			continue
		}
		for _, rn := range accentRunsOf(row, hex) {
			if rn.from >= rightStart {
				continue // the right pane's values wear th.text by contract
			}
			for _, word := range strings.Fields(rn.text) {
				if !fresh[word] {
					t.Errorf("%q at row %d col %d wears the header tone inside a tile body, and it is not a token that just landed\n%s",
						rn.text, y, rn.from, rowContext(rows, y))
					break
				}
			}
		}
	}
}

// rightPaneStart is the column the right pane's content begins at: one past the
// last tile divider on the frame. Taken from the frame rather than recomputed
// from the geometry, so a layout change moves it without the test noticing.
func rightPaneStart(rows [][]pcell, w int) int {
	at := 0
	for _, row := range rows {
		for x := 0; x < w-1; x++ {
			if row[x].r == '│' && x > at {
				at = x
			}
		}
	}
	return at + 1
}

// accentRunsOf returns the maximal runs of visible cells on a row that wear one
// exact foreground.
//
// Unlike accentRuns it walks through the trailing half of a wide rune — a cell
// with a zero rune and the same colour — instead of ending the run there. The
// digit gate never needs that, because a figure is narrow; this one reads
// Hangul, where ending a run at every syllable would cut the words apart and
// no word would ever match.
func accentRunsOf(row []pcell, hex string) []run {
	var out []run
	for x := 0; x < len(row); {
		if row[x].fg != hex || row[x].r == 0 || row[x].r == ' ' {
			x++
			continue
		}
		var b strings.Builder
		from := x
		for x < len(row) && row[x].fg == hex && row[x].r != ' ' {
			if row[x].r != 0 {
				b.WriteRune(row[x].r)
			}
			x++
		}
		out = append(out, run{from: from, to: x, text: b.String()})
	}
	return out
}

// The card screen's own emphasis contract (TTP-42, lead 2026-09-13).
//
// cardScreen painted every line of the text card in th.text, so the MEMORY
// row's [██████████] gauges — twenty solid cells of the brightest tone on the
// screen — were the first thing the eye found, and the box-drawing frame
// around the card was as bright as the figures inside it. That frame is the
// clip's final image, the one a reader looks at longest, and it was the one
// region that never got the treatment the rest of the screen has had since
// TTP-28: shapes are demoted, chrome is dim, and the figures are what is left
// lit.
//
// The gate reads the frame by rune class, which is what paintCardLine keys
// off: a gauge's fill wears the muted accent the placement bars wear, its
// track wears the dark fill, the frame wears dim, and nothing in those three
// classes wears th.text. The counters are the non-vacuity half — the example's
// gauges are nearly full, so the ░ class is only on the frame at all because
// the model below makes one gauge partial.
func TestCardScreenPaintsByRole(t *testing.T) {
	const w, h = 120, 36
	th := ColourTheme()
	m := ModelAt(ExampleTapeN(4), doneAt)
	m.Mode = ModeCard
	m.Theme = th
	m.TapePath = "~/.toktape/runs/" + m.Summary.ID + ".tape"
	// The example ends with both cards' VRAM nearly full, so every gauge cell
	// is a fill and the track class would go unchecked. Half of one device's
	// bytes puts both glyphs on the frame.
	if len(m.Summary.GPUsAtEnd) == 0 {
		t.Fatal("the example has no GPU at the end; this test would measure no gauge")
	}
	// Copied before it is edited: Summary is a value but its slice is not, and
	// a halved device leaking into another test's fixture would be a failure
	// nobody could read.
	m.Summary.GPUsAtEnd = append([]tape.GPUSample(nil), m.Summary.GPUsAtEnd...)
	m.Summary.GPUsAtEnd[0].UsedBytes /= 2

	rows := parseFrame(View(m, doneAt, w, h), w, h)
	text := styleHex(th.text)
	want := map[rune]struct {
		hex  string
		what string
	}{
		'█': {styleHex(th.accentMuted), "a gauge fill"},
		'░': {styleHex(th.darkFill), "a gauge track"},
	}
	for _, r := range []rune("┌┐└┘├┤┬┴┼─│") {
		want[r] = struct {
			hex  string
			what string
		}{styleHex(th.dim), "the card's frame"}
	}
	seen := map[rune]int{}
	for y, row := range rows {
		for x, c := range row {
			w, ok := want[c.r]
			if !ok {
				continue
			}
			seen[c.r]++
			if c.fg == text {
				t.Errorf("%s (%q at row %d col %d) wears the card's text tone; shapes and chrome are demoted\n%s",
					w.what, c.r, y, x, rowContext(rows, y))
				continue
			}
			if c.fg != w.hex {
				t.Errorf("%s (%q at row %d col %d) is %s, want %s", w.what, c.r, y, x, c.fg, w.hex)
			}
		}
	}
	for _, r := range []rune{'█', '░', '─', '│'} {
		if seen[r] == 0 {
			t.Errorf("no %q on the card frame; the gate checked nothing for that class", r)
		}
	}
}

// TestTheWriteHeadCarriesAFill is TTP-47: the answer's freshest band is the
// one body style that paints a fill behind the text.
//
// The glow contract already had a ladder (TestTheBodyIsOneToneDown), and the
// ladder alone did not reach the user: "마지막 출력토큰의 하일라이팅이 아직도
// 제대로 안보인다" (2026-09-14). Lowering the body another stop was the other
// option they named and it is the worse one — the body/header band is pinned
// at 0.44–0.54 and the answer ladder would collapse onto the reasoning one —
// so the step at the write head is spent on a fill instead, which is the one
// axis the body was not using.
//
// The contract is read off bodyStyle rather than off a frame because a fill is
// a decision about a band, and the resource graph's track carries the same
// colour for an unrelated reason.
func TestTheWriteHeadCarriesAFill(t *testing.T) {
	th := ColourTheme()
	answer := bodyLine{}
	if bg := styleBG(bodyStyle(th, answer, bandFresh)); bg == "" {
		t.Errorf("the freshest answer band paints no fill; the user cannot see where the write head is")
	}
	for _, c := range []struct {
		name string
		bl   bodyLine
		band tokenBand
	}{
		{"a settled answer", answer, bandSettled},
		{"an answer 150–500 ms old", answer, bandMid},
		{"fresh reasoning", bodyLine{reasoning: true}, bandFresh},
		{"settled reasoning", bodyLine{reasoning: true}, bandSettled},
		{"the answer marker", bodyLine{marker: true}, bandFresh},
	} {
		if bg := styleBG(bodyStyle(th, c.bl, c.band)); bg != "" {
			t.Errorf("%s paints a fill (%s); only the write head may", c.name, bg)
		}
	}
	// The fill is the dark the bars and the graph tracks already use, not a
	// new colour: one accent hue at the bottom of its lightness range.
	if got, want := styleBG(bodyStyle(th, answer, bandFresh)), styleHex(th.darkFill); got != want {
		t.Errorf("the write head's fill is %s, want the theme's dark fill %s", got, want)
	}
}
