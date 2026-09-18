package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"runtime"
	"strings"
)

// copyToClipboard puts s on the system clipboard and says, on stderr, what
// it did — and only what it observed.
//
// Two paths, tried in this order (TTP-117, spec S10 "OSC 52 + 폴백"):
//
//  1. The platform's own clipboard tool, when one is on PATH: pbcopy on
//     macOS; wl-copy, then xclip, then xsel on Linux. A tool that exits 0
//     has copied — that is a success this program can see, so the message
//     says "copied".
//  2. OSC 52 to the controlling terminal, which is the only path that works
//     the same over SSH, where the tool that would own the clipboard runs on
//     another machine. The terminal may silently refuse (Terminal.app never
//     implements it, iTerm2 needs it enabled), so this message says what was
//     sent and not that it arrived.
//
// The order is the one that makes the message true most often: on a local
// Mac the first cut of this flag wrote OSC 52 to Terminal.app and printed a
// line about having tried, while pbpaste still held the previous clipboard
// (measured 2026-09-18). With a tool present the copy is observable; without
// one, the terminal is the only chance and the message stays honest about it.
//
// Nothing here writes to stdout: the card's contract is that it carries no
// ANSI, and a piped card must not gain the escape sequence.
func copyToClipboard(stderr io.Writer, s string) {
	if tool, args, ok := clipboardTool(runtime.GOOS, osexec.LookPath); ok {
		cmd := osexec.Command(tool, args...)
		cmd.Stdin = strings.NewReader(s)
		cmd.Stdout = io.Discard
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err == nil {
			fmt.Fprintf(stderr, "Clipboard: copied with %s\n", tool)
			return
		}
		// A tool that is present but fails (no display, no Wayland socket)
		// is not the end: the terminal may still take it.
		fmt.Fprintf(stderr, "Clipboard: %s failed (%s); trying the terminal\n", tool, strings.TrimSpace(errBuf.String()))
	}
	copyOSC52(stderr, s)
}

// clipboardTool picks the clipboard writer for goos from what lookPath can
// find, in preference order. ok is false when none is on PATH — which is the
// normal case over SSH and inside containers, not an error.
func clipboardTool(goos string, lookPath func(string) (string, error)) (tool string, args []string, ok bool) {
	var candidates [][]string
	switch goos {
	case "darwin":
		candidates = [][]string{{"pbcopy"}}
	case "linux":
		candidates = [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
	default:
		return "", nil, false
	}
	for _, c := range candidates {
		if path, err := lookPath(c[0]); err == nil {
			return path, c[1:], true
		}
	}
	return "", nil, false
}

// copyOSC52 asks the terminal to put s on the system clipboard.
//
// OSC 52 is the only clipboard path that works the same locally and over SSH,
// which is where this tool lives: the sequence is written to the terminal and
// the terminal, not the program, owns the clipboard. It is written to the
// controlling terminal rather than to stdout so a piped or redirected card
// does not gain an escape sequence it must not contain — the card's contract
// is that it carries no ANSI.
//
// The terminal may silently refuse (many do by default), so the message says
// what was attempted rather than claiming success.
func copyOSC52(stderr io.Writer, s string) {
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(s)) + "\x07"
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		fmt.Fprintln(stderr, "Clipboard: not reached — no clipboard tool did it and there is no terminal to copy through (not a tty). The card is on stdout above.")
		return
	}
	defer tty.Close()
	if _, err := io.WriteString(tty, seq); err != nil {
		fmt.Fprintf(stderr, "Clipboard: could not write to the terminal: %v\n", err)
		return
	}
	fmt.Fprintln(stderr, "Clipboard: sent to the terminal via OSC 52 (Terminal.app ignores it; iTerm2 needs it enabled)")
}
