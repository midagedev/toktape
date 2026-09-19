package card

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// unknown is what the card prints for anything that was not observed. Repo
// rule: an unobserved value is the empty string or 0 and prints as "?".
// Never print a default you did not observe.
const unknown = "?"

// gib is one gibibyte; every byte figure on the card is printed in GiB.
const gib = 1 << 30

// formatGiB renders a byte count as gibibytes with one decimal. Zero is
// unknown and prints "?" — never "0.0 GiB", which would be a lie about a rig
// we failed to read.
func formatGiB(b int64) string {
	if b <= 0 {
		return unknown
	}
	return fmt.Sprintf("%.1f GiB", float64(b)/gib)
}

// formatGiBNum is formatGiB without the unit, for use inside a "a/b GiB" pair.
func formatGiBNum(b int64) string {
	if b <= 0 {
		return unknown
	}
	return strconv.FormatFloat(float64(b)/gib, 'f', 1, 64)
}

// formatRate renders a tok/s figure.
//
// One decimal below 100, integer at or above it. docs/toktape-spec.ko.md §4
// and the track contract print "68.4 tok/s", "12.1 tok/s", "96.8 tok/s" and
// "2450 tok/s"; that set is only consistent with a 100 threshold, so the
// printed samples win over the prose "1 decimal (< 10)" in the same spec.
func formatRate(v float64) string {
	if v <= 0 {
		return unknown
	}
	if v < 100 {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(v, 'f', 0, 64)
}

// formatRateUnit is formatRate with " tok/s" appended (and no unit on "?").
func formatRateUnit(v float64) string {
	r := formatRate(v)
	if r == unknown {
		return unknown + " tok/s"
	}
	return r + " tok/s"
}

// formatGBs renders a bandwidth in decimal GB/s: one decimal below 10,
// integer above (the spec prints "≈ 91 GB/s"). Bandwidth is conventionally
// decimal, so this divides by 1e9, not by 2^30.
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
// unit printed once after the second number: "91 GB/s", "112–206 GB/s"
// (lead, 2026-09-16 — concurrent streams share a forward pass, so the Decode
// row's clause is a range). Bounds that render identically print as one
// figure: a range whose ends round together says nothing a figure does not.
func formatGBsRange(low, high int64) string {
	lowS, highS := formatGBs(low), formatGBs(high)
	if lowS == highS {
		return highS
	}
	return strings.TrimSuffix(lowS, " GB/s") + "–" + highS
}

// formatMs renders a millisecond figure as an integer.
func formatMs(v float64) string {
	if v <= 0 {
		return unknown
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + " ms"
}

// formatProbeMs renders the probe fit's fixed cost, where zero is a reading
// rather than an unknown: the intercept is observed, and a server whose
// fixed cost measures 0 ms is the interesting case, not a missing one
// (TTP-137, 2026-09-19). queueWaitPart holds the same rule for its observed
// wait; formatMs's "?" belongs to figures this run may simply not have.
func formatProbeMs(v float64) string {
	return strconv.FormatFloat(v, 'f', 0, 64) + " ms"
}

// formatDuration renders a wall-clock budget the way the operator typed it:
// "20s", "1m30s". The value is rounded to a tenth of a second first, because
// the only durations the card prints are a `--for` budget and the moment the
// clock actually cut (tape.LimitSummary), and the second of those is a
// measured nanosecond count that would otherwise print as "21.412345678s".
// Zero is unknown and prints "?" — a run with no clock has no budget, not a
// zero-length one.
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return unknown
	}
	return d.Round(100 * time.Millisecond).String()
}

// formatPct renders a 0..1 ratio as an integer percentage.
func formatPct(ratio float64) string {
	if ratio <= 0 {
		return "0%"
	}
	return strconv.FormatFloat(ratio*100, 'f', 0, 64) + "%"
}

// formatPctRange renders one ratio, or the bounds between two, with the sign
// printed once after the second number: "9%", "15–27%" — the ratio form of
// formatGBsRange, over OfPeakRange's pair.
func formatPctRange(low, high float64) string {
	lowS, highS := formatPct(low), formatPct(high)
	if lowS == highS {
		return highS
	}
	return strings.TrimSuffix(lowS, "%") + "–" + highS
}

// formatInt renders a count, or "?" when it is zero (unobserved).
func formatInt(n int) string {
	if n <= 0 {
		return unknown
	}
	return strconv.Itoa(n)
}

// formatFloat1 renders a float with one decimal, without treating zero as
// unknown. Used for counters whose zero is a real, observed measurement (page
// faults per token); the caller decides whether the counter was observed.
func formatFloat1(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// formatParamsB renders a parameter count the way llama-bench does: billions
// with two decimals ("35.00 B").
func formatParamsB(n int64) string {
	if n <= 0 {
		return unknown
	}
	return strconv.FormatFloat(float64(n)/1e9, 'f', 2, 64) + " B"
}

// formatFileGiB renders a file size the way llama-bench does: gibibytes with
// two decimals ("19.83 GiB").
func formatFileGiB(b int64) string {
	if b <= 0 {
		return unknown
	}
	return strconv.FormatFloat(float64(b)/gib, 'f', 2, 64) + " GiB"
}

// orUnknown returns s, or "?" when s is empty.
func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return unknown
	}
	return s
}

// serverDefault is what one of the always-printed flags reads as when the
// recorder read the server's argv and the flag was not in it.
//
// "?" and "default" are different claims. "?" is "nobody looked"; an argv that
// was read and does not carry -fa is a reading, and what it says is that the
// server's own default is in effect. A card that prints "?" for both makes the
// reader ask the question the card exists to answer (launch research
// docs/research/04-launch-channels.md, Risk 3: "was flash attention on?").
// The value itself is still never guessed — the card names whose default it is
// by naming the build on the ENGINE line, and prints no number.
const serverDefault = "default"

// argvObserved reports whether the recorder read the server's command line.
//
// It is Args and not PID because the two can disagree: the recorder finds the
// PID first and reads /proc/<pid>/cmdline second (internal/recorder/record.go
// collectProcess), and a PID whose argv could not be read leaves every flag
// genuinely unobserved. Args is non-empty exactly when ServerFlags was parsed.
func argvObserved(srv tape.ServerInfo) bool {
	return len(srv.Args) > 0
}

// flagValue renders one of the always-printed flags: its value when it was
// given, "default" when an argv that was read did not carry it, and "?" when
// no argv was read at all.
//
// internal/card/png/content.go carries a character-for-character copy: the two
// renderings of one summary must never disagree about what is known.
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
// did or when the server's slot count was never read.
//
// A run of N streams on a server with fewer than N slots is partly serialised:
// the server admits as many as it has slots and the rest queue. Its per-stream
// rate is then not the per-stream rate of a run that fitted, and two cards
// compared side by side look like a difference in the machine when the
// difference was in the request. The card says which it was.
//
// NSlots is /props total_slots. Zero means /props was never read (or the
// server did not report it), and a queue derived from an unread slot count
// would be exactly the invented figure the repo rule forbids.
func queuedString(s *tape.RunSummary) string {
	slots := s.Server.NSlots
	if slots <= 0 || s.Concurrency <= slots {
		return ""
	}
	return fmt.Sprintf("%d slots, %d queued", slots, s.Concurrency-slots)
}

// yesNo renders a bool the way the card's labels read.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// formatSeconds is a duration in whole tenths of a second: the PNG's own
// spelling, which this package needs since RaggedClause moved here.
func formatSeconds(ms float64) string {
	if ms <= 0 {
		return unknown
	}
	return strconv.FormatFloat(ms/1000, 'f', 1, 64) + " s"
}
