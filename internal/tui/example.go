package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// The example run: card.Example's rig — a dense 70B at Q4_K_M on two RTX
// 3090s — with a full token timeline under it.
//
// It exists so the golden tests, the headless frame dump, the hero clip and
// the README all draw the same run instead of each inventing a fixture. Every
// figure is derived from the card's, so a frame and the card it ends on cannot
// disagree (see internal/card/example.go for where the rig's numbers come
// from).
//
// 2026-09-13 (TTP-28, user: "토큰 생성하는 화면을 충분히 살펴보기에 재생시간이 너무
// 짧아"). The run used to be 80 tokens a stream and over in seven seconds,
// which is long enough to prove the layout renders and too short to watch. Now
// every request carries a 320-token budget, the answers stop on an end token
// somewhere past 296, and four streams take about twenty-six seconds — long
// enough that a reader of the clip sees tokens arrive, a sparkline scroll, a
// thinking stream cross into its answer, and the card at the end.
const (
	// exampleMaxTokens is the cap every request is sent with. Answer streams
	// stop short of it on their own; the reasoning-only stream runs into it,
	// which is the state the "thinking · cut" badge names.
	exampleMaxTokens = 320
	// exampleSingleRate is card.Example's single-stream decode rate, and
	// exampleBatchPenalty is how much each additional concurrent stream costs
	// the others: a batched decoding step reads the same weights for every
	// slot, so the server-wide rate rises while each session slows.
	exampleSingleRate   = 17.4
	exampleBatchPenalty = 0.13
	// The prompt: 512 tokens of which the prefix cache already held 128, so
	// 384 are evaluated at card.Example's 610 tok/s. Several agent sessions
	// share a system prompt, which makes a partial hit the ordinary case — and
	// the case worth drawing, because the cached prefix and the part being
	// evaluated are different shades of the same bar.
	examplePromptTotal = 512
	examplePromptCache = 128
	examplePrefillRate = 610.0
	// exampleQueueStep is how long each stream waits behind the one before it
	// for its turn at the prompt. The requests all go out together — an agent
	// workload dispatches its sessions at once — so the spread in time to
	// first token comes from the slots queueing, not from the client sending
	// late.
	exampleQueueStep = 60 * time.Millisecond
)

// ExampleMidRun is the instant the goldens, the tuidump frames and the
// self-check captures are taken at: far enough in that every stream has a rate,
// a sparkline and a page of text, and close enough to a thinking stream's
// crossing that the "▸ answer" marker is still on screen.
//
// It is here rather than in the tests because the fixture is built around it:
// exampleReasoningN puts the middle stream's crossing three seconds before it,
// at whatever rate the stream count implies.
const ExampleMidRun = 12 * time.Second

// exampleCrossLead is how long before ExampleMidRun the middle thinking stream
// stops thinking.
//
// A second and a half is a dozen or so tokens: two or three wrapped lines, so
// the "▸ answer" marker has moved up off the newest line but is still inside
// the tail even the shortest tile shows. Measured against the eight-stream
// frame at 140×40, which gives a tile five body rows — the smallest tile any
// test frames this state in.
const exampleCrossLead = 1500 * time.Millisecond

// ExampleTape returns the eight-stream example run.
func ExampleTape() *tape.Tape { return ExampleTapeN(8) }

// ExampleTapeN is ExampleTape with a chosen stream count (2..8). The four
// stream form is the README hero (user, 2026-09-13: eight tiles are too busy
// for a clip; four read well). Thinking streams keep their three states at any
// count: the second stream thinks briefly, one in the middle crosses into its
// answer just before ExampleMidRun, and the last never stops.
func ExampleTapeN(streams int) *tape.Tape {
	if streams < 2 {
		streams = 2
	}
	if streams > 8 {
		streams = 8
	}
	perStream := examplePerStreamRate(streams)
	itl := time.Duration(float64(time.Second) / perStream)

	summary := *card.ExampleConcurrent()
	summary.Concurrency = streams
	summary.Server.NSlots = streams
	tp := &tape.Tape{Schema: tape.SchemaVersion, Summary: summary}

	runEnd := time.Duration(0)
	for i := 0; i < streams; i++ {
		ttft := exampleTTFT(i)
		tokens := exampleTokenCount(i, streams)

		req := tape.RequestRecord{
			Index:     i,
			Slot:      i,
			StartedAt: 0,
			Prompt: tape.PromptRecord{
				Messages:       []tape.Message{{Role: "user", Content: examplePrompt(i)}},
				RenderedPrompt: exampleRendered(i),
				// The cap has a field of its own — the recorder fills it with
				// what the request was sent with — and is in Params as well,
				// because Params is the body as it went over the wire (lesson
				// 4, internal/server/stream.go).
				MaxTokens: exampleMaxTokens,
				Params:    map[string]any{"max_tokens": exampleMaxTokens},
			},
			Timings: tape.TimingsSummary{
				PromptN:                  examplePromptTotal - examplePromptCache,
				CacheN:                   examplePromptCache,
				PromptMs:                 examplePrefillMs(),
				PromptPerSecond:          examplePrefillRate,
				PredictedN:               tokens,
				PredictedPerSecond:       perStream,
				TTFTMs:                   msOf(ttft),
				ClientPredictedPerSecond: perStream,
				ClientAgreesWithServer:   true,
				DecodeLabel:              "decode",
				ITLp50Ms:                 1000 / perStream,
				ITLp95Ms:                 1000 / perStream * 1.08,
				ITLp99Ms:                 1000 / perStream * 1.19,
			},
			Cache: tape.CacheSummary{
				HitTokens:   examplePromptCache,
				PromptTotal: examplePromptTotal,
				HitRatio:    float64(examplePromptCache) / float64(examplePromptTotal),
				Label:       tape.CacheWarm,
			},
			Progress: exampleProgress(i),
		}

		reasoningN := exampleReasoningN(i, streams, tokens, perStream)
		words := exampleWords(i, tokens, reasoningN)
		at := ttft
		for k := 0; k < tokens; k++ {
			// Real inter-token latency is never flat, so the strip and the
			// sparklines have something to draw. The jitter pattern averages
			// to exactly 1 over its length, which keeps the stream decoding at
			// the rate the summary reports.
			req.Tokens = append(req.Tokens, tape.TokenEvent{
				T:           at,
				Index:       k,
				Text:        words[k],
				Reasoning:   k < reasoningN,
				PredictedN:  k + 1,
				PredictedMs: msOf(at - ttft),
			})
			if at > runEnd {
				runEnd = at
			}
			at += time.Duration(float64(itl) * itlJitter[(k+i)%len(itlJitter)])
		}

		// Completion is the answer alone and Reasoning the monologue: the
		// server counts both in predicted_n, and only the transcript keeps
		// them apart (internal/tape PromptRecord).
		var answer, thinking strings.Builder
		for _, tk := range req.Tokens {
			if tk.Reasoning {
				thinking.WriteString(tk.Text)
				continue
			}
			answer.WriteString(tk.Text)
		}
		req.Prompt.Completion = answer.String()
		req.Prompt.Reasoning = thinking.String()
		req.Prompt.ReasoningN = reasoningN
		req.Timings.ReasoningN = reasoningN
		// Only the stream that spent its whole budget thinking stopped because
		// it ran out; the rest stopped because they were finished.
		req.Prompt.FinishReason = "stop"
		if reasoningN >= tokens {
			req.Prompt.FinishReason = "length"
		}

		// The rate the summary reports is measured off the timeline that was
		// just generated rather than copied from the card fixture: the jitter
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
	var predicted, ttftSum float64
	for _, req := range tp.Requests {
		rateSum += req.Timings.PredictedPerSecond
		predicted += float64(req.Timings.PredictedN)
		ttftSum += req.Timings.TTFTMs
	}
	perStreamMean := rateSum / float64(len(tp.Requests))
	tp.Summary.Timings.PredictedN = int(predicted) / len(tp.Requests)
	tp.Summary.Timings.PredictedPerSecond = perStreamMean
	tp.Summary.Timings.ClientPredictedPerSecond = perStreamMean
	tp.Summary.Timings.PredictedMs = tp.Requests[0].Timings.PredictedMs
	tp.Summary.Timings.TTFTMs = ttftSum / float64(len(tp.Requests))
	tp.Summary.Cache = tp.Requests[0].Cache
	// Effective bandwidth follows the rate it is derived from. It is a
	// property of this run at this concurrency, not of the card fixture the
	// summary was copied from, and a card printing 12.5 tok/s beside a figure
	// computed for 9.1 would be two measurements of different runs on one
	// line (CLAUDE.md).
	tp.Summary.Timings.EffectiveBandwidthBytesPerSec =
		int64(float64(tp.Summary.Model.ActiveBytesPerToken) * perStreamMean)
	tp.Summary.Aggregate.WallMs = msOf(runEnd)
	tp.Summary.Aggregate.TotalPredictedN = int(predicted)
	tp.Summary.Aggregate.TotalPromptN = streams * (examplePromptTotal - examplePromptCache)
	tp.Summary.Aggregate.Streams = streams
	tp.Summary.Aggregate.SlotsBusyMax = streams
	tp.Summary.Aggregate.PerStreamPredictedPerSecond = perStreamMean
	tp.Summary.Aggregate.AggregatePredictedPerSecond = rateSum
	tp.Summary.Aggregate.TTFTp50Ms = percentile(exampleTTFTs(streams), 0.5)
	tp.Summary.Aggregate.TTFTp95Ms = percentile(exampleTTFTs(streams), 0.95)
	// The server-wide prompt rate, by the field's own definition: every
	// stream's evaluated tokens over the window that ends when the last of
	// them has its first token.
	tp.Summary.Aggregate.AggregatePromptPerSecond =
		float64(tp.Summary.Aggregate.TotalPromptN) / exampleTTFT(streams-1).Seconds()
	tp.Summary.FinishedAt = tp.Summary.StartedAt.Add(runEnd)

	tp.Samples = exampleSamples(tp, runEnd)
	// Weights that live in VRAM are not paged in during decode, so this run
	// takes no major faults at all. That is the ordinary case on a rig that
	// fits, and a fixture that showed a fault storm as the ordinary case was
	// teaching the reader the wrong alarm (TTP-28).
	tp.Summary.Memory.MajFaultsTotal = 0
	tp.Summary.Memory.MajFaultsDecode = 0
	tp.Summary.Memory.MajFaultsPerToken = 0
	return tp
}

// examplePerStreamRate is what one of n concurrent streams decodes at.
func examplePerStreamRate(streams int) float64 {
	return exampleSingleRate / (1 + exampleBatchPenalty*float64(streams-1))
}

// examplePrefillMs is how long the evaluated part of the prompt takes.
func examplePrefillMs() float64 {
	return float64(examplePromptTotal-examplePromptCache) / examplePrefillRate * 1000
}

// exampleTTFT is stream i's time to first token: its own prompt evaluation,
// plus the wait for the slots ahead of it.
func exampleTTFT(i int) time.Duration {
	return time.Duration(examplePrefillMs()*float64(time.Millisecond)) + time.Duration(i)*exampleQueueStep
}

func exampleTTFTs(streams int) []float64 {
	out := make([]float64, streams)
	for i := range out {
		out[i] = msOf(exampleTTFT(i))
	}
	return out
}

// exampleTokenCount is how many tokens stream i produces.
//
// The reasoning-only stream spends the whole budget; the rest stop on an end
// token somewhere in the high two hundreds, which is what a bounded answer
// actually does. A fixture where every stream stopped at the cap would show
// "320/320" on every tile and teach the reader that hitting the budget is
// normal.
func exampleTokenCount(i, streams int) int {
	if i == exampleCutStream(streams) {
		return exampleMaxTokens
	}
	return 296 + (i*5)%21
}

// exampleProgress is stream i's return_progress rows: one when the server takes
// the request, then eight as it works through the prompt.
//
// time_ms is the slot's own processing time and excludes the wait for the slots
// ahead, which is what llama-server reports and what makes every row reduce to
// the same 610 tok/s the summary carries. processed counts the tokens actually
// evaluated, not the cached prefix (docs/research/03: total 4096, cache 2048,
// processed 1024) — the bar adds the two to draw how much of the prompt is
// done.
func exampleProgress(i int) []tape.PromptProgress {
	queue := time.Duration(i) * exampleQueueStep
	// The row the server emits as it accepts the request, stamped at zero on
	// every stream: the requests all arrive together and the prefix cache's
	// share is known immediately, even on a stream that will wait for a slot.
	// processed starts at the cache length, the way the slot's prompt buffer
	// does upstream, so the row puts a bar rather than a blank wait on the
	// opening frame and still reduces to no rate at all.
	out := []tape.PromptProgress{{
		Total:     examplePromptTotal,
		Cache:     examplePromptCache,
		Processed: examplePromptCache,
	}}
	const rows = 8
	evaluated := examplePromptTotal - examplePromptCache
	for s := 1; s <= rows; s++ {
		frac := float64(s) / rows
		spent := time.Duration(examplePrefillMs() * frac * float64(time.Millisecond))
		out = append(out, tape.PromptProgress{
			T:         queue + spent,
			Total:     examplePromptTotal,
			Cache:     examplePromptCache,
			Processed: examplePromptCache + int(float64(evaluated)*frac),
			TimeMs:    msOf(spent),
		})
	}
	return out
}

// exampleCPUBase is the CPU time the example server had spent before the run
// began, in seconds: it loaded a model and served earlier requests, so its
// counter does not start at zero, and a zero would read as "not read".
const exampleCPUBase = 1843.2

// exampleSamples takes one host reading every tape.DefaultSampleInterval. VRAM
// climbs as the KV cache fills, and nothing else moves much: the run is GPU
// bound on a quiet machine, which is the picture the card reports.
//
// Utilisation and CPU time are shaped like a real run (TTP-39), so the
// resource graphs have something true-looking to draw: the GPUs pinned during
// prefill, working in proportion to the busy slots with a little jitter while
// they decode, and idle once the last token is out; the server process using
// a core or so throughout. Every figure is a function of the sample index k,
// so the tape is the same on every build.
func exampleSamples(tp *tape.Tape, runEnd time.Duration) []tape.RunSample {
	var out []tape.RunSample
	end := tp.Summary.GPUsAtEnd
	lastToken := time.Duration(0)
	for _, req := range tp.Requests {
		if n := len(req.Tokens); n > 0 && req.StartedAt+req.Tokens[n-1].T > lastToken {
			lastToken = req.StartedAt + req.Tokens[n-1].T
		}
	}
	cpu := exampleCPUBase
	k := 0
	// The sampler keeps reading until the recorder stops it, which is after
	// the last token: two readings past it show the devices going idle. frac
	// holds at 1 there, so the fields that ramp over the run do not overshoot.
	last := max(runEnd, lastToken+2*tape.DefaultSampleInterval)
	streams := tp.Summary.Concurrency
	if streams <= 0 {
		streams = len(tp.Requests)
	}
	for t := time.Duration(0); t <= last; t, k = t+tape.DefaultSampleInterval, k+1 {
		frac := min(1, float64(t)/float64(runEnd))
		mem := tp.Summary.Memory.AtEnd
		mem.MinFaults = uint64(128402 + 900*frac)
		mem.CPUSeconds = cpu
		// busy is the share of the streams still decoding. A rounds tape
		// counts every round's streams, so it is capped at one.
		busy := min(1, float64(slotsBusyAt(tp, t))/float64(max(1, streams)))
		prefill := tokensBefore(tp, t) == 0
		finished := t > lastToken
		cores := 0.6 + 1.2*busy + 0.05*float64((k*13)%5)
		if finished {
			cores = 0.1
		}
		cpu += cores * tape.DefaultSampleInterval.Seconds()
		var gpus []tape.GPUSample
		for i, g := range end {
			util := 52 + 36*busy - 5*float64(i) + float64((k*37+i*11)%13-6)
			switch {
			case prefill:
				util = 100
			case finished:
				util = 3
			}
			// The KV cache is what grows during the run; the weights were
			// resident before the first request arrived.
			kv := int64(float64(tp.Summary.Placement.VRAMKVBytes) / float64(len(end)))
			gpus = append(gpus, tape.GPUSample{
				Index:     i,
				UsedBytes: g.UsedBytes - kv + int64(float64(kv)*frac),
				ProcBytes: g.ProcBytes - kv + int64(float64(kv)*frac),
				UtilPct:   max(0, min(100, util)),
				TempC:     g.TempC - 3 + 3*frac,
				PowerW:    g.PowerW - 8 + 8*frac,
				ClockMHz:  g.ClockMHz + int(60*(1-frac)),
			})
		}
		out = append(out, tape.RunSample{
			T:           t,
			Mem:         mem,
			GPUs:        gpus,
			LoadAvg1:    tp.Summary.Contention.LoadAvg1,
			TokensSoFar: tokensBefore(tp, t),
			SlotsBusy:   slotsBusyAt(tp, t),
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

// slotsBusyAt is how many streams have not finished by t. The streams stop at
// different moments now, so the count falls towards the end of the run the way
// /slots would report it.
func slotsBusyAt(tp *tape.Tape, t time.Duration) int {
	n := 0
	for _, req := range tp.Requests {
		if len(req.Tokens) == 0 {
			continue
		}
		if req.StartedAt+req.Tokens[len(req.Tokens)-1].T >= t {
			n++
		}
	}
	return n
}

// itlJitter scales successive inter-token intervals. The sixteen factors
// average to exactly 1, so a stream's decode rate still matches the figure the
// summary reports.
//
// They wander rather than alternate. A short pattern that went fast, slow,
// fast, slow drew every sparkline as a comb — a texture, not a line — and a
// reader learns nothing from a shape that is the same in every tile and in
// every frame. The spread is about seven per cent either way, which is what an
// otherwise healthy stream looks like.
var itlJitter = []float64{
	0.97, 1.02, 1.06, 1.01, 0.95, 0.99, 1.04, 1.07,
	1.00, 0.94, 0.98, 1.03, 1.05, 0.96, 1.01, 0.92,
}

// exampleCutStream is the stream that spends its whole budget thinking: the
// last one, except at eight, where the grid's second page would hide it.
func exampleCutStream(streams int) int {
	if streams == 8 {
		return 6
	}
	return streams - 1
}

// exampleReasoningN is how many of stream i's n tokens are thinking.
//
// The counts are chosen so that one frame at ExampleMidRun shows all three
// states the pane can be in, at any stream count:
//
//   - stream 2 (0-based 1) thinks briefly and crossed into its answer early;
//   - one stream in the middle crosses exampleCrossLead before ExampleMidRun,
//     so the "▸ answer" marker is still inside its visible tail. Its count is
//     derived from the rate, because the rate depends on how many streams are
//     running and a fixed count would put the crossing in a different place
//     for four streams than for eight;
//   - exampleCutStream never stops, which is what a run that hits its budget
//     mid-monologue looks like — the state the header calls "thinking · cut".
func exampleReasoningN(i, streams, n int, rate float64) int {
	brief, mid := 1, 2
	if streams == 8 {
		mid = 4
	}
	switch i {
	case exampleCutStream(streams):
		return n
	case brief:
		return 24
	case mid:
		cross := ExampleMidRun - exampleCrossLead - exampleTTFT(i)
		return clampInt(int(cross.Seconds()*rate), 24, n-40)
	}
	return 0
}

// exampleWords is stream i's n token texts, the first reasoningN of them from
// the thinking monologue and the rest from the answer.
//
// Both are longer than the budget a stream can spend, so neither wraps; the
// shingle gate (TestExampleStreamsNeverRepeatThemselves) is what keeps that
// true as the counts change.
func exampleWords(i, n, reasoningN int) []string {
	answer := exampleFields(exampleAnswers[i%len(exampleAnswers)])
	thinking := exampleFields(exampleThinking[i%len(exampleThinking)])
	out := make([]string, 0, n)
	for j := 0; j < n; j++ {
		fields, at := answer, j-reasoningN
		if j < reasoningN {
			fields, at = thinking, j
		}
		w := fields[at%len(fields)]
		// No leading space at the start of the text or at the start of the
		// answer: the wrap treats a leading space as an empty first word, and
		// the answer begins its own line under the marker anyway.
		if j == 0 || j == reasoningN {
			out = append(out, w)
			continue
		}
		out = append(out, " "+w)
	}
	return out
}

// exampleFields splits one example paragraph into words, never returning an
// empty slice: the token loop indexes it modulo its length.
func exampleFields(s string) []string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return []string{"…"}
	}
	return fields
}

// examplePrompt is what stream i was asked. It is the first line of the answer
// turned back into a question, so a reader of the prompt modal sees a request
// that matches the reply under it.
func examplePrompt(i int) string {
	return fmt.Sprintf("%s — explain.", firstWords(exampleAnswers[i%len(exampleAnswers)], 8))
}

// firstWords is the first n words of s.
func firstWords(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) > n {
		fields = fields[:n]
	}
	return strings.Join(fields, " ")
}

func exampleRendered(i int) string {
	return "<|begin_of_text|><|start_header_id|>system<|end_header_id|>\n\n" +
		"You are a helpful assistant.<|eot_id|>" +
		"<|start_header_id|>user<|end_header_id|>\n\n" + examplePrompt(i) + "<|eot_id|>" +
		"<|start_header_id|>assistant<|end_header_id|>\n\n"
}
