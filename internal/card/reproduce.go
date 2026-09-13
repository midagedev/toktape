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
// attached to, the number of streams it opened, and the tokens it asked for.
// An option whose value was not observed is left off rather than guessed, so
// the worst case is a bare "toktape" — which is also the command that produced
// a zero-config run, and so is never wrong.
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
	// The cap that was sent is not in the schema, so this is the count the
	// server reported predicting. For a run that stopped on the cap the two
	// are the same number; a run that hit an end-of-sequence token stopped
	// early, and re-running with this value asks for what this run produced.
	if n := s.Timings.PredictedN; n > 0 {
		parts = append(parts, "--n-predict", strconv.Itoa(n))
	}
	return strings.Join(parts, " ")
}
