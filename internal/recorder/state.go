package recorder

import (
	"context"
	"sync"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/procmon"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// state is everything the concurrent halves of a run share: the one
// procmon.FaultSampler, the fault timeline the tokens build, the periodic
// samples and the progress callback.
//
// One mutex guards all of it. procmon.Sampler is documented as not safe for
// concurrent use, and under RunConcurrent every stream's OnToken hook runs on
// its own goroutine, so the hot path has to be serialised anyway; giving the
// samples and the callback the same mutex costs nothing and makes the
// callback safe for an implementation that keeps no lock.
//
// The sampler is touched by the token hooks and by nothing else. The periodic
// reader takes its memory picture through the stateless procmon.ReadMem for
// the reason given at takeSample: sharing the sampler's latch would let the
// samples eat the faults the tokens are there to report.
type state struct {
	mu      sync.Mutex
	sampler procmon.FaultSampler
	// perStream[i][j] is the major-fault delta of stream i's j-th token.
	perStream [][]uint64
	// timeline holds the same deltas in arrival order across all streams.
	// It is what procmon.Summarize reduces.
	timeline []uint64
	// firstIdx[i] is the index in timeline of stream i's first token, or -1.
	firstIdx []int
	tokens   int
	samples  []tape.RunSample
	progress func(Event)
	streams  int

	// Multi-round runs (TTP-31). perRound is N, the streams of one round, and
	// 0 in a single-round run. The per-stream slices above are then indexed
	// by the run-wide stream index g = round*N + i, so one state and one
	// sampler serve the whole run and the fault timeline stays one series.
	perRound int
	// round is the round being sent, stamped on the sample events.
	round int
	// roundStart[k] is len(timeline) when round k began: rounds are
	// sequential, so round k's tokens are timeline[roundStart[k]:roundStart[k+1]].
	roundStart []int

	// started and ended track which streams are live, for the run's clock
	// (TTP-76). A stream is live once its request has gone out and until it
	// has stopped for any reason. The clock reads both: the floor is about the
	// streams it would cut, and a stream that has not started — a later round
	// of a multi-round run — or that ended on its own is not one of them.
	//
	// They are here rather than in clock because they are written by the
	// stream goroutines and must share the mutex OnToken already takes; that
	// is also what makes cutSnapshot exact.
	started, ended []bool

	// slotsDead latches a /slots route that answered with an error (a server
	// started with --no-slots returns 501), so the poll is not retried every
	// interval for the length of the run.
	slotsDead bool

	// treeProcs latches the process count of the FIRST periodic reading that
	// saw more than one process in an engine run's tree (2026-09-15,
	// ExLlamaV3), for the warning the recorder prints after the sampler
	// stops. 0 means no reading ever saw a child.
	treeProcs int
}

func newState(n int, sampler procmon.FaultSampler, progress func(Event)) *state {
	st := &state{
		sampler:   sampler,
		perStream: make([][]uint64, n),
		firstIdx:  make([]int, n),
		started:   make([]bool, n),
		ended:     make([]bool, n),
		progress:  progress,
		streams:   n,
	}
	for i := range st.firstIdx {
		st.firstIdx[i] = -1
	}
	return st
}

// emit calls the progress callback. The caller holds mu.
func (st *state) emit(ev Event) {
	if st.progress == nil {
		return
	}
	st.progress(ev)
}

// notify emits an event without the caller holding mu.
func (st *state) notify(ev Event) {
	st.mu.Lock()
	st.emit(ev)
	st.mu.Unlock()
}

// hooks returns the per-stream hooks RunConcurrent asks for. OnToken reads
// the major-fault counter at the instant the token arrived, which is the
// whole reason the hook exists (internal/server StreamHooks doc).
//
// tape.TokenEvent arrives by value, so the delta cannot be written back into
// the record from here; it is recorded per stream and per arrival order and
// stitched into the records after RunConcurrent returns.
func (st *state) hooks(i int) server.StreamHooks {
	return server.StreamHooks{
		OnEnd: func() {
			st.mu.Lock()
			if i < len(st.ended) {
				st.ended[i] = true
			}
			st.mu.Unlock()
		},
		OnToken: func(ev tape.TokenEvent) {
			st.mu.Lock()
			var maj uint64
			if st.sampler != nil {
				maj, _ = st.sampler.MajDelta()
			}
			if st.firstIdx[i] < 0 {
				st.firstIdx[i] = len(st.timeline)
			}
			st.timeline = append(st.timeline, maj)
			st.perStream[i] = append(st.perStream[i], maj)
			st.tokens++
			ev.MajFaultsDelta = maj
			stream, round := i, 0
			if st.perRound > 0 {
				stream, round = i%st.perRound, i/st.perRound
			}
			st.emit(Event{Kind: EventToken, Stream: stream, Round: round, Streams: st.streams, Token: ev})
			st.mu.Unlock()
		},
	}
}

// beginStream marks stream g live. It is called as the request goes out, so
// that a stream which has not been sent yet — a later round's — never holds
// the clock's floor open (TTP-76).
func (st *state) beginStream(g int) {
	st.mu.Lock()
	if g >= 0 && g < len(st.started) {
		st.started[g] = true
	}
	st.mu.Unlock()
}

// atFloor reports whether every live stream has produced min tokens, which is
// when the clock's cut becomes legal. With min 0, or with nothing live, it is
// vacuously true: the first is a run with no floor and the second is a run
// whose streams have all ended, and in neither is there anything to wait for.
func (st *state) atFloor(min int) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i, on := range st.started {
		if !on || st.ended[i] {
			continue
		}
		if len(st.perStream[i]) < min {
			return false
		}
	}
	return true
}

// cutSnapshot is the live streams at the instant the clock decides to cut: the
// ones the cancel is about to end. The caller holds no lock; taking it here,
// under the same mutex the OnEnd hook takes, is what makes the answer exact —
// a stream that ends from now on has to wait for this mutex, so it is in the
// snapshot and was indeed ended by the cut.
func (st *state) cutSnapshot() []bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	live := make([]bool, len(st.started))
	for i := range st.started {
		live[i] = st.started[i] && !st.ended[i]
	}
	return live
}

// applyDeltas writes the recorded per-token major-fault deltas back into the
// records. The j-th OnToken call of stream i is the j-th token of record i,
// because internal/server fires the hook in order for exactly the tokens it
// appends.
func (st *state) applyDeltas(recs []tape.RequestRecord) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range recs {
		if i >= len(st.perStream) {
			break
		}
		d := st.perStream[i]
		for j := range recs[i].Tokens {
			if j >= len(d) {
				break
			}
			recs[i].Tokens[j].MajFaultsDelta = d[j]
		}
	}
}

// decodeFaults is the number of major faults stream i took after its first
// token: the denominator-free half of the cold verdict for that stream.
func (st *state) decodeFaults(i int) uint64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	if i >= len(st.perStream) || len(st.perStream[i]) < 2 {
		return 0
	}
	var sum uint64
	for _, v := range st.perStream[i][1:] {
		sum += v
	}
	return sum
}

// firstTokenIndex is the index procmon.Summarize splits the fault timeline
// at: everything at or before it is the prompt phase, everything after it is
// decode.
//
// With one stream that is index 0, which is what Summarize's doc describes.
// With N streams the merged timeline interleaves N prefills, and the faults a
// later stream took while its own prompt was still being evaluated arrive
// after the earliest stream's first token. Counting those as decode faults
// pushes MajFaultsPerToken over tape.ColdMajFaultsPerToken and labels a warm
// concurrent run cold — the exact mistake the Summarize doc warns about. So
// the split is the LAST stream's first token: every prefill has finished by
// then. It reduces to 0 for N == 1.
func (st *state) firstTokenIndex() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	last := 0
	for _, idx := range st.firstIdx {
		if idx > last {
			last = idx
		}
	}
	return last
}

func (st *state) snapshot() ([]uint64, []tape.RunSample, int) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.timeline, st.samples, st.tokens
}

// noteTreeProcs latches n when it is the first reading that saw a tree of
// more than one process (takeSample doc). The count at first sight is what
// the warning names: a worker that appears later does not change what the
// earlier samples already summed.
func (st *state) noteTreeProcs(n int) {
	st.mu.Lock()
	if st.treeProcs == 0 {
		st.treeProcs = n
	}
	st.mu.Unlock()
}

// firstTreeProcs is the latched process count, 0 when no reading ever saw a
// child (warnTreeProcesses decides on it).
func (st *state) firstTreeProcs() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.treeProcs
}

// sampleLoop takes a host reading every interval until stop is closed. It
// takes one reading immediately and one on the way out, so a run shorter than
// the interval still produces samples rather than an empty time series.
func (st *state) sampleLoop(ctx context.Context, stop <-chan struct{}, dep sampleDeps) {
	st.takeSample(ctx, dep)
	t := time.NewTicker(dep.interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			st.takeSample(ctx, dep)
			return
		case <-ctx.Done():
			st.takeSample(ctx, dep)
			return
		case <-t.C:
			st.takeSample(ctx, dep)
		}
	}
}

// sampleDeps is what one periodic reading needs.
type sampleDeps struct {
	interval time.Duration
	runStart time.Time
	fsRoot   string
	pid      int
	gpus     gpu.Collector
	client   *server.Client
	pollSlot bool
	// tree says the pid's whole process tree is the engine (2026-09-15,
	// ExLlamaV3): the reading sums parent and children, and latches the
	// count for the recorder's warning. Every other kind reads one pid.
	tree bool
}

// takeSample reads the process, the devices, the load average and the slot
// table once and appends a tape.RunSample.
//
// The process picture comes from procmon.ReadMem and deliberately NOT from
// procmon.Sampler.Sample, although the Sampler holds an open handle and would
// be the cheaper read. Sample latches the fault counters exactly as MajDelta
// does, so every periodic reading would swallow the faults taken since the
// previous token and hand them to nobody: the per-token deltas would sum to
// less than the run's real major-fault count, and the sparkline — the most
// watched number in the clip — would under-report by however often the
// sampler happened to tick. ReadMem is stateless and returns the same
// tape.MemSample, so the token hooks stay the only consumer of the latch and
// sum(per-token deltas) is the whole run. ReadMemTree, the engine run's
// variant, is stateless for the same reason.
//
// Nothing here is taken under the mutex: ReadMem, the GPU read and the /slots
// poll touch no shared state, and an nvidia-smi exec can outlast the sample
// interval, which would stall every token hook in the run.
func (st *state) takeSample(ctx context.Context, dep sampleDeps) {
	var mem tape.MemSample
	if dep.pid > 0 {
		if dep.tree {
			if m, n, err := procmon.ReadMemTree(dep.fsRoot, dep.pid); err == nil {
				mem = m
				if n > 1 {
					st.noteTreeProcs(n)
				}
			}
		} else if m, err := procmon.ReadMem(dep.fsRoot, dep.pid); err == nil {
			mem = m
		}
	}
	st.mu.Lock()
	tokens := st.tokens
	slotsDead := st.slotsDead
	st.mu.Unlock()

	var gpuSamples []tape.GPUSample
	if dep.gpus != nil {
		if s, err := dep.gpus.Sample(ctx, dep.pid); err == nil {
			gpuSamples = s
		}
	}

	var load float64
	if v, err := procmon.LoadAvg(dep.fsRoot); err == nil {
		load = v
	}

	busy := 0
	if dep.pollSlot && !slotsDead && dep.client != nil {
		slots, err := dep.client.Slots(ctx)
		if err != nil {
			st.mu.Lock()
			st.slotsDead = true
			st.mu.Unlock()
		} else {
			busy = server.BusyCount(slots)
		}
	}

	s := tape.RunSample{
		T:           time.Since(dep.runStart),
		Mem:         mem,
		GPUs:        gpuSamples,
		LoadAvg1:    load,
		TokensSoFar: tokens,
		SlotsBusy:   busy,
	}
	st.mu.Lock()
	st.samples = append(st.samples, s)
	st.emit(Event{Kind: EventSample, Stream: -1, Streams: st.streams, Round: st.round, Sample: s})
	st.mu.Unlock()
}
