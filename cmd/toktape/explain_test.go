package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// `card --explain` is the debuggability layer of the qualification round
// (TTP-74, 2026-09-14): internal/card.ExplainCaveats and
// internal/bandwidth.Explain both existed and neither could be run, so the
// question they answer — "why does this card not say X" — was still answered
// by reading source with a tape open beside it, which is how the 5.6 tok/s
// prefill survived to be found by a human.

// explainTape is a run with something to explain: two streams, one of which
// failed, and a recorder warning, so the listing has both a fired check and
// the ones that did not fire.
func explainTape(t *testing.T) string {
	t.Helper()
	s := &tape.RunSummary{
		ID:          "20260914-101500-qwen3",
		Concurrency: 2,
		Warnings:    []string{"pid not found, no /proc view"},
	}
	s.Aggregate.Streams, s.Aggregate.StreamsFailed = 2, 1
	s.Timings.PredictedN, s.Timings.PredictedPerSecond = 240, 12.5
	return writeTape(t, t.TempDir(), s)
}

// TestExplainGoesToStderr: the listing is diagnostic output and leaves by the
// diagnostic door, so the card on stdout is byte for byte the card that would
// have been printed without the flag.
func TestExplainGoesToStderr(t *testing.T) {
	path := explainTape(t)

	code, plain, _ := exec(t, "card", path)
	if code != exitOK {
		t.Fatalf("card exited %d", code)
	}
	code, stdout, stderr := exec(t, "card", path, "--explain")
	if code != exitOK {
		t.Fatalf("card --explain exited %d\nstderr:\n%s", code, stderr)
	}
	if stdout != plain {
		t.Errorf("--explain changed the product on stdout:\n--- with ---\n%s\n--- without ---\n%s", stdout, plain)
	}
	// Both explainers, under one flag: the reader asking why a figure is
	// missing does not know which package declined to produce it.
	for _, want := range []string{
		"caveats of 20260914-101500-qwen3", // internal/card
		"streams_failed",                   // a check that fired
		"cold_cache",                       // one that did not, which is the point
		"record ",                          // internal/bandwidth
		"ceiling ?",                        // and a figure it could not derive
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the explanation does not carry %q:\n%s", want, stderr)
		}
	}
}

// TestExplainDoesNotContaminateJSON: --json's contract is exactly one object on
// stdout, and a diagnostic must not be able to break it.
func TestExplainDoesNotContaminateJSON(t *testing.T) {
	path := explainTape(t)
	code, stdout, stderr := exec(t, "card", path, "--json", "--explain")
	if code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
	}
	if _, ok := doc["caveats"]; !ok {
		t.Error("the JSON card lost its caveats array")
	}
	if strings.Contains(stdout, "caveats of ") {
		t.Error("the listing was written to stdout")
	}
	if !strings.Contains(stderr, "caveats of ") {
		t.Error("the listing was not written to stderr")
	}
}

// TestExplainIsFindable: a debugging tool nobody can find is the defect it was
// written to close. It is in the flag list every caller reads, and in the topic
// written for the reader most likely to need it.
func TestExplainIsFindable(t *testing.T) {
	if !strings.Contains(usageText, "--explain") {
		t.Error("--help does not list --explain")
	}
	if !strings.Contains(agentsTopic(), "--explain") {
		t.Error("`help agents` does not mention --explain")
	}
}

// TestAgentsTopicSendsTheReaderToCaveats: the topic names both fields and says
// which one answers "is this number quotable".
//
// Until 2026-09-14 it named only "warnings", which is the recorder's own free
// text and a SUBSET of the derived list — so the one reader this whole release
// is for was being sent to the smaller list.
func TestAgentsTopicSendsTheReaderToCaveats(t *testing.T) {
	topic := agentsTopic()
	if !strings.Contains(topic, "caveats") {
		t.Fatal("`help agents` does not name caveats")
	}
	// The three severities are the field a machine branches on.
	for _, want := range []string{"figure", "run", "view"} {
		if !strings.Contains(topic, want) {
			t.Errorf("`help agents` does not name the %q severity", want)
		}
	}
	// And the relationship between the two lists, so a reader of warnings
	// knows what it is missing.
	if !strings.Contains(topic, "recorded") {
		t.Error("`help agents` does not say that warnings ride through caveats under a code")
	}
}
