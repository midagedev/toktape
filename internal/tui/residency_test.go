package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// The ws rig's figures, which are what TTP-63 is about (user 2026-09-14: "cpu
// 388g 찍혀있는데 이게 ram이랑 nvme랑 구분이 안되나?"): 388 GiB placed on the
// CPU of a 252 GB box, 195 GiB of the mapping resident, so 193 GiB comes back
// from the model file on every touch.
const (
	testGiB      = int64(1) << 30
	testPlaced   = 388 * testGiB
	testResident = 195 * testGiB
)

// residencyModel is a run with a host placement larger than RAM. perTok is the
// major-fault average, which is what decides whether the paged share is an
// alarm; with no streams majFaultsPerToken falls back to this summary figure.
func residencyModel(perTok float64) Model {
	return Model{Summary: tape.RunSummary{
		Host: tape.HostInfo{RAMBytes: 252 * testGiB},
		Placement: tape.PlacementSummary{Devices: []tape.DevicePlacement{
			{Device: tape.DeviceCPU, Bytes: testPlaced},
		}},
		Memory: tape.MemorySummary{
			AtEnd:             tape.MemSample{RSSBytes: testResident + testGiB, RSSFileBytes: testResident},
			MajFaultsPerToken: perTok,
		},
	}}
}

// hostPlainRows renders the placement section without the palette.
func hostPlainRows(m Model, cw int) []string {
	return placementRows(m, PlainTheme(), 0, cw)
}

// TestHostBarIsPlacementNotRAM is the shape of the fix. The CPU row used to be
// placed ÷ RAM — 1.54 on this rig, clamped to a full bar, which is the shape of
// "it fits" — and carried no legend. It is now the placement measured against
// itself, split into what is in RAM and what is not.
func TestHostBarIsPlacementNotRAM(t *testing.T) {
	const cw = 37
	rows := hostPlainRows(residencyModel(41.1), cw)
	if len(rows) != 2 {
		t.Fatalf("want a bar row and a legend row, got %d:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if !strings.Contains(rows[0], "CPU") || !strings.Contains(rows[0], "388G") {
		t.Errorf("the bar row lost its label or its placed figure:\n%s", rows[0])
	}
	// The legend names both halves, in that order, with the figures the
	// schema's derivation produced.
	r := tape.Residency(residencyModel(0).Summary.Placement, residencyModel(0).Summary.Memory.AtEnd)
	want := "▆ ram " + fmtG(r.Resident) + "  ▆ disk " + fmtG(r.Paged)
	if !strings.Contains(rows[1], want) {
		t.Errorf("the legend row is not %q:\n%q", want, rows[1])
	}

	// The bar's full width is the placement, so every cell in it is ink: what
	// the old row drew as an empty remainder is now the paged share, and the
	// reader can see how much of the placement it is.
	const labelW, valueW = 5, 7
	barW := cw - labelW - valueW - 1
	bar := string([]rune(rows[0])[labelW : labelW+barW])
	if n := strings.Count(bar, string(barGlyph)); n != barW {
		t.Errorf("the bar is %d cells of %d; the placement is the whole width", n, barW)
	}
	// Both shares take cells, in the proportion the reading gives: 195 of 388
	// is a little over half.
	segs := segmentBar([]float64{float64(r.Resident), float64(r.Paged)}, float64(r.Placed), barW)
	if len(segs[0]) == 0 || len(segs[1]) == 0 {
		t.Fatalf("both shares must take cells, got %q and %q", segs[0], segs[1])
	}
	if segs[2] != "" {
		t.Errorf("the bar has an empty remainder %q; the two shares are the whole placement", segs[2])
	}
}

// TestPagedShareIsWarmOnlyWhenCold: the warm hue is the palette's alarm
// (CLAUDE.md) and the thing it alarms about is weights being read from disk
// during decode. A rig that keeps a little of the mapping out of RAM and never
// faults while decoding is working fine and must not be lit.
func TestPagedShareIsWarmOnlyWhenCold(t *testing.T) {
	const cw = 37
	const labelW = 5
	th := ColourTheme()
	warm, dim := styleHex(th.warn), styleHex(th.dim)

	// The last cell of the bar is in the paged share: it is the far end of the
	// placement, which is the part that is not resident.
	shadeAt := func(perTok float64) string {
		rows := placementRows(residencyModel(perTok), th, 0, cw)
		cells := parseFrame(strings.Join(rows, "\n"), cw, len(rows))
		for x := cw - 1; x > labelW; x-- {
			if cells[0][x].r == barGlyph {
				return cells[0][x].fg
			}
		}
		t.Fatalf("no bar cell on the row:\n%s", rows[0])
		return ""
	}

	if got := shadeAt(tape.ColdMajFaultsPerToken + 40); got != warm {
		t.Errorf("a cold run's paged share is %s, want the warm hue %s", got, warm)
	}
	if got := shadeAt(tape.ColdMajFaultsPerToken / 3); got != dim {
		t.Errorf("a warm run's paged share is %s, want dim %s", got, dim)
	}
}

// TestNoProcSampleKeepsThePlainBar: a remote server has a placement and no
// /proc reading, so the split is not derivable. The row is what it always was
// and there is no legend — "disk 0G" would be a figure nobody observed
// (CLAUDE.md: unknown prints "?", never a plausible default).
func TestNoProcSampleKeepsThePlainBar(t *testing.T) {
	m := residencyModel(41.1)
	m.Summary.Memory.AtEnd = tape.MemSample{}
	rows := hostPlainRows(m, 37)
	if len(rows) != 1 {
		t.Fatalf("want one row and no legend, got %d:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	for _, forbidden := range []string{"ram", "disk"} {
		if strings.Contains(rows[0], forbidden) {
			t.Errorf("the split was not observed and must not be named: %q", rows[0])
		}
	}
	if !strings.Contains(rows[0], "388G") {
		t.Errorf("the placed figure is still a reading and must print:\n%s", rows[0])
	}
}

// TestFullyResidentHostNamesOnlyRAM: everything placed is in RAM, so the
// legend has one entry. "disk 0G" would read as an alarm that is not there.
func TestFullyResidentHostNamesOnlyRAM(t *testing.T) {
	m := residencyModel(0.1)
	m.Summary.Memory.AtEnd = tape.MemSample{RSSBytes: testPlaced + testGiB, RSSFileBytes: testPlaced + testGiB}
	rows := hostPlainRows(m, 37)
	if len(rows) != 2 {
		t.Fatalf("want a bar and a legend, got %d:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if !strings.Contains(rows[1], "▆ ram 388G") {
		t.Errorf("the legend does not name the resident placement:\n%q", rows[1])
	}
	if strings.Contains(rows[1], "disk") {
		t.Errorf("nothing is paged, so the legend must not name the disk:\n%q", rows[1])
	}
}

// TestNoHostPlacementDrawsNoHostRow: a fully offloaded model has nothing on the
// CPU, and the section says nothing rather than "0G".
func TestNoHostPlacementDrawsNoHostRow(t *testing.T) {
	m := residencyModel(41.1)
	m.Summary.Placement.Devices = nil
	if rows := hostPlainRows(m, 37); len(rows) != 0 {
		t.Errorf("nothing is placed on the host, so there is no host row:\n%s", strings.Join(rows, "\n"))
	}
}

// TestHostRowsFitEveryPaneWidth: the legend carries two figures and the pane is
// as narrow as 28 columns. Neither figure may be lost to the clip, and the row
// is still exactly cw columns.
func TestHostRowsFitEveryPaneWidth(t *testing.T) {
	m := residencyModel(41.1)
	// The narrowest figures are the widest strings: "39.3G" is five columns
	// where "195G" is four.
	m.Summary.Placement.Devices = []tape.DevicePlacement{{Device: tape.DeviceCPU, Bytes: 79 * testGiB}}
	m.Summary.Memory.AtEnd = tape.MemSample{RSSBytes: 40 * testGiB, RSSFileBytes: 39*testGiB + testGiB/3}
	for _, cw := range []int{28, 37, 42} {
		rows := hostPlainRows(m, cw)
		if len(rows) != 2 {
			t.Fatalf("cw=%d: got %d rows", cw, len(rows))
		}
		for i, row := range rows {
			if got := width(row); got != cw {
				t.Errorf("cw=%d: row %d is %d columns", cw, i, got)
			}
		}
		r := tape.Residency(m.Summary.Placement, m.Summary.Memory.AtEnd)
		for _, want := range []string{"ram " + fmtG(r.Resident), "disk " + fmtG(r.Paged)} {
			if !strings.Contains(rows[1], want) {
				t.Errorf("cw=%d: the legend lost %q:\n%q", cw, want, rows[1])
			}
		}
	}
}

// TestHostBarEasesOnTheResidentShare: what moves during a run is how much of
// the mapping is in RAM, so that is what tweens — the same way a GPU bar tweens
// on the bytes in VRAM. A frame taken between two samples must sit between
// their two readings, not jump to the newer one.
func TestHostBarEasesOnTheResidentShare(t *testing.T) {
	m := residencyModel(41.1)
	m.Samples = []tape.RunSample{
		{T: time.Second, Mem: tape.MemSample{RSSBytes: 100 * testGiB, RSSFileBytes: 100 * testGiB}},
		{T: 2 * time.Second, Mem: tape.MemSample{RSSBytes: testResident + testGiB, RSSFileBytes: testResident}},
	}
	mid := placementRows(m, PlainTheme(), 2*time.Second+easeDur/3, 37)
	settled := placementRows(m, PlainTheme(), 2*time.Second+easeDur*2, 37)
	if len(mid) != 2 || len(settled) != 2 {
		t.Fatalf("want two rows at both instants")
	}
	if mid[1] == settled[1] {
		t.Errorf("the legend did not move between the tween and its end:\n%q", mid[1])
	}
	if !strings.Contains(settled[1], "ram "+fmtG(testResident)) {
		t.Errorf("the settled legend is not the newer sample's reading:\n%q", settled[1])
	}
}

// TestModalSplitsTheHostSegment: the modal is the clip's last frame, and a
// single CPU segment there reads as "and the rest is in RAM". It is split the
// same way the pane's bar is, and the legend keeps one entry per device with
// both figures on the host's.
func TestModalSplitsTheHostSegment(t *testing.T) {
	p := tape.PlacementSummary{Devices: []tape.DevicePlacement{
		{Device: tape.DeviceCPU, Bytes: testPlaced},
		{Device: "GPU0", Bytes: 32 * testGiB},
	}}
	sample := tape.MemSample{RSSBytes: testResident + testGiB, RSSFileBytes: testResident}
	r := tape.Residency(p, sample)

	rows := placementLines(PlainTheme(), p, r, false, 84, 2)
	if len(rows) != 2 {
		t.Fatalf("want a bar and a legend, got %d", len(rows))
	}
	want := "▆ CPU " + fmtG(r.Resident) + " ram · " + fmtG(r.Paged) + " disk"
	if !strings.Contains(rows[1], want) {
		t.Errorf("the legend does not carry the host split %q:\n%q", want, rows[1])
	}
	if !strings.Contains(rows[1], "▆ GPU0 "+fmtG(32*testGiB)) {
		t.Errorf("the legend lost a device:\n%q", rows[1])
	}

	// Not derivable: the modal says what it always said.
	plain := placementLines(PlainTheme(), p, tape.Residency(p, tape.MemSample{}), false, 84, 2)
	if len(plain) != 2 {
		t.Fatalf("want a bar and a legend, got %d", len(plain))
	}
	if !strings.Contains(plain[1], "▆ CPU "+fmtG(testPlaced)) {
		t.Errorf("the unsplit legend is not the placed figure:\n%q", plain[1])
	}
	for _, forbidden := range []string{"ram", "disk"} {
		if strings.Contains(plain[1], forbidden) {
			t.Errorf("the split was not observed and must not be named:\n%q", plain[1])
		}
	}
}

// TestModalAlarmsWhenTheRunIsCold (TTP-63, 2026-09-14). The pane paints the
// paged share the warm hue while the weights are being read back from the
// file; the modal is the frame a reader looks at longest, so it says the same
// thing rather than leaving the alarm on the screen nobody screenshots.
func TestModalAlarmsWhenTheRunIsCold(t *testing.T) {
	th := ColourTheme()
	p := tape.PlacementSummary{Devices: []tape.DevicePlacement{
		{Device: tape.DeviceCPU, Bytes: testPlaced},
		{Device: "GPU0", Bytes: 32 * testGiB},
	}}
	r := tape.Residency(p, tape.MemSample{RSSBytes: testResident + testGiB, RSSFileBytes: testResident})

	// The host's paged share is the second segment of the bar; the cell just
	// before the GPU's is inside it.
	const w = 84
	shadeAt := func(cold bool) string {
		rows := placementLines(th, p, r, cold, w, 2)
		if len(rows) != 2 {
			t.Fatalf("want a bar and a legend, got %d", len(rows))
		}
		cells := parseFrame(strings.Join(rows, "\n"), w, len(rows))
		host := int(float64(w-4) * float64(r.Placed) / float64(r.Placed+32*testGiB))
		return cells[0][host-1].fg
	}
	if got, want := shadeAt(true), styleHex(th.warn); got != want {
		t.Errorf("a cold run's paged share is %s, want the warm hue %s", got, want)
	}
	if got, want := shadeAt(false), styleHex(th.darkFill); got != want {
		t.Errorf("a warm run's paged share is %s, want the empty shade %s", got, want)
	}
}

// TestModalSaysWhereTheWeightsLive (2026-09-14, user: "통합메모리 계열인지 순수
// vram인지 오프로딩인지 좀 더 명확하게 갈리도록 … 모델도 모델 크기와 양자화가
// 잘 나와야 되고 날짜도").
//
// Three different machines produce three different cards, and the reader has
// to be told which before the rates mean anything. The model line carries the
// quant and the size with the name, and the run carries its date.
func TestModalSaysWhereTheWeightsLive(t *testing.T) {
	const gib = 1 << 30
	base := ModelAt(ExampleTapeN(2), doneAt)
	base.Mode = ModeCard
	base.Summary.Model.FileBytes = 440 * gib
	base.Summary.Model.Quant = "Q3_K_M"
	base.Summary.StartedAt = time.Date(2026, 9, 14, 7, 4, 58, 0, time.Local)

	frameOf := func(devices []tape.DevicePlacement) string {
		m := base
		m.Summary.Placement = tape.PlacementSummary{Devices: devices}
		return card.StripANSI(View(m, doneAt, 120, 36))
	}

	gpu := []tape.DevicePlacement{{Device: "GPU0", Bytes: 40 * gib}}
	host := []tape.DevicePlacement{{Device: tape.DeviceCPU, Bytes: 388 * gib}}
	for name, tc := range map[string]struct {
		devices []tape.DevicePlacement
		want    string
		absent  string
	}{
		"all on the cards":  {gpu, "all in VRAM", "offloaded"},
		"all in host RAM":   {host, "all in host RAM", "offloaded"},
		"experts offloaded": {append(append([]tape.DevicePlacement{}, gpu...), host...), "offloaded · 9% in VRAM · 91% in host RAM", "all in VRAM"},
	} {
		got := frameOf(tc.devices)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: the modal does not say %q:\n%s", name, tc.want, got)
		}
		if strings.Contains(got, tc.absent) {
			t.Errorf("%s: the modal says %q as well", name, tc.absent)
		}
	}

	// A placement nobody derived says nothing rather than guessing a shape.
	if got := frameOf(nil); strings.Contains(got, "in VRAM") || strings.Contains(got, "offloaded") {
		t.Errorf("an underived placement was given a shape:\n%s", got)
	}

	full := frameOf(gpu)
	for _, want := range []string{"Q3_K_M", "440G", "2026-09-14"} {
		if !strings.Contains(full, want) {
			t.Errorf("the modal does not carry %q:\n%s", want, full)
		}
	}

	// No StartedAt, no date: a made-up day on a card is worse than none.
	undated := base
	undated.Summary.StartedAt = time.Time{}
	undated.Summary.Placement = tape.PlacementSummary{Devices: gpu}
	if got := card.StripANSI(View(undated, doneAt, 120, 36)); strings.Contains(got, "2026-09-14") {
		t.Errorf("an undated run was given a date:\n%s", got)
	}
}
