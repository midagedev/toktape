package recorder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// fakeDecision is a SystemOne endpoint that answers every question with a
// fixed distribution: a choice puts 0.7 on its first option, a score on level
// 0, a noul is 0.9. A state containing FAIL500 is refused with HTTP 500 and
// one containing BADSCHEMA gets a 200 that answers nothing.
type fakeDecision struct {
	delay   time.Duration
	timings bool
	cacheN  int
	// props, when set, replaces the /props body the fake serves — an
	// ik_llama.cpp-shaped one names neither an engine nor a build.
	props string

	mu      sync.Mutex
	bodies  [][]byte
	running int
	peak    int
}

func (f *fakeDecision) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			body := f.props
			if body == "" {
				body = `{"engine":"fake-systemone","build":"t1","model_path":"/m/fake-Q4_K_M.gguf","quant":"Q4_K_M"}`
			}
			fmt.Fprint(w, body)
			return
		case server.SystemOnePath:
		default:
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.running++
		f.peak = max(f.peak, f.running)
		f.mu.Unlock()
		defer func() { f.mu.Lock(); f.running--; f.mu.Unlock() }()
		time.Sleep(f.delay)
		switch {
		case bytes.Contains(body, []byte("FAIL500")):
			http.Error(w, `{"error":"boom"}`, 500)
			return
		case bytes.Contains(body, []byte("BADSCHEMA")):
			fmt.Fprint(w, `{"model":"m","answers":{},"usage":{"input_tokens":1,"output_tokens":0}}`)
			return
		}
		req, err := server.ParseSystemOneRequest(body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		answers := map[string]any{}
		for _, q := range req.Questions {
			switch q.Type {
			case tape.DecisionNoul:
				answers[q.ID] = map[string]any{"type": "noul", "noul": 0.9}
			default:
				probs := map[string]float64{}
				for i, o := range q.Options {
					probs[o.Key] = 0.3 / float64(len(q.Options)-1)
					if i == 0 {
						probs[o.Key] = 0.7
					}
				}
				a := map[string]any{"type": q.Type, "confidence": 0.7, "probabilities": probs}
				if q.Type == tape.DecisionChoice {
					a["choice"] = q.Options[0].Key
				} else {
					a["score"] = 0.3
				}
				answers[q.ID] = a
			}
		}
		out := map[string]any{"model": req.Model, "answers": answers,
			"usage": map[string]int{"input_tokens": (len(body) + 3) / 4, "output_tokens": 0}}
		if f.timings {
			out["timings"] = map[string]any{"prompt_n": (len(body) + 3) / 4, "prompt_ms": float64(f.delay.Milliseconds()) + 0.5, "head_ms": 1.4, "cache_n": f.cacheN}
		}
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testOpts(url string) DecisionOptions {
	return DecisionOptions{BaseURL: url, Gap: NoGap, GPU: gpu.Null{}, FSRoot: "/nonexistent-fsroot"}
}

const smallSuite = `{"id":"a","model":"m","state":"one","questions":{"q":{"type":"choice","criteria":{"x":"X","y":"Y"}}}}
{"id":"b","model":"m","state":"two","questions":{"n":{"type":"noul"}}}
{"model":"m","state":"three","questions":{"s":{"type":"score","criteria":["lo","hi"]}}}
`

func TestStripIDKeepsEveryOtherByte(t *testing.T) {
	for name, c := range map[string]struct{ in, id, out string }{
		"first":     {`{"id": "r-1", "model": "m", "state": 1}`, "r-1", `{"model": "m", "state": 1}`},
		"middle":    {`{"model": "m", "id": "r-1", "state": 1}`, "r-1", `{"model": "m", "state": 1}`},
		"last":      {`{"model": "m", "state": {"z":1,"a":[2, 3]}, "id": "r-1"}`, "r-1", `{"model": "m", "state": {"z":1,"a":[2, 3]}}`},
		"only":      {`{"id":"r-1"}`, "r-1", `{}`},
		"no id":     {`{"state":  "x" ,"model":"m"}`, "", `{"state":  "x" ,"model":"m"}`},
		"nested id": {`{"state":{"id":"inner"},"model":"m"}`, "", `{"state":{"id":"inner"},"model":"m"}`},
		"compact":   {`{"id":"r","state":"x"}`, "r", `{"state":"x"}`},
	} {
		id, body, err := stripID([]byte(c.in))
		if err != nil || id != c.id || string(body) != c.out {
			t.Errorf("%s: id %q body %q err %v; want %q %q", name, id, body, err, c.id, c.out)
		}
	}
	for name, in := range map[string]string{
		"number id": `{"id": 7, "state": 1}`,
		"twice":     `{"id":"a","state":1,"id":"b"}`,
		"array":     `[1]`,
		"trailing":  `{"id":"a"} 1`,
	} {
		if _, _, err := stripID([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestBuiltInSuiteIsSentWithoutItsIDs(t *testing.T) {
	cases, sha, err := loadSuite(defaultDecisionSuite)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 8 || cases[0].ID != "route-01" || cases[7].ID != "long-08" || len(sha) != 64 {
		t.Fatalf("%d cases, first %q, sha %q", len(cases), cases[0].ID, sha)
	}
	lines := strings.Split(strings.TrimSpace(string(defaultDecisionSuite)), "\n")
	for i, c := range cases {
		if bytes.Contains(c.Body, []byte(`"id"`)) {
			t.Errorf("%s: the body still carries an id", c.ID)
		}
		if want := strings.Replace(lines[i], `"id": "`+c.ID+`", `, "", 1); string(c.Body) != want {
			t.Errorf("%s: body differs from its line minus the id:\n%s\n%s", c.ID, c.Body, want)
		}
	}
	// The order the criteria were written in survives into the questions.
	if got := cases[0].Questions[0].Options; got[0].Key != "billing" || got[1].Key != "technical" {
		t.Errorf("route-01 options = %+v", got)
	}
}

func TestSuiteDefaultsAndRefusals(t *testing.T) {
	cases, _, err := loadSuite([]byte(smallSuite))
	if err != nil || len(cases) != 3 || cases[2].ID != "case-3" {
		t.Fatalf("%+v %v", cases, err)
	}
	// The hash covers what is sent, so renaming a case does not change it.
	_, a, _ := loadSuite([]byte(smallSuite))
	_, b, _ := loadSuite([]byte(strings.Replace(smallSuite, `"id":"a"`, `"id":"renamed"`, 1)))
	if a != b {
		t.Error("the suite hash depends on the ids, which are never sent")
	}
	for name, raw := range map[string]string{
		"empty":     "\n\n",
		"duplicate": smallSuite + `{"id":"a","model":"m","state":"x","questions":{"n":{"type":"noul"}}}` + "\n",
		"bad json":  "{nope\n",
		"no state":  `{"questions":{"n":{"type":"noul"}}}` + "\n",
	} {
		if _, _, err := loadSuite([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRecordDecisionBuiltInSuite(t *testing.T) {
	f := &fakeDecision{delay: 2 * time.Millisecond, timings: true}
	srv := f.serve(t)
	o := testOpts(srv.URL)
	o.Repeats = 2
	tp, err := RecordDecision(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if !tp.Summary.IsDecision() || len(tp.Requests) != 0 || len(tp.Decisions) != 8*3 {
		t.Fatalf("mode %q, %d requests, %d decisions", tp.Summary.Mode, len(tp.Requests), len(tp.Decisions))
	}
	for i, d := range tp.Decisions {
		if d.Index != i {
			t.Errorf("record %d has index %d", i, d.Index)
		}
		if bytes.Contains(d.Request, []byte(`"id"`)) {
			t.Errorf("record %d: the sent body carries an id", i)
		}
		if i > 0 && d.SentAt < tp.Decisions[i-1].SentAt {
			t.Errorf("record %d was sent before record %d", i, i-1)
		}
		wantPhase := map[bool]string{true: tape.DecisionPhaseShowcase, false: tape.DecisionPhaseBurst}[i < 8]
		if d.Phase != wantPhase || d.Repeat != i/8 || d.CaseID == "" || len(d.Answers) != len(d.Questions) {
			t.Errorf("record %d = phase %s repeat %d case %q, %d answers for %d questions", i, d.Phase, d.Repeat, d.CaseID, len(d.Answers), len(d.Questions))
		}
		if d.Latency() <= 0 || d.Server == nil || d.InputTokens == 0 {
			t.Errorf("record %d: latency %s server %v tokens %d", i, d.Latency(), d.Server, d.InputTokens)
		}
	}
	// What went over the wire is what the record holds, and carries no id.
	for _, b := range f.bodies {
		if bytes.Contains(b, []byte(`"id"`)) {
			t.Fatalf("the server received an id: %.80s", b)
		}
	}
	if !bytes.Equal(f.bodies[0], tp.Decisions[0].Request) {
		t.Error("record 0's request is not the bytes the server received")
	}
	s := tp.Summary.Decision
	if s.Cases != 8 || s.Repeats != 3 || s.Requests != 24 || s.Errors != 0 || s.Concurrency != 1 ||
		s.Endpoint != "/v1/systemone" || s.TimingSource != tape.DecisionTimingServer || s.Suite != "" || len(s.SuiteSHA) != 64 ||
		s.Model != "clef-flash" || len(s.PerCase) != 8 || s.PerCase[0].CaseID != "route-01" || s.PerCase[7].InputTokens < 1000 {
		t.Errorf("summary = %+v", s)
	}
	if s.ColdMs != float64(tp.Decisions[0].Latency())/1e6 || s.WarmP50Ms <= 0 || s.RequestsPerSecond <= 0 {
		t.Errorf("cold %v p50 %v rps %v", s.ColdMs, s.WarmP50Ms, s.RequestsPerSecond)
	}
	if s.LongWarmP50Ms <= 0 || s.ShortWarmP50Ms <= 0 {
		t.Errorf("short %v long %v", s.ShortWarmP50Ms, s.LongWarmP50Ms)
	}
	sum := tp.Summary
	if sum.Server.Kind != "fake-systemone" || sum.Server.Build != "t1" || sum.Model.FileName != "fake-Q4_K_M.gguf" || sum.Model.Quant != "Q4_K_M" ||
		sum.Server.URL != srv.URL || !strings.HasSuffix(sum.ID, "-fake-q4-k-m") {
		t.Errorf("identity: server %+v model %+v id %s", sum.Server, sum.Model, sum.ID)
	}
}

// The gap is real idle time between showcase requests: it is on the
// timestamps and in no latency.
func TestShowcaseGapIsIdleTimeNotLatency(t *testing.T) {
	f := &fakeDecision{delay: 15 * time.Millisecond}
	srv := f.serve(t)
	o := testOpts(srv.URL)
	o.Suite, o.Repeats, o.Gap = []byte(smallSuite), NoBurst, 90*time.Millisecond
	tp, err := RecordDecision(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(tp.Decisions) != 3 {
		t.Fatalf("%d records, want the showcase pass only", len(tp.Decisions))
	}
	for i := 1; i < 3; i++ {
		idle := tp.Decisions[i].SentAt - tp.Decisions[i-1].AnsweredAt
		if idle < 90*time.Millisecond {
			t.Errorf("idle before request %d = %s, want >= 90ms", i, idle)
		}
		if l := tp.Decisions[i].Latency(); l < 15*time.Millisecond || l > 80*time.Millisecond {
			t.Errorf("latency of request %d = %s: the gap leaked in, or the request did not wait", i, l)
		}
	}
	if tp.Summary.Decision.RequestsPerSecond != 0 {
		t.Error("a run with no burst phase reports a throughput")
	}
	if tp.Summary.Decision.TimingSource != tape.DecisionTimingClient {
		t.Errorf("a server with no timings key is %q timed", tp.Summary.Decision.TimingSource)
	}
}

func TestBurstLanes(t *testing.T) {
	f := &fakeDecision{delay: 25 * time.Millisecond}
	srv := f.serve(t)
	o := testOpts(srv.URL)
	o.Suite, o.Repeats, o.Concurrency = []byte(smallSuite), 4, 3
	tp, err := RecordDecision(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	lanes := map[int]int{}
	for i, d := range tp.Decisions {
		if d.Index != i {
			t.Fatalf("index %d at position %d", d.Index, i)
		}
		if d.Phase == tape.DecisionPhaseBurst {
			lanes[d.Lane]++
		} else if d.Lane != 0 {
			t.Errorf("showcase request on lane %d", d.Lane)
		}
	}
	if n := len(tp.Decisions); n != 3*5 || len(lanes) != 3 || f.peak < 2 {
		t.Errorf("%d records, lanes %v, peak in flight %d", len(tp.Decisions), lanes, f.peak)
	}
	if tp.Summary.Concurrency != 3 || tp.Summary.Decision.Concurrency != 3 {
		t.Errorf("concurrency %d / %d", tp.Summary.Concurrency, tp.Summary.Decision.Concurrency)
	}
	// Every (case, pass) pair of the burst was sent exactly once.
	seen := map[string]int{}
	for _, d := range tp.Decisions[3:] {
		seen[fmt.Sprintf("%s/%d", d.CaseID, d.Repeat)]++
	}
	for k, n := range seen {
		if n != 1 {
			t.Errorf("%s sent %d times", k, n)
		}
	}
	if len(seen) != 12 {
		t.Errorf("%d distinct burst requests, want 12", len(seen))
	}
}

func TestFailedRequestsAreRecordsNotAborts(t *testing.T) {
	f := &fakeDecision{}
	srv := f.serve(t)
	suite := `{"id":"ok","model":"m","state":"fine","questions":{"n":{"type":"noul"}}}
{"id":"boom","model":"m","state":"FAIL500","questions":{"n":{"type":"noul"}}}
{"id":"odd","model":"m","state":"BADSCHEMA","questions":{"n":{"type":"noul"}}}
`
	o := testOpts(srv.URL)
	o.Suite, o.Repeats = []byte(suite), 2
	tp, err := RecordDecision(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	s := tp.Summary.Decision
	if len(tp.Decisions) != 9 || s.Errors != 6 || s.Requests != 9 {
		t.Fatalf("%d records, %d errors", len(tp.Decisions), s.Errors)
	}
	for _, d := range tp.Decisions {
		switch d.CaseID {
		case "ok":
			if d.Error != "" || d.Latency() <= 0 {
				t.Errorf("ok: %+v", d)
			}
		case "boom":
			if !strings.Contains(d.Error, "500") || !strings.Contains(d.Error, "boom") || d.AnsweredAt != 0 || d.Latency() != 0 {
				t.Errorf("boom: error %q answered %s", d.Error, d.AnsweredAt)
			}
		case "odd":
			if !strings.Contains(d.Error, "schema") || d.AnsweredAt != 0 || len(d.Response) == 0 {
				t.Errorf("odd: error %q answered %s response %s", d.Error, d.AnsweredAt, d.Response)
			}
		}
	}
	if s.PerCase[0].Answered != 3 || s.PerCase[1].Answered != 0 || s.PerCase[1].WarmP50Ms != 0 {
		t.Errorf("per case = %+v", s.PerCase)
	}
	if len(tp.Summary.Warnings) == 0 || !strings.Contains(strings.Join(tp.Summary.Warnings, "|"), "6 of 9 requests failed") {
		t.Errorf("warnings = %v", tp.Summary.Warnings)
	}
}

func TestEveryRequestFailing(t *testing.T) {
	f := &fakeDecision{}
	srv := f.serve(t)
	o := testOpts(srv.URL)
	o.Suite, o.Repeats = []byte(`{"model":"m","state":"FAIL500","questions":{"n":{"type":"noul"}}}`), NoBurst
	if _, err := RecordDecision(context.Background(), o); !errors.Is(err, ErrAllStreamsFailed) || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v", err)
	}
	srv.Close()
	if _, err := RecordDecision(context.Background(), o); !errors.Is(err, ErrUnreachable) {
		t.Errorf("nothing listening: err = %v", err)
	}
}

func TestCacheHitsAreSurfaced(t *testing.T) {
	f := &fakeDecision{timings: true, cacheN: 12}
	srv := f.serve(t)
	o := testOpts(srv.URL)
	o.Suite, o.Repeats = []byte(smallSuite), 1
	tp, err := RecordDecision(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if h := tp.Summary.Decision.CacheHits; h != 6 {
		t.Errorf("cache hits = %d, want all 6", h)
	}
}

// reduceDecision from hand-made records, so every figure has a derivation
// that does not go through a server.
func TestReduceDecisionFigures(t *testing.T) {
	ms := func(x float64) time.Duration { return time.Duration(x * float64(time.Millisecond)) }
	rec := func(i int, c string, phase string, sent, lat float64, tokens int, timed bool) tape.DecisionRecord {
		r := tape.DecisionRecord{Index: i, CaseID: c, Phase: phase, SentAt: ms(sent), AnsweredAt: ms(sent + lat), InputTokens: tokens, Model: "m"}
		if phase == tape.DecisionPhaseBurst {
			r.Repeat = 1
		}
		if timed {
			r.Server = &tape.DecisionServerTimings{PromptN: tokens, PromptMs: lat - 2, HeadMs: 1.5}
		}
		return r
	}
	cases := []suiteCase{{ID: "s"}, {ID: "l"}}
	recs := []tape.DecisionRecord{
		rec(0, "s", "showcase", 0, 500, 200, true), // cold
		rec(1, "l", "showcase", 2000, 400, 4000, true),
		rec(2, "s", "burst", 3000, 30, 200, true),
		rec(3, "s", "burst", 3040, 40, 200, true),
		rec(4, "l", "burst", 3100, 410, 4000, true),
		rec(5, "s", "burst", 3600, 50, 200, true),
	}
	s := reduceDecision(recs, cases, 1)
	near := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-6 {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	// warm = 400, 30, 40, 410, 50 -> sorted 30 40 50 400 410.
	near("cold", s.ColdMs, 500)
	near("warm p50", s.WarmP50Ms, 50)
	near("warm p95", s.WarmP95Ms, 400+(410-400)*0.8) // rank 0.95*4 = 3.8
	near("warm mean", s.WarmMeanMs, (30+40+50+400+410)/5.0)
	near("short p50", s.ShortWarmP50Ms, 40) // 30 40 50
	near("long p50", s.LongWarmP50Ms, 405)  // 400 410
	// prefill: tokens / (prompt_ms/1000) per warm request, median.
	// 4000/0.398=10050.25, 200/0.028=7142.86, 200/0.038=5263.16, 4000/0.408=9803.92, 200/0.048=4166.67
	near("prefill", s.PrefillPerSecond, 7142.857142857143)
	// engine = prompt_ms + head_ms = latency - 0.5 per warm request: 399.5 29.5 39.5 409.5 49.5.
	near("engine p50", s.EngineWarmP50Ms, 49.5)
	// 4 burst requests answered, window = last AnsweredAt (3650) - first SentAt (3000).
	near("req/s", s.RequestsPerSecond, 4/0.650)
	if s.TimingSource != tape.DecisionTimingServer || s.Repeats != 2 || s.Errors != 0 || s.InputTokensMin != 200 || s.InputTokensMax != 4000 || s.Model != "m" {
		t.Errorf("%+v", s)
	}
	near("case l p50", s.PerCase[1].WarmP50Ms, 405)
	if s.PerCase[0].Answered != 4 || s.PerCase[1].Answered != 2 || s.PerCase[1].InputTokens != 4000 {
		t.Errorf("per case = %+v", s.PerCase)
	}

	// One answer without timings turns the whole run client-timed, and the
	// prefill rate becomes tokens / client latency.
	recs[3].Server = nil
	s = reduceDecision(recs, cases, 1)
	if s.TimingSource != tape.DecisionTimingClient {
		t.Errorf("source = %s", s.TimingSource)
	}
	// 4000/0.4=10000, 200/0.03=6666.7, 200/0.04=5000, 4000/0.41=9756.1, 200/0.05=4000
	near("client prefill", s.PrefillPerSecond, 6666.666666666667)
	near("client engine p50", s.EngineWarmP50Ms, 0) // no engine figure without every answer timed

	// Two models: no single model to name.
	recs[1].Model = "other"
	if s = reduceDecision(recs, cases, 1); s.Model != "" {
		t.Errorf("model = %q for disagreeing responses", s.Model)
	}
}

func TestQuantile(t *testing.T) {
	if quantile(nil, 0.5) != 0 || quantile([]float64{7}, 0.95) != 7 {
		t.Error("edge cases")
	}
	// numpy.percentile([1,2,3,4], 50) == 2.5, ([1,2,3,4], 95) == 3.85
	v := []float64{4, 1, 3, 2}
	if got := quantile(v, 0.5); got != 2.5 {
		t.Errorf("p50 = %v", got)
	}
	if got := quantile(v, 0.95); math.Abs(got-3.85) > 1e-9 {
		t.Errorf("p95 = %v", got)
	}
	if v[0] != 4 {
		t.Error("quantile sorted its input")
	}
}

// A reference built from the fake's own answers with one probability moved:
// a: x 0.7/y 0.3 against 0.6/0.4 (delta 0.1, same top); b: noul 0.9 against
// 0.4 (delta 0.5, the top flips); c has no reference and is skipped.
func TestReferenceAgreement(t *testing.T) {
	f := &fakeDecision{}
	srv := f.serve(t)
	ref := `{"id":"a","answers":{"q":{"type":"choice","choice":"x","confidence":0.6,"probabilities":{"x":0.6,"y":0.4}}}}
{"id":"b","response":{"model":"m","answers":{"n":{"type":"noul","noul":0.4}},"usage":{"input_tokens":9,"output_tokens":0}}}
{"id":"not-in-suite","answers":{"q":{"type":"noul","noul":0.5}}}
`
	o := testOpts(srv.URL)
	o.Suite, o.Repeats, o.Reference, o.ReferenceName = []byte(smallSuite), 1, []byte(ref), "/tmp/dir/ref.jsonl"
	tp, err := RecordDecision(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	ag := tp.Summary.Decision.Reference
	if ag == nil || ag.File != "ref.jsonl" || ag.Questions != 2 || ag.TopFlips != 1 ||
		math.Abs(ag.MaxAbsDeltaP-0.5) > 1e-9 || math.Abs(ag.BrierDelta-0.26) > 1e-9 {
		t.Errorf("agreement = %+v, want 2 questions, 1 flip, max 0.5, brier 0.26", ag)
	}

	o.Reference = nil
	tp, err = RecordDecision(context.Background(), o)
	if err != nil || tp.Summary.Decision.Reference != nil {
		t.Errorf("no reference given: %+v, %v", tp.Summary.Decision.Reference, err)
	}

	// A reference that does not fit its case fails before anything is sent.
	n := len(f.bodies)
	o.Reference = []byte(`{"id":"a","answers":{"q":{"type":"choice","choice":"x","confidence":0.6,"probabilities":{"x":0.6}}}}` + "\n")
	if _, err := RecordDecision(context.Background(), o); err == nil || !strings.Contains(err.Error(), "reference line 1") {
		t.Errorf("err = %v", err)
	}
	if len(f.bodies) != n {
		t.Error("requests were sent before the reference was checked")
	}
}

// ikProcFixture writes under root the /proc slice a decision run reads when
// /props named nothing: pid 42 holds the listening socket on port, and its
// exe is a binary inside an ik_llama.cpp checkout whose HEAD is hash. The ik
// tree sits under the same root but on the real filesystem, because the
// commit is read from the live path the exe link names.
func ikProcFixture(t *testing.T, root string, port int, hash string) {
	t.Helper()
	exe := filepath.Join(root, "ik_llama.cpp", "build", "bin", "llama-server")
	for _, d := range []string{
		filepath.Join(root, "proc", "net"),
		filepath.Join(root, "proc", "42", "fd"),
		filepath.Dir(exe),
		filepath.Join(root, "ik_llama.cpp", ".git", "refs", "heads"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tcp := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt  uid  timeout inode\n" +
		fmt.Sprintf("   0: 00000000000000000000000000000000:%04X 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 123456 0 0 0 0\n", port)
	for path, data := range map[string]string{
		filepath.Join(root, "proc", "net", "tcp"):                              tcp,
		filepath.Join(root, "proc", "42", "cmdline"):                           "llama-server\x00-m\x00m.gguf\x00",
		filepath.Join(root, "ik_llama.cpp", ".git", "HEAD"):                    "ref: refs/heads/master\n",
		filepath.Join(root, "ik_llama.cpp", ".git", "refs", "heads", "master"): hash + "\n",
		exe: "\x00",
	} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		filepath.Join(root, "proc", "42", "fd", "3"): "socket:[123456]",
		filepath.Join(root, "proc", "42", "exe"):     exe,
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
}

// A decision run against a server whose /props names neither an engine nor a
// build (ik_llama.cpp, TTP-33) still names the engine: the loopback port
// finds the process, the binary's path says ik, and the checkout around it
// names a commit with a warning that says where the commit was read from.
func TestDecisionRunNamesIkLlamaFromTheProcess(t *testing.T) {
	f := &fakeDecision{props: `{"model_path":"/home/me/models/small-Q4_K_M.gguf","total_slots":1,"n_ctx":4096}`}
	srv := f.serve(t)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	const hash = "0badc0de0badc0de0badc0de0badc0de0badc0de"
	root := t.TempDir()
	ikProcFixture(t, root, port, hash)

	o := testOpts(srv.URL)
	o.Suite = []byte(smallSuite)
	o.Repeats = NoBurst
	o.Gap = NoGap
	o.FSRoot = root
	tp, err := RecordDecision(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	s := tp.Summary.Server
	if s.Kind != tape.ServerIKLlama {
		t.Fatalf("engine kind = %q, want ik_llama.cpp", s.Kind)
	}
	if s.Commit != hash[:8] {
		t.Fatalf("engine commit = %q, want %q read from the checkout", s.Commit, hash[:8])
	}
	if got := tp.Summary.Model.FileName; got != "small-Q4_K_M.gguf" {
		t.Fatalf("model file = %q, want small-Q4_K_M.gguf", got)
	}
	var provenance bool
	for _, w := range tp.Summary.Warnings {
		if strings.Contains(w, "read from the checkout next to the binary") {
			provenance = true
		}
	}
	if !provenance {
		t.Fatalf("no warning says where the commit came from: %v", tp.Summary.Warnings)
	}
}
