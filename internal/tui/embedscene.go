package tui

// The search scene (2026-10-15): the default embedding screen for a tape
// whose inputs carry roles (the built-in search set). It shows what an
// embedding model is for: a corpus is indexed one doc at a time, then each
// query is typed, embedded, and answered with a ranked top-k against the
// index — with the expected doc marked, its rank with and without the task
// prompts, and its rank when the vectors are cut to 256 and 128 dims (MRL:
// truncate, then cosine renormalises). After the last query, the docs'
// cosine matrix ordered by topic shows the clusters.
//
// Every ranking is computed here from the recorded vectors, as a pure
// function of (m, t); the pacing (typing, holds) is the recorder's idle time
// on the tape, never part of a latency.

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/midagedev/toktape/internal/tape"
)

const (
	sceneTopK    = 5
	sceneStagger = 120 * time.Millisecond // between result rows appearing
	// EmbedTick is the replay's redraw period while an embedding scene runs.
	EmbedTick  = 33 * time.Millisecond
	growDur    = 450 * time.Millisecond // a bar or strip reaching its value
	fadeDur    = 200 * time.Millisecond // a row coming up from dim
	blinkDur   = 530 * time.Millisecond // the typing cursor's half period
	clusterLag = 350 * time.Millisecond // between topic blocks fading in
)

// grow is 0..1 eased progress over growDur from since.
func grow(since, t time.Duration) float64 {
	if t <= since {
		return 0
	}
	return easeOutCubic(float64(t-since) / float64(growDur))
}

// fading reports whether a row that appeared at since is still coming up.
func fading(since, t time.Duration) bool { return t < since+fadeDur }

// pulse is the in-flight item's style: the accent breathing low → high.
func pulse(th Theme, t time.Duration) style {
	return []style{th.accentLow, th.accentMid, th.accentHigh}[breathPhase(t)]
}

// typedCount is how many runes of q are on screen at t. The typing runs
// from ShownAt to SentAt, with a per-rune weight from a hash of the id and
// position so the rate wavers like a hand's, and is the same every replay.
func typedCount(q tape.EmbeddingRecord, t time.Duration) int {
	rs := []rune(q.Input)
	if q.SentAt <= q.ShownAt || t >= q.SentAt {
		return len(rs)
	}
	if t <= q.ShownAt {
		return 0
	}
	w := make([]float64, len(rs))
	total := 0.0
	for i := range rs {
		h := uint32(2166136261)
		for _, c := range q.ID {
			h = (h ^ uint32(c)) * 16777619
		}
		h = (h ^ uint32(i)) * 16777619
		w[i] = 0.55 + float64(h%1000)/1000 // 0.55..1.55
		if rs[i] == ' ' {
			w[i] *= 1.6 // a beat between words
		}
		total += w[i]
	}
	f := float64(t-q.ShownAt) / float64(q.SentAt-q.ShownAt) * total
	n := 0
	for acc := 0.0; n < len(rs) && acc+w[n] <= f; n++ {
		acc += w[n]
	}
	return n
}

// sceneMRL are the truncations a query is re-ranked at.
var sceneMRL = []int{768, 256, 128}

// hasScene reports whether m's tape is a labelled set (rows with roles)
// the scene can draw.
func hasScene(m Model) bool {
	if m.EmbedVerify || m.EmbedRef != nil {
		return false
	}
	for _, r := range m.Embeddings {
		if r.Role != "" {
			return true
		}
	}
	return false
}

type sceneDoc struct {
	rec  tape.EmbeddingRecord
	vec  []float32 // primary (prompted when there are prompts)
	bare []float32
}

type scene struct {
	m       Model
	th      Theme
	t       time.Duration
	docs    []sceneDoc // in send order, answered by t
	queries []tape.EmbeddingRecord
	bareQ   map[string][]float32
	byID    map[string]int
}

func primary(r tape.EmbeddingRecord, prompts bool) bool {
	return (r.Prompt != "") == prompts
}

func newScene(m Model, th Theme, t time.Duration) *scene {
	s := &scene{m: m, th: th, t: t, bareQ: map[string][]float32{}, byID: map[string]int{}}
	prompts := false
	for _, r := range m.Embeddings {
		if r.Prompt != "" {
			prompts = true
		}
	}
	bareDoc := map[string][]float32{}
	for _, r := range m.Embeddings {
		if r.Prompt == "" && embedAnswered(r, t) {
			if r.Role == tape.EmbeddingRoleDoc {
				bareDoc[r.ID] = r.Values()
			} else {
				s.bareQ[r.ID] = r.Values()
			}
		}
	}
	for _, r := range m.Embeddings {
		if !primary(r, prompts) {
			continue
		}
		switch r.Role {
		case tape.EmbeddingRoleDoc:
			if embedAnswered(r, t) {
				s.byID[r.ID] = len(s.docs)
				s.docs = append(s.docs, sceneDoc{rec: r, vec: r.Values(), bare: bareDoc[r.ID]})
			}
		case tape.EmbeddingRoleQuery:
			s.queries = append(s.queries, r)
		}
	}
	return s
}

type hit struct {
	doc   int
	score float64
}

// rank orders the index by cosine to q, over the first dims values (0 =
// all); bare ranks the bare doc vectors.
func (s *scene) rank(q []float32, dims int, bare bool) []hit {
	cut := func(v []float32) []float32 {
		if dims > 0 && dims < len(v) {
			return v[:dims]
		}
		return v
	}
	var hs []hit
	for i, d := range s.docs {
		v := d.vec
		if bare {
			v = d.bare
		}
		if v == nil || q == nil {
			continue
		}
		hs = append(hs, hit{i, tape.Cosine(cut(q), cut(v))})
	}
	sort.SliceStable(hs, func(a, b int) bool { return hs[a].score > hs[b].score })
	return hs
}

// rankOf is the 1-based place of doc id in hs, 0 when absent.
func (s *scene) rankOf(hs []hit, id string) int {
	for i, h := range hs {
		if s.docs[h.doc].rec.ID == id {
			return i + 1
		}
	}
	return 0
}

// active is the query on screen at t: the last one whose typing began.
func (s *scene) active() (tape.EmbeddingRecord, bool) {
	var q tape.EmbeddingRecord
	ok := false
	for _, r := range s.queries {
		shown := r.ShownAt
		if shown == 0 {
			shown = r.SentAt
		}
		if shown <= s.t {
			q, ok = r, true
		}
	}
	return q, ok
}

func (s *scene) outro() bool {
	e := s.m.Summary.Embedding
	return e != nil && e.OutroAt > 0 && s.t >= e.OutroAt && len(s.queries) > 0 &&
		embedAnswered(s.queries[len(s.queries)-1], s.t)
}

// topicStyle gives each topic its own colour, in the order topics first
// appear; a plain theme leaves them all dim.
func (s *scene) topicStyle(topic string) style {
	for i, tp := range s.topics() {
		if tp == topic {
			return topicColour(s.th, i)
		}
	}
	return s.th.dim
}

var (
	topicOnce   sync.Once
	topicStyles []style
)

func topicColour(th Theme, i int) style {
	if !th.colour {
		return th.dim
	}
	topicOnce.Do(func() {
		r := lipgloss.NewRenderer(io.Discard)
		r.SetColorProfile(termenv.TrueColor)
		r.SetHasDarkBackground(true)
		for _, c := range []string{"#7fe3d0", "#e0b25c", "#f07a5c", "#9fb4ff", "#c9a0e8", "#d9d9d9"} {
			topicStyles = append(topicStyles, seqOf(r.NewStyle().Foreground(lipgloss.Color(c))))
		}
	})
	return topicStyles[i%len(topicStyles)]
}

func (s *scene) topics() []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range s.m.Embeddings {
		if r.Role == tape.EmbeddingRoleDoc && !seen[r.Topic] {
			seen[r.Topic] = true
			out = append(out, r.Topic)
		}
	}
	return out
}

func (s *scene) totalDocs() int {
	if e := s.m.Summary.Embedding; e != nil && e.Docs > 0 {
		return e.Docs
	}
	return len(s.docs)
}

func (s *scene) prompted() bool {
	e := s.m.Summary.Embedding
	return e != nil && len(e.Prompts) > 0
}

// ---- the index column -------------------------------------------------------

func (s *scene) indexColumn(w, h int, hits map[int]int) []string {
	th := s.th
	title := fmt.Sprintf("INDEX  %d/%d docs", len(s.docs), s.totalDocs())
	out := []string{sectionRow(th, w, title)}
	for i, d := range s.docs {
		l := newLine(th, w)
		if r, ok := hits[i]; ok {
			l.add(th.accentBold, fmt.Sprintf("%d›", r))
		} else {
			l.space(2)
		}
		l.add(s.topicStyle(d.rec.Topic), "● ")
		l.add(th.dim, fmt.Sprintf("%-7s", d.rec.ID))
		ms := fmtLat(msOf(d.rec.Latency())) + " ms"
		st := th.text
		if d.rec.AnsweredAt > s.t-400*time.Millisecond {
			st = th.accentBold // just arrived
		}
		l.addTrunc(st, truncate(oneLine(d.rec.Input), max(0, l.left()-width(ms)-1)))
		l.gapTo(width(ms))
		l.add(th.dim, ms)
		out = append(out, l.String())
	}
	// The doc in flight.
	for _, r := range s.m.Embeddings {
		if r.Role == tape.EmbeddingRoleDoc && primary(r, s.prompted()) && !embedAnswered(r, s.t) && r.Error == "" {
			l := newLine(th, w)
			l.space(2)
			l.add(pulse(th, s.t), "● ")
			l.add(pulse(th, s.t), fmt.Sprintf("%-7s", r.ID))
			l.addTrunc(pulse(th, s.t), truncate(oneLine(r.Input), l.left()))
			out = append(out, l.String())
			break
		}
	}
	return fitRows(out, blankRow(w), h)
}

// ---- the search column ------------------------------------------------------

func (s *scene) searchColumn(w, h int) ([]string, map[int]int) {
	th := s.th
	out := []string{sectionRow(th, w, "SEARCH")}
	hits := map[int]int{}
	q, ok := s.active()
	if !ok {
		l := newLine(th, w)
		l.add(th.dim, "› indexing the corpus…")
		return fitRows(append(out, l.String()), blankRow(w), h), hits
	}
	// Typing: the text appears over ShownAt..SentAt at a wavering rate.
	typed := []rune(q.Input)[:typedCount(q, s.t)]
	l := newLine(th, w)
	l.add(th.accentBold, "› ")
	l.addTrunc(th.text, truncate(string(typed), max(0, l.left()-12)))
	switch {
	case s.t < q.SentAt:
		cur := "▌"
		if int(s.t/blinkDur)%2 == 1 {
			cur = " "
		}
		l.add(th.accent, cur)
	case !embedAnswered(q, s.t):
		l.add(pulse(th, s.t), " embedding…")
	default:
		ms := fmtLat(msOf(q.Latency())) + " ms"
		l.gapTo(width(ms))
		l.add(th.dim, ms)
	}
	out = append(out, l.String())
	if q.Prompt != "" {
		p := newLine(th, w)
		p.add(th.dim, "  prefix ")
		p.addTrunc(th.text, fmt.Sprintf("%q", s.m.Summary.Embedding.Prompts[q.Prompt]))
		out = append(out, p.String())
	}
	out = append(out, blankRow(w))
	if !embedAnswered(q, s.t) {
		return fitRows(append(out, s.history(w, q.ID)...), blankRow(w), h), hits
	}

	qv := q.Values()
	// The query's vector fills in left to right as the answer lands.
	sv := newLine(th, w)
	sv.add(th.dim, "  vec ")
	cells := downsample(qv, max(0, sv.left()))
	scale := 0.0
	for _, x := range cells {
		scale = math.Max(scale, math.Abs(x))
	}
	shown := int(math.Round(grow(q.AnsweredAt, s.t) * float64(len(cells))))
	sv.used += shown
	sv.b.WriteString(vectorStrip(th, cells[:shown], scale))
	sv.add(th.darkFill, repeat('·', len(cells)-shown))
	out = append(out[:len(out)-1], sv.String(), blankRow(w))

	ranked := s.rank(qv, 0, false)
	out = append(out, sectionRow(th, w, fmt.Sprintf("TOP %d", sceneTopK)))
	barW := min(12, max(4, w/5))
	for i, hh := range ranked[:min(sceneTopK, len(ranked))] {
		at := q.AnsweredAt + time.Duration(i)*sceneStagger
		if s.t < at {
			out = append(out, blankRow(w))
			continue
		}
		d := s.docs[hh.doc]
		hits[hh.doc] = i + 1
		good := d.rec.ID == q.Expect
		r := newLine(th, w)
		st := th.text
		mark := "  "
		if good {
			st, mark = th.accentBold, "✓ "
		}
		if fading(at, s.t) {
			st = th.dim
		}
		// Slide in from the right: the row starts two cells over.
		r.space(int(math.Round(2 * (1 - grow(at, s.t)))))
		r.add(th.dim, fmt.Sprintf("%d ", i+1))
		// The bar grows to its score; painted cell by cell, so pre-measured.
		bw := min(barW, r.left())
		r.b.WriteString(eighthBar(th, (hh.score-0.3)/0.7*grow(at, s.t), bw, good, ' '))
		r.used += bw
		r.add(st, fmt.Sprintf(" %.3f ", hh.score*grow(at, s.t)))
		r.add(st, mark)
		r.add(s.topicStyle(d.rec.Topic), "● ")
		r.addTrunc(st, truncate(oneLine(d.rec.Input), r.left()))
		out = append(out, r.String())
	}
	vAt := q.AnsweredAt + sceneTopK*sceneStagger
	if s.t < vAt {
		return fitRows(out, blankRow(w), h), hits
	}
	out = append(out, blankRow(w))
	out = append(out, s.verdict(w, q, qv, ranked, vAt)...)
	out = append(out, blankRow(w))
	out = append(out, s.history(w, q.ID)...)
	return fitRows(out, blankRow(w), h), hits
}

// verdict is the expected doc's rank three ways: as ranked, without the
// task prompts, and at the MRL truncations.
func (s *scene) verdict(w int, q tape.EmbeddingRecord, qv []float32, ranked []hit, from time.Duration) []string {
	th := s.th
	if q.Expect == "" {
		return nil
	}
	// Each figure comes up in turn, from dim, a beat after the one before.
	k := 0
	place := func(l *lineBuf, r int) {
		at := from + time.Duration(k)*sceneStagger
		k++
		if s.t < at {
			l.add(th.darkFill, "··")
			return
		}
		st := th.accentBold
		if r != 1 {
			st = th.warn
		}
		if fading(at, s.t) {
			st = th.dim
		}
		if r == 0 {
			l.add(th.dim, "–")
			return
		}
		l.add(st, fmt.Sprintf("#%d", r))
	}
	var out []string
	p := newLine(th, w)
	p.add(th.dim, "PROMPTS ")
	if s.prompted() {
		bare := s.rank(s.bareQ[q.ID], 0, true)
		p.add(th.dim, "with ")
		place(p, s.rankOf(ranked, q.Expect))
		p.add(th.dim, " "+margin(s, ranked, q.Expect, grow(from, s.t)))
		p.add(th.dim, "  bare ")
		place(p, s.rankOf(bare, q.Expect))
		p.add(th.dim, " "+margin(s, bare, q.Expect, grow(from+sceneStagger, s.t)))
	} else {
		p.add(th.dim, "none sent (--no-prompts)")
	}
	out = append(out, p.String())
	m := newLine(th, w)
	m.add(th.dim, "MRL     ")
	top1 := ""
	stable := true
	for i, d := range sceneMRL {
		hs := s.rank(qv, d, false)
		if i > 0 {
			m.add(th.dim, " · ")
		}
		m.add(th.dim, fmt.Sprintf("%d ", d))
		place(m, s.rankOf(hs, q.Expect))
		if len(hs) > 0 {
			id := s.docs[hs[0].doc].rec.ID
			if top1 == "" {
				top1 = id
			} else if id != top1 {
				stable = false
			}
		}
	}
	out = append(out, m.String())
	st := newLine(th, w)
	st.space(8)
	if s.t >= from+time.Duration(k)*sceneStagger {
		if stable {
			st.add(th.dim, "top-1 the same at every size")
		} else {
			st.add(th.warn, "top-1 changes with the size")
		}
	}
	return append(out, st.String())
}

// history is one line per query answered before current.
func (s *scene) history(w int, current string) []string {
	th := s.th
	var out []string
	for _, q := range s.queries {
		if q.ID == current || !embedAnswered(q, s.t) {
			continue
		}
		if len(out) == 0 {
			out = append(out, sectionRow(th, w, "EARLIER"))
		}
		ranked := s.rank(q.Values(), 0, false)
		r := s.rankOf(ranked, q.Expect)
		l := newLine(th, w)
		if r == 1 {
			l.add(th.accentBold, "✓ ")
		} else {
			l.add(th.warn, "✗ ")
		}
		mrl := make([]string, 0, len(sceneMRL))
		for _, d := range sceneMRL {
			mrl = append(mrl, fmt.Sprintf("#%d", s.rankOf(s.rank(q.Values(), d, false), q.Expect)))
		}
		tail := fmt.Sprintf("  #%d · bare #%d · %s", r, s.rankOf(s.rank(s.bareQ[q.ID], 0, true), q.Expect), strings.Join(mrl, "/"))
		l.addTrunc(th.text, truncate(q.Input, max(0, l.left()-width(tail))))
		l.gapTo(width(tail))
		l.add(th.dim, tail)
		out = append(out, l.String())
	}
	return out
}

// ---- the outro: clusters ----------------------------------------------------

func (s *scene) clusters(w, h int) []string {
	th := s.th
	docs := append([]sceneDoc(nil), s.docs...)
	order := s.topics()
	idx := map[string]int{}
	for i, tp := range order {
		idx[tp] = i
	}
	sort.SliceStable(docs, func(a, b int) bool { return idx[docs[a].rec.Topic] < idx[docs[b].rec.Topic] })
	n := len(docs)
	lo, hi := 1.0, -1.0
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j {
				c := tape.Cosine(docs[i].vec, docs[j].vec)
				lo, hi = math.Min(lo, c), math.Max(hi, c)
			}
		}
	}
	const labelW, cellW = 18, 3
	// Topic blocks fade in one after another: a cell is up once both its
	// row's and its column's topics are.
	outro := s.m.Summary.Embedding.OutroAt
	upAt := func(d sceneDoc) time.Duration { return outro + time.Duration(idx[d.rec.Topic])*clusterLag }
	out := []string{sectionRow(th, w, "CLUSTERS  doc × doc cosine ×100, ordered by topic")}
	for i := 0; i < n && len(out) < h-2; i++ {
		l := newLine(th, w)
		if s.t < upAt(docs[i]) {
			out = append(out, l.String())
			continue
		}
		l.add(s.topicStyle(docs[i].rec.Topic), "● ")
		l.add(th.text, fmt.Sprintf("%-*s", labelW-2, truncate(docs[i].rec.ID+" "+docs[i].rec.Topic, labelW-3)))
		for j := 0; j < n && l.left() >= cellW; j++ {
			c := tape.Cosine(docs[i].vec, docs[j].vec)
			cell := fmt.Sprintf("%*d", cellW, int(math.Round(c*100)))
			at := max(upAt(docs[i]), upAt(docs[j]))
			if s.t < at {
				l.add(th.darkFill, fmt.Sprintf("%*s", cellW, "·"))
				continue
			}
			if fading(at, s.t) {
				l.add(th.dim, cell)
				continue
			}
			if i == j {
				l.add(th.darkFill, fmt.Sprintf("%*s", cellW, "▪▪"))
				continue
			}
			level := 1 + int(math.Min(embedLevels-1, (c-lo)/math.Max(1e-9, hi-lo)*embedLevels))
			st := embedStyle(th, false, level)
			if docs[i].rec.Topic == docs[j].rec.Topic {
				st = th.accentBold
			}
			l.add(st, cell)
		}
		out = append(out, l.String())
	}
	// Recall line.
	with, bare, n1 := 0, 0, 0
	for _, q := range s.queries {
		if q.Expect == "" || !embedAnswered(q, s.t) {
			continue
		}
		n1++
		if s.rankOf(s.rank(q.Values(), 0, false), q.Expect) == 1 {
			with++
		}
		if s.rankOf(s.rank(s.bareQ[q.ID], 0, true), q.Expect) == 1 {
			bare++
		}
	}
	out = append(out, blankRow(w))
	l := newLine(th, w)
	if s.t < outro+time.Duration(len(order))*clusterLag {
		return fitRows(out, blankRow(w), h)
	}
	l.add(th.dim, "same-topic pairs bold · ")
	l.add(th.accentBold, fmt.Sprintf("recall@1 %d/%d", with, n1))
	if s.prompted() {
		l.add(th.dim, fmt.Sprintf(" with task prompts · %d/%d bare", bare, n1))
	}
	out = append(out, l.String())
	return fitRows(out, blankRow(w), h)
}

// sceneBody is the scene's body rows.
func sceneBody(m Model, th Theme, t time.Duration, bw, bodyH int) []string {
	s := newScene(m, th, t)
	// The machine pane on the right is the record screen's own (rightPane):
	// speed, placement, memory, and the resource graphs from the samples.
	paneW := rightWidth(bw + 4)
	mainW := bw - paneW - 3
	pane := rightPane(m, th, t, paneW, bodyH, false)
	sep := th.paint(th.dim, " │ ")
	var main []string
	if s.outro() {
		main = s.clusters(mainW, bodyH)
	} else {
		lw := mainW * 45 / 100
		rw := mainW - lw - 3
		right, hits := s.searchColumn(rw, bodyH)
		left := s.indexColumn(lw, bodyH, hits)
		for i := 0; i < bodyH; i++ {
			main = append(main, left[i]+sep+right[i])
		}
	}
	rows := make([]string, bodyH)
	for i := range rows {
		rows[i] = main[i] + sep + pane[i]
	}
	return rows
}

// margin is the expected doc's lead over the best other doc ("+0.08"), or
// how far behind the top it is ("-0.03").
// The figure counts up from zero as f goes 0..1.
func margin(s *scene, hs []hit, id string, f float64) string {
	r := s.rankOf(hs, id)
	if r == 0 || len(hs) < 2 {
		return ""
	}
	if r == 1 {
		return "(+" + strings.TrimPrefix(fmt.Sprintf("%.3f", (hs[0].score-hs[1].score)*f), "0") + ")"
	}
	return "(-" + strings.TrimPrefix(fmt.Sprintf("%.3f", (hs[0].score-hs[r-1].score)*f), "0") + ")"
}
