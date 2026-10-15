package tape

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"
	"time"
)

// An embedding tape (2026-10-15) records an embedding model: each input is
// sent on its own to an embeddings endpoint and answered with one vector.
// Nothing is generated, so — like a decision tape — Requests stays empty and
// every token figure is absent; Tape.Embeddings holds the run. Every reader
// that reads tokens branches on IsEmbedding first.

// ModeEmbedding is RunSummary.Mode for a `toktape embed` run.
const ModeEmbedding = "embedding"

// IsEmbedding reports whether the run is an embedding-model run.
func (s *RunSummary) IsEmbedding() bool {
	return s != nil && s.Mode == ModeEmbedding
}

// Values of EmbeddingSummary.API: which request shape was sent.
const (
	EmbeddingAPIOpenAI = "openai" // POST /v1/embeddings
	EmbeddingAPILlama  = "llama"  // POST /embedding (llama-server's own)
)

// EmbeddingHead is how many leading values every record keeps in the clear,
// whatever the vector cap: enough to eyeball two engines against each other.
const EmbeddingHead = 8

// EmbeddingRecord is one input: what was sent, what came back, and when.
type EmbeddingRecord struct {
	Index int `json:"index"` // send order, from 0
	// ID is the input set row's id ("en-q1"); toktape's own, never sent.
	ID    string `json:"id"`
	Input string `json:"input"`
	// SentAt and AnsweredAt are since run start; AnsweredAt is 0 on a
	// failed request.
	SentAt     time.Duration `json:"sent_at"`
	AnsweredAt time.Duration `json:"answered_at,omitempty"`
	// PromptTokens is the server's usage figure; 0 = not reported.
	PromptTokens int `json:"prompt_tokens,omitempty"`
	// Dim and Norm are of the full vector as returned, read before any cap.
	Dim  int       `json:"dim,omitempty"`
	Norm float64   `json:"norm,omitempty"`
	Head []float32 `json:"head,omitempty"`
	// Vector is the first Stored values, little-endian float32, base64
	// (std encoding). Stored == Dim unless the run capped it.
	Vector string `json:"vector,omitempty"`
	Stored int    `json:"stored,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Latency is send to the last byte of the answer; 0 for a failed request.
func (r EmbeddingRecord) Latency() time.Duration {
	if r.AnsweredAt <= 0 {
		return 0
	}
	return r.AnsweredAt - r.SentAt
}

// EmbeddingSummary is an embedding run's summary.
type EmbeddingSummary struct {
	API      string `json:"api"`      // EmbeddingAPI*
	Endpoint string `json:"endpoint"` // the path requests went to
	// Model is the "model" sent (and echoed back when the server did); "" =
	// none sent.
	Model string `json:"model,omitempty"`
	// Dimensions is the "dimensions" asked for (MRL truncation); 0 = none.
	Dimensions int `json:"dimensions,omitempty"`
	// EncodingFormat is what was asked for: "float" or "base64".
	EncodingFormat string `json:"encoding_format,omitempty"`
	InputSet       string `json:"input_set,omitempty"` // file name; "" = built-in
	Inputs         int    `json:"inputs"`
	Errors         int    `json:"errors,omitempty"`
	Dim            int    `json:"dim,omitempty"` // of the first answer
	// VectorCap is the per-record cap on stored values; 0 = whole vectors.
	VectorCap    int     `json:"vector_cap,omitempty"`
	PromptTokens int     `json:"prompt_tokens,omitempty"` // sum of the reported
	LatencyP50Ms float64 `json:"latency_p50_ms,omitempty"`
	LatencyMaxMs float64 `json:"latency_max_ms,omitempty"`
}

// EncodeVector is the stored form of v: little-endian float32, base64.
func EncodeVector(v []float32) string {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return base64.StdEncoding.EncodeToString(b)
}

// DecodeVector reads EncodeVector's form (also what an OpenAI server sends
// for encoding_format "base64").
func DecodeVector(s string) ([]float32, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(b)%4 != 0 {
		return nil, errors.New("vector bytes are not a whole number of float32")
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v, nil
}

// Values returns the stored vector, nil when none was stored.
func (r EmbeddingRecord) Values() []float32 {
	if r.Vector == "" {
		return nil
	}
	v, err := DecodeVector(r.Vector)
	if err != nil {
		return nil
	}
	return v
}

// L2Norm is the Euclidean norm of v.
func L2Norm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

// Cosine is the cosine similarity of a and b over their common prefix; NaN
// when either is empty or zero.
func Cosine(a, b []float32) float64 {
	n := min(len(a), len(b))
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return math.NaN()
	}
	return dot / math.Sqrt(na*nb)
}

// MaxAbsDiff is the largest |a[i]-b[i]| over the common prefix.
func MaxAbsDiff(a, b []float32) float64 {
	n := min(len(a), len(b))
	var m float64
	for i := 0; i < n; i++ {
		m = math.Max(m, math.Abs(float64(a[i])-float64(b[i])))
	}
	return m
}
