package card

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// clean is the fixture every case below starts from: a run nothing is wrong
// with. It is asserted to be clean rather than assumed, because a fixture that
// quietly acquires a caveat would turn every "this mutation adds code X" case
// below into a test that passes for the wrong reason.
func clean(t *testing.T) *tape.RunSummary {
	t.Helper()
	s := Example()
	if cs := Caveats(s); len(cs) != 0 {
		t.Fatalf("the base fixture is not clean: %+v", cs)
	}
	return s
}

// TestACleanRunHasNoCaveats. The empty list is the load-bearing case: it is
// the card saying, in the one field a consumer has to read, that nothing about
// this run disqualifies the figures on it. Every shipped fixture is checked,
// so a change that makes one of them "always cold" or "always contended" is
// caught here rather than by a reader wondering why every card has a caveat.
func TestACleanRunHasNoCaveats(t *testing.T) {
	for name, s := range map[string]*tape.RunSummary{
		"Example":            Example(),
		"ExampleConcurrent":  ExampleConcurrent(),
		"ExampleSpeculative": ExampleSpeculative(),
		"ExampleRounds":      ExampleRounds(),
		"ExampleSharded":     ExampleSharded(),
		"ExampleSweep":       ExampleSweep(),
	} {
		if cs := Caveats(s); len(cs) != 0 {
			t.Errorf("%s raises caveats: %+v", name, cs)
		}
		if w := warningSection(s); len(w) != 0 {
			t.Errorf("%s prints a warning block: %q", name, w)
		}
	}
}

// caveatCase is one code, the smallest change to a clean run that raises it,
// and the fragment the TEXT CARD must show for it. A "" fragment means the
// caveat has no place of its own on the card and travels only in the caveat
// line and in --json.
type caveatCase struct {
	code    string
	mutate  func(*tape.RunSummary)
	onCard  string
	notCard string
}

func caveatCases() []caveatCase {
	return []caveatCase{{
		code: CodeStreamsFailed,
		// Streams is the total the run sent, failures included
		// (internal/server/concurrent.go), so eight sent and one failed reads
		// "1 of 8" and never "1 of 9".
		mutate: func(s *tape.RunSummary) { s.Aggregate.Streams, s.Aggregate.StreamsFailed = 8, 1 },
		onCard: "1 of 8 streams failed",
	}, {
		code:   CodeAnswerCut,
		mutate: func(s *tape.RunSummary) { s.Timings.ReasoningN = s.Timings.PredictedN },
		onCard: "answer cut",
	}, {
		code: CodeShortGeneration,
		mutate: func(s *tape.RunSummary) {
			s.Timings.PredictedN, s.Timings.DecodeLabel = 19, "sample"
		},
		// Lesson 2 lives in the row's own label, and the caveat must not be
		// able to fire without it.
		onCard:  "Sample ",
		notCard: "Decode ",
	}, {
		code: CodeShortStream,
		// Four streams, one of them 10 tokens long, and a mean well over the
		// floor (TTP-83). The row keeps its Decode label — the aggregate is
		// still a rate — so the qualification is the caveat and nothing else,
		// and a card that relabels the whole run Sample fails here.
		mutate:  func(s *tape.RunSummary) { s.Aggregate.Streams, s.Aggregate.MinPredictedN = 4, 10 },
		onCard:  "Decode ",
		notCard: "Sample ",
	}, {
		code: CodeColdCache,
		mutate: func(s *tape.RunSummary) {
			s.Cache.Label = tape.CacheCold
			s.Memory.MajFaultsPerToken = tape.ColdMajFaultsPerToken + 0.4
		},
		onCard: "cold",
	}, {
		code: CodeShortPromptForPrefill,
		mutate: func(s *tape.RunSummary) {
			s.Cache.PromptTotal, s.Cache.HitTokens = 63, 0
			s.Timings.PromptN, s.Timings.CacheN = 63, 0
		},
		onCard: "not a prefill measurement",
	}, {
		code:   CodeClientDisagrees,
		mutate: func(s *tape.RunSummary) { s.Timings.ClientAgreesWithServer = false },
	}, {
		code:   CodeRecorded,
		mutate: func(s *tape.RunSummary) { s.Warnings = []string{"the -ot rule was dropped"} },
		onCard: "the -ot rule was dropped",
	}, {
		code:   CodeMachineContended,
		mutate: func(s *tape.RunSummary) { s.Contention.Contended = true },
		onCard: "contended: yes",
	}, {
		code: CodeConditionsChanged,
		mutate: func(s *tape.RunSummary) {
			s.Contention.Witnesses = []tape.ContentionWitness{
				{Edge: "start", CPUMaxKHz: 3_600_000},
				{Edge: "end", CPUMaxKHz: 2_700_000},
			}
		},
		onCard: "CPU cap 3.6 → 2.7 GHz",
	}, {
		code: CodeRunCutByClock,
		mutate: func(s *tape.RunSummary) {
			s.Limit = tape.LimitSummary{For: 20 * time.Second, CutAt: 20 * time.Second, MinTokens: tape.MinCutTokens}
		},
	}, {
		code:   CodeNoProcView,
		mutate: func(s *tape.RunSummary) { s.Memory = tape.MemorySummary{} },
	}}
}

// TestEachCaveatHasAFixtureThatRaisesIt. One tape per code, and the code has
// to be the ONLY one the change raises: a predicate that fires on something it
// was not asked about is how a card ends up with a warning block nobody trusts.
func TestEachCaveatHasAFixtureThatRaisesIt(t *testing.T) {
	for _, c := range caveatCases() {
		t.Run(c.code, func(t *testing.T) {
			s := clean(t)
			c.mutate(s)
			got := Caveats(s)
			codes := make([]string, len(got))
			for i, g := range got {
				codes[i] = g.Code
			}
			if len(codes) != 1 || codes[0] != c.code {
				t.Fatalf("Caveats = %v, want exactly [%s]", codes, c.code)
			}
			if strings.TrimSpace(got[0].Text) == "" {
				t.Errorf("%s carries no sentence", c.code)
			}
			switch got[0].Severity {
			case SeverityFigure, SeverityRun, SeverityView:
			default:
				t.Errorf("%s has severity %q, which is not one of the three levels", c.code, got[0].Severity)
			}
		})
	}
}

// TestAQualifiedFigureCannotBeRenderedWithoutItsQualification is the gate axis
// this repo was missing, and it is why a human had to notice "Prefill 5.6
// tok/s" (TTP-74, 2026-09-14).
//
// Every renderer read the same summary and each decided for itself whether a
// figure needed qualifying, so the card could print a rate whose condition was
// only in a field the reader never looked at. This asserts the join: when a
// caveat fires, the card that prints the figure prints its qualification too,
// and `--json` carries the code. The card and the list cannot drift apart
// without failing here.
func TestAQualifiedFigureCannotBeRenderedWithoutItsQualification(t *testing.T) {
	for _, c := range caveatCases() {
		t.Run(c.code, func(t *testing.T) {
			s := clean(t)
			c.mutate(s)
			text := Text(s)

			if c.onCard != "" && !strings.Contains(text, c.onCard) {
				t.Errorf("the card raises %s but does not show %q:\n%s", c.code, c.onCard, text)
			}
			if c.notCard != "" && strings.Contains(text, c.notCard) {
				t.Errorf("the card raises %s and still shows %q:\n%s", c.code, c.notCard, text)
			}
			// The caveat line itself: every run with a caveat has one, and the
			// most serious caveat's own sentence is on it.
			if !strings.Contains(text, "! ") {
				t.Errorf("the card raises %s and prints no caveat line:\n%s", c.code, text)
			}
			if !hasWrapped(text, Caveats(s)[0].Text) {
				t.Errorf("the caveat line does not carry the sentence %q:\n%s", Caveats(s)[0].Text, text)
			}
			// And --json carries the code, so an agent that never reads the
			// card gets the same answer.
			if !strings.Contains(string(mustJSON(t, s)), `"code": "`+c.code+`"`) {
				t.Errorf("--json does not carry %s", c.code)
			}
		})
	}
}

// hasWrapped reports whether want appears in the card, allowing for the line
// wrapping and gutter the caveat block applies. The card is 72 columns and
// most of these sentences are longer, so a plain Contains would only ever pass
// for the short ones.
func hasWrapped(card, want string) bool {
	flat := strings.Join(strings.Fields(strings.ReplaceAll(card, "│", " ")), " ")
	return strings.Contains(flat, strings.Join(strings.Fields(want), " "))
}

func mustJSON(t *testing.T, s *tape.RunSummary) []byte {
	t.Helper()
	b, err := JSON(s)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	return b
}

// TestTheCaveatLineIsALineAndNotAWall: five caveats at once must not turn the
// bottom of the card into a block of exclamation marks. The line names how
// many there are and spells out the most serious; the rest are codes, which is
// the handle that finds their sentence in --json.
func TestTheCaveatLineIsALineAndNotAWall(t *testing.T) {
	s := clean(t)
	for _, c := range caveatCases() {
		c.mutate(s)
	}
	cs := Caveats(s)
	if len(cs) < 5 {
		t.Fatalf("the combined fixture raises only %d caveats", len(cs))
	}
	block := warningSection(s)
	if len(block) > 4 {
		t.Errorf("%d caveats produced a %d-line block, which is a wall:\n%s",
			len(cs), len(block), strings.Join(block, "\n"))
	}
	joined := strings.Join(block, " ")
	if !strings.Contains(joined, "caveats") {
		t.Errorf("the line does not say how many there are:\n%s", joined)
	}
	rest := cs[1:]
	for i, c := range rest {
		named := strings.Contains(joined, c.Code)
		switch {
		case i < maxCaveatCodesListed && !named:
			t.Errorf("the line does not name %s:\n%s", c.Code, joined)
		case i >= maxCaveatCodesListed && named:
			t.Errorf("the line names %s, past the %d it lists:\n%s", c.Code, maxCaveatCodesListed, joined)
		}
	}
	if n := len(rest) - maxCaveatCodesListed; n > 0 {
		if want := fmt.Sprintf("+%d more", n); !strings.Contains(joined, want) {
			t.Errorf("the line does not say %q:\n%s", want, joined)
		}
	}
	// The most serious one is spelled out, and it is the first in rank order.
	if cs[0].Code != CodeStreamsFailed {
		t.Errorf("the most serious caveat is %s; streams_failed outranks everything", cs[0].Code)
	}
	if !hasWrapped(strings.Join(block, "\n"), cs[0].Text) {
		t.Errorf("the line does not spell out the most serious caveat:\n%s", joined)
	}
}

// TestCaveatsAreRankedBySeverity: the list is ordered, because the card prints
// the first entry and a consumer that reads only caveats[0] has to get the one
// that matters most. Severity must not go backwards down the list.
func TestCaveatsAreRankedBySeverity(t *testing.T) {
	s := clean(t)
	for _, c := range caveatCases() {
		c.mutate(s)
	}
	rank := map[string]int{SeverityFigure: 0, SeverityRun: 1, SeverityView: 2}
	got := Caveats(s)
	for i := 1; i < len(got); i++ {
		if rank[got[i].Severity] < rank[got[i-1].Severity] {
			t.Errorf("%s (%s) is listed after %s (%s)",
				got[i].Code, got[i].Severity, got[i-1].Code, got[i-1].Severity)
		}
	}
	// Every code the package declares has a rank, or sort.SliceStable would
	// silently file it first.
	for _, c := range caveatCases() {
		if _, ok := caveatRank[c.code]; !ok {
			t.Errorf("%s has no entry in caveatRank", c.code)
		}
	}
}

// TestJSONCarriesAnEmptyCaveatsArray: the field is always present and is [],
// never null, on a clean run. A consumer must not have to tell "no caveats"
// from "this toktape does not have the field".
func TestJSONCarriesAnEmptyCaveatsArray(t *testing.T) {
	var doc struct {
		Caveats *[]Caveat `json:"caveats"`
	}
	if err := json.Unmarshal(mustJSON(t, clean(t)), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Caveats == nil {
		t.Fatal("--json has no caveats field")
	}
	if len(*doc.Caveats) != 0 {
		t.Errorf("a clean run reports caveats %+v", *doc.Caveats)
	}
}

// TestJSONStillUnmarshalsIntoARunSummary: the caveats ride alongside the
// summary, not inside it. Every consumer that parses `toktape card --json`
// into a tape.RunSummary today keeps working, and "warnings" keeps being the
// array of strings the recorder wrote.
func TestJSONStillUnmarshalsIntoARunSummary(t *testing.T) {
	s := clean(t)
	s.Warnings = []string{"pid not found, no /proc view"}
	b := mustJSON(t, s)

	var back tape.RunSummary
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("a run summary no longer unmarshals from --json: %v", err)
	}
	if back.ID != s.ID || back.Timings.PredictedPerSecond != s.Timings.PredictedPerSecond {
		t.Errorf("the summary did not survive the round trip: %q %v", back.ID, back.Timings.PredictedPerSecond)
	}
	if len(back.Warnings) != 1 || back.Warnings[0] != s.Warnings[0] {
		t.Errorf("warnings = %v, want the recorder's own text unchanged", back.Warnings)
	}
}

// TestRunCutByClockTellsTheTwoCasesApart (TTP-76): a run cut at its budget and
// a run whose token floor held the cut back are different facts about the
// machine, and one sentence for both would hide the second.
func TestRunCutByClockTellsTheTwoCasesApart(t *testing.T) {
	atBudget := clean(t)
	atBudget.Limit = tape.LimitSummary{For: 20 * time.Second, CutAt: 20 * time.Second, MinTokens: tape.MinCutTokens}
	held := clean(t)
	held.Limit = tape.LimitSummary{For: 20 * time.Second, CutAt: 31*time.Second + 400*time.Millisecond, MinTokens: tape.MinCutTokens}

	a, b := runCutByClockText(atBudget), runCutByClockText(held)
	if a == "" || b == "" {
		t.Fatalf("a cut run has no sentence: %q / %q", a, b)
	}
	if a == b {
		t.Errorf("both cases read the same: %q", a)
	}
	if !strings.Contains(b, "31.4s") || !strings.Contains(b, "20s") {
		t.Errorf("the held-back sentence names neither the cut nor the budget: %q", b)
	}
	if !strings.Contains(b, "floor") {
		t.Errorf("the held-back sentence does not say why the run ran long: %q", b)
	}
	// No clock, no sentence.
	if got := runCutByClockText(clean(t)); got != "" {
		t.Errorf("a run with no clock reports %q", got)
	}
}

// TestIsSampleIsTheOnlyOwnerOfTheSampleVerdict: the row label and the caveat
// ask one predicate. A tape whose recorder wrote "decode" over a generation
// that is in fact a sample is labelled by its own count, not by the stale
// verdict — the direction that cannot overstate the figure.
func TestIsSampleIsTheOnlyOwnerOfTheSampleVerdict(t *testing.T) {
	s := clean(t)
	s.Timings.PredictedN, s.Timings.DecodeLabel = 19, "decode"
	if !isSample(s) {
		t.Fatal("19 generated tokens is a sample whatever the tape's label says")
	}
	if !strings.Contains(Text(s), "Sample ") {
		t.Errorf("the row is still labelled Decode:\n%s", Text(s))
	}
	// Zero tokens is no generation, not a short one: the row prints "?" and
	// there is nothing to warn about.
	none := clean(t)
	none.Timings.PredictedN, none.Timings.DecodeLabel = 0, ""
	if isSample(none) {
		t.Error("a run that generated nothing is not a sample")
	}
}

// TestShortPromptUsesTheWholePrompt: the threshold is measured against the
// prompt the run sent, cached prefix included, which is the count the Prefill
// row prints. A 512-token prompt served entirely from the prefix cache is
// still a 512-token prompt.
func TestShortPromptUsesTheWholePrompt(t *testing.T) {
	s := clean(t)
	s.Cache.PromptTotal, s.Cache.HitTokens = 512, 500
	s.Timings.PromptN, s.Timings.CacheN = 12, 500
	if ShortPrompt(s) {
		t.Error("a 512-token prompt is not short because most of it was cached")
	}
	s.Cache.PromptTotal, s.Cache.HitTokens = MinPrefillPromptTokens-1, 0
	s.Timings.PromptN, s.Timings.CacheN = MinPrefillPromptTokens-1, 0
	if !ShortPrompt(s) {
		t.Errorf("%d prompt tokens is under the %d threshold", MinPrefillPromptTokens-1, MinPrefillPromptTokens)
	}
	// An unobserved prompt length is unknown, not short.
	s.Cache = tape.CacheSummary{}
	s.Timings.PromptN, s.Timings.CacheN = 0, 0
	if ShortPrompt(s) {
		t.Error("a prompt nobody counted is unknown, not short")
	}
}
