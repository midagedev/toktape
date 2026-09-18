package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// `toktape help <topic>` (TTP-71, 2026-09-14).
//
// The usage text is read whole by whoever runs --help, so it stays scannable
// and carries only what every caller needs — the verbs, the flags and the exit
// codes. The detail one particular caller needs goes behind a topic. There is
// one topic, and it is the one this tool did not have: what a script or a
// coding agent has to know to drive toktape without reading the source.

// agentsTopic is `toktape help agents`.
//
// Everything in it is checkable from outside the process, because that is all
// its reader has: exit codes, the shape on stdout, and the two defaults that
// surprise an automated caller (a verb that generates load, and a ten-minute
// block).
func agentsTopic() string {
	return `toktape for scripts and coding agents

The contract
  Every invocation ends in one of five exit codes, listed in ` + "`toktape --help`" + `.
  Branch on the code, never on the message; the messages are for people.
  With -o json (or -o jsonl), stdout carries exactly one JSON object either
  way, so there is one parse path and not two.

Careful: the default verb generates load
  ` + "`toktape`" + ` with no verb is ` + "`toktape record`" + `: it attaches to a running
  llama-server and sends it real requests. To look without recording, use
  ` + "`toktape --help`" + `, ` + "`toktape ls`" + ` or ` + "`toktape log`" + `.

Careful: a run generates for twenty seconds by default
  --for is the run's wall-clock budget and defaults to 20s, measured from the
  first request. Name --n-predict instead and the clock is off, the token cap
  is the only limit, and the run takes as long as that many tokens take on
  that machine. --for 0 turns the clock off without naming a cap.

  A clock will not cut a stream out of its own measurement: while one is in
  force the run waits until every live stream has ` + strconv.Itoa(tape.MinCutTokens) + ` tokens or has stopped on
  its own, so --for 5s on a slow box ends later than it says. There is no flag
  for that floor; the tape records it as limit.min_tokens.

Careful: -n is tokens, not streams
  -n is --n-predict: tokens per stream, which is what llama-bench's -n means.
  The number of streams sent at once is --sessions N, and it has no short
  form; --concurrency no longer exists. --sessions stops at 8 unless
  --max-sessions names the same number (--sessions 16 --max-sessions 16), and
  a run asking for more sessions than the server has slots is refused,
  because the streams past the slots measure queue wait, not concurrency.
  Both refusals are exit 1 with a hint that names the flag to use.

Careful: one invocation can block for ten minutes
  --wait defaults to 10m, because a server loading a 450 GB model is the case
  worth waiting for. That is longer than most harnesses' command timeout. A
  caller with a timeout of its own should pass --wait 0 (fail fast if the
  server is not ready) or a short budget such as --wait 30s.

-o json on success
  ` + "`record -o json`" + ` and ` + "`card <tape> -o json`" + ` print the run summary — the
  same object the .tape file stores under "summary". ` + "`-o jsonl`" + ` prints the
  same object on one line and nothing else on it, so runs appended to one
  file stay one object per line. It has no "error" key; that is how a reader
  tells success from failure. Pin the shape with "toktape_version". The
  fields most callers want:

    id                            the run id, and the .tape file's basename
    toktape_version               the build that recorded it
    concurrency                   streams sent at once (--sessions)
    model.name                    the model's own name, when it declared one
    model.file_name               the GGUF that was loaded
    model.quant                   the exact sub-type: Q4_K_M, UD-Q4_K_M
    model.file_bytes              weights on disk
    model.params                  parameter count, when it could be read
    timings.predicted_per_second  decode tok/s, one representative stream,
                                  as the server reported it
    timings.prompt_per_second     prefill tok/s
    timings.ttft_ms               time to first token
    timings.client_predicted_per_second
                                  the client's own decode figure — the check
                                  on the server's, never the one to quote
    aggregate.aggregate_predicted_per_second
                                  decode tok/s across every stream
    aggregate.streams             how many were sent
    aggregate.streams_failed      how many did not answer
    limit.for                     the wall-clock budget the run was recorded
                                  with, 0 when it had none
    limit.cut_at                  when the clock actually ended it, 0 when
                                  nothing was cut. Non-zero means the run was
                                  stopped short and the figures cover only
                                  what was generated by then; larger than
                                  limit.for means the 64-token floor held the
                                  cut back and the run ran long
    placement                     what sits on GPU and what sits in RAM
    sampling                      temperature, thinking, endpoint
    caveats                       everything that qualifies the figures above
    warnings                      the recorder's own free text, already inside
                                  caveats under the "recorded" code

  The names are llama.cpp's own, not renamed: predicted is decode, prompt is
  prefill. Server figures are the record and client figures are the check; a
  disagreement between them is a fact about the run, not a number to pick
  between.

Is this number quotable: read caveats, not warnings
  ` + "`caveats`" + ` is the derived, complete list — a stable ` + "`code`" + `, a ` + "`severity`" + ` and a
  sentence per qualification, most serious first — and it is the one field
  that answers whether the headline may be quoted: empty means yes.
  ` + "`warnings`" + ` is the recorder's own free text ("cold run: 1.4 major faults
  per token", "pid not found, no /proc view") and is a SUBSET of it, riding
  through under the code "recorded", so a reader of warnings alone sees the
  smaller list. The severities:

    figure  the headline does not mean what it looks like. Quoting the
            number without the sentence beside it is wrong
    run     the figures are what they say, but the run was not one clean
            measurement, so two cards are not comparable on it alone
    view    a view of the machine is missing, so part of the card prints "?"

  Branch on ` + "`code`" + ` and ` + "`severity`" + `; the sentence is for people. The codes today:
  streams_failed, answer_cut, short_generation, cold_cache,
  short_prompt_for_prefill, client_disagrees_with_server, recorded,
  machine_contended, conditions_changed, run_cut_by_clock, no_proc_view. The
  list is open — a newer toktape may add one, and a reader that branched on a
  code it knows keeps working.

Why a card does not say what you expected
  ` + "`card <tape> --explain`" + ` prints, on stderr, every qualification check with
  its verdict and the reading behind it — INCLUDING the checks that did not
  fire — and then every figure the bandwidth clauses are built from, including
  the ones that made a clause report nothing. It is an addition to whatever
  rendering was asked for, so ` + "`-o json --explain`" + ` still leaves exactly one
  object on stdout.

The ledger formats
  ` + "`log -o json`" + ` is a different shape: one JSON array of ledger rows, one
  object per recorded run, every value a string. ` + "`log -o jsonl`" + ` is the same
  objects one per line. ` + "`-o csv`" + `, ` + "`-o tsv`" + ` and ` + "`-o sql`" + ` are the same columns;
  sql creates the "runs" table if it is missing and inserts one row per run,
  with unknown as NULL, so ` + "`toktape log -o sql | sqlite3 runs.db`" + ` is a
  database. On record and card, csv, tsv and sql are that run's one row.
  Each verb refuses a format it does not take and names the ones it does.

-o json on failure
  {"toktape_version":"` + version + `","schema_version":` + strconv.Itoa(tape.SchemaVersion) + `,
   "error":{"code":"unreachable","exit":2,"message":"toktape: ...",
            "hint":"..."}}

  "code" is one of: usage, unreachable, streams, unavailable. It is the name
  of "exit", so a reader needs only one of the two. "hint" is absent when
  there is nothing useful to say.

Unknown is never guessed
  A figure toktape did not observe is "" or 0 in the JSON and prints as "?" on
  the card. It is never filled in with a plausible default, so a zero means
  "not measured", not "measured as zero".

A prompts file (--prompts FILE), one JSON object per line
  {"name":"sql","prompt":"Write a SQL query that ..."}
  {"name":"chat","messages":[{"role":"user","content":"hi"}],"max_tokens":512}

  Each line is one round of --sessions streams, run in order into one tape.
  "name" is optional and labels the round. Exactly one of "prompt" and
  "messages" is required. An unknown key is an error, not ignored.

Asking a reasoning model for less thinking
  --no-think sends the template switch that turns thinking off;
  --think-budget N caps it at N tokens. Both are chat-only: on
  --endpoint completion the prompt is sent verbatim and there is no template
  to ask. On the wire the budget is llama.cpp's reasoning_budget_tokens — the
  similarly named reasoning_budget is parsed and then ignored, so do not send
  it by hand with --param.

Figures the machine cannot read
  On Linux the memory speed and channel count live in the DMI tables, which
  are root-only, so a partially offloaded run has no host bandwidth ceiling
  and the card prints no "of peak" ratio. An operator who knows the number can
  state it: --ram-gbs N, or --ram-gbs-measured N for a STREAM result, or
  --ram-speed DDR5-5200 --ram-channels 8. The tape records which of the three
  it was, and nothing is ever assumed in their absence.

A tape that gets posted
  A tape is made to be shared, and the hostname is the one field in it that
  names a place rather than a measurement. --host-label TEXT stores TEXT
  instead of what /proc says, so the file that gets replayed never had the
  name; --host-label "" stores none, and the card then leaves the machine out
  of its ENGINE line rather than printing "?". The substitution happens once,
  where the host line is assembled, so every field that carries the name gets
  the label and not just the one on the card.

  The tape says which it was — hostname_source is "observed" or "labelled" —
  because a label is not an observation, and two runs both labelled
  "workstation" are not evidence they ran on one machine. toktape card -o json
  shows it. Nothing else in the tape carries the name: --url has to be a
  loopback address, so server.url cannot hold one either.
`
}

// helpTopics are the words `toktape help <word>` answers with something other
// than the usage text.
var helpTopics = map[string]func() string{
	"agents": agentsTopic,
}

// runHelp prints the usage text, or one topic.
//
// A verb name is accepted and answered with the usage text, because `toktape
// help card` is a reasonable thing to type and the flags it wants are in
// there. Anything else is a mistake worth naming: a topic silently answered
// with generic help reads, to the caller that asked for it, exactly like a
// topic that exists.
func runHelp(c *cli, args []string) int {
	topic := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			topic = a
			break
		}
	}
	switch {
	case topic == "":
		fmt.Fprint(c.stdout, usageText)
	case helpTopics[topic] != nil:
		fmt.Fprint(c.stdout, helpTopics[topic]())
	case verbs[topic]:
		fmt.Fprint(c.stdout, usageFor(topic))
	default:
		return c.fail(failure{
			code: exitUsage,
			msg:  fmt.Sprintf("toktape help: no topic %q", topic),
			hint: "topics: " + strings.Join(topicNames(), ", ") + " — or a verb name for the usage text",
		})
	}
	return exitOK
}

// usageFor is the one place a verb's name becomes its usage text, so `help
// <verb>`, `<verb> --help` and a rejected `<verb>` invocation print the same
// bytes and cannot drift (TTP-92: render had a block of its own that only
// `help render` reached, while `render --help` printed the top-level text).
// Most verbs are documented in the main text; render has a block of its own
// because the main text gives it one line.
func usageFor(verb string) string {
	if verb == "render" {
		return renderUsage
	}
	return usageText
}

// topicNames is the topic list, in a stable order.
func topicNames() []string {
	out := make([]string, 0, len(helpTopics))
	for name := range helpTopics {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
