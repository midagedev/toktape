package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// memoryLines is the MEMORY block of a card, without its gutter.
func memoryLines(s *tape.RunSummary) []string {
	var out []string
	keep := false
	for _, line := range strings.Split(Text(s), "\n") {
		body := strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │")
		switch {
		case strings.HasPrefix(body, "MEMORY "):
			keep = true
		case keep && !strings.HasPrefix(body, strings.Repeat(" ", blockLabelW)):
			keep = false
		}
		if keep {
			out = append(out, strings.TrimSpace(body))
		}
	}
	return out
}

// hasLine reports whether any of lines is exactly want.
func hasLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// TestHostPlacedSeparatesRAMFromDisk is TTP-63 (user 2026-09-14: "cpu 388g
// 찍혀있는데 이게 ram이랑 nvme랑 구분이 안되나?").
//
// The card printed the VRAM figures and the process RSS and nothing that said
// how much of the CPU placement was actually in RAM. On the ws rig that is the
// whole question: 388.1 GiB is placed on a 252 GB box, so ~193 GiB comes back
// from the model file on every touch. The three cases are the three readings
// tape.Residency can produce, and the card must not invent the ones it cannot
// take: a remote server has no /proc sample and gets the placed figure alone,
// never a zero for what is paged.
func TestHostPlacedSeparatesRAMFromDisk(t *testing.T) {
	const gib = 1 << 30

	// The ws figures: 388 GiB on the CPU, 195 GiB of the mapping resident.
	withCPU := func(placed, rssFile int64) *tape.RunSummary {
		s := Example()
		s.Placement.Devices = append(s.Placement.Devices, tape.DevicePlacement{
			Device:  tape.DeviceCPU,
			Bytes:   placed,
			Classes: map[tape.TensorClass]int64{tape.ClassExperts: placed},
			Layers:  "0-39 experts",
		})
		s.Memory.AtEnd.RSSBytes = rssFile + gib
		s.Memory.AtEnd.RSSFileBytes = rssFile
		return s
	}

	t.Run("a placement larger than RAM says what is on disk", func(t *testing.T) {
		got := memoryLines(withCPU(388*gib, 195*gib))
		want := "Host placed 388.0 GiB (195.0 in RAM / 193.0 on disk)"
		if !hasLine(got, want) {
			t.Errorf("no %q in the MEMORY block:\n%s", want, strings.Join(got, "\n"))
		}
	})

	t.Run("a placement that fits says so without a zero", func(t *testing.T) {
		got := memoryLines(withCPU(40*gib, 41*gib))
		if !hasLine(got, "Host placed 40.0 GiB (all in RAM)") {
			t.Errorf("a fully resident placement is not reported as such:\n%s", strings.Join(got, "\n"))
		}
		for _, l := range got {
			if strings.Contains(l, "disk") {
				t.Errorf("nothing is paged, so no line may name the disk: %q", l)
			}
		}
	})

	t.Run("no /proc sample prints the placed figure alone", func(t *testing.T) {
		s := withCPU(388*gib, 195*gib)
		// A remote server: the placement was derived from the command line,
		// the process was never read.
		s.Memory.AtEnd = tape.MemSample{}
		got := memoryLines(s)
		if !hasLine(got, "Host placed 388.0 GiB") {
			t.Errorf("the placed figure is missing:\n%s", strings.Join(got, "\n"))
		}
		for _, l := range got {
			if strings.Contains(l, "in RAM") || strings.Contains(l, "on disk") {
				t.Errorf("the split was not observed and must not be printed: %q", l)
			}
		}
	})

	t.Run("nothing on the CPU has no line at all", func(t *testing.T) {
		// Example() is fully offloaded to two GPUs.
		for _, l := range memoryLines(Example()) {
			if strings.Contains(l, "Host placed") {
				t.Errorf("a fully offloaded run has no host placement: %q", l)
			}
		}
	})
}

// TestHostPlacedUsesTheSchemaDerivation pins that the card does not re-derive
// the split. Every surface reads tape.Residency so the pane, the modal, the
// card and the PNG cannot drift apart; a second copy of "resident =
// min(rss_file, placed)" in this package is how they would.
func TestHostPlacedUsesTheSchemaDerivation(t *testing.T) {
	const gib = 1 << 30
	s := Example()
	s.Placement.Devices = append(s.Placement.Devices, tape.DevicePlacement{
		Device: tape.DeviceCPU, Bytes: 388 * gib,
	})
	s.Memory.AtEnd.RSSBytes = 196 * gib
	s.Memory.AtEnd.RSSFileBytes = 195 * gib

	r := tape.Residency(s.Placement, s.Memory.AtEnd)
	want := "Host placed " + formatGiB(r.Placed) +
		" (" + formatGiBNum(r.Resident) + " in RAM / " + formatGiBNum(r.Paged) + " on disk)"
	if !hasLine(memoryLines(s), want) {
		t.Errorf("the card's line is not tape.Residency's figures (%+v); want %q", r, want)
	}
}
