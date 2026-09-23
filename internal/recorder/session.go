package recorder

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// An interactive session (`toktape chat`, 2026-09-24).
//
// A benchmark run knows every round before the first request goes out, and
// Record plans, calibrates and fetches the template for all of them at once.
// A chat session does not: each round is one turn a person types after reading
// the last answer. Session is the same recorder cut at that seam — Open does
// everything that happens once per tape, Send sends one turn, Close reduces
// the turns into one tape with Summary.Mode = tape.ModeChat.
//
// A session is one stream. The history is the session's own: Send appends the
// user's text, sends the whole conversation, and appends the answer, so the
// caller never holds a second copy that could disagree with the tape.
//
// Where each step of Record lives in a session, and why:
//
//   - Open: normalize, the chat refusals, attach, checkSlots, collectModel,
//     collectProcess, collectHost, collectPlacement and the prefill probe
//     pass — everything Record does before its first request that does not
//     depend on what will be asked. The probe is kept: it is a measurement
//     of the box the card prints, taken before any turn so no turn's cache
//     picture or fault baseline sees it; it emits its own EventNote
//     ("measuring this server's prefill…") before it starts. Then the
//     request shape every turn is cloned from is built (buildRequests, the
//     one owner of params, --model and the endpoint), and chooseOpenAIModel
//     and shapeOpenAIRequests run on it here, not on the first Send: a
//     --model the server does not list fails before the person types, and
//     the model is settled before the two things that read it — the
//     EventAttached summary's model name, and the OpenAI-kind context limit
//     (openaiModelCtx is per model). The slot's context is read once here
//     too; a slot's n_ctx does not change under a running server.
//   - Never: plan/applyPlan (the salt, the trim, the slot cap of a benchmark
//     run — a chat is the person's own words, sent as typed, and its cap is
//     the room the conversation has left, not a plan's), calibrateOpenAI and
//     applyCalibratedCap (both size an answer against a clock, and a chat has
//     none — chatLimit), and collectTemplate's per-run description beyond the
//     first turn.
//   - The first Send: the process sampler opens (its latch is the first
//     turn's baseline), the state is made, the periodic host reader starts
//     under the session's own context, and the origin every record's
//     StartedAt is measured from is taken — StartedAt is since the session's
//     first request, as it is since a rounds run's first.
//   - Every Send: the conversation is rendered through /apply-template (the
//     record carries what the model saw; the first turn's rendering is also
//     the tape's TemplateInfo), counted against the slot, capped, and sent as
//     one round of one stream through sendRound, the owner of a round's send
//     that streamRounds uses too.
//   - Close: the sampler stops, the warnings that must follow it are said,
//     and the turns go through reduce — which, with state.perRound set,
//     reduces them with reduceRounds: each turn's windows are its own, and
//     the minutes a person spent reading between turns are in no rate and no
//     WallMs, exactly as the gaps between a rounds run's rounds are not.

// ErrContextFull is returned by Send when the conversation plus the answer
// cap no longer fits the server's context. The session is still open and
// Close still writes the tape; the turn was not sent. The recorder never
// shortens the history to make it fit — a chat trimmed behind the user's back
// is a different conversation from the one they typed.
var ErrContextFull = errors.New("the conversation no longer fits the server's context")

// ErrSessionClosed is returned by Send and Close on a session Close has
// already closed (2026-09-24, an addition to the contract).
var ErrSessionClosed = errors.New("recorder: the chat session is closed")

const (
	// chatRunawayGuard is the answer cap a turn is sent with when the user
	// named none and the server did not say how much room its context has
	// (an OpenAI-kind server whose listing carries no max_model_len, a
	// llama-server started with --no-slots whose /props named no n_ctx).
	// It is not a target: with a known context the cap is the room left,
	// and this is what stands behind a server that would otherwise generate
	// forever (Ollama's default num_predict is unbounded, and a llama-server
	// with context shift on never runs out of room). 32768 is the longest
	// generation the reasoning models this tool records are published to
	// use — DeepSeek-R1's own card sets its maximum generation length there
	// — so a thought is never cut by the guard on a server that would have
	// finished it, and a person who wants less has ^C.
	chatRunawayGuard = 32768
	// chatCountedMarginTokens is what a turn adds to the server's own count
	// of its rendered conversation: the BOS token /tokenize leaves out
	// (add_special off) and room for a template whose rendering and whose
	// tokenization disagree by a few tokens. The rendering already holds
	// every role marker and the generation prompt, so the count is close to
	// exact and slotCtxTemplateTokens' 192 would waste room every turn.
	chatCountedMarginTokens = 16
	// chatPricedMessageTokens is the per-message template allowance when the
	// conversation could not be rendered and counted and is priced instead:
	// slotCtxTemplateTokens covers one message with its system lines and
	// generation prompt, and every further message adds its own role
	// markers (about five tokens in the ChatML, Llama 3 and Gemma templates).
	chatPricedMessageTokens = 8
)

// Session is one open chat session. It is not safe for concurrent use: one
// turn at a time, which is what a chat is.
//
// Send and Close hold one mutex, so a Close called while a Send is in flight
// waits for that turn to end: cancel the Send's context first, then Close.
// History takes its own lock and never waits for a turn, so a screen may
// read it while an answer streams.
type Session struct {
	r *run

	mu     sync.Mutex // one turn at a time; Close waits for an in-flight Send
	closed bool
	// shape is the request every turn is cloned from: params, model,
	// protocol and endpoint settled in Open. Its Messages are nil.
	shape server.StreamRequest
	// slotCtx is the per-request context read in Open, 0 when unknown —
	// which is never a limit.
	slotCtx int

	histMu  sync.Mutex
	history []tape.Message

	// The session's own context, for the periodic host reader: a turn's
	// context is the caller's and ^C cancels it, which must end that turn
	// and nothing else.
	sessCtx  context.Context
	sessStop context.CancelFunc

	st           *state // nil until the first Send
	closeSampler func()
	stopSampling func()
	origin       time.Time
	startedAt    time.Time
	finishedAt   time.Time
	recs         []tape.RequestRecord
	firstErr     error

	// The template route's misses, counted over the session and said once in
	// Close, in the sentence collectTemplate uses.
	tmplFailed, tmplTried int
}

// chatRefusals is what a chat session will not take, each with the reason in
// the error. Asked on the options as given, before normalize fills anything.
func chatRefusals(o Options) error {
	switch {
	case o.Concurrency > 1:
		return fmt.Errorf("recorder: a chat session is one stream; %d sessions asked", o.Concurrency)
	case len(o.Rounds) > 0:
		return errors.New("recorder: a chat session takes no prompt rounds; its turns are typed")
	case len(o.Prompts) > 0:
		return errors.New("recorder: a chat session takes no prompts; its turns are typed")
	case len(o.SpecNMax) > 0:
		return errors.New("recorder: a chat session runs no --spec-n-max sweep")
	case o.For > 0:
		return fmt.Errorf("recorder: a chat session has no clock; --for %s cannot apply", o.For)
	case o.Endpoint == tape.EndpointCompletion:
		// The raw path sends one prompt with no template, and a
		// conversation is the list of messages the server templates.
		return errors.New("recorder: a chat session sends messages; --endpoint completion has none")
	}
	return nil
}

// Open attaches to the server and does everything that happens once per tape.
// It fails exactly where Record would fail before its first request.
//
// Progress events, in order: the attach events (EventDiscovered, EventProps,
// EventLoading while waiting), EventPIDFound or EventPIDNotFound, warnings,
// an EventNote before the prefill probe pass on a llama-kind server, and
// EventAttached last — its Summary carries Mode = tape.ModeChat and the
// chat's Limit (For 0; MaxTokens the user's cap or 0).
func Open(ctx context.Context, opts Options) (*Session, error) {
	if err := chatRefusals(opts); err != nil {
		return nil, err
	}
	opts = opts.normalize()
	if err := checkEngineKind(opts.EngineKind); err != nil {
		return nil, err
	}
	// Options.Progress promises serialised calls. A benchmark run keeps that
	// by construction — its warnings are said after the sampler stops — but a
	// session's sampler runs between turns, while Send may warn. One lock
	// around the callback keeps the promise for every caller in the session;
	// the state's hot path takes it after its own mutex, and nothing takes
	// them the other way round.
	if p := opts.Progress; p != nil {
		var pmu sync.Mutex
		opts.Progress = func(ev Event) {
			pmu.Lock()
			defer pmu.Unlock()
			p(ev)
		}
	}
	r := &run{opts: opts, limit: opts.chatLimit(), mode: tape.ModeChat}

	if err := r.attach(ctx); err != nil {
		return nil, err
	}
	if err := r.checkSlots(); err != nil {
		return nil, err
	}
	r.collectModel()
	r.collectProcess()
	r.collectHost(ctx)
	ok := false
	defer func() {
		if !ok && r.ownGPU && r.gpus != nil {
			r.gpus.Close()
		}
	}()
	r.collectPlacement()
	r.prefillProbe(ctx)

	ro := opts
	ro.Prompts = []server.StreamRequest{{}}
	ro.Concurrency = 1
	ro.MaxTokens = 0 // each turn sets its own (Session.answerCap)
	shape := buildRequests(ro, r.model.ActiveBytesPerToken)
	if err := r.chooseOpenAIModel(shape); err != nil {
		return nil, err
	}
	shape, err := r.shapeOpenAIRequests(shape)
	if err != nil {
		return nil, err
	}
	if r.kind.SpeaksLlamaProtocol() {
		r.template.ChatTemplate = r.props.ChatTemplate
	}
	slotCtx := r.smallestSlotCtx(ctx)
	r.emitAttached()

	sessCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	ok = true
	return &Session{
		r:            r,
		shape:        shape[0],
		slotCtx:      slotCtx,
		sessCtx:      sessCtx,
		sessStop:     stop,
		closeSampler: func() {},
		stopSampling: func() {},
	}, nil
}

// Send sends one turn — text as the user's message after the history so
// far — and returns its record once the answer has finished. Cancelling ctx
// ends this turn only: the partial answer is kept as a cancelled record and
// the session stays open.
//
// Send blocks until the turn is over; tokens arrive meanwhile as
// EventToken{Round: turn, Stream: 0} on Options.Progress, after one
// EventStreamStarted{Round: turn, Rounds: 0, Streams: 1, MaxTokens: the
// turn's cap}. The returned record's Timings are reduced (its rates are
// readable at once; Close reduces them again with the session's fault
// deltas).
//
// What each outcome returns, and what it does to History:
//
//   - answered: the record and nil. The user's message and the answer's
//     text (PromptRecord.Completion — the reasoning tokens are not part of
//     it, as no chat client sends a model its own thinking back) are
//     appended to the history.
//   - cancelled (ctx ended mid-turn): the record, carrying every token that
//     arrived, marked as the clock marks a stream it ends (markCut):
//     PromptRecord.Cut true, FinishReason "" (the server never said a word)
//     and no Error — so its rate counts and a session whose only turn was
//     stopped still writes a tape — and an error for which
//     errors.Is(err, context.Canceled) holds. The user's
//     message and the partial answer ARE appended, even when the partial
//     answer is empty: the person saw that answer begin and will talk about
//     it, and dropping the turn would send the next one a conversation the
//     screen does not show. An empty assistant message keeps the roles
//     alternating, which strict templates require.
//   - failed (the server refused or dropped the stream): the record, with
//     its Error, and the error. The history is unchanged — the server never
//     answered, and the natural next act is to send the same text again.
//   - ErrContextFull (wrapped with the numbers, errors.Is holds): nothing
//     was sent, no record is kept, the history is unchanged, and the
//     session remains usable for Close.
//   - ctx already done before anything was sent: ctx.Err(), nothing kept.
//   - ErrSessionClosed after Close.
//
// Every record kept — answered, cancelled or failed — is one turn of the tape
// and counts in Summary.Rounds.
func (s *Session) Send(ctx context.Context, text string) (tape.RequestRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return tape.RequestRecord{}, ErrSessionClosed
	}
	if err := ctx.Err(); err != nil {
		return tape.RequestRecord{}, err
	}
	r := s.r
	user := tape.Message{Role: "user", Content: text}
	reqs := []server.StreamRequest{cloneRequest(s.shape)}
	q := &reqs[0] // the one request of this turn's round; edited in place
	q.Messages = append(s.History(), user)

	// The conversation as the model will see it: fetched before the send so
	// the record carries it, and counted from it, because the rendering is
	// every token the slot will hold — role markers and generation prompt
	// included.
	if r.kind.SpeaksLlamaProtocol() {
		failed, tried := r.renderPrompts(ctx, reqs)
		s.tmplFailed += failed
		s.tmplTried += tried
	}
	promptTokens := s.promptTokens(ctx, q)
	if err := ctx.Err(); err != nil {
		return tape.RequestRecord{}, err // ^C before the turn went out
	}
	answer, err := s.answerCap(promptTokens)
	if err != nil {
		return tape.RequestRecord{}, err
	}
	q.MaxTokens = answer

	k := len(s.recs)
	if k == 0 {
		// The first turn's rendering describes the tape, as the first
		// stream's does a benchmark run's.
		r.describeTemplate(q)
		sampler, closeSampler := r.openSampler()
		s.closeSampler = closeSampler
		s.st = newState(0, sampler, r.opts.Progress)
		s.st.perRound, s.st.streams = 1, 1
	} else {
		s.st.relatch()
	}
	got, sendErr := r.sendRound(ctx, s.st, k, reqs, 0, "", &s.origin, func() context.Context {
		s.stopSampling = r.startSampling(s.sessCtx, s.st)
		s.startedAt = r.opts.Clock.Now()
		s.origin = time.Now()
		return ctx
	})
	s.finishedAt = r.opts.Clock.Now()
	// A turn the caller stopped is marked the way the clock's cut is — by
	// markCut, the one owner of that mark — and not left carrying the read
	// loop's "cancelled" Error. Its tokens arrived with the server's own
	// timings, so its rate is a measurement; an Error would put it among
	// the failed streams, out of every rate, and a session whose only turn
	// was stopped would reduce to ErrAllStreamsFailed and write no tape at
	// all. A stream whose finish chunk beat the cancel keeps its word.
	stopped := ctx.Err() != nil && markCut(got, []bool{true}) > 0
	rec := got[0]
	s.recs = append(s.recs, rec)

	out := rec
	out.Timings = server.Reduce(&out, s.startedAt.Add(out.StartedAt), r.model.ActiveBytesPerToken)

	switch {
	case stopped:
		s.appendTurn(user, rec.Prompt.Completion)
		return out, fmt.Errorf("recorder: turn %d cancelled after %d tokens: %w", k+1, len(rec.Tokens), ctx.Err())
	case rec.Error == "":
		// Answered — also when the cancel arrived after the final chunk: the
		// server finished the turn, and the record says so.
		s.appendTurn(user, rec.Prompt.Completion)
		return out, nil
	default:
		if sendErr == nil {
			sendErr = errors.New(rec.Error)
		}
		if s.firstErr == nil {
			s.firstErr = sendErr
		}
		return out, sendErr
	}
}

// appendTurn adds one exchange to the history.
func (s *Session) appendTurn(user tape.Message, answer string) {
	s.histMu.Lock()
	defer s.histMu.Unlock()
	s.history = append(s.history, user, tape.Message{Role: "assistant", Content: answer})
}

// promptTokens is how much of the slot the turn's conversation will hold.
//
// On a llama-kind server the rendered conversation is counted by the
// server's own tokenizer: the rendering is every token the slot will hold,
// so the count is the figure, with chatCountedMarginTokens for what the two
// routes leave out. When either route failed — or on an OpenAI-kind server,
// which has no /tokenize — the messages are priced on the run's one ruler
// (pricedTokens, or the calibrated openAIPricedTokens) with the template's
// allowance, which errs toward more tokens: a priced conversation reaches
// ErrContextFull early, never a refused stream.
func (s *Session) promptTokens(ctx context.Context, q *server.StreamRequest) int {
	r := s.r
	if r.kind.SpeaksLlamaProtocol() && q.RenderedPrompt != "" {
		if n, err := r.client.Tokenize(ctx, q.RenderedPrompt); err == nil {
			return n + chatCountedMarginTokens
		}
	}
	bytes := 0
	for _, m := range q.Messages {
		bytes += len(m.Content)
	}
	priced := pricedTokens(bytes)
	if r.kind == tape.ServerOpenAI {
		priced = r.openAIPricedTokens(bytes)
	}
	return priced + slotCtxTemplateTokens + chatPricedMessageTokens*max(len(q.Messages)-1, 0)
}

// answerCap is the cap this turn is sent with, or ErrContextFull.
//
//   - The user named a cap: it is every turn's cap, and a conversation that
//     no longer leaves that much room is full (decision of 2026-09-24: the
//     cap is the user's word, and lowering it turn by turn would answer a
//     question they did not ask).
//   - They named none and the slot's context is known: the room it has
//     left, so the answer runs to the model's own end and the slot is the
//     only bound. Less room than tape.MinCutTokens — the threshold at which
//     capAnswersToSlot refuses a benchmark prompt — is full: an answer that
//     short is not an answer.
//   - Neither is known: chatRunawayGuard.
func (s *Session) answerCap(promptTokens int) (int, error) {
	lim := s.r.limit
	if s.slotCtx <= 0 {
		if lim.MaxTokensNamed {
			return lim.MaxTokens, nil
		}
		return chatRunawayGuard, nil
	}
	room := s.slotCtx - promptTokens
	need := tape.MinCutTokens
	if lim.MaxTokensNamed {
		need = lim.MaxTokens
	}
	if room < need {
		return 0, fmt.Errorf("%w: the conversation is ~%d tokens, the answer needs %d more, and the context is %d",
			ErrContextFull, promptTokens, need, s.slotCtx)
	}
	if lim.MaxTokensNamed {
		return lim.MaxTokens, nil
	}
	return room, nil
}

// History is the conversation so far, oldest first, as it will be sent with
// the next turn. The returned slice is a copy.
func (s *Session) History() []tape.Message {
	s.histMu.Lock()
	defer s.histMu.Unlock()
	return append([]tape.Message(nil), s.history...)
}

// Close stops the session's sampling and reduces every turn into one tape.
// A session with no answered turn is ErrAllStreamsFailed, as a run is.
//
// Close waits for a Send in flight to return (cancel its context first). It
// emits the warnings that follow the sampler, then EventDone with the
// summary, exactly as Record does. A second Close is ErrSessionClosed.
func (s *Session) Close() (*tape.Tape, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrSessionClosed
	}
	s.closed = true
	r := s.r
	defer func() {
		if r.ownGPU && r.gpus != nil {
			r.gpus.Close()
		}
	}()
	s.stopSampling()
	s.closeSampler()
	s.sessStop()
	if s.st != nil {
		r.warnTreeProcesses(s.st)
	}
	if s.tmplFailed > 0 {
		r.warnTemplateFailures(s.tmplFailed, s.tmplTried)
	}
	if len(s.recs) == 0 {
		return nil, fmt.Errorf("%w: no turn was sent", ErrAllStreamsFailed)
	}
	if allFailed(s.recs) {
		return nil, fmt.Errorf("%w: no turn was answered: %v", ErrAllStreamsFailed, s.firstErr)
	}
	s.st.applyDeltas(s.recs)
	// One name per turn sent; "" and the card labels a turn by its number.
	r.roundNames = make([]string, len(s.recs))
	t := r.reduce(s.recs, s.st, s.startedAt, s.finishedAt)
	r.emit(Event{Kind: EventDone, Stream: -1, Summary: &t.Summary})
	return t, nil
}
