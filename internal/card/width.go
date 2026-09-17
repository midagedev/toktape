// Package card renders a tape.RunSummary as the shareable result card.
//
// The card is the viral unit of toktape (docs/toktape-spec.ko.md §4): a
// fixed-layout box where every argument-settling field is always printed in
// the same place, and anything that was not observed prints as "?" — never a
// plausible default. Three renderings come out of the same summary:
//
//   - Text: a CardWidth-column Unicode box that survives a Reddit code block.
//   - Markdown: Text inside a ```text fence plus a llama-bench compatible table.
//   - JSON: the RunSummary itself, indented, for compare tooling.
//
// Width handling (handover lesson 5, "CJK is two columns"): every line of Text
// is exactly CardWidth display columns wide, measured with East Asian width so
// Hangul counts as two. The measurement deliberately does NOT follow the
// process locale — see the cond variable below.
package card

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

const (
	// CardWidth is the display width of every line of the text card, borders
	// included. 72 columns is the widest that does not wrap in the Reddit
	// mobile code block (docs/toktape-spec.ko.md §4).
	CardWidth = 72
	// innerWidth is the writable width between "│ " and " │".
	innerWidth = CardWidth - 4

	// ellipsis is appended by truncate; it is one display column wide.
	ellipsis = "…"
)

// cond measures display width for the card.
//
// runewidth's package-level functions read LANG/LC_ALL/LC_CTYPE at init and
// widen every East Asian *Ambiguous* rune to two columns on a CJK locale. The
// card is built out of ambiguous runes — the box drawing set, "█", "░", "·",
// "×", "°", "≈", "…" — so a locale-sensitive measurement would lay the same
// tape out differently on the recorder's machine and the reader's. Hangul
// syllables are category Wide, not Ambiguous, so pinning EastAsianWidth to
// false still gives them two columns, which is what the contract asks for.
var cond = &runewidth.Condition{EastAsianWidth: false, StrictEmojiNeutral: true}

// ansiPattern matches the escape sequences a terminal renderer may leave in a
// string, in the order they must be tried: OSC (terminated by BEL or ST), CSI,
// the nF sequences that carry intermediate bytes (ESC ( B and friends), and
// the bare two-character escapes.
var ansiPattern = regexp.MustCompile(
	"\x1b(?:" +
		"\\][^\x07\x1b]*(?:\x07|\x1b\\\\)" + // OSC ... BEL | ST
		"|\\[[0-?]*[ -/]*[@-~]" + // CSI
		"|[ -/]+[0-~]" + // nF: intermediate bytes then final
		"|[@-Z\\\\-_]" + // Fe/Fs/Fp two-character escapes
		")")

// StripANSI removes ANSI escape sequences from s.
func StripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	return ansiPattern.ReplaceAllString(s, "")
}

// Width returns the display width of s in terminal columns, ignoring any ANSI
// escape sequences it contains and counting East Asian wide runes (Hangul,
// Han, Kana) as two columns.
//
// Text emits no ANSI, but the TUI reuses this function on styled strings, so
// stripping is part of the contract.
func Width(s string) int {
	return cond.StringWidth(StripANSI(s))
}

// truncate shortens s so that it fits w display columns, appending "…" when
// anything was cut. A cut that would land inside a wide rune leaves the line
// one column short rather than splitting the rune.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = StripANSI(s)
	if cond.StringWidth(s) <= w {
		return s
	}
	return cond.Truncate(s, w, ellipsis)
}

// pad right-pads s with spaces to exactly w display columns, truncating first
// when it is too wide.
func pad(s string, w int) string {
	return cond.FillRight(truncate(s, w), w)
}

// repeat returns n copies of the single-column rune r.
func repeat(r rune, n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(string(r), n)
}

// ClipANSI shortens s to at most w display columns, keeping every ANSI escape
// sequence it passes so the styling that was in force still applies to what
// survives, and closing with a reset when any escape was kept. Nothing is
// appended in place of what was cut.
//
// It exists because truncate above strips the escapes: that is right for the
// card, which emits none, and wrong for the TUI, which cuts styled lines when
// the scoreboard's digits reach past the pane divider into the answer pane
// (internal/tui/scoreboard.go). Width is measured with this package's cond, so
// a cut lands on the same column the layout counted — a truncator with its own
// width table would put the frame's right border one column out on Hangul.
func ClipANSI(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if !strings.ContainsRune(s, 0x1b) {
		if cond.StringWidth(s) <= w {
			return s
		}
		return cond.Truncate(s, w, "")
	}
	var b strings.Builder
	styled := false
	rest, col := s, 0
	for rest != "" {
		if rest[0] == 0x1b {
			if loc := ansiPattern.FindStringIndex(rest); loc != nil && loc[0] == 0 {
				b.WriteString(rest[:loc[1]])
				rest = rest[loc[1]:]
				styled = true
				continue
			}
		}
		r, n := utf8.DecodeRuneInString(rest)
		rw := cond.RuneWidth(r)
		if col+rw > w {
			break
		}
		b.WriteString(rest[:n])
		rest = rest[n:]
		col += rw
	}
	if styled {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}
