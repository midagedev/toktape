//go:build unix

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// foregroundGroup reports whether this process's group is the foreground
// group of the terminal tty. An ioctl that fails is "no": the terminal could
// not say it would answer.
func foregroundGroup(tty *os.File) bool {
	pgrp, err := unix.IoctlGetInt(int(tty.Fd()), unix.TIOCGPGRP)
	return err == nil && pgrp == unix.Getpgrp()
}
