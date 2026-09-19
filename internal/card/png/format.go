package png

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// The formatters below are deliberate duplicates of the unexported ones in
// internal/card/format.go. The text card and the PNG card must make the same
// claim from the same summary — "68.4 tok/s" in one and "68.4" in the other,
// "?" in both when a field was never observed — so the rules are copied rather
// than re-invented. They are unexported there; de-duplicating them means
// exporting them from internal/card, which is another track's file. See the
// PNG track report, "anything the lead must do".

// unknown is what the card prints for anything that was not observed. Repo
// rule: an unobserved value is the empty string or 0 and prints as "?". Never
// print a default you did not observe.
const unknown = "?"

// serverDefault is what one of the five always-printed flags reads as when an
// argv the recorder actually read did not carry it: "?" means nobody looked,
// "default" means the reading says the server's own default is in effect.
// internal/card/format.go carries the derivation.
const serverDefault = "default"

// gib is one gibibyte.
const gib = 1 << 30

func formatGiB(b int64) string {
	if b <= 0 {
		return unknown
	}
	return fmt.Sprintf("%.1f GiB", float64(b)/gib)
}

func formatGiBNum(b int64) string {
	if b <= 0 {
		return unknown
	}
	return strconv.FormatFloat(float64(b)/gib, 'f', 1, 64)
}

// formatRate renders a tok/s figure: one decimal below 100, integer at or
// above it (internal/card/format.go carries the derivation).
func formatRate(v float64) string {
	if v <= 0 {
		return unknown
	}
	if v < 100 {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(v, 'f', 0, 64)
}

func formatRateUnit(v float64) string {
	r := formatRate(v)
	if r == unknown {
		return unknown + " tok/s"
	}
	return r + " tok/s"
}

func formatGBs(bytesPerSec int64) string {
	if bytesPerSec <= 0 {
		return unknown
	}
	v := float64(bytesPerSec) / 1e9
	if v < 10 {
		return strconv.FormatFloat(v, 'f', 1, 64) + " GB/s"
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + " GB/s"
}

// formatGBsRange renders one bandwidth, or the bounds between two, with the
// unit printed once after the second number: "91 GB/s", "112–206 GB/s".
// Character-for-character the text card's rule (internal/card/format.go) —
// the two renderings must bound the same run the same way.
func formatGBsRange(low, high int64) string {
	lowS, highS := formatGBs(low), formatGBs(high)
	if lowS == highS {
		return highS
	}
	return strings.TrimSuffix(lowS, " GB/s") + "–" + highS
}

func formatMs(v float64) string {
	if v <= 0 {
		return unknown
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + " ms"
}

// formatProbeMs mirrors internal/card.formatProbeMs (this file keeps the
// card's formatters by design): the probe fit's fixed cost, where zero is a
// reading — the intercept is observed, and a server with no measurable fixed
// cost is the interesting case — not the "?" a figure nobody measured
// would print (TTP-137, 2026-09-19).
func formatProbeMs(v float64) string {
	return strconv.FormatFloat(v, 'f', 0, 64) + " ms"
}

// formatSeconds renders a wall-clock duration given in milliseconds.
func formatSeconds(ms float64) string {
	if ms <= 0 {
		return unknown
	}
	return strconv.FormatFloat(ms/1000, 'f', 1, 64) + " s"
}

func formatPct(ratio float64) string {
	if ratio <= 0 {
		return "0%"
	}
	return strconv.FormatFloat(ratio*100, 'f', 0, 64) + "%"
}

// formatPctRange renders one ratio, or the bounds between two, with the sign
// printed once after the second number: "10%", "15–27%". The text card's rule
// (internal/card/format.go).
func formatPctRange(low, high float64) string {
	lowS, highS := formatPct(low), formatPct(high)
	if lowS == highS {
		return highS
	}
	return strings.TrimSuffix(lowS, "%") + "–" + highS
}

func formatInt(n int) string {
	if n <= 0 {
		return unknown
	}
	return strconv.Itoa(n)
}

// formatFloat1 renders a counter whose zero is a real measurement.
func formatFloat1(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// formatParamsB renders a parameter count in billions the way a model card
// names it: "35B", "671B", "3.0B". One decimal below ten (the difference
// between a 3B and a 3.8B is the whole argument at that size), an integer at
// or above it, and never a trailing ".00".
func formatParamsB(n int64) string {
	if n <= 0 {
		return unknown
	}
	v := float64(n) / 1e9
	if v < 10 {
		return strconv.FormatFloat(v, 'f', 1, 64) + "B"
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + "B"
}

func formatFileGiB(b int64) string {
	if b <= 0 {
		return unknown
	}
	return strconv.FormatFloat(float64(b)/gib, 'f', 2, 64) + " GiB"
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return unknown
	}
	return s
}

// argvObserved reports whether the recorder read the server's command line.
// Args and not PID: a PID whose /proc/<pid>/cmdline could not be read leaves
// every flag genuinely unobserved (internal/card/format.go).
func argvObserved(srv tape.ServerInfo) bool {
	return len(srv.Args) > 0
}

// flagValue renders one of the five always-printed flags: its value when it
// was given, "default" when a read argv did not carry it, "?" when no argv was
// read. Character-for-character the text card's rule (internal/card/format.go)
// — the two renderings of one summary must not disagree about what is known.
func flagValue(v string, argvWasRead bool) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	if argvWasRead {
		return serverDefault
	}
	return unknown
}

// queuedString names the streams that had to wait for a slot, or "" when none
// did or when the server's slot count was never read. Character-for-character
// the text card's rule (internal/card/format.go carries the derivation).
func queuedString(s *tape.RunSummary) string {
	slots := s.Server.NSlots
	if slots <= 0 || s.Concurrency <= slots {
		return ""
	}
	return fmt.Sprintf("%d slots, %d queued", slots, s.Concurrency-slots)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// joinParts joins the non-empty parts with sep, returning "?" when none of
// them was observed. Sections of the footer grid are built this way so a cell
// never shows a stray separator around a missing field.
func joinParts(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return unknown
	}
	return strings.Join(kept, sep)
}

// omitUnknown drops a value that turned out to be unobserved, for places where
// a bare "?" would sit in a list with nothing to say which field it is. A
// labelled "TTFT ?" is informative; a lone "?" after a separator is noise.
func omitUnknown(s string) string {
	if s == unknown {
		return ""
	}
	return s
}

// upper is the small-caps emulation's case transform.
func upper(s string) string { return strings.ToUpper(s) }
