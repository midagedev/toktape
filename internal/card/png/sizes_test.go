package png

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// wsRigSummary is the example run wearing the ws box's rig strings: the
// 41-character Threadripper name and the two-card mixed GPU list are the
// longest real rig the card has ever been given, and they are what the rig
// line's fallback chain exists for (2026-09-19; they decided the footer's
// three-column reflow before that). They are literals here rather than read
// from scratch/wsreal so the contract survives without the tapes.
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

// contentOf builds the content the way a render does. Nothing measures at
// build time since the identity band replaced the footer grid (2026-09-19):
// its lines are picked by pickWidest at draw time, so the builder returns
// preferred strings and fallback lists, and tests that want the picked line
// read the drawn mark.
func contentOf(t *testing.T, s *tape.RunSummary) *content {
	t.Helper()
	return build(s)
}

// bandFixtures is the set every identity-band assertion runs over: the
// structural fixtures plus the shard, sweep, ws-rig and ExLlamaV3 variants
// that stress the band's measured lines (TestIdentityLinesFitItsBand's set,
// named so the later gates iterate the same tapes it does).
func bandFixtures() map[string]*tape.RunSummary {
	sums := fixtures()
	sums["sharded"] = card.ExampleSharded()
	sums["sweep"] = card.ExampleSweep()
	sums["ws rig"] = wsRigSummary()
	sums["exl3"] = card.ExampleExLlamaV3()
	sums["exl3 split"] = card.ExampleExLlamaV3Split()
	return sums
}

// heroTapePath is the recording the lead rendered when the band was reviewed
// (2026-09-19): the one-card form of the ws rig, and the tape whose CPU fell
// out of the rig line whole. It is committed, unlike the scratch/wsreal
// tapes, so this pin survives without them; wsRigSummary carries the same
// strings for the two-card form.
const heroTapePath = "../../../assets/hero.tape"

// heroTapeSummary loads the hero recording, skipping the test when the asset
// is not readable — the cmd package's rule for the same file: another track
// owns the asset, and a missing hero is their problem to report, not a reason
// to fail this one.
func heroTapeSummary(t *testing.T) *tape.RunSummary {
	t.Helper()
	tp, err := tape.Read(heroTapePath)
	if err != nil {
		t.Skipf("the hero recording is not readable here: %v", err)
	}
	return &tp.Summary
}

// TestIdentityLinesFitItsBand is this round's core assertion (2026-09-19): the
// three 26 px identity lines carry what the footer grid's twelve 14 px rows
// carried, on a third of the lines, so every one of them must resolve — by
// preferred or by fallback — to a line that fits the 1080 px content width
// whole. An ellipsis in the band means a field the reader cannot use, exactly
// as it did in a truncated footer cell.
//
// It reads the drawn mark, not the builder's preferred string, because
// "fits" is pickWidest's decision and only the canvas makes it.
func TestIdentityLinesFitItsBand(t *testing.T) {
	for name, s := range bandFixtures() {
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			for _, id := range []string{"ident.model", "ident.modelsub", "ident.rig", "ident.engine"} {
				m, ok := c.markByID(id)
				if !ok {
					t.Fatalf("%s was never drawn", id)
				}
				if strings.HasSuffix(m.Text, ellipsis) {
					t.Errorf("%s was truncated: %q — no fallback of this line fits", id, m.Text)
				}
				if m.Rect.Min.X < contentL || m.Rect.Max.X > contentR {
					t.Errorf("%s (%q) spans x=%d..%d, outside the content box (%d..%d)",
						id, m.Text, m.Rect.Min.X, m.Rect.Max.X, contentL, contentR)
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

// TestStripCarriesItsTwoLines is the bottom strip's contract since the flags
// and environment lines left (2026-09-19): one rule, then the provenance pair
// sharing a row. The credit is the line that says where the card came from,
// and a reflow that quietly dropped it would be the worst outcome of any
// round; this is the check that it survived this one.
func TestStripCarriesItsTwoLines(t *testing.T) {
	for name, s := range fixtures() {
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			rule, ok := c.markByID("rule.strip")
			if !ok {
				t.Fatal("rule.strip was never drawn")
			}
			if rule.Rect.Min.Y != stripRuleY || rule.Rect.Dy() != 1 {
				t.Errorf("the strip rule sits at y=%d..%d, want the 1px rule at %d",
					rule.Rect.Min.Y, rule.Rect.Max.Y, stripRuleY)
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
			if credit.Rect.Max.Y > panelBottom {
				t.Errorf("the credit line ends at y=%d, past the panel (%d)", credit.Rect.Max.Y, panelBottom)
			}
			// The pair shares a row and must not run into each other.
			if mark.Rect.Max.X >= credit.Rect.Min.X {
				t.Errorf("the mark (%q, to x=%d) runs into the credit (from x=%d)",
					mark.Text, mark.Rect.Max.X, credit.Rect.Min.X)
			}
			// The two lines this round deleted exist on no card.
			for _, id := range []string{"strip.flags", "strip.env"} {
				if _, ok := c.markByID(id); ok {
					t.Errorf("%s was drawn on a card that no longer has that line", id)
				}
			}
		})
	}
}

// TestEnvironmentLineKeepsTheVerdictsAndCutsTheGPUsFirst was deleted on
// 2026-09-19 with the line it ordered (category 1). What it protected splits
// two ways: the throttle and contention verdicts, always printed and labelled,
// are pills now (TestThrottledPill, TestContentionReadingIsPrinted), and the
// GPU state — the part that grew with the rig and was cut first — left the
// card for the tape, the run page and -o md.

// TestConditionsClauseIsAPill (TTP-57's clause, 2026-09-19): the conditions
// sentence is the third claim about the machine and joins the throttle and
// contention verdicts where they live now — the pill row beside the placement
// bar, present only when it fired, not truncated, and inside the content box
// on a row that grew to five.
func TestConditionsClauseIsAPill(t *testing.T) {
	s := card.Example()
	s.Contention.Witnesses = []tape.ContentionWitness{
		{Edge: "start", CPUMaxKHz: 3600000, TempC: 41, TempSensor: "k10temp Tctl"},
		{Edge: "end", CPUMaxKHz: 2700000, TempC: 50, TempSensor: "k10temp Tctl"},
	}
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	pill, ok := c.markByID("memory.pill4")
	if !ok {
		t.Fatal("memory.pill4 was never drawn — the conditions clause is the row's fifth pill")
	}
	if !strings.Contains(pill.Text, "cap 3.6 → 2.7 GHz") {
		t.Errorf("the conditions pill does not say the cap moved: %q", pill.Text)
	}
	if strings.HasSuffix(pill.Text, ellipsis) {
		t.Errorf("the conditions pill was truncated: %q", pill.Text)
	}
	if pill.Rect.Min.X < contentL {
		t.Errorf("the five-pill row starts at x=%d, left of the content box (%d)",
			pill.Rect.Min.X, contentL)
	}
	t.Logf("pill row: %q ends at x=%d of %d", pill.Text, pill.Rect.Max.X, contentR)

	// A run whose machine did not move says nothing, rather than "?" or a
	// pair of identical numbers: the row keeps its four pills and no fifth.
	quiet := card.Example()
	c2, err := renderCanvas(quiet)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	if m2, ok := c2.markByID("memory.pill4"); ok && strings.Contains(m2.Text, "cap ") {
		t.Errorf("an unchanged machine printed a conditions pill: %q", m2.Text)
	}
}

// TestStripCoreSuffix (2026-09-19): the rig line's first fallback drops the
// core-count tail and nothing else. The count sits between the space and the
// suffix — lscpu reports "5975WX 32-Cores" — so a matcher on " -Cores" alone
// never fires and the CPU was falling out of the line whole on the real rig
// (caught on assets/hero.tape, whose CPU is exactly this string).
func TestStripCoreSuffix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"AMD Ryzen Threadripper PRO 5975WX 32-Cores", "AMD Ryzen Threadripper PRO 5975WX"},
		{"AMD EPYC 9754 128-Core Processor", "AMD EPYC 9754"},
		{"AMD Ryzen Threadripper PRO 7985WX 96-Core", "AMD Ryzen Threadripper PRO 7985WX"},
		// Not the shape: an Intel suffix, a name with no count, and a bare
		// count with nothing to cut at all come back unchanged.
		{"Intel(R) Xeon(R) Gold 6248R CPU @ 3.00GHz", "Intel(R) Xeon(R) Gold 6248R CPU @ 3.00GHz"},
		{"Apple M2 Max", "Apple M2 Max"},
		{"", ""},
	} {
		if got := stripCoreSuffix(tc.in); got != tc.want {
			t.Errorf("stripCoreSuffix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestStripCPUVendor (2026-09-19) tests the rig line's rung between the
// preferred spelling and the core-stripped one, the way TestStripCoreSuffix
// tests its neighbour: a leading vendor word goes, and nothing else.
func TestStripCPUVendor(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"AMD Ryzen Threadripper PRO 5975WX 32-Cores", "Ryzen Threadripper PRO 5975WX 32-Cores"},
		{"Intel(R) Xeon(R) Gold 6248R CPU @ 3.00GHz", "Xeon(R) Gold 6248R CPU @ 3.00GHz"},
		// The two-word Intel form is its own entry: matching "Intel(R) "
		// alone would leave a "Core(TM) " that names no machine either.
		{"Intel(R) Core(TM) i7-9700K CPU @ 3.00GHz", "i7-9700K CPU @ 3.00GHz"},
		{"Intel Xeon E5-2680 v4", "Xeon E5-2680 v4"},
		// Not the shape: no vendor prefix, and a vendor word that is not
		// leading, each come back unchanged — the stripper shortens, it
		// never invents.
		{"Apple M2 Max", "Apple M2 Max"},
		{"Ryzen Threadripper PRO branded by AMD", "Ryzen Threadripper PRO branded by AMD"},
		{"", ""},
	} {
		if got := stripCPUVendor(tc.in); got != tc.want {
			t.Errorf("stripCPUVendor(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRigLineKeepsTheCPU is the defect-2 gate (2026-09-19): the CPU is the
// part of the rig line that names the machine, and the line exists to say
// which machine produced the figure above it. TestIdentityLinesFitItsBand
// cannot catch it losing the CPU — with pickWidest and a final fallback of
// bare GPUs, that assertion only fails when the SHORTEST form overflows, and
// "RTX A6000 48G · 252 GB DDR4-3600" fits fine. So the gate is content, not
// fit: on the hero recording and on the two-GPU ws rig (wsRigSummary, which
// carries the wsreal tape's rig strings as literals so the pin survives
// without the scratch tape), the line the canvas actually drew still names
// the CPU model, a GPU model and the RAM figure.
//
// FAIL-first on the pre-fix tree at sizeIdent 26: the hero's core-stripped
// rung measured 1088 px against the 1080 px content box — eight pixels — and
// both tapes fell through to the GPUs · RAM rung, which is why this test
// exists.
func TestRigLineKeepsTheCPU(t *testing.T) {
	for name, s := range map[string]*tape.RunSummary{
		"hero tape": heroTapeSummary(t),
		"ws rig":    wsRigSummary(),
	} {
		s := s
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			m, ok := c.markByID("ident.rig")
			if !ok {
				t.Fatal("ident.rig was never drawn")
			}
			for _, want := range []string{"5975WX", "A6000", "252 GB"} {
				if !strings.Contains(m.Text, want) {
					t.Errorf("ident.rig = %q, want it to still name %q", m.Text, want)
				}
			}
			if strings.HasSuffix(m.Text, ellipsis) {
				t.Errorf("ident.rig was truncated: %q", m.Text)
			}
		})
	}
}

// TestMemorySumDropsWholeParts is the defect-3 gate (2026-09-19): with four
// or five pills the summary beside the placement bar was cut by the old
// character truncation inside a number — "host RSS 1.…" — and a number cut
// after its first digit invites a reader to finish it wrongly. The summary
// is built like the identity lines now, a preferred string plus fallbacks
// that drop whole trailing parts through pickWidest against the width the
// pill row leaves, so what it draws is either the whole summary or a prefix
// of it that ends at a part boundary — never an ellipsis, never a part cut
// short. If even the first part alone did not fit, the truncation would be
// the intended last resort; no fixture in this set is that narrow, and one
// that was would fail here on purpose.
func TestMemorySumDropsWholeParts(t *testing.T) {
	for name, s := range bandFixtures() {
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			m, ok := c.markByID("memory.sum")
			if !ok {
				t.Fatal("memory.sum was never drawn")
			}
			if strings.HasSuffix(m.Text, ellipsis) {
				t.Errorf("memory.sum = %q, cut inside a part", m.Text)
			}
			pref := contentOf(t, s).sum.preferred
			if m.Text == pref {
				return
			}
			if !strings.HasPrefix(pref, m.Text) || !strings.HasPrefix(pref[len(m.Text):], " · ") {
				t.Errorf("memory.sum = %q is not a whole-part prefix of %q", m.Text, pref)
			}
		})
	}
}
