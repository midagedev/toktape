package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// `--for`, the flag that decides how long a run is (TTP-76, 2026-09-14).
//
// The recorder owns the table of what may end a generation and asserts it in
// internal/recorder/limit_test.go. What is checked here is the half the CLI
// owns: that the three states of the flag reach recorder.Options intact, that a
// budget nobody can honour is rejected rather than silently normalised, and
// that --help teaches the table it actually implements.

// captureOptions pins the collectors like hermetic does and also hands back the
// options the verb assembled, so a flag combination can be asserted without a
// server to record against.
//
// It points the run at a URL nothing listens on with a zero wait, so every case
// ends in exit 2 after the options have been built and before a single request
// is sent — the record verb must never generate load from the test suite.
func captureOptions(t *testing.T) (url string, got *recorder.Options) {
	t.Helper()
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close() // nothing listens there now

	root := t.TempDir()
	got = &recorder.Options{}
	prev := pinCollectors
	pinCollectors = func(o recorder.Options) recorder.Options {
		*got = o
		o.FSRoot = root
		o.GPU = gpu.Null{}
		return o
	}
	t.Cleanup(func() { pinCollectors = prev })
	return deadURL, got
}

// TestForFlagReachesTheRecorder: the three states of --for and of --n-predict,
// as the recorder receives them.
//
// A duration has two states and the flag has three, so the sentinel carries the
// third. Getting that wrong is silent in both directions — an unset flag that
// arrived as "no clock" turns the default off for everybody, and `--for 0` that
// arrived as "unset" gives a clock to the one person who asked for none — which
// is why each row is spelled out rather than inferred.
func TestForFlagReachesTheRecorder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		wantFor time.Duration
		wantMax int
	}{
		{"neither", nil, 0, 0},
		{"--for only", []string{"--for", "45s"}, 45 * time.Second, 0},
		{"--n-predict only", []string{"--n-predict", "240"}, 0, 240},
		// -n is the same flag, as llama-bench's -n is tokens (2026-09-14).
		{"-n only", []string{"-n", "240"}, 0, 240},
		{"--for and -n", []string{"--for", "8s", "-n", "240"}, 8 * time.Second, 240},
		{"both", []string{"--for", "8s", "--n-predict", "240"}, 8 * time.Second, 240},
		{"--for 0", []string{"--for", "0"}, recorder.NoClock, 0},
		{"--for 0s", []string{"--for", "0s"}, recorder.NoClock, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deadURL, got := captureOptions(t)
			args := append([]string{"--url", deadURL, "--wait", "0", "--out", t.TempDir()}, tc.args...)
			if code, _, stderr := exec(t, args...); code != exitUnreachable {
				t.Fatalf("exit %d, want %d (the options are built and then nothing answers)\n%s",
					code, exitUnreachable, stderr)
			}
			if got.For != tc.wantFor {
				t.Errorf("Options.For = %v, want %v", got.For, tc.wantFor)
			}
			if got.MaxTokens != tc.wantMax {
				t.Errorf("Options.MaxTokens = %d, want %d", got.MaxTokens, tc.wantMax)
			}
		})
	}
}

// TestNegativeForIsAUsageError: a budget in the past is a typo, not a run.
//
// It is caught in the CLI rather than normalised away, because every other
// reading of a negative duration is a guess about what the user meant: NoClock
// is already spelled `--for 0`, and treating -1s as "no clock" would silently
// record something they did not ask for.
func TestNegativeForIsAUsageError(t *testing.T) {
	hermetic(t)
	code, _, stderr := exec(t, "--for", "-1s", "--out", t.TempDir())
	if code != exitUsage {
		t.Fatalf("exit %d, want %d\n%s", code, exitUsage, stderr)
	}
	if !strings.Contains(stderr, "--for") {
		t.Errorf("the rejection does not name the flag it is about:\n%s", stderr)
	}
	if !strings.Contains(stderr, "-1s") {
		t.Errorf("the rejection does not quote what was given:\n%s", stderr)
	}
	if !strings.Contains(stderr, "→ ") {
		t.Errorf("the rejection has no hint saying what to do instead:\n%s", stderr)
	}
}

// TestHelpTeachesTheLimitMatrix: --help is where an agent reads the table, so
// it names both flags, says the default, and says what each of the five rows
// does. The words are checked, not the prose: a row dropped from the text is a
// row the reader has to discover by running it.
func TestHelpTeachesTheLimitMatrix(t *testing.T) {
	block, ok := recordFlagsBlock(usageText)
	if !ok {
		t.Fatal("--help has no \"Record flags:\" section")
	}
	forAt, nAt := strings.Index(block, "--for "), strings.Index(block, "--n-predict ")
	switch {
	case forAt < 0:
		t.Fatal("--help does not list --for among the record flags")
	case nAt < 0:
		t.Fatal("--help does not list --n-predict among the record flags")
	case forAt > nAt:
		// The user's words, 2026-09-14: the run's length "이게 토큰제한보다 더
		// 우선순위가 있게 노출되어야 한다" — it is exposed above the token limit.
		t.Error("--n-predict is listed above --for; the wall clock is the flag a reader meets first")
	}
	if first, _, _ := strings.Cut(block, "\n"); !strings.Contains(first, "--for ") {
		t.Errorf("--for is not the first record flag; it is the answer to the question every first run has, and the first line is %q", first)
	}
	for _, want := range []struct{ what, text string }{
		{"the default budget", recorder.DefaultFor.String()},
		{"the no-clock row", "--for 0"},
		{"the floor", fmt.Sprint(tape.MinCutTokens)},
		{"the row where a named cap silences the clock", "turns the clock off"},
		{"the row where both are named", "whichever comes first"},
	} {
		if !strings.Contains(block, want.text) {
			t.Errorf("the record flags do not state %s (%q):\n%s", want.what, want.text, block)
		}
	}
}

// TestHelpSeparatesForFromRenderDuration: the two flags a reader will conflate.
//
// One aims the run when you record it; the other replays the run you have at
// the wrong speed, which render.NewSchedule's own doc calls a lie about the
// machine. An agent reading both verbs needs the difference in writing.
func TestHelpSeparatesForFromRenderDuration(t *testing.T) {
	if !strings.Contains(usageText, "--duration") {
		t.Error("--help never mentions --duration, so nothing warns a reader off it")
	}
	if !strings.Contains(renderUsage, "--for") {
		t.Error("the clip-length note does not name --for, the flag that actually aims a run")
	}
	// EOS first is a fact about the prompt, and a reader who does not know it
	// concludes the budget was ignored.
	if !strings.Contains(usageText, "EOS") {
		t.Error("--help does not say a run is often shorter than its budget because EOS arrives first")
	}
}

// TestDiscoveryFailureNamesEveryPortAndTheWayOut (TTP-75, 2026-09-14).
//
// A run on the rig this project develops against saw "no server answered
// /props" while a server was serving on :8001, one port away, and concluded
// there was no server rather than that it should pass --url. So a fruitless
// scan says what it looked at and what to do, and :8001 is in the list it looks
// at.
func TestDiscoveryFailureNamesEveryPortAndTheWayOut(t *testing.T) {
	if !contains(server.DefaultCandidates, "http://127.0.0.1:8001") {
		t.Error("discovery does not probe :8001, which is as conventional a second llama-server port as :8081")
	}
	// Discovery is pointed at ports nothing can be listening on, never at the
	// real defaults: this suite runs on the very rig that serves on :8001, and
	// a bare scan here would attach to it and record a real run. The hint is
	// built from server.DefaultCandidates whatever was probed, so the ports it
	// must name are still the real ones.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	root := t.TempDir()
	prev := pinCollectors
	pinCollectors = func(o recorder.Options) recorder.Options {
		o.FSRoot, o.GPU = root, gpu.Null{}
		o.Candidates = []string{deadURL}
		return o
	}
	t.Cleanup(func() { pinCollectors = prev })

	code, _, stderr := exec(t, "--wait", "0", "--out", t.TempDir())
	if code != exitUnreachable {
		t.Fatalf("exit %d, want %d\n%s", code, exitUnreachable, stderr)
	}
	for _, c := range server.DefaultCandidates {
		port := c[strings.LastIndex(c, ":")+1:]
		if !strings.Contains(stderr, port) {
			t.Errorf("the failure never names port %s, one of the ports it tried:\n%s", port, stderr)
		}
	}
	if !strings.Contains(stderr, "--url") {
		t.Errorf("the failure does not say that --url is the answer:\n%s", stderr)
	}
}

// recordFlagsBlock is the "Record flags:" section of the usage text.
func recordFlagsBlock(text string) (string, bool) {
	_, block, ok := strings.Cut(text, "Record flags:\n")
	if !ok {
		return "", false
	}
	block, _, _ = strings.Cut(block, "\nCard flags:")
	return block, true
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
