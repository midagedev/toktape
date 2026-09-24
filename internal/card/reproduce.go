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
	// A generic OpenAI-compatible server's engine is the user's claim, not
	// an argv toktape ever saw (TTP-99): it travels as a comment, never as
	// a flag the next run would re-ask for.
	if s.Server.Kind == tape.ServerOpenAI && strings.TrimSpace(s.Server.EngineClaim) != "" {
		fmt.Fprintf(&b, "    # engine claim: %s (record with --engine %q)\n\n",
			strings.TrimSpace(s.Server.EngineClaim), strings.TrimSpace(s.Server.EngineClaim))
	}

	// A chat tape carries what the person typed and what came back, so the
	// card names that instead of inviting readers to pass the file around.
	if s.ID != "" && s.IsChat() {
		fmt.Fprintf(&b, "Tape: `%s` (it holds the conversation's text)\n", s.ID+tape.Ext)
	} else if s.ID != "" {
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
//
// A chat is rebuilt as `toktape chat`: the conversation is not replayable
// from flags, but the attachment and the named cap are, and every record-only
// flag (--sessions, --for) is one chat refuses (2026-09-24).
func recordCommand(s *tape.RunSummary) string {
	parts := []string{"toktape"}
	if IsChat(s) {
		parts = append(parts, "chat")
	}
	if u := strings.TrimSpace(s.Server.URL); u != "" {
		parts = append(parts, "--url", u)
	}
	// A generic OpenAI-compatible run re-attaches only in its own mode
	// (TTP-99): without the flag the next invocation would try /props first,
	// which is a detour that still lands, but the command should say what
	// the run was.
	if s.Server.Kind == tape.ServerOpenAI {
		parts = append(parts, "--engine-kind", "openai")
	}
	// Concurrency 1 is the default; printing "--sessions 1" would suggest the
	// run was configured when it was not.
	//
	// The block is a command to paste into the CURRENT binary, so the stream
	// count is spelled --sessions for every tape, including one recorded when
	// the flag was -n (2026-09-14). Printing the old spelling for an old tape
	// would ask today's binary for that many TOKENS per stream: a different
	// run, and precisely the accident the rename exists to prevent. The run
	// reproduced is the same one — Concurrency's meaning never changed, only
	// the flag's name. The cap stays spelled --n-predict, never -n, because
	// the long form reads the same in every version.
	if IsChat(s) {
		if s.Limit.MaxTokensNamed && s.Limit.MaxTokens > 0 {
			parts = append(parts, "--n-predict", strconv.Itoa(s.Limit.MaxTokens))
		}
		return strings.Join(parts, " ")
	}
	if s.Concurrency > 1 {
		parts = append(parts, "--sessions", strconv.Itoa(s.Concurrency))
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
// where the same function omits "--sessions 1" for being one. The asymmetry is chosen:
// a concurrency of 1 is what `toktape` means in every version, while
// recorder.DefaultFor is a constant that may move, and a card outlives the
// binary that printed it. The flag pins the run; the omission does not.
//
// The matrix's "both" row is the one that needs the tape's help. A run recorded
// with `--for 10s --n-predict 300` ends at whichever comes first, and a command
// rebuilt from the clock alone would leave the cap at whatever the reading
// version defaults to — a different run on a fast box. Limit.MaxTokensNamed
// says whether the cap was the user's, so it is re-asked for exactly then and
// never for the runaway guard (lead, 2026-09-14). A tape recorded before that
// field reads as not named and prints the clock alone, as it did.
func limitArgs(s *tape.RunSummary) []string {
	switch {
	case s.Limit.For > 0:
		args := []string{"--for", s.Limit.For.String()}
		if s.Limit.MaxTokensNamed && s.Limit.MaxTokens > 0 {
			args = append(args, "--n-predict", strconv.Itoa(s.Limit.MaxTokens))
		}
		return args
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
