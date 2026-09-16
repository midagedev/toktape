package tui

import (
	"math"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Everything that moves on this screen is a pure function of the clip time t.
//
// That is decision 10 of docs/toktape-spec.ko.md: live and replay share one
// render function, so a recorded tape must reproduce the exact frames a viewer
// saw. No function in this file reads time.Now; the only place that does is
// run.go, which turns the wall clock into a clip time once.
const (
	// easeDur is how long a bar or a number takes to travel to a new value.
	easeDur = 300 * time.Millisecond
	// breathDur is one full cycle of the active stream's cursor.
	breathDur = 1200 * time.Millisecond
	// spinFrame is one braille spinner frame.
	spinFrame = 80 * time.Millisecond
	// shimmerDur is one pass of the bright segment along the top border.
	shimmerDur = 4 * time.Second
	// shimmerW is how many cells of the top border the bright segment covers.
	shimmerW = 6
	// TickInterval is the redraw period of the live program. The tick carries
	// no state; it only advances t.
	TickInterval = 50 * time.Millisecond
	// GleamSweep is the one pass of light the result modal's figures take when
	// the card appears: CardAge 0 to GleamSweep, settled after. It is the modal
	// clock's only use, so it lives here beside the other durations.
	GleamSweep = 1400 * time.Millisecond
)

// The gleam's shape. A band of five lightnesses-worth of travel, leaning
// forward by two columns a row so it reads as a slash of light rather than a
// vertical wipe (the italic the face cannot carry: shearing the glyphs
// themselves steps in whole columns, which breaks the "5" and the "8" and
// grows a five-digit figure past the modal's column).
const (
	gleamBand  = 5
	gleamSlant = 2
	// gleamLead is the travel added at each end so the lean enters and leaves
	// clean: the largest lean offset is gleamSlant*(bigRows-1)/2, and an
	// extension of exactly that still leaves the top row's first cell at
	// distance 5 — inside the band — when the sweep starts. One more settles
	// both ends, for every figure width from 1 to 35.
	gleamLead = gleamSlant*(bigRows-1)/2 + 1
)

// spinnerFrames is the spinner shown while a stream is still in prefill.
//
// Quarter circles rather than the braille wheel this started as: braille is
// missing from enough monospace fonts to render as a replacement box, and a
// tool whose whole pitch is a terminal screenshot cannot afford a tofu in the
// first frame of the clip.
var spinnerFrames = []rune{'◐', '◓', '◑', '◒'}

// easeOutCubic maps a 0..1 progress to a 0..1 eased position. Out-cubic is the
// curve that reads as "a value settling", not as "a value sliding".
func easeOutCubic(p float64) float64 {
	switch {
	case p <= 0:
		return 0
	case p >= 1:
		return 1
	}
	q := 1 - p
	return 1 - q*q*q
}

// ease interpolates from prev to cur over easeDur, starting at since.
//
// t before since gives prev, t at or after since+easeDur gives cur. This is
// the single helper behind every moving bar and every counting number, so a
// frame never shows two quantities easing at different rates.
func ease(prev, cur float64, since, t time.Duration) float64 {
	if t <= since {
		return prev
	}
	p := float64(t-since) / float64(easeDur)
	return prev + (cur-prev)*easeOutCubic(p)
}

// spinnerAt returns the braille frame for clip time t.
func spinnerAt(t time.Duration) string {
	if t < 0 {
		t = 0
	}
	return string(spinnerFrames[int(t/spinFrame)%len(spinnerFrames)])
}

// breathPhase returns 0, 1 or 2: which of the three accent shades the active
// stream's cursor wears at clip time t. The cycle is breathDur long and steps
// low → mid → high → mid, so the cursor pulses rather than blinking.
func breathPhase(t time.Duration) int {
	if t < 0 {
		t = 0
	}
	step := int(t/(breathDur/4)) % 4
	switch step {
	case 0:
		return 0
	case 1, 3:
		return 1
	default:
		return 2
	}
}

// shimmerStart returns the first column of the bright segment travelling along
// a rule of w columns at clip time t.
//
// The segment wraps: what runs off the right edge re-enters on the left in the
// same frame, so the highlight is always somewhere on the rule. A version that
// let it leave the screen spent part of every cycle showing nothing, which
// reads as an intermittent flicker rather than a slow sweep. Callers test a
// column with (col - start) mod w < shimmerW.
func shimmerStart(t time.Duration, w int) int {
	if w <= 0 {
		return 0
	}
	if t < 0 {
		t = 0
	}
	phase := float64(t%shimmerDur) / float64(shimmerDur)
	return int(phase*float64(w)) % w
}

// gleamStyle is the style of one cell of a result-modal figure: row r of
// bigRows (0 at the top) and column x of a figure w columns wide, at phase
// p = CardAge/GleamSweep.
//
// One hue, five lightnesses, one pass (TTP-28: the accent is reserved for
// exactly these figures, so spending four more of its lightnesses on them
// breaks nothing the contract holds; a second hue would). At p ≥ 1 the answer
// is accentBold on every cell — byte-identical in colour to the settled card,
// which is load-bearing: the poster frame, the thumbnail and the last four
// seconds of the card hold are exactly what they were before the gleam
// existed, and the GIF encoder's dropped-frame run depends on those frames
// not moving. That is also why the sweep happens once and not on a loop: a
// repeating gleam would keep the card's frames different from each other for
// the whole hold, which is the one property the card's settled tail has.
//
// The band leans: the top row is lit ahead of the bottom row by
// gleamSlant*(bigRows-1)/2 columns, so the light reads as a slash travelling
// across the figures rather than a bar wiping them. The lean widens the
// travel at both ends — the band reaches the top row's first cell before it
// would reach an unleaned one, and leaves the bottom row's last cell after —
// so the travel is w + 2*gleamBand + 2*gleamLead with the band starting at
// -(gleamBand + gleamLead), and at p = 0 and p = 1 every cell of every figure
// width is back on accentBold rather than snapping to it mid-lit.
func gleamStyle(th Theme, p float64, w, r, x int) lipgloss.Style {
	if p >= 1 {
		return th.accentBold
	}
	c := p*float64(w+2*gleamBand+2*gleamLead) - float64(gleamBand+gleamLead) +
		float64(gleamSlant)*float64((bigRows-1)/2-r)
	// Signed, not absolute: the figure is dim ahead of the band and settled
	// behind it, so the pass reads as the number lighting up rather than as a
	// brighter smudge travelling over an already-lit one (2026-09-16). The
	// end state is unchanged — every cell is accentBold once the band has
	// passed it, which is what keeps the settled tail and the poster frame
	// byte-identical to a modal with no gleam at all.
	d := float64(x) - c
	switch {
	case d > float64(gleamBand):
		return th.accentMuted // not lit yet
	case d > 2:
		return th.accentMid
	case d > 1:
		return th.accentHigh
	case d >= -1:
		return th.gleamPeak // the core
	case d >= -3:
		return th.accentHigh
	}
	return th.accentBold
}

// p95 returns the 95th percentile of vals using the nearest-rank method. An
// empty slice gives 0; the callers treat that as "no scale yet".
func p95(vals []float64) float64 {
	return percentile(vals, 0.95)
}

// percentile returns the q-quantile of vals by nearest rank. vals is copied
// before sorting so the caller's window is not reordered.
func percentile(vals []float64, q float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	cp := make([]float64, len(vals))
	copy(cp, vals)
	insertionSort(cp)
	rank := int(math.Ceil(q*float64(len(cp)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(cp) {
		rank = len(cp) - 1
	}
	return cp[rank]
}

// insertionSort sorts in place. The windows here are tens of elements wide, so
// this avoids pulling sort into a hot render path for no measurable gain.
func insertionSort(v []float64) {
	for i := 1; i < len(v); i++ {
		x := v[i]
		j := i - 1
		for j >= 0 && v[j] > x {
			v[j+1] = v[j]
			j--
		}
		v[j+1] = x
	}
}
