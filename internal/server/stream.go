package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// ChatPath is the streaming endpoint.
const ChatPath = "/v1/chat/completions"

// StreamRequest is one chat request to record.
type StreamRequest struct {
	// Messages is the conversation as sent. Recorded verbatim in
	// tape.PromptRecord.Messages.
	Messages []tape.Message
	// Model names the model when the server hosts more than one. Empty means
	// "whatever is loaded".
	Model string
	// MaxTokens caps the answer (max_tokens). 0 leaves it to the server.
	MaxTokens int
	// Params are merged into the request body verbatim: temperature, seed,
	// reasoning_effort, chat_template_kwargs, and anything else the build
	// honours. They are recorded in tape.PromptRecord.Params, which is what
	// lesson 4 asks for: the card shows what was actually sent. Params may
	// override the defaults below except "messages" and "stream".
	Params map[string]any
	// ActiveBytesPerToken is tape.ModelInfo.ActiveBytesPerToken, used only to
	// fill EffectiveBandwidthBytesPerSec. 0 leaves that field 0.
	ActiveBytesPerToken int64
	// RenderedPrompt, when the caller already fetched it from ApplyTemplate,
	// is copied into the record.
	RenderedPrompt string
	// Endpoint chooses the server path (TTP-55, 2026-09-14): "" or
	// tape.EndpointChat sends Messages through ChatPath and lets the server
	// apply its chat template; tape.EndpointCompletion sends Prompt verbatim
	// to CompletionPath, with no template and no messages at all.
	Endpoint string
	// Prompt is the raw prompt of a completion request, sent as the model
	// will see it. It is ignored on the chat path, where Messages are the
	// request and the template decides what the model sees.
	Prompt string
	// Set names the published prompt set this request came from
	// (server.PromptSetID), and is empty for a prompt the user supplied. It
	// never reaches the wire — Body assembles the request field by field —
	// because it says nothing about what to generate. It says what the run
	// can be compared against, which is the tape's question, and the tape
	// carries it as RunSummary.PromptSet (TTP-112).
	//
	// The set stamps its own requests rather than the recorder inferring "no
	// --prompts was given, so it must have been the built-in set": an
	// inference like that is right until the day another caller builds the
	// requests, and then it is silently wrong on a field search groups by.
	Set string
	// Protocol is the server this request is shaped for. "" (or any kind
	// that speaks the llama protocol) builds today's body; tape.ServerOpenAI
	// builds the generic OpenAI-compatible one (TTP-99): only model,
	// messages, stream, stream_options.include_usage, max_tokens when set,
	// and Params. timings_per_token and return_progress are left off —
	// strict vLLM builds reject unknown top-level fields with 400, and
	// neither field is worth that risk where no timings object exists to
	// fill anyway.
	Protocol tape.ServerKind
}

// SentMaxTokens is the generation cap this request will actually carry.
//
// It reads the assembled body rather than the MaxTokens field because Params
// is merged over the defaults: a caller that put llama.cpp's own "n_predict"
// there, or that overrode "max_tokens", changed the cap, and what the tape
// records has to be what went over the wire (lesson 4). A negative value is
// llama-server's "no limit", which is not a cap and comes back as 0.
func (r StreamRequest) SentMaxTokens() int {
	body := r.Body()
	for _, key := range []string{"max_tokens", "n_predict"} {
		if n, ok := bodyInt(body[key]); ok && n > 0 {
			return n
		}
	}
	return 0
}

// bodyInt reads a request-body value that should be a whole number. Params
// comes from a caller, so the same figure can arrive as any of Go's numeric
// types.
func bodyInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float32:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

// StreamHooks receive events as they arrive, not after the stream ends.
//
// Every hook runs synchronously on the goroutine reading the stream, at the
// instant the event arrived, so that a hook can sample a host counter that
// belongs to that instant: the process recorder reads /proc major faults in
// OnToken, and buffering the tokens would turn the sparkline into a flat
// average. A slow hook therefore delays the read loop and skews the timings.
//
// Under RunConcurrent the hooks of different streams run on different
// goroutines at the same time, so anything they share must be synchronised.
// Nil fields are skipped.
type StreamHooks struct {
	// OnToken fires for each token that carried text, in order — every token
	// the record keeps, a thinking model's reasoning tokens included. The
	// process recorder pairs its per-token readings with
	// tape.RequestRecord.Tokens by arrival order, so a token that skipped this
	// hook would shift every later reading onto the wrong token.
	// tape.TokenEvent.Reasoning says which kind arrived.
	OnToken func(tape.TokenEvent)
	// OnProgress fires for each return_progress event during prefill.
	OnProgress func(tape.PromptProgress)
	// OnReasoning fires for each reasoning ("thinking") delta, after OnToken
	// has already fired for the same event. It is the transcript hook: a
	// caller that wants only the thinking text does not have to filter
	// OnToken for it. The event it receives is the one in Tokens, so its
	// Index is that token's place in the shared sequence, not -1.
	OnReasoning func(tape.TokenEvent)
	// OnEnd fires exactly once per stream, after it has stopped for any
	// reason at all: EOS, the token cap, a server error, a dropped socket or
	// a cancelled context. RunConcurrent fires it, not Stream, so a request
	// that failed before a single chunk arrived still ends (TTP-76,
	// 2026-09-14).
	//
	// It exists because a caller that has to decide something about the
	// streams still live — the recorder's wall-clock budget, which will not
	// cut a stream under tape.MinCutTokens — otherwise cannot tell "this
	// stream is slow" from "this stream is finished". Counting tokens is not
	// enough: a stream that stopped at EOS with ten tokens would hold such a
	// floor open forever.
	OnEnd func()
}

// Body builds the JSON request body. timings_per_token, return_progress and
// stream_options.include_usage are always asked for: they are what makes the
// per-token timeline, the prefill progress and the prompt-cache hit
// recordable, and a server that does not know a field ignores it.
func (r StreamRequest) Body() map[string]any {
	if r.IsCompletion() {
		return r.completionBody()
	}
	if r.Protocol == tape.ServerOpenAI {
		return r.openaiBody()
	}
	body := map[string]any{
		"timings_per_token": true,
		"return_progress":   true,
		"stream_options":    map[string]any{"include_usage": true},
	}
	if r.Model != "" {
		body["model"] = r.Model
	}
	if r.MaxTokens > 0 {
		body["max_tokens"] = r.MaxTokens
	}
	for k, v := range r.Params {
		body[k] = v
	}
	body["messages"] = r.Messages
	body["stream"] = true
	return body
}

// openaiBody is the request body for a generic OpenAI-compatible server
// (TTP-99): model, messages, stream, stream_options.include_usage, max_tokens
// when set, and Params — and nothing llama-shaped. model is required by these
// servers; the recorder defaults it to the first /v1/models id when the user
// gave none, so an empty Model here means a server that listed nothing.
func (r StreamRequest) openaiBody() map[string]any {
	body := map[string]any{
		"stream_options": map[string]any{"include_usage": true},
	}
	if r.Model != "" {
		body["model"] = r.Model
	}
	if r.MaxTokens > 0 {
		body["max_tokens"] = r.MaxTokens
	}
	for k, v := range r.Params {
		body[k] = v
	}
	body["messages"] = r.Messages
	body["stream"] = true
	return body
}

// Stream sends one chat request and records the whole stream.
//
// It returns the record, the server's final timings object, and an error. The
// record is non-nil whenever the server answered at all, even when the error
// is non-nil: a stream that died after 200 tokens is still evidence, and
// RunConcurrent stores it with rec.Error set. The record is complete on
// return, Reduce and CacheVerdict already applied; a caller holding major
// fault counters re-runs CacheVerdict with them.
//
// The only deadline is ctx. Generation takes as long as it takes, so the
// control-call timeout deliberately does not apply here.
func (c *Client) Stream(ctx context.Context, req StreamRequest, hooks StreamHooks) (*tape.RequestRecord, ServerTimings, error) {
	path := req.Path()
	body, err := json.Marshal(req.Body())
	if err != nil {
		return nil, ServerTimings{}, fmt.Errorf("server: stream: encode body: %w", err)
	}
	httpReq, err := c.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return nil, ServerTimings{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	sentAt := time.Now()
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, ServerTimings{}, fmt.Errorf("server: POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, ServerTimings{}, fmt.Errorf("server: POST %s: %s: %s", path, resp.Status, clip(strings.TrimSpace(string(msg)), 200))
	}

	rec := newRecorder(hooks)
	rec.rawPath = req.IsCompletion()
	// Which path recorded the rate, and what was asked of the model's
	// thinking (TTP-55). Chat says so explicitly from now on; "" is only ever
	// a tape older than the field.
	rec.rec.Prompt.Endpoint = req.EndpointName()
	rec.rec.Prompt.Thinking = req.ThinkingSetting()
	if req.IsCompletion() {
		// There is no template on this path and there are no messages: the
		// prompt as sent is the prompt as the model saw it, which is exactly
		// what RenderedPrompt means.
		rec.rec.Prompt.RenderedPrompt = req.Prompt
	} else {
		rec.rec.Prompt.Messages = req.Messages
		rec.rec.Prompt.RenderedPrompt = req.RenderedPrompt
	}
	// Record the parameters as they went over the wire, not as the caller
	// typed them: max_tokens and the streaming switches are part of what was
	// measured (lesson 4). The prompt and stream live in their own fields.
	rec.rec.Prompt.Params = req.RecordedParams()
	// The cap gets a field of its own as well as its place in Params: every
	// renderer asks "how far through its budget is this stream", and reading
	// that out of a free-form parameter map means each one re-implements
	// which key llama-server honoured.
	rec.rec.Prompt.MaxTokens = req.SentMaxTokens()

	var sc sseScanner
	buf := make([]byte, 16<<10)
	var readErr error
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			// One arrival time for every event in this read: they were all
			// delivered by the same packet, so they share an instant.
			at := time.Since(sentAt)
			for _, ev := range sc.feed(buf[:n]) {
				if ferr := rec.feed(ev, at); ferr != nil {
					readErr = ferr
					break
				}
			}
		}
		if readErr != nil {
			break
		}
		if err != nil {
			if err != io.EOF {
				readErr = fmt.Errorf("server: stream: read: %w", err)
			}
			break
		}
		if rec.done {
			break
		}
	}
	if readErr == nil {
		at := time.Since(sentAt)
		for _, ev := range sc.close() {
			if ferr := rec.feed(ev, at); ferr != nil {
				readErr = ferr
				break
			}
		}
	}

	out, timings, err := rec.finish(sentAt, req.ActiveBytesPerToken)
	if readErr != nil {
		// The read failure is the cause and must win: finish only sees that
		// the stream stopped early and would otherwise report "no finish
		// chunk" for what was really a cancelled context or a dropped socket.
		out.Error = readErr.Error()
		return out, timings, readErr
	}
	return out, timings, err
}
