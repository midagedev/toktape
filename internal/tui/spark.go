package tui

import "math"

// sparkRunes are the eight sparkline levels, low to high. Index 0 is what a
// measured zero draws: an empty column would read as "not sampled".
var sparkRunes = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// stripRunes are the eight partial-block widths of the token latency strip,
// narrow to full. A fast token is a hairline, a stalled one is a solid block,
// so a page-in burst reads as a thickening of the strip.
var stripRunes = []rune{'▏', '▎', '▍', '▌', '▋', '▊', '▉', '█'}

// Severity is how alarming one cell's value is, and therefore what colour the
// caller paints it.
type Severity int

const (
	// SevNormal is a value inside the healthy band: the accent colour.
	SevNormal Severity = iota
	// SevWarn is a value past the series' first threshold: amber.
	SevWarn
	// SevBad is a value past its second: red.
	SevBad
)

// Cell is one glyph of a sparkline or a latency strip together with its
// severity. Returning cells rather than a string lets the caller paint a
// single spike without re-measuring the line.
type Cell struct {
	R   rune
	Sev Severity
}

// Sparkline renders the last w values of vals as ▁▂▃▄▅▆▇█ scaled to the
// maximum inside that window, with 0 always drawing ▁.
//
// hot is the value at or above which a cell is marked SevBad; pass 0 to mark
// nothing. An empty series returns no cells — the caller decides whether a
// blank gap or a flat line is the honest drawing, and everywhere in this
// package it is a blank gap, because no sample is not the same as a zero
// sample.
func Sparkline(vals []float64, w int, hot float64) []Cell {
	if w <= 0 || len(vals) == 0 {
		return nil
	}
	if len(vals) > w {
		vals = vals[len(vals)-w:]
	}
	maxV := 0.0
	for _, v := range vals {
		if v > maxV {
			maxV = v
		}
	}
	out := make([]Cell, len(vals))
	for i, v := range vals {
		idx := 0
		if maxV > 0 && v > 0 {
			idx = int(math.Round(v / maxV * float64(len(sparkRunes)-1)))
			if idx < 0 {
				idx = 0
			}
			if idx >= len(sparkRunes) {
				idx = len(sparkRunes) - 1
			}
		}
		sev := SevNormal
		if hot > 0 && v >= hot {
			sev = SevBad
		}
		out[i] = Cell{R: sparkRunes[idx], Sev: sev}
	}
	return out
}

// StripCeiling is how many multiples of the window's median turn a latency
// strip cell red.
const StripCeiling = 3

// LatencyStrip renders the last w inter-token latencies (milliseconds) as one
// glyph each, ▏ through █.
//
// The ramp runs from the window's median to its p95: a typical token is a
// hairline and anything at or past the p95 is a full block. Anchoring the
// bottom at the median is what makes a stall legible — a ramp that started at
// zero put every healthy token two thirds of the way up, and the page-in burst
// it exists to show was a barely thicker patch in a solid bar. Anchoring the
// top at a percentile rather than at the maximum is the other half: one 400 ms
// stall must not flatten the two hundred healthy tokens around it.
//
// A window with no spread at all (every token identical) draws flat hairlines,
// which is the honest picture — the absolute level is the p50 the caller
// prints beside the strip, not something the glyphs can carry.
//
// Colour has its own two thresholds, because a raw "past the p95" flags
// nothing useful on this data. Nearest-rank p95 lands on the window's maximum
// once the window is small, so no cell can be strictly past it; and a burst
// wide enough to cover more than one token in twenty drags the p95 up into
// itself. So amber is the p95 with a floor of half again the median — an even
// run has no outliers to mark — and red is StripCeiling times the median,
// never below the amber line.
func LatencyStrip(ms []float64, w int) []Cell {
	if w <= 0 || len(ms) == 0 {
		return nil
	}
	if len(ms) > w {
		ms = ms[len(ms)-w:]
	}
	scale := p95(ms)
	lo, hi := percentile(ms, 0.5), scale
	if hi <= lo {
		// The p95 landed on the median, which happens when the window is
		// almost flat or when a lone outlier sits outside the percentile.
		// Open the ramp to the maximum so that outlier still has somewhere to
		// go; a genuinely flat window leaves span at zero and draws hairlines.
		hi = maxOf(ms)
	}
	span := hi - lo

	warnAt := scale
	if floor := 1.5 * lo; floor > warnAt {
		warnAt = floor
	}
	badAt := StripCeiling * lo
	if badAt < warnAt {
		badAt = warnAt
	}

	out := make([]Cell, len(ms))
	for i, v := range ms {
		idx := 0
		if span > 0 {
			idx = int(math.Round((v - lo) / span * float64(len(stripRunes)-1)))
		}
		if idx < 0 {
			idx = 0
		}
		if idx >= len(stripRunes) {
			idx = len(stripRunes) - 1
		}
		sev := SevNormal
		switch {
		case badAt > 0 && v >= badAt:
			sev = SevBad
		case warnAt > 0 && v >= warnAt:
			sev = SevWarn
		}
		out[i] = Cell{R: stripRunes[idx], Sev: sev}
	}
	return out
}

func maxOf(vals []float64) float64 {
	top := 0.0
	for _, v := range vals {
		if v > top {
			top = v
		}
	}
	return top
}

// barCells splits a w-cell bar at the given 0..1 fraction and returns the
// filled and empty halves. A fraction above 0 always lights at least one cell,
// so "a little" never draws as "nothing".
//
// Both halves are the same solid block; only their colour differs. The empty
// remainder is the accent hue at its darkest, not a hatch glyph — ░ renders as
// a noisy texture in most terminal fonts and stops reading as a measure.
func barCells(frac float64, w int) (filled, empty string) {
	if w <= 0 {
		return "", ""
	}
	if math.IsNaN(frac) || frac <= 0 {
		return "", repeat('█', w)
	}
	if frac > 1 {
		frac = 1
	}
	n := int(math.Round(frac * float64(w)))
	if n == 0 {
		n = 1
	}
	if n > w {
		n = w
	}
	return repeat('█', n), repeat('█', w-n)
}

// segmentBar splits a w-cell bar into three shades in proportion to parts,
// which need not sum to total; whatever is left over draws as empty cells.
//
// It is used for the one place the weights/kv/compute split is actually
// observed — the aggregate VRAM figures on tape.PlacementSummary. Per-device
// bars do not use it: the schema records the split for the run, not per
// device, and apportioning it across devices would print a number nobody
// measured.
func segmentBar(parts []float64, total float64, w int) []string {
	out := make([]string, len(parts)+1)
	if w <= 0 {
		return out
	}
	if total <= 0 {
		out[len(parts)] = repeat('█', w)
		return out
	}
	// Largest remainder, with a floor of one cell for any part that was
	// actually measured: rounding each share independently loses the compute
	// buffer, which is the share the legend beside the bar is naming.
	exact := make([]float64, len(parts))
	n := make([]int, len(parts))
	used := 0
	for i, p := range parts {
		if p <= 0 {
			continue
		}
		exact[i] = p / total * float64(w)
		n[i] = int(exact[i])
		if n[i] == 0 {
			n[i] = 1
		}
		used += n[i]
	}
	for used < w {
		best, bestRem := -1, -1.0
		for i := range parts {
			if parts[i] <= 0 {
				continue
			}
			if rem := exact[i] - float64(n[i]); rem > bestRem {
				best, bestRem = i, rem
			}
		}
		if best < 0 {
			break
		}
		n[best]++
		used++
	}
	for used > w {
		big, bigN := -1, 1
		for i := range parts {
			if n[i] > bigN {
				big, bigN = i, n[i]
			}
		}
		if big < 0 {
			break
		}
		n[big]--
		used--
	}
	// Every share is the same block glyph; the caller separates them by
	// lightness, the same way the filled and empty halves of a plain bar are
	// separated. Shade-glyph shares (▓, ▒) read as hatching, not as volume.
	for i := range parts {
		out[i] = repeat('█', n[i])
	}
	out[len(parts)] = repeat('█', w-used)
	return out
}
