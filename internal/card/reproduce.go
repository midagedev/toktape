package card

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// Reproduce renders the collapsible provenance block that closes the Markdown
// card.
//
// The launch research (docs/research/04-launch-channels.md, Risk 3) says the
// comment that kills a benchmark post is always one of three: "what was your
// batch", "was the prompt cached", "paste your command". The card's figures
// answer the first two. This block answers the third, and adds the one thing a
// screenshot can never carry — the tape, which turns "trust me" into a file
// anyone can replay.
//
// It is folded into a <details> because it is evidence, not the headline: a
// reader who believes the numbers should not have to scroll past an argv to
// reach the next comment. Nothing in it is reconstructed. When the recorder
// never read the server's command line the block says so, because an argv
// rebuilt out of parsed flags would invent an order and a set of arguments
// nobody observed.
func Reproduce(s *tape.RunSummary) string {
	if s == nil {
		s = &tape.RunSummary{}
	}
	var b strings.Builder
	b.WriteString("<details><summary>Reproduce</summary>\n\n")

	if argv := strings.Join(s.Server.Args, " "); argv != "" {
		fmt.Fprintf(&b, "%s\n\n", serverArgvHeading(s.Server))
		fmt.Fprintf(&b, "    %s\n\n", argv)
	} else {
		b.WriteString("Server command line not observed (toktape had no /proc view of the server process).\n\n")
	}

	b.WriteString("Recorded with:\n\n")
	fmt.Fprintf(&b, "    %s\n\n", recordCommand(s))

	if s.ID != "" {
		fmt.Fprintf(&b, "Tape: `%s` (attach it and anyone can `toktape play` it)\n", s.ID+tape.Ext)
	}
	b.WriteString("</details>\n")
	return b.String()
}

// serverArgvHeading names where the command line came from. The PID is part of
// the claim: it says the argv was read off a live process on the machine that
// ran the model, not typed from memory.
func serverArgvHeading(srv tape.ServerInfo) string {
	if srv.PID > 0 {
		return fmt.Sprintf("Server (as seen from /proc/%d/cmdline):", srv.PID)
	}
	return "Server command line:"
}

// recordCommand rebuilds the toktape invocation that would produce this run.
//
// Every option on it is a value the summary carries: the URL the recorder
// attached to, the number of streams it opened, and what was allowed to end
// the generation. An option whose value was not observed is left off rather
// than guessed, so the worst case is a bare "toktape" — which is also the
// command that produced a zero-config run, and so is never wrong.
func recordCommand(s *tape.RunSummary) string {
	parts := []string{"toktape"}
	if u := strings.TrimSpace(s.Server.URL); u != "" {
		parts = append(parts, "--url", u)
	}
	// Concurrency 1 is the default; printing "-n 1" would suggest the run was
	// configured when it was not.
	if s.Concurrency > 1 {
		parts = append(parts, "-n", strconv.Itoa(s.Concurrency))
	}
	return strings.Join(append(parts, limitArgs(s)...), " ")
}

// limitArgs is how the rebuilt command re-asks for what ended the generation.
//
// It mirrors the table internal/recorder.Options.limit resolves, because that
// table is what the printed flag will be read against when somebody pastes it:
//
//   - a wall-clock budget was in force → "--for D". D is the budget, never
//     Limit.CutAt. A cut run is one whose budget was SPENT, and re-asking for
//     the same budget is the reproduction of the intent; re-asking for the
//     22 s the floor actually took would aim the next run at this box's
//     slowness. The same reasoning makes CutAt > For nothing to print here.
//   - no clock → "--n-predict N", the cap the requests carried. Naming a token
//     count is an answer on the same axis as --for, so it also turns the clock
//     off, which is what this run had.
//
// The count the server predicted is deliberately NOT the source of either
// figure. Timings.PredictedN is the representative single stream — Requests[0]
// at Concurrency 1 and the per-stream MEAN above it (see tape.RunSummary) — so
// on a run whose streams produced 200 and 300 tokens it is 250, a number no
// stream produced and no flag ever set.
//
// A default run therefore prints "--for 20s" even though 20 s is the default,
// where the same function omits "-n 1" for being one. The asymmetry is chosen:
// a concurrency of 1 is what `toktape` means in every version, while
// recorder.DefaultFor is a constant that may move, and a card outlives the
// binary that printed it. The flag pins the run; the omission does not.
//
// The gap this cannot close is the matrix's "both" row. A run recorded with
// `--for 10s --n-predict 300` ends at whichever comes first, and the command
// below re-asks only for the clock, leaving the cap at whatever this version
// defaults to — a different run on a fast box. Telling the two apart needs a
// tape that says whether MaxTokens was named or defaulted, which the schema
// does not record; see the report.
func limitArgs(s *tape.RunSummary) []string {
	switch {
	case s.Limit.For > 0:
		return []string{"--for", s.Limit.For.String()}
	case s.Limit.MaxTokens > 0:
		return []string{"--n-predict", strconv.Itoa(s.Limit.MaxTokens)}
	case s.Timings.PredictedN > 0:
		// A tape older than the limit field (TTP-76) carries no cap at all,
		// so this is the count the server reported predicting — the honest
		// best such a tape allows. For a run that stopped on the cap the two
		// are the same number; a run that hit an end-of-sequence token
		// stopped early, and re-running with this value asks for what this
		// run produced.
		return []string{"--n-predict", strconv.Itoa(s.Timings.PredictedN)}
	}
	return nil
}
