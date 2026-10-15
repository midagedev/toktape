package png

import (
	"fmt"
	"image"
	"strconv"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// The embedding card (2026-10-15) is the decision card's frame — header,
// hero split, mid band, identity, strip — with an embedding run's figures:
// the request p50 as the lit hero, recall@1 on the search set beside it, and
// the run's shape in the mid band. Every figure is the summary's.
func (c *canvas) drawEmbedding(s *tape.RunSummary) {
	e := tape.EmbeddingSummary{}
	if s.Embedding != nil {
		e = *s.Embedding
	}
	ct := &content{}
	ct.buildHeader(s)
	ct.buildIdent(s)
	ct.ident3 = decisionEngineIdent(s.Server)

	c.fill(image.Rect(0, 0, Width, Height), colBase)
	panel := image.Rect(panelInset, panelTop, Width-panelInset, panelBottom)
	c.roundRect("panel.border", panel, panelRadius, colBorder)
	c.roundRect("panel", panel.Inset(1), panelRadius-1, colPanel)
	c.drawHeader(ct)
	c.hairline("rule.header", contentL, contentR, bandHeroTop, colBorder)

	c.vrule("hero.split", heroSplitX, heroRuleTop, heroRuleBottom, colBorder)
	left := heroCol{
		eyebrow: "Embedding",
		number:  msFig(e.LatencyP50Ms),
		unit:    "ms",
		sub1:    "p50 per request · end to end",
		sub2:    fmt.Sprintf("%d requests · POST %s", e.Inputs, orUnknown(e.Endpoint)),
	}
	if left.number == unknown {
		left.unit = ""
	}
	right := heroCol{eyebrow: "Recall@1", number: unknown, sub1: "no graded queries"}
	if e.Graded > 0 {
		right.number = fmt.Sprintf("%d/%d", e.Hits, e.Graded)
		right.sub1 = "expected doc ranked first"
		if len(e.Prompts) > 0 {
			right.sub2 = fmt.Sprintf("with task prompts · %d/%d bare", e.HitsBare, e.Graded)
		}
	}
	c.drawHeroCol("hero.left", left, contentL, heroSplitX-heroGutter, func(x0, x1 int) image.Image {
		return hGradient{x0: x0, x1: x1, c0: colAccent, c1: colAccentHigh}
	})
	c.drawHeroCol("hero.right", right, heroRightX, contentR, func(int, int) image.Image {
		return solid(colText)
	})
	c.hairline("rule.hero", contentL, contentR, bandMemTop, colBorder)

	cells := []card.DecisionRow{
		{Key: "dim", Label: "Dim", Parts: []string{strconv.Itoa(e.Dim), "per vector"}},
		{Key: "docs", Label: "Docs", Parts: []string{strconv.Itoa(e.Docs), "indexed"}},
		{Key: "queries", Label: "Queries", Parts: []string{strconv.Itoa(e.Queries), "searched"}},
		{Key: "max", Label: "Slowest", Parts: []string{msFig(e.LatencyMaxMs) + " ms", "one request"}},
	}
	w := contentW / len(cells)
	for i, r := range cells {
		x := contentL + i*w
		if i > 0 {
			c.vrule("stat.rule"+strconv.Itoa(i), x-14, statRuleTop, statRuleBottom, colBorder)
		}
		c.drawStat("stat."+r.Key, r, x, w-28)
	}
	c.drawIdent(ct)
	c.drawDecisionStrip(card.DecisionView{Note: s.Note})
}

func msFig(ms float64) string {
	switch {
	case ms <= 0:
		return unknown
	case ms < 100:
		return strconv.FormatFloat(ms, 'f', 1, 64)
	}
	return strconv.FormatFloat(ms, 'f', 0, 64)
}
