package png

import (
	"image/color"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// hostPlacedSummary is the example run with a CPU placement of placed bytes and
// a file-backed resident set of rssFile, the two figures tape.Residency reads.
// rss is passed separately because a sample with no RSSBytes at all is no
// reading, whatever RSSFileBytes says.
func hostPlacedSummary(placed, rss, rssFile int64) *tape.RunSummary {
	s := card.Example()
	s.Placement.Devices = append(s.Placement.Devices, tape.DevicePlacement{
		Device:  tape.DeviceCPU,
		Bytes:   placed,
		Classes: map[tape.TensorClass]int64{tape.ClassExperts: placed},
	})
	s.Memory.AtEnd.RSSBytes = rss
	s.Memory.AtEnd.RSSFileBytes = rssFile
	return s
}

// hostSegmentOf returns the bar segment drawn for the CPU placement, which is
// always the last device segment the example builds.
func hostSegmentOf(t *testing.T, s *tape.RunSummary) (*canvas, segment) {
	t.Helper()
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	ct := build(s)
	for _, seg := range ct.segments {
		if seg.col == colHost || len(seg.parts) == 2 {
			return c, seg
		}
	}
	t.Fatal("no host segment was built")
	return nil, segment{}
}

// TestHostSegmentSplitsRAMFromDisk is TTP-63 (2026-09-14, user: "cpu 388g
// 찍혀있는데 이게 ram이랑 nvme랑 구분이 안되나?"). A CPU placement is
// llama.cpp's backend assignment, not a residency: on the ws rig 388.1 GiB is
// placed on a 252 GB box and the rest is read from the model file on every
// touch. One sand block said the opposite.
//
// The three cases are the three things the tape can say. FAIL-first: before
// this change the host was one block for all three, so the two-part case drew
// no memory.bar.seg2.part1 and the legend read "CPU 388.0 GiB".
func TestHostSegmentSplitsRAMFromDisk(t *testing.T) {
	t.Run("more placed than resident draws two parts", func(t *testing.T) {
		// The ws code tape's shape, rounded: 388 GiB placed, 195 of it in RAM.
		s := hostPlacedSummary(388*gib, 196*gib, 195*gib)
		c, seg := hostSegmentOf(t, s)
		if len(seg.parts) != 2 {
			t.Fatalf("host segment has %d parts, want 2", len(seg.parts))
		}
		if seg.parts[0].bytes != 195*gib || seg.parts[1].bytes != 193*gib {
			t.Errorf("parts = %d / %d bytes, want %d resident and %d paged",
				seg.parts[0].bytes, seg.parts[1].bytes, 195*gib, 193*gib)
		}
		if !hasLegendEntry(c, "ram 195.0 GiB") || !hasLegendEntry(c, "disk 193.0 GiB") {
			t.Errorf("legend = %v, want a ram and a disk entry", legendEntries(c))
		}
		// "disk", never "NVMe": what was observed is that the pages are not
		// resident, not what they come back from.
		for _, e := range legendEntries(c) {
			if e == "CPU 388.0 GiB" {
				t.Error("the legend still names the whole CPU placement as one figure")
			}
		}
	})

	t.Run("everything resident is one block that says ram", func(t *testing.T) {
		s := hostPlacedSummary(40*gib, 42*gib, 40*gib)
		c, seg := hostSegmentOf(t, s)
		if len(seg.parts) != 0 {
			t.Errorf("host segment has %d parts, want one whole block", len(seg.parts))
		}
		if !hasLegendEntry(c, "ram 40.0 GiB") {
			t.Errorf("legend = %v, want a single ram entry", legendEntries(c))
		}
	})

	t.Run("no /proc sample keeps the block the card always had", func(t *testing.T) {
		// A remote server: the placement was read from the GGUF and the args,
		// but nothing sampled the process. Printing 0 paged for it would be a
		// default nobody observed.
		s := hostPlacedSummary(388*gib, 0, 0)
		c, seg := hostSegmentOf(t, s)
		if len(seg.parts) != 0 {
			t.Errorf("host segment has %d parts, want one whole block", len(seg.parts))
		}
		if !hasLegendEntry(c, "CPU 388.0 GiB") {
			t.Errorf("legend = %v, want the device's own entry", legendEntries(c))
		}
		for _, e := range legendEntries(c) {
			if e == "disk ?" || e == "disk 0.0 GiB" {
				t.Errorf("legend entry %q invents a figure for an unsampled process", e)
			}
		}
	})
}

// TestHostShadesAreDistinguishable: the two halves are one hue at two
// lightnesses, and if they are not far enough apart the split is invisible and
// the ticket is not closed. The threshold is TestShadeSteps' — the same "they
// will read as one block" floor the VRAM steps are held to.
func TestHostShadesAreDistinguishable(t *testing.T) {
	lum := func(c color.RGBA) float64 {
		return 0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)
	}
	resident, paged := shade(colHost, shadeResident), shade(colHost, shadePaged)
	if resident != colHost {
		t.Errorf("the resident step should keep the host hue exactly: %v != %v", resident, colHost)
	}
	if d := lum(resident) - lum(paged); d < 20 {
		t.Errorf("ram and disk are %.1f apart in luminance; they will read as one block", d)
	}
	// And the paged half must not be mistaken for the never-loaded segment,
	// which is the inert surface colour and means something else entirely.
	if paged == colSurface {
		t.Error("the paged half is the never-loaded colour; the two mean different things")
	}
}

// TestHostSplitIsNotAppliedToSomebodyElsesBytes: tape.Residency sums every CPU
// device in the placement, so the split describes a segment only when that
// segment is the whole of it. A second host device would otherwise be drawn
// with a residency measured against bytes that are not its own.
func TestHostSplitIsNotAppliedToSomebodyElsesBytes(t *testing.T) {
	s := hostPlacedSummary(200*gib, 196*gib, 100*gib)
	s.Placement.Devices = append(s.Placement.Devices, tape.DevicePlacement{
		Device: tape.DeviceCPU,
		Bytes:  188 * gib,
	})
	ct := build(s)
	for _, seg := range ct.segments {
		if seg.col != colHost {
			continue
		}
		if len(seg.parts) != 0 {
			t.Errorf("host segment %q was split against a residency over every CPU device", seg.label)
		}
	}
}

// TestLegendFitsWithTheHostSplit is the headroom this change spent. drawLegend
// drops an entry that will not fit rather than overprinting it, so a legend
// that has run out of width loses a figure silently — and the host now
// contributes two entries where it contributed one. The worst realistic shape
// is two GPUs with the VRAM breakdown and a host shortfall in ws-sized
// figures: seven entries, all of which must be drawn.
func TestLegendFitsWithTheHostSplit(t *testing.T) {
	s := hostPlacedSummary(388*gib, 196*gib, 195*gib)
	s.Placement.VRAMKVBytes = 3 * gib
	s.Placement.VRAMComputeBytes = 1536 * (1 << 20)
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	got := legendEntries(c)
	want := []string{
		"GPU0 23.9 GiB", "GPU1 23.1 GiB", "ram 195.0 GiB", "disk 193.0 GiB",
		"weights 42.5 GiB", "kv 3.0 GiB", "compute 1.5 GiB",
	}
	if len(got) != len(want) {
		t.Fatalf("legend drew %d of %d entries — one was dropped for width: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("legend entry %d = %q, want %q", i, got[i], w)
		}
	}
	// And the last swatch must still be inside the content box.
	last, ok := c.markByID("memory.legend.text6")
	if !ok {
		t.Fatal("memory.legend.text6 was never drawn")
	}
	if last.Rect.Max.X > contentR {
		t.Errorf("the legend ends at x=%d, past the content box (%d)", last.Rect.Max.X, contentR)
	}
	t.Logf("seven-entry legend ends at x=%d of %d", last.Rect.Max.X, contentR)
}
