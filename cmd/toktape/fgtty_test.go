package main

import (
	"errors"
	"os"
	"testing"
)

// The decision with the open and the group check injected. FAIL-first
// (2026-10-11): before foregroundTTY a background process group opened
// /dev/tty and stopped on SIGTTIN at the first read; the end-to-end gate is
// tools/check-no-tty-stop.py.
func TestOpenForegroundTTYDecision(t *testing.T) {
	open := func() (*os.File, error) { return os.Open(os.DevNull) }

	asked := 0
	tty, ok := openForegroundTTY(open, func(*os.File) bool { asked++; return true })
	if !ok || tty == nil || asked != 1 {
		t.Fatalf("foreground: ok=%v tty=%v asked=%d, want a terminal", ok, tty, asked)
	}
	tty.Close()

	tty, ok = openForegroundTTY(open, func(*os.File) bool { return false })
	if ok || tty != nil {
		t.Errorf("background: ok=%v tty=%v, want refusal and no file handed out", ok, tty)
	}

	tty, ok = openForegroundTTY(
		func() (*os.File, error) { return nil, errors.New("no tty") },
		func(*os.File) bool { t.Error("group asked with no terminal"); return true })
	if ok || tty != nil {
		t.Errorf("no terminal: ok=%v tty=%v, want refusal", ok, tty)
	}
}
