package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// coldRun is the fixture that was flagged cold before the probe existed
// (caveat_test.go's own cold case): the run's own fault figure above
// tape.ColdMajFaultsPerToken, the label the recorder derived from it.
func coldRun() *tape.RunSummary {
	s := Example()
	s.Cache.Label = tape.CacheCold
	s.Memory.MajFaultsPerToken = tape.ColdMajFaultsPerToken + 0.4
	return s
}

// soleCaveat returns the single caveat with the given code, failing when the
// code fired twice or not at all — a caveat that fires per source is two
// sentences about one fact.
func soleCaveat(t *testing.T, s *tape.RunSummary, code string) Caveat {
	t.Helper()
	var found []Caveat
	for _, c := range Caveats(s) {
		if c.Code == code {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s fired %d times, want exactly once: %+v", code, len(found), Caveats(s))
	}
	return found[0]
}

// TTP-143 (FAIL-first, 2026-09-19) — the gate this item exists for: a
// fixture that was flagged cold before the probe existed must still be
// flagged cold with a probe in front of it. The probe pass pays the faults a
// cold server takes for its weights, and the run's sampler latches only
// after the pass, so the shape a cold box actually records is a cold probe
// figure over a run that reads warm.
func TestAColdServerStaysColdWithAProbeInFront(t *testing.T) {
	// The recorder's actual shape on a cold box: the probe's figure high,
	// the run's own low because its baseline latched after the pass.
	s := coldRun()
	s.Memory.MajFaultsPerToken = 0
	s.Probe = &tape.ProbeSummary{
		PrefillPerSecond:  1182,
		MajFaults:         900,
		MajFaultsPerToken: tape.ColdMajFaultsPerToken + 0.4,
	}
	if c := soleCaveat(t, s, CodeColdCache); !strings.Contains(c.Text, "1.4") {
		t.Errorf("cold_cache sentence = %q, want it to name the probe's own 1.4 maj faults/token", c.Text)
	}

	// The survivor: a tape whose run figure is cold and whose probe did not
	// fault — the pre-recorder-change tape re-rendered, or a server that
	// paged everything in before the run but after the pass. The run's
	// figure is the one that fires, with its own sentence.
	s = coldRun()
	s.Probe = &tape.ProbeSummary{PrefillPerSecond: 1182, MajFaultsPerToken: 0.2}
	if c := soleCaveat(t, s, CodeColdCache); !strings.Contains(c.Text, "1.4") {
		t.Errorf("cold_cache sentence = %q, want the run's own 1.4, which is still the figure observed", c.Text)
	}
}

// The defect this item closes, stated as a gate: a cold probe over a
// warm-labelled run must fire. Without the probe's fields in the verdict, a
// genuinely cold server reads warm — the label says warm precisely because
// the probe already paid for the weights.
func TestAProbingColdRunFiresColdCache(t *testing.T) {
	s := Example() // warm label, no faults during the run
	s.Probe = &tape.ProbeSummary{MajFaults: 900, MajFaultsPerToken: tape.ColdMajFaultsPerToken + 0.4}
	if c := soleCaveat(t, s, CodeColdCache); !strings.Contains(c.Text, "1.4") {
		t.Errorf("cold_cache sentence = %q, want it to name the probe's 1.4", c.Text)
	}
}

// And the probe does not invent cold: a warm pass over a warm run fires
// nothing, exactly as before the field existed.
func TestAWarmProbeFiresNoColdCache(t *testing.T) {
	s := Example()
	s.Probe = &tape.ProbeSummary{PrefillPerSecond: 1182, FixedMs: 28, MajFaultsPerToken: 0.2}
	for _, c := range Caveats(s) {
		if c.Code == CodeColdCache {
			t.Errorf("warm run with a warm probe raised cold_cache: %q", c.Text)
		}
	}
	// A probe whose fit was refused carries no fault figure either (0 is not
	// observed on a pass that ran), and fires nothing.
	s.Probe = &tape.ProbeSummary{}
	for _, c := range Caveats(s) {
		if c.Code == CodeColdCache {
			t.Errorf("a probe that observed nothing raised cold_cache: %q", c.Text)
		}
	}
}
