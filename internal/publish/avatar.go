package publish

import (
	"bytes"
	"fmt"
	"image"
	_ "image/png"
	"net/url"
	"os"
	"strings"
	"unicode"
)

// The author profile and the lab-note: what travels beside the record
// (TTP-125). The limits below are the contract's, shared by the profile verb
// (which refuses at set time), the publish verb (which refuses before
// sending) and uploadBody (which refuses the same limits before sending, so
// a hand-edited config cannot smuggle past the verb). The Worker enforces
// the same limits again server-side in web/src/upload.js — a client that
// refuses first is courtesy, the server refusing is the defence.
const (
	// MaxAuthorNameRunes bounds the nickname after TrimSpace.
	MaxAuthorNameRunes = 40
	// MaxAuthorLinkBytes bounds the one link, in bytes.
	MaxAuthorLinkBytes = 200
	// MaxTitleRunes bounds the note's title after TrimSpace.
	MaxTitleRunes = 120
	// MaxNoteRunes bounds the note's body after TrimSpace.
	MaxNoteRunes = 4000
	// MaxBioRunes bounds the user-home bio after TrimSpace (TTP-127).
	MaxBioRunes = 600
	// MaxAvatarBytes bounds the avatar file.
	MaxAvatarBytes = 65536
	// MaxAvatarWidth and MaxAvatarHeight bound the avatar's decoded header
	// dimensions. The server does not decode the image — it checks the PNG
	// signature and the size — so this side checks the dimensions at set
	// time and the bytes are what travel.
	MaxAvatarWidth  = 256
	MaxAvatarHeight = 256
)

// pngSignature is the 8-byte magic every PNG begins with, the same bytes the
// Worker checks in web/src/upload.js (isPNG).
var pngSignature = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

// Author is the per-machine profile one publish carries. Either field of the
// wire object may be absent; Avatar travels as its own part and only ever
// beside an Author.
type Author struct {
	Name   string
	Link   string
	Avatar []byte
}

// ValidateAuthorName trims and checks the nickname: 1–40 runes, no control
// characters, no newline (a newline is a control character; it is named
// because a name is printed on one line everywhere it appears).
func ValidateAuthorName(name string) (string, error) {
	t := strings.TrimSpace(name)
	n := len([]rune(t))
	if n == 0 {
		return "", fmt.Errorf("author name is empty: 1–%d runes travel", MaxAuthorNameRunes)
	}
	if n > MaxAuthorNameRunes {
		return "", fmt.Errorf("author name is %d runes, over the %d-rune limit", n, MaxAuthorNameRunes)
	}
	if strings.IndexFunc(t, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("author name carries a control character: names print on one line")
	}
	return t, nil
}

// ValidateAuthorLink trims and checks the one link: 1–200 bytes, a URL whose
// scheme is http or https with a non-empty host. The scheme is refused by
// name — javascript:, data:, file: and anything else — because this is where
// a stored XSS would enter, and the refusal has to say which scheme it saw.
func ValidateAuthorLink(link string) (string, error) {
	t := strings.TrimSpace(link)
	if t == "" {
		return "", fmt.Errorf("author link is empty: 1–%d bytes travel", MaxAuthorLinkBytes)
	}
	if len(t) > MaxAuthorLinkBytes {
		return "", fmt.Errorf("author link is %d bytes, over the %d-byte limit", len(t), MaxAuthorLinkBytes)
	}
	u, err := url.Parse(t)
	if err != nil {
		return "", fmt.Errorf("author link is not a URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		scheme := u.Scheme
		if scheme == "" {
			scheme = "(none)"
		} else {
			scheme += ":"
		}
		return "", fmt.Errorf("author link uses scheme %q: only http and https travel", scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("author link has no host: only http and https URLs with a host travel")
	}
	return t, nil
}

// ValidateTitle trims and checks the note's title: 1–120 runes, a single
// line. When the title leads an h1 it cannot contain the line break.
func ValidateTitle(title string) (string, error) {
	t := strings.TrimSpace(title)
	n := len([]rune(t))
	if n == 0 {
		return "", fmt.Errorf("title is empty: 1–%d runes travel", MaxTitleRunes)
	}
	if n > MaxTitleRunes {
		return "", fmt.Errorf("title is %d runes, over the %d-rune limit", n, MaxTitleRunes)
	}
	if strings.ContainsAny(t, "\n\r") {
		return "", fmt.Errorf("title must be a single line")
	}
	return t, nil
}

// NormalizeNote folds CRLF to LF, the only transformation the note gets. The
// body is plain text — paragraphs separated by blank lines — so nothing else
// is rewritten: what was typed is what is stored.
func NormalizeNote(note string) string {
	return strings.ReplaceAll(note, "\r\n", "\n")
}

// ValidateNote normalises, trims and checks the note's body: 1–4000 runes.
// It returns the normalised text that travels.
func ValidateNote(note string) (string, error) {
	t := strings.TrimSpace(NormalizeNote(note))
	n := len([]rune(t))
	if n == 0 {
		return "", fmt.Errorf("note is empty: 1–%d runes travel", MaxNoteRunes)
	}
	if n > MaxNoteRunes {
		return "", fmt.Errorf("note is %d runes, over the %d-rune limit", n, MaxNoteRunes)
	}
	return t, nil
}

// ValidateBio normalises, trims and checks the user-home bio (TTP-127):
// 1–600 runes, plain paragraphs like the note — CRLF folded to LF, and
// nothing else rewritten. It returns the normalised text that travels.
func ValidateBio(bio string) (string, error) {
	t := strings.TrimSpace(NormalizeNote(bio))
	n := len([]rune(t))
	if n == 0 {
		return "", fmt.Errorf("bio is empty: 1–%d runes travel", MaxBioRunes)
	}
	if n > MaxBioRunes {
		return "", fmt.Errorf("bio is %d runes, over the %d-rune limit", n, MaxBioRunes)
	}
	return t, nil
}

// ValidateAvatarBytes checks avatar bytes already in hand: PNG signature,
// at most 65536 bytes, at most 256×256 by decoded header. It returns the
// header dimensions for the --dry-run listing. The bytes are returned
// unchanged — no re-encoding, no resizing.
func ValidateAvatarBytes(b []byte) (width, height int, err error) {
	if len(b) > MaxAvatarBytes {
		return 0, 0, fmt.Errorf("avatar is %d bytes, over the %d-byte limit", len(b), MaxAvatarBytes)
	}
	if !bytes.HasPrefix(b, pngSignature) {
		return 0, 0, fmt.Errorf("avatar is not a PNG: the 8-byte signature does not match")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return 0, 0, fmt.Errorf("avatar does not decode as a PNG: %v", err)
	}
	if cfg.Width > MaxAvatarWidth || cfg.Height > MaxAvatarHeight {
		return 0, 0, fmt.Errorf("avatar is %d×%d, over the %d×%d limit", cfg.Width, cfg.Height, MaxAvatarWidth, MaxAvatarHeight)
	}
	return cfg.Width, cfg.Height, nil
}

// LoadAvatar reads the avatar file and returns its bytes unchanged. The path
// is checked to exist and be a PNG within the size and dimension limits, and
// the refusal names the exact size measured and the limit. Tilde expansion
// happens in the verb, not here: this takes the path as resolved.
func LoadAvatar(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("avatar: read %s: %w", path, err)
	}
	if _, _, err := ValidateAvatarBytes(b); err != nil {
		return nil, fmt.Errorf("avatar %s: %w", path, err)
	}
	return b, nil
}
