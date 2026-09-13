package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// TestResourceGraphHeight pins the height the pane chooses at the sizes the
// layout is designed around (TTP-39): three rows at the clip's 120x36 with
// four and with eight streams, and whatever fits below that. It also pins the
// rule behind the choice — the pane at that height is never cut by fitRows —
// and that one row taller would have been.
func TestResourceGraphHeight(t *testing.T) {
	for _, c := range []struct {
		w, h, n int
		want    int
	}{
		{100, 30, 4, 1},
		{120, 36, 4, 3},
		{120, 36, 8, 3},
		{140, 40, 4, 3},
		{160, 48, 4, 3},
	} {
		t.Run(fmt.Sprintf("%dx%d-n%d", c.w, c.h, c.n), func(t *testing.T) {
			m := ModelAt(ExampleTapeN(c.n), midRun)
			rows := c.h - chromeH
			lines, got := rightPaneLines(m, PlainTheme(), midRun, rightW-2, rows)
			if got != c.want {
				t.Errorf("graph height %d, want %d", got, c.want)
			}
			if len(lines) > rows {
				t.Errorf("the pane is %d lines at height %d and has %d rows; fitRows would cut it", len(lines), got, rows)
			}
			if got < resourceHeights[0] {
				if taller := buildRightPane(m, PlainTheme(), midRun, rightW-2, got+1); len(taller) <= rows {
					t.Errorf("height %d fits in %d rows (%d lines) and was not chosen", got+1, rows, len(taller))
				}
			}
		})
	}
}

// resourceBlock returns the RESOURCES section of a plain pane: the title and
// every line under it.
func resourceBlock(t *testing.T, m Model, at time.Duration, h int) []string {
	t.Helper()
	lines := buildRightPane(m, PlainTheme(), at, rightW-2, h)
	for i, l := range lines {
		if strings.HasPrefix(l, "RESOURCES") {
			return lines[i:]
		}
	}
	t.Fatalf("no RESOURCES section in the pane:\n%s", strings.Join(lines, "\n"))
	return nil
}

func TestResourceSection(t *testing.T) {
	m := ModelAt(ExampleTapeN(4), midRun)
	block := resourceBlock(t, m, midRun, 3)
	// Title, then CPU, GPU0 and GPU1 each with a header and three graph rows.
	if len(block) != 1+3*(1+3) {
		t.Fatalf("RESOURCES is %d lines, want 13:\n%s", len(block), strings.Join(block, "\n"))
	}
	if !strings.HasSuffix(block[0], " contended no") {
		t.Errorf("the contended tag is not on the title line: %q", block[0])
	}
	for i, label := range []string{"CPU", "GPU0", "GPU1"} {
		header := block[1+i*4]
		if !strings.HasPrefix(header, label+" ") {
			t.Errorf("resource %d header is %q, want it to start with %s", i, header, label)
		}
		if strings.Contains(header, "?") {
			t.Errorf("%s: the example measures everything, and the header prints a ?: %q", label, header)
		}
		// Every graph row of a decoding GPU is lit at the bottom: a filled area,
		// not scattered dots.
		bottom := block[1+i*4+3]
		if strings.TrimSpace(bottom) == "" {
			t.Errorf("%s: the bottom graph row is empty at mid-run", label)
		}
	}
	if !strings.Contains(block[1], "load 3.1") || !strings.Contains(block[1], " cores") {
		t.Errorf("the CPU header lost the load average or the cores: %q", block[1])
	}
}

// TestResourceNotObserved: a device whose utilisation was never read draws a
// blank graph and prints ?, as does a CPU whose counter or thread count is
// missing. Once a device has been seen, its zeros are measurements.
func TestResourceNotObserved(t *testing.T) {
	tp := ExampleTapeN(4)
	for i := range tp.Samples {
		tp.Samples[i].Mem.CPUSeconds = 0
		for j := range tp.Samples[i].GPUs {
			if tp.Samples[i].GPUs[j].Index == 1 {
				tp.Samples[i].GPUs[j].UtilPct = 0
			}
		}
	}
	m := ModelAt(tp, midRun)
	block := resourceBlock(t, m, midRun, 3)
	cpu, gpu1 := block[1], block[9]
	if !strings.Contains(cpu, "CPU") || !strings.Contains(cpu, "?") {
		t.Errorf("CPU without a counter: %q, want ? figures", cpu)
	}
	if !strings.HasPrefix(gpu1, "GPU1") || !strings.Contains(gpu1, "?") {
		t.Errorf("GPU1 without utilisation: %q, want a ? percentage", gpu1)
	}
	for _, g := range append(block[2:5], block[10:13]...) {
		if strings.TrimSpace(g) != "" {
			t.Errorf("an unobserved series drew %q, want a blank row", g)
		}
	}

	// No thread count: the counter alone cannot say a share of the host.
	tp = ExampleTapeN(4)
	tp.Summary.Host.CPUThreads = 0
	block = resourceBlock(t, ModelAt(tp, midRun), midRun, 3)
	if fields := strings.Fields(block[1]); len(fields) < 2 || fields[1] != "?" {
		t.Errorf("CPU with no thread count: %q, want the percentage to print ?", block[1])
	}

	// A measured zero on a device that reported utilisation elsewhere in the
	// tape prints 0% and draws the baseline — even when every sample up to
	// the frame is zero, because the whole tape decides.
	tp = ExampleTapeN(4)
	for i := range tp.Samples {
		if tp.Samples[i].T <= midRun {
			for j := range tp.Samples[i].GPUs {
				tp.Samples[i].GPUs[j].UtilPct = 0
			}
		}
	}
	block = resourceBlock(t, ModelAt(tp, midRun), midRun, 3)
	if !strings.Contains(block[5], " 0% ") {
		t.Errorf("GPU0 at a measured 0: %q, want 0%%", block[5])
	}
	// The block style (the default since 2026-09-13) draws a measured zero as
	// a ▁ baseline; in the plain theme the track above it is blank.
	if strings.Trim(block[8], "▁ ") != "" || !strings.Contains(block[8], "▁") {
		t.Errorf("GPU0's bottom row at a measured 0 is %q, want a baseline of ▁", block[8])
	}
	if strings.TrimSpace(block[6]) != "" {
		t.Errorf("GPU0's upper rows at a measured 0 are %q, want blank", block[6])
	}
}

// TestCPUSeries: cores from counter deltas, NaN where there is no delta.
func TestCPUSeries(t *testing.T) {
	m := Model{
		Summary: tape.RunSummary{Host: tape.HostInfo{CPUThreads: 4}},
		Samples: []tape.RunSample{
			{T: 0, Mem: tape.MemSample{CPUSeconds: 100}},
			{T: 500 * time.Millisecond, Mem: tape.MemSample{CPUSeconds: 101}},  // 2 cores
			{T: time.Second, Mem: tape.MemSample{CPUSeconds: 100.5}},           // went backwards
			{T: 1500 * time.Millisecond, Mem: tape.MemSample{}},                // not read
			{T: 2 * time.Second, Mem: tape.MemSample{CPUSeconds: 101}},         // no readable previous
			{T: 2500 * time.Millisecond, Mem: tape.MemSample{CPUSeconds: 101}}, // idle, a measured 0
		},
	}
	got := cpuUtilSeries(m, time.Hour)
	want := []string{"NaN", "50", "NaN", "NaN", "NaN", "0"}
	for i := range want {
		if s := fmt.Sprintf("%g", got[i]); s != want[i] {
			t.Errorf("sample %d: %s, want %s (series %v)", i, s, want[i], got)
		}
	}
	if got := cpuUtilSeries(m, 600*time.Millisecond); len(got) != 2 {
		t.Errorf("up to 600ms: %d values, want the 2 samples at or before it", len(got))
	}
}

// TestResourceHeaderDropOrder: when the header does not fit, load goes first,
// then cores, then power; the label and the percentage never go.
func TestResourceHeaderDropOrder(t *testing.T) {
	th := PlainTheme()
	figs := []headerFig{
		{segs: []headerSeg{{th.text, "100%"}}},
		{segs: []headerSeg{{th.text, "12.5"}, {th.dim, " cores"}}, drop: 2},
		{segs: []headerSeg{{th.dim, "load "}, {th.text, "31.2"}}, drop: 3},
	}
	for _, c := range []struct {
		cw   int
		want string
	}{
		{40, "CPU" + strings.Repeat(" ", 40-3-27) + "100%  12.5 cores  load 31.2"},
		{28, "CPU" + strings.Repeat(" ", 28-3-16) + "100%  12.5 cores"}, // 3+1+27 = 31 > 28: load goes
		{12, "CPU     100%"},
		{6, "CPU 10"}, // nothing droppable is left; the line clips
	} {
		got := strings.TrimRight(resourceHeader(th, c.cw, "CPU", figs), " ")
		if got != strings.TrimRight(c.want, " ") {
			t.Errorf("width %d: %q, want %q", c.cw, got, c.want)
		}
	}
	gpu := []headerFig{
		{segs: []headerSeg{{th.text, "100%"}}},
		{segs: []headerSeg{{th.text, "69°C"}}},
		{segs: []headerSeg{{th.text, "344W"}}, drop: 1},
	}
	if got := strings.TrimRight(resourceHeader(th, 16, "GPU0", gpu), " "); got != "GPU0  100%  69°C" {
		t.Errorf("GPU header at 16: %q, want power dropped", got)
	}
}

// TestGraphRidgeOverBody pins the graph's painting (TTP-39, user 2026-09-13:
// "너무 배낀거 같지는 않게", "너무 색상 튀지않게"). A column's topmost lit
// cell is the ridge, every lit cell under it is the body, and the body is the
// dimmer of the two, so a GPU at 85 % reads as a line with depth rather than a
// slab. Neither shade is the full accent.
func TestGraphRidgeOverBody(t *testing.T) {
	rows := Graph([]float64{90, 10}, 100, 2, 3, GraphBlock)
	// Column 0 is at 90 %: lit in all three rows, ridge at the top.
	// Column 1 is at 10 %: lit in the bottom row only, which is its ridge.
	kinds := func(r int) []int {
		above := ""
		if r > 0 {
			above = rows[r-1]
		}
		c, a, f := []rune(rows[r]), []rune(above), []rune(rows[len(rows)-1])
		return []int{graphCellKind(c, a, f, 0), graphCellKind(c, a, f, 1)}
	}
	// Column 1's unlit cells above its ridge are track: it was measured.
	for r, want := range [][]int{{cellRidge, cellTrack}, {cellBody, cellTrack}, {cellBody, cellRidge}} {
		if got := kinds(r); got[0] != want[0] || got[1] != want[1] {
			t.Errorf("row %d (%q) kinds = %v, want %v", r, rows[r], got, want)
		}
	}

	// A column with no sample is blank all the way down, never track.
	unseen := Graph([]float64{50}, 100, 2, 3, GraphBlock)
	for r := range unseen {
		c, f := []rune(unseen[r]), []rune(unseen[len(unseen)-1])
		if k := graphCellKind(c, nil, f, 0); k != cellBlank {
			t.Errorf("row %d of an unsampled column is kind %d, want blank", r, k)
		}
	}

	th := ColourTheme()
	full := styleHex(th.accent)
	body, ridge := styleHex(th.accentLow), styleHex(th.graphRidge)
	if body == full || ridge == full {
		t.Errorf("a graph shade is the full accent: body %s ridge %s", body, ridge)
	}
	if relLuminance(body) >= relLuminance(ridge) {
		t.Errorf("the body (%s) is not dimmer than the ridge (%s)", body, ridge)
	}
	if track := styleHex(th.darkFill); relLuminance(track) >= relLuminance(body) {
		t.Errorf("the track (%s) is not dimmer than the body (%s)", track, body)
	}
}
