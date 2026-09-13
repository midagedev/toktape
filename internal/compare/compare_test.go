package compare_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/compare"
	"github.com/midagedev/toktape/internal/tape"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// modified is card.Example() after the kind of change this tool exists to
// explain: flash attention turned off, one -ot rule replaced, a newer build,
// and the slower numbers that came out of it.
func modified() *tape.RunSummary {
	s := *card.Example()
	s.ID = "20260913-151212-llama3.3-70b"
	s.Server.Build = "b3701"
	s.Server.Commit = "f9e8d7c"
	s.Server.Flags.FlashAttn = "off"
	s.Server.Flags.Batch = "4096"
	s.Server.Flags.OverrideTens = []string{`blk\.(4[0-7])\.ffn_.*_exps=CPU`}

	s.Timings.TTFTMs = 512
	s.Timings.PromptPerSecond = 1980
	s.Timings.PredictedPerSecond = 51.2
	s.Memory.AtEnd.RSSBytes += 3 << 30
	s.Memory.MajFaultsPerToken = 1.8
	s.Cache.HitRatio = 0
	s.Contention.Contended = true

	gpus := make([]tape.GPUSample, len(s.GPUsAtEnd))
	copy(gpus, s.GPUsAtEnd)
	for i := range gpus {
		gpus[i].UsedBytes -= 1 << 30
	}
	s.GPUsAtEnd = gpus
	return &s
}

// 2026-09-13 TTP-28: re-baselined. The left-hand run is card.Example(), which
// became the Llama 3.3 70B fixture, so every figure the diff quotes moved. What
// the test asserts — that a difference is shown and an equality is not — is
// unchanged, and the -ot pair the diff needs is now built inside this file
// rather than borrowed from the example, which no longer carries one.
func TestTextGolden(t *testing.T) {
	got := compare.Text(compare.Diff(card.Example(), modified()))
	golden := filepath.Join("testdata", "example-vs-modified.txt")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Errorf("compare.Text does not match %s:\n--- got ---\n%s\n--- want ---\n%s", golden, got, want)
	}
}

// TestWidth: a compare block is pasted into the same code block as the card,
// so no line of it may be wider than the card.
func TestWidth(t *testing.T) {
	out := compare.Text(compare.Diff(card.Example(), modified()))
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := card.Width(line); w > compare.Width {
			t.Errorf("line %d is %d columns, max %d: %q", i+1, w, compare.Width, line)
		}
	}
}

// TestDiffMetrics checks the arithmetic and the two rules that are easy to
// get wrong: a zero baseline has no delta, and the aggregate row only appears
// when a run was concurrent.
func TestDiffMetrics(t *testing.T) {
	r := compare.Diff(card.Example(), modified())
	byLabel := map[string]compare.Row{}
	for _, m := range r.Metrics {
		byLabel[m.Label] = m
	}

	decode, ok := byLabel["decode tok/s"]
	if !ok {
		t.Fatal("no decode row")
	}
	if !decode.HasDelta {
		t.Fatal("decode row has no delta")
	}
	a := card.Example().Timings.PredictedPerSecond
	want := (51.2 - a) / a * 100
	if diff := decode.DeltaPct - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("decode DeltaPct = %v, want %v", decode.DeltaPct, want)
	}

	if _, ok := byLabel["aggregate tok/s"]; ok {
		t.Error("aggregate row present for two single-stream runs")
	}

	if c := byLabel["contended"]; c.HasDelta || c.A != "no" || c.B != "yes" {
		t.Errorf("contended row = %+v, want no → yes with no delta", c)
	}

	// The A run's cache hit is non-zero and the B run's is zero, so the
	// delta is -100%; the reverse pair has no baseline and must not claim
	// one.
	rev := compare.Diff(modified(), card.Example())
	for _, m := range rev.Metrics {
		if m.Label == "cache hit" && m.HasDelta {
			t.Errorf("cache hit claims a delta against a zero baseline: %+v", m)
		}
	}
}

func TestDiffConcurrentAddsAggregate(t *testing.T) {
	r := compare.Diff(card.Example(), card.ExampleConcurrent())
	found := false
	for _, m := range r.Metrics {
		if m.Label == "aggregate tok/s" {
			found = true
		}
	}
	if !found {
		t.Error("no aggregate row although one run sent several streams")
	}
}

func TestFlagAndMetaChanges(t *testing.T) {
	// The -ot half of this test needs a rule on both sides: the example rig is
	// a dense model fully offloaded by layer and carries none (2026-09-13,
	// TTP-28), so the pair is built here rather than taken from the fixture.
	// One rule replaced by another must diff as one removal and one addition,
	// which is the thing a naive per-index comparison gets wrong.
	a := *card.Example()
	a.Server.Flags.OverrideTens = []string{`blk\.(3[6-9]|4[0-7])\.ffn_.*_exps=CPU`}
	b := *modified()
	r := compare.Diff(&a, &b)

	var fa, added, removed bool
	for _, c := range r.Flags {
		switch {
		case c.Label == "-fa" && c.A == "on" && c.B == "off":
			fa = true
		case c.Label == "-ot" && c.Added:
			added = true
		case c.Label == "-ot" && c.Removed:
			removed = true
		}
	}
	if !fa {
		t.Errorf("no -fa on → off change in %+v", r.Flags)
	}
	if !added || !removed {
		t.Errorf("-ot rules not diffed as a set (added=%v removed=%v): %+v", added, removed, r.Flags)
	}

	var build bool
	for _, c := range r.Meta {
		if c.Label == "build" && c.A == "b3650" && c.B == "b3701" {
			build = true
		}
		if c.Label == "model" {
			t.Error("model reported as changed although both runs use the same file")
		}
	}
	if !build {
		t.Errorf("no build change in %+v", r.Meta)
	}
}

// TestDiffIdentical: comparing a run with itself must report nothing changed.
func TestDiffIdentical(t *testing.T) {
	r := compare.Diff(card.Example(), card.Example())
	if len(r.Flags) != 0 || len(r.Meta) != 0 {
		t.Errorf("a run differs from itself: flags=%+v meta=%+v", r.Flags, r.Meta)
	}
	for _, m := range r.Metrics {
		if m.HasDelta && m.DeltaPct != 0 {
			t.Errorf("%s moved against itself: %v%%", m.Label, m.DeltaPct)
		}
	}
	out := compare.Text(r)
	if !strings.Contains(out, "(identical)") {
		t.Error("identical flags are not called out")
	}
}

// TestDiffNil: a tape whose summary failed to load must not panic.
func TestDiffNil(t *testing.T) {
	out := compare.Text(compare.Diff(nil, nil))
	if !strings.Contains(out, "?") {
		t.Error("an all-unknown compare does not print unknowns")
	}
}
