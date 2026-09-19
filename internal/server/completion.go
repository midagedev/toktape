package server

import (
	"encoding/json"
	"fmt"

	"github.com/midagedev/toktape/internal/tape"
)

// The raw completion path (TTP-55, 2026-09-14).
//
// Measured on DeepSeek-V4.1-Flash Q3_K_M with a DSpark draft, same twenty
// prompts and the same server: greedy /completion 25.6 tok/s median,
// server-default sampling on /completion 24.5, and toktape's own path —
// /v1/chat/completions with default sampling and thinking on — 22.4. The card
// could not show the engine's real number because the recorder had no way to
// send a prompt without a chat template around it.
//
// So the request grows one switch. `Endpoint: tape.EndpointCompletion` posts
// Prompt verbatim to /completion; everything downstream of the decoded chunk
// is shared with the chat path, because a rate that depends on which of our
// two code paths recorded it would be worth nothing.

// CompletionPath is llama-server's own streaming endpoint: no chat template,
// no messages, the prompt as the model will see it.
const CompletionPath = "/completion"

// IsCompletion reports whether this request goes to /completion.
func (r StreamRequest) IsCompletion() bool { return r.Endpoint == tape.EndpointCompletion }

// EndpointName is the tape.PromptRecord.Endpoint value this request records.
// An empty Endpoint is chat, the only path that existed before this field.
func (r StreamRequest) EndpointName() string {
	if r.IsCompletion() {
		return tape.EndpointCompletion
	}
	return tape.EndpointChat
}

// Path is the server route this request is posted to.
func (r StreamRequest) Path() string {
	if r.IsCompletion() {
		return CompletionPath
	}
	return ChatPath
}

// ThinkingSetting is what this request asks of a reasoning model's thinking,
// for tape.PromptRecord.Thinking: "off" when the body carries the engine's own
// switch turning it off, "" when it says nothing and the server decides.
//
// It reads the assembled body rather than a flag of its own so there is one
// source of truth for what went over the wire (lesson 4). A user who wrote the
// switch by hand with --param therefore gets the same record as one who typed
// --no-think, which is the honest answer: the tape describes the request, not
// the spelling that produced it.
func (r StreamRequest) ThinkingSetting() string {
	kwargs, ok := r.Body()["chat_template_kwargs"].(map[string]any)
	if !ok {
		return ""
	}
	if enabled, ok := kwargs["enable_thinking"].(bool); ok && !enabled {
		return "off"
	}
	return ""
}

// completionBody is the request body of the raw path.
//
// The fields are llama-server's own (tools/server/README.md): `prompt`,
// `n_predict` for the cap — not the chat path's max_tokens — `stream`, and the
// same `timings_per_token` and `return_progress` the chat path asks for, which
// is what makes the per-token timeline and the prefill progress recordable.
// `model` and `stream_options` are OpenAI-compatibility fields and are left
// off: this endpoint serves the one model it loaded and reports its cache in
// `timings`.
func (r StreamRequest) completionBody() map[string]any {
	body := map[string]any{
		"timings_per_token": true,
		"return_progress":   true,
	}
	if r.MaxTokens > 0 {
		body["n_predict"] = r.MaxTokens
	}
	for k, v := range r.Params {
		body[k] = v
	}
	body["prompt"] = r.Prompt
	body["stream"] = true
	return body
}

// RecordedParams is the body as it went over the wire minus the two fields
// that have their own places in the record: the prompt (Messages or
// RenderedPrompt) and the streaming switch. It is what lands in
// tape.PromptRecord.Params, so the card shows what was actually sent.
func (r StreamRequest) RecordedParams() map[string]any {
	params := r.Body()
	delete(params, "stream")
	delete(params, "messages")
	delete(params, "prompt")
	return params
}

// completionChunk is one decoded `data:` payload of a /completion stream.
//
// Every field is llama-server's own spelling (tools/server/README.md):
// `content` is "the next token as a string" in streaming mode, `stop` is the
// "boolean for use with stream to check whether the generation has stopped",
// `stop_type` is one of none / eos / limit / word, and `timings` and
// `prompt_progress` are the same objects the chat stream carries.
type completionChunk struct {
	Content  *string `json:"content"`
	Stop     bool    `json:"stop"`
	StopType string  `json:"stop_type"`
	// Truncated is llama-server's own flag, emitted on the final response
	// beside `stop_type`: the sequence ran out of context capacity during
	// generation (context shift off), or the shift path evicted early KV
	// cells and carried on. It is what splits `stop_type` "limit" into its
	// two meanings — the n_predict budget and the context running out — and
	// it travels to the record verbatim (tape.PromptRecord.Truncated).
	Truncated      bool           `json:"truncated"`
	IDSlot         *int           `json:"id_slot"`
	Timings        *ServerTimings `json:"timings"`
	PromptProgress *chunkProgress `json:"prompt_progress"`
	Error          *chunkError    `json:"error"`
}

// parseCompletionChunk decodes one /completion payload into the shape the
// recorder applies.
//
// The translation is deliberately total: after it, nothing downstream knows
// which endpoint produced the chunk, so token events, timings (draft_n
// included), progress and the whole reduction are the same function of the
// same bytes on both paths.
//
// stop_type is recorded verbatim in FinishReason rather than translated into
// the chat vocabulary ("eos" is not renamed to "stop", nor "limit" to
// "length"). The server's own word is the record; no renderer branches on this
// field, and inventing the other endpoint's spelling would put a word in the
// server's mouth. "none" is the chunk saying it has not stopped, which is not
// a finish reason and is dropped. `truncated` rides the same final response
// and is carried the same way — verbatim, beside the word it qualifies.
func parseCompletionChunk(trimmed string) (*streamChunk, error) {
	var c completionChunk
	if err := json.Unmarshal([]byte(trimmed), &c); err != nil {
		return nil, fmt.Errorf("server: decode completion chunk %q: %w", clip(trimmed, 160), err)
	}
	out := &streamChunk{
		IDSlot:         c.IDSlot,
		Timings:        c.Timings,
		PromptProgress: c.PromptProgress,
		Error:          c.Error,
		stop:           c.Stop,
		truncated:      c.Truncated,
	}
	var finish *string
	if c.StopType != "" && c.StopType != "none" {
		st := c.StopType
		finish = &st
	}
	// One choice, always: the raw endpoint generates a single sequence, and
	// giving it the chat path's shape is what lets apply() stay the only place
	// a token is born. A chunk with neither text nor a finish reason still
	// makes a choice-free chunk, exactly as a chat progress chunk does.
	if (c.Content != nil && *c.Content != "") || finish != nil {
		out.Choices = []chunkChoice{{
			Delta:        chunkDelta{Content: c.Content},
			FinishReason: finish,
		}}
	}
	return out, nil
}
