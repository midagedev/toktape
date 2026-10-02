// Command mock-systemone is a stand-in for a decision model's endpoint
// (POST /v1/systemone, TTP-192, 2026-10-02): enough of the SystemOne /
// bloomery shape to record a `toktape decide` run against before a real
// engine serves it. It answers per Cloudflare's response schema, in the
// request's own question order, with deterministic invented probabilities and
// an invented latency. Nothing it returns is a measurement of anything.
//
//	go run ./tools/mock-systemone --addr 127.0.0.1:8091
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// config is the latency model and the identity the mock reports.
type config struct {
	Latency   time.Duration // base per request
	Jitter    time.Duration // uniform +- around the base
	PerToken  time.Duration // added per input token
	Cold      time.Duration // added to the first request only
	Timings   bool          // add bloomery's top-level "timings" key
	ModelFile string
}

// headMs is the joint schema head's share of the latency, a constant: the
// mock's engine time is PromptMs + HeadMs = latency.
const headMs = 1.4

// bytesPerToken prices the request body: input_tokens = ceil(len / 3.6).
const bytesPerToken = 3.6

type mock struct {
	cfg   config
	first atomic.Bool // set once the first request has been answered
	mu    sync.Mutex
	rng   *rand.Rand
}

func newHandler(cfg config) http.Handler {
	m := &mock{cfg: cfg, rng: rand.New(rand.NewSource(192))}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/systemone", m.systemone)
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"engine": "mock-systemone", "build": "dev", "model_path": cfg.ModelFile, "quant": "Q4_K_M",
		})
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]string{{"id": cfg.ModelFile}}})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

func (m *mock) systemone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	req, err := server.ParseSystemOneRequest(body)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	tokens := int(math.Ceil(float64(len(body)) / bytesPerToken))
	latency := m.latency(tokens)

	out := m.answer(req, tokens, latency)

	timer := time.NewTimer(latency)
	select {
	case <-timer.C:
	case <-r.Context().Done():
		timer.Stop()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

// latency is base + jitter + per-token, plus the cold extra on the first
// request the mock ever answers. The cold flag is taken at validation, not at
// reply, so two concurrent first requests do not both pay it.
func (m *mock) latency(tokens int) time.Duration {
	m.mu.Lock()
	j := (m.rng.Float64()*2 - 1) * float64(m.cfg.Jitter)
	m.mu.Unlock()
	d := m.cfg.Latency + time.Duration(j) + time.Duration(tokens)*m.cfg.PerToken
	if m.first.CompareAndSwap(false, true) {
		d += m.cfg.Cold
	}
	if d < 0 {
		d = 0
	}
	return d
}

// answer writes the response by hand: a Go map would sort the question ids,
// and the answers are in the request's order.
func (m *mock) answer(req *server.SystemOneRequest, tokens int, latency time.Duration) []byte {
	var b bytes.Buffer
	model := req.Model
	if model == "" {
		model = "clef-flash"
	}
	b.WriteString(`{"model":`)
	b.Write(jsonString(model))
	b.WriteString(`,"answers":{`)
	for i, q := range req.Questions {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(jsonString(q.ID))
		b.WriteByte(':')
		writeAnswer(&b, req.State, q)
	}
	fmt.Fprintf(&b, `},"usage":{"input_tokens":%d,"output_tokens":0}`, tokens)
	if m.cfg.Timings {
		ms := float64(latency) / float64(time.Millisecond)
		fmt.Fprintf(&b, `,"timings":{"prompt_n":%d,"prompt_ms":%.3f,"head_ms":%g,"cache_n":0}`, tokens, max(ms-headMs, 0), headMs)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func jsonString(s string) []byte { b, _ := json.Marshal(s); return b }

// unit is a deterministic number in [0, 1) from (state, question id, salt).
func unit(state json.RawMessage, id string, salt byte) float64 {
	h := sha256.New()
	h.Write(state)
	h.Write([]byte{0})
	h.Write([]byte(id))
	h.Write([]byte{salt})
	return float64(binary.BigEndian.Uint64(h.Sum(nil)[:8])>>11) / (1 << 53)
}

func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }

// distribution is n probabilities with one clear top option, 4 decimals,
// summing to 1: the remainder after rounding goes to the top option.
func distribution(state json.RawMessage, id string, n int) (ps []float64, top int) {
	ps = make([]float64, n)
	if n == 1 {
		ps[0] = 1
		return ps, 0
	}
	top = int(unit(state, id, 'T') * float64(n))
	ptop := 0.80 + 0.17*unit(state, id, 'P')
	weights := make([]float64, n)
	var sum float64
	for i := range weights {
		if i != top {
			weights[i] = 0.2 + unit(state, id, byte('a'+i))
			sum += weights[i]
		}
	}
	var rest float64
	for i := range ps {
		if i == top {
			continue
		}
		ps[i] = round4((1 - ptop) * weights[i] / sum)
		rest += ps[i]
	}
	ps[top] = round4(1 - rest)
	return ps, top
}

func writeAnswer(b *bytes.Buffer, state json.RawMessage, q tape.DecisionQuestion) {
	if q.Type == tape.DecisionNoul {
		p := round4(0.02 + 0.96*unit(state, q.ID, 'N'))
		fmt.Fprintf(b, `{"type":"noul","noul":%g}`, p)
		return
	}
	ps, top := distribution(state, q.ID, len(q.Options))
	fmt.Fprintf(b, `{"type":%s,`, jsonString(q.Type))
	if q.Type == tape.DecisionChoice {
		b.WriteString(`"choice":`)
		b.Write(jsonString(q.Options[top].Key))
		b.WriteByte(',')
	} else {
		var score float64
		for i, p := range ps {
			score += float64(i) * p
		}
		fmt.Fprintf(b, `"score":%g,`, round4(score))
	}
	fmt.Fprintf(b, `"confidence":%g,`, ps[top])
	if q.Type == tape.DecisionScore {
		b.WriteString(`"legend":{`)
		for i, o := range q.Options {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(jsonString(o.Key))
			b.WriteByte(':')
			b.Write(jsonString(o.Text))
		}
		b.WriteString(`},`)
	}
	b.WriteString(`"probabilities":{`)
	for i, o := range q.Options {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(jsonString(o.Key))
		fmt.Fprintf(b, `:%g`, ps[i])
	}
	b.WriteString(`}}`)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8091", "listen address")
	cfg := config{}
	flag.DurationVar(&cfg.Latency, "latency", 36*time.Millisecond, "base latency of one request")
	flag.DurationVar(&cfg.Jitter, "jitter", 2*time.Millisecond, "uniform jitter around the base, plus or minus")
	flag.DurationVar(&cfg.PerToken, "per-token", 90*time.Microsecond, "latency added per input token")
	flag.DurationVar(&cfg.Cold, "cold", 600*time.Millisecond, "extra latency on the first request only")
	flag.BoolVar(&cfg.Timings, "timings", true, `add bloomery's top-level "timings" key to every response`)
	flag.StringVar(&cfg.ModelFile, "model-file", "Cloudflare_clef-flash-Q4_K_M.gguf", "model file name /props and /v1/models report")
	flag.Parse()
	log.Printf("mock-systemone on %s (latency %s, jitter %s, per-token %s, cold +%s)", *addr, cfg.Latency, cfg.Jitter, cfg.PerToken, cfg.Cold)
	log.Fatal(http.ListenAndServe(*addr, newHandler(cfg)))
}
