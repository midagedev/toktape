#!/usr/bin/env python3
"""Gate: a first publish from a background process group must not stop.

Failure mode it blocks: a first publish from a background process group stops
on SIGTTIN and ignores TERM. The process has a controlling terminal, /dev/tty
opens, and reading it from outside the foreground group stops the process;
`timeout` cannot end a stopped process (18 minutes lost over ssh, 2026-10-11).

pty.fork gives a child a controlling terminal. That child forks a grandchild
that moves to its own process group (so it is not the terminal's foreground
group) and execs `toktape publish <tape> --url http://127.0.0.1:1` with a
hermetic HOME (no config, so it is a first publish) and stdin from /dev/null.
The child polls the grandchild with waitpid(WNOHANG | WUNTRACED) for up to
10 s and reports what it saw on a pipe; a stopped grandchild is SIGKILLed by
its captured pid.

PASS: it exits non-zero with "no terminal to confirm on" on stderr.
FAIL: it stops (WIFSTOPPED) or is still running after 10 s.

Usage: python3 tools/check-no-tty-stop.py <toktape binary> <tape>
"""
import os
import pty
import select
import signal
import sys
import tempfile
import time

LIMIT = 10.0
WANT = "no terminal to confirm on"


def grandchild(binary, tape, home, errw):
    os.setpgid(0, 0)
    os.dup2(os.open(os.devnull, os.O_RDONLY), 0)
    os.dup2(errw, 2)
    env = {"PATH": os.environ.get("PATH", ""), "HOME": home,
           "XDG_CONFIG_HOME": home + "/.config", "XDG_DATA_HOME": home + "/.local"}
    try:
        os.execve(binary, [binary, "publish", tape, "--url", "http://127.0.0.1:1"], env)
    finally:
        os._exit(127)


def watcher(gpid, statusw):
    """Runs in the pty child: poll the grandchild and write one verdict line."""
    deadline = time.time() + LIMIT
    verdict = "running"
    while time.time() < deadline:
        r, st = os.waitpid(gpid, os.WNOHANG | os.WUNTRACED)
        if r:
            if os.WIFSTOPPED(st):
                verdict = "stopped %s" % signal.Signals(os.WSTOPSIG(st)).name
            elif os.WIFSIGNALED(st):
                verdict = "signaled %s" % signal.Signals(os.WTERMSIG(st)).name
            else:
                verdict = "exit %d" % os.WEXITSTATUS(st)
            break
        time.sleep(0.05)
    if verdict.startswith(("stopped", "running")):
        os.kill(gpid, signal.SIGKILL)
        os.waitpid(gpid, 0)
    os.write(statusw, (verdict + "\n").encode())
    os._exit(0)


def read_line(fd, timeout):
    buf = b""
    end = time.time() + timeout
    while b"\n" not in buf and time.time() < end:
        if select.select([fd], [], [], 0.2)[0]:
            chunk = os.read(fd, 4096)
            if not chunk:
                break
            buf += chunk
    return buf.decode().strip()


def drain(fd):
    buf = b""
    while select.select([fd], [], [], 0.3)[0]:
        chunk = os.read(fd, 4096)
        if not chunk:
            break
        buf += chunk
    return buf.decode(errors="replace")


def main():
    if len(sys.argv) != 3:
        print(__doc__)
        return 2
    binary, tape = (os.path.abspath(a) for a in sys.argv[1:])
    home = tempfile.mkdtemp(prefix="toktape-notty-")
    errr, errw = os.pipe()
    statusr, statusw = os.pipe()

    pid, master = pty.fork()
    if pid == 0:
        gpid = os.fork()
        if gpid == 0:
            grandchild(binary, tape, home, errw)
        watcher(gpid, statusw)
    os.close(errw)
    os.close(statusw)
    # One line, not read-to-EOF: a stray holder of a pipe end must not hang
    # the gate. The watcher answers within LIMIT, so LIMIT + 5 s is a bug.
    verdict = read_line(statusr, LIMIT + 5) or "running"
    stderr = drain(errr)
    try:
        os.waitpid(pid, os.WNOHANG)
    except ChildProcessError:
        pass
    os.close(master)

    if verdict.startswith("stopped") or verdict == "running":
        print("FAIL: first publish from a background process group: %s; "
              "it should refuse at once, not stop" % verdict)
        return 1
    if verdict == "exit 0" or WANT not in stderr:
        print("FAIL: %s, stderr %r; want non-zero and %r" % (verdict, stderr.strip()[-200:], WANT))
        return 1
    print("PASS: background-group first publish refused (%s): %s" % (verdict, WANT))
    return 0


if __name__ == "__main__":
    sys.exit(main())
