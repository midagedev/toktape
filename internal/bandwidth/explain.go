package bandwidth

// Explain exists because finding TTP-56 and TTP-68 took a scratch script.
//
// The question "why does this tape print no 'of peak' ratio?" had exactly one
// answer available — Ceiling() returning ok=false — and every figure that
// decides it was a local variable inside an unexported function. Answering it
// meant re-deriving the per-device active bytes by hand against a tape dump,
// twice, on two different machines, and the answer the second time was a
// 10.71 % gap nobody could see from outside the package.
//
// So the intermediate figures are now the package's output too. Explain walks
// the same path Ceiling, OfPeak, RAM and Speculative walk and returns every
// number they consulted, including the ones that made them give up. Its String
// form is meant to be pasted into a ticket.

import (
	"fmt"
	"strings"

	"github.com/midagedev/toktape/internal/placement"
	"github.com/midagedev/toktape/internal/tape"
)

// DeviceBandwidth is one device's leg of the ceiling, with its provenance.
type DeviceBandwidth struct {
	// Device is the placement name: tape.DeviceCPU, "GPU0", ...
	Device string
	// Bytes is every weight byte resident on the device.
	Bytes int64
	// ActiveBytesPerToken is what a token reads of them.
	ActiveBytesPerToken int64
	// Recorded is true when ActiveBytesPerToken came from the tape's own
	// per-device figure (the recorder counted this device's tensors by name)
	// and false when it is this package's class-proportion estimate, which
	// under-counts any device holding a router or a shared expert.
	Recorded bool
	// PeakBytesPerSec is the device's peak bandwidth, 0 when unknown. A
	// device that reads for every token and has no known peak is on its own
	// enough to make the whole ceiling underivable.
	PeakBytesPerSec int64
	// FloorSeconds is ActiveBytesPerToken / PeakBytesPerSec: this device's
	// share of one token's floor time, and the term it contributes to the
	// harmonic mean. 0 when the peak is unknown.
	FloorSeconds float64
}

// Explanation is every figure the bandwidth clauses are built from, including
// the ones that made a clause report nothing.
type Explanation struct {
	// RecordActiveBytesPerToken is Model.ActiveBytesPerToken: the record.
	RecordActiveBytesPerToken int64
	// Devices are the placement's devices in placement order.
	Devices []DeviceBandwidth
	// SumActiveBytesPerToken is the per-device sum: the check.
	SumActiveBytesPerToken int64
	// Gap is record − sum, and GapFraction is it as a fraction of the record.
	// |GapFraction| over SplitTolerance is what makes Ceiling report nothing.
	Gap         int64
	GapFraction float64
	// WithinTolerance is |GapFraction| <= SplitTolerance.
	WithinTolerance bool

	// HostBytesPerSec and HostSource are HostBandwidth's answer; HostKnown is
	// its ok. Without a host figure any placement with CPU-resident weights
	// has no ceiling.
	HostBytesPerSec int64
	HostSource      string
	HostKnown       bool

	// CeilingBytesPerSec / CeilingKnown are Ceiling.
	CeilingBytesPerSec int64
	CeilingKnown       bool
	// EffectiveBytesPerSec is the measured figure the ratio divides, and
	// EffectiveKnown is Combined's ok: the recorder may have written
	// Timings.EffectiveBandwidthBytesPerSec on a placement where one figure
	// over every bus is refused (an engine placement, 2026-09-15), so the
	// field's presence and the card's willingness to print it are two
	// different questions and this carries the second.
	EffectiveBytesPerSec int64
	EffectiveKnown       bool
	// OfPeak / OfPeakKnown are OfPeak.
	OfPeak      float64
	OfPeakKnown bool

	// RAM / RAMKnown are the host-bus view, Verify / VerifyKnown the
	// verify-step view.
	RAM         RAMSide
	RAMKnown    bool
	Verify      Verify
	VerifyKnown bool

	// ExpertsSplit / ExpertsSplitKnown are the model-wide sparse/dense split
	// of ClassExperts, which is what explains a gap when there is one.
	ExpertsSplit      ExpertsClassBytes
	ExpertsSplitKnown bool

	// EnginePlacement says the placement is an engine's own report through
	// /props (placement.SourceEngine, 2026-09-15, ExLlamaV3) rather than a
	// replay. Its devices' active bytes, when absent, are not estimated —
	// the per-device and ram/verify lines below say so instead of silently
	// reading as a failure to derive.
	EnginePlacement bool
}

// Explain derives every bandwidth figure of a run and reports the workings.
//
// It never fails: a run it can say nothing about produces an Explanation whose
// Known flags are all false, which is itself the answer to "why is the card
// not printing it".
func Explain(s *tape.RunSummary) Explanation {
	var e Explanation
	if s == nil {
		return e
	}
	e.RecordActiveBytesPerToken = s.Model.ActiveBytesPerToken
	// Combined, not the raw timings field, so the listing and the card answer
	// "may this figure be printed" the same way for an engine placement.
	e.EffectiveBytesPerSec, e.EffectiveKnown = Combined(s)
	e.EnginePlacement = s.Placement.Source == placement.SourceEngine

	tied := tiedEmbeddings(s.Placement)
	for _, d := range s.Placement.Devices {
		db := DeviceBandwidth{
			Device:              d.Device,
			Bytes:               d.Bytes,
			ActiveBytesPerToken: activeBytesOn(d, s, tied),
			Recorded:            d.ActiveBytesPerToken > 0,
			PeakBytesPerSec:     DeviceBytesPerSec(s, d.Device),
		}
		if db.PeakBytesPerSec > 0 && db.ActiveBytesPerToken > 0 {
			db.FloorSeconds = float64(db.ActiveBytesPerToken) / float64(db.PeakBytesPerSec)
		}
		e.SumActiveBytesPerToken += db.ActiveBytesPerToken
		e.Devices = append(e.Devices, db)
	}
	e.Gap = e.RecordActiveBytesPerToken - e.SumActiveBytesPerToken
	if e.RecordActiveBytesPerToken > 0 {
		e.GapFraction = float64(e.Gap) / float64(e.RecordActiveBytesPerToken)
		f := e.GapFraction
		if f < 0 {
			f = -f
		}
		e.WithinTolerance = f <= SplitTolerance
	}

	e.HostBytesPerSec, e.HostSource, e.HostKnown = HostBandwidth(s.Host)
	e.CeilingBytesPerSec, e.CeilingKnown = Ceiling(s)
	e.OfPeak, e.OfPeakKnown = OfPeak(s)
	e.RAM, e.RAMKnown = RAM(s)
	e.Verify, e.VerifyKnown = Speculative(s)
	e.ExpertsSplit, e.ExpertsSplitKnown = ExpertsSplit(s)
	return e
}

// String renders the explanation as a block meant for a ticket or a terminal.
// Unknown prints as "?" (CLAUDE.md), never as a zero dressed up as a figure.
func (e Explanation) String() string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	p("record  %s/token (model.active_bytes_per_token)", gb(e.RecordActiveBytesPerToken))
	if e.EnginePlacement {
		p("source  engine — placement reported by the engine through /props, not replayed")
	}
	for _, d := range e.Devices {
		src := "estimated from class totals"
		active := gb(d.ActiveBytesPerToken)
		switch {
		case d.Recorded && e.EnginePlacement:
			src = "reported by the engine"
		case !d.Recorded && e.EnginePlacement:
			src = "not estimated — engine placement"
			// The engine said nothing here; "active 0" would read as a
			// measured zero, and an absence prints as "?" (CLAUDE.md).
			active = "?"
		case d.Recorded:
			src = "recorded per device"
		}
		p("  %-5s resident %-10s active %-10s  %-26s  peak %-10s  floor %s",
			d.Device, gb(d.Bytes), active, src,
			gbps(d.PeakBytesPerSec), ms(d.FloorSeconds))
	}
	// An engine placement that reported no per-device active bytes has no sum
	// either: the 0 is the same absence the device rows just printed as "?",
	// and a gap line against it would read "+100 %" against nothing. The
	// figures stay computed on the struct (GapFraction, WithinTolerance); only
	// the rendering stands aside, the same deal the ram/verify lines below
	// already have for this placement.
	if e.EnginePlacement && !anyRecorded(e.Devices) {
		p("sum     ? — the engine reported no per-device active bytes, so there is nothing to sum")
	} else {
		p("sum     %s/token   gap %s (%+.2f %%)  within %.0f %% tolerance: %v",
			gb(e.SumActiveBytesPerToken), gb(e.Gap), e.GapFraction*100, SplitTolerance*100, e.WithinTolerance)
	}
	if e.ExpertsSplitKnown {
		p("experts sparse %s + dense %s (router + shared expert, read in full)",
			gb(e.ExpertsSplit.Sparse), gb(e.ExpertsSplit.Dense))
	} else {
		p("experts split not solvable from this tape")
	}

	if e.HostKnown {
		p("host    %s (%s)", gbps(e.HostBytesPerSec), e.HostSource)
	} else {
		p("host    ? — no RAMBytesPerSec and no RAMSpeed x RAMChannels")
	}
	if e.CeilingKnown {
		p("ceiling %s", gbps(e.CeilingBytesPerSec))
	} else {
		p("ceiling ? — a device carrying active bytes has no known peak, or the gap is over tolerance")
	}
	if e.EffectiveKnown || !e.EnginePlacement {
		// A GGUF tape prints what it always printed, figure or "?".
		p("effective %s", gbps(e.EffectiveBytesPerSec))
	} else {
		// The recorder wrote the figure; Combined refuses it for this
		// placement, and the listing says why rather than dressing the
		// refusal up as a bandwidth.
		p("effective ? — refused for an engine placement: one figure over RAM and VRAM buses is not a bandwidth")
	}
	if e.OfPeakKnown {
		p("of peak %.1f %%", e.OfPeak*100)
	} else {
		p("of peak ? — no ceiling")
	}

	if e.RAMKnown {
		exact := "approximate"
		if e.RAM.Exact {
			exact = "exact"
		}
		p("ram     %s/token -> %s over %d stream(s), %s",
			gb(e.RAM.ActiveBytesPerToken), gbps(e.RAM.BytesPerSec), e.RAM.Streams, exact)
		if e.RAM.OfPeak > 0 {
			p("        %.1f %% of the host bus", e.RAM.OfPeak*100)
		}
	} else if e.EnginePlacement {
		p("ram     ? — engine placement without per-device active bytes; the class estimate is off")
	} else {
		p("ram     ? — not a mixed placement, or no decode rate")
	}
	if e.VerifyKnown {
		v := e.Verify
		p("verify  %d steps over %d stream(s), batch %.3f, %.2f distinct experts/layer",
			v.Steps, v.Streams, v.Batch, v.DistinctExpertsPerLayer)
		p("        %s/step x %.3f steps/s = %s off host RAM, accept %.1f %%",
			gb(v.RAMBytesPerStep), v.StepsPerSec, gbps(v.RAMBytesPerSec), v.AcceptRate*100)
		// The two figures the card's "of peak" clause is gated on, so a card
		// that prints the rate and no percentage says which of the two
		// withheld it.
		exact := "approximate"
		if v.Exact {
			exact = "exact"
		}
		if v.OfPeak > 0 {
			p("        %.1f %% of the host bus, %s", v.OfPeak*100, exact)
		} else {
			p("        ? %% of the host bus — no host figure; %s", exact)
		}
	} else if e.EnginePlacement {
		p("verify  ? — no draft figures, or an engine placement without per-device active bytes")
	} else {
		p("verify  ? — no draft figures, or a concurrent run with no aggregate token count")
	}
	return b.String()
}

// anyRecorded reports whether any device's active-bytes figure is the tape's
// own rather than this package's estimate or the engine's silence.
func anyRecorded(devs []DeviceBandwidth) bool {
	for _, d := range devs {
		if d.Recorded {
			return true
		}
	}
	return false
}

func gb(n int64) string {
	if n == 0 {
		return "0"
	}
	return fmt.Sprintf("%.3f GB", float64(n)/1e9)
}

func gbps(n int64) string {
	if n == 0 {
		return "?"
	}
	return fmt.Sprintf("%.1f GB/s", float64(n)/1e9)
}

func ms(sec float64) string {
	if sec == 0 {
		return "?"
	}
	return fmt.Sprintf("%.2f ms", sec*1000)
}
