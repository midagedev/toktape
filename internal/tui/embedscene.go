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
)

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
			l.add(th.accent, spinnerAt(s.t)+" ")
			l.add(th.dim, fmt.Sprintf("%-7s", r.ID))
			l.addTrunc(th.dim, truncate(oneLine(r.Input), l.left()))
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
	// Typing: the text appears over ShownAt..SentAt.
	typed := []rune(q.Input)
	if q.SentAt > q.ShownAt && s.t < q.SentAt {
		f := float64(s.t-q.ShownAt) / float64(q.SentAt-q.ShownAt)
		typed = typed[:int(math.Max(0, math.Min(1, f))*float64(len(typed)))]
	}
	l := newLine(th, w)
	l.add(th.accentBold, "› ")
	l.addTrunc(th.text, truncate(string(typed), max(0, l.left()-12)))
	switch {
	case s.t < q.SentAt:
		l.add(th.accent, "▌")
	case !embedAnswered(q, s.t):
		l.add(th.accent, " "+spinnerAt(s.t)+" embedding")
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
	ranked := s.rank(qv, 0, false)
	out = append(out, sectionRow(th, w, fmt.Sprintf("TOP %d", sceneTopK)))
	barW := 12
	for i, hh := range ranked[:min(sceneTopK, len(ranked))] {
		if s.t < q.AnsweredAt+time.Duration(i)*sceneStagger {
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
		r.add(th.dim, fmt.Sprintf("%d ", i+1))
		// The bar is painted cell by cell, so it goes in pre-measured.
		r.b.WriteString(eighthBar(th, (hh.score-0.3)/0.7, barW, good, ' '))
		r.used += barW
		r.add(st, fmt.Sprintf(" %.3f ", hh.score))
		r.add(st, mark)
		r.add(s.topicStyle(d.rec.Topic), "● ")
		r.addTrunc(st, truncate(oneLine(d.rec.Input), r.left()))
		out = append(out, r.String())
	}
	if s.t < q.AnsweredAt+sceneTopK*sceneStagger {
		return fitRows(out, blankRow(w), h), hits
	}
	out = append(out, blankRow(w))
	out = append(out, s.verdict(w, q, qv, ranked)...)
	out = append(out, blankRow(w))
	out = append(out, s.history(w, q.ID)...)
	return fitRows(out, blankRow(w), h), hits
}

// verdict is the expected doc's rank three ways: as ranked, without the
// task prompts, and at the MRL truncations.
func (s *scene) verdict(w int, q tape.EmbeddingRecord, qv []float32, ranked []hit) []string {
	th := s.th
	if q.Expect == "" {
		return nil
	}
	place := func(l *lineBuf, r int) {
		st := th.accentBold
		if r != 1 {
			st = th.warn
		}
		if r == 0 {
			l.add(th.dim, "–")
			return
		}
		l.add(st, fmt.Sprintf("#%d", r))
	}
	var out []string
	p := newLine(th, w)
	p.add(th.dim, "PROMPTS   ")
	if s.prompted() {
		bare := s.rank(s.bareQ[q.ID], 0, true)
		p.add(th.dim, "with ")
		place(p, s.rankOf(ranked, q.Expect))
		p.add(th.dim, " "+margin(s, ranked, q.Expect))
		p.add(th.dim, "   bare ")
		place(p, s.rankOf(bare, q.Expect))
		p.add(th.dim, " "+margin(s, bare, q.Expect))
	} else {
		p.add(th.dim, "none sent (--no-prompts)")
	}
	out = append(out, p.String())
	m := newLine(th, w)
	m.add(th.dim, "MRL       ")
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
	if stable {
		m.add(th.dim, "   top-1 stable")
	} else {
		m.add(th.warn, "   top-1 moves")
	}
	return append(out, m.String())
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
	const labelW, cellW = 18, 4
	out := []string{sectionRow(th, w, "CLUSTERS  doc × doc cosine, ordered by topic")}
	for i := 0; i < n && len(out) < h-2; i++ {
		l := newLine(th, w)
		l.add(s.topicStyle(docs[i].rec.Topic), "● ")
		l.add(th.text, fmt.Sprintf("%-*s", labelW-2, truncate(docs[i].rec.ID+" "+docs[i].rec.Topic, labelW-3)))
		for j := 0; j < n && l.left() >= cellW; j++ {
			c := tape.Cosine(docs[i].vec, docs[j].vec)
			cell := fmt.Sprintf("%*s", cellW, strings.TrimPrefix(fmt.Sprintf("%.2f", c), "0"))
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
	if s.outro() {
		return s.clusters(bw, bodyH)
	}
	lw := bw * 46 / 100
	rw := bw - lw - 3
	right, hits := s.searchColumn(rw, bodyH)
	left := s.indexColumn(lw, bodyH, hits)
	sep := th.paint(th.dim, " │ ")
	rows := make([]string, bodyH)
	for i := range rows {
		rows[i] = left[i] + sep + right[i]
	}
	return rows
}

// margin is the expected doc's lead over the best other doc ("+0.08"), or
// how far behind the top it is ("-0.03").
func margin(s *scene, hs []hit, id string) string {
	r := s.rankOf(hs, id)
	if r == 0 || len(hs) < 2 {
		return ""
	}
	if r == 1 {
		return fmt.Sprintf("(+%.3f)", hs[0].score-hs[1].score)
	}
	return fmt.Sprintf("(%.3f)", hs[r-1].score-hs[0].score)
}
