package card

import (
	"fmt"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// promptsLines is the Prompts row of a rendered card: its first line and every
// line indented under it, with the box and the padding stripped.
func promptsLines(s *tape.RunSummary) []string {
	var out []string
	keep := false
	for _, line := range strings.Split(Text(s), "\n") {
		body := strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │")
		switch {
		case strings.HasPrefix(body, "Prompts "):
			keep = true
		case keep && !strings.HasPrefix(body, strings.Repeat(" ", speedLabelW)):
			keep = false
		}
		if keep {
			out = append(out, strings.TrimRight(body, " "))
		}
	}
	return out
}

// TestPromptsRow pins the speed section's Prompts row (TTP-31, 2026-09-13).
//
// One prompt's card is misleading when a draft model is accepted 13 % on prose
// and 87 % on SQL, so a multi-prompt run prints the median with its range and
// then every round's own figures, labelled by name or by number.
func TestPromptsRow(t *testing.T) {
	t.Run("the example prints the medians, the ranges and every round", func(t *testing.T) {
		got := promptsLines(ExampleRounds())
		want := []string{
			"Prompts       6 rounds · 14.8 tok/s median (9.1–19.3)",
			"              accepted 61% median (13–87%)",
			"              1 sql-1 19.3 tok/s 87%  ·  2 sql-2 18.4 tok/s 83%",
			"              3 prose-1 9.1 tok/s 13%  ·  4 prose-2 10.2 tok/s 21%",
			"              5 code-1 15.6 tok/s 64%  ·  6 chat-1 14.0 tok/s 58%",
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("Prompts row:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})

	t.Run("a single-round run has no row", func(t *testing.T) {
		for name, s := range map[string]*tape.RunSummary{
			"speculative": ExampleSpeculative(),
			"concurrent":  ExampleConcurrent(),
			"one round":   oneRound(),
		} {
			if got := promptsLines(s); len(got) != 0 {
				t.Errorf("%s printed a Prompts row: %q", name, got)
			}
		}
	})

	t.Run("no draft, no accepted clause", func(t *testing.T) {
		s := ExampleRounds()
		s.Spread.DraftAcceptRate = tape.Spread{}
		for i := range s.PerRound {
			s.PerRound[i].DraftN, s.PerRound[i].DraftNAccepted = nil, nil
		}
		got := strings.Join(promptsLines(s), "\n")
		if strings.Contains(got, "accepted") || strings.Contains(got, "%") {
			t.Errorf("a run without a draft printed an acceptance figure:\n%s", got)
		}
		if !strings.Contains(got, "1 sql-1 19.3 tok/s  ·  2 sql-2 18.4 tok/s") {
			t.Errorf("round list without draft figures is\n%s", got)
		}
	})

	t.Run("an unnamed round is labelled by its number alone", func(t *testing.T) {
		s := ExampleRounds()
		s.PerRound[1].Name = ""
		got := strings.Join(promptsLines(s), "\n")
		if !strings.Contains(got, "  ·  2 18.4 tok/s 83%") {
			t.Errorf("unnamed round 2 is not \"2 18.4 tok/s 83%%\":\n%s", got)
		}
	})

	t.Run("at most eight rounds are listed", func(t *testing.T) {
		s := ExampleRounds()
		for k := len(s.PerRound); k < 11; k++ {
			r := s.PerRound[k%6]
			r.Index, r.Name = k, fmt.Sprintf("extra-%d", k+1)
			s.PerRound = append(s.PerRound, r)
		}
		s.Rounds = len(s.PerRound)
		got := strings.Join(promptsLines(s), "\n")
		if !strings.Contains(got, "8 extra-8 ") || strings.Contains(got, "9 extra-9") {
			t.Errorf("the list does not stop after round 8:\n%s", got)
		}
		if !strings.Contains(got, "… +3 more") {
			t.Errorf("the list does not say how many rounds it left out:\n%s", got)
		}
		if lines := promptsLines(s); len(lines) == 0 || !strings.HasPrefix(lines[0], "Prompts       11 rounds · ") {
			t.Errorf("the head does not count every round: %q", lines)
		}
	})

	t.Run("the row is the last of the speed section", func(t *testing.T) {
		order := map[string]int{}
		for i, line := range strings.Split(Text(ExampleRounds()), "\n") {
			body := strings.TrimPrefix(line, "│ ")
			for _, label := range []string{"Draft ", "Streams ", "Prompts ", "MEMORY "} {
				if _, seen := order[label]; !seen && strings.HasPrefix(body, label) {
					order[label] = i
				}
			}
		}
		if len(order) != 4 || !(order["Draft "] < order["Streams "] && order["Streams "] < order["Prompts "]) {
			t.Errorf("row order is %v, want Draft, Streams, then Prompts", order)
		}
	})

	t.Run("every line is the card's width", func(t *testing.T) {
		for i, line := range strings.Split(strings.TrimRight(Text(ExampleRounds()), "\n"), "\n") {
			if w := Width(line); w != CardWidth {
				t.Errorf("line %d is %d columns: %q", i+1, w, line)
			}
		}
	})
}

// oneRound is a prompts file with a single line: Rounds is 1, and the schema
// leaves PerRound and Spread empty, so the card is a plain run's.
func oneRound() *tape.RunSummary {
	s := ExampleSpeculative()
	s.Rounds = 1
	return s
}
