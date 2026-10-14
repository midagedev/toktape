package publish

import (
	"encoding/json"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestIndexOfShortPrompt is the search contract for a prompt under
// tape.MinPrefillPromptTokens (card.ShortPrompt): the server's prefill rate
// stays on the tape and leaves the index, because it is not a prefill
// measurement. The probe's own figures travel instead, on probeLine's gate
// (PrefillPerSecond > 0). Assertions are on the marshalled keys.
func TestIndexOfShortPrompt(t *testing.T) {
	probe := &tape.ProbeSummary{PrefillPerSecond: 679, FixedMs: 134}

	t.Run("33 tokens with a probe", func(t *testing.T) {
		m := indexKeys(t, oneStream(33, 206, probe))
		if m["short_prompt"] != true {
			t.Errorf("short_prompt = %v, want true", m["short_prompt"])
		}
		if _, ok := m["prefill_per_sec"]; ok {
			t.Errorf("prefill_per_sec = %v, want the key absent", m["prefill_per_sec"])
		}
		if m["probe_prefill_per_sec"] != float64(679) {
			t.Errorf("probe_prefill_per_sec = %v, want 679", m["probe_prefill_per_sec"])
		}
		if m["probe_fixed_ms"] != float64(134) {
			t.Errorf("probe_fixed_ms = %v, want 134", m["probe_fixed_ms"])
		}
	})

	t.Run("5673 tokens", func(t *testing.T) {
		m := indexKeys(t, oneStream(5673, 206, probe))
		if _, ok := m["short_prompt"]; ok {
			t.Errorf("short_prompt = %v, want the key absent", m["short_prompt"])
		}
		if m["prefill_per_sec"] != float64(206) {
			t.Errorf("prefill_per_sec = %v, want 206", m["prefill_per_sec"])
		}
		// The probe is a fact about the machine, not about this prompt.
		if m["probe_prefill_per_sec"] != float64(679) || m["probe_fixed_ms"] != float64(134) {
			t.Errorf("probe fields = %v / %v, want 679 / 134", m["probe_prefill_per_sec"], m["probe_fixed_ms"])
		}
	})

	t.Run("short with no probe", func(t *testing.T) {
		m := indexKeys(t, oneStream(33, 206, nil))
		if m["short_prompt"] != true {
			t.Errorf("short_prompt = %v, want true", m["short_prompt"])
		}
		if _, ok := m["prefill_per_sec"]; ok {
			t.Errorf("prefill_per_sec = %v, want the key absent", m["prefill_per_sec"])
		}
		for _, key := range []string{"probe_prefill_per_sec", "probe_fixed_ms"} {
			if _, ok := m[key]; ok {
				t.Errorf("key %q present on a tape with no probe: %v", key, m[key])
			}
		}
	})

	// A measured intercept of 0 ms is a reading (formatProbeMs), not an
	// unknown, so the key stays on the wire.
	t.Run("probe fixed cost of 0", func(t *testing.T) {
		m := indexKeys(t, oneStream(5673, 206, &tape.ProbeSummary{PrefillPerSecond: 679, FixedMs: 0}))
		if m["probe_fixed_ms"] != float64(0) {
			t.Errorf("probe_fixed_ms = %v, want 0 present", m["probe_fixed_ms"])
		}
		if _, ok := m["probe_fixed_ms"]; !ok {
			t.Error("probe_fixed_ms key absent; 0 ms is a reading")
		}
	})

	// A refused fit (rate 0, points kept) is the same "not observed" as no
	// probe: probeLine prints nothing.
	t.Run("refused fit", func(t *testing.T) {
		m := indexKeys(t, oneStream(33, 206, &tape.ProbeSummary{PrefillPerSecond: 0, FixedMs: 0}))
		for _, key := range []string{"probe_prefill_per_sec", "probe_fixed_ms"} {
			if _, ok := m[key]; ok {
				t.Errorf("key %q present on a refused fit: %v", key, m[key])
			}
		}
	})

	// Decision rows stay as they were: no short_prompt, no probe keys, and
	// the prefill figure the old path copied is still copied.
	t.Run("decision untouched", func(t *testing.T) {
		s := oneStream(33, 206, probe)
		s.Mode = tape.ModeDecision
		m := indexKeys(t, s)
		if _, ok := m["short_prompt"]; ok {
			t.Errorf("short_prompt on a decision row: %v", m["short_prompt"])
		}
		for _, key := range []string{"probe_prefill_per_sec", "probe_fixed_ms"} {
			if _, ok := m[key]; ok {
				t.Errorf("key %q on a decision row: %v", key, m[key])
			}
		}
		if m["prefill_per_sec"] != float64(206) {
			t.Errorf("prefill_per_sec = %v, want the decision path left at 206", m["prefill_per_sec"])
		}
	})
}

// oneStream is a one-stream tape whose whole prompt is promptN tokens
// (Cache.PromptTotal, the count card.ShortPrompt asks through PromptTokens)
// and whose server prefill rate is rate. The rate is what must disappear
// from the index when the prompt is short.
func oneStream(promptN int, rate float64, probe *tape.ProbeSummary) tape.RunSummary {
	return tape.RunSummary{
		Concurrency: 1,
		Cache:       tape.CacheSummary{PromptTotal: promptN},
		Timings:     tape.TimingsSummary{PromptN: promptN, PromptPerSecond: rate},
		Probe:       probe,
	}
}

// indexKeys is the marshalled index as a key set: an omitempty field that
// did not travel is not in the map.
func indexKeys(t *testing.T, s tape.RunSummary) map[string]any {
	t.Helper()
	b, err := json.Marshal(IndexOf(&tape.Tape{Summary: s}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}
