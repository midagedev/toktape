package card

import (
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// The sharded example is the run TTP-32 exists for (2026-09-13, rig log): a
// 671 B MoE split into nine GGUF parts, whose experts sit in host RAM while
// the attention stack and the embeddings ride on two 3090s, served to four
// concurrent sessions.
//
// The model is a hard-linked variant set. The same rig holds
// /models/DeepSeek-V4.1-Flash-engramQ8-tokembdBF16 and
// /models/DeepSeek-V4.1-Flash-engramQ4-tokembdQ8, and every part inside them
// has the identical file name, so a card that printed
// "DeepSeek-V4.1-Flash-00001-of-00009.gguf" would say the same thing about
// both runs. The directory is the only thing that tells them apart, and the
// size of part one is not the size of the model, so the MODEL line prints the
// variant directory, the part count and the sum of all nine parts.
//
// The derivation, so a reviewer can check the arithmetic rather than trust it:
//
//	file         nine parts of about 44.4 GiB each, 400.00 GiB together, 671.03 B
//	             params at Q4_K_M, 61 layers, 8 of 256 experts routed per token.
//	             FileBytes is the sum; Memory.MappedFileBytes is the same figure
//	             because the server maps the whole set.
//	placement    -ot sends every routed expert to the CPU (357.0 GiB) and leaves
//	             the attention stack on the GPUs: GPU0 takes layers 0-30 plus the
//	             token embeddings (21.5 GiB), GPU1 layers 31-60 plus the output
//	             head (21.5 GiB). 357.0 + 21.5 + 21.5 = 400.0.
//	vram         MLA keeps the KV cache small: 3.0 GiB at 16384 context over four
//	             slots, split evenly, plus 0.9 and 0.6 GiB of compute buffers at
//	             -b 2048 -ub 512. GPU0 reports 23.9 of 24.0 GiB, GPU1 23.6.
//	rss          the experts are resident, so RSS is 359.0 GiB of a 768 GiB box
//	             and no page faults during decode — a warm run whose weights
//	             genuinely are in RAM, which is what makes the rate below the
//	             memory bandwidth's story rather than the disk's.
//	decode       9.6 tok/s single stream: 21.70 GB of weights are read per token
//	             (attention plus the eight routed experts) against the twelve
//	             DDR5-4800 channels. Four at once follow the same batching law
//	             as the concurrent example, 9.6 / (1 + 0.13·(N−1)): 6.9 tok/s
//	             each, 27.6 together.
//	prefill      172 tok/s per stream. 512-token prompts with 128 cached, so 384
//	             are evaluated; four of them share the batch and the last first
//	             token lands 3.1 s in, which is 1536 / 3.1 ≈ 495 tok/s server-wide.
//	bandwidth    21.70 GB/token × 6.9 tok/s ≈ 149.7 GB/s per stream, the figure
//	             the card's Decode row carries for one session.

// ExampleSharded returns a realistic four-stream summary of a nine-part model
// set. It is the fixture for the MODEL line's shard form; every other renderer
// reads the same struct, so it doubles as the PNG and TUI variant fixture.
//
// The figures are internally consistent on purpose, in the same way Example's
// are: predicted_ms matches predicted_n / predicted_per_second, the client
// rates agree with the server's inside tape.RateTolerance, the effective
// bandwidth is ActiveBytesPerToken × the decode rate, the three devices' bytes
// add up to FileBytes and the VRAM split adds up to what the cards report.
func ExampleSharded() *tape.RunSummary {
	started := time.Date(2026, 9, 13, 16, 15, 40, 0, time.UTC)
	return &tape.RunSummary{
		ID:             "20260913-161540-deepseek-v4-1-flash",
		ToktapeVersion: "0.1.0",
		StartedAt:      started,
		FinishedAt:     started.Add(38950 * time.Millisecond),

		Server: tape.ServerInfo{
			Kind:   tape.ServerLlamaCPP,
			URL:    "http://127.0.0.1:8080",
			Build:  "b3650",
			Commit: "a1b2c3d",
			PID:    51877,
			Host:   "bigram",
			Args: []string{
				"/usr/local/bin/llama-server",
				"-m", "/models/DeepSeek-V4.1-Flash-engramQ8-tokembdBF16/DeepSeek-V4.1-Flash-00001-of-00009.gguf",
				"-c", "16384",
				"--parallel", "4",
				"-ngl", "99",
				"-fa", "on",
				"-b", "2048",
				"-ub", "512",
				"-ctk", "q8_0",
				"-ctv", "q8_0",
				"-ot", `blk\.[0-9]+\.ffn_(gate|up|down)_exps\.weight=CPU`,
				"-t", "32",
			},
			Flags: tape.ServerFlags{
				NGL:          "99",
				FlashAttn:    "on",
				Batch:        "2048",
				UBatch:       "512",
				CacheTypeK:   "q8_0",
				CacheTypeV:   "q8_0",
				OverrideTens: []string{`blk\.[0-9]+\.ffn_(gate|up|down)_exps\.weight=CPU`},
				Threads:      "32",
			},
			NSlots:  4,
			CtxSize: 16384,
		},
		Model: tape.ModelInfo{
			Path:     "/models/DeepSeek-V4.1-Flash-engramQ8-tokembdBF16/DeepSeek-V4.1-Flash-00001-of-00009.gguf",
			FileName: "DeepSeek-V4.1-Flash-00001-of-00009.gguf",
			Dir:      "DeepSeek-V4.1-Flash-engramQ8-tokembdBF16",
			Shards:   9,
			Name:     "DeepSeek V4.1 Flash",
			Arch:     "deepseek2",
			Quant:    "Q4_K_M",
			// The whole set, not part one: nine parts summed by the recorder.
			FileBytes:    429496729600, // 400.00 GiB
			Params:       671030000000, // 671.03 B
			NLayers:      61,
			NExperts:     256,
			NExpertsUsed: 8,
			CtxTrain:     163840,
			// Attention, the shared expert and the eight routed experts a
			// token actually touches. The embedding table is a row lookup and
			// the other 248 experts are not read.
			// The placement's dense classes (41.34 GB) plus 8 of 256 experts
			// from the 383.33 GB stack. It was 21.70 GB until 2026-09-14,
			// which no reading of this placement produces — the two accounts
			// of one tensor list disagreed by 59 % and every ratio derived
			// from it was refused (TTP-46). The placement wins: it is the
			// detailed one.
			ActiveBytesPerToken: 53317992448,
		},
		Host: tape.HostInfo{
			Hostname:    "bigram",
			OS:          "linux",
			Kernel:      "6.8.0-45-generic",
			CPU:         "AMD EPYC 9354 32-Core Processor",
			CPUCores:    32,
			CPUThreads:  64,
			RAMBytes:    824633720832, // 768 GiB
			RAMSpeed:    "DDR5-4800",
			RAMChannels: 12,
			GPUs:        exampleGPUs(),
		},
		Placement: tape.PlacementSummary{
			Devices: []tape.DevicePlacement{
				{
					Device: "GPU0",
					Bytes:  23085449216, // 21.5 GiB
					Classes: map[tape.TensorClass]int64{
						tape.ClassAttention: 18253611008,
						tape.ClassEmbed:     4831838208,
					},
					Layers: "0-30",
				},
				{
					Device: "GPU1",
					Bytes:  23085449216, // 21.5 GiB
					Classes: map[tape.TensorClass]int64{
						tape.ClassAttention: 18253611008,
						tape.ClassOutput:    4831838208,
					},
					Layers: "31-60",
				},
				{
					Device: tape.DeviceCPU,
					Bytes:  383325831168, // 357.0 GiB of routed experts
					Classes: map[tape.TensorClass]int64{
						tape.ClassExperts: 383325831168,
					},
					Layers: "0-60, experts",
				},
			},
			Source:           "gguf+args",
			VRAMWeightsBytes: 46170898432, // 43.0 GiB, the two devices together
			VRAMKVBytes:      3221225472,  // 3.0 GiB, MLA at 16k over four slots
			VRAMComputeBytes: 1610612736,  // 1.5 GiB of compute buffers
		},
		Memory: tape.MemorySummary{
			// The opposite picture to Example's: the experts really are in
			// host RAM, so RSS is most of the file and there is nothing to
			// fault in during decode.
			AtEnd: tape.MemSample{
				VirtBytes:    484205133824, // 451 GiB of mapping
				RSSBytes:     385473314816, // 359.0 GiB
				RSSFileBytes: 383325831168, // 357.0 GiB, the mapped experts
				RSSAnonBytes: 2147483648,   // 2.0 GiB
				MinFaults:    2841203,
			},
			PeakRSSBytes:    385473314816,
			MappedFileBytes: 429496729600, // the whole nine-part set
		},
		Concurrency: 4,
		Timings: tape.TimingsSummary{
			PromptN:                       384,
			CacheN:                        128,
			PromptMs:                      2232.6,
			PromptPerSecond:               172.0,
			PredictedN:                    248,
			PredictedMs:                   35942.0,
			PredictedPerSecond:            6.9,
			TTFTMs:                        2650,
			ClientPromptPerSecond:         170.0,
			ClientPredictedPerSecond:      6.9,
			ClientAgreesWithServer:        true,
			DecodeLabel:                   "decode",
			ITLp50Ms:                      144.9,
			ITLp95Ms:                      158.0,
			ITLp99Ms:                      174.2,
			EffectiveBandwidthBytesPerSec: 367894147891, // 53.32 GB/token × 6.9 tok/s
		},
		Aggregate: tape.AggregateTimings{
			Streams:         4,
			WallMs:          38950,
			TotalPromptN:    1536,
			TotalPredictedN: 992,
			// Four streams at 6.9 each. The server-wide prompt rate is the
			// four prompts' 1536 evaluated tokens over the window that ends
			// when the last of them gets its first token.
			AggregatePredictedPerSecond: 27.6,
			AggregatePromptPerSecond:    495.5,
			PerStreamPredictedPerSecond: 6.9,
			TTFTp50Ms:                   2650,
			TTFTp95Ms:                   3100,
			SlotsBusyMax:                4,
		},
		Cache: tape.CacheSummary{
			HitTokens:   128,
			PromptTotal: 512,
			HitRatio:    0.25,
			Label:       tape.CacheWarm,
		},
		Contention: tape.ContentionInfo{LoadAvg1: 28.4},
		Template: tape.TemplateInfo{
			ChatTemplate:         "deepseek3",
			RenderedPromptSHA256: "4d1f8a3c9b2e57061d8c3a7f2b4e69015c8d3a7f1b9e24605c7d1a3f8b2e4960",
			RenderedPromptTokens: 512,
		},
		GPUsAtEnd: []tape.GPUSample{
			{Index: 0, UsedBytes: 25662847385, ProcBytes: 25449051750, UtilPct: 42, TempC: 58, PowerW: 174, ClockMHz: 1710},
			{Index: 1, UsedBytes: 25340708454, ProcBytes: 25126912819, UtilPct: 39, TempC: 56, PowerW: 166, ClockMHz: 1710},
		},
	}
}
