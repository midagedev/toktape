package main

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestRecordVerbSpecNMaxUsage: a --spec-n-max list the parser rejects is a
// usage error that names the element.
func TestRecordVerbSpecNMaxUsage(t *testing.T) {
	hermetic(t)
	for _, tc := range []struct{ list, want string }{
		{"3,x", `--spec-n-max 3,x: element 2 "x" is not a positive integer`},
		{"3,5,3", "n_max 3 is listed twice"},
		{"", "empty list"},
	} {
		code, _, stderr := exec(t, "--url", "http://127.0.0.1:1", "--spec-n-max", tc.list)
		if code != exitUsage || !strings.Contains(stderr, tc.want) {
			t.Errorf("--spec-n-max %q: exit %d, stderr %q, want a usage error containing %q", tc.list, code, stderr, tc.want)
		}
	}
}

// TestRecordVerbSpecNMax runs a sweep end to end through the CLI (TTP-35):
// with a prompts file every value runs every line, without one every value is
// one round of the default prompts under --n-predict, and the card prints the
// Draft sweep row. The hermetic /proc has no server, so no argv is read and
// every value runs.
func TestRecordVerbSpecNMax(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)

	readOne := func(t *testing.T, dir string) *tape.Tape {
		t.Helper()
		tapes, err := filepath.Glob(filepath.Join(dir, "*"+tape.Ext))
		if err != nil || len(tapes) != 1 {
			t.Fatalf("run files = %v (err %v), want one tape", tapes, err)
		}
		tp, err := tape.Read(tapes[0])
		if err != nil {
			t.Fatal(err)
		}
		return tp
	}

	t.Run("with a prompts file", func(t *testing.T) {
		dir := t.TempDir()
		path := writePrompts(t, `{"name":"sql-1","prompt":"select"}`+"\n"+`{"prompt":"tell me a story"}`+"\n")
		code, stdout, stderr := exec(t, "--url", srv.URL, "--out", dir, "--prompts", path, "--spec-n-max", "3,5")
		if code != exitOK {
			t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
		}
		tp := readOne(t, dir)
		s := tp.Summary
		if s.Rounds != 4 || len(tp.Requests) != 4 || !reflect.DeepEqual(s.SpecNMax, []int{3, 5}) || len(s.BySpecNMax) != 2 {
			t.Fatalf("Rounds %d, %d requests, SpecNMax %v, %d groups; want 4, 4, [3 5], 2",
				s.Rounds, len(tp.Requests), s.SpecNMax, len(s.BySpecNMax))
		}
		for k, want := range []string{"3", "3", "5", "5"} {
			if got := fmt.Sprint(tp.Requests[k].Prompt.Params["speculative.n_max"]); got != want {
				t.Errorf("request %d speculative.n_max = %s, want %s", k, got, want)
			}
		}
		if !strings.Contains(stdout, "│ Draft sweep   n_max 3  ") || !strings.Contains(stdout, "3·sql-1 ") {
			t.Errorf("the card has no Draft sweep row or no n_max-labelled round:\n%s", stdout)
		}
	})

	t.Run("without a prompts file", func(t *testing.T) {
		dir := t.TempDir()
		code, _, stderr := exec(t, "--url", srv.URL, "--out", dir, "--spec-n-max", "3,5")
		if code != exitOK {
			t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
		}
		tp := readOne(t, dir)
		if s := tp.Summary; s.Rounds != 2 || len(tp.Requests) != 2 || !reflect.DeepEqual(s.SpecNMax, []int{3, 5}) {
			t.Fatalf("Rounds %d, %d requests, SpecNMax %v; want 2, 2, [3 5]", s.Rounds, len(tp.Requests), s.SpecNMax)
		}
		for k, r := range tp.Requests {
			if r.Prompt.MaxTokens != defaultNPredict {
				t.Errorf("request %d MaxTokens = %d, want --n-predict's default %d", k, r.Prompt.MaxTokens, defaultNPredict)
			}
		}
	})
}
