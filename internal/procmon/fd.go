package procmon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// FDTarget returns what file descriptor fd of pid is connected to, read from
// the /proc/<pid>/fd/<fd> link: the file stdout or stderr was redirected to,
// or a kernel spelling such as "pipe:[12345]" and a device path for
// everything that is not a file. The caller decides what a target is worth;
// this only reports it.
//
// The link of another user's process is not readable without privilege, so a
// caller treats any error as "not observed" rather than as a fault — the
// same contract as Exe.
func FDTarget(fsRoot string, pid int, fd int) (string, error) {
	p := pidPath(fsRoot, pid, "fd", strconv.Itoa(fd))
	target, err := os.Readlink(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("procmon: pid %d fd %d: %w", pid, fd, ErrNotFound)
		}
		return "", fmt.Errorf("procmon: readlink %s: %w", p, err)
	}
	return strings.TrimSuffix(target, deletedSuffix), nil
}
