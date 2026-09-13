package bandwidth

import (
	"math"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

func TestHostBytesPerSec(t *testing.T) {
	cases := []struct {
		name string
		host tape.HostInfo
		want int64
	}{
		{
			// The fixture rig of card.Example() / card.ExampleSpeculative().
			name: "DDR5-6000 dual channel",
			host: tape.HostInfo{RAMSpeed: "DDR5-6000", RAMChannels: 2},
			want: 96_000_000_000,
		},
		{
			// The fixture rig of card.ExampleSharded().
			name: "DDR5-4800 twelve channels",
			host: tape.HostInfo{RAMSpeed: "DDR5-4800", RAMChannels: 12},
			want: 460_800_000_000,
		},
		{
			name: "DDR4-3200 dual channel",
			host: tape.HostInfo{RAMSpeed: "DDR4-3200", RAMChannels: 2},
			want: 51_200_000_000,
		},
		{
			name: "a generation the table has never seen still parses",
			host: tape.HostInfo{RAMSpeed: "LPDDR5-8533", RAMChannels: 8},
			want: 546_112_000_000,
		},
		{
			// procmon leaves both empty on every real Linux run: DMI is
			// root-only. Unknown is unknown, never a guessed default.
			name: "nothing observed",
			host: tape.HostInfo{},
			want: 0,
		},
		{
			name: "speed without a channel count",
			host: tape.HostInfo{RAMSpeed: "DDR5-6000"},
			want: 0,
		},
		{
			name: "channel count without a speed",
			host: tape.HostInfo{RAMChannels: 2},
			want: 0,
		},
		{
			name: "negative channel count",
			host: tape.HostInfo{RAMSpeed: "DDR5-6000", RAMChannels: -1},
			want: 0,
		},
		{
			name: "unparseable speed",
			host: tape.HostInfo{RAMSpeed: "DDR5", RAMChannels: 2},
			want: 0,
		},
		{
			name: "speed with no transfer rate after the dash",
			host: tape.HostInfo{RAMSpeed: "DDR5-", RAMChannels: 2},
			want: 0,
		},
		{
			name: "a bare number is not a memory part",
			host: tape.HostInfo{RAMSpeed: "6000", RAMChannels: 2},
			want: 0,
		},
		{
			name: "a non-numeric transfer rate",
			host: tape.HostInfo{RAMSpeed: "DDR5-fast", RAMChannels: 2},
			want: 0,
		},
		{
			name: "zero transfer rate",
			host: tape.HostInfo{RAMSpeed: "DDR5-0", RAMChannels: 2},
			want: 0,
		},
		{
			name: "surrounding whitespace",
			host: tape.HostInfo{RAMSpeed: "  DDR5-6000  ", RAMChannels: 2},
			want: 96_000_000_000,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HostBytesPerSec(c.host); got != c.want {
				t.Errorf("HostBytesPerSec(%q, %d) = %d, want %d",
					c.host.RAMSpeed, c.host.RAMChannels, got, c.want)
			}
		})
	}
}

// TestActiveBytesOnMirrorsActiveGo pins the class rule one class at a time.
// It is the mirror of internal/placement/active.go: the two sum the same
// tensors on a real run, so a divergence here is a bug, not a policy choice.
func TestActiveBytesOnMirrorsActiveGo(t *testing.T) {
	moe := tape.ModelInfo{NExperts: 128, NExpertsUsed: 8}
	dense := tape.ModelInfo{}

	cases := []struct {
		name    string
		classes map[tape.TensorClass]int64
		model   tape.ModelInfo
		tied    bool
		want    int64
	}{
		{
			name:    "attention, ffn and other count in full",
			classes: map[tape.TensorClass]int64{tape.ClassAttention: 100, tape.ClassFFN: 200, tape.ClassOther: 3},
			model:   dense,
			want:    303,
		},
		{
			// active.go counts the output projection in full; the card's
			// first spec for this package did not, which was the defect the
			// lead accepted on 2026-09-13.
			name:    "the output head counts in full",
			classes: map[tape.TensorClass]int64{tape.ClassAttention: 100, tape.ClassOutput: 50},
			model:   dense,
			want:    150,
		},
		{
			name:    "the embedding matrix is a row lookup on an untied model",
			classes: map[tape.TensorClass]int64{tape.ClassAttention: 100, tape.ClassEmbed: 900},
			model:   dense,
			tied:    false,
			want:    100,
		},
		{
			// A tied model has no output projection: the embedding matrix IS
			// the output projection, so every token reads all of it.
			name:    "a tied model reads its embedding matrix in full",
			classes: map[tape.TensorClass]int64{tape.ClassAttention: 100, tape.ClassEmbed: 900},
			model:   dense,
			tied:    true,
			want:    1000,
		},
		{
			// Handover lesson 3: on the demo machine these were 84.6 GB that
			// was never read at all.
			name:    "n-gram tables are never read during decode",
			classes: map[tape.TensorClass]int64{tape.ClassAttention: 100, tape.ClassNGram: 84_600_000_000},
			model:   dense,
			want:    100,
		},
		{
			name:    "routed experts count as the used fraction",
			classes: map[tape.TensorClass]int64{tape.ClassExperts: 12_800},
			model:   moe,
			want:    800, // 12800 x 8 / 128
		},
		{
			// A dense model whose FFN was bucketed as experts: with no expert
			// count there is no fraction to take, so it is all read.
			name:    "experts count in full when the model reports no expert count",
			classes: map[tape.TensorClass]int64{tape.ClassExperts: 12_800},
			model:   dense,
			want:    12_800,
		},
		{
			// This is the case the spec wanted to call unknown. active.go
			// counts it in full and the lead ruled that active.go wins
			// (2026-09-13), because the two figures must agree by
			// construction on a real run.
			name:    "experts count in full when the used count is unknown",
			classes: map[tape.TensorClass]int64{tape.ClassExperts: 12_800},
			model:   tape.ModelInfo{NExperts: 128},
			want:    12_800,
		},
		{
			name:    "experts count in full when every expert is routed",
			classes: map[tape.TensorClass]int64{tape.ClassExperts: 12_800},
			model:   tape.ModelInfo{NExperts: 8, NExpertsUsed: 8},
			want:    12_800,
		},
		{
			name:    "a device carrying nothing reads nothing",
			classes: map[tape.TensorClass]int64{},
			model:   dense,
			want:    0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := tape.DevicePlacement{Device: "GPU0", Classes: c.classes}
			if got := activeBytesOn(d, c.model, c.tied); got != c.want {
				t.Errorf("activeBytesOn = %d, want %d", got, c.want)
			}
		})
	}
}

func TestTiedEmbeddings(t *testing.T) {
	cases := []struct {
		name    string
		devices []tape.DevicePlacement
		want    bool
	}{
		{
			name: "an output head anywhere means untied",
			devices: []tape.DevicePlacement{
				{Device: "GPU0", Classes: map[tape.TensorClass]int64{tape.ClassAttention: 100}},
				{Device: "GPU1", Classes: map[tape.TensorClass]int64{tape.ClassOutput: 50}},
			},
			want: false,
		},
		{
			name: "no output bytes anywhere reads as tied",
			devices: []tape.DevicePlacement{
				{Device: "GPU0", Classes: map[tape.TensorClass]int64{tape.ClassAttention: 100, tape.ClassEmbed: 900}},
			},
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tiedEmbeddings(tape.PlacementSummary{Devices: c.devices}); got != c.want {
				t.Errorf("tiedEmbeddings = %v, want %v", got, c.want)
			}
		})
	}
}

// rtx3090 is the peak bandwidth of the card both fixture rigs carry. The
// host bus is DDR5-6000 on two channels, 96.0 GB/s, which HostBytesPerSec
// derives from the fixtures' RAMSpeed and RAMChannels rather than a constant.
const rtx3090 = 936_200_000_000

// denseTwoGPU is card.Example()'s shape, reduced to what this package reads:
// a dense model layer-split across two identical 3090s with nothing on the
// CPU. The per-device class bytes are the fixture's own.
//
// internal/card cannot be imported here — it imports this package — so the
// fixtures are rebuilt rather than borrowed. Each case below copies this and
// edits the one field it is about, so a failure names one cause.
func denseTwoGPU() *tape.RunSummary {
	return &tape.RunSummary{
		Model: tape.ModelInfo{ActiveBytesPerToken: 45_044_097_024},
		Host: tape.HostInfo{
			RAMSpeed:    "DDR5-6000",
			RAMChannels: 2,
			GPUs: []tape.GPUInfo{
				{Index: 0, PeakBandwidthBytesPerSec: rtx3090},
				{Index: 1, PeakBandwidthBytesPerSec: rtx3090},
			},
		},
		Placement: tape.PlacementSummary{Devices: []tape.DevicePlacement{
			{Device: "GPU0", Classes: map[tape.TensorClass]int64{
				tape.ClassAttention: 4_294_967_296,
				tape.ClassFFN:       18_307_921_510,
				tape.ClassEmbed:     589_934_592,
			}},
			{Device: "GPU1", Classes: map[tape.TensorClass]int64{
				tape.ClassAttention: 4_294_967_296,
				tape.ClassFFN:       17_287_092_506,
				tape.ClassOutput:    859_148_416,
			}},
		}},
		Timings: tape.TimingsSummary{EffectiveBandwidthBytesPerSec: 784_740_000_000},
	}
}

// offloadedMoE is the shape the ticket exists for and the one no card fixture
// currently provides honestly: attention on two GPUs, routed experts in host
// RAM, and a split that agrees with the model's own ActiveBytesPerToken.
//
//	GPU0 attention          2.0 GB
//	GPU1 attention + head   2.5 GB
//	CPU  8 of 128 experts   2.0 GB  (32.0 GB of expert weights x 8/128)
//	                        6.5 GB per token = ActiveBytesPerToken
func offloadedMoE() *tape.RunSummary {
	return &tape.RunSummary{
		Model: tape.ModelInfo{
			NExperts:            128,
			NExpertsUsed:        8,
			ActiveBytesPerToken: 6_500_000_000,
		},
		Host: tape.HostInfo{
			RAMSpeed:    "DDR5-6000",
			RAMChannels: 2,
			GPUs: []tape.GPUInfo{
				{Index: 0, PeakBandwidthBytesPerSec: rtx3090},
				{Index: 1, PeakBandwidthBytesPerSec: rtx3090},
			},
		},
		Placement: tape.PlacementSummary{Devices: []tape.DevicePlacement{
			{Device: "GPU0", Classes: map[tape.TensorClass]int64{
				tape.ClassAttention: 2_000_000_000,
			}},
			{Device: "GPU1", Classes: map[tape.TensorClass]int64{
				tape.ClassAttention: 2_000_000_000,
				tape.ClassOutput:    500_000_000,
			}},
			{Device: tape.DeviceCPU, Classes: map[tape.TensorClass]int64{
				tape.ClassExperts: 32_000_000_000,
			}},
		}},
		Timings: tape.TimingsSummary{EffectiveBandwidthBytesPerSec: 126_000_000_000},
	}
}

func TestCeiling(t *testing.T) {
	cases := []struct {
		name string
		run  func() *tape.RunSummary
		want int64
		ok   bool
	}{
		{
			// The heart of the ticket. Two identical cards in a layer split
			// read one after the other, never together, so the ceiling is one
			// card's bandwidth and not the pair's 1.87 TB/s.
			name: "a layer split across two identical GPUs has one card's ceiling",
			run:  denseTwoGPU,
			want: rtx3090,
			ok:   true,
		},
		{
			// The reduction the formula must satisfy: with every byte on one
			// device, ceiling == that device's bandwidth, which is what the
			// card printed for a single-GPU run before this ticket.
			name: "a single-GPU dense run reduces to that GPU's bandwidth",
			run: func() *tape.RunSummary {
				s := denseTwoGPU()
				s.Placement.Devices = []tape.DevicePlacement{{
					Device:  "GPU0",
					Classes: map[tape.TensorClass]int64{tape.ClassFFN: 45_044_097_024},
				}}
				s.Host.GPUs = []tape.GPUInfo{{Index: 0, PeakBandwidthBytesPerSec: rtx3090}}
				return s
			},
			want: rtx3090,
			ok:   true,
		},
		{
			// The partial-offload case: 4.5 GB off the GPUs at 936.2 GB/s and
			// 2.0 GB off DDR5-6000 at 96.0 GB/s is 25.64 ms per token, so the
			// placement allows 253.5 GB/s — a fraction of either GPU's peak,
			// which is the point the old denominator missed entirely.
			name: "experts in host RAM drag the ceiling down to the slow bus",
			run:  offloadedMoE,
			want: 253_510_154_487,
			ok:   true,
		},
		{
			// Heterogeneous cards: the weighted harmonic mean, not either
			// card's figure and not their sum.
			name: "two different GPUs give the harmonic mean weighted by bytes",
			run: func() *tape.RunSummary {
				s := denseTwoGPU()
				s.Model.ActiveBytesPerToken = 6_000_000_000
				s.Host.GPUs = []tape.GPUInfo{
					{Index: 0, PeakBandwidthBytesPerSec: rtx3090},
					{Index: 1, PeakBandwidthBytesPerSec: 672_000_000_000},
				}
				s.Placement.Devices = []tape.DevicePlacement{
					{Device: "GPU0", Classes: map[tape.TensorClass]int64{tape.ClassFFN: 3_000_000_000}},
					{Device: "GPU1", Classes: map[tape.TensorClass]int64{tape.ClassFFN: 3_000_000_000}},
				}
				return s
			},
			want: 782_398_209_178,
			ok:   true,
		},
		{
			// procmon never fills RAMSpeed on a real Linux run (DMI is
			// root-only), so this is what today's real mixed-placement
			// recordings do: print the bandwidth and no ratio.
			name: "a mixed placement with no observed RAM speed is unknown",
			run: func() *tape.RunSummary {
				s := offloadedMoE()
				s.Host.RAMSpeed = ""
				return s
			},
			ok: false,
		},
		{
			name: "a mixed placement with an unparseable RAM speed is unknown",
			run: func() *tape.RunSummary {
				s := offloadedMoE()
				s.Host.RAMSpeed = "DDR5"
				return s
			},
			ok: false,
		},
		{
			name: "a mixed placement with no channel count is unknown",
			run: func() *tape.RunSummary {
				s := offloadedMoE()
				s.Host.RAMChannels = 0
				return s
			},
			ok: false,
		},
		{
			// gpu.Bandwidth reports 0 for a card its table does not name.
			name: "a GPU whose peak bandwidth is unknown is unknown",
			run: func() *tape.RunSummary {
				s := denseTwoGPU()
				s.Host.GPUs[1].PeakBandwidthBytesPerSec = 0
				return s
			},
			ok: false,
		},
		{
			// The placement names a device this package cannot price.
			name: "a device name that is neither CPU nor GPU<n> is unknown",
			run: func() *tape.RunSummary {
				s := denseTwoGPU()
				s.Placement.Devices[1].Device = "NPU0"
				return s
			},
			ok: false,
		},
		{
			name: "a GPU index the host never reported is unknown",
			run: func() *tape.RunSummary {
				s := denseTwoGPU()
				s.Placement.Devices[1].Device = "GPU7"
				return s
			},
			ok: false,
		},
		{
			// The check that stopped this ticket's first round: the card
			// fixtures' placements and their ActiveBytesPerToken disagree
			// about what a token reads, so no ratio is honest. 10 % of
			// 45.04 GB is 4.50 GB; this moves the record by 9.0 GB.
			name: "a split disagreeing with the model figure by more than 10% is unknown",
			run: func() *tape.RunSummary {
				s := denseTwoGPU()
				s.Model.ActiveBytesPerToken = 54_044_097_024
				return s
			},
			ok: false,
		},
		{
			// Just inside the tolerance: the split still governs.
			name: "a split inside the 10% tolerance still yields a ceiling",
			run: func() *tape.RunSummary {
				s := denseTwoGPU()
				s.Model.ActiveBytesPerToken = 47_000_000_000 // +4.3%
				return s
			},
			want: rtx3090,
			ok:   true,
		},
		{
			name: "a model that never reported its active bytes is unknown",
			run: func() *tape.RunSummary {
				s := denseTwoGPU()
				s.Model.ActiveBytesPerToken = 0
				return s
			},
			ok: false,
		},
		{
			name: "a placement carrying no active bytes is unknown",
			run: func() *tape.RunSummary {
				s := denseTwoGPU()
				s.Placement.Devices = nil
				return s
			},
			ok: false,
		},
		{
			// Only lookup tables placed: nothing is read per token.
			name: "a placement of nothing but n-gram tables is unknown",
			run: func() *tape.RunSummary {
				s := denseTwoGPU()
				s.Placement.Devices = []tape.DevicePlacement{{
					Device:  "GPU0",
					Classes: map[tape.TensorClass]int64{tape.ClassNGram: 84_600_000_000},
				}}
				return s
			},
			ok: false,
		},
		{
			// card.ExampleSharded()'s own class bytes. Its placement puts
			// 36.5 GB of attention plus a 4.8 GB head on the GPUs while its
			// ActiveBytesPerToken says a whole token is 21.7 GB: +145.7%. The
			// card drops the clause rather than pick one of the two figures.
			// This is pinned here as well as in the card goldens so the cause
			// has a name when someone rebuilds that fixture.
			name: "the sharded card fixture disagrees with itself and is unknown",
			run: func() *tape.RunSummary {
				s := offloadedMoE()
				s.Model = tape.ModelInfo{
					NExperts: 256, NExpertsUsed: 8,
					ActiveBytesPerToken: 21_700_000_000,
				}
				s.Host.RAMSpeed, s.Host.RAMChannels = "DDR5-4800", 12
				s.Placement.Devices = []tape.DevicePlacement{
					{Device: "GPU0", Classes: map[tape.TensorClass]int64{
						tape.ClassAttention: 18_253_611_008,
						tape.ClassEmbed:     4_831_838_208,
					}},
					{Device: "GPU1", Classes: map[tape.TensorClass]int64{
						tape.ClassAttention: 18_253_611_008,
						tape.ClassOutput:    4_831_838_208,
					}},
					{Device: tape.DeviceCPU, Classes: map[tape.TensorClass]int64{
						tape.ClassExperts: 383_325_831_168,
					}},
				}
				return s
			},
			ok: false,
		},
		{
			// card.ExampleSpeculative()'s own class bytes: +15.9%, the same
			// disagreement in the other direction and only just outside the
			// tolerance. card.ExampleRounds() and card.ExampleSweep() derive
			// from it and lose the clause for the same reason.
			name: "the speculative card fixture disagrees with itself and is unknown",
			run: func() *tape.RunSummary {
				s := offloadedMoE()
				s.Model = tape.ModelInfo{
					NExperts: 128, NExpertsUsed: 8,
					ActiveBytesPerToken: 6_720_000_000,
				}
				s.Placement.Devices = []tape.DevicePlacement{
					{Device: "GPU0", Classes: map[tape.TensorClass]int64{
						tape.ClassAttention: 1_610_612_736,
						tape.ClassEmbed:     2_040_109_465,
					}},
					{Device: "GPU1", Classes: map[tape.TensorClass]int64{
						tape.ClassAttention: 1_610_612_736,
						tape.ClassOutput:    1_932_735_283,
					}},
					{Device: tape.DeviceCPU, Classes: map[tape.TensorClass]int64{
						tape.ClassExperts: 42_198_053_684,
					}},
				}
				return s
			},
			ok: false,
		},
		{
			name: "a nil summary is unknown",
			run:  func() *tape.RunSummary { return nil },
			ok:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Ceiling(c.run())
			if ok != c.ok {
				t.Fatalf("Ceiling ok = %v, want %v (got %d)", ok, c.ok, got)
			}
			if ok && got != c.want {
				t.Errorf("Ceiling = %d, want %d", got, c.want)
			}
			if !ok && got != 0 {
				t.Errorf("Ceiling = %d, want 0 when not derivable", got)
			}
		})
	}
}

func TestOfPeak(t *testing.T) {
	cases := []struct {
		name string
		run  func() *tape.RunSummary
		want float64
		ok   bool
	}{
		{
			// The flagship figure of the ticket: 784.7 GB/s against one
			// 3090's 936.2 GB/s. The card printed 42 % before, against the
			// pair's sum.
			name: "the dense two-GPU example reaches 83.8% of its ceiling",
			run:  denseTwoGPU,
			want: 0.838,
			ok:   true,
		},
		{
			// The same rig under eight concurrent streams: one stream's share
			// of the same ceiling. This is the README's Decode line.
			name: "one stream of the concurrent example reaches 43.8%",
			run: func() *tape.RunSummary {
				s := denseTwoGPU()
				s.Timings.EffectiveBandwidthBytesPerSec = 410_410_000_000
				return s
			},
			want: 0.438,
			ok:   true,
		},
		{
			name: "a partially offloaded MoE is measured against its slow bus",
			run:  offloadedMoE,
			want: 0.497,
			ok:   true,
		},
		{
			// The reduction: one device, so the ratio is exactly
			// effective / that device's bandwidth.
			name: "a single-GPU run is exactly effective over that GPU's bandwidth",
			run: func() *tape.RunSummary {
				s := denseTwoGPU()
				s.Placement.Devices = []tape.DevicePlacement{{
					Device:  "GPU0",
					Classes: map[tape.TensorClass]int64{tape.ClassFFN: 45_044_097_024},
				}}
				s.Host.GPUs = []tape.GPUInfo{{Index: 0, PeakBandwidthBytesPerSec: rtx3090}}
				s.Timings.EffectiveBandwidthBytesPerSec = 500_000_000_000
				return s
			},
			want: 500_000_000_000.0 / rtx3090,
			ok:   true,
		},
		{
			name: "no measured effective bandwidth means no ratio",
			run: func() *tape.RunSummary {
				s := denseTwoGPU()
				s.Timings.EffectiveBandwidthBytesPerSec = 0
				return s
			},
			ok: false,
		},
		{
			name: "an underivable ceiling means no ratio",
			run: func() *tape.RunSummary {
				s := offloadedMoE()
				s.Host.RAMSpeed = ""
				return s
			},
			ok: false,
		},
		{
			name: "a nil summary means no ratio",
			run:  func() *tape.RunSummary { return nil },
			ok:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := OfPeak(c.run())
			if ok != c.ok {
				t.Fatalf("OfPeak ok = %v, want %v (got %.4f)", ok, c.ok, got)
			}
			if !ok {
				if got != 0 {
					t.Errorf("OfPeak = %.4f, want 0 when not derivable", got)
				}
				return
			}
			// One decimal of a per cent: the card rounds to whole per cent,
			// so this pins the figure well below what it prints.
			if math.Abs(got-c.want) > 0.0005 {
				t.Errorf("OfPeak = %.4f (%.1f%%), want %.4f (%.1f%%)",
					got, got*100, c.want, c.want*100)
			}
		})
	}
}
