package recorder

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// embedServer answers both shapes with a vector derived from the input's
// length, so two inputs get different vectors and the test can predict them.
func embedServer(t *testing.T, rows int) *httptest.Server {
	t.Helper()
	vec := func(s string) []float64 { return []float64{float64(len(s)), 1, 2, 3, 4, 5, 6, 7, 8, 9} }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch r.URL.Path {
		case "/v1/embeddings":
			in, _ := req["input"].(string)
			var emb any = vec(in)
			if req["encoding_format"] == "base64" {
				f := vec(in)
				v := make([]float32, len(f))
				for i, x := range f {
					v[i] = float32(x)
				}
				emb = tape.EncodeVector(v)
			}
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"embedding": emb}},
				"usage": map[string]int{"prompt_tokens": 3}})
		case "/embedding":
			in, _ := req["content"].(string)
			m := [][]float64{}
			for i := 0; i < rows; i++ {
				m = append(m, vec(in))
			}
			json.NewEncoder(w).Encode([]any{map[string]any{"index": 0, "embedding": m}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const twoInputs = `{"id":"a","input":"x"}
{"id":"b","input":"hello"}
`

func TestRecordEmbeddingShapes(t *testing.T) {
	srv := embedServer(t, 1)
	for _, tc := range []struct{ api, format string }{
		{tape.EmbeddingAPIOpenAI, "float"},
		{tape.EmbeddingAPIOpenAI, "base64"},
		{tape.EmbeddingAPILlama, ""},
	} {
		tp, err := RecordEmbedding(context.Background(), EmbeddingOptions{
			BaseURL: srv.URL, API: tc.api, EncodingFormat: tc.format,
			Inputs: []byte(twoInputs), InputsName: "/x/set.jsonl", VectorCap: 4,
		})
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.api, tc.format, err)
		}
		e := tp.Summary.Embedding
		if !tp.Summary.IsEmbedding() || e.Inputs != 2 || e.Dim != 10 || e.InputSet != "set.jsonl" || len(tp.Requests) != 0 {
			t.Fatalf("%s: summary %+v", tc.api, e)
		}
		r := tp.Embeddings[1]
		// dim, norm and head come from the whole vector, before the cap.
		if r.ID != "b" || r.Dim != 10 || r.Stored != 4 || len(r.Head) != tape.EmbeddingHead || r.Head[0] != 5 {
			t.Errorf("%s: record %+v", tc.api, r)
		}
		if v := r.Values(); len(v) != 4 || v[0] != 5 || v[3] != 3 {
			t.Errorf("%s: stored %v", tc.api, v)
		}
		if r.Norm < 17.60 || r.Norm > 17.61 { // sqrt(25+285)
			t.Errorf("%s: norm %v", tc.api, r.Norm)
		}
		if tc.api == tape.EmbeddingAPIOpenAI && (r.PromptTokens != 3 || e.Endpoint != "/v1/embeddings") {
			t.Errorf("openai: tokens %d endpoint %s", r.PromptTokens, e.Endpoint)
		}
	}
}

func TestRecordEmbeddingUnpooledFails(t *testing.T) {
	srv := embedServer(t, 2)
	_, err := RecordEmbedding(context.Background(), EmbeddingOptions{BaseURL: srv.URL, API: tape.EmbeddingAPILlama, Inputs: []byte(twoInputs)})
	if !errors.Is(err, ErrAllStreamsFailed) || !strings.Contains(err.Error(), "--pooling") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadEmbeddingInputs(t *testing.T) {
	if in, err := loadEmbeddingInputs(defaultEmbeddingInputs); err != nil || len(in) != 26 {
		t.Fatalf("built-in set: %d %v", len(in), err)
	}
	for name, raw := range map[string]string{
		"empty":    "\n",
		"no input": `{"id":"a"}`,
		"dup id":   `{"id":"a","input":"x"}` + "\n" + `{"id":"a","input":"y"}`,
		"not json": `x`,
	} {
		if _, err := loadEmbeddingInputs([]byte(raw)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
