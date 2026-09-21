package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// The general gate of TTP-177 (2026-09-21): the server counted a completion
// the client never saw. Measured on Ollama 0.34.2 with qwen3:1.7b — usage
// said 48, the client parsed 0, every delta carried its text under a key the
// parser did not know — and nothing anywhere said why. TokensObserved beside
// PredictedN is the contradiction, whatever the fourth engine calls its key,
// and this caveat is the sentence that makes it loud.
//
// The code is pinned by its string in this file on purpose: a test that
// referenced the constant would not compile against the pre-change source,
// and this one must FAIL there (and did; output in the round report).

// unseen is a client-timed usage-counted summary shaped by a miss: the
// counts, the client's observed figure and whether any token was ever seen
// (TTFTMs is the summary's own witness of a non-empty timeline — Reduce
// derives it from the first token).
func unseen(t *testing.T, predicted, observed int, ttft float64) *tape.RunSummary {
	t.Helper()
	s := openaiSummary(t)
	s.Timings.PredictedN = predicted
	s.Timings.TokensObserved = observed
	s.Timings.TTFTMs = ttft
	return s
}

func codesOf(cs []Caveat) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Code
	}
	return out
}

// TestTokensUnseenTotalMissIsLoud: the defect's own shape — the server
// counted 48 and the client parsed none — raises exactly this code, at
// figure severity, with the two counts in the sentence (a caveat a reader
// cannot check is one they will ignore).
func TestTokensUnseenTotalMissIsLoud(t *testing.T) {
	s := unseen(t, 48, 0, 0)
	cs := Caveats(s)
	// A client-timed summary always carries client_timed; the miss adds
	// exactly this code beside it, ranked under it.
	if got := codesOf(cs); strings.Join(got, ",") != "client_timed,tokens_unseen" {
		t.Fatalf("Caveats = %v, want exactly [client_timed tokens_unseen] on 48 counted and none parsed", got)
	}
	var c Caveat
	for _, k := range cs {
		if k.Code == "tokens_unseen" {
			c = k
		}
	}
	if c.Severity != SeverityFigure {
		t.Errorf("severity = %q, want figure: the rate over unseen tokens does not mean what it looks like", c.Severity)
	}
	want := "tokens unseen: the server counted 48 completion tokens and the client parsed none — a field this toktape does not read carried the whole answer, so no client figure exists to check the server's"
	if c.Text != want {
		t.Errorf("text = %q\nwant           %q", c.Text, want)
	}
	// The card's caveat line spells out only the most serious caveat — on a
	// client-timed summary that is client_timed, which outranks this — and
	// names the rest by code, the handle a reader takes to the sentence in
	// --json (caveatLines' own rule). The join that must hold here is that
	// the code is on the card beside the figures it qualifies.
	if text := Text(s); !strings.Contains(text, "tokens_unseen") {
		t.Errorf("the card names neither the sentence nor its code:\n%s", text)
	}
	if !strings.Contains(string(mustJSON(t, s)), `"text": "`+want) {
		t.Errorf("--json does not carry the sentence")
	}
}

// TestTokensUnseenPartialMiss: a dialect that hides only the thinking phase
// leaves the answer visible — the client's count well under the server's.
// "Well under" is measured, not tasted: strictly below half (the one healthy
// delta-to-token ratio this repo has measured sits exactly at half — servers
// that batch two tokens per delta), and a shortfall of at least
// tape.MinDecodeTokens (below the sample floor a count is noise).
func TestTokensUnseenPartialMiss(t *testing.T) {
	for _, tc := range []struct {
		name                string
		predicted, observed int
		want                bool
	}{
		{"the thinking phase invisible, the answer visible", 500, 40, true},
		{"exactly half, the measured batching ratio", 12, 6, false},
		{"under half but a shortfall inside the sample floor", 40, 10, false},
		{"a healthy stream, every delta parsed", 48, 46, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := unseen(t, tc.predicted, tc.observed, 612)
			got := false
			var text string
			for _, c := range Caveats(s) {
				if c.Code == "tokens_unseen" {
					got, text = true, c.Text
				}
			}
			if got != tc.want {
				t.Fatalf("fired = %v, want %v (caveats %v)", got, tc.want, codesOf(Caveats(s)))
			}
			if tc.want {
				// The sentence names both counts, the reader's way to check it.
				if !strings.Contains(text, "500") || !strings.Contains(text, "40") {
					t.Errorf("the sentence does not name both counts: %q", text)
				}
				if !strings.HasPrefix(text, "tokens unseen") {
					t.Errorf("the sentence does not open with its subject: %q", text)
				}
			}
		})
	}
}

// TestTokensUnseenStaysSilentWhereTheCountIsUnknown: 0 is also "absent" —
// tokens_observed is omitempty, so a tape older than the field, a
// hand-assembled summary and a multi-stream run whose per-stream mean drops
// the figure all carry 0 beside a positive count. The witness that separates
// a measured nothing from an unknown one is the timeline itself: TTFTMs is 0
// exactly when no token was ever seen. A summary that saw its first token
// stays silent whatever its observed count reads.
//
// FAIL-first is the total-miss test above: before the witness, the naive
// predicate (0 beside a positive count) fired on every summary above, and
// the clean-fixture gate (TestACleanRunHasNoCaveats via openaiSummary's
// base) would have failed with it.
func TestTokensUnseenStaysSilentWhereTheCountIsUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    *tape.RunSummary
	}{
		{"a tape older than the field, or a mean that dropped it", unseen(t, 100, 0, 630)},
		{"a chunks run: the server counted nothing, tokens_uncounted owns it", func() *tape.RunSummary {
			s := unseen(t, 6, 0, 0)
			s.Timings.PredictedNSource = "chunks"
			return s
		}()},
		{"a server-timed run: predicted_n is the timings object's, not usage", clean(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, c := range Caveats(tc.s) {
				if c.Code == "tokens_unseen" {
					t.Errorf("fired on %s: %+v (caveats %v)", tc.name, c, codesOf(Caveats(tc.s)))
				}
			}
		})
	}
}
