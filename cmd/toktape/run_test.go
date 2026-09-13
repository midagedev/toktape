package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// exec runs one invocation against buffers and returns the exit code with
// what each stream received. No verb here reaches the network: `record` is
// covered end to end by internal/recorder, and a test that discovered a
// server would attach to whatever llama-server this box happens to run.
func exec(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(context.Background(), &out, &errOut, args)
	return code, out.String(), errOut.String()
}

// writeTape saves a summary as a run file and returns its path.
func writeTape(t *testing.T, dir string, s *tape.RunSummary) string {
	t.Helper()
	path := filepath.Join(dir, s.ID+tape.Ext)
	tp := &tape.Tape{Schema: tape.SchemaVersion, Summary: *s}
	if err := tape.Write(path, tp); err != nil {
		t.Fatalf("tape.Write: %v", err)
	}
	return path
}

func TestHelpAndVersion(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}} {
		code, out, _ := exec(t, args...)
		if code != exitOK {
			t.Errorf("%v: exit %d, want 0", args, code)
		}
		if !strings.Contains(out, "Usage:") {
			t.Errorf("%v: no usage text on stdout", args)
		}
	}

	code, out, _ := exec(t, "version")
	if code != exitOK {
		t.Fatalf("version: exit %d", code)
	}
	if !strings.HasPrefix(out, "toktape ") {
		t.Errorf("version printed %q", out)
	}
	if card.Version != version {
		t.Errorf("card.Version = %q, want the binary's %q", card.Version, version)
	}
}

// TestHelpBeatsRecord: `--help` must never start a run against a live server.
func TestHelpBeatsRecord(t *testing.T) {
	if verb, _ := splitVerb([]string{"--url", "http://x", "--help"}); verb != "help" {
		t.Errorf("splitVerb = %q, want help", verb)
	}
	if verb, rest := splitVerb([]string{"--url", "http://x"}); verb != "record" || len(rest) != 2 {
		t.Errorf("splitVerb = %q / %v, want record with both flags", verb, rest)
	}
	if verb, rest := splitVerb(nil); verb != "record" || len(rest) != 0 {
		t.Errorf("bare toktape is %q / %v, want record", verb, rest)
	}
	if verb, rest := splitVerb([]string{"card", "a.tape", "--md"}); verb != "card" || len(rest) != 2 {
		t.Errorf("splitVerb = %q / %v, want card", verb, rest)
	}
}

func TestUnknownVerb(t *testing.T) {
	code, _, errOut := exec(t, "recrd")
	if code != exitUsage {
		t.Errorf("exit %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errOut, "unknown command") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestCardVerb(t *testing.T) {
	dir := t.TempDir()
	path := writeTape(t, dir, card.Example())

	code, out, _ := exec(t, "card", path)
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if out != card.Text(card.Example()) {
		t.Error("card verb did not render the tape's own summary")
	}
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := card.Width(line); w != card.CardWidth {
			t.Fatalf("line %d is %d columns, want %d", i+1, w, card.CardWidth)
		}
	}

	code, mdOut, _ := exec(t, "card", path, "--md")
	if code != exitOK {
		t.Fatalf("--md exit %d", code)
	}
	if !strings.Contains(mdOut, "```") || !strings.Contains(mdOut, "| model") {
		t.Error("--md did not produce a fence and a llama-bench table")
	}

	code, jsonOut, _ := exec(t, "card", path, "--json")
	if code != exitOK {
		t.Fatalf("--json exit %d", code)
	}
	var s tape.RunSummary
	if err := json.Unmarshal([]byte(jsonOut), &s); err != nil {
		t.Fatalf("--json is not valid JSON: %v", err)
	}
	if s.ID != card.Example().ID {
		t.Errorf("--json summary ID = %q", s.ID)
	}
}

func TestCardVerbUsageErrors(t *testing.T) {
	dir := t.TempDir()
	path := writeTape(t, dir, card.Example())

	if code, _, _ := exec(t, "card"); code != exitUsage {
		t.Errorf("no argument: exit %d, want %d", code, exitUsage)
	}
	if code, _, _ := exec(t, "card", filepath.Join(dir, "missing.tape")); code != exitUsage {
		t.Errorf("missing file: exit %d, want %d", code, exitUsage)
	}
	if code, _, _ := exec(t, "card", path, "--md", "--json"); code != exitUsage {
		t.Errorf("--md --json together: exit %d, want %d", code, exitUsage)
	}
}

func TestLsVerb(t *testing.T) {
	dir := t.TempDir()

	older := card.Example()
	older.ID = "20260912-101010-qwen3.5-35b-a3b"
	older.StartedAt = time.Date(2026, 9, 12, 10, 10, 10, 0, time.UTC)
	writeTape(t, dir, older)

	newer := card.ExampleConcurrent()
	newer.ID = "20260913-142530-qwen3.5-35b-a3b"
	newer.StartedAt = time.Date(2026, 9, 13, 14, 25, 30, 0, time.UTC)
	writeTape(t, dir, newer)

	// A file that is not a tape must not appear, and must not break the list.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, _ := exec(t, "ls", "--out", dir)
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("ls printed %d lines, want a header and two runs:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "ID") || !strings.Contains(lines[0], "QUANT") {
		t.Errorf("header = %q", lines[0])
	}
	if !strings.Contains(lines[1], newer.ID) {
		t.Errorf("newest run is not first: %q", lines[1])
	}
	if !strings.Contains(lines[2], older.ID) {
		t.Errorf("second row = %q", lines[2])
	}
	if !strings.Contains(out, "Q4_K_M") {
		t.Error("the exact quant sub-type is missing from the list")
	}
	if strings.Contains(out, "notes") {
		t.Error("a non-tape file was listed")
	}
}

func TestLsEmptyDir(t *testing.T) {
	code, out, errOut := exec(t, "ls", "--out", filepath.Join(t.TempDir(), "nothing-here"))
	if code != exitOK {
		t.Errorf("exit %d, want 0: an empty run directory is not an error", code)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if !strings.Contains(errOut, "No runs yet") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestCompareVerb(t *testing.T) {
	dir := t.TempDir()
	a := writeTape(t, dir, card.Example())

	modified := *card.Example()
	modified.ID = "20260913-151212-qwen3.5-35b-a3b"
	modified.Server.Flags.FlashAttn = "off"
	modified.Timings.PredictedPerSecond = 51.2
	b := writeTape(t, dir, &modified)

	code, out, _ := exec(t, "compare", a, b)
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "decode tok/s") {
		t.Error("no decode row in the compare output")
	}
	if !strings.Contains(out, "on → off") {
		t.Error("the -fa change is not in the compare output")
	}
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := card.Width(line); w > card.CardWidth {
			t.Errorf("line %d is %d columns, max %d", i+1, w, card.CardWidth)
		}
	}

	if code, _, _ := exec(t, "compare", a); code != exitUsage {
		t.Errorf("one argument: exit %d, want %d", code, exitUsage)
	}
}

// TestRecordUsageErrors covers the argument handling of the root verb without
// contacting a server.
func TestRecordUsageErrors(t *testing.T) {
	if code, _, _ := exec(t, "record", "stray-argument"); code != exitUsage {
		t.Errorf("stray argument: exit %d, want %d", code, exitUsage)
	}
	if code, _, _ := exec(t, "record", "--nope"); code != exitUsage {
		t.Errorf("unknown flag: exit %d, want %d", code, exitUsage)
	}
}

// TestHeaderLine checks the attach line the run prints before any token.
func TestHeaderLine(t *testing.T) {
	got := headerLine(card.Example())
	for _, want := range []string{
		"→ llama-server at http://127.0.0.1:8080 (b3650)",
		"R1 Distill Llama 70B Q4_K_M",
		"pid 48213",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("header %q is missing %q", got, want)
		}
	}

	blind := *card.Example()
	blind.Server.PID = 0
	if got := headerLine(&blind); !strings.Contains(got, "no /proc view") {
		t.Errorf("header without a pid = %q", got)
	}
}

// TestPromptRequestsCycling: the recorder cycles prompts, so the CLI only has
// to hand over what the user typed, in order.
func TestPromptRequests(t *testing.T) {
	if promptRequests(nil) != nil {
		t.Error("no --prompt must leave the recorder's default set in place")
	}
	got := promptRequests([]string{"alpha", "beta"})
	if len(got) != 2 || got[0].Messages[0].Content != "alpha" || got[1].Messages[0].Content != "beta" {
		t.Errorf("promptRequests = %+v", got)
	}
	if got[0].Messages[0].Role != "user" {
		t.Errorf("prompt role = %q, want user", got[0].Messages[0].Role)
	}
}

// TestProgressLine: the live rate is measured over the token window, never
// over wall time that keeps running after generation stopped (lesson 1).
func TestProgressLine(t *testing.T) {
	var buf bytes.Buffer
	p := newProgress(&buf, false)
	p.streams = 1
	p.tokens = 11
	p.majTotal = 0
	p.firstToken = time.Now()
	p.lastToken = p.firstToken.Add(time.Second)
	p.line()
	got := strings.TrimSpace(buf.String())
	if want := "stream 1/1 · 11 tok · 10.0 tok/s · 0 maj/tok"; got != want {
		t.Errorf("progress line = %q, want %q", got, want)
	}

	buf.Reset()
	p2 := newProgress(&buf, false)
	p2.streams = 4
	p2.perStream = map[int]int{0: 5, 1: 5, 2: 5}
	p2.tokens = 15
	p2.majTotal = 30
	p2.firstToken = time.Now()
	p2.lastToken = p2.firstToken.Add(time.Second)
	p2.line()
	if got := strings.TrimSpace(buf.String()); !strings.HasPrefix(got, "3/4 streams · 15 tok") || !strings.HasSuffix(got, "2.0 maj/tok") {
		t.Errorf("concurrent progress line = %q", got)
	}

	// Before any stream is announced there is nothing to report: a line
	// during discovery would name a stream count that is not decided yet.
	buf.Reset()
	q := newProgress(&buf, false)
	q.tokens = 5
	q.line()
	if buf.Len() != 0 {
		t.Errorf("a progress line was printed before any stream started: %q", buf.String())
	}

	// Once a stream is announced but no token has arrived, the line says so
	// rather than reporting a rate it has not measured.
	buf.Reset()
	q.streams = 1
	q.tokens = 0
	q.line()
	if got := strings.TrimSpace(buf.String()); got != "stream 1/1 · prefill…" {
		t.Errorf("prefill line = %q", got)
	}
}
