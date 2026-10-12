package recorder

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// writeTinyGGUF writes a valid GGUF v3 with two string/uint32 keys and no
// tensors: enough for the header reader to answer, small enough to be a
// fixture. general.file_type 15 is Q4_K_M.
func writeTinyGGUF(t *testing.T, path string) int64 {
	t.Helper()
	var b bytes.Buffer
	put := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	str := func(s string) { put(uint64(len(s))); b.WriteString(s) }
	b.WriteString("GGUF")
	put(uint32(3))
	put(uint64(0)) // tensors
	put(uint64(2)) // kvs
	str("general.architecture")
	put(uint32(8))
	str("llama")
	str("general.file_type")
	put(uint32(4))
	put(uint32(15))
	for b.Len()%32 != 0 {
		b.WriteByte(0)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return int64(b.Len())
}

// bloomeryProc is a fake procfs root where pid 4242 runs argv with its
// working directory at cwd ("" leaves the cwd link out).
func bloomeryProc(t *testing.T, cwd string, argv []string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "proc", "4242")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := strings.Join(argv, "\x00") + "\x00"
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmd), 0o644); err != nil {
		t.Fatal(err)
	}
	if cwd != "" {
		if err := os.Symlink(cwd, filepath.Join(dir, "cwd")); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// bloomeryRun is a run attached to a bloomery that declared its pid and its
// argv, and named the file alone: the DGX Spark case (v0.7.4).
func bloomeryRun(root string, argv []string, declaredPID int) *run {
	return &run{
		opts: Options{FSRoot: root},
		kind: tape.ServerKind("bloomery"),
		props: &server.Props{Engine: &server.EngineProps{
			Name: "bloomery", Args: argv, ServerPID: declaredPID,
		}},
		model: tape.ModelInfo{FileName: "Tiny-Q4_K_M.gguf"},
		// A server on another host: the port names no process here.
		client: server.New("http://spark.example:8080"),
	}
}

func TestBloomeryRelativeModelResolvesAgainstProcessCwd(t *testing.T) {
	argv := []string{"bin/serve", "-m", "models/x/Tiny-Q4_K_M.gguf"}
	cwd := t.TempDir()
	want := filepath.Join(cwd, "models", "x", "Tiny-Q4_K_M.gguf")
	size := writeTinyGGUF(t, want)

	r := bloomeryRun(bloomeryProc(t, cwd, argv), argv, 4242)
	r.collectProcess()

	if r.model.Path != want {
		t.Errorf("model.path = %q, want %q", r.model.Path, want)
	}
	if r.model.FileBytes != size {
		t.Errorf("model.file_bytes = %d, want %d", r.model.FileBytes, size)
	}
	if r.model.Quant != "Q4_K_M" || r.model.Arch != "llama" {
		t.Errorf("header not read: quant %q arch %q", r.model.Quant, r.model.Arch)
	}
	if len(r.warnings) != 0 {
		t.Errorf("warnings = %q, want none", r.warnings)
	}
}

func TestBloomeryRelativeModelNamesWhatFailed(t *testing.T) {
	argv := []string{"bin/serve", "-m", "models/x/Tiny-Q4_K_M.gguf"}
	cases := []struct {
		name string
		root func(t *testing.T) string
		pid  int
		want string
	}{
		{"file missing", func(t *testing.T) string { return bloomeryProc(t, t.TempDir(), argv) }, 4242, "Tiny-Q4_K_M.gguf"},
		{"cwd unreadable", func(t *testing.T) string { return bloomeryProc(t, "", argv) }, 4242, "working directory"},
		{"no local process", func(t *testing.T) string { return t.TempDir() }, 0, "process"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := bloomeryRun(tc.root(t), argv, tc.pid)
			r.collectProcess()
			if r.model.Path != "" || r.model.FileBytes != 0 || r.model.Quant != "" {
				t.Errorf("model = %+v, want path, bytes and quant left empty", r.model)
			}
			var hit bool
			for _, w := range r.warnings {
				if strings.Contains(w, "Tiny-Q4_K_M.gguf") && strings.Contains(w, tc.want) {
					hit = true
				}
			}
			if !hit {
				t.Errorf("warnings = %q, want one naming the file and %q", r.warnings, tc.want)
			}
		})
	}
}

// An engine that reported its own bytes, or a non-GGUF format, is the record:
// the argv is not consulted (ExLlamaV3 keeps its model directory in -m).
func TestArgvModelLeavesEngineReportsAlone(t *testing.T) {
	argv := []string{"serve", "--model", "models/x/Tiny-Q4_K_M.gguf"}
	cwd := t.TempDir()
	writeTinyGGUF(t, filepath.Join(cwd, "models", "x", "Tiny-Q4_K_M.gguf"))
	for name, m := range map[string]tape.ModelInfo{
		"engine bytes": {FileName: "Tiny-Q4_K_M.gguf", FileBytes: 777, Quant: "EXL3 4.05 bpw"},
		"exl3 format":  {FileName: "Tiny", Format: "exl3"},
	} {
		t.Run(name, func(t *testing.T) {
			r := bloomeryRun(bloomeryProc(t, cwd, argv), argv, 4242)
			r.model = m
			r.collectProcess()
			if r.model != m {
				t.Errorf("model = %+v, want %+v untouched", r.model, m)
			}
		})
	}
}

func TestModelFromArgvSpellings(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"x", "-m", "a.gguf"}, "a.gguf"},
		{[]string{"x", "--model", "a.gguf"}, "a.gguf"},
		{[]string{"x", "--model=a.gguf"}, "a.gguf"},
		{[]string{"x", "-m=a.gguf"}, "a.gguf"},
		{[]string{"x", "-ngl", "99"}, ""},
		{[]string{"x", "-m"}, ""},
	}
	for _, tc := range cases {
		if got := modelFromArgv(tc.argv); got != tc.want {
			t.Errorf("modelFromArgv(%q) = %q, want %q", tc.argv, got, tc.want)
		}
	}
}

// The 3060 host's tape (2026-10-12, toktape 0.6 against bloomery 0.3.0): the
// engine block named the absolute path in model_path and sent no quant and no
// size, so the card printed "?" for both on a file on the same disk. Here
// the whole engine path runs, collectModelShape then collectProcess, as
// record does.
func TestBloomeryAbsoluteModelPathFillsQuantAndSize(t *testing.T) {
	want := filepath.Join(t.TempDir(), "models", "Tiny-Q4_K_M.gguf")
	size := writeTinyGGUF(t, want)
	argv := []string{"bin/bloomery-serve", "-m", want, "--host", "127.0.0.1"}

	r := bloomeryRun(bloomeryProc(t, "", argv), argv, 4242)
	r.props.ModelPath = want
	r.collectModelShape()
	r.collectProcess()

	if r.model.Path != want || r.model.FileName != "Tiny-Q4_K_M.gguf" {
		t.Errorf("model path %q, file name %q", r.model.Path, r.model.FileName)
	}
	if r.model.FileBytes != size {
		t.Errorf("model.file_bytes = %d, want %d", r.model.FileBytes, size)
	}
	if r.model.Quant != "Q4_K_M" {
		t.Errorf("model.quant = %q, want Q4_K_M", r.model.Quant)
	}
	if len(r.warnings) != 0 {
		t.Errorf("warnings = %q, want none", r.warnings)
	}
}
