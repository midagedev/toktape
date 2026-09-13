package tui

import "testing"

// TestPlacementBarsStandApart is TTP-49 (user 2026-09-14: "우측 패널 상단에 램
// 그래프들이 너무 뭉쳐서 하나의 차트처럼 보이는 문제").
//
// The placement section draws one bar per row: each GPU, the host, the VRAM
// split, and the legend's keys under it. A bar drawn with the full block fills
// its cell to the top edge, so it touches the bar in the row above, and on a
// real two-GPU rig with experts in RAM the four bars came out as one rectangle
// with notches in it. Stacked bars must leave a seam of ground between them:
// no cell whose ink reaches its bottom edge may sit on a cell whose ink reaches
// its top edge.
func TestPlacementBarsStandApart(t *testing.T) {
	m := ModelAt(ExampleTapeN(4), midRun)
	for _, cw := range []int{28, 37, 42} {
		rows := placementRows(m, PlainTheme(), midRun, cw)
		if len(rows) < 2 {
			t.Fatalf("cw=%d: the example placed %d bar rows; the test needs two stacked", cw, len(rows))
		}
		for y := 1; y < len(rows); y++ {
			above, below := []rune(rows[y-1]), []rune(rows[y])
			for x := 0; x < len(above) && x < len(below); x++ {
				if inkReachesBottom(above[x]) && inkReachesTop(below[x]) {
					t.Errorf("cw=%d: rows %d and %d touch at column %d (%q on %q)\n%s\n%s",
						cw, y-1, y, x, below[x], above[x], rows[y-1], rows[y])
					break
				}
			}
		}
	}
}

// inkReachesTop and inkReachesBottom classify a block element by the edges of
// its cell it paints. Anything else — letters, spaces — reaches neither.
func inkReachesTop(r rune) bool {
	return r == '█' || r == '▀' || (r >= '▉' && r <= '▐') || (r >= '░' && r <= '▓')
}

func inkReachesBottom(r rune) bool {
	return (r >= '▁' && r <= '█') || (r >= '▉' && r <= '▐') || (r >= '░' && r <= '▓')
}
