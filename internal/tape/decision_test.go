package tape

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// TestDecisionRoundTrip: a decision tape keeps its records, the option order
// the request wrote, and a noul answer of exactly 0 through Write and Read.
// Blocks a schema where a Go map reorders the criteria or omitempty drops
// p(true) = 0.
func TestDecisionRoundTrip(t *testing.T) {
	zero := 0.0
	in := &Tape{Schema: SchemaVersion,
		Summary: RunSummary{Mode: ModeDecision, Decision: &DecisionSummary{Endpoint: "/v1/systemone", Cases: 1, Requests: 1, WarmP50Ms: 38.8}},
		Decisions: []DecisionRecord{{
			CaseID: "route-01", Phase: DecisionPhaseShowcase,
			SentAt: time.Second, AnsweredAt: time.Second + 39*time.Millisecond,
			Request: json.RawMessage(`{"model":"clef-flash"}`),
			Questions: []DecisionQuestion{{ID: "department", Type: DecisionChoice,
				Options: []DecisionOption{{Key: "technical"}, {Key: "billing"}}}},
			Answers: []DecisionAnswer{
				{QuestionID: "department", Type: DecisionChoice, Choice: "technical",
					Probabilities: []DecisionProb{{"technical", 0.97}, {"billing", 0.03}}},
				{QuestionID: "outage", Type: DecisionNoul, Noul: &zero},
			},
		}},
	}
	var buf bytes.Buffer
	if err := Encode(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Summary.IsDecision() || out.Summary.Decision == nil || out.Summary.Decision.WarmP50Ms != 38.8 {
		t.Fatalf("summary lost: %+v", out.Summary.Decision)
	}
	d := out.Decisions[0]
	if got := d.Questions[0].Options[0].Key + "," + d.Questions[0].Options[1].Key; got != "technical,billing" {
		t.Errorf("option order %q, want technical,billing", got)
	}
	if d.Answers[1].Noul == nil || *d.Answers[1].Noul != 0 {
		t.Errorf("noul p(true)=0 lost: %v", d.Answers[1].Noul)
	}
	if d.Latency() != 39*time.Millisecond {
		t.Errorf("latency %v, want 39ms", d.Latency())
	}
	if (&RunSummary{Mode: ModeChat}).IsDecision() || (*RunSummary)(nil).IsDecision() {
		t.Error("IsDecision true for a chat or a nil summary")
	}
}

// TestDecisionExampleReads pins the synthetic fixture the screen and card
// tracks draw against (tools/decision-example/gen.py): it decodes, it is a
// decision tape with no token stream, and the request order of the criteria
// survived (route-01 lists billing before technical).
func TestDecisionExampleReads(t *testing.T) {
	tp, err := Read("testdata/decision-example.tape")
	if err != nil {
		t.Fatal(err)
	}
	if !tp.Summary.IsDecision() || tp.Summary.Decision == nil || len(tp.Requests) != 0 {
		t.Fatalf("not a decision tape: mode %q, %d token streams", tp.Summary.Mode, len(tp.Requests))
	}
	if n := len(tp.Decisions); n != tp.Summary.Decision.Requests || n == 0 {
		t.Fatalf("%d records, summary says %d", n, tp.Summary.Decision.Requests)
	}
	d := tp.Decisions[0]
	if d.CaseID != "route-01" || d.Questions[0].Options[0].Key != "billing" || d.Answers[0].Probabilities[0].Key != "billing" {
		t.Errorf("first record %s, first option %q / prob %q: request order lost", d.CaseID, d.Questions[0].Options[0].Key, d.Answers[0].Probabilities[0].Key)
	}
	if d.Server == nil || d.Server.HeadMs == 0 || d.Latency() <= 0 {
		t.Errorf("engine timing or latency missing: %+v %v", d.Server, d.Latency())
	}
}
