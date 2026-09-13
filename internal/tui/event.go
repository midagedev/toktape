package tui

import (
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// EventKind is what a recorder observed.
type EventKind int

const (
	// EventDiscovered: a server answered at a URL and identified itself.
	EventDiscovered EventKind = iota
	// EventProps: /props returned, so the model, placement and rig are known.
	EventProps
	// EventPID: the server process was found on this host, so the /proc view
	// is available.
	EventPID
	// EventStreamStart: request Stream was sent.
	EventStreamStart
	// EventToken: one token arrived on stream Stream.
	EventToken
	// EventSample: one periodic host reading.
	EventSample
	// EventProgress: one prompt_progress row for stream Stream.
	EventProgress
	// EventDone: the run finished and the tape was written.
	EventDone
	// EventError: the run failed; Err says how.
	EventError
)

// Event is one observation from the recorder.
//
// internal/recorder is not merged yet, so this is the shape the view needs
// rather than an import: one struct with a Kind, because the recorder produces
// these on a channel and a single type keeps the wiring to one send. The
// fields a Kind does not use are zero. Every Event carries T, the clip time
// since the run started — the view's whole time base, and the reason nothing
// downstream has to ask the clock.
type Event struct {
	Kind   EventKind
	T      time.Duration
	Stream int

	Server    tape.ServerInfo
	Model     tape.ModelInfo
	Host      tape.HostInfo
	Placement tape.PlacementSummary
	PID       int

	Token    tape.TokenEvent
	Sample   tape.RunSample
	Progress tape.PromptProgress

	// MaxTokens is the answer cap the request carried, set on
	// EventStreamStart. Zero means the recorder did not say, and the tile
	// prints a bare token count rather than a fraction of a budget it does
	// not know.
	MaxTokens int

	// Round is the 0-based prompt round a stream belongs to, Rounds how many
	// rounds the run sends (0 when it is not a rounds run) and RoundName the
	// prompts file's name for the round, "" when the line had none. All three
	// are set on EventStreamStart (TTP-38): a `record --prompts` run reuses
	// stream indices 0..N-1 in every round, and without them the screen
	// cannot tell round 2's stream 0 from round 1's.
	Round     int
	Rounds    int
	RoundName string

	// Tape is the finished run, set on EventDone. TapePath is where it was
	// written, empty when it was not.
	Tape     *tape.Tape
	TapePath string

	Err error
}

// Apply folds one event into the model and returns the new state.
//
// The model is a value, so applying an event never mutates a frame that is
// being rendered; the live program replaces its model wholesale.
func (m Model) Apply(e Event) Model {
	if e.T > m.At {
		m.At = e.T
	}
	switch e.Kind {
	case EventDiscovered:
		m.Summary.Server = e.Server
	case EventProps:
		if e.Server.URL != "" {
			m.Summary.Server = e.Server
		}
		if e.Model.FileName != "" || e.Model.Path != "" {
			m.Summary.Model = e.Model
		}
		if e.Host.OS != "" {
			m.Summary.Host = e.Host
		}
		if len(e.Placement.Devices) > 0 || e.Placement.Source != "" {
			m.Summary.Placement = e.Placement
		}
	case EventPID:
		m.PID = e.PID
		m.Summary.Server.PID = e.PID
	case EventStreamStart:
		// A rounds run sends stream indices 0..N-1 again in every round, so
		// the first start of a new round clears the tiles before anything of
		// the round lands on them (TTP-38).
		if e.Rounds > 1 && (m.Rounds == 0 || e.Round != m.Round) {
			m.beginRound(e)
		}
		m.ensureStream(e.Stream)
		s := &m.Streams[m.streamPos(e.Stream)]
		s.StartedAt = e.T
		if e.MaxTokens > 0 {
			s.MaxTokens = e.MaxTokens
		}
		if n := e.Stream + 1; n > m.Summary.Concurrency {
			m.Summary.Concurrency = n
		}
	case EventToken:
		m.ensureStream(e.Stream)
		s := &m.Streams[m.streamPos(e.Stream)]
		itl := time.Duration(0)
		if n := len(s.Tokens); n > 0 {
			itl = e.T - s.Tokens[n-1].T
		}
		s.Tokens = append(s.Tokens, Token{
			T:              e.T,
			Stream:         e.Stream,
			Text:           e.Token.Text,
			ITL:            itl,
			MajFaultsDelta: e.Token.MajFaultsDelta,
			Reasoning:      e.Token.Reasoning,
		})
		s.Text += e.Token.Text
		s.EndedAt = e.T
		if e.T > m.RunEnd {
			m.RunEnd = e.T
		}
	case EventProgress:
		m.ensureStream(e.Stream)
		s := &m.Streams[m.streamPos(e.Stream)]
		p := e.Progress
		p.T = e.T
		s.Progress = append(s.Progress, p)
	case EventSample:
		sm := e.Sample
		sm.T = e.T
		m.Samples = append(m.Samples, sm)
	case EventDone:
		if e.Tape != nil {
			// The recorder's tape is the record; rebuild from it so the
			// frozen screen shows exactly what was written to disk.
			done := ModelAt(e.Tape, maxDur(e.T, tapeEnd(e.Tape)))
			done.Mode = m.Mode
			done.Theme = m.Theme
			// The grid and the page are the reader's, not the tape's: a run
			// that finishes must not jump back to page one or to a layout the
			// viewer did not choose.
			done.Grid = m.Grid
			done.Page = m.Page
			done.PID = m.PID
			done.TapePath = e.TapePath
			done.Done = true
			return done
		}
		for i := range m.Streams {
			m.Streams[i].Done = true
		}
		m.Done = true
		m.TapePath = e.TapePath
	case EventError:
		if e.Err != nil {
			m.Err = e.Err.Error()
		}
		if e.Stream >= 0 && e.Stream < len(m.Streams) {
			m.Streams[m.streamPos(e.Stream)].Err = m.Err
		}
	}
	return m
}

// beginRound switches the model to the round e starts.
//
// Everything a tile shows is per round — the text, the tokens and therefore
// the thinking badge, the count against the cap, the prefill rows, the final
// timings, the error — and is cleared. The slice keeps its length: a round is
// N streams, as the one before it was, and Summary.Concurrency stays N. What
// is not per round is kept: the host samples run across the whole tape, and
// RunEnd is the run's. The first token of the round finds no previous token,
// so its ITL is 0 rather than the gap between the rounds.
//
// The slice is a new one rather than the old one zeroed in place, so a frame
// still holding the previous model keeps drawing the previous round.
func (m *Model) beginRound(e Event) {
	m.Round, m.Rounds, m.RoundName = e.Round, e.Rounds, e.RoundName
	fresh := make([]Stream, len(m.Streams))
	for i, s := range m.Streams {
		fresh[i] = Stream{Index: s.Index, Slot: s.Slot}
	}
	m.Streams = fresh
}

// ensureStream grows the stream slice so index i exists.
func (m *Model) ensureStream(i int) {
	if i < 0 {
		return
	}
	for len(m.Streams) <= i {
		m.Streams = append(m.Streams, Stream{Index: len(m.Streams), Slot: -1})
	}
}

// streamPos maps a stream index to its slot in the slice. The recorder sends
// indices in start order, so the two agree, but a gap must not panic a render.
func (m Model) streamPos(i int) int {
	if i >= 0 && i < len(m.Streams) && m.Streams[i].Index == i {
		return i
	}
	for p := range m.Streams {
		if m.Streams[p].Index == i {
			return p
		}
	}
	return 0
}

// tapeEnd is the arrival time of the last token in tp.
func tapeEnd(tp *tape.Tape) time.Duration {
	var end time.Duration
	for _, req := range tp.Requests {
		if n := len(req.Tokens); n > 0 {
			if t := req.StartedAt + req.Tokens[n-1].T; t > end {
				end = t
			}
		}
	}
	return end
}

func maxDur(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
