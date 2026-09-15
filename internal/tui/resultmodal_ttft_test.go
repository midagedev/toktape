package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// TestResultModalLeadsWithDecodeAndTTFT pins the 2026-09-15 hierarchy change:
// the right-hand column of the result modal is the time to first token, and
// the prefill rate lives in the caption labelled with its prompt length
// (card.PrefillLabel) rather than standing at feed size without one.
//
// The three runs are three shapes the caption ladder takes:
//
//	hero          pair=false (p50 and p95 render the same), so the ladder has
//	              two rungs and the worded one fits
//	8-stream ex.  pair=true, so the fullest rung names the p95; at 53 columns
//	              against the caption column's 39 it gives way to the worded
//	              rung, which fits at 36
//	short prompt  the label itself carries " · short prompt", so only the
//	              label alone fits
//
// The ladder is asserted the strong way: the highest candidate that fits is on
// the frame, and every fuller candidate is nowhere on it. (modalWidth caps the
// box at 86 columns on every screen the layout supports, so the caption budget
// is colW-2 = 39 at both sizes here — the ladder is exercised against that
// fixed budget, and step 1 would need a wider box than the layout ever draws.)
func TestResultModalLeadsWithDecodeAndTTFT(t *testing.T) {
	hero, err := tape.Read("../../assets/hero.tape")
	if err != nil {
		t.Fatalf("read ../../assets/hero.tape: %v", err)
	}
	// A recorded run's done instant is its last token plus a second, the way
	// tuidump dates a tape (a frame stamped a few milliseconds early is a live
	// model that merely looks finished).
	heroDone := ModelAt(hero, time.Duration(1<<62)).RunEnd + time.Second

	short := *card.ExampleConcurrent()
	short.Concurrency = 4
	short.Aggregate.Streams = 4
	short.Cache.PromptTotal, short.Cache.HitTokens = 63, 0
	short.Timings.PromptN, short.Timings.CacheN = 63, 0

	cases := []struct {
		name    string
		summary tape.RunSummary
		model   func(t *testing.T) Model
		at      time.Duration
	}{
		{"hero", hero.Summary, func(t *testing.T) Model {
			return ModelAt(hero, heroDone)
		}, heroDone},
		{"eight-stream example", *card.ExampleConcurrent(), func(t *testing.T) Model {
			return goldenModel(t, doneAt)
		}, doneAt},
		{"short prompt", short, func(t *testing.T) Model {
			return ModelAt(ExampleTapeN(4), doneAt)
		}, doneAt},
	}

	for _, sz := range []struct{ w, h int }{{100, 30}, {156, 38}} {
		for _, c := range cases {
			t.Run(fmt.Sprintf("%s-%dx%d", c.name, sz.w, sz.h), func(t *testing.T) {
				m := c.model(t)
				// The modal reads m.Summary; the tape behind the live screen
				// stays the example's, the way goldenModel overrides TapePath.
				m.Summary = c.summary
				m.Mode = ModeCard
				frame := View(m, c.at, sz.w, sz.h)
				checkFrame(t, frame, sz.w, sz.h)
				plain := card.StripANSI(frame)

				// The caption's payload: the prefill rate with its length, whole.
				label := card.PrefillLabel(&c.summary)
				if !strings.Contains(plain, label) {
					t.Errorf("the caption lost the prefill label %q", label)
				}

				// The ladder, asserted the strong way: the highest candidate
				// that fits the caption column is printed, and no fuller one
				// is. The budget is the modal's own geometry: colW-2, the
				// space the left caption gets.
				_, p95, pair := card.TTFTPercentiles(&c.summary, fmtMs)
				falls := []string{"first token · " + label}
				if pair {
					falls = append([]string{"first token p50 · p95 " + p95 + " · " + label}, falls...)
				}
				// The right caption runs from the right figure's column to the
				// modal's inner edge, so its budget is colW — the left
				// caption's is colW-2, indented by two.
				budget := (modalWidth(sz.w-2) - 4) / 2
				want := ""
				for _, cand := range falls {
					if width(cand) <= budget {
						want = cand
						break
					}
				}
				if want == "" {
					// No rung fits, so the caption wraps: the words on their
					// own line and the label whole on the next. Both must be
					// on the frame, and neither is allowed to be a rung.
					if !strings.Contains(plain, label) {
						t.Errorf("the wrapped caption lost the label %q", label)
					}
				} else if !strings.Contains(plain, want) {
					t.Errorf("the caption is not the highest ladder step that fits %d cols:\n%q", budget, want)
				}
				for _, cand := range falls {
					if cand == want || width(cand) <= budget {
						continue
					}
					if strings.Contains(plain, cand) {
						t.Errorf("a caption step that does not fit %d cols is on the frame:\n%q", budget, cand)
					}
				}

				// Whatever the ladder gives up, it never gives up naming the
				// figure above it. This is asserted directly rather than
				// through the ladder above, because the ladder here is a copy
				// of the renderer's and a copy agrees with a mistake: the
				// worded rung was four columns too wide for the eight-stream
				// example at 100×30 until 2026-09-15, so "810 ms" stood over a
				// caption that said only what the prefill was (lead).
				if !strings.Contains(plain, "first token") {
					t.Errorf("the caption never names the figure above it — no %q on the frame", "first token")
				}

				// The big TTFT figure's bottom row carries the decode unit and
				// the TTFT's own unit beside the glyphs, exactly once.
				num, unit := fmtMsParts(card.TTFTMs(&c.summary))
				unitRows := 0
				for _, row := range parseFrame(frame, sz.w, sz.h) {
					p := plainRow(row)
					if countSubstring(p, " tok/s") > 0 && strings.Contains(p, " "+unit) && countSubstring(p, "▀") > 0 {
						unitRows++
					}
				}
				if unitRows != 1 {
					t.Errorf("%d rows carry the decode unit and the TTFT unit %q beside big-figure glyphs, want exactly 1", unitRows, unit)
				}
				// The figure drawn is THIS number's face: its bottom glyph row
				// sits directly beside the unit, so the frame shows the TTFT
				// the summary reports and not another figure's glyphs.
				if fig := bigFigure(num)[bigRows-1] + " " + unit; !strings.Contains(plain, fig) {
					t.Errorf("the TTFT figure row %q (glyphs + unit) is nowhere on the frame", fig)
				}
			})
		}
	}
}
