package card

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// decisionExample reads the synthetic decision tape the card is developed
// against (TTP-192, 2026-10-02).
func decisionExample(t *testing.T) *tape.RunSummary {
	t.Helper()
	tp, err := tape.Read(filepath.Join("..", "tape", "testdata", "decision-example.tape"))
	if err != nil {
		t.Fatalf("read the decision example tape: %v", err)
	}
	if !tp.Summary.IsDecision() {
		t.Fatal("the example tape is not a decision tape")
	}
	return &tp.Summary
}

// withDecision returns a copy of s whose DecisionSummary is edited by f.
func withDecision(s *tape.RunSummary, f func(*tape.DecisionSummary)) *tape.RunSummary {
	c := *s
	d := *s.Decision
	f(&d)
	c.Decision = &d
	return &c
}

// TestDecisionTextGolden pins the whole text card for the example tape. The
// token card's goldens are untouched by this track: TestTextGolden still
// compares them byte for byte.
func TestDecisionTextGolden(t *testing.T) {
	golden(t, "decision-example.txt", []byte(Text(decisionExample(t))))
}

func TestDecisionTextIsTheDecisionCard(t *testing.T) {
	out := Text(decisionExample(t))
	for _, want := range []string{
		"Decision      37.6 ms p50 · warm · engine",
		"Cold          640 ms · first request",
		"Short         37.4 ms p50 · 7 cases under 1,000 tok",
		"Long          417 ms p50 · 4,386 tok",
		"p95           417 ms",
		"Throughput    11.7 req/s · c1",
		"Requests      168 · 8 cases × 21 passes · 0 errors",
		"SYNTHETIC",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the card lacks %q:\n%s", want, out)
		}
	}
	// Words and figures that belong to the token card, or to nobody's tape.
	for _, bad := range []string{"Decode", "TTFT", "Context", "MEMORY", "FLAGS", "38.8", "no /proc view", "Cache ", "Reference"} {
		if strings.Contains(out, bad) {
			t.Errorf("the decision card contains %q:\n%s", bad, out)
		}
	}
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := Width(line); w != CardWidth {
			t.Errorf("line %d is %d columns, want %d: %q", i, w, CardWidth, line)
		}
	}
}

// TestDecisionClientTimingCaveat: a client-timed tape says so beside the hero
// and in one caveat line; an engine-timed one raises none.
func TestDecisionClientTimingCaveat(t *testing.T) {
	base := decisionExample(t)
	if strings.Contains(Text(base), "network and JSON") {
		t.Error("an engine-timed tape prints the client-timing caveat")
	}
	if cs := DecisionCard(base).Caveats; len(cs) != 0 {
		t.Errorf("engine-timed caveats = %v, want none", cs)
	}

	client := withDecision(base, func(d *tape.DecisionSummary) { d.TimingSource = tape.DecisionTimingClient })
	out := Text(client)
	for _, want := range []string{
		"Decision      37.6 ms p50 · warm · client, end to end",
		"! client timing: network and JSON are inside every latency",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the client-timed card lacks %q:\n%s", want, out)
		}
	}
	cs := DecisionCard(client).Caveats
	if len(cs) != 1 || cs[0].Code != CodeDecisionClientTiming {
		t.Errorf("client-timed caveats = %v, want one %s", cs, CodeDecisionClientTiming)
	}
}

// TestDecisionOptionalRows: Cache and Reference exist only when the tape has
// a hit or a reference file, and say what they found.
func TestDecisionOptionalRows(t *testing.T) {
	base := decisionExample(t)
	v := DecisionCard(base)
	for _, k := range []string{DecisionRowCache, DecisionRowReference} {
		if _, ok := v.Row(k); ok {
			t.Errorf("row %s present on a tape without it", k)
		}
	}

	both := withDecision(base, func(d *tape.DecisionSummary) {
		d.CacheHits = 12
		d.Reference = &tape.DecisionAgreement{File: "official-bf16.json", Questions: 40, MaxAbsDeltaP: 0.012, TopFlips: 0, BrierDelta: 0.0004}
	})
	out := Text(both)
	// The reference row wraps at its separators on a 54-column value, so
	// its two halves are looked for apart.
	for _, want := range []string{
		"Cache         12 hits · prompt partly reused",
		"Reference     max |Δp| 0.012 · 0 flips · Brier +0.0004",
		"vs official-bf16.json",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the card lacks %q:\n%s", want, out)
		}
	}
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := Width(line); w != CardWidth {
			t.Errorf("line %d is %d columns, want %d: %q", i, w, CardWidth, line)
		}
	}
}

// TestDecisionUnknownPrintsQuestionMark: a figure the tape lacks is "?", and
// a side of the suite with no prompt on it has no row at all.
func TestDecisionUnknownPrintsQuestionMark(t *testing.T) {
	s := &tape.RunSummary{Mode: tape.ModeDecision}
	out := Text(s)
	if !strings.Contains(out, "Decision      ? · warm · timing ?") {
		t.Errorf("an empty decision tape's hero:\n%s", out)
	}
	if !strings.Contains(out, "Cold          ? · first request") {
		t.Errorf("an empty decision tape's cold row:\n%s", out)
	}
	for _, none := range []string{"Short", "Long"} {
		if strings.Contains(out, none) {
			t.Errorf("a suite with no case on that side has a %s row:\n%s", none, out)
		}
	}
}

// TestDecisionLongRowCounts: several long cases print a count, not a token
// figure that belongs to one of them.
func TestDecisionLongRowCounts(t *testing.T) {
	s := withDecision(decisionExample(t), func(d *tape.DecisionSummary) {
		d.PerCase = append(append([]tape.DecisionCaseSummary(nil), d.PerCase...),
			tape.DecisionCaseSummary{CaseID: "long-09", InputTokens: 2100, Answered: 21})
	})
	r, _ := DecisionCard(s).Row(DecisionRowLong)
	if got := r.Detail(); got != "2 cases" {
		t.Errorf("long detail = %q, want %q", got, "2 cases")
	}
}

// TestDecisionJSONAndMarkdown: the surfaces that wrap the card do not leak the
// token-stream parts.
func TestDecisionJSONAndMarkdown(t *testing.T) {
	s := decisionExample(t)
	b, err := JSON(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "no_proc_view") {
		t.Errorf("a decision tape's JSON carries a token-stream caveat:\n%s", b)
	}
	if !strings.Contains(string(b), `"caveats": []`) {
		t.Errorf("an engine-timed decision tape's JSON caveats are not an empty array:\n%s", b)
	}
	md := Markdown(s)
	if strings.Contains(md, "llama-bench") || strings.Contains(md, "Reproduce") || strings.Contains(md, "| ") {
		t.Errorf("a decision tape's markdown carries the token-stream table:\n%s", md)
	}
}
