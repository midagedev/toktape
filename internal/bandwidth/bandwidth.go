// Package bandwidth owns every peak-bandwidth figure the card prints.
//
// The card's Decode line carries an effective bandwidth — the bytes a token
// really moved, Model.ActiveBytesPerToken × the decode rate — and a ratio
// against a ceiling. Before TTP-34 (2026-09-13) that ceiling was the sum of
// every GPU's peak bandwidth, and it was wrong twice:
//
//  1. Partial CPU offload. With -ot or -ncmoe the routed experts live in host
//     RAM, so most of a token's active bytes never touch a GPU. Dividing by
//     GPU bandwidth alone reported 7–8 % for runs that were in fact close to
//     their real ceiling.
//  2. More than one GPU. llama.cpp's default split is by layer: a token passes
//     GPU0's layers, then GPU1's. The two never read at the same time for one
//     token, so the sum of their bandwidths is a ceiling no run can reach.
//
// Both are the same mistake — summing devices in *bandwidth* when the layer
// split makes them sequential. Devices are summed in **time**: for a placement
// whose device d reads a_d bytes per token at peak bandwidth bw_d, one token's
// floor time is Σ a_d / bw_d, so the fastest effective bandwidth the placement
// allows is
//
//	ceiling = (Σ a_d) / (Σ a_d / bw_d)
//
// which is the harmonic mean of the devices' bandwidths weighted by the bytes
// each one carries. With every byte on one device it collapses to that
// device's bandwidth, which is exactly what the card printed before for a
// single-GPU run. The user chose to fix both cases on 2026-09-13.
//
// # Why a_d is counted the way active.go counts it
//
// The per-device active bytes here and Model.ActiveBytesPerToken are two views
// of one quantity, and on a real recording they are built from the same parse
// of the same GGUF: internal/placement/gguf.go hands one []Tensor to
// ActiveBytesPerToken, and internal/recorder/record.go hands the same slice to
// placement.EstimateVerbose, which buckets it into DevicePlacement.Classes. So
// the class rule below is a deliberate mirror of internal/placement/active.go
// rather than a second opinion.
//
// The mirror is not exact, and TTP-56 (2026-09-14) found where it breaks.
// active.go decides sparseness by tensor NAME; DevicePlacement.Classes has no
// names, only bytes per class, and ClassExperts holds three different things:
// the stacked per-expert weights (sparse), the MoE router ffn_gate_inp (read in
// full) and the shared expert _shexp (read in full). Scaling all three by
// used/count under-counts every model that has a router or a shared expert —
// 0.818 GB of a 7.639 GB token on the ws DeepSeek V4.1 Flash recording,
// 10.71 %, which is over SplitTolerance and is why that run printed no ratio.
//
// TTP-68 (2026-09-14) closed it at the owner of the problem. The names exist
// exactly once, at record time, so the recorder now answers the question while
// it still has them and writes DevicePlacement.ActiveBytesPerToken; the reader
// uses that figure and the class rule below is never consulted. What remains
// of the defect is the tapes already on disk, which carry no such field — the
// class rule is their fallback, ExpertsSplit in split.go still recovers how
// many bytes the gap is, and the 10 % check still does its job on them, which
// is to report nothing rather than a wrong ratio. DeviceExpertsSplit locates
// those bytes per device on a tape that does carry the field, which is the
// part TTP-56 recorded as not recoverable.
//
// # Unknown is unknown
//
// A ratio is reported only when every device carrying active bytes has a known
// bandwidth AND the per-device sum agrees with Model.ActiveBytesPerToken. When
// it does not, the placement and the record disagree about what a token reads,
// and a ratio built on either one would be a number picked for being nicer
// than the other (CLAUDE.md: never fix a disagreement by picking the nicer
// number). The card then prints the effective bandwidth and no ratio at all —
// no "?", no partial figure (lead's ruling on TTP-34, 2026-09-13, after the
// first implementation round found that the hand-written card fixtures
// disagree with themselves; rebuilding those fixtures from a real recording is
// a separate ticket).
package bandwidth

import (
	"math"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// bytesPerTransfer is the width of one memory channel. A DDR channel moves 64
// bits per transfer, so a DDR<n>-<MT/s> part on C channels peaks at
// MT/s × 8 × C bytes per second: DDR5-6000 on two channels is 96.0 GB/s,
// DDR5-4800 on twelve is 460.8 GB/s.
const bytesPerTransfer = 8

// SplitTolerance is how far the per-device active-byte sum may differ from
// Model.ActiveBytesPerToken before the split is treated as underivable.
//
// The two are computed from one tensor list on a real run, so any real
// disagreement is a bug in one of them rather than noise; the tolerance is
// there for rounding and for a placement estimated from args rather than read
// from a server log. Changing it needs a dated comment and a FAIL-first check
// (CLAUDE.md).
const SplitTolerance = 0.10

// HostBandwidth is the host's RAM bandwidth in bytes per second, where the
// figure came from, and whether there is one at all.
//
// There are two sources and they are not equal. A recorded
// HostInfo.RAMBytesPerSec wins, because it is the only one a Linux run can
// actually have: RAMSpeed and RAMChannels live in the DMI tables and
// /sys/firmware/dmi/tables/DMI is mode 0400 root-only, so
// internal/procmon/host.go leaves both empty on every real recording and a
// mixed CPU/GPU placement — the very case the "of peak" ratio exists for — had
// no host leg and printed no ratio (TTP-45, 2026-09-14). The way out is to let
// the operator state it.
//
// source is one of tape.RAMSourceDMI, tape.RAMSourceStated or
// tape.RAMSourceMeasured, and the caller prints it, because a theoretical peak
// derived from the fitted modules, a number the operator typed and a STREAM
// run are three different claims and only the last is a measurement of this
// machine. A tape carrying RAMBytesPerSec with no RAMSource is read as
// "stated": the bytes did not come off the machine, so the weakest provenance
// that fits is the honest one.
//
// A derivation from RAMSpeed × RAMChannels reports tape.RAMSourceDMI: the
// trailing number of a "DDR<generation>-<MT/s>" string is transfers per second
// in millions and each channel is 8 bytes wide. A string that does not parse,
// or a zero or negative channel count, is unknown — ok is false and the caller
// prints no ceiling rather than a guessed one.
func HostBandwidth(h tape.HostInfo) (bytesPerSec int64, source string, ok bool) {
	if h.RAMBytesPerSec > 0 {
		src := h.RAMSource
		if src == "" {
			src = tape.RAMSourceStated
		}
		return h.RAMBytesPerSec, src, true
	}
	if h.RAMChannels <= 0 {
		return 0, "", false
	}
	mts, ok := transfersPerSecMillions(h.RAMSpeed)
	if !ok {
		return 0, "", false
	}
	return mts * bytesPerTransfer * int64(h.RAMChannels) * 1_000_000, tape.RAMSourceDMI, true
}

// HostBytesPerSec is HostBandwidth without the provenance: the host's RAM
// bandwidth in bytes per second, or 0 when it was not observed. It is what
// every arithmetic caller in this package wants; a caller that LABELS the
// figure wants HostBandwidth instead.
func HostBytesPerSec(h tape.HostInfo) int64 {
	bps, _, _ := HostBandwidth(h)
	return bps
}

// transfersPerSecMillions pulls the MT/s figure out of a memory-speed string
// such as "DDR5-6000" or "LPDDR5-8533". It is a pure function of the string so
// it is testable without a machine: the part after the last "-" must be a
// positive integer and the part before it must be non-empty, so a bare "6000"
// or a trailing "DDR5-" is not accepted.
func transfersPerSecMillions(speed string) (int64, bool) {
	speed = strings.TrimSpace(speed)
	i := strings.LastIndex(speed, "-")
	if i <= 0 || i == len(speed)-1 {
		return 0, false
	}
	n, err := strconv.ParseInt(speed[i+1:], 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// DeviceBytesPerSec is the peak bandwidth of one placement device — "GPU<n>"
// or tape.DeviceCPU — or 0 when it was not observed or the name is not one
// this package knows.
func DeviceBytesPerSec(s *tape.RunSummary, device string) int64 {
	if s == nil {
		return 0
	}
	if device == tape.DeviceCPU {
		return HostBytesPerSec(s.Host)
	}
	rest, ok := strings.CutPrefix(device, "GPU")
	if !ok {
		return 0
	}
	idx, err := strconv.Atoi(rest)
	if err != nil {
		return 0
	}
	for _, g := range s.Host.GPUs {
		if g.Index == idx {
			return g.PeakBandwidthBytesPerSec
		}
	}
	return 0
}

// activeBytesOn is the bytes device d reads to decode one token, given the
// model's expert counts and whether the model ties its embedding matrix to the
// output projection.
//
// The rule mirrors internal/placement/active.go class for class, because the
// two sum the same tensors (see the package doc):
//
//   - attention, ffn, other and output count in full: every token reads all of
//     them. The output head is a matrix against one token's hidden state, but
//     it is still read in full to produce the logits, and active.go counts it.
//   - experts count as bytes × used / N, and in full when N is 0 (a dense
//     model whose FFN was classed as experts) or when used >= N (every expert
//     routed). That is active.go's own condition — but active.go applies the
//     fraction only to tensors whose NAME is a stacked expert weight, while
//     this can only apply it to the whole class, router and shared expert
//     included. That under-count is the whole of TTP-56, and on a pre-TTP-68
//     tape it is still what happens; see the package doc and ExpertsSplit in
//     split.go.
//   - embeddings count in full only for a tied model, where the matrix *is*
//     the output projection; otherwise decoding looks up one row, not the
//     matrix, and they are excluded.
//   - n-gram tables never count: they are lookup tables, not weights in the
//     per-token matmul chain, and they are what NeverLoadedBytes exists for
//     (handover lesson 3).
//
// All of that is the FALLBACK. When the recorder answered the question itself
// — DevicePlacement.ActiveBytesPerToken, filled from this device's own tensor
// NAMES by placement.ActiveBytesPerTokenTied (TTP-68, 2026-09-14) — that
// figure is used instead and the class rule is not consulted at all. It is not
// a better estimate, it is the same computation Model.ActiveBytesPerToken is,
// restricted to one device, so the per-device figures sum to the record with
// no residue and the SplitTolerance check below passes by construction. Zero
// means a tape recorded before the field existed, and every tape already on
// disk is that shape, so the fallback stays live and tested.
func activeBytesOn(d tape.DevicePlacement, m tape.ModelInfo, tied bool) int64 {
	if d.ActiveBytesPerToken > 0 {
		return d.ActiveBytesPerToken
	}
	var total int64
	for class, b := range d.Classes {
		if b <= 0 {
			continue
		}
		switch class {
		case tape.ClassNGram:
			// Never read during decode.
		case tape.ClassEmbed:
			if tied {
				total += b
			}
		case tape.ClassExperts:
			if m.NExperts > 0 && m.NExpertsUsed > 0 && m.NExpertsUsed < m.NExperts {
				total += b * int64(m.NExpertsUsed) / int64(m.NExperts)
			} else {
				total += b
			}
		default:
			total += b
		}
	}
	return total
}

// tiedEmbeddings reports whether the placement looks like a model that ties
// its embedding matrix to its output projection, in which case the matrix is
// read in full for every token.
//
// This is a PROXY for the test internal/placement/active.go makes, and it is
// weaker than that one. active.go looks for a tensor literally named
// "output.weight"; DevicePlacement.Classes has no tensor names, only bytes per
// class, so the proxy is "no device carries any ClassOutput bytes". The
// difference matters and is deliberate: output_norm.weight is also classed
// ClassOutput (internal/placement/classify.go) and every transformer has one,
// tied or not, so a real tied model's placement still carries a few ClassOutput
// bytes and this proxy will call it untied. It therefore errs towards
// excluding the embedding matrix, which undercounts a tied model's active
// bytes — and the SplitTolerance check below is what catches that, by
// reporting no ratio rather than a wrong one.
func tiedEmbeddings(p tape.PlacementSummary) bool {
	for _, d := range p.Devices {
		if d.Classes[tape.ClassOutput] > 0 {
			return false
		}
	}
	return true
}

// Ceiling is the highest effective bandwidth this run's placement allows, in
// bytes per second, and whether it was derivable at all.
//
// It is (Σ a_d) / (Σ a_d / bw_d) over the devices carrying active bytes — the
// devices summed in time, not in bandwidth, because llama.cpp's layer split
// makes them sequential for any one token. See the package doc.
//
// ok is false when the model reports no active bytes per token, when the
// placement carries none, when any device carrying active bytes has an
// unknown bandwidth, or when the per-device sum disagrees with
// Model.ActiveBytesPerToken by more than SplitTolerance.
func Ceiling(s *tape.RunSummary) (int64, bool) {
	if s == nil || s.Model.ActiveBytesPerToken <= 0 {
		return 0, false
	}
	tied := tiedEmbeddings(s.Placement)

	var sum int64
	var seconds float64
	for _, d := range s.Placement.Devices {
		a := activeBytesOn(d, s.Model, tied)
		if a <= 0 {
			continue
		}
		bw := DeviceBytesPerSec(s, d.Device)
		if bw <= 0 {
			// A device reads for every token and we do not know how fast.
			// Any ceiling we returned would be a guess.
			return 0, false
		}
		sum += a
		seconds += float64(a) / float64(bw)
	}
	if sum <= 0 || seconds <= 0 {
		return 0, false
	}

	// The model figure is the record and this split is the check (CLAUDE.md).
	// When they disagree, the placement and the model do not agree about what
	// a token reads, and no ratio built on either is honest.
	record := float64(s.Model.ActiveBytesPerToken)
	if diff := float64(sum) - record; diff/record > SplitTolerance || -diff/record > SplitTolerance {
		return 0, false
	}

	// Rounded, not truncated: with every byte on one device the division is
	// sum / (sum / bw), which float64 can land a hair under bw, and truncating
	// would report a ceiling of bw-1 for the single-device case the formula is
	// supposed to reduce to exactly.
	return int64(math.Round(float64(sum) / seconds)), true
}

// OfPeak is the run's measured effective bandwidth as a fraction of the
// ceiling its placement allows, and whether it was derivable.
//
// A run at the ceiling returns 1.0. Values above 1.0 are possible in principle
// — a cache hit the model of a token's reads does not know about — and are
// returned as measured rather than clamped, because a ratio over 100 % is a
// signal that one of the two figures is wrong and hiding it would waste it.
func OfPeak(s *tape.RunSummary) (float64, bool) {
	if s == nil || s.Timings.EffectiveBandwidthBytesPerSec <= 0 {
		return 0, false
	}
	ceiling, ok := Ceiling(s)
	if !ok || ceiling <= 0 {
		return 0, false
	}
	return float64(s.Timings.EffectiveBandwidthBytesPerSec) / float64(ceiling), true
}
