package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

func embedTapes(t *testing.T) (a, b *tape.Tape) {
	t.Helper()
	var err error
	if a, err = tape.Read("../../assets/embedding-demo.tape"); err != nil {
		t.Fatal(err)
	}
	if b, err = tape.Read("../tape/testdata/embedding-llama.tape"); err != nil {
		t.Fatal(err)
	}
	return a, b
}

// frame draws m at t plain and in colour, and checks the two agree and the
// plain one is exactly w x h.
func frame(t *testing.T, m Model, at time.Duration, w, h int) string {
	t.Helper()
	m.Replay = true
	p := View(m, at, w, h)
	m.Theme = ColourTheme()
	if c := View(m, at, w, h); card.StripANSI(c) != p {
		t.Fatal("the colour frame is not the plain frame with escapes")
	}
	lines := strings.Split(p, "\n")
	if len(lines) != h {
		t.Fatalf("%d lines, want %d", len(lines), h)
	}
	for i, l := range lines {
		if width(l) != w {
			t.Fatalf("line %d is %d wide, want %d: %q", i, width(l), w, l)
		}
	}
	return p
}

func TestEmbeddingScreen(t *testing.T) {
	a, _ := embedTapes(t)
	end := EmbeddingEnd(a)
	for _, size := range [][2]int{{120, 40}, {MinWidth, MinHeight}} {
		at := a.Embeddings[2].SentAt + time.Millisecond
		mid := frame(t, ModelAt(a, at), at, size[0], size[1])
		if !strings.Contains(mid, "2/8 inputs") || !strings.Contains(mid, "embedding") || strings.Contains(mid, "ko-query") {
			t.Errorf("%v mid-run frame shows the wrong inputs:\n%s", size, mid)
		}
		done := frame(t, ModelAt(a, end), end, size[0], size[1])
		for _, want := range []string{"EMBEDDING", "8/8 inputs", "COSINE SIMILARITY", "code-other", "norm 1.0000", "0.80"} {
			if !strings.Contains(done, want) {
				t.Errorf("%v done frame lacks %q:\n%s", size, want, done)
			}
		}
	}
}

func TestEmbeddingAgainst(t *testing.T) {
	a, b := embedTapes(t)
	m := ModelAt(b, EmbeddingEnd(b))
	SetEmbedRef(&m, a, "v1")
	f := frame(t, m, EmbeddingEnd(b), 120, 40)
	if !strings.Contains(f, "vs v1 · band") || !strings.Contains(f, "0.999") || !strings.Contains(f, "Δ0.00") {
		t.Errorf("compare frame:\n%s", f)
	}
}

func TestDownsampleAndStrip(t *testing.T) {
	got := downsample([]float32{1, 3, -2, -4, 0, 0}, 3)
	if len(got) != 3 || got[0] != 2 || got[1] != -3 || got[2] != 0 {
		t.Fatalf("downsample = %v", got)
	}
	if s := vectorStrip(Theme{}, []float64{-1, 0, 1}, 1); s != "▁▅█" {
		t.Errorf("strip = %q", s)
	}
}
