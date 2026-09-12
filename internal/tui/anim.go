package tui

import (
	"math"
	"time"
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
