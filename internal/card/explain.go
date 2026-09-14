package card

import (
	"fmt"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// ExplainCaveats prints every qualification the card can make, whether it
// fired, and the reading it was decided on.
//
// It exists because "why does this card not warn about X" and "where did this
// caveat come from" were questions that could only be answered by reading
// caveat.go with the tape open beside it — which is how the 5.6 tok/s prefill
// survived long enough for a human to find it. internal/bandwidth.Explain does
// the same job one layer down, and this is deliberately its shape: one line per
// check, the verdict, and the numbers behind the verdict, so a disagreement
// between two cards can be located rather than argued about.
//
// The predicates are the same calls Caveats makes, so a check that reads
// differently here than it decides there is impossible by construction — the
// reading printed is the input, not a second opinion.
//
// The verdict column is the caveat's severity when it fired and "no" when it
// did not, so the listing says not only that a check tripped but what it costs.
//
//	caveats of 20260914-070458-deepseek-v4-1-flash-q3-k
//	  streams_failed            no      0 failed of 2 streams
//	  cold_cache                Figure  cache label "cold", 5.8 maj faults/token
//	  short_prompt_for_prefill  Figure  30 prompt tokens, floor 100
//	  conditions_changed        Run     k10temp Tctl 69 → 84 °C
func ExplainCaveats(s *tape.RunSummary) string {
	if s == nil {
		s = &tape.RunSummary{}
	}
	fired := map[string]Caveat{}
	for _, c := range Caveats(s) {
		fired[c.Code] = c
	}

	t := s.Timings
	// Readings in caveatRank order, so the listing and the list agree about
	// what outranks what.
	readings := []struct{ code, reading string }{
		{CodeStreamsFailed, fmt.Sprintf("%d failed of %d streams",
			s.Aggregate.StreamsFailed, streamsSent(s))},
		{CodeAnswerCut, fmt.Sprintf("%d of %d predicted tokens were reasoning",
			t.ReasoningN, t.PredictedN)},
		{CodeShortGeneration, fmt.Sprintf("predicted_n %d, recorded label %q, floor %d",
			t.PredictedN, t.DecodeLabel, tape.MinDecodeTokens)},
		// The minimum it decides on beside the mean it does not, so a
		// four-stream card that did not warn shows both numbers.
		{CodeShortStream, fmt.Sprintf("min_predicted_n %s over %d streams (mean %d), floor %d",
			orUnknown(countOrEmpty(s.Aggregate.MinPredictedN)), streamsSent(s), t.PredictedN, tape.MinDecodeTokens)},
		{CodeColdCache, fmt.Sprintf("cache label %q, %s maj faults/token",
			s.Cache.Label, formatFloat1(s.Memory.MajFaultsPerToken))},
		{CodeShortPromptForPrefill, fmt.Sprintf("%d prompt tokens, floor %d",
			promptTokens(s), MinPrefillPromptTokens)},
		{CodeClientDisagrees, fmt.Sprintf("server %s, client %s, agrees %v, tolerance %s",
			formatRate(t.PredictedPerSecond), formatRate(t.ClientPredictedPerSecond),
			t.ClientAgreesWithServer, formatPct(tape.RateTolerance))},
		{CodeRecorded, fmt.Sprintf("%d recorded warning(s)", len(s.Warnings))},
		{CodeMachineContended, fmt.Sprintf("contended %v, %d reason(s), loadavg1 %s",
			s.Contention.Contended, len(s.Contention.Reasons), formatFloat1(s.Contention.LoadAvg1))},
		{CodeConditionsChanged, explainConditions(s)},
		{CodeRunCutByClock, fmt.Sprintf("for %s, cut at %s, floor %d tokens",
			formatDuration(s.Limit.For), formatDuration(s.Limit.CutAt), s.Limit.MinTokens)},
		{CodeNoProcView, "rss at end " + formatGiB(s.Memory.AtEnd.RSSBytes)},
	}

	var b strings.Builder
	fmt.Fprintf(&b, "caveats of %s\n", orUnknown(s.ID))
	for _, r := range readings {
		verdict := "no "
		if c, ok := fired[r.code]; ok {
			verdict = strings.ToUpper(c.Severity[:1]) + c.Severity[1:]
		}
		fmt.Fprintf(&b, "  %-28s %-7s %s\n", r.code, verdict, r.reading)
	}
	// The figures the card qualifies, and which spelling it chose for each, so
	// a card that prints a bandwidth nobody expected says where it came from.
	fmt.Fprintf(&b, "  %-28s %s\n", "decode row", strings.Join(decodeParts(s), " · "))
	fmt.Fprintf(&b, "  %-28s %s\n", "prefill row", strings.Join(prefillParts(s, promptTokens(s)), " · "))
	if parts := draftParts(s); len(parts) > 0 {
		fmt.Fprintf(&b, "  %-28s %s\n", "draft row", strings.Join(parts, " · "))
	}
	if len(fired) == 0 {
		b.WriteString("  nothing qualifies this run's figures — every number on the card is quotable\n")
	}
	return b.String()
}

// countOrEmpty is n, or "" when it was never recorded, for orUnknown.
func countOrEmpty(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprint(n)
}

// explainConditions is the conditions reading, or why there was none.
func explainConditions(s *tape.RunSummary) string {
	start, end := conditionEdges(s.Contention.Witnesses)
	switch {
	case len(s.Contention.Witnesses) == 0:
		return "no witnesses"
	case start == nil || end == nil:
		return fmt.Sprintf("%d witness(es), but not both edges", len(s.Contention.Witnesses))
	}
	clauses := conditionsClauses(s)
	if len(clauses) == 0 {
		return fmt.Sprintf("both edges read, nothing moved (cap %d → %d kHz, %s %s → %s °C)",
			start.CPUMaxKHz, end.CPUMaxKHz, orUnknown(start.TempSensor),
			formatFloat1(start.TempC), formatFloat1(end.TempC))
	}
	return strings.Join(clauses, " · ")
}
