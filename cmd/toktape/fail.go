package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
)

// One door out of every unsuccessful invocation (TTP-71, 2026-09-14).
//
// Most people who run toktape drive it from a coding agent, and an agent has
// no source to read: it has the exit code, whatever is on stdout and --help.
// A failure therefore owes three things at once — the exit code a wrapper
// branches on, a sentence a person reads, and, when --json was asked for, one
// parseable object on stdout so the caller has a single parse path instead of
// two. Before this, a failed run under --json left stdout empty.
//
// Three obligations discharged in three places is three places to forget one,
// so they are discharged here and nowhere else: every verb returns through
// cli.fail, and cmd/toktape/agent_test.go's TestFailuresLeaveThroughOneDoor
// fails the build if a `return exitUsage` ever appears outside this file.

// cli is one invocation's output contract: where the two streams go, and
// whether the caller asked to be answered in JSON.
//
// json is set by the verb once its flags are parsed, because that is when the
// answer is known. A flag set that failed to parse never gets that far, so
// there it is recovered from the raw arguments instead (jsonRequested).
type cli struct {
	stdout, stderr io.Writer
	json           bool
}

// failure is one unsuccessful outcome, whole.
type failure struct {
	// code is the process exit code and picks the name in exitCodeNames.
	code int
	// msg is the sentence, already carrying its "toktape: " prefix. It is
	// printed on stderr and copied verbatim into the JSON, so the two can
	// never say different things.
	msg string
	// hint is one sentence naming what to do next, or "". It is what turns
	// "connection refused" into an instruction.
	hint string
	// trailer is stderr-only text appended after a blank line — the usage
	// block, which belongs on a terminal and not in a JSON string.
	trailer string
}

// exitCodeNames is the closed set of outcomes, name by exit code. The names
// are the CLI's contract: they appear in --help, in the JSON error object and
// nowhere else, so the three cannot drift.
var exitCodeNames = map[int]string{
	exitOK:          "ok",
	exitUsage:       "usage",
	exitUnreachable: "unreachable",
	exitStreams:     "streams",
	exitUnavailable: "unavailable",
}

// errorReport is the JSON object a failed invocation prints under --json.
//
// It is deliberately not the success payload with a field added: success is
// tape.RunSummary exactly as it always was, and a reader tells the two apart
// by the presence of "error". toktape_version and schema_version are how a
// reader pins the shape it is parsing.
type errorReport struct {
	ToktapeVersion string      `json:"toktape_version"`
	SchemaVersion  int         `json:"schema_version"`
	Error          errorDetail `json:"error"`
}

// errorDetail is the failure itself.
type errorDetail struct {
	Code    string `json:"code"` // one of exitCodeNames
	Exit    int    `json:"exit"` // the process exit code, so a reader needs only one of the two
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// fail reports one unsuccessful outcome on every channel it is owed and
// returns the exit code, so a verb ends with `return c.fail(...)`.
func (c *cli) fail(f failure) int {
	msg := strings.TrimRight(f.msg, "\n")
	fmt.Fprintln(c.stderr, msg)
	if f.hint != "" {
		// The same arrow the end of a finished run uses for the commands it
		// offers, so "here is what to do next" looks the same either way.
		fmt.Fprintf(c.stderr, "→ %s\n", f.hint)
	}
	if f.trailer != "" {
		fmt.Fprintf(c.stderr, "\n%s", f.trailer)
	}
	if c.json {
		name := exitCodeNames[f.code]
		if name == "" {
			// An exit code with no name is a hole in the contract, not a
			// usage error: say so loudly rather than quietly mislabelling it
			// as one of the documented five. TestFailuresLeaveThroughOneDoor
			// fails the build before this can ship.
			name = fmt.Sprintf("exit-%d", f.code)
		}
		b, err := json.Marshal(errorReport{
			ToktapeVersion: version,
			SchemaVersion:  tape.SchemaVersion,
			Error:          errorDetail{Code: name, Exit: f.code, Message: msg, Hint: f.hint},
		})
		if err == nil {
			fmt.Fprintf(c.stdout, "%s\n", b)
		}
	}
	return f.code
}

// failf is the common shape: a code and a sentence.
func (c *cli) failf(code int, format string, a ...any) int {
	return c.fail(failure{code: code, msg: fmt.Sprintf(format, a...)})
}

// usagef is the commonest shape of all: the invocation or its inputs were
// rejected.
func (c *cli) usagef(format string, a ...any) int {
	return c.failf(exitUsage, format, a...)
}

// usageTextf rejects an invocation and prints the usage block after it, for
// the mistakes where the list of verbs or flags is the answer.
func (c *cli) usageTextf(text, format string, a ...any) int {
	return c.fail(failure{code: exitUsage, msg: fmt.Sprintf(format, a...), trailer: text})
}

// reportRecordError maps a failed run to its exit code and says what to do
// next about it.
//
// The recorder's own error already names the state, the URL and the cause, so
// the hint adds only the sentence it cannot know: which of the two shapes of
// "nothing answered" this was. Discovery found nothing on any candidate port,
// which means no server is running or it is somewhere else; an explicit --url
// that did not answer is a port or a server that has not finished starting.
func (c *cli) reportRecordError(err error, urlGiven bool) int {
	switch {
	case errors.Is(err, recorder.ErrUnreachable):
		hint := "start a llama-server, or name one with --url http://host:port"
		if urlGiven {
			hint = "check the host and port, or add --wait 30s to wait for a server that is still starting"
		}
		return c.fail(failure{
			code: exitUnreachable,
			msg:  fmt.Sprintf("toktape: %v", err),
			hint: hint,
		})
	case errors.Is(err, recorder.ErrAllStreamsFailed):
		return c.fail(failure{
			code: exitStreams,
			msg:  fmt.Sprintf("toktape: %v", err),
			hint: "the server was reachable but answered no stream; check its log and its slot count",
		})
	default:
		return c.failf(exitUsage, "toktape: %v", err)
	}
}

// badFlags answers a flag set that would not parse.
//
// The flag package writes its own complaint and its own usage block to the
// set's output; both are discarded (newFlagSet) and rewritten here instead, so
// that a rejection looks like every other rejection and its sentence is the
// one the JSON carries. `-help`, which the flag package answers itself, is not
// a rejection at all.
func (c *cli) badFlags(verb, usage string, args []string, err error) int {
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(c.stdout, usage)
		return exitOK
	}
	// The flags did not parse, so the parsed --json is not to be trusted;
	// the raw arguments are all there is.
	c.json = jsonRequested(args)
	return c.fail(failure{
		code:    exitUsage,
		msg:     fmt.Sprintf("toktape %s: %v", verb, err),
		trailer: usage,
	})
}

// jsonRequested reads --json out of the raw arguments.
//
// It is the fallback for the one moment the parsed flag is not available: a
// flag set that failed to parse. Everywhere else the parsed bool is used, so
// the only way to reach this is an invocation that was already being rejected,
// and the worst a false positive can do is add a JSON object to a failure that
// was going to fail anyway. Scanning stops at "--", which ends the flags.
func jsonRequested(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || name != "json" {
			continue
		}
		if !hasValue {
			return true
		}
		v, err := strconv.ParseBool(value)
		return err == nil && v
	}
	return false
}
