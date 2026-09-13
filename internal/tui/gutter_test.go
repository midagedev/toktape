package tui

import (
	"strings"
	"testing"
)

// TestTheAnswerHasNoGutter is TTP-50 (user 2026-09-14: "팬에 왼쪽에 라인
// 그려진거 불필요하게 자리 차지하는 것 같아").
//
// Every answer line used to open with a one-cell rule and a space. The rule
// separated stacked stream blocks in the list layout, which is gone; tiles are
// separated by the grid's own rules, and the stream that is talking is still
// marked by its breathing cursor. So the two columns go back to the answer: a
// body line starts at its tile's first column.
func TestTheAnswerHasNoGutter(t *testing.T) {
	const cw, rows = 40, 8
	m := ModelAt(ExampleTapeN(4), midRun)
	drew := 0
	for _, s := range m.Streams {
		if len(s.Tokens) == 0 {
			continue
		}
		for _, active := range []bool{false, true} {
			for i, l := range streamBody(m, PlainTheme(), midRun, s, cw, rows, active) {
				if strings.TrimSpace(l) == "" {
					continue
				}
				drew++
				if strings.HasPrefix(l, "▏") {
					t.Errorf("stream %d (active=%v) line %d opens with the gutter: %q", s.Index, active, i, l)
					break
				}
			}
		}
	}
	if drew == 0 {
		t.Fatal("no stream drew an answer line at mid-run")
	}
}
