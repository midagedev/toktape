package card

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// EmbeddingText is an embedding tape's view (2026-10-15): who answered,
// one row per input (tokens, latency, dim, norm, first values), and the
// pairwise cosine-similarity matrix over the stored vectors. It reads the
// records, not only the summary, because the vectors are the result.
func EmbeddingText(tp *tape.Tape) string {
	s := &tp.Summary
	e := s.Embedding
	if e == nil {
		e = &tape.EmbeddingSummary{}
	}
	var b strings.Builder
	model := orQ(s.Model.FileName)
	if model == "?" && e.Model != "" {
		model = e.Model
	}
	fmt.Fprintf(&b, "toktape %s · %s\n", orQ(s.ToktapeVersion), s.ID)
	fmt.Fprintf(&b, "MODEL    %s\n", model)
	engine := orQ(string(s.Server.Kind))
	if s.Server.Commit != "" {
		engine += " " + s.Server.Commit
	}
	fmt.Fprintf(&b, "ENGINE   %s · POST %s\n", engine, orQ(e.Endpoint))
	req := []string{fmt.Sprintf("%d inputs", e.Inputs)}
	if e.EncodingFormat != "" {
		req = append(req, "encoding_format "+e.EncodingFormat)
	}
	if e.Dimensions > 0 {
		req = append(req, fmt.Sprintf("dimensions %d", e.Dimensions))
	}
	if e.Model != "" {
		req = append(req, "model "+e.Model)
	}
	if e.VectorCap > 0 {
		req = append(req, fmt.Sprintf("vectors capped at %d values", e.VectorCap))
	}
	fmt.Fprintf(&b, "REQUEST  %s\n", strings.Join(req, " · "))
	fmt.Fprintf(&b, "ANSWER   dim %s · latency p50 %s ms max %s ms · %s prompt tokens\n",
		intQ(e.Dim), msQ(e.LatencyP50Ms), msQ(e.LatencyMaxMs), intQ(e.PromptTokens))

	b.WriteString("\n  #  id            tok      ms   dim    norm  first values\n")
	for i, r := range tp.Embeddings {
		if r.Error != "" {
			fmt.Fprintf(&b, "%3d  %-12s  failed: %s\n", i, clip(r.ID, 12), clip(r.Error, 60))
			continue
		}
		head := make([]string, 0, 4)
		for _, x := range r.Head[:min(len(r.Head), 4)] {
			head = append(head, fmt.Sprintf("%+.4f", x))
		}
		fmt.Fprintf(&b, "%3d  %-12s %4s %7s %5d %7.4f  %s\n", i, clip(r.ID, 12), intQ(r.PromptTokens),
			msQ(float64(r.Latency())/float64(time.Millisecond)), r.Dim, r.Norm, strings.Join(head, " "))
	}

	vecs := make([][]float32, len(tp.Embeddings))
	for i, r := range tp.Embeddings {
		vecs[i] = r.Values()
	}
	if len(vecs) > 1 {
		b.WriteString("\ncosine similarity\n     ")
		for j := range vecs {
			fmt.Fprintf(&b, " %5d", j)
		}
		b.WriteString("\n")
		for i := range vecs {
			fmt.Fprintf(&b, "%3d  ", i)
			for j := range vecs {
				c := math.NaN()
				if vecs[i] != nil && vecs[j] != nil {
					c = tape.Cosine(vecs[i], vecs[j])
				}
				if math.IsNaN(c) {
					b.WriteString("     ?")
				} else {
					fmt.Fprintf(&b, " %5.2f", c)
				}
			}
			b.WriteString("\n")
		}
		if e.VectorCap > 0 && e.Dim > e.VectorCap {
			fmt.Fprintf(&b, "  (over the first %d of %d values only)\n", e.VectorCap, e.Dim)
		}
	}
	for _, w := range s.Warnings {
		fmt.Fprintf(&b, "! %s\n", w)
	}
	return b.String()
}

// EmbeddingCompare matches two embedding tapes' inputs by id and reports,
// per input, the cosine and the largest absolute difference between the two
// vectors (over the values both stored). a is the run under test, b the
// reference.
func EmbeddingCompare(a, b *tape.Tape) string {
	ref := map[string]tape.EmbeddingRecord{}
	for _, r := range b.Embeddings {
		ref[r.ID] = r
	}
	var out strings.Builder
	fmt.Fprintf(&out, "A %s · %s %s\n", a.Summary.ID, orQ(string(a.Summary.Server.Kind)), endpointOf(a))
	fmt.Fprintf(&out, "B %s · %s %s\n\n", b.Summary.ID, orQ(string(b.Summary.Server.Kind)), endpointOf(b))
	out.WriteString("  id            dim A/B    cosine   max |diff|\n")
	worst, worstID, matched := 1.0, "", 0
	for _, r := range a.Embeddings {
		o, ok := ref[r.ID]
		switch {
		case !ok:
			fmt.Fprintf(&out, "  %-12s  not in B\n", clip(r.ID, 12))
			continue
		case r.Error != "" || o.Error != "":
			fmt.Fprintf(&out, "  %-12s  failed on one side\n", clip(r.ID, 12))
			continue
		}
		va, vb := r.Values(), o.Values()
		c, d := tape.Cosine(va, vb), tape.MaxAbsDiff(va, vb)
		note := ""
		if r.Input != o.Input {
			note = "  ! inputs differ"
		}
		fmt.Fprintf(&out, "  %-12s  %4d/%-4d  %8.6f  %10.6f%s\n", clip(r.ID, 12), r.Dim, o.Dim, c, d, note)
		matched++
		if !math.IsNaN(c) && c < worst {
			worst, worstID = c, r.ID
		}
	}
	if matched > 0 {
		fmt.Fprintf(&out, "\n%d inputs matched · lowest cosine %.6f (%s)\n", matched, worst, worstID)
	}
	return out.String()
}

func endpointOf(t *tape.Tape) string {
	if t.Summary.Embedding == nil {
		return "?"
	}
	return t.Summary.Embedding.Endpoint
}

func orQ(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

func intQ(n int) string {
	if n <= 0 {
		return "?"
	}
	return fmt.Sprint(n)
}

func msQ(ms float64) string {
	switch {
	case ms <= 0:
		return "?"
	case ms < 100:
		return fmt.Sprintf("%.1f", ms)
	default:
		return fmt.Sprintf("%.0f", ms)
	}
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
