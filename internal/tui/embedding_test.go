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

func TestEmbeddingVerifyScreen(t *testing.T) {
	a, _ := embedTapes(t)
	end := EmbeddingEnd(a)
	for _, size := range [][2]int{{120, 40}, {MinWidth, MinHeight}} {
		m := ModelAt(a, end)
		m.EmbedVerify = true
		done := frame(t, m, end, size[0], size[1])
		for _, want := range []string{"EMBEDDING", "52/52 inputs", "COSINE SIMILARITY", "q-index", "norm 1.0000", "query prompt"} {
			if !strings.Contains(done, want) {
				t.Errorf("%v verify frame lacks %q:\n%s", size, want, done)
			}
		}
	}
}

// TestSearchScene walks the demo through its phases: indexing, a query
// being typed, its ranked answer with the expected hit, and the clusters.
func TestSearchScene(t *testing.T) {
	a, _ := embedTapes(t)
	var q tape.EmbeddingRecord
	for _, r := range a.Embeddings {
		if r.Role == tape.EmbeddingRoleQuery && r.Prompt != "" {
			q = r
			break
		}
	}
	if q.ShownAt == 0 || q.SentAt <= q.ShownAt {
		t.Fatalf("the demo's first query has no typing time: %+v", q)
	}
	e := a.Summary.Embedding
	steps := []struct {
		at   time.Duration
		want []string
	}{
		{q.ShownAt / 2, []string{"INDEX", "indexing the corpus"}},
		// Mid-typing, on a cursor-on half of the blink.
		{(q.ShownAt+q.SentAt)/2 - ((q.ShownAt+q.SentAt)/2)%(2*blinkDur) + time.Millisecond, []string{"20/20 docs", "▌"}},
		{q.AnsweredAt + 2*time.Second, []string{"TOP 5", "✓", "PROMPTS with #1", "MRL     768 #1", "top-1 the same", "1›", "RESOURCES", "CPU"}},
		{e.OutroAt + 3*time.Second, []string{"CLUSTERS", "recall@1 6/6", "RESOURCES"}},
	}
	for _, size := range [][2]int{{120, 40}, {MinWidth, MinHeight}} {
		for _, st := range steps {
			f := frame(t, ModelAt(a, st.at), st.at, size[0], size[1])
			for _, w := range st.want {
				if !strings.Contains(f, w) {
					t.Errorf("%v at %s lacks %q:\n%s", size, st.at, w, f)
				}
			}
		}
	}
	if end := EmbeddingEnd(a); end < 45*time.Second || end > 90*time.Second {
		t.Errorf("the demo replays in %s, want 45-90s", end)
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

// TestSceneMotion: what moves does so as a function of t — the typing is
// monotonic and complete at send, the bars and strip grow after the answer,
// and once settled the frame stops changing.
func TestSceneMotion(t *testing.T) {
	a, _ := embedTapes(t)
	var q tape.EmbeddingRecord
	for _, r := range a.Embeddings {
		if r.Role == tape.EmbeddingRoleQuery && r.Prompt != "" {
			q = r
			break
		}
	}
	prev := 0
	for at := q.ShownAt; at <= q.SentAt; at += 20 * time.Millisecond {
		n := typedCount(q, at)
		if n < prev {
			t.Fatalf("typing went back at %s: %d after %d", at, n, prev)
		}
		prev = n
	}
	if typedCount(q, q.SentAt) != len([]rune(q.Input)) {
		t.Error("the query is not fully typed when it is sent")
	}
	f := func(at time.Duration) string { return frame(t, ModelAt(a, at), at, 120, 40) }
	early, settled := f(q.AnsweredAt+60*time.Millisecond), f(q.AnsweredAt+3*time.Second)
	if early == settled {
		t.Error("nothing moved between the answer landing and settling")
	}
	if f(q.AnsweredAt+3*time.Second+100*time.Millisecond) != settled {
		t.Error("the settled frame still moves")
	}
}
