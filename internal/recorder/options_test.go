package recorder

import (
	"testing"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// Sampling and the raw path reaching every request (TTP-55, 2026-09-14).

// TestBuildRequestsMergesParams: --temp and --param are run-wide, so they must
// land on the default prompt set too, which is the set a zero-config run uses
// and therefore the one most cards are made from.
func TestBuildRequestsMergesParams(t *testing.T) {
	o := Options{Concurrency: 3, Params: map[string]any{"temperature": 0.0, "seed": 7}}.normalize()
	reqs := buildRequests(o, 0)
	if len(reqs) != 3 {
		t.Fatalf("got %d requests, want 3", len(reqs))
	}
	for i, r := range reqs {
		if r.Params["temperature"] != 0.0 || r.Params["seed"] != 7 {
			t.Fatalf("request %d params %+v, want the run's", i, r.Params)
		}
		if r.Body()["temperature"] != 0.0 {
			t.Fatalf("request %d body lost the temperature: %+v", i, r.Body())
		}
	}
}

// TestBuildRequestsMergesTemplateKwargs: two switches share that one object.
// Replacing it would let --no-think silently drop a kwarg the prompt carried,
// and the tape would then describe a request that was never sent.
func TestBuildRequestsMergesTemplateKwargs(t *testing.T) {
	o := Options{
		Prompts: []server.StreamRequest{{
			Messages: []tape.Message{{Role: "user", Content: "hi"}},
			Params:   map[string]any{"chat_template_kwargs": map[string]any{"tools": "none"}},
		}},
		Params: map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false}},
	}.normalize()

	reqs := buildRequests(o, 0)
	kw, ok := reqs[0].Params["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("kwargs %T, want an object: %+v", reqs[0].Params["chat_template_kwargs"], reqs[0].Params)
	}
	if kw["tools"] != "none" || kw["enable_thinking"] != false {
		t.Fatalf("kwargs %+v, want both switches", kw)
	}
	if reqs[0].ThinkingSetting() != "off" {
		t.Fatalf("thinking %q, want off", reqs[0].ThinkingSetting())
	}
	// The prompt's own map must not have been mutated underneath it: two
	// requests cycled from one prompt would otherwise share and overwrite it.
	orig := o.Prompts[0].Params["chat_template_kwargs"].(map[string]any)
	if _, leaked := orig["enable_thinking"]; leaked {
		t.Fatalf("the run's switch leaked into the caller's prompt: %+v", orig)
	}
}

// TestBuildRequestsSwitchesToTheRawPath: on /completion there is no template
// and there are no messages, so the prompt text moves into Prompt and the
// conversation is dropped. A record that kept the messages would claim a
// conversation the server never saw.
func TestBuildRequestsSwitchesToTheRawPath(t *testing.T) {
	o := Options{Concurrency: 2, Endpoint: tape.EndpointCompletion}.normalize()
	reqs := buildRequests(o, 0)
	for i, r := range reqs {
		if !r.IsCompletion() {
			t.Fatalf("request %d is not on the raw path: %+v", i, r)
		}
		if len(r.Messages) != 0 {
			t.Fatalf("request %d kept %d messages", i, len(r.Messages))
		}
		if r.Prompt == "" {
			t.Fatalf("request %d has no prompt", i)
		}
		if _, ok := r.Body()["messages"]; ok {
			t.Fatalf("request %d would send messages: %+v", i, r.Body())
		}
	}
	if reqs[0].Prompt == reqs[1].Prompt {
		t.Fatal("two streams got the same prompt: they would share a prefix cache")
	}
}

// TestRoundRequestsCarryTheRunsSampling: a --prompts run must be measured
// under the same sampling as a single-prompt one, or the rounds of one tape
// would not be comparable with each other.
func TestRoundRequestsCarryTheRunsSampling(t *testing.T) {
	o := Options{
		Rounds: []Round{
			{Name: "prose", Prompts: []server.StreamRequest{{Messages: []tape.Message{{Role: "user", Content: "explain mmap"}}}}},
			{Name: "sql", Prompts: []server.StreamRequest{{Messages: []tape.Message{{Role: "user", Content: "write a query"}}}}},
		},
		Endpoint: tape.EndpointCompletion,
		Params:   map[string]any{"temperature": 0.0},
	}.normalize()

	rounds := roundRequests(o, 0)
	if len(rounds) != 2 {
		t.Fatalf("got %d rounds, want 2", len(rounds))
	}
	for k, reqs := range rounds {
		for _, r := range reqs {
			if !r.IsCompletion() || r.Prompt == "" || len(r.Messages) != 0 {
				t.Fatalf("round %d request not on the raw path: %+v", k, r)
			}
			if r.Params["temperature"] != 0.0 {
				t.Fatalf("round %d lost the temperature: %+v", k, r.Params)
			}
		}
	}
	if rounds[0][0].Prompt != "explain mmap" || rounds[1][0].Prompt != "write a query" {
		t.Fatalf("rounds got the wrong prompts: %q, %q", rounds[0][0].Prompt, rounds[1][0].Prompt)
	}
}

// TestMergeParamsReplacesNonObjectKwargs: a user who typed something that is
// not an object into chat_template_kwargs gets what they typed on the tape,
// not a merge we invented on top of it.
func TestMergeParamsReplacesNonObjectKwargs(t *testing.T) {
	r := server.StreamRequest{Params: map[string]any{"chat_template_kwargs": "not-an-object"}}
	mergeParams(&r, map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false}})
	kw, ok := r.Params["chat_template_kwargs"].(map[string]any)
	if !ok || kw["enable_thinking"] != false {
		t.Fatalf("kwargs %+v, want the object that was sent", r.Params["chat_template_kwargs"])
	}
}
