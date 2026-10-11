package tape

import "strings"

// Where a host's single memory pool was learned from. Only UnifiedName is
// derived today.
const (
	// UnifiedName: the host's own part names say it is unified — a GB10 in
	// Host.GPUs or an Apple Silicon chip in Host.CPU. It is a statement about
	// the part, never a measurement of its bandwidth or its capacity.
	UnifiedName = "name"
	// UnifiedStated is reserved. The later sources are an operator statement
	// and the engine's own --list-devices report; neither is wired now, and no
	// code may return this value until one is.
	UnifiedStated = "stated"
)

// Unified says whether CPU and GPU share one memory pool on this host, and
// where that was learned from; "" means it is not known to be.
//
// It is a pure function of fields a tape already carries and is evaluated when
// a card is drawn, never stamped at record time (TTP-178, 2026-10-11): a Spark
// tape recorded before this existed, and any tape redrawn by `toktape
// reindex`, must read as unified without being recorded again.
//
// The pool's size is HostInfo.RAMBytes — MemTotal on a Spark, hw.memsize on a
// Mac. There is no second size field, so the bytes are counted once by
// construction. The GB10 stays in Host.GPUs with VRAMBytes 0 (unknown): nvidia-smi
// answers [N/A] for it, and the hub's GPU filter reads the name from there.
func Unified(h HostInfo) string {
	for _, g := range h.GPUs {
		if unifiedKey(g.Name) == "gb10" {
			return UnifiedName
		}
	}
	if strings.HasPrefix(h.CPU, "Apple M") {
		return UnifiedName
	}
	return ""
}

// unifiedBrandWords are the vendor words dropped from a GPU name before it is
// matched, the way internal/gpu's table key does; this package cannot import it.
var unifiedBrandWords = map[string]bool{"nvidia": true, "geforce": true, "tesla": true, "quadro": true}

// unifiedKey lower-cases a reported GPU name, drops its vendor words and
// punctuation, and joins what is left: "NVIDIA GB10" and "GB10" are "gb10".
func unifiedKey(name string) string {
	fields := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	var b strings.Builder
	for _, f := range fields {
		if !unifiedBrandWords[f] {
			b.WriteString(f)
		}
	}
	return b.String()
}
