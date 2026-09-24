package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// chatFixture is a real `toktape chat` session against a local llama-server
// (2026-09-24, two turns of the lead's own test prompts), so every chat
// renderer is pinned against what the recorder actually writes.
const chatFixture = "testdata/chat-e2e.toktape"

func readChatFixture(t *testing.T) *tape.Tape {
	t.Helper()
	tp, err := tape.Read(chatFixture)
	if err != nil {
		t.Fatalf("read %s: %v", chatFixture, err)
	}
	if tp.Summary.Mode != tape.ModeChat {
		t.Fatalf("%s is not a chat tape: mode %q", chatFixture, tp.Summary.Mode)
	}
	return tp
}

// TestChatCardSpeaksOfTurns: a chat tape's rounds are turns a person typed,
// so the row that lists them says so, with the benchmark row's figures and
// layout. A benchmark tape keeps its Prompts row (TestPromptsRow pins it).
func TestChatCardSpeaksOfTurns(t *testing.T) {
	s := &readChatFixture(t).Summary
	var row []string
	keep := false
	for _, line := range strings.Split(Text(s), "\n") {
		body := strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │")
		switch {
		case strings.HasPrefix(body, "Chat "):
			keep = true
		case keep && !strings.HasPrefix(body, strings.Repeat(" ", speedLabelW)):
			keep = false
		}
		if keep {
			row = append(row, strings.TrimRight(body, " "))
		}
		if strings.HasPrefix(body, "Prompts ") {
			t.Errorf("a chat card prints a Prompts row: %q", body)
		}
	}
	want := []string{
		"Chat          2 turns · 83.8 tok/s median (81.9–85.6)",
		"              1 85.6 tok/s  ·  2 81.9 tok/s",
		"              prefill 2 turns under 100 tokens, not measured",
	}
	if strings.Join(row, "\n") != strings.Join(want, "\n") {
		t.Errorf("the chat row:\n%s\nwant:\n%s", strings.Join(row, "\n"), strings.Join(want, "\n"))
	}
}

// TestChatCardAdvisesNoRecordFlag: every sentence a chat card can print that
// names a way out names one `toktape chat` accepts. --prompt, --prompts,
// --sessions and --for are record's, and chat refuses them
// (cmd/toktape/chat.go chatRefusedFlags), so advising one sends the reader to
// a flag that errors.
func TestChatCardAdvisesNoRecordFlag(t *testing.T) {
	refused := []string{"--prompt", "--sessions", "--for", "--spec-n-max"}
	check := func(t *testing.T, s *tape.RunSummary) {
		t.Helper()
		for _, c := range Caveats(s) {
			for _, f := range refused {
				if strings.Contains(c.Text, f) {
					t.Errorf("%s advises %s on a chat tape: %q", c.Code, f, c.Text)
				}
			}
		}
		md := Markdown(s)
		for _, f := range refused {
			if strings.Contains(md, f+" ") {
				t.Errorf("the Markdown card names %s on a chat tape:\n%s", f, md)
			}
		}
	}

	t.Run("the fixture's short generation", func(t *testing.T) {
		s := &readChatFixture(t).Summary
		var found bool
		for _, c := range Caveats(s) {
			if c.Code == CodeShortGeneration {
				found = true
				if !strings.Contains(c.Text, "ask for a longer answer") {
					t.Errorf("short_generation on a chat tape: %q", c.Text)
				}
			}
		}
		if !found {
			t.Fatal("the fixture no longer raises short_generation; the test needs a new witness")
		}
		check(t, s)
	})

	t.Run("an answer that was all reasoning", func(t *testing.T) {
		s := &readChatFixture(t).Summary
		s.Timings.ReasoningN = s.Timings.PredictedN
		w := answerCutWarning(s)
		if !strings.HasSuffix(w, "try --think-budget") {
			t.Errorf("answer_cut on a chat tape: %q", w)
		}
		check(t, s)
	})

	t.Run("the Markdown card's command is toktape chat", func(t *testing.T) {
		s := &readChatFixture(t).Summary
		md := Markdown(s)
		if !strings.Contains(md, "    toktape chat --url http://127.0.0.1:8080\n") {
			t.Errorf("the Reproduce block does not rebuild the chat:\n%s", md)
		}
		s.Limit.MaxTokensNamed, s.Limit.MaxTokens = true, 256
		if got := recordCommand(s); got != "toktape chat --url http://127.0.0.1:8080 --n-predict 256" {
			t.Errorf("recordCommand with a named cap = %q", got)
		}
	})
}

// TestBenchmarkAdviceUnchanged pins the benchmark wording the chat branches
// sit beside, so a mode check that inverted would show up here and not only
// as a golden diff.
func TestBenchmarkAdviceUnchanged(t *testing.T) {
	s := ExampleRounds()
	s.Timings.ReasoningN = s.Timings.PredictedN
	if w := answerCutWarning(s); !strings.Contains(w, "try --for") {
		t.Errorf("answer_cut on a benchmark tape: %q", w)
	}
	if got := RoundsLabel(s); got != "Prompts" {
		t.Errorf("RoundsLabel on a benchmark tape = %q", got)
	}
	if got := RoundsNoun(s, 2); got != "prompts" {
		t.Errorf("RoundsNoun on a benchmark tape = %q", got)
	}
}

// TestChatExplainNamesTurns: `card --explain` lists a chat's rounds by the
// same word the card's row uses.
func TestChatExplainNamesTurns(t *testing.T) {
	s := &readChatFixture(t).Summary
	got := ExplainCaveats(s)
	if !strings.Contains(got, "  turn 1 ") || strings.Contains(got, "  round 1 ") {
		t.Errorf("--explain on a chat:\n%s", got)
	}
}

// TestChatCardGolden pins the whole chat card as it prints for the fixture.
func TestChatCardGolden(t *testing.T) {
	golden(t, "chat-e2e.txt", []byte(Text(&readChatFixture(t).Summary)))
}
