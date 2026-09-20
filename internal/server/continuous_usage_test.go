package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// Lead, 2026-09-21. Measured on vLLM 0.29.0 (CPU, Qwen3-0.6B): the default
// 20 s clock cut the stream at 197 tokens, a cancelled stream never sends its
// closing usage chunk, and the card printed "Sample ?". vLLM will put its
// usage object on every chunk when asked (continuous_usage_stats), so the
// last chunk a cut stream saw already carries the server's count.

func TestContinuousUsageIsAskedForOnlyWhenSet(t *testing.T) {
	msgs := []tape.Message{{Role: "user", Content: "hi"}}
	plain := StreamRequest{Protocol: tape.ServerOpenAI, Model: "m", Messages: msgs}.openaiBody()
	if _, ok := plain["stream_options"].(map[string]any)["continuous_usage_stats"]; ok {
		t.Error("a server not known to be vLLM was sent a vLLM-only option; a strict server may refuse it")
	}
	on := StreamRequest{Protocol: tape.ServerOpenAI, Model: "m", Messages: msgs, ContinuousUsage: true}.openaiBody()
	so := on["stream_options"].(map[string]any)
	if so["continuous_usage_stats"] != true || so["include_usage"] != true {
		t.Errorf("stream_options = %v, want include_usage and continuous_usage_stats", so)
	}
}

func TestACutStreamKeepsTheCountItLastSaw(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for i := 1; i <= 6; i++ {
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ab cd \"}}],\"usage\":{\"prompt_tokens\":40,\"completion_tokens\":%d,\"total_tokens\":%d}}\n\n", i*2, 40+i*2)
			fl.Flush()
			time.Sleep(10 * time.Millisecond)
		}
		<-r.Context().Done() // never finishes: the client's clock ends it
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	rec, _, _ := New(srv.URL).Stream(ctx, StreamRequest{
		Protocol: tape.ServerOpenAI, Model: "m", ContinuousUsage: true,
		Messages: []tape.Message{{Role: "user", Content: "hi"}},
	}, StreamHooks{})
	if rec == nil {
		t.Fatal("a cut stream returned no record")
	}
	// Six chunks, twelve tokens: the count is the server's, not the chunks'.
	if rec.Timings.PredictedNSource != "usage" || rec.Timings.PredictedN != 12 {
		t.Errorf("PredictedN = %d from %q, want 12 from usage", rec.Timings.PredictedN, rec.Timings.PredictedNSource)
	}
	if rec.Timings.PromptN != 40 || rec.Timings.PromptNSource != "usage" {
		t.Errorf("PromptN = %d from %q, want 40 from usage", rec.Timings.PromptN, rec.Timings.PromptNSource)
	}
}
