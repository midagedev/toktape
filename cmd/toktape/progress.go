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

// progress turns the recorder's event stream into the plain stderr lines of
// the non-interactive run.
//
// The counters are written from the recorder's callback, which is serialised
// but runs on the stream goroutines, and read by the ticker goroutine, so
// they are held under a mutex of their own.
type progress struct {
	w     io.Writer
	quiet bool

	mu         sync.Mutex
	streams    int
	tokens     int
	majTotal   uint64
	firstToken time.Time
	lastToken  time.Time
	perStream  map[int]int

	stopCh chan struct{}
	done   chan struct{}
}

func newProgress(w io.Writer, quiet bool) *progress {
	return &progress{
		w:         w,
		quiet:     quiet,
		perStream: map[int]int{},
		stopCh:    make(chan struct{}),
		done:      make(chan struct{}),
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
}

// handle receives every recorder event.
func (p *progress) handle(ev recorder.Event) {
	switch ev.Kind {
	case recorder.EventAttached:
		// The attach line and the warnings are written from the recorder's
		// own goroutine while the ticker may be writing a progress line, so
		// they take the same mutex: two Fprintf calls racing on one stderr
		// interleave mid-line.
		if !p.quiet && ev.Summary != nil {
			p.mu.Lock()
			fmt.Fprintln(p.w, headerLine(ev.Summary))
			p.mu.Unlock()
		}
	case recorder.EventWarning:
		if !p.quiet {
			p.mu.Lock()
			fmt.Fprintf(p.w, "  ! %s\n", ev.Message)
			p.mu.Unlock()
		}
	case recorder.EventStreamStarted:
		p.mu.Lock()
		if ev.Streams > p.streams {
			p.streams = ev.Streams
		}
		p.mu.Unlock()
	case recorder.EventToken:
		now := time.Now()
		p.mu.Lock()
		if p.firstToken.IsZero() {
			p.firstToken = now
		}
		p.lastToken = now
		p.tokens++
		p.majTotal += ev.Token.MajFaultsDelta
		p.perStream[ev.Stream]++
		p.mu.Unlock()
	}
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
