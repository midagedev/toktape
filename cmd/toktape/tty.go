package main

import (
	"io"
	"os"
)

// isTTY reports whether w is a terminal.
//
// Two decisions hang on this and they pull in opposite directions, which is
// why it is one helper and not two: a terminal gets the redrawn spinner line
// and the live screen, and everything else — a pipe, a file, a CI log — gets
// plain lines that are still readable when nothing can move. The check is the
// character-device bit rather than a dependency, because that is all either
// decision needs and it behaves the same on Linux and macOS.
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
