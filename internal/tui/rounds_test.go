package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// The rounds contract (TTP-38). `record --prompts` writes K sequential rounds
// of N streams into one tape, and every stream index 0..N-1 comes back in each
// round. The screen draws one round at a time — live, in replay and in a clip —
// and says which one in the title.

// roundsTape is the example the gates below run over: three rounds of four
// streams, named sql-1, prose-1 and code-1.
func roundsTape() *tape.Tape { return ExampleRoundsTape(4, 3) }

// inRound1 is an instant well inside round 1 (the second round): every stream
// has decoded tokens and none has finished.
func inRound1(tp *tape.Tape) time.Duration {
	return ExampleRoundStart(tp, 1) + 5*time.Second
}

// roundEventsOf is eventsOf for a rounds tape: the events a live recorder
// sends, with the round, the round count and the round's name on every
// stream start, and the request's cap with it.
func roundEventsOf(tp *tape.Tape) []Event {
	var evs []Event
	evs = append(evs, Event{Kind: EventProps, Server: tp.Summary.Server, Model: tp.Summary.Model,
		Host: tp.Summary.Host, Placement: tp.Summary.Placement})
	for _, req := range tp.Requests {
		name := ""
		if req.Round < len(tp.Summary.PerRound) {
			name = tp.Summary.PerRound[req.Round].Name
		}
		evs = append(evs, Event{Kind: EventStreamStart, Stream: req.Index, T: req.StartedAt,
			Round: req.Round, Rounds: tp.Summary.Rounds, RoundName: name,
			MaxTokens: req.Prompt.MaxTokens})
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
	// Arrival order, stable, and a stream start before anything else stamped
	// at the same instant: the recorder announces a request before it sends it.
	for i := 1; i < len(evs); i++ {
		e := evs[i]
		j := i - 1
		for j >= 0 && evs[j].T > e.T {
			evs[j+1] = evs[j]
			j--
		}
		evs[j+1] = e
	}
	return evs
}

// TestApplyResetsTheTilesOnANewRound is the live half: round 1's first stream
// start clears what round 0 left on every tile, so its tokens do not append to
// round 0's text, its count does not run past the cap, and its first token's
// latency is not the gap between the rounds.
func TestApplyResetsTheTilesOnANewRound(t *testing.T) {
	tp := roundsTape()
	start1 := ExampleRoundStart(tp, 1)

	live := Model{Mode: ModeLive}
	var rest []Event
	for _, ev := range roundEventsOf(tp) {
		if ev.T < start1 {
			live = live.Apply(ev)
			continue
		}
		rest = append(rest, ev)
	}
	if got := len(live.Streams[0].Tokens); got == 0 {
		t.Fatal("round 0 left no tokens on the tiles; the test is not exercising a reset")
	}
	if live.Round != 0 || live.Rounds != 3 || live.RoundName != "sql-1" {
		t.Errorf("after round 0: Round %d Rounds %d RoundName %q, want 0 3 sql-1", live.Round, live.Rounds, live.RoundName)
	}
	if live.Streams[1].Tokens[0].Reasoning != true {
		t.Fatal("round 0's stream 2 does not think; the badge reset is not exercised")
	}

	// Round 1's first stream start, carrying a cap round 0 never used.
	const cap1 = 200
	first := rest[0]
	if first.Kind != EventStreamStart || first.Round != 1 {
		t.Fatalf("the first event of round 1 is %+v, want its stream start", first)
	}
	first.MaxTokens = cap1
	live = live.Apply(first)

	if len(live.Streams) != 4 {
		t.Errorf("%d streams after the round change, want the round's 4", len(live.Streams))
	}
	if live.Summary.Concurrency != 4 {
		t.Errorf("Concurrency = %d, want 4", live.Summary.Concurrency)
	}
	if live.Round != 1 || live.Rounds != 3 || live.RoundName != "prose-1" {
		t.Errorf("Round %d Rounds %d RoundName %q, want 1 3 prose-1", live.Round, live.Rounds, live.RoundName)
	}
	if len(live.Samples) == 0 {
		t.Error("the host samples were reset; they run across the whole tape")
	}
	for _, s := range live.Streams {
		if len(s.Tokens) != 0 || s.Text != "" || s.Done || s.EndedAt != 0 || len(s.Progress) != 0 ||
			s.Err != "" || s.Timings != (tape.TimingsSummary{}) || s.Cache != (tape.CacheSummary{}) {
			t.Errorf("stream %d kept round 0 state: %d tokens, text %q, done %v, ended %v, %d progress rows",
				s.Index+1, len(s.Tokens), clip(s.Text, 20), s.Done, s.EndedAt, len(s.Progress))
		}
		if thinkingBadge(s) != "" {
			t.Errorf("stream %d kept round 0's badge %q", s.Index+1, thinkingBadge(s))
		}
		want := 0
		if s.Index == first.Stream {
			want = cap1
		}
		if s.MaxTokens != want {
			t.Errorf("stream %d MaxTokens = %d, want %d (round 1's, or unknown until its own start)", s.Index+1, s.MaxTokens, want)
		}
	}

	// The rest of round 1 up to an instant inside it.
	at := inRound1(tp)
	for _, ev := range rest[1:] {
		if ev.T > at {
			break
		}
		live = live.Apply(ev)
	}
	for _, s := range live.Streams {
		if len(s.Tokens) == 0 {
			t.Fatalf("stream %d has no round 1 tokens at %v", s.Index+1, at)
		}
		if s.Tokens[0].ITL != 0 {
			t.Errorf("stream %d: the first token of round 1 has ITL %v, want 0", s.Index+1, s.Tokens[0].ITL)
		}
		if s.MaxTokens > 0 && len(s.Tokens) > s.MaxTokens {
			t.Errorf("stream %d: %d tokens past its cap of %d", s.Index+1, len(s.Tokens), s.MaxTokens)
		}
		if s.Tokens[0].T < ExampleRoundStart(tp, 1) {
			t.Errorf("stream %d still holds a token from before round 1", s.Index+1)
		}
	}
}

// TestModelAtShowsOneRound is the replay half.
func TestModelAtShowsOneRound(t *testing.T) {
	tp := roundsTape()
	start1, start2 := ExampleRoundStart(tp, 1), ExampleRoundStart(tp, 2)
	end := tapeEnd(tp)

	cases := []struct {
		name  string
		at    time.Duration
		round int
		rname string
	}{
		{"t=0", 0, 0, "sql-1"},
		{"the gap before round 1", start1 - 500*time.Millisecond, 0, "sql-1"},
		{"round 1 starts", start1, 1, "prose-1"},
		{"inside round 1", inRound1(tp), 1, "prose-1"},
		{"inside round 2", start2 + 3*time.Second, 2, "code-1"},
		{"after the end", end + time.Minute, 2, "code-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := ModelAt(tp, tc.at)
			if m.Round != tc.round || m.Rounds != 3 || m.RoundName != tc.rname {
				t.Errorf("Round %d Rounds %d RoundName %q, want %d 3 %q", m.Round, m.Rounds, m.RoundName, tc.round, tc.rname)
			}
			if len(m.Streams) != 4 {
				t.Fatalf("%d streams, want exactly 4", len(m.Streams))
			}
			seen := map[int]bool{}
			for p, s := range m.Streams {
				if s.Index != p || seen[s.Index] {
					t.Errorf("stream at %d has Index %d (seen before: %v)", p, s.Index, seen[s.Index])
				}
				seen[s.Index] = true
				req := roundRequest(t, tp, tc.round, s.Index)
				if len(s.Tokens) > len(req.Tokens) {
					t.Errorf("stream %d has %d tokens, its round-%d request %d", s.Index+1, len(s.Tokens), tc.round, len(req.Tokens))
				}
				for j, tk := range s.Tokens {
					if tk.Text != req.Tokens[j].Text || tk.T != req.StartedAt+req.Tokens[j].T {
						t.Fatalf("stream %d token %d is not round %d's", s.Index+1, j, tc.round)
					}
				}
				if s.StartedAt != req.StartedAt {
					t.Errorf("stream %d StartedAt %v, want round %d's %v", s.Index+1, s.StartedAt, tc.round, req.StartedAt)
				}
			}
			// The run is finished only when its last round is: a round that
			// has ended with more to come must not put the done footer and the
			// card hint up during the gap.
			if wantDone := tc.at >= end; m.Done != wantDone {
				t.Errorf("Done = %v at %v, want %v", m.Done, tc.at, wantDone)
			}
			if m.Summary.Concurrency != 4 {
				t.Errorf("Concurrency = %d, want 4", m.Summary.Concurrency)
			}
		})
	}

	// Every stream of a round that has ended is itself finished, even while
	// the run is not.
	gap := ModelAt(tp, start1-500*time.Millisecond)
	for _, s := range gap.Streams {
		if !s.Done {
			t.Errorf("in the gap after round 0, stream %d is not finished", s.Index+1)
		}
	}
}

// TestApplyMatchesModelAtInsideARound: the live fold and the replay agree on
// the tiles at an instant inside a later round, not only once the tape has
// been rebuilt on EventDone.
func TestApplyMatchesModelAtInsideARound(t *testing.T) {
	tp := roundsTape()
	at := inRound1(tp)
	live := Model{Mode: ModeLive}
	for _, ev := range roundEventsOf(tp) {
		if ev.T > at {
			break
		}
		live = live.Apply(ev)
	}
	replay := ModelAt(tp, at)
	if live.Round != replay.Round || live.RoundName != replay.RoundName || live.Rounds != replay.Rounds {
		t.Errorf("live round %d/%d %q, replay %d/%d %q", live.Round+1, live.Rounds, live.RoundName,
			replay.Round+1, replay.Rounds, replay.RoundName)
	}
	if len(live.Streams) != len(replay.Streams) {
		t.Fatalf("live has %d streams, replay %d", len(live.Streams), len(replay.Streams))
	}
	for i := range replay.Streams {
		a, b := live.Streams[i], replay.Streams[i]
		if a.Text != b.Text || len(a.Tokens) != len(b.Tokens) {
			t.Fatalf("stream %d: live %d tokens, replay %d", i+1, len(a.Tokens), len(b.Tokens))
		}
		for j := range b.Tokens {
			if a.Tokens[j] != b.Tokens[j] {
				t.Fatalf("stream %d token %d: %+v != %+v", i+1, j, a.Tokens[j], b.Tokens[j])
			}
		}
	}
}

func roundRequest(t *testing.T, tp *tape.Tape, round, index int) tape.RequestRecord {
	t.Helper()
	for _, req := range tp.Requests {
		if req.Round == round && req.Index == index {
			return req
		}
	}
	t.Fatalf("no request %d in round %d", index, round)
	return tape.RequestRecord{}
}

// TestTitleNamesTheRound: the title bar says which round is on screen, in dim,
// right after the model.
func TestTitleNamesTheRound(t *testing.T) {
	tp := roundsTape()
	m := ModelAt(tp, inRound1(tp))
	frame := View(m, 0, 120, 36)
	top := strings.SplitN(frame, "\n", 2)[0]
	if !strings.Contains(top, "Q4_K_M · round 2/3 prose-1 · llama-server") {
		t.Errorf("the title does not name the round after the model:\n%s", top)
	}

	// Colour: the whole segment is dim, and none of it is accent.
	m.Theme = ColourTheme()
	coloured := strings.SplitN(View(m, 0, 120, 36), "\n", 2)[0]
	if !strings.Contains(coloured, m.Theme.paint(m.Theme.dim, "round 2/3 prose-1")) {
		t.Errorf("the round segment is not painted dim:\n%q", coloured)
	}

	// A round without a name prints its number alone.
	unnamed := *tp
	unnamed.Summary.PerRound = append([]tape.RoundSummary(nil), tp.Summary.PerRound...)
	unnamed.Summary.PerRound[1].Name = ""
	top = strings.SplitN(View(ModelAt(&unnamed, inRound1(tp)), 0, 120, 36), "\n", 2)[0]
	if !strings.Contains(top, "· round 2/3 · llama-server") {
		t.Errorf("an unnamed round did not print its number alone:\n%s", top)
	}

	// A single-round tape has no round segment at all.
	if top := strings.SplitN(View(ModelAt(ExampleTapeN(4), midRun), 0, 120, 36), "\n", 2)[0]; strings.Contains(top, "round") {
		t.Errorf("a single-round run names a round:\n%s", top)
	}
}

// TestTitleDropsTheRigFirst pins the order the title gives segments up in when
// it does not fit: the rig, then the engine, then the round. The model is never
// cut while any of the round is on screen.
func TestTitleDropsTheRigFirst(t *testing.T) {
	tp := roundsTape()
	m := ModelAt(tp, inRound1(tp))
	const (
		// 2026-09-15 (user: "모델이 다 실제값으로 찍혀야해"): the segment is
		// card.ModelNameQuant — the file's stem, which carries the quant — not
		// the header's "R1 Distill Llama 70B Q4_K_M". FAIL-first: the old title
		// spelled the segment the general.name way and this test pinned it.
		model  = "DeepSeek-R1-Distill-Llama-70B-Q4_K_M"
		round  = "round 2/3 prose-1"
		engine = "llama-server b3650"
		rig    = "2× RTX 3090"
	)
	full := "─ toktape · " + model + " · " + round + " · " + engine + " · " + rig
	// The column each segment ends at, measured on the untruncated title.
	endOf := func(seg string) int { return width(full[:strings.Index(full, seg)+len(seg)]) }

	cases := []struct {
		name  string
		inner int
		// whole says which segments must be on the bar in full; the rest must
		// be cut or absent.
		whole map[string]bool
	}{
		{"everything fits", endOf(rig) + 2, map[string]bool{model: true, round: true, engine: true, rig: true}},
		{"the rig is cut", endOf(rig) - 3, map[string]bool{model: true, round: true, engine: true}},
		{"the rig is gone and the engine cut", endOf(engine) - 4, map[string]bool{model: true, round: true}},
		{"the engine is gone and the round cut", endOf(round) - 5, map[string]bool{model: true}},
		{"only the model is left", endOf(model) + 1, map[string]bool{model: true}},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/inner=%d", tc.name, tc.inner), func(t *testing.T) {
			bar := card.StripANSI(topBorder(m, PlainTheme(), 0, tc.inner))
			if w := width(bar); w != tc.inner+2 {
				t.Fatalf("title is %d columns, want %d", w, tc.inner+2)
			}
			for _, seg := range []string{model, round, engine, rig} {
				if got := strings.Contains(bar, seg); got != tc.whole[seg] {
					t.Errorf("%q whole on the bar = %v, want %v\n%s", seg, got, tc.whole[seg], bar)
				}
			}
			// The order, whatever the width: a later segment is never on the
			// bar while an earlier one is cut.
			if !strings.Contains(bar, round) && strings.Contains(bar, "llama-server") {
				t.Errorf("the engine outlived the round:\n%s", bar)
			}
			if !strings.Contains(bar, engine) && strings.Contains(bar, "RTX") {
				t.Errorf("the rig outlived the engine:\n%s", bar)
			}
			if strings.Contains(bar, "round") && !strings.Contains(bar, model) {
				t.Errorf("the model was cut before the round:\n%s", bar)
			}
		})
	}
	// Every width from wide to narrow keeps the order.
	for inner := endOf(rig) + 4; inner >= 20; inner-- {
		bar := card.StripANSI(topBorder(m, PlainTheme(), 0, inner))
		if !strings.Contains(bar, round) && strings.Contains(bar, "llama-server") ||
			!strings.Contains(bar, engine) && strings.Contains(bar, "RTX") ||
			strings.Contains(bar, "round") && !strings.Contains(bar, model) {
			t.Errorf("inner=%d drops out of order:\n%s", inner, bar)
		}
	}
}

// TestTitleUnknownEnginePrintsQuestionMark (TTP-37): an engine toktape could
// not identify prints "?", never the word "unknown", joined to its build the
// way the card joins it.
func TestTitleUnknownEnginePrintsQuestionMark(t *testing.T) {
	cases := []struct {
		name string
		srv  tape.ServerInfo
		want string // the engine segment, between separators
	}{
		{"unknown kind with a build", tape.ServerInfo{Kind: tape.ServerUnknown, Build: "b3650", URL: "http://h:8080"}, " · ? b3650 · "},
		{"unknown kind alone is a lone ?", tape.ServerInfo{Kind: tape.ServerUnknown, URL: "http://h:8080"}, " · ? · "},
		{"an empty kind with a build", tape.ServerInfo{Build: "b3650", URL: "http://h:8080"}, " · ? b3650 · "},
		{"a known kind is unchanged", tape.ServerInfo{Kind: tape.ServerIKLlama, URL: "http://h:8080"}, " · ik_llama.cpp · "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := card.ExampleConcurrent()
			s.Server = tc.srv
			m := Model{Summary: *s}
			top := strings.SplitN(View(m, 0, 140, 40), "\n", 2)[0]
			if !strings.Contains(top, tc.want) {
				t.Errorf("title %q does not carry %q", top, tc.want)
			}
			if strings.Contains(top, "unknown") {
				t.Errorf("title prints the word unknown: %q", top)
			}
		})
	}
}

// TestTitleBeforeAttachHasNoEngine: a live model that has not attached yet
// knows no kind and no build, and the title waits rather than printing "?"
// for a question it has not asked (TTP-37, lead review 2026-09-13).
func TestTitleBeforeAttachHasNoEngine(t *testing.T) {
	m := Model{Summary: tape.RunSummary{Server: tape.ServerInfo{URL: "http://h:8080"}}}
	top := strings.SplitN(View(m, 0, 140, 40), "\n", 2)[0]
	if strings.Contains(top, "?") {
		t.Errorf("title before attach prints an engine: %q", top)
	}
}
