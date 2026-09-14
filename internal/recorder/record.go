package recorder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/placement"
	"github.com/midagedev/toktape/internal/procmon"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// run carries one recording's mutable state through the collection steps.
type run struct {
	opts     Options
	warnings []string
	client   *server.Client

	props    *server.Props
	kind     tape.ServerKind
	build    string // from /props (attach)
	commit   string // from /props, else the ik checkout (collectProcess, TTP-33)
	model    tape.ModelInfo
	tensors  []placement.Tensor
	pid      int
	args     []string
	flags    tape.ServerFlags
	host     tape.HostInfo
	gpus     gpu.Collector
	ownGPU   bool
	place    tape.PlacementSummary
	template tape.TemplateInfo
	// roundNames are the names of the rounds that were actually sent, in
	// order; nil in a single-round run (TTP-31).
	roundNames []string
	// witnesses are the contention readings taken at the start and the end
	// of every measurement round (TTP-36); nil when the server is not local.
	witnesses []tape.ContentionWitness
	// limit is what may end this run's generation, resolved once by
	// Options.limit (TTP-76). cutAt is filled in only if the clock actually
	// ended something, and is what the tape's LimitSummary.CutAt carries.
	limit tape.LimitSummary
	cutAt time.Duration
}

// Record performs one run end to end: attach, collect the static picture,
// send the streams, watch the host, and reduce everything into a tape.
//
// Every collector degrades into a warning. Only two conditions fail the run:
// a server that cannot be reached (ErrUnreachable) and a run in which no
// stream produced a record (ErrAllStreamsFailed). A run the clock cut is not
// one of them — it is a run, and the tape says the clock ended it (TTP-76).
func Record(ctx context.Context, opts Options) (*tape.Tape, error) {
	opts = opts.normalize()
	// The one place the "what may end this generation" table is read, and
	// before anything is built from the options: the resolved cap is what
	// every request carries from here down, so no later step has to ask the
	// question again.
	limit := opts.limit()
	opts.MaxTokens = limit.MaxTokens
	r := &run{opts: opts, limit: limit}

	if err := r.attach(ctx); err != nil {
		return nil, err
	}
	r.collectModel()
	r.collectProcess()
	r.collectHost(ctx)
	defer func() {
		if r.ownGPU && r.gpus != nil {
			r.gpus.Close()
		}
	}()
	r.collectPlacement()
	// A --spec-n-max sweep becomes rounds here, once the argv says whether a
	// draft model is loaded (TTP-35).
	r.planSweep()

	if len(r.opts.Rounds) > 0 {
		return r.recordRounds(ctx)
	}

	reqs := buildRequests(opts, r.model.ActiveBytesPerToken)
	r.collectTemplate(ctx, reqs)
	r.emitAttached()

	startedAt := opts.Clock.Now()
	recs, st, err := r.stream(ctx, reqs)
	finishedAt := opts.Clock.Now()
	if err != nil {
		return nil, err
	}

	t := r.reduce(recs, st, startedAt, finishedAt)
	if opts.Progress != nil {
		opts.Progress(Event{Kind: EventDone, Stream: -1, Streams: opts.Concurrency, Summary: &t.Summary})
	}
	return t, nil
}

// warn records a caveat the card prints verbatim and reports it to the
// progress callback.
func (r *run) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.warnings = append(r.warnings, msg)
	if r.opts.Progress != nil {
		r.opts.Progress(Event{Kind: EventWarning, Stream: -1, Message: msg})
	}
}

// emitAttached publishes the static half of the summary: everything that is
// known before a single token exists. The CLI's header line and the TUI's
// chrome are drawn from it, so neither has to wait for the run to finish to
// name the model it is measuring.
func (r *run) emitAttached() {
	if r.opts.Progress == nil {
		return
	}
	s := &tape.RunSummary{
		ToktapeVersion: r.opts.Version,
		Server: tape.ServerInfo{
			Kind:    r.kind,
			URL:     r.client.BaseURL(),
			PID:     r.pid,
			Host:    r.host.Hostname,
			Args:    r.args,
			Flags:   r.flags,
			NSlots:  r.props.TotalSlots,
			CtxSize: r.props.CtxSize(),
		},
		Model:       r.model,
		Host:        r.host,
		Placement:   r.place,
		Concurrency: r.opts.Concurrency,
		Template:    r.template,
		Warnings:    r.warnings,
		// What may end this run, before it starts (TTP-76). CutAt is
		// necessarily 0 here — nothing has been cut yet — but For and
		// MaxTokens are already decided, and a live screen that draws a
		// stream's progress against its token cap needs to know when the
		// budget, not the cap, is what the run will end on.
		Limit: r.limit,
	}
	s.Server.Build, s.Server.Commit = r.build, r.commit
	r.emit(Event{Kind: EventAttached, Stream: -1, Summary: s})
}

func (r *run) emit(ev Event) {
	if r.opts.Progress != nil {
		ev.Streams = r.opts.Concurrency
		r.opts.Progress(ev)
	}
}

// attach finds the server and reads /props, waiting while the server is
// loading its model. This is the only step that can fail the run outright.
//
// The wait is the difference between a tool that works on a box with a 450 GB
// model and one that does not: such a server listens long before it answers,
// and every attempt before the model is resident looks exactly like a wrong
// URL. See the Options.WaitForModel doc for the measurement.
func (r *run) attach(ctx context.Context) error {
	start := time.Now()
	for {
		// The caller's own cancellation is checked first and every time
		// round: a cancelled run must return now, not after one more poll
		// interval, and its cancellation is not a server state.
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %v", ErrUnreachable, err)
		}
		c, props, err := r.probe(ctx)
		if err == nil {
			r.client, r.props = c, props
			r.kind = server.DetectKind(props)
			r.build, r.commit = server.BuildFromProps(props.BuildInfo)
			r.emit(Event{Kind: EventDiscovered, Stream: -1, Message: c.BaseURL()})
			r.emit(Event{Kind: EventProps, Stream: -1, Message: r.build})
			return nil
		}

		reason, waitable := r.waitReason(err)
		elapsed := time.Since(start)
		if !waitable {
			return fmt.Errorf("%w: %w", ErrUnreachable, err)
		}
		if elapsed >= r.opts.WaitForModel {
			// The exit code stays 2: a wrapper script branches on it and
			// "the server never became ready" is still "could not attach".
			//
			// The cause is wrapped rather than formatted (TTP-75): the CLI
			// tells "nothing was listening on the ports I probed" from "a
			// server was there and never became ready" by asking the chain,
			// and those two failures need different instructions.
			if r.opts.WaitForModel == 0 {
				return fmt.Errorf("%w: %w", ErrUnreachable, err)
			}
			return fmt.Errorf("%w: gave up after %s: %w",
				ErrUnreachable, r.opts.WaitForModel.Round(time.Second), err)
		}
		r.emit(Event{Kind: EventLoading, Stream: -1, Elapsed: elapsed, Message: reason})

		timer := time.NewTimer(r.opts.LoadingPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%w: %v", ErrUnreachable, ctx.Err())
		case <-timer.C:
		}
	}
}

// probe is one attach attempt: discovery when no URL was given, then /props.
//
// Discovery is redone on every attempt rather than once, because a server that
// opens its port late is found by scanning the candidates again — re-polling a
// URL that discovery never produced would wait forever on nothing.
func (r *run) probe(ctx context.Context) (*server.Client, *server.Props, error) {
	c := server.New(r.opts.BaseURL)
	if r.opts.BaseURL == "" {
		url, err := c.Discover(ctx, r.opts.Candidates)
		if err != nil {
			return nil, nil, err
		}
		c = c.WithBaseURL(url)
	}
	props, err := c.Props(ctx)
	if err != nil {
		return nil, nil, err
	}
	return c, props, nil
}

// waitReason says whether err is worth waiting out, and which sentence the CLI
// should print while it does.
//
// A server that is loading is always worth waiting for, and so is one busy
// with another request (TTP-33). A refused connection
// is only worth waiting for when the user asked (--wait), because otherwise a
// mistyped --url would hang for ten minutes instead of failing in a second.
func (r *run) waitReason(err error) (reason string, waitable bool) {
	switch {
	case errors.Is(err, server.ErrLoading):
		return ReasonLoading, true
	case errors.Is(err, server.ErrBusy):
		return ReasonBusy, true
	case r.opts.WaitForStart:
		return ReasonStarting, true
	default:
		return "", false
	}
}

// collectModel reads the GGUF header when the model file is on this host.
// When it is not — the normal case for a remote server — the file name and
// the quantisation are still recoverable from the path /props reported, and
// everything the header would have supplied stays zero.
func (r *run) collectModel() {
	path := r.props.ModelPath
	if path == "" {
		r.warn("server did not report model_path, model shape unknown")
		return
	}
	base := filepath.Base(path)
	mi, tensors, err := placement.ModelInfoFromFile(path)
	if err == nil {
		r.model, r.tensors = mi, tensors
		// The header was read from the named part; the variant directory and
		// the whole set's size come from the file system around it (TTP-32).
		r.fillShardSet(path, true)
		return
	}
	// A warning is printed verbatim on the card, so it is a short human
	// sentence and never a wrapped Go error chain: four lines of
	// "open ...: no such file" would crowd out the figures the card exists
	// to show. The path itself is on the tape in Model.Path.
	r.warn("model file not readable here, shape and placement unknown")
	r.model = tape.ModelInfo{
		Path:     path,
		FileName: base,
		Quant:    placement.QuantFromFileName(base),
	}
	// The parts are not on this machine either, so they are named but never
	// stat'ed: FileBytes stays 0 and the card prints "?" rather than the size
	// of a file it could not see. The warning above already says the file was
	// unreadable, and a second one about the other eight parts would only
	// crowd the card.
	r.fillShardSet(path, false)
}

// collectProcess finds the local server process and reads its argv, which is
// where every flag the card prints comes from. The process also settles the
// engine /props may not have named, and an ik_llama.cpp server's commit.
func (r *run) collectProcess() {
	if r.props.ModelPath == "" {
		return
	}
	pid, err := procmon.FindPID(r.opts.FSRoot, r.props.ModelPath)
	if err != nil {
		r.warn("pid not found: no memory, page faults or flags")
		r.emit(Event{Kind: EventPIDNotFound, Stream: -1})
		return
	}
	r.pid = pid
	r.emit(Event{Kind: EventPIDFound, Stream: -1, Message: strconv.Itoa(pid)})

	args, err := procmon.Args(r.opts.FSRoot, pid)
	if err != nil {
		r.warn("argv of pid %d unreadable, server flags unknown", pid)
	} else {
		r.args = args
		r.flags = server.ParseFlags(args)
	}

	// ik_llama.cpp's /props names neither the engine nor a build (TTP-33,
	// 2026-09-13), so the binary does: its path says ik, and the checkout
	// around it names a commit. The exe link of another user's process is
	// unreadable, which is normal and not a caveat for the card — the kind
	// then stays what /props said. The server's own build_info, when there is
	// one, is the record and is never replaced by the checkout.
	exe, err := procmon.Exe(r.opts.FSRoot, pid)
	if err != nil {
		exe = ""
	}
	r.kind = server.RefineKind(r.kind, exe, r.args)
	if r.kind == tape.ServerIKLlama && r.commit == "" && exe != "" {
		// The checkout's HEAD is what the clone says now, not what the binary
		// was built from: the measured workstation ran a binary under a local
		// commit on top of the base, with uncommitted changes. The card prints
		// warnings verbatim, so the provenance travels with the figure.
		if c, ok := procmon.FindGitCommit("", exe); ok {
			r.commit = c.Hash
			msg := fmt.Sprintf("engine commit %s read from the checkout next to the binary, not from the binary", c.Hash)
			if procmon.BinaryOlderThanCheckout("", exe, c) {
				msg += "; the binary is older than that commit"
			}
			r.warn("%s", msg)
		}
	}
}

// collectHost reads the hardware line and opens the GPU backend.
func (r *run) collectHost(ctx context.Context) {
	host, err := procmon.HostInfo(r.opts.FSRoot)
	if err != nil {
		r.warn("/proc not readable, CPU, RAM and kernel unknown")
	}
	// What the operator stated wins over what the machine could be read for,
	// because on Linux the machine cannot be read for it at all (TTP-45).
	r.opts.HostRAM.apply(&host)
	r.host = host

	r.gpus = r.opts.GPU
	if r.gpus == nil {
		c, warns := gpu.Open(ctx)
		r.gpus, r.ownGPU = c, true
		for _, w := range warns {
			r.warn("%s", w)
		}
	}
	devices, err := r.gpus.Devices(ctx)
	if err != nil {
		r.warn("GPU inventory unreadable, device list empty")
		return
	}
	r.host.GPUs = devices
}

// collectPlacement replays llama.cpp's placement rules over the tensor
// headers and the observed flags. Without a header there is nothing to
// replay, and the summary says so rather than guessing a breakdown.
func (r *run) collectPlacement() {
	if len(r.tensors) == 0 {
		r.place = tape.PlacementSummary{Source: placement.SourceUnknown}
		return
	}
	// lazy: no observable flag says whether the server loads the n-gram /
	// engram tables lazily, so it is not claimed. See the report.
	//
	// WithModel is what lets each device say what it is read FOR on one token
	// (TTP-68, 2026-09-14). The expert counts come off the GGUF metadata, which
	// only exists here, and collectModel runs before this, so r.model is
	// already filled. Without it the per-device field stays 0 and a reader
	// falls back to the class-proportion estimate that under-counts a sparse
	// MoE's router and shared expert.
	sum, warns := placement.EstimateVerbose(r.tensors, r.flags, len(r.host.GPUs), false,
		placement.WithModel(r.model))
	r.place = sum
	for _, w := range warns {
		r.warn("%s", w)
	}
}

// collectTemplate records what is actually being sent (handover lesson 4:
// the chat template decides what is being measured). The rendered prompt of
// every stream is fetched so the record carries it; the run's TemplateInfo
// describes the first stream.
func (r *run) collectTemplate(ctx context.Context, reqs []server.StreamRequest) {
	r.template.ChatTemplate = r.props.ChatTemplate
	if len(reqs) == 0 {
		return
	}
	failed, templated := 0, 0
	for i := range reqs {
		// A raw /completion request has no template to apply (TTP-55): its
		// prompt is already what the model sees, and asking the server to
		// render it would record a prompt that was never sent.
		if reqs[i].IsCompletion() {
			reqs[i].RenderedPrompt = reqs[i].Prompt
			continue
		}
		templated++
		rendered, err := r.client.ApplyTemplate(ctx, reqs[i].Messages)
		if err != nil {
			failed++
			continue
		}
		reqs[i].RenderedPrompt = rendered
	}
	if failed > 0 {
		r.warn("/apply-template failed for %d/%d streams, prompt unknown", failed, templated)
	}
	first := reqs[0].RenderedPrompt
	if first != "" {
		sum := sha256.Sum256([]byte(first))
		r.template.RenderedPromptSHA256 = hex.EncodeToString(sum[:])
		r.template.RenderedHasThinkClose = strings.Contains(first, "</think>")
	}
	if v, ok := reqs[0].Params["reasoning_effort"].(string); ok {
		r.template.ReasoningEffort = v
	}
	if kw, ok := reqs[0].Params["chat_template_kwargs"].(map[string]any); ok && len(kw) > 0 {
		r.template.TemplateKwargs = make(map[string]string, len(kw))
		for k, v := range kw {
			r.template.TemplateKwargs[k] = fmt.Sprint(v)
		}
	}
}

// stream opens the sampler, starts the periodic host reader, sends every
// request at once under the run's clock, and stops the reader again.
//
// The clock cancels a context derived from ctx, never ctx itself: the sampler
// keeps its own, so a cut run still takes its last host reading, and a
// cancellation that came from the caller stays distinguishable from one this
// run made.
func (r *run) stream(ctx context.Context, reqs []server.StreamRequest) ([]tape.RequestRecord, *state, error) {
	sampler, closeSampler := r.openSampler()
	defer closeSampler()

	st := newState(len(reqs), sampler, r.opts.Progress)
	for i := range reqs {
		st.beginStream(i)
		st.notify(Event{
			Kind:    EventStreamStarted,
			Stream:  i,
			Streams: len(reqs),
			// The cap this request will be sent with, so the live screen can
			// show progress against it from the first token. It is the same
			// figure the tape keeps in PromptRecord.MaxTokens.
			MaxTokens: reqs[i].SentMaxTokens(),
		})
	}

	stopSampling := r.startSampling(ctx, st)
	r.observe(0, 0, witnessStart)
	origin := time.Now() // the origin RunConcurrent stamps StartedAt against
	runCtx, clk := r.startClock(ctx, st, origin)
	recs, err := server.RunConcurrent(runCtx, r.client, reqs, st.hooks)
	clk.stop()
	r.observe(time.Since(origin), 0, witnessEnd)
	stopSampling()

	if recs == nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrAllStreamsFailed, err)
	}
	// The clock's cut is applied before the run is judged: cutting every live
	// stream makes RunConcurrent report that all of them failed, and a run
	// ended by its own budget is not a failed run.
	if at, live := clk.cut(); at > 0 {
		if markCut(recs, live) > 0 {
			r.cutAt = at
		}
	}
	if allFailed(recs) {
		if err == nil {
			err = errors.New(recs[0].Error)
		}
		return nil, nil, fmt.Errorf("%w: %v", ErrAllStreamsFailed, err)
	}
	st.applyDeltas(recs)
	return recs, st, nil
}

// openSampler opens the process sampler when the server process is local, and
// returns the function that closes it again. The sampler is nil, and the
// closer a no-op, when there is no /proc view.
func (r *run) openSampler() (*procmon.Sampler, func()) {
	if r.pid <= 0 {
		return nil, func() {}
	}
	s, err := procmon.NewSamplerAt(r.opts.FSRoot, r.pid)
	if err != nil {
		r.warn("process sampler unavailable, no memory or fault series")
		return nil, func() {}
	}
	// The first delta has nothing to subtract from. Latching here, before
	// anything is sent, is what makes the first token's delta the prompt
	// phase (procmon.Sampler.FaultDelta doc).
	_, _, _ = s.FaultDelta()
	return s, func() { s.Close() }
}

// startSampling starts the periodic host reader and returns the function that
// stops it and waits for its last reading.
func (r *run) startSampling(ctx context.Context, st *state) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	dep := sampleDeps{
		interval: r.opts.SampleInterval,
		runStart: time.Now(),
		fsRoot:   r.opts.FSRoot,
		pid:      r.pid,
		gpus:     r.gpus,
		client:   r.client,
		pollSlot: r.props.TotalSlots > 0,
	}
	go func() {
		defer close(done)
		st.sampleLoop(ctx, stop, dep)
	}()
	return func() {
		close(stop)
		<-done
	}
}

// serverThreads is the server's own thread count from its -t flag, or 0 when
// it was not observed. It is the better base for the contention verdict
// (internal/gpu Contention doc).
func serverThreads(flags tape.ServerFlags) int {
	n, err := strconv.Atoi(strings.TrimSpace(flags.Threads))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
