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
type streamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	IDSlot  *int   `json:"id_slot"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string  `json:"role"`
			Content          *string `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Timings        *ServerTimings `json:"timings"`
	PromptProgress *struct {
		Total     int     `json:"total"`
		Cache     int     `json:"cache"`
		Processed int     `json:"processed"`
		TimeMs    float64 `json:"time_ms"`
	} `json:"prompt_progress"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
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

	rec        tape.RequestRecord
	timings    ServerTimings
	sawTimings bool
	completion strings.Builder
	cached     int
	sawCached  bool
	errMsg     string
	done       bool
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
	var c streamChunk
	if err := json.Unmarshal([]byte(trimmed), &c); err != nil {
		return fmt.Errorf("server: decode stream chunk %q: %w", clip(trimmed, 160), err)
	}
	r.apply(&c, t)
	return nil
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
	for i := range c.Choices {
		ch := &c.Choices[i]
		if ch.FinishReason != nil && *ch.FinishReason != "" {
			r.rec.Prompt.FinishReason = *ch.FinishReason
		}
		// Reasoning text is generated but is not part of the answer, so it
		// stays out of Completion and out of Tokens (counting it would move
		// the decode window). It reaches the caller through OnReasoning.
		if rc := ch.Delta.ReasoningContent; rc != nil && *rc != "" {
			if r.hooks.OnReasoning != nil {
				r.hooks.OnReasoning(tape.TokenEvent{T: t, Index: -1, Text: *rc})
			}
		}
		// Lesson 1: only a delta that carried text is a token. The role-only
		// chunk, the empty content deltas and the finish chunk are not.
		if ct := ch.Delta.Content; ct != nil && *ct != "" {
			ev := tape.TokenEvent{T: t, Index: len(r.rec.Tokens), Text: *ct}
			if r.sawTimings {
				ev.PredictedN = r.timings.PredictedN
				ev.PredictedMs = r.timings.PredictedMs
			}
			r.rec.Tokens = append(r.rec.Tokens, ev)
			r.completion.WriteString(*ct)
			if r.hooks.OnToken != nil {
				r.hooks.OnToken(ev)
			}
		}
	}
}

// finish closes the record. sentAt and activeBytesPerToken are handed to
// Reduce. The returned error is non-nil when the stream carried a server
// error frame; the (partial) record is returned either way.
func (r *recorder) finish(sentAt time.Time, activeBytesPerToken int64) (*tape.RequestRecord, ServerTimings, error) {
	rec := &r.rec
	rec.Prompt.Completion = r.completion.String()
	rec.Timings = r.timings.Summary()
	if r.sawCached && rec.Timings.CacheN == 0 {
		// stream_options.include_usage reports the same figure as timings.cache_n
		// on builds that do not fill the latter.
		rec.Timings.CacheN = r.cached
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
	r := newRecorder(hooks)
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
			var c streamChunk
			if json.Unmarshal(ev, &c) == nil && c.Timings != nil {
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
