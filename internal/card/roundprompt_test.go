package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestPrefillSweepGolden pins the prefill sweep example's card (TTP-64,
// TTP-66): the only golden carrying the Prompts row's prefill and cache lines.
func TestPrefillSweepGolden(t *testing.T) {
	golden(t, "example-prefill-sweep.txt", []byte(Text(ExamplePrefillSweep())))
}

// subLine is the Prompts row's line that starts with word ("prefill",
// "cache"), with its continuation lines joined, or "" when there is none.
func subLine(s *tape.RunSummary, word string) string {
	var out []string
	keep := false
	for _, line := range promptsLines(s)[1:] {
		body := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(body, word+" "):
			keep = true
		case keep && (strings.HasPrefix(body, "prefill ") || strings.HasPrefix(body, "cache ")):
			keep = false
		}
		if keep {
			out = append(out, body)
		}
	}
	return strings.Join(out, " ")
}

// TestRoundPromptLines (TTP-64, TTP-66, 2026-09-14) is the FAIL-first gate for
// the Prompts row's per-round prefill and prefix-cache lines. The recorder
// records both per round from the server's own timings; before this the card
// printed neither, so a prompts file of 4k, 16k and 32k prompts produced one
// Prefill figure — the mean of four rates, one of them a cached repeat.
func TestRoundPromptLines(t *testing.T) {
	t.Run("the example prints prefill by length and the cached round", func(t *testing.T) {
		got := promptsLines(ExamplePrefillSweep())
		want := []string{
			"Prompts       4 rounds · 17.9 tok/s median (15.8–19.6)",
			"              1 diff 19.6 tok/s  ·  2 repo 17.9 tok/s",
			"              3 monorepo 15.8 tok/s  ·  4 repo-again 17.9 tok/s",
			"              prefill 4k 129 · 16k 104 · 32k 71.0 tok/s",
			"              cache 4 repo-again 97% hit of 16k, 516 evaluated",
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("Prompts row:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})

	// Rule 1: a round under tape.MinPrefillPromptTokens has no prefill figure,
	// and the line says why it is missing. The four-stream case is TTP-65's own
	// recording — four 63-token requests — whose round PromptN is the sum, 252,
	// over the floor although no stream's prompt was.
	for name, streams := range map[string]int{"one stream": 1, "four streams of 63": 4} {
		t.Run("a short round is not a prefill measurement, "+name, func(t *testing.T) {
			s := ExamplePrefillSweep()
			s.PerRound[0].Streams = streams
			s.PerRound[0].PromptN = 63 * streams
			s.PerRound[0].PromptPerSecond = 5.6
			line := subLine(s, "prefill")
			if strings.Contains(line, "5.6") || strings.Contains(line, "63 ") {
				t.Errorf("the short round's rate is on the prefill line: %q", line)
			}
			if !strings.Contains(line, "1 prompt under 100 tokens, not measured") {
				t.Errorf("the prefill line does not say a round was too short: %q", line)
			}
			if !strings.HasPrefix(line, "prefill 16k 104 · 32k 71.0 tok/s") {
				t.Errorf("the measured rounds are not listed: %q", line)
			}
		})
	}

	// Rule 2: PromptPerSecond 0 is a server that reported no prompt timings.
	t.Run("no prompt timings, no prefill figure", func(t *testing.T) {
		s := ExamplePrefillSweep()
		s.PerRound[2].PromptPerSecond = 0
		if line := subLine(s, "prefill"); line != "prefill 4k 129 · 16k 104 tok/s" {
			t.Errorf("prefill line with round 3 untimed is %q", line)
		}
		for i := range s.PerRound {
			s.PerRound[i].PromptPerSecond = 0
		}
		if line := subLine(s, "prefill"); line != "" {
			t.Errorf("a run with no prompt timings printed a prefill line: %q", line)
		}
		for _, l := range promptsLines(s) {
			if strings.Contains(l, "0 tok/s") && !strings.Contains(l, ".0 tok/s") {
				t.Errorf("a zero rate is printed: %q", l)
			}
		}
		if line := subLine(s, "cache"); line == "" {
			t.Errorf("the cache line depends on the prompt timings it does not print")
		}
	})

	// Rule 2's other half: a cold prefix is not a line of its own, and a cache
	// hit of a template's first tokens does not move a round off the prefill
	// line — the same "mostly served from the cache" reading the run's label
	// uses decides it.
	t.Run("a template-prefix hit stays a prefill figure", func(t *testing.T) {
		s := ExamplePrefillSweep()
		s.PerRound[1].PromptN, s.PerRound[1].CacheN = 16390, 1
		if line := subLine(s, "prefill"); line != "prefill 4k 129 · 16k 104 · 32k 71.0 tok/s" {
			t.Errorf("prefill line is %q", line)
		}
		if line := subLine(s, "cache"); strings.Contains(line, "repo ") || strings.Contains(line, "2 ") {
			t.Errorf("round 2's one cached token is on the cache line: %q", line)
		}
	})

	t.Run("no round cached, no cache line", func(t *testing.T) {
		s := ExamplePrefillSweep()
		s.PerRound[3].PromptN, s.PerRound[3].CacheN = 16391, 0
		if line := subLine(s, "cache"); line != "" {
			t.Errorf("a run with no cache hit printed %q", line)
		}
	})

	// Rule 3: a length label is the round's own count, per stream.
	t.Run("the length label is the round's own tokens per stream", func(t *testing.T) {
		s := ExamplePrefillSweep()
		s.PerRound[0].Streams = 4
		s.PerRound[0].PromptN = 4 * 4103
		if line := subLine(s, "prefill"); !strings.HasPrefix(line, "prefill 4k ") {
			t.Errorf("four streams of a 4k prompt are labelled %q", line)
		}
	})

	// The round and the run ask one threshold (shortPromptCount): at the floor
	// both say measured, one under it both say short.
	t.Run("the round and the run agree at the floor", func(t *testing.T) {
		for _, n := range []int{MinPrefillPromptTokens - 1, MinPrefillPromptTokens} {
			s := ExamplePrefillSweep()
			s.PerRound[0].PromptN = n
			s.PerRound[0].PromptPerSecond = 50
			roundShort := strings.Contains(subLine(s, "prefill"), "under 100 tokens")
			run := &tape.RunSummary{Timings: tape.TimingsSummary{PromptN: n}}
			if roundShort != ShortPrompt(run) {
				t.Errorf("at %d prompt tokens the round says short=%v and the run short=%v",
					n, roundShort, ShortPrompt(run))
			}
		}
	})

	t.Run("a run whose rounds carry no prompt figures has neither line", func(t *testing.T) {
		for name, s := range map[string]*tape.RunSummary{
			"rounds": ExampleRounds(),
			"sweep":  ExampleSweep(),
		} {
			if p, c := subLine(s, "prefill"), subLine(s, "cache"); p != "" || c != "" {
				t.Errorf("%s printed %q / %q", name, p, c)
			}
		}
	})

	t.Run("every line is the card's width", func(t *testing.T) {
		for i, line := range strings.Split(strings.TrimRight(Text(ExamplePrefillSweep()), "\n"), "\n") {
			if w := Width(line); w != CardWidth {
				t.Errorf("line %d is %d columns: %q", i+1, w, line)
			}
		}
	})
}

// TestExplainRoundPrompts: `card --explain` says, per round, which line its
// prompt figures went to and why a prefill figure was or was not printed.
func TestExplainRoundPrompts(t *testing.T) {
	s := ExamplePrefillSweep()
	s.PerRound[2].PromptN, s.PerRound[2].PromptPerSecond = 63, 5.6
	out := ExplainCaveats(s)
	for _, want := range []string{
		"round 1 diff",
		"prefill line: 4103 evaluated per stream at 129 tok/s, floor 100",
		"no prefill figure: 63 evaluated per stream, under the floor of 100",
		"cache line: 15875 of 16391 cached (97%, cached from 50%), 516 evaluated at 50.6 tok/s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing has no %q:\n%s", want, out)
		}
	}
	if out := ExplainCaveats(ExampleRounds()); strings.Contains(out, "round 1 ") {
		t.Errorf("a run whose rounds carry no prompt figures lists them:\n%s", out)
	}
}
