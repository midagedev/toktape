package main

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

// profileAvatar writes a small PNG for the avatar flag to point at.
func profileAvatar(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 0x80, G: uint8(x), B: uint8(y), A: 0xff})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	path := filepath.Join(t.TempDir(), "pic.png")
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatalf("write avatar: %v", err)
	}
	return path
}

func TestProfileSetAndPrint(t *testing.T) {
	publishHome(t, "")
	pic := profileAvatar(t, 32, 32)

	code, stdout, stderr := exec(t, "profile", "--name", "Lab Rat", "--link", "https://github.com/example", "--avatar", pic)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"Lab Rat", "https://github.com/example", pic, "32×32 PNG"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the save names %q nowhere:\n%s", want, stdout)
		}
	}

	// Printing reads the same file back: one field per line.
	code, stdout, stderr = exec(t, "profile")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"name    Lab Rat", "link    https://github.com/example", "bytes, 32×32 PNG"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the print carries %q nowhere:\n%s", want, stdout)
		}
	}
}

func TestProfilePrintsUnknownAsQuestion(t *testing.T) {
	publishHome(t, "")
	code, stdout, stderr := exec(t, "profile")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"name    ?", "link    ?", "avatar  ?"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("an unset field is not ?: want %q in:\n%s", want, stdout)
		}
	}
}

func TestProfileNamesAMissingAvatar(t *testing.T) {
	publishHome(t, "")
	pic := profileAvatar(t, 8, 8)
	if code, _, stderr := exec(t, "profile", "--name", "x", "--avatar", pic); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	// The file the profile points at is deleted afterwards: the print must
	// say "missing" beside the `?`, not crash and not print a stale size.
	if err := os.Remove(pic); err != nil {
		t.Fatalf("remove avatar: %v", err)
	}
	code, stdout, stderr := exec(t, "profile")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "? (missing:") || !strings.Contains(stdout, pic) {
		t.Errorf("a missing avatar is not said to be missing:\n%s", stdout)
	}
}

func TestProfileClear(t *testing.T) {
	publishHome(t, "")
	if code, _, stderr := exec(t, "profile", "--name", "Lab Rat"); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	code, stdout, stderr := exec(t, "profile", "--clear")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "cleared") {
		t.Errorf("the clear says nothing:\n%s", stdout)
	}
	code, stdout, _ = exec(t, "profile")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(stdout, "Lab Rat") {
		t.Errorf("the name survived --clear:\n%s", stdout)
	}
}

// Every refusal names the limit it hit: the message is the contract.
func TestProfileRefusalsNameTheLimit(t *testing.T) {
	publishHome(t, "")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"name too long", []string{"profile", "--name", strings.Repeat("n", 41)}, "40-rune limit"},
		{"bad link scheme", []string{"profile", "--link", "javascript:alert(1)"}, `"javascript:"`},
		{"avatar not a PNG", []string{"profile", "--avatar", func() string {
			p := filepath.Join(t.TempDir(), "note.txt")
			if err := os.WriteFile(p, []byte("not an image"), 0o600); err != nil {
				t.Fatal(err)
			}
			return p
		}()}, "not a PNG"},
		{"avatar missing", []string{"profile", "--avatar", filepath.Join(t.TempDir(), "gone.png")}, "read"},
		{"clear with setters", []string{"profile", "--clear", "--name", "x"}, "--clear"},
		{"note and note-file", []string{"publish", "x.tape", "--note", "a", "--note-file", "b"}, "--note"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := exec(t, tc.args...)
			if code == exitOK {
				t.Fatalf("%v was accepted", tc.args)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want it to name %q", stderr, tc.want)
			}
		})
	}
}
