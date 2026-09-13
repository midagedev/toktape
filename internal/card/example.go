package card

import (
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// The example run is a dense 70B at Q4_K_M on two RTX 3090s, fully offloaded.
//
// 2026-09-13 (TTP-28, user: "테스트 데이터긴 하지만 개별 tps와 우측에 나오는 요약
// 전체 tps의 수치가 비현실적이다"). The fixture used to be a 35B MoE decoding at
// 68 tok/s single-stream and 12.1 × 8 concurrent, which is not a shape anyone
// recognises. The launch post is about 70B, and a 70B Q4_K_M on 2×3090 is a rig
// the audience owns and whose numbers they already know, so every figure below
// can be checked against their own logs.
//
// 2026-09-13, later the same day: the model is DeepSeek-R1-Distill-Llama-70B
// rather than Llama-3.3-70B-Instruct. The two are the same dense Llama
// architecture at the same parameter count, so every figure in the derivation
// below is unchanged, but the fixture's streams think before they answer and
// Llama 3.3 emits no reasoning_content. A screen showing three thinking states
// on a model that has none teaches the reader something false about the tool,
// which is the one thing a fixture must not do. The distill is also a model
// this audience actually runs at this quant.
//
// The derivation, so a reviewer can check the arithmetic rather than trust it:
//
//	file         DeepSeek-R1-Distill-Llama-70B-Q4_K_M.gguf, 42.52 GiB, 70.55 B
//	             params, 80 layers, dense: a distill of the Llama 3.3 70B
//	             architecture, so the placement and the rates below are the
//	             ones that model reaches. (Hugging Face lists the quant as
//	             "42.52 GB"; the card's unit is GiB and the fixture keeps the
//	             figure, so weights + KV + buffers still add up to what the
//	             two devices report.)
//	placement    layer split, no -ot and no -ncmoe: GPU0 keeps layers 0-39
//	             (21.6 GiB) and the token embeddings, GPU1 layers 40-79
//	             (20.9 GiB) and the output head. Nothing on the CPU, so the
//	             card prints no CPU device and no "never loaded" line.
//	kv           q8_0 at 16384 context is about 1.06 bytes per element:
//	             80 layers × 2 × 1024 KV channels × 16384 × 1.0625 ≈ 2.6 GiB,
//	             which the two devices split. Compute buffers are 0.9 GiB on
//	             GPU0 and 0.6 on GPU1 at -b 2048 -ub 512.
//	vram         GPU0 21.6 + 1.3 + 0.9 = 23.8 of 24.0 GiB; GPU1 20.9 + 1.3 +
//	             0.6 = 22.8. That is what a 70B on two 24 GB cards feels like:
//	             it fits, and only just.
//	decode       17.4 tok/s single stream — the figure a layer-split 70B
//	             Q4_K_M on 2×3090 reaches. Per stream with N at once it is
//	             17.4 / (1 + 0.13·(N−1)): batched decode reads the same
//	             weights for every stream, so the server-wide rate climbs
//	             while each session slows. N=8 gives 9.1 each, 72.9 together.
//	prefill      610 tok/s. 512-token prompt with 128 cached, so 384 are
//	             evaluated and the first token lands at 384/610 ≈ 630 ms.
//	bandwidth    45.1 GB of weights are read per token (the file less the
//	             embedding table, which is a lookup); × 17.4 tok/s ≈ 785 GB/s
//	             against the pair's 1.87 TB/s of peak, which is the 40-some
//	             per cent a layer split leaves on the table.
//	host         GPU-bound: load 3.1 on 32 threads, no other compute process,
//	             so the run is not contended, and RSS is 1.2 GiB because the
//	             weights live in VRAM and the page cache holds the rest —
//	             which is exactly the "RSS is not loaded" picture (lesson 3).

// Example returns a realistic, fully populated single-stream summary. It exists
// so the TUI, the PNG renderer, the README and the golden tests all draw the
// same card without inventing a fixture each time.
//
// The figures are internally consistent on purpose: predicted_ms matches
// predicted_n / predicted_per_second, the client rate agrees with the server
// rate inside tape.RateTolerance, the effective bandwidth is
// ActiveBytesPerToken × the decode rate, and the VRAM split adds up to what the
// devices report at the end of the run.
func Example() *tape.RunSummary {
	started := time.Date(2026, 9, 13, 14, 25, 30, 0, time.UTC)
	return &tape.RunSummary{
		ID:             "20260913-142530-r1-distill-llama-70b",
		ToktapeVersion: "0.1.0",
		StartedAt:      started,
		FinishedAt:     started.Add(18963 * time.Millisecond),

		Server: tape.ServerInfo{
			Kind:   tape.ServerLlamaCPP,
			URL:    "http://127.0.0.1:8080",
			Build:  "b3650",
			Commit: "a1b2c3d",
			PID:    48213,
			Host:   "workstation",
			// Args is the process's own argv, the way the recorder reads it
			// out of /proc/<pid>/cmdline. It is what the Reproduce block in
			// the Markdown card quotes, so the fixture carries a command line
			// a reader could actually paste. Flags below is the parsed view:
			// every named flag here agrees with it.
			// A live recording would also put the tokens this parser does not
			// name (-m, -c, --parallel) into Flags.Other; the fixture leaves
			// Other empty so the golden FLAGS row stays the named set.
			Args: []string{
				"/usr/local/bin/llama-server",
				"-m", "/models/DeepSeek-R1-Distill-Llama-70B-Q4_K_M.gguf",
				"-c", "16384",
				"--parallel", "8",
				"-ngl", "99",
				"-fa", "on",
				"-b", "2048",
				"-ub", "512",
				"-ctk", "q8_0",
				"-ctv", "q8_0",
				"-t", "16",
			},
			Flags: tape.ServerFlags{
				NGL:        "99",
				FlashAttn:  "on",
				Batch:      "2048",
				UBatch:     "512",
				CacheTypeK: "q8_0",
				CacheTypeV: "q8_0",
				Threads:    "16",
			},
			NSlots:  8,
			CtxSize: 16384,
		},
		Model: tape.ModelInfo{
			Path:      "/models/DeepSeek-R1-Distill-Llama-70B-Q4_K_M.gguf",
			FileName:  "DeepSeek-R1-Distill-Llama-70B-Q4_K_M.gguf",
			Name:      "R1 Distill Llama 70B",
			Arch:      "llama",
			Quant:     "Q4_K_M",
			FileBytes: 45655502848, // 42.52 GiB
			Params:    70553706496, // 70.55 B
			NLayers:   80,
			CtxTrain:  131072,
			// The weights actually read per decoded token: the whole file
			// less the embedding table, which is a row lookup rather than a
			// matrix multiply. Dense, so there is no expert share to subtract.
			ActiveBytesPerToken: 45100000000,
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
					Bytes:  23192823398, // 21.6 GiB
					Classes: map[tape.TensorClass]int64{
						tape.ClassAttention: 4294967296,
						tape.ClassFFN:       18307921510,
						tape.ClassEmbed:     589934592,
					},
					Layers: "0-39",
				},
				{
					Device: "GPU1",
					Bytes:  22441208218, // 20.9 GiB
					Classes: map[tape.TensorClass]int64{
						tape.ClassAttention: 4294967296,
						tape.ClassFFN:       17287092506,
						tape.ClassOutput:    859148416,
					},
					Layers: "40-79",
				},
			},
			Source:           "gguf+args",
			VRAMWeightsBytes: 45634031616, // 42.5 GiB, the two devices together
			VRAMKVBytes:      2791728742,  // 2.6 GiB, q8_0 at 16k context
			VRAMComputeBytes: 1610612736,  // 1.5 GiB of compute buffers
		},
		Memory: tape.MemorySummary{
			// A fully offloaded model leaves almost nothing resident: the
			// weights were read once into VRAM and the page cache holds what
			// is left of the mapping. RSS is not "loaded" (lesson 3).
			AtEnd: tape.MemSample{
				VirtBytes:    51539607552, // 48 GiB of mapping
				RSSBytes:     1288490189,  // 1.2 GiB
				RSSFileBytes: 858993459,   // 0.8 GiB
				RSSAnonBytes: 429496730,   // 0.4 GiB
				MinFaults:    128402,
			},
			PeakRSSBytes:    1288490189,
			MappedFileBytes: 45655502848,
		},
		Concurrency: 1,
		Timings: tape.TimingsSummary{
			PromptN:                       384,
			CacheN:                        128,
			PromptMs:                      629.5,
			PromptPerSecond:               610.0,
			PredictedN:                    320,
			PredictedMs:                   18333.3,
			PredictedPerSecond:            17.4,
			TTFTMs:                        630,
			ClientPromptPerSecond:         604.0,
			ClientPredictedPerSecond:      17.3,
			ClientAgreesWithServer:        true,
			DecodeLabel:                   "decode",
			ITLp50Ms:                      57.5,
			ITLp95Ms:                      62.0,
			ITLp99Ms:                      71.3,
			EffectiveBandwidthBytesPerSec: 784740000000, // 45.1 GB/token × 17.4 tok/s
		},
		Aggregate: tape.AggregateTimings{
			Streams:                     1,
			WallMs:                      18963,
			TotalPromptN:                384,
			TotalPredictedN:             320,
			AggregatePredictedPerSecond: 17.4,
			AggregatePromptPerSecond:    610.0,
			PerStreamPredictedPerSecond: 17.4,
			TTFTp50Ms:                   630,
			TTFTp95Ms:                   630,
			SlotsBusyMax:                1,
		},
		Cache: tape.CacheSummary{
			HitTokens:   128,
			PromptTotal: 512,
			HitRatio:    0.25,
			Label:       tape.CacheWarm,
		},
		Contention: tape.ContentionInfo{LoadAvg1: 3.1},
		// The request's shape (TTP-55): this run sent no temperature, so the
		// server's own sampler was in effect and the card says "temp default"
		// rather than a number nobody chose.
		Sampling: tape.SamplingSummary{Endpoint: tape.EndpointChat},
		Template: tape.TemplateInfo{
			ChatTemplate:         "llama3",
			RenderedPromptSHA256: "9f2c4b1a8d5e6037c1a9b2d4e8f70516a3c9d2b4e6f8017a3c5d9e2b4f6a8017",
			RenderedPromptTokens: 512,
		},
		GPUsAtEnd: []tape.GPUSample{
			{Index: 0, UsedBytes: 25555748454, ProcBytes: 25341952819, UtilPct: 96, TempC: 68, PowerW: 340, ClockMHz: 1830},
			{Index: 1, UsedBytes: 24481361100, ProcBytes: 24267565465, UtilPct: 91, TempC: 64, PowerW: 310, ClockMHz: 1815},
		},
	}
}

// ExampleConcurrent returns the same rig running eight streams at once.
//
// It is the run the card's Streams line exists for: the aggregate climbs to
// 72.8 tok/s while each session drops from 17.4 to 9.1, and only one of those
// two numbers describes what an agent sitting on one of the slots feels. The
// prefill is queued, so the first token of the last stream lands 420 ms after
// the first token of the first. The figures are the ones internal/tui's
// ExampleTapeN derives from its own token timeline at eight streams, so a clip
// and the card it ends on report the same run.
func ExampleConcurrent() *tape.RunSummary {
	s := Example()
	started := time.Date(2026, 9, 13, 15, 2, 10, 0, time.UTC)
	s.ID = "20260913-150210-r1-distill-llama-70b"
	s.StartedAt = started
	s.FinishedAt = started.Add(36007 * time.Millisecond)
	s.Concurrency = 8

	// The representative stream: the per-stream mean. 306 predicted tokens is
	// what a 320-token budget produces when most streams stop on an end token
	// a little short of it and one runs the budget out.
	s.Timings = tape.TimingsSummary{
		PromptN:                       384,
		CacheN:                        128,
		PromptMs:                      629.5,
		PromptPerSecond:               610.0,
		PredictedN:                    307,
		PredictedMs:                   33594.0,
		PredictedPerSecond:            9.1,
		TTFTMs:                        810,
		ClientPromptPerSecond:         604.0,
		ClientPredictedPerSecond:      9.1,
		ClientAgreesWithServer:        true,
		DecodeLabel:                   "decode",
		ITLp50Ms:                      109.8,
		ITLp95Ms:                      118.6,
		ITLp99Ms:                      130.6,
		EffectiveBandwidthBytesPerSec: 410410000000, // per stream: 45.1 GB/token × 9.1 tok/s
	}
	s.Aggregate = tape.AggregateTimings{
		Streams:                     8,
		WallMs:                      36007,
		TotalPromptN:                3072,
		TotalPredictedN:             2460,
		AggregatePredictedPerSecond: 72.9,
		// The server-wide prompt rate: every stream's 384 evaluated tokens
		// over the window that ends when the last of them gets its first
		// token (3072 / 1.05 s). It is above one stream's own 610 because the
		// slots' prompts share a batch, and below eight times it because they
		// still queue for their turn.
		AggregatePromptPerSecond:    2927.0,
		PerStreamPredictedPerSecond: 9.1,
		TTFTp50Ms:                   810,
		TTFTp95Ms:                   1050,
		SlotsBusyMax:                8,
	}
	s.GPUsAtEnd = []tape.GPUSample{
		{Index: 0, UsedBytes: 25555748454, ProcBytes: 25341952819, UtilPct: 98, TempC: 71, PowerW: 348, ClockMHz: 1770},
		{Index: 1, UsedBytes: 24481361100, ProcBytes: 24267565465, UtilPct: 94, TempC: 67, PowerW: 318, ClockMHz: 1755},
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
