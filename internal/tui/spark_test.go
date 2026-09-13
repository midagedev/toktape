package tui

import (
	"strings"
	"testing"
	"time"
)

func glyphs(cells []Cell) string {
	var b strings.Builder
	for _, c := range cells {
		b.WriteRune(c.R)
	}
	return b.String()
}

// sevMask renders the severity of each cell: "." normal, "!" warn, "X" bad.
func hotMask(cells []Cell) string {
	var b strings.Builder
	for _, c := range cells {
		switch c.Sev {
		case SevWarn:
			b.WriteByte('!')
		case SevBad:
			b.WriteByte('X')
		default:
			b.WriteByte('.')
		}
	}
	return b.String()
}

func TestSparkline(t *testing.T) {
	tests := []struct {
		name string
		vals []float64
		w    int
		hot  float64
		want string
		mask string
	}{
		{
			name: "empty draws nothing",
			vals: nil, w: 8, want: "", mask: "",
		},
		{
			name: "a single sample is one column at full height",
			vals: []float64{3}, w: 8, want: "█", mask: "........"[:1],
		},
		{
			name: "all zero is a flat floor, not a blank",
			vals: []float64{0, 0, 0}, w: 8, want: "▁▁▁", mask: "...",
		},
		{
			name: "scaled to the window maximum",
			vals: []float64{0, 1, 2, 3, 4, 5, 6, 7}, w: 8,
			want: "▁▂▃▄▅▆▇█", mask: "........",
		},
		{
			name: "equal non-zero samples all saturate",
			vals: []float64{2, 2, 2}, w: 8, want: "███", mask: "...",
		},
		{
			name: "only the last w values are drawn",
			vals: []float64{9, 9, 9, 0, 4, 8}, w: 3, want: "▁▅█", mask: "...",
		},
		{
			name: "the hot threshold marks the spike, not its neighbours",
			vals: []float64{0, 0, 2, 0}, w: 8, hot: 1,
			want: "▁▁█▁", mask: "..X.",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Sparkline(tc.vals, tc.w, tc.hot)
			if g := glyphs(got); g != tc.want {
				t.Errorf("glyphs = %q, want %q", g, tc.want)
			}
			if m := hotMask(got); m != tc.mask {
				t.Errorf("hot mask = %q, want %q", m, tc.mask)
			}
			if len(got) > tc.w {
				t.Errorf("returned %d cells, more than the %d asked for", len(got), tc.w)
			}
		})
	}
}

func TestSparklineZeroWidth(t *testing.T) {
	if got := Sparkline([]float64{1, 2, 3}, 0, 0); got != nil {
		t.Errorf("width 0 returned %d cells", len(got))
	}
}

func TestLatencyStrip(t *testing.T) {
	tests := []struct {
		name string
		ms   []float64
		w    int
		want string
		mask string
	}{
		{name: "empty", ms: nil, w: 8, want: "", mask: ""},
		{
			// One sample is its own median and its own p95: no spread to draw.
			name: "single", ms: []float64{40}, w: 8, want: "▏", mask: ".",
		},
		{
			// A window where every token took the same time is a flat
			// hairline. How long "the same" was is the p50 printed beside it.
			name: "a flat run is a flat hairline",
			ms:   flat(20, 80), w: 20,
			want: strings.Repeat("▏", 20), mask: strings.Repeat(".", 20),
		},
		{
			name: "a stall towers over its neighbours and goes red",
			ms:   append(flat(19, 10), 400), w: 20,
			want: strings.Repeat("▏", 19) + "█", mask: strings.Repeat(".", 19) + "X",
		},
		{
			// Between the p95 and three times it, a token is amber rather than
			// red: slower than the run's normal, not a stall.
			name: "the amber band sits between p95 and the ceiling",
			ms:   append(flat(19, 10), 25), w: 20,
			want: strings.Repeat("▏", 19) + "█", mask: strings.Repeat(".", 19) + "!",
		},
		{
			// The dropped 500s take no part: the window is [10, 20], and 20 is
			// twice its median, which is what amber means.
			name: "only the last w latencies are drawn",
			ms:   []float64{500, 500, 10, 20}, w: 2, want: "▏█", mask: ".!",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LatencyStrip(tc.ms, tc.w)
			if g := glyphs(got); g != tc.want {
				t.Errorf("glyphs = %q, want %q", g, tc.want)
			}
			if m := hotMask(got); m != tc.mask {
				t.Errorf("hot mask = %q, want %q", m, tc.mask)
			}
		})
	}
}

// TestLatencyStripKeepsItsShapeAroundAStall: a single huge outlier must not
// collapse the healthy tokens around it into one flat height, which is what
// scaling the ramp to the window's maximum would do. Against a percentile they
// keep their own spread, and the outlier saturates and turns red.
func TestLatencyStripKeepsItsShapeAroundAStall(t *testing.T) {
	ms := []float64{70, 78, 85, 92, 98, 70, 80, 88, 95, 100,
		72, 82, 86, 90, 96, 74, 84, 87, 93, 400}
	cells := LatencyStrip(ms, 20)

	seen := map[rune]bool{}
	for i := 0; i < 19; i++ {
		seen[cells[i].R] = true
		if cells[i].Sev == SevBad {
			t.Fatalf("healthy token %d (%.0f ms) was flagged as a stall", i, ms[i])
		}
	}
	if len(seen) < 4 {
		t.Errorf("the healthy tokens used %d glyphs; the outlier flattened the strip", len(seen))
	}
	if cells[19].R != '█' || cells[19].Sev != SevBad {
		t.Fatalf("the stall drew %q at severity %d, want a full red block", cells[19].R, cells[19].Sev)
	}
}

func flat(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestBarCells(t *testing.T) {
	// 2026-09-14 (TTP-49): the glyph moved from the full block to ▆ so that
	// stacked bars keep a seam between them; every count is unchanged.
	tests := []struct {
		frac         float64
		w            int
		full, hollow string
	}{
		{0, 10, "", "▆▆▆▆▆▆▆▆▆▆"},
		{-1, 10, "", "▆▆▆▆▆▆▆▆▆▆"},
		{0.01, 10, "▆", "▆▆▆▆▆▆▆▆▆"}, // a little is never nothing
		{0.5, 10, "▆▆▆▆▆", "▆▆▆▆▆"},
		{1, 10, "▆▆▆▆▆▆▆▆▆▆", ""},
		{2, 10, "▆▆▆▆▆▆▆▆▆▆", ""},
		{0.5, 0, "", ""},
	}
	for _, tc := range tests {
		f, e := barCells(tc.frac, tc.w)
		if f != tc.full || e != tc.hollow {
			t.Errorf("barCells(%v, %d) = %q/%q, want %q/%q", tc.frac, tc.w, f, e, tc.full, tc.hollow)
		}
		if got := len([]rune(f + e)); tc.w > 0 && got != tc.w {
			t.Errorf("barCells(%v, %d) produced %d cells", tc.frac, tc.w, got)
		}
	}
}

// TestSegmentBarKeepsEverySharePresent: the compute buffer is a tenth of the
// VRAM split and the legend next to the bar names it, so it must never round
// away to nothing.
func TestSegmentBarKeepsEverySharePresent(t *testing.T) {
	parts := []float64{13.5, 3.0, 0.9}
	segs := segmentBar(parts, 17.4, 15)
	total := 0
	for i, s := range segs {
		n := len([]rune(s))
		total += n
		if i < len(parts) && parts[i] > 0 && n == 0 {
			t.Errorf("share %d (%.1f) drew no cells", i, parts[i])
		}
	}
	if total != 15 {
		t.Errorf("the bar is %d cells wide, want 15", total)
	}
}

func TestSegmentBarUnknownTotal(t *testing.T) {
	segs := segmentBar([]float64{0, 0, 0}, 0, 6)
	if segs[3] != "▆▆▆▆▆▆" {
		t.Errorf("an unmeasured split drew %q, want six empty cells", segs[3])
	}
}

// TestEaseSettles: a bar reaches its target exactly at the end of the window
// and never overshoots on the way.
func TestEase(t *testing.T) {
	const since = time.Second
	if got := ease(10, 20, since, since-time.Millisecond); got != 10 {
		t.Errorf("before the step the value is %v, want the previous one", got)
	}
	if got := ease(10, 20, since, since); got != 10 {
		t.Errorf("at the step the value is %v, want the previous one", got)
	}
	if got := ease(10, 20, since, since+easeDur); got != 20 {
		t.Errorf("at the end of the window the value is %v, want the target", got)
	}
	if got := ease(10, 20, since, since+2*easeDur); got != 20 {
		t.Errorf("past the window the value is %v, want the target", got)
	}
	prev := 10.0
	for step := time.Duration(0); step <= easeDur; step += easeDur / 20 {
		v := ease(10, 20, since, since+step)
		if v < prev-1e-9 || v > 20+1e-9 {
			t.Fatalf("at +%v the value is %v: not monotonic toward the target", step, v)
		}
		prev = v
	}
}

// TestAnimationsAreFunctionsOfT: every moving part must repeat exactly one
// period later, which is what makes a replay reproduce the frames.
func TestAnimationsAreFunctionsOfT(t *testing.T) {
	for _, base := range []time.Duration{0, 37 * time.Millisecond, 4321 * time.Millisecond} {
		if a, b := spinnerAt(base), spinnerAt(base+spinFrame*time.Duration(len(spinnerFrames))); a != b {
			t.Errorf("spinner at %v is %q, one period later %q", base, a, b)
		}
		if a, b := breathPhase(base), breathPhase(base+breathDur); a != b {
			t.Errorf("breath at %v is %d, one period later %d", base, a, b)
		}
		if a, b := shimmerStart(base, 80), shimmerStart(base+shimmerDur, 80); a != b {
			t.Errorf("shimmer at %v is %d, one period later %d", base, a, b)
		}
		if got := shimmerStart(base, 80); got < 0 || got >= 80 {
			t.Errorf("shimmer at %v is at column %d, outside the rule", base, got)
		}
	}
	if spinnerAt(0) == spinnerAt(spinFrame) {
		t.Error("the spinner does not advance between frames")
	}
	seen := map[int]bool{}
	for step := time.Duration(0); step < breathDur; step += breathDur / 8 {
		seen[breathPhase(step)] = true
	}
	if len(seen) != 3 {
		t.Errorf("the cursor used %d of its three shades in one cycle", len(seen))
	}
}

func TestPercentile(t *testing.T) {
	vals := []float64{5, 1, 4, 2, 3}
	if got := percentile(vals, 0.5); got != 3 {
		t.Errorf("median = %v, want 3", got)
	}
	if got := p95(vals); got != 5 {
		t.Errorf("p95 = %v, want 5", got)
	}
	if got := percentile(nil, 0.5); got != 0 {
		t.Errorf("percentile of nothing = %v, want 0", got)
	}
	if vals[0] != 5 {
		t.Error("percentile reordered the caller's slice")
	}
}
