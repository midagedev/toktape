package main

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestNextStep: the one owner of "what to type next" after a run went wrong.
//
// The table is the first-run matrix's measured failures (2026-09-21): llama's
// context refusal, vLLM's 400 with the real numbers in it, a 429, an auth
// wall, and the mid-answer cut of a server that died. Each row checks the
// lever NAMES the setting the user's engine actually has — the audit's
// "advice naming a lever the user's server does not have is partial" rule —
// and the last row checks the other half: an error the table does not know
// gets no invented advice (""; the caller prints its own fallback).
func TestNextStep(t *testing.T) {
	llama := &tape.RunSummary{Server: tape.ServerInfo{Kind: tape.ServerLlamaCPP}}
	ik := &tape.RunSummary{Server: tape.ServerInfo{Kind: tape.ServerIKLlama}}
	vllm := &tape.RunSummary{Server: tape.ServerInfo{Kind: tape.ServerOpenAI, EngineClaim: "vLLM 0.11"}}
	ollama := &tape.RunSummary{Server: tape.ServerInfo{Kind: tape.ServerOpenAI, EngineClaim: "Ollama"}}
	// A server that spoke the OpenAI protocol and never said what engine it
	// is: the lever cannot assume a flag vocabulary, so it names all three.
	stranger := &tape.RunSummary{Server: tape.ServerInfo{Kind: tape.ServerOpenAI}}

	for _, tc := range []struct {
		name    string
		errText string
		s       *tape.RunSummary
		want    string
	}{
		{"llama's own refusal", "context_length_exceeded: the request exceeds the available context size", llama, "-c (which -np divides)"},
		{"ik_llama is a llama kind", "context_length_exceeded", ik, "-c (which -np divides)"},
		{"vLLM's 400, the engine claimed", `POST /v1/chat/completions: 400 Bad Request: {"error":{"message":"This model's maximum context length is 8192 tokens. However, you requested 15421 tokens (7421 in your messages, 8000 in the completion)"`, vllm, "--max-model-len"},
		{"an Ollama refusal, the engine claimed", "Requested tokens exceed context limit", ollama, "OLLAMA_CONTEXT_LENGTH"},
		{"a context refusal from an engine that did not say", "context overflow: the prompt plus the answer is larger than the model's context size", stranger, "--max-model-len on vLLM, OLLAMA_CONTEXT_LENGTH on Ollama, -c on llama-server"},
		// Before a tape exists there is no summary, so the engine is unknown
		// even on a llama-server: the all-three wording is the only honest one.
		{"no summary, so no engine", "recorder: all streams failed: server: stream error: context_length_exceeded", nil, "--max-model-len on vLLM, OLLAMA_CONTEXT_LENGTH on Ollama, -c on llama-server"},
		{"a rate limit", "429 Too Many Requests", llama, "fewer --sessions"},
		{"a rate limit in other words", "rate limit exceeded, retry later", nil, "fewer --sessions"},
		{"authentication refused", `HTTP 401: {"error":{"message":"Invalid API key"}}`, nil, "no flag for one yet"},
		{"forbidden", "HTTP 403 Forbidden", nil, "no flag for one yet"},
		{"credentials by name", "invalid credentials provided", nil, "no flag for one yet"},
		{"a connection cut mid-answer", "server: stream: read: unexpected EOF", llama, "may have crashed or been killed"},
		{"a reset connection", "read tcp 127.0.0.1:1->127.0.0.1:2: read: connection reset by peer", nil, "may have crashed or been killed"},
		{"a server that closed the connection", "the server closed the connection", nil, "may have crashed or been killed"},
		{"an error the table does not know", "something novel happened", llama, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := nextStep(tc.errText, tc.s)
			if tc.want == "" {
				if got != "" {
					t.Errorf("nextStep(%q) = %q, want no invented advice", tc.errText, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("nextStep(%q) = %q, want it to name %q", tc.errText, got, tc.want)
			}
		})
	}
}

// TestNextStepNamesNoLeverTheEngineLacks: the engine-specific levers must not
// leak into each other. A vLLM user told to "raise -c" and a llama-server user
// told to "raise --max-model-len" are both advice naming a flag their server
// does not have — the exact partial-verdict shape the matrix audit defined.
func TestNextStepNamesNoLeverTheEngineLacks(t *testing.T) {
	vllm := &tape.RunSummary{Server: tape.ServerInfo{Kind: tape.ServerOpenAI, EngineClaim: "vLLM 0.11"}}
	ollama := &tape.RunSummary{Server: tape.ServerInfo{Kind: tape.ServerOpenAI, EngineClaim: "Ollama"}}
	llama := &tape.RunSummary{Server: tape.ServerInfo{Kind: tape.ServerLlamaCPP}}
	const refusal = "This model's maximum context length is 8192 tokens. However, you requested 15421 tokens"

	for _, tc := range []struct {
		name    string
		s       *tape.RunSummary
		notWant string
	}{
		{"vLLM is not told llama flags", vllm, "-c (which"},
		{"Ollama is not told vLLM's flag", ollama, "--max-model-len"},
		{"llama-server is not told vLLM's flag", llama, "--max-model-len"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextStep(refusal, tc.s); strings.Contains(got, tc.notWant) {
				t.Errorf("nextStep on a %s names %q:\n%s", tc.name, tc.notWant, got)
			}
		})
	}
}

// Lead, 2026-09-21: substrings are not status codes.
func TestNextStepDoesNotMatchInsideWords(t *testing.T) {
	for _, e := range []string{"failed to generate a reply", "prompt has 14012 tokens", "error 4290"} {
		if got := nextStep(e, nil); got != "" {
			t.Errorf("nextStep(%q) = %q, want no advice", e, got)
		}
	}
	if nextStep("HTTP 429 Too Many Requests", nil) == "" || nextStep("status 401", nil) == "" {
		t.Error("a real status code lost its lever")
	}
}
