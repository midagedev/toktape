package transcript

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// ended inputs → labels, one line per case:
// error                       → "failed: <Error>"
// cut (FinishReason empty)    → "clock cut"
// FinishReason "stop"         → "model stopped"
// FinishReason "length"       → "token cap"
// FinishReason "tool_calls"   → "tool_calls" (verbatim)
// FinishReason ""             → "?"
func TestEndedLabels(t *testing.T) {
	cases := []struct {
		name   string
		record tape.RequestRecord
		want   string
	}{
		{"error", tape.RequestRecord{Error: "connection reset"}, "failed: connection reset"},
		{"error beats cut", tape.RequestRecord{Error: "boom", Prompt: tape.PromptRecord{Cut: true}}, "failed: boom"},
		{"error beats finish reason", tape.RequestRecord{Error: "boom", Prompt: tape.PromptRecord{FinishReason: "stop"}}, "failed: boom"},
		{"cut", tape.RequestRecord{Prompt: tape.PromptRecord{Cut: true}}, "clock cut"},
		{"cut beats finish reason", tape.RequestRecord{Prompt: tape.PromptRecord{Cut: true, FinishReason: "stop"}}, "clock cut"},
		{"stop", tape.RequestRecord{Prompt: tape.PromptRecord{FinishReason: "stop"}}, "model stopped"},
		{"length", tape.RequestRecord{Prompt: tape.PromptRecord{FinishReason: "length"}}, "token cap"},
		{"other verbatim", tape.RequestRecord{Prompt: tape.PromptRecord{FinishReason: "tool_calls"}}, "tool_calls"},
		{"empty", tape.RequestRecord{}, "?"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Streams(&tape.Tape{Schema: tape.SchemaVersion, Requests: []tape.RequestRecord{c.record}})
			if len(got) != 1 {
				t.Fatalf("Streams returned %d streams, want 1", len(got))
			}
			if got[0].Ended != c.want {
				t.Errorf("Ended = %q, want %q", got[0].Ended, c.want)
			}
			// The finish word, the clock flag and the error travel beside
			// the label, so the page never has to parse the label back.
			if got[0].FinishReason != c.record.Prompt.FinishReason {
				t.Errorf("FinishReason = %q, want %q", got[0].FinishReason, c.record.Prompt.FinishReason)
			}
			if got[0].Cut != c.record.Prompt.Cut {
				t.Errorf("Cut = %v, want %v", got[0].Cut, c.record.Prompt.Cut)
			}
			if got[0].Error != c.record.Error {
				t.Errorf("Error = %q, want %q", got[0].Error, c.record.Error)
			}
		})
	}
}

func TestFieldsPassThrough(t *testing.T) {
	in := tape.RequestRecord{
		Index:     2,
		Round:     1,
		Slot:      3,
		StartedAt: 1500 * time.Millisecond,
		Prompt: tape.PromptRecord{
			Name:         "second",
			Messages:     []tape.Message{{Role: "system", Content: "be brief"}, {Role: "user", Content: "hi"}},
			Reasoning:    "they said hi",
			ReasoningN:   4,
			Completion:   "hello",
			FinishReason: "stop",
			MaxTokens:    512,
			Endpoint:     tape.EndpointChat,
			Thinking:     "off",
		},
		Tokens: []tape.TokenEvent{{T: time.Second, Index: 0, Text: "hello"}},
		Timings: tape.TimingsSummary{
			PromptN: 10, CacheN: 2, PredictedN: 1, ReasoningN: 4,
			PredictedPerSecond: 20.5, PromptPerSecond: 100.25,
			TTFTMs: 300, ITLp50Ms: 40, ITLp95Ms: 60,
			ClientAgreesWithServer: true, DecodeLabel: "sample",
		},
	}
	got := Streams(&tape.Tape{Schema: tape.SchemaVersion, Requests: []tape.RequestRecord{in}})
	if len(got) != 1 {
		t.Fatalf("Streams returned %d streams, want 1", len(got))
	}
	s := got[0]
	if s.Index != 2 || s.Round != 1 || s.Name != "second" || s.Slot != 3 || s.StartedMs != 1500 {
		t.Errorf("identity fields = %+v, want index 2 round 1 name second slot 3 started 1500ms", s)
	}
	if len(s.Messages) != 2 || s.Messages[0] != (Msg{Role: "system", Content: "be brief"}) || s.Messages[1].Content != "hi" {
		t.Errorf("Messages = %+v, want the two prompt messages verbatim", s.Messages)
	}
	if s.Reasoning != "they said hi" || s.ReasoningN != 4 || s.Completion != "hello" {
		t.Errorf("text fields = %q %d %q, want them verbatim", s.Reasoning, s.ReasoningN, s.Completion)
	}
	if s.Tokens != 1 {
		t.Errorf("Tokens = %d, want the count 1, never the list", s.Tokens)
	}
	ts := s.Timings
	if ts.PromptN != 10 || ts.CacheN != 2 || ts.PredictedN != 1 || ts.ReasoningN != 4 ||
		ts.PredictedPerSecond != 20.5 || ts.PromptPerSecond != 100.25 ||
		ts.TTFTMs != 300 || ts.ITLP50Ms != 40 || ts.ITLP95Ms != 60 ||
		!ts.ClientAgreesWithSer || ts.DecodeLabel != "sample" {
		t.Errorf("Timings = %+v, want an unrounded copy", ts)
	}
	if s.MaxTokens != 512 || s.Endpoint != "chat" || s.Thinking != "off" {
		t.Errorf("request fields = %d %q %q, want 512 chat off", s.MaxTokens, s.Endpoint, s.Thinking)
	}
}

// An empty run is a run with nothing to show, not a null: the page iterates
// the array without checking.
func TestEmptyRequests(t *testing.T) {
	got := Streams(&tape.Tape{Schema: tape.SchemaVersion})
	if got == nil {
		t.Fatal("Streams of a tape with no requests is nil, want an empty non-nil slice")
	}
	if len(got) != 0 {
		t.Fatalf("Streams returned %d streams, want 0", len(got))
	}
	if Streams(nil) == nil {
		t.Fatal("Streams(nil) is nil, want an empty non-nil slice")
	}
}

// A role the page has never heard of still arrives verbatim: the page prints
// the string and sanitises only the CSS class it derives from it.
func TestUnknownRolePassesThrough(t *testing.T) {
	in := tape.RequestRecord{
		Prompt: tape.PromptRecord{Messages: []tape.Message{{Role: "tool", Content: "exit 0"}}},
	}
	got := Streams(&tape.Tape{Schema: tape.SchemaVersion, Requests: []tape.RequestRecord{in}})
	if len(got) != 1 || len(got[0].Messages) != 1 || got[0].Messages[0].Role != "tool" {
		t.Fatalf("Messages = %+v, want the tool role verbatim", got)
	}
}

// Markup in model text crosses into JavaScript raw: escaping is the page's
// job (host.js escapeHTML), and a Go layer that escaped would double-escape
// once the page does its own.
func TestMarkupPassesThroughRaw(t *testing.T) {
	raw := `</pre><script>alert(1)</script>`
	in := tape.RequestRecord{
		Prompt: tape.PromptRecord{
			Messages:   []tape.Message{{Role: "user", Content: raw}},
			Completion: raw,
			Reasoning:  raw,
		},
	}
	got := Streams(&tape.Tape{Schema: tape.SchemaVersion, Requests: []tape.RequestRecord{in}})
	s := got[0]
	if s.Messages[0].Content != raw || s.Completion != raw || s.Reasoning != raw {
		t.Errorf("text = %+v, want the markup byte-identical", s)
	}
}

// The README hero is a real recording; it must project to at least one
// stream with an answer. assets/ is read-only: this test only reads.
func TestHeroTape(t *testing.T) {
	tp, err := tape.Read("../../assets/hero.tape")
	if err != nil {
		t.Fatalf("Read hero.tape: %v", err)
	}
	got := Streams(tp)
	if len(got) < 1 {
		t.Fatal("hero.tape projected to no streams")
	}
	if got[0].Completion == "" {
		t.Error("hero.tape's first stream has an empty completion")
	}
	t.Logf("hero.tape: %d streams, first ended %q", len(got), got[0].Ended)
}
