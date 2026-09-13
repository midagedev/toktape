package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
)

// TestBridgeEventCarriesTheRound (TTP-38): a stream start reaches the screen
// with its round, and recordTUI's stamp adds the round count and the name from
// the prompts file, so the screen can tell round 2's stream 1 from round 1's.
func TestBridgeEventCarriesTheRound(t *testing.T) {
	const at = 1500 * time.Millisecond
	got, ok := bridgeEvent(recorder.Event{Kind: recorder.EventStreamStarted, Stream: 1, Streams: 4, Round: 1, MaxTokens: 32}, at)
	if !ok {
		t.Fatal("a stream start was dropped")
	}
	if got.Round != 1 || got.Stream != 1 || got.MaxTokens != 32 {
		t.Errorf("stream start = round %d stream %d cap %d, want 1 1 32", got.Round, got.Stream, got.MaxTokens)
	}

	rounds := []recorder.Round{{Name: "sql-1"}, {Name: "prose-1"}, {}}
	stamped := stampRound(got, rounds)
	if stamped.Rounds != 3 || stamped.RoundName != "prose-1" {
		t.Errorf("stamped = %d rounds, name %q; want 3, prose-1", stamped.Rounds, stamped.RoundName)
	}
	got.Round = 2
	if s := stampRound(got, rounds); s.Rounds != 3 || s.RoundName != "" {
		t.Errorf("an unnamed round stamped = %d rounds, name %q; want 3, empty", s.Rounds, s.RoundName)
	}
	got.Round = 7
	if s := stampRound(got, rounds); s.RoundName != "" {
		t.Errorf("a round past the file stamped the name %q", s.RoundName)
	}
	// A single-round run is not a rounds run: nothing is stamped.
	if s := stampRound(got, nil); s.Rounds != 0 || s.RoundName != "" {
		t.Errorf("a single-round run stamped %d rounds, name %q", s.Rounds, s.RoundName)
	}
	// Only stream starts carry a round.
	tok, _ := bridgeEvent(recorder.Event{Kind: recorder.EventToken, Stream: 1, Round: 1}, at)
	if s := stampRound(tok, rounds); s.Rounds != 0 || s.RoundName != "" {
		t.Errorf("a token was stamped with %d rounds, name %q", s.Rounds, s.RoundName)
	}
}

// TestProgressAnnouncesEachRound (TTP-38): the plain progress lines say when a
// new round begins and count its streams afresh, rather than accumulating
// round 2's tokens onto round 1's.
func TestProgressAnnouncesEachRound(t *testing.T) {
	var buf bytes.Buffer
	p := newProgress(&buf, false)
	p.rounds = []recorder.Round{{Name: "sql-1"}, {Name: "prose-1"}, {}}

	start := func(round, stream int) {
		p.handle(recorder.Event{Kind: recorder.EventStreamStarted, Stream: stream, Streams: 2, Round: round})
	}
	token := func(round, stream int) {
		p.handle(recorder.Event{Kind: recorder.EventToken, Stream: stream, Streams: 2, Round: round,
			Token: tape.TokenEvent{Text: "x", MajFaultsDelta: 1}})
	}

	start(0, 0)
	start(0, 1)
	for i := 0; i < 3; i++ {
		token(0, 0)
		token(0, 1)
	}
	if p.tokens != 6 || len(p.perStream) != 2 {
		t.Fatalf("round 0 counted %d tokens over %d streams, want 6 over 2", p.tokens, len(p.perStream))
	}

	start(1, 0)
	if !strings.Contains(buf.String(), "  round 2/3 prose-1\n") {
		t.Errorf("no round line for round 2:\n%s", buf.String())
	}
	for s, n := range p.perStream {
		if n != 0 {
			t.Errorf("stream %d kept %d tokens from round 1", s, n)
		}
	}
	if p.tokens != 0 || p.majTotal != 0 || !p.firstToken.IsZero() {
		t.Errorf("round 1's counters survived: %d tokens, %d faults, first token %v", p.tokens, p.majTotal, p.firstToken)
	}
	start(1, 1)
	if n := strings.Count(buf.String(), "round 2/3"); n != 1 {
		t.Errorf("round 2 was announced %d times, want once:\n%s", n, buf.String())
	}

	// The line after the reset is round 2's prefill, not round 1's rate.
	buf.Reset()
	p.line()
	if got := strings.TrimSpace(buf.String()); got != "0/2 streams · prefill…" {
		t.Errorf("progress line after the round change = %q", got)
	}

	buf.Reset()
	start(2, 0)
	if !strings.Contains(buf.String(), "  round 3/3\n") {
		t.Errorf("an unnamed round was not announced by its number alone: %q", buf.String())
	}

	// A single-round run prints no round line at all.
	buf.Reset()
	q := newProgress(&buf, false)
	q.handle(recorder.Event{Kind: recorder.EventStreamStarted, Stream: 0, Streams: 1})
	if strings.Contains(buf.String(), "round") {
		t.Errorf("a single-round run announced a round: %q", buf.String())
	}
}

// TestHeaderLineUnknownEngine (TTP-37): an engine that did not identify itself
// prints "?", never the word "unknown".
func TestHeaderLineUnknownEngine(t *testing.T) {
	for _, kind := range []tape.ServerKind{tape.ServerUnknown, ""} {
		s := *card.Example()
		s.Server.Kind = kind
		got := headerLine(&s)
		if !strings.HasPrefix(got, "→ ? at http://127.0.0.1:8080 (b3650)") {
			t.Errorf("kind %q: header = %q", kind, got)
		}
		if strings.Contains(got, "unknown") {
			t.Errorf("kind %q: header prints the word unknown: %q", kind, got)
		}
	}
}
