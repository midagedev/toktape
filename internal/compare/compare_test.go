package compare_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	s.ID = "20260913-151212-r1-distill-llama-70b"
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
// became the R1 Distill Llama 70B fixture, so every figure the diff quotes moved. What
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

// noDraft is card.ExampleSpeculative() with the draft model taken back off:
// the same rig, the same target, the same four streams, and the rate the run
// reaches when every token is decoded by the target alone.
//
// It is the baseline the speculative run is read against, and it is the pair
// the "did the draft actually help" question is asked of. The answer is the
// decode tok/s row and its delta — there is deliberately no second "speed-up"
// row, because a speed-up is the delta of a rate and printing it twice invites
// the two to disagree.
func noDraft() *tape.RunSummary {
	s := *card.ExampleSpeculative()
	s.ID = "20260913-160902-deepseek-v4-1-flash-q4-k"

	flags := s.Server.Flags
	flags.DraftModel, flags.DraftMax, flags.DraftMin = "", "", ""
	s.Server.Flags = flags
	s.Server.Args = []string{
		"/usr/local/bin/llama-server",
		"-m", "/models/DeepSeek-V4.1-Flash-Q4_K_M.gguf",
		"-c", "16384", "--parallel", "4", "-ngl", "99", "-ncmoe", "48",
		"-fa", "on", "-b", "2048", "-ub", "512",
		"-ctk", "q8_0", "-ctv", "q8_0", "-t", "16",
	}

	// One token per pass instead of up to four, so the same memory bus gives
	// 11.8 tok/s per stream rather than 19.0.
	t := s.Timings
	t.DraftN, t.DraftNAccepted = nil, nil
	t.PredictedN = 68
	t.PredictedMs = 5762.7
	t.PredictedPerSecond = 11.8
	t.ClientPredictedPerSecond = 11.8
	t.ITLp50Ms = 84.7
	t.ITLp95Ms = 92.0
	t.ITLp99Ms = 108.4
	t.EffectiveBandwidthBytesPerSec = 79296000000 // 6.72 GB/token × 11.8 tok/s
	s.Timings = t

	a := s.Aggregate
	a.WallMs = 6480
	a.AggregatePredictedPerSecond = 47.2
	a.PerStreamPredictedPerSecond = 11.8
	s.Aggregate = a

	s.FinishedAt = s.StartedAt.Add(6480 * time.Millisecond)
	return &s
}

// TestDraftAcceptedRow (TTP-30, 2026-09-13): the acceptance rate is a metric
// row, and it appears only when one of the two runs actually drafted.
//
// A side that reported no draft prints "?" rather than 0 %: an acceptance rate
// of zero is a draft model the target never agreed with, which is a different
// and much more interesting fact than not having run one.
func TestDraftAcceptedRow(t *testing.T) {
	byLabel := func(r compare.Report) map[string]compare.Row {
		out := map[string]compare.Row{}
		for _, m := range r.Metrics {
			out[m.Label] = m
		}
		return out
	}

	t.Run("absent when neither run drafted", func(t *testing.T) {
		if _, ok := byLabel(compare.Diff(card.Example(), modified()))["draft accepted"]; ok {
			t.Error("a pair of runs with no draft between them carries a draft row")
		}
	})

	t.Run("present when one side drafted, and ? on the other", func(t *testing.T) {
		row, ok := byLabel(compare.Diff(noDraft(), card.ExampleSpeculative()))["draft accepted"]
		if !ok {
			t.Fatal("no draft accepted row on a pair where B drafted")
		}
		if row.A != "?" {
			t.Errorf("A = %q, want %q: that run reported no draft at all", row.A, "?")
		}
		if row.B != "60%" {
			t.Errorf("B = %q, want %q (174 of 290)", row.B, "60%")
		}
		if row.HasDelta {
			t.Error("the row carries a delta against a side that has no figure")
		}
	})

	t.Run("it follows decode tok/s", func(t *testing.T) {
		r := compare.Diff(noDraft(), card.ExampleSpeculative())
		at := func(label string) int {
			for i, m := range r.Metrics {
				if m.Label == label {
					return i
				}
			}
			return -1
		}
		if at("draft accepted") != at("decode tok/s")+1 {
			t.Errorf("draft accepted is at %d and decode tok/s at %d; the acceptance rate belongs directly under the rate it explains",
				at("draft accepted"), at("decode tok/s"))
		}
	})

	t.Run("both sides drafted: the delta is the change in acceptance", func(t *testing.T) {
		a := card.ExampleSpeculative()
		b := card.ExampleSpeculative()
		drafted, accepted := 290, 87 // 30%
		b.Timings.DraftN, b.Timings.DraftNAccepted = &drafted, &accepted
		row := byLabel(compare.Diff(a, b))["draft accepted"]
		if row.A != "60%" || row.B != "30%" {
			t.Errorf("A/B = %q/%q, want 60%%/30%%", row.A, row.B)
		}
		if !row.HasDelta || row.DeltaPct > -49 || row.DeltaPct < -51 {
			t.Errorf("delta = %v (has=%v), want about -50%%", row.DeltaPct, row.HasDelta)
		}
	})

	t.Run("a draft that never drafted has no rate", func(t *testing.T) {
		b := card.ExampleSpeculative()
		zero := 0
		b.Timings.DraftN, b.Timings.DraftNAccepted = &zero, &zero
		row := byLabel(compare.Diff(noDraft(), b))["draft accepted"]
		if row.B != "?" {
			t.Errorf("B = %q, want %q: nothing was drafted, so there is no rate", row.B, "?")
		}
	})
}

// TestDraftAndDirMetaChanges: what the two runs were measuring. A decode rate
// that moved means nothing if the draft model moved with it, and a shard set
// that differs only by its directory (TTP-32) is two different models wearing
// one file name.
func TestDraftAndDirMetaChanges(t *testing.T) {
	meta := func(r compare.Report) map[string][2]string {
		out := map[string][2]string{}
		for _, c := range r.Meta {
			out[c.Label] = [2]string{c.A, c.B}
		}
		return out
	}

	t.Run("the draft model and its block size", func(t *testing.T) {
		got := meta(compare.Diff(noDraft(), card.ExampleSpeculative()))
		if want := [2]string{"?", "DSpark-0.6B-Q8_0.gguf"}; got["draft model"] != want {
			t.Errorf("draft model = %v, want %v", got["draft model"], want)
		}
		if want := [2]string{"?", "3"}; got["draft n_max"] != want {
			t.Errorf("draft n_max = %v, want %v", got["draft n_max"], want)
		}
	})

	t.Run("identical runs report no draft metadata", func(t *testing.T) {
		if got := meta(compare.Diff(card.ExampleSpeculative(), card.ExampleSpeculative())); len(got) != 0 {
			t.Errorf("two copies of one run differ in %v", got)
		}
	})

	t.Run("model dir, when both runs have one", func(t *testing.T) {
		a, b := card.Example(), card.Example()
		a.Model.Dir = "unsloth-UD-Q4_K_XL"
		b.Model.Dir = "bartowski-Q4_K_M"
		if want := [2]string{"unsloth-UD-Q4_K_XL", "bartowski-Q4_K_M"}; meta(compare.Diff(a, b))["model dir"] != want {
			t.Errorf("model dir = %v, want %v", meta(compare.Diff(a, b))["model dir"], want)
		}
	})

	t.Run("model dir is silent when only one side recorded one", func(t *testing.T) {
		a, b := card.Example(), card.Example()
		b.Model.Dir = "bartowski-Q4_K_M"
		if _, ok := meta(compare.Diff(a, b))["model dir"]; ok {
			t.Error("a directory that only one run recorded is a gap in the recording, not a difference between the runs")
		}
	})
}

// TestNoDraftGolden is the pair the acceptance row exists for, rendered whole.
// It is a second golden rather than an edit to example-vs-modified.txt: that
// fixture pins the ordinary flag-change diff and must not move.
func TestNoDraftGolden(t *testing.T) {
	got := compare.Text(compare.Diff(noDraft(), card.ExampleSpeculative()))
	golden := filepath.Join("testdata", "no-draft-vs-speculative.txt")
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
	for i, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if w := card.Width(line); w > compare.Width {
			t.Errorf("line %d is %d columns, max %d: %q", i+1, w, compare.Width, line)
		}
	}
}

// TestDiffUnknownSideHasNoDelta: a figure one side never observed prints "?",
// and a change against "?" is no change at all — the old rule printed -100%
// because only A's zero was checked. A zero that is a real measurement (the
// page-fault rate) still carries its delta.
func TestDiffUnknownSideHasNoDelta(t *testing.T) {
	a := card.Example()
	b := card.Example()
	b.GPUsAtEnd = nil
	b.Memory.AtEnd.RSSBytes = 0
	b.Timings.TTFTMs = 0
	b.Timings.PromptPerSecond = 0
	b.Timings.PredictedPerSecond = 0
	b.Memory.MajFaultsPerToken = 0
	if a.Memory.MajFaultsPerToken == 0 {
		a.Memory.MajFaultsPerToken = 2
	}
	for _, pair := range [][2]*tape.RunSummary{{a, b}, {b, a}} {
		for _, m := range compare.Diff(pair[0], pair[1]).Metrics {
			unknown := m.A == "?" || m.B == "?"
			if unknown && m.HasDelta {
				t.Errorf("%s: %q → %q carries a delta of %.1f%%", m.Label, m.A, m.B, m.DeltaPct)
			}
		}
	}
	for _, m := range compare.Diff(a, b).Metrics {
		if m.Label == "maj faults/token" && !m.HasDelta {
			t.Errorf("maj faults/token: a measured zero lost its delta")
		}
	}
}
