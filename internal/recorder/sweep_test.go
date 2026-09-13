package recorder

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// TestParseSpecNMax pins the --spec-n-max list (TTP-35): positive integers in
// run order, and an error that names the element it is about.
func TestParseSpecNMax(t *testing.T) {
	good := []struct {
		in   string
		want []int
	}{
		{"3,5", []int{3, 5}},
		{"5,3", []int{5, 3}},
		{" 3 , 5 ", []int{3, 5}},
		{"8", []int{8}},
		{"1,2,4,8,16", []int{1, 2, 4, 8, 16}},
	}
	for _, tc := range good {
		got, err := ParseSpecNMax(tc.in)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseSpecNMax(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}

	bad := []struct{ in, want string }{
		{"", "empty list"},
		{"  ", "empty list"},
		{"3,,5", `element 2 "" is not a positive integer`},
		{"3,x", `element 2 "x" is not a positive integer`},
		{"0", `element 1 "0" is not a positive integer`},
		{"3,-5", `element 2 "-5" is not a positive integer`},
		{"2.5", `element 1 "2.5" is not a positive integer`},
		{"3,", `element 2 "" is not a positive integer`},
		{"3,5,3", "element 3: n_max 3 is listed twice (element 1)"},
	}
	for _, tc := range bad {
		_, err := ParseSpecNMax(tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ParseSpecNMax(%q) error = %v, want it to contain %q", tc.in, err, tc.want)
		}
	}
}

func req(content string, params map[string]any) server.StreamRequest {
	return server.StreamRequest{
		Messages: []tape.Message{{Role: "user", Content: content}},
		Params:   params,
	}
}

// TestExpandSweep: every value runs the whole prompt set, the groups are
// contiguous in the order given, and the caller's rounds are never written.
func TestExpandSweep(t *testing.T) {
	in := []Round{
		{Name: "sql", Prompts: []server.StreamRequest{
			req("alpha", map[string]any{"temperature": 0.2}),
			req("alpha-2", nil),
		}},
		{Name: "", Prompts: []server.StreamRequest{req("beta", nil)}},
	}

	out := expandSweep(in, []int{3, 5})
	if len(out) != 4 {
		t.Fatalf("%d rounds, want 2 rounds × 2 values", len(out))
	}
	want := []struct {
		name  string
		nmax  int
		first string
		n     int
	}{
		{"sql", 3, "alpha", 2}, {"", 3, "beta", 1},
		{"sql", 5, "alpha", 2}, {"", 5, "beta", 1},
	}
	for k, w := range want {
		rd := out[k]
		if rd.Name != w.name || len(rd.Prompts) != w.n || rd.Prompts[0].Messages[0].Content != w.first {
			t.Errorf("round %d = %q with %d prompts starting %q; want %q, %d, %q",
				k, rd.Name, len(rd.Prompts), rd.Prompts[0].Messages[0].Content, w.name, w.n, w.first)
		}
		for i, p := range rd.Prompts {
			if got := p.Params[specNMaxParam]; got != w.nmax {
				t.Errorf("round %d prompt %d speculative.n_max = %v, want %d", k, i, got, w.nmax)
			}
		}
	}
	if got := out[0].Prompts[0].Params["temperature"]; got != 0.2 {
		t.Errorf("the prompt's own params were dropped: temperature = %v", got)
	}

	// The caller's rounds are untouched and share nothing with the copies.
	if _, ok := in[0].Prompts[0].Params[specNMaxParam]; ok {
		t.Error("expandSweep wrote speculative.n_max into the caller's Params map")
	}
	if in[0].Prompts[1].Params != nil {
		t.Errorf("expandSweep gave the caller's nil Params a map: %v", in[0].Prompts[1].Params)
	}
	out[0].Prompts[0].Params["temperature"] = 1.0
	out[0].Prompts[0].Messages[0].Content = "changed"
	if in[0].Prompts[0].Params["temperature"] != 0.2 || in[0].Prompts[0].Messages[0].Content != "alpha" {
		t.Error("a copy aliases the caller's Params or Messages")
	}
	if out[2].Prompts[0].Params["temperature"] != 0.2 || out[2].Prompts[0].Params[specNMaxParam] != 5 {
		t.Errorf("the n_max 5 copy aliases the n_max 3 copy: %v", out[2].Prompts[0].Params)
	}

	if got := expandSweep(in, nil); !reflect.DeepEqual(got, in) {
		t.Errorf("no values changed the rounds: %+v", got)
	}
}

// TestSweepBaseDefaultPrompts: without a prompts file the sweep repeats
// exactly what a plain run sends — the default prompts, one per stream, under
// --n-predict — as one round per value.
func TestSweepBaseDefaultPrompts(t *testing.T) {
	o := Options{Concurrency: 2, MaxTokens: 256}.normalize()
	base := sweepBase(o)
	if len(base) != 1 || len(base[0].Prompts) != 2 {
		t.Fatalf("base = %+v, want one round of two prompts", base)
	}
	defaults := server.DefaultPrompts(2)
	for i, p := range base[0].Prompts {
		if p.Messages[0].Content != defaults[i].Messages[0].Content {
			t.Errorf("prompt %d = %q, want the default %q", i, p.Messages[0].Content, defaults[i].Messages[0].Content)
		}
		// The default prompts carry 320; the rounds path would keep it, so
		// the base has to carry --n-predict already.
		if p.MaxTokens != 256 {
			t.Errorf("prompt %d MaxTokens = %d, want --n-predict's 256, not the default prompt's %d", i, p.MaxTokens, defaults[i].MaxTokens)
		}
	}
	rounds := expandSweep(base, []int{3, 5})
	if len(rounds) != 2 || rounds[1].Prompts[1].Params[specNMaxParam] != 5 {
		t.Errorf("expanded = %+v, want two rounds, the second at n_max 5", rounds)
	}

	// Without --n-predict the default prompts keep their own cap, as a plain
	// run's do.
	if got := sweepBase(Options{}.normalize())[0].Prompts[0].MaxTokens; got != server.DefaultPrompts(1)[0].MaxTokens {
		t.Errorf("MaxTokens without --n-predict = %d, want the default prompt's own", got)
	}

	// A prompts file's rounds are kept as they are, and a round with no
	// prompt gets the default set it would have had.
	o = Options{Rounds: []Round{{Name: "a", Prompts: []server.StreamRequest{req("x", nil)}}, {Name: "b"}}, Concurrency: 2}.normalize()
	base = sweepBase(o)
	if len(base) != 2 || base[0].Prompts[0].Messages[0].Content != "x" || len(base[1].Prompts) != 2 {
		t.Errorf("base = %+v", base)
	}
	if len(o.Rounds[1].Prompts) != 0 {
		t.Error("sweepBase filled the caller's empty round")
	}
}

func TestSpecNMaxOf(t *testing.T) {
	cases := []struct {
		v    any
		want int
	}{
		{3, 3},
		{int64(5), 5},
		{float64(5), 5},
		{json.Number("8"), 8},
		{2.5, 0},
		{0, 0},
		{-3, 0},
		{"3", 0},
		{nil, 0},
	}
	for _, tc := range cases {
		if got := specNMaxOf(map[string]any{specNMaxParam: tc.v}); got != tc.want {
			t.Errorf("specNMaxOf(%#v) = %d, want %d", tc.v, got, tc.want)
		}
	}
	if got := specNMaxOf(nil); got != 0 {
		t.Errorf("specNMaxOf(nil) = %d", got)
	}
}

// withNMax stamps the records of round k with the n_max they were sent with,
// as the recorder records it: the parameter map of the request body.
func withNMax(recs []tape.RequestRecord, byRound map[int]any) []tape.RequestRecord {
	for i := range recs {
		if v, ok := byRound[recs[i].Round]; ok {
			recs[i].Prompt.Params = map[string]any{specNMaxParam: v, "max_tokens": 256}
		}
	}
	return recs
}

// TestReduceRoundsStampsSpecNMax (TTP-35, 2026-09-13) is the FAIL-first gate
// for the reduction of a sweep: each round names the n_max its requests were
// sent with, and the run names every value that ran with one group per value.
func TestReduceRoundsStampsSpecNMax(t *testing.T) {
	// Round 0 as recorded in process (int), round 1 as read back from a tape
	// (float64).
	recs := withNMax(twoRoundsWithAGap(), map[int]any{0: 3, 1: float64(5)})
	// A stream that failed before the server answered records no parameters;
	// the round still has its value from the other stream.
	recs[0].Prompt.Params = nil

	_, per, spread := reduceRounds(recs, []string{"sql", "sql"}, 2)
	if len(per) != 2 || per[0].SpecNMax != 3 || per[1].SpecNMax != 5 {
		t.Fatalf("PerRound SpecNMax = %+v, want 3 then 5", per)
	}

	s := tape.RunSummary{Rounds: 2, PerRound: per, Spread: spread}
	applySweep(&s, recs)
	if !reflect.DeepEqual(s.SpecNMax, []int{3, 5}) {
		t.Errorf("SpecNMax = %v, want [3 5]", s.SpecNMax)
	}
	want := []tape.SpecNMaxGroup{
		{NMax: 3, Rounds: 1, Spread: *roundSpread(per[:1])},
		{NMax: 5, Rounds: 1, Spread: *roundSpread(per[1:])},
	}
	if !reflect.DeepEqual(s.BySpecNMax, want) {
		t.Errorf("BySpecNMax = %+v, want %+v", s.BySpecNMax, want)
	}
	if s.BySpecNMax[0].Spread.PerStreamPredictedPerSecond.Median != 20 {
		t.Errorf("n_max 3 median = %v, want round 0's per-stream mean 20", s.BySpecNMax[0].Spread.PerStreamPredictedPerSecond.Median)
	}

	t.Run("a run without a sweep carries nothing", func(t *testing.T) {
		recs := twoRoundsWithAGap()
		_, per, spread := reduceRounds(recs, []string{"sql", ""}, 2)
		for _, p := range per {
			if p.SpecNMax != 0 {
				t.Errorf("round %d SpecNMax = %d, want 0", p.Index, p.SpecNMax)
			}
		}
		s := tape.RunSummary{Rounds: 2, PerRound: per, Spread: spread}
		applySweep(&s, recs)
		if s.SpecNMax != nil || s.BySpecNMax != nil {
			t.Errorf("SpecNMax %v, BySpecNMax %v; want both nil", s.SpecNMax, s.BySpecNMax)
		}
	})

	t.Run("a sweep cut to one round still names its value", func(t *testing.T) {
		recs := withNMax(twoRoundsWithAGap()[:2], map[int]any{0: 3})
		_, per, spread := reduceRounds(recs, []string{""}, 2)
		s := tape.RunSummary{Rounds: 1, PerRound: per, Spread: spread}
		applySweep(&s, recs)
		if !reflect.DeepEqual(s.SpecNMax, []int{3}) || s.BySpecNMax != nil {
			t.Errorf("SpecNMax %v, BySpecNMax %v; want [3] and no groups (one round has no PerRound)", s.SpecNMax, s.BySpecNMax)
		}
	})

	t.Run("values read back from JSON", func(t *testing.T) {
		b, err := json.Marshal(withNMax(twoRoundsWithAGap(), map[int]any{0: 5, 1: 3}))
		if err != nil {
			t.Fatal(err)
		}
		var back []tape.RequestRecord
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		if got := sweepValues(back); !reflect.DeepEqual(got, []int{5, 3}) {
			t.Errorf("sweepValues after a JSON round trip = %v, want [5 3]", got)
		}
	})
}

// TestSweepGroups: a group per value in first-appearance order, over the
// rounds that carry it wherever they sit.
func TestSweepGroups(t *testing.T) {
	per := []tape.RoundSummary{
		{Index: 0, SpecNMax: 5, PerStreamPredictedPerSecond: 10},
		{Index: 1, SpecNMax: 3, PerStreamPredictedPerSecond: 20},
		{Index: 2, SpecNMax: 5, PerStreamPredictedPerSecond: 14},
		{Index: 3, PerStreamPredictedPerSecond: 99},
	}
	got := sweepGroups(per)
	if len(got) != 2 || got[0].NMax != 5 || got[0].Rounds != 2 || got[1].NMax != 3 || got[1].Rounds != 1 {
		t.Fatalf("groups = %+v, want n_max 5 over 2 rounds, then 3 over 1", got)
	}
	if m := got[0].Spread.PerStreamPredictedPerSecond; m.Median != 12 || m.Min != 10 || m.Max != 14 {
		t.Errorf("n_max 5 spread = %+v, want 12 (10–14)", m)
	}
	if sweepGroups(per[3:]) != nil {
		t.Error("rounds with no value made a group")
	}
}

// TestExampleSweepFollowsTheReduction: the card fixture's spreads, groups and
// run-level draft totals are what this package's reduction computes from its
// rounds, so the goldens drawn from it pin the recorder's arithmetic.
func TestExampleSweepFollowsTheReduction(t *testing.T) {
	s := card.ExampleSweep()
	if got := roundSpread(s.PerRound); *got != *s.Spread {
		t.Errorf("roundSpread(PerRound) = %+v, fixture says %+v", *got, *s.Spread)
	}
	if got := sweepGroups(s.PerRound); !reflect.DeepEqual(got, s.BySpecNMax) {
		t.Errorf("sweepGroups(PerRound) = %+v\nfixture says        %+v", got, s.BySpecNMax)
	}
	var values []int
	for _, p := range s.PerRound {
		if !containsInt(values, p.SpecNMax) {
			values = append(values, p.SpecNMax)
		}
	}
	if !reflect.DeepEqual(values, s.SpecNMax) {
		t.Errorf("the rounds ran %v, SpecNMax says %v", values, s.SpecNMax)
	}
	drafted, accepted := 0, 0
	for k, p := range s.PerRound {
		if p.Index != k {
			t.Errorf("round %d has Index %d", k, p.Index)
		}
		drafted += *p.DraftN
		accepted += *p.DraftNAccepted
	}
	if drafted != *s.Timings.DraftN || accepted != *s.Timings.DraftNAccepted {
		t.Errorf("rounds sum to %d/%d drafted/accepted, run-level Timings say %d/%d",
			drafted, accepted, *s.Timings.DraftN, *s.Timings.DraftNAccepted)
	}
	if s.Rounds != len(s.PerRound) || s.Rounds != len(s.SpecNMax)*6 || s.Concurrency != s.Aggregate.Streams {
		t.Errorf("Rounds %d with %d PerRound over %v; Concurrency %d vs Aggregate.Streams %d",
			s.Rounds, len(s.PerRound), s.SpecNMax, s.Concurrency, s.Aggregate.Streams)
	}
}
