package png

import (
	"fmt"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// TTP-138 (FAIL-first, 2026-09-19): the window figure is the decode column's
// headline when the tape carries one, exactly as the text card's Decode row
// leads with it. Where there is no window, TestConcurrentHeroShowsBothFigures
// already pins today's figure standing.
func TestWindowFigureLeadsTheDecodeColumn(t *testing.T) {
	c, err := renderCanvas(card.ExampleWindow())
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("hero.left.number")
	if !ok {
		t.Fatal("hero.left.number was never drawn")
	}
	if m.Text != "66.7" {
		t.Errorf("decode headline = %q, want the window figure 66.7", m.Text)
	}
	// The per-stream rate stays under it: N × per-stream is the arithmetic
	// the window reconciles, and a headline alone would be a different claim.
	//
	// It is the window's own per-stream rate, not the whole wall's (lead,
	// 2026-09-19). The first round pinned 17.4 here, on the spec's clause
	// that nothing else about the row moves — but this column spells the
	// product out, so 4 × 17.4 printed under a headline of 66.7 invites the
	// reader to compute a third number the card never showed. That is the
	// arithmetic ragged_aggregate disputes, reproduced inside the column
	// that displaced it. The 17.4 assertion described behaviour that is now
	// gone by design and is replaced rather than re-pinned.
	sub, _ := c.markByID("hero.left.sub1")
	if !strings.Contains(sub.Text, "16.7") {
		t.Errorf("decode sub-line = %q, want the window's own per-stream 16.7 beside the headline", sub.Text)
	}
	if strings.Contains(sub.Text, "17.4") {
		t.Errorf("decode sub-line = %q, want the whole-wall per-stream figure gone: it is measured over a different span than the headline above it", sub.Text)
	}
	if !strings.Contains(sub.Text, "4 ×") {
		t.Errorf("decode sub-line = %q, want the count the window was taken over, so the product it spells out holds", sub.Text)
	}
	// And the clause beside them names the window rather than denying it
	// (vision, 2026-09-19). "not all decoding at once" was the right thing
	// to say next to a whole-wall aggregate and is a contradiction next to
	// this one: 66.7 is the rate of the span in which all four were
	// decoding at once.
	if strings.Contains(sub.Text, "not all decoding at once") {
		t.Errorf("decode sub-line = %q, want the clause not to deny the figure it is attached to", sub.Text)
	}
	if !strings.Contains(sub.Text, "all 4 overlapped for") {
		t.Errorf("decode sub-line = %q, want the window named beside the figures: the PNG has no caveat band to say it anywhere else", sub.Text)
	}
}

// A run whose tape carries no window keeps the old clause: there the
// aggregate really is the whole wall and really does not reconcile, which is
// what the clause was written to say (vision, 2026-09-19).
func TestRaggedClauseWithoutAWindowIsWhatItWas(t *testing.T) {
	c, err := renderCanvas(card.ExampleRagged())
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	sub, _ := c.markByID("hero.left.sub1")
	if !strings.Contains(sub.Text, "not all decoding at once") {
		t.Errorf("decode sub-line = %q, want today's clause on a tape with no window", sub.Text)
	}
}

// TTP-137 (FAIL-first, 2026-09-19): the probe's two figures ride the prefill
// column's second sub-line, beside the figures already there, and must not
// be cut — an ellipsis inside a figure is the defect sub2Fallbacks exist to
// prevent.
func TestProbeClauseRidesThePrefillColumn(t *testing.T) {
	c, err := renderCanvas(card.ExampleProbed())
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("hero.right.sub2")
	if !ok {
		t.Fatal("hero.right.sub2 was never drawn")
	}
	for _, want := range []string{"1182 tok/s on one stream", "28 ms fixed"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("prefill sub-line = %q, want it to carry %q whole", m.Text, want)
		}
	}
	if strings.Contains(m.Text, "…") {
		t.Errorf("prefill sub-line = %q, the probe clause was cut", m.Text)
	}

	// The concurrent column cannot carry it beside the TTFT spread — the
	// frame is fixed at a 504 px column, and the full line measures 792 px
	// (655 px with the prompt count dropped), measured with this canvas's
	// own font at stBody. The spread the concurrent card is judged on stays;
	// the machine's rate reaches the concurrent reader through the text
	// card's Prefill row. Pinned so that the day the frame grows room, the
	// clause moves over deliberately rather than by drift.
	s := card.ExampleConcurrent()
	s.Probe = card.ExampleProbed().Probe
	cc, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m2, _ := cc.markByID("hero.right.sub2")
	if !strings.Contains(m2.Text, "p95") || strings.Contains(m2.Text, "…") {
		t.Errorf("concurrent prefill sub-line = %q, want the TTFT spread whole — it outranks nothing the frame can hold beside it", m2.Text)
	}

	// And it is absent when there is no probe at all — the ordinary card
	// keeps the sub-line it always had.
	plain, err := renderCanvas(card.Example())
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	if m3, _ := plain.markByID("hero.right.sub2"); strings.Contains(m3.Text, "on one stream") {
		t.Errorf("prefill sub-line = %q, want no probe clause without a probe", m3.Text)
	}
}

// TTP-135 (FAIL-first, 2026-09-19): the cap counts its streams in the pill
// row, with the denominator, not as a bare flag.
//
// The spelling is the pill row's, not the Context row's (lead, 2026-09-19).
// The original round used the text card's whole sentence, "4 of 4 hit the
// cap"; the other four pills are a label and a value, so a sentence among
// them reads as an alarm on a card that states conditions and does not
// warn, and it was long enough to push the observed "46.6 GiB placed" off
// the line the pill row shares with the placement summary. The assertion on
// the sentence is replaced rather than re-pinned; what is pinned is the
// rule it was there for — the count and its denominator, in the warn
// colour, and nothing at all when the endings were never observed.
func TestCapPillNamesItsStreams(t *testing.T) {
	s := card.Example()
	s.Limit.CappedStreams = 4
	s.Limit.EndingsObserved = 4
	c := contentOf(t, s)
	var texts []string
	var capPill *pill
	for i := range c.pills {
		texts = append(texts, c.pills[i].text)
		if strings.HasPrefix(c.pills[i].text, "cap ") {
			capPill = &c.pills[i]
		}
	}
	if capPill == nil {
		t.Fatalf("no cap pill among %v", texts)
	}
	// Warn, not Bad: the capped tokens are real tokens, truncated by a limit
	// the user set — a caution about where the run stopped, not a defect in
	// the figures beside it.
	if capPill.col != colWarn {
		t.Errorf("cap pill colour = %v, want the warn colour a caution carries", capPill.col)
	}

	// Silent when the tape cannot say: no endings observed, no pill — and
	// no invented count.
	s.Limit.EndingsObserved = 0
	for _, p := range contentOf(t, s).pills {
		if strings.Contains(p.text, "cap") {
			t.Errorf("EndingsObserved == 0 still drew a cap pill: %q", p.text)
		}
	}

	// The layout guard, the answer-cut pill's own rule: nothing leaves the
	// content box with the extra pill in the row.
	s.Limit.EndingsObserved = 4
	cv, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	for _, m := range cv.marks {
		if m.Kind != "text" || m.Text == "" {
			continue
		}
		if m.Rect.Min.X < contentL || m.Rect.Max.X > contentR {
			t.Errorf("%s (%q) spans x=%d..%d, outside the content box (%d..%d)",
				m.ID, m.Text, m.Rect.Min.X, m.Rect.Max.X, contentL, contentR)
		}
	}
}

// TTP-137's KV figure (2026-09-19): the servers's own KV-cache size renders
// in the placement legend, and a miss stays a "?" rather than a zero that
// would read as "no cache". The subdivided legend is pinned by
// TestGPUSegmentsAreSubdivided; this is the zero half of the same rule.
func TestKVFigureRendersOrStaysUnknown(t *testing.T) {
	s := card.Example()
	if s.Placement.VRAMKVBytes <= 0 {
		t.Fatal("the fixture lost its KV figure; the known half is pinned elsewhere")
	}
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	if joined := strings.Join(legendEntries(c), " · "); !strings.Contains(joined, "kv 2.6 GiB") {
		t.Errorf("placement legend = %q, want the server's own kv 2.6 GiB in it", joined)
	}

	// Zero is not observed and prints as "?", never as "kv 0.0 GiB".
	s.Placement.VRAMKVBytes = 0
	c2, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	if joined := strings.Join(legendEntries(c2), " · "); strings.Contains(joined, "kv 0.0") {
		t.Errorf("placement legend = %q, an unobserved KV size printed as zero", joined)
	}
}

// The placement summary keeps its measured figure when a fifth pill takes
// the row (vision, 2026-09-19).
//
// FAIL-first: with the cap pill in the row, drawPills leaves 89 px where the
// whole "· 46.6 GiB placed" needs 143, and the line dropped the clause
// entire — with no ellipsis, so a reader of that card could not tell a
// figure had ever been there — although "· 46.6 GiB" fits in 80. The line
// now gives up the noun before the number. A measured figure is not
// surrendered to make room for a derived pill while a shorter spelling of it
// would fit; the rule is general, and this is where it first cost something.
func TestPlacementSummaryKeepsItsFigureBesideFivePills(t *testing.T) {
	s := card.ExampleWindow()
	s.Limit.CappedStreams = 4
	s.Limit.EndingsObserved = 4
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	if n := len(contentOf(t, s).pills); n != 5 {
		t.Fatalf("pills = %d, want the 5 this gate is about", n)
	}
	m, ok := c.markByID("memory.sum")
	if !ok {
		t.Fatal("memory.sum was never drawn")
	}
	if !strings.Contains(m.Text, "GiB") {
		t.Errorf("placement summary = %q, want it to still name the placed figure", m.Text)
	}
	if strings.Contains(m.Text, "…") {
		t.Errorf("placement summary = %q, want it shortened by a whole word, never cut inside a number", m.Text)
	}
}

// The cap pill rejoins the row it sits in (vision, 2026-09-19): the same
// fill as its four neighbours, with the colour kept for the text. Measured
// on the first spelling: the warm fill made 111 pixels that appear nowhere
// else on the card, and a colour family used exactly once, in the last seat
// of a row of all-clear readings, reads as an alarm rather than a finding.
func TestCapPillFillMatchesItsNeighbours(t *testing.T) {
	s := card.Example()
	s.Limit.CappedStreams = 4
	s.Limit.EndingsObserved = 4

	// Measured on the rendered image, not on the content struct: the fill is
	// chosen in one place and drawn in another, and a gate that reads only
	// the field stays green if the drawing stops honouring it. This is the
	// judge's own method — count what colour actually reaches the canvas.
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	img, err := Render(s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var capIdx, otherIdx = -1, -1
	for i := range contentOf(t, s).pills {
		if strings.HasPrefix(contentOf(t, s).pills[i].text, "cap ") {
			capIdx = i
		} else if otherIdx < 0 {
			otherIdx = i
		}
	}
	if capIdx < 0 || otherIdx < 0 {
		t.Fatal("want a cap pill and a neighbour in the row")
	}
	fillOf := func(i int) (uint32, uint32, uint32) {
		m, ok := c.markByID(fmt.Sprintf("memory.pill%d", i))
		if !ok {
			t.Fatalf("memory.pill%d was never drawn", i)
		}
		// Three pixels in from the rounded left edge, on the centre line:
		// inside the fill and clear of the glyphs.
		r, g, b, _ := img.At(m.Rect.Min.X+6, (m.Rect.Min.Y+m.Rect.Max.Y)/2).RGBA()
		return r >> 8, g >> 8, b >> 8
	}
	cr, cg, cb := fillOf(capIdx)
	nr, ng, nb := fillOf(otherIdx)
	if cr != nr || cg != ng || cb != nb {
		t.Errorf("cap pill fill = rgb(%d,%d,%d), its neighbour = rgb(%d,%d,%d): the text carries the distinction, not the box",
			cr, cg, cb, nr, ng, nb)
	}
	// A warm fill is what made it read as an alarm: the first spelling put
	// 111 pixels on the card in a colour family used nowhere else.
	if int(cr)-int(cb) > 30 {
		t.Errorf("cap pill fill = rgb(%d,%d,%d), warm enough to be the only warm thing on the card", cr, cg, cb)
	}
	// The distinction still has to live somewhere, and that somewhere is the
	// text.
	var capPill, neighbour pill
	for i, pl := range contentOf(t, s).pills {
		if i == capIdx {
			capPill = pl
		} else if i == otherIdx {
			neighbour = pl
		}
	}
	if capPill.col == neighbour.col {
		t.Errorf("cap pill text colour = %v, the same as its neighbours: nothing marks it as a finding", capPill.col)
	}
}

// The two renderers say the same thing about the same run (vision,
// 2026-09-19).
//
// FAIL-first: the PNG's decode sub-line said "all 4 overlapped for 3.6 s"
// while the text card's Streams row still said "not all decoding at once" —
// two renderers characterising one run in opposite words. The predicate was
// already shared, which is exactly what made it look safe; only the question
// was, and each renderer spelled the answer itself. card.RaggedClause now
// owns the sentence and this asserts the PNG carries it verbatim.
func TestBothRenderersUseOneRaggedClause(t *testing.T) {
	cases := map[string]*tape.RunSummary{
		"with a window": card.ExampleWindow(),
		"without one":   card.ExampleRagged(),
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			want := card.RaggedClause(s)
			if want == "" {
				t.Fatalf("fixture is not ragged, so this gate tests nothing")
			}
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			sub, _ := c.markByID("hero.left.sub1")
			if !strings.Contains(sub.Text, want) {
				t.Errorf("decode sub-line = %q, want it to carry the one clause %q", sub.Text, want)
			}
			if !strings.Contains(card.Text(s), want) {
				t.Errorf("text card does not carry the clause %q the PNG prints", want)
			}
		})
	}
}

// The clause has to survive a wider run (vision, 2026-09-19): "all 12
// overlapped for 12.4 s" is longer than the four-stream spelling this was
// designed against, and the column is a fixed 504 px. If it stops fitting,
// sub1Fallbacks drops it — and the PNG has no caveat band, so the condition
// under which the headline holds would leave the image entirely.
func TestRaggedClauseFitsAWideRun(t *testing.T) {
	s := card.ExampleWindow()
	s.Concurrency = 12
	s.Aggregate.Streams = 12
	s.Aggregate.ConcurrentStreams = 12
	s.Aggregate.ConcurrentWindowMs = 12400
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	sub, _ := c.markByID("hero.left.sub1")
	// The contract is the fact, not the wording: at 12 streams neither the
	// sentence nor the sentence-without-the-queue-note fits 504 px, so the
	// line takes RaggedClauseShort — "overlapped 12.4 s", which is missing
	// nothing, since the count it drops opens the same line. What must not
	// happen is the window leaving the image.
	if !strings.Contains(sub.Text, "12.4 s") {
		t.Errorf("decode sub-line = %q, want the window figure still on the image at 12 streams", sub.Text)
	}
	if !strings.Contains(sub.Text, "12 ×") {
		t.Errorf("decode sub-line = %q, want the count the short clause leans on", sub.Text)
	}
	if strings.Contains(sub.Text, "…") {
		t.Errorf("decode sub-line = %q, cut rather than shortened", sub.Text)
	}
	// And the queue note is what yielded, not the window: the original
	// fallback order gave up the window first.
	if strings.Contains(sub.Text, "queued") {
		t.Errorf("decode sub-line = %q, want the queue note to yield before the headline's own condition", sub.Text)
	}
}
