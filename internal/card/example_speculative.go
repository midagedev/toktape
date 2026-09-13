package card

import (
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// The speculative example is the run TTP-30 exists for: a large MoE target on
// ik_llama.cpp with its experts on the CPU, a small draft model on the GPUs,
// and four agent sessions at once.
//
// It is a separate fixture rather than a variant of Example() because it has
// to be a different *shape* of run, not the same run with two extra fields.
// Speculative decoding is used where a pass over the weights is expensive
// enough that guessing three tokens and checking them is cheaper than reading
// them three times — which is the CPU-resident-expert case, not the
// fully-offloaded dense case Example() describes. It also carries the other
// half of the same rig log: ik_llama.cpp stamps no bNNNN build number, so
// ServerInfo.Build is empty and the version is a bare commit (TTP-33).
//
// The derivation, so a reviewer can check the arithmetic rather than trust it:
//
//	target       DeepSeek-V4.1-Flash-Q4_K_M.gguf, 46.0 GiB, 81.4 B params,
//	             48 layers, 128 experts with 8 used per token.
//	draft        DSpark-0.6B-Q8_0.gguf at --draft-max 3: the server drafts up
//	             to three tokens, the target verifies them in one pass, and the
//	             ones it agrees with are free.
//	placement    -ncmoe 48 puts every expert tensor in host RAM (39.3 GiB) and
//	             leaves attention, the shared expert and the embeddings on the
//	             two GPUs (3.4 + 3.3 GiB). The draft model's own weights sit in
//	             what is left of the VRAM; the tape has one Model, so they show
//	             up in the devices' used bytes and not in the placement.
//	per token    4.0 GB of attention and shared weights off the GPUs plus
//	             8/128 of 43.5 GB of experts off DDR5 ≈ 6.72 GB read per
//	             verification pass — and a pass that gets three drafts right
//	             emits four tokens for that one read, which is the whole point.
//	decode       19.0 tok/s per stream with four at once, 76.0 together. The
//	             host memory bus is the floor: 19 passes a second × 2.72 GB of
//	             expert weights ≈ 52 GB/s of the ~70 GB/s a dual-channel
//	             DDR5-6000 pair actually reaches.
//	acceptance   290 tokens drafted across the four streams and 174 accepted,
//	             60 %. That is the run-level figure the card prints, and it is
//	             the SUM over the streams rather than a mean of their rates
//	             (tape.TimingsSummary): accepted over drafted is a ratio, and
//	             pooling is the only reduction that keeps it honest.
//	tokens       ~97 verification passes produced 272 tokens across the four
//	             streams (97 + 174 accepted), 68 each — a short answer, which
//	             is what an agent session usually sends.
//	prefill      817 tok/s per stream, 2130 server-wide. 512-token prompt with
//	             128 cached, so 384 are evaluated and the first token lands at
//	             640 ms once the four prompts have shared a batch.
//	host         load 5.4 on 32 threads: the expert matmuls are on the CPU, so
//	             this run is busy in a way Example()'s is not, and it is still
//	             not contended — nothing else is competing for the box.

// ExampleSpeculative returns a realistic four-stream summary of a run with
// speculative decoding on ik_llama.cpp.
//
// It is what the Draft row of the text card, the draft clause of the PNG hero
// and compare's "draft accepted" row are drawn from, and the goldens under
// testdata pin all three.
func ExampleSpeculative() *tape.RunSummary {
	started := time.Date(2026, 9, 13, 16, 14, 20, 0, time.UTC)
	draftN, draftAccepted := 290, 174
	return &tape.RunSummary{
		ID:             "20260913-161420-deepseek-v4-1-flash-q4-k",
		ToktapeVersion: "0.1.0",
		StartedAt:      started,
		FinishedAt:     started.Add(4280 * time.Millisecond),

		Server: tape.ServerInfo{
			Kind: tape.ServerIKLlama,
			URL:  "http://127.0.0.1:8080",
			// ik_llama.cpp has no bNNNN release counter, so /props carries a
			// bare commit and the card prints "ik_llama.cpp 7b79b229" with no
			// parentheses around it (TTP-33).
			Build:  "",
			Commit: "7b79b229",
			PID:    51402,
			Host:   "workstation",
			Args: []string{
				"/usr/local/bin/llama-server",
				"-m", "/models/DeepSeek-V4.1-Flash-Q4_K_M.gguf",
				"-md", "/models/drafts/DSpark-0.6B-Q8_0.gguf",
				"--draft-max", "3",
				"--draft-min", "1",
				"-c", "16384",
				"--parallel", "4",
				"-ngl", "99",
				"-ncmoe", "48",
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
				CPUMoE:     "48",
				Threads:    "16",
				DraftModel: "DSpark-0.6B-Q8_0.gguf",
				DraftMax:   "3",
				DraftMin:   "1",
			},
			NSlots:  4,
			CtxSize: 16384,
		},
		Model: tape.ModelInfo{
			Path:         "/models/DeepSeek-V4.1-Flash-Q4_K_M.gguf",
			FileName:     "DeepSeek-V4.1-Flash-Q4_K_M.gguf",
			Name:         "DeepSeek V4.1 Flash",
			Arch:         "deepseek2",
			Quant:        "Q4_K_M",
			FileBytes:    49392123904, // 46.0 GiB
			Params:       81400000000, // 81.40 B
			NLayers:      48,
			NExperts:     128,
			NExpertsUsed: 8,
			CtxTrain:     131072,
			// Attention, the shared expert and the norms (4.0 GB) plus the
			// eight experts a token routes to (8/128 of 43.5 GB). The
			// embedding table is a row lookup and is not counted.
			ActiveBytesPerToken: 6720000000,
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
					Bytes:  3650722201, // 3.4 GiB
					Classes: map[tape.TensorClass]int64{
						tape.ClassAttention: 1610612736,
						tape.ClassEmbed:     2040109465,
					},
					Layers: "0-23 attn",
				},
				{
					Device: "GPU1",
					Bytes:  3543348019, // 3.3 GiB
					Classes: map[tape.TensorClass]int64{
						tape.ClassAttention: 1610612736,
						tape.ClassOutput:    1932735283,
					},
					Layers: "24-47 attn",
				},
				{
					Device: tape.DeviceCPU,
					Bytes:  42198053684, // 39.3 GiB, every expert tensor
					Classes: map[tape.TensorClass]int64{
						tape.ClassExperts: 42198053684,
					},
					Layers: "0-47 experts",
				},
			},
			Source: "gguf+args",
			// The two GPU devices above; the experts are host RAM and the
			// draft model's weights belong to a model this tape does not
			// describe, so neither is in the VRAM weights figure.
			VRAMWeightsBytes: 7194070220, // 6.7 GiB
			VRAMKVBytes:      2576980378, // 2.4 GiB, q8_0 at 16k over 4 slots
			VRAMComputeBytes: 1503238554, // 1.4 GiB
		},
		Memory: tape.MemorySummary{
			// The opposite picture to Example(): the experts are read from
			// host RAM on every pass, so they are resident and RSS is most of
			// the file. It is still not "loaded" in any sense the tool would
			// claim — it is the page cache holding a mapping that is being
			// read (lesson 3) — and the run is warm, so nothing faulted in
			// from disk during decode.
			AtEnd: tape.MemSample{
				VirtBytes:    53687091200, // 50 GiB of mapping
				RSSBytes:     45204530790, // 42.1 GiB
				RSSFileBytes: 44238163149, // 41.2 GiB
				RSSAnonBytes: 966367642,   // 0.9 GiB
				MinFaults:    2148302,
			},
			PeakRSSBytes:    45204530790,
			MappedFileBytes: 49392123904,
		},
		Concurrency: 4,
		Timings: tape.TimingsSummary{
			PromptN:                       384,
			CacheN:                        128,
			PromptMs:                      470.0,
			PromptPerSecond:               817.0,
			PredictedN:                    68,
			PredictedMs:                   3578.9,
			PredictedPerSecond:            19.0,
			DraftN:                        &draftN,
			DraftNAccepted:                &draftAccepted,
			TTFTMs:                        640,
			ClientPromptPerSecond:         809.0,
			ClientPredictedPerSecond:      18.9,
			ClientAgreesWithServer:        true,
			DecodeLabel:                   "decode",
			ITLp50Ms:                      52.6,
			ITLp95Ms:                      58.1,
			ITLp99Ms:                      71.0,
			EffectiveBandwidthBytesPerSec: 127680000000, // 6.72 GB/token × 19.0 tok/s
		},
		Aggregate: tape.AggregateTimings{
			Streams:                     4,
			WallMs:                      4280,
			TotalPromptN:                1536,
			TotalPredictedN:             272,
			AggregatePredictedPerSecond: 76.0,
			AggregatePromptPerSecond:    2130.0,
			PerStreamPredictedPerSecond: 19.0,
			TTFTp50Ms:                   640,
			TTFTp95Ms:                   720,
			SlotsBusyMax:                4,
		},
		Cache: tape.CacheSummary{
			HitTokens:   128,
			PromptTotal: 512,
			HitRatio:    0.25,
			Label:       tape.CacheWarm,
		},
		Contention: tape.ContentionInfo{LoadAvg1: 5.4},
		Template: tape.TemplateInfo{
			ChatTemplate:         "deepseek3",
			RenderedPromptSHA256: "3d81f04ac6b25e79f0a1c8d3b6e5074a2c9f1b8d4e6a03157c2d9b4f6e8a0135",
			RenderedPromptTokens: 512,
		},
		GPUsAtEnd: []tape.GPUSample{
			// Attention plus its share of the KV cache and the compute
			// buffers, and on GPU0 the draft model as well.
			{Index: 0, UsedBytes: 6657199309, ProcBytes: 6442450944, UtilPct: 88, TempC: 64, PowerW: 240, ClockMHz: 1830},
			{Index: 1, UsedBytes: 5690831667, ProcBytes: 5476083302, UtilPct: 84, TempC: 61, PowerW: 225, ClockMHz: 1815},
		},
	}
}
