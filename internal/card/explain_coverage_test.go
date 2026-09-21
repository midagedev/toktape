package card

import (
	"regexp"
	"testing"
)

// TestExplainReadsEveryRankedCaveat: ExplainCaveats' readings table is kept in
// step with caveatRank by hand, and on 2026-09-21 it fell out of step —
// tokens_unseen was added to the rank, to the predicate and to the card, and
// --explain silently had no row for it, so the one command a reader runs to
// ask "why did this NOT fire" could not answer for the newest code.
//
// The generic gate is cheaper than remembering: every key of caveatRank must
// have a reading, whatever the run. The listing prints unfired readings too,
// which is the whole point of it, so one clean fixture exercises them all.
//
// FAIL-first: with the tokens_unseen row removed from explain.go this reports
// "caveatRank has tokens_unseen, --explain has no reading for it".
func TestExplainReadsEveryRankedCaveat(t *testing.T) {
	out := ExplainCaveats(Example())
	for code := range caveatRank {
		// The code opens its line; the \s guards against one code matching
		// inside a longer one.
		if !regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(code) + `\s`).MatchString(out) {
			t.Errorf("caveatRank has %s, --explain has no reading for it", code)
		}
	}
}
