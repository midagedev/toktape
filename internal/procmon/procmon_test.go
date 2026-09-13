package procmon_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/midagedev/toktape/internal/procmon"
	"github.com/midagedev/toktape/internal/tape"
)

// fsRoot is the fixture tree: testdata/proc mirrors a real /proc from a
// 2-socket EPYC box running llama-server on a 9-shard GGUF.
const fsRoot = "testdata"

// modelPath is the model pid 1234 in the fixture tree serves.
const modelPath = "/models/gguf/DeepSeek-V3-0324-UD-Q4_K_XL-00001-of-00009.gguf"

const (
	kB = 1024
	mB = 1024 * kB
	gB = 1024 * mB
)

func TestParseStat(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    procmon.Stat
		wantErr bool
	}{
		{
			// The comm carries both a space and a nested pair of parentheses,
			// so a Fields split of the whole line reads utime as majflt.
			name: "comm with spaces and parens",
			in:   "1234 (llama-server (cuda)) S 1 1234 1234 0 -1 4194560 987654 0 12345 0 456789 12345 0 0 20 0 97 0 8675309",
			want: procmon.Stat{PID: 1234, Comm: "llama-server (cuda)", State: "S", MinFaults: 987654, MajFaults: 12345, UTime: 456789, STime: 12345},
		},
		{
			name: "plain comm",
			in:   "42 (bash) S 1 42 42 34816 42 4194304 1234 0 5 0 10 20 0 0 20 0 1 0 900",
			want: procmon.Stat{PID: 42, Comm: "bash", State: "S", MinFaults: 1234, MajFaults: 5, UTime: 10, STime: 20},
		},
		{
			name: "comm that is only a paren",
			in:   "7 ()) R 1 7 7 0 -1 4194560 1 0 2 0 3 4 0 0 20 0 1 0 5",
			want: procmon.Stat{PID: 7, Comm: ")", State: "R", MinFaults: 1, MajFaults: 2, UTime: 3, STime: 4},
		},
		{
			name: "trailing newline",
			in:   "9 (server) S 1 9 9 0 -1 0 11 0 22 0 1 2 0 0 20 0 1 0 3\n",
			want: procmon.Stat{PID: 9, Comm: "server", State: "S", MinFaults: 11, MajFaults: 22, UTime: 1, STime: 2},
		},
		{
			// A real llama-server line after a few minutes of decoding, all 52
			// fields: utime (14) and stime (15) are far apart, so a parser that
			// swapped or shifted them fails here.
			name: "cpu time",
			in:   "2718 (llama-server) R 2701 2718 2701 34817 2718 4194560 3371528 0 60612 0 1829417 34211 0 0 20 0 97 0 1204555 412316860416 14680064 18446744073709551615 94207382573056 94207392108544 140729128386256 0 0 0 0 4096 16800975 0 0 0 17 12 0 0 3 0 0 94207392534288 94207393042592 94207421337600 140729128394639 140729128394839 140729128394839 140729128398287 0",
			want: procmon.Stat{PID: 2718, Comm: "llama-server", State: "R", MinFaults: 3371528, MajFaults: 60612, UTime: 1829417, STime: 34211},
		},
		{name: "no comm", in: "1234 llama-server S 1", wantErr: true},
		// Twelve fields reach majflt but not utime and stime.
		{name: "no cpu time", in: "1 (a) S 1 1 1 0 -1 0 1 0 2 0", wantErr: true},
		{name: "non-numeric stime", in: "1 (a) S 1 1 1 0 -1 0 1 0 2 0 1 x 0 0 20 0 1 0 3", wantErr: true},
		{name: "truncated", in: "1234 (llama-server) S 1 1234 1234 0", wantErr: true},
		{name: "empty", in: "", wantErr: true},
		{name: "non-numeric majflt", in: "1 (a) S 1 1 1 0 -1 0 1 0 x 0 1 2 0 0 20 0 1 0 3", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := procmon.ParseStat([]byte(tt.in))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseStat(%q) = %+v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseStat(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseStat(%q) =\n got %+v\nwant %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseStatusFixture(t *testing.T) {
	got, err := procmon.ParseStatus(readFixture(t, "proc/1234/status"))
	if err != nil {
		t.Fatal(err)
	}
	want := procmon.Status{
		Name:          "llama-server (c", // the kernel truncates comm to 15 characters
		VirtBytes:     402653184 * kB,
		RSSBytes:      58720256 * kB,
		RSSFileBytes:  54525952 * kB,
		RSSAnonBytes:  4194304 * kB,
		RSSShmemBytes: 0,
		SwapBytes:     1048576 * kB,
		Threads:       97,
	}
	if got != want {
		t.Errorf("ParseStatus =\n got %+v\nwant %+v", got, want)
	}
	// The kernel's own invariant; a fixture that breaks it would hide a bug.
	if sum := got.RSSFileBytes + got.RSSAnonBytes + got.RSSShmemBytes; sum != got.RSSBytes {
		t.Errorf("RssFile+RssAnon+RssShmem = %d, VmRSS = %d", sum, got.RSSBytes)
	}
}

func TestParseStatusPartial(t *testing.T) {
	// A kernel that does not publish the Rss* breakdown must leave those
	// fields at 0 rather than inventing one.
	got, err := procmon.ParseStatus([]byte("Name:\tsmall\nVmSize:\t 1024 kB\nVmRSS:\t  512 kB\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := procmon.Status{Name: "small", VirtBytes: 1024 * kB, RSSBytes: 512 * kB}
	if got != want {
		t.Errorf("ParseStatus =\n got %+v\nwant %+v", got, want)
	}
	if _, err := procmon.ParseStatus([]byte("VmRSS:\t not-a-number kB\n")); err == nil {
		t.Error("ParseStatus with a malformed value: want error, got nil")
	}
}

func TestParseSmapsRollup(t *testing.T) {
	got, err := procmon.ParseSmapsRollup(readFixture(t, "proc/1234/smaps_rollup"))
	if err != nil {
		t.Fatal(err)
	}
	want := procmon.Rollup{
		RSSBytes:          58720256 * kB,
		PSSBytes:          57671680 * kB,
		SharedCleanBytes:  1048576 * kB,
		PrivateDirtyBytes: 4194304 * kB,
		SwapBytes:         1048576 * kB,
	}
	if got != want {
		t.Errorf("ParseSmapsRollup =\n got %+v\nwant %+v", got, want)
	}
	// Pss_Dirty and Pss_Anon must not be mistaken for Pss: a prefix match
	// would silently take the last such line.
	if got.PSSBytes == 4194304*kB {
		t.Error("Pss picked up Pss_Dirty")
	}
}

func TestParseCmdline(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []string
	}{
		{"trailing NUL dropped", []byte("a\x00b\x00"), []string{"a", "b"}},
		{"no trailing NUL", []byte("a\x00b"), []string{"a", "b"}},
		{"single arg", []byte("/bin/true\x00"), []string{"/bin/true"}},
		{"empty arg kept in the middle", []byte("a\x00\x00b\x00"), []string{"a", "", "b"}},
		{"kernel thread", []byte(""), nil},
		{"only NULs", []byte("\x00\x00"), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := procmon.ParseCmdline(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseCmdline(%q) = %q, want %q", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("ParseCmdline(%q) = %q, want %q", tt.in, got, tt.want)
				}
			}
		})
	}
}

func TestArgsFixture(t *testing.T) {
	argv, err := procmon.Args(fsRoot, 1234)
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) != 27 {
		t.Fatalf("argv has %d entries: %q", len(argv), argv)
	}
	if argv[0] != "/usr/local/bin/llama-server" || argv[1] != "--model" || argv[2] != modelPath {
		t.Errorf("argv[0:3] = %q", argv[:3])
	}
	// The -ot pattern must survive verbatim: the card prints it as sent.
	if got, want := argv[len(argv)-3], `blk\.[0-9]+\.ffn_.*_exps\.=CPU`; got != want {
		t.Errorf("-ot value = %q, want %q", got, want)
	}
	if _, err := procmon.Args(fsRoot, 31337); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("Args for a dead pid = %v, want ErrNotFound", err)
	}
}

func TestIsServerCommand(t *testing.T) {
	yes := []string{
		"llama-server", "/usr/local/bin/llama-server", "llama-server-cuda",
		"llama-server (cuda)", "llama-server-cu", // comm truncated to 15 chars
		"server", "/opt/ik_llama.cpp/build/bin/llama-server", "ik_llama-server",
		"LLAMA-SERVER",
	}
	for _, s := range yes {
		if !procmon.IsServerCommand(s) {
			t.Errorf("IsServerCommand(%q) = false, want true", s)
		}
	}
	no := []string{"bash", "sh", "python3", "sha256sum", "llama-bench", "llamafile", "", "ollama"}
	for _, s := range no {
		if procmon.IsServerCommand(s) {
			t.Errorf("IsServerCommand(%q) = true, want false", s)
		}
	}
}

func TestFindPID(t *testing.T) {
	// 777 maps the same file name from another directory and sorts first;
	// 999 is a shell whose argv contains the model path exactly; 4242 is a
	// real server on another model. Only 1234 is both a server and an exact
	// path match.
	got, err := procmon.FindPID(fsRoot, modelPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1234 {
		t.Errorf("FindPID = %d, want 1234 (exact path match must beat the basename match at pid 777)", got)
	}
}

func TestFindPIDBasenameFallback(t *testing.T) {
	// A server started through a symlinked model directory still matches on
	// the file name, and the lowest such PID wins so the answer is stable.
	other := "/mnt/elsewhere/DeepSeek-V3-0324-UD-Q4_K_XL-00001-of-00009.gguf"
	got, err := procmon.FindPID(fsRoot, other)
	if err != nil {
		t.Fatal(err)
	}
	if got != 777 {
		t.Errorf("FindPID(%q) = %d, want 777", other, got)
	}
}

func TestFindPIDRejectsDecoy(t *testing.T) {
	// A tree holding only the decoy: the model path is named exactly, so
	// nothing but the command check can reject it.
	root := t.TempDir()
	copyTree(t, filepath.Join(fsRoot, "proc", "999"), filepath.Join(root, "proc", "999"))
	_, err := procmon.FindPID(root, modelPath)
	if !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("FindPID with only the decoy = %v, want ErrNotFound", err)
	}
}

func TestFindPIDNotFound(t *testing.T) {
	_, err := procmon.FindPID(fsRoot, "/models/gguf/NotHere-Q4_K_M.gguf")
	if !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("FindPID for an unserved model = %v, want ErrNotFound", err)
	}
	if _, err := procmon.FindPID(fsRoot, ""); err == nil {
		t.Error("FindPID with an empty model path: want error, got nil")
	}
	if _, err := procmon.FindPID(t.TempDir(), modelPath); err == nil {
		t.Error("FindPID with no proc directory: want error, got nil")
	}
}

func TestReadMem(t *testing.T) {
	got, err := procmon.ReadMem(fsRoot, 1234)
	if err != nil {
		t.Fatal(err)
	}
	want := tape.MemSample{
		VirtBytes:     402653184 * kB,
		RSSBytes:      58720256 * kB,
		RSSFileBytes:  54525952 * kB,
		RSSAnonBytes:  4194304 * kB,
		RSSShmemBytes: 0,
		SwapBytes:     1048576 * kB,
		MajFaults:     12345,
		MinFaults:     987654,
		// utime 456789 + stime 12345 ticks over USER_HZ.
		CPUSeconds: 4691.34,
	}
	if got != want {
		t.Errorf("ReadMem =\n got %+v\nwant %+v", got, want)
	}
	if _, err := procmon.ReadMem(fsRoot, 31337); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("ReadMem for a dead pid = %v, want ErrNotFound", err)
	}
}

// readFixture reads one file from the fixture tree.
func readFixture(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fsRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return data
}

// copyTree copies the regular files of a fixture directory so a test can
// assemble a /proc holding only the processes it wants to reason about.
// Symlinks are skipped, which is also how a process whose cwd link the caller
// may not read behaves.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyTree(t, s, d)
			continue
		}
		if !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(d, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// mkdirAll creates rel under root and returns nothing; the test uses the path
// it already knows.
func mkdirAll(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestFindPIDByMapping(t *testing.T) {
	// A server started with -hf: /props reports the resolved cache path, which
	// is nowhere in argv, so only the mapping list can connect the two. This is
	// the zero-config first run, so it must not fall back to "no /proc view".
	const cached = "/home/rig/.cache/llama.cpp/unsloth_DeepSeek-V3-0324-GGUF_UD-Q4_K_XL-00003-of-00009.gguf"
	got, err := procmon.FindPID(fsRoot, cached)
	if err != nil {
		t.Fatal(err)
	}
	if got != 5150 {
		t.Errorf("FindPID(%q) = %d, want 5150", cached, got)
	}
}

func TestFindPIDByMappingIgnoresNonServers(t *testing.T) {
	// The mapping fallback must keep the command check: a shell that happens to
	// have the model file open is still not the server.
	root := t.TempDir()
	dir := filepath.Join(root, "proc", "999")
	copyTree(t, filepath.Join(fsRoot, "proc", "999"), dir)
	if err := os.WriteFile(filepath.Join(dir, "maps"), readFixture(t, "proc/1234/maps"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := procmon.FindPID(root, modelPath); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("FindPID = %v, want ErrNotFound", err)
	}
}

func TestRelativeModelPath(t *testing.T) {
	// /props reports model_path as it was passed, so a server started with
	// "-m gguf/...gguf" from /models reports a relative path while maps holds
	// the absolute one. pid 1234's cwd link in the fixture points at /models.
	const rel = "gguf/DeepSeek-V3-0324-UD-Q4_K_XL-00001-of-00009.gguf"

	pid, err := procmon.FindPID(fsRoot, rel)
	if err != nil {
		t.Fatal(err)
	}
	if pid != 1234 {
		t.Errorf("FindPID(%q) = %d, want 1234", rel, pid)
	}

	got, err := procmon.MappedFileBytes(fsRoot, 1234, rel)
	if err != nil {
		t.Fatal(err)
	}
	const want = 8*42*gB + 30*gB
	if got != want {
		t.Errorf("MappedFileBytes(%q) = %d, want %d", rel, got, want)
	}
}

func TestRelativeModelPathWithoutCwdLink(t *testing.T) {
	// Another user's process: the cwd link is not readable. The relative path
	// is used as-is, so the file-name match still finds the server and the
	// mapped size is 0 rather than a wrong number.
	root := t.TempDir()
	copyTree(t, filepath.Join(fsRoot, "proc", "1234"), filepath.Join(root, "proc", "1234"))
	const rel = "gguf/DeepSeek-V3-0324-UD-Q4_K_XL-00001-of-00009.gguf"
	pid, err := procmon.FindPID(root, rel)
	if err != nil {
		t.Fatal(err)
	}
	if pid != 1234 {
		t.Errorf("FindPID(%q) = %d, want 1234", rel, pid)
	}
	got, err := procmon.MappedFileBytes(root, 1234, rel)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("MappedFileBytes without a cwd link = %d, want 0", got)
	}
}
