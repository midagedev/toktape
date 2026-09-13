package recorder

import (
	"errors"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// ErrUnreachable wraps every failure to reach the server: discovery found
// nothing, /props did not answer, or a server that was loading never became
// ready. The CLI maps it to exit code 2.
//
// The sentence is short because the error it wraps carries the detail: the
// server package already names the state, the URL and the cause, and a
// message that says "unreachable" twice is one a user has to read twice.
var ErrUnreachable = errors.New("recorder: cannot attach")

// Waiting for a server that is not ready yet.
//
// Measured 2026-09-13 on a real box: llama-server opened its TCP port and then
// answered nothing on /props for minutes while it lazily loaded a 450 GB
// model, and toktape exited "unreachable" after ten seconds. The first run is
// the one that decides whether anybody uses this tool, so the default is to
// wait a long time and say so, not to fail fast and be wrong.
const (
	// DefaultWaitForModel is how long a run keeps polling a server that is
	// loading. It is generous because the alternative — giving up on a
	// correct setup — is the worse mistake.
	DefaultWaitForModel = 10 * time.Minute
	// DefaultLoadingPoll is the gap between /props polls while waiting.
	DefaultLoadingPoll = 2 * time.Second
	// NoWait is the Options.WaitForModel value that means "fail fast"; it is
	// what the CLI's `--wait 0` sets. Zero cannot mean it, because zero is
	// the zero value and the zero value must be the sane default.
	NoWait = -1 * time.Nanosecond
)

// Reasons carried in Event.Message for EventLoading. The CLI prints a
// different verb for each: a model being read off disk, a server that has not
// opened its port yet, and a server that is serving someone else.
const (
	// ReasonLoading: a server answered and said it is not ready, or accepted
	// the connection and never answered.
	ReasonLoading = "loading"
	// ReasonStarting: nothing is listening there yet.
	ReasonStarting = "starting"
	// ReasonBusy: /props did not answer while /health did, which is a server
	// with the model resident and another request running — ik_llama.cpp
	// holds /props until a completion finishes (TTP-33, measured 2026-09-13).
	ReasonBusy = "busy"
)

// ErrAllStreamsFailed is returned when no stream produced a usable record.
// The CLI maps it to exit code 3. Partial failure is not an error: a run in
// which three of four streams answered is still evidence, and the failures
// are recorded in the records' Error fields and in AggregateTimings.
var ErrAllStreamsFailed = errors.New("recorder: all streams failed")

// Clock supplies the run's wall-clock stamps. Only Now is needed; the
// recorder does not sleep on it. Tests pass a fixed clock to pin the run ID.
type Clock interface {
	Now() time.Time
}

// systemClock is the real clock.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Options configures one run. The zero value is valid: it discovers the
// server, sends one default prompt and writes to the live /proc.
type Options struct {
	// BaseURL is the server to attach to. Empty means discover it.
	BaseURL string
	// Prompts are the requests to send. Empty means
	// server.DefaultPrompts(Concurrency). When fewer prompts than
	// Concurrency are given they are cycled; when more are given and
	// Concurrency is 0, Concurrency becomes len(Prompts).
	Prompts []server.StreamRequest
	// Rounds, when non-empty, replaces Prompts with a sequence of prompt
	// rounds (TTP-31): each round sends Concurrency streams of its own
	// prompts, the rounds run strictly one after another, and all of them
	// land in one tape. Empty means exactly one round of Prompts.
	Rounds []Round
	// SpecNMax, when non-empty, runs the prompt set once per value with
	// "speculative.n_max" set on every request (TTP-35): Rounds, or the one
	// round of Prompts, are repeated per value in order, and a server whose
	// argv was read and names no draft model runs only the first value.
	SpecNMax []int
	// Concurrency is the number of streams sent at once. 0 or less means 1,
	// or len(Prompts) when prompts were supplied, or the longest round's
	// prompt count when Rounds were.
	Concurrency int
	// MaxTokens caps every stream's answer (the server's max_tokens /
	// n_predict). 0 leaves whatever the prompt itself carries.
	MaxTokens int
	// SampleInterval is how often a tape.RunSample is taken. 0 means
	// tape.DefaultSampleInterval.
	SampleInterval time.Duration
	// Progress, when non-nil, receives an Event for each step of the run.
	// The recorder serialises the calls, so an implementation needs no lock
	// of its own — but it runs on the stream goroutines and must not block:
	// a slow callback delays the read loop and skews the timings.
	Progress func(Event)
	// FSRoot is the directory that contains "proc". "" means "/". Tests
	// point it at a fixture tree.
	FSRoot string
	// GPU is the device collector. nil means gpu.Open. A collector supplied
	// here is owned by the caller and is not closed by Record.
	GPU gpu.Collector
	// Clock supplies StartedAt and FinishedAt. nil means the system clock.
	Clock Clock
	// Version is written into tape.RunSummary.ToktapeVersion. The CLI sets
	// it from its build-time version string.
	Version string
	// Candidates are the base URLs discovery probes when BaseURL is empty.
	// nil means server.DefaultCandidates.
	Candidates []string
	// WaitForModel is how long attaching keeps retrying a server that is
	// loading its model. 0 means DefaultWaitForModel; NoWait (any negative
	// value) means fail on the first attempt.
	WaitForModel time.Duration
	// WaitForStart extends that patience to a server that is not listening
	// yet, for the user who launches llama-server and toktape together. It
	// is off by default so a typo in --url stays an immediate failure
	// instead of a ten-minute one.
	WaitForStart bool
	// LoadingPoll is the gap between attach attempts while waiting. 0 means
	// DefaultLoadingPoll.
	LoadingPoll time.Duration
}

// EventKind names a step of the run. The CLI prints some of them and the TUI
// draws on all of them.
type EventKind string

const (
	// EventDiscovered fires once the server's base URL is known.
	EventDiscovered EventKind = "discovered"
	// EventLoading fires once per attach attempt while the server is not
	// ready. Elapsed is the time spent waiting so far and Message is
	// ReasonLoading, ReasonStarting or ReasonBusy.
	EventLoading EventKind = "loading"
	// EventProps fires once /props answered; Message carries the build.
	EventProps EventKind = "props"
	// EventPIDFound fires when the local server process was identified.
	EventPIDFound EventKind = "pid_found"
	// EventPIDNotFound fires when it was not: the run has no /proc view.
	EventPIDNotFound EventKind = "pid_not_found"
	// EventWarning fires for every sentence appended to
	// tape.RunSummary.Warnings.
	EventWarning EventKind = "warning"
	// EventAttached fires once the static picture is complete — server,
	// model, flags, host and devices — and before the first request is
	// sent. Its Summary is that partial summary, which is what the CLI's
	// header line and the TUI's chrome are drawn from; no timing field is
	// filled yet.
	EventAttached EventKind = "attached"
	// EventStreamStarted fires once per stream, before it is sent.
	EventStreamStarted EventKind = "stream_started"
	// EventToken fires for every token that carried text, with the major
	// fault delta of that token already filled in.
	EventToken EventKind = "token"
	// EventSample fires for every periodic host reading.
	EventSample EventKind = "sample"
	// EventDone fires once, with the finished summary.
	EventDone EventKind = "done"
)

// Event is one progress notification. Only the fields the Kind names are
// filled; the rest are zero.
type Event struct {
	Kind EventKind
	// Stream is the stream index for the stream-scoped kinds, else -1. In a
	// multi-round run it is the index within the round.
	Stream int
	// Streams is the total stream count, for the stream-scoped kinds. In a
	// multi-round run it is the streams of one round, never streams × rounds.
	Streams int
	// Round is the 0-based prompt round the event belongs to (TTP-31). It is
	// always 0 in a single-round run.
	Round int
	// Message is human-readable detail: the URL, the warning sentence, the
	// build and model line.
	Message string
	// Token is filled for EventToken.
	Token tape.TokenEvent
	// Sample is filled for EventSample.
	Sample tape.RunSample
	// MaxTokens is filled for EventStreamStarted: the generation cap that
	// stream's request carries. A live tile prints "12/320" from its first
	// token rather than a bare count that only becomes a fraction once the
	// tape is written. Zero means the request named no cap.
	MaxTokens int
	// Summary is filled for EventDone.
	Summary *tape.RunSummary
	// Elapsed is filled for EventLoading: how long attaching has been
	// waiting. It is wall time, not the run's clock, because the wait budget
	// and the line the user watches are both real seconds.
	Elapsed time.Duration
}

// normalize fills the defaults and returns a copy that the run can rely on.
func (o Options) normalize() Options {
	if o.Clock == nil {
		o.Clock = systemClock{}
	}
	if o.FSRoot == "" {
		o.FSRoot = "/"
	}
	if o.SampleInterval <= 0 {
		o.SampleInterval = tape.DefaultSampleInterval
	}
	switch {
	case o.WaitForModel == 0:
		o.WaitForModel = DefaultWaitForModel
	case o.WaitForModel < 0:
		o.WaitForModel = 0
	}
	if o.LoadingPoll <= 0 {
		o.LoadingPoll = DefaultLoadingPoll
	}
	if o.Concurrency <= 0 {
		if len(o.Rounds) > 0 {
			o.Concurrency = roundsConcurrency(o.Rounds)
		} else if len(o.Prompts) > 0 {
			o.Concurrency = len(o.Prompts)
		} else {
			o.Concurrency = 1
		}
	}
	return o
}

// buildRequests returns exactly Concurrency requests. Supplied prompts are
// cycled to fill the count and copied so that per-stream fields (the
// rendered prompt) cannot alias across streams.
func buildRequests(o Options, activeBytesPerToken int64) []server.StreamRequest {
	var reqs []server.StreamRequest
	if len(o.Prompts) == 0 {
		reqs = server.DefaultPrompts(o.Concurrency)
	} else {
		reqs = make([]server.StreamRequest, o.Concurrency)
		for i := range reqs {
			reqs[i] = cloneRequest(o.Prompts[i%len(o.Prompts)])
		}
	}
	for i := range reqs {
		if o.MaxTokens > 0 {
			reqs[i].MaxTokens = o.MaxTokens
		}
		reqs[i].ActiveBytesPerToken = activeBytesPerToken
	}
	return reqs
}

// cloneRequest deep-copies the parts of a request the recorder mutates.
func cloneRequest(r server.StreamRequest) server.StreamRequest {
	out := r
	out.Messages = append([]tape.Message(nil), r.Messages...)
	if r.Params != nil {
		out.Params = make(map[string]any, len(r.Params))
		for k, v := range r.Params {
			out.Params[k] = v
		}
	}
	return out
}
