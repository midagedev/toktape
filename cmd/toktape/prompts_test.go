package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
)

// TestParsePromptsJSONL pins the prompts file format of `record --prompts`
// (TTP-31): one JSON object per line, one round per object, and a line number
// in every error.
func TestParsePromptsJSONL(t *testing.T) {
	t.Run("prompt and messages lines, names, caps, blank lines, CRLF and a BOM", func(t *testing.T) {
		in := "\xef\xbb\xbf{\"name\":\"sql-1\",\"prompt\":\"select the top ten\"}\r\n" +
			"\n" +
			`{"messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hi"}],"max_tokens":512}` + "\n" +
			`   {"name":"한글","prompt":"설명해줘"}   ` // no trailing newline
		rounds, err := parsePromptsJSONL(strings.NewReader(in))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(rounds) != 3 {
			t.Fatalf("%d rounds, want 3", len(rounds))
		}
		r0 := rounds[0]
		if r0.Name != "sql-1" || len(r0.Prompts) != 1 || r0.Prompts[0].MaxTokens != 0 {
			t.Errorf("round 0 = %+v", r0)
		}
		if got := r0.Prompts[0].Messages; len(got) != 1 || got[0] != (tape.Message{Role: "user", Content: "select the top ten"}) {
			t.Errorf("a prompt line becomes one user message, got %+v", got)
		}
		r1 := rounds[1]
		if r1.Name != "" {
			t.Errorf("a line without a name has name %q, want \"\"", r1.Name)
		}
		if r1.Prompts[0].MaxTokens != 512 || len(r1.Prompts[0].Messages) != 2 || r1.Prompts[0].Messages[0].Role != "system" {
			t.Errorf("round 1 = %+v", r1.Prompts[0])
		}
		if rounds[2].Name != "한글" || rounds[2].Prompts[0].Messages[0].Content != "설명해줘" {
			t.Errorf("round 2 = %+v", rounds[2])
		}
	})

	errs := []struct {
		name, in, want string
	}{
		{"empty file", "", "line 0: end of file with no prompt line"},
		{"only blank lines", "\n  \n", "line 2: end of file with no prompt line"},
		{"not JSON", "{\"prompt\":\"a\"}\nnope\n", "line 2: not a prompt object"},
		{"an array", `["a"]`, "line 1: not a prompt object"},
		{"a typo'd key", `{"promt":"a"}`, `line 1: not a prompt object: json: unknown field "promt"`},
		{"both", `{"prompt":"a","messages":[{"role":"user","content":"b"}]}`, `line 1: has both "prompt" and "messages"`},
		{"neither", `{"name":"x"}`, `line 1: needs "prompt" or "messages"`},
		{"null", `null`, `line 1: needs "prompt" or "messages"`},
		{"empty prompt", `{"prompt":"  "}`, `line 1: "prompt" is empty`},
		{"empty messages", `{"messages":[]}`, `line 1: "messages" is empty`},
		{"a message without a role", `{"messages":[{"content":"x"}]}`, `line 1: message 1 has no "role"`},
		{"zero cap", "{\"prompt\":\"a\"}\n\n{\"prompt\":\"b\",\"max_tokens\":0}", `line 3: "max_tokens" is 0`},
		{"fractional cap", `{"prompt":"a","max_tokens":1.5}`, "line 1: not a prompt object"},
		{"two values on a line", `{"prompt":"a"} {"prompt":"b"}`, "line 1: more than one JSON value"},
	}
	for _, tc := range errs {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePromptsJSONL(strings.NewReader(tc.in))
			if err == nil {
				t.Fatalf("no error, want %q", tc.want)
			}
			if !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to start with %q", err, tc.want)
			}
		})
	}
}

func writePrompts(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prompts.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRecordVerbPromptsUsage: --prompts is an alternative to --prompt, and a
// file the parser rejects is a usage error that names the line.
func TestRecordVerbPromptsUsage(t *testing.T) {
	hermetic(t)
	good := writePrompts(t, `{"prompt":"a"}`+"\n")

	code, _, stderr := exec(t, "--url", "http://127.0.0.1:1", "--prompts", good, "--prompt", "b")
	if code != exitUsage || !strings.Contains(stderr, "--prompts and --prompt are alternatives") {
		t.Errorf("--prompts with --prompt: exit %d, stderr %q", code, stderr)
	}

	bad := writePrompts(t, `{"prompt":"a"}`+"\n"+`{"prompt":""}`+"\n")
	code, _, stderr = exec(t, "--url", "http://127.0.0.1:1", "--prompts", bad)
	if code != exitUsage || !strings.Contains(stderr, "line 2:") {
		t.Errorf("a bad line: exit %d, stderr %q, want a usage error naming line 2", code, stderr)
	}

	code, _, stderr = exec(t, "--url", "http://127.0.0.1:1", "--prompts", filepath.Join(t.TempDir(), "missing.jsonl"))
	if code != exitUsage || !strings.Contains(stderr, "opening the prompts file") {
		t.Errorf("a missing file: exit %d, stderr %q", code, stderr)
	}
}

// TestRecordVerbPrompts runs the multi-prompt path end to end: one tape with a
// round per line, and a card with the Prompts row.
func TestRecordVerbPrompts(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	dir := t.TempDir()
	path := writePrompts(t, `{"name":"sql-1","prompt":"select"}`+"\n"+`{"prompt":"tell me a story","max_tokens":32}`+"\n")

	code, stdout, stderr := exec(t, "--url", srv.URL, "--out", dir, "--prompts", path, "--sessions", "2")
	if code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
	}
	tapes, err := filepath.Glob(filepath.Join(dir, "*"+tape.Ext))
	if err != nil || len(tapes) != 1 {
		t.Fatalf("run files = %v (err %v), want one tape", tapes, err)
	}
	tp, err := tape.Read(tapes[0])
	if err != nil {
		t.Fatal(err)
	}
	s := tp.Summary
	if s.Rounds != 2 || s.Concurrency != 2 || len(tp.Requests) != 4 {
		t.Fatalf("Rounds %d, Concurrency %d, %d requests; want 2, 2, 4", s.Rounds, s.Concurrency, len(tp.Requests))
	}
	if tp.Requests[0].Prompt.Name != "sql-1" || tp.Requests[3].Prompt.Name != "" || tp.Requests[3].Round != 1 {
		t.Errorf("records carry names %q / %q and round %d", tp.Requests[0].Prompt.Name, tp.Requests[3].Prompt.Name, tp.Requests[3].Round)
	}
	// The line's own cap wins over the run's; a line without one gets the
	// run's. That cap was --n-predict's default of 256 until TTP-76
	// (2026-09-14) and is now the recorder's runaway guard, because the
	// default limit on "how long" is a wall clock and not a token count.
	if tp.Requests[0].Prompt.MaxTokens != recorder.DefaultMaxTokens || tp.Requests[2].Prompt.MaxTokens != 32 {
		t.Errorf("caps = %d / %d, want %d / 32", tp.Requests[0].Prompt.MaxTokens, tp.Requests[2].Prompt.MaxTokens, recorder.DefaultMaxTokens)
	}
	if !strings.Contains(stdout, "│ Prompts       2 rounds · ") {
		t.Errorf("the card has no Prompts row:\n%s", stdout)
	}
}
