// Package fonts embeds the typefaces the PNG result card is drawn with.
//
// Both families are licensed under the SIL Open Font License 1.1; the license
// texts ship next to the files and must stay with them:
//
//   - JetBrains Mono (JetBrainsMono-OFL.txt) — Latin, digits and the box/maths
//     runes the card uses. Advance is exactly 0.6 em at every weight, which is
//     what makes the card's tabular numbers line up.
//   - D2Coding (D2Coding-OFL.txt) — Hangul fallback. Hangul syllables are
//     exactly 2× the Latin advance in this face, which is the property
//     docs/research/00-handover-brief.md lesson 5 asks for; most monospace
//     faces do not have it.
//
// The bytes are embedded so a `toktape` binary renders the same card on a
// machine with no fonts installed. That costs ~5 MB of binary; see the PNG
// track report for the subsetting follow-up.
package fonts

import _ "embed"

// JetBrainsMonoRegular is the body face: labels, sub-lines, the flags strip.
//
//go:embed JetBrainsMono-Regular.ttf
var JetBrainsMonoRegular []byte

// JetBrainsMonoBold is the face for column titles and pill text.
//
//go:embed JetBrainsMono-Bold.ttf
var JetBrainsMonoBold []byte

// JetBrainsMonoExtraBold is the display face: the wordmark and the two hero
// numbers.
//
//go:embed JetBrainsMono-ExtraBold.ttf
var JetBrainsMonoExtraBold []byte

// D2CodingRegular is the Hangul fallback, used for any rune JetBrains Mono
// has no glyph for.
//
//go:embed D2Coding-Regular.ttf
var D2CodingRegular []byte
