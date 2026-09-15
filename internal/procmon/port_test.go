package procmon

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// netTCP is the shape of /proc/net/tcp on a real box: a header line, a LISTEN
// row on port 8000 (0x1F40) whose socket inode pid 2001 holds, an
// ESTABLISHED row on the same port that must be ignored (state 01, not 0A),
// and a LISTEN row on another port. Column layout is whitespace-split:
// local_address is field 1, st field 3, inode field 9.
const netTCP = `sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
 0: 0100007F:1F40 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0
 1: 0100007F:1F40 0100007F:C7FD 01 00000000:00000000 00:00000000 00000000     0        0 99999 1 0000000000000000 20 4 30 10 -1
 2: 00000000:1F47 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 777 1 0000000000000000 100 0 0 10 0
 3: 00000000:1F42 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 88888 1 0000000000000000 100 0 0 10 0
`

// netTCP6 carries the tcp6-only listener: port 8001 (0x1F41), inode 22222.
const netTCP6 = `sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
 0: 00000000000000000000000001000000:1F41 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 22222 1 0000000000000000 100 0 0 10 0
`

// portFixtureRoot builds a /proc tree whose sockets are held by known pids:
// 2001 holds inode 12345 (tcp, port 8000), 2002 holds 777 (tcp, port 8007),
// 2003 holds 22222 (tcp6, port 8001), and 2004 and 2005 BOTH hold 88888
// (tcp, port 8002) — the ambiguity FindPIDByPort must refuse rather than
// guess at.
func portFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("proc/net/tcp", netTCP)
	write("proc/net/tcp6", netTCP6)
	pid := func(id, inode string) {
		write(filepath.Join("proc", id, "cmdline"), "\x00")
		write(filepath.Join("proc", id, "stat"), id+" (test) S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n")
		// The fd table is where the socket inode lives: one symlink per open
		// descriptor, "socket:[<inode>]".
		if err := os.MkdirAll(filepath.Join(root, "proc", id, "fd"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("socket:["+inode+"]", filepath.Join(root, "proc", id, "fd", "3")); err != nil {
			t.Fatal(err)
		}
	}
	pid("2001", "12345")
	pid("2002", "777")
	pid("2003", "22222")
	pid("2004", "88888")
	pid("2005", "88888")
	return root
}

// TestFindPIDByPort: the pid that holds the LISTEN socket of a port. tcp and
// tcp6 are both read; a port nobody listens on is an error, and so is a port
// whose socket two processes hold — naming one of them would be a guess.
func TestFindPIDByPort(t *testing.T) {
	root := portFixtureRoot(t)
	if pid, err := FindPIDByPort(root, 8000); err != nil || pid != 2001 {
		t.Errorf("FindPIDByPort(8000) = %d, %v; want 2001, nil", pid, err)
	}
	// The listener is only in net/tcp6.
	if pid, err := FindPIDByPort(root, 8001); err != nil || pid != 2003 {
		t.Errorf("FindPIDByPort(8001) = %d, %v; want 2003, nil", pid, err)
	}
	// An ESTABLISHED row on a port is not a listener, and port 7000 has no
	// row at all.
	if _, err := FindPIDByPort(root, 7000); err == nil {
		t.Error("FindPIDByPort(7000) = nil error, want an error for a port nothing listens on")
	} else if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindPIDByPort(7000) error = %v, want ErrNotFound", err)
	}
	// Two pids hold the one socket: an error naming the count, never a pick.
	_, err := FindPIDByPort(root, 8002)
	if err == nil {
		t.Fatal("FindPIDByPort(8002) = nil error, want an error naming the ambiguity")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(2)) {
		t.Errorf("error does not name the count: %v", err)
	}
}
