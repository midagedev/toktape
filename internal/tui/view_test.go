package tui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

var update = flag.Bool("update", false, "rewrite the golden frames in testdata")

// midRun and doneAt are the two interesting offsets into ExampleTape: one
// while every stream is decoding, one after the last token.
const (
	midRun = 1500 * time.Millisecond
	doneAt = 8 * time.Second
)

func goldenModel(t *testing.T, at time.Duration) Model {
	t.Helper()
	m := ModelAt(ExampleTape(), at)
	m.TapePath = "~/.toktape/runs/20260913-150210-qwen3.5-35b-a3b.tape"
	return m
}

func TestViewGolden(t *testing.T) {
	sizes := []struct{ w, h int }{{100, 30}, {140, 40}}
	offsets := []struct {
		name string
		at   time.Duration
	}{
		{"t0", 0},
		{"mid", midRun},
		{"done", doneAt},
	}
	for _, sz := range sizes {
		for _, off := range offsets {
			name := fmt.Sprintf("%dx%d-%s", sz.w, sz.h, off.name)
			t.Run(name, func(t *testing.T) {
				got := View(goldenModel(t, off.at), off.at, sz.w, sz.h)
				checkFrame(t, got, sz.w, sz.h)
				compareGolden(t, "view-"+name+".txt", got)
			})
		}
	}
}

// TestViewGeometry pins the contract every frame has to meet at every size the
// layout claims to support: exactly h lines, every one exactly w columns, and
// the vertical borders in the same column on every body row.
func TestViewGeometry(t *testing.T) {
	sizes := []struct{ w, h int }{
		{100, 30}, {101, 31}, {120, 36}, {140, 40}, {160, 50}, {199, 33},
	}
	states := []struct {
		name string
		at   time.Duration
		mode Mode
	}{
		{"start", 0, ModeLive},
		{"mid", midRun, ModeLive},
		{"done", doneAt, ModeLive},
		{"prompt", midRun, ModePrompt},
		{"card", doneAt, ModeCard},
	}
	for _, sz := range sizes {
		for _, st := range states {
			t.Run(fmt.Sprintf("%dx%d-%s", sz.w, sz.h, st.name), func(t *testing.T) {
				m := goldenModel(t, st.at)
				m.Mode = st.mode
				checkFrame(t, View(m, st.at, sz.w, sz.h), sz.w, sz.h)
			})
		}
	}
}

// TestBorderColumnIsConstant is the Hangul test from the track contract: an
// answer written in Korean must not move the pane border by a single column.
// Hangul syllables are two columns wide, and measuring them as one is the
// mistake that shifted every coloured line fifteen columns in the prototype
// (handover lesson 5).
func TestBorderColumnIsConstant(t *testing.T) {
	tp := ExampleTape()
	// Force every stream to answer in Korean, including the punctuation and
	// the Latin fragments a real answer mixes in.
	for i := range tp.Requests {
		for j := range tp.Requests[i].Tokens {
			tp.Requests[i].Tokens[j].Text = hangulToken(j)
		}
	}
	m := ModelAt(tp, midRun)
	frame := View(m, midRun, 100, 30)
	checkFrame(t, frame, 100, 30)

	lines := strings.Split(frame, "\n")
	// Body rows only: the top border, the divider and the bottom border have
	// their own junction characters.
	want := -1
	for i := 1; i < len(lines)-3; i++ {
		col := separatorColumn(lines[i])
		if col < 0 {
			t.Fatalf("body row %d has no pane separator: %q", i, lines[i])
		}
		if want < 0 {
			want = col
		}
		if col != want {
			t.Errorf("body row %d: separator at column %d, want %d\n%s", i, col, want, lines[i])
		}
	}
	if want < 0 {
		t.Fatal("no body rows found")
	}
}

// hangulToken returns a token of Korean text, with one Latin word mixed in
// every few tokens the way a real answer about llama.cpp would.
func hangulToken(i int) string {
	words := []string{
		" 페이지", " 폴트가", " 토큰마다", " NVMe에서", " 전문가", " 텐서를",
		" 읽어", " 오면서", " 생기는", " 지연이다.", " PCIe", " 대역폭이",
	}
	return words[i%len(words)]
}

// TestColourMatchesPlain is the invariant that makes the goldens meaningful:
// the coloured rendering must be the plain one with escape sequences added,
// never with different text or different widths. It is also what lets the lead
// read a plain frame and trust that the coloured capture says the same thing.
func TestColourMatchesPlain(t *testing.T) {
	for _, at := range []time.Duration{0, midRun, doneAt} {
		for _, mode := range []Mode{ModeLive, ModePrompt, ModeCard} {
			m := goldenModel(t, at)
			m.Mode = mode
			plain := View(m, at, 120, 36)
			m.Theme = ColourTheme()
			coloured := View(m, at, 120, 36)
			if coloured == plain {
				t.Fatalf("at=%v mode=%d: colour theme emitted no escapes", at, mode)
			}
			if got := card.StripANSI(coloured); got != plain {
				t.Errorf("at=%v mode=%d: stripping the palette does not reproduce the plain frame", at, mode)
				diffLines(t, plain, got)
			}
		}
	}
}

// TestViewIsPure pins that a frame depends on nothing but its arguments: the
// same model and the same clip time render byte for byte the same, whatever
// the wall clock is doing. Replay and the GIF track rest on this.
func TestViewIsPure(t *testing.T) {
	m := goldenModel(t, midRun)
	first := View(m, midRun, 120, 36)
	time.Sleep(5 * time.Millisecond)
	if second := View(m, midRun, 120, 36); second != first {
		t.Error("two renders of the same model and clip time differ")
	}
}

// TestViewAnimates is the other half of purity: the frame has to actually move
// when only t changes. A screen that is pure but static would pass every other
// test in this file and fail the whole point of the track.
//
// It renders in colour, because two of the four moving parts — the header
// shimmer and the cursor's breath — are changes of colour alone and leave no
// trace in a plain frame.
func TestViewAnimates(t *testing.T) {
	th := ColourTheme()
	cases := []struct {
		what string
		at   time.Duration // where in the run to look
		dt   time.Duration // how far to step
	}{
		// Every stream is in prefill at t=0, so the spinner is on screen.
		{"the prefill spinner", 0, spinFrame},
		{"the header shimmer", 0, shimmerDur / 8},
		// Mid-run there is a cursor to breathe and bars easing toward a sample.
		{"the stream cursor", midRun, breathDur / 4},
		{"the eased bars", 1250 * time.Millisecond, easeDur / 3},
	}
	for _, c := range cases {
		m := goldenModel(t, c.at)
		m.Theme = th
		if View(m, c.at+c.dt, 120, 36) == View(m, c.at, 120, 36) {
			t.Errorf("%s did not move over %v", c.what, c.dt)
		}
	}
}

// TestTooSmallStillFits: a terminal below the minimum gets a message, not a
// broken frame, and the message still occupies exactly the space it was given.
func TestTooSmallStillFits(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{40, 10}, {99, 30}, {100, 29}} {
		got := View(goldenModel(t, midRun), midRun, sz.w, sz.h)
		checkFrame(t, got, sz.w, sz.h)
		if !strings.Contains(got, "100×30") {
			t.Errorf("%dx%d: no size hint in the frame", sz.w, sz.h)
		}
	}
}

// TestUnknownsPrintQuestionMark: a summary with nothing observed must print
// "?" everywhere rather than a plausible zero (CLAUDE.md).
func TestUnknownsPrintQuestionMark(t *testing.T) {
	m := ModelAt(&tape.Tape{Schema: tape.SchemaVersion}, 0)
	frame := View(m, 0, 120, 36)
	checkFrame(t, frame, 120, 36)
	if strings.Contains(frame, "0.0G") {
		t.Error("an unobserved byte figure printed as 0.0G instead of ?")
	}
	if !strings.Contains(frame, "?") {
		t.Error("an all-unknown run printed no ? at all")
	}
	if !strings.Contains(frame, "waiting for the first request") {
		t.Error("a run with no streams did not say it was waiting")
	}
}

// TestCardModeNeedsDone: "c" is only meaningful once there is a card to show,
// but View must still render a frame if a caller sets the mode early.
func TestCardModeRenders(t *testing.T) {
	m := goldenModel(t, doneAt)
	m.Mode = ModeCard
	frame := View(m, doneAt, 120, 36)
	checkFrame(t, frame, 120, 36)
	if !strings.Contains(frame, "VERIFIED") && !strings.Contains(frame, "toktape · github.com") {
		t.Error("card mode did not render the card footer")
	}
	if !strings.Contains(frame, m.TapePath) {
		t.Error("card mode did not name the saved tape")
	}
}

// checkFrame asserts the frame is exactly h lines of exactly w columns.
func checkFrame(t *testing.T, frame string, w, h int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if len(lines) != h {
		t.Fatalf("frame has %d rows, want %d", len(lines), h)
	}
	for i, line := range lines {
		if got := card.Width(line); got != w {
			t.Errorf("row %d is %d columns, want %d: %q", i, got, w, line)
		}
	}
}

// separatorColumn returns the display column of the pane separator: the second
// "│" on a body row, counting in display columns so a Hangul answer shifts it
// if and only if the width maths is wrong.
func separatorColumn(line string) int {
	plain := card.StripANSI(line)
	seen := 0
	col := 0
	for _, r := range plain {
		if r == '│' {
			seen++
			if seen == 2 {
				return col
			}
		}
		col += runeWidth(r)
	}
	return -1
}

func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if strings.TrimRight(string(want), "\n") != got {
		diffLines(t, strings.TrimRight(string(want), "\n"), got)
		t.Errorf("frame differs from %s", path)
	}
}

// diffLines reports the first few differing lines rather than dumping two
// full frames into the test log.
func diffLines(t *testing.T, want, got string) {
	t.Helper()
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	shown := 0
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var a, b string
		if i < len(wl) {
			a = wl[i]
		}
		if i < len(gl) {
			b = gl[i]
		}
		if a == b {
			continue
		}
		t.Logf("row %d:\n want %q\n  got %q", i, a, b)
		if shown++; shown >= 5 {
			t.Logf("… further differences suppressed")
			return
		}
	}
}

// sgrPrefix returns the escape sequence a style emits, by painting a marker
// and taking everything before it. Building the expectation from the theme
// rather than from a hardcoded escape means the palette can be retuned without
// rewriting the hierarchy test.
func sgrPrefix(th Theme, st lipgloss.Style) string {
	painted := th.paint(st, "\x00")
	return painted[:strings.Index(painted, "\x00")]
}

// TestColourHierarchy pins the rule the palette rests on: dim is for labels and
// chrome only. Content wears the text colour, and the three figures the screen
// exists to show — plus the stream that is currently talking — wear the accent
// in bold. A round that quietly dimmed the answer text again would pass every
// other test in this file.
func TestColourHierarchy(t *testing.T) {
	th := ColourTheme()
	m := goldenModel(t, midRun)
	m.Theme = th
	frame := View(m, midRun, 120, 36)

	dim := sgrPrefix(th, th.dim)
	text := sgrPrefix(th, th.text)
	accentBold := sgrPrefix(th, th.accentBold)

	mustBe := []struct {
		style, prefix, what string
	}{
		{accentBold, "PLACEMENT", "a section title"},
		{accentBold, "MEMORY", "a section title"},
		{accentBold, "SPEED", "a section title"},
		{accentBold, "HOST", "a section title"},
		{text, "The page fault spike", "answer text"},
		{dim, "never loaded", "a label"},
		{dim, "virt", "a label"},
		{dim, "ttft", "a label"},
		{dim, "load", "a label"},
	}
	for _, c := range mustBe {
		if !strings.Contains(frame, c.style+c.prefix) {
			t.Errorf("%s %q is not in its expected colour", c.what, c.prefix)
		}
	}

	mustNotBe := []struct{ style, prefix, what string }{
		{dim, "The page fault spike", "answer text"},
		{dim, "PLACEMENT", "a section title"},
		{dim, "stream 1", "a stream header"},
	}
	for _, c := range mustNotBe {
		if strings.Contains(frame, c.style+c.prefix) {
			t.Errorf("%s %q is dim; dim is for labels and chrome only", c.what, c.prefix)
		}
	}

	// Exactly one stream is the active one, and it is the only stream header
	// in bold: two bold headers would mean the eye has nowhere to land.
	bold := 0
	for i := 1; i <= len(m.Streams); i++ {
		if strings.Contains(frame, accentBold+fmt.Sprintf("stream %d", i)) {
			bold++
		}
	}
	if bold != 1 {
		t.Errorf("%d stream headers are accent bold, want exactly 1", bold)
	}
}

// TestBarsUseOneGlyph: the filled and empty halves of a bar are the same solid
// block and differ only in colour, so a hatch glyph can never creep back in.
func TestBarsUseOneGlyph(t *testing.T) {
	th := ColourTheme()
	m := goldenModel(t, midRun)
	m.Theme = th
	frame := View(m, midRun, 120, 36)
	for _, bad := range []string{"░", "▓", "▒"} {
		if strings.Contains(frame, bad) {
			t.Errorf("the frame still draws a bar with the hatch glyph %q", bad)
		}
	}
	if !strings.Contains(frame, sgrPrefix(th, th.darkFill)+"█") {
		t.Error("no bar remainder is drawn in the dark fill colour")
	}
}

// TestPrefillFrameShowsProgress: the opening frame is the one a reader sees
// first in a clip, and "waiting for the first token" says nothing about what
// the server is doing. Every stream must show the spinner and a prompt-progress
// bar from the very first frame, which means ExampleTape has to carry a
// return_progress row stamped at zero.
func TestPrefillFrameShowsProgress(t *testing.T) {
	m := goldenModel(t, 0)
	frame := View(m, 0, 120, 36)

	if strings.Contains(frame, "waiting for the first token") {
		t.Error("the opening frame still falls back to the waiting message")
	}
	bars := strings.Count(frame, "prefill ")
	if bars == 0 {
		t.Fatal("no prefill progress bar on the opening frame")
	}
	for i, s := range m.Streams {
		if len(s.Progress) == 0 {
			t.Errorf("stream %d has no progress row at t=0", i)
		}
		if len(s.Tokens) != 0 {
			t.Errorf("stream %d already has tokens at t=0", i)
		}
	}
	// The counts beside the bar are the ones the tape recorded, not a guess.
	p := m.Streams[0].Progress[0]
	if !strings.Contains(frame, fmt.Sprintf("%d/%d · cache %d", p.Processed, p.Total, p.Cache)) {
		t.Errorf("the bar does not print the recorded counts %d/%d cache %d",
			p.Processed, p.Total, p.Cache)
	}
	if strings.Contains(frame, "░") || strings.Contains(frame, "▓") {
		t.Error("the prefill bar uses a hatch glyph")
	}

	// The bar has to show all three shades, or it is not saying anything the
	// "176/512 · cache 128" beside it does not already say.
	th := ColourTheme()
	m.Theme = th
	coloured := View(m, 0, 120, 36)
	for _, part := range []struct {
		st   lipgloss.Style
		what string
	}{
		{th.accentLow, "the prefix the cache already held"},
		{th.accent, "the part processed since"},
		{th.darkFill, "the part still to do"},
	} {
		if !strings.Contains(coloured, sgrPrefix(th, part.st)+"█") {
			t.Errorf("the prefill bar does not draw %s", part.what)
		}
	}
}

// TestDoneStateFillsThePane: once nothing is moving, empty rows below the last
// answer are wasted screen. The rows are shared out across the streams, which
// is the frame a reader lands on at the end of a clip.
func TestDoneStateFillsThePane(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{100, 30}, {120, 36}, {140, 40}, {160, 50}} {
		m := goldenModel(t, doneAt)
		if !m.Done {
			t.Fatal("the fixture is not finished at the done offset")
		}
		bodyH := sz.h - chromeH
		lines := leftPane(m, PlainTheme(), doneAt, sz.w-33-2, bodyH)

		blanks := 0
		for i := len(lines) - 1; i >= 0 && strings.TrimSpace(lines[i]) == ""; i-- {
			blanks++
		}
		// The done footer is a blank spacer and the saved-tape line, so one
		// trailing blank belongs to it; anything beyond that is waste.
		if blanks > 0 {
			t.Errorf("%dx%d: %d blank rows at the bottom of the answer pane", sz.w, sz.h, blanks)
		}

		n := len(m.Streams)
		_, budget, gap := streamLayout(n, bodyH-2, true)
		if gap != 0 {
			t.Errorf("%dx%d: the done layout still spends rows on gaps", sz.w, sz.h)
		}
		lo, hi := budget[0], budget[0]
		for _, k := range budget {
			lo, hi = min(lo, k), max(hi, k)
		}
		if hi-lo > 1 {
			t.Errorf("%dx%d: line budgets range %d..%d; the remainder is not shared evenly",
				sz.w, sz.h, lo, hi)
		}
		if used := n + sum(budget); used != bodyH-2 {
			t.Errorf("%dx%d: the layout uses %d of %d rows", sz.w, sz.h, used, bodyH-2)
		}
	}
}

func sum(v []int) int {
	n := 0
	for _, x := range v {
		n += x
	}
	return n
}
