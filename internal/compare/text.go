package compare

import (
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/card"
)

// Column widths of the metric table. They add up to exactly Width with the
// three single-space separators, so the table has the same right edge as the
// card's box.
const (
	labelW = 24
	colW   = 15
)

// Text renders a report as a Width-column plain-text block: an A/B header, a
// metric table with a relative-change column, then the flags and the metadata
// that differ.
//
// The output carries no ANSI and no box drawing. A compare block is read next
// to a card, not instead of one, and a second box around it would fight the
// card's for the reader's eye.
func Text(r Report) string {
	var b strings.Builder

	b.WriteString("toktape compare\n")
	b.WriteString("  A  " + trunc(r.IDA, Width-5) + "\n")
	b.WriteString("  B  " + trunc(r.IDB, Width-5) + "\n")
	b.WriteString(rule() + "\n")

	b.WriteString(row("metric", "A", "B", "Δ") + "\n")
	for _, m := range r.Metrics {
		b.WriteString(row(m.Label, m.A, m.B, delta(m)) + "\n")
	}

	b.WriteString("\n" + section("flags") + "\n")
	if len(r.Flags) == 0 {
		b.WriteString("  (identical)\n")
	}
	for _, c := range r.Flags {
		b.WriteString("  " + changeLine(c) + "\n")
	}

	b.WriteString("\n" + section("run") + "\n")
	if len(r.Meta) == 0 {
		b.WriteString("  (same model, build and engine)\n")
	}
	for _, c := range r.Meta {
		b.WriteString("  " + changeLine(c) + "\n")
	}

	return b.String()
}

// row lays out one table line: a left label and three right-aligned columns.
func row(label, a, b, d string) string {
	return padRight(label, labelW) + " " +
		padLeft(a, colW) + " " +
		padLeft(b, colW) + " " +
		padLeft(d, colW)
}

// rule is the horizontal separator under the header.
func rule() string { return strings.Repeat("─", Width) }

// section is a dim-small-caps-style heading in plain text: the card's titles
// are lower case, so these are too.
func section(name string) string { return name }

// delta renders the relative-change cell. A metric with no baseline prints
// nothing rather than "+Inf%" or a misleading "0%".
func delta(m Row) string {
	if !m.HasDelta {
		return ""
	}
	v := m.DeltaPct
	sign := "+"
	if v < 0 {
		sign = "-"
		v = -v
	}
	if v < 10 {
		return sign + strconv.FormatFloat(v, 'f', 1, 64) + "%"
	}
	return sign + strconv.FormatFloat(v, 'f', 0, 64) + "%"
}

// changeLine renders one flag or metadata change.
func changeLine(c Change) string {
	label := padRight(c.Label, 14)
	switch {
	case c.Added:
		return trunc(label+"+ "+c.B, Width-2)
	case c.Removed:
		return trunc(label+"- "+c.A, Width-2)
	default:
		return trunc(label+c.A+" → "+c.B, Width-2)
	}
}

// padRight and padLeft measure with card.Width so a value that ever carries a
// wide rune still lands on the same column (handover lesson 5).
func padRight(s string, w int) string {
	s = trunc(s, w)
	if n := w - card.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func padLeft(s string, w int) string {
	s = trunc(s, w)
	if n := w - card.Width(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// trunc shortens s to w display columns, marking the cut with "…".
func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if card.Width(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := card.Width(string(r))
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}
