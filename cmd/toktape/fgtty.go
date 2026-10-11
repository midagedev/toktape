package main

import "os"

// foregroundTTY is the one place the program decides whether a person can be
// asked something through the controlling terminal.
//
// Opening /dev/tty is not enough: a process that has a controlling terminal
// but sits in a background process group (a script run over ssh, a job put in
// the background) opens it fine, and then the first read raises SIGTTIN and
// the first write under `tostop` raises SIGTTOU. Both stop the process, and a
// stopped process ignores TERM, so `timeout` cannot end it. The terminal is
// returned only when this process's group is the terminal's foreground group,
// so a caller that gets it can read and write without being stopped. The
// caller closes it.
func foregroundTTY() (*os.File, bool) {
	return openForegroundTTY(openDevTTY, foregroundGroup)
}

func openDevTTY() (*os.File, error) { return os.OpenFile("/dev/tty", os.O_RDWR, 0) }

// openForegroundTTY is foregroundTTY with the open and the group check
// injected, so the decision is tested without a terminal.
func openForegroundTTY(open func() (*os.File, error), foreground func(*os.File) bool) (*os.File, bool) {
	tty, err := open()
	if err != nil {
		return nil, false
	}
	if !foreground(tty) {
		tty.Close()
		return nil, false
	}
	return tty, true
}
