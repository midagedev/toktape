package card

import (
	"fmt"
	"strconv"
	"strings"
)

// unknown is what the card prints for anything that was not observed. Repo
// rule: an unobserved value is the empty string or 0 and prints as "?".
// Never print a default you did not observe.
const unknown = "?"

// gib is one gibibyte; every byte figure on the card is printed in GiB.
const gib = 1 << 30

// formatGiB renders a byte count as gibibytes with one decimal. Zero is
// unknown and prints "?" — never "0.0 GiB", which would be a lie about a rig
// we failed to read.
func formatGiB(b int64) string {
	if b <= 0 {
		return unknown
	}
	return fmt.Sprintf("%.1f GiB", float64(b)/gib)
}

// formatGiBNum is formatGiB without the unit, for use inside a "a/b GiB" pair.
func formatGiBNum(b int64) string {
	if b <= 0 {
		return unknown
	}
	return strconv.FormatFloat(float64(b)/gib, 'f', 1, 64)
}

// formatRate renders a tok/s figure.
//
// One decimal below 100, integer at or above it. docs/toktape-spec.ko.md §4
// and the track contract print "68.4 tok/s", "12.1 tok/s", "96.8 tok/s" and
// "2450 tok/s"; that set is only consistent with a 100 threshold, so the
// printed samples win over the prose "1 decimal (< 10)" in the same spec.
func formatRate(v float64) string {
	if v <= 0 {
		return unknown
	}
	if v < 100 {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(v, 'f', 0, 64)
}

// formatRateUnit is formatRate with " tok/s" appended (and no unit on "?").
func formatRateUnit(v float64) string {
	r := formatRate(v)
	if r == unknown {
		return unknown + " tok/s"
	}
	return r + " tok/s"
}

// formatGBs renders a bandwidth in decimal GB/s: one decimal below 10,
// integer above (the spec prints "≈ 91 GB/s"). Bandwidth is conventionally
// decimal, so this divides by 1e9, not by 2^30.
func formatGBs(bytesPerSec int64) string {
	if bytesPerSec <= 0 {
		return unknown
	}
	v := float64(bytesPerSec) / 1e9
	if v < 10 {
		return strconv.FormatFloat(v, 'f', 1, 64) + " GB/s"
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + " GB/s"
}

// formatMs renders a millisecond figure as an integer.
func formatMs(v float64) string {
	if v <= 0 {
		return unknown
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + " ms"
}

// formatPct renders a 0..1 ratio as an integer percentage.
func formatPct(ratio float64) string {
	if ratio <= 0 {
		return "0%"
	}
	return strconv.FormatFloat(ratio*100, 'f', 0, 64) + "%"
}

// formatInt renders a count, or "?" when it is zero (unobserved).
func formatInt(n int) string {
	if n <= 0 {
		return unknown
	}
	return strconv.Itoa(n)
}

// formatFloat1 renders a float with one decimal, without treating zero as
// unknown. Used for counters whose zero is a real, observed measurement (page
// faults per token); the caller decides whether the counter was observed.
func formatFloat1(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// formatParamsB renders a parameter count the way llama-bench does: billions
// with two decimals ("35.00 B").
func formatParamsB(n int64) string {
	if n <= 0 {
		return unknown
	}
	return strconv.FormatFloat(float64(n)/1e9, 'f', 2, 64) + " B"
}

// formatFileGiB renders a file size the way llama-bench does: gibibytes with
// two decimals ("19.83 GiB").
func formatFileGiB(b int64) string {
	if b <= 0 {
		return unknown
	}
	return strconv.FormatFloat(float64(b)/gib, 'f', 2, 64) + " GiB"
}

// orUnknown returns s, or "?" when s is empty.
func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return unknown
	}
	return s
}

// yesNo renders a bool the way the card's labels read.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
