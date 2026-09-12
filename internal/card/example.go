package card

import (
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// Example returns a realistic, fully populated single-stream summary: a
// Qwen3.5-35B-A3B UD-Q4_K_M on two RTX 3090s with twelve MoE layers kept on
// the CPU. It exists so the TUI, the PNG renderer, the README and the golden
// tests all draw the same card without inventing a fixture each time.
//
// The figures are internally consistent on purpose: predicted_ms matches
// predicted_n / predicted_per_second, the client rate agrees with the server
// rate inside tape.RateTolerance, the effective bandwidth is
// ActiveBytesPerToken × the decode rate, and the VRAM split adds up to what
// the devices report at the end of the run.
func Example() *tape.RunSummary {
	started := time.Date(2026, 9, 13, 14, 25, 30, 0, time.UTC)
	return &tape.RunSummary{
		ID:             "20260913-142530-qwen3.5-35b-a3b",
		ToktapeVersion: "0.1.0",
		StartedAt:      started,
		FinishedAt:     started.Add(2050 * time.Millisecond),

		Server: tape.ServerInfo{
			Kind:   tape.ServerLlamaCPP,
			URL:    "http://127.0.0.1:8080",
			Build:  "b3650",
			Commit: "a1b2c3d",
			PID:    48213,
			Host:   "workstation",
			Flags: tape.ServerFlags{
				NGL:          "99",
				FlashAttn:    "on",
				Batch:        "2048",
				UBatch:       "512",
				CacheTypeK:   "q8_0",
				CacheTypeV:   "q8_0",
				LoadMode:     "mmap",
				CPUMoE:       "12",
				Threads:      "16",
				OverrideTens: []string{`blk\.(3[6-9]|4[0-7])\.ffn_.*_exps=CPU`},
			},
			NSlots:  8,
			CtxSize: 32768,
		},
		Model: tape.ModelInfo{
			Path:                "/models/Qwen3.5-35B-A3B-UD-Q4_K_M.gguf",
			FileName:            "Qwen3.5-35B-A3B-UD-Q4_K_M.gguf",
			Name:                "Qwen3.5 35B A3B",
			Arch:                "qwen3moe",
			Quant:               "UD-Q4_K_M",
			FileBytes:           21292085248, // 19.83 GiB
			Params:              35000000000,
			NLayers:             48,
			NExperts:            128,
			NExpertsUsed:        8,
			CtxTrain:            262144,
			ActiveBytesPerToken: 1330000000,
		},
		Host: tape.HostInfo{
			Hostname:    "workstation",
			OS:          "linux",
			Kernel:      "6.8.0-45-generic",
			CPU:         "AMD Ryzen 9 7950X",
			CPUCores:    16,
			CPUThreads:  32,
			RAMBytes:    68719476736, // 64 GiB
			RAMSpeed:    "DDR5-6000",
			RAMChannels: 2,
			GPUs:        exampleGPUs(),
		},
		Placement: tape.PlacementSummary{
			Devices: []tape.DevicePlacement{
				{
					Device: "GPU0",
					Bytes:  7247757312,
					Classes: map[tape.TensorClass]int64{
						tape.ClassAttention: 1073741824,
						tape.ClassExperts:   5637144576,
						tape.ClassEmbed:     536870912,
					},
					Layers: "0-23",
				},
				{
					Device: "GPU1",
					Bytes:  7247757312,
					Classes: map[tape.TensorClass]int64{
						tape.ClassAttention: 1073741824,
						tape.ClassExperts:   5637144576,
						tape.ClassOutput:    536870912,
					},
					Layers: "24-47",
				},
				{
					Device: "CPU",
					Bytes:  6796570624,
					Classes: map[tape.TensorClass]int64{
						tape.ClassExperts: 6796570624,
					},
					Layers: "ffn_exps 36-47",
				},
			},
			Source:           "gguf+args",
			VRAMWeightsBytes: 14495514624, // 13.5 GiB
			VRAMKVBytes:      3221225472,  // 3.0 GiB
			VRAMComputeBytes: 966367641,   // 0.9 GiB
		},
		Memory: tape.MemorySummary{
			AtEnd: tape.MemSample{
				VirtBytes:    42949672960,
				RSSBytes:     3650722201, // 3.4 GiB
				RSSFileBytes: 3113851289, // 2.9 GiB
				RSSAnonBytes: 536870912,  // 0.5 GiB
				MinFaults:    128402,
			},
			PeakRSSBytes:    3650722201,
			MappedFileBytes: 21292085248,
		},
		Concurrency: 1,
		Timings: tape.TimingsSummary{
			PromptN:                       112,
			CacheN:                        400,
			PromptMs:                      45.7,
			PromptPerSecond:               2450.0,
			PredictedN:                    128,
			PredictedMs:                   1871.3,
			PredictedPerSecond:            68.4,
			TTFTMs:                        143,
			ClientPromptPerSecond:         2411.0,
			ClientPredictedPerSecond:      67.9,
			ClientAgreesWithServer:        true,
			DecodeLabel:                   "decode",
			ITLp50Ms:                      14.2,
			ITLp95Ms:                      19.8,
			ITLp99Ms:                      31.4,
			EffectiveBandwidthBytesPerSec: 90972000000, // 1.33 GB/token × 68.4 tok/s
		},
		Aggregate: tape.AggregateTimings{
			Streams:                     1,
			WallMs:                      2050,
			TotalPromptN:                112,
			TotalPredictedN:             128,
			AggregatePredictedPerSecond: 68.4,
			AggregatePromptPerSecond:    2450.0,
			PerStreamPredictedPerSecond: 68.4,
			TTFTp50Ms:                   143,
			TTFTp95Ms:                   143,
			SlotsBusyMax:                1,
		},
		Cache: tape.CacheSummary{
			HitTokens:   400,
			PromptTotal: 512,
			HitRatio:    0.78125,
			Label:       tape.CacheWarm,
		},
		Contention: tape.ContentionInfo{LoadAvg1: 1.2},
		Template: tape.TemplateInfo{
			ChatTemplate:          "chatml",
			ReasoningEffort:       "none",
			RenderedHasThinkClose: true,
			RenderedPromptSHA256:  "9f2c4b1a8d5e6037c1a9b2d4e8f70516a3c9d2b4e6f8017a3c5d9e2b4f6a8017",
			RenderedPromptTokens:  512,
		},
		GPUsAtEnd: []tape.GPUSample{
			{Index: 0, UsedBytes: 9878753280, ProcBytes: 9663676416, UtilPct: 97, TempC: 64, PowerW: 310, ClockMHz: 1830},
			{Index: 1, UsedBytes: 9449766912, ProcBytes: 9234780160, UtilPct: 95, TempC: 61, PowerW: 295, ClockMHz: 1815},
		},
	}
}

// ExampleConcurrent returns the same rig running eight streams at once, cold:
// the prompt cache misses and weights are still being paged in, so the page
// fault lines and the "cold" label are exercised alongside the Streams line.
func ExampleConcurrent() *tape.RunSummary {
	s := Example()
	started := time.Date(2026, 9, 13, 15, 2, 10, 0, time.UTC)
	s.ID = "20260913-150210-qwen3.5-35b-a3b"
	s.StartedAt = started
	s.FinishedAt = started.Add(12840 * time.Millisecond)
	s.Concurrency = 8

	s.Timings = tape.TimingsSummary{
		PromptN:                       512,
		PromptMs:                      258.6,
		PromptPerSecond:               1980.0,
		PredictedN:                    128,
		PredictedMs:                   10578.5,
		PredictedPerSecond:            12.1,
		TTFTMs:                        210,
		ClientPromptPerSecond:         1962.0,
		ClientPredictedPerSecond:      12.0,
		ClientAgreesWithServer:        true,
		DecodeLabel:                   "decode",
		ITLp50Ms:                      78.4,
		ITLp95Ms:                      164.0,
		ITLp99Ms:                      402.5,
		EffectiveBandwidthBytesPerSec: 16093000000, // per stream: 1.33 GB/token × 12.1 tok/s
	}
	s.Aggregate = tape.AggregateTimings{
		Streams:                     8,
		WallMs:                      12840,
		TotalPromptN:                4096,
		TotalPredictedN:             1024,
		AggregatePredictedPerSecond: 96.8,
		AggregatePromptPerSecond:    8250.0,
		PerStreamPredictedPerSecond: 12.1,
		TTFTp50Ms:                   210,
		TTFTp95Ms:                   480,
		SlotsBusyMax:                8,
	}
	s.Cache = tape.CacheSummary{PromptTotal: 512, Label: tape.CacheCold}
	s.Memory.MajFaultsTotal = 2100
	s.Memory.MajFaultsPrompt = 680
	s.Memory.MajFaultsDecode = 1420
	s.Memory.MajFaultsPerToken = 1.4
	s.GPUsAtEnd = []tape.GPUSample{
		{Index: 0, UsedBytes: 10307921510, ProcBytes: 10092838092, UtilPct: 99, TempC: 71, PowerW: 340, ClockMHz: 1755},
		{Index: 1, UsedBytes: 9878753280, ProcBytes: 9663676416, UtilPct: 98, TempC: 69, PowerW: 330, ClockMHz: 1770},
	}
	s.Warnings = []string{
		"nvml unavailable, VRAM read from nvidia-smi",
		"cold run: 1.4 major faults per token during decode",
	}
	return s
}

func exampleGPUs() []tape.GPUInfo {
	gpus := make([]tape.GPUInfo, 0, 2)
	for i := 0; i < 2; i++ {
		gpus = append(gpus, tape.GPUInfo{
			Index:                    i,
			Name:                     "NVIDIA GeForce RTX 3090",
			VRAMBytes:                25769803776, // 24 GiB
			Driver:                   "550.54.14",
			PCIe:                     "4.0 x16",
			PeakBandwidthBytesPerSec: 936200000000,
		})
	}
	return gpus
}
