package tui

// The embedding screen (2026-10-15): what View draws for a tape whose
// Summary.IsEmbedding() is true. It follows the decision screen's frame —
// the same border, the same feed of blocks arriving one at a time, the same
// request-latency footer — with what an embedding answers in place of the
// question tiles: each input's vector as a strip of cells (the 768 values
// averaged down to the strip's width, height and colour by sign and size),
// its norm, and below the feed the cosine similarity of every answered pair.
//
// With a reference tape (Model.EmbedRef, `toktape play a --against b`) each
// block also carries a bar of its cosine to the same input in the reference,
// scaled over [EmbedBarFloor, 1] with the band marked: at or above EmbedBand
// the two engines agree to run-to-run noise.
//
// Everything is a pure function of (m, t).

import (
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/midagedev/toktape/internal/tape"
)

const (
	// EmbedBand is the cosine at or above which two vectors of one input
	// count as the same answer: two runs of one llama-server endpoint on the
	// demo tape agree to >= 0.9995, so 0.999 leaves room for that noise.
	EmbedBand = 0.999
	// EmbedBarFloor is where the compare bar starts: below it the bar is
	// empty, and nothing that low is "close" anyway.
	EmbedBarFloor = 0.998
	embedBlockH   = 3 // text row, strip row, separator
)

// embeddingModel fills m for an embedding tape: the records sent by at.
func embeddingModel(m *Model, tp *tape.Tape, at time.Duration) {
	for _, r := range tp.Embeddings {
		// A query is on screen from when the replay starts typing it.
		if shown := r.ShownAt; r.SentAt <= at || (shown > 0 && shown <= at) {
			m.Embeddings = append(m.Embeddings, r)
		}
	}
	if len(tp.Embeddings) > 0 {
		m.RunStart, m.runStarted = tp.Embeddings[0].SentAt, true
	}
	for _, sm := range tp.Samples {
		if sm.T > at {
			break
		}
		m.Samples = append(m.Samples, sm)
	}
	m.seen = seenIn(tp.Samples)
	m.RunEnd = EmbeddingEnd(tp)
	m.Done = len(tp.Embeddings) > 0 && at >= m.RunEnd
}

// EmbeddingEnd is when an embedding run's last answer (or failed send) was.
func EmbeddingEnd(tp *tape.Tape) time.Duration {
	var end time.Duration
	for _, r := range tp.Embeddings {
		end = max(end, r.AnsweredAt, r.SentAt)
	}
	if e := tp.Summary.Embedding; e != nil {
		end = max(end, e.SceneEnd)
	}
	return end
}

func embedAnswered(r tape.EmbeddingRecord, t time.Duration) bool {
	return r.Error == "" && r.AnsweredAt > 0 && r.AnsweredAt <= t
}

// ---- colour ---------------------------------------------------------------

// embedLevels is how many steps each side of zero a strip cell has; a
// cosine cell uses the positive side only.
const embedLevels = 4

var (
	embedOnce sync.Once
	embedNeg  [embedLevels]style
	embedPos  [embedLevels]style
)

// embedStyle is the diverging scale: clay below zero, teal above, brighter
// with size. level is 1..embedLevels.
func embedStyle(th Theme, neg bool, level int) style {
	if !th.colour || level < 1 {
		return th.dim
	}
	embedOnce.Do(func() {
		r := lipgloss.NewRenderer(io.Discard)
		r.SetColorProfile(termenv.TrueColor)
		r.SetHasDarkBackground(true)
		negs := []string{"#5a3a34", "#8a4c3f", "#bf5f4a", "#f07a5c"}
		poss := []string{"#2c4f4a", "#3c7a70", "#4fae9f", "#7fe3d0"}
		for i := 0; i < embedLevels; i++ {
			embedNeg[i] = seqOf(r.NewStyle().Foreground(lipgloss.Color(negs[i])))
			embedPos[i] = seqOf(r.NewStyle().Foreground(lipgloss.Color(poss[i])))
		}
	})
	if neg {
		return embedNeg[level-1]
	}
	return embedPos[level-1]
}

// downsample averages v into w buckets.
func downsample(v []float32, w int) []float64 {
	if w <= 0 || len(v) == 0 {
		return nil
	}
	w = min(w, len(v))
	out := make([]float64, w)
	for i := range out {
		lo, hi := i*len(v)/w, (i+1)*len(v)/w
		var s float64
		for _, x := range v[lo:hi] {
			s += float64(x)
		}
		out[i] = s / float64(hi-lo)
	}
	return out
}

// vectorStrip draws cells (already downsampled) scaled by scale: the glyph's
// height is the value's place between -scale and +scale, the colour its sign
// and size, so the plain rendering still shows the shape.
func vectorStrip(th Theme, cells []float64, scale float64) string {
	const glyphs = "▁▂▃▄▅▆▇█"
	g := []rune(glyphs)
	var b strings.Builder
	for _, x := range cells {
		f := 0.5
		if scale > 0 {
			f = (x/scale + 1) / 2
		}
		f = math.Max(0, math.Min(1, f))
		gi := min(len(g)-1, int(f*float64(len(g))))
		mag := 0.0
		if scale > 0 {
			mag = math.Abs(x) / scale
		}
		level := min(embedLevels, int(math.Ceil(mag*embedLevels)))
		b.WriteString(th.paint(embedStyle(th, x < 0, level), string(g[gi])))
	}
	return b.String()
}

// ---- body -----------------------------------------------------------------

type embedView struct {
	m      Model
	th     Theme
	t      time.Duration
	bw     int
	stripW int
	scale  float64
	vecs   map[int][]float32 // by Index, answered only
}

func newEmbedView(m Model, th Theme, t time.Duration, bw int) *embedView {
	v := &embedView{m: m, th: th, t: t, bw: bw, vecs: map[int][]float32{}}
	right := 14 // "norm 1.0000" and its gap
	if m.EmbedRef != nil {
		right += 32
	}
	v.stripW = max(8, bw-4-right)
	// One scale for every strip on screen, so strips compare by eye.
	for _, r := range m.Embeddings {
		if !embedAnswered(r, t) {
			continue
		}
		vals := r.Values()
		if vals == nil {
			continue
		}
		v.vecs[r.Index] = vals
		for _, x := range downsample(vals, v.stripW) {
			v.scale = math.Max(v.scale, math.Abs(x))
		}
	}
	return v
}

func (v *embedView) headline() string {
	th, e := v.th, v.m.Summary.Embedding
	if e == nil {
		e = &tape.EmbeddingSummary{}
	}
	done := 0
	for _, r := range v.m.Embeddings {
		if embedAnswered(r, v.t) {
			done++
		}
	}
	l := newLine(th, v.bw)
	l.add(th.accentBold, "EMBEDDING")
	l.add(th.dim, "  POST "+orUnknown(e.Endpoint))
	if e.Dimensions > 0 {
		l.add(th.dim, fmt.Sprintf(" · dimensions %d", e.Dimensions))
	}
	if e.EncodingFormat != "" {
		l.add(th.dim, " · "+e.EncodingFormat)
	}
	right := fmt.Sprintf("%d/%d inputs · dim %s", done, e.Inputs, fmtTok(e.Dim))
	if v.m.EmbedRef != nil {
		right = fmt.Sprintf("vs %s · band ┃ %.3f · %s", v.m.EmbedRefName, EmbedBand, right)
	}
	l.gapTo(width(right))
	l.add(th.text, right)
	return l.String()
}

// block is one input: id, text and figures; the vector strip, its norm and
// (with a reference) the compare bar; a separator.
func (v *embedView) block(r tape.EmbeddingRecord) []string {
	th, t := v.th, v.t
	answered := embedAnswered(r, t)
	var right string
	var rst style
	switch {
	case answered:
		right, rst = fmtLat(msOf(r.Latency()))+" ms", th.accentBold
	case r.Error != "":
		right, rst = "✗ failed", th.bad
	default:
		right, rst = "embedding…", pulse(th, t)
	}
	tok := "input " + fmtTok(r.PromptTokens) + " tok · "
	if r.Prompt != "" {
		tok = r.Prompt + " prompt · " + tok
	}
	l := newLine(th, v.bw)
	l.add(th.accentBold, fmt.Sprintf("%-11s", r.ID))
	l.space(1)
	l.addTrunc(th.text, truncate(oneLine(r.Input), max(0, l.left()-width(tok)-width(right)-2)))
	l.gapTo(width(tok) + width(right))
	l.add(th.dim, tok)
	l.add(rst, right)
	out := []string{l.String()}

	s := newLine(th, v.bw)
	s.space(2)
	vals, ok := v.vecs[r.Index]
	switch {
	case ok:
		cells := downsample(vals, v.stripW)
		// The strip fills left to right as the answer lands; it is painted
		// cell by cell, so it goes in pre-measured.
		shown := int(math.Round(grow(r.AnsweredAt, t) * float64(len(cells))))
		s.used += shown
		s.b.WriteString(vectorStrip(th, cells[:shown], v.scale))
		s.add(th.darkFill, repeat('·', len(cells)-shown))
		s.space(v.stripW - len(cells) + 2)
		s.add(th.dim, "norm ")
		s.add(th.text, fmt.Sprintf("%.4f", r.Norm))
		if v.m.EmbedRef != nil {
			s.space(2)
			v.compareCell(s, r, vals)
		}
	case r.Error != "":
		s.addTrunc(th.bad, r.Error)
	default:
		s.add(th.darkFill, repeat('·', v.stripW))
	}
	return append(out, s.String(), blankRow(v.bw))
}

// compareCell is "████┃██▌── 0.99988 Δ0.0018" against the reference's
// record of the same id.
func (v *embedView) compareCell(l *lineBuf, r tape.EmbeddingRecord, vals []float32) {
	th := v.th
	ref, ok := v.m.EmbedRef[r.Key()]
	refVals := ref.Values()
	if !ok || refVals == nil {
		l.add(th.dim, "not in reference")
		return
	}
	c := tape.Cosine(vals, refVals)
	const barW = 10
	frac := (c - EmbedBarFloor) / (1 - EmbedBarFloor)
	bandAt := int(math.Round((EmbedBand - EmbedBarFloor) / (1 - EmbedBarFloor) * barW))
	bar := []rune(eighthBar(Theme{}, frac, barW, true, '─')) // plain glyphs
	st := th.accent
	if c < EmbedBand {
		st = th.warn
	}
	// The band is always marked, inside the fill or past it.
	bandAt = min(bandAt, barW-1)
	l.add(st, string(bar[:bandAt]))
	l.add(th.text, "┃")
	l.add(st, string(bar[bandAt+1:]))
	l.space(1)
	l.add(st, fmt.Sprintf("%.5f", c))
	l.add(th.dim, fmt.Sprintf(" Δ%.4f", tape.MaxAbsDiff(vals, refVals)))
}

// matrix is the cosine section: a header of column numbers and one row per
// input, each cell coloured by its value over the matrix's own range.
func (v *embedView) matrix(room int) []string {
	th := v.th
	recs := v.m.Embeddings
	n := len(recs)
	if n < 2 || room < 4 {
		return nil
	}
	n = min(n, room-2)
	lo, hi := 1.0, -1.0
	cos := make([][]float64, n)
	for i := 0; i < n; i++ {
		cos[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			a, oka := v.vecs[recs[i].Index]
			b, okb := v.vecs[recs[j].Index]
			cos[i][j] = math.NaN()
			if oka && okb {
				cos[i][j] = tape.Cosine(a, b)
				if i != j {
					lo, hi = math.Min(lo, cos[i][j]), math.Max(hi, cos[i][j])
				}
			}
		}
	}
	const labelW, cellW = 16, 6
	out := []string{sectionRow(th, v.bw, "COSINE SIMILARITY")}
	h := newLine(th, v.bw)
	h.space(labelW)
	for j := 0; j < n && h.left() >= cellW; j++ {
		h.add(th.dim, fmt.Sprintf("%*s", cellW, fmt.Sprintf("%d", j)))
	}
	out = append(out, h.String())
	for i := 0; i < n; i++ {
		l := newLine(th, v.bw)
		l.add(th.dim, fmt.Sprintf("%2d ", i))
		l.add(th.text, fmt.Sprintf("%-*s", labelW-3, truncate(recs[i].ID, labelW-4)))
		for j := 0; j < n && l.left() >= cellW; j++ {
			c := cos[i][j]
			switch {
			case math.IsNaN(c):
				l.add(th.darkFill, fmt.Sprintf("%*s", cellW, "·"))
			case i == j:
				l.add(th.dim, fmt.Sprintf("%*s", cellW, "1.00"))
			default:
				level := 1
				if hi > lo {
					level = 1 + int(math.Min(embedLevels-1, (c-lo)/(hi-lo)*embedLevels))
				}
				st := embedStyle(th, false, level)
				if level == embedLevels {
					st = th.accentBold
				}
				l.add(st, fmt.Sprintf("%*.2f", cellW, c))
			}
		}
		out = append(out, l.String())
	}
	return out
}

func embeddingBody(m Model, th Theme, t time.Duration, bw, bodyH int) []string {
	v := newEmbedView(m, th, t, bw)
	rows := []string{v.headline(), blankRow(bw), sectionRow(th, bw, "INPUTS")}
	mat := v.matrix(min(len(m.Embeddings)+2, bodyH/2))
	room := bodyH - len(rows) - len(mat) - 1
	// A block is three rows with its separator, two without; the separator
	// goes first when every input would not fit otherwise.
	bh := embedBlockH
	if len(m.Embeddings)*bh > room {
		bh = embedBlockH - 1
	}
	var feed []string
	for _, r := range m.Embeddings {
		feed = append(feed, v.block(r)[:bh]...)
	}
	// Newest at the bottom: when the feed still does not fit, the oldest go.
	if over := len(feed) - max(0, room); over > 0 {
		cut := (over + bh - 1) / bh * bh
		feed = feed[min(cut, len(feed)):]
	}
	rows = append(rows, feed...)
	if len(rows)+len(mat) <= bodyH {
		rows = append(rows, fitRows(nil, blankRow(bw), max(0, bodyH-len(rows)-len(mat)))...)
		rows = append(rows, mat...)
	}
	return fitRows(rows, blankRow(bw), bodyH)
}

func embeddingFooter(m Model, th Theme, t time.Duration, w int) string {
	var series []float64
	for _, r := range m.Embeddings {
		if embedAnswered(r, t) {
			series = append(series, msOf(r.Latency()))
		}
	}
	hints := "q quit"
	if m.Replay {
		hints = ""
	}
	l := newLine(th, w)
	l.add(th.dim, "request latency ")
	median := "p50 " + fmtLat(percentile(series, 0.5)) + " ms"
	stripW := max(0, l.left()-width(median)-width(hints)-4)
	if len(series) > stripW {
		series = series[len(series)-stripW:]
	}
	cells := LatencyStrip(series, stripW)
	l.space(stripW - len(cells))
	writeCells(l, th, cells, cellPalette{base: th.dim, warn: th.text, bad: th.warn})
	l.space(2)
	if len(cells) > 0 {
		l.add(th.dim, median)
	}
	l.gapTo(width(hints))
	l.add(th.dim, hints)
	return l.String()
}

// embeddingView is View for an embedding tape.
func embeddingView(m Model, t time.Duration, w, h int) string {
	th := m.Theme
	inner := w - 2
	bodyH := h - chromeH
	bar := th.paint(th.dim, "│")
	lines := make([]string, 0, h)
	lines = append(lines, topBorder(m, th, t, inner))
	body := embeddingBody
	if hasScene(m) {
		body = sceneBody
	}
	for _, row := range body(m, th, t, inner-2, bodyH) {
		lines = append(lines, bar+" "+row+" "+bar)
	}
	lines = append(lines, th.paint(th.dim, "├"+repeat('─', inner)+"┤"))
	lines = append(lines, bar+" "+embeddingFooter(m, th, t, inner-2)+" "+bar)
	lines = append(lines, th.paint(th.dim, "└"+repeat('─', inner)+"┘"))
	return strings.Join(lines, "\n")
}

// SetEmbedRef makes ref the reference m's inputs are compared with, labelled
// name; a nil ref clears it.
func SetEmbedRef(m *Model, ref *tape.Tape, name string) {
	if ref == nil {
		m.EmbedRef, m.EmbedRefName = nil, ""
		return
	}
	m.EmbedRef = map[string]tape.EmbeddingRecord{}
	for _, r := range ref.Embeddings {
		m.EmbedRef[r.Key()] = r
	}
	m.EmbedRefName = name
}

// embedSpeedRows are the machine pane's SPEED rows on an embedding tape:
// requests answered, their latency p50, and the prompt tokens embedded.
// There is no tok/s to show: an embedding is one prefill, no decode.
func embedSpeedRows(m Model, th Theme, t time.Duration, cw int) []string {
	var lat []float64
	n, tok := 0, 0
	for _, r := range m.Embeddings {
		if embedAnswered(r, t) {
			n++
			tok += r.PromptTokens
			lat = append(lat, msOf(r.Latency()))
		}
	}
	total := 0
	if e := m.Summary.Embedding; e != nil {
		total = e.Inputs
	}
	row := func(label, value string, st style) string {
		l := newLine(th, cw)
		l.add(th.dim, label)
		l.gapTo(width(value))
		l.add(st, value)
		return l.String()
	}
	p50 := unknown
	if len(lat) > 0 {
		p50 = fmtLat(percentile(lat, 0.5)) + " ms"
	}
	return []string{
		row("requests", fmt.Sprintf("%d/%d", n, total), th.text),
		row("latency p50", p50, th.accentBold),
		row("prompt tokens", fmtTok(tok), th.text),
	}
}
