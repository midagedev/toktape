package png

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// TestTheShortStreamQualificationReachesTheDecodeEyebrow (TTP-83, 2026-09-14).
// A four-stream run with one 10-token stream keeps its "aggregate decode"
// eyebrow — the aggregate is a rate — and adds the qualification in the slot
// the prefill column already uses for "short prompt".
func TestTheShortStreamQualificationReachesTheDecodeEyebrow(t *testing.T) {
	s := card.ExampleConcurrent()
	if m := decodeEyebrowOf(t, s); strings.Contains(m, "SHORT STREAM") {
		t.Fatalf("a run with no recorded minimum is qualified anyway: %q", m)
	}
	s.Aggregate.MinPredictedN = 10
	got := decodeEyebrowOf(t, s)
	if !strings.Contains(got, "DECODE") || strings.Contains(got, "SAMPLE") {
		t.Errorf("decode eyebrow = %q, want DECODE kept", got)
	}
	if !strings.Contains(got, "SHORT STREAM") {
		t.Errorf("decode eyebrow = %q, want the short-stream qualification", got)
	}
	m, _ := mustCanvas(t, s).markByID("hero.left.eyebrow")
	if strings.Contains(m.Text, ellipsis) || m.Rect.Max.X > heroSplitX-heroGutter {
		t.Errorf("the eyebrow %q ends at x=%d, past its column (%d)", m.Text, m.Rect.Max.X, heroSplitX-heroGutter)
	}
}

// TestTheShortStreamQualifiesTheSingleConnectionEyebrowToo: a run of several
// sequential rounds at one connection takes the single-stream hero branch while
// Timings is still a mean over every round's stream, so the qualification is
// not the concurrent branch's alone.
func TestTheShortStreamQualifiesTheSingleConnectionEyebrowToo(t *testing.T) {
	s := card.Example()
	s.Aggregate.Streams, s.Aggregate.MinPredictedN = 6, 10
	if got := decodeEyebrowOf(t, s); got != "DECODE · SHORT STREAM" {
		t.Errorf("decode eyebrow = %q, want %q", got, "DECODE · SHORT STREAM")
	}
}

// TestTheDecodeEyebrowAsksIsSample: the image labels the figure through the
// one predicate the text card and the TUI ask. It read the recorder's stored
// DecodeLabel directly, so a tape whose label said "decode" over 19 tokens was
// Sample on the text card and DECODE on the image.
func TestTheDecodeEyebrowAsksIsSample(t *testing.T) {
	s := card.Example()
	s.Timings.PredictedN, s.Timings.DecodeLabel = 19, "decode"
	if got := decodeEyebrowOf(t, s); got != "SAMPLE" {
		t.Errorf("decode eyebrow = %q, want SAMPLE, as card.IsSample says", got)
	}
}

func decodeEyebrowOf(t *testing.T, s *tape.RunSummary) string {
	t.Helper()
	m, ok := mustCanvas(t, s).markByID("hero.left.eyebrow")
	if !ok {
		t.Fatal("hero.left.eyebrow was never drawn")
	}
	return m.Text
}
