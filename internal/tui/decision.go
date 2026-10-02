package tui

// The decision screen (TTP-192, 2026-10-02): what View draws for a tape whose
// Summary.IsDecision() is true. A decision model reads a state and answers
// typed questions with one probability per option, in one prefill pass, so
// there is nothing to stream, no tile of tokens and no tok/s anywhere on this
// screen: the unit is the request, and the figure is how long it took.
//
// Two phases, as the recording has them. The showcase is a paced pass, one
// request at a time; each is a case block in a feed, newest at the bottom, so
// several question/answer pairs are on screen together (user, 2026-10-02:
// "answers come one after another"). The burst is the same suite back to back,
// ~40 ms a request, which no block could be read at: it becomes a table of the
// suite that fills in as answers land.
//
// Everything here is a pure function of (m, t). Every figure that moves does so
// through ease() from the answer's own AnsweredAt, never from a clock.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

const (
	// caseBlockH is one case of the feed: the id row, a four-row tile band
	// and a blank separator.
	caseBlockH    = 6
	questionTileH = 4
	// maxTilesPerCase is the suite's widest case; a tile's width is a third
	// of the body whatever the case has, so a two-question case leaves a
	// third of its band empty rather than stretching its tiles.
	maxTilesPerCase = 3

	// burstRateSettle is how much of the burst window must have passed before
	// the footer shows a request rate.
	burstRateSettle = 2 * time.Second
	// latestTick is how often the burst's LATEST ANSWERS blocks change: a block
	// is readable for half a second, never a 40 ms flicker.
	latestTick = 500 * time.Millisecond

	// tableW is the burst table's columns: case, input, latency, p50, n.
	tableCase, tableTok, tableSpark, tableP50, tableN = 12, 6, 16, 6, 3
	tableW                                            = tableCase + tableTok + tableSpark + tableP50 + tableN + 8
)

// decisionModel fills m for a decision tape. Records sent after at are not in
// the model, the same cut ModelAt makes for tokens; whether an answer has
// arrived is the view's question, asked of t.
func decisionModel(m *Model, tp *tape.Tape, at time.Duration) {
	for _, r := range tp.Decisions {
		if r.SentAt <= at {
			m.Decisions = append(m.Decisions, r)
		}
	}
	if len(tp.Decisions) > 0 {
		m.RunStart, m.runStarted = tp.Decisions[0].SentAt, true
	}
	end := time.Duration(0)
	for _, r := range tp.Decisions {
		end = max(end, r.AnsweredAt)
		if r.Error != "" {
			end = max(end, r.SentAt)
		}
	}
	m.RunEnd = end
	m.Done = len(tp.Decisions) > 0 && at >= end
}

// answeredBy reports whether r's answer is in by t.
func answeredBy(r tape.DecisionRecord, t time.Duration) bool {
	return r.Error == "" && r.AnsweredAt > 0 && r.AnsweredAt <= t
}

// fmtLat prints a request latency: one decimal under 100 ms, an integer from
// there, "?" when it was never measured.
func fmtLat(ms float64) string {
	switch {
	case ms <= 0:
		return unknown
	case ms < 100:
		return strconv.FormatFloat(ms, 'f', 1, 64)
	}
	return strconv.FormatFloat(ms, 'f', 0, 64)
}

// fmtThousands is n with a comma every three digits.
func fmtThousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func fmtTok(n int) string {
	if n <= 0 {
		return unknown
	}
	return fmtThousands(n)
}

func fmtProb(p float64) string { return strconv.FormatFloat(p, 'f', 2, 64) }

// decisionStyles are the two bar styles the theme has no slot for: a partial
// cell is an eighth block, and the part of the cell it does not fill has to be
// the track's colour, which only a background can say. They are built the way
// ColourTheme builds its own, and a plain theme gets the zero value.
type decisionStyleSet struct{ accent, muted style }

var (
	decStyleOnce sync.Once
	decStyles    decisionStyleSet
)

func decisionStyles(th Theme) decisionStyleSet {
	if !th.colour {
		return decisionStyleSet{}
	}
	decStyleOnce.Do(func() {
		r := lipgloss.NewRenderer(io.Discard)
		r.SetColorProfile(termenv.TrueColor)
		r.SetHasDarkBackground(true)
		on := func(fg string) style {
			return seqOf(r.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(colDarkFill)))
		}
		decStyles = decisionStyleSet{accent: on(colAccent), muted: on(colAccentMuted)}
	})
	return decStyles
}

// eighthBar is a w-cell bar filled to frac in eighth blocks over a dark track.
// A fraction above zero lights at least one eighth, so "a little" is never
// "nothing" (barCells' rule). rest is the track's glyph.
func eighthBar(th Theme, frac float64, w int, top bool, rest rune) string {
	if w <= 0 {
		return ""
	}
	eighths := 0
	if frac > 0 && !math.IsNaN(frac) {
		eighths = max(1, int(math.Round(math.Min(frac, 1)*float64(w*8))))
	}
	full, part := eighths/8, eighths%8
	st, edge := th.accentMuted, decisionStyles(th).muted
	if top {
		st, edge = th.accent, decisionStyles(th).accent
	}
	var b strings.Builder
	b.WriteString(th.paint(st, repeat('█', full)))
	used := full
	if part > 0 {
		b.WriteString(th.paint(edge, string([]rune("▏▎▍▌▋▊▉")[part-1])))
		used++
	}
	b.WriteString(th.paint(th.darkFill, repeat(rest, w-used)))
	return b.String()
}

// stateLine is the request's "state" as one line: text as it is (its lines
// joined with ↵), a JSON state as compact "key: value, key: value" in the
// request's own key order.
func stateLine(req json.RawMessage) string {
	var env struct {
		State json.RawMessage `json:"state"`
	}
	if json.Unmarshal(req, &env) != nil || len(env.State) == 0 {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader(env.State))
	dec.UseNumber()
	s, err := flattenJSON(dec)
	if err != nil {
		return strings.TrimSpace(string(env.State))
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ↵ ")), " ")
}

func flattenJSON(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	switch v := tok.(type) {
	case json.Delim:
		var parts []string
		for dec.More() {
			if v == '{' {
				k, err := dec.Token()
				if err != nil {
					return "", err
				}
				val, err := flattenJSON(dec)
				if err != nil {
					return "", err
				}
				parts = append(parts, fmt.Sprint(k)+": "+val)
			} else {
				val, err := flattenJSON(dec)
				if err != nil {
					return "", err
				}
				parts = append(parts, val)
			}
		}
		if _, err := dec.Token(); err != nil {
			return "", err
		}
		if v == '{' {
			return "{" + strings.Join(parts, ", ") + "}", nil
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case nil:
		return "null", nil
	default:
		return fmt.Sprint(v), nil
	}
}

// topLevelState is stateLine without the outer braces of an object state: the
// block header reads "key: value, key: value", not "{key: value}".
func topLevelState(req json.RawMessage) string {
	s := stateLine(req)
	var env struct {
		State json.RawMessage `json:"state"`
	}
	if json.Unmarshal(req, &env) == nil && len(env.State) > 0 && env.State[0] == '{' &&
		strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		return s[1 : len(s)-1]
	}
	return s
}

// ---- the figures ----------------------------------------------------------

// heroAt is the headline figure over the answers that were in by at: the cold
// request's latency until a warm one has answered, then the warm p50. The cold
// request is the run's first (Index 0) and is never part of a warm figure.
func heroAt(recs []tape.DecisionRecord, at time.Duration) (v float64, warm, ok bool) {
	var lat []float64
	for _, r := range recs {
		if !answeredBy(r, at) {
			continue
		}
		if r.Index == 0 {
			v, ok = msOf(r.Latency()), true
			continue
		}
		lat = append(lat, msOf(r.Latency()))
	}
	if len(lat) > 0 {
		return percentile(lat, 0.5), true, true
	}
	return v, false, ok
}

// decisionHero is the figure, eased from the previous reading when the newest
// answer changed it, and its label.
func decisionHero(m Model, t time.Duration) (fig, label string) {
	var last time.Duration
	for _, r := range m.Decisions {
		if answeredBy(r, t) {
			last = max(last, r.AnsweredAt)
		}
	}
	cur, warm, ok := heroAt(m.Decisions, t)
	if !ok {
		return unknown, ""
	}
	prev, _, _ := heroAt(m.Decisions, last-1)
	label = "cold · first request"
	if warm {
		label = card.DecisionHeroCaption
	}
	return fmtLat(ease(prev, cur, last, t)), label
}

func decisionHeroRows(m Model, th Theme, t time.Duration, bw int) []string {
	fig, label := decisionHero(m, t)
	glyphs := bigFigure(fig)
	var info []string
	if d := m.Summary.Decision; d != nil && d.Endpoint != "" {
		info = append(info, "POST "+d.Endpoint)
	}
	info = append(info, "prefill only · no tokens generated")
	answered := 0
	for _, r := range m.Decisions {
		if answeredBy(r, t) {
			answered++
		}
	}
	if d := m.Summary.Decision; d != nil && d.Requests > 0 {
		info = append(info, fmt.Sprintf("%d of %d requests answered", answered, d.Requests))
	}
	// While the end card is up the modal's figure is the one lit thing on the
	// screen (lead, 2026-10-02): the token screen leaves its hero lit behind the
	// modal, which here made two accent figures of the same size compete, so the
	// hero steps back to dim for as long as the card is shown.
	figSt := th.accentBold
	if m.Mode == ModeCard {
		figSt = th.dim
	}
	// The unit sits dim beside the bottom row, the modal's own arrangement.
	const unit = " ms"
	out := make([]string, 0, bigRows+2)
	for k, row := range glyphs {
		l := newLine(th, bw)
		if k >= 1 && k-1 < len(info) {
			l.add(th.dim, info[k-1])
		}
		l.gapTo(width(row) + width(unit))
		l.add(figSt, row)
		if k == bigRows-1 && fig != unknown {
			l.add(th.dim, unit)
		}
		out = append(out, l.String())
	}
	l := newLine(th, bw)
	l.gapTo(width(label))
	l.add(th.dim, label)
	out = append(out, l.String(), blankRow(bw))
	return out
}

// ---- the showcase feed ----------------------------------------------------

// showcaseRecords are the paced records sent by t, in send order.
func showcaseRecords(m Model, t time.Duration) []tape.DecisionRecord {
	var out []tape.DecisionRecord
	for _, r := range m.Decisions {
		if r.Phase == tape.DecisionPhaseShowcase && r.SentAt <= t {
			out = append(out, r)
		}
	}
	return out
}

// inBurst reports whether t is at or past the first burst request.
func inBurst(m Model, t time.Duration) bool {
	for _, r := range m.Decisions {
		if r.Phase == tape.DecisionPhaseBurst && r.SentAt <= t {
			return true
		}
	}
	return false
}

// decisionFeed draws room rows of case blocks. Blocks stack from the top until
// the feed is full, then the newest sits at the bottom and the older ones are
// pushed up: a new block slides in over easeDur from its SentAt.
func decisionFeed(m Model, th Theme, t time.Duration, bw, room int) []string {
	recs := showcaseRecords(m, t)
	blank := blankRow(bw)
	if len(recs) == 0 {
		l := newLine(th, bw)
		l.add(th.accent, spinnerAt(t)+" ")
		l.add(th.dim, "waiting for the first request")
		return fitRows([]string{l.String()}, blank, room)
	}
	var rows []string
	for _, r := range recs {
		rows = append(rows, caseBlock(m, th, t, r, bw)...)
	}
	start := max(0, len(rows)-room)
	if start > 0 {
		newest := recs[len(recs)-1].SentAt
		off := int(math.Round((1 - easeOutCubic(float64(t-newest)/float64(easeDur))) * caseBlockH))
		start = max(0, start-off)
	}
	return fitRows(rows[start:], blank, room)
}

// caseBlock is one case: the id row, the tile band and the separator.
func caseBlock(m Model, th Theme, t time.Duration, r tape.DecisionRecord, bw int) []string {
	out := make([]string, 0, caseBlockH)

	var right []struct {
		st style
		s  string
	}
	add := func(st style, s string) {
		right = append(right, struct {
			st style
			s  string
		}{st, s})
	}
	add(th.dim, "input "+fmtTok(r.InputTokens)+" tok · ")
	switch {
	case answeredBy(r, t):
		add(th.accentBold, fmtLat(msOf(r.Latency()))+" ms")
	case r.Error != "":
		add(th.bad, "✗ failed")
	default:
		add(th.accent, spinnerAt(t)+" deciding")
	}
	rw := 0
	for _, p := range right {
		rw += width(p.s)
	}
	l := newLine(th, bw)
	l.add(th.accentBold, r.CaseID)
	l.space(2)
	l.addTrunc(th.dim, truncate(topLevelState(r.Request), max(0, l.left()-rw-2)))
	l.gapTo(rw)
	for _, p := range right {
		l.add(p.st, p.s)
	}
	out = append(out, l.String())

	tw := (bw - 2*(maxTilesPerCase-1)) / maxTilesPerCase
	var tiles [][]string
	for i, q := range r.Questions {
		var a *tape.DecisionAnswer
		if answeredBy(r, t) && i < len(r.Answers) {
			a = &r.Answers[i]
		}
		tiles = append(tiles, questionTile(th, t, r.AnsweredAt, q, a, tw))
	}
	for row := 0; row < questionTileH; row++ {
		var b strings.Builder
		used := 0
		for i, tile := range tiles {
			if i > 0 {
				b.WriteString("  ")
				used += 2
			}
			b.WriteString(tile[row])
			used += tw
		}
		b.WriteString(strings.Repeat(" ", max(0, bw-used)))
		out = append(out, b.String())
	}
	return append(out, blankRow(bw))
}

// questionTile is one question: a rounded box, its id and type on the top
// border, an answer row and a shape row. a is nil until the answer is in.
func questionTile(th Theme, t, answeredAt time.Duration, q tape.DecisionQuestion, a *tape.DecisionAnswer, tw int) []string {
	iw := tw - 4
	top := newLine(th, tw)
	top.add(th.dim, "╭─ ")
	top.addTrunc(th.textMid, truncate(q.ID, max(1, tw-10-width(q.Type))))
	top.add(th.dim, " · "+q.Type+" ")
	top.add(th.dim, repeat('─', top.left()-1))
	top.add(th.dim, "╮")

	rowA, rowB := newLine(th, iw), newLine(th, iw)
	rest := '█'
	if q.Type == tape.DecisionNoul {
		rest = '░'
	}
	if a == nil {
		rowA.add(th.dim, spinnerAt(t)+" deciding")
		rowB.add(th.darkFill, repeat(rest, iw))
	} else {
		e := ease(0, 1, answeredAt, t)
		switch q.Type {
		case tape.DecisionScore:
			scoreRows(th, e, q, a, rowA, rowB, iw)
		case tape.DecisionNoul:
			noulRows(th, e, a, rowA, rowB, iw)
		default:
			choiceRows(th, e, q, a, rowA, rowB, iw)
		}
	}
	box := func(inner string) string {
		return th.paint(th.dim, "│") + " " + inner + " " + th.paint(th.dim, "│")
	}
	bottom := th.paint(th.dim, "╰"+repeat('─', tw-2)+"╯")
	return []string{top.String(), box(rowA.String()), box(rowB.String()), bottom}
}

// topOption is the index of the largest probability (the first on a tie) and
// the runner-up's, -1 when there is none.
func topOption(ps []tape.DecisionProb) (top, second int) {
	top, second = -1, -1
	for i, p := range ps {
		switch {
		case top < 0 || p.P > ps[top].P:
			second, top = top, i
		case second < 0 || p.P > ps[second].P:
			second = i
		}
	}
	return top, second
}

// rightAligned puts value at the right end of l after left has been added.
func rightAligned(l *lineBuf, th Theme, st style, value string) {
	l.gapTo(width(value))
	l.add(st, value)
}

func choiceRows(th Theme, e float64, q tape.DecisionQuestion, a *tape.DecisionAnswer, rowA, rowB *lineBuf, iw int) {
	top, second := topOption(a.Probabilities)
	if top < 0 {
		rowA.addTrunc(th.accentBold, "▶ "+orUnknown(a.Choice))
		rowB.add(th.darkFill, repeat('█', iw))
		return
	}
	p := a.Probabilities[top].P
	val := fmtProb(p * e)
	rowA.add(th.accentBold, "▶ ")
	rowA.addTrunc(th.accentBold, truncate(a.Probabilities[top].Key, max(1, iw-3-width(val))))
	rightAligned(rowA, th, th.accentBold, val)

	runner := ""
	if second >= 0 {
		runner = truncate(a.Probabilities[second].Key, 12) + " " + fmtProb(a.Probabilities[second].P*e)
	}
	barW := iw
	if runner != "" {
		barW = max(4, iw-width(runner)-1)
	}
	rowB.b.WriteString(eighthBar(th, p*e, barW, true, '█'))
	rowB.used = barW
	if runner != "" {
		rowB.space(1)
		rowB.add(th.dim, runner)
	}
}

func scoreRows(th Theme, e float64, q tape.DecisionQuestion, a *tape.DecisionAnswer, rowA, rowB *lineBuf, iw int) {
	top, _ := topOption(a.Probabilities)
	val := strconv.FormatFloat(a.Score*e, 'f', 2, 64)
	text := ""
	if top >= 0 && top < len(q.Options) {
		text = q.Options[top].Text
	}
	if text == "" && top >= 0 {
		text = a.Probabilities[top].Key
	}
	rowA.add(th.accentBold, "● ")
	rowA.addTrunc(th.accentBold, truncate(text, max(1, iw-3-width(val))))
	rightAligned(rowA, th, th.accentBold, val)

	levels := []rune("▁▂▃▄▅▆▇█")
	b := rowB
	for i, p := range a.Probabilities {
		if i > 0 {
			b.space(1)
		}
		g := string(levels[int(math.Round(math.Min(p.P*e, 1)*7))])
		st := th.accentMuted
		if i == top {
			st = th.accent
		}
		b.add(st, g+g)
	}
	if n := len(q.Options); n >= 2 {
		span := q.Options[0].Text + " → " + q.Options[n-1].Text
		if room := b.left() - 2; room >= 8 {
			b.space(2)
			b.addTrunc(th.dim, truncate(span, room))
		}
	}
}

func noulRows(th Theme, e float64, a *tape.DecisionAnswer, rowA, rowB *lineBuf, iw int) {
	if a.Noul == nil {
		rowA.add(th.dim, "? no answer")
		rowB.add(th.darkFill, repeat('░', iw))
		return
	}
	p := *a.Noul
	if p >= 0.5 {
		rowA.add(th.accentBold, "✓ YES")
	} else {
		rowA.add(th.accentBold, "✗ NO")
	}
	rightAligned(rowA, th, th.accentBold, "p "+fmtProb(p*e))
	rowB.b.WriteString(eighthBar(th, p*e, iw, true, '░'))
	rowB.used = iw
}

// ---- the burst table ------------------------------------------------------

// caseStats is one suite row as of t.
type caseStats struct {
	id       string
	tok      int
	lat      []float64 // warm answered latencies, in answer order
	p50, pre float64   // p50 now, and before the newest answer
	since    time.Duration
}

func suiteAt(m Model, t time.Duration) []caseStats {
	var out []caseStats
	idx := map[string]int{}
	recs := append([]tape.DecisionRecord(nil), m.Decisions...)
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].AnsweredAt < recs[j].AnsweredAt })
	for _, r := range m.Decisions {
		if _, ok := idx[r.CaseID]; !ok && r.SentAt <= t {
			idx[r.CaseID] = len(out)
			out = append(out, caseStats{id: r.CaseID})
		}
	}
	for _, r := range recs {
		i, ok := idx[r.CaseID]
		if !ok {
			continue
		}
		if r.InputTokens > 0 {
			out[i].tok = r.InputTokens
		}
		if r.Index == 0 || !answeredBy(r, t) {
			continue
		}
		out[i].lat = append(out[i].lat, msOf(r.Latency()))
		out[i].since = r.AnsweredAt
	}
	for i := range out {
		c := &out[i]
		c.p50 = percentile(c.lat, 0.5)
		if n := len(c.lat); n > 0 {
			c.pre = percentile(c.lat[:n-1], 0.5)
		}
	}
	return out
}

func decisionTable(m Model, th Theme, t time.Duration, bw int) []string {
	cases := suiteAt(m, t)
	gmax := 0.0
	for _, c := range cases {
		for _, v := range c.lat {
			gmax = max(gmax, v)
		}
	}
	levels := []rune("▁▂▃▄▅▆▇█")
	hdr := newLine(th, tableW)
	hdr.add(th.dim, pad("case", tableCase)+"  "+padLeft("input", tableTok)+"  "+pad("latency", tableSpark)+"  "+padLeft("p50", tableP50)+"  "+padLeft("n", tableN))
	out := []string{hdr.String()}
	for _, c := range cases {
		l := newLine(th, tableW)
		l.add(th.text, pad(truncate(c.id, tableCase), tableCase))
		l.space(2)
		l.add(th.textMid, padLeft(fmtTok(c.tok), tableTok))
		l.space(2)
		vals := c.lat
		if len(vals) > tableSpark {
			vals = vals[len(vals)-tableSpark:]
		}
		// Left to right and scrolling once full, the newest cell on the right
		// of what there is: a row with few answers is short, not shifted.
		for i, v := range vals {
			idx := 0
			if gmax > 0 {
				idx = int(math.Round(v / gmax * 7))
			}
			st := th.accentMuted
			if i == len(vals)-1 {
				st = th.accent
			}
			l.add(st, string(levels[idx]))
		}
		l.space(tableSpark - len(vals) + 2)
		p50 := "?"
		if len(c.lat) > 0 {
			p50 = fmtLat(ease(c.pre, c.p50, c.since, t))
		}
		l.add(th.text, padLeft(p50, tableP50))
		l.space(2)
		l.add(th.textMid, padLeft(strconv.Itoa(len(c.lat)), tableN))
		out = append(out, l.String())
	}
	return out
}

// burstFigures are the footer's: answered of total, the warm p95, and the
// burst window's request rate so far.
func burstFigures(m Model, t time.Duration) (answered int, p95ms, rps float64) {
	var warm []float64
	var first, lastAns time.Duration = -1, 0
	n := 0
	for _, r := range m.Decisions {
		if !answeredBy(r, t) {
			continue
		}
		answered++
		if r.Index != 0 {
			warm = append(warm, msOf(r.Latency()))
		}
		if r.Phase == tape.DecisionPhaseBurst {
			n++
			lastAns = max(lastAns, r.AnsweredAt)
		}
	}
	for _, r := range m.Decisions {
		if r.Phase == tape.DecisionPhaseBurst && (first < 0 || r.SentAt < first) {
			first = r.SentAt
		}
	}
	// The rate prints "?" until the burst window so far is long enough to be a
	// rate: over the first half second the same count read 26 req/s and
	// settled at 13 (lead, 2026-10-02).
	if n > 0 && lastAns-first >= burstRateSettle {
		rps = float64(n) / (lastAns - first).Seconds()
	}
	return answered, percentile(warm, 0.95), rps
}

func decisionBurst(m Model, th Theme, t time.Duration, bw, room int) []string {
	table := decisionTable(m, th, t, bw)
	answered, p95v, rps := burstFigures(m, t)
	total := answered
	if d := m.Summary.Decision; d != nil && d.Requests > 0 {
		total = d.Requests
	}
	rate := unknown
	if rps > 0 {
		rate = strconv.FormatFloat(rps, 'f', 1, 64)
	}
	foot := newLine(th, tableW)
	foot.add(th.dim, fmt.Sprintf("%d/%d requests · p95 %s ms · %s req/s", answered, total, fmtLat(p95v), rate))
	left := append(table, blankRow(tableW), foot.String())

	rw := bw - tableW - 3
	right := burstSide(m, th, t, rw)
	var out []string
	for i := 0; i < max(len(left), len(right)); i++ {
		l, r := blankRow(tableW), blankRow(max(rw, 0))
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, l+"   "+r)
	}
	// Not under the end card: a block half covered by the modal reads as clipped.
	if m.Mode != ModeCard {
		out = append(out, latestAnswers(m, th, t, bw, room-len(out))...)
	}
	return fitRows(out, blankRow(bw), room)
}

// sectionRow is a section title with a rule to the column's edge.
func sectionRow(th Theme, cw int, title string) string {
	l := newLine(th, cw)
	l.add(th.dim, title)
	l.space(1)
	l.add(th.dim, repeat('─', l.left()))
	return l.String()
}

// latestAnswers is the burst's lower half (2026-10-02): the newest answered
// burst requests as the showcase's own case blocks, so the half of the screen
// under the table is a place to read an actual question and answer instead of
// empty. At ~40 ms a request no block could be read as they land, so the
// content steps on a clock of its own: tick = floor((t - first burst send) /
// latestTick), the newest block is the latest request answered by the start of
// the tick, and the block above it is the one that was newest a tick before
// (of another case, when that one is the same). A
// tick's blocks are static (drawn settled, not re-eased), and everything is a
// function of t alone.
//
// It takes the two blocks if they fit in room, else one, else nothing: a
// block is never drawn clipped.
func latestAnswers(m Model, th Theme, t time.Duration, bw, room int) []string {
	const chrome = 2 // the blank row before the title, and the title
	n := 2
	for n > 0 && room < chrome+n*caseBlockH-1 {
		n--
	}
	if n == 0 {
		return nil
	}
	var answered []tape.DecisionRecord
	first := time.Duration(-1)
	for _, r := range m.Decisions {
		if r.Phase != tape.DecisionPhaseBurst {
			continue
		}
		if first < 0 || r.SentAt < first {
			first = r.SentAt
		}
		if r.Error == "" && r.AnsweredAt > 0 {
			answered = append(answered, r)
		}
	}
	sort.SliceStable(answered, func(i, j int) bool { return answered[i].AnsweredAt < answered[j].AnsweredAt })
	// newestBy is the latest request answered at or before at, -1 when none.
	newestBy := func(at time.Duration) int {
		return sort.Search(len(answered), func(i int) bool { return answered[i].AnsweredAt > at }) - 1
	}
	tick := int((t - first) / latestTick)
	cur := newestBy(first + time.Duration(tick)*latestTick)
	prev := newestBy(first + time.Duration(tick-1)*latestTick)
	if prev < 0 {
		prev = cur - 1 // the first ticks: the answer before the newest
	}
	// A pass over the suite is about one tick, so the two newest blocks are
	// often the same case a pass apart, which reads as a stalled screen: the
	// upper block steps back to the nearest answer of another case.
	for cur >= 0 && prev >= 0 && answered[prev].CaseID == answered[cur].CaseID {
		prev--
	}
	blank := blankRow(bw)
	rows := []string{blank, sectionRow(th, bw, "LATEST ANSWERS")}
	// Slots top to bottom, the newest last; a slot with no request yet (the
	// first ticks of the burst) stays blank so the rows below never move.
	slots := [][]string{fitRows(nil, blank, caseBlockH-1), fitRows(nil, blank, caseBlockH-1)}
	for k, i := range []int{prev, cur} {
		if i < 0 {
			continue
		}
		r := answered[i]
		// Drawn at the instant the answer has fully eased in, whatever t is.
		slots[k] = caseBlock(m, th, r.AnsweredAt+easeDur, r, bw)[:caseBlockH-1]
	}
	for k, b := range slots[2-n:] {
		if k > 0 {
			rows = append(rows, blank)
		}
		rows = append(rows, b...)
	}
	return rows
}

// burstSide is the burst's right column: the last requests' latencies as a
// scrolling sparkline, and the short and long warm p50 apart, because one long
// prompt would otherwise be a number nobody can place.
func burstSide(m Model, th Theme, t time.Duration, cw int) []string {
	if cw < 24 {
		return nil
	}
	section := func(title string) string { return sectionRow(th, cw, title) }
	var lat []float64
	var short, long []float64
	longTok := 0
	for _, r := range m.Decisions {
		if !answeredBy(r, t) || r.Index == 0 {
			continue
		}
		v := msOf(r.Latency())
		lat = append(lat, v)
		if r.InputTokens >= tape.DecisionLongPromptTokens {
			long = append(long, v)
			longTok = max(longTok, r.InputTokens)
		} else {
			short = append(short, v)
		}
	}
	// The window is the newest answers by arrival, which is the record order.
	out := []string{section("LAST REQUESTS")}
	win := lat
	if len(win) > cw {
		win = win[len(win)-cw:]
	}
	cells := Sparkline(win, cw, 0)
	l := newLine(th, cw)
	l.space(cw - len(cells))
	for i, c := range cells {
		st := th.accentMuted
		if i == len(cells)-1 {
			st = th.accent
		}
		l.add(st, string(c.R))
	}
	out = append(out, l.String())
	l = newLine(th, cw)
	if len(win) > 0 {
		l.add(th.dim, fmt.Sprintf("last %d · max %s ms", len(win), fmtLat(maxOf(win))))
	}
	out = append(out, l.String(), blankRow(cw), section("SHORT AND LONG PROMPTS"))
	row := func(label string, vals []float64, extra string) string {
		l := newLine(th, cw)
		l.add(th.dim, label)
		v := unknown
		if len(vals) > 0 {
			v = fmtLat(percentile(vals, 0.5)) + " ms"
		}
		right := v
		if extra != "" {
			right += " · " + extra
		}
		l.gapTo(width(right))
		l.add(th.textMid, right)
		return l.String()
	}
	out = append(out, row("short p50", short, ""))
	extra := ""
	if longTok > 0 {
		extra = fmtThousands(longTok) + " tok"
	}
	out = append(out, row("long p50", long, extra))
	return out
}

// ---- the frame ------------------------------------------------------------

func decisionBody(m Model, th Theme, t time.Duration, bw, bodyH int) []string {
	rows := decisionHeroRows(m, th, t, bw)
	room := bodyH - len(rows)
	if inBurst(m, t) {
		rows = append(rows, decisionBurst(m, th, t, bw, room)...)
	} else {
		rows = append(rows, decisionFeed(m, th, t, bw, room)...)
	}
	return fitRows(rows, blankRow(bw), bodyH)
}

// decisionFooter is the request-latency strip: the token strip's twin, one
// glyph per answered request, with the warm p50 beside it.
func decisionFooter(m Model, th Theme, t time.Duration, w int) string {
	var series []float64
	for _, r := range m.Decisions {
		if answeredBy(r, t) && r.Index != 0 {
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
	stripW := l.left() - width(median) - width(hints) - 4
	if stripW < minStripW {
		stripW = max(0, stripW)
	}
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

// decisionView is View for a decision tape.
func decisionView(m Model, t time.Duration, w, h int) string {
	th := m.Theme
	inner := w - 2
	bodyH := h - chromeH
	bar := th.paint(th.dim, "│")
	var body []string
	for _, row := range decisionBody(m, th, t, inner-2, bodyH) {
		body = append(body, bar+" "+row+" "+bar)
	}
	if m.Mode == ModeCard {
		overlayModal(th, body, resultModal(m, th, modalWidth(inner)), inner)
	}
	lines := make([]string, 0, h)
	lines = append(lines, topBorder(m, th, t, inner))
	lines = append(lines, body...)
	lines = append(lines, th.paint(th.dim, "├"+repeat('─', inner)+"┤"))
	lines = append(lines, bar+" "+decisionFooter(m, th, t, inner-2)+" "+bar)
	lines = append(lines, th.paint(th.dim, "└"+repeat('─', inner)+"┘"))
	return strings.Join(lines, "\n")
}
