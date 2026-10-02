package tape

import (
	"encoding/json"
	"time"
)

// A decision tape (TTP-192, 2026-10-02) records a decision model: a
// prefill-only classifier that reads a state and a schema of typed questions
// and answers each with one probability per allowed option (Cloudflare Clef,
// POST /v1/systemone). Nothing is generated, so nothing on it is a token
// stream: Requests stays empty, every decode and inter-token figure is
// absent, and every renderer that reads tokens must branch on IsDecision
// before it reads anything. A decision is not a RequestRecord wearing a
// different meaning; it is its own record, Tape.Decisions.
//
// What one run is: a suite of request bodies (cases) sent one after another,
// repeated, and timed send to full response. The first request is the cold
// one and is reported apart; every other request is warm.

// ModeDecision is RunSummary.Mode for a `toktape decide` run.
const ModeDecision = "decision"

// IsDecision reports whether the run is a decision-model run. It is the one
// predicate every reader asks — the screen, the clip schedule, the card, and
// the surfaces that refuse a decision tape (publish, compare, the ledger) —
// so they cannot disagree about which tapes hold no token stream.
func (s *RunSummary) IsDecision() bool {
	return s != nil && s.Mode == ModeDecision
}

// Values of DecisionRecord.Phase.
const (
	// DecisionPhaseShowcase is a paced pass: one request at a time with real
	// idle time between them, so a replay at 1:1 shows each answer arrive.
	// The idle time is recorded (the gap between one AnsweredAt and the next
	// SentAt) and is never part of any latency.
	DecisionPhaseShowcase = "showcase"
	// DecisionPhaseBurst is back to back, Concurrency lanes at once: the
	// pass the throughput figure is measured over.
	DecisionPhaseBurst = "burst"
)

// Values of DecisionQuestion.Type and DecisionAnswer.Type, as the API spells
// them.
const (
	DecisionChoice = "choice"
	DecisionScore  = "score"
	DecisionNoul   = "noul" // a yes/no question; the answer is p(true)
)

// Values of DecisionSummary.TimingSource.
const (
	// DecisionTimingClient: every latency is the client's wall clock, send
	// to the last byte of the response — end to end, network and JSON
	// included. The card says "client, end to end".
	DecisionTimingClient = "client"
	// DecisionTimingServer: the engine reported its own prompt timing on
	// every answered request (DecisionRecord.Server), and the engine-side
	// figures are the record; the client latency stays the check.
	DecisionTimingServer = "server"
)

// DecisionRecord is one request: what was sent, what came back, and when.
type DecisionRecord struct {
	// Index is the send order over the whole run, from 0.
	Index int `json:"index"`
	// CaseID is the suite row's id ("route-01"). It is toktape's own field
	// and is never sent: Request is the body without it.
	CaseID string `json:"case_id"`
	// Repeat is which pass over the suite this was, from 0.
	Repeat int    `json:"repeat"`
	Phase  string `json:"phase"` // DecisionPhase*
	// Lane is the concurrency lane, 0..Concurrency-1; 0 on a paced pass.
	Lane int `json:"lane"`

	// SentAt is when the request was written, since run start. AnsweredAt is
	// when the full response had been read; 0 when the request failed.
	// Latency is AnsweredAt - SentAt and nothing else.
	SentAt     time.Duration `json:"sent_at"`
	AnsweredAt time.Duration `json:"answered_at,omitempty"`

	// Request and Response are the bodies verbatim. They are the record;
	// Questions and Answers are their parse, kept so no renderer has to
	// parse JSON, and in the request's own key order — a Go map would lose
	// the order the criteria were written in, and the screen shows them in
	// that order.
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response,omitempty"`

	Questions []DecisionQuestion `json:"questions"`
	Answers   []DecisionAnswer   `json:"answers,omitempty"` // in Questions order

	// InputTokens is usage.input_tokens; 0 when the response had none.
	InputTokens int `json:"input_tokens,omitempty"`
	// Model is the response's "model" string, verbatim.
	Model string `json:"model,omitempty"`
	// Server is the engine's own timing when the response carried one, nil
	// otherwise (most SystemOne servers send none).
	Server *DecisionServerTimings `json:"server,omitempty"`

	Error string `json:"error,omitempty"` // non-empty when the request failed
}

// Latency is the request's send-to-response time, 0 for a failed request.
func (r DecisionRecord) Latency() time.Duration {
	if r.Error != "" || r.AnsweredAt <= r.SentAt {
		return 0
	}
	return r.AnsweredAt - r.SentAt
}

// DecisionServerTimings is engine-side timing, llama-server style: the
// response's extra top-level "timings" key (bloomery sends it; the official
// SystemOne servers do not). Engine time is PromptMs + HeadMs.
type DecisionServerTimings struct {
	PromptN  int     `json:"prompt_n"`
	PromptMs float64 `json:"prompt_ms"` // the backbone's prefill
	// HeadMs is the joint schema head, after the prefill. 0 when the engine
	// did not report it separately.
	HeadMs float64 `json:"head_ms,omitempty"`
	// CacheN is how many prompt tokens the engine reused from its cache. A
	// request with CacheN > 0 did not pay for its whole prompt, and the card
	// says so rather than reporting the latency as a prefill.
	CacheN int `json:"cache_n"`
}

// DecisionQuestion is one question of a request, parsed from its schema.
type DecisionQuestion struct {
	ID           string `json:"id"`
	Type         string `json:"type"` // DecisionChoice | DecisionScore | DecisionNoul
	Instructions string `json:"instructions,omitempty"`
	// Options are the allowed answers in the order the request wrote them:
	// a choice's criteria keys with their descriptions; a score's levels,
	// Key "0", "1", ... with the criterion text; none for a noul.
	Options []DecisionOption `json:"options,omitempty"`
}

// DecisionOption is one allowed answer.
type DecisionOption struct {
	Key  string `json:"key"`
	Text string `json:"text,omitempty"`
}

// DecisionAnswer is one question's answer, parsed from the response.
type DecisionAnswer struct {
	QuestionID string `json:"question_id"`
	Type       string `json:"type"`
	// Choice is a choice answer's key; Score a score answer's expected level
	// index, sum(i * p_i). Confidence is max p for both.
	Choice     string  `json:"choice,omitempty"`
	Score      float64 `json:"score,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	// Probabilities are one per option, in DecisionQuestion.Options order.
	// Empty for a noul.
	Probabilities []DecisionProb `json:"probabilities,omitempty"`
	// Noul is a noul answer's p(true). A pointer because 0.0 is an answer:
	// nil is "not a noul, or the server sent none".
	Noul *float64 `json:"noul,omitempty"`
}

// DecisionProb is one option's probability, as the server rounded it.
type DecisionProb struct {
	Key string  `json:"key"`
	P   float64 `json:"p"`
}

// DecisionSummary is everything the decision card needs. Like RunSummary it
// is computed once, by the recorder, from Tape.Decisions; renderers of the
// card must not need the records.
type DecisionSummary struct {
	Endpoint string `json:"endpoint"`            // the path posted to, "/v1/systemone"
	Model    string `json:"model,omitempty"`     // the responses' "model", when they all agreed
	Suite    string `json:"suite,omitempty"`     // the suite's file name, "" for the built-in one
	SuiteSHA string `json:"suite_sha,omitempty"` // sha256 of the suite bytes as sent, hex

	Cases       int `json:"cases"`       // distinct suite rows
	Repeats     int `json:"repeats"`     // passes over the suite, both phases
	Concurrency int `json:"concurrency"` // lanes in the burst phase
	Requests    int `json:"requests"`    // every request sent
	Errors      int `json:"errors"`

	TimingSource string `json:"timing_source"` // DecisionTiming*

	// ColdMs is the first request's latency. Every Warm figure is over the
	// answered requests after it, both phases.
	ColdMs     float64 `json:"cold_ms,omitempty"`
	WarmP50Ms  float64 `json:"warm_p50_ms,omitempty"`
	WarmP95Ms  float64 `json:"warm_p95_ms,omitempty"`
	WarmMeanMs float64 `json:"warm_mean_ms,omitempty"`

	InputTokensMin int `json:"input_tokens_min,omitempty"`
	InputTokensMax int `json:"input_tokens_max,omitempty"`
	// PrefillPerSecond is the median over warm requests of
	// input_tokens / latency: an end-to-end figure on a client-timed run,
	// the engine's prompt_n / prompt_ms on a server-timed one.
	PrefillPerSecond float64 `json:"prefill_per_second,omitempty"`
	// RequestsPerSecond is the burst phase's answered requests over its
	// window, first SentAt to last AnsweredAt. 0 without a burst phase.
	RequestsPerSecond float64 `json:"requests_per_second,omitempty"`
	// CacheHits counts requests whose engine timing reported CacheN > 0.
	CacheHits int `json:"cache_hits,omitempty"`

	PerCase []DecisionCaseSummary `json:"per_case,omitempty"` // suite order

	// Reference is the comparison with a reference answer file, nil when
	// none was given. The card prints no correctness row without it.
	Reference *DecisionAgreement `json:"reference,omitempty"`
}

// DecisionCaseSummary is one suite row's figures.
type DecisionCaseSummary struct {
	CaseID      string  `json:"case_id"`
	InputTokens int     `json:"input_tokens,omitempty"`
	WarmP50Ms   float64 `json:"warm_p50_ms,omitempty"`
	Answered    int     `json:"answered"`
}

// DecisionAgreement compares this run's answers with a reference file of
// the same suite answered by the official model (BF16 Python systemone()).
// For a decision model a quantization moves the probabilities before it
// moves the answers, so the probability figures lead.
type DecisionAgreement struct {
	File      string `json:"file"`
	Questions int    `json:"questions"` // question answers compared
	// MaxAbsDeltaP is the largest |p - p_ref| over every option of every
	// compared question.
	MaxAbsDeltaP float64 `json:"max_abs_delta_p"`
	// TopFlips counts compared questions whose top option differs.
	TopFlips int `json:"top_flips"`
	// BrierDelta is mean squared distance between the two distributions,
	// averaged over compared questions.
	BrierDelta float64 `json:"brier_delta"`
}
