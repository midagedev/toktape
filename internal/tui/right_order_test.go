package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The pane hierarchy (2026-09-15, user-approved): the measured result comes
// first. SPEED opens the pane and the decode rate opens SPEED, so the headline
// figure is the first row under the first title rather than the third section
// down; the machine that produced it — placement, memory, resources — reads
// under it in its original order. This is the order contract only; what the
// rows say belongs to speedRows and its own tests.

// rightPaneRows is the right pane of a plain frame, one entry per body row:
// the runes between the pane divider and the frame's right border, trailing
// padding trimmed. The divider is located the way separatorColumn locates it
// (the second-to-last vertical mark, so tile rules on the left never win), and
// the slice is taken in runes, not bytes: the marks are multibyte and a byte
// cut would split one. Everything right of the divider is narrow, so rune
// index and display column agree there, whatever Hangul the answers carry.
func rightPaneRows(frame string) []string {
	lines := strings.Split(frame, "\n")
	var out []string
	// Skip the top and bottom borders: they carry no pane content, and the
	// title bar between them is a full-width row of its own.
	for i := 1; i+1 < len(lines); i++ {
		rs := []rune(lines[i])
		var marks []int
		for j, r := range rs {
			if r == '│' || r == '┤' {
				marks = append(marks, j)
			}
		}
		if len(marks) < 2 {
			continue // a rule row, or the footer: no pane divider on it
		}
		row := strings.TrimRight(string(rs[marks[len(marks)-2]+1:marks[len(marks)-1]]), " ")
		out = append(out, row)
	}
	return out
}

// TestRightPaneOrderIsResultFirst pins both halves of the hierarchy on the
// frames the layout is designed around: the section titles read SPEED,
// PLACEMENT, MEMORY, RESOURCES top to bottom, and inside SPEED the decode (or
// sample) row sits above the prefill row. A round that moves a section or a
// row back, however it words the change, fails here before it reaches the
// goldens.
//
// FAIL-first: on the pre-reorder source (sections PLACEMENT, MEMORY, SPEED,
// RESOURCES; prefill above decode) this fails every subtest on both halves.
func TestRightPaneOrderIsResultFirst(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{120, 36}, {156, 38}} {
		for _, off := range []struct {
			name string
			at   time.Duration
		}{{"mid", midRun}, {"done", doneAt}} {
			t.Run(fmt.Sprintf("%dx%d-%s", sz.w, sz.h, off.name), func(t *testing.T) {
				rows := rightPaneRows(View(goldenModel(t, off.at), off.at, sz.w, sz.h))

				at := map[string]int{}
				for i, row := range rows {
					if f := strings.Fields(row); len(f) > 0 {
						at[f[0]] = i
					}
				}
				want := []string{"SPEED", "PLACEMENT", "MEMORY", "RESOURCES"}
				for _, title := range want {
					if _, ok := at[title]; !ok {
						t.Errorf("the pane has no %s title:\n%s", title, strings.Join(rows, "\n"))
					}
				}
				for i := 1; i < len(want); i++ {
					if at[want[i]] <= at[want[i-1]] {
						t.Errorf("%s (row %d) does not come after %s (row %d)",
							want[i], at[want[i]], want[i-1], at[want[i-1]])
					}
				}

				// The decode row is "decode", or "sample" on a tape the card
				// would call a sample (card.IsSample owns that predicate).
				dec, pre := -1, -1
				for i, row := range rows {
					switch f := strings.Fields(row); {
					case len(f) == 0:
					case f[0] == "decode" || f[0] == "sample":
						dec = i
					case f[0] == "prefill":
						pre = i
					}
				}
				if dec < 0 || pre < 0 {
					t.Fatalf("no decode/sample row (%d) or no prefill row (%d) in the pane:\n%s", dec, pre, strings.Join(rows, "\n"))
				}
				if dec > pre {
					t.Errorf("the decode row (%d) is below the prefill row (%d); the measured result leads", dec, pre)
				}
			})
		}
	}
}
