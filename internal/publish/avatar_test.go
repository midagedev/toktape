package publish

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tinyPNG builds a w×h PNG in memory, so the tests never depend on a fixture
// file surviving beside them.
func tinyPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x80, A: 0xff})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return b.Bytes()
}

func writeTemp(t *testing.T, b []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "avatar.png")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write temp avatar: %v", err)
	}
	return path
}

func TestLoadAvatarRoundTripsBytes(t *testing.T) {
	want := tinyPNG(t, 16, 16)
	got, err := LoadAvatar(writeTemp(t, want))
	if err != nil {
		t.Fatalf("LoadAvatar: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("LoadAvatar re-encoded or resized the file: the bytes must travel unchanged")
	}
}

func TestLoadAvatarRefusals(t *testing.T) {
	for _, c := range []struct {
		name  string
		build func(t *testing.T) []byte
		wants []string
	}{
		{
			name: "too big",
			build: func(t *testing.T) []byte {
				return append(append([]byte{}, pngSignature...), make([]byte, 70000-len(pngSignature))...)
			},
			wants: []string{"70000", "65536"},
		},
		{
			name:  "not a PNG",
			build: func(t *testing.T) []byte { return []byte("this is not an image") },
			wants: []string{"not a PNG"},
		},
		{
			name:  "too wide",
			build: func(t *testing.T) []byte { return tinyPNG(t, 300, 10) },
			wants: []string{"300×10", "256"},
		},
		{
			name:  "too tall",
			build: func(t *testing.T) []byte { return tinyPNG(t, 10, 300) },
			wants: []string{"10×300", "256"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadAvatar(writeTemp(t, c.build(t)))
			if err == nil {
				t.Fatalf("LoadAvatar accepted %s", c.name)
			}
			for _, want := range c.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to name %q", err, want)
				}
			}
		})
	}
}

func TestLoadAvatarMissingFile(t *testing.T) {
	_, err := LoadAvatar(filepath.Join(t.TempDir(), "no-such-file.png"))
	if err == nil {
		t.Fatal("LoadAvatar accepted a missing file")
	}
}

func TestValidateAuthorName(t *testing.T) {
	if _, err := ValidateAuthorName("Lab Rat"); err != nil {
		t.Errorf("ValidateAuthorName: %v", err)
	}
	for _, c := range []struct {
		name, in, wants string
	}{
		{"empty", "   ", "1–40"},
		{"41 runes", strings.Repeat("r", 41), "41 runes, over the 40-rune limit"},
		{"newline", "a\nb", "control"},
		{"control", "a\x07b", "control"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ValidateAuthorName(c.in); err == nil {
				t.Fatalf("ValidateAuthorName accepted %q", c.in)
			} else if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("error = %q, want it to name %q", err, c.wants)
			}
		})
	}
}

func TestValidateAuthorLink(t *testing.T) {
	for _, ok := range []string{"https://github.com/example", "http://example.com/x"} {
		if _, err := ValidateAuthorLink(ok); err != nil {
			t.Errorf("ValidateAuthorLink(%q): %v", ok, err)
		}
	}
	for _, c := range []struct {
		name, in, wants string
	}{
		{"javascript scheme", "javascript:alert(1)", `"javascript:"`},
		{"data scheme", "data:text/html,<p>x", `"data:"`},
		{"file scheme", "file:///etc/passwd", `"file:"`},
		{"no scheme", "github.com/example", "only http and https"},
		{"no host", "https://", "no host"},
		{"empty", "", "1–200"},
		{"too long", "https://example.com/" + strings.Repeat("p", 200), "over the 200-byte limit"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ValidateAuthorLink(c.in); err == nil {
				t.Fatalf("ValidateAuthorLink accepted %q", c.in)
			} else if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("error = %q, want it to name %q", err, c.wants)
			}
		})
	}
}

func TestValidateTitle(t *testing.T) {
	if _, err := ValidateTitle("First ik_llama sweep"); err != nil {
		t.Errorf("ValidateTitle: %v", err)
	}
	if _, err := ValidateTitle(strings.Repeat("t", 121)); err == nil {
		t.Fatal("ValidateTitle accepted 121 runes")
	} else if !strings.Contains(err.Error(), "121 runes, over the 120-rune limit") {
		t.Errorf("error = %q, want it to name the count and the limit", err)
	}
	if _, err := ValidateTitle("one\ntwo"); err == nil {
		t.Fatal("ValidateTitle accepted two lines")
	} else if !strings.Contains(err.Error(), "single line") {
		t.Errorf("error = %q, want it to say single line", err)
	}
}

func TestValidateNote(t *testing.T) {
	got, err := ValidateNote("Trying -fa on.\r\n\r\nSecond paragraph.")
	if err != nil {
		t.Fatalf("ValidateNote: %v", err)
	}
	if strings.Contains(got, "\r") {
		t.Errorf("CRLF survived normalisation: %q", got)
	}
	if _, err := ValidateNote(strings.Repeat("n", 4001)); err == nil {
		t.Fatal("ValidateNote accepted 4001 runes")
	} else if !strings.Contains(err.Error(), "4001 runes, over the 4000-rune limit") {
		t.Errorf("error = %q, want it to name the count and the limit", err)
	}
}

// The bio is paragraphs like the note with its own limit (TTP-127): 600
// runes travel, 601 do not, CRLF is folded, and the refusal names the
// count and the limit.
func TestValidateBio(t *testing.T) {
	got, err := ValidateBio("Line one.\r\n\r\nLine two.")
	if err != nil {
		t.Fatalf("ValidateBio: %v", err)
	}
	if got != "Line one.\n\nLine two." {
		t.Errorf("ValidateBio = %q, want CRLF folded to LF", got)
	}
	if _, err := ValidateBio(strings.Repeat("b", 600)); err != nil {
		t.Errorf("ValidateBio refused 600 runes: %v", err)
	}
	if _, err := ValidateBio(strings.Repeat("b", 601)); err == nil {
		t.Fatal("ValidateBio accepted 601 runes")
	} else if !strings.Contains(err.Error(), "601 runes, over the 600-rune limit") {
		t.Errorf("error = %q, want it to name the count and the limit", err)
	}
	if _, err := ValidateBio("   "); err == nil {
		t.Fatal("ValidateBio accepted an empty bio")
	} else if !strings.Contains(err.Error(), "1–600") {
		t.Errorf("error = %q, want it to name the range", err)
	}
}
