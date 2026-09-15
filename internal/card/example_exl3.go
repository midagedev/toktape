package card

import (
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// The ExLlamaV3 example run: GLM-5.3-Flash at EXL3 4.05 bpw behind a
// llama-server-protocol shim, the shape the engine object exists for
// (2026-09-15, ExLlamaV3).
//
// Everything a llama-server run reads off a GGUF header and an argv came from
// /props instead: the model's figures from engine.model, the placement from
// engine.placement, the flags from engine.args. The card must not read as a
// list of question marks just because no GGUF was opened — every one of those
// figures was reported, and the fixture carries them all.
//
// The derivation, so a reviewer can check the arithmetic rather than trust it:
//
//	model      GLM-5.3-Flash-exl3-4.05, 165,151,541,665 bytes over 30 files.
//	           EXL3 4.05 bpw with a 6.0 bpw head, 320 B params, 45 layers,
//	           288 experts of which 8 route each token, 1,048,576 ctx train.
//	           7.0 GB of weights are read per decoded token — the routed
//	           experts' share plus every dense tensor, the number an EXL3
//	           engine states because it knows its own layout.
//	placement  the engine's own report: GPU0 (an RTX A6000) holds the
//	           attention, the embeddings and experts 0-20 (40 GB); GPU1 (an
//	           RTX 3090) holds experts 21-30 and the output head (20 GB); the
//	           CPU holds experts 31-44 (105.2 GB) in a multiprocessing child
//	           of the port listener. KV cache is 6 GB across the two GPUs.
//	           Dynamic expert placement swaps experts between devices every
//	           sweep, so the engine does NOT say what one device is read per
//	           token — the normal case, and the reason no RAM or verify
//	           bandwidth line may appear on this card: absent, not "?".
//	flags      the engine's own argv: -gs 44,21 -mcs 185 -mct 32 -mtp. -mtp
//	           is multi-token prediction, the model drafting its own
//	           continuations, so the timings carry draft figures with no
//	           draft model to name.
//	decode     2 streams at 19.8 tok/s each, 39.6 together. 240 tokens a
//	           stream over 12.1 s; 400 drafted, 300 accepted run-wide (75 %),
//	           so the target model ran 480 − 300 = 180 verify steps of ~3.2
//	           tokens. ITL p50 50.5 ms is 12.1 s over 240.
//	host       A6000 768 GB/s + RTX 3090 936.2 GB/s, host bus measured at
//	           230 GB/s. The CPU expert stack sits behind that bus, which is
//	           the wall this rig runs against — stated, not derived, because
//	           the engine did not report the split that would derive it.
//	memory     summed over the listener (pid 8821) and its expert child, the
//	           two processes the engine is; the card says so in its warnings.

// ExampleExLlamaV3 returns the normal engine run: every model, placement and
// flag figure reported through /props, and no per-device active bytes — the
// case the bandwidth clauses must stay silent for rather than estimate.
func ExampleExLlamaV3() *tape.RunSummary {
	started := time.Date(2026, 9, 15, 21, 8, 44, 0, time.UTC)
	draftN, draftAccepted := 400, 300
	return &tape.RunSummary{
		ID:             "20260915-210844-glm-5.3-flash-exl3-4.05",
		ToktapeVersion: "0.1.0",
		StartedAt:      started,
		FinishedAt:     started.Add(12810 * time.Millisecond),

		Server: tape.ServerInfo{
			Kind:  tape.ServerExLlamaV3,
			URL:   "http://127.0.0.1:8000",
			Build: "1.5.0",
			// No commit: the engine reports a version and no hash, and an
			// empty one prints nothing rather than a bracketed gap.
			PID:  8821,
			Host: "workstation",
			// Args is the listener's own argv out of /proc/8821/cmdline; it
			// is two processes, and this is the one holding the port. Flags
			// is the engine's own args list from /props — the same launch
			// the operator typed, which is why the two agree.
			Args: []string{
				"python", "-m", "exllamav3.server",
				"--model", "/models/GLM-5.3-Flash-exl3-4.05",
				"--host", "127.0.0.1", "--port", "8000",
				"-gs", "44,21", "-mcs", "185", "-mct", "32", "-mtp",
			},
			Flags: tape.ServerFlags{
				Other:      []string{"-gs", "44,21", "-mcs", "185", "-mct", "32", "-mtp"},
				DraftModel: "mtp", // engine.draft: the model's own MTP head
				DraftMax:   "1",
			},
			// The shim's own /props: two slots at 32k context.
			NSlots:  2,
			CtxSize: 32768,
		},
		Model: tape.ModelInfo{
			Path:      "/models/GLM-5.3-Flash-exl3-4.05",
			FileName:  "GLM-5.3-Flash-exl3-4.05",
			Format:    "exl3",
			Arch:      "Glm5NextForConditionalGeneration",
			Quant:     "EXL3 4.05 bpw · head 6.0",
			FileBytes: 165151541665,
			Params:    320000000000,
			NLayers:   45,
			NExperts:  288,
			// 8 of 288 experts route each token.
			NExpertsUsed:        8,
			CtxTrain:            1048576,
			ActiveBytesPerToken: 7000000000,
		},
		Host: tape.HostInfo{
			Hostname:    "workstation",
			OS:          "linux",
			Kernel:      "6.8.0-51-generic",
			CPU:         "AMD EPYC 9554P",
			CPUCores:    64,
			CPUThreads:  128,
			RAMBytes:    549755813888, // 512 GiB
			RAMSpeed:    "DDR5-4800",
			RAMChannels: 12,
			GPUs: []tape.GPUInfo{
				{
					Index: 0, Name: "NVIDIA RTX A6000",
					VRAMBytes: 51539607552, // 48 GiB
					Driver:    "550.54.14", PCIe: "4.0 x16",
					PeakBandwidthBytesPerSec: 768000000000,
				},
				{
					Index: 1, Name: "NVIDIA GeForce RTX 3090",
					VRAMBytes: 25769803776, // 24 GiB
					Driver:    "550.54.14", PCIe: "4.0 x16",
					PeakBandwidthBytesPerSec: 936200000000,
				},
			},
			RAMBytesPerSec: 230000000000,
			RAMSource:      tape.RAMSourceMeasured,
		},
		Placement: tape.PlacementSummary{
			// The engine's own report, in its own device order.
			Devices: []tape.DevicePlacement{
				{
					Device: "GPU0", Bytes: 40000000000,
					Classes: map[tape.TensorClass]int64{
						tape.ClassAttention: 8000000000,
						tape.ClassExperts:   30000000000,
						tape.ClassEmbed:     2000000000,
					},
					Layers: "experts 0-20",
				},
				{
					Device: "GPU1", Bytes: 20000000000,
					Classes: map[tape.TensorClass]int64{
						tape.ClassExperts: 19000000000,
						tape.ClassOutput:  1000000000,
					},
					Layers: "experts 21-30",
				},
				{
					Device: tape.DeviceCPU, Bytes: 105151541665,
					Classes: map[tape.TensorClass]int64{
						tape.ClassExperts: 105151541665,
					},
					Layers: "experts 31-44",
				},
			},
			Source:           "engine",
			VRAMWeightsBytes: 60000000000, // the two GPUs together
			VRAMKVBytes:      6000000000,
		},
		Memory: tape.MemorySummary{
			// The tree sum: the listener plus its expert child. How the
			// engine holds its CPU experts (file pages or its own memory) is
			// not something an engine placement lets the card attribute, so
			// these figures print as RSS only and never as a RAM/disk split
			// (tape.Residency). MappedFileBytes is 0 because the model is a
			// directory of 30 files and no single mapping was measured.
			AtEnd: tape.MemSample{
				VirtBytes:    150323855360, // 140 GiB
				RSSBytes:     103079215104, // 96 GiB
				RSSFileBytes: 98876291584,  // 92 GiB
				RSSAnonBytes: 4203086352,   // 4 GiB, both processes
				MinFaults:    3140668,
			},
			PeakRSSBytes:    103079215104,
			MappedFileBytes: 0,
		},
		Concurrency: 2,
		Timings: tape.TimingsSummary{
			// The per-stream mean: 240 of the run's 480 tokens.
			PromptN:            640,
			CacheN:             128,
			PromptMs:           556.5,
			PromptPerSecond:    1150.0,
			PredictedN:         240,
			PredictedMs:        12121.2,
			PredictedPerSecond: 19.8,
			TTFTMs:             612,
			// MTP drafts: run-wide sums, 200 drafted / 150 accepted a stream.
			DraftN:                   &draftN,
			DraftNAccepted:           &draftAccepted,
			ClientPromptPerSecond:    1142.0,
			ClientPredictedPerSecond: 19.7,
			ClientAgreesWithServer:   true,
			DecodeLabel:              "decode",
			ITLp50Ms:                 50.5,
			ITLp95Ms:                 58.1,
			ITLp99Ms:                 71.9,
			// Per stream: 7.0 GB/token × 19.8 tok/s.
			EffectiveBandwidthBytesPerSec: 138600000000,
		},
		Aggregate: tape.AggregateTimings{
			Streams:                     2,
			WallMs:                      12810,
			TotalPromptN:                1280,
			TotalPredictedN:             480,
			AggregatePredictedPerSecond: 39.6,
			// The prompts share the batch: 1280 over the window that ends at
			// the last stream's first token (0.75 s).
			AggregatePromptPerSecond:    1706.0,
			PerStreamPredictedPerSecond: 19.8,
			TTFTp50Ms:                   612,
			TTFTp95Ms:                   705,
			SlotsBusyMax:                2,
		},
		Cache: tape.CacheSummary{
			HitTokens:   128,
			PromptTotal: 768,
			HitRatio:    0.1667,
			Label:       tape.CacheWarm,
		},
		Contention: tape.ContentionInfo{LoadAvg1: 4.2},
		Sampling:   tape.SamplingSummary{Endpoint: tape.EndpointChat},
		Template: tape.TemplateInfo{
			ChatTemplate:         "glm5",
			RenderedPromptSHA256: "4a7d1c9e2b8f60a3d5e1f7c4b9a2e8d6f0c3b5a7d9e1f4c8b2a6d0e3f7c1b5a9",
			RenderedPromptTokens: 768,
		},
		GPUsAtEnd: []tape.GPUSample{
			{Index: 0, UsedBytes: 46560654526, ProcBytes: 46368559104, UtilPct: 96, TempC: 72, PowerW: 298, ClockMHz: 1815},
			{Index: 1, UsedBytes: 23174611968, ProcBytes: 22983488512, UtilPct: 93, TempC: 67, PowerW: 341, ClockMHz: 1950},
		},
		Warnings: []string{
			"memory, faults and CPU summed over 2 processes (pid 8821 and its children)",
		},
	}
}

// ExampleExLlamaV3Split is the same run reported by an engine that DID state
// what each device is read per token — GPU0 3.0 GB, GPU1 1.5 GB, CPU 2.5 GB,
// summing to the model's 7.0 GB. Nothing else moves: the figures are the
// engine's own, so the bandwidth clauses come back built from them rather than
// from the class-proportion estimate that stays off for engine placements.
func ExampleExLlamaV3Split() *tape.RunSummary {
	s := ExampleExLlamaV3()
	s.Placement.Devices = []tape.DevicePlacement{
		{
			Device: "GPU0", Bytes: 40000000000, ActiveBytesPerToken: 3000000000,
			Classes: map[tape.TensorClass]int64{
				tape.ClassAttention: 8000000000,
				tape.ClassExperts:   30000000000,
				tape.ClassEmbed:     2000000000,
			},
			Layers: "experts 0-20",
		},
		{
			Device: "GPU1", Bytes: 20000000000, ActiveBytesPerToken: 1500000000,
			Classes: map[tape.TensorClass]int64{
				tape.ClassExperts: 19000000000,
				tape.ClassOutput:  1000000000,
			},
			Layers: "experts 21-30",
		},
		{
			Device: tape.DeviceCPU, Bytes: 105151541665, ActiveBytesPerToken: 2500000000,
			Classes: map[tape.TensorClass]int64{
				tape.ClassExperts: 105151541665,
			},
			Layers: "experts 31-44",
		},
	}
	return s
}
