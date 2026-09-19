package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// runOne writes tp to a temp file and captures what one() prints for it.
func runOne(t *testing.T, tp *tape.Tape) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "probe.tape")
	if err := tape.Write(path, tp); err != nil {
		t.Fatalf("tape.Write: %v", err)
	}
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()
	err = one(path)
	w.Close()
	os.Stdout = old
	out := <-done
	if err != nil {
		t.Fatalf("one: %v", err)
	}
	return out
}

// The probe line names each point's cache_n beside its length (lead,
// 2026-09-19), so "why did this box get no fit" is answerable with one
// command: a fit refused because a point was cache-served shows the hit on
// the line, and a cold refusal shows two 0s — the reason has to be somewhere
// other than the cache.
func TestProbeLineNamesEachPointsCache(t *testing.T) {
	tp := &tape.Tape{Schema: 1, Summary: tape.RunSummary{ID: "probe-cache-test"}}
	tp.Summary.Probe = &tape.ProbeSummary{
		Prefill: []tape.PrefillPoint{
			{PromptN: 128, PromptMs: 94, CacheN: 0},
			{PromptN: 1707, PromptMs: 883.5, CacheN: 300},
		},
	}
	out := runOne(t, tp)
	for _, want := range []string{
		"128 tok (0 cached)",
		"1707 tok (300 cached)",
		"fit refused",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("probe line = %q, want it to carry %q", out, want)
		}
	}

	// The cold shape keeps its fit, and the line still names both 0s.
	tp.Summary.Probe = &tape.ProbeSummary{
		Prefill: []tape.PrefillPoint{
			{PromptN: 128, PromptMs: 94},
			{PromptN: 2007, PromptMs: 1033.5},
		},
		PrefillPerSecond: 2000,
		FixedMs:          30,
	}
	out = runOne(t, tp)
	for _, want := range []string{"2000 tok/s", "128 tok (0 cached)", "2007 tok (0 cached)"} {
		if !strings.Contains(out, want) {
			t.Errorf("probe line = %q, want it to carry %q", out, want)
		}
	}
}

// The endings line prints all three counts raw (lead, 2026-09-19). The
// schema's contract is that the two limit counts are disjoint and no
// renderer subtracts; the tool that exists to check the reducer is where a
// pair that violates it has to be visible, so it names both counts against
// the observed total and derives nothing.
func TestEndingsLineNamesBothCountsRaw(t *testing.T) {
	tp := &tape.Tape{Schema: 1, Summary: tape.RunSummary{ID: "endings-test"}}
	tp.Summary.Limit = tape.LimitSummary{
		CappedStreams:           3,
		ContextExhaustedStreams: 1,
		EndingsObserved:         4,
	}
	out := runOne(t, tp)
	if want := "3 capped · 1 ran out of context · 4 observed"; !strings.Contains(out, want) {
		t.Errorf("endings line = %q, want it to carry %q", out, want)
	}
}
