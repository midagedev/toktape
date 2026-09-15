package recorder

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestRepresentativeTimingsAgreesOnTheMeans (lead, 2026-09-15). Above one
// stream RunSummary.Timings is the per-stream mean, and the agreement flag has
// to describe the figures the card prints beside it — those means — not the
// AND of the streams' own verdicts. The real tape this round is about read
// 11.79 against 11.49 on one stream (2.6 %, disagreeing) and 11.57 against
// 11.58 on the other (agreeing): the means, 11.68 against 11.54, agree to
// 1.2 %, so the AND made the caveat contradict the numbers it printed. The
// per-stream fact is Aggregate.DisagreeingStreams now.
//
// FAIL-first: before this commit the flag was the AND of the per-stream flags,
// so the first case below returned false.
func TestRepresentativeTimingsAgreesOnTheMeans(t *testing.T) {
	stream := func(server, client float64, agrees bool) tape.RequestRecord {
		return tape.RequestRecord{Timings: tape.TimingsSummary{
			PromptN:                  640,
			PredictedN:               213,
			PredictedPerSecond:       server,
			ClientPredictedPerSecond: client,
			ClientAgreesWithServer:   agrees,
		}}
	}

	t.Run("the means agree while one stream does not", func(t *testing.T) {
		got := representativeTimings([]tape.RequestRecord{
			stream(11.4935, 11.7869, false), // the tape's stream 0: 2.6 % apart
			stream(11.5775, 11.5671, true),  // the tape's stream 1: 0.1 % apart
		})
		if !got.ClientAgreesWithServer {
			t.Errorf("ClientAgreesWithServer = false, want true: the means are %.4f against %.4f, 1.2 %% apart",
				got.ClientPredictedPerSecond, got.PredictedPerSecond)
		}
	})

	t.Run("the means disagree", func(t *testing.T) {
		got := representativeTimings([]tape.RequestRecord{
			stream(10, 12, false),
			stream(10, 12, false),
		})
		if got.ClientAgreesWithServer {
			t.Errorf("ClientAgreesWithServer = true, want false: the means are 10 against 12")
		}
	})

	t.Run("a single stream keeps its own verdict", func(t *testing.T) {
		got := representativeTimings([]tape.RequestRecord{stream(11.4935, 11.7869, false)})
		if got.ClientAgreesWithServer {
			t.Error("the single-stream path returned a summary's verdict, not the stream's own")
		}
	})

	t.Run("a rate that was never measured still does not agree", func(t *testing.T) {
		got := representativeTimings([]tape.RequestRecord{
			stream(0, 11.7, false),
			stream(0, 11.6, false),
		})
		if got.ClientAgreesWithServer {
			t.Error("ClientAgreesWithServer = true with no server figure to agree with")
		}
	})
}
