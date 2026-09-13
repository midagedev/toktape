package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// What the example run has to be, as three measurements (TTP-28).
//
// The fixture is not decoration: it is the run the goldens pin, the run the
// hero clip plays, and the run a reader of the README sees before they have
// ever started the tool. So the three things the user asked for — figures a
// reader recognises, a screen that agrees with its own card, and a run long
// enough to watch — are gates rather than good intentions.

// TestExampleRunIsLongEnoughToWatch: the four-stream run, which is the hero,
// lasts between 24 and 27 seconds.
//
// Under that a viewer sees the layout and not the tool; over it the clip stops
// being postable (internal/render's per-streaming-second budget). The bound is
// the lead's, 2026-09-13.
func TestExampleRunIsLongEnoughToWatch(t *testing.T) {
	tp := ExampleTapeN(4)
	run := time.Duration(tp.Summary.Aggregate.WallMs * float64(time.Millisecond))
	if run < 24*time.Second || run > 27*time.Second {
		t.Errorf("the four-stream example run lasts %v, want between 24 s and 27 s", run.Round(time.Millisecond))
	}
	// Every stream spends most of its budget, and only the reasoning-only one
	// spends all of it: a fixture where every tile read "320/320" would teach
	// the reader that running out of budget is the ordinary ending.
	cut := exampleCutStream(4)
	for _, req := range tp.Requests {
		n := len(req.Tokens)
		switch {
		case req.Index == cut:
			if n != exampleMaxTokens || req.Prompt.FinishReason != "length" {
				t.Errorf("the cut stream produced %d tokens and finished %q, want %d and \"length\"",
					n, req.Prompt.FinishReason, exampleMaxTokens)
			}
		case n < 296 || n > 316:
			t.Errorf("stream %d produced %d tokens, want between 296 and 316", req.Index, n)
		case req.Prompt.FinishReason != "stop":
			t.Errorf("stream %d finished %q, want \"stop\"", req.Index, req.Prompt.FinishReason)
		}
	}
}

// TestExampleStreamsNeverRepeatThemselves: no eight-word run occurs twice
// inside one stream's monologue and answer together.
//
// A fixture whose material is shorter than the budget loops back to its own
// first sentence, and a tile that repeats itself reads as a model stuttering —
// which is the one thing a viewer of a twenty-five-second clip is certain to
// notice. Eight words is long enough that ordinary prose does not trip it and
// short enough to catch a loop the moment it starts.
func TestExampleStreamsNeverRepeatThemselves(t *testing.T) {
	const shingle = 8
	for n := 2; n <= 8; n++ {
		tp := ExampleTapeN(n)
		for _, req := range tp.Requests {
			text := req.Prompt.Reasoning + " " + req.Prompt.Completion
			words := strings.Fields(text)
			if len(words) < shingle {
				continue
			}
			seen := map[string]int{}
			for i := 0; i+shingle <= len(words); i++ {
				key := strings.Join(words[i:i+shingle], " ")
				if first, ok := seen[key]; ok {
					t.Errorf("n=%d stream %d repeats %q at words %d and %d",
						n, req.Index, key, first, i)
					break
				}
				seen[key] = i
			}
		}
	}
}

// TestExamplePrefillAgreesWithTheSummary: the figure the right pane computes
// from the progress rows equals the one the card prints, at every frame where
// the pane shows it.
//
// The two used to disagree by forty per cent — the pane said 2787 tok/s beside
// a card that said 1980 — because the fixture's rows were shaped without regard
// to the reduction reading them. A screen that contradicts the artifact it
// produces is worse than a screen with one fewer figure on it (CLAUDE.md:
// never fix a disagreement by picking the nicer number).
func TestExamplePrefillAgreesWithTheSummary(t *testing.T) {
	for _, n := range []int{2, 4, 8} {
		tp := ExampleTapeN(n)
		want := tp.Summary.Timings.PromptPerSecond
		for at := time.Duration(0); at <= 3*time.Second; at += 50 * time.Millisecond {
			live := livePromptRate(ModelAt(tp, at), false)
			if live == 0 {
				continue // nothing has been evaluated yet; the row prints "?"
			}
			if diff := (live - want) / want; diff > 0.05 || diff < -0.05 {
				t.Fatalf("n=%d at %v the live prefill is %.0f tok/s and the summary says %.0f (%.1f%% off)",
					n, at, live, want, diff*100)
			}
		}
	}
}

// TestExampleTapeIsTheCardsRun: the figures the tape derives from its own
// timeline are the figures the card fixture carries, so a clip that ends on the
// card cannot contradict the frames before it.
func TestExampleTapeIsTheCardsRun(t *testing.T) {
	tp := ExampleTapeN(8)
	a := tp.Summary.Aggregate
	want := card.ExampleConcurrent()
	// As printed: the tape measures its rates off the token timeline and the
	// card carries them as constants, so they agree to the digit a reader
	// sees rather than to the last bit of a float.
	for _, tc := range []struct {
		what      string
		got, want float64
	}{
		{"per-stream decode", a.PerStreamPredictedPerSecond, want.Aggregate.PerStreamPredictedPerSecond},
		{"aggregate decode", a.AggregatePredictedPerSecond, want.Aggregate.AggregatePredictedPerSecond},
		{"prefill", tp.Summary.Timings.PromptPerSecond, want.Timings.PromptPerSecond},
		{"ttft p50", a.TTFTp50Ms, want.Aggregate.TTFTp50Ms},
	} {
		if fmtRate(tc.got) != fmtRate(tc.want) {
			t.Errorf("%s is %s on the tape and %s on the card", tc.what, fmtRate(tc.got), fmtRate(tc.want))
		}
	}
	// And the run takes no major faults: the weights are in VRAM, so nothing
	// is paged in during decode and the fault sparkline is flat by fact rather
	// than by omission.
	if f := tp.Summary.Memory.MajFaultsPerToken; f != 0 {
		t.Errorf("maj/tok is %v on a fully offloaded run, want 0", f)
	}
}

// TestExampleMidRunShowsEveryThinkingStateAtEveryCount: the instant the goldens
// and the captures are framed at has to show all three states at any stream
// count, because the four-stream form is the hero and the eight-stream form is
// the goldens.
func TestExampleMidRunShowsEveryThinkingStateAtEveryCount(t *testing.T) {
	for n := 4; n <= 8; n++ {
		t.Run(fmt.Sprintf("n%d", n), func(t *testing.T) {
			m := ModelAt(ExampleTapeN(n), midRun)
			var thinking, crossed int
			for _, s := range m.Streams {
				if thinkingBadge(s) == "thinking" {
					thinking++
				}
				for _, bl := range streamTextLines(s, 48) {
					if bl.marker {
						crossed++
						break
					}
				}
			}
			if thinking == 0 {
				t.Errorf("no stream is thinking at %v", midRun)
			}
			if crossed == 0 {
				t.Errorf("no stream still shows the answer marker at %v", midRun)
			}
		})
	}
}

// TestExampleBandwidthFollowsItsOwnRate: the effective bandwidth on the card a
// clip ends on is derived from that clip's decode rate.
//
// The summary starts as a copy of the eight-stream card fixture, so every
// figure that depends on the stream count has to be recomputed or it describes
// a different run. Bandwidth is the one that says so least loudly: "12.5 tok/s
// · ≈ 410 GB/s" is arithmetic nobody can check and everybody would believe.
func TestExampleBandwidthFollowsItsOwnRate(t *testing.T) {
	for _, n := range []int{2, 4, 8} {
		tp := ExampleTapeN(n)
		perToken := float64(tp.Summary.Model.ActiveBytesPerToken)
		want := int64(perToken * tp.Summary.Aggregate.PerStreamPredictedPerSecond)
		if got := tp.Summary.Timings.EffectiveBandwidthBytesPerSec; got != want {
			t.Errorf("n=%d: effective bandwidth %d, want %d (%.0f bytes/token × %.2f tok/s)",
				n, got, want, perToken, tp.Summary.Aggregate.PerStreamPredictedPerSecond)
		}
	}
}

// TestPrefillReadsProcessedTheWayTheServerWritesIt pins which half of a
// return_progress row is which (TTP-28, 2026-09-13).
//
// llama-server fills the field from the slot's prompt buffer, and that buffer
// starts at the cached prefix and grows as chunks are evaluated
// (tools/server/server-context.cpp: `slot.prompt.tokens.keep_first(n_past)`
// followed by `progress.processed = slot.prompt.tokens.size()`). Its README
// says the same from the other side: "The overall progress is processed/total,
// while the actual timed progress is (processed-cache)/(total-cache)."
//
// So processed already contains the cache, and the two readers of a row have to
// split on that: the header prints processed against total, and the rate
// divides only the evaluated remainder by the elapsed time. Reading it the
// other way is invisible on a fixture built to match the misreading — both
// halves cancel — and shows up on a real tape as a header that counts past the
// prompt and a prefill rate inflated by the whole cached prefix. The row below
// is therefore built here rather than taken from the example: it is a row as
// the server writes one.
func TestPrefillReadsProcessedTheWayTheServerWritesIt(t *testing.T) {
	const (
		total     = 4096
		cache     = 2048
		evaluated = 1024
		elapsedMs = 500.0
	)
	row := tape.PromptProgress{
		Total:     total,
		Cache:     cache,
		Processed: cache + evaluated, // what the slot's prompt buffer holds
		TimeMs:    elapsedMs,
	}
	m := Model{Streams: []Stream{{Progress: []tape.PromptProgress{row}}}}

	// The rate is the evaluated remainder over the elapsed time, which is what
	// the server's own timings report as prompt_per_second.
	want := float64(evaluated) / (elapsedMs / 1000)
	if got := livePromptRate(m, false); got != want {
		t.Errorf("the live prefill rate is %.0f tok/s, want %.0f — processed carries the cache, and dividing it whole credits the prefix to the prefill", got, want)
	}

	// And the counts are processed against total: a header that added the cache
	// back in would print 5120 of a 4096-token prompt.
	head := streamHeader(m, PlainTheme(), m.Streams[0], 70, true, false)
	if w := fmt.Sprintf("%d/%d · cache %d", cache+evaluated, total, cache); !strings.Contains(head, w) {
		t.Errorf("the header reads %q, want it to contain %q", strings.TrimSpace(head), w)
	}
}
