package tui

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// This file is the second row of every tile: the figure the tile exists to
// report.
//
// A tile used to carry its rate as a small dim number at the right of its
// header, beside the stream's name, and its p50 at the far end of the
// sparkline footer. Both were legible and neither was first (user,
// 2026-09-13: "각 pane마다 핵심적으로 tok/s가 표시가 안 되는데, 표시해야 될 지표에
// 대해서 좀 잘 생각해 보자"). The header is now identity and state, and the rate
// has a row of its own directly under it, in the accent and in bold, with the
// figures that qualify it dim and behind it.
//
// The stream's own rate graph joined them there (TTP-29, user 2026-09-13:
// "팬의 스파크와 실제 스탯이 상하로 분리되어서 보기 힘든데"). The number and the
// shape are the same measurement, so they are read together or not at all:
//
//	12.1 tok/s ▂▃▃▂▃▂▃▂ ttft 250 ms · 80/128
//
// The stat line exists from t = 0, before there is a rate to print, so that
// the first decoded token changes a figure and not the layout.

// tileRateW is the field the decode rate is padded to, unit included. Every
// tile on a page is a different width, but the dim qualifiers all start at the
// same column inside their tile, which is what makes a page of them scan as a
// table rather than as four sentences.
//
// Eleven columns is the widest rate the formatter can produce ("2787 tok/s",
// ten) plus the space that separates it from what follows.
const tileRateW = 11

// The sparkline's geometry (TTP-29, user 2026-09-13: "한 라인 다 먹기에는 토큰
// 오르락내리락이 너무 적어서 별 의미 없는 듯하니 반 줄 정도로 줄이자").
//
// tileSparkMax caps the graph at two dozen columns: past that a decode rate
// that moves by a token or two per second is a wide flat band, which is what
// the full-width footer looked like. tileSparkMin is the width below which it
// stops being a line at all — under eight cells the scroll is a flicker — so a
// tile that cannot spare eight draws none.
const (
	tileSparkMax = 24
	tileSparkMin = 8
)

// tileSparkW is how many cells of sparkline a cw-column tile may spend: half
// the tile, less the rate field it follows and the space before the figures
// after it, capped at tileSparkMax. Zero means the tile has no room for a
// graph the reader could read.
func tileSparkW(cw int) int {
	w := cw/2 - tileRateW - 1
	if w > tileSparkMax {
		w = tileSparkMax
	}
	if w < tileSparkMin {
		return 0
	}
	return w
}

// minP50Intervals is how many inter-token gaps a stream must have before its
// median is printed. Under it the figure would move every frame and describe
// nothing; "?" is the honest reading (CLAUDE.md: an unobserved value prints
// "?", never a plausible default).
const minP50Intervals = 8

// tileStatLine is the tile's hero row: this stream's decode rate, the last few
// seconds of it as a graph, then the figures that qualify it.
//
// It always returns exactly cw columns. What it gives up as the tile narrows,
// in order: the median, the token count, the graph, then the TTFT. The rate
// itself never goes — a tile without it is not reporting anything — and
// nothing is ever half-printed, because a clipped number reads as a wrong
// number. The graph outlives the count and dies before the TTFT: a shape says
// more than a third figure and less than a measurement.
//
// The rate is bold accent on every tile, active or not. Painting the inactive
// ones plain would make the hero figure dimmest on the seven tiles a reader
// scans and brightest only on the one that happens to be talking; the
// breathing cursor already says which that is.
func tileStatLine(m Model, th Theme, t time.Duration, s Stream, cw int) string {
	if cw <= 0 {
		return ""
	}
	l := newLine(th, cw)
	rate := streamRateFigure(s) + " tok/s"
	if width(rate) > cw {
		// No room for the unit. The bare figure still says something, but a
		// clipped one says something wrong, so a tile too narrow even for
		// that prints nothing.
		if fig := streamRateFigure(s); width(fig) <= cw {
			l.addRaw(th.accentBold, fig)
		}
		return l.String()
	}
	l.addRaw(th.accentBold, rate)
	l.space(tileRateW - width(rate))
	for _, tail := range statTails(m, t, s, tileSparkW(cw)) {
		if tail.reserve > l.left() {
			continue
		}
		if tail.spark > 0 {
			writeTileSpark(l, th, s, tail.spark)
			l.space(1)
		}
		l.addRaw(th.dim, tail.text)
		break
	}
	return l.String()
}

// writeTileSpark draws this stream's last w instantaneous decode rates as a
// sparkline, with only the newest cell lit.
//
// The graph is anchored to its right-hand end: a stream with fewer samples
// than cells pads on the left, so the line scrolls under a fixed edge instead
// of growing out of the rate beside it.
//
// One bright cell, and it is the last one (TTP-29, user 2026-09-13:
// "스파크라인도 마지막 것만 밝게 하고 이전 것은 어둡게 하고"). The rest wear the
// muted accent the emphasis contract gives every shape (TTP-28): a whole line
// in the full accent was the loudest thing in the tile. The lit cell is not a
// second reading of the glow on the body text — it is the write head of the
// graph, which is why it is a fixed column rather than a fading tail.
func writeTileSpark(l *lineBuf, th Theme, s Stream, w int) {
	cells := Sparkline(streamRates(s, w), w, 0)
	l.space(w - len(cells))
	if len(cells) == 0 {
		// No gap to measure yet. The cells stay blank rather than drawing a
		// flat line at zero, which would be a rate nobody observed
		// (CLAUDE.md).
		return
	}
	older, newest := cells[:len(cells)-1], cells[len(cells)-1:]
	writeCells(l, th, older, cellPalette{base: th.accentMuted, warn: th.accentMuted, bad: th.accentMuted})
	writeCells(l, th, newest, cellPalette{base: th.accent, warn: th.accent, bad: th.accent})
}

// statTail is one candidate for the dim half of the stat line: the text to
// draw, and the width the fit is decided against.
//
// The two differ because a figure grows as the run goes on — "ttft ?" becomes
// "ttft 250 ms", "4/128" becomes "80/128", "p50 ?" becomes "p50 86 ms" — and a
// tail chosen against what is printed now would fit at half a second and stop
// fitting at three seconds. The reader would see a figure appear and then
// vanish, which is worse than never having shown it. Every part is measured
// against the widest shape it will grow into, so the stat line settles on its
// layout in the first frame and keeps it.
type statTail struct {
	text    string
	reserve int
	// spark is how many cells of sparkline this candidate draws before its
	// text, or zero for the candidates that have given the graph up. reserve
	// already counts it and the space after it.
	spark int
}

// The widths the growing parts are reserved against: a millisecond figure of
// three digits, which is what a healthy TTFT and a healthy median both are,
// and a token count with as many digits as the budget it is counting towards.
const (
	ttftNominalW = 11 // "ttft 250 ms"
	p50NominalW  = 10 // "p50 250 ms"
)

// statTails is everything the stat line may carry after the rate, widest
// first: the graph of sw cells and the dim qualifiers behind it. The last
// entry is empty, so a tile with room for the rate alone always finds a fit.
//
// The ladder is one priority order and not two (TTP-29): the median goes
// first, then the count, then the graph, then the TTFT. There is deliberately
// no "count without the graph" rung — a tile that could not hold the count
// beside the graph does not get the count back by dropping it, or the same
// tile would print a different pair of figures at two adjacent widths.
func statTails(m Model, t time.Duration, s Stream, sw int) []statTail {
	parts := []statTail{
		reserved("ttft "+streamTTFTFigure(s), ttftNominalW),
		reserved(streamCountFigure(m, t, s), countNominalW(m, s)),
		reserved("p50 "+streamP50Figure(s), p50NominalW),
	}
	out := make([]statTail, 0, len(parts)+2)
	if sw > 0 {
		for n := len(parts); n > 0; n-- {
			tail := joinStat(parts[:n])
			tail.spark = sw
			tail.reserve += sw + 1
			out = append(out, tail)
		}
		// The graph gives way here, and the TTFT alone is what is left.
		return append(out, parts[0], statTail{})
	}
	for n := len(parts); n > 0; n-- {
		out = append(out, joinStat(parts[:n]))
	}
	return append(out, statTail{})
}

// reserved pairs a part with the room it is measured against: its own width,
// or the nominal one when the figure has not grown into it yet.
func reserved(text string, nominal int) statTail {
	return statTail{text: text, reserve: max(width(text), nominal)}
}

// countNominalW is the width the third figure grows to: "80/128" once the
// count catches up with the cap, or "12/20s" once the clock catches up with
// the budget. A stream with neither has nothing to grow towards, so it is
// measured as it is.
func countNominalW(m Model, s Stream) int {
	if b := runBudget(m); b > 0 {
		// One column more than the budget's own spelling. The floor can hold
		// the cut past the budget (tape.LimitSummary.CutAt), and "102/20s"
		// must not be the frame where the tile changes its layout.
		secs := budgetSeconds(b)
		return width(fmt.Sprintf("%d/%ds", secs, secs)) + 1
	}
	if s.MaxTokens <= 0 {
		return 0
	}
	return width(fmt.Sprintf("%d/%d", s.MaxTokens, s.MaxTokens))
}

// joinStat is the parts separated by the chrome's own middle dot.
func joinStat(parts []statTail) statTail {
	var out statTail
	for i, p := range parts {
		if i > 0 {
			out.text += " · "
			out.reserve += width(" · ")
		}
		out.text += p.text
		out.reserve += p.reserve
	}
	return out
}

// streamRateFigure is the number the stat line leads with.
//
// While the stream is running it is the client-side rate over the content
// window, the same reduction the tile header used to print. Once the stream is
// done the server's own per-request figure replaces it, because server figures
// are the record and the client's is the check (CLAUDE.md).
//
// One arrival is not a rate: a stream with a single token has no gap to
// measure and prints "?", even on a replayed tape whose final timings were
// known from the first frame. Printing the server's answer before the run has
// produced it would put the tile at odds with the live figure beside it.
func streamRateFigure(s Stream) string {
	if s.Done && s.Timings.PredictedPerSecond > 0 {
		return fmtRate(s.Timings.PredictedPerSecond)
	}
	return fmtRate(streamRate(s))
}

// streamTTFTFigure is this stream's first-token latency: the recorded figure
// when the timings carry one, otherwise the arrival of the first token
// measured against the moment the request was sent.
//
// A stream with no token has no TTFT, whatever its timings say. A tape being
// replayed carries the final figures from its first frame, and printing one of
// them beside a prompt that is visibly still being evaluated would be the
// screen reporting a measurement the run has not taken yet — the same leak the
// speed pane is pinned against.
func streamTTFTFigure(s Stream) string {
	if len(s.Tokens) == 0 {
		return unknown
	}
	if s.Timings.TTFTMs > 0 {
		return fmtMs(s.Timings.TTFTMs)
	}
	return fmtMs(msOf(s.Tokens[0].T - s.StartedAt))
}

// streamCountFigure is how far through what will end it the stream is:
// "12/20s" when the run has a wall-clock budget, "80/128" when the request
// named a cap and nothing else, "80 tok" when it named neither.
//
// The clock outranks the cap because on a clock run the cap is not a target
// (TTP-76, 2026-09-14). A default run carries recorder.DefaultMaxTokens as a
// runaway guard, so a 20-second run drew "116/2048" — six per cent, crawling —
// while it was in fact twelve seconds into twenty and more than half done. The
// fraction was arithmetically correct and said the opposite of the truth,
// which is the one thing a figure on this screen may not do.
//
// It is deliberately ONE figure and not two. The tail this sits in is the
// widest thing the stat line gives up as a tile narrows, and "116 tok ·
// 12/20s" does not fit a half-width tile, so a pair would mean the ladder
// dropping both and the tile reporting nothing but its TTFT. Between the two,
// how far through the run the stream is beats how many tokens it has made: the
// rate beside it is the token figure the tile exists to report, and the count
// is on the card at the end.
//
// Reasoning tokens count toward the cap form. The server counts them in
// predicted_n and they are spent out of the same n_predict budget, so a
// thinking model that never reaches an answer still shows the budget running
// out — which is exactly the state the "cut" badge above it names.
func streamCountFigure(m Model, t time.Duration, s Stream) string {
	if b := runBudget(m); b > 0 {
		return fmt.Sprintf("%d/%ds", int(budgetElapsed(m, t, s).Seconds()), budgetSeconds(b))
	}
	if s.MaxTokens > 0 {
		return fmt.Sprintf("%d/%d", len(s.Tokens), s.MaxTokens)
	}
	return fmt.Sprintf("%d tok", len(s.Tokens))
}

// minBudgetFigure is the shortest budget the tile will draw a clock against.
//
// Under a second the figure would be "0/0s" or would need a decimal the run
// does not justify, and the cap form is then the more informative of the two.
// It is a bound on the spelling, not a judgement about the run: `--for 500ms`
// is a legal thing to record and this only declines to round it.
const minBudgetFigure = time.Second

// runBudget is the run's wall-clock budget when there is one worth drawing
// against, and 0 when there is not — a tape recorded before the clock existed,
// a `--n-predict` run, or `--for 0`. Every one of those falls back to the cap
// form, which is what those runs really end on.
func runBudget(m Model) time.Duration {
	if m.Summary.Limit.For < minBudgetFigure {
		return 0
	}
	return m.Summary.Limit.For
}

// budgetSeconds is the budget as the tile spells it.
func budgetSeconds(b time.Duration) int {
	return int(b.Round(time.Second) / time.Second)
}

// budgetElapsed is how much of the run's budget this stream has seen at clip
// time t.
//
// It is measured from Model.RunStart, the run's first request, because that is
// the origin tape.LimitSummary.For is defined against — not from t = 0, which
// on the live screen is the program starting and can be a ten-minute --wait
// earlier.
//
// A finished stream freezes at its last token. Its own clock has stopped, and
// a tile that kept counting to 20 after ending on EOS at 14 s would be
// reporting the run's remaining budget as this stream's progress. Why it
// stopped early — EOS, or the cap it did not print — is the header's job: it
// says "done" on the row above.
//
// Past the budget the figure keeps going, and that is the honest reading: the
// floor holds the cut back until every live stream has tape.MinCutTokens, so
// on a slow box "22/20s" is what happened (tape.LimitSummary.CutAt).
func budgetElapsed(m Model, t time.Duration, s Stream) time.Duration {
	// A finished stream is read off its own EndedAt and never off t, including
	// one that failed before its first token (EndedAt 0, so it saw nothing of
	// the budget). That also keeps the frozen live screen honest: after
	// EventDone the model is rebuilt from the tape on the tape's timeline while
	// the program's t keeps counting from its own start, and a figure that
	// consulted t there would mix the two clocks.
	at := t
	if s.Done && s.EndedAt < at {
		at = s.EndedAt
	}
	if d := at - m.RunStart; d > 0 {
		return d
	}
	return 0
}

// streamP50Figure is the median gap between this stream's tokens, or "?" until
// there are enough gaps for a median to mean anything.
func streamP50Figure(s Stream) string {
	itls := streamITLs(s)
	if len(itls) < minP50Intervals {
		return unknown
	}
	return fmtMs(percentile(itls, 0.5))
}

// promptMaxTokens is the answer cap the request asked for, or 0 when it did
// not ask for one.
//
// tape.PromptRecord.MaxTokens is the record: the recorder fills it with the
// cap the request was actually sent with. Params is the fallback, and it is
// what a tape written before that field existed carries — the parameters go
// in as they went over the wire (internal/server/stream.go), which is
// "max_tokens" for the OpenAI-shaped endpoint and "n_predict" for a caller
// that typed llama.cpp's own name for it. A negative value is llama-server's
// "no limit", which is not a cap and is read here as unknown.
func promptMaxTokens(p tape.PromptRecord) int {
	if p.MaxTokens > 0 {
		return p.MaxTokens
	}
	for _, key := range []string{"max_tokens", "n_predict"} {
		if n := paramInt(p.Params[key]); n > 0 {
			return n
		}
	}
	return 0
}

// paramInt reads a parameter that should be a whole number. A tape built in
// this process carries a Go int; the same tape read back from JSON carries a
// float64, and a decoder configured for json.Number carries one of those, so
// all three have to come back as the same figure.
func paramInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float32:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0
		}
		return int(i)
	}
	return 0
}
