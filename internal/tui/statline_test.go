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
	const cw = 60

	// t = 0: the prompt is still being evaluated. There is no rate, no first
	// token and no median, and every one of them says so rather than printing
	// a figure the tape happens to carry from a later frame.
	start := ModelAt(ExampleTapeN(4), 0).Streams[0]
	line := tileStatLine(th, start, cw)
	for _, want := range []string{"? tok/s", "ttft ?", "p50 ?"} {
		if !strings.Contains(line, want) {
			t.Errorf("the opening stat line %q does not read %q", line, want)
		}
	}

	// One arrival is not a rate: the gap before the first token is a TTFT, a
	// different measurement (handover lesson 1).
	one := ModelAt(ExampleTapeN(4), 215*time.Millisecond).Streams[0]
	if got := len(one.Tokens); got != 1 {
		t.Fatalf("the fixture's first stream has %d tokens just after its TTFT, want 1", got)
	}
	if line := tileStatLine(th, one, cw); !strings.Contains(line, "? tok/s") {
		t.Errorf("a stream with one token prints a rate: %q", line)
	} else if !strings.Contains(line, "ttft 210 ms") {
		t.Errorf("a stream with one token has a TTFT and does not print it: %q", line)
	}

	// Mid-run: the client-side figure over the content window, and the
	// stream's own median once there are enough gaps to take one.
	mid := ModelAt(ExampleTapeN(4), 3*time.Second).Streams[0]
	line = tileStatLine(th, mid, cw)
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
	if line := tileStatLine(th, split, cw); !strings.HasPrefix(line, "9.4 tok/s") {
		t.Errorf("a finished stat line %q does not lead with the server's figure", line)
	}

	// Live, the same stream reports what the timeline says: a tape being
	// replayed carries its final timings from the first frame, and printing
	// them beside a half-written answer would put the tile at odds with the
	// live figure in the speed panel.
	split.Done = false
	if line := tileStatLine(th, split, cw); !strings.HasPrefix(line, "10.0 tok/s") {
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
	line := tileStatLine(th, s, 60)

	rate := fmtRate(streamRate(s)) + " tok/s"
	if !strings.Contains(line, sgrPrefix(th, th.accentBold)+rate) {
		t.Errorf("the rate is not drawn in the headline style: %q", card.StripANSI(line))
	}
	if !strings.Contains(line, sgrPrefix(th, th.dim)+"ttft") {
		t.Errorf("the qualifiers are not dim: %q", card.StripANSI(line))
	}
	if got := card.StripANSI(line); got != tileStatLine(PlainTheme(), s, 60) {
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
	// ms" and the count needs room for "128/128", whatever this frame prints.
	tests := []struct {
		cw    int
		parts []string
		gone  []string
	}{
		{60, []string{"tok/s", "ttft", "/128", "p50"}, nil},
		{45, []string{"tok/s", "ttft", "/128", "p50"}, nil},
		{44, []string{"tok/s", "ttft", "/128"}, []string{"p50"}},
		{32, []string{"tok/s", "ttft", "/128"}, []string{"p50"}},
		{31, []string{"tok/s", "ttft"}, []string{"p50", "/128"}},
		{30, []string{"tok/s", "ttft"}, []string{"p50", "/128"}},
		{24, []string{"tok/s", "ttft"}, []string{"p50", "/128"}},
		{22, []string{"tok/s", "ttft"}, []string{"p50", "/128"}},
		{21, []string{"tok/s"}, []string{"p50", "/128", "ttft"}},
		{18, []string{"tok/s"}, []string{"p50", "/128", "ttft"}},
	}
	for _, tc := range tests {
		got := tileStatLine(th, s, tc.cw)
		if width(got) != tc.cw {
			t.Fatalf("the stat line at %d columns is %d wide: %q", tc.cw, width(got), got)
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

	// The order itself, at every width rather than at four of them: a part
	// that has gone never comes back on a narrower line, and no part outlives
	// the one before it.
	var dropped [4]int
	for cw := 90; cw >= 1; cw-- {
		got := tileStatLine(th, s, cw)
		present := [4]bool{
			strings.Contains(got, "tok/s") || strings.Contains(got, fmtRate(streamRate(s))),
			strings.Contains(got, "ttft"),
			strings.Contains(got, "/128"),
			strings.Contains(got, "p50"),
		}
		for i, on := range present {
			if !on {
				dropped[i] = cw
				continue
			}
			if dropped[i] != 0 {
				t.Fatalf("part %d came back at %d columns, having gone at %d: %q", i, cw, dropped[i], got)
			}
		}
		for i := 1; i < len(present); i++ {
			if present[i] && !present[i-1] {
				t.Fatalf("the stat line at %d columns kept part %d without part %d: %q", cw, i, i-1, got)
			}
		}
	}

	// Every width, all the way down: the line is exact, and a figure is either
	// whole or absent.
	for _, at := range []time.Duration{0, 500 * time.Millisecond, midRun, doneAt} {
		for _, st := range ModelAt(ExampleTapeN(4), at).Streams {
			for cw := 1; cw <= 90; cw++ {
				got := tileStatLine(th, st, cw)
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
			shapes = append(shapes, statShape(tileStatLine(th, s, cw)))
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
	if s.MaxTokens != 128 {
		t.Fatalf("the fixture's max_tokens came through as %d, want 128", s.MaxTokens)
	}
	if want := fmt.Sprintf("%d/128", len(s.Tokens)); !strings.Contains(tileStatLine(PlainTheme(), s, 60), want) {
		t.Errorf("the stat line does not count against the budget (%q)", want)
	}

	s.MaxTokens = 0
	if want := fmt.Sprintf("%d tok", len(s.Tokens)); !strings.Contains(tileStatLine(PlainTheme(), s, 60), want) {
		t.Errorf("a stream with no recorded budget does not print a bare count (%q)", want)
	}

	// Reasoning tokens are decode tokens: the server counts them in
	// predicted_n, they are spent out of the same budget, and a thinking model
	// that never reaches an answer still shows the budget running down.
	thinking := ModelAt(ExampleTapeN(4), 3*time.Second).Streams[3]
	if thinkingBadge(thinking) != "thinking" {
		t.Fatalf("the fixture's fourth stream is not thinking at 3 s")
	}
	if n, ok := statCount(tileStatLine(PlainTheme(), thinking, 60)); !ok || n != len(thinking.Tokens) {
		t.Errorf("a thinking stream counts %d of its %d tokens", n, len(thinking.Tokens))
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

// TestFooterLabelIsTheWindowMean: the figure beside a sparkline is the average
// of the cells it is beside, within what one decimal can say. A shape and a
// number that disagree are two claims, and the reader cannot tell which one to
// believe.
func TestFooterLabelIsTheWindowMean(t *testing.T) {
	for _, at := range []time.Duration{midRun, 3 * time.Second, doneAt} {
		for i, s := range ModelAt(ExampleTapeN(4), at).Streams {
			const cw = 41
			line := tileFooter(PlainTheme(), s, cw, false)
			want := mean(streamRates(s, cw-2-tileAvgW))
			got, ok := footerMean(line)
			if !ok {
				t.Fatalf("at %v stream %d has no footer label: %q", at, i, line)
			}
			if diff := got - want; diff > 0.05 || diff < -0.05 {
				t.Errorf("at %v stream %d labels its sparkline %.2f, want %.2f", at, i, got, want)
			}
		}
	}
}

// footerMean reads the "11.8 avg" label back off a tile footer.
func footerMean(line string) (float64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[len(fields)-1] != "avg" {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[len(fields)-2], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
