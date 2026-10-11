package card

import (
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// The GB10 example run (TTP-178, 2026-10-11): a MoE model on a DGX Spark, the
// host whose one LPDDR5x pool the CPU and GPU share.
//
// What it is for is the host, not the model. nvidia-smi lists the GB10 and
// answers [N/A] for its memory, so the GPU carries VRAMBytes 0 (unknown), and
// the placement still splits across "GPU0" and "CPU" because llama.cpp assigns
// tensors to backends whatever the silicon behind them. The card must call that
// one pool and not an offload. No field says so: the fixture sets nothing a
// pre-TTP-178 recording would not have set, and the card reads the name.
//
// The figures are plausible, not a capture (the Spark has not been recorded
// yet; the derivation is what a reviewer can check):
//
//	host       121.7 GiB of MemTotal (130,668,019,712 bytes), shared. No RAM
//	           speed, channels or bandwidth are stated: none was observed, so
//	           the card prints none and the ceiling is "?".
//	model      gpt-oss-120b MXFP4, 63.4 GB over 36 layers, 128 experts of
//	           which 4 route each token, about 2.9 GB read per token.
//	placement  52 GB on GPU0 (attention, embeddings, experts of the first 28
//	           layers), 11.4 GB on CPU (the experts of the last 8 layers).
//	decode     52 tok/s, so 2.9 GB × 52 = 150.8 GB/s moved, a measured figure
//	           with no peak to divide it by.

// ExampleGB10 returns the unified-memory run. See above.
func ExampleGB10() *tape.RunSummary {
	started := time.Date(2026, 10, 11, 9, 12, 5, 0, time.UTC)
	return &tape.RunSummary{
		ID:             "20261011-091205-gpt-oss-120b",
		ToktapeVersion: "0.7.1",
		StartedAt:      started,
		FinishedAt:     started.Add(6700 * time.Millisecond),

		Server: tape.ServerInfo{
			Kind:   tape.ServerLlamaCPP,
			URL:    "http://127.0.0.1:8080",
			Build:  "b6890",
			Commit: "c4d5e6f",
			PID:    3120,
			Host:   "spark",
			Args: []string{
				"/opt/llama.cpp/build/bin/llama-server",
				"-m", "/models/gpt-oss-120b-mxfp4.gguf",
				"-c", "32768",
				"-ngl", "99",
				"-fa", "on",
				"-ncmoe", "8",
			},
			Flags: tape.ServerFlags{
				NGL:       "99",
				FlashAttn: "on",
			},
			NSlots:  1,
			CtxSize: 32768,
		},
		Model: tape.ModelInfo{
			Path:                "/models/gpt-oss-120b-mxfp4.gguf",
			FileName:            "gpt-oss-120b-mxfp4.gguf",
			Name:                "gpt-oss 120b",
			Arch:                "gpt-oss",
			Quant:               "MXFP4",
			FileBytes:           63387346944, // 59.0 GiB
			Params:              116829156672,
			NLayers:             36,
			NExperts:            128,
			NExpertsUsed:        4,
			CtxTrain:            131072,
			ActiveBytesPerToken: 2900000000,
		},
		Host: tape.HostInfo{
			Hostname: "spark",
			OS:       "linux",
			Kernel:   "6.11.0-1016-nvidia",
			// No CPU name: aarch64 /proc/cpuinfo has no "model name", and a
			// real Spark's tape (bloomery PR #16, 2026-10-11) carries none.
			CPU:        "",
			CPUCores:   20,
			CPUThreads: 20,
			RAMBytes:   130661761024, // MemTotal 127599376 kB, a real Spark: the one pool
			GPUs: []tape.GPUInfo{
				{Index: 0, Name: "NVIDIA GB10", VRAMBytes: 0, Driver: "595.84"},
			},
		},
		Placement: tape.PlacementSummary{
			Devices: []tape.DevicePlacement{
				{
					Device: "GPU0",
					Bytes:  52000000000,
					Classes: map[tape.TensorClass]int64{
						tape.ClassAttention: 4000000000,
						tape.ClassExperts:   47000000000,
						tape.ClassEmbed:     1000000000,
					},
					Layers: "0-27",
				},
				{
					Device: tape.DeviceCPU,
					Bytes:  11387346944,
					Classes: map[tape.TensorClass]int64{
						tape.ClassExperts: 11387346944,
					},
					Layers: "28-35",
				},
			},
			Source:           "gguf+args",
			VRAMWeightsBytes: 52000000000,
			VRAMKVBytes:      1610612736,
			VRAMComputeBytes: 805306368,
		},
		Memory: tape.MemorySummary{
			AtEnd: tape.MemSample{
				VirtBytes:    75161927680,
				RSSBytes:     60129542144,
				RSSFileBytes: 58720256000,
				RSSAnonBytes: 1409286144,
				MinFaults:    402118,
			},
			PeakRSSBytes:    60129542144,
			MappedFileBytes: 63387346944,
		},
		Concurrency: 1,
		Timings: tape.TimingsSummary{
			PromptN:                       512,
			PromptMs:                      395.1,
			PromptPerSecond:               1296.0,
			PredictedN:                    320,
			PredictedMs:                   6153.8,
			PredictedPerSecond:            52.0,
			TTFTMs:                        410,
			ClientPromptPerSecond:         1280.0,
			ClientPredictedPerSecond:      51.8,
			ClientAgreesWithServer:        true,
			DecodeLabel:                   "decode",
			ITLp50Ms:                      19.2,
			ITLp95Ms:                      21.0,
			ITLp99Ms:                      24.4,
			EffectiveBandwidthBytesPerSec: 150800000000, // 2.9 GB/token × 52 tok/s
		},
		Aggregate: tape.AggregateTimings{
			Streams:                     1,
			WallMs:                      6700,
			TotalPromptN:                512,
			TotalPredictedN:             320,
			AggregatePredictedPerSecond: 52.0,
			AggregatePromptPerSecond:    1296.0,
			PerStreamPredictedPerSecond: 52.0,
			TTFTp50Ms:                   410,
			TTFTp95Ms:                   410,
			SlotsBusyMax:                1,
		},
		Cache: tape.CacheSummary{
			PromptTotal: 512,
			Label:       tape.CacheWarm,
		},
		Contention: tape.ContentionInfo{LoadAvg1: 1.4},
		Sampling:   tape.SamplingSummary{Endpoint: tape.EndpointChat},
		Template: tape.TemplateInfo{
			ChatTemplate:         "harmony",
			RenderedPromptSHA256: "1b3d5f7a9c2e4a6c8e0b2d4f6a8c0e2b4d6f8a0c2e4b6d8f0a2c4e6b8d0f2a4c",
			RenderedPromptTokens: 512,
		},
		// UsedBytes is 0: memory.used is [N/A] on this part, the same unknown.
		GPUsAtEnd: []tape.GPUSample{
			{Index: 0, UtilPct: 94, TempC: 71, PowerW: 38, ClockMHz: 2418},
		},
	}
}
