package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// recordOne runs the record verb against the fake server and returns the tape
// it saved. Every assertion here is about what went over the wire or what was
// written down, so the tape — not the card — is what is read back.
func recordOne(t *testing.T, srvURL string, args ...string) *tape.Tape {
	t.Helper()
	dir := t.TempDir()
	full := append([]string{"--url", srvURL, "--out", dir, "--n-predict", "16", "--no-card", "--quiet"}, args...)
	code, _, stderr := exec(t, full...)
	if code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
	}
	tapes, err := filepath.Glob(filepath.Join(dir, "*"+tape.Ext))
	if err != nil || len(tapes) != 1 {
		t.Fatalf("run files = %v (err %v), want one tape", tapes, err)
	}
	tp, err := tape.Read(tapes[0])
	if err != nil {
		t.Fatalf("the saved tape does not reload: %v", err)
	}
	return tp
}

// TestThinkBudgetSendsTheKeyTheServerReads (TTP-73, 2026-09-14).
//
// The spelling is the whole flag. llama.cpp reads `reasoning_budget_tokens`
// and its alias `thinking_budget_tokens`
// (tools/server/server-common.cpp:1388) and silently ignores
// `reasoning_budget`: measured on a real server at max_tokens 300 with one
// prompt, reasoning_budget=64 produced 387 characters of reasoning — the
// server's unbounded default — and reasoning_budget_tokens=64 produced 294,
// about sixty-four tokens, and then the answer. A flag that sent the ignored
// key would record a tape claiming a cap that never applied.
func TestThinkBudgetSendsTheKeyTheServerReads(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)

	tp := recordOne(t, srv.URL, "--think-budget", "64")
	if len(tp.Requests) == 0 {
		t.Fatal("no request recorded")
	}
	params := tp.Requests[0].Prompt.Params
	got, ok := params["reasoning_budget_tokens"]
	if !ok {
		t.Fatalf("the request carried no reasoning_budget_tokens: %+v", params)
	}
	if n, ok := got.(float64); !ok || n != 64 {
		t.Errorf("reasoning_budget_tokens = %v (%T), want 64", got, got)
	}
	if _, ok := params["reasoning_budget"]; ok {
		t.Error("the request carried reasoning_budget, which llama-server ignores")
	}

	// 0 is a budget, not the absence of one, and must reach the wire.
	tp = recordOne(t, srv.URL, "--think-budget", "0")
	if got := tp.Requests[0].Prompt.Params["reasoning_budget_tokens"]; got != 0.0 {
		t.Errorf("--think-budget 0 sent %v, want 0", got)
	}

	// Without the flag nothing is sent: the server's own budget applies, and
	// the tape must not claim a cap nobody asked for.
	tp = recordOne(t, srv.URL)
	if _, ok := tp.Requests[0].Prompt.Params["reasoning_budget_tokens"]; ok {
		t.Errorf("a run without --think-budget sent a budget: %+v", tp.Requests[0].Prompt.Params)
	}
}

// TestThinkBudgetRefusals: the three ways of asking for a budget that cannot
// apply, each of which would otherwise record a request nobody made.
func TestThinkBudgetRefusals(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"on a raw prompt", []string{"--think-budget", "64", "--endpoint", "completion"}, "thinking is the template's"},
		{"with --no-think", []string{"--think-budget", "64", "--no-think"}, "give one"},
		{"a negative budget", []string{"--think-budget", "-1"}, "give a token count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--url", srv.URL, "--out", t.TempDir()}, tc.args...)
			code, _, stderr := exec(t, args...)
			if code != exitUsage {
				t.Fatalf("exit %d, want %d\n%s", code, exitUsage, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr does not mention %q:\n%s", tc.want, stderr)
			}
		})
	}
}

// TestIgnoredThinkKeyIsCalledOut: a --param that spells the budget the way
// llama-server drops it still runs, and says so. It is a misuse worth a
// sentence rather than a refusal — a future build may read the key — but a
// silent run would be a card claiming a cap that was never applied.
func TestIgnoredThinkKeyIsCalledOut(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	code, _, stderr := exec(t, "--url", srv.URL, "--out", t.TempDir(), "--n-predict", "16",
		"--no-card", "--param", "reasoning_budget=64")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "reasoning_budget_tokens") {
		t.Errorf("nothing named the key the server actually reads:\n%s", stderr)
	}
}

// TestRAMFlagsRecordWhatTheOperatorSaid (TTP-45, 2026-09-14).
//
// On Linux the memory speed and channel count live in the DMI tables, which
// are root-only, so a partially offloaded run has no host bandwidth ceiling
// and the card can print no "of peak" ratio. The operator may state the
// figure — and the tape must then say that is what happened, because a number
// somebody typed and a number the tool measured are different claims.
func TestRAMFlagsRecordWhatTheOperatorSaid(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)

	t.Run("stated GB/s", func(t *testing.T) {
		h := recordOne(t, srv.URL, "--ram-gbs", "204.8").Summary.Host
		if h.RAMBytesPerSec != 204_800_000_000 {
			t.Errorf("RAMBytesPerSec = %d, want 204800000000", h.RAMBytesPerSec)
		}
		if h.RAMSource != tape.RAMSourceStated {
			t.Errorf("RAMSource = %q, want %q", h.RAMSource, tape.RAMSourceStated)
		}
	})

	t.Run("measured GB/s", func(t *testing.T) {
		h := recordOne(t, srv.URL, "--ram-gbs-measured", "181.5").Summary.Host
		if h.RAMBytesPerSec != 181_500_000_000 {
			t.Errorf("RAMBytesPerSec = %d, want 181500000000", h.RAMBytesPerSec)
		}
		if h.RAMSource != tape.RAMSourceMeasured {
			t.Errorf("RAMSource = %q, want %q", h.RAMSource, tape.RAMSourceMeasured)
		}
	})

	t.Run("speed and channels", func(t *testing.T) {
		h := recordOne(t, srv.URL, "--ram-speed", "DDR5-5200", "--ram-channels", "8").Summary.Host
		if h.RAMSpeed != "DDR5-5200" || h.RAMChannels != 8 {
			t.Errorf("RAMSpeed/RAMChannels = %q/%d", h.RAMSpeed, h.RAMChannels)
		}
		// The derivation is internal/bandwidth's; the CLI states the pair and
		// computes no ceiling of its own.
		if h.RAMBytesPerSec != 0 {
			t.Errorf("the CLI computed a bandwidth of its own: %d", h.RAMBytesPerSec)
		}
		if h.RAMSource != tape.RAMSourceStated {
			t.Errorf("RAMSource = %q, want %q", h.RAMSource, tape.RAMSourceStated)
		}
	})

	// Without the flags nothing is filled in. This is the rule the card rests
	// on: unknown is 0 and "", never a plausible default.
	t.Run("no flags invents nothing", func(t *testing.T) {
		h := recordOne(t, srv.URL).Summary.Host
		if h.RAMBytesPerSec != 0 || h.RAMSource != "" {
			t.Errorf("a plain run stated a bandwidth: %d %q", h.RAMBytesPerSec, h.RAMSource)
		}
	})
}

// TestRAMFlagRefusals: two ceilings, or a figure that is not a figure.
func TestRAMFlagRefusals(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"two sources", []string{"--ram-gbs", "200", "--ram-gbs-measured", "180"}, "one figure has one source"},
		{"two ceilings", []string{"--ram-gbs", "200", "--ram-channels", "8"}, "give one of the two"},
		{"not a bandwidth", []string{"--ram-gbs", "0"}, "positive GB/s"},
		{"not a channel count", []string{"--ram-channels", "0"}, "positive channel count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--url", srv.URL, "--out", t.TempDir()}, tc.args...)
			code, _, stderr := exec(t, args...)
			if code != exitUsage {
				t.Fatalf("exit %d, want %d\n%s", code, exitUsage, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr does not mention %q:\n%s", tc.want, stderr)
			}
		})
	}
}
