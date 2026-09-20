package recorder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/placement"
	"github.com/midagedev/toktape/internal/placement/gguf"
	"github.com/midagedev/toktape/internal/procmon"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// run carries one recording's mutable state through the collection steps.
type run struct {
	opts     Options
	warnings []string
	client   *server.Client

	props  *server.Props
	kind   tape.ServerKind
	build  string // from /props (attach)
	commit string // from /props, else the ik checkout (collectProcess, TTP-33)
	// openaiModel is the first /v1/models id the server listed (TTP-99): the
	// model name the server itself gave, which fills ModelInfo.FileName and
	// defaults the requests' model. "" when the server listed nothing.
	openaiModel string
	model       tape.ModelInfo
	tensors     []placement.Tensor
	pid         int
	args        []string
	flags       tape.ServerFlags
	host        tape.HostInfo
	gpus        gpu.Collector
	ownGPU      bool
	place       tape.PlacementSummary
	template    tape.TemplateInfo
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
	// promptSet is the published set every request came from, "" when they
	// did not all come from one (TTP-112). Read where the requests are built
	// because that is the only place that knows.
	promptSet string
	// planSalt is the line this run put in front of every set prompt
	// (lead, 2026-09-20), "" when none was put; recorded as
	// RunSummary.PromptSalt so a verifier strips exactly the bytes sent.
	planSalt string
	// planTrimTokens is the token length every set prompt was cut to, 0
	// when they went whole; recorded as RunSummary.PromptTrimTokens.
	planTrimTokens int
	// planTokenized says the set's lengths were counted by the server's
	// /tokenize rather than priced from bytes.
	planTokenized bool
	// runPlan is the shape the run decided before its first request — the
	// target, which limit set it, and the figures it was set against
	// (lead, 2026-09-20). nil on a run that planned nothing (a user's own
	// prompts); LongestPromptTokens is filled by the slot-cap step, which
	// is where the answer cap meets the prompts as sent. Named runPlan,
	// not plan, because plan is the method that decides it.
	runPlan *tape.RunPlan
	// prefill is the prefill measurement pass's own figures (TTP-137), nil
	// when the run did not make one. Named for the pass, not the schema
	// type, because run already has a probe method — the attach gate.
	// It is reduced into the summary whole; the run's requests never see it.
	prefill *tape.ProbeSummary
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
	switch opts.EngineKind {
	case "", "auto", "llama", "openai":
	default:
		return nil, fmt.Errorf("recorder: --engine-kind %q: use auto, llama or openai", opts.EngineKind)
	}
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
	if err := r.checkSlots(); err != nil {
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

	// The prefill probe pass (TTP-137): two prompts the set never used,
	// before the first run request, so the run's own timeline and the
	// sampler's fault baseline start clean after it. It runs before the
	// requests are built because its fit is one of the ceilings the run
	// plan takes (2026-09-20, plan.go) — the measurement has to exist
	// before the thing it measures for.
	r.prefillProbe(ctx)

	reqs := buildRequests(opts, r.model.ActiveBytesPerToken)
	// The run plan: one owner for the salt, the prompt length and the
	// answer cap, decided together against the probe's fit and the slot's
	// context before the first request goes out (lead, 2026-09-20).
	if err := r.plan(ctx, reqs); err != nil {
		return nil, err
	}
	if shaped, err := r.shapeOpenAIRequests(reqs); err != nil {
		return nil, err
	} else {
		reqs = shaped
	}
	r.promptSet = promptSetOf(reqs)
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
	if r.kind == tape.ServerOpenAI {
		s.Server.EngineClaim = r.opts.EngineClaim
	}
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
			// probeOpenAI already stamped r.kind: an empty synthesized Props
			// would DetectKind as ServerUnknown, and there is no build to
			// split — the build stays "" and prints "?".
			if r.kind != tape.ServerOpenAI {
				r.kind = server.DetectKind(props)
				r.build, r.commit = server.BuildFromProps(props.BuildInfo)
				// An engine that named itself reports its build in its version,
				// verbatim, with no commit to name: an engine version is not a
				// bNNNN counter, and the engine object is the record over any
				// build_info a shim keeps (2026-09-15).
				if props.Engine != nil && r.kind.SelfDeclared() {
					r.build, r.commit = props.Engine.Version, ""
				}
			}
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

// ErrMoreSessionsThanSlots is returned when the run asks for more streams at
// once than the server's /props says it has slots (2026-09-14). The error is a
// *SlotsError carrying both numbers. The CLI maps it to exit code 1: the
// invocation asked for something the server cannot be measured doing.
//
// Streams past the slot count do not measure more concurrency. They wait in
// the server's queue, and that wait lands in TTFT and in the aggregate as if
// the box were slow — a figure the card cannot tell apart from slowness. So
// the run is refused before a request is sent, rather than recorded and
// qualified afterwards.
var ErrMoreSessionsThanSlots = errors.New("recorder: more sessions than server slots")

// SlotsError is ErrMoreSessionsThanSlots with the numbers in it.
type SlotsError struct {
	// Sessions is the streams the run would have sent at once.
	Sessions int
	// Slots is the server's total_slots, always > 0: a server that did not
	// say is never refused.
	Slots int
	// URL is the server that said it.
	URL string
}

func (e *SlotsError) Error() string {
	return fmt.Sprintf("%d sessions asked of %s, which offers %d slots; the %d past its slots would wait in its queue, and the card cannot tell queue wait from slowness",
		e.Sessions, e.URL, e.Slots, e.Sessions-e.Slots)
}

// Is makes errors.Is(err, ErrMoreSessionsThanSlots) true for a *SlotsError.
func (e *SlotsError) Is(target error) bool { return target == ErrMoreSessionsThanSlots }

// checkSlots refuses a run that would send more streams than the server has
// slots.
//
// A server whose /props carried no total_slots has TotalSlots 0, which is
// unknown and not zero: there is no limit to enforce, so nothing is refused
// (the repo's first rule — never act on a figure that was not observed).
func (r *run) checkSlots() error {
	// An OpenAI-compatible server numbers no slots (TTP-99): TotalSlots 0 is
	// unknown, never a limit, so no run is refused on it.
	if r.kind == tape.ServerOpenAI {
		return nil
	}
	slots := r.props.TotalSlots
	if slots <= 0 || r.opts.Concurrency <= slots {
		return nil
	}
	return &SlotsError{Sessions: r.opts.Concurrency, Slots: slots, URL: r.client.BaseURL()}
}

// shapeOpenAIRequests adapts built requests to an OpenAI-compatible server
// (TTP-99): the protocol selects the minimal body, and the model defaults to
// the first /v1/models id when the user gave none — these servers require
// one. The raw /completion path is refused: it is llama-server's own
// endpoint, and an OpenAI-compatible server has /v1/chat/completions only.
// On any other kind the requests pass through untouched.
func (r *run) shapeOpenAIRequests(reqs []server.StreamRequest) ([]server.StreamRequest, error) {
	if r.kind != tape.ServerOpenAI {
		return reqs, nil
	}
	for i := range reqs {
		if reqs[i].IsCompletion() {
			return nil, errors.New("--endpoint completion is llama-server's raw endpoint; an OpenAI-compatible server has /v1/chat/completions only")
		}
		reqs[i].Protocol = tape.ServerOpenAI
		if reqs[i].Model == "" {
			reqs[i].Model = r.openaiModel
		}
	}
	return reqs, nil
}

// probe is one attach attempt: discovery when no URL was given, then /props.
//
// Discovery is redone on every attempt rather than once, because a server that
// opens its port late is found by scanning the candidates again — re-polling a
// URL that discovery never produced would wait forever on nothing.
//
// The engine kind selects the gate (TTP-99): "llama" is /props exactly;
// "openai" skips /props and "auto" tries /props first and falls back to
// /v1/models on ErrNoProps only — never on a refusal, never while loading.
func (r *run) probe(ctx context.Context) (*server.Client, *server.Props, error) {
	c := server.New(r.opts.BaseURL)
	if r.opts.BaseURL == "" {
		url, err := c.Discover(ctx, r.opts.Candidates)
		if err != nil {
			return nil, nil, err
		}
		c = c.WithBaseURL(url)
	}
	if r.opts.EngineKind == "openai" {
		return r.probeOpenAI(ctx, c)
	}
	props, err := c.Props(ctx)
	if err != nil {
		if (r.opts.EngineKind == "" || r.opts.EngineKind == "auto") && errors.Is(err, server.ErrNoProps) {
			return r.probeOpenAI(ctx, c)
		}
		return nil, nil, err
	}
	return c, props, nil
}

// probeOpenAI attaches the generic OpenAI-compatible mode (TTP-99):
// /v1/models is the gate. The stored Props is synthesized so nothing else
// nil-derefs — Raw empty, Engine nil, TotalSlots 0, which is slots unknown
// and never a limit — and r.kind is stamped here, because an empty Props
// would DetectKind as ServerUnknown.
func (r *run) probeOpenAI(ctx context.Context, c *server.Client) (*server.Client, *server.Props, error) {
	models, err := c.Models(ctx)
	if err != nil {
		return nil, nil, err
	}
	r.kind = tape.ServerOpenAI
	r.openaiModel = models.FirstID()
	return c, &server.Props{}, nil
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

// collectModel records the model's shape, then where it came from. The shape
// has two sources and the provenance has a third, which is why they are two
// steps: the path is evidence no header carries, and it is there whether the
// header was readable, unreadable, or never a GGUF at all (TTP-119).
func (r *run) collectModel() {
	r.collectModelShape()
	if repo := hfCacheRepo(r.model.Path); repo != "" {
		r.model.Repo, r.model.RepoSource = repo, "hf-cache"
	}
}

// hfCacheRepo returns the Hugging Face repo id a path proves, or "" for a
// path that proves nothing. The layout is the one `huggingface_hub` writes:
//
//	<cache>/hub/models--<org>--<repo>/snapshots/<sha>/<file>
//
// The `snapshots` component is required, not decoration: it is what separates
// a real cache entry from a directory somebody happened to name that way, and
// it is also where an exl3 model's own directory lives, so this reads a
// non-GGUF run exactly as well as a GGUF one.
//
// A name that splits into anything but two non-empty halves is refused. A
// repo whose own name contains "--" would arrive here indistinguishable from
// an org that does, and a wrong repo id is worse than none: Repo is read as
// proof.
func hfCacheRepo(path string) string {
	if path == "" {
		return ""
	}
	// The path is the *server's*, not this machine's, so the separator cannot
	// be filepath.Separator: a laptop on macOS attached with --url to a
	// Windows server reads a Windows path. filepath.ToSlash is a no-op off
	// Windows for exactly that reason, so both separators are split on here.
	// A POSIX file name may legally contain a backslash; the models--/snapshots
	// pair either side of it makes that harmless.
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	for i, p := range parts {
		if !strings.HasPrefix(p, "models--") || i+1 >= len(parts) || parts[i+1] != "snapshots" {
			continue
		}
		half := strings.Split(strings.TrimPrefix(p, "models--"), "--")
		if len(half) != 2 || half[0] == "" || half[1] == "" {
			return ""
		}
		return half[0] + "/" + half[1]
	}
	return ""
}

// collectModelShape reads the GGUF header when the model file is on this host.
// When it is not — the normal case for a remote server — the file name and
// the quantisation are still recoverable from the path /props reported, and
// everything the header would have supplied stays zero.
//
// An engine block short-circuits all of that (2026-09-15, ExLlamaV3): an
// engine that is not llama.cpp has no GGUF to open and reports the model in
// its own words, which is the record — there is no file to fail to read and
// no warning to print about one.
func (r *run) collectModelShape() {
	if r.props.Engine != nil {
		r.collectEngineModel()
		return
	}
	// An OpenAI-compatible server names its model in /v1/models and nothing
	// else (TTP-99): the first id is an observation, so it fills FileName —
	// but not Path (never observed) and not Repo (a bare id proves no cache
	// layout). No header is opened and no warning is printed about one.
	if !r.kind.SpeaksLlamaProtocol() {
		r.model = tape.ModelInfo{FileName: r.openaiModel}
		return
	}
	path := r.props.ModelPath
	if path == "" {
		r.warn("server did not report model_path, model shape unknown")
		return
	}
	base := filepath.Base(path)
	mi, tensors, err := gguf.ModelInfo(path)
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
	// The name is the whole of what a remote run has to identify the model
	// with, and reading the convention out of it is observing rather than
	// guessing — the parser refuses a name that does not follow it. This is
	// the branch a laptop attached with --url takes, so it is the one most
	// published runs will come through (TTP-119).
	if b, s := gguf.NameFromFileName(base); b != "" {
		r.model.BaseName, r.model.SizeLabel, r.model.NameSource = b, s, "filename"
	}
	// The parts are not on this machine either, so they are named but never
	// stat'ed: FileBytes stays 0 and the card prints "?" rather than the size
	// of a file it could not see. The warning above already says the file was
	// unreadable, and a second one about the other eight parts would only
	// crowd the card.
	r.fillShardSet(path, false)
}

// collectEngineModel records the model the engine block reported, verbatim:
// every key it did not send stays 0 and prints "?", exactly as an unobserved
// key always has. Name stays "" so the card falls back to the directory name
// the path ends in, and Shards stays 0 — the engine's Files count is its own,
// and a split-GGUF naming scheme does not apply to it.
func (r *run) collectEngineModel() {
	m := r.props.Engine.Model
	fileName := ""
	if path := r.props.ModelPath; path != "" {
		fileName = filepath.Base(path)
	}
	r.model = tape.ModelInfo{
		Path:                r.props.ModelPath,
		FileName:            fileName,
		Format:              m.Format,
		Arch:                m.Arch,
		Quant:               m.Quant,
		FileBytes:           m.Bytes,
		Params:              m.Params,
		NLayers:             m.NLayers,
		NExperts:            m.NExperts,
		NExpertsUsed:        m.NExpertsUsed,
		CtxTrain:            m.CtxTrain,
		ActiveBytesPerToken: m.ActiveBytesPerToken,
	}
}

// collectProcess finds the local server process and reads its argv, which is
// where every flag the card prints comes from. The process also settles the
// engine /props may not have named, and an ik_llama.cpp server's commit.
//
// An engine run's flags are the engine block's own argv, recorded before the
// PID search so a run whose process was never found still has them: the
// engine object is the record, and server.ParseFlags — which knows
// llama.cpp's argument starters — would misread every engine flag
// (2026-09-15; any engine that named itself, TTP-105 2026-09-17). The
// process's real argv is still recorded verbatim whenever the pid is found;
// it is simply not parsed.
func (r *run) collectProcess() {
	// An OpenAI-compatible server has no model_path in any argv to find it
	// by (TTP-99), and guessing a pid from a listening socket would name a
	// process nobody proved is the server. PID 0, no argv, zero flags — and
	// the card omits the flags block on this kind.
	if !r.kind.SpeaksLlamaProtocol() {
		r.pid, r.args, r.flags = 0, nil, tape.ServerFlags{}
		r.warn("pid not found: no memory, page faults or flags")
		r.emit(Event{Kind: EventPIDNotFound, Stream: -1})
		return
	}
	if r.kind.SelfDeclared() && r.props.Engine != nil {
		r.flags = tape.ServerFlags{Other: r.props.Engine.Args}
		if d := r.props.Engine.Draft; d != nil {
			r.flags.DraftModel = d.Model
			if d.NMax > 0 {
				r.flags.DraftMax = strconv.Itoa(d.NMax)
			}
		}
	}
	pid := 0
	// A declared pid outranks both searches below (TTP-107, 2026-09-17).
	//
	// Both of them name the process toktape is talking to — an argv that holds
	// the model path, or the holder of the listening socket — and for a server
	// that fronts another process that is the front. Every figure a pid
	// produces then describes the proxy: memory, page faults, and the GPU
	// processes counted as "not ours", which is how eight mistral.rs takes on
	// a box under a single lease all read "contended: yes" while their ik
	// pairs from the same sweep read "contended: no".
	//
	// A pid this host cannot see is not trusted quietly: the run says so and
	// searches anyway, because a wrong subject and a missing one are different
	// failures and only one of them is silent.
	if declared := r.props.ServerPID(); declared > 0 {
		switch {
		case procmon.Exists(r.opts.FSRoot, declared):
			pid = declared
		case r.pidReadable():
			r.warn("server declared pid %d, which is not running here; searching for it instead", declared)
		}
	}
	if pid == 0 && r.props.ModelPath != "" {
		if p, err := procmon.FindPID(r.opts.FSRoot, r.props.ModelPath); err == nil {
			pid = p
		}
	}
	if pid == 0 {
		// No model path named a process — for an engine there never is one.
		// The listening socket still names its holder, and for a loopback URL
		// that is a process this /proc can read. A port on another host names
		// a process over there, so only loopback asks (2026-09-15).
		if p, err := r.findPIDByURLPort(); err == nil {
			pid = p
		}
	}
	if pid == 0 {
		r.warn("pid not found: no memory, page faults or flags")
		r.emit(Event{Kind: EventPIDNotFound, Stream: -1})
		return
	}
	r.pid = pid
	r.emit(Event{Kind: EventPIDFound, Stream: -1, Message: strconv.Itoa(pid)})

	args, err := procmon.Args(r.opts.FSRoot, pid)
	if err != nil {
		// For llama.cpp the argv is where every flag comes from, so losing it
		// loses the flags. An engine that named itself already has its flags
		// from the engine block; only the verbatim argv line is missing here.
		if r.kind.SelfDeclared() {
			r.warn("argv of pid %d unreadable", pid)
		} else {
			r.warn("argv of pid %d unreadable, server flags unknown", pid)
		}
	} else {
		r.args = args
		if !r.kind.SelfDeclared() {
			r.flags = server.ParseFlags(args)
		}
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

// findPIDByURLPort is the generic pid fallback: the pid holding the socket
// that listens on the server URL's port (procmon.FindPIDByPort). Only a
// loopback URL asks — a port on another host names a process over there, not
// one this /proc can read — and it never overrides a successful model-path
// match, because the model path is the surer identification when there is
// one (2026-09-15, ExLlamaV3).
func (r *run) findPIDByURLPort() (int, error) {
	port, err := r.loopbackPort()
	if err != nil {
		return 0, err
	}
	return procmon.FindPIDByPort(r.opts.FSRoot, port)
}

// loopbackPort is the server URL's port when the server runs on this host,
// and an error saying why not otherwise.
func (r *run) loopbackPort() (int, error) {
	u, err := url.Parse(r.client.BaseURL())
	if err != nil {
		return 0, err
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
	default:
		return 0, fmt.Errorf("recorder: %s is not a loopback host", u.Hostname())
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 {
		return 0, fmt.Errorf("recorder: no port in %s", r.client.BaseURL())
	}
	return port, nil
}

// pidReadable reports whether this run could have looked a process up at all:
// the server has to be on this host, and this host has to have a procfs to
// look in. It is what separates "the server named a pid that is not there"
// from "the server named a pid I was never in a position to see" — a remote
// attach, or any macOS or Windows run (TTP-107, 2026-09-17). The first is
// worth a line on the card; the second is not the declaration's fault, and a
// run with no /proc already says once that it found no pid.
func (r *run) pidReadable() bool {
	if _, err := r.loopbackPort(); err != nil {
		return false
	}
	return procmon.HasProc(r.opts.FSRoot)
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
	// The one place the name is replaced. record.go:137 and reduce.go:74 copy
	// r.host.Hostname onward, so replacing it here covers them and any copy
	// added later; replacing it at each copy site would not (TTP-93).
	r.opts.HostLabel.apply(&host)
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
//
// An engine block is taken instead of any replay (2026-09-15, ExLlamaV3):
// the engine knows where it put each tensor, and its figures are the record.
func (r *run) collectPlacement() {
	if r.props.Engine != nil {
		r.place = r.enginePlacement()
		return
	}
	// An OpenAI-compatible server reports no model shape to replay over
	// (TTP-99): the placement is unknown rather than guessed.
	if !r.kind.SpeaksLlamaProtocol() {
		r.place = tape.PlacementSummary{Source: placement.SourceUnknown}
		return
	}
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

// enginePlacement records the placement the engine block reported, in the
// engine's own words: device order verbatim, bytes per device, classes
// summed into the tape's vocabulary. Three checks run over it, and each one
// is a warning plus the honest figure — never a correction, because the
// server's figures are the record:
//
//   - a device whose bytes are not the sum of its classes keeps its bytes;
//   - per-device active bytes: NO device sending them is the normal case —
//     an engine that swaps experts between devices on the fly has no static
//     answer — and keeps the model's figure without a word; a partial or
//     inconsistent set zeroes every active figure, model included, so no
//     bandwidth line is ever computed from a bad split;
//   - a GPU<n> the host probe did not name is kept and said, since the card's
//     other rows name that same device.
func (r *run) enginePlacement() tape.PlacementSummary {
	ep := r.props.Engine.Placement
	sum := tape.PlacementSummary{Source: placement.SourceEngine}
	nActive, sumActive := 0, int64(0)
	for _, d := range ep.Devices {
		dev := tape.DevicePlacement{
			Device:              d.Device,
			Bytes:               d.Bytes,
			Layers:              d.Layers,
			Classes:             map[tape.TensorClass]int64{},
			ActiveBytesPerToken: d.ActiveBytesPerToken,
		}
		var classSum int64
		for k, v := range d.Classes {
			dev.Classes[tensorClass(k)] += v
			classSum += v
		}
		if d.Bytes != classSum {
			r.warn("%s reports %s over classes summing %s; the device's own figure is kept",
				d.Device, humanBytes(d.Bytes), humanBytes(classSum))
		}
		if d.ActiveBytesPerToken > 0 {
			nActive++
			sumActive += d.ActiveBytesPerToken
		}
		// The probe's list is the only ground truth toktape has for what
		// GPUs exist; with no list at all there is already a warning about
		// that, and one per device would only crowd it.
		if n, ok := gpuIndex(d.Device); ok && len(r.host.GPUs) > 0 && (n < 0 || n >= len(r.host.GPUs)) {
			r.warn("%s is not among this host's %d GPUs; the device is kept as the engine reported it",
				d.Device, len(r.host.GPUs))
		}
		sum.Devices = append(sum.Devices, dev)
		if d.Device != tape.DeviceCPU {
			sum.VRAMWeightsBytes += d.Bytes
		}
	}
	sum.VRAMKVBytes = ep.VRAMKVBytes

	// The per-device active split, when the engine sends one at all, must be
	// whole (every device) and agree with the model's figure within 1%.
	model := r.props.Engine.Model.ActiveBytesPerToken
	if nActive > 0 {
		consistent := nActive == len(ep.Devices) && model > 0 &&
			abs64(sumActive-model)*100 <= model
		if !consistent {
			for i := range sum.Devices {
				sum.Devices[i].ActiveBytesPerToken = 0
			}
			// The model's figure goes too, here and now: reduce reads it for
			// the per-record bandwidth, so a bad split must not survive into
			// that line either.
			r.model.ActiveBytesPerToken = 0
			r.warn("per-device active bytes (%d of %d devices, %s) disagree with the model's %s; all active figures zeroed",
				nActive, len(ep.Devices), humanBytes(sumActive), humanBytes(model))
		}
	}
	return sum
}

// tensorClass maps an engine placement's class key onto the tape's class
// values. A key outside the vocabulary is not dropped: its bytes are real
// wherever the engine filed them, so they sum into ClassOther.
func tensorClass(key string) tape.TensorClass {
	switch key {
	case "attention":
		return tape.ClassAttention
	case "experts":
		return tape.ClassExperts
	case "ffn":
		return tape.ClassFFN
	case "embeddings":
		return tape.ClassEmbed
	case "output":
		return tape.ClassOutput
	case "ngram":
		return tape.ClassNGram
	default:
		return tape.ClassOther
	}
}

// gpuIndex reads the n of a "GPU<n>" device name, which is the nvidia-smi
// index the engine contract says it is. ok is false for the CPU and for any
// other spelling.
func gpuIndex(device string) (n int, ok bool) {
	if !strings.HasPrefix(device, "GPU") || len(device) <= len("GPU") {
		return 0, false
	}
	n, err := strconv.Atoi(device[len("GPU"):])
	return n, err == nil
}

// abs64 is |a| for the tolerance check above.
func abs64(a int64) int64 {
	if a < 0 {
		return -a
	}
	return a
}

// humanBytes sizes a byte count the way the card does, for warnings that
// name figures next to figures.
func humanBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// collectTemplate records what is actually being sent (handover lesson 4:
// the chat template decides what is being measured). The rendered prompt of
// every stream is fetched so the record carries it; the run's TemplateInfo
// describes the first stream.
func (r *run) collectTemplate(ctx context.Context, reqs []server.StreamRequest) {
	// An OpenAI-compatible server has no /apply-template (TTP-99): the chat
	// template is the server's own business, so the run records none and
	// asks for none. RenderedPrompt stays "" — unknown, never a guess.
	if !r.kind.SpeaksLlamaProtocol() {
		return
	}
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
	r.warnTreeProcesses(st)

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
//
// An engine that named itself watches the whole process tree (2026-09-15,
// ExLlamaV3; any self-declared engine, TTP-105 2026-09-17). A tree sampler
// over a single-process server sums exactly that one process, so it is never
// a worse number than the single-pid sampler, only a slower one; and where
// the pid that was found fronts the real work — a multiprocessing engine,
// or a shim holding the port — the tree is the only sampler that sees the
// work at all. llama-server and ik are not self-declared and keep the
// single-process sampler byte-for-byte; that measured path does not move.
// This does not close TTP-107: a shim's tree is still not the server's tree
// when the server is not its child, and the fix for that is the server
// declaring its own pid.
func (r *run) openSampler() (procmon.FaultSampler, func()) {
	s, err := r.newFaultSampler()
	if err != nil {
		r.warn("process sampler unavailable, no memory or fault series")
		return nil, func() {}
	}
	if s == nil {
		return nil, func() {}
	}
	return s, func() { s.Close() }
}

// newFaultSampler opens a latched process sampler without the run's warning
// behaviour, for callers whose silence is part of their contract: the probe
// pass never warns, so it cannot go through openSampler's "sampler
// unavailable" sentence. A nil sampler with a nil error means no /proc view
// — nothing to observe, nothing to say (TTP-143, 2026-09-19).
//
// The latch is here rather than at each caller because both callers need the
// same property: the first delta after this call is the first thing that
// happened after it. For the run that makes the first token's delta the
// prompt phase; for the probe it makes the pass's figure the pass's own
// cost, the faults of loading the weights included, which is the whole
// reason the pass samples at all.
func (r *run) newFaultSampler() (procmon.FaultSampler, error) {
	if r.pid <= 0 {
		return nil, nil
	}
	var s procmon.FaultSampler
	var err error
	if r.kind.SelfDeclared() {
		s, err = procmon.NewTreeSamplerAt(r.opts.FSRoot, r.pid)
	} else {
		s, err = procmon.NewSamplerAt(r.opts.FSRoot, r.pid)
	}
	if err != nil {
		return nil, err
	}
	// The first delta has nothing to subtract from. Latching here, before
	// anything is sent, is what makes the first delta the phase that
	// followed this call (procmon.Sampler.FaultDelta doc).
	_, _, _ = s.FaultDelta()
	return s, nil
}

// warnTreeProcesses says, once per engine run, that its process figures are
// sums over the engine's process tree rather than one pid (2026-09-15,
// ExLlamaV3). It is emitted after the sampler stops — r.warn calls Progress,
// and Options.Progress promises calls serialised with the sampler's events —
// and only when a reading ever saw more than one process. The sentence stops
// where the honesty does: summed RSS can double-count the file pages both
// processes map, and it does not claim otherwise.
func (r *run) warnTreeProcesses(st *state) {
	if n := st.firstTreeProcs(); n > 1 {
		// Without the pid: the sentence is printed on a card people publish,
		// where a process id of a machine the reader does not have is noise,
		// and the tape carries it anyway as summary.server.pid (lead,
		// 2026-09-15).
		children := "1 child process"
		if n > 2 {
			children = fmt.Sprintf("%d child processes", n-1)
		}
		r.warn("memory, faults and CPU summed over the server and its %s", children)
	}
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
		// Engine runs sum the process tree (openSampler doc); every other
		// kind reads one pid, as they always have.
		tree: r.kind.SelfDeclared(),
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
