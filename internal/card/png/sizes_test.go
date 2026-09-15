package png

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// wsRigSummary is the example run wearing the ws box's rig strings: the
// 41-character Threadripper name and the two-card mixed GPU list are the
// longest real rows the footer grid has ever been given, and they are what
// decided the three-column reflow (TTP-54, 2026-09-14). They are literals here
// rather than read from scratch/wsreal so the contract survives without the
// tapes.
func wsRigSummary() *tape.RunSummary {
	s := card.Example()
	s.Host.CPU = "AMD Ryzen Threadripper PRO 5975WX 32-Cores"
	s.Host.CPUCores, s.Host.CPUThreads = 32, 64
	s.Host.RAMBytes = 252 * gib
	s.Host.RAMSpeed = ""
	s.Host.GPUs = []tape.GPUInfo{
		{Name: "NVIDIA RTX A6000", VRAMBytes: 48 * gib},
		{Name: "NVIDIA GeForce RTX 3090", VRAMBytes: 24 * gib},
	}
	return s
}

// footerSlack lives in theme.go since 2026-09-15: the model column's wrap
// (modelFooterRows) measures against the same clearance this test does, so the
// number is production's to own. Still 8.

// contentOf builds the content the way a render does, canvas and all: since
// 2026-09-15 the footer's model column measures its rows during build
// (modelFooterRows), so a build handed no canvas would return strings the card
// never draws.
func contentOf(t *testing.T, s *tape.RunSummary) *content {
	t.Helper()
	fs, err := newFontSet()
	if err != nil {
		t.Fatalf("newFontSet: %v", err)
	}
	defer fs.Close()
	return build(newCanvas(fs), s)
}

// TestFooterRowsFitTheirColumn is the TTP-54 contract in numbers (2026-09-14,
// user: "png카드가 너무 트위터에서 보기에 글씨가 작아"). The body type grew
// about a quarter, so the grid had to lose a column; this is the measurement
// that says the three that remain are wide enough for the rows they carry.
//
// It reads the advance width rather than the drawn mark, because a cell that
// was truncated has a mark that fits by construction. FAIL-first: on the
// four-column grid the ws rig's CPU name measured 294 px against a 255 px
// column and came out as "AMD Ryzen Threadripper PRO 5975W…".
func TestFooterRowsFitTheirColumn(t *testing.T) {
	fs, err := newFontSet()
	if err != nil {
		t.Fatalf("newFontSet: %v", err)
	}
	defer fs.Close()
	cv := newCanvas(fs)

	sums := fixtures()
	sums["sharded"] = card.ExampleSharded()
	sums["sweep"] = card.ExampleSweep()
	sums["ws rig"] = wsRigSummary()

	for name, s := range sums {
		t.Run(name, func(t *testing.T) {
			ct := build(cv, s)
			for i, col := range ct.cols {
				if got := cv.measure(col.label, stColLabel); got > footerColW-footerSlack {
					t.Errorf("col%d label %q measures %dpx, want <= %dpx",
						i, col.label, got, footerColW-footerSlack)
				}
				for r, row := range col.rows {
					style := stSmall
					if r == 0 {
						style = stSmallB
					}
					if got := cv.measure(row, style); got > footerColW-footerSlack {
						t.Errorf("col%d row%d %q measures %dpx, want <= %dpx",
							i, r, row, got, footerColW-footerSlack)
					}
				}
			}
		})
	}
}

// TestFooterCellsAreNotTruncated is the drawn half of the same contract: the
// grid is the part of the card that names the rig and the engine, and an
// ellipsis in it is a field the reader cannot use.
func TestFooterCellsAreNotTruncated(t *testing.T) {
	sums := fixtures()
	sums["ws rig"] = wsRigSummary()
	for name, s := range sums {
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			for i := 0; i < footerCols; i++ {
				for _, part := range []string{"label", "row0", "row1", "row2", "row3"} {
					m, ok := c.markByID(colID(i, part))
					if !ok {
						t.Fatalf("%s was never drawn", colID(i, part))
					}
					if strings.HasSuffix(m.Text, ellipsis) {
						t.Errorf("%s was truncated: %q", m.ID, m.Text)
					}
				}
			}
		})
	}
}

// TestHeroSubLinesAreNotCut is why sizeBody is 15 and not 16. The two lines
// under each hero number carry the clauses the other tracks put there — the
// draft acceptance rate, the rounds median, the sweep's verdict, the TTFT
// spread — and every one of them has to survive the size increase whole.
// FAIL-first: at 16 px the speculative draft clause measures 520 px against a
// 504 px column and comes out "…60% accept…".
func TestHeroSubLinesAreNotCut(t *testing.T) {
	sums := fixtures()
	sums["sharded"] = card.ExampleSharded()
	sums["sweep"] = card.ExampleSweep()
	for name, s := range sums {
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			for _, id := range []string{
				"hero.left.sub1", "hero.left.sub2", "hero.right.sub1", "hero.right.sub2",
			} {
				m, ok := c.markByID(id)
				if !ok {
					t.Fatalf("%s was never drawn", id)
				}
				if strings.HasSuffix(m.Text, ellipsis) {
					t.Errorf("%s was truncated: %q", id, m.Text)
				}
			}
		})
	}
}

// TestStripCarriesEnvironmentAndCredit is the bottom strip's contract after
// the reflow (TTP-54 and TTP-62, 2026-09-14): three lines, none of them
// overlapping, all inside the panel, and the two provenance strings still on
// the last one. The credit is the line that says where the card came from, and
// a reflow that quietly dropped it would be the worst outcome of this round.
func TestStripCarriesEnvironmentAndCredit(t *testing.T) {
	for name, s := range fixtures() {
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			flags, ok := c.markByID("strip.flags")
			if !ok {
				t.Fatal("strip.flags was never drawn")
			}
			env, ok := c.markByID("strip.env")
			if !ok {
				t.Fatal("strip.env was never drawn")
			}
			mark, ok := c.markByID("strip.verified")
			if !ok {
				t.Fatal("strip.verified was never drawn")
			}
			credit, ok := c.markByID("strip.credit")
			if !ok {
				t.Fatal("strip.credit was never drawn")
			}
			if mark.Text != "VERIFIED BY TOKTAPE" {
				t.Errorf("the mark = %q, want the small-caps %q", mark.Text, "VERIFIED BY TOKTAPE")
			}
			if credit.Text != creditText {
				t.Errorf("the credit = %q, want %q uncut", credit.Text, creditText)
			}
			// The three lines stack without touching, and the last one clears
			// the panel edge.
			if env.Rect.Min.Y < flags.Rect.Max.Y {
				t.Errorf("the environment line (%v) overlaps the flags line (%v)", env.Rect, flags.Rect)
			}
			if credit.Rect.Min.Y < env.Rect.Max.Y {
				t.Errorf("the credit line (%v) overlaps the environment line (%v)", credit.Rect, env.Rect)
			}
			if credit.Rect.Max.Y > panelBottom {
				t.Errorf("the credit line ends at y=%d, past the panel (%d)", credit.Rect.Max.Y, panelBottom)
			}
			// The mark and the credit share a row and must not run into each
			// other.
			if mark.Rect.Max.X >= credit.Rect.Min.X {
				t.Errorf("the mark (%q, to x=%d) runs into the credit (from x=%d)",
					mark.Text, mark.Rect.Max.X, credit.Rect.Min.X)
			}
		})
	}
}

// TestEnvironmentLineKeepsTheVerdictsAndCutsTheGPUsFirst pins the order the
// environment line was built in. The throttle and contention verdicts are
// claims about the machine and are always printed, labelled; the GPU state is
// the only part that grows with the rig, so it goes last and is what a long
// line loses.
func TestEnvironmentLineKeepsTheVerdictsAndCutsTheGPUsFirst(t *testing.T) {
	s := card.Example()
	s.GPUsAtEnd = nil
	for i := 0; i < 16; i++ {
		s.GPUsAtEnd = append(s.GPUsAtEnd, tape.GPUSample{Index: i, TempC: 68, PowerW: 340})
	}
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("strip.env")
	if !ok {
		t.Fatal("strip.env was never drawn")
	}
	if !strings.HasSuffix(m.Text, ellipsis) {
		t.Fatalf("a sixteen-GPU environment line was not truncated: %q", m.Text)
	}
	for _, want := range []string{"throttled no", "contended no", "2026-09-13"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("the environment line lost %q: %q", want, m.Text)
		}
	}
	if m.Rect.Max.X > contentR {
		t.Errorf("the environment line ends at x=%d, past the content box (%d)", m.Rect.Max.X, contentR)
	}
}

// TestEnvironmentLineHoldsTheConditionsClause (TTP-57, 2026-09-14). The
// conditions clause is the third claim about the machine and joins the
// environment line beside the throttle and contention verdicts. It is ~37
// columns, so this pins that the line still fits on a real rig, and that on a
// rig long enough to cut, the clause outlives the GPU state — the cap is what
// explains the rate, the GPU temperatures are not.
func TestEnvironmentLineHoldsTheConditionsClause(t *testing.T) {
	s := card.Example()
	s.Contention.Witnesses = []tape.ContentionWitness{
		{Edge: "start", CPUMaxKHz: 3600000, TempC: 41, TempSensor: "k10temp Tctl"},
		{Edge: "end", CPUMaxKHz: 2700000, TempC: 50, TempSensor: "k10temp Tctl"},
	}
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("strip.env")
	if !ok {
		t.Fatal("strip.env was never drawn")
	}
	if strings.HasSuffix(m.Text, ellipsis) {
		t.Errorf("a two-GPU rig lost the end of its environment line: %q", m.Text)
	}
	if !strings.Contains(m.Text, "cap 3.6 → 2.7 GHz") {
		t.Errorf("the environment line does not say the cap moved: %q", m.Text)
	}
	if m.Rect.Max.X > contentR {
		t.Errorf("the environment line ends at x=%d, past the content box (%d)", m.Rect.Max.X, contentR)
	}
	t.Logf("environment line ends at x=%d of %d: %q", m.Rect.Max.X, contentR, m.Text)

	// A run whose machine did not move says nothing, rather than "?" or a
	// pair of identical numbers.
	quiet := card.Example()
	c2, err := renderCanvas(quiet)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	if m2, ok := c2.markByID("strip.env"); ok && strings.Contains(m2.Text, "cap ") {
		t.Errorf("an unchanged machine printed a conditions clause: %q", m2.Text)
	}
}
