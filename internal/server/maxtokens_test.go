package server

import (
	"context"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestSentMaxTokensReadsTheAssembledBody: the cap the tape records has to be
// the cap the server was asked for, which is the body after Params has been
// merged over the defaults — not the MaxTokens field on its own.
func TestSentMaxTokensReadsTheAssembledBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  StreamRequest
		want int
	}{
		{"the field", StreamRequest{MaxTokens: 320}, 320},
		{"no cap at all", StreamRequest{}, 0},
		{
			"n_predict in params",
			StreamRequest{Params: map[string]any{"n_predict": 96}},
			96,
		},
		{
			"params override the field",
			StreamRequest{MaxTokens: 320, Params: map[string]any{"max_tokens": 64}},
			64,
		},
		{
			// llama-server's "no limit". Not a cap, so nothing to print a
			// fraction of (CLAUDE.md: an unobserved value is 0, never a
			// plausible default).
			"negative is no limit",
			StreamRequest{Params: map[string]any{"n_predict": -1}},
			0,
		},
		{
			// A caller that built the params from JSON hands over float64s.
			"a float from json",
			StreamRequest{Params: map[string]any{"max_tokens": float64(128)}},
			128,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.req.SentMaxTokens(); got != tc.want {
				t.Errorf("SentMaxTokens() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestStreamRecordsTheCapItWasSentWith is the round trip: a real stream over
// the test server leaves the cap in the record, so a replayed tape can print
// "80/320" without re-deriving it from the parameter map.
func TestStreamRecordsTheCapItWasSentWith(t *testing.T) {
	srv := replayServer(t, "stream_basic.sse", 0, nil)

	rec, _, err := New(srv.URL).Stream(context.Background(), StreamRequest{
		Messages:  []tape.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 320,
	}, StreamHooks{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if rec.Prompt.MaxTokens != 320 {
		t.Errorf("recorded MaxTokens = %d, want 320", rec.Prompt.MaxTokens)
	}
}
