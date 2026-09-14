package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
	"github.com/midagedev/toktape/internal/tui"
)

// tuiQueue is how many observations may be in flight to the screen.
//
// The recorder's callback runs on the stream goroutines and must not block —
// a slow consumer would show up as skewed inter-token latencies, which are
// exactly the numbers the run exists to measure. The queue is therefore deep
// enough for a whole fast run, and a send that would still block drops the
// event instead of waiting: a dropped frame is a cosmetic loss, a delayed
// token is a corrupted measurement.
const tuiQueue = 8192

// tuiInput is the seam the tests drive the live screen through. In production
// it is nil, which leaves bubbletea reading the real terminal; a test replaces
// it with a pipe so the quit key can be delivered without a PTY.
var tuiInput io.Reader

// recordGrid is the tile grid --grid asked for, left here by runRecord.
//
// It is a package var for the same reason recordLabels is one: recordTUI's
// signature is called from record.go, and threading one more argument through
// would change a shape this track does not own. One invocation records one
// run, and runRecord always assigns it, so nothing leaks between two Run calls
// in the same process.
var recordGrid = tui.DefaultGrid

// recordTUI runs the recorder under the live screen.
//
// The recorder owns a goroutine, the screen owns the terminal, and they meet
// on one channel. The tape is written by the recording side, so the Done event
// the screen freezes on is the run as it was actually saved.
func recordTUI(ctx context.Context, c *cli, opts recorder.Options, cfg recordConfig) int {
	// Quitting the screen ends the run: the user asked for the terminal back
	// and a recorder still streaming into a closed screen has nobody to show
	// its result to.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	events := make(chan tui.Event, tuiQueue)
	start := time.Now()
	send := func(e tui.Event) {
		select {
		case events <- e:
		default:
		}
	}
	opts.Progress = func(ev recorder.Event) {
		if e, ok := bridgeEvent(ev, time.Since(start)); ok {
			send(e)
		}
	}

	type result struct {
		tp   *tape.Tape
		arts artifacts
		err  error
	}
	res := make(chan result, 1)
	go func() {
		defer close(events)
		tp, err := recorder.Record(ctx, opts)
		if err != nil {
			send(tui.Event{Kind: tui.EventError, T: time.Since(start), Stream: -1, Err: err})
			res <- result{err: err}
			return
		}
		arts, saveErr := saveRun(cfg.outDir, tp, cfg.card)
		if saveErr != nil {
			send(tui.Event{Kind: tui.EventError, T: time.Since(start), Stream: -1, Err: saveErr})
		}
		send(tui.Event{
			Kind:     tui.EventDone,
			T:        time.Since(start),
			Stream:   -1,
			Tape:     tp,
			TapePath: tildePath(arts.tape),
		})
		res <- result{tp: tp, arts: arts, err: saveErr}
	}()

	screenErr := tui.Run(ctx, tui.Options{
		Events: events, Out: c.stdout, In: tuiInput, Colour: true, Grid: recordGrid,
	})
	cancel()
	r := <-res

	if r.err != nil && r.tp == nil {
		return c.reportRecordError(r.err, opts.BaseURL != "")
	}
	if screenErr != nil {
		fmt.Fprintf(c.stderr, "toktape: %v\n", screenErr)
	}
	if r.err != nil {
		fmt.Fprintf(c.stderr, "toktape: %v\n", r.err)
	}
	// The screen is gone by now, so the card lands in the scrollback where
	// the run's own output would have been.
	if code := printCard(c, r.tp, cfg); code != exitOK {
		return code
	}
	if !cfg.quiet {
		fmt.Fprint(c.stderr, shareHint(cfg.outDir, r.tp, r.arts))
	}
	return exitOK
}

// roundName is how the live views name a round: the prompts file's name, with
// the speculative.n_max a sweep sent it at — "sql-1 n_max 5", or "n_max 5" for
// an unnamed prompt (TTP-35). Empty for an unnamed round outside a sweep, which
// the views label by its number alone.
func roundName(name string, nmax int) string {
	if nmax <= 0 {
		return name
	}
	return strings.TrimSpace(name + " n_max " + strconv.Itoa(nmax))
}

// bridgeEvent translates one recorder observation into one the screen draws,
// reporting false for the ones it does not.
//
// It is a pure function so the mapping is testable without a terminal, a
// server or a PTY. Three recorder kinds are deliberately dropped: EventProps
// and EventPIDNotFound say nothing the screen does not already have, warnings
// belong on the card rather than over a live view, and EventDone is re-sent by
// the caller once the tape has been written — only then are the tape and its
// path both known, and the screen's frozen final frame is built from them.
func bridgeEvent(ev recorder.Event, t time.Duration) (tui.Event, bool) {
	out := tui.Event{T: t, Stream: ev.Stream}
	switch ev.Kind {
	case recorder.EventDiscovered:
		out.Kind = tui.EventDiscovered
		out.Server = tape.ServerInfo{URL: ev.Message}
	case recorder.EventAttached:
		if ev.Summary == nil {
			return tui.Event{}, false
		}
		out.Kind = tui.EventProps
		out.Server = ev.Summary.Server
		out.Model = ev.Summary.Model
		out.Host = ev.Summary.Host
		out.Placement = ev.Summary.Placement
		// What may end the run, which the recorder has decided before the
		// first request goes out (recorder.emitAttached). Without it the live
		// tiles draw a stream's progress against the token cap, which on a
		// clock run is a runaway guard the run will never reach.
		out.Limit = ev.Summary.Limit
	case recorder.EventPIDFound:
		pid, err := strconv.Atoi(ev.Message)
		if err != nil {
			return tui.Event{}, false
		}
		out.Kind = tui.EventPID
		out.PID = pid
	case recorder.EventStreamStarted:
		out.Kind = tui.EventStreamStart
		// The cap travels with the start event so a live tile reads "12/320"
		// from its first token. Without it the tile prints a bare count until
		// the run ends and the tape supplies the budget.
		out.MaxTokens = ev.MaxTokens
		// The round, so the screen clears the tiles when a new one begins
		// rather than appending its tokens to the last one's (TTP-38). The
		// count and name come from the recorder, which owns the plan: a
		// --spec-n-max sweep only becomes rounds inside Record (TTP-35). A
		// run of one round is not a rounds run and carries neither.
		out.Round = ev.Round
		if ev.Rounds > 1 {
			out.Rounds = ev.Rounds
			out.RoundName = roundName(ev.RoundName, ev.SpecNMax)
		}
	case recorder.EventToken:
		out.Kind = tui.EventToken
		out.Token = ev.Token
	case recorder.EventSample:
		out.Kind = tui.EventSample
		out.Sample = ev.Sample
	default:
		return tui.Event{}, false
	}
	return out, true
}
