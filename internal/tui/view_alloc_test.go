package tui

// Byte-identity oracle (TTP-123) and allocation recurrence gate.
//
// Clause → assertion map:
//   - "zero frame bytes change" → TestViewFramesPinned (fixtures sweep+hero ×
//     10 t-samples × 2 sizes = 40 sha256 lines in testdata/view_frames.sha256).
//     FAIL-first: golden test failing when one border character was changed,
//     then reverted (see report, 2026-09-19).
//   - "allocs/op ≤ 50% of baseline" → TestViewAllocs (AllocsPerRun ceiling).
//     FAIL-first: this test failing on the unchanged source (see report).
//   - "B/op ≤ 60% of baseline" → benchmark table in view_bench_test.go header
//     (before/after B/op); FAIL-first: the baseline B/op itself exceeds the
//     60% target by construction (see report).

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/midagedev/toktape/internal/tape"
)

const viewGoldenPath = "testdata/view_frames.sha256"

// sweepRunEnd is render.RunEnd's rule (max over requests of
// StartedAt+last-token-T) without importing internal/render, which would be
// an import cycle from this package's tests.
func sweepRunEnd(tp *tape.Tape) time.Duration {
	var end time.Duration
	for _, req := range tp.Requests {
		if n := len(req.Tokens); n > 0 {
			if e := req.StartedAt + req.Tokens[n-1].T; e > end {
				end = e
			}
		}
	}
	return end
}

type viewSample struct {
	fixture string
	mode    Mode
	at      time.Duration
	w, h    int
}

func loadViewTapes(t *testing.T) (sweep, hero *tape.Tape) {
	t.Helper()
	var err error
	sweep, err = tape.Read("testdata/qwen36-35b-a3b-q6k-4stream-ik-sweep-0.2.4.tape")
	if err != nil {
		t.Fatalf("read sweep tape: %v", err)
	}
	hero, err = tape.Read("../../assets/hero.tape")
	if err != nil {
		t.Fatalf("read hero tape: %v", err)
	}
	return sweep, hero
}

func viewSamples(sweep, hero *tape.Tape) []viewSample {
	var out []viewSample
	for _, fx := range []struct {
		name string
		tp   *tape.Tape
	}{
		{"sweep", sweep},
		{"hero", hero},
	} {
		end := sweepRunEnd(fx.tp)
		live := []time.Duration{0, end / 10, end / 4, end / 2, end * 3 / 4, end, end + time.Second, end + 6*time.Second}
		for _, at := range live {
			for _, size := range [][2]int{{120, 36}, {156, 38}} {
				out = append(out, viewSample{fx.name, ModeLive, at, size[0], size[1]})
			}
		}
		for _, at := range []time.Duration{end + time.Second, end + 6*time.Second} {
			for _, size := range [][2]int{{120, 36}, {156, 38}} {
				out = append(out, viewSample{fx.name, ModeCard, at, size[0], size[1]})
			}
		}
	}
	return out
}

func renderViewSample(sweep, hero *tape.Tape, s viewSample) string {
	tp := sweep
	if s.fixture == "hero" {
		tp = hero
	}
	m := ModelAt(tp, s.at)
	m.Theme = ColourTheme()
	m.Mode = s.mode
	m.Replay = true
	return View(m, s.at, s.w, s.h)
}

func sampleKey(s viewSample) string {
	mode := "live"
	if s.mode == ModeCard {
		mode = "card"
	}
	return fmt.Sprintf("%s-%s %d %d %d", s.fixture, mode, int64(s.at), s.w, s.h)
}

// TestViewFramesPinned pins every byte of 40 frames. Generate with
// VIEW_GOLDEN_UPDATE=1 from the UNCHANGED source; afterwards every run must
// match.
//
// 2026-09-21: hero re-recorded on prompts@v2 under the run plan — the 20
// hero lines were regenerated that way (the sweep fixture's 20 are byte-
// identical to before; only the hero's end-derived sample keys and frames
// moved). The regenerated hero frames were eyeballed through tuidump before
// being accepted: four streams on the new 20-second clock, ttft 8.25 s /
// 8.45 s, no place names.
func TestViewFramesPinned(t *testing.T) {
	sweep, hero := loadViewTapes(t)
	samples := viewSamples(sweep, hero)
	if os.Getenv("VIEW_GOLDEN_UPDATE") == "1" {
		var b strings.Builder
		for _, s := range samples {
			sum := sha256.Sum256([]byte(renderViewSample(sweep, hero, s)))
			fmt.Fprintf(&b, "%s %x\n", sampleKey(s), sum)
		}
		if err := os.WriteFile(viewGoldenPath, []byte(b.String()), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %d golden lines", len(samples))
		return
	}
	raw, err := os.ReadFile(viewGoldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (generate with VIEW_GOLDEN_UPDATE=1)", err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(line)
		if len(f) != 5 {
			t.Fatalf("bad golden line: %q", line)
		}
		want[strings.Join(f[:4], " ")] = f[4]
	}
	for _, s := range samples {
		frame := renderViewSample(sweep, hero, s)
		got := fmt.Sprintf("%x", sha256.Sum256([]byte(frame)))
		if want[sampleKey(s)] != got {
			t.Errorf("frame moved: %s\nwant %s got %s\n%s", sampleKey(s), want[sampleKey(s)], got, frameExcerpt(frame))
		}
	}
}

// frameExcerpt prints a mismatching frame's first lines so a failure shows
// where the bytes moved. The golden file holds hashes only, so there is no
// second frame to diff line-by-line here; diffFrames below is the helper the
// lead uses with two live renders (e.g. pre- vs post-change outputs).

// frameExcerpt renders the first 8 lines of a frame for failure output.
func frameExcerpt(frame string) string {
	lines := strings.Split(frame, "\n")
	if len(lines) > 8 {
		lines = lines[:8]
	}
	return strings.Join(lines, "\n")
}

// diffFrames shows the first differing line and column of two frames.
func diffFrames(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			xr, yr := []rune(x), []rune(y)
			col := 0
			for col < len(xr) && col < len(yr) && xr[col] == yr[col] {
				col++
			}
			return fmt.Sprintf("first diff at line %d col %d:\n got: %q\nwant: %q", i, col, y, x)
		}
	}
	return "lengths differ"
}

// TestPaintMatchesRender pins the TTP-123 self-review class "escape cache
// wrong for a non-default style": every style of ColourTheme must paint
// exactly what lipgloss Render emits, on prose, box-drawing, CJK and empty
// input. Only ColourTheme and the zero PlainTheme construct Themes (grep
// "Theme{" outside tests), so covering both covers every theme.
func TestPaintMatchesRender(t *testing.T) {
	th := ColourTheme()
	styles := []style{
		th.accent, th.warn, th.bad, th.text, th.dim,
		th.accentBold, th.darkFill,
		th.accentLow, th.accentMid, th.accentHigh, th.accentMuted,
		th.textMid, th.textMuted, th.dimMid,
		th.textFresh, th.gleamPeak,
		th.devGPU[0], th.devGPU[1], th.devGPU[2], th.devHost,
		th.codeKeyword, th.codeFunc, th.codeType, th.codeString,
		th.codeNumber, th.codeComment, th.codeVar,
		th.graphTrack, th.graphRidge, th.graphSolo,
	}
	samples := []string{"hello stream 1/4", "│─┤█▍", "한글 프롬프트", "x"}
	for i, st := range styles {
		if st.raw {
			t.Errorf("style %d falls back to Render; the fast path must cover every theme style", i)
		}
		for _, s := range samples {
			if got, want := th.paint(st, s), st.st.Render(s); got != want {
				t.Errorf("style %d paints %q as %q, Render gives %q", i, s, got, want)
			}
		}
		if got := th.paint(st, ""); got != "" {
			t.Errorf("style %d paints empty as %q, want empty", i, got)
		}
	}
	plain := PlainTheme()
	for _, s := range samples {
		if got := plain.paint(styles[0], s); got != s {
			t.Errorf("plain theme paints %q as %q", s, got)
		}
	}
}

// TestBodyBandsSliceExact pins the other two TTP-123 self-review classes:
// a sliced-up line must reassemble to itself even when a row is shorter than
// the one before it (no cross-row buffer to leak), and byte slicing must not
// split a wide rune (CJK prompt text, as in hero.tape).
func TestBodyBandsSliceExact(t *testing.T) {
	mkTok := func(dt time.Duration, text string) Token {
		return Token{T: dt, Text: text}
	}
	text := "a long first line of prose with words\n한글 짧은 줄\n```go\nfunc main() { println(\"한글\") }\n```\nshort"
	var toks []Token
	for i, r := range []rune(text) {
		toks = append(toks, mkTok(time.Duration(i)*time.Millisecond, string(r)))
	}
	s := Stream{Text: text, Tokens: toks}
	at := time.Duration(len(toks)) * time.Millisecond
	lines := streamTextLines(s, 40)
	if len(lines) == 0 {
		t.Fatal("no lines wrapped")
	}
	bands := bodyBands(s, lines, at)
	for i, bl := range lines {
		if bl.marker {
			continue
		}
		var b strings.Builder
		for _, sg := range bands[i] {
			b.WriteString(sg.text)
		}
		if got := b.String(); got != bl.text {
			t.Errorf("line %d reassembles to %q, want %q", i, got, bl.text)
		}
		for _, sg := range bands[i] {
			if !utf8.ValidString(sg.text) {
				t.Errorf("line %d segment %q splits a rune", i, sg.text)
			}
		}
	}
	// A shorter tail of the same stream must not carry the longer line's bytes.
	short := Stream{Text: "short", Tokens: []Token{mkTok(0, "short")}}
	slines := streamTextLines(short, 40)
	sbands := bodyBands(short, slines, 0)
	for i, bl := range slines {
		var b strings.Builder
		for _, sg := range sbands[i] {
			b.WriteString(sg.text)
		}
		if got := b.String(); got != bl.text {
			t.Errorf("short line %d reassembles to %q, want %q", i, got, bl.text)
		}
	}
}

// viewAllocEndModel builds the TTP-123 alloc-gate input: sweep tape at RunEnd,
// 120×36, colour theme — the same input BenchmarkView/end measures.
func viewAllocEndModel(t *testing.T) (Model, time.Duration) {
	t.Helper()
	tp, err := tape.Read("testdata/qwen36-35b-a3b-q6k-4stream-ik-sweep-0.2.4.tape")
	if err != nil {
		t.Fatalf("read sweep tape: %v", err)
	}
	end := sweepRunEnd(tp)
	m := ModelAt(tp, end)
	m.Theme = ColourTheme()
	m.Replay = true
	return m, end
}

// TestViewAllocs is the recurrence gate: the end frame at 120×36 must stay
// under maxViewAllocs allocations.
//
// Ceiling: measured post-change allocs/op × 1.25, rounded up to a round
// number. It FAILS on the unchanged source: with the 50%-of-baseline interim
// ceiling (6800) the unchanged source measured 13632 allocs/op (see report);
// the final 3600 ceiling is tighter still, so it fails there too.
func TestViewAllocs(t *testing.T) {
	m, end := viewAllocEndModel(t)
	// 2026-09-19 (TTP-123): AllocsPerRun(20) before 13632, after 2835.
	// Ceiling is after × 1.25 rounded up to a round number.
	const maxViewAllocs = 3600
	if n := testing.AllocsPerRun(20, func() { _ = View(m, end, 120, 36) }); n > maxViewAllocs {
		t.Fatalf("View allocs/op = %v, ceiling %d", n, maxViewAllocs)
	}
}
