package tui

import (
	"encoding/json"
	"fmt"

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
// figures that qualify it dim and behind it:
//
//	12.1 tok/s ttft 250 ms · 80/128 · p50 86 ms
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

// minP50Intervals is how many inter-token gaps a stream must have before its
// median is printed. Under it the figure would move every frame and describe
// nothing; "?" is the honest reading (CLAUDE.md: an unobserved value prints
// "?", never a plausible default).
const minP50Intervals = 8

// tileStatLine is the tile's hero row: this stream's decode rate, then the
// figures that qualify it.
//
// It always returns exactly cw columns. What it gives up as the tile narrows,
// in order: the median, then the token count, then the TTFT. The rate itself
// never goes — a tile without it is not reporting anything — and nothing is
// ever half-printed, because a clipped number reads as a wrong number.
//
// The rate is bold accent on every tile, active or not. Painting the inactive
// ones plain would make the hero figure dimmest on the seven tiles a reader
// scans and brightest only on the one that happens to be talking; the gutter
// and the breathing cursor already say which that is.
func tileStatLine(th Theme, s Stream, cw int) string {
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
	for _, tail := range statTails(s) {
		if tail.reserve <= l.left() {
			l.addRaw(th.dim, tail.text)
			break
		}
	}
	return l.String()
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
}

// The widths the growing parts are reserved against: a millisecond figure of
// three digits, which is what a healthy TTFT and a healthy median both are,
// and a token count with as many digits as the budget it is counting towards.
const (
	ttftNominalW = 11 // "ttft 250 ms"
	p50NominalW  = 10 // "p50 250 ms"
)

// statTails are the dim qualifiers of the stat line, widest first. The last
// entry is empty, so a tile with room for the rate alone always finds a fit.
func statTails(s Stream) []statTail {
	parts := []statTail{
		reserved("ttft "+streamTTFTFigure(s), ttftNominalW),
		reserved(streamCountFigure(s), countNominalW(s)),
		reserved("p50 "+streamP50Figure(s), p50NominalW),
	}
	out := make([]statTail, 0, len(parts)+1)
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

// countNominalW is the width "80/128" grows to when the count catches up with
// the budget. A stream whose budget is unknown has nothing to grow towards, so
// it is measured as it is.
func countNominalW(s Stream) int {
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

// streamCountFigure is how far through its budget the stream is: "80/128" when
// the request named a cap, "80 tok" when it did not.
//
// Reasoning tokens count. The server counts them in predicted_n and they are
// spent out of the same n_predict budget, so a thinking model that never
// reaches an answer still shows the budget running out — which is exactly the
// state the "cut" badge above it names.
func streamCountFigure(s Stream) string {
	if s.MaxTokens > 0 {
		return fmt.Sprintf("%d/%d", len(s.Tokens), s.MaxTokens)
	}
	return fmt.Sprintf("%d tok", len(s.Tokens))
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
// tape.PromptRecord has no field for it: the parameters go into Params as they
// went over the wire (internal/server/stream.go), which is "max_tokens" for
// the OpenAI-shaped endpoint and "n_predict" for a caller that typed
// llama.cpp's own name for it. A negative value is llama-server's "no limit",
// which is not a cap and is read here as unknown.
func promptMaxTokens(p tape.PromptRecord) int {
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
