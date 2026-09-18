package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TTP-117: --copy on a Mac terminal wrote OSC 52 to a terminal that ignores
// it and printed a line about having tried. The fallback order is the
// platform's own tool first — an observable success — then the terminal.
func TestClipboardToolOrder(t *testing.T) {
	present := func(have ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			for _, h := range have {
				if h == name {
					return "/usr/bin/" + name, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	cases := []struct {
		goos     string
		have     []string
		wantTool string
		wantArgs string
		wantOK   bool
	}{
		{"darwin", []string{"pbcopy"}, "/usr/bin/pbcopy", "", true},
		{"darwin", nil, "", "", false},
		{"linux", []string{"xclip", "wl-copy"}, "/usr/bin/wl-copy", "", true},
		{"linux", []string{"xclip", "xsel"}, "/usr/bin/xclip", "-selection clipboard", true},
		{"linux", []string{"xsel"}, "/usr/bin/xsel", "--clipboard --input", true},
		{"linux", nil, "", "", false},
		{"windows", []string{"clip"}, "", "", false},
	}
	for _, c := range cases {
		tool, args, ok := clipboardTool(c.goos, present(c.have...))
		if ok != c.wantOK || tool != c.wantTool || strings.Join(args, " ") != c.wantArgs {
			t.Errorf("%s with %v: got (%q, %q, %v), want (%q, %q, %v)", c.goos, c.have, tool, strings.Join(args, " "), ok, c.wantTool, c.wantArgs, c.wantOK)
		}
	}
}

// The tool on PATH receives the text on stdin and the message says "copied";
// a stand-in tool proves the wiring without touching the real clipboard.
func TestCopyToClipboardUsesTheTool(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no clipboard tool is looked up on " + runtime.GOOS)
	}
	dir := t.TempDir()
	sink := filepath.Join(dir, "sink")
	name := "pbcopy"
	if runtime.GOOS == "linux" {
		name = "wl-copy"
	}
	script := "#!/bin/sh\n/bin/cat > " + sink + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	var stderr strings.Builder
	copyToClipboard(&stderr, "the card\n")

	got, err := os.ReadFile(sink)
	if err != nil {
		t.Fatalf("the tool was not run: %v\nstderr: %s", err, stderr.String())
	}
	if string(got) != "the card\n" {
		t.Errorf("the tool received %q", got)
	}
	if !strings.Contains(stderr.String(), "Clipboard: copied with ") {
		t.Errorf("the message does not say it copied: %q", stderr.String())
	}
}

// With no tool on PATH and no terminal the message says the clipboard was
// not reached — never "copied", never "sent" (FAIL-first: the previous
// message read "sent to the terminal" only when /dev/tty opened, and said
// nothing about the tool it never looked for).
func TestCopyToClipboardSaysNotReached(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		t.Skip("a controlling terminal is present; the OSC 52 path would write to it")
	}
	var stderr strings.Builder
	copyToClipboard(&stderr, "x")
	msg := stderr.String()
	if !strings.Contains(msg, "not reached") || strings.Contains(msg, "copied") {
		t.Errorf("message: %q", msg)
	}
}
