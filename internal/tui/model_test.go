package tui

import (
	"errors"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// TestModelAtIsMonotonic: the state at a later instant must contain the state
// at an earlier one. A replay that could lose a token as it moved forward
// would make every frame after it a different run.
func TestModelAtIsMonotonic(t *testing.T) {
	tp := ExampleTape()
	var prev Model
	for at := time.Duration(0); at <= doneAt; at += 311 * time.Millisecond {
		m := ModelAt(tp, at)
		if at > 0 {
			if len(m.Streams) != len(prev.Streams) {
				t.Fatalf("at=%v: stream count changed from %d to %d", at, len(prev.Streams), len(m.Streams))
			}
			for i := range m.Streams {
				now, before := m.Streams[i], prev.Streams[i]
				if len(now.Tokens) < len(before.Tokens) {
					t.Fatalf("at=%v stream %d: token count fell from %d to %d",
						at, i, len(before.Tokens), len(now.Tokens))
				}
				for j := range before.Tokens {
					if now.Tokens[j] != before.Tokens[j] {
						t.Fatalf("at=%v stream %d: token %d changed", at, i, j)
					}
				}
				if len(now.Text) < len(before.Text) || now.Text[:len(before.Text)] != before.Text {
					t.Fatalf("at=%v stream %d: text is not an extension of the earlier text", at, i)
				}
				if len(now.Progress) < len(before.Progress) {
					t.Fatalf("at=%v stream %d: progress rows were lost", at, i)
				}
				if before.Done && !now.Done {
					t.Fatalf("at=%v stream %d: a finished stream became unfinished", at, i)
				}
			}
			if len(m.Samples) < len(prev.Samples) {
				t.Fatalf("at=%v: samples were lost", at)
			}
			if prev.Done && !m.Done {
				t.Fatalf("at=%v: a finished run became unfinished", at)
			}
		}
		prev = m
	}
	if !prev.Done {
		t.Error("the run never reported Done")
	}
}

// TestModelAtCutsAtTheInstant: nothing after the cut may appear in the model,
// and every token's timestamp is on the run's timeline, not its request's.
func TestModelAtCutsAtTheInstant(t *testing.T) {
	tp := ExampleTape()
	const at = 2 * time.Second
	m := ModelAt(tp, at)
	for _, s := range m.Streams {
		for _, tk := range s.Tokens {
			if tk.T > at {
				t.Fatalf("stream %d: token at %v is past the cut %v", s.Index, tk.T, at)
			}
		}
		if n := len(s.Tokens); n > 0 {
			req := tp.Requests[s.Index]
			if want := req.StartedAt + req.Tokens[n-1].T; s.Tokens[n-1].T != want {
				t.Errorf("stream %d: last token at %v, want %v (request start plus its own offset)",
					s.Index, s.Tokens[n-1].T, want)
			}
		}
	}
	for _, sm := range m.Samples {
		if sm.T > at {
			t.Fatalf("sample at %v is past the cut %v", sm.T, at)
		}
	}
	if m.Done {
		t.Error("the run reported Done while tokens were still to come")
	}
}

// TestApplyMatchesModelAt: folding the recorder's events must land on the same
// state that cutting the finished tape produces. Live and replay share one
// renderer, so they have to share one state shape too (decision 10).
func TestApplyMatchesModelAt(t *testing.T) {
	tp := ExampleTape()
	live := Model{Mode: ModeLive}
	for _, ev := range eventsOf(tp) {
		live = live.Apply(ev)
	}
	replay := ModelAt(tp, tapeEnd(tp))
	replay.TapePath = "/tmp/run.tape"

	if len(live.Streams) != len(replay.Streams) {
		t.Fatalf("live has %d streams, replay %d", len(live.Streams), len(replay.Streams))
	}
	for i := range replay.Streams {
		a, b := live.Streams[i], replay.Streams[i]
		if a.Text != b.Text {
			t.Errorf("stream %d: live text differs from replay text", i)
		}
		if len(a.Tokens) != len(b.Tokens) {
			t.Fatalf("stream %d: live has %d tokens, replay %d", i, len(a.Tokens), len(b.Tokens))
		}
		for j := range b.Tokens {
			if a.Tokens[j] != b.Tokens[j] {
				t.Fatalf("stream %d token %d: %+v != %+v", i, j, a.Tokens[j], b.Tokens[j])
			}
		}
	}
	if len(live.Samples) != len(replay.Samples) {
		t.Errorf("live has %d samples, replay %d", len(live.Samples), len(replay.Samples))
	}
	// The rendered frames have to agree too, which is the claim that actually
	// matters to a viewer.
	if View(live, tapeEnd(tp), 120, 36) != View(replay, tapeEnd(tp), 120, 36) {
		t.Error("the frame built from events differs from the frame built from the tape")
	}
}

// eventsOf flattens a tape into the event stream a recorder would have
// produced, in arrival order.
func eventsOf(tp *tape.Tape) []Event {
	var evs []Event
	evs = append(evs, Event{Kind: EventProps, Server: tp.Summary.Server, Model: tp.Summary.Model,
		Host: tp.Summary.Host, Placement: tp.Summary.Placement})
	for _, req := range tp.Requests {
		evs = append(evs, Event{Kind: EventStreamStart, Stream: req.Index, T: req.StartedAt})
		for _, pr := range req.Progress {
			evs = append(evs, Event{Kind: EventProgress, Stream: req.Index,
				T: req.StartedAt + pr.T, Progress: pr})
		}
		for _, tk := range req.Tokens {
			evs = append(evs, Event{Kind: EventToken, Stream: req.Index,
				T: req.StartedAt + tk.T, Token: tk})
		}
	}
	for _, sm := range tp.Samples {
		evs = append(evs, Event{Kind: EventSample, T: sm.T, Sample: sm})
	}
	// Arrival order, stable so tokens of one stream keep their sequence.
	for i := 1; i < len(evs); i++ {
		e := evs[i]
		j := i - 1
		for j >= 0 && evs[j].T > e.T {
			evs[j+1] = evs[j]
			j--
		}
		evs[j+1] = e
	}
	evs = append(evs, Event{Kind: EventDone, T: tapeEnd(tp), Tape: tp,
		TapePath: "/tmp/run.tape"})
	return evs
}

// TestApplyDoneRebuildsFromTheTape: the recorder's tape is the record, so the
// frozen screen must show what was written to disk, not what the live fold
// happened to accumulate.
func TestApplyDoneRebuildsFromTheTape(t *testing.T) {
	tp := ExampleTape()
	m := Model{Mode: ModePrompt}
	m = m.Apply(Event{Kind: EventDone, T: time.Second, Tape: tp, TapePath: "/tmp/run.tape"})
	if !m.Done {
		t.Error("Done was not set")
	}
	if m.TapePath != "/tmp/run.tape" {
		t.Errorf("TapePath = %q", m.TapePath)
	}
	if m.Mode != ModePrompt {
		t.Error("the user's open panel was closed by the done event")
	}
	if len(m.Streams) != len(tp.Requests) {
		t.Fatalf("got %d streams, want %d", len(m.Streams), len(tp.Requests))
	}
	if n := len(m.Streams[0].Tokens); n != len(tp.Requests[0].Tokens) {
		t.Errorf("stream 0 has %d tokens, want %d", n, len(tp.Requests[0].Tokens))
	}
}

// TestApplyErrorSurfaces: a fatal error replaces the body rather than being
// swallowed into an empty screen.
func TestApplyErrorSurfaces(t *testing.T) {
	m := Model{}.Apply(Event{Kind: EventError, Stream: -1, Err: errors.New("connection refused")})
	if m.Err != "connection refused" {
		t.Fatalf("Err = %q", m.Err)
	}
	frame := View(m, 0, 120, 36)
	if !contains(frame, "connection refused") {
		t.Error("the error is not on the screen")
	}
}

// TestApplyGrowsStreamsOnDemand: a recorder that reports stream 3 before
// stream 0 must not panic the view.
func TestApplyGrowsStreamsOnDemand(t *testing.T) {
	m := Model{}.Apply(Event{Kind: EventToken, Stream: 3, T: time.Second,
		Token: tape.TokenEvent{Text: "hi"}})
	if len(m.Streams) != 4 {
		t.Fatalf("got %d streams, want 4", len(m.Streams))
	}
	if m.Streams[3].Text != "hi" {
		t.Errorf("stream 3 text = %q", m.Streams[3].Text)
	}
}

// TestDecodeRateExcludesPrefill: the rate is measured from the first content
// token to the last, never from the moment the request was sent. Counting the
// prefill gap is what turned 18 tok/s into a recorded 4.5 (handover lesson 1).
func TestDecodeRateExcludesPrefill(t *testing.T) {
	tp := &tape.Tape{Schema: tape.SchemaVersion, Requests: []tape.RequestRecord{{
		Index: 0,
		Tokens: []tape.TokenEvent{
			{T: 5 * time.Second, Text: "a"}, // a five second prefill
			{T: 5100 * time.Millisecond, Text: "b"},
			{T: 5200 * time.Millisecond, Text: "c"},
		},
	}}}
	m := ModelAt(tp, 6*time.Second)
	_, rate, _ := m.decodeRateAt(6 * time.Second)
	if want := 10.0; rate < want-0.01 || rate > want+0.01 {
		t.Errorf("rate = %.3f tok/s, want %.1f (two intervals of 100 ms)", rate, want)
	}
}

// TestMajFaultSeriesSpansTheStreams: with eight concurrent streams a column
// per merged token would cover a third of a second, and the burst the
// sparkline exists to show would never be on screen. The window therefore
// merges tokens into columns, and a burst inside one column has to survive
// that merge.
//
// 2026-09-13 (TTP-28): the fixture this used to read is a run that takes no
// major faults at all — a 70B that fits in VRAM does not page weights in
// during decode, and a fixture where it did was teaching the reader that a
// fault storm is the ordinary case. So the burst is built here instead, which
// is where it belongs: this test is about the reduction, not about the example.
func TestMajFaultSeriesSpansTheStreams(t *testing.T) {
	const (
		streams = 8
		tokens  = 120
		itl     = 80 * time.Millisecond
	)
	tp := &tape.Tape{Schema: tape.SchemaVersion}
	for i := 0; i < streams; i++ {
		req := tape.RequestRecord{Index: i, Slot: i}
		for k := 0; k < tokens; k++ {
			at := time.Duration(k+1) * itl
			var faults uint64
			// A page-in burst near the end of the window, on every stream at
			// once, the way a server that ran out of page cache faults.
			if k >= tokens-10 && k < tokens-2 {
				faults = uint64(2 + (k+i)%3)
			}
			req.Tokens = append(req.Tokens, tape.TokenEvent{
				T: at, Index: k, Text: " x", MajFaultsDelta: faults,
			})
		}
		tp.Requests = append(tp.Requests, req)
	}

	m := ModelAt(tp, time.Duration(tokens)*itl)
	series := m.majFaultSeries(28)
	if len(series) != 28 {
		t.Fatalf("got %d columns, want 28", len(series))
	}
	hot := 0
	for _, v := range series {
		if v >= tape.ColdMajFaultsPerToken {
			hot++
		}
	}
	if hot == 0 {
		t.Errorf("the page-in burst is not visible anywhere in the window: %v", series)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
