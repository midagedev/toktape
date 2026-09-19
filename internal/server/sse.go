package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// ServerTimings is the server's own `timings` object, carried by every chunk
// when the request asked for timings_per_token and by the final chunk always.
// These are the record; client-side figures are the check (see Reduce).
type ServerTimings struct {
	PromptN            int     `json:"prompt_n"`
	PromptMs           float64 `json:"prompt_ms"`
	PromptPerSecond    float64 `json:"prompt_per_second"`
	PredictedN         int     `json:"predicted_n"`
	PredictedMs        float64 `json:"predicted_ms"`
	PredictedPerSecond float64 `json:"predicted_per_second"`
	CacheN             int     `json:"cache_n"`
	// Speculative decoding. nil when the build does not report it.
	DraftN         *int `json:"draft_n,omitempty"`
	DraftNAccepted *int `json:"draft_n_accepted,omitempty"`
}

// Summary copies the server figures into a tape.TimingsSummary. The
// client-side fields are left zero; Reduce fills them.
func (s ServerTimings) Summary() tape.TimingsSummary {
	return tape.TimingsSummary{
		PromptN:            s.PromptN,
		CacheN:             s.CacheN,
		PromptMs:           s.PromptMs,
		PromptPerSecond:    s.PromptPerSecond,
		PredictedN:         s.PredictedN,
		PredictedMs:        s.PredictedMs,
		PredictedPerSecond: s.PredictedPerSecond,
		DraftN:             s.DraftN,
		DraftNAccepted:     s.DraftNAccepted,
	}
}

// streamChunk is one decoded `data:` payload of a chat-completion stream.
// Every field is optional: a chunk can be prefill progress with no choices at
// all, a role-only delta, a content delta, a reasoning delta, the finish
// chunk, a usage-only chunk, or an error frame.
//
// Its parts are named types rather than anonymous structs (TTP-55,
// 2026-09-14) so the raw /completion path can translate its own chunk shape
// into this one and hand it to the same recorder: the reduction must not
// depend on which endpoint fed it, and the cheapest way to guarantee that is
// to leave exactly one apply().
type streamChunk struct {
	ID             string         `json:"id"`
	Model          string         `json:"model"`
	IDSlot         *int           `json:"id_slot"`
	Choices        []chunkChoice  `json:"choices"`
	Timings        *ServerTimings `json:"timings"`
	PromptProgress *chunkProgress `json:"prompt_progress"`
	Usage          *chunkUsage    `json:"usage"`
	Error          *chunkError    `json:"error"`

	// stop is not a wire field of the chat stream: it is how the /completion
	// translator reports that chunk's `"stop": true`, which is where that
	// endpoint ends its stream instead of sending the chat path's [DONE].
	stop bool `json:"-"`
	// truncated is not a wire field of the chat stream either, and for the
	// same reason: it is how the /completion translator reports that
	// endpoint's own `truncated` key, which upstream emits on the final
	// response beside `stop_type` — the pair is what tells the token cap
	// from the context running out. The chat path has no such field (its
	// vocabulary collapses every limit into "length"), so it sets nothing
	// here and PromptRecord.Truncated stays false because nothing said
	// otherwise, never because something did.
	truncated bool `json:"-"`
}

// chunkChoice is one entry of a chat chunk's choices array.
type chunkChoice struct {
	Index        int        `json:"index"`
	Delta        chunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

// chunkDelta is the incremental text of one choice. A nil pointer and an
// empty string are both "no text": lesson 1 counts only a delta that carried
// text as a token.
type chunkDelta struct {
	Role             string  `json:"role"`
	Content          *string `json:"content"`
	ReasoningContent *string `json:"reasoning_content"`
}

// chunkProgress is the return_progress object of a prefill chunk.
type chunkProgress struct {
	Total     int     `json:"total"`
	Cache     int     `json:"cache"`
	Processed int     `json:"processed"`
	TimeMs    float64 `json:"time_ms"`
}

// chunkUsage is the stream_options.include_usage object of the final chunk.
type chunkUsage struct {
	PromptTokens        int               `json:"prompt_tokens"`
	CompletionTokens    int               `json:"completion_tokens"`
	PromptTokensDetails *chunkUsageCached `json:"prompt_tokens_details"`
}

// chunkUsageCached is the cached-prompt half of the usage object.
type chunkUsageCached struct {
	CachedTokens int `json:"cached_tokens"`
}

// chunkError is a server error frame delivered inside the stream.
type chunkError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Type    string `json:"type"`
}

// sseScanner is an incremental Server-Sent Events decoder. It is a pure
// function of the bytes fed to it: the caller supplies chunks of any size
// (including splits in the middle of a line) and gets back whole event
// payloads in order. Only `data:` fields are kept; comments and the other
// SSE fields are ignored, and CRLF is accepted alongside LF.
type sseScanner struct {
	buf     []byte
	pending []byte
	hasData bool
}

// feed appends p and returns every event completed by it.
func (s *sseScanner) feed(p []byte) [][]byte {
	s.buf = append(s.buf, p...)
	var out [][]byte
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			return out
		}
		line := s.buf[:i]
		s.buf = s.buf[i+1:]
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		if ev, ok := s.line(line); ok {
			out = append(out, ev)
		}
	}
}

// close flushes an event that was not terminated by a blank line, which is
// what a server that drops the connection right after the last chunk leaves
// behind.
func (s *sseScanner) close() [][]byte {
	var out [][]byte
	if len(s.buf) > 0 {
		line := s.buf
		s.buf = nil
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		if ev, ok := s.line(line); ok {
			out = append(out, ev)
		}
	}
	if s.hasData {
		out = append(out, s.take())
	}
	return out
}

// line consumes one complete SSE line and reports an event when the line
// terminated one.
func (s *sseScanner) line(line []byte) ([]byte, bool) {
	if len(line) == 0 {
		if s.hasData {
			return s.take(), true
		}
		return nil, false
	}
	if line[0] == ':' { // comment / keep-alive
		return nil, false
	}
	name, value := splitField(line)
	if name != "data" {
		return nil, false
	}
	if s.hasData {
		s.pending = append(s.pending, '\n')
	}
	s.pending = append(s.pending, value...)
	s.hasData = true
	return nil, false
}

func (s *sseScanner) take() []byte {
	ev := s.pending
	s.pending = nil
	s.hasData = false
	return ev
}

// splitField splits an SSE line into its field name and value, dropping the
// single optional space after the colon.
func splitField(line []byte) (string, []byte) {
	i := bytes.IndexByte(line, ':')
	if i < 0 {
		return string(line), nil
	}
	value := line[i+1:]
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	return string(line[:i]), value
}

// doneMarker ends an OpenAI-compatible stream.
const doneMarker = "[DONE]"

// recorder turns SSE event payloads plus their arrival times into a
// tape.RequestRecord. It holds no I/O: Stream drives it from a live body and
// ReplayStream drives it from recorded bytes, so both produce the same record.
type recorder struct {
	hooks StreamHooks
	// rawPath selects the chunk decoder: false for the OpenAI-shaped chat
	// stream, true for llama-server's own /completion stream (TTP-55). It
	// changes nothing after the chunk is decoded — apply, addToken and finish
	// see one shape — which is what makes the two endpoints reduce alike.
	rawPath bool

	rec        tape.RequestRecord
	timings    ServerTimings
	sawTimings bool
	completion strings.Builder
	// reasoning is the thinking text, kept apart from the answer even though
	// its tokens are in rec.Tokens alongside the answer's.
	reasoning  strings.Builder
	reasoningN int
	cached     int
	sawCached  bool
	// usage is the final chunk's stream_options.include_usage object, when
	// the server sent one. It is the token count on a server that reports no
	// timings (TTP-99): usage.completion_tokens becomes PredictedN with
	// PredictedNSource "usage", and its absence means chunks are counted
	// instead — never tokens.
	usage  *chunkUsage
	errMsg string
	done   bool
	// last is the arrival of the most recent event, used to synthesise a time
	// for events that carry no timings of their own.
	last time.Duration
}

func newRecorder(hooks StreamHooks) *recorder {
	r := &recorder{hooks: hooks}
	r.rec.Slot = -1 // "unknown slot" per tape.RequestRecord
	return r
}

// feed consumes one event payload that arrived at t (since the request was
// sent). It returns an error only for a malformed payload; a server error
// frame is recorded and reported by finish.
func (r *recorder) feed(data []byte, t time.Duration) error {
	r.last = t
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	if trimmed == doneMarker {
		r.done = true
		return nil
	}
	c, err := r.parse(trimmed)
	if err != nil {
		return err
	}
	r.apply(c, t)
	return nil
}

// parse decodes one payload into the common chunk shape.
func (r *recorder) parse(trimmed string) (*streamChunk, error) {
	if r.rawPath {
		return parseCompletionChunk(trimmed)
	}
	var c streamChunk
	if err := json.Unmarshal([]byte(trimmed), &c); err != nil {
		return nil, fmt.Errorf("server: decode stream chunk %q: %w", clip(trimmed, 160), err)
	}
	return &c, nil
}

func (r *recorder) apply(c *streamChunk, t time.Duration) {
	if c.Error != nil {
		msg := c.Error.Message
		if msg == "" {
			msg = c.Error.Type
		}
		if c.Error.Code != 0 {
			msg = fmt.Sprintf("%s (code %d)", msg, c.Error.Code)
		}
		r.errMsg = msg
		return
	}
	if c.stop {
		// The /completion stream's own end marker. The chunk that carries it
		// also carries the final timings, so it is applied like any other and
		// only the loop is told to stop.
		r.done = true
	}
	if c.IDSlot != nil && *c.IDSlot >= 0 {
		r.rec.Slot = *c.IDSlot
	}
	if c.Timings != nil {
		r.timings = *c.Timings
		r.sawTimings = true
	}
	if p := c.PromptProgress; p != nil {
		ev := tape.PromptProgress{T: t, Total: p.Total, Cache: p.Cache, Processed: p.Processed, TimeMs: p.TimeMs}
		r.rec.Progress = append(r.rec.Progress, ev)
		if r.hooks.OnProgress != nil {
			r.hooks.OnProgress(ev)
		}
	}
	if u := c.Usage; u != nil && u.PromptTokensDetails != nil {
		r.cached = u.PromptTokensDetails.CachedTokens
		r.sawCached = true
	}
	// The last usage object wins: usage arrives on the final chunk, and a
	// repeated one can only be the server restating it. A negative
	// completion count is kept and refused at finish — it is a malformed
	// figure, not a count, and finish is the one place that decides what
	// PredictedN becomes.
	if c.Usage != nil {
		u := *c.Usage
		r.usage = &u
	}
	// The truncation flag lands beside the finish word it rides with: the
	// raw endpoint emits `truncated` adjacent to `stop_type` on its final
	// response, and the pair is what separates "limit" the token cap from
	// "limit" the context running out (tape.PromptRecord.Truncated). Only
	// true is news — truncation is a fact about what happened, not a state
	// a later chunk can take back, and false is the zero value that means
	// nothing said otherwise.
	if c.truncated {
		r.rec.Prompt.Truncated = true
	}
	for i := range c.Choices {
		ch := &c.Choices[i]
		if ch.FinishReason != nil && *ch.FinishReason != "" {
			r.rec.Prompt.FinishReason = *ch.FinishReason
		}
		// A reasoning ("thinking") delta is a decode token: the server counts
		// it in predicted_n exactly like an answer token, so TTFT and every
		// rate must count it too (TTP-20, 2026-09-13). Only the transcript
		// keeps the two apart — the text goes to Prompt.Reasoning, never to
		// Prompt.Completion, and the event carries Reasoning: true so a
		// renderer can style it differently.
		if rc := ch.Delta.ReasoningContent; rc != nil && *rc != "" {
			ev := r.addToken(t, *rc, true)
			r.reasoning.WriteString(*rc)
			// OnReasoning is the transcript hook and still fires, so a caller
			// that wants only the thinking text does not have to filter
			// OnToken. addToken has already fired OnToken for this event.
			if r.hooks.OnReasoning != nil {
				r.hooks.OnReasoning(ev)
			}
		}
		// Lesson 1: only a delta that carried text is a token. The role-only
		// chunk, the empty content deltas and the finish chunk are not.
		if ct := ch.Delta.Content; ct != nil && *ct != "" {
			r.addToken(t, *ct, false)
			r.completion.WriteString(*ct)
		}
	}
}

// addToken appends one generated token to the record and hands it to OnToken.
//
// Reasoning and answer tokens share one Index sequence, because they share the
// server's predicted_n sequence: they are the same decode steps. Every token
// the record keeps fires OnToken, which is what lets the process recorder
// stitch its per-token major-fault readings back onto rec.Tokens by arrival
// order (internal/recorder state.applyDeltas). A token that skipped the hook
// would shift every later reading onto the wrong token.
func (r *recorder) addToken(t time.Duration, text string, reasoning bool) tape.TokenEvent {
	ev := tape.TokenEvent{T: t, Index: len(r.rec.Tokens), Text: text, Reasoning: reasoning}
	if r.sawTimings {
		ev.PredictedN = r.timings.PredictedN
		ev.PredictedMs = r.timings.PredictedMs
	}
	r.rec.Tokens = append(r.rec.Tokens, ev)
	if reasoning {
		r.reasoningN++
	}
	if r.hooks.OnToken != nil {
		r.hooks.OnToken(ev)
	}
	return ev
}

// finish closes the record. sentAt and activeBytesPerToken are handed to
// Reduce. The returned error is non-nil when the stream carried a server
// error frame; the (partial) record is returned either way.
func (r *recorder) finish(sentAt time.Time, activeBytesPerToken int64) (*tape.RequestRecord, ServerTimings, error) {
	rec := &r.rec
	rec.Prompt.Completion = r.completion.String()
	rec.Prompt.Reasoning = r.reasoning.String()
	rec.Prompt.ReasoningN = r.reasoningN
	rec.Timings = r.timings.Summary()
	if r.sawCached && rec.Timings.CacheN == 0 {
		// stream_options.include_usage reports the same figure as timings.cache_n
		// on builds that do not fill the latter.
		rec.Timings.CacheN = r.cached
	}
	// No timings object arrived: the server does not report its own figures,
	// so the recorder's clock is the record (TTP-99). Source "client" says
	// so, and Reduce — which branches on exactly this — derives the rates
	// from the client timeline instead of falling back silently. The token
	// count is the final chunk's usage.completion_tokens ("usage"); without
	// it the chunk count is kept with PredictedNSource "chunks", and lesson
	// 1 holds: chunks are not tokens, so no rate is derived from them.
	// A zero or negative completion count is no usage figure at all.
	if !r.sawTimings {
		rec.Timings.Source = "client"
		if r.usage != nil && r.usage.CompletionTokens > 0 {
			rec.Timings.PredictedN = r.usage.CompletionTokens
			rec.Timings.PredictedNSource = "usage"
		} else {
			rec.Timings.PredictedN = len(rec.Tokens)
			rec.Timings.PredictedNSource = "chunks"
		}
	}
	rec.Timings = Reduce(rec, sentAt, activeBytesPerToken)
	rec.Cache = CacheVerdict(rec.Timings, 0, rec.Timings.PredictedN)
	if r.errMsg != "" {
		rec.Error = r.errMsg
		return rec, r.timings, fmt.Errorf("server: stream error: %s", r.errMsg)
	}
	if !r.done && rec.Prompt.FinishReason == "" {
		// The connection ended mid-generation. The partial record is still
		// worth keeping, but it must not be reported as a completed run.
		rec.Error = "stream ended without a finish chunk"
		return rec, r.timings, fmt.Errorf("server: %s (%d tokens received)", rec.Error, len(rec.Tokens))
	}
	return rec, r.timings, nil
}

// ReplayStream is the pure core of Stream: it turns recorded SSE bytes into a
// tape.RequestRecord without any I/O, so every parsing and timing rule is
// testable against a fixture.
//
// arrivals holds the time each `data:` event arrived, relative to the moment
// the request was sent, one entry per event in order. When arrivals is nil the
// times are synthesised from each chunk's own timings (prompt_ms +
// predicted_ms), which is what a recorded stream without a client clock
// allows; note that those times make the client-side rate an identity of the
// server figures rather than an independent check.
//
// The returned record is complete: Timings carries both the server figures and
// the reduced client-side ones, and Cache carries the verdict with zero major
// faults (the recorder re-runs CacheVerdict once it has the fault counters).
func ReplayStream(sse []byte, arrivals []time.Duration, hooks StreamHooks) (*tape.RequestRecord, ServerTimings, error) {
	return replay(sse, arrivals, hooks, false)
}

// ReplayCompletionStream is ReplayStream for the raw /completion stream
// (TTP-55, 2026-09-14): the same pure core over llama-server's own chunk
// shape, so the rule that the two endpoints reduce alike is testable against
// two fixtures and no server.
//
// Like ReplayStream it leaves Prompt.Endpoint and Prompt.Thinking empty.
// Those describe the request, and a replay has only the answer's bytes; Stream
// fills them from the StreamRequest that was actually sent. Leaving them here
// is also what lets a test assert that two fixtures of the same generation
// reduce to byte-identical records.
func ReplayCompletionStream(sse []byte, arrivals []time.Duration, hooks StreamHooks) (*tape.RequestRecord, ServerTimings, error) {
	return replay(sse, arrivals, hooks, true)
}

// replay is the body both of them share; completion selects the decoder.
func replay(sse []byte, arrivals []time.Duration, hooks StreamHooks, rawPath bool) (*tape.RequestRecord, ServerTimings, error) {
	r := newRecorder(hooks)
	r.rawPath = rawPath
	var sc sseScanner
	events := sc.feed(sse)
	events = append(events, sc.close()...)
	for i, ev := range events {
		var at time.Duration
		switch {
		case arrivals != nil:
			if i >= len(arrivals) {
				return nil, ServerTimings{}, fmt.Errorf("server: replay: %d arrival times for %d events", len(arrivals), len(events))
			}
			at = arrivals[i]
		default:
			at = r.last
			// The same decoder the record will use, so a /completion chunk's
			// timings are found where that endpoint puts them.
			if c, err := r.parse(strings.TrimSpace(string(ev))); err == nil && c.Timings != nil {
				at = time.Duration((c.Timings.PromptMs + c.Timings.PredictedMs) * float64(time.Millisecond))
			}
		}
		if err := r.feed(ev, at); err != nil {
			return nil, ServerTimings{}, err
		}
	}
	return r.finish(time.Time{}, 0)
}

// ParseArrivals reads a sidecar arrival file: one millisecond offset per SSE
// event, blank lines and `#` comments ignored.
func ParseArrivals(b []byte) ([]time.Duration, error) {
	var out []time.Duration
	for i, ln := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(ln)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		ms, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("server: arrivals line %d: %w", i+1, err)
		}
		out = append(out, time.Duration(ms*float64(time.Millisecond)))
	}
	return out, nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
