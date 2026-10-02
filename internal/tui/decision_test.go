package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// The decision screen's goldens (TTP-192, 2026-10-02). They are new files, and
// regenerate with TOKTAPE_UPDATE_DECISION_GOLDENS=1 so that rewriting them can
// never rewrite the token screen's by accident (those use -update).
const decisionExample = "../tape/testdata/decision-example.tape"

func decisionTape(t *testing.T) *tape.Tape {
	t.Helper()
	tp, err := tape.Read(decisionExample)
	if err != nil {
		t.Fatal(err)
	}
	return tp
}

func decisionModelAt(t *testing.T, tp *tape.Tape, at time.Duration) Model {
	t.Helper()
	m := ModelAt(tp, at)
	m.Replay = true
	return m
}

// Instants into the example: the cold request in flight, a block mid-answer
// with the spinner, four blocks on screen, the long case in flight, the burst
// early and late, and the held card.
var decisionInstants = []struct {
	name string
	at   time.Duration
	card bool
}{
	{"cold-deciding", 1800 * time.Millisecond, false},
	{"cold-answered", 2300 * time.Millisecond, false},
	{"four-blocks", 9 * time.Second, false},
	{"long-deciding", 11 * time.Second, false},
	{"burst-early", 13 * time.Second, false},
	{"burst-late", 24 * time.Second, false},
	{"card", 27 * time.Second, true},
}

func TestDecisionGolden(t *testing.T) {
	tp := decisionTape(t)
	for _, sz := range []struct{ w, h int }{{120, 36}, {100, 30}} {
		for _, in := range decisionInstants {
			t.Run(fmt.Sprintf("%dx%d-%s", sz.w, sz.h, in.name), func(t *testing.T) {
				m := decisionModelAt(t, tp, in.at)
				if in.card {
					m.Mode = ModeCard
					m.CardAge = GleamSweep
				}
				got := View(m, in.at, sz.w, sz.h)
				checkFrame(t, got, sz.w, sz.h)
				path := filepath.Join("testdata", fmt.Sprintf("decision-%dx%d-%s.txt", sz.w, sz.h, in.name))
				if os.Getenv("TOKTAPE_UPDATE_DECISION_GOLDENS") != "" {
					if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read golden (TOKTAPE_UPDATE_DECISION_GOLDENS=1 creates it): %v", err)
				}
				if strings.TrimRight(string(want), "\n") != got {
					diffLines(t, strings.TrimRight(string(want), "\n"), got)
					t.Errorf("frame differs from %s", path)
				}
			})
		}
	}
}

// TestDecisionGeometry: every size and instant is exactly w x h, down to the
// minimum, with no half-printed row.
func TestDecisionGeometry(t *testing.T) {
	tp := decisionTape(t)
	for _, sz := range []struct{ w, h int }{{100, 30}, {120, 36}, {156, 38}, {140, 40}} {
		for at := time.Duration(0); at < 28*time.Second; at += 370 * time.Millisecond {
			m := decisionModelAt(t, tp, at)
			checkFrame(t, View(m, at, sz.w, sz.h), sz.w, sz.h)
			m.Mode = ModeCard
			checkFrame(t, View(m, at, sz.w, sz.h), sz.w, sz.h)
		}
	}
}

// TestDecisionFeedFits: at 120x36 four whole case blocks are on screen once
// the feed has filled, at 100x30 three. Counted by their id rows, and by the
// last tile border, so a block with its tiles cut off does not count.
func TestDecisionFeedFits(t *testing.T) {
	tp := decisionTape(t)
	at := 9 * time.Second
	for _, c := range []struct{ w, h, want int }{{120, 36, 4}, {100, 30, 3}} {
		m := decisionModelAt(t, tp, at)
		frame := View(m, at, c.w, c.h)
		if got := strings.Count(frame, "╰"); got < c.want {
			t.Errorf("%dx%d: %d whole question tiles rows visible, want at least %d blocks\n%s", c.w, c.h, got, c.want, frame)
		}
		if got := strings.Count(frame, " tok · "); got < c.want {
			t.Errorf("%dx%d: %d block headers, want at least %d", c.w, c.h, got, c.want)
		}
	}
}

// TestDecisionOptionsKeepRequestOrder: the questions draw in the request's own
// order (a Go map would have lost it), and the options of a choice are read in
// the order the request wrote them: route-01 lists billing before technical.
func TestDecisionOptionsKeepRequestOrder(t *testing.T) {
	tp := decisionTape(t)
	r := tp.Decisions[0]
	if r.CaseID != "route-01" {
		t.Fatalf("first case is %q", r.CaseID)
	}
	if got := r.Answers[0].Probabilities; len(got) != 2 || got[0].Key != "billing" || got[1].Key != "technical" {
		t.Fatalf("probabilities are not in request order: %+v", got)
	}
	m := decisionModelAt(t, tp, 2600*time.Millisecond)
	frame := card.StripANSI(View(m, 2600*time.Millisecond, 120, 36))
	i, j, k := strings.Index(frame, "department"), strings.Index(frame, "urgency"), strings.Index(frame, "outage")
	if i < 0 || !(i < j && j < k) {
		t.Errorf("tiles are not in request order: department %d urgency %d outage %d", i, j, k)
	}
	// The top option is technical (0.90) and billing is the runner-up.
	if !strings.Contains(frame, "▶ technical") || !strings.Contains(frame, "billing") {
		t.Errorf("choice tile does not lead with technical and name billing:\n%s", frame)
	}
}

// TestDecisionAnswersEaseIn: before AnsweredAt a tile says deciding and shows
// no figure; after it the values travel from 0 over easeDur and settle.
func TestDecisionAnswersEaseIn(t *testing.T) {
	tp := decisionTape(t)
	ans := tp.Decisions[1].AnsweredAt
	before := View(decisionModelAt(t, tp, ans-time.Millisecond), ans-time.Millisecond, 120, 36)
	if !strings.Contains(before, "deciding") {
		t.Error("no spinner before the answer")
	}
	early := card.StripANSI(View(decisionModelAt(t, tp, ans+30*time.Millisecond), ans+30*time.Millisecond, 120, 36))
	settled := card.StripANSI(View(decisionModelAt(t, tp, ans+easeDur), ans+easeDur, 120, 36))
	if early == settled {
		t.Error("the answer did not ease: frames 30 ms and easeDur after it are identical")
	}
	if !strings.Contains(settled, "0.9") {
		t.Errorf("settled frame carries no probability:\n%s", settled)
	}
}

func TestDecisionColourMatchesPlain(t *testing.T) {
	tp := decisionTape(t)
	for _, in := range decisionInstants {
		m := decisionModelAt(t, tp, in.at)
		if in.card {
			m.Mode = ModeCard
		}
		plain := View(m, in.at, 120, 36)
		m.Theme = ColourTheme()
		coloured := View(m, in.at, 120, 36)
		if coloured == plain {
			t.Fatalf("%s: colour theme emitted no escapes", in.name)
		}
		if got := card.StripANSI(coloured); got != plain {
			t.Errorf("%s: stripping the palette does not reproduce the plain frame", in.name)
			diffLines(t, plain, got)
		}
	}
}

func TestDecisionViewIsPure(t *testing.T) {
	tp := decisionTape(t)
	m := decisionModelAt(t, tp, 9*time.Second)
	first := View(m, 9*time.Second, 120, 36)
	time.Sleep(5 * time.Millisecond)
	if View(m, 9*time.Second, 120, 36) != first {
		t.Error("two renders of the same model and clip time differ")
	}
}

// TestDecisionNeverPrintsTokenFigures: nothing on a decision frame may read as
// a token rate.
func TestDecisionNeverPrintsTokenFigures(t *testing.T) {
	tp := decisionTape(t)
	for _, in := range decisionInstants {
		m := decisionModelAt(t, tp, in.at)
		frame := View(m, in.at, 120, 36)
		if strings.Contains(frame, "tok/s") && !in.card {
			t.Errorf("%s: frame prints tok/s", in.name)
		}
	}
}

// TestDecisionLongCaseStaysVisible: the burst table keeps the long case's row
// and its p50 is the slow one; the short and long figures are apart.
func TestDecisionLongCaseStaysVisible(t *testing.T) {
	tp := decisionTape(t)
	at := 24 * time.Second
	frame := card.StripANSI(View(decisionModelAt(t, tp, at), at, 120, 36))
	for _, want := range []string{"long-08", "4,386", "long p50", "short p50", "requests · p95"} {
		if !strings.Contains(frame, want) {
			t.Errorf("burst frame lacks %q:\n%s", want, frame)
		}
	}
	re := regexp.MustCompile(`long-08\s+4,386\s+\S+\s+(\d+)\s`)
	m := re.FindStringSubmatch(frame)
	if m == nil || m[1] < "400" {
		t.Errorf("long-08 row does not show a slow p50: %v", m)
	}
}

func TestDecisionCardRows(t *testing.T) {
	tp := decisionTape(t)
	m := decisionModelAt(t, tp, 27*time.Second)
	m.Mode = ModeCard
	m.CardAge = GleamSweep
	frame := card.StripANSI(View(m, 27*time.Second, 120, 36))
	for _, want := range []string{"p50 · warm · end to end", "engine", "36.1 ms · prompt + head", "cold", "640 ms", "short p50", "long p50", "4,386 tok", "p95",
		"prefill", "5,228 tok/s", "throughput", "11.7 req/s · c1", "requests", "168 · errors 0", "SYNTHETIC"} {
		if !strings.Contains(frame, want) {
			t.Errorf("card lacks %q:\n%s", want, frame)
		}
	}
	for _, not := range []string{"cache hits", "vs "} {
		if strings.Contains(frame, not) {
			t.Errorf("card prints %q without the figure", not)
		}
	}
	tp.Summary.Decision.CacheHits = 3
	tp.Summary.Decision.Reference = &tape.DecisionAgreement{File: "ref.json", MaxAbsDeltaP: 0.012, TopFlips: 0}
	m = decisionModelAt(t, tp, 27*time.Second)
	m.Mode, m.CardAge = ModeCard, GleamSweep
	frame = card.StripANSI(View(m, 27*time.Second, 120, 36))
	for _, want := range []string{"cache hits", "vs ref.json: max |Δp| 0.012 · 0 flips"} {
		if !strings.Contains(frame, want) {
			t.Errorf("card lacks %q:\n%s", want, frame)
		}
	}
}

// TestDecisionCardTimingWords (2026-10-02): the caption says "end to end"
// whoever timed the engine, and the engine row exists only when the tape has
// the engine's figure. FAIL-first: the old card said "engine" on a
// server-timed tape, "client, end to end" on a client one, and had no engine
// row.
func TestDecisionCardTimingWords(t *testing.T) {
	tp := decisionTape(t)
	tp.Summary.Decision.TimingSource = tape.DecisionTimingClient
	tp.Summary.Decision.EngineWarmP50Ms = 0
	m := decisionModelAt(t, tp, 27*time.Second)
	m.Mode, m.CardAge = ModeCard, GleamSweep
	frame := card.StripANSI(View(m, 27*time.Second, 120, 36))
	if !strings.Contains(frame, "p50 · warm · end to end") {
		t.Errorf("a client-timed card does not say end to end:\n%s", frame)
	}
	for _, bad := range []string{"engine ", "prompt + head", "client, end to end"} {
		if strings.Contains(frame, bad) {
			t.Errorf("a card without an engine figure prints %q:\n%s", bad, frame)
		}
	}
}

// burstStart is the first burst request's send time.
func burstStart(tp *tape.Tape) time.Duration {
	for _, r := range tp.Decisions {
		if r.Phase == tape.DecisionPhaseBurst {
			return r.SentAt
		}
	}
	return 0
}

// latestSection is the frame's LATEST ANSWERS rows, ANSI stripped, from the
// title down to the footer divider, "" when the frame has no such section.
func latestSection(frame string) string {
	rows := strings.Split(card.StripANSI(frame), "\n")
	var out []string
	in := false
	for _, r := range rows {
		if strings.Contains(r, "LATEST ANSWERS") {
			in = true
		}
		if in && strings.HasPrefix(r, "├") {
			break
		}
		if in {
			out = append(out, r)
		}
	}
	return strings.Join(out, "\n")
}

// TestDecisionLatestAnswers (2026-10-02): the burst's lower half is a feed of
// whole case blocks that changes at most every 500 ms. FAIL-first: the old
// burst screen had no such section (the lower half was empty).
func TestDecisionLatestAnswers(t *testing.T) {
	tp := decisionTape(t)
	b0 := burstStart(tp)
	frameAt := func(at time.Duration, w, h int) string {
		return View(decisionModelAt(t, tp, at), at, w, h)
	}
	// Whole blocks: every header has its tile rows, and the counts are what
	// fits: two at 120x36, one at 100x30.
	for _, c := range []struct{ w, h, blocks int }{{120, 36, 2}, {100, 30, 1}} {
		sec := latestSection(frameAt(b0+8*time.Second, c.w, c.h))
		if sec == "" {
			t.Fatalf("%dx%d: no LATEST ANSWERS section", c.w, c.h)
		}
		if got := strings.Count(sec, " tok · "); got != c.blocks {
			t.Errorf("%dx%d: %d block headers, want %d\n%s", c.w, c.h, got, c.blocks, sec)
		}
		if tops, bottoms := strings.Count(sec, "╭"), strings.Count(sec, "╰"); tops == 0 || tops != bottoms {
			t.Errorf("%dx%d: %d tile tops and %d bottoms: a block is clipped\n%s", c.w, c.h, tops, bottoms, sec)
		}
	}
	// Inside one tick the section does not move; across ticks it does.
	tick := 10
	start := b0 + time.Duration(tick)*latestTick
	want := latestSection(frameAt(start, 120, 36))
	for _, off := range []time.Duration{40, 200, 333, 499} {
		if got := latestSection(frameAt(start+off*time.Millisecond, 120, 36)); got != want {
			t.Errorf("tick %d: the section changed %d ms in:\n%s\nvs\n%s", tick, off, want, got)
		}
	}
	distinct := map[string]bool{}
	for k := 4; k < 24; k++ {
		distinct[latestSection(frameAt(b0+time.Duration(k)*latestTick+time.Millisecond, 120, 36))] = true
	}
	if len(distinct) < 10 {
		t.Errorf("20 ticks showed only %d distinct sections", len(distinct))
	}
	// No flicker: scanned every 40 ms the section changes no more often than
	// once a tick.
	changes, prev := 0, ""
	for at := b0 + 3*time.Second; at < b0+9*time.Second; at += 40 * time.Millisecond {
		cur := latestSection(frameAt(at, 120, 36))
		if prev != "" && cur != prev {
			changes++
		}
		prev = cur
	}
	if max := 6 * int(time.Second/latestTick); changes > max {
		t.Errorf("the section changed %d times in 6 s, want at most %d", changes, max)
	}
}

// TestDecisionRateSettles (2026-10-02): the burst footer's req/s is "?" until
// the burst window so far is 2 s. FAIL-first: it printed a value from the
// first answer (26, settling at 13).
func TestDecisionRateSettles(t *testing.T) {
	tp := decisionTape(t)
	b0 := burstStart(tp)
	footer := func(at time.Duration) string {
		for _, r := range strings.Split(card.StripANSI(View(decisionModelAt(t, tp, at), at, 120, 36)), "\n") {
			if strings.Contains(r, "req/s") {
				return r
			}
		}
		return ""
	}
	if got := footer(b0 + time.Second); !strings.Contains(got, "? req/s") {
		t.Errorf("1 s into the burst the footer reads %q, want ? req/s", got)
	}
	if got := footer(b0 + 5*time.Second); strings.Contains(got, "? req/s") || !strings.Contains(got, " req/s") {
		t.Errorf("5 s into the burst the footer reads %q, want a rate", got)
	}
}

// TestDecisionCardFigureIsTheOnlyLitOne (2026-10-02): the modal's big figure
// wears the token modal's lead-figure style (accentBold) and is the only
// figure on the screen in it: the hero behind the card is dim. FAIL-first: the
// hero stayed accentBold behind the card, two lit figures of one size.
func TestDecisionCardFigureIsTheOnlyLitOne(t *testing.T) {
	th := ColourTheme()
	lit := regexp.MustCompile(regexp.QuoteMeta(th.accentBold.open) + `[ ▀▄█]{3,}`)

	tok := ModelAt(ExampleTapeN(4), doneAt)
	tok.Mode, tok.Theme, tok.CardAge = ModeCard, th, GleamSweep
	if n := len(lit.FindAllString(View(tok, doneAt, 120, 36), -1)); n == 0 {
		t.Fatal("the token modal's lead figure is no longer accentBold: this test's reference moved")
	}

	tp := decisionTape(t)
	m := decisionModelAt(t, tp, 27*time.Second)
	m.Mode, m.Theme, m.CardAge = ModeCard, th, GleamSweep
	frame := View(m, 27*time.Second, 120, 36)
	if n := len(lit.FindAllString(frame, -1)); n != bigRows {
		t.Errorf("%d accentBold figure rows on the end card, want %d (the modal's alone)", n, bigRows)
	}
	dim := regexp.MustCompile(regexp.QuoteMeta(th.dim.open) + `[ ▀▄█]{3,}`)
	if n := len(dim.FindAllString(frame, -1)); n < bigRows {
		t.Errorf("the hero behind the card has %d dim figure rows, want %d", n, bigRows)
	}
	// Live (no card) the hero is lit.
	m.Mode = ModeLive
	if n := len(lit.FindAllString(View(m, 27*time.Second, 120, 36), -1)); n != bigRows {
		t.Errorf("%d accentBold figure rows on the live screen, want the hero's %d", n, bigRows)
	}
}

func TestDecisionStateLine(t *testing.T) {
	for _, c := range []struct{ req, want string }{
		{`{"state":"hello\nworld"}`, "hello ↵ world"},
		{`{"state":{"zeta":1,"alpha":{"b":2.0,"a":[1,"x"]}}}`, "{zeta: 1, alpha: {b: 2.0, a: [1, x]}}"},
		{`{"questions":{}}`, ""},
	} {
		if got := stateLine([]byte(c.req)); got != c.want {
			t.Errorf("stateLine(%s) = %q, want %q", c.req, got, c.want)
		}
	}
	if got := topLevelState([]byte(`{"state":{"zeta":1,"alpha":2}}`)); got != "zeta: 1, alpha: 2" {
		t.Errorf("topLevelState = %q", got)
	}
}

func TestDecisionHeroColdThenWarm(t *testing.T) {
	tp := decisionTape(t)
	at := 2300 * time.Millisecond
	if fig, label := decisionHero(decisionModelAt(t, tp, at+easeDur), at+easeDur); label != "cold · first request" || fig != "640" {
		t.Errorf("cold hero = %q %q", fig, label)
	}
	at = 27 * time.Second
	if fig, label := decisionHero(decisionModelAt(t, tp, at), at); label != "p50 · warm · end to end" || fig != "37.6" {
		t.Errorf("warm hero = %q %q", fig, label)
	}
}
