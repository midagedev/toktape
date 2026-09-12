package procmon

import "github.com/midagedev/toktape/internal/tape"

// Summarize reduces a run's periodic samples and per-token fault deltas to the
// memory line of the card. It is pure, so the same reduction runs on a live
// run and on a replayed tape.
//
// tokensMajDelta[i] is the number of major faults the process took between the
// arrival of event i-1 and the arrival of event i; for i == 0 it is the faults
// taken between the request being sent and the first event. firstTokenIdx is
// the index of the first event that carried text — llama-server sends an empty
// role chunk first, and counting it is exactly the mistake that turned 18
// tok/s into a recorded 4.5 (handover lesson 1).
//
// The split follows from those two definitions. The delta recorded AT
// firstTokenIdx accumulated while the prompt was still being evaluated, so it
// belongs to the prompt phase:
//
//	MajFaultsPrompt = sum(tokensMajDelta[:firstTokenIdx+1])
//	MajFaultsDecode = sum(tokensMajDelta[firstTokenIdx+1:])
//	MajFaultsPerToken = MajFaultsDecode / decoded tokens
//
// Putting the prefill burst on the decode side instead would push
// MajFaultsPerToken over tape.ColdMajFaultsPerToken and label a warm run
// "cold" — a run whose weights were paged in once, before generation started,
// is not a run that faulted while decoding.
//
// MappedFileBytes is left zero: it comes from MappedFileBytes, not from these
// samples, and total-minus-RSS is never used to infer it (handover lesson 3).
func Summarize(samples []tape.RunSample, tokensMajDelta []uint64, firstTokenIdx int) tape.MemorySummary {
	var m tape.MemorySummary

	if n := len(samples); n > 0 {
		for _, s := range samples {
			if s.Mem.RSSBytes > m.PeakRSSBytes {
				m.PeakRSSBytes = s.Mem.RSSBytes
			}
		}
		m.AtEnd = samples[n-1].Mem
	}

	if n := len(tokensMajDelta); n > 0 {
		cut := firstTokenIdx + 1
		if cut < 0 {
			cut = 0
		}
		if cut > n {
			cut = n
		}
		for _, d := range tokensMajDelta[:cut] {
			m.MajFaultsPrompt += d
		}
		for _, d := range tokensMajDelta[cut:] {
			m.MajFaultsDecode += d
		}
		m.MajFaultsTotal = m.MajFaultsPrompt + m.MajFaultsDecode
		if decoded := n - cut; decoded > 0 {
			m.MajFaultsPerToken = float64(m.MajFaultsDecode) / float64(decoded)
		}
		return m
	}

	// No per-token deltas: fall back to the periodic samples, which can only
	// give the run total. Attributing it to the prompt phase is the honest
	// choice — the decode figure drives the cold label, and a number this
	// coarse must not be the thing that sets it.
	if n := len(samples); n > 1 {
		m.MajFaultsTotal = sub(samples[n-1].Mem.MajFaults, samples[0].Mem.MajFaults)
		m.MajFaultsPrompt = m.MajFaultsTotal
	}
	return m
}

// IsCold reports whether a run's memory summary says the weights were being
// paged in from disk during decode, per tape.ColdMajFaultsPerToken.
func IsCold(m tape.MemorySummary) bool {
	return m.MajFaultsPerToken >= tape.ColdMajFaultsPerToken
}
