package card

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/placement"
	"github.com/midagedev/toktape/internal/tape"
)

// ExplainEnginePlacement is the explainer for a run whose figures came from an
// engine's /props report rather than from a GGUF header and an argv
// (2026-09-15, ExLlamaV3).
//
// It exists for the same reason ExplainCaveats does: "where did this number
// come from" had no answer a reader could check. An engine card's answer is
// one sentence — the model, the placement and the flags are the engine's own
// words — plus the three checks the recorder ran over that report, each with
// its verdict and the figures behind it, so a card that prints no bandwidth
// clause or a warning nobody expected can be located rather than argued
// about. It returns "" for every non-engine run and calls nothing else.
//
// The verdicts are derived from the tape the card itself renders — the same
// figures, the same comparisons — so an outcome that reads differently here
// than on the card is impossible by construction. The one thing the figures
// alone cannot say is whether an absent active split was never sent (the
// normal case) or was sent, disagreed, and got zeroed: the recorder recorded
// that as a warning, and its trailing phrase is matched verbatim below. Both
// spellings are this change's own.
//
//	engine placement of 20260915-210844-glm-5.3-flash-exl3-4.05
//	  source        exllamav3 1.5.0 through /props — no GGUF opened, no argv parsed
//	  split sums    ok        3 devices, classes sum to bytes on every one
//	  active bytes  absent    no device figure; the model's 7.000 GB/token stands, nothing estimated
//	  gpu indices   ok        GPU0, GPU1 among the 2 probed
func ExplainEnginePlacement(s *tape.RunSummary) string {
	if s == nil || s.Placement.Source != placement.SourceEngine {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "engine placement of %s\n", orUnknown(s.ID))
	fmt.Fprintf(&b, "  %-13s %s %s through /props — no GGUF opened, no argv parsed\n",
		"source", engineString(s.Server), "reported the model, the placement and the flags")

	// The recorder's validation (a): a device's byte total against the sum of
	// its own classes, recomputed here from the same map the card prints.
	var mismatched []string
	for _, d := range s.Placement.Devices {
		var sum int64
		for _, bytes := range d.Classes {
			sum += bytes
		}
		if d.Bytes != sum {
			mismatched = append(mismatched, fmt.Sprintf("%s reports %s, its classes sum to %s",
				d.Device, gbDecimal(d.Bytes), gbDecimal(sum)))
		}
	}
	if len(mismatched) == 0 {
		fmt.Fprintf(&b, "  %-13s %-9s %d devices, classes sum to bytes on every one\n",
			"split sums", "ok", len(s.Placement.Devices))
	} else {
		fmt.Fprintf(&b, "  %-13s %-9s %s\n", "split sums", "MISMATCH", strings.Join(mismatched, "; "))
	}

	// The recorder's validation (b): per-device active bytes. Three states,
	// and only the warning tells the last two apart — see the doc above.
	var activeDevices int
	var activeSum int64
	for _, d := range s.Placement.Devices {
		if d.ActiveBytesPerToken > 0 {
			activeDevices++
			activeSum += d.ActiveBytesPerToken
		}
	}
	zeroed := false
	for _, w := range s.Warnings {
		if strings.HasSuffix(w, "all active figures zeroed") {
			zeroed = true
		}
	}
	switch {
	case activeDevices > 0:
		fmt.Fprintf(&b, "  %-13s %-9s %d of %d devices, %s/token against the model's %s\n",
			"active bytes", "kept", activeDevices, len(s.Placement.Devices),
			gbDecimal(activeSum), gbDecimal(s.Model.ActiveBytesPerToken))
	case zeroed:
		fmt.Fprintf(&b, "  %-13s %-9s per-device figures disagreed with the model's and every active figure was zeroed; no bandwidth clause derives\n",
			"active bytes", "zeroed")
	default:
		fmt.Fprintf(&b, "  %-13s %-9s no device figure; the model's %s/token stands, nothing estimated\n",
			"active bytes", "absent", gbDecimal(s.Model.ActiveBytesPerToken))
	}

	// The recorder's validation (c): the engine names devices by nvidia-smi
	// index, and the host probe is the only witness of how many there are.
	var gpus []string
	var offHost []string
	for _, d := range s.Placement.Devices {
		idx, ok := engineGPUIndex(d.Device)
		if !ok {
			continue
		}
		if len(s.Host.GPUs) > 0 && idx < len(s.Host.GPUs) {
			gpus = append(gpus, d.Device)
		} else {
			offHost = append(offHost, d.Device)
		}
	}
	switch {
	case len(offHost) > 0:
		fmt.Fprintf(&b, "  %-13s %-9s %s not among the %d probed\n",
			"gpu indices", "unmatched", strings.Join(offHost, ", "), len(s.Host.GPUs))
	case len(gpus) > 0:
		fmt.Fprintf(&b, "  %-13s %-9s %s among the %d probed\n",
			"gpu indices", "ok", strings.Join(gpus, ", "), len(s.Host.GPUs))
	default:
		fmt.Fprintf(&b, "  %-13s %-9s no GPU device named, or no host probe to check against\n",
			"gpu indices", "—")
	}
	return b.String()
}

// engineGPUIndex reads the nvidia-smi index out of a placement device name:
// GPU<n>. ok is false for the CPU and for anything the engine named
// differently, exactly the names the recorder's own check skips.
func engineGPUIndex(device string) (int, bool) {
	if !strings.HasPrefix(device, "GPU") {
		return 0, false
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(device, "GPU"))
	if err != nil || idx < 0 {
		return 0, false
	}
	return idx, true
}

// gbDecimal is this file's byte figure: decimal GB, one decimal, the unit the
// engine contract itself reports ("bytes": 165151541665 reads 165.2). The
// card proper prints GiB (formatGiB) and keeps doing so; this explainer
// quotes the engine's own numbers in the engine's own unit so a reader can
// hold them against the /props dump without converting.
func gbDecimal(n int64) string {
	if n <= 0 {
		return unknown
	}
	return fmt.Sprintf("%.1f GB", float64(n)/1e9)
}
