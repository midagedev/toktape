package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// rowLines is the row of a rendered card that starts with label: its first
// line and every line indented under it, with the box and the padding
// stripped.
func rowLines(s *tape.RunSummary, label string) []string {
	var out []string
	keep := false
	for _, line := range strings.Split(Text(s), "\n") {
		body := strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │")
		switch {
		case strings.HasPrefix(body, label+" "):
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

// TestSweepGolden pins the sweep example's card and summary (TTP-35): the only
// goldens carrying the Draft sweep row and the n_max-prefixed Prompts list.
func TestSweepGolden(t *testing.T) {
	s := ExampleSweep()
	golden(t, "example-sweep.txt", []byte(Text(s)))
	b, err := JSON(s)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	golden(t, "example-sweep.json", b)
}

func joinLines(lines []string) string { return strings.Join(lines, "\n") }

// TestDraftSweepRow (TTP-35, 2026-09-13) is the FAIL-first gate for the text
// card of a speculative n_max sweep: one aligned line per value with its median
// rate and acceptance, the fastest named, and every round of the Prompts list
// labelled by the n_max it ran at.
func TestDraftSweepRow(t *testing.T) {
	t.Run("the example prints one line per value and names the fastest", func(t *testing.T) {
		got := rowLines(ExampleSweep(), "Draft sweep")
		want := []string{
			"Draft sweep   n_max 3  14.8 tok/s median  61% accepted",
			"              n_max 5  16.1 tok/s median  51% accepted · fastest",
		}
		if joinLines(got) != joinLines(want) {
			t.Errorf("Draft sweep row:\n%s\nwant:\n%s", joinLines(got), joinLines(want))
		}
	})

	t.Run("the Prompts list names each round by its n_max", func(t *testing.T) {
		got := rowLines(ExampleSweep(), "Prompts")
		want := []string{
			"Prompts       12 rounds · 15.5 tok/s median (8.0–20.9)",
			"              accepted 56% median (5–87%)",
			"              3·sql-1 19.3 tok/s 87%  ·  3·sql-2 18.4 tok/s 83%",
			"              3·prose-1 9.1 tok/s 13%  ·  3·prose-2 10.2 tok/s 21%",
			"              3·code-1 15.6 tok/s 64%  ·  3·chat-1 14.0 tok/s 58%",
			"              5·sql-1 20.9 tok/s 73%  ·  5·sql-2 20.0 tok/s 70%",
			"              … +4 more",
		}
		if joinLines(got) != joinLines(want) {
			t.Errorf("Prompts row:\n%s\nwant:\n%s", joinLines(got), joinLines(want))
		}
	})

	t.Run("an unnamed round is its n_max and its place in the prompt set", func(t *testing.T) {
		s := ExampleSweep()
		s.PerRound[7].Name = ""
		if got := joinLines(rowLines(s, "Prompts")); !strings.Contains(got, "  ·  5·2 20.0 tok/s 70%") {
			t.Errorf("unnamed second prompt at n_max 5 is not \"5·2 20.0 tok/s 70%%\":\n%s", got)
		}
	})

	t.Run("columns are padded to the widest value", func(t *testing.T) {
		s := ExampleSweep()
		s.BySpecNMax[0].Spread.PerStreamPredictedPerSecond.Median = 9.1
		s.BySpecNMax[1].NMax = 16
		want := []string{
			"Draft sweep   n_max  3   9.1 tok/s median  61% accepted",
			"              n_max 16  16.1 tok/s median  51% accepted · fastest",
		}
		if got := rowLines(s, "Draft sweep"); joinLines(got) != joinLines(want) {
			t.Errorf("Draft sweep row:\n%s\nwant:\n%s", joinLines(got), joinLines(want))
		}
	})

	t.Run("a value with no acceptance prints ?", func(t *testing.T) {
		s := ExampleSweep()
		s.BySpecNMax[0].Spread.DraftAcceptRate = tape.Spread{}
		want := "Draft sweep   n_max 3  14.8 tok/s median    ? accepted"
		if got := rowLines(s, "Draft sweep"); len(got) == 0 || got[0] != want {
			t.Errorf("first line = %q, want %q", got, want)
		}
	})

	t.Run("no fastest without two different observed rates", func(t *testing.T) {
		tie := ExampleSweep()
		tie.BySpecNMax[0].Spread.PerStreamPredictedPerSecond.Median = 16.1
		lone := ExampleSweep()
		lone.BySpecNMax[0].Spread.PerStreamPredictedPerSecond = tape.Spread{}
		for name, s := range map[string]*tape.RunSummary{"a tie": tie, "one observed": lone} {
			if got := joinLines(rowLines(s, "Draft sweep")); strings.Contains(got, "fastest") {
				t.Errorf("%s named a fastest value:\n%s", name, got)
			}
		}
		if got := rowLines(lone, "Draft sweep"); len(got) == 0 || got[0] != "Draft sweep   n_max 3     ? tok/s median  61% accepted" {
			t.Errorf("unobserved rate line = %q", got)
		}
	})

	t.Run("no sweep, or one value, has no row", func(t *testing.T) {
		one := ExampleSweep()
		one.BySpecNMax = one.BySpecNMax[:1]
		for name, s := range map[string]*tape.RunSummary{
			"rounds":      ExampleRounds(),
			"speculative": ExampleSpeculative(),
			"one value":   one,
		} {
			if got := rowLines(s, "Draft sweep"); len(got) != 0 {
				t.Errorf("%s printed a Draft sweep row: %q", name, got)
			}
		}
		if got := joinLines(rowLines(ExampleRounds(), "Prompts")); strings.Contains(got, "·sql") {
			t.Errorf("a run without a sweep prefixed its rounds:\n%s", got)
		}
	})

	t.Run("the row follows Prompts and ends the speed section", func(t *testing.T) {
		order := map[string]int{}
		for i, line := range strings.Split(Text(ExampleSweep()), "\n") {
			body := strings.TrimPrefix(line, "│ ")
			for _, label := range []string{"Prompts ", "Draft sweep ", "MEMORY "} {
				if _, seen := order[label]; !seen && strings.HasPrefix(body, label) {
					order[label] = i
				}
			}
		}
		if len(order) != 3 || !(order["Prompts "] < order["Draft sweep "] && order["Draft sweep "] < order["MEMORY "]) {
			t.Errorf("row order is %v, want Prompts, Draft sweep, then MEMORY", order)
		}
	})

	t.Run("every line is the card's width", func(t *testing.T) {
		for i, line := range strings.Split(strings.TrimRight(Text(ExampleSweep()), "\n"), "\n") {
			if w := Width(line); w != CardWidth {
				t.Errorf("line %d is %d columns: %q", i+1, w, line)
			}
		}
	})
}

// TestDraftRowNamesTheSweptNMax (TTP-35, 2026-09-13) is the FAIL-first gate
// for the Draft row of a sweep. The requests overrode the server's --draft-max,
// so the flag's value is not what ran: the row names the values the requests
// carried, and a sweep of one value names that value, not the flag's.
func TestDraftRowNamesTheSweptNMax(t *testing.T) {
	for _, tc := range []struct {
		name string
		nmax []int
		want string
	}{
		{"two values", []int{3, 5}, "Draft         DSpark-0.6B-Q8_0.gguf · n_max 3,5"},
		{"one value over a flag of 3", []int{5}, "Draft         DSpark-0.6B-Q8_0.gguf · n_max 5"},
		{"no sweep keeps the flag", nil, "Draft         DSpark-0.6B-Q8_0.gguf · n_max 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := ExampleSweep()
			s.SpecNMax = tc.nmax
			got := rowLines(s, "Draft")
			if len(got) == 0 || !strings.HasPrefix(got[0], tc.want) {
				t.Fatalf("Draft row = %q, want it to start %q", got, tc.want)
			}
			if rest := strings.TrimPrefix(got[0], tc.want); rest != "" && !strings.HasPrefix(rest, " ·") {
				t.Errorf("Draft row = %q: the n_max clause runs on past %q", got[0], tc.want)
			}
		})
	}
}
