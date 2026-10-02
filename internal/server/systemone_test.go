package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// The shown order is the written order (TTP-192, 2026-10-02): the screen
// draws questions and options as the request wrote them. These ids and keys
// are in reverse alphabetical order so a map-backed decode, which iterates
// sorted or random, cannot pass by luck.
const orderedRequest = `{"model":"clef-flash","state":{"b":1,"a":2},"questions":{` +
	`"zulu":{"type":"choice","instructions":"Which?","criteria":{"zeta":"last","alpha":"first","mu":"mid"}},` +
	`"yankee":{"type":"score","criteria":["Low","Mid","High"]},` +
	`"alpha":{"type":"noul","instructions":"Is it?"}}}`

func TestParseRequestKeepsWrittenOrder(t *testing.T) {
	req, err := ParseSystemOneRequest([]byte(orderedRequest))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, q := range req.Questions {
		ids = append(ids, q.ID)
	}
	if got := strings.Join(ids, ","); got != "zulu,yankee,alpha" {
		t.Fatalf("question order = %s, want zulu,yankee,alpha", got)
	}
	var keys []string
	for _, o := range req.Questions[0].Options {
		keys = append(keys, o.Key)
	}
	if got := strings.Join(keys, ","); got != "zeta,alpha,mu" {
		t.Errorf("choice option order = %s, want zeta,alpha,mu", got)
	}
	if o := req.Questions[0].Options[1]; o.Text != "first" {
		t.Errorf("option text = %q", o.Text)
	}
	score := req.Questions[1]
	if score.Type != tape.DecisionScore || len(score.Options) != 3 ||
		score.Options[0].Key != "0" || score.Options[2].Key != "2" || score.Options[2].Text != "High" {
		t.Errorf("score options = %+v", score.Options)
	}
	if n := req.Questions[2]; n.Type != tape.DecisionNoul || len(n.Options) != 0 || n.Instructions != "Is it?" {
		t.Errorf("noul question = %+v", n)
	}
	if req.Model != "clef-flash" || string(req.State) != `{"b":1,"a":2}` {
		t.Errorf("model %q state %s", req.Model, req.State)
	}
}

func TestParseRequestRefusals(t *testing.T) {
	for name, body := range map[string]string{
		"array":         `[]`,
		"no state":      `{"questions":{"q":{"type":"noul"}}}`,
		"no questions":  `{"state":"x"}`,
		"empty":         `{"state":"x","questions":{}}`,
		"unknown type":  `{"state":"x","questions":{"q":{"type":"rank"}}}`,
		"no criteria":   `{"state":"x","questions":{"q":{"type":"choice"}}}`,
		"score object":  `{"state":"x","questions":{"q":{"type":"score","criteria":{"a":"b"}}}}`,
		"dup question":  `{"state":"x","questions":{"q":{"type":"noul"},"q":{"type":"noul"}}}`,
		"trailing junk": `{"state":"x","questions":{"q":{"type":"noul"}}} {}`,
	} {
		if _, err := ParseSystemOneRequest([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// responseBody answers orderedRequest with the probabilities written in a
// different order from the request's criteria, and a top-level timings key.
const responseBody = `{"model":"clef-flash","answers":{` +
	`"alpha":{"type":"noul","noul":0.9123},` +
	`"yankee":{"type":"score","score":1.8,"confidence":0.7,"legend":{"0":"Low","1":"Mid","2":"High"},"probabilities":{"2":0.7,"1":0.2,"0":0.1}},` +
	`"zulu":{"type":"choice","choice":"alpha","confidence":0.8,"probabilities":{"mu":0.05,"alpha":0.8,"zeta":0.15}}},` +
	`"usage":{"input_tokens":161,"output_tokens":0},` +
	`"timings":{"prompt_n":161,"prompt_ms":35.2,"head_ms":1.4,"cache_n":0}}`

func TestParseResponseFollowsTheRequestOrder(t *testing.T) {
	req, _ := ParseSystemOneRequest([]byte(orderedRequest))
	resp, err := ParseSystemOneResponse([]byte(responseBody), req.Questions)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Answers) != 3 || resp.Answers[0].QuestionID != "zulu" || resp.Answers[2].QuestionID != "alpha" {
		t.Fatalf("answers not in question order: %+v", resp.Answers)
	}
	zulu := resp.Answers[0]
	var keys []string
	for _, p := range zulu.Probabilities {
		keys = append(keys, p.Key)
	}
	if strings.Join(keys, ",") != "zeta,alpha,mu" || zulu.Probabilities[0].P != 0.15 || zulu.Choice != "alpha" || zulu.Confidence != 0.8 {
		t.Errorf("choice answer = %+v", zulu)
	}
	if y := resp.Answers[1]; y.Score != 1.8 || y.Probabilities[0].Key != "0" || y.Probabilities[0].P != 0.1 {
		t.Errorf("score answer = %+v", y)
	}
	if n := resp.Answers[2]; n.Noul == nil || *n.Noul != 0.9123 {
		t.Errorf("noul answer = %+v", n)
	}
	if resp.InputTokens != 161 || resp.Model != "clef-flash" {
		t.Errorf("tokens %d model %q", resp.InputTokens, resp.Model)
	}
	want := tape.DecisionServerTimings{PromptN: 161, PromptMs: 35.2, HeadMs: 1.4}
	if resp.Timings == nil || *resp.Timings != want {
		t.Errorf("timings = %+v, want %+v", resp.Timings, want)
	}
}

// A noul of 0 is an answer and not an absent one.
func TestParseResponseZeroNoulIsAnAnswer(t *testing.T) {
	q := []tape.DecisionQuestion{{ID: "n", Type: tape.DecisionNoul}}
	resp, err := ParseSystemOneResponse([]byte(`{"model":"m","answers":{"n":{"type":"noul","noul":0}},"usage":{"input_tokens":3,"output_tokens":0}}`), q)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Answers[0].Noul == nil || *resp.Answers[0].Noul != 0 {
		t.Errorf("noul = %v", resp.Answers[0].Noul)
	}
	if resp.Timings != nil {
		t.Errorf("a response with no timings key carries timings %+v", resp.Timings)
	}
}

func TestParseResponseMismatches(t *testing.T) {
	req, _ := ParseSystemOneRequest([]byte(orderedRequest))
	cut := func(old, new string) string { return strings.Replace(responseBody, old, new, 1) }
	for name, body := range map[string]string{
		"not json":         `<html>`,
		"no usage":         cut(`"usage":{"input_tokens":161,"output_tokens":0},`, ""),
		"missing question": cut(`"alpha":{"type":"noul","noul":0.9123},`, ""),
		"wrong type":       cut(`"type":"noul"`, `"type":"choice"`),
		"unknown option":   cut(`"alpha":0.8`, `"omega":0.8`),
		"missing option":   cut(`"mu":0.05,`, ""),
		"choice off list":  cut(`"choice":"alpha"`, `"choice":"omega"`),
		"p above one":      cut(`"zeta":0.15`, `"zeta":1.5`),
		"no confidence":    cut(`"confidence":0.8,`, ""),
	} {
		if _, err := ParseSystemOneResponse([]byte(body), req.Questions); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A timings key that is not the engine's shape is no timing, not a failure.
	resp, err := ParseSystemOneResponse([]byte(cut(`"timings":{"prompt_n":161,"prompt_ms":35.2,"head_ms":1.4,"cache_n":0}`, `"timings":"fast"`)), req.Questions)
	if err != nil || resp.Timings != nil {
		t.Errorf("odd timings: err %v timings %+v", err, resp.Timings)
	}
}

func TestPostSystemOneMeasuresSendToLastByte(t *testing.T) {
	const delay = 40 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != SystemOnePath || r.Method != http.MethodPost {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		time.Sleep(delay)
		w.Write([]byte(responseBody))
	}))
	defer srv.Close()
	resp, sent, answered, err := PostSystemOne(context.Background(), srv.URL, []byte(orderedRequest))
	if err != nil {
		t.Fatal(err)
	}
	if string(resp) != responseBody {
		t.Errorf("body = %s", resp)
	}
	if d := answered.Sub(sent); d < delay || d > delay+500*time.Millisecond {
		t.Errorf("latency %s, want about %s", d, delay)
	}
}

func TestPostSystemOneKeepsTheServersWordsOnRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"no such question type"}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	resp, _, answered, err := PostSystemOne(context.Background(), srv.URL, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "no such question type") {
		t.Errorf("err = %v", err)
	}
	if !answered.IsZero() || !strings.Contains(string(resp), "no such question type") {
		t.Errorf("answered %v resp %s", answered, resp)
	}
}

// bloomery names its engine as a string; an engine object is the other
// spelling the existing Props reads. Both fill the identity, and neither
// invents what the server did not say.
func TestProbeEngineReadsBothSpellings(t *testing.T) {
	cases := map[string]struct {
		props  string
		models string
		want   EngineIdentity
	}{
		"string engine": {
			`{"engine":"Bloomery","build":"0.1.0","model_path":"/m/Cloudflare_clef-flash-Q4_K_M.gguf","quant":"Q4_K_M"}`, ``,
			EngineIdentity{Kind: "bloomery", Build: "0.1.0", ModelPath: "/m/Cloudflare_clef-flash-Q4_K_M.gguf", ModelFile: "Cloudflare_clef-flash-Q4_K_M.gguf", Quant: "Q4_K_M"},
		},
		"engine object": {
			`{"engine":{"name":"exllamav3","version":"0.0.9","model":{"quant":"4.0bpw"}},"model_path":"/x/y.gguf"}`, ``,
			EngineIdentity{Kind: "exllamav3", Build: "0.0.9", ModelPath: "/x/y.gguf", ModelFile: "y.gguf", Quant: "4.0bpw"},
		},
		"models fallback": {
			`{"engine":"bloomery"}`, `{"data":[{"id":"clef.gguf"}]}`,
			EngineIdentity{Kind: "bloomery", ModelFile: "clef.gguf"},
		},
		"nothing said": {`{}`, `{"data":[]}`, EngineIdentity{}},
	}
	for name, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/props":
				w.Write([]byte(c.props))
			case "/v1/models":
				if c.models == "" {
					http.NotFound(w, r)
					return
				}
				w.Write([]byte(c.models))
			}
		}))
		got, err := New(srv.URL).ProbeEngine(context.Background())
		srv.Close()
		if err != nil || got != c.want {
			t.Errorf("%s: %+v, %v; want %+v", name, got, err, c.want)
		}
	}
}

func TestProbeEngineNothingListening(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	if id, err := New(url).ProbeEngine(context.Background()); err != nil || id != (EngineIdentity{}) {
		t.Errorf("404s are an unknown engine, not a failure: %+v, %v", id, err)
	}
	srv.Close()
	if _, err := New(url).ProbeEngine(context.Background()); !errors.Is(err, ErrUnreachable) {
		t.Errorf("closed server: err = %v, want ErrUnreachable", err)
	}
}

func TestDiscoverSystemOneAcceptsAnEngineStringProps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/props" {
			w.Write([]byte(`{"engine":"bloomery"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	got, err := New("").DiscoverSystemOne(context.Background(), []string{"http://127.0.0.1:1", srv.URL})
	if err != nil || got != srv.URL {
		t.Errorf("found %q, %v", got, err)
	}
	// The same body makes the llama-protocol discovery give up on it, which
	// is why this probe exists.
	if _, err := New("").Discover(context.Background(), []string{srv.URL}); err == nil {
		t.Error("Discover accepted a string engine; the DiscoverSystemOne premise is gone")
	}
}
