package png

import (
	"image"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// TestLlamaFlagRowUnchanged (2026-09-15 ExLlamaV3 round's contract, re-pinned
// 2026-09-19): the fa/ctk/ctv row the footer's engine column drew survives on
// the identity band's engine line — same literals, same runs — and the argv
// the strip carried is still in the tape, the run page and -o md. The row is
// pinned at the builder's preferred string rather than the drawn mark because
// the drawn line is pickWidest's choice: at 26 px the example's engine string
// plus the row plus the context pair is 81 characters against a 69-character
// line, so the row is exactly the element the fallback chain gives up first
// (content.go's order), and the mark would pin the chain's outcome rather
// than the row's content.
func TestLlamaFlagRowUnchanged(t *testing.T) {
	cases := []struct {
		name string
		s    *tape.RunSummary
		row  string
	}{
		{"example", card.Example(), "fa on · ctk q8_0 · ctv q8_0"},
		{"speculative", card.ExampleSpeculative(), "fa on · ctk q8_0 · ctv q8_0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ct := contentOf(t, c.s)
			if !strings.Contains(ct.ident3.preferred, c.row) {
				t.Errorf("engine line preferred = %q, want it to carry the pre-change row %q",
					ct.ident3.preferred, c.row)
			}
			// The drawn line is some member of the chain — preferred or a
			// fallback, never an invention — and it starts with the engine
			// itself, which every member does.
			cv, err := renderCanvas(c.s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			m, ok := cv.markByID("ident.engine")
			if !ok {
				t.Fatal("ident.engine was never drawn")
			}
			chain := append([]string{ct.ident3.preferred}, ct.ident3.fallbacks...)
			var inChain bool
			for _, member := range chain {
				if m.Text == member {
					inChain = true
					break
				}
			}
			if !inChain {
				t.Errorf("ident.engine = %q, want a member of the chain %v", m.Text, chain)
			}
			if !strings.HasPrefix(m.Text, engineString(c.s.Server)) {
				t.Errorf("ident.engine = %q, want it to lead with %q", m.Text, engineString(c.s.Server))
			}
		})
	}
}

// TestExLlamaV3Card: the engine fixture's picture. The engine's own argv rides
// the identity band's engine line — on the preferred string, which the
// fallback chain gives up before the engine or the context when the whole
// does not fit — no mark anywhere teaches a llama.cpp flag, and every text
// mark sits inside the content box.
func TestExLlamaV3Card(t *testing.T) {
	s := card.ExampleExLlamaV3()
	img, err := Render(s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got, want := img.Bounds(), image.Rect(0, 0, Width, Height); got != want {
		t.Fatalf("bounds = %v, want %v", got, want)
	}
	ct := contentOf(t, s)
	if !strings.Contains(ct.ident3.preferred, "-gs 44,21 -mcs 185 -mct 32 -mtp") {
		t.Errorf("engine preferred = %q, want it to carry the engine argv on one line",
			ct.ident3.preferred)
	}
	cv, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := cv.markByID("ident.engine")
	if !ok {
		t.Fatal("ident.engine was never drawn")
	}
	chain := append([]string{ct.ident3.preferred}, ct.ident3.fallbacks...)
	var inChain bool
	for _, member := range chain {
		if m.Text == member {
			inChain = true
			break
		}
	}
	if !inChain {
		t.Errorf("ident.engine = %q, want a member of the chain %v", m.Text, chain)
	}
	for _, m := range cv.marks {
		if m.Kind != "text" || m.Text == "" {
			continue
		}
		for _, llama := range []string{"-fa ", "-ctk ", "-ctv ", "-ub ", "-b 2048", "-ngl "} {
			if strings.Contains(m.Text, llama) {
				t.Errorf("%s (%q) teaches a llama.cpp flag an engine run never had", m.ID, m.Text)
			}
		}
		// The engine named no per-device split, so no bandwidth clause may be
		// derived: absent, not "?" — the text card's contract, on the image.
		for _, absent := range []string{"from RAM", "of peak"} {
			if strings.Contains(m.Text, absent) {
				t.Errorf("%s (%q) prints %q the engine's silence should have withheld", m.ID, m.Text, absent)
			}
		}
		if m.Rect.Min.X < contentL || m.Rect.Max.X > contentR {
			t.Errorf("%s (%q) spans x=%d..%d, outside the content box (%d..%d)",
				m.ID, m.Text, m.Rect.Min.X, m.Rect.Max.X, contentL, contentR)
		}
	}
}

// TestExLlamaV3SplitBandwidth: the fixture variant whose engine reported a
// consistent per-device active split gets bandwidth figures back — the
// engine's own, not the class estimate.
//
// The clause a two-stream hero can carry is the verify-step one: the plain
// "from RAM" spelling only renders under a single-stream number
// (content.go's hero build), and this run drafted with -mtp, so the
// per-accepted-token view is replaced by the per-step view in the draft
// clause — the same precedence the text card applies. When the column is
// full, pickWidest gives up the "of peak" ratio before the rate
// (content.go's fallback order), so the image's guarantee is the rate; the
// ratio is pinned on the text card, where the room is.
func TestExLlamaV3SplitBandwidth(t *testing.T) {
	s := card.ExampleExLlamaV3Split()
	cv, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	var sawRate bool
	for _, m := range cv.marks {
		if m.Kind == "text" && strings.Contains(m.Text, "GB/s RAM/step") {
			sawRate = true
		}
	}
	if !sawRate {
		t.Error("no mark carries the verify-step RAM rate for the engine's consistent split")
	}
}
