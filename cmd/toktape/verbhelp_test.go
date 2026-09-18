package main

import (
	"strings"
	"testing"
)

// TestVerbHelpIsTheVerbs: `toktape <verb> --help`, `<verb> -h` and `help
// <verb>` print the same bytes, exit 0, on stdout — for every verb (TTP-92).
//
// FAIL-first, 2026-09-19: on the source before usageFor, `render --help`
// printed the top-level text (98 lines, no --prefill-lead) while `help
// render` printed renderUsage; this table caught render and passed the rest.
// A coding agent asks a verb for its flags with `--help` before anything
// else, and was told about record's.
func TestVerbHelpIsTheVerbs(t *testing.T) {
	for verb := range verbs {
		if verb == "version" {
			continue // prints the version, has no flags
		}
		code, want, _ := exec(t, "help", verb)
		if code != exitOK || want == "" {
			t.Fatalf("help %s: exit %d, %d bytes", verb, code, len(want))
		}
		for _, spelling := range [][]string{{verb, "--help"}, {verb, "-h"}, {verb, "some.tape", "--help"}} {
			code, got, stderr := exec(t, spelling...)
			if code != exitOK {
				t.Errorf("%v exited %d: %s", spelling, code, stderr)
			}
			if got != want {
				t.Errorf("%v printed %d bytes that differ from `help %s` (%d bytes)", spelling, len(got), verb, len(want))
			}
		}
	}
	// render is the verb with a block of its own; the table above only
	// proves the three spellings agree, this proves they agree on the right
	// text.
	if _, out, _ := exec(t, "render", "--help"); !strings.Contains(out, "--prefill-lead") {
		t.Error("render --help does not list --prefill-lead")
	}
}
