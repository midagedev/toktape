package tape

import "sort"

// Which GPUs did the server use? (TTP-209, 2026-10-12.) A bloomery clip ran
// with its server pinned to the RTX A6000 of a 3090 + A6000 host, and the
// card still named both cards, so the published run needed a note to say
// which one worked. The tape already holds the answer: GPUSample.ProcBytes is
// the VRAM held by the server's own process, read from nvidia-smi's
// compute-apps table. This file is the only place that rule lives; the text
// card, the PNG card and the hub index all call ServerRig.

// usedIndexes is the indexes, ascending, of the GPUs a reading shows the
// server holding memory on; known is false when no reading carried any.
func usedIndexes(readings []GPUSample) (used []int, known bool) {
	seen := map[int]bool{}
	for _, g := range readings {
		if g.ProcBytes > 0 && !seen[g.Index] {
			seen[g.Index] = true
			used = append(used, g.Index)
		}
	}
	sort.Ints(used)
	return used, len(used) > 0
}

// ServerRig is the host's GPU list as a RIG line should print it: the cards
// the server used, in host order, and how many host cards it did not touch.
// It reads the summary's end reading (GPUsAtEnd), the one GPU reading every
// renderer holds, so the card and the hub index cannot disagree; a card the
// server held only mid-run and freed before the end is not counted. When use
// is unknown, or names no card the host lists, it
// returns the host list and idle 0: nothing was observed, nothing is claimed.
func ServerRig(s *RunSummary) (gpus []GPUInfo, idle int) {
	used, known := usedIndexes(s.GPUsAtEnd)
	if !known {
		return s.Host.GPUs, 0
	}
	in := map[int]bool{}
	for _, i := range used {
		in[i] = true
	}
	for _, g := range s.Host.GPUs {
		if in[g.Index] {
			gpus = append(gpus, g)
		}
	}
	if len(gpus) == 0 {
		return s.Host.GPUs, 0
	}
	return gpus, len(s.Host.GPUs) - len(gpus)
}
