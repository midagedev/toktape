package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// concurrentSparseMoE is the four-stream sparse take of internal/bandwidth's
// fourStreamSparseMoE, rebuilt here because that package is imported by this
// one and its test fixtures are not exportable (the same deal denseTwoGPU's
// comment documents). The numbers and their derivation are that fixture's:
//
//	2.970 GB active per token, of which 0.832 GB is the routed stack a token
//	reads (26.624 GB × 8/256), 4 streams at 37.6 tok/s each, one 750 GB/s GPU
//
// so the bounds are 111.672 and 205.522 GB/s, 15 % and 27 % of peak.
func concurrentSparseMoE() *tape.RunSummary {
	return &tape.RunSummary{
		Model: tape.ModelInfo{
			NExperts:            256,
			NExpertsUsed:        8,
			ActiveBytesPerToken: 2_970_000_000,
		},
		Host: tape.HostInfo{
			GPUs: []tape.GPUInfo{{Index: 0, PeakBandwidthBytesPerSec: 750_000_000_000}},
		},
		Placement: tape.PlacementSummary{
			Devices: []tape.DevicePlacement{{
				Device:              "GPU0",
				ActiveBytesPerToken: 2_970_000_000,
				Classes: map[tape.TensorClass]int64{
					tape.ClassAttention: 1_092_000_000,
					tape.ClassOutput:    540_000_000,
					tape.ClassOther:     288_000_000,
					tape.ClassExperts:   26_842_000_000,
				},
			}},
		},
		Concurrency: 4,
		Timings: tape.TimingsSummary{
			PredictedPerSecond:            37.6,
			EffectiveBandwidthBytesPerSec: 111_672_000_000,
		},
		Aggregate: tape.AggregateTimings{
			Streams:                     4,
			AggregatePredictedPerSecond: 149.0,
			PerStreamPredictedPerSecond: 37.6,
		},
	}
}

// TestDecodeRowPrintsTheRange (2026-09-16): four streams share a forward pass,
// so the row's bandwidth clause is the range the traffic lies between, not the
// lower bound dressed as the figure. Before this round the same take printed
// "≈ 112 GB/s, 15% of peak", which is every stream picking the same 8 experts.
func TestDecodeRowPrintsTheRange(t *testing.T) {
	got := Text(concurrentSparseMoE())
	if want := "≈ 112–206 GB/s, 15–27% of peak"; !strings.Contains(got, want) {
		t.Errorf("Decode row must carry the range, want %q in:\n%s", want, got)
	}
	if strings.Contains(got, "≈ 112 GB/s,") {
		t.Errorf("Decode row still prints the lower bound as the figure:\n%s", got)
	}
}

// TestDecodeRowRangeLeavesTheOtherClausesAlone: the range lands on the clause
// this round is about and nothing else moves.
func TestDecodeRowRangeLeavesTheOtherClausesAlone(t *testing.T) {
	// A single-stream run is pinned byte-for-byte by the card goldens; this
	// asserts the same row the cheap way, because the goldens do not carry a
	// concurrent sparse MoE and this file's fixture does.
	if got := Text(Example()); !strings.Contains(got, "≈ 785 GB/s, 84% of peak") {
		t.Errorf("Example(): the single-stream clause moved:\n%s", got)
	}

	// A mixed placement keeps its RAM row: the TTP-56 branch above the range
	// still decides what that card prints. (CombinedRange itself stays ok for
	// a mixed take — it mirrors Combined — so this is the guarantee that
	// actually matters, and it lives where the branch lives.)
	mixed := concurrentSparseMoE()
	mixed.Placement.Devices = []tape.DevicePlacement{
		{Device: "GPU0", ActiveBytesPerToken: 1_920_000_000, Classes: map[tape.TensorClass]int64{
			tape.ClassAttention: 1_092_000_000,
			tape.ClassOutput:    540_000_000,
			tape.ClassOther:     288_000_000,
		}},
		{Device: tape.DeviceCPU, ActiveBytesPerToken: 1_050_000_000, Classes: map[tape.TensorClass]int64{
			tape.ClassExperts: 26_842_000_000,
		}},
	}
	got := Text(mixed)
	if !strings.Contains(got, "from RAM") {
		t.Errorf("a mixed placement must keep its RAM clause:\n%s", got)
	}
	if strings.Contains(got, "112–206") {
		t.Errorf("a mixed placement prints one bus, not a range over all of them:\n%s", got)
	}

	// The concurrent dense fixture is the golden example-concurrent.txt; its
	// clause is a single figure because a dense model reads its weights once a
	// pass whatever the batch. Checked here for the same reason as Example().
	if got := Text(ExampleConcurrent()); !strings.Contains(got, "≈ 410 GB/s, 44% of peak") {
		t.Errorf("ExampleConcurrent(): the dense concurrent clause moved:\n%s", got)
	}
}
