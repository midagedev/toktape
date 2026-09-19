// Package transcript projects a tape's requests into per-stream views for
// the run page's Details section.
//
// The web Worker never opens a tape (docs/toktape-spec.ko.md §9.6), so every
// judgement about the schema lives here, in Go, beside the schema's owner:
// which label ends a stream, what counts as its transcript, what its
// figures are. JavaScript only displays what this package returns — it
// formats numbers and escapes text, and decides nothing about the record.
package transcript

import (
	"github.com/midagedev/toktape/internal/tape"
)

// Msg is one chat message of a stream's prompt, verbatim.
type Msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Timings is the stream's own timings, copied without rounding and without
// defaults: an unobserved figure stays its zero value and the page prints
// "?" for it, the same rule the card keeps.
type Timings struct {
	PromptN             int     `json:"prompt_n"`
	CacheN              int     `json:"cache_n"`
	PredictedN          int     `json:"predicted_n"`
	ReasoningN          int     `json:"reasoning_n"`
	PredictedPerSecond  float64 `json:"predicted_per_second"`
	PromptPerSecond     float64 `json:"prompt_per_second"`
	TTFTMs              float64 `json:"ttft_ms"`
	ITLP50Ms            float64 `json:"itl_p50_ms"`
	ITLP95Ms            float64 `json:"itl_p95_ms"`
	ClientAgreesWithSer bool    `json:"client_agrees_with_server"`
	DecodeLabel         string  `json:"decode_label"`
}

// Stream is one request of the run: its prompt, its answer, and how it ended.
type Stream struct {
	Index        int     `json:"index"`
	Round        int     `json:"round"`
	Name         string  `json:"name"`
	Slot         int     `json:"slot"`
	StartedMs    int64   `json:"started_ms"`
	Messages     []Msg   `json:"messages"`
	Reasoning    string  `json:"reasoning"`
	ReasoningN   int     `json:"reasoning_n"`
	Completion   string  `json:"completion"`
	Ended        string  `json:"ended"`
	FinishReason string  `json:"finish_reason"`
	Cut          bool    `json:"cut"`
	Error        string  `json:"error"`
	MaxTokens    int     `json:"max_tokens"`
	Endpoint     string  `json:"endpoint"`
	Thinking     string  `json:"thinking"`
	Timings      Timings `json:"timings"`
	// Tokens is the count of token events, never the tokens themselves.
	Tokens int `json:"tokens"`
}

// Streams projects every request of t into a Stream, in start order. A nil
// tape gives an empty non-nil slice, so the caller never checks for null.
func Streams(t *tape.Tape) []Stream {
	out := []Stream{}
	if t == nil {
		return out
	}
	for _, r := range t.Requests {
		msgs := []Msg{}
		for _, m := range r.Prompt.Messages {
			// Verbatim, even a role this package has never heard of: the
			// page prints the string it was sent and sanitises only the
			// CSS class it derives from it.
			msgs = append(msgs, Msg{Role: m.Role, Content: m.Content})
		}
		ts := r.Timings
		out = append(out, Stream{
			Index:        r.Index,
			Round:        r.Round,
			Name:         r.Prompt.Name,
			Slot:         r.Slot,
			StartedMs:    r.StartedAt.Milliseconds(),
			Messages:     msgs,
			Reasoning:    r.Prompt.Reasoning,
			ReasoningN:   r.Prompt.ReasoningN,
			Completion:   r.Prompt.Completion,
			Ended:        ended(r),
			FinishReason: r.Prompt.FinishReason,
			Cut:          r.Prompt.Cut,
			Error:        r.Error,
			MaxTokens:    r.Prompt.MaxTokens,
			Endpoint:     r.Prompt.Endpoint,
			Thinking:     r.Prompt.Thinking,
			Timings: Timings{
				PromptN:             ts.PromptN,
				CacheN:              ts.CacheN,
				PredictedN:          ts.PredictedN,
				ReasoningN:          ts.ReasoningN,
				PredictedPerSecond:  ts.PredictedPerSecond,
				PromptPerSecond:     ts.PromptPerSecond,
				TTFTMs:              ts.TTFTMs,
				ITLP50Ms:            ts.ITLp50Ms,
				ITLP95Ms:            ts.ITLp95Ms,
				ClientAgreesWithSer: ts.ClientAgreesWithServer,
				DecodeLabel:         ts.DecodeLabel,
			},
			Tokens: len(r.Tokens),
		})
	}
	return out
}

// ended is the stream's end label. Precedence is exact: the error first,
// then the clock (PromptRecord.Cut's own comment says a reader asks Cut
// first and FinishReason second), then the server's finish word, and "?"
// when nothing was observed — never a default invented here.
func ended(r tape.RequestRecord) string {
	if r.Error != "" {
		return "failed: " + r.Error
	}
	if r.Prompt.Cut {
		return "clock cut"
	}
	// The limit words are read as the pair, not as the word (lead,
	// 2026-09-19). "limit" and "length" are both a limit, but four upstream
	// paths set that word and only the context-capacity one also sets
	// `truncated`, so this label says which — through the schema's own
	// predicates, so this renderer and the reducer's counts cannot land on
	// different sides of one record. The voice is this renderer's: the card
	// says "4 of 4 ran out of context" about a run, and a stream says it
	// about itself.
	if r.Prompt.EndedOnContextExhaustion() {
		return "context full"
	}
	if r.Prompt.EndedOnCap() {
		return "token cap"
	}
	switch r.Prompt.FinishReason {
	case "":
		return "?"
	// "stop" is the chat path's word for both of the raw path's own two,
	// which it collapses; the raw path says which, and a transcript that
	// prints "eos" at one and "model stopped" at the other would be naming
	// the endpoint rather than the ending.
	case "stop", "eos":
		return "model stopped"
	case "word":
		return "stop string"
	default:
		return r.Prompt.FinishReason
	}
}
