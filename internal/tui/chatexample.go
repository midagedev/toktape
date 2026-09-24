package tui

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/midagedev/toktape/internal/tape"
)

// A scripted chat session for the look loop and the goldens (TTP-184).
//
// Like ExampleTape it is a fabricated session with the shape a real one has —
// an attach, a probe, four turns at a decode rate that sags a little as the
// context grows, a prefix cache that holds the conversation so far, a thinking
// model's monologue on the last turn, a Korean message — so a frame of any
// state the screen can be in is one call away without a server. Every instant
// is a clip time; ExampleChatAt(at) is the session as it stood at `at`.

// chatExampleTurn is one scripted exchange.
type chatExampleTurn struct {
	user      string
	answer    string
	reasoning string
	typeAt    time.Duration // the first key of the message
	ttft      time.Duration
	rate      float64 // decode tok/s
	promptN   int     // prompt tokens the server evaluated
	cacheN    int     // prompt tokens the prefix cache held
	// stopAt, when above zero, is the token ctrl+c lands after: the turn
	// ends cancelled with that much of its answer.
	stopAt int
}

var chatExampleTurns = []chatExampleTurn{
	{
		user:   "What's the fastest way to count the lines in a 40 GB log file?",
		answer: "Use `wc -l` — it streams the file and never holds it in memory, so the only limit is how fast the disk reads:\n\n```sh\nwc -l /var/log/big.log\n# 812345678 /var/log/big.log\n```\n\nOn an NVMe drive that is a few seconds per 10 GB. If the file is compressed, pipe it: `zstd -dc big.log.zst | wc -l`.",
		typeAt: 4 * time.Second, ttft: 210 * time.Millisecond, rate: 44.1, promptN: 58,
	},
	{
		user:   "Show me the same thing in Go, reading in chunks.",
		answer: "Count the newline bytes in fixed-size chunks; a 1 MiB buffer keeps the syscalls rare:\n\n```go\nfunc countLines(r io.Reader) (int, error) {\n\tbuf := make([]byte, 1<<20)\n\tn := 0\n\tfor {\n\t\tc, err := r.Read(buf)\n\t\tn += bytes.Count(buf[:c], []byte{'\\n'})\n\t\tif err == io.EOF {\n\t\t\treturn n, nil\n\t\t}\n\t\tif err != nil {\n\t\t\treturn n, err\n\t\t}\n\t}\n}\n```\n\nThis runs at memory bandwidth, so on a warm page cache it matches `wc -l`.",
		typeAt: 13 * time.Second, ttft: 95 * time.Millisecond, rate: 43.2, promptN: 21, cacheN: 196,
	},
	{
		user:   "고마워! 한국어로 짧게 요약해 줄래?",
		answer: "요약하면: 큰 파일의 줄 수는 `wc -l`로 세는 것이 가장 빠르고, Go에서는 1 MiB 버퍼로 읽으면서 줄바꿈 바이트만 세면 같은 속도가 나옵니다.",
		typeAt: 26 * time.Second, ttft: 88 * time.Millisecond, rate: 41.8, promptN: 30, cacheN: 489,
	},
	{
		user:      "Does the thinking of a reasoning model count toward tok/s?",
		reasoning: "The user asks whether reasoning tokens count in the decode rate. llama-server counts reasoning_content deltas in predicted_n, so they are decode tokens like any other, measured at the same speed.",
		answer:    "Yes. The server counts a thinking token exactly like an answer token, so the rate covers both — toktape shows the thought dim and folds it once the answer starts.",
		typeAt:    36 * time.Second, ttft: 102 * time.Millisecond, rate: 40.9, promptN: 24, cacheN: 612,
	},
}

// chatExampleMoreTurns carry the session on to twelve turns, for the frame the
// by-turn chart is judged on (look round 1, 2026-09-24): rates that drift down
// as the context fills, one slow turn, and one the user stopped.
var chatExampleMoreTurns = []chatExampleTurn{
	{
		user:   "And with ripgrep?",
		answer: "`rg -c ''` counts lines too, but on one big file `wc -l` is already at disk speed; rg only wins when you search as you count.",
		typeAt: 46 * time.Second, ttft: 90 * time.Millisecond, rate: 39.6, promptN: 14, cacheN: 745,
	},
	{
		user:   "What about only the lines that contain ERROR?",
		answer: "`grep -c -F ERROR big.log` prints how many lines match; `-F` takes the pattern literally and skips the regex engine.",
		typeAt: 53 * time.Second, ttft: 92 * time.Millisecond, rate: 38.2, promptN: 16, cacheN: 812,
	},
	{
		user:   "Can that run on every core?",
		answer: "Split on line boundaries and count each part: `parallel --pipepart -a big.log --block 1G grep -c ERROR | paste -sd+ | bc`. It scales until the disk is the limit.",
		typeAt: 60 * time.Second, ttft: 97 * time.Millisecond, rate: 36.9, promptN: 13, cacheN: 874,
	},
	{
		user:   "Explain how --pipepart finds the line boundaries, in detail.",
		answer: "GNU parallel opens the file once and computes a block size, then for every block it seeks to the block's nominal start offset and scans forward to the first newline after it, so that each worker receives whole lines only and nothing is counted twice.",
		typeAt: 67 * time.Second, ttft: 99 * time.Millisecond, rate: 36.1, promptN: 17, cacheN: 945, stopAt: 16,
	},
	{
		user:   "Short version please.",
		answer: "Each block starts at the next newline after its offset, so no line is cut in two.",
		typeAt: 73 * time.Second, ttft: 104 * time.Millisecond, rate: 34.8, promptN: 9, cacheN: 979,
	},
	{
		user:   "Would mmap beat read here?",
		answer: "Rarely. For one sequential pass the kernel's readahead already streams at disk speed; mmap mostly adds page faults.",
		typeAt: 79 * time.Second, ttft: 310 * time.Millisecond, rate: 21.4, promptN: 11, cacheN: 1004,
	},
	{
		user:   "Thanks, that's all.",
		answer: "You're welcome — the whole session is on the tape.",
		typeAt: 86 * time.Second, ttft: 101 * time.Millisecond, rate: 33.9, promptN: 8, cacheN: 1041,
	},
	{
		user:   "One last thing: which is fastest overall?",
		answer: "`wc -l` for a plain count, `grep -c -F` for a pattern, and `parallel --pipepart` once one core is the limit.",
		typeAt: 91 * time.Second, ttft: 103 * time.Millisecond, rate: 32.4, promptN: 12, cacheN: 1063,
	},
}

// chatExampleMarkdownTurn is an answer written in markdown, the way a small
// local model answers "as a list" (look round 2, 2026-09-24: a live Qwen3-1.7B
// answer showed "### Explanation:" and "- " verbatim): a heading, bullets with
// a nested one and a wrapped Korean one, a numbered list with a wrapped item,
// bold, inline code, and a code block whose "#" and "**" are code and must
// stay as written.
var chatExampleMarkdownTurn = chatExampleTurn{
	user:   "Sum it up as a list, please.",
	answer: "### Counting lines\n\nThree tools cover it:\n\n- `wc -l` for a plain count, at **disk speed**.\n- `grep -c -F` when only matching lines count.\n  - `-F` skips the regex engine.\n* 한국어 항목: 큰 파일은 한 번에 읽지 말고 1 MiB 버퍼로 나눠 읽으면 메모리를 거의 쓰지 않고 디스크 속도로 끝납니다.\n\n1. Try `wc -l` first.\n2. Reach for **parallel** only when one core is the limit: it splits the file by byte range, so each job reads its own part once.\n\n```sh\n# **not bold**: a comment\nwc -l big.log\n```\n\n**Bottom line:** `wc -l` is usually enough.",
	typeAt: 4 * time.Second, ttft: 120 * time.Millisecond, rate: 44.1, promptN: 40,
}

// chatExampleTwelveEnd is an instant after the twelfth turn has finished.
const chatExampleTwelveEnd = 97 * time.Second

// Instants of the example session worth a frame.
const (
	chatExampleOpened = 3 * time.Second
	// chatExampleTypeDur is how long a message takes to type, whatever its
	// length: the script is about the screen, not about typing speed.
	chatExampleTypeDur = 1500 * time.Millisecond
)

// chatExampleSentAt is when turn i's Enter is pressed.
func chatExampleSentAt(i int) time.Duration {
	return chatExampleTurns[i].typeAt + chatExampleTypeDur
}

// sentAt is when this turn's Enter is pressed.
func (tr chatExampleTurn) sentAt() time.Duration { return tr.typeAt + chatExampleTypeDur }

// tokenCount is how many tokens the turn streams before it ends.
func (tr chatExampleTurn) tokenCount() int {
	n := len(chatExampleTokens(tr.reasoning)) + len(chatExampleTokens(tr.answer))
	if tr.stopAt > 0 {
		n = min(n, tr.stopAt)
	}
	return n
}

// chatExampleTokens splits text into the pieces a tokenizer would stream: a
// word with the space before it, a newline on its own, and a long word in
// four-rune pieces.
func chatExampleTokens(text string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range text {
		switch {
		case r == '\n':
			flush()
			out = append(out, "\n")
		case r == ' ':
			flush()
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
			if utf8.RuneCountInString(cur.String()) >= 4 {
				flush()
			}
		}
	}
	flush()
	return out
}

// chatExampleStep is one scripted thing that happens at an instant.
type chatExampleStep struct {
	at time.Duration
	do func(ChatModel) ChatModel
}

// chatExampleOpts picks a variant of the example session.
type chatExampleOpts struct {
	// full adds a fifth message the server's context cannot take.
	full bool
	// many carries the session on to twelve turns instead.
	many bool
	// noHost is a box the host picture is not available on — macOS, or a
	// server that is not local: no GPU is listed and the samples carry no
	// figure at all (look round 2, 2026-09-24).
	noHost bool
	// markdown is a one-turn session whose answer is chatExampleMarkdownTurn.
	markdown bool
}

// chatExampleScript is the whole session, in time order.
func chatExampleScript(o chatExampleOpts) []chatExampleStep {
	full, many := o.full, o.many
	turns := chatExampleTurns
	sampleEnd := 60 * time.Second
	if many {
		turns = append(append([]chatExampleTurn(nil), chatExampleTurns...), chatExampleMoreTurns...)
		sampleEnd = chatExampleTwelveEnd
	}
	if o.markdown {
		turns = []chatExampleTurn{chatExampleMarkdownTurn}
	}
	ref := ExampleTapeN(1)
	sum := ref.Summary
	if sum.Server.CtxSize == 0 {
		sum.Server.CtxSize = 8192
	}
	if o.noHost {
		sum.Host.GPUs, sum.GPUsAtEnd = nil, nil
	}
	var steps []chatExampleStep
	add := func(at time.Duration, do func(ChatModel) ChatModel) {
		steps = append(steps, chatExampleStep{at: at, do: do})
	}
	observe := func(e Event) func(ChatModel) ChatModel {
		return func(m ChatModel) ChatModel { return m.Apply(ChatEvent{Kind: ChatObserved, T: e.T, Event: e}) }
	}
	key := func(k ChatKey) func(ChatModel) ChatModel {
		return func(m ChatModel) ChatModel {
			m, _ = m.HandleKey(k, 0, 120, 36)
			return m
		}
	}

	add(900*time.Millisecond, observe(Event{
		Kind: EventProps, T: 900 * time.Millisecond, Stream: -1,
		Server: sum.Server, Model: sum.Model, Host: sum.Host, Placement: sum.Placement, Limit: sum.Limit,
	}))
	add(1200*time.Millisecond, func(m ChatModel) ChatModel {
		return m.Apply(ChatEvent{Kind: ChatStatus, T: 1200 * time.Millisecond, Note: "measuring this server's prefill…"})
	})
	add(chatExampleOpened, func(m ChatModel) ChatModel { return m.Apply(ChatEvent{Kind: ChatOpened, T: chatExampleOpened}) })

	// Host samples every quarter second: the GPU works while a turn decodes
	// and idles between turns, the way a chat actually loads a box.
	var busy [][2]time.Duration
	for _, tr := range turns {
		start := tr.sentAt()
		n := tr.tokenCount()
		busy = append(busy, [2]time.Duration{start, start + tr.ttft + time.Duration(float64(n)/tr.rate*float64(time.Second))})
	}
	base := ref.Samples[0]
	cpu := 100.0
	for at := 500 * time.Millisecond; at <= sampleEnd; at += 250 * time.Millisecond {
		working := false
		for _, b := range busy {
			if at >= b[0] && at <= b[1] {
				working = true
			}
		}
		sm := base
		sm.T = at
		if o.noHost {
			// The sampler still ticks; it has nothing to read.
			add(at, observe(Event{Kind: EventSample, T: at, Stream: -1, Sample: tape.RunSample{T: at}}))
			continue
		}
		sm.GPUs = append([]tape.GPUSample(nil), base.GPUs...)
		util, cores := 4.0, 0.2
		if working {
			util, cores = 91+float64(int(at/(250*time.Millisecond))%5), 1.1
		}
		cpu += cores * 0.25
		sm.Mem.CPUSeconds = cpu
		for g := range sm.GPUs {
			sm.GPUs[g].UtilPct = util
		}
		add(at, observe(Event{Kind: EventSample, T: at, Stream: -1, Sample: sm}))
	}

	for i, tr := range turns {
		// Type the message a few runes at a time, then send it.
		runes := []rune(tr.user)
		const chunks = 6
		for c := 0; c < chunks; c++ {
			lo, hi := len(runes)*c/chunks, len(runes)*(c+1)/chunks
			part := append([]rune(nil), runes[lo:hi]...)
			add(tr.typeAt+time.Duration(c)*chatExampleTypeDur/chunks, key(ChatKey{Runes: part}))
		}
		sent := tr.sentAt()
		add(sent, func(m ChatModel) ChatModel {
			m, _ = m.HandleKey(ChatKey{Name: "enter"}, sent, 120, 36)
			return m
		})
		add(sent+5*time.Millisecond, observe(Event{Kind: EventStreamStart, T: sent + 5*time.Millisecond, Stream: 0, Round: i, MaxTokens: 2048}))

		var rec tape.RequestRecord
		rec.Index, rec.Round, rec.Slot = 0, i, 0
		k := 0
		emit := func(text string, reasoning bool) {
			for _, piece := range chatExampleTokens(text) {
				if tr.stopAt > 0 && k >= tr.stopAt {
					return
				}
				rel := tr.ttft + time.Duration(float64(k)/tr.rate*float64(time.Second))
				tk := tape.TokenEvent{T: rel, Index: k, Text: piece, Reasoning: reasoning}
				rec.Tokens = append(rec.Tokens, tk)
				add(sent+rel, observe(Event{Kind: EventToken, T: sent + rel, Stream: 0, Token: tk}))
				k++
			}
		}
		emit(tr.reasoning, true)
		emit(tr.answer, false)
		last := rec.Tokens[len(rec.Tokens)-1].T
		rec.Timings = tape.TimingsSummary{
			PromptN: tr.promptN, CacheN: tr.cacheN, PromptPerSecond: 850,
			PredictedN: k, PredictedPerSecond: tr.rate, TTFTMs: msOf(tr.ttft),
			ClientPredictedPerSecond: tr.rate * 0.99, ClientAgreesWithServer: true,
		}
		if tr.reasoning != "" {
			rec.Timings.ReasoningN = len(chatExampleTokens(tr.reasoning))
		}
		rec.Cache = tape.CacheSummary{HitTokens: tr.cacheN, PromptTotal: tr.cacheN + tr.promptN}
		if rec.Cache.PromptTotal > 0 {
			rec.Cache.HitRatio = float64(tr.cacheN) / float64(rec.Cache.PromptTotal)
		}
		done := sent + last + 20*time.Millisecond
		stopped := tr.stopAt > 0
		if stopped {
			// ctrl+c lands just after the last token; a server cut off
			// mid-answer reports no timings, so the rate is the client's.
			add(sent+last+5*time.Millisecond, key(ChatKey{Name: "ctrl+c"}))
			rec.Timings.PredictedPerSecond = 0
		}
		recCopy := rec
		add(done, func(m ChatModel) ChatModel {
			return m.Apply(ChatEvent{Kind: ChatTurnDone, T: done, Record: &recCopy, Cancelled: stopped})
		})
	}

	if full {
		at := 46 * time.Second
		for c, part := range []string{"Now paste the whole ", "40 GB file here and ", "summarise it."} {
			add(at+time.Duration(c)*200*time.Millisecond, key(ChatKey{Runes: []rune(part)}))
		}
		sent := at + time.Second
		add(sent, func(m ChatModel) ChatModel {
			m, _ = m.HandleKey(ChatKey{Name: "enter"}, sent, 120, 36)
			return m
		})
		add(sent+40*time.Millisecond, func(m ChatModel) ChatModel {
			return m.Apply(ChatEvent{Kind: ChatContextFull, T: sent + 40*time.Millisecond})
		})
	}
	// Stable by time; steps at one instant keep their script order.
	for i := 1; i < len(steps); i++ {
		for j := i; j > 0 && steps[j].at < steps[j-1].at; j-- {
			steps[j], steps[j-1] = steps[j-1], steps[j]
		}
	}
	return steps
}

// ExampleChatAt is the example session as it stood at clip time at. full
// scripts the context-full ending.
func ExampleChatAt(at time.Duration, th Theme, full bool) ChatModel {
	return exampleChatAt(at, th, chatExampleOpts{full: full})
}

// exampleChatAt is ExampleChatAt with every variant of the session a choice.
func exampleChatAt(at time.Duration, th Theme, o chatExampleOpts) ChatModel {
	m := NewChatModel(th)
	for _, s := range chatExampleScript(o) {
		if s.at > at {
			break
		}
		m = s.do(m)
	}
	return m
}

// ExampleChatState is one named frame of the example session.
type ExampleChatState struct {
	Name string
	At   time.Duration
	W, H int
	Full bool
	// Many carries the session on to twelve turns, one of them stopped.
	Many bool
	// NoHost is the session on a box with no host picture (macOS).
	NoHost bool
	// Markdown is the one-turn session whose answer is in markdown.
	Markdown bool
	// Keys are pressed after the script, at At: a help request, a scroll.
	Keys []ChatKey
}

// ExampleChatStates are the frames the look loop and the goldens take: every
// state the screen can be in, at the size the clip is recorded in, and the
// narrow layout.
func ExampleChatStates() []ExampleChatState {
	turn2 := chatExampleSentAt(1)
	return []ExampleChatState{
		{Name: "1-measuring", At: 1500 * time.Millisecond, W: 120, H: 36},
		{Name: "2-prefill", At: chatExampleSentAt(0) + 150*time.Millisecond, W: 120, H: 36},
		{Name: "3-code-streaming", At: turn2 + 95*time.Millisecond + 2600*time.Millisecond, W: 120, H: 36},
		{Name: "4-thinking", At: chatExampleSentAt(3) + 102*time.Millisecond + 700*time.Millisecond, W: 120, H: 36},
		{Name: "5-four-turns", At: 45 * time.Second, W: 120, H: 36,
			Keys: []ChatKey{{Runes: []rune("다음 질문은")}}},
		{Name: "6-context-full", At: 50 * time.Second, W: 120, H: 36, Full: true},
		{Name: "7-narrow-90", At: 45 * time.Second, W: 90, H: 32},
		{Name: "8-help", At: 45 * time.Second, W: 120, H: 36,
			Keys: []ChatKey{{Runes: []rune("/help")}, {Name: "enter"}, {Runes: []rune("/foo")}, {Name: "enter"}}},
		{Name: "9-scrolled", At: 45 * time.Second, W: 120, H: 36, Keys: []ChatKey{{Name: "pgup"}}},
		{Name: "10-twelve-turns", At: chatExampleTwelveEnd, W: 120, H: 36, Many: true},
		{Name: "11-twelve-narrow", At: chatExampleTwelveEnd, W: 90, H: 32, Many: true},
		{Name: "12-no-host", At: 45 * time.Second, W: 120, H: 36, NoHost: true},
		{Name: "13-markdown", At: chatExampleMarkdownEnd, W: 120, H: 36, Markdown: true},
		// Mid-answer, the write head on the wrapped Korean bullet's hang.
		{Name: "14-markdown-streaming", At: chatExampleMarkdownAt(78), W: 120, H: 36, Markdown: true},
		{Name: "15-markdown-narrow", At: chatExampleMarkdownEnd, W: 90, H: 32, Markdown: true},
	}
}

// chatExampleMarkdownEnd is an instant after the markdown turn has finished.
const chatExampleMarkdownEnd = 12 * time.Second

// chatExampleMarkdownAt is the instant just after the markdown turn's k-th
// token has landed.
func chatExampleMarkdownAt(k int) time.Duration {
	tr := chatExampleMarkdownTurn
	return tr.sentAt() + tr.ttft + time.Duration(float64(k)/tr.rate*float64(time.Second)) + 40*time.Millisecond
}

// Model builds the state's model under th.
func (s ExampleChatState) Model(th Theme) ChatModel {
	m := exampleChatAt(s.At, th, chatExampleOpts{full: s.Full, many: s.Many, noHost: s.NoHost, markdown: s.Markdown})
	for _, k := range s.Keys {
		m, _ = m.HandleKey(k, s.At, s.W, s.H)
	}
	return m
}
