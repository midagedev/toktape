package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// fourStreamsOneShort is the run TTP-83 is about: four streams, three of them
// healthy and one of 10 tokens, so the per-stream mean (228) clears
// tape.MinDecodeTokens while the shortest stream is a sample.
func fourStreamsOneShort(t *testing.T) *tape.RunSummary {
	t.Helper()
	s := clean(t)
	s.Concurrency = 4
	s.Timings.PredictedN = (300 + 300 + 300 + 10 + 2) / 4
	s.Aggregate.Streams = 4
	s.Aggregate.TotalPredictedN = 910
	s.Aggregate.MinPredictedN = 10
	return s
}

// TestAShortStreamIsNotHiddenByTheMean (TTP-83, 2026-09-14): above one stream
// Timings.PredictedN is a mean, and a 10-token stream beside 300-token ones
// averages clear of the floor. The card must read the minimum the recorder
// kept, name the shortest stream's token count, and keep the Decode label: the
// aggregate is a rate and the qualification is about the "each" figure.
func TestAShortStreamIsNotHiddenByTheMean(t *testing.T) {
	s := fourStreamsOneShort(t)
	cs := Caveats(s)
	if len(cs) != 1 || cs[0].Code != CodeShortStream {
		t.Fatalf("Caveats = %+v, want exactly [%s]", cs, CodeShortStream)
	}
	if cs[0].Severity != SeverityFigure {
		t.Errorf("severity = %q, want %q: the each figure does not mean what it looks like",
			cs[0].Severity, SeverityFigure)
	}
	for _, want := range []string{"10 tokens", "under 32"} {
		if !strings.Contains(cs[0].Text, want) {
			t.Errorf("text %q does not name %q", cs[0].Text, want)
		}
	}
	// Qualify, not relabel: three healthy streams are not a sample.
	if IsSample(s) {
		t.Error("IsSample fired on a run whose mean is a rate; the short stream is a caveat, not a label")
	}
	if text := Text(s); !strings.Contains(text, "Decode ") || strings.Contains(text, "Sample ") {
		t.Errorf("the row was relabelled:\n%s", text)
	}
}

// TestAnUnknownMinimumFiresNothing: MinPredictedN is 0 on a tape older than
// the field and on a run with no answered stream (tape.AggregateTimings). Both
// are unknown, and unknown is never a short stream.
func TestAnUnknownMinimumFiresNothing(t *testing.T) {
	s := fourStreamsOneShort(t)
	s.Aggregate.MinPredictedN = 0
	if cs := Caveats(s); len(cs) != 0 {
		t.Errorf("an unrecorded minimum raised %+v", cs)
	}
	if line := lineFor(t, ExplainCaveats(s), CodeShortStream); !strings.Contains(line, "min_predicted_n ?") {
		t.Errorf("an unrecorded minimum is listed %q, want it printed as ?", line)
	}
}

// TestTheShortStreamCaveatStandsAside: at the floor the stream is a rate, and
// when the mean itself is under the floor the row is already Sample and
// short_generation says so — a second caveat for the same tokens would be the
// wall the caveat line was built to avoid.
func TestTheShortStreamCaveatStandsAside(t *testing.T) {
	atFloor := fourStreamsOneShort(t)
	atFloor.Aggregate.MinPredictedN = tape.MinDecodeTokens
	if cs := Caveats(atFloor); len(cs) != 0 {
		t.Errorf("a %d-token shortest stream raised %+v", tape.MinDecodeTokens, cs)
	}

	allShort := fourStreamsOneShort(t)
	allShort.Timings.PredictedN, allShort.Timings.DecodeLabel = 19, "sample"
	cs := Caveats(allShort)
	if len(cs) != 1 || cs[0].Code != CodeShortGeneration {
		t.Errorf("Caveats = %+v, want only %s when the mean is already a sample", cs, CodeShortGeneration)
	}
}

// TestTheShortStreamRanksUnderShortGenerationAndOverCold: the rank comment's
// claim, checked where the card spends its one spelled-out sentence.
func TestTheShortStreamRanksUnderShortGenerationAndOverCold(t *testing.T) {
	s := fourStreamsOneShort(t)
	s.Cache.Label = tape.CacheCold
	s.Memory.MajFaultsPerToken = tape.ColdMajFaultsPerToken + 0.4
	cs := Caveats(s)
	if len(cs) != 2 || cs[0].Code != CodeShortStream || cs[1].Code != CodeColdCache {
		t.Fatalf("Caveats = %+v, want [%s %s]", cs, CodeShortStream, CodeColdCache)
	}
	joined := strings.Join(warningSection(s), " ")
	if !strings.Contains(joined, "2 caveats") || !strings.Contains(joined, CodeColdCache) {
		t.Errorf("the caveat line lost the cold run:\n%s", joined)
	}
	if !caveatRankOrdered(CodeShortGeneration, CodeShortStream, CodeColdCache) {
		t.Errorf("caveatRank does not put %s between %s and %s", CodeShortStream, CodeShortGeneration, CodeColdCache)
	}
}

func caveatRankOrdered(codes ...string) bool {
	for i := 1; i < len(codes); i++ {
		if caveatRank[codes[i-1]] >= caveatRank[codes[i]] {
			return false
		}
	}
	return true
}

// TestExplainShowsTheShortStreamReading: the listing prints the minimum it
// decided on beside the mean it did not, so "why did this four-stream card not
// warn" is answered by the two numbers.
func TestExplainShowsTheShortStreamReading(t *testing.T) {
	s := fourStreamsOneShort(t)
	line := lineFor(t, ExplainCaveats(s), CodeShortStream)
	for _, want := range []string{"Figure", "min_predicted_n 10", "mean 228", "floor 32"} {
		if !strings.Contains(line, want) {
			t.Errorf("listing line %q lacks %q", line, want)
		}
	}
}
