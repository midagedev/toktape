package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/card"
)

// The chat screen (TTP-184, 2026-09-24).
//
//	┌─ toktape chat · model · engine · rig ───────────────shimmer──┐
//	│ ▌ what the user typed           │ THIS TURN ───────────────── │
//	│                                 │           42.7   (big face) │
//	│   the answer, as it streams▍    │ ttft · tokens · cache · ctx │
//	│   42.7 tok/s · ttft 250 ms      │ SESSION ─────────────────── │
//	│                                 │ turns · ▂▃▅▆ by turn        │
//	│                                 │ MACHINE ─────────────────── │
//	├─────────────────────────────────┴─────────────────────────────┤
//	│ › the next message▍                                           │
//	├───────────────────────────────────────────────────────────────┤
//	│ ◐ prefill 1.2 s                enter send · alt+enter newline │
//	└───────────────────────────────────────────────────────────────┘
//
// Under chatSplitWidth the right column goes and its figures fold into one
// status row under the transcript. The frame is the record screen's frame —
// its border, its shimmer, its section rules, its figures and its body-text
// ladder — so the two screens read as one tool.
//
// ChatView is a pure function of (m, t, w, h), like View: the spinner, the
// breathing cursors and the write head's glow move with t and nothing else.

const (
	// ChatMinWidth and ChatMinHeight are the smallest screen the chat lays
	// out for. They are below the record screen's MinWidth×MinHeight because
	// a chat is one column of text: at 60×20 the transcript, a status row and
	// the input still fit, where the record screen's tiles would not.
	ChatMinWidth  = 60
	ChatMinHeight = 20
	// chatSplitWidth is the width from which the figures get a column of
	// their own. Under it they fold into one status row.
	chatSplitWidth = 100
	// chatInputMax is the most rows the input grows to before it scrolls.
	chatInputMax = 6
	// chatChromeH is the rows the frame spends outside the body and the
	// input: the top border, the two dividers, the footer, the bottom border.
	chatChromeH = 5
)

// chatGeom is where everything goes on a w×h screen.
type chatGeom struct {
	split       bool
	inner       int
	leftW       int // the transcript column, gutters included
	paneW       int // the figures column, gutters included; 0 when folded
	cw          int // the transcript's text width
	inRows      int
	bodyH       int // rows between the top border and the first divider
	transH      int // of which the transcript takes this many
	statusInset bool
}

func chatLayout(m ChatModel, w, h int) chatGeom {
	g := chatGeom{inner: w - 2}
	g.split = w >= chatSplitWidth
	g.leftW = g.inner
	if g.split {
		g.paneW = rightWidth(w)
		g.leftW = g.inner - 1 - g.paneW
	}
	g.cw = g.leftW - 2
	g.inRows = inputHeight(m.Input, g.inner-2, chatInputMax)
	g.bodyH = h - chatChromeH - g.inRows
	g.transH = g.bodyH
	if !g.split {
		// The status row and the dim rule that parts it from the transcript.
		g.transH -= 2
		g.statusInset = true
	}
	return g
}

// ChatView renders one frame of the chat at clip time t into a w×h block:
// exactly h lines, each exactly w columns.
func ChatView(m ChatModel, t time.Duration, w, h int) string {
	if w < ChatMinWidth || h < ChatMinHeight {
		return chatTooSmall(w, h)
	}
	th := m.Machine.Theme
	g := chatLayout(m, w, h)
	bar := th.paint(th.dim, "│")

	trans := chatTranscriptView(m, th, t, g.cw, g.transH)
	var right []string
	if g.split {
		right = chatPane(m, th, t, g.paneW-2, g.bodyH)
	} else {
		trans = append(trans, chatStatusRule(th, g.cw), chatStatusLine(m, th, t, g.cw))
	}

	lines := make([]string, 0, h)
	lines = append(lines, titleBorder(th, t, g.inner, chatTitle(m, g.inner)))
	for i := 0; i < g.bodyH; i++ {
		if g.split {
			lines = append(lines, bar+" "+trans[i]+" "+bar+" "+right[i]+" "+bar)
		} else {
			lines = append(lines, bar+" "+trans[i]+" "+bar)
		}
	}
	divider := "├" + repeat('─', g.inner) + "┤"
	if g.split {
		lines = append(lines, th.paint(th.dim, "├"+repeat('─', g.leftW)+"┴"+repeat('─', g.paneW)+"┤"))
	} else {
		lines = append(lines, th.paint(th.dim, divider))
	}
	enabled := m.Phase != ChatMeasuring && m.Phase != ChatFailed
	for _, row := range inputView(th, t, m.Input, g.inner-2, g.inRows, enabled, chatPlaceholder(m)) {
		lines = append(lines, bar+" "+row+" "+bar)
	}
	lines = append(lines, th.paint(th.dim, divider))
	lines = append(lines, bar+" "+chatFooter(m, th, t, g.inner-2)+" "+bar)
	lines = append(lines, th.paint(th.dim, "└"+repeat('─', g.inner)+"┘"))
	return strings.Join(lines, "\n")
}

// chatTooSmall is tooSmall with the chat's own minimum in it.
func chatTooSmall(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	msg := fmt.Sprintf("toktape chat needs at least %d×%d", ChatMinWidth, ChatMinHeight)
	lines := make([]string, h)
	for i := range lines {
		if i == h/2 {
			lines[i] = pad(clip(center(msg, w), w), w)
		} else {
			lines[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(lines, "\n")
}

// chatTitle is the record screen's title with the verb in it: "toktape chat ·
// model · engine · rig", the model appearing when the attach does.
//
// Where the bar is too narrow for all of it, whole segments go from the end —
// the rig, then the engine — rather than the last one being cut mid-word into
// the border: a title that runs into its own corner reads as a rendering
// fault. The model is never dropped, only truncated, and the rule keeps at
// least chatTitleRule cells so the shimmer still has somewhere to travel.
func chatTitle(m ChatModel, inner int) []titleSeg {
	segs := titleSegments(m.Machine)
	out := make([]titleSeg, 0, len(segs)+1)
	out = append(out, segs[0], titleSeg{text: " chat", role: roleText})
	out = append(out, segs[1:]...)
	segW := func(ss []titleSeg) int {
		n := 2 // "─ "
		for _, s := range ss {
			n += width(s.text)
		}
		return n + 1
	}
	// Keep the brand, the verb and the model (brand, verb, sep, model).
	for len(out) > 4 && segW(out)+chatTitleRule > inner {
		out = out[:len(out)-2]
	}
	return out
}

// chatTitleRule is the fewest cells of rule the title bar keeps for its
// shimmer once segments start to go.
const chatTitleRule = 6

// chatPlaceholder is the dim text an empty input shows: what the input is
// waiting for, in the words of the state the session is in.
func chatPlaceholder(m ChatModel) string {
	switch m.Phase {
	case ChatMeasuring:
		return "waiting for the server…"
	case ChatPrefill, ChatStreaming:
		return "the next message can be typed while this one answers"
	case ChatFull:
		return "/exit saves the conversation"
	case ChatFailed:
		return ""
	}
	return "type a message"
}

// chatTranscriptView is the transcript cut to rows lines of exactly cw
// columns: the tail, lifted by m.Scroll.
func chatTranscriptView(m ChatModel, th Theme, t time.Duration, cw, rows int) []string {
	blank := strings.Repeat(" ", cw)
	if len(m.Items) == 0 {
		return chatEmptyState(m, th, t, cw, rows)
	}
	lines := chatTranscript(m, th, t, cw)
	scroll := min(max(0, m.Scroll), max(0, len(lines)-rows))
	end := len(lines) - scroll
	start := max(0, end-rows)
	out := fitRows(append([]string(nil), lines[start:end]...), blank, rows)
	if scroll > 0 && rows > 0 {
		// Scrolled back: the bottom row says so, and how far the live end is,
		// so a reader who paged up is never left wondering why nothing moves.
		msg := fmt.Sprintf(" ↓ %d more · pgdn ", scroll)
		l := newLine(th, cw)
		side := max(0, (cw-width(msg))/2)
		l.add(th.dim, repeat('─', side))
		l.add(th.text, msg)
		l.add(th.dim, repeat('─', l.left()))
		out[rows-1] = l.String()
	}
	return out
}

// chatEmptyState is the transcript before anything has been said: what the
// session is doing, in the middle of the column.
func chatEmptyState(m ChatModel, th Theme, t time.Duration, cw, rows int) []string {
	blank := strings.Repeat(" ", cw)
	type seg struct {
		st   style
		text string
	}
	var msg [][]seg
	model := card.ModelNameQuant(m.Machine.Summary.Model)
	switch m.Phase {
	case ChatMeasuring:
		if model != "" && model != unknown {
			msg = append(msg, []seg{{th.dim, "attached to "}, {th.text, model}}, nil)
		}
		// The recorder's own words, when it has said what it is measuring,
		// replace the generic ones; the line under them says why the wait.
		head := "measuring the server…"
		if m.MeasureNote != "" {
			head = m.MeasureNote
		}
		msg = append(msg,
			[]seg{{th.accent, spinnerAt(t)}, {th.text, " " + head}},
			[]seg{{th.dim, "a short probe runs once, before the first turn"}})
	case ChatFailed:
		msg = append(msg,
			[]seg{{th.bad, "✗ " + m.Err}},
			nil,
			[]seg{{th.dim, "enter or ctrl+c to leave"}})
	default:
		who := "the model"
		if model != "" && model != unknown {
			who = model
		}
		msg = append(msg,
			[]seg{{th.dim, "say something to "}, {th.text, who}},
			[]seg{{th.dim, "every answer is timed and recorded · /help lists the keys"}})
	}
	out := make([]string, 0, rows)
	top := max(0, (rows-len(msg))/2)
	for i := 0; i < top; i++ {
		out = append(out, blank)
	}
	for _, line := range msg {
		w := 0
		for _, s := range line {
			w += width(s.text)
		}
		l := newLine(th, cw)
		l.space(max(0, (cw-w)/2))
		for _, s := range line {
			l.addTrunc(s.st, s.text)
		}
		out = append(out, l.String())
	}
	return fitRows(out, blank, rows)
}

// chatTranscript is every line of the conversation, oldest first, each
// exactly cw columns.
func chatTranscript(m ChatModel, th Theme, t time.Duration, cw int) []string {
	blank := strings.Repeat(" ", cw)
	out := []string{blank}
	for i, it := range m.Items {
		if i > 0 {
			out = append(out, blank)
		}
		if it.Turn != nil {
			out = append(out, turnLines(th, t, it.Turn, cw)...)
			continue
		}
		out = append(out, noteLines(th, it, cw)...)
	}
	return out
}

// answerIndent is the columns an answer hangs in from the user's bar, so the
// two voices read as two columns of one conversation.
const answerIndent = 2

// turnLines draws one exchange: the user's message on its bar, the answer
// under it, and — once it is done — the turn's figures in one line.
func turnLines(th Theme, t time.Duration, tr *ChatTurn, cw int) []string {
	var out []string
	for _, line := range wrap(tr.User, cw-2) {
		l := newLine(th, cw)
		l.add(th.accentMuted, "▌")
		l.space(1)
		l.add(th.text, line)
		out = append(out, l.String())
	}
	out = append(out, strings.Repeat(" ", cw))

	s := tr.Answer
	live := !s.Done
	if len(s.Tokens) == 0 {
		if live {
			out = append(out, prefillRow(th, t, tr, cw))
		}
	} else {
		out = append(out, answerLines(th, t, tr, cw)...)
	}
	if s.Err != "" {
		l := newLine(th, cw)
		l.space(answerIndent)
		l.addTrunc(th.bad, "✗ "+s.Err)
		out = append(out, l.String())
	} else if !live {
		out = append(out, turnMetaLine(th, tr, cw))
	}
	return out
}

// prefillRow is the answer's place while the server has not produced a token:
// the spinner, and how long it has been waiting.
func prefillRow(th Theme, t time.Duration, tr *ChatTurn, cw int) string {
	l := newLine(th, cw)
	l.space(answerIndent)
	l.add(th.accent, spinnerAt(t))
	if tr.Stopping {
		l.add(th.dim, " stopping…")
		return l.String()
	}
	l.add(th.dim, " "+prefillWord)
	if wait := t - tr.SentAt; wait > 0 {
		l.add(th.dim, " "+fmtMs(msOf(wait)))
	}
	return l.String()
}

// answerLines draws the answer's text with the record screen's body ladder.
//
// A thought that has been followed by an answer folds to one line — "▸ thought
// · 212 tok" — because the reader asked a question and the monologue in front
// of the reply is the part they scroll past. A thought still running, or one
// that never reached an answer, stays open, dim: it is the only thing on screen
// that says the model is working.
func answerLines(th Theme, t time.Duration, tr *ChatTurn, cw int) []string {
	s := tr.Answer
	aw := cw - answerIndent
	var out []string
	thought, answered := 0, false
	for _, tk := range s.Tokens {
		if tk.Reasoning {
			thought++
		} else {
			answered = true
		}
	}
	if thought > 0 && answered {
		l := newLine(th, cw)
		l.space(answerIndent)
		l.add(th.dim, fmt.Sprintf("▸ thought · %d tok", thought))
		out = append(out, l.String())
		s = answerOnly(s)
	} else if thought > 0 && !s.Done {
		l := newLine(th, cw)
		l.space(answerIndent)
		l.add(th.accent, spinnerAt(t))
		l.add(th.dim, fmt.Sprintf(" thinking · %d tok", thought))
		out = append(out, l.String())
	}
	// Two columns held back, as streamBody does, so the cursor never forces a
	// re-wrap when it appears at the end of the last line.
	// Read as markdown (chatmd.go): headings, bullets and bold draw as what
	// they mean, not as the characters that mark them.
	md := newChatMD(s)
	lines := streamTextLinesWith(s, aw-2, md.wrap)
	bands := bodyBands(s, lines, t)
	lastIsText := false
	inFence := false
	for i, bl := range lines {
		l := newLine(th, cw)
		l.space(answerIndent)
		if isFenceRow(bl, bands[i]) {
			// A fence is markup, not text the reader wants: the opening one
			// becomes its language, dim, and the closing one goes — the blank
			// row after the block already ends it (look round 1, 2026-09-24).
			// The record screen keeps its fences; its tiles are not this.
			inFence = !inFence
			if !inFence {
				continue
			}
			lang, _ := fenceLine(bl.text)
			if lang == "" {
				lang = "code"
			}
			l.add(th.dim, lang)
		} else {
			md.paintBody(l, th, bl, bands[i])
		}
		out = append(out, l.String())
		lastIsText = !bl.marker
	}
	if !s.Done && lastIsText && len(out) > 0 {
		last := out[len(out)-1]
		if strings.TrimSpace(card.StripANSI(last)) == "" {
			// A line the answer has only just started — the newline between a
			// list and what follows it — holds the cursor at the answer's edge,
			// not at the user's bar where trimming the line would leave it
			// (look round 2, 2026-09-24).
			out[len(out)-1] = strings.Repeat(" ", answerIndent) + appendCursor(th, t, "", cw-answerIndent, true)
		} else {
			out[len(out)-1] = appendCursor(th, t, last, cw, true)
		}
	}
	return out
}

// isFenceRow says whether a drawn body line is a code fence of the answer: a
// line the fence classifier marked whole, not a ``` inside a thought or inside
// a code block's own text.
func isFenceRow(bl bodyLine, segs []bodySeg) bool {
	if bl.marker || bl.reasoning || len(segs) == 0 {
		return false
	}
	if _, ok := fenceLine(bl.text); !ok {
		return false
	}
	for _, sg := range segs {
		if sg.class != classFence && strings.TrimSpace(sg.text) != "" {
			return false
		}
	}
	return true
}

// answerOnly is s with its reasoning tokens left out, for a turn whose thought
// is folded. The bands and the code classes are computed over what is drawn,
// so they stay aligned with it.
func answerOnly(s Stream) Stream {
	out := s
	out.Tokens = make([]Token, 0, len(s.Tokens))
	var b strings.Builder
	for _, tk := range s.Tokens {
		if !tk.Reasoning {
			out.Tokens = append(out.Tokens, tk)
			b.WriteString(tk.Text)
		}
	}
	out.Text = b.String()
	return out
}

// turnMetaLine is a finished turn's figures, dim, under its answer: the rate
// first, as the one figure of the turn worth reading.
func turnMetaLine(th Theme, tr *ChatTurn, cw int) string {
	f := turnFigures(tr, 0)
	l := newLine(th, cw)
	l.space(answerIndent)
	if tr.Cancelled {
		l.add(th.warn, "stopped")
		l.add(th.dim, " · "+f.tokens+" tok")
		return l.String()
	}
	l.add(th.accent, f.rate)
	l.add(th.dim, " tok/s")
	parts := []string{"ttft " + f.ttft, f.tokens + " tok"}
	if f.cache != "" {
		parts = append(parts, "cache "+f.cache)
	}
	for _, p := range parts {
		if l.left() < width(p)+3 {
			break
		}
		l.add(th.dim, " · "+p)
	}
	return l.String()
}

// noteLines draws a notice: toktape speaking, not the model, so it wears the
// chrome's dim and a glyph of its own rather than either voice's column.
func noteLines(th Theme, it ChatItem, cw int) []string {
	if it.NoteOf == NoteHelp {
		var out []string
		for _, kv := range chatHelpLines {
			l := newLine(th, cw)
			l.space(answerIndent)
			l.add(th.text, pad(kv[0], 14))
			l.addTrunc(th.dim, kv[1])
			out = append(out, l.String())
		}
		return out
	}
	glyph, st := "·", th.dim
	switch it.NoteOf {
	case NoteFull:
		glyph, st = "●", th.warn
	case NoteError:
		glyph, st = "✗", th.bad
	}
	var out []string
	for i, line := range wrap(it.Note, cw-answerIndent-2) {
		l := newLine(th, cw)
		l.space(answerIndent)
		if i == 0 {
			l.add(st, glyph)
		} else {
			l.space(width(glyph))
		}
		l.space(1)
		l.add(th.dim, line)
		out = append(out, l.String())
	}
	return out
}

// turnFigs are one turn's figures, formatted, "?" where unobserved.
type turnFigs struct {
	rate, ttft, tokens, prompt, cache, ctx string
	rateV                                  float64
	live                                   bool
}

// turnFigures reads a turn's figures: the recorder's once the turn is done,
// the screen's own while it streams. Server figures are the record, the client
// ones the check (CLAUDE.md), so a finished turn shows the server's rate when
// the server reported one.
func turnFigures(tr *ChatTurn, ctxSize int) turnFigs {
	s := tr.Answer
	f := turnFigs{rate: unknown, ttft: unknown, prompt: unknown, live: !s.Done}
	f.tokens = strconv.Itoa(len(s.Tokens))
	if len(s.Tokens) > 0 {
		f.ttft = fmtMs(msOf(s.Tokens[0].T - tr.SentAt))
	}
	f.rateV = streamRate(s)
	if rec := tr.Record; rec != nil {
		tm := rec.Timings
		switch {
		case tm.PredictedPerSecond > 0:
			f.rateV = tm.PredictedPerSecond
		case tm.ClientPredictedPerSecond > 0:
			f.rateV = tm.ClientPredictedPerSecond
		}
		if tm.TTFTMs > 0 {
			f.ttft = fmtMs(tm.TTFTMs)
		}
		if tm.PredictedN > 0 {
			f.tokens = strconv.Itoa(tm.PredictedN)
		}
		if tm.PromptPerSecond > 0 {
			f.prompt = fmtRate(tm.PromptPerSecond) + " tok/s"
		}
		if c := rec.Cache; c.PromptTotal > 0 {
			f.cache = fmtPct(float64(c.HitTokens)/float64(c.PromptTotal)) + " of " + fmtCount(c.PromptTotal)
		}
		used := tm.PromptN + tm.CacheN + tm.PredictedN
		if used > 0 {
			f.ctx = fmtCount(used) + " / " + orUnknown(countOrEmpty(ctxSize))
		}
	}
	f.rate = fmtRate(f.rateV)
	return f
}

// fmtCount is a token count for a narrow column: 812, 1.8k, 32k.
func fmtCount(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10000:
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
	}
	return strconv.Itoa((n+500)/1000) + "k"
}

func countOrEmpty(n int) string {
	if n <= 0 {
		return ""
	}
	return fmtCount(n)
}

// currentTurn is the turn the figures describe: the one in flight, else the
// last one that finished. nil before the first.
func (m ChatModel) currentTurn() *ChatTurn {
	turns := m.Turns()
	if len(turns) == 0 {
		return nil
	}
	return turns[len(turns)-1]
}

// sessionRates is the decode rate of every finished, uncancelled turn, oldest
// first: the series the per-turn sparkline draws.
func (m ChatModel) sessionRates() []float64 {
	var out []float64
	for _, tr := range m.Turns() {
		if !tr.Answer.Done || tr.Cancelled || tr.Answer.Err != "" {
			continue
		}
		if f := turnFigures(tr, 0); f.rateV > 0 {
			out = append(out, f.rateV)
		}
	}
	return out
}

// turnBar is one finished turn's column in the by-turn chart: its decode
// rate (0 when none was observed) and whether ctrl+c cut it short.
type turnBar struct {
	rate    float64
	stopped bool
}

// sessionBars is every finished turn, oldest first, for the by-turn chart.
// Unlike sessionRates it keeps the stopped and the failed turns, so the chart
// has one column per turn the reader can count in the transcript; a failed
// turn has no rate and draws as an empty column.
func (m ChatModel) sessionBars() []turnBar {
	var out []turnBar
	for _, tr := range m.Turns() {
		if !tr.Answer.Done {
			continue
		}
		b := turnBar{stopped: tr.Cancelled}
		if tr.Answer.Err == "" {
			b.rate = turnFigures(tr, 0).rateV
		}
		out = append(out, b)
	}
	return out
}

// turnBarMax is the widest one turn's bar gets. Four turns fill the pane's
// value area; wider, one or two turns drew as a slab that read as a single
// measure rather than a series.
const turnBarMax = 4

// chatStatusBarsMax is the most cells the folded status row spends on the
// chart, so the figures after it keep their room.
const chatStatusBarsMax = 16

// turnBarGeom is how n bars sit in w cells: each bw wide, gap cells apart,
// and how many of them (the most recent) fit. The gap is kept even when it
// costs the oldest turns their column: bars that touch merge back into the
// one block this chart replaced.
func turnBarGeom(n, w int) (bw, gap, k int) {
	if n <= 0 || w <= 0 {
		return 0, 0, 0
	}
	if w > 1 {
		gap = 1
	}
	bw = min(max(1, (w+gap)/n-gap), turnBarMax)
	k = min(n, (w+gap)/(bw+gap))
	return bw, gap, k
}

// turnBarsWidth is the cells n bars take when given at most w.
func turnBarsWidth(n, w int) int {
	bw, gap, k := turnBarGeom(n, w)
	if k == 0 {
		return 0
	}
	return k*bw + (k-1)*gap
}

// turnBars draws the by-turn chart right-aligned in w cells of l: one bar per
// turn, newest at the right, the most recent that fit. A bar's height is its
// decode rate over the tallest shown, from zero, so the heights compare
// honestly; any answered turn is at least the lowest step so a slow one is
// still there. A stopped turn is drawn dim, and a turn with no rate is an
// empty column rather than a bar pretending to be a zero (2026-09-24, look
// round 1: four turns drew as one solid block through Sparkline, which is a
// per-sample line with no room between samples).
func turnBars(l *lineBuf, th Theme, bars []turnBar, w int) {
	w = min(w, l.left())
	bw, gap, k := turnBarGeom(len(bars), w)
	if k == 0 {
		l.space(max(0, w))
		return
	}
	bars = bars[len(bars)-k:]
	top := 0.0
	for _, b := range bars {
		top = max(top, b.rate)
	}
	l.space(w - (k*bw + (k-1)*gap))
	for i, b := range bars {
		if i > 0 {
			l.space(gap)
		}
		if b.rate <= 0 || top <= 0 {
			l.space(bw)
			continue
		}
		lv := int(math.Round(b.rate / top * float64(len(sparkRunes))))
		lv = min(max(lv, 1), len(sparkRunes))
		st := th.accentMuted
		if b.stopped {
			st = th.dim
		}
		l.add(st, strings.Repeat(string(sparkRunes[lv-1]), bw))
	}
}

// sessionTokens is every generated token of the session, finished turns by the
// recorder's count.
func (m ChatModel) sessionTokens() int {
	n := 0
	for _, tr := range m.Turns() {
		c, _ := strconv.Atoi(turnFigures(tr, 0).tokens)
		n += c
	}
	return n
}

// chatPane is the figures column: this turn, the session, the machine.
func chatPane(m ChatModel, th Theme, t time.Duration, cw, rows int) []string {
	for _, big := range []bool{true, false} {
		for _, gh := range resourceHeights {
			out := buildChatPane(m, th, t, cw, big, gh)
			if len(out) <= rows {
				return fitRows(out, strings.Repeat(" ", cw), rows)
			}
		}
	}
	return fitRows(buildChatPane(m, th, t, cw, false, 0), strings.Repeat(" ", cw), rows)
}

func buildChatPane(m ChatModel, th Theme, t time.Duration, cw int, big bool, graphH int) []string {
	var out []string
	blank := strings.Repeat(" ", cw)
	section := func(title, tag string) {
		if len(out) > 0 {
			out = append(out, blank)
		}
		l := newLine(th, cw)
		l.add(th.dim, title)
		l.space(1)
		if tag == "" {
			l.add(th.dim, repeat('─', l.left()))
		} else {
			l.add(th.dim, repeat('─', l.left()-width(tag)-1))
			l.space(1)
			l.add(th.dim, tag)
		}
		out = append(out, l.String())
	}

	tr := m.currentTurn()
	f := turnFigs{rate: unknown, ttft: unknown, tokens: unknown, prompt: unknown}
	tag := ""
	if tr != nil {
		f = turnFigures(tr, m.Machine.Summary.Server.CtxSize)
		tag = fmt.Sprintf("#%d", len(m.Turns()))
		if f.live {
			tag += " live"
		}
	}
	section("THIS TURN", tag)
	fig := "?"
	if f.rateV > 0 {
		fig = strconv.FormatFloat(f.rateV, 'f', 1, 64)
	}
	if big && tr != nil && bigFigureWidth(fig) <= cw {
		for _, row := range bigFigure(fig) {
			l := newLine(th, cw)
			l.space(cw - width(row))
			l.add(th.accentBold, row)
			out = append(out, l.String())
		}
		unit := "decode tok/s"
		out = append(out, newLine(th, cw).space(cw-width(unit)).add(th.dim, unit).String())
	} else {
		l := newLine(th, cw)
		l.add(th.dim, "decode")
		val := f.rate + " tok/s"
		l.gapTo(width(val))
		l.add(th.accentBold, f.rate)
		l.add(th.dim, " tok/s")
		out = append(out, l.String())
	}
	out = append(out,
		kvRow(th, cw, "ttft", f.ttft, th.text),
		kvRow(th, cw, "tokens", f.tokens, th.text),
		kvRow(th, cw, "prefill", f.prompt, th.text),
		kvRow(th, cw, "cache reuse", orUnknown(f.cache), th.text),
		kvRow(th, cw, "context", orUnknown(f.ctx), th.text),
	)

	section("SESSION", "")
	rates := m.sessionRates()
	// One pair per row, as THIS TURN's (2026-09-24, look round 1: the two
	// pairs shared a row and "tok" floated between them).
	out = append(out,
		kvRow(th, cw, "turns", strconv.Itoa(len(m.Turns())), th.text),
		kvRow(th, cw, "tokens", fmtCount(m.sessionTokens()), th.text),
	)
	// Until a turn has finished there is no bar to draw, and a label with
	// nothing after it reads as a row that failed to render: it shows the
	// unknown mark where the first bar will stand, so the row is there from
	// the start and nothing moves when the bar comes (look round 3, 2026-09-24).
	if bars := m.sessionBars(); len(bars) == 0 {
		out = append(out, kvRow(th, cw, "by turn", unknown, th.dim))
	} else {
		l := newLine(th, cw)
		l.add(th.dim, "by turn")
		l.space(1)
		turnBars(l, th, bars, l.left())
		out = append(out, l.String())
	}
	mean := unknown
	if len(rates) > 0 {
		sum := 0.0
		for _, r := range rates {
			sum += r
		}
		mean = fmtRate(sum/float64(len(rates))) + " tok/s"
	}
	out = append(out, kvRow(th, cw, "mean", mean, th.text))

	if m.Attached || len(m.Machine.Samples) > 0 {
		section("MACHINE", "")
		if note := hostUnobserved(m.Machine); note != "" {
			out = append(out, newLine(th, cw).addTrunc(th.dim, note).String())
		} else {
			out = append(out, resourceRows(m.Machine, th, t, cw, graphH)...)
		}
	}
	return out
}

// hostUnobserved says why the MACHINE block has nothing to show, or "" when
// it has something (look round 2, 2026-09-24).
//
// Where the host picture is not available — macOS has no /proc and no GPU
// telemetry, a remote server has no local process — the block drew "CPU  ?  ?
// cores  load ?" over an empty column: three unknowns that each look like a
// reading that failed. When not one host figure has been observed, the block
// is its header and one dim line saying why, in the card's words for the same
// fact (card.CodeNoProcView: "no /proc view of the server"). One figure is
// enough to keep the rows, with "?" only where a figure is missing: the
// honesty of each "?" is not in question, only a block made of nothing else.
//
// Before the first turn nothing has been sampled yet on any box — the sampler
// starts with the first turn — so the line says when the reading comes rather
// than that it never will.
func hostUnobserved(m Model) string {
	if len(m.Summary.Host.GPUs) > 0 || len(m.Summary.GPUsAtEnd) > 0 {
		return ""
	}
	for _, sm := range m.Samples {
		if sm.Mem.CPUSeconds > 0 || sm.Mem.RSSBytes > 0 || sm.LoadAvg1 > 0 || len(sm.GPUs) > 0 {
			return ""
		}
	}
	if len(m.Samples) == 0 {
		return "read while a turn runs"
	}
	return "no /proc view of the server"
}

// chatStatusRule parts the folded status row from the transcript above it: a
// dim rule from the answers' edge, the same thin line the wide layout's column
// divider is, so the figures read as their own region and not as one more
// line of the last answer (look round 1, 2026-09-24).
func chatStatusRule(th Theme, cw int) string {
	l := newLine(th, cw)
	l.space(answerIndent)
	l.add(th.dim, repeat('─', l.left()))
	return l.String()
}

// chatStatusLine is the figures column folded into one row, for a screen too
// narrow for the column. While a turn is in flight it is that turn — the live
// rate, its latency, its size; between turns the last turn's figures are
// already under its answer, so the row says what the transcript does not: the
// session's shape and how much of the context it has used. Parts are dropped
// whole from the right as the room runs out, never cut.
func chatStatusLine(m ChatModel, th Theme, t time.Duration, cw int) string {
	l := newLine(th, cw)
	// It hangs from the answers' edge, not the user's bar: it is figures, and
	// figures in the transcript are the answers' (look round 1, 2026-09-24).
	l.space(answerIndent)
	tr := m.currentTurn()
	if tr == nil {
		l.addTrunc(th.dim, "no turns yet · the figures of each answer appear under it")
		return l.String()
	}
	type part struct {
		label, value string
	}
	put := func(parts []part) {
		for _, p := range parts {
			if l.left() < width(p.label)+width(p.value)+3 {
				return
			}
			l.add(th.dim, " · "+p.label)
			l.add(th.text, p.value)
		}
	}
	f := turnFigures(tr, m.Machine.Summary.Server.CtxSize)
	if f.live {
		l.add(th.accentBold, f.rate)
		l.add(th.dim, " tok/s")
		put([]part{{"ttft ", f.ttft}, {"", f.tokens + " tok"}})
		return l.String()
	}
	rates := m.sessionRates()
	n := len(m.Turns())
	l.add(th.text, strconv.Itoa(n))
	l.add(th.dim, plural(n, " turn", " turns"))
	if bars := m.sessionBars(); len(bars) > 0 && l.left() > 6 {
		l.space(1)
		turnBars(l, th, bars, min(l.left()-1, turnBarsWidth(len(bars), chatStatusBarsMax)))
	}
	if len(rates) > 0 {
		sum := 0.0
		for _, r := range rates {
			sum += r
		}
		put([]part{{"mean ", fmtRate(sum/float64(len(rates))) + " tok/s"}})
	}
	parts := []part{{"", fmtCount(m.sessionTokens()) + " tok"}}
	if f.ctx != "" {
		parts = append(parts, part{"context ", f.ctx})
	}
	put(parts)
	return l.String()
}

// plural is one when n is 1 and many otherwise: "1 turn", "2 turns".
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// chatFooter is the session's state on the left and the keys that act on it
// on the right, the longest key list that fits.
func chatFooter(m ChatModel, th Theme, t time.Duration, w int) string {
	l := newLine(th, w)
	switch m.Phase {
	case ChatMeasuring:
		l.add(th.accent, spinnerAt(t))
		l.add(th.dim, " measuring the server")
	case ChatPrefill:
		l.add(th.accent, spinnerAt(t))
		if tr := m.liveTurn(); tr != nil && tr.Stopping {
			l.add(th.dim, " stopping")
		} else {
			l.add(th.dim, " "+prefillWord)
		}
	case ChatStreaming:
		l.add(th.accent, spinnerAt(t))
		if tr := m.liveTurn(); tr != nil && tr.Stopping {
			l.add(th.dim, " stopping")
		} else if tr != nil {
			l.add(th.dim, fmt.Sprintf(" answering · %d tok", len(tr.Answer.Tokens)))
		}
	case ChatFull:
		l.add(th.warn, "●")
		l.add(th.dim, " context full")
	case ChatFailed:
		l.add(th.bad, "✗")
		l.add(th.dim, " not attached")
	default:
		l.add(th.accentMuted, "●")
		l.add(th.dim, " ready")
	}
	var cands []string
	switch m.Phase {
	case ChatMeasuring:
		cands = []string{"ctrl+c leave"}
	case ChatPrefill, ChatStreaming:
		cands = []string{"ctrl+c stop · pgup/pgdn scroll", "ctrl+c stop"}
	case ChatFull:
		cands = []string{"/exit save and leave · pgup/pgdn scroll", "/exit save and leave"}
	case ChatFailed:
		cands = []string{"enter leave"}
	default:
		cands = []string{
			"enter send · alt+enter newline · pgup/pgdn scroll · /help · ctrl+c leave",
			"enter send · alt+enter newline · /help · ctrl+c leave",
			"enter send · alt+enter newline · /help",
			"enter send · /help",
		}
	}
	for _, c := range cands {
		if width(c)+2 <= l.left() {
			l.gapTo(width(c))
			l.add(th.dim, c)
			break
		}
	}
	return l.String()
}

// chatPage is how far one pgup moves: a screen of transcript, less two rows
// kept for context.
func chatPage(m ChatModel, w, h int) int {
	if w < ChatMinWidth || h < ChatMinHeight {
		return 1
	}
	return max(1, chatLayout(m, w, h).transH-2)
}

// chatMaxScroll is how far the transcript can be lifted before its first row
// reaches the top of the screen.
func chatMaxScroll(m ChatModel, t time.Duration, w, h int) int {
	if w < ChatMinWidth || h < ChatMinHeight || len(m.Items) == 0 {
		return 0
	}
	g := chatLayout(m, w, h)
	return max(0, len(chatTranscript(m, m.Machine.Theme, t, g.cw))-g.transH)
}
