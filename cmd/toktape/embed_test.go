package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/ledger"
	"github.com/midagedev/toktape/internal/tape"
)

func TestEmbedRecordsCardAndCompare(t *testing.T) {
	hermetic(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		var req struct{ Input string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		v := []float64{1, 0, 0}
		if strings.Contains(req.Input, "b") {
			v = []float64{0, 1, 0}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"embedding": v}}})
	}))
	t.Cleanup(srv.Close)
	inputs := filepath.Join(t.TempDir(), "in.jsonl")
	if err := os.WriteFile(inputs, []byte(`{"id":"one","input":"a"}`+"\n"+`{"id":"two","input":"b"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	code, stdout, stderr := exec(t, "embed", "--url", srv.URL, "--inputs", inputs, "--out", dir, "--no-pace")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	for _, want := range []string{"POST /v1/embeddings", "cosine similarity", "  0    1.00  0.00", "dim 3"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("summary lacks %q:\n%s", want, stdout)
		}
	}
	tapes, _ := filepath.Glob(filepath.Join(dir, "*"+tape.Ext))
	if len(tapes) != 1 {
		t.Fatalf("tapes %v", tapes)
	}
	tp, err := tape.Read(tapes[0])
	if err != nil || !tp.Summary.IsEmbedding() || len(tp.Embeddings) != 2 {
		t.Fatalf("tape: %v", err)
	}
	if ledger.Holds(tp) {
		t.Error("the ledger holds an embedding tape")
	}
	if code, out, _ := exec(t, "card", tapes[0]); code != exitOK || !strings.Contains(out, "cosine similarity") {
		t.Errorf("card exit %d:\n%s", code, out)
	}
	code, out, _ := exec(t, "compare", tapes[0], tapes[0])
	if code != exitOK || !strings.Contains(out, "1.000000    0.000000") || !strings.Contains(out, "2 inputs matched") {
		t.Errorf("compare exit %d:\n%s", code, out)
	}
	if code, _, _ := exec(t, "compare", tapes[0], filepath.Join("..", "..", "assets", "hero.tape")); code == exitOK {
		t.Error("an embedding tape compared with a token tape")
	}
	if code, _, _ := exec(t, "publish", tapes[0], "--yes"); code == exitOK {
		t.Error("an embedding tape was published")
	}
}

func TestEmbedFlagRefusals(t *testing.T) {
	hermetic(t)
	for name, args := range map[string][]string{
		"bad api":     {"embed", "--api", "x"},
		"bad format":  {"embed", "--encoding-format", "f16"},
		"neg dims":    {"embed", "--dimensions", "-1"},
		"neg cap":     {"embed", "--vector-cap", "-1"},
		"stray arg":   {"embed", "extra"},
		"missing set": {"embed", "--inputs", filepath.Join(t.TempDir(), "none")},
	} {
		if code, _, _ := exec(t, args...); code == exitOK {
			t.Errorf("%s: exit 0", name)
		}
	}
}

// TestEmbeddingDemoTape: the recorded llama-server tape still reads and
// shows (EmbeddingGemma 2 Q8_0, built-in search set).
func TestEmbeddingDemoTape(t *testing.T) {
	code, out, _ := exec(t, "card", filepath.Join("..", "..", "assets", "embedding-demo.tape"))
	if code != exitOK || !strings.Contains(out, "embeddinggemma-2") || !strings.Contains(out, "q-starter") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}
