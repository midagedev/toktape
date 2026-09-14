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
	// Concurrency is the number of streams sent at once — the CLI's
	// --sessions, recorded as tape.RunSummary.Concurrency. 0 or less means 1,
	// or len(Prompts) when prompts were supplied, or the longest round's
	// prompt count when Rounds were. Sessions is that resolution; a server
	// whose /props names fewer slots refuses the run
	// (ErrMoreSessionsThanSlots).
	Concurrency int
	// For is the run's wall-clock budget, measured from the first request
	// going out (TTP-76). 0 means "the user named none", which is what
	// DefaultFor is for; NoClock means they named none on purpose. It is
	// resolved together with MaxTokens — see Options.limit for the table, and
	// do not read either field as the answer on its own.
	For time.Duration
	// MaxTokens caps every stream's answer (the server's max_tokens /
	// n_predict). 0 means the user named no cap, and Record then sends
	// DefaultMaxTokens: a request with no cap at all generates unbounded, and
	// a clock that failed would have nothing behind it. Naming a cap here is
	// also what silences the default clock (Options.limit).
	//
	// Callers below Record — buildRequests, roundRequests — see the resolved
	// value, so 0 there still means "leave whatever the prompt carries".
	MaxTokens int
	// Params are merged verbatim into every request body (TTP-55,
	// 2026-09-14): temperature, seed, chat_template_kwargs, and anything else
	// the build honours. They are what `--temp` and `--param` fill, and they
	// win over a prompt's own Params for the same key — the flag is the more
	// recently expressed intent. The card shows what was actually sent
	// (lesson 4), so this map ends up in tape.PromptRecord.Params.
	Params map[string]any
	// Endpoint chooses the server path for every stream: "" or
	// tape.EndpointChat for /v1/chat/completions, tape.EndpointCompletion to
	// post each prompt verbatim to /completion with no template around it.
	//
	// Measured 2026-09-14 on DeepSeek-V4.1-Flash Q3_K_M: the chat path with
	// thinking on cost 11.6 % against greedy /completion on the same twenty
	// prompts, so which path recorded a rate is part of the result and not an
	// implementation detail.
	Endpoint string
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
	// HostRAM is what the operator said about the host's memory bandwidth
	// (TTP-45). Its zero value changes nothing: the run records whatever the
	// machine could be asked, which on Linux is neither the memory speed nor
	// the channel count.
	HostRAM HostRAM
}

// HostRAM is the operator's answer to a question the machine cannot be asked.
//
// On Linux the memory speed and the channel count live in the DMI tables and
// /sys/firmware/dmi/tables/DMI is mode 0400 root-only, so procmon leaves both
// empty and a partially offloaded run has no host bandwidth ceiling at all.
// These fields are `record --ram-gbs` and friends, and they are written into
// tape.HostInfo after the machine has been read, never instead of it: only the
// fields the operator actually named are replaced.
//
// Source is the tape.RAMSource* the figure is recorded under, which is how the
// card avoids presenting a number somebody typed as one it observed. The zero
// value of the whole struct overrides nothing.
type HostRAM struct {
	// BytesPerSec is the bandwidth itself, 0 when it was not stated.
	BytesPerSec int64
	// Speed and Channels are the DMI-shaped pair, "" and 0 when not stated.
	Speed    string
	Channels int
	// Source is tape.RAMSourceStated or tape.RAMSourceMeasured, "" when
	// nothing was stated at all.
	Source string
}

// apply writes what the operator stated over what the machine could be read
// for. A field they did not name is left exactly as collected.
func (r HostRAM) apply(h *tape.HostInfo) {
	if r.Source == "" {
		return
	}
	if r.BytesPerSec > 0 {
		h.RAMBytesPerSec = r.BytesPerSec
	}
	if r.Speed != "" {
		h.RAMSpeed = r.Speed
	}
	if r.Channels > 0 {
		h.RAMChannels = r.Channels
	}
	h.RAMSource = r.Source
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
	// Rounds, RoundName and SpecNMax are filled for EventStreamStarted in a
	// multi-round run: how many rounds the run sends, the name of this one,
	// and the speculative.n_max it was sent with (0 outside a sweep). The
	// recorder fills them because it owns the plan — a --spec-n-max sweep is
	// expanded into rounds only after the server's argv is read — so a caller
	// never counts rounds from the Options it passed in (TTP-35).
	Rounds    int
	RoundName string
	SpecNMax  int
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

// Sessions is the number of streams the run will send at once, resolved
// exactly as Record resolves it. A caller that guards the count — the CLI's
// ceiling — reads it here rather than repeating the rules, because a guard on
// the flag alone is walked around by a list of prompts.
func (o Options) Sessions() int {
	return o.normalize().Concurrency
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
		mergeParams(&reqs[i], o.Params)
		applyEndpoint(&reqs[i], o.Endpoint)
	}
	return reqs
}

// mergeParams merges the run's parameters into one request's own.
//
// chat_template_kwargs is merged key by key rather than replaced, because two
// switches that both live in it must be able to coexist: `--no-think` puts
// enable_thinking there and a `--param chat_template_kwargs={...}` may put
// something else. Replacing the map would silently drop one of the two, and a
// request that quietly lost a switch is the worst kind of wrong number.
// Every other key is replaced: the run's flag is the more recently expressed
// intent than a prompt file's line.
func mergeParams(r *server.StreamRequest, params map[string]any) {
	if len(params) == 0 {
		return
	}
	if r.Params == nil {
		r.Params = make(map[string]any, len(params))
	}
	for k, v := range params {
		if k == templateKwargsKey {
			if merged, ok := mergedKwargs(r.Params[k], v); ok {
				r.Params[k] = merged
				continue
			}
		}
		r.Params[k] = v
	}
}

// templateKwargsKey is the engine's bag of template switches; enable_thinking
// is the one this recorder sets.
const templateKwargsKey = "chat_template_kwargs"

// mergedKwargs returns old overlaid with new when both are objects, and
// reports whether it could merge at all. A value that is not an object is left
// to the caller to replace: the user typed it and the tape must show it.
func mergedKwargs(old, add any) (map[string]any, bool) {
	addMap, ok := add.(map[string]any)
	if !ok {
		return nil, false
	}
	oldMap, ok := old.(map[string]any)
	if !ok {
		return addMap, true
	}
	out := make(map[string]any, len(oldMap)+len(addMap))
	for k, v := range oldMap {
		out[k] = v
	}
	for k, v := range addMap {
		out[k] = v
	}
	return out, true
}

// applyEndpoint moves a request onto the raw path when the run asked for it.
//
// The prompt sent to /completion is the last user message's text, which is
// what every prompt source in this recorder produces: the default set, a
// repeated --prompt and a prompts file all build a one-message conversation.
// The messages are then cleared, because on this path they were never sent —
// a record that kept them would claim a conversation the server never saw.
func applyEndpoint(r *server.StreamRequest, endpoint string) {
	if endpoint != tape.EndpointCompletion {
		return
	}
	r.Endpoint = tape.EndpointCompletion
	if r.Prompt == "" {
		for i := len(r.Messages) - 1; i >= 0; i-- {
			if r.Messages[i].Content != "" {
				r.Prompt = r.Messages[i].Content
				break
			}
		}
	}
	r.Messages = nil
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
