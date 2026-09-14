package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// -n is tokens and streams are --sessions (2026-09-14). These tests pin the
// three things the rename is for: one name per concept, a ceiling a typo
// cannot cross, and refusals whose hint contains the flag the reader needs.

// countingServer answers nothing useful and counts every request, so a test
// can say a refusal happened before toktape contacted anything.
func countingServer(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "no", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &hits
}

// errorHint is the hint of the one JSON error object a failed --json run
// prints.
func errorHint(t *testing.T, stdout string) string {
	t.Helper()
	var got struct {
		Error struct {
			Code string `json:"code"`
			Hint string `json:"hint"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("--json failure is not one JSON object: %v\n%s", err, stdout)
	}
	if got.Error.Code != "usage" {
		t.Errorf("error.code = %q, want usage", got.Error.Code)
	}
	return got.Error.Hint
}

// TestSessionCeilingRefuses: a stream count over the ceiling, or a ceiling
// below the count, is exit 1 before any server is contacted, and the hint
// carries the exact flags that get past it.
func TestSessionCeilingRefuses(t *testing.T) {
	hermetic(t)
	nine := make([]string, 0, 18)
	for range 9 {
		nine = append(nine, "--prompt", "Explain mmap.")
	}
	cases := []struct {
		name string
		args []string
		// want are substrings of stderr; the hint is on it too.
		want []string
	}{
		{
			name: "over the ceiling",
			args: []string{"--sessions", "9"},
			want: []string{"--sessions 9 is over the ceiling of 8", "→ ", "--sessions 9 --max-sessions 9"},
		},
		{
			// The reader who typed 128 learned it from llama-bench, where
			// it is a token count; the hint says so in as many words.
			name: "a llama-bench token count typed as sessions",
			args: []string{"--sessions", "128"},
			want: []string{"--sessions 128 --max-sessions 128", "llama-bench", "-n 128"},
		},
		{
			name: "a ceiling below the count",
			args: []string{"--sessions", "4", "--max-sessions", "2"},
			want: []string{"--max-sessions 2 is below the 4 streams", "ceiling, not a second count", "--sessions 4 --max-sessions 4"},
		},
		{
			name: "a ceiling below a count that is over the default",
			args: []string{"--sessions", "16", "--max-sessions", "12"},
			want: []string{"--max-sessions 12 is below the 16 streams", "--sessions 16 --max-sessions 16"},
		},
		{
			// Nine --prompt flags are nine streams with no --sessions at
			// all; a ceiling the prompt list walks around is not one.
			name: "the prompts alone are over the ceiling",
			args: nine,
			want: []string{"9 streams at once, over the ceiling of 8", "--max-sessions 9"},
		},
		{
			name: "no streams at all",
			args: []string{"--sessions", "0"},
			want: []string{"--sessions 0", "→ "},
		},
		{
			name: "a ceiling of nothing",
			args: []string{"--max-sessions", "0"},
			want: []string{"--max-sessions 0", "→ "},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, hits := countingServer(t)
			args := append([]string{"--url", url, "--wait", "0", "--out", t.TempDir(), "--json"}, tc.args...)
			code, stdout, stderr := exec(t, args...)
			if code != exitUsage {
				t.Fatalf("exit %d, want %d\nstderr:\n%s", code, exitUsage, stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr does not say %q:\n%s", want, stderr)
				}
			}
			if errorHint(t, stdout) == "" {
				t.Error("the JSON error carries no hint")
			}
			if n := hits.Load(); n != 0 {
				t.Errorf("the refusal contacted the server %d times; it must come first", n)
			}
		})
	}
}

// TestSessionCeilingLetsThrough: at the ceiling, and past it when the number
// is said twice, the run goes on to the server. A dead URL makes "went on" an
// exit 2 rather than a recording.
func TestSessionCeilingLetsThrough(t *testing.T) {
	for _, args := range [][]string{
		{"--sessions", "8"},
		{"--sessions", "16", "--max-sessions", "16"},
		{"--sessions", "4", "--max-sessions", "32"},
		{"--max-sessions", "1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			deadURL, got := captureOptions(t)
			full := append([]string{"--url", deadURL, "--wait", "0", "--out", t.TempDir()}, args...)
			if code, _, stderr := exec(t, full...); code != exitUnreachable {
				t.Fatalf("exit %d, want %d: the ceiling refused a count it should allow\n%s",
					code, exitUnreachable, stderr)
			}
			if flagIn(args, "--sessions") && got.Sessions() != atoiArg(t, args, "--sessions") {
				t.Errorf("the recorder was told %d sessions", got.Sessions())
			}
		})
	}
}

// TestSlotRefusalReachesTheDoor: more sessions than the server's slots is the
// recorder's refusal, and it leaves through cli.fail as a usage error whose
// hint names both ways out. cliServer offers four slots.
func TestSlotRefusalReachesTheDoor(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	code, stdout, stderr := exec(t, "--url", srv.URL, "--out", t.TempDir(), "--sessions", "5", "-n", "8", "--json")
	if code != exitUsage {
		t.Fatalf("exit %d, want %d\n%s", code, exitUsage, stderr)
	}
	for _, want := range []string{"5 sessions", "offers 4 slots", "queue", "--sessions 4", "-np 5"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not say %q:\n%s", want, stderr)
		}
	}
	if hint := errorHint(t, stdout); !strings.Contains(hint, "--sessions 4") {
		t.Errorf("the JSON hint does not name the flag: %q", hint)
	}
	t.Logf("stderr:\n%s", stderr)
}

// TestRetiredFlagsNameTheirReplacement: --concurrency is gone, not an alias,
// and typing it — or llama-server's slot flag — is answered with the flag
// toktape uses instead.
func TestRetiredFlagsNameTheirReplacement(t *testing.T) {
	for _, tc := range []struct {
		flag string
		want []string
	}{
		{"--concurrency", []string{"--sessions N", "-n is tokens", "llama-bench"}},
		{"-concurrency", []string{"--sessions N"}},
		{"--parallel", []string{"llama-server's slot count", "--sessions N"}},
		{"-np", []string{"llama-server's slot count", "--sessions N"}},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			code, stdout, stderr := exec(t, tc.flag, "4", "--json")
			if code != exitUsage {
				t.Fatalf("exit %d, want %d: %s must not parse\n%s", code, exitUsage, tc.flag, stderr)
			}
			hint := errorHint(t, stdout)
			for _, want := range tc.want {
				if !strings.Contains(hint, want) {
					t.Errorf("hint %q does not say %q", hint, want)
				}
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr does not say %q:\n%s", want, stderr)
				}
			}
		})
	}
	// A flag this verb never had is not given somebody else's advice.
	if _, stdout, _ := exec(t, "--nope", "--json"); errorHint(t, stdout) != "" {
		t.Errorf("an unknown flag got a retired-flag hint: %s", stdout)
	}
}

// TestNoSurfaceSpellsTheRetiredFlag: every place a reader copies a command
// from — --help, `help agents`, `help render`, the three READMEs' code blocks
// and docs/agents.md — names the stream count --sessions and never
// --concurrency. A rename is 90 % done when this is the part that is missing.
//
// The Korean and Japanese READMEs are checked through their code blocks only:
// their prose is written by the lead, and TestREADMECodeBlocksMatchAcrossLanguages
// already holds the blocks to README.md's bytes.
func TestNoSurfaceSpellsTheRetiredFlag(t *testing.T) {
	surfaces := map[string]string{
		"--help":         usageText,
		"help agents":    agentsTopic(),
		"help render":    renderUsage,
		"docs/agents.md": repoFile(t, "docs/agents.md"),
		"README.md":      repoFile(t, "README.md"),
	}
	for _, name := range readmes {
		var blocks []string
		for _, m := range fence.FindAllStringSubmatch(repoFile(t, name), -1) {
			blocks = append(blocks, m[1])
		}
		surfaces[name+" code blocks"] = strings.Join(blocks, "\n")
	}
	for name, text := range surfaces {
		// Saying the flag is gone is allowed, and is worth saying to a reader
		// who remembers it; teaching it anywhere else is not.
		// Fields first: a Markdown paragraph wraps the sentence anywhere.
		text = strings.Join(strings.Fields(text), " ")
		text = strings.NewReplacer(
			"--concurrency no longer exists", "",
			"`--concurrency` no longer exists", "",
		).Replace(text)
		if strings.Contains(text, "--concurrency") {
			t.Errorf("%s still spells --concurrency", name)
		}
	}
	for name, want := range map[string][]string{
		"--help":         {"--sessions N", "--max-sessions N", "-n, --n-predict N", "llama-bench"},
		"help agents":    {"--sessions", "--max-sessions", "llama-bench"},
		"docs/agents.md": {"--sessions", "llama-bench"},
		"README.md":      {"--sessions", "--max-sessions"},
	} {
		for _, w := range want {
			if !strings.Contains(surfaces[name], w) {
				t.Errorf("%s does not mention %q", name, w)
			}
		}
	}
}

func flagIn(args []string, name string) bool { return indexOf(args, name) >= 0 }

func atoiArg(t *testing.T, args []string, name string) int {
	t.Helper()
	n, err := strconv.Atoi(args[indexOf(args, name)+1])
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return n
}
