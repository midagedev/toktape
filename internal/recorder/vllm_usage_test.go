package recorder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// Lead, 2026-09-21: continuous usage is a vLLM extension, so it rides only on
// requests for a model the listing says vLLM owns.
func TestContinuousUsageFollowsTheListingsOwner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"a","owned_by":"vllm"},{"id":"b","owned_by":"library"}]}`))
	}))
	defer srv.Close()
	r := &run{}
	if _, _, err := r.probeOpenAI(context.Background(), server.New(srv.URL)); err != nil {
		t.Fatal(err)
	}
	msg := []tape.Message{{Role: "user", Content: "hi"}}
	reqs, err := r.shapeOpenAIRequests([]server.StreamRequest{{Messages: msg}, {Messages: msg, Model: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reqs[0].ContinuousUsage {
		t.Error("the vLLM-owned model's request does not ask for usage on every chunk")
	}
	if reqs[1].ContinuousUsage {
		t.Error("a model vLLM does not own was sent the vLLM-only option")
	}
}
