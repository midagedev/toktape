package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// TestStatLineLeadsWithTheRate walks one stream through a run and pins what
// its second row says at each point: nothing it has not measured, the live
// figure while it is decoding, and the server's own figure once it is done.
func TestStatLineLeadsWithTheRate(t *testing.T) {
	th := PlainTheme()
	// 2026-09-13 (TTP-29): 60 columns, until the sparkline joined this row and
	// took the space the median used to sit in. The width is raised rather
	// than the expectations lowered — this test is about which figures a tile
	// prints and which it refuses to guess, not about how narrow a tile can
	// be, and TestStatLineDegradesInOrder owns the latter. 67 is the narrowest
	// tile that still carries all four; 85 is the one-tile-wide pane at 120
	// columns.
	const cw = 85

	// t = 0: the prompt is still being evaluated. There is no rate, no first
	// token and no median, and every one of them says so rather than printing
	// a figure the tape happens to carry from a later frame.
	start := ModelAt(ExampleTapeN(4), 0).Streams[0]
	line := tileStatLine(Model{}, th, 0, start, cw)
	for _, want := range []string{"? tok/s", "ttft ?", "p50 ?"} {
		if !strings.Contains(line, want) {
			t.Errorf("the opening stat line %q does not read %q", line, want)
		}
	}

	// One arrival is not a rate: the gap before the first token is a TTFT, a
	// different measurement (handover lesson 1).
	one := ModelAt(ExampleTapeN(4), 640*time.Millisecond).Streams[0]
	if got := len(one.Tokens); got != 1 {
		t.Fatalf("the fixture's first stream has %d tokens just after its TTFT, want 1", got)
	}
	if line := tileStatLine(Model{}, th, 0, one, cw); !strings.Contains(line, "? tok/s") {
		t.Errorf("a stream with one token prints a rate: %q", line)
	} else if !strings.Contains(line, "ttft 630 ms") {
		t.Errorf("a stream with one token has a TTFT and does not print it: %q", line)
	}

	// Mid-run: the client-side figure over the content window, and the
	// stream's own median once there are enough gaps to take one.
	mid := ModelAt(ExampleTapeN(4), 3*time.Second).Streams[0]
	line = tileStatLine(Model{}, th, 0, mid, cw)
	if want := fmtRate(streamRate(mid)) + " tok/s"; !strings.HasPrefix(line, want) {
		t.Errorf("the mid-run stat line %q does not lead with %q", line, want)
	}
	if want := "p50 " + fmtMs(percentile(streamITLs(mid), 0.5)); !strings.Contains(line, want) {
		t.Errorf("the mid-run stat line %q does not carry %q", line, want)
	}

	// Done: server figures are the record (CLAUDE.md), and the client's is the
	// check. The example's two agree by construction, so the one that has to
	// win is pinned on a stream where they do not: four tokens 100 ms apart
	// are 10 tok/s measured from the timeline, and the server says 9.4.
	split := Stream{
		Done: true,
		Tokens: []Token{
			{T: 200 * time.Millisecond},
			{T: 300 * time.Millisecond, ITL: 100 * time.Millisecond},
			{T: 400 * time.Millisecond, ITL: 100 * time.Millisecond},
			{T: 500 * time.Millisecond, ITL: 100 * time.Millisecond},
		},
		Timings: tape.TimingsSummary{PredictedPerSecond: 9.4, TTFTMs: 200},
	}
	if got := fmtRate(streamRate(split)); got != "10.0" {
		t.Fatalf("the fixture's client-side rate is %s, want 10.0", got)
	}
	if line := tileStatLine(Model{}, th, 0, split, cw); !strings.HasPrefix(line, "9.4 tok/s") {
		t.Errorf("a finished stat line %q does not lead with the server's figure", line)
	}

	// Live, the same stream reports what the timeline says: a tape being
	// replayed carries its final timings from the first frame, and printing
	// them beside a half-written answer would put the tile at odds with the
	// live figure in the speed panel.
	split.Done = false
	if line := tileStatLine(Model{}, th, 0, split, cw); !strings.HasPrefix(line, "10.0 tok/s") {
		t.Errorf("a running stat line %q does not lead with the measured figure", line)
	}
}

// TestStatLineIsAccented: the rate is the one figure on the tile that wears the
// headline style, and the qualifiers behind it are dim. Colour is the whole
// reason the row reads as a hero rather than as another line of chrome, and
// the plain rendering cannot see it.
func TestStatLineIsAccented(t *testing.T) {
	th := ColourTheme()
	s := ModelAt(ExampleTapeN(4), 3*time.Second).Streams[0]
	line := tileStatLine(Model{}, th, 0, s, 60)

	rate := fmtRate(streamRate(s)) + " tok/s"
	if !strings.Contains(line, sgrPrefix(th, th.accentBold)+rate) {
		t.Errorf("the rate is not drawn in the headline style: %q", card.StripANSI(line))
	}
	if !strings.Contains(line, sgrPrefix(th, th.dim)+"ttft") {
		t.Errorf("the qualifiers are not dim: %q", card.StripANSI(line))
	}
	if got := card.StripANSI(line); got != tileStatLine(Model{}, PlainTheme(), 0, s, 60) {
		t.Errorf("the coloured stat line strips to something else:\n%q", got)
	}
}

// TestStatLineDegradesInOrder: what a narrowing tile gives up, and in what
// order. The rate never goes, nothing is ever half-printed, and the line is
// exactly as wide as it was asked to be at every width a tile can have.
func TestStatLineDegradesInOrder(t *testing.T) {
	th := PlainTheme()
	s := ModelAt(ExampleTapeN(4), 3*time.Second).Streams[0]

	// The widths each part survives down to. They are the reserved widths
	// rather than the printed ones (see TestStatLineDoesNotReflowAsFiguresGrow)
	// and so they are wider than the figures look: the median needs "p50 250
	// ms" and the count needs room for "320/320", whatever this frame prints.
	//
	// 2026-09-13 (TTP-29): the graph is now one of the parts, and it takes
	// nineteen columns at the sizes a tile is usually drawn at, so the median
	// survives only on a tile 67 columns or wider. The expectations moved with
	// the layout; nothing here was relaxed.
	tests := []struct {
		cw    int
		spark bool
		parts []string
		gone  []string
	}{
		{85, true, []string{"tok/s", "ttft", "/320", "p50"}, nil},
		{67, true, []string{"tok/s", "ttft", "/320", "p50"}, nil},
		{66, true, []string{"tok/s", "ttft", "/320"}, []string{"p50"}},
		{60, true, []string{"tok/s", "ttft", "/320"}, []string{"p50"}},
		{45, true, []string{"tok/s", "ttft", "/320"}, []string{"p50"}},
		{44, true, []string{"tok/s", "ttft", "/320"}, []string{"p50"}},
		{41, true, []string{"tok/s", "ttft", "/320"}, []string{"p50"}},
		{40, true, []string{"tok/s", "ttft"}, []string{"p50", "/320"}},
		{39, false, []string{"tok/s", "ttft", "/320"}, []string{"p50"}},
		{32, false, []string{"tok/s", "ttft", "/320"}, []string{"p50"}},
		{31, false, []string{"tok/s", "ttft"}, []string{"p50", "/320"}},
		{30, false, []string{"tok/s", "ttft"}, []string{"p50", "/320"}},
		{24, false, []string{"tok/s", "ttft"}, []string{"p50", "/320"}},
		{22, false, []string{"tok/s", "ttft"}, []string{"p50", "/320"}},
		{21, false, []string{"tok/s"}, []string{"p50", "/320", "ttft"}},
		{18, false, []string{"tok/s"}, []string{"p50", "/320", "ttft"}},
	}
	for _, tc := range tests {
		got := tileStatLine(Model{}, th, 0, s, tc.cw)
		if width(got) != tc.cw {
			t.Fatalf("the stat line at %d columns is %d wide: %q", tc.cw, width(got), got)
		}
		if drawn := strings.ContainsAny(got, string(sparkRunes)); drawn != tc.spark {
			t.Errorf("the stat line at %d columns draws a graph = %v, want %v: %q", tc.cw, drawn, tc.spark, got)
		}
		for _, want := range tc.parts {
			if !strings.Contains(got, want) {
				t.Errorf("the stat line at %d columns dropped %q: %q", tc.cw, want, got)
			}
		}
		for _, unwanted := range tc.gone {
			if strings.Contains(got, unwanted) {
				t.Errorf("the stat line at %d columns kept %q: %q", tc.cw, unwanted, got)
			}
		}
	}

	// The order itself, at every width rather than at a dozen of them: a part
	// that has gone never comes back on a narrower line.
	//
	// 2026-09-13 (TTP-29): the graph has a minimum size — under eight cells it
	// is a flicker, not a line — so it is not simply the third rung of a
	// priority ladder. On a tile too narrow to hold eight cells there is no
	// graph to give up, and the count takes the room instead. That is one
	// seam, at one width, and it is named here rather than papered over: below
	// it the ladder is the one this test has always pinned, above it the graph
	// outranks the count. Every other part still only ever goes away.
	//
	// The seam is derived, not typed: it is the narrowest tile the width
	// formula gives a graph to, so a change to tileRateW or tileSparkMin moves
	// the carve-out with it instead of leaving it at a stale column.
	seam := 1
	for cw := 1; cw <= 90; cw++ {
		if tileSparkW(cw) > 0 {
			seam = cw
			break
		}
	}
	if tileSparkW(seam) == 0 || tileSparkW(seam-1) != 0 {
		t.Fatalf("the seam came out at %d columns, which draws no graph", seam)
	}
	var dropped [5]int
	for cw := 90; cw >= 1; cw-- {
		got := tileStatLine(Model{}, th, 0, s, cw)
		present := [5]bool{
			strings.Contains(got, "tok/s") || strings.Contains(got, fmtRate(streamRate(s))),
			strings.Contains(got, "ttft"),
			strings.ContainsAny(got, string(sparkRunes)),
			strings.Contains(got, "/320"),
			strings.Contains(got, "p50"),
		}
		for i, on := range present {
			if !on {
				dropped[i] = cw
				continue
			}
			if dropped[i] != 0 && !(i == 3 && cw == seam-1 && dropped[i] == seam) {
				t.Fatalf("part %d came back at %d columns, having gone at %d: %q", i, cw, dropped[i], got)
			}
			dropped[i] = 0
		}
		// No part outlives the one before it. The graph is left out of this
		// chain, because below the seam there is none to outlive: the rest of
		// the ladder is rate, ttft, count, median at every width.
		if present[2] && !present[1] {
			t.Fatalf("the stat line at %d columns kept its graph without its ttft: %q", cw, got)
		}
		if present[1] && !present[0] {
			t.Fatalf("the stat line at %d columns kept its ttft without its rate: %q", cw, got)
		}
		if present[4] && !present[3] {
			t.Fatalf("the stat line at %d columns kept its median without its count: %q", cw, got)
		}
	}

	// Every width, all the way down: the line is exact, and a figure is either
	// whole or absent.
	for _, at := range []time.Duration{0, 500 * time.Millisecond, midRun, doneAt} {
		for _, st := range ModelAt(ExampleTapeN(4), at).Streams {
			for cw := 1; cw <= 90; cw++ {
				got := tileStatLine(Model{}, th, 0, st, cw)
				if width(got) != cw {
					t.Fatalf("at %v the stat line at %d columns is %d wide: %q", at, cw, width(got), got)
				}
				if strings.Contains(got, "tok/") && !strings.Contains(got, "tok/s") {
					t.Errorf("at %v the stat line at %d columns cut its unit: %q", at, cw, got)
				}
				if strings.Contains(got, "ttf") && !strings.Contains(got, "ttft ") {
					t.Errorf("at %v the stat line at %d columns cut its ttft: %q", at, cw, got)
				}
				// Below the unit's width the figure stands alone, whole or
				// not at all: "11.1" clipped to "1" is a wrong rate.
				if bare := strings.TrimSpace(got); bare != "" && !strings.Contains(got, "tok/s") {
					if want := streamRateFigure(st); bare != want {
						t.Errorf("at %v the stat line at %d columns printed %q, want %q or nothing", at, cw, bare, want)
					}
				}
			}
		}
	}
}

// TestStatLineDoesNotReflowAsFiguresGrow is why the layout is decided against
// the width a figure will grow into rather than the one it has.
//
// "ttft ?" becomes "ttft 250 ms" and "4/128" becomes "80/128" over a run. A
// line fitted to what it prints now would show the median at half a second and
// drop it at three seconds, and a reader would watch a figure appear and then
// vanish from a screen where nothing had changed but the clock.
func TestStatLineDoesNotReflowAsFiguresGrow(t *testing.T) {
	th := PlainTheme()
	for _, cw := range []int{31, 41, 60, 67, 86} {
		var shapes []string
		for _, at := range []time.Duration{0, 500 * time.Millisecond, midRun, 3 * time.Second, doneAt} {
			s := ModelAt(ExampleTapeN(4), at).Streams[0]
			shapes = append(shapes, statShape(tileStatLine(Model{}, th, 0, s, cw)))
		}
		for i, shape := range shapes[1:] {
			if shape != shapes[0] {
				t.Errorf("at %d columns the stat line carries %s in the first frame and %s in frame %d",
					cw, shapes[0], shape, i+1)
			}
		}
	}
}

// statShape names the parts a stat line is carrying, which is what must not
// change over a run. The figures themselves do change — that is the point of
// the row — and they get wider as they do.
func statShape(line string) string {
	// The rate's own unit carries a slash, so the tail is read after it.
	_, tail, _ := strings.Cut(line, "tok/s")
	parts := []string{"rate"}
	for _, p := range []struct{ mark, name string }{
		{"ttft", "ttft"},
		{"/", "count"},
		{"p50", "p50"},
	} {
		if strings.Contains(tail, p.mark) {
			parts = append(parts, p.name)
		}
	}
	return strings.Join(parts, "+")
}

// TestStatLineCountsAgainstTheBudget: the token count is a fraction of what
// the request asked for when the tape says what that was, and a bare count
// when it does not. A budget is never invented (CLAUDE.md).
func TestStatLineCountsAgainstTheBudget(t *testing.T) {
	s := ModelAt(ExampleTapeN(4), 3*time.Second).Streams[0]
	if s.MaxTokens != exampleMaxTokens {
		t.Fatalf("the fixture's max_tokens came through as %d, want %d", s.MaxTokens, exampleMaxTokens)
	}
	if want := fmt.Sprintf("%d/%d", len(s.Tokens), exampleMaxTokens); !strings.Contains(tileStatLine(Model{}, PlainTheme(), 0, s, 60), want) {
		t.Errorf("the stat line does not count against the budget (%q)", want)
	}

	s.MaxTokens = 0
	if want := fmt.Sprintf("%d tok", len(s.Tokens)); !strings.Contains(tileStatLine(Model{}, PlainTheme(), 0, s, 60), want) {
		t.Errorf("a stream with no recorded budget does not print a bare count (%q)", want)
	}

	// Reasoning tokens are decode tokens: the server counts them in
	// predicted_n, they are spent out of the same budget, and a thinking model
	// that never reaches an answer still shows the budget running down.
	thinking := ModelAt(ExampleTapeN(4), 3*time.Second).Streams[3]
	if thinkingBadge(thinking) != "thinking" {
		t.Fatalf("the fixture's fourth stream is not thinking at 3 s")
	}
	if n, ok := statCount(tileStatLine(Model{}, PlainTheme(), 0, thinking, 60)); !ok || n != len(thinking.Tokens) {
		t.Errorf("a thinking stream counts %d of its %d tokens", n, len(thinking.Tokens))
	}
}

// TestStatLineCountsAgainstTheClockWhenThereIsOne: a run with a wall-clock
// budget shows its progress in TIME, not against a token cap it will never
// reach (TTP-76, 2026-09-14).
//
// A default run's cap is recorder.DefaultMaxTokens, a runaway guard of 2048.
// The stat line drew "116/2048" — six per cent and crawling — twelve seconds
// into a twenty-second run that was more than half over. Each case below is a
// frame of that run.
func TestStatLineCountsAgainstTheClockWhenThereIsOne(t *testing.T) {
	budgeted := func(for_ time.Duration) Model {
		m := Model{}
		m.Summary.Limit = tape.LimitSummary{For: for_, MaxTokens: 2048, MinTokens: 64}
		return m
	}
	// 116 tokens at twelve seconds, the cap nowhere in sight.
	running := Stream{MaxTokens: 2048, Tokens: make([]Token, 116)}

	m := budgeted(20 * time.Second)
	line := tileStatLine(m, PlainTheme(), 12*time.Second, running, 60)
	if !strings.Contains(line, "12/20s") {
		t.Errorf("the stat line does not say how far through the budget the run is: %q", line)
	}
	if strings.Contains(line, "2048") {
		t.Errorf("the stat line draws progress against the runaway guard: %q", line)
	}

	// The floor can hold the cut past the budget, and the frame says so
	// rather than stopping at the number that was asked for.
	over := Stream{MaxTokens: 2048, Tokens: make([]Token, 64), Done: true, EndedAt: 22 * time.Second}
	if line := tileStatLine(m, PlainTheme(), 22*time.Second, over, 60); !strings.Contains(line, "22/20s") {
		t.Errorf("a run the floor held past its budget does not say so: %q", line)
	}

	// A stream that ended on EOS freezes at its own last token: the run's
	// remaining budget is not this stream's progress.
	done := Stream{MaxTokens: 2048, Tokens: make([]Token, 300), Done: true, EndedAt: 14 * time.Second}
	if line := tileStatLine(m, PlainTheme(), 19*time.Second, done, 60); !strings.Contains(line, "14/20s") {
		t.Errorf("a finished stream keeps counting the run's clock: %q", line)
	}

	// A stream that failed before its first token saw none of the budget, and
	// must not borrow the run's clock (or, on the frozen live screen, the
	// program's) to say otherwise.
	failed := Stream{MaxTokens: 2048, Done: true, Err: "connection reset"}
	if line := tileStatLine(m, PlainTheme(), 19*time.Second, failed, 60); !strings.Contains(line, "0/20s") {
		t.Errorf("a stream that never produced a token reports progress through the budget: %q", line)
	}

	// No budget, or one too short to spell in whole seconds: the cap form is
	// what those runs really end on.
	for _, m := range []Model{{}, budgeted(500 * time.Millisecond)} {
		if line := tileStatLine(m, PlainTheme(), 12*time.Second, running, 60); !strings.Contains(line, "116/2048") {
			t.Errorf("a run with no clock lost its count against the cap: %q", line)
		}
	}
}

// TestBudgetElapsedIsMeasuredFromTheFirstRequest: the budget's origin is the
// first request going out (tape.LimitSummary.For), which on the LIVE screen is
// not clip time zero — t starts when the program does, and discovery plus a
// --wait for a loading model can put minutes in front of it.
func TestBudgetElapsedIsMeasuredFromTheFirstRequest(t *testing.T) {
	limit := tape.LimitSummary{For: 20 * time.Second, MaxTokens: 2048, MinTokens: 64}
	var m Model
	// Ninety seconds of waiting for a model to load, then the run.
	m = m.Apply(Event{Kind: EventProps, T: 89 * time.Second,
		Server: tape.ServerInfo{URL: "http://127.0.0.1:8080"}, Limit: limit})
	m = m.Apply(Event{Kind: EventStreamStart, T: 90 * time.Second, Stream: 0, MaxTokens: 2048})
	if m.Summary.Limit.For != limit.For {
		t.Fatalf("the attached summary's limit did not reach the model: %+v", m.Summary.Limit)
	}
	if m.RunStart != 90*time.Second {
		t.Fatalf("RunStart = %v, want the first request at 90s", m.RunStart)
	}
	m = m.Apply(Event{Kind: EventToken, T: 102 * time.Second, Stream: 0, Token: tape.TokenEvent{Text: "x"}})

	line := tileStatLine(m, PlainTheme(), 102*time.Second, m.Streams[0], 60)
	if !strings.Contains(line, "12/20s") {
		t.Errorf("the budget is being measured from the program's start, not the run's: %q", line)
	}
}

// statCount reads the "80/128" figure back off a stat line.
func statCount(line string) (int, bool) {
	for _, field := range strings.Split(line, " · ") {
		n, rest, ok := strings.Cut(strings.TrimSpace(field), "/")
		if !ok || rest == "" {
			continue
		}
		v, err := strconv.Atoi(n)
		if err != nil {
			continue
		}
		return v, true
	}
	return 0, false
}

// TestPromptMaxTokensReadsEitherSpelling: the cap goes into the tape as the
// parameters went over the wire, which is "max_tokens" for the OpenAI-shaped
// endpoint and "n_predict" for llama.cpp's own name for it — and as a float64
// once the tape has been round-tripped through JSON.
func TestPromptMaxTokensReadsEitherSpelling(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   int
	}{
		{"none", nil, 0},
		{"max_tokens as an int", map[string]any{"max_tokens": 320}, 320},
		{"max_tokens from JSON", map[string]any{"max_tokens": float64(320)}, 320},
		{"n_predict", map[string]any{"n_predict": 256}, 256},
		{"unlimited", map[string]any{"n_predict": -1}, 0},
		{"not a number", map[string]any{"max_tokens": "lots"}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := promptMaxTokens(tape.PromptRecord{Params: tc.params}); got != tc.want {
				t.Errorf("promptMaxTokens = %d, want %d", got, tc.want)
			}
		})
	}
}

// The footer's "11.8 avg" label had a test here that read the figure back off
// the tile's last row and compared it with the mean of the cells beside it.
// Both are gone (TTP-29): the graph moved into the stat line and the number
// beside it is the live rate, which the emphasis gate already pins.
// TestStatLineSparkIsTheStreamsOwnWindow (tilespark_test.go) is what replaced
// the shape-and-number agreement it was checking.

// TestPromptMaxTokensPrefersTheRecordedField: the cap has a field of its own on
// tape.PromptRecord now (the recorder fills it with what the request was sent
// with). Params stays the fallback so a tape written before that field existed
// still draws "80/128" rather than a bare count.
func TestPromptMaxTokensPrefersTheRecordedField(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  tape.PromptRecord
		want int
	}{
		{"the recorded field", tape.PromptRecord{MaxTokens: 320}, 320},
		{
			"an older tape, max_tokens in params",
			tape.PromptRecord{Params: map[string]any{"max_tokens": 128}},
			128,
		},
		{
			"an older tape, llama.cpp's own name",
			tape.PromptRecord{Params: map[string]any{"n_predict": 96}},
			96,
		},
		{
			// The field is the record: a tape whose params disagree with it
			// was sent with the field's figure.
			"the field wins over params",
			tape.PromptRecord{MaxTokens: 320, Params: map[string]any{"max_tokens": 128}},
			320,
		},
		{"no cap anywhere", tape.PromptRecord{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := promptMaxTokens(tc.rec); got != tc.want {
				t.Errorf("promptMaxTokens() = %d, want %d", got, tc.want)
			}
		})
	}
}
