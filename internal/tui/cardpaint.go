package tui

import (
	"strings"
)

// Painting the text card for the card screen (TTP-42, lead 2026-09-13).
//
// The card is produced by internal/card as plain text: a box-drawing frame, a
// column of labels and figures, and two VRAM gauges built out of █ and ░. The
// card screen used to paint all of it in one tone, th.text, which put twenty
// solid gauge cells and a seventy-column frame at the same brightness as the
// figures they exist to qualify. On the last frame of a clip — the one a
// reader looks at longest — the two brightest things on the screen were a bar
// and a border.
//
// So the card screen paints by role, the way every other region has since the
// emphasis contract (TTP-28): a shape wears the muted accent, an empty measure
// wears the dark fill, chrome is dim, and what is left lit is the text. The
// card's content is not touched — internal/card owns what it says and how wide
// it is; this file only decides what colour each rune it already wrote is.

// cardRole is what one rune of the card is for.
type cardRole int

const (
	cardFigure cardRole = iota // everything the card says: labels, figures, the brand
	cardFill                   // the filled part of a gauge
	cardTrack                  // the empty remainder of a gauge
	cardChrome                 // the box-drawing frame and its rules
)

// cardRoleOf classifies one rune of the card.
//
// The classes are rune sets rather than positions because the card's layout is
// internal/card's to change: a gauge that moves to another row, or a rule that
// gains a ┬, keeps its role without this file being told.
func cardRoleOf(r rune) cardRole {
	switch r {
	case '█':
		return cardFill
	case '░':
		return cardTrack
	case '┌', '┐', '└', '┘', '├', '┤', '┬', '┴', '┼', '─', '│':
		return cardChrome
	}
	return cardFigure
}

// paintCardLine paints one line of the text card for the card screen.
//
// A run of one role is emitted as a single styled segment, so the card's frame
// costs one escape sequence per line rather than one per column. The plain
// theme returns the line unchanged, which is what keeps the coloured rendering
// the plain one with escapes added (TestColourMatchesPlain, which covers
// ModeCard) and keeps every width the caller computed true.
func paintCardLine(th Theme, line string) string {
	if !th.colour || line == "" {
		return line
	}
	runes := []rune(line)
	var b strings.Builder
	for i := 0; i < len(runes); {
		role := cardRoleOf(runes[i])
		j := i
		for j < len(runes) && cardRoleOf(runes[j]) == role {
			j++
		}
		b.WriteString(th.paint(th.cardStyle(role), string(runes[i:j])))
		i = j
	}
	return b.String()
}

// cardStyle is the style each role is drawn in. The fill is the shade the
// right pane's placement bars wear, so the two measures on a clip's last two
// frames read as the same kind of thing.
func (th Theme) cardStyle(role cardRole) style {
	switch role {
	case cardFill:
		return th.accentMuted
	case cardTrack:
		return th.darkFill
	case cardChrome:
		return th.dim
	}
	return th.text
}
