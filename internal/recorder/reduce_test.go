package recorder

import (
	"fmt"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/bandwidth"
	"github.com/midagedev/toktape/internal/placement"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// TestRepresentativeTimingsCarriesReasoningN: the thinking-token count has to
// survive the per-stream mean, or the card's Context row prints "N out" with
// no thinking clause for exactly the mode this project calls first-class —
// N concurrent streams of an agent workload, which is where thinking models
// live (TTP-20, 2026-09-13).
//
// ReasoningN is a token count among PredictedN, so it is averaged the way
// PredictedN is: summed over the streams that produced tokens, then divided
// and rounded.
func TestRepresentativeTimingsCarriesReasoningN(t *testing.T) {
	stream := func(predicted, reasoning int) tape.RequestRecord {
		return tape.RequestRecord{Timings: tape.TimingsSummary{
			PromptN:            43,
			PredictedN:         predicted,
			ReasoningN:         reasoning,
			PredictedMs:        2436,
			PredictedPerSecond: 39.4,
			TTFTMs:             222,
		}}
	}

	t.Run("single stream is copied whole", func(t *testing.T) {
		got := representativeTimings([]tape.RequestRecord{stream(96, 96)})
		if got.ReasoningN != 96 {
			t.Errorf("ReasoningN = %d, want 96", got.ReasoningN)
		}
	})

	t.Run("mean over streams", func(t *testing.T) {
		got := representativeTimings([]tape.RequestRecord{
			stream(96, 96), stream(100, 40), stream(104, 41),
		})
		// (96 + 40 + 41) / 3 = 59, the same rounding PredictedN gets.
		if want := 59; got.ReasoningN != want {
			t.Errorf("ReasoningN = %d, want %d", got.ReasoningN, want)
		}
		if want := 100; got.PredictedN != want {
			t.Errorf("PredictedN = %d, want %d (guard: the mean itself still works)", got.PredictedN, want)
		}
	})

	t.Run("failed streams are excluded", func(t *testing.T) {
		bad := stream(0, 0)
		bad.Error = "stream ended without a finish chunk"
		got := representativeTimings([]tape.RequestRecord{stream(96, 96), bad})
		if want := 96; got.ReasoningN != want {
			t.Errorf("ReasoningN = %d, want %d (a failed stream must not halve the figure)", got.ReasoningN, want)
		}
	})

	t.Run("a model that does not think reports zero", func(t *testing.T) {
		got := representativeTimings([]tape.RequestRecord{stream(96, 0), stream(96, 0)})
		if got.ReasoningN != 0 {
			t.Errorf("ReasoningN = %d, want 0", got.ReasoningN)
		}
	})
}

// TestRepresentativeTimingsSumsDraftFigures (TTP-30, 2026-09-13).
//
// The run-level draft figures are the SUM over the streams that reported them,
// not the first stream's pair and not a mean. Accepted over drafted is a
// ratio, so the only reduction that produces the run's real acceptance rate is
// to pool the numerator and the denominator: a mean of the per-stream rates
// would weight a stream that drafted twelve tokens the same as one that
// drafted three hundred, and the first stream's pair alone describes an eighth
// of a run of eight streams.
//
// FAIL-first: before this commit representativeTimings copied the first
// reporting stream's pointers and returned 120/200 for the fixture below.
func TestRepresentativeTimingsSumsDraftFigures(t *testing.T) {
	stream := func(predicted int, draftN, accepted *int) tape.RequestRecord {
		return tape.RequestRecord{Timings: tape.TimingsSummary{
			PromptN:            43,
			PredictedN:         predicted,
			PredictedMs:        2436,
			PredictedPerSecond: 39.4,
			TTFTMs:             222,
			DraftN:             draftN,
			DraftNAccepted:     accepted,
		}}
	}
	ptr := func(n int) *int { return &n }
	got := func(t *testing.T, recs []tape.RequestRecord) (int, int) {
		t.Helper()
		out := representativeTimings(recs)
		if out.DraftN == nil || out.DraftNAccepted == nil {
			t.Fatalf("DraftN/DraftNAccepted = %v/%v, want a reported pair", out.DraftN, out.DraftNAccepted)
		}
		return *out.DraftN, *out.DraftNAccepted
	}

	t.Run("two streams pool their figures", func(t *testing.T) {
		drafted, accepted := got(t, []tape.RequestRecord{
			stream(96, ptr(200), ptr(120)),
			stream(96, ptr(90), ptr(54)),
		})
		if drafted != 290 || accepted != 174 {
			t.Errorf("drafted/accepted = %d/%d, want 290/174", drafted, accepted)
		}
	})

	t.Run("a stream that reported nothing contributes nothing", func(t *testing.T) {
		drafted, accepted := got(t, []tape.RequestRecord{
			stream(96, nil, nil),
			stream(96, ptr(200), ptr(120)),
			stream(96, ptr(90), ptr(54)),
		})
		if drafted != 290 || accepted != 174 {
			t.Errorf("drafted/accepted = %d/%d, want 290/174", drafted, accepted)
		}
	})

	t.Run("a failed stream is excluded like every other figure", func(t *testing.T) {
		bad := stream(0, ptr(1000), ptr(0))
		bad.Error = "stream ended without a finish chunk"
		drafted, accepted := got(t, []tape.RequestRecord{
			stream(96, ptr(200), ptr(120)),
			bad,
			stream(96, ptr(90), ptr(54)),
		})
		if drafted != 290 || accepted != 174 {
			t.Errorf("drafted/accepted = %d/%d, want 290/174 (a failed stream must not be pooled)", drafted, accepted)
		}
	})

	t.Run("no draft at all stays nil", func(t *testing.T) {
		out := representativeTimings([]tape.RequestRecord{stream(96, nil, nil), stream(96, nil, nil)})
		if out.DraftN != nil || out.DraftNAccepted != nil {
			t.Errorf("DraftN/DraftNAccepted = %v/%v, want nil/nil: no stream reported a draft", out.DraftN, out.DraftNAccepted)
		}
	})

	t.Run("the sum does not alias a stream's own figures", func(t *testing.T) {
		a, b := ptr(200), ptr(120)
		out := representativeTimings([]tape.RequestRecord{
			stream(96, a, b),
			stream(96, ptr(90), ptr(54)),
		})
		if out.DraftN == a || out.DraftNAccepted == b {
			t.Error("the run-level pair points at stream 0's own ints; writing the sum would rewrite the request record")
		}
		if *a != 200 || *b != 120 {
			t.Errorf("stream 0's figures moved to %d/%d", *a, *b)
		}
	})

	t.Run("a single stream is copied whole", func(t *testing.T) {
		drafted, accepted := got(t, []tape.RequestRecord{stream(96, ptr(200), ptr(120))})
		if drafted != 200 || accepted != 120 {
			t.Errorf("drafted/accepted = %d/%d, want 200/120", drafted, accepted)
		}
	})
}

// narrowRun is a two-GPU run whose first estimate already happened, exactly
// the way collectPlacement leaves it: a dense 4-block model fully offloaded
// over both host GPUs, per-device active bytes filled by WithModel.
//
// Block tensors are 1 GiB and the output 256 MiB, so the even split leaves
// each card over the 1 GiB floor bandwidth.Contradiction holds a device to
// (3 GiB and 1.25 GiB). Peaks are distinct so a ceiling averaged over the two
// can never be mistaken for either card's own.
func narrowRun(t *testing.T, flags tape.ServerFlags) *run {
	t.Helper()
	var ts []placement.Tensor
	for i := 0; i < 4; i++ {
		ts = append(ts, placement.NewTensor(fmt.Sprintf("blk.%d.attn_q.weight", i), 1<<30))
	}
	ts = append(ts,
		placement.NewTensor("token_embd.weight", 64<<20),
		placement.NewTensor("output.weight", 256<<20),
	)
	r := &run{
		tensors: ts,
		flags:   flags,
		props:   &server.Props{}, // no engine block: the GGUF+args path
		model: tape.ModelInfo{
			ActiveBytesPerToken: 4*(1<<30) + 256<<20, // dense: every block plus the output
		},
		host: tape.HostInfo{GPUs: []tape.GPUInfo{
			{Index: 0, Name: "card A", PeakBandwidthBytesPerSec: 700 << 30},
			{Index: 1, Name: "card B", PeakBandwidthBytesPerSec: 900 << 30},
		}},
	}
	r.collectPlacement()
	return r
}

// narrowSamples is the run's last device reading: both cards seen, the
// server's own VRAM (ProcBytes) as given. UsedBytes values are a card A that
// holds the model and a card B with one foreign MiB — the shape of the
// CUDA_VISIBLE_DEVICES take this round came from.
func narrowSamples(gpu0Proc, gpu1Proc int64) []tape.GPUSample {
	return []tape.GPUSample{
		{Index: 0, UsedBytes: 30 << 30, ProcBytes: gpu0Proc},
		{Index: 1, UsedBytes: 1 << 20, ProcBytes: gpu1Proc},
	}
}

// narrowSummary is the summary a narrowed run reduces to, carrying only what
// the placement questions consult.
func narrowSummary(r *run, gpusAtEnd []tape.GPUSample) *tape.RunSummary {
	return &tape.RunSummary{
		Model:     r.model,
		Host:      r.host,
		Placement: r.place,
		GPUsAtEnd: gpusAtEnd,
	}
}

func deviceIs(p tape.PlacementSummary, name string) bool {
	for _, d := range p.Devices {
		if d.Device == name {
			return true
		}
	}
	return false
}

func countWarning(warnings []string, substr string) int {
	n := 0
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			n++
		}
	}
	return n
}

// TestNarrowPlacement: the recorder re-estimates the placement over only the
// devices its own readings show holding the server's weights (lead, 2026-09-16).
//
// collectPlacement runs before the run does, so it can only spread the model
// over every device the box has. A server launched with CUDA_VISIBLE_DEVICES=0
// on a two-GPU box then had half a 29 GB model claimed for a card that held
// one MiB, and bandwidth.Contradiction — correctly — refused to derive
// anything from that placement at all. The samples that exist by reduce time
// name the devices the server's PID actually holds VRAM on, so the estimate
// is replayed over exactly those. FAIL-first: the recorder package did not
// compile against this test (*run has no narrowPlacement).
func TestNarrowPlacement(t *testing.T) {
	gpu0HoldsWeights := narrowSamples(30<<30, 0)
	bothHoldWeights := narrowSamples(30<<30, 28<<30)

	t.Run("one card holding the weights narrows to it", func(t *testing.T) {
		r := narrowRun(t, tape.ServerFlags{NGL: "99"})
		if !deviceIs(r.place, "GPU1") {
			t.Fatal("fixture: the first estimate spread the model over both cards")
		}
		r.narrowPlacement(gpu0HoldsWeights)

		if !deviceIs(r.place, "GPU0") || deviceIs(r.place, "GPU1") {
			t.Fatalf("devices = %v, want the CPU and GPU0 only", deviceNamesOf(r.place))
		}
		if got, want := r.place.VRAMWeightsBytes, int64(4*(1<<30)+256<<20); got != want {
			t.Errorf("VRAMWeightsBytes = %d, want %d (every GPU byte on the one card)", got, want)
		}
		// The card the reading contradicted is gone from the placement, so
		// the contradiction the narrow run used to carry is gone with it.
		if c := bandwidth.Contradiction(narrowSummary(r, gpu0HoldsWeights)); c != nil {
			t.Errorf("Contradiction = %+v, want nil: the narrowed placement contradicts no reading", c)
		}
		if n := countWarning(r.warnings, "the only device holding the server's weights"); n != 1 {
			t.Errorf("narrowing warning appears %d times in %q, want exactly 1", n, r.warnings)
		}
	})

	t.Run("both cards holding weights leaves the placement alone", func(t *testing.T) {
		r := narrowRun(t, tape.ServerFlags{NGL: "99"})
		before := r.place
		r.narrowPlacement(bothHoldWeights)
		if !deviceIs(r.place, "GPU1") || r.place.VRAMWeightsBytes != before.VRAMWeightsBytes {
			t.Errorf("placement changed under a reading that contradicts no device of it")
		}
		if n := countWarning(r.warnings, "the only device"); n != 0 {
			t.Errorf("warning printed for an un-narrowed run: %q", r.warnings)
		}
	})

	t.Run("no ProcBytes anywhere leaves the placement alone", func(t *testing.T) {
		r := narrowRun(t, tape.ServerFlags{NGL: "99"})
		before := r.place
		// The server's PID was never identified, so the column is absent:
		// UsedBytes is all the reading has, and another process's allocation
		// is not this server's placement.
		r.narrowPlacement(narrowSamples(0, 0))
		if r.place.VRAMWeightsBytes != before.VRAMWeightsBytes || !deviceIs(r.place, "GPU1") {
			t.Errorf("placement narrowed from readings that carry no ProcBytes at all")
		}
	})

	t.Run("ProcBytes under the floor on every card leaves the placement alone", func(t *testing.T) {
		r := narrowRun(t, tape.ServerFlags{NGL: "99"})
		before := r.place
		r.narrowPlacement(narrowSamples(100<<20, 50<<20))
		if r.place.VRAMWeightsBytes != before.VRAMWeightsBytes || !deviceIs(r.place, "GPU1") {
			t.Errorf("placement narrowed although no card held weights above the floor")
		}
	})

	t.Run("an engine placement is never re-estimated", func(t *testing.T) {
		r := narrowRun(t, tape.ServerFlags{NGL: "99"})
		r.place = tape.PlacementSummary{
			Source: placement.SourceEngine,
			Devices: []tape.DevicePlacement{
				{Device: "GPU0", Bytes: 2 << 30},
				{Device: "GPU1", Bytes: 2 << 30},
			},
		}
		r.narrowPlacement(gpu0HoldsWeights)
		if !deviceIs(r.place, "GPU1") || r.place.Source != placement.SourceEngine {
			t.Errorf("engine placement replaced by an estimate; the engine's word is the record")
		}
	})

	t.Run("an unknown placement is never re-estimated", func(t *testing.T) {
		r := narrowRun(t, tape.ServerFlags{NGL: "99"})
		r.place = tape.PlacementSummary{Source: placement.SourceUnknown}
		r.narrowPlacement(gpu0HoldsWeights)
		if r.place.Source != placement.SourceUnknown || len(r.place.Devices) != 0 {
			t.Errorf("unknown placement replaced by an estimate")
		}
	})

	t.Run("estimator warnings are not printed twice", func(t *testing.T) {
		// The dropped -ot rule warns on every estimate; the re-estimate runs
		// over the same tensors and flags, so its warning is the sentence
		// collectPlacement already appended.
		r := narrowRun(t, tape.ServerFlags{NGL: "99", OverrideTens: []string{`blk\.0\.=NVME`}})
		if n := countWarning(r.warnings, "-ot"); n != 1 {
			t.Fatalf("fixture: first estimate warned %d times, want 1 (%q)", n, r.warnings)
		}
		r.narrowPlacement(gpu0HoldsWeights)
		if n := countWarning(r.warnings, "-ot"); n != 1 {
			t.Errorf("-ot warning appears %d times after re-estimating, want 1 (%q)", n, r.warnings)
		}
	})

	t.Run("several cards in play are named together", func(t *testing.T) {
		// Three host cards, the middle one dark: the in-play set keeps host
		// names, so the surviving cards are GPU0 and GPU2.
		r := narrowRun(t, tape.ServerFlags{NGL: "99"})
		r.host.GPUs = append(r.host.GPUs, tape.GPUInfo{Index: 2, Name: "card C", PeakBandwidthBytesPerSec: 700 << 30})
		r.collectPlacement() // re-spread over the three cards the box now has
		r.narrowPlacement([]tape.GPUSample{
			{Index: 0, UsedBytes: 30 << 30, ProcBytes: 30 << 30},
			{Index: 1, UsedBytes: 1 << 20},
			{Index: 2, UsedBytes: 30 << 30, ProcBytes: 29 << 30},
		})
		if deviceIs(r.place, "GPU1") || !deviceIs(r.place, "GPU2") {
			t.Fatalf("devices = %v, want GPU1 gone and GPU2 kept under its host name", deviceNamesOf(r.place))
		}
		if n := countWarning(r.warnings, "GPU0 and GPU2, the only devices holding the server's weights"); n != 1 {
			t.Errorf("warning = %q, want the two in-play cards named once", r.warnings)
		}
	})
}

// TestNarrowPlacementCeiling: the narrowed placement gives the Decode line
// its ratio back, and the ceiling is the one card's own peak — never an
// average over a card the run never touched (lead, 2026-09-16). FAIL-first:
// before the narrowing, Contradiction made Ceiling refuse the fixture and
// OfPeak reported nothing.
func TestNarrowPlacementCeiling(t *testing.T) {
	r := narrowRun(t, tape.ServerFlags{NGL: "99"})
	gpusAtEnd := narrowSamples(30<<30, 0)

	// Before: the even split claims card B, the reading contradicts it, and
	// no ceiling is derivable from a placement like that.
	if _, ok := bandwidth.Ceiling(narrowSummary(r, gpusAtEnd)); ok {
		t.Fatal("fixture: the un-narrowed placement derived a ceiling despite the contradiction")
	}

	r.narrowPlacement(gpusAtEnd)
	s := narrowSummary(r, gpusAtEnd)
	s.Timings.EffectiveBandwidthBytesPerSec = 350 << 30

	ceiling, ok := bandwidth.Ceiling(s)
	if !ok {
		t.Fatalf("Ceiling not derivable after narrowing (%+v)", s.Placement)
	}
	if want := int64(700 << 30); ceiling != want {
		t.Errorf("ceiling = %d, want %d: card A's own peak, not an average over the dark card", ceiling, want)
	}
	ratio, ok := bandwidth.OfPeak(s)
	if !ok {
		t.Fatal("OfPeak not derivable after narrowing")
	}
	if want := 0.5; ratio != want {
		t.Errorf("ratio = %v, want %v (350 of 700 GiB/s)", ratio, want)
	}
}

// deviceNamesOf lists a placement's device names, for failure messages.
func deviceNamesOf(p tape.PlacementSummary) []string {
	var out []string
	for _, d := range p.Devices {
		out = append(out, d.Device)
	}
	return out
}
