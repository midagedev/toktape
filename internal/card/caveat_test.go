package card

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/placement"
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
		code: CodeStreamsNotConcurrent,
		// Two streams sent at once, both answered, neither too short for a
		// window — and the token timeline says they took turns
		// (Aggregate.PeakDecodingStreams, 2026-09-15).
		mutate: func(s *tape.RunSummary) {
			s.Concurrency = 2
			s.Aggregate.Streams, s.Aggregate.MinPredictedN = 2, 100
			s.Aggregate.PeakDecodingStreams = 1
			// The mutation is about the timeline, so the arithmetic has to
			// agree: the clean fixture's aggregate is one stream's rate,
			// and two streams at that rate with this aggregate is a ragged
			// run too (TTP-108, 2026-09-19).
			s.Aggregate.AggregatePredictedPerSecond = 2 * s.Aggregate.PerStreamPredictedPerSecond
		},
		onCard: "decoded one at a time",
	}, {
		// TTP-108: four streams at 17.4 each is 69.6, not this aggregate.
		code: CodeRaggedAggregate,
		mutate: func(s *tape.RunSummary) {
			s.Concurrency = 4
			s.Aggregate.Streams, s.Aggregate.MinPredictedN = 4, 280
			s.Aggregate.AggregatePredictedPerSecond = 12.0
		},
		// The Streams block's own clause is the reason the caveat exists;
		// the sentence is checked wrap-insensitively by the shared test.
		onCard: "not all decoding at once",
	}, {
		code: CodePlacementContradicted,
		// The estimate splits the model across both cards; the run's own
		// reading says GPU1 held one MiB (lead, 2026-09-15). The q6k take's
		// shape: the server was started under CUDA_VISIBLE_DEVICES, which
		// the argument parser cannot see, so the placement is a guess about
		// a machine this run was not.
		mutate: func(s *tape.RunSummary) {
			s.GPUsAtEnd[1].ProcBytes = 0
			s.GPUsAtEnd[1].UsedBytes = 1 << 20
		},
		// Wrap-safe on purpose: onCard is a plain Contains against the wrapped
		// card, and the sentence's tail ("... are not this run's") breaks
		// across lines at this width. The whole sentence is checked by
		// hasWrapped below.
		onCard: "weights on GPU1",
	}, {
		code: CodeBandwidthOverCeiling,
		// The recorded figure is the model's own product, put 10x over the
		// ceiling the placement allows (2026-09-16). The qwen38 tapes printed
		// "1416 GB/s from RAM, 978% of peak" on a 144.8 GB/s bus because a
		// 26.8 GiB lookup table was counted as streamed weight traffic; the
		// refusal and this caveat are the recurrence layer of that defect.
		mutate: func(s *tape.RunSummary) { s.Timings.EffectiveBandwidthBytesPerSec = 9_362_000_000_000 },
		// Wrap-safe: the words that open the sentence's first wrapped line.
		onCard:  "implies read 9362 GB/s",
		notCard: "of peak",
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
		// TTP-88: a second run of the same prompt — 497 of 512 tokens from
		// the cache. The prefill rate beside it is a cache-hit rate, and
		// only the caveat says so.
		code: CodeCachedPrefill,
		mutate: func(s *tape.RunSummary) {
			s.Cache.PromptTotal, s.Cache.HitTokens = 512, 497
			s.Cache.HitRatio = 497.0 / 512
			s.Cache.Label = tape.CacheCached
			s.Timings.PromptN, s.Timings.CacheN = 15, 497
		},
		onCard: "cached prefill",
	}, {
		code:   CodeClientDisagrees,
		mutate: func(s *tape.RunSummary) { s.Timings.ClientAgreesWithServer = false },
	}, {
		// TTP-99: the server reported no timings, so the recorder's clock is
		// the record. Flipping Source alone must raise exactly this: the
		// clientDisagrees guard stands aside for a run with one clock.
		code:   CodeClientTimed,
		mutate: func(s *tape.RunSummary) { s.Timings.Source = "client" },
		onCard: "client-timed",
	}, {
		// TTP-99: the server sent no usage figure, so chunks were counted.
		// The count is not a token count; the shortness caveats stand aside
		// (their sentences count tokens), so exactly this fires.
		code:   CodeTokensUncounted,
		mutate: func(s *tape.RunSummary) { s.Timings.PredictedNSource = "chunks" },
		onCard: "tokens uncounted",
	}, {
		// TTP-106: thinking off was sent and the streams reasoned anyway,
		// which is what llama-server does with chat_template_kwargs when it
		// was started without --jinja. The count is the recorder's, taken at
		// record time because the card never reads Tape.Requests.
		code: CodeThinkingIgnored,
		mutate: func(s *tape.RunSummary) {
			s.Sampling.Thinking = "off"
			s.Sampling.ThoughtAnyway = s.Concurrency
		},
		onCard:  "thinking off (ignored)",
		notCard: "thinking off ·",
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
		// Compared wrap-insensitively (2026-09-19, TTP-99): "+N more" is two
		// words, so the card may wrap between them — and with the longer
		// code list it does ("· +6" / "  more"). The gutter then puts three
		// spaces where the sentence has one. hasWrapped normalises the same
		// way for sentences; FAIL-first: joining the raw block fails now
		// that the combined fixture wraps there.
		flat := strings.Join(strings.Fields(joined), " ")
		if want := fmt.Sprintf("+%d more", n); !strings.Contains(flat, want) {
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

// TestAnOvershootTooSmallToPrintIsNotTheFloor (lead, 2026-09-14): the clock
// waits out its timer, polls for the floor, then cancels and drains, so a cut
// always lands a shade past the budget. The v0.2.0 hero take was cut 257 us
// past a 30 s budget and the card said "the clock cut at 30s, not the 30s
// asked for" — two identical numbers and a claim that they differ, plus a
// cause, the token floor, that nothing had held anything back.
func TestAnOvershootTooSmallToPrintIsNotTheFloor(t *testing.T) {
	s := clean(t)
	s.Limit = tape.LimitSummary{
		For:       30 * time.Second,
		CutAt:     30*time.Second + 257819*time.Nanosecond,
		MinTokens: tape.MinCutTokens,
	}
	got := runCutByClockText(s)
	if strings.Contains(got, "not the") || strings.Contains(got, "floor") {
		t.Errorf("an overshoot of %v reads as the floor holding the run back: %q",
			s.Limit.CutAt-s.Limit.For, got)
	}
	if got == "" {
		t.Error("the run was still cut by its clock and says nothing")
	}
	// And the sentence it does get is the one a run cut at its budget gets,
	// because that is what happened.
	atBudget := clean(t)
	atBudget.Limit = tape.LimitSummary{For: 30 * time.Second, CutAt: 30 * time.Second, MinTokens: tape.MinCutTokens}
	if want := runCutByClockText(atBudget); got != want {
		t.Errorf("cut %v past the budget reads as\n  %q\nbut cut exactly at it reads as\n  %q",
			s.Limit.CutAt-s.Limit.For, got, want)
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

// TestStreamsNotConcurrentTextAndGates (lead, 2026-09-15): two sentences — the
// trial's own "one at a time" when the peak is 1, the partial one when it is
// higher — and every gate that must hold the caveat back. A caveat that fires
// on a run the timeline does not contradict is how a warning block loses the
// reader, so each gate is its own case.
func TestStreamsNotConcurrentTextAndGates(t *testing.T) {
	// twoStreams is the trial's summary shape: two streams sent at once, both
	// answered, neither too short for a window.
	twoStreams := func(mutate func(*tape.RunSummary)) []Caveat {
		s := clean(t)
		s.Concurrency = 2
		s.Aggregate.Streams, s.Aggregate.MinPredictedN = 2, 100
		mutate(s)
		// The timeline is what these cases vary; the arithmetic agrees, or
		// the ragged_aggregate caveat (TTP-108, 2026-09-19) fires beside
		// the sentence under test.
		s.Aggregate.AggregatePredictedPerSecond = float64(streamsSent(s)) * s.Aggregate.PerStreamPredictedPerSecond
		return Caveats(s)
	}
	has := func(cs []Caveat, code string) bool {
		for _, c := range cs {
			if c.Code == code {
				return true
			}
		}
		return false
	}

	cs := twoStreams(func(s *tape.RunSummary) { s.Aggregate.PeakDecodingStreams = 1 })
	if want := "the 2 streams decoded one at a time: the aggregate is one stream behind a queue, not 2 at once"; len(cs) != 1 || cs[0].Text != want {
		t.Errorf("peak 1 of 2: Caveats = %+v, want exactly that one sentence %q", cs, want)
	}
	if cs[0].Severity != SeverityFigure {
		t.Errorf("severity = %q, want figure: the aggregate row's claim is what is wrong", cs[0].Severity)
	}
	cs = twoStreams(func(s *tape.RunSummary) {
		s.Concurrency = 3
		s.Aggregate.PeakDecodingStreams = 2
	})
	if want := "at most 2 of 3 streams decoded at once: the aggregate is not 3 concurrent streams"; len(cs) != 1 || cs[0].Text != want {
		t.Errorf("peak 2 of 3: Caveats = %+v, want exactly that one sentence %q", cs, want)
	}

	// The gates. Some raise another code instead — a failed stream is
	// streams_failed's partial run, a short stream is short_stream's — so the
	// assertion is that THIS code stays out, not that the list is empty.
	for name, mutate := range map[string]func(*tape.RunSummary){
		"the peak equals the concurrency":            func(s *tape.RunSummary) { s.Aggregate.PeakDecodingStreams = 2 },
		"the peak is 0, a tape older than the field": func(s *tape.RunSummary) {},
		"a stream failed":                            func(s *tape.RunSummary) { s.Aggregate.StreamsFailed = 1 },
		"a stream is too short for a window":         func(s *tape.RunSummary) { s.Aggregate.MinPredictedN, s.Aggregate.ShortStreams = 10, 1 },
		"the minimum itself was never recorded":      func(s *tape.RunSummary) { s.Aggregate.MinPredictedN = 0 },
		"the run sent one stream":                    func(s *tape.RunSummary) { s.Concurrency = 1 },
	} {
		if cs := twoStreams(mutate); has(cs, CodeStreamsNotConcurrent) {
			t.Errorf("%s: the caveat fired on %+v", name, cs)
		}
	}
}

// TestPlacementContradictedSentenceIsTheTwoFigures (lead, 2026-09-15): the
// sentence names the device and the two figures the predicate compared,
// because a caveat a reader cannot check is one they will ignore — and the
// two figures are the whole case.
func TestPlacementContradictedSentenceIsTheTwoFigures(t *testing.T) {
	s := clean(t)
	s.GPUsAtEnd[1].ProcBytes = 0
	s.GPUsAtEnd[1].UsedBytes = 1 << 20
	want := "the placement estimate puts 20.9 GiB of weights on GPU1, which held 0.0 GiB: the split and everything derived from it are not this run's"
	cs := Caveats(s)
	if len(cs) != 1 || cs[0].Code != CodePlacementContradicted || cs[0].Text != want {
		t.Fatalf("Caveats = %+v, want exactly %s: %q", cs, CodePlacementContradicted, want)
	}
	if cs[0].Severity != SeverityFigure {
		t.Errorf("severity = %q, want figure: the split carries more than the ratio the card already declined to print", cs[0].Severity)
	}
	// The explain listing carries its reading in the same rank position, so
	// "why did this card warn" is answered where the other codes are.
	ex := ExplainCaveats(s)
	if i, j := strings.Index(ex, CodePlacementContradicted), strings.Index(ex, CodeStreamsNotConcurrent); i < 0 || j < 0 || i < j {
		t.Errorf("the reading is missing or not ranked after streams_not_concurrent:\n%s", ex)
	}
	if !strings.Contains(ex, "20.9 GiB") || !strings.Contains(ex, "0.0 GiB") {
		t.Errorf("the reading does not carry the two figures:\n%s", ex)
	}
}

// TestBandwidthOverCeilingSentenceIsTheTwoFigures (2026-09-16): the sentence
// names both numbers it compared, the same deal as the placement
// contradiction's — a caveat a reader cannot check is one they will ignore,
// and the two figures are the whole case. The Decode row keeps its rate and
// loses its bandwidth clause: a figure over the ceiling is not a reading of
// anything, and the honest clause is none.
func TestBandwidthOverCeilingSentenceIsTheTwoFigures(t *testing.T) {
	s := clean(t)
	// The placement allows 936.2 GB/s (two 3090s, everything offloaded); the
	// recorded figure is put at 10x that.
	s.Timings.EffectiveBandwidthBytesPerSec = 9_362_000_000_000
	want := "the bytes this run's placement implies read 9362 GB/s against the 936 GB/s this placement allows, so no bandwidth is derived: some of what the placement counts as streamed is read by row"
	cs := Caveats(s)
	if len(cs) != 1 || cs[0].Code != CodeBandwidthOverCeiling || cs[0].Text != want {
		t.Fatalf("Caveats = %+v, want exactly %s: %q", cs, CodeBandwidthOverCeiling, want)
	}
	if cs[0].Severity != SeverityFigure {
		t.Errorf("severity = %q, want figure: the clause the card gave up was its headline qualifier", cs[0].Severity)
	}
	for _, part := range decodeParts(s) {
		if strings.Contains(part, "GB/s") {
			t.Errorf("the Decode row still carries a bandwidth clause: %q", part)
		}
	}
	// The explain listing carries its reading in the same rank position,
	// directly under placement_contradicted's.
	ex := ExplainCaveats(s)
	if i, j := strings.Index(ex, CodeBandwidthOverCeiling), strings.Index(ex, CodeAnswerCut); i < 0 || j < 0 || i > j {
		t.Errorf("the reading is missing or not ranked under placement_contradicted:\n%s", ex)
	}
	if !strings.Contains(ex, "9362 GB/s") || !strings.Contains(ex, "936 GB/s") {
		t.Errorf("the reading does not carry the two figures:\n%s", ex)
	}
}

// TestPlacementContradictedNeedsAnEstimateAndAMeasurement: the two shapes the
// caveat must stay out of. An engine placement is the engine's own report,
// not a guess to check against the box, and a placement the reading agrees
// with is not contradicted whatever its source.
func TestPlacementContradictedNeedsAnEstimateAndAMeasurement(t *testing.T) {
	has := func(s *tape.RunSummary) bool {
		for _, c := range Caveats(s) {
			if c.Code == CodePlacementContradicted {
				return true
			}
		}
		return false
	}
	s := clean(t)
	s.Placement.Source = placement.SourceEngine
	s.GPUsAtEnd[1].ProcBytes, s.GPUsAtEnd[1].UsedBytes = 0, 1<<20
	if has(s) {
		t.Error("the caveat fired on an engine placement")
	}
	if has(clean(t)) {
		t.Error("the caveat fired on a placement the measurement agrees with")
	}
}

// TestStreamsNotConcurrentRanksDirectlyUnderStreamsFailed: it qualifies the
// same Streams row a failed stream does, one step less loudly — the figure
// exists, but it is a queue's. The ranks below it keep their order and stay
// consecutive, or the listing and the caveat line disagree about what outranks
// what.
func TestStreamsNotConcurrentRanksDirectlyUnderStreamsFailed(t *testing.T) {
	// placement_contradicted entered at rank 2 (lead, 2026-09-15), directly
	// under the two streams codes and above answer_cut: it qualifies a
	// derived figure rather than a measured one — the ratio the card has
	// already declined to print — but what it contradicts is the placement
	// the whole MEMORY block is built on.
	//
	// bandwidth_over_ceiling entered at rank 3 (2026-09-16), directly under
	// placement_contradicted: it is the same kind of caveat — a derived
	// figure the card has already declined to print, with the two figures it
	// compared named in the sentence — and it qualifies the same memory
	// block: a bandwidth over the bus that carried it means the active split
	// is wrong, so the placement story and the rate disagree about one run.
	//
	// thinking_ignored entered at rank 10 (TTP-106, 2026-09-17), directly
	// under client_disagrees and above recorded: it qualifies every figure on
	// the card at once — a run that reasoned when reasoning was switched off
	// is not the run the SAMPLING row describes — but it is a disagreement
	// between what was asked for and what happened, which is the family
	// client_disagrees opens, not a failure of the measurement itself.
	//
	// ragged_aggregate entered at rank 2 (TTP-108, 2026-09-19), directly
	// under streams_not_concurrent: the same concurrency story told by the
	// arithmetic rather than the timeline, and the timeline names the
	// mechanism where the arithmetic only names the shortfall.
	//
	// cached_prefill entered at rank 10 (TTP-88, 2026-09-19), directly under
	// short_prompt_for_prefill: both qualify the Prefill row, but a short
	// prompt was never a measurement while a cached one measured the cache
	// path — the stronger disqualification ranks first.
	want := []string{
		CodeStreamsFailed, CodeStreamsNotConcurrent, CodeRaggedAggregate, CodePlacementContradicted,
		CodeBandwidthOverCeiling, CodeAnswerCut, CodeShortGeneration,
		CodeShortStream, CodeColdCache, CodeShortPromptForPrefill, CodeCachedPrefill, CodeClientDisagrees,
		CodeClientTimed, CodeTokensUncounted,
		CodeThinkingIgnored,
		CodeRecorded, CodeMachineContended, CodeConditionsChanged, CodeRunCutByClock,
		CodeNoProcView,
	}
	for i, c := range want {
		if caveatRank[c] != i {
			t.Errorf("caveatRank[%s] = %d, want %d", c, caveatRank[c], i)
		}
	}
}

// TestThinkingIgnoredNeedsAPositiveCount: a zero raises nothing, because a run
// where no stream reasoned and a tape written before the recorder counted are
// the same bytes — `thought_anyway` is omitempty, so "measured zero" and "never
// measured" serialise identically (TTP-103). Only a positive count asserts
// anything, and the card must not qualify a run on a field it cannot read.
//
// FAIL-first: with the predicate written as `s.Sampling.Thinking == "off" &&
// s.Sampling.ThoughtAnyway >= 0`, every tape that ever asked for thinking off
// carries the caveat, and both subtests below fail.
func TestThinkingIgnoredNeedsAPositiveCount(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		want bool
	}{
		{"no stream reasoned, or the tape predates the count", 0, false},
		{"one stream of four reasoned", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := clean(t)
			s.Sampling.Thinking = "off"
			s.Sampling.ThoughtAnyway = tc.n
			got := false
			for _, c := range Caveats(s) {
				if c.Code == CodeThinkingIgnored {
					got = true
				}
			}
			if got != tc.want {
				t.Errorf("thought_anyway %d raises the caveat = %v, want %v", tc.n, got, tc.want)
			}
		})
	}
}
