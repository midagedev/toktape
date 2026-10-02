package main

import (
	"bytes"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// The questions and the criteria are deliberately not in alphabetical order:
// the mock must answer in the request's order, which a map-backed encoder
// would silently sort.
const reqBody = `{"model":"clef-flash","state":"Checkout is down.","questions":{` +
	`"urgency":{"type":"score","criteria":["Can wait","This week","Today"]},` +
	`"department":{"type":"choice","criteria":{"zeta":"last letter","alpha":"first letter","mid":"between"}},` +
	`"outage":{"type":"noul","instructions":"Is a service down?"}}}`

func post(t *testing.T, srv *httptest.Server, body string) (int, []byte) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/v1/systemone", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newHandler(config{Timings: true, ModelFile: "m.gguf"}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAnswersInRequestOrderAndSumToOne(t *testing.T) {
	srv := newServer(t)
	code, body := post(t, srv, reqBody)
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	// The raw bytes carry the order; a decoded map would not.
	iu, id, io_ := bytes.Index(body, []byte(`"urgency"`)), bytes.Index(body, []byte(`"department"`)), bytes.Index(body, []byte(`"outage"`))
	if !(iu >= 0 && iu < id && id < io_) {
		t.Fatalf("answers are not in the request's question order: %s", body)
	}
	if iz, ia := bytes.Index(body, []byte(`"zeta"`)), bytes.Index(body, []byte(`"alpha"`)); !(iz >= 0 && iz < ia) {
		t.Fatalf("probabilities are not in the criteria's order: %s", body)
	}

	req, err := server.ParseSystemOneRequest([]byte(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.ParseSystemOneResponse(body, req.Questions)
	if err != nil {
		t.Fatalf("the mock's response does not satisfy the schema: %v", err)
	}
	var ids []string
	for _, a := range resp.Answers {
		ids = append(ids, a.QuestionID)
		switch a.Type {
		case tape.DecisionNoul:
			if a.Noul == nil {
				t.Errorf("noul answer %q carries no noul", a.QuestionID)
			}
			continue
		}
		sum, top := 0.0, 0.0
		for _, p := range a.Probabilities {
			sum += p.P
			top = math.Max(top, p.P)
		}
		if math.Abs(sum-1) > 0.0002 {
			t.Errorf("%q probabilities sum to %v", a.QuestionID, sum)
		}
		if a.Confidence != top {
			t.Errorf("%q confidence %v is not max p %v", a.QuestionID, a.Confidence, top)
		}
		if a.Type == tape.DecisionScore {
			want := 0.0
			for i, p := range a.Probabilities {
				want += float64(i) * p.P
			}
			if math.Abs(a.Score-want) > 0.0002 {
				t.Errorf("score %v, want sum(i*p_i) = %v", a.Score, want)
			}
		}
	}
	if got := strings.Join(ids, ","); got != "urgency,department,outage" {
		t.Errorf("question order %s", got)
	}
	if resp.Timings == nil || resp.Timings.CacheN != 0 || resp.Timings.PromptN != resp.InputTokens {
		t.Errorf("timings = %+v for input_tokens %d", resp.Timings, resp.InputTokens)
	}
	if want := int(math.Ceil(float64(len(reqBody)) / 3.6)); resp.InputTokens != want {
		t.Errorf("input_tokens %d, want ceil(len/3.6) = %d", resp.InputTokens, want)
	}
}

func TestDeterministicAcrossCalls(t *testing.T) {
	srv := newServer(t)
	_, b1 := post(t, srv, reqBody)
	_, b2 := post(t, srv, reqBody)
	req, _ := server.ParseSystemOneRequest([]byte(reqBody))
	r1, err1 := server.ParseSystemOneResponse(b1, req.Questions)
	r2, err2 := server.ParseSystemOneResponse(b2, req.Questions)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if !reflect.DeepEqual(r1.Answers, r2.Answers) {
		t.Errorf("two calls answered differently:\n%+v\n%+v", r1.Answers, r2.Answers)
	}
	// A different state is a different draw.
	_, b3 := post(t, srv, strings.Replace(reqBody, "Checkout is down.", "All quiet.", 1))
	r3, _ := server.ParseSystemOneResponse(b3, req.Questions)
	if reflect.DeepEqual(r1.Answers, r3.Answers) {
		t.Error("a different state gave identical answers")
	}
}

func TestRefusesWhatTheEndpointWould(t *testing.T) {
	srv := newServer(t)
	for name, body := range map[string]string{
		"not json":       `nope`,
		"no state":       `{"questions":{"q":{"type":"noul"}}}`,
		"no questions":   `{"state":"x"}`,
		"unknown type":   `{"state":"x","questions":{"q":{"type":"rank"}}}`,
		"choice no crit": `{"state":"x","questions":{"q":{"type":"choice"}}}`,
	} {
		if code, b := post(t, srv, body); code != 400 {
			t.Errorf("%s: status %d, want 400 (%s)", name, code, b)
		}
	}
}

func TestPropsAndModels(t *testing.T) {
	srv := newServer(t)
	id, err := server.New(srv.URL).ProbeEngine(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if id.Kind != "mock-systemone" || id.Build != "dev" || id.ModelFile != "m.gguf" || id.Quant != "Q4_K_M" {
		t.Errorf("identity = %+v", id)
	}
}
