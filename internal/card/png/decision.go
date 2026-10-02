package png

import (
	"image"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// The decision card (TTP-192, 2026-10-02) keeps the token card's frame, header,
// hero split, identity band and credit line, so a reader sees one product, and
// replaces what is about a token stream:
//
//	hero left   Decision 37.6 ms — the card's one lit figure (the Accent ramp
//	            the decode figure wears on the token card), the client's
//	            send-to-last-byte p50: "p50 · warm · end to end"
//	hero right  Engine 36.1 ms, "prompt + head · engine-reported", in Text and
//	            never the accent, when the tape carries the engine's own time;
//	            Cold 640 ms otherwise. With ENGINE on the right, cold moves to
//	            the left column's detail line (2026-10-02): it is a figure that
//	            must stay visible on every surface, never dropped for a
//	            breakdown
//	mid band    short | long | p95 | prefill | throughput, one cell each, and
//	            one line under them for the Cache and Reference rows when the
//	            tape has them (the token card's memory band had a bar here)
//	strip       the caveat and the tape's note, dim — a synthetic tape says
//	            SYNTHETIC here instead of "verified by toktape", which would be
//	            a claim nobody made
//
// Every string comes from card.DecisionCard, the same view the text card
// prints, so the two cannot word a figure differently.
//
// The example answer block (a case's questions with their top options as bars)
// is not drawn. It needs about 100 px, the canvas has none to give without
// shrinking the type — the bands above sum to the 635 px panel — and it would
// need Tape.Decisions, which Render(*RunSummary) does not take. The report
// proposes where it could live.

// Mid band geometry, inside bandMemTop..bandIdentTop.
const (
	statLabelBase  = bandMemTop + 30
	statValueBase  = bandMemTop + 66
	statDetailBase = bandMemTop + 90
	statExtraBase  = bandMemTop + 116
	statRuleTop    = bandMemTop + 12
	statRuleBottom = bandMemTop + 98

	sizeStatValue = 30
	sizeStatUnit  = 15
	statUnitGap   = 8
)

var (
	stStatValue = textStyle{size: sizeStatValue, weight: wBold}
	stStatUnit  = textStyle{size: sizeStatUnit, weight: wBold}
)

// statKeys are the mid band's cells, in order; a key the view has no row for
// leaves no cell.
var statKeys = []string{
	card.DecisionRowShort, card.DecisionRowLong, card.DecisionRowP95,
	card.DecisionRowPrefill, card.DecisionRowThroughput,
}

// drawDecision paints the whole decision card. It is draw's twin for a run
// with no token stream.
func (c *canvas) drawDecision(s *tape.RunSummary) {
	v := card.DecisionCard(s)

	ct := &content{}
	ct.buildHeader(s)
	ct.buildIdent(s)
	// The token card's engine line carries the llama.cpp flag row, which for
	// any other server prints "?" in the middle; a decision run has no flags
	// worth a reader's line, so the line is the engine and its context.
	ct.ident3 = decisionEngineIdent(s.Server)

	c.fill(image.Rect(0, 0, Width, Height), colBase)
	panel := image.Rect(panelInset, panelTop, Width-panelInset, panelBottom)
	c.roundRect("panel.border", panel, panelRadius, colBorder)
	c.roundRect("panel", panel.Inset(1), panelRadius-1, colPanel)

	c.drawHeader(ct)
	c.hairline("rule.header", contentL, contentR, bandHeroTop, colBorder)
	c.drawDecisionHero(v)
	c.hairline("rule.hero", contentL, contentR, bandMemTop, colBorder)
	c.drawStats(v)
	c.drawIdent(ct)
	c.drawDecisionStrip(v)
}

func decisionEngineIdent(srv tape.ServerInfo) fallbackLine {
	eng := engineString(srv)
	var tail []string
	if srv.CtxSize > 0 {
		tail = append(tail, "ctx "+strconv.Itoa(srv.CtxSize))
	}
	if srv.NSlots > 0 {
		// "1 slot", not "1 slots" (the token card's content.go has the same
		// bug and is filed separately).
		word := " slots"
		if srv.NSlots == 1 {
			word = " slot"
		}
		tail = append(tail, strconv.Itoa(srv.NSlots)+word)
	}
	return fallbackLine{
		preferred: joinParts(" · ", eng, strings.Join(tail, " · ")),
		fallbacks: []string{eng},
	}
}

// drawDecisionHero draws the two hero columns through the token card's own
// drawHeroCol: same eyebrow, number and unit voices, same sub-line colours.
func (c *canvas) drawDecisionHero(v card.DecisionView) {
	c.vrule("hero.split", heroSplitX, heroRuleTop, heroRuleBottom, colBorder)

	requests, _ := v.Row(card.DecisionRowRequests)
	cold, _ := v.Row(card.DecisionRowCold)
	engine, hasEngine := v.Row(card.DecisionRowEngine)
	left := heroCol{
		eyebrow: "Decision",
		number:  v.Hero.Value,
		unit:    v.Hero.Unit,
		sub1:    card.DecisionHeroCaption,
	}
	reqLine := ""
	if len(requests.Parts) > 0 {
		reqLine = strings.Join(append([]string{requests.Parts[0] + " requests"}, requests.Parts[1:]...), " · ")
	}
	right := heroCol{}
	if hasEngine {
		// Cold rides the detail line, ahead of the request count, so a
		// truncation takes the passes and never the cold figure.
		coldWord := "cold " + cold.Value() + " first request"
		left.sub2 = strings.Join(nonEmpty(coldWord, reqLine), " · ")
		// The ladder drops the passes first, then the errors, and never cold.
		if n := len(requests.Parts); n > 0 {
			count := requests.Parts[0] + " requests"
			withErrors := nonEmpty(coldWord, count)
			if n >= 3 {
				withErrors = append(withErrors, requests.Parts[n-1]) // "0 errors"
			}
			left.sub2Fallbacks = []string{
				strings.Join(withErrors, " · "), strings.Join(nonEmpty(coldWord, count), " · "), coldWord,
			}
		}
		right = heroCol{
			eyebrow: "Engine",
			number:  strings.Fields(engine.Value())[0], // "36.1 ms p50" -> "36.1"
			sub1:    "prompt + head · engine-reported",
		}
	} else {
		left.sub2 = reqLine
		right = heroCol{
			eyebrow: "Cold",
			number:  strings.TrimSuffix(cold.Value(), " ms"),
			sub1:    "first request, reported apart",
			sub2:    "not in any warm figure",
		}
	}
	if right.number != unknown {
		right.unit = "ms"
	}
	c.drawHeroCol("hero.left", left, contentL, heroSplitX-heroGutter, func(x0, x1 int) image.Image {
		return hGradient{x0: x0, x1: x1, c0: colAccent, c1: colAccentHigh}
	})
	c.drawHeroCol("hero.right", right, heroRightX, contentR, func(int, int) image.Image {
		return solid(colText)
	})
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// drawStats draws the mid band: one cell per figure, label over value over
// detail, hairlines between them.
func (c *canvas) drawStats(v card.DecisionView) {
	var cells []card.DecisionRow
	for _, k := range statKeys {
		if r, ok := v.Row(k); ok {
			cells = append(cells, r)
		}
	}
	if len(cells) > 0 {
		w := contentW / len(cells)
		for i, r := range cells {
			x := contentL + i*w
			if i > 0 {
				c.vrule("stat.rule"+strconv.Itoa(i), x-14, statRuleTop, statRuleBottom, colBorder)
			}
			c.drawStat("stat."+r.Key, r, x, w-28)
		}
	}

	// Cache and Reference ride one line under the cells, only when present.
	var extra []card.DecisionRow
	for _, k := range []string{card.DecisionRowCache, card.DecisionRowReference} {
		if r, ok := v.Row(k); ok {
			extra = append(extra, r)
		}
	}
	x := contentL
	for _, r := range extra {
		lab := c.text(textOpts{
			id: "stat.extra." + r.Key + ".label", s: r.Label, x: x, baseline: statExtraBase,
			style: stColLabel, src: solid(colFaint),
		})
		val := c.text(textOpts{
			id: "stat.extra." + r.Key, s: strings.Join(r.Parts, " · "), x: lab.Max.X + 12, baseline: statExtraBase,
			style: stSmall, src: solid(colText), maxW: contentR - (lab.Max.X + 12),
		})
		x = val.Max.X + 32
	}
}

func (c *canvas) drawStat(id string, r card.DecisionRow, x, w int) {
	c.text(textOpts{
		id: id + ".label", s: r.Label, x: x, baseline: statLabelBase,
		style: stColLabel, src: solid(colDim), maxW: w,
	})
	// The figure is set large and its unit small and dim, the hero's own
	// number-and-unit voice at cell size, so "37.4 ms p50" does not run into
	// the next cell's rule.
	num, unit, _ := strings.Cut(r.Value(), " ")
	nr := c.text(textOpts{
		id: id + ".value", s: num, x: x, baseline: statValueBase,
		style: stStatValue, src: solid(colText), maxW: w,
	})
	if unit != "" {
		c.text(textOpts{
			id: id + ".unit", s: unit, x: nr.Max.X + statUnitGap, baseline: statValueBase,
			style: stStatUnit, src: solid(colDim), maxW: w - nr.Dx() - statUnitGap,
		})
	}
	c.text(textOpts{
		id: id + ".detail", s: r.Detail(), x: x, baseline: statDetailBase,
		style: stMicro, src: solid(colFaint), maxW: w,
	})
}

// stripLineGap is the pitch of the strip's text lines.
const stripLineGap = 16

// drawDecisionStrip draws the bottom strip: the caveat and the note on the
// left, the credit on the right. A card with neither keeps the token card's
// "verified by toktape".
//
// The lines stack upward from the credit's baseline and the rule rises with
// them, so a note that needs a second line (the synthetic tape's does) is
// wrapped whole instead of cut: a note truncated on a synthetic tape is the
// one place the cut would hide what the note is for. The note takes at most
// two lines; the caveat one.
func (c *canvas) drawDecisionStrip(v card.DecisionView) {
	c.text(textOpts{
		id: "strip.credit", s: creditText, x: contentR, baseline: stripFootBase,
		style: stMicro, src: solid(colFaint), align: alignRight,
	})
	credit, _ := c.markByID("strip.credit")
	maxW := credit.Rect.Min.X - 24 - contentL

	var lines []string
	for _, cv := range v.Caveats {
		lines = append(lines, cv.Text)
	}
	if v.Note != "" {
		lines = append(lines, c.wrapWords(v.Note, stMicro, maxW, 2)...)
	}
	if len(lines) == 0 {
		c.hairline("rule.strip", contentL, contentR, stripRuleY, colBorder)
		c.text(textOpts{
			id: "strip.verified", s: "verified by toktape", x: contentL, baseline: stripFootBase,
			style: stColLabel, src: solid(colFaint),
		})
		return
	}
	top := stripFootBase - stripLineGap*(len(lines)-1)
	c.hairline("rule.strip", contentL, contentR, min(stripRuleY, top-20), colBorder)
	for i, l := range lines {
		c.text(textOpts{
			id: "strip.line" + strconv.Itoa(i), s: l, x: contentL, baseline: top + i*stripLineGap,
			style: stMicro, src: solid(colDim), maxW: maxW,
		})
	}
}

// wrapWords breaks s at spaces into lines of at most maxW pixels in st. After
// maxLines the rest is left to the last line, whose draw truncates it with an
// ellipsis.
func (c *canvas) wrapWords(s string, st textStyle, maxW, maxLines int) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		next := w
		if cur != "" {
			next = cur + " " + w
		}
		if cur != "" && c.measure(next, st) > maxW && len(lines) < maxLines-1 {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur = next
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
