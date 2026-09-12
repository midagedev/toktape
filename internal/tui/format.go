package tui

import (
	"strconv"
	"time"
)

// unknown is what the screen prints for anything that was not observed. Repo
// rule (CLAUDE.md): an unobserved value is "" or 0 and prints "?" — never a
// plausible default.
const unknown = "?"

const gib = 1 << 30

// fmtG renders a byte count in gibibytes for a narrow column: "20.6G".
// The unit is compressed to one letter because the right pane is 28 columns
// wide and the figures have to stay tabular.
func fmtG(b int64) string {
	if b <= 0 {
		return unknown
	}
	v := float64(b) / gib
	if v >= 100 {
		return strconv.FormatFloat(v, 'f', 0, 64) + "G"
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + "G"
}

// fmtRate renders a tok/s figure the way internal/card does: one decimal below
// 100, integer at or above it, "?" when it was never measured.
func fmtRate(v float64) string {
	if v <= 0 {
		return unknown
	}
	if v < 100 {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(v, 'f', 0, 64)
}

// fmtMs renders a millisecond figure as an integer with its unit.
func fmtMs(v float64) string {
	if v <= 0 {
		return unknown
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + " ms"
}

// fmtPct renders a 0..1 ratio as an integer percentage. Zero is a real
// measurement for a cache hit ratio, so it prints "0%", not "?".
func fmtPct(ratio float64) string {
	if ratio < 0 {
		return unknown
	}
	return strconv.FormatFloat(ratio*100, 'f', 0, 64) + "%"
}

// fmtFloat1 renders a float with one decimal, treating zero as a measurement.
func fmtFloat1(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// orUnknown returns s, or "?" when s is empty.
func orUnknown(s string) string {
	if s == "" {
		return unknown
	}
	return s
}

// msOf converts a duration to float milliseconds.
func msOf(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
