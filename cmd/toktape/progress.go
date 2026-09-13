package main

import (
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
)

// progressTick is how often a running line is printed. One line per second is
// enough to show a run is alive and slow enough that a two-minute generation
// does not fill the scrollback (this track prints plain lines; the TUI track
// owns the live view).
const progressTick = time.Second

// spinnerFrames is one Braille cycle. The frame is chosen by elapsed whole
// seconds, so the line is a function of how long the wait has been and not of
// how many times it happened to be redrawn.
var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// loadingLineInterval is how often the waiting line is repeated when stderr is
// not a terminal. A log that gains a line every ten seconds shows the wait is
// alive; one that gains a line every second shows nothing else.
const loadingLineInterval = 10 * time.Second

// progress turns the recorder's event stream into the plain stderr lines of
// the non-interactive run.
//
// The counters are written from the recorder's callback, which is serialised
// but runs on the stream goroutines, and read by the ticker goroutine, so
// they are held under a mutex of their own.
type progress struct {
	w     io.Writer
	quiet bool
	// tty selects the redrawn waiting line over the repeated one.
	tty bool

	mu         sync.Mutex
	streams    int
	tokens     int
	majTotal   uint64
	firstToken time.Time
	lastToken  time.Time
	perStream  map[int]int

	// waiting is the attach wait: the server is there and is not ready. It
	// is the only state in which the ticker draws something other than the
	// run, and the only one that leaves an unterminated line on a terminal.
	waiting    bool
	waitReason string
	waitSince  time.Time
	waitDecade int
	lineOpen   bool
	stopCh     chan struct{}
	done       chan struct{}
}

func newProgress(w io.Writer, quiet bool) *progress {
	return &progress{
		w:          w,
		quiet:      quiet,
		tty:        isTTY(w),
		perStream:  map[int]int{},
		waitDecade: -1,
		stopCh:     make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// start begins the once-a-second line. It is a no-op under --quiet.
func (p *progress) start() {
	if p.quiet {
		close(p.done)
		return
	}
	go func() {
		defer close(p.done)
		t := time.NewTicker(progressTick)
		defer t.Stop()
		for {
			select {
			case <-p.stopCh:
				return
			case <-t.C:
				p.line()
			}
		}
	}()
}

// stop ends the ticker and waits for it, so no progress line can interleave
// with the card that follows.
func (p *progress) stop() {
	close(p.stopCh)
	<-p.done
	p.mu.Lock()
	p.closeLine()
	p.mu.Unlock()
}

// handle receives every recorder event.
//
// The whole body runs under the mutex. The events arrive on the recorder's
// goroutines while the ticker may be writing a line of its own, and two
// Fprintf calls racing on one stderr interleave mid-line.
func (p *progress) handle(ev recorder.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if ev.Kind == recorder.EventLoading {
		if !p.quiet {
			p.noteWaiting(ev)
		}
		return
	}
	// Anything else means the wait is over: the redrawn line must be closed
	// before the next one is written on top of it.
	p.endWaiting()

	switch ev.Kind {
	case recorder.EventAttached:
		if !p.quiet && ev.Summary != nil {
			fmt.Fprintln(p.w, headerLine(ev.Summary))
		}
	case recorder.EventWarning:
		if !p.quiet {
			fmt.Fprintf(p.w, "  ! %s\n", ev.Message)
		}
	case recorder.EventStreamStarted:
		if ev.Streams > p.streams {
			p.streams = ev.Streams
		}
	case recorder.EventToken:
		now := time.Now()
		if p.firstToken.IsZero() {
			p.firstToken = now
		}
		p.lastToken = now
		p.tokens++
		p.majTotal += ev.Token.MajFaultsDelta
		p.perStream[ev.Stream]++
	}
}

// noteWaiting records one attach-wait poll and draws the line. The caller
// holds the mutex.
//
// The origin is reconstructed from the event's own Elapsed rather than from
// the first event's arrival, so the ticker can redraw the line between polls
// — the recorder polls every two seconds and a spinner that moved twice a
// second reads as a program that is working, not one that is stuck.
func (p *progress) noteWaiting(ev recorder.Event) {
	p.waiting = true
	p.waitReason = ev.Message
	p.waitSince = time.Now().Add(-ev.Elapsed)
	p.drawWaiting(ev.Elapsed)
}

// endWaiting closes the waiting line, if one is open. The caller holds the
// mutex.
func (p *progress) endWaiting() {
	if !p.waiting {
		return
	}
	p.waiting = false
	p.closeLine()
}

// closeLine terminates an unterminated terminal line. The caller holds the
// mutex.
func (p *progress) closeLine() {
	if p.lineOpen {
		fmt.Fprintln(p.w)
		p.lineOpen = false
	}
}

// drawWaiting writes the waiting line. The caller holds the mutex.
func (p *progress) drawWaiting(elapsed time.Duration) {
	if p.quiet {
		return
	}
	if p.tty {
		// \r returns to the start and the erase keeps a shorter line from
		// leaving the tail of a longer one behind it.
		fmt.Fprintf(p.w, "\r%s\x1b[K", loadingLine(p.waitReason, elapsed))
		p.lineOpen = true
		return
	}
	decade := int(elapsed / loadingLineInterval)
	if decade == p.waitDecade {
		return
	}
	p.waitDecade = decade
	fmt.Fprintln(p.w, loadingLine(p.waitReason, elapsed))
}

// loadingLine is the sentence a user watches while a server gets ready. It is
// a pure function of the reason and the elapsed time, which is what lets the
// ticker redraw it without asking the recorder anything.
func loadingLine(reason string, elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	frame := spinnerFrames[int(elapsed/time.Second)%len(spinnerFrames)]
	verb := "server is loading the model"
	if reason == recorder.ReasonStarting {
		verb = "waiting for the server to come up"
	}
	return fmt.Sprintf("%c %s … %s", frame, verb, elapsed.Round(time.Second))
}

// line prints one progress line.
//
// The rate is the client-side one over the window from the first token to the
// last, never over wall time that keeps running after generation stopped —
// that is handover lesson 1, and a progress line that disagreed with the card
// by a factor of four would undo the card's whole point. It is a live
// estimate; the card's figure is the server's own.
func (p *progress) line() {
	p.mu.Lock()
	defer p.mu.Unlock()
	// While the server is getting ready the tick redraws that line instead,
	// which is what advances the spinner between the recorder's polls.
	if p.waiting {
		p.drawWaiting(time.Since(p.waitSince))
		return
	}
	// Before the first stream is announced there is nothing to report, and
	// "stream 1/1" printed during discovery would name a stream count that
	// has not been decided yet.
	if p.streams == 0 {
		return
	}
	if p.tokens == 0 {
		fmt.Fprintf(p.w, "  %s · prefill…\n", p.streamLabel())
		return
	}
	rate := 0.0
	if window := p.lastToken.Sub(p.firstToken); window > 0 && p.tokens > 1 {
		rate = float64(p.tokens-1) / window.Seconds()
	}
	fmt.Fprintf(p.w, "  %s · %d tok · %s tok/s · %s maj/tok\n",
		p.streamLabel(), p.tokens, formatRate(rate), formatMajPerToken(p.majTotal, p.tokens))
}

// streamLabel names the streams. With one stream the spec's exact wording is
// used; with several, one aggregate label replaces N lines a second.
func (p *progress) streamLabel() string {
	if p.streams <= 1 {
		return "stream 1/1"
	}
	done := 0
	for _, n := range p.perStream {
		if n > 0 {
			done++
		}
	}
	return fmt.Sprintf("%d/%d streams", done, p.streams)
}

// formatRate matches the card's rule so the live line and the card read the
// same: one decimal below 100, integer above.
func formatRate(v float64) string {
	if v <= 0 {
		return "?"
	}
	if v < 100 {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(v, 'f', 0, 64)
}

// formatMajPerToken renders the page-fault rate. An integer 0 is printed as
// "0" so a warm run reads cleanly, and anything above zero gets a decimal
// because tape.ColdMajFaultsPerToken sits at 1.0.
func formatMajPerToken(maj uint64, tokens int) string {
	if tokens == 0 {
		return "?"
	}
	v := float64(maj) / float64(tokens)
	if v == 0 {
		return "0"
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// summaryRate is the decode figure the finished run reports, used by ls.
func summaryRate(s tape.RunSummary) float64 { return s.Timings.PredictedPerSecond }
