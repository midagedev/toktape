// Package tui renders toktape's live two-pane screen: the streaming answers on
// the left, the machine on the right.
//
// Two rules shape everything here.
//
// The view is a pure function. View(m, t, w, h) draws frame t of the run held
// in m and reads no clock, no terminal and no global state; run.go is the only
// file that touches the wall clock, and it does so once, to turn elapsed real
// time into a clip time. That is decision 10 of docs/toktape-spec.ko.md: the
// live screen and the replay of a .tape share one renderer, so a recording
// reproduces the exact frames a viewer saw. Everything that moves — eased
// bars, the breathing cursor, the prefill spinner, the scrolling sparkline,
// the header shimmer — is a function of that clip time.
//
// Every line is exactly w display columns. Widths are measured with East Asian
// width (handover lesson 5), so Hangul counts two columns and a syllable is
// never split across the pane border. Colour is added last, to text that has
// already been fitted, which is why the coloured and the plain rendering of a
// frame differ only in escape sequences.
//
// Time base: t and every duration on Model are clip time since the run
// started. tape.TokenEvent.T is relative to its own request, so ModelAt adds
// tape.RequestRecord.StartedAt when it places a token on the run's timeline.
package tui

import (
	"sort"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// Mode is what the body of the screen is showing.
type Mode int

const (
	// ModeLive is the two-pane screen: streams left, machine right.
	ModeLive Mode = iota
	// ModePrompt overlays the rendered prompt and the template info ("p").
	ModePrompt
	// ModeCard replaces the body with the result card ("c", after Done).
	ModeCard
)

// Token is one generated token placed on the run's timeline.
type Token struct {
	// T is the arrival time since the run started, not since its own request
	// was sent: tape.TokenEvent.T plus the request's StartedAt.
	T time.Duration
	// Stream is the index of the request that produced it.
	Stream int
	Text   string
	// ITL is the gap to the previous token of the same stream. The first
	// token of a stream has ITL 0 — its latency is the TTFT, a different
	// measurement, and averaging the two is how a prefill gets counted as
	// decode (handover lesson 1).
	ITL time.Duration
	// MajFaultsDelta is the major faults the server took since the previous
	// token. Zero is a measurement here, not an unknown.
	MajFaultsDelta uint64
	// Reasoning marks a token a thinking model emitted as reasoning_content.
	// It is a decode token like any other — it counts toward every rate and
	// toward TTFT — and it differs only in how the answer pane draws it: dim,
	// so the reader can see the model thinking without mistaking it for the
	// answer (tape.TokenEvent.Reasoning).
	Reasoning bool
}

// Stream is one concurrent request as the screen knows it.
type Stream struct {
	Index     int
	Slot      int
	StartedAt time.Duration
	// Text is everything the stream has generated so far, in arrival order:
	// for a thinking model that is the reasoning monologue and then the
	// answer. It is the concatenation of the tokens' text, and Tokens says
	// which runes came from thinking (see reasoning.go).
	Text string
	// RenderedPrompt is what /apply-template returned for this request, empty
	// when the endpoint was unavailable.
	RenderedPrompt string
	Tokens         []Token
	Progress       []tape.PromptProgress
	Timings        tape.TimingsSummary
	Cache          tape.CacheSummary
	// MaxTokens is the answer cap the request asked for (max_tokens /
	// n_predict), 0 when it asked for none or when the recorder did not say.
	// The tile prints "80/128" when it is known and "80 tok" when it is not;
	// an unknown budget is never guessed at (CLAUDE.md).
	MaxTokens int
	// EndedAt is when the last token arrived; 0 while the stream is running.
	EndedAt time.Duration
	Done    bool
	Err     string
}

// Model is the whole state the view draws. It changes only when an event is
// applied or when ModelAt cuts a tape at an instant; it never changes on a
// tick.
type Model struct {
	Summary tape.RunSummary
	Streams []Stream
	Samples []tape.RunSample

	// At is the instant the state was cut at: no token, sample or progress
	// row later than At is in the model. For the live program it is the
	// arrival time of the newest event.
	At time.Duration
	// RunEnd is when the last token of the run arrived. 0 until the run ends.
	RunEnd time.Duration
	// Done is set when every stream has finished.
	Done bool
	// TapePath is printed in the footer once the tape has been written.
	TapePath string
	// PID of the server process, 0 when it was not found locally.
	PID int
	// Err is the last fatal error; non-empty replaces the body with it.
	Err string

	Mode  Mode
	Theme Theme

	// Grid is the largest tile arrangement one page of the answer pane may
	// use. The zero value chooses one from the room the pane has; ModelAt
	// sets DefaultGrid, so a replayed tape lays out the way the run did.
	Grid Grid
	// Page is which page of tiles is on screen, from zero. It is ordinary
	// model state rather than something the view remembers, so a replay of a
	// tape shows the page the model names and the same (m, t) always draws
	// the same frame.
	Page int
}

// ModelAt builds the state of tp as of clip time at: every token, sample and
// progress row with a timestamp at or before at, and nothing after it.
//
// This is what makes replay possible (decision 10) — the same function the
// GIF/mp4 track will step through, frame by frame.
func ModelAt(tp *tape.Tape, at time.Duration) Model {
	m := Model{At: at, Mode: ModeLive, Grid: DefaultGrid}
	if tp == nil {
		return m
	}
	m.Summary = tp.Summary
	m.PID = tp.Summary.Server.PID

	m.Streams = make([]Stream, 0, len(tp.Requests))
	runEnd := time.Duration(0)
	allDone := len(tp.Requests) > 0
	for _, req := range tp.Requests {
		s := Stream{
			Index:          req.Index,
			Slot:           req.Slot,
			StartedAt:      req.StartedAt,
			RenderedPrompt: req.Prompt.RenderedPrompt,
			MaxTokens:      promptMaxTokens(req.Prompt),
			Timings:        req.Timings,
			Cache:          req.Cache,
			Err:            req.Error,
		}
		var (
			text   []byte
			prevT  time.Duration
			lastAt time.Duration
		)
		for i, tk := range req.Tokens {
			abs := req.StartedAt + tk.T
			if abs > runEnd {
				runEnd = abs
			}
			if abs > at {
				break
			}
			itl := time.Duration(0)
			if i > 0 {
				itl = abs - prevT
			}
			prevT = abs
			lastAt = abs
			s.Tokens = append(s.Tokens, Token{
				T:              abs,
				Stream:         req.Index,
				Text:           tk.Text,
				ITL:            itl,
				MajFaultsDelta: tk.MajFaultsDelta,
				Reasoning:      tk.Reasoning,
			})
			text = append(text, tk.Text...)
		}
		s.Text = string(text)
		for _, pr := range req.Progress {
			if req.StartedAt+pr.T > at {
				break
			}
			pr.T += req.StartedAt
			s.Progress = append(s.Progress, pr)
		}
		// A stream is finished once its last recorded token is in the past.
		if n := len(req.Tokens); n > 0 && req.StartedAt+req.Tokens[n-1].T <= at {
			s.Done = true
			s.EndedAt = lastAt
		} else if req.Error != "" {
			s.Done = true
		}
		if !s.Done {
			allDone = false
		}
		m.Streams = append(m.Streams, s)
	}
	m.RunEnd = runEnd
	m.Done = allDone && at >= runEnd

	for _, sm := range tp.Samples {
		if sm.T > at {
			break
		}
		m.Samples = append(m.Samples, sm)
	}
	return m
}

// tokenWindow returns the last n tokens of the whole run, ordered by arrival.
// The sparkline and the latency strip are run-wide, not per stream: a page-in
// burst hits whichever stream is decoding when the fault happens.
func (m Model) tokenWindow(n int) []Token {
	if n <= 0 {
		return nil
	}
	all := make([]Token, 0, n*2)
	for _, s := range m.Streams {
		all = append(all, s.Tokens...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].T < all[j].T })
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}

// perCell is how many of the run's tokens one sparkline column covers.
//
// One column per token is the single-stream design and it stays exactly that
// when Concurrency is 1. With eight streams it would not: the merged timeline
// produces eight tokens per instant, so a 28-column sparkline would cover a
// third of a second of history and could never show the page-in burst it
// exists to show. One column per token *per stream* keeps the time span the
// reader expects, and the cell is the mean over that slice.
func (m Model) perCell() int {
	if n := len(m.Streams); n > 1 {
		return n
	}
	return 1
}

// majFaultSeries is the major faults per token over the last w columns.
func (m Model) majFaultSeries(w int) []float64 {
	per := m.perCell()
	win := m.tokenWindow(w * per)
	vals := make([]float64, len(win))
	for i, t := range win {
		vals[i] = float64(t.MajFaultsDelta)
	}
	return bucketMeans(vals, per)
}

// latencySeries is the inter-token latencies in milliseconds over the last w
// columns. First-token gaps are dropped: a TTFT is not an ITL, and averaging
// the two is how a prefill gets counted as decode (handover lesson 1).
func (m Model) latencySeries(w int) []float64 {
	per := m.perCell()
	win := m.tokenWindow((w + 1) * per)
	vals := make([]float64, 0, len(win))
	for _, t := range win {
		if t.ITL <= 0 {
			continue
		}
		vals = append(vals, float64(t.ITL)/float64(time.Millisecond))
	}
	if n := w * per; len(vals) > n {
		vals = vals[len(vals)-n:]
	}
	// The slowest token in a slice, not the average of the slice. The strip
	// exists to make a stall visible, and averaging one 265 ms page-in with
	// seven healthy neighbours hides exactly the event it was drawn for. The
	// fault sparkline above keeps the mean, because faults per token is a rate
	// and its average is the quantity the label names.
	return bucketMaxima(vals, per)
}

// streamITLs is one stream's inter-token latencies in milliseconds, oldest
// first.
//
// The first token of a stream contributes nothing: its gap is the TTFT, a
// different measurement, and averaging the two is how a prefill gets counted
// as decode (handover lesson 1). Unlike latencySeries this is one stream's own
// history, un-bucketed — a tile is asking about one slot, not about the run.
func streamITLs(s Stream) []float64 {
	out := make([]float64, 0, len(s.Tokens))
	for _, tk := range s.Tokens {
		if tk.ITL <= 0 {
			continue
		}
		out = append(out, float64(tk.ITL)/float64(time.Millisecond))
	}
	return out
}

// streamRates is the last w instantaneous decode rates of one stream, in
// tokens per second: the reciprocal of each gap above.
//
// A rate rather than a latency because the tile's label is tok/s and the
// reader is comparing tiles: on a rate sparkline the stall the run took is a
// dip, and a starving slot is visibly lower than its neighbours. Reading the
// same picture off latencies would mean inverting it by eye.
func streamRates(s Stream, w int) []float64 {
	ms := streamITLs(s)
	if w > 0 && len(ms) > w {
		ms = ms[len(ms)-w:]
	}
	out := make([]float64, len(ms))
	for i, v := range ms {
		out[i] = float64(time.Second/time.Millisecond) / v
	}
	return out
}

// bucketMeans reduces vals to one mean per consecutive group of per values.
// A trailing partial group keeps its own mean rather than being dropped, so
// the newest tokens always reach the right-hand end of the line.
func bucketMeans(vals []float64, per int) []float64 {
	return bucket(vals, per, func(group []float64) float64 {
		sum := 0.0
		for _, v := range group {
			sum += v
		}
		return sum / float64(len(group))
	})
}

// bucketMaxima reduces vals to the largest value of each consecutive group.
func bucketMaxima(vals []float64, per int) []float64 {
	return bucket(vals, per, func(group []float64) float64 {
		top := group[0]
		for _, v := range group[1:] {
			if v > top {
				top = v
			}
		}
		return top
	})
}

func bucket(vals []float64, per int, reduce func([]float64) float64) []float64 {
	if per <= 1 || len(vals) == 0 {
		return vals
	}
	out := make([]float64, 0, (len(vals)+per-1)/per)
	for i := 0; i < len(vals); i += per {
		end := i + per
		if end > len(vals) {
			end = len(vals)
		}
		out = append(out, reduce(vals[i:end]))
	}
	return out
}

// sampleAt returns the newest sample at or before t and the one before it,
// which is what a bar eases between. Both are nil when nothing was sampled.
func (m Model) sampleAt(t time.Duration) (cur, prev *tape.RunSample) {
	for i := range m.Samples {
		if m.Samples[i].T > t {
			break
		}
		prev = cur
		cur = &m.Samples[i]
	}
	return cur, prev
}

// tokensSoFar counts the tokens of every stream in the model.
func (m Model) tokensSoFar() int {
	n := 0
	for _, s := range m.Streams {
		n += len(s.Tokens)
	}
	return n
}

// activeStream is the index of the stream whose cursor breathes: the one that
// received the most recent token, or the first unfinished stream before any
// token has arrived. It is -1 when there are no streams.
func (m Model) activeStream() int {
	best, bestT := -1, time.Duration(-1)
	for _, s := range m.Streams {
		if n := len(s.Tokens); n > 0 && s.Tokens[n-1].T > bestT {
			bestT = s.Tokens[n-1].T
			best = s.Index
		}
	}
	if best >= 0 {
		return best
	}
	for _, s := range m.Streams {
		if !s.Done {
			return s.Index
		}
	}
	if len(m.Streams) > 0 {
		return m.Streams[0].Index
	}
	return -1
}

// decodeRateAt returns the aggregate decode rate as of t and as of just before
// the newest token, plus that token's arrival time.
//
// The rate is a step function of token arrivals, so the tween between the two
// figures needs no stored "previous value": the previous value is the same
// reduction over one token fewer. Counting only tokens after each stream's
// first one keeps the prefill out of the decode figure (handover lesson 1).
func (m Model) decodeRateAt(t time.Duration) (prev, cur float64, since time.Duration) {
	type acc struct {
		n     int
		first time.Duration
		last  time.Duration
	}
	newest := time.Duration(-1)
	for _, s := range m.Streams {
		for _, tk := range s.Tokens {
			if tk.T > t {
				break
			}
			if tk.T > newest {
				newest = tk.T
			}
		}
	}
	rate := func(cut time.Duration) float64 {
		var total float64
		for _, s := range m.Streams {
			var a acc
			for _, tk := range s.Tokens {
				if tk.T > cut {
					break
				}
				if a.n == 0 {
					a.first = tk.T
				}
				a.last = tk.T
				a.n++
			}
			if a.n < 2 {
				continue
			}
			win := a.last - a.first
			if win <= 0 {
				continue
			}
			total += float64(a.n-1) / win.Seconds()
		}
		return total
	}
	if newest < 0 {
		return 0, 0, 0
	}
	return rate(newest - time.Nanosecond), rate(newest), newest
}
