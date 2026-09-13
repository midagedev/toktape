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
//
// 2026-09-13 (TTP-28): the run is twenty-five seconds of streaming rather than
// seven, so both moved with it — midRun is the fixture's own ExampleMidRun,
// the instant the goldens and the captures are framed at, and doneAt is past
// the last token of the eight-stream form, whose reasoning-only stream spends
// all 320 of its tokens at the eight-way rate.
const (
	midRun = ExampleMidRun
	doneAt = 38 * time.Second
)

func goldenModel(t *testing.T, at time.Duration) Model {
	t.Helper()
	m := ModelAt(ExampleTape(), at)
	m.TapePath = "~/.toktape/runs/20260913-150210-qwen3.5-35b-a3b.tape"
	return m
}

// TestViewGolden pins whole frames of the eight-stream example at the two
// sizes the layout is designed around.
//
// 2026-09-13: these six frames were re-baselined. The stacked per-stream list
// they used to show was replaced by the tile grid (user decision, TTP-24 spec
// change: "리스트 레이아웃 아예 버리고 싶어, 다 카드로 하고"). The list goldens are
// gone rather than loosened — no assertion here was weakened, the layout under
// them is a different one.
//
// 2026-09-13, later the same day: re-baselined again, for the stat line. These
// are tile frames too, so they moved with TestTileGolden: the rate and the
// TTFT left the header, which now carries the state and the prefill bar, for a
// row of their own under it (user decision — see TestTileGolden for the
// wording). Again a different layout rather than a loosened assertion.
//
// 2026-09-13 TTP-28: re-baselined once more. The example is now a dense
// Llama 3.3 70B on two 3090s running 320-token answers, so every figure in
// these frames moved, and the emphasis contract demoted the sparklines, the
// section titles, the bars and the tile headers out of the accent (see
// TestOnlyTheRateIsAccent). A third different set of frames, not a loosened
// assertion: the gates above them were added, not relaxed.
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

// separatorColumn returns the display column of the pane separator, counting
// in display columns so a Hangul answer shifts it if and only if the width
// maths is wrong.
//
// It is the second-to-last vertical mark on the row: the answer pane now draws
// rules of its own between the tile columns, so "the second │" is one of those
// rather than the separator, while the last mark is always the frame's own
// right border. A rule row carries ┤ there instead of │, which is the same
// column and the same claim.
//
// 2026-09-13: rewritten when the list layout was replaced by the tile grid.
// The assertion is unchanged — the border may not move by one column — only
// the way the border is located.
func separatorColumn(line string) int {
	plain := card.StripANSI(line)
	var cols []int
	col := 0
	for _, r := range plain {
		if r == '│' || r == '┤' {
			cols = append(cols, col)
		}
		col += runeWidth(r)
	}
	if len(cols) < 2 {
		return -1
	}
	return cols[len(cols)-2]
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

// TestColourHierarchy pins the rule the palette rests on: content wears the
// text colour, labels and chrome wear dim, and the accent is spent on the
// figures the screen exists to show. A round that quietly dimmed the answer
// text again would pass every other test in this file.
//
// 2026-09-13 (TTP-28, user: "화면에 너무 많은 요소들이 강조되어 있다"): the section
// titles and the active stream's header used to be accent bold, and are now
// dim and plain respectively. Six lit things on one screen is none. What stayed
// accent is pinned by TestOnlyTheRateIsAccent and TestAccentIsReserved; what
// this test still owns is the other half of the rule, that content is not dim.
//
// 2026-09-13 TTP-28, later the same day (user: "토큰 내용 자체는 한 톤 내리는 게
// 맞겠어"): the answer's settled shade is textMuted, one stop under the header,
// and the header keeps text. The rule is unchanged and the shade it names moved
// — content is still not dim, and the ladder itself is pinned by
// TestBodyToneLadderDescends.
func TestColourHierarchy(t *testing.T) {
	th := ColourTheme()
	m := goldenModel(t, midRun)
	m.Theme = th
	frame := View(m, midRun, 120, 36)

	dim := sgrPrefix(th, th.dim)
	text := sgrPrefix(th, th.text)
	body := sgrPrefix(th, th.textMuted)
	accentBold := sgrPrefix(th, th.accentBold)

	answer := firstAnswerWord(frame)
	mustBe := []struct {
		style, prefix, what string
	}{
		{dim, "PLACEMENT", "a section title"},
		{dim, "MEMORY", "a section title"},
		{dim, "SPEED", "a section title"},
		{dim, "HOST", "a section title"},
		{body, answer, "answer text"},
		{text, "stream 1", "a tile header"},
		{dim, "maj/tok", "a label"},
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
		{dim, answer, "answer text"},
		// Settled answer text does not wear the header's tone: that shade is
		// reserved for the tile header and for the token that just landed.
		{text, answer, "settled answer text"},
		{accentBold, "PLACEMENT", "a section title"},
		{dim, "stream 1", "a stream header"},
	}
	for _, c := range mustNotBe {
		if strings.Contains(frame, c.style+c.prefix) {
			t.Errorf("%s %q is dim; dim is for labels and chrome only", c.what, c.prefix)
		}
	}

	// No stream header is lit at all. The one that is talking is marked beside
	// its text — an accent gutter and a breathing cursor — rather than by
	// lighting the word above its rate (TTP-28).
	bold := 0
	for i := 1; i <= len(m.Streams); i++ {
		if strings.Contains(frame, accentBold+fmt.Sprintf("stream %d", i)) {
			bold++
		}
	}
	if bold != 0 {
		t.Errorf("%d stream headers are accent bold, want none", bold)
	}
}

// firstAnswerWord is a word of answer text taken off the frame itself: the
// first one on a tile body row, which the gutter glyph marks. Reading it off
// the frame rather than hard-coding a sentence means the example's material can
// be rewritten without this test going quietly vacuous.
func firstAnswerWord(frame string) string {
	for _, line := range strings.Split(card.StripANSI(frame), "\n") {
		_, rest, ok := strings.Cut(line, "▏ ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 2 {
			continue
		}
		return strings.Join(fields[:2], " ")
	}
	return ""
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
// first in a clip, and it has to say what the server is doing. Every stream
// shows a prompt-progress bar and a spinner from the very first frame, which
// means ExampleTape has to carry a return_progress row stamped at zero.
//
// 2026-09-13: the bar moved from the body of the tile to the right of its
// header, and the body kept the spinner (stat-line track, user decision). The
// assertions moved with it — the frame must still carry the bar, the recorded
// counts and all three shades — and one was added: the body says what the
// stream is waiting for, which is the row the bar used to occupy.
func TestPrefillFrameShowsProgress(t *testing.T) {
	m := goldenModel(t, 0)
	frame := View(m, 0, 120, 36)

	bars := strings.Count(frame, "prefill ")
	if bars == 0 {
		t.Fatal("no prefill progress bar on the opening frame")
	}
	if !strings.Contains(frame, "waiting for the first token") {
		t.Error("the body of a prefilling tile says nothing about what it is waiting for")
	}
	if !strings.ContainsAny(frame, string(spinnerFrames)) {
		t.Error("no spinner on the opening frame: a stream stuck in prefill has to read as alive")
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
	//
	// What the bar reports is how much of the prompt is done, which is
	// processed against total: llama-server counts the cached prefix inside
	// processed, since it fills the field from the slot's prompt buffer and
	// that buffer starts at the cache length (upstream server-context.cpp; its
	// README puts the overall progress at processed/total and the timed one at
	// (processed-cache)/(total-cache), checked 2026-09-13). A tile is a
	// fraction of the pane, so the cache figure is the part that does not fit
	// there; a header with the room prints all of it.
	p := m.Streams[0].Progress[0]
	done := p.Processed
	if !strings.Contains(frame, fmt.Sprintf("%d/%d", done, p.Total)) {
		t.Errorf("the bar does not print the recorded counts %d/%d", done, p.Total)
	}
	wide := streamHeader(m, PlainTheme(), m.Streams[0], 70, true, false)
	if !strings.Contains(wide, fmt.Sprintf("%d/%d · cache %d", done, p.Total, p.Cache)) {
		t.Errorf("a header with room dropped the cache count: %q", wide)
	}
	if strings.Contains(frame, "░") || strings.Contains(frame, "▓") {
		t.Error("the prefill bar uses a hatch glyph")
	}

	// The bar has to show all three shades, or it is not saying anything the
	// "176/512 · cache 128" beside it does not already say.
	//
	// Not on the opening frame, though: at t = 0 the request has only just
	// been accepted, so the cached prefix is known and nothing has been
	// evaluated yet, and a bar drawing a processed share there would be
	// drawing a measurement that does not exist. Three hundred milliseconds
	// in, the server is halfway through the part it has to evaluate.
	const shades = 300 * time.Millisecond
	th := ColourTheme()
	m = goldenModel(t, shades)
	m.Theme = th
	coloured := View(m, shades, 120, 36)
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
// answer are wasted screen. The grid spends every row it is given, so the only
// blank the pane may end on is the done footer's own spacer.
func TestDoneStateFillsThePane(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{100, 30}, {120, 36}, {140, 40}, {160, 50}} {
		m := goldenModel(t, doneAt)
		if !m.Done {
			t.Fatal("the fixture is not finished at the done offset")
		}
		cw, bodyH := paneGeometry(sz.w, sz.h)
		pane := leftPane(m, PlainTheme(), doneAt, cw, bodyH)
		lines := paneText(pane)
		if len(lines) != bodyH {
			t.Fatalf("%dx%d: the pane is %d rows, want %d", sz.w, sz.h, len(lines), bodyH)
		}

		blanks := 0
		for i := len(lines) - 1; i >= 0 && strings.TrimSpace(lines[i]) == ""; i-- {
			blanks++
		}
		if blanks > 0 {
			t.Errorf("%dx%d: %d blank rows at the bottom of the answer pane", sz.w, sz.h, blanks)
		}

		// The row above the saved-tape line is the grid's last tile row, and
		// that tile's own footer is the last thing in it: the grid reaches the
		// footer rather than trailing off into space.
		//
		// 2026-09-13: the footer's label is the window's mean rate rather than
		// the stream's p50, and it dropped the "tok/s" the unit was spelled
		// out in (stat-line track, user decision). The row this looks for is
		// the same row.
		last := lines[len(lines)-3]
		if !strings.Contains(last, " avg") {
			t.Errorf("%dx%d: the bottom tile row does not end on its sparkline: %q", sz.w, sz.h, last)
		}
	}
}

// paneText is the answer pane's rows as plain strings, for the tests that care
// about what it said rather than about how View joins its rules to the frame.
func paneText(p paneLayout) []string {
	out := make([]string, len(p.rows))
	for i, row := range p.rows {
		out[i] = row.text
	}
	return out
}
