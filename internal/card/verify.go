package card

import (
	"fmt"
	"strconv"

	"github.com/midagedev/toktape/internal/bandwidth"
	"github.com/midagedev/toktape/internal/tape"
)

// The verify-step view of a run with a draft model (TTP-67, measured
// 2026-09-14).
//
// With `--spec-draft-n-max 3` the engine does not run one forward pass per
// token. It drafts up to three, runs the target model ONCE over the batch of
// (1 + drafted) and keeps the longest accepted prefix. So the weights are read
// once per verify STEP, and "bytes per accepted token" is not a rate the
// hardware ever experienced — it is the real rate divided by the acceptance.
//
// On the ws prose run that is the difference between 65.5 GB/s off host RAM
// (57 % of the machine's 115.8 GB/s STREAM figure) and 109.9 GB/s (94.9 %).
// The first reads as headroom where there is none, which is the opposite of
// what the card is for.
//
// internal/bandwidth.Speculative owns the arithmetic and answers a concurrent
// run as well as a single one, which matters here: this project's hero is a
// two-stream run, so a figure that only worked at Concurrency 1 would never
// appear on the card anyone actually looks at. Nothing in this file recomputes
// a byte count — it decides what the card says and whether a ratio is allowed.

// VerifyRAM is the host-memory-bus rate a speculative run really sustained:
// the bytes one verify step reads off host RAM, times the steps per second.
//
// ofPeak is its share of the host's memory bandwidth, or 0 when no ratio may
// be printed. Two separate things have to hold for a ratio: the host ceiling
// has to have been observed at all (internal/procmon leaves it unreadable on
// Linux, so an operator states it — tape.HostInfo.RAMSource), and the CPU's
// active bytes have to be provably the whole of what it reads. The second is
// bandwidth.RAMSide.Exact, and it is the right gate for this figure too
// because Speculative builds its step from the same per-device active bytes:
// if that count is an under-count, so is this one, and a ratio over an
// under-count is a claim the tape cannot support.
//
// bandwidth.Verify carries neither of those two fields today, which is the
// only reason this function exists rather than a field read — see the report.
//
// ok is false when the run used no draft model, or the step arithmetic is not
// derivable from what the tape recorded.
func VerifyRAM(s *tape.RunSummary) (bytesPerSec int64, ofPeak float64, ok bool) {
	v, ok := bandwidth.Speculative(s)
	if !ok || v.RAMBytesPerSec <= 0 {
		return 0, 0, false
	}
	r, haveRAM := bandwidth.RAM(s)
	peak := bandwidth.HostBytesPerSec(s.Host)
	if haveRAM && r.Exact && peak > 0 {
		ofPeak = float64(v.RAMBytesPerSec) / float64(peak)
	}
	return v.RAMBytesPerSec, ofPeak, true
}

// verifyRAM is the text card's Decode-row clause for a speculative run, or ""
// when there is none. It replaces the per-accepted-token clause rather than
// joining it — see bandwidthString.
func verifyRAM(s *tape.RunSummary) (string, bool) {
	bps, ofPeak, ok := VerifyRAM(s)
	if !ok {
		return "", false
	}
	out := "≈ " + formatGBs(bps) + " from RAM per verify step"
	if ofPeak > 0 {
		out += ", " + formatPct(ofPeak) + " of peak"
	}
	return out, true
}

// verifyParts is what the Draft row adds so the bandwidth clause above it has
// a denominator on the same card: how many times the target model ran, and how
// many tokens it was handed each time.
//
// Without these two the reader is asked to take "per verify step" on faith.
// With them the arithmetic is checkable from the card alone — steps times the
// batch is roughly the token count on the Context row — which is the standard
// the acceptance figure beside it already meets by carrying its own counts.
func verifyParts(s *tape.RunSummary) []string {
	v, ok := bandwidth.Speculative(s)
	if !ok {
		return nil
	}
	// One part and not two, so wrapJoin cannot put the step count on one line
	// and the batch that gives it its meaning on another. "of" and not "×":
	// the product is the tokens the target model was HANDED, which is more
	// than the run generated, because a rejected draft is thrown away. A "×"
	// would invite the reader to check it against the Context row and find it
	// wrong.
	return []string{fmt.Sprintf("%d verify steps of %s tokens",
		v.Steps, strconv.FormatFloat(v.Batch, 'f', 1, 64))}
}
