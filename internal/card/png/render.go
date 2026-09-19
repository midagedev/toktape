package png

import (
	"fmt"
	"image"

	"github.com/midagedev/toktape/internal/tape"
)

// draw paints the whole card. The order is back to front: panel, rules, then
// every band top to bottom.
func (c *canvas) draw(s *tape.RunSummary) {
	ct := build(s)

	c.fill(image.Rect(0, 0, Width, Height), colBase)
	panel := image.Rect(panelInset, panelTop, Width-panelInset, panelBottom)
	// A 1px border is two fills, not a stroke: the outer rounded rect in the
	// border colour, then the same shape inset by one pixel in the panel
	// colour. A rasterised 1px stroke would be a grey smear at this radius.
	c.roundRect("panel.border", panel, panelRadius, colBorder)
	c.roundRect("panel", panel.Inset(1), panelRadius-1, colPanel)

	c.drawHeader(ct)
	c.hairline("rule.header", contentL, contentR, bandHeroTop, colBorder)
	c.drawHero(ct)
	c.hairline("rule.hero", contentL, contentR, bandMemTop, colBorder)
	c.drawMemory(ct)
	c.drawIdent(ct)
	c.drawStrip(ct)
}

// ---------------------------------------------------------------- header ---

func (c *canvas) drawHeader(ct *content) {
	// Wordmark. Two runs, one colour change: the only branding on the card,
	// and the reason it needs no logo.
	markBase := bandHeaderTop + 38
	r := c.text(textOpts{
		id: "header.wordmark.tok", s: "tok", x: contentL, baseline: markBase,
		style: stWordmark, src: solid(colAccent),
	})
	r = c.text(textOpts{
		id: "header.wordmark.tape", s: "tape", x: r.Max.X, baseline: markBase,
		style: stWordmark, src: solid(colText),
	})
	c.text(textOpts{
		id: "header.version", s: ct.version, x: r.Max.X + 14, baseline: markBase,
		style: stVersion, src: solid(colDim),
	})

	c.text(textOpts{
		id: "header.runid", s: ct.runID, x: contentL, baseline: bandHeaderTop + 62,
		style: stRunID, src: solid(colFaint), maxW: heroSplitX - contentL - 20,
	})

	// Right: when the run happened, and on what. The model's name is the
	// identity band's first line now (2026-09-19), so this side carries the
	// run's moment instead — and never the hostname: the published view
	// strips the machine's name, and a card must not change shape between
	// the local and the published render. Both lines are dropped when
	// unobserved rather than drawn as "?".
	if ct.when != "" {
		c.text(textOpts{
			id: "header.when", s: ct.when, x: contentR, baseline: bandHeaderTop + 34,
			style: stIdentSub, src: solid(colDim), align: alignRight, maxW: contentR - heroSplitX,
		})
	}
	if ct.os != "" {
		c.text(textOpts{
			id: "header.os", s: ct.os, x: contentR, baseline: bandHeaderTop + 60,
			style: stMicro, src: solid(colFaint), align: alignRight, maxW: contentR - heroSplitX,
		})
	}
}

// ------------------------------------------------------------------ hero ---

func (c *canvas) drawHero(ct *content) {
	c.vrule("hero.split", heroSplitX, heroRuleTop, heroRuleBottom, colBorder)

	c.drawHeroCol("hero.left", ct.left, contentL, heroSplitX-heroGutter, func(x0, x1 int) image.Image {
		// The card's single accent gradient, Accent → AccentHigh across the
		// decode number. Nothing else on the card ramps.
		return hGradient{x0: x0, x1: x1, c0: colAccent, c1: colAccentHigh}
	})
	c.drawHeroCol("hero.right", ct.right, heroRightX, contentR, func(int, int) image.Image {
		// Prefill is a primary figure, not an accent one: the accent is the
		// decode rate's alone, as in the TUI's emphasis contract.
		return solid(colText)
	})
}

func (c *canvas) drawHeroCol(id string, h heroCol, x0, x1 int, src func(int, int) image.Image) {
	maxW := x1 - x0

	c.text(textOpts{
		id: id + ".eyebrow", s: h.eyebrow, x: x0, baseline: heroEyebrowBase,
		style: stEyebrow, src: solid(colDim), maxW: maxW,
	})

	numW := c.measure(h.number, stHero)
	c.text(textOpts{
		id: id + ".number", s: h.number, x: x0, baseline: heroNumberBase,
		style: stHero, src: src(x0, x0+numW),
	})
	c.text(textOpts{
		id: id + ".unit", s: h.unit, x: x0 + numW + heroUnitGap, baseline: heroNumberBase,
		style: stHeroUnit, src: solid(colDim),
	})

	c.text(textOpts{
		id: id + ".sub1", s: pickWidest(c, h.sub1, h.sub1Fallbacks, stBody, maxW), x: x0, baseline: heroSub1Base,
		style: stBody, src: solid(colDim), maxW: maxW,
	})
	c.text(textOpts{
		id: id + ".sub2", s: pickWidest(c, h.sub2, h.sub2Fallbacks, stBody, maxW), x: x0, baseline: heroSub2Base,
		style: stBody, src: solid(colFaint), maxW: maxW,
	})
}

// pickWidest returns the first of want and its fallbacks that measures inside
// maxW in st, or the last one when none does — which textOpts then truncates,
// as it did before there were fallbacks. The style is a parameter since the
// identity band joined the hero sub-lines on this mechanism (2026-09-19): its
// lines are picked at stIdent, the hero's at stBody.
//
// The measurement is the canvas's, not a character count: the card is set in a
// proportional face, so "fits" is a question only the rasteriser can answer
// and a rule of thumb here would be wrong on exactly the long names it exists
// for.
func pickWidest(c *canvas, want string, fallbacks []string, st textStyle, maxW int) string {
	if c.measure(want, st) <= maxW {
		return want
	}
	for _, alt := range fallbacks {
		if alt != "" && c.measure(alt, st) <= maxW {
			return alt
		}
	}
	if n := len(fallbacks); n > 0 && fallbacks[n-1] != "" {
		return fallbacks[n-1]
	}
	return want
}

// ---------------------------------------------------------------- memory ---

func (c *canvas) drawMemory(ct *content) {
	label := c.text(textOpts{
		id: "memory.label", s: "memory placement", x: contentL, baseline: memLabelBase,
		style: stEyebrow, src: solid(colDim),
	})
	pillsLeft := c.drawPills("memory.pill", ct.pills, contentR, memPillTop)
	// The placed/RSS total sits on the label row, between the section name and
	// the pills: the legend row below needs its whole width once the VRAM
	// breakdown is in it. It is a measured line (2026-09-19): pickWidest
	// drops its trailing parts whole against the width the pill row leaves,
	// rather than letting the truncation cut inside a number.
	sumX := label.Max.X + 24
	sumW := pillsLeft - 24 - sumX
	c.text(textOpts{
		id: "memory.sum", s: pickWidest(c, ct.sum.preferred, ct.sum.fallbacks, stSmall, sumW),
		x: sumX, baseline: memLabelBase,
		style: stSmall, src: solid(colFaint), maxW: sumW,
	})

	bar := image.Rect(contentL, memBarTop, contentR, memBarTop+memBarHeight)
	mask := roundRectMask(bar, memBarRadius)
	// The track is drawn first so an unobserved or partial placement still
	// reads as a bar with a hole in it rather than as nothing at all.
	c.maskedFill("memory.bar.track", bar, bar, mask, colSurface)

	var total int64
	for _, seg := range ct.segments {
		total += seg.bytes
	}
	if total > 0 {
		x := bar.Min.X
		for i, seg := range ct.segments {
			end := bar.Max.X
			if i < len(ct.segments)-1 {
				end = x + int(float64(bar.Dx())*float64(seg.bytes)/float64(total))
			}
			// The last segment absorbs the rounding so the bar always ends
			// exactly at the content edge.
			c.drawSegment(segmentID(i), bar, image.Rect(x, bar.Min.Y, end, bar.Max.Y), mask, seg)
			x = end
		}
	}

	c.drawLegend(ct)
}

// drawSegment paints one device segment, subdividing it into weights | kv |
// compute when the placement reported the split. The divisions are 1px of
// panel colour so the three steps read as parts of one device, not as three
// devices.
func (c *canvas) drawSegment(id string, bar, r image.Rectangle, mask *image.Alpha, seg segment) {
	c.record(id, "rect", r, seg.label)
	if len(seg.parts) == 0 || seg.bytes <= 0 {
		c.maskedFill(id+".fill", bar, r, mask, seg.col)
		return
	}
	x := r.Min.X
	for i, part := range seg.parts {
		end := r.Max.X
		if i < len(seg.parts)-1 {
			end = x + int(float64(r.Dx())*float64(part.bytes)/float64(seg.bytes))
		}
		c.maskedFill(fmt.Sprintf("%s.part%d", id, i), bar, image.Rect(x, r.Min.Y, end, r.Max.Y), mask, part.col)
		if i > 0 && x > r.Min.X {
			c.maskedFill(fmt.Sprintf("%s.div%d", id, i), bar, image.Rect(x, r.Min.Y, x+1, r.Max.Y), mask, colPanel)
		}
		x = end
	}
}

func segmentID(i int) string {
	return fmt.Sprintf("memory.bar.seg%d", i)
}

// drawLegend lays the segment key out left to right across the full content
// width: first one entry per device, then the VRAM breakdown that explains the
// lightness steps inside each GPU segment. Entries that will not fit are
// dropped rather than overprinted.
func (c *canvas) drawLegend(ct *content) {
	if !ct.hasPlaced {
		c.text(textOpts{
			id: "memory.legend.none", s: "no placement observed", x: contentL, baseline: memLegendBase,
			style: stSmall, src: solid(colFaint), maxW: contentW,
		})
		return
	}

	entries := make([]legendEntry, 0, len(ct.segments)+len(ct.breakdown))
	for _, seg := range ct.segments {
		// A segment that carries its own entries names its halves itself: the
		// host's "ram" and "disk" say what one "CPU 388.1 GiB" could not.
		if len(seg.legend) > 0 {
			entries = append(entries, seg.legend...)
			continue
		}
		entries = append(entries, legendEntry{label: seg.label + " " + seg.size, col: seg.col})
	}
	entries = append(entries, ct.breakdown...)

	x := contentL
	for i, e := range entries {
		w := memLegendSwatch + memLegendGap + c.measure(e.label, stSmall)
		if x+w > contentR {
			break
		}
		sw := image.Rect(x, memLegendBase-memLegendSwatch, x+memLegendSwatch, memLegendBase)
		c.roundRect(fmt.Sprintf("memory.legend.swatch%d", i), sw, 2.5, e.col)
		c.text(textOpts{
			id: fmt.Sprintf("memory.legend.text%d", i), s: e.label,
			x: x + memLegendSwatch + memLegendGap, baseline: memLegendBase,
			style: stSmall, src: solid(colText),
		})
		x += w + memLegendSpacing
	}
}

// drawPills lays a row of pills out right to left ending at rightX and
// returns the x the row starts at, so the caller knows what space is left.
func (c *canvas) drawPills(idPrefix string, pills []pill, rightX, top int) int {
	const padX = 12
	const gap = 10

	widths := make([]int, len(pills))
	totalW := 0
	for i, p := range pills {
		widths[i] = c.measure(p.text, stPill) + 2*padX
		totalW += widths[i]
	}
	if len(pills) > 1 {
		totalW += gap * (len(pills) - 1)
	}

	left := rightX - totalW
	x := left
	baseline := top + memPillHeight - 9
	for i, p := range pills {
		r := image.Rect(x, top, x+widths[i], top+memPillHeight)
		c.roundRect(fmt.Sprintf("%s%d.bg", idPrefix, i), r, float32(memPillHeight)/2, alpha(p.col, 0x2b))
		c.record(fmt.Sprintf("%s%d", idPrefix, i), "pill", r, p.text)
		c.text(textOpts{
			id: fmt.Sprintf("%s%d.text", idPrefix, i), s: p.text,
			x: x + padX, baseline: baseline, style: stPill, src: solid(p.col),
		})
		x += widths[i] + gap
	}
	return left
}

// ----------------------------------------------------------------- ident ---

// drawIdent paints the identity band (2026-09-19): the model, the rig and the
// engine, one line each at 26 px, each picked by measurement against the full
// content width — the hero sub-lines' pickWidest mechanism at stIdent.
func (c *canvas) drawIdent(ct *content) {
	c.text(textOpts{
		id: "ident.model", s: pickWidest(c, ct.ident1.preferred, ct.ident1.fallbacks, stIdent, contentW),
		x: contentL, baseline: identLine1Base, style: stIdent, src: solid(colText), maxW: contentW,
	})
	c.text(textOpts{
		id: "ident.modelsub", s: ct.identSub, x: contentL, baseline: identSubBase,
		style: stIdentSub, src: solid(colFaint), maxW: contentW,
	})
	c.text(textOpts{
		id: "ident.rig", s: pickWidest(c, ct.ident2.preferred, ct.ident2.fallbacks, stIdent, contentW),
		x: contentL, baseline: identLine2Base, style: stIdent, src: solid(colText), maxW: contentW,
	})
	c.text(textOpts{
		id: "ident.engine", s: pickWidest(c, ct.ident3.preferred, ct.ident3.fallbacks, stIdent, contentW),
		x: contentL, baseline: identLine3Base, style: stIdent, src: solid(colText), maxW: contentW,
	})
}

// ----------------------------------------------------------------- strip ---

func (c *canvas) drawStrip(ct *content) {
	c.hairline("rule.strip", contentL, contentR, stripRuleY, colBorder)
	// The mark is set in the column labels' voice, not in an accent colour: it
	// is a provenance note, and shouting it would read as a badge the tool
	// awarded itself.
	c.text(textOpts{
		id: "strip.verified", s: "verified by toktape", x: contentL, baseline: stripFootBase,
		style: stColLabel, src: solid(colFaint),
	})
	c.text(textOpts{
		id: "strip.credit", s: creditText, x: contentR, baseline: stripFootBase,
		style: stMicro, src: solid(colFaint), align: alignRight,
	})
}

// creditText is the card's last line, the same string the text card ends with.
const creditText = "toktape · github.com/midagedev/toktape"
