package card

import (
	"testing"

	"github.com/midagedev/toktape/internal/bandwidth"
	"github.com/midagedev/toktape/internal/tape"
)

// allCPUSpeculativeRun is a draft-model run whose weights are entirely in host
// RAM: one bus, and it is the one the verify step reads.
//
// The figures are internally consistent rather than recorded, which is what
// this fixture is for — the recorded speculative run (ExampleSpeculative) is a
// MIXED placement, so it cannot ask the question below. A token reads
// experts×used/count plus the attention block:
//
//	64 GB × 8/128 + 1 GB = 5 GB/token
//
// so the per-device figure, the model figure and the class totals all agree
// and bandwidth.DeviceExpertsSplit solves to a dense share of zero.
func allCPUSpeculativeRun() *tape.RunSummary {
	draftN, accepted := 240, 120
	return &tape.RunSummary{
		Concurrency: 1,
		Model: tape.ModelInfo{
			ActiveBytesPerToken: 5_000_000_000,
			NExperts:            128,
			NExpertsUsed:        8,
		},
		Host: tape.HostInfo{
			RAMBytesPerSec: 200_000_000_000,
			RAMSource:      tape.RAMSourceMeasured,
		},
		Placement: tape.PlacementSummary{
			Devices: []tape.DevicePlacement{{
				Device:              tape.DeviceCPU,
				Bytes:               65_000_000_000,
				ActiveBytesPerToken: 5_000_000_000,
				Classes: map[tape.TensorClass]int64{
					tape.ClassExperts:   64_000_000_000,
					tape.ClassAttention: 1_000_000_000,
				},
			}},
		},
		Timings: tape.TimingsSummary{
			PredictedN:         200,
			PredictedMs:        10_000,
			PredictedPerSecond: 20,
			DraftN:             &draftN,
			DraftNAccepted:     &accepted,
		},
	}
}

// TestVerifyRAMReadsTheRatioItIsGiven: the card divides nothing (2026-09-14).
//
// VerifyRAM used to compute the ratio itself — bandwidth.Verify.RAMBytesPerSec
// over bandwidth.HostBytesPerSec — and gate it on bandwidth.RAMSide.Exact,
// which is a judgement about a figure internal/bandwidth built out of its own
// per-device active bytes. Both now belong to Verify, and this pins that the
// card reports them rather than a second opinion of its own.
func TestVerifyRAMReadsTheRatioItIsGiven(t *testing.T) {
	s := allCPUSpeculativeRun()
	v, ok := bandwidth.Speculative(s)
	if !ok {
		t.Fatal("Speculative is not derivable from the fixture")
	}
	bps, ofPeak, ok := VerifyRAM(s)
	if !ok {
		t.Fatal("VerifyRAM not ok")
	}
	if bps != v.RAMBytesPerSec {
		t.Errorf("VerifyRAM rate = %d, want %d — the card must report the package's figure", bps, v.RAMBytesPerSec)
	}
	if ofPeak != v.OfPeak {
		t.Errorf("VerifyRAM ofPeak = %v, want %v — the card must report the package's ratio", ofPeak, v.OfPeak)
	}
}

// TestVerifyRAMRatioOnAnAllCPUPlacement: a run with every weight in host RAM
// gets its "of peak" ratio (2026-09-14).
//
// This is a deliberate behaviour change. The old gate required
// bandwidth.RAM(s) to be ok, and RAM() reports nothing on a placement that is
// not MIXED — not because the host figure is untrustworthy there but because
// splitting one bus out of one bus says nothing. The verify-step rate is a
// different figure with the same denominator: on an all-CPU box the host bus
// is the only wall there is, and withholding the percentage there hid the one
// reading the card exists to give.
func TestVerifyRAMRatioOnAnAllCPUPlacement(t *testing.T) {
	s := allCPUSpeculativeRun()
	if _, ok := bandwidth.RAM(s); ok {
		t.Fatal("the fixture is meant to be a single-bus placement, which RAM() reports nothing about")
	}
	bps, ofPeak, ok := VerifyRAM(s)
	if !ok {
		t.Fatal("VerifyRAM not ok")
	}
	if bps <= 0 {
		t.Fatalf("VerifyRAM rate = %d", bps)
	}
	if ofPeak <= 0 {
		t.Error("no ratio on a placement whose only bus is the host's; the wall is knowable here")
	}
	// The ratio is the rate over the stated host bandwidth and nothing else.
	want := float64(bps) / float64(s.Host.RAMBytesPerSec)
	if ofPeak != want {
		t.Errorf("ofPeak = %v, want %v", ofPeak, want)
	}
}

// TestVerifyRAMWithholdsTheRatioWhenTheCountMayBeShort: "we could not check"
// is not "we checked and it is fine" (RAMSide.Exact). With no per-device
// figure and router/shared-expert bytes unaccounted for, the rate is still
// printed and the percentage is not.
func TestVerifyRAMWithholdsTheRatioWhenTheCountMayBeShort(t *testing.T) {
	s := allCPUSpeculativeRun()
	// A pre-TTP-68 tape: the device states no active bytes of its own, and
	// the model's total no longer solves the class split (the dense share
	// lands outside [0, E]), so the attribution cannot be proved complete.
	s.Placement.Devices[0].ActiveBytesPerToken = 0
	s.Model.ActiveBytesPerToken = 20_000_000_000
	if v, ok := bandwidth.Speculative(s); !ok || v.Exact {
		t.Fatalf("the fixture should be derivable and not exact: ok=%v exact=%v", ok, v.Exact)
	}
	bps, ofPeak, ok := VerifyRAM(s)
	if !ok || bps <= 0 {
		t.Fatalf("VerifyRAM = %d, ok=%v; the rate is printed either way", bps, ok)
	}
	if ofPeak != 0 {
		t.Errorf("ofPeak = %v over a count that may be short", ofPeak)
	}
}
