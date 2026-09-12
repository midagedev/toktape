package compare

import "strconv"

// The formatters mirror internal/card's unexported ones so a figure reads the
// same in a compare block as on the card it came from. They are duplicated
// rather than imported because card keeps them private; see the report's API
// proposal.

const gib = 1 << 30

// formatMs renders a millisecond figure. Zero is unobserved and prints "?".
func formatMs(v float64) string {
	if v <= 0 {
		return "?"
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + " ms"
}

// formatRate renders a tok/s figure: one decimal below 100, integer above.
func formatRate(v float64) string {
	if v <= 0 {
		return "?"
	}
	if v < 100 {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(v, 'f', 0, 64)
}

// formatBytesGiB renders a byte count in gibibytes.
func formatBytesGiB(v float64) string {
	if v <= 0 {
		return "?"
	}
	return strconv.FormatFloat(v/gib, 'f', 1, 64) + " GiB"
}

// formatFloat1 renders a counter whose zero is a real measurement.
func formatFloat1(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// formatPct renders an already-scaled percentage.
func formatPct(v float64) string {
	if v <= 0 {
		return "0%"
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + "%"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func itoa(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}
