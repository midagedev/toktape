package tui

import (
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// ExampleTape returns a realistic eight-stream run: the rig and the headline
// figures of card.ExampleConcurrent, with a full token timeline under them.
//
// It exists for the same reason card.Example does — the golden tests, the
// headless frame dump and, later, the GIF track all draw the same run instead
// of each inventing a fixture. The figures are internally consistent: every
// stream decodes 80 tokens at the summary's 12.1 tok/s, eight of them make the
// summary's 96.8 tok/s aggregate, and the run ends at 7.0 s so that a frame
// taken at 8 s is a finished run.
//
// The token count is 80 per stream rather than the 30 of a smaller fixture
// because the two have to agree: at 12.1 tok/s, 30 tokens are over in 2.4 s,
// and every frame after that would be the same frozen screen.
func ExampleTape() *tape.Tape {
	const (
		streams     = 8
		tokens      = 80
		promptTotal = 512
	)
	// perStream is a variable, not a constant: the inter-token interval below
	// is derived from it, and a constant expression there would not be a whole
	// number of nanoseconds.
	perStream := 12.1 // tok/s, the summary's per-stream decode rate
	itl := time.Duration(float64(time.Second) / perStream)

	summary := *card.ExampleConcurrent()
	// The v41-demo's signature figure: tensors the server maps but never
	// reads. It comes from the GGUF header, never from total-minus-RSS
	// (handover lesson 3).
	summary.Placement.NeverLoadedBytes = 90800000000
	summary.Contention.Contended = true
	summary.Contention.Reasons = []string{"loadavg 12.3 > cores 16"}
	summary.Contention.LoadAvg1 = 12.3

	tp := &tape.Tape{Schema: tape.SchemaVersion, Summary: summary}

	runEnd := time.Duration(0)
	for i := 0; i < streams; i++ {
		started := time.Duration(i) * 25 * time.Millisecond
		ttft := time.Duration(210+i*40) * time.Millisecond

		req := tape.RequestRecord{
			Index:     i,
			Slot:      i,
			StartedAt: started,
			Prompt: tape.PromptRecord{
				Messages:       []tape.Message{{Role: "user", Content: examplePrompt(i)}},
				RenderedPrompt: exampleRendered(i),
			},
			Timings: tape.TimingsSummary{
				PromptN:                  promptTotal,
				PromptPerSecond:          1031,
				PredictedN:               tokens,
				PredictedMs:              float64(tokens-1) / perStream * 1000,
				PredictedPerSecond:       perStream,
				TTFTMs:                   msOf(ttft),
				ClientPredictedPerSecond: perStream,
				ClientAgreesWithServer:   true,
				DecodeLabel:              "decode",
				ITLp50Ms:                 78.4,
				ITLp95Ms:                 164.0,
				ITLp99Ms:                 402.5,
			},
			Cache: tape.CacheSummary{PromptTotal: promptTotal, Label: tape.CacheCold},
		}

		// Prompt progress: four rows ramping to the full prompt, all of it a
		// miss — this run is cold, which is what the amber cache tag says.
		for s := 1; s <= 4; s++ {
			frac := float64(s) / 4
			req.Progress = append(req.Progress, tape.PromptProgress{
				T:         time.Duration(float64(ttft) * frac * 0.8),
				Total:     promptTotal,
				Cache:     0,
				Processed: int(float64(promptTotal) * frac),
				TimeMs:    msOf(ttft) * frac * 0.8,
			})
		}

		words := exampleWords(i, tokens)
		at := ttft
		for k := 0; k < tokens; k++ {
			// A page-in burst between 2.4 s and 3.1 s of the run: the stall
			// the sparkline and the latency strip both have to show.
			abs := started + at
			var faults uint64
			// Real inter-token latency is never flat. The jitter pattern
			// averages to exactly 1, so the stream still decodes at the rate
			// the summary reports while the latency strip has something to
			// draw.
			step := time.Duration(float64(itl) * itlJitter[(k+i)%len(itlJitter)])
			if abs > 2400*time.Millisecond && abs < 3100*time.Millisecond {
				faults = uint64(1 + (k+i)%4)
				step += time.Duration(faults) * 45 * time.Millisecond
			}
			req.Tokens = append(req.Tokens, tape.TokenEvent{
				T:              at,
				Index:          k,
				Text:           words[k],
				PredictedN:     k + 1,
				PredictedMs:    msOf(at - ttft),
				MajFaultsDelta: faults,
			})
			if abs > runEnd {
				runEnd = abs
			}
			at += step
		}
		var b strings.Builder
		for _, tk := range req.Tokens {
			b.WriteString(tk.Text)
		}
		req.Prompt.Completion = b.String()
		req.Prompt.FinishReason = "length"

		// The rate the summary reports is measured off the timeline that was
		// just generated, not copied from the card fixture: the page-in burst
		// costs real milliseconds, and a summary that ignored them would put
		// the screen's live figure at odds with its own header (CLAUDE.md:
		// never fix a disagreement by picking the nicer number).
		win := req.Tokens[len(req.Tokens)-1].T - req.Tokens[0].T
		rate := float64(len(req.Tokens)-1) / win.Seconds()
		req.Timings.PredictedMs = msOf(win)
		req.Timings.PredictedPerSecond = rate
		req.Timings.ClientPredictedPerSecond = rate
		tp.Requests = append(tp.Requests, req)
	}

	var rateSum float64
	for _, req := range tp.Requests {
		rateSum += req.Timings.PredictedPerSecond
	}
	perStreamMean := rateSum / float64(len(tp.Requests))
	tp.Summary.Timings.PredictedPerSecond = perStreamMean
	tp.Summary.Timings.ClientPredictedPerSecond = perStreamMean
	tp.Summary.Timings.PredictedMs = tp.Requests[0].Timings.PredictedMs
	tp.Summary.Aggregate.WallMs = msOf(runEnd)
	tp.Summary.Aggregate.TotalPredictedN = streams * tokens
	tp.Summary.Aggregate.TotalPromptN = streams * promptTotal
	tp.Summary.Aggregate.PerStreamPredictedPerSecond = perStreamMean
	tp.Summary.Aggregate.AggregatePredictedPerSecond = rateSum
	tp.Summary.FinishedAt = tp.Summary.StartedAt.Add(runEnd)

	tp.Samples = exampleSamples(tp, runEnd)
	tp.Summary.Memory.MajFaultsTotal = totalFaults(tp)
	tp.Summary.Memory.MajFaultsDecode = tp.Summary.Memory.MajFaultsTotal
	tp.Summary.Memory.MajFaultsPerToken =
		float64(tp.Summary.Memory.MajFaultsTotal) / float64(streams*tokens)
	return tp
}

// exampleSamples takes one host reading every tape.DefaultSampleInterval, with
// VRAM climbing as the KV cache fills and load rising as the streams pile up.
func exampleSamples(tp *tape.Tape, runEnd time.Duration) []tape.RunSample {
	gibF := float64(gib)
	var out []tape.RunSample
	for t := time.Duration(0); t <= runEnd; t += tape.DefaultSampleInterval {
		frac := float64(t) / float64(runEnd)
		mem := tape.MemSample{
			VirtBytes:    42949672960,
			RSSBytes:     int64(14 * gibF * (0.55 + 0.45*frac)),
			RSSFileBytes: int64(13 * gibF * (0.55 + 0.45*frac)),
			RSSAnonBytes: int64(1.1 * gibF),
			MinFaults:    uint64(128402 + 4000*frac),
		}
		var gpus []tape.GPUSample
		for i := 0; i < 2; i++ {
			base := int64(9.2*gibF) + int64(i)*int64(0.2*gibF)
			gpus = append(gpus, tape.GPUSample{
				Index:     i,
				UsedBytes: base + int64(1.1*gibF*frac),
				ProcBytes: base,
				UtilPct:   88 + 11*frac,
				TempC:     58 + 13*frac,
				PowerW:    280 + 60*frac,
				ClockMHz:  1830 - int(75*frac),
			})
		}
		out = append(out, tape.RunSample{
			T:           t,
			Mem:         mem,
			GPUs:        gpus,
			LoadAvg1:    3.1 + 9.2*frac,
			TokensSoFar: tokensBefore(tp, t),
			SlotsBusy:   8,
		})
	}
	return out
}

func tokensBefore(tp *tape.Tape, t time.Duration) int {
	n := 0
	for _, req := range tp.Requests {
		for _, tk := range req.Tokens {
			if req.StartedAt+tk.T > t {
				break
			}
			n++
		}
	}
	return n
}

func totalFaults(tp *tape.Tape) uint64 {
	var n uint64
	for _, req := range tp.Requests {
		for _, tk := range req.Tokens {
			n += tk.MajFaultsDelta
		}
	}
	return n
}

// Two of the eight streams answer in Korean. That is not decoration: it is the
// case the border has to survive, and a frame the lead looks at should show
// it (handover lesson 5, "CJK is two columns").
var exampleAnswers = []string{
	"The page fault spike you are seeing is the expert tensors being read " +
		"back from NVMe. With twelve MoE layers pinned to the CPU the router " +
		"picks eight experts per token, and any expert that fell out of the " +
		"page cache has to come back over PCIe before the next token can be " +
		"emitted. That is the gap in the latency strip.",
	"레이어 배치를 바꾸면 디코드 속도가 달라지는 이유는 간단하다. " +
		"전문가 텐서가 호스트 메모리에 남아 있으면 토큰마다 PCIe를 건너야 하고, " +
		"페이지 캐시에서 밀려난 텐서는 NVMe까지 내려간다. 스파크라인이 튀는 구간이 " +
		"정확히 그 지점이다.",
	"Flash attention changes the compute buffer size, not the weights. When " +
		"the buffer grows past the free VRAM the fit pass silently moves a " +
		"block of experts to host RAM, and the decode rate halves without a " +
		"single line in the log to say so.",
	"Prefix caching only helps when the system prompt is byte identical. A " +
		"single changed character invalidates the whole prefix, the cache hit " +
		"drops to zero, and the prefill you thought was free costs the full " +
		"five hundred tokens again.",
	"측정값이 흔들릴 때는 먼저 머신이 한가한지 본다. 다른 프로세스가 같은 GPU를 " +
		"쓰고 있으면 재는 대상보다 잡음이 크다. 그래서 이 화면은 load 와 다른 GPU " +
		"프로세스 수를 항상 같이 보여 준다.",
	"The aggregate rate is what the server does; the per stream rate is what " +
		"one agent feels. Eight sessions at twelve tokens a second is a very " +
		"different product from one session at ninety six, and only one of " +
		"those two numbers tells you which one you have.",
	"Speculative decoding is a bet on the draft model. Below roughly half " +
		"acceptance you are paying for two forward passes and taking one " +
		"token, so the headline speedup turns into a slowdown and nothing on " +
		"the usual dashboards says why.",
	"RSS is not loaded. Under mmap the resident set is what this process has " +
		"touched, the page cache holds the rest, and whatever was copied to " +
		"VRAM may have been dropped again. Never loaded comes from the tensor " +
		"headers, not from subtracting.",
}

// itlJitter scales successive inter-token intervals. The eight factors average
// to exactly 1, so a stream's decode rate still matches the figure the summary
// reports.
var itlJitter = []float64{0.86, 1.14, 0.94, 1.06, 0.90, 1.10, 0.96, 1.04}

// exampleWords splits one answer into n token-sized pieces, keeping the space
// that precedes a word attached to it the way a real tokenizer does. The answer
// repeats when it runs out, with the space kept so two sentences never collide.
func exampleWords(i, n int) []string {
	fields := strings.Fields(exampleAnswers[i%len(exampleAnswers)])
	if len(fields) == 0 {
		fields = []string{"…"}
	}
	out := make([]string, 0, n)
	for j := 0; j < n; j++ {
		w := fields[j%len(fields)]
		if j == 0 {
			out = append(out, w)
			continue
		}
		out = append(out, " "+w)
	}
	return out
}

func examplePrompt(i int) string {
	return exampleAnswers[i%len(exampleAnswers)][:40] + " — explain."
}

func exampleRendered(i int) string {
	return "<|im_start|>system\nYou are a helpful assistant.<|im_end|>\n" +
		"<|im_start|>user\n" + examplePrompt(i) + "<|im_end|>\n" +
		"<|im_start|>assistant\n<think>\n\n</think>\n\n"
}
