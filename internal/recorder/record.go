package recorder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
}

// Record performs one run end to end: attach, collect the static picture,
// send the streams, watch the host, and reduce everything into a tape.
//
// Every collector degrades into a warning. Only two conditions fail the run:
// a server that cannot be reached (ErrUnreachable) and a run in which no
// stream produced a record (ErrAllStreamsFailed).
func Record(ctx context.Context, opts Options) (*tape.Tape, error) {
	opts = opts.normalize()
	r := &run{opts: opts}

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
	}
	s.Server.Build, s.Server.Commit = server.BuildFromProps(r.props.BuildInfo)
	r.emit(Event{Kind: EventAttached, Stream: -1, Summary: s})
}

func (r *run) emit(ev Event) {
	if r.opts.Progress != nil {
		ev.Streams = r.opts.Concurrency
		r.opts.Progress(ev)
	}
}

// attach finds the server and reads /props. This is the only step that can
// fail the run outright.
func (r *run) attach(ctx context.Context) error {
	c := server.New(r.opts.BaseURL)
	if r.opts.BaseURL == "" {
		url, err := c.Discover(ctx, r.opts.Candidates)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrUnreachable, err)
		}
		c = c.WithBaseURL(url)
	}
	props, err := c.Props(ctx)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrUnreachable, c.BaseURL(), err)
	}
	r.client, r.props = c, props
	r.kind = server.DetectKind(props)
	r.emit(Event{Kind: EventDiscovered, Stream: -1, Message: c.BaseURL()})
	build, _ := server.BuildFromProps(props.BuildInfo)
	r.emit(Event{Kind: EventProps, Stream: -1, Message: build})
	return nil
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
		return
	}
	// A warning is printed verbatim on the card, so it is a short human
	// sentence and never a wrapped Go error chain: four lines of
	// "open ...: no such file" would crowd out the figures the card exists
	// to show. The path itself is on the tape in Model.Path.
	r.warn("model file not readable here (%s), model shape and placement unknown", base)
	r.model = tape.ModelInfo{
		Path:     path,
		FileName: base,
		Quant:    placement.QuantFromFileName(base),
	}
}

// collectProcess finds the local server process and reads its argv, which is
// where every flag the card prints comes from.
func (r *run) collectProcess() {
	if r.props.ModelPath == "" {
		return
	}
	pid, err := procmon.FindPID(r.opts.FSRoot, r.props.ModelPath)
	if err != nil {
		r.warn("pid not found, no /proc view: memory, page faults and flags unavailable")
		r.emit(Event{Kind: EventPIDNotFound, Stream: -1})
		return
	}
	r.pid = pid
	r.emit(Event{Kind: EventPIDFound, Stream: -1, Message: strconv.Itoa(pid)})

	args, err := procmon.Args(r.opts.FSRoot, pid)
	if err != nil {
		r.warn("argv of pid %d unreadable, server flags unknown", pid)
		return
	}
	r.args = args
	r.flags = server.ParseFlags(args)
}

// collectHost reads the hardware line and opens the GPU backend.
func (r *run) collectHost(ctx context.Context) {
	host, err := procmon.HostInfo(r.opts.FSRoot)
	if err != nil {
		r.warn("/proc not readable, CPU, RAM and kernel unknown")
	}
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
	sum, warns := placement.EstimateVerbose(r.tensors, r.flags, len(r.host.GPUs), false)
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
	failed := 0
	for i := range reqs {
		rendered, err := r.client.ApplyTemplate(ctx, reqs[i].Messages)
		if err != nil {
			failed++
			continue
		}
		reqs[i].RenderedPrompt = rendered
	}
	if failed > 0 {
		r.warn("/apply-template failed for %d of %d streams: rendered prompt and </think> presence unknown", failed, len(reqs))
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
// request at once and stops the reader again.
func (r *run) stream(ctx context.Context, reqs []server.StreamRequest) ([]tape.RequestRecord, *state, error) {
	var sampler *procmon.Sampler
	if r.pid > 0 {
		s, err := procmon.NewSamplerAt(r.opts.FSRoot, r.pid)
		if err != nil {
			r.warn("process sampler for pid %d unavailable, no memory or page fault series", r.pid)
		} else {
			sampler = s
			defer sampler.Close()
			// The first delta has nothing to subtract from. Latching here,
			// before anything is sent, is what makes the first token's delta
			// the prompt phase (procmon.Sampler.FaultDelta doc).
			_, _, _ = sampler.FaultDelta()
		}
	}

	st := newState(len(reqs), sampler, r.opts.Progress)
	for i := range reqs {
		st.notify(Event{Kind: EventStreamStarted, Stream: i, Streams: len(reqs)})
	}

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

	recs, err := server.RunConcurrent(ctx, r.client, reqs, st.hooks)
	close(stop)
	<-done

	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrAllStreamsFailed, err)
	}
	st.applyDeltas(recs)
	return recs, st, nil
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
