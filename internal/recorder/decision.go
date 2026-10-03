package recorder

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/procmon"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// A decision-model run (TTP-192, 2026-10-02): the suite's requests are sent to
// POST /v1/systemone one after another and timed send to the last byte of the
// answer. There is no stream and no token, so nothing here goes through
// Record's attach, plan or sampler; what it shares with Record is the host
// reading, the run id and the tape it hands back.
//
// The run has two phases because they answer different questions. The
// showcase pass is one request at a time with real idle time after each, so a
// replay at 1:1 shows every answer arrive and the idle time is on the tape's
// timestamps and in no latency. The burst phase is the suite repeated back to
// back, and is where throughput is measured.

// The defaults of a decision run.
const (
	// DefaultDecisionGap is the idle time after each showcase answer.
	DefaultDecisionGap = 1200 * time.Millisecond
	// DefaultDecisionRepeats is the burst phase's passes over the suite.
	DefaultDecisionRepeats = 20
	// NoGap and NoBurst are the values that mean "none": zero is the zero
	// value and means the default, the same arrangement as NoWait.
	NoGap   = -1 * time.Nanosecond
	NoBurst = -1
)

//go:embed decision_suite.jsonl
var defaultDecisionSuite []byte

// DecisionOptions configures one decision run.
type DecisionOptions struct {
	// BaseURL is the server. Empty means discover it.
	BaseURL string
	// Candidates are the base URLs discovery probes; nil is
	// server.DefaultCandidates.
	Candidates []string
	// Suite is the JSONL suite and SuiteName its file name; nil Suite is the
	// built-in one and its name is recorded as "".
	Suite     []byte
	SuiteName string
	// Repeats is the burst phase's passes over the suite: 0 is
	// DefaultDecisionRepeats, NoBurst is a showcase-only run.
	Repeats int
	// Concurrency is the burst phase's lanes; 0 or less is one.
	Concurrency int
	// Gap is the idle time after each showcase answer: 0 is
	// DefaultDecisionGap, NoGap is none.
	Gap time.Duration
	// Reference is a JSONL file of the official model's answers to the same
	// cases, ReferenceName its file name; nil means no comparison.
	Reference     []byte
	ReferenceName string

	Tag, Note string
	Version   string
	Clock     Clock
	// FSRoot, GPU and HostLabel are Options' fields of the same names: the
	// host line is read the way a record reads it.
	FSRoot    string
	GPU       gpu.Collector
	HostLabel HostLabel
	// Progress receives each request's record as it finishes. Calls are
	// serialised; it must not block, a slow callback delays the next send
	// on its lane.
	Progress func(tape.DecisionRecord)
}

func (o DecisionOptions) normalize() DecisionOptions {
	if o.Repeats == 0 {
		o.Repeats = DefaultDecisionRepeats
	}
	if o.Repeats < 0 {
		o.Repeats = 0
	}
	if o.Concurrency < 1 {
		o.Concurrency = 1
	}
	switch {
	case o.Gap == 0:
		o.Gap = DefaultDecisionGap
	case o.Gap < 0:
		o.Gap = 0
	}
	if o.Clock == nil {
		o.Clock = systemClock{}
	}
	return o
}

// suiteCase is one suite row ready to send.
type suiteCase struct {
	ID string
	// Body is the row with its "id" key cut out and every other byte kept.
	Body      []byte
	Questions []tape.DecisionQuestion
}

// loadSuite parses a JSONL suite. A row is one request; the optional
// top-level "id" is toktape's own name for it ("case-<n>" when absent) and
// never reaches the server. The hash is of the bodies as sent, each followed
// by a newline, in suite order — so it is the same for two suite files that
// differ only in their ids.
func loadSuite(raw []byte) (cases []suiteCase, sha string, err error) {
	seen := map[string]bool{}
	h := sha256.New()
	for n, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		id, body, err := stripID(line)
		if err != nil {
			return nil, "", fmt.Errorf("suite line %d: %w", n+1, err)
		}
		if id == "" {
			id = fmt.Sprintf("case-%d", len(cases)+1)
		}
		if seen[id] {
			return nil, "", fmt.Errorf("suite line %d: id %q is used twice", n+1, id)
		}
		seen[id] = true
		req, err := server.ParseSystemOneRequest(body)
		if err != nil {
			return nil, "", fmt.Errorf("suite line %d (%s): %w", n+1, id, err)
		}
		cases = append(cases, suiteCase{ID: id, Body: body, Questions: req.Questions})
		h.Write(body)
		h.Write([]byte{'\n'})
	}
	if len(cases) == 0 {
		return nil, "", errors.New("the suite has no requests")
	}
	return cases, hex.EncodeToString(h.Sum(nil)), nil
}

// stripID cuts the top-level "id" member out of one JSON object, by byte
// span, so the other members keep their order, spelling and spacing exactly:
// a request is the record, and a re-encode through a map would sort it.
func stripID(line []byte) (id string, body []byte, err error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", nil, errors.New("not a JSON object")
	}
	prevEnd := int(dec.InputOffset()) // end of the previous member, or just after '{'
	first := true
	cutFrom, cutTo := -1, -1
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return "", nil, err
		}
		key, _ := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return "", nil, err
		}
		end := int(dec.InputOffset())
		if key == "id" {
			if cutFrom >= 0 {
				return "", nil, errors.New(`"id" is written twice`)
			}
			if err := json.Unmarshal(v, &id); err != nil || id == "" {
				return "", nil, errors.New(`"id" must be a non-empty string`)
			}
			if first {
				// Cut from the key to the next member's key, so the comma
				// that separated them goes too.
				cutFrom = skipSpace(line, prevEnd)
				cutTo = end
				if j := skipSpace(line, cutTo); j < len(line) && line[j] == ',' {
					cutTo = skipSpace(line, j+1)
				}
			} else {
				cutFrom, cutTo = prevEnd, end // the leading comma goes with it
			}
		}
		prevEnd, first = end, false
	}
	if _, err := dec.Token(); err != nil {
		return "", nil, err
	}
	if dec.More() {
		return "", nil, errors.New("trailing data after the JSON object")
	}
	if cutFrom < 0 {
		return "", slices.Clone(line), nil
	}
	return id, append(slices.Clone(line[:cutFrom]), line[cutTo:]...), nil
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r' || b[i] == '\n') {
		i++
	}
	return i
}

// loadReference parses the reference file into per-case answers against the
// suite's own questions. Each line is {"id": ..., ...} holding a systemone
// response at the top level or under "response"; only its "answers" are read,
// so a reference that kept no usage or model still compares. A line that does
// not answer its case's questions in the shape their types ask for is an
// error here, before a request is sent: a reference that half fits would
// print a comparison nobody could trust.
func loadReference(raw []byte, cases []suiteCase) (map[string][]tape.DecisionAnswer, error) {
	byID := map[string]suiteCase{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	out := map[string][]tape.DecisionAnswer{}
	for n, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var row struct {
			ID       string          `json:"id"`
			Answers  json.RawMessage `json:"answers"`
			Response struct {
				Answers json.RawMessage `json:"answers"`
			} `json:"response"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, fmt.Errorf("reference line %d: %w", n+1, err)
		}
		if row.ID == "" {
			return nil, fmt.Errorf(`reference line %d: no "id"`, n+1)
		}
		answers := row.Answers
		if len(answers) == 0 {
			answers = row.Response.Answers
		}
		if len(answers) == 0 {
			return nil, fmt.Errorf(`reference line %d (%s): no "answers" at the top level or under "response"`, n+1, row.ID)
		}
		c, ok := byID[row.ID]
		if !ok {
			continue // a case this suite does not have
		}
		if _, dup := out[row.ID]; dup {
			return nil, fmt.Errorf("reference line %d: id %q is used twice", n+1, row.ID)
		}
		wrapped := append(append([]byte(`{"model":"","usage":{"input_tokens":0,"output_tokens":0},"answers":`), answers...), '}')
		resp, err := server.ParseSystemOneResponse(wrapped, c.Questions)
		if err != nil {
			return nil, fmt.Errorf("reference line %d (%s): %w", n+1, row.ID, err)
		}
		out[row.ID] = resp.Answers
	}
	return out, nil
}

// RecordDecision performs one decision run end to end. Only a server that
// cannot be reached (ErrUnreachable) and a run in which no request was
// answered (ErrAllStreamsFailed) fail it; a request that fails is a record
// with Error and the run goes on. A run cancelled part way returns what it
// had.
func RecordDecision(ctx context.Context, opts DecisionOptions) (*tape.Tape, error) {
	opts = opts.normalize()
	suiteRaw := opts.Suite
	if suiteRaw == nil {
		suiteRaw = defaultDecisionSuite
	}
	cases, suiteSHA, err := loadSuite(suiteRaw)
	if err != nil {
		return nil, err
	}
	var reference map[string][]tape.DecisionAnswer
	if opts.Reference != nil {
		if reference, err = loadReference(opts.Reference, cases); err != nil {
			return nil, err
		}
	}

	client := server.New(opts.BaseURL)
	base := client.BaseURL()
	if base == "" {
		if base, err = client.DiscoverSystemOne(ctx, opts.Candidates); err != nil {
			return nil, err
		}
	}
	id, err := client.WithBaseURL(base).ProbeEngine(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}

	var warnings []string
	refineDecisionEngine(opts, base, &id, &warnings)
	host, hostWarns := collectDecisionHost(ctx, opts)
	warnings = append(warnings, hostWarns...)
	if id == (server.EngineIdentity{}) {
		warnings = append(warnings, "the server answered neither /props nor /v1/models, so its engine and model file are unknown")
	}

	startedAt := opts.Clock.Now()
	t0 := time.Now()

	var (
		mu   sync.Mutex
		recs []tape.DecisionRecord
	)
	do := func(c suiteCase, repeat int, phase string, lane int) {
		rec := sendDecision(ctx, base, t0, c, repeat, phase, lane)
		if ctx.Err() != nil && rec.Error != "" {
			return // cut by the user, not answered and not failed
		}
		mu.Lock()
		recs = append(recs, rec)
		if opts.Progress != nil {
			opts.Progress(rec)
		}
		mu.Unlock()
	}

	// Showcase: one pass, one request at a time, idle time after each answer.
	for i, c := range cases {
		if ctx.Err() != nil {
			break
		}
		do(c, 0, tape.DecisionPhaseShowcase, 0)
		more := i < len(cases)-1 || opts.Repeats > 0
		if more && opts.Gap > 0 && !sleepCtx(ctx, opts.Gap) {
			break
		}
	}

	// Burst: Repeats passes back to back; every lane takes the next job.
	if total := opts.Repeats * len(cases); total > 0 && ctx.Err() == nil {
		var next atomic.Int64
		var wg sync.WaitGroup
		for lane := 0; lane < opts.Concurrency; lane++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ctx.Err() == nil {
					n := int(next.Add(1)) - 1
					if n >= total {
						return
					}
					do(cases[n%len(cases)], 1+n/len(cases), tape.DecisionPhaseBurst, lane)
				}
			}()
		}
		wg.Wait()
	}
	finishedAt := opts.Clock.Now()

	// Index is send order. The lanes race between taking a job and stamping
	// SentAt, so the order is settled by the stamps; the showcase is serial
	// and already first.
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].SentAt < recs[j].SentAt })
	for i := range recs {
		recs[i].Index = i
	}

	answered, firstErr := 0, ""
	for _, r := range recs {
		if r.Error == "" {
			answered++
		} else if firstErr == "" {
			firstErr = r.Error
		}
	}
	if answered == 0 {
		if firstErr == "" {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %s", ErrAllStreamsFailed, firstErr)
	}
	if n := len(recs) - answered; n > 0 {
		warnings = append(warnings, fmt.Sprintf("%d of %d requests failed and are not in any figure; first: %s", n, len(recs), firstErr))
	}

	sum := reduceDecision(recs, cases, opts.Concurrency)
	sum.Suite, sum.SuiteSHA = "", suiteSHA
	if opts.Suite != nil {
		sum.Suite = filepath.Base(opts.SuiteName)
	}
	if reference != nil {
		sum.Reference = compareReference(opts.ReferenceName, recs, cases, reference)
	}

	modelFile := id.ModelFile
	slugFrom := modelFile
	if slugFrom == "" {
		slugFrom = sum.Model
	}
	if slugFrom == "" {
		slugFrom = "decision"
	}
	summary := tape.RunSummary{
		ID:             runID(startedAt, slugFrom),
		ToktapeVersion: opts.Version,
		StartedAt:      startedAt,
		FinishedAt:     finishedAt,
		Server: tape.ServerInfo{
			Kind: id.Kind, URL: base, Host: host.Hostname, Build: id.Build, Commit: id.Commit,
		},
		Model:       tape.ModelInfo{Path: id.ModelPath, FileName: modelFile, Quant: id.Quant},
		Host:        host,
		Concurrency: opts.Concurrency,
		Warnings:    warnings,
		Mode:        tape.ModeDecision,
		Decision:    sum,
		Tag:         opts.Tag,
		Note:        opts.Note,
	}
	return &tape.Tape{Schema: tape.SchemaVersion, Summary: summary, Decisions: recs}, nil
}

// sendDecision sends one case and returns its record. The response is parsed
// against the case's own questions; a response that does not answer them is a
// failed request with the reason, and it has no AnsweredAt.
func sendDecision(ctx context.Context, base string, t0 time.Time, c suiteCase, repeat int, phase string, lane int) tape.DecisionRecord {
	resp, sent, answered, err := server.PostSystemOne(ctx, base, c.Body)
	rec := tape.DecisionRecord{
		CaseID: c.ID, Repeat: repeat, Phase: phase, Lane: lane,
		SentAt:    sent.Sub(t0),
		Request:   c.Body,
		Questions: c.Questions,
	}
	// A body that is not JSON cannot ride in a RawMessage; the error text
	// carries its start instead.
	if len(resp) > 0 && json.Valid(resp) {
		rec.Response = resp
	}
	if err != nil {
		rec.Error = err.Error()
		return rec
	}
	parsed, perr := server.ParseSystemOneResponse(resp, c.Questions)
	if perr != nil {
		rec.Error = "response does not fit the schema: " + perr.Error()
		return rec
	}
	rec.AnsweredAt = answered.Sub(t0)
	rec.Answers = parsed.Answers
	rec.InputTokens = parsed.InputTokens
	rec.Model = parsed.Model
	rec.Server = parsed.Timings
	return rec
}

// sleepCtx waits d and reports false if ctx ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// refineDecisionEngine reads what the local process says about the engine
// when /props did not: ik_llama.cpp names neither an engine nor a build
// (TTP-33, 2026-09-13), so the binary does — the loopback port names the
// process, the process's path and argv[0] say ik, and the checkout around
// the binary names a commit, the same reading a record does minus its flags.
// A server that said what it is (an engine string or object) is never
// outvoted; a remote server has no process here to read; a /proc that cannot
// be read leaves the identity exactly as /props wrote it.
func refineDecisionEngine(opts DecisionOptions, base string, id *server.EngineIdentity, warnings *[]string) {
	if id.Kind.SelfDeclared() || id.Kind == tape.ServerIKLlama {
		return
	}
	port, err := loopbackPortOf(base)
	if err != nil {
		return
	}
	pid, err := procmon.FindPIDByPort(opts.FSRoot, port)
	if err != nil {
		return
	}
	exe, err := procmon.Exe(opts.FSRoot, pid)
	if err != nil {
		exe = ""
	}
	argv, _ := procmon.Args(opts.FSRoot, pid)
	id.Kind = server.RefineKind(id.Kind, exe, argv)
	if id.Kind != tape.ServerIKLlama || id.Commit != "" || exe == "" {
		return
	}
	// The checkout's HEAD is what the clone says now, not what the binary was
	// built from (the record path's caveat, verbatim): the card prints
	// warnings as written, so the provenance travels with the figure.
	if c, ok := procmon.FindGitCommit("", exe); ok {
		id.Commit = c.Hash
		msg := fmt.Sprintf("engine commit %s read from the checkout next to the binary, not from the binary", c.Hash)
		if procmon.BinaryOlderThanCheckout("", exe, c) {
			msg += "; the binary is older than that commit"
		}
		*warnings = append(*warnings, msg)
	}
}

// collectDecisionHost reads the host line the way a record does (procmon,
// the label, the GPU inventory), as warnings rather than failures.
func collectDecisionHost(ctx context.Context, opts DecisionOptions) (tape.HostInfo, []string) {
	var warns []string
	host, err := procmon.HostInfo(opts.FSRoot)
	if err != nil {
		warns = append(warns, "/proc not readable, CPU, RAM and kernel unknown")
	}
	opts.HostLabel.apply(&host)
	gpus := opts.GPU
	if gpus == nil {
		c, ws := gpu.Open(ctx)
		gpus = c
		defer c.Close()
		warns = append(warns, ws...)
	}
	if devices, err := gpus.Devices(ctx); err != nil {
		warns = append(warns, "GPU inventory unreadable, device list empty")
	} else {
		host.GPUs = devices
	}
	return host, warns
}

// reduceDecision computes the summary every figure of the decision card is
// read from. Latency is the client's send-to-last-byte for every figure, in
// both timing sources: the engine's own timing (DecisionRecord.Server) is
// what PrefillPerSecond is made of when every answer carried it, and the
// client latency stays the number a reader can check against.
func reduceDecision(recs []tape.DecisionRecord, cases []suiteCase, concurrency int) *tape.DecisionSummary {
	s := &tape.DecisionSummary{
		Endpoint:     server.SystemOnePath,
		Cases:        len(cases),
		Concurrency:  concurrency,
		Requests:     len(recs),
		TimingSource: tape.DecisionTimingClient,
	}
	var (
		warm, shortMs, longMs []float64
		prefill, engine       []float64
		perCase               = map[string][]float64{}
		answered              = map[string]int{}
		caseTokens            = map[string]int{}
		models                = map[string]bool{}
		allTimed, anyAnswered = true, false
		burstSent             = time.Duration(-1)
		burstEnd              time.Duration
		burstAnswered         int
	)
	for _, r := range recs {
		s.Repeats = max(s.Repeats, r.Repeat+1)
		if r.Phase == tape.DecisionPhaseBurst && (burstSent < 0 || r.SentAt < burstSent) {
			burstSent = r.SentAt
		}
		if r.Error != "" {
			s.Errors++
			continue
		}
		anyAnswered = true
		answered[r.CaseID]++
		if r.InputTokens > 0 {
			caseTokens[r.CaseID] = r.InputTokens
			if s.InputTokensMin == 0 || r.InputTokens < s.InputTokensMin {
				s.InputTokensMin = r.InputTokens
			}
			s.InputTokensMax = max(s.InputTokensMax, r.InputTokens)
		}
		if r.Model != "" {
			models[r.Model] = true
		}
		if r.Server == nil {
			allTimed = false
		} else if r.Server.CacheN > 0 {
			s.CacheHits++
		}
		if r.Phase == tape.DecisionPhaseBurst {
			burstAnswered++
			burstEnd = max(burstEnd, r.AnsweredAt)
		}
		ms := float64(r.Latency()) / float64(time.Millisecond)
		if r.Index == 0 {
			s.ColdMs = ms
			continue
		}
		warm = append(warm, ms)
		perCase[r.CaseID] = append(perCase[r.CaseID], ms)
		switch {
		case r.InputTokens <= 0:
		case r.InputTokens < tape.DecisionLongPromptTokens:
			shortMs = append(shortMs, ms)
		default:
			longMs = append(longMs, ms)
		}
	}
	if anyAnswered && allTimed {
		s.TimingSource = tape.DecisionTimingServer
	}
	// The prefill rate needs the same record set the timing source judged.
	for _, r := range recs {
		if r.Error != "" || r.Index == 0 {
			continue
		}
		if s.TimingSource == tape.DecisionTimingServer {
			// The engine's own time is a breakdown beside the client
			// latency, never the record (internal/tape/decision.go).
			engine = append(engine, r.Server.PromptMs+r.Server.HeadMs)
			if r.Server.PromptMs > 0 && r.Server.PromptN > 0 {
				prefill = append(prefill, float64(r.Server.PromptN)/(r.Server.PromptMs/1000))
			}
		} else if l := r.Latency(); l > 0 && r.InputTokens > 0 {
			prefill = append(prefill, float64(r.InputTokens)/l.Seconds())
		}
	}
	s.WarmP50Ms, s.WarmP95Ms, s.WarmMeanMs = quantile(warm, 0.5), quantile(warm, 0.95), mean(warm)
	s.ShortWarmP50Ms, s.LongWarmP50Ms = quantile(shortMs, 0.5), quantile(longMs, 0.5)
	s.PrefillPerSecond = quantile(prefill, 0.5)
	s.EngineWarmP50Ms = quantile(engine, 0.5)
	if burstAnswered > 0 && burstEnd > burstSent {
		s.RequestsPerSecond = float64(burstAnswered) / (burstEnd - burstSent).Seconds()
	}
	if len(models) == 1 {
		for m := range models {
			s.Model = m
		}
	}
	for _, c := range cases {
		s.PerCase = append(s.PerCase, tape.DecisionCaseSummary{
			CaseID: c.ID, InputTokens: caseTokens[c.ID], WarmP50Ms: quantile(perCase[c.ID], 0.5), Answered: answered[c.ID],
		})
	}
	return s
}

// quantile is the linear-interpolation quantile between closest ranks
// (numpy's default, Python statistics.quantiles "inclusive"): 0 for no
// values. The repo's own percentile helpers are nearest-rank, and the one in
// internal/server is unexported; the figures the screen and card tracks were
// drawn against (tools/decision-example) use this one, so the same data gives
// the same p50 and p95 on both sides.
func quantile(v []float64, q float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	pos := q * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := min(lo+1, len(s)-1)
	return s[lo] + (s[hi]-s[lo])*(pos-float64(lo))
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	var t float64
	for _, x := range v {
		t += x
	}
	return t / float64(len(v))
}

// compareReference compares the showcase pass's answers with the reference's,
// per question: every option's probability, and for a noul [p, 1-p]. A case
// with no reference, or whose showcase request failed, is not compared and
// not counted in Questions.
func compareReference(file string, recs []tape.DecisionRecord, cases []suiteCase, ref map[string][]tape.DecisionAnswer) *tape.DecisionAgreement {
	got := map[string][]tape.DecisionAnswer{}
	for _, r := range recs {
		if r.Phase == tape.DecisionPhaseShowcase && r.Error == "" {
			got[r.CaseID] = r.Answers
		}
	}
	ag := &tape.DecisionAgreement{File: filepath.Base(file)}
	var brier float64
	for _, c := range cases {
		ours, theirs := got[c.ID], ref[c.ID]
		if ours == nil || theirs == nil {
			continue
		}
		for i := range ours {
			a, b := distOf(ours[i]), distOf(theirs[i])
			if len(a) != len(b) || len(a) == 0 {
				continue
			}
			var sq float64
			for k := range a {
				d := a[k] - b[k]
				sq += d * d
				ag.MaxAbsDeltaP = max(ag.MaxAbsDeltaP, math.Abs(d))
			}
			brier += sq
			if argmax(a) != argmax(b) {
				ag.TopFlips++
			}
			ag.Questions++
		}
	}
	if ag.Questions > 0 {
		ag.BrierDelta = brier / float64(ag.Questions)
	}
	return ag
}

func distOf(a tape.DecisionAnswer) []float64 {
	if a.Type == tape.DecisionNoul {
		if a.Noul == nil {
			return nil
		}
		return []float64{*a.Noul, 1 - *a.Noul}
	}
	out := make([]float64, len(a.Probabilities))
	for i, p := range a.Probabilities {
		out[i] = p.P
	}
	return out
}

// argmax is the first index of the largest value, so an exact tie resolves
// the same way on both sides.
func argmax(v []float64) int {
	best := 0
	for i, x := range v {
		if x > v[best] {
			best = i
		}
	}
	return best
}
