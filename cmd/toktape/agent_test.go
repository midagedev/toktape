package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// The CLI's contract as a machine reads it (TTP-71, 2026-09-14).
//
// Most people who run toktape drive it from a coding agent, which has no
// source to read: it has the exit code, stdout and --help. These tests pin the
// three things that makes usable — every terminal outcome carries a documented
// exit code, --json puts one parseable object on stdout whether the run
// succeeded or failed, and the codes are written down where a reader looks.

// wantExitCodes is the closed set. It is declared here, not imported from the
// code under test, so a code renamed on one side and not the other fails
// rather than agrees with itself.
var wantExitCodes = map[int]string{
	0: "ok",
	1: "usage",
	2: "unreachable",
	3: "streams",
	4: "unavailable",
}

var helpExitLine = regexp.MustCompile(`^\s{2}(\d)\s{2}([a-z]+)`)

// TestExitCodesAreNamedInHelp: the contract a wrapper branches on is in the
// usage text, with the same numbers and the same names the code uses.
func TestExitCodesAreNamedInHelp(t *testing.T) {
	_, block, ok := strings.Cut(usageText, "Exit codes:\n")
	if !ok {
		t.Fatal("--help has no \"Exit codes:\" section")
	}
	got := map[int]string{}
	for _, line := range strings.Split(block, "\n") {
		m := helpExitLine.FindStringSubmatch(line)
		if m == nil {
			if strings.TrimSpace(line) == "" {
				break
			}
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("exit code line %q: %v", line, err)
		}
		got[n] = m[2]
	}
	for code, name := range wantExitCodes {
		if got[code] != name {
			t.Errorf("--help names exit %d %q, want %q", code, got[code], name)
		}
	}
	for code, name := range got {
		if _, ok := wantExitCodes[code]; !ok {
			t.Errorf("--help names an exit code the contract does not have: %d %q", code, name)
		}
	}
}

// TestEveryExitCodeIsReachable drives one invocation per code and asserts both
// halves of the contract at once: the code itself, and that --json answered on
// stdout with the matching name.
//
// A code nobody can reach is a documented lie, and a failure that prints
// nothing on stdout under --json leaves an agent with two parse paths.
func TestEveryExitCodeIsReachable(t *testing.T) {
	hermetic(t)
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close() // nothing listens there now

	// A server that attaches and then fails every stream.
	brokenStreams := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/props" {
			w.Header().Set("Server", "llama.cpp")
			_, _ = w.Write([]byte(cliProps))
			return
		}
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	t.Cleanup(brokenStreams.Close)

	tapePath := writeTape(t, t.TempDir(), &tape.RunSummary{ID: "20260913-101500-qwen3", Concurrency: 1})

	cases := []struct {
		name string
		code int
		args []string
		// json says the verb takes --json, so the failure must also be one
		// JSON object on stdout. `render` does not take it (there is no JSON
		// product to print on success), so exit 4 is pinned by its code and
		// its sentence alone.
		json bool
		// noPATH strips PATH so a tool the output needs cannot be found.
		noPATH bool
	}{
		{"ok", exitOK, []string{"version"}, true, false},
		{"usage", exitUsage, []string{"frobnicate"}, true, false},
		{"unreachable", exitUnreachable, []string{"--url", deadURL, "--wait", "0", "--out", t.TempDir()}, true, false},
		{"streams", exitStreams, []string{"--url", brokenStreams.URL, "--n-predict", "8", "--out", t.TempDir()}, true, false},
		{"unavailable", exitUnavailable, []string{"render", tapePath, "--mp4", filepath.Join(t.TempDir(), "x.mp4")}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.noPATH {
				t.Setenv("PATH", "")
			}
			args := tc.args
			if tc.json {
				args = append(append([]string(nil), args...), "--json")
			}
			code, stdout, stderr := exec(t, args...)
			if code != tc.code {
				t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.code, stdout, stderr)
			}
			if code == exitOK {
				return
			}
			if stderr == "" {
				t.Error("a failure said nothing on stderr")
			}
			if !tc.json {
				return
			}
			var got struct {
				ToktapeVersion string `json:"toktape_version"`
				SchemaVersion  int    `json:"schema_version"`
				Error          struct {
					Code    string `json:"code"`
					Exit    int    `json:"exit"`
					Message string `json:"message"`
					Hint    string `json:"hint"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("--json failure is not one JSON object on stdout: %v\nstdout:\n%q", err, stdout)
			}
			if got.Error.Code != wantExitCodes[tc.code] {
				t.Errorf("error.code = %q, want %q", got.Error.Code, wantExitCodes[tc.code])
			}
			if got.Error.Exit != tc.code {
				t.Errorf("error.exit = %d, want %d", got.Error.Exit, tc.code)
			}
			if strings.TrimSpace(got.Error.Message) == "" {
				t.Error("error.message is empty")
			}
			if got.ToktapeVersion != version {
				t.Errorf("toktape_version = %q, want %q", got.ToktapeVersion, version)
			}
			if got.SchemaVersion == 0 {
				t.Error("schema_version is missing")
			}
		})
	}
}

// TestJSONSuccessIsTheRunSummary: the success payload is unchanged — the
// run summary itself, no envelope — so a reader written against it keeps
// working, and it is told apart from a failure by the absence of "error".
func TestJSONSuccessIsTheRunSummary(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	code, stdout, stderr := exec(t, "--url", srv.URL, "--out", t.TempDir(), "--n-predict", "16", "--json")
	if code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("--json success is not one JSON object on stdout: %v\n%s", err, stdout)
	}
	if _, ok := got["error"]; ok {
		t.Error("a successful run carried an \"error\" key")
	}
	for _, key := range []string{"id", "toktape_version", "server", "model", "timings"} {
		if _, ok := got[key]; !ok {
			t.Errorf("the run summary has no %q", key)
		}
	}
}

// failureExits are the returns that must not appear outside the one place that
// turns a failure into all three of an exit code, a stderr sentence and a JSON
// object. This is the structural half of TTP-71: a new failure path added in
// any verb cannot forget one of the three, because the compiler-visible way to
// return a non-zero code is that helper.
var failureExits = map[string]bool{
	"exitUsage": true, "exitUnreachable": true, "exitStreams": true, "exitUnavailable": true,
}

// failOwner is the file allowed to return them.
const failOwner = "fail.go"

func TestFailuresLeaveThroughOneDoor(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if filepath.Base(name) == failOwner {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				ret, ok := n.(*ast.ReturnStmt)
				if !ok || len(ret.Results) != 1 {
					return true
				}
				id, ok := ret.Results[0].(*ast.Ident)
				if !ok || !failureExits[id.Name] {
					return true
				}
				t.Errorf("%s: returns %s directly; a failure must go through %s so it also gets a stderr sentence and a --json object",
					fset.Position(ret.Pos()), id.Name, failOwner)
				return true
			})
		}
	}
}

// TestExitCodeNamesAreTheClosedSet: the names the JSON error object carries
// are the names --help lists and nothing else. Three places say the contract —
// the help text, exitCodeNames and this table — and all three must agree, or
// a reader that branched on one of them is branching on a fiction.
func TestExitCodeNamesAreTheClosedSet(t *testing.T) {
	if len(exitCodeNames) != len(wantExitCodes) {
		t.Errorf("exitCodeNames has %d entries, the contract has %d: %v",
			len(exitCodeNames), len(wantExitCodes), exitCodeNames)
	}
	for code, name := range wantExitCodes {
		if exitCodeNames[code] != name {
			t.Errorf("exitCodeNames[%d] = %q, want %q", code, exitCodeNames[code], name)
		}
	}
}

// TestHelpAgentsTopic: the topic exists, is reachable by the spelling --help
// points at, and says the things a caller cannot find out any other way.
func TestHelpAgentsTopic(t *testing.T) {
	if !strings.Contains(usageText, "toktape help agents") {
		t.Error("--help does not point at the agents topic")
	}
	code, stdout, stderr := exec(t, "help", "agents")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{
		"--wait",         // the ten-minute block
		"toktape record", // the default verb generates load
		"schema_version", // how to pin the shape
		"reasoning",      // nothing here yet, but the ram note lives with it
		"--ram-gbs",      // the figures the machine cannot read
		`"prompt"`,       // the prompts file
		"unreachable",    // the error codes
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("`help agents` does not mention %q", want)
		}
	}

	// A topic that does not exist is a mistake worth naming, not generic help.
	code, _, stderr = exec(t, "help", "nosuchtopic")
	if code != exitUsage {
		t.Errorf("an unknown help topic exited %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "agents") {
		t.Errorf("the refusal does not list the topics:\n%s", stderr)
	}

	// A verb name is answered with the usage text rather than refused.
	if code, out, _ := exec(t, "help", "card"); code != exitOK || !strings.Contains(out, "Usage:") {
		t.Errorf("`help card` exited %d without the usage text", code)
	}
}

// TestHelpStaysScannable: the usage text is read whole by whoever runs --help,
// so detail that grows without bound belongs behind `toktape help <topic>`.
// The bound is a judgement, not a measurement; it exists so that growing past
// it is a decision somebody makes rather than one that happens.
//
// It was 70 until the Examples section (2026-09-14), which an agent copies
// from and which therefore has to be in the text everybody reads. Raising it
// was the decision; the verbose halves of --wait, --prompts, --grid and the
// --ram pair were trimmed in the same change to pay part of it.
//
// It was 82 until --for (TTP-76, 2026-09-14). That flag is how long a run is,
// which is the question every first-time reader has, and it cost six lines:
// the two-flag matrix in the flag list and a worked example that aims a clip.
// Raising it a second time was a deliberate decision and not a measurement —
// 96 leaves four lines of headroom on purpose, so the next thing to grow has
// room to land while somebody decides what it displaces.
func TestHelpStaysScannable(t *testing.T) {
	const maxLines = 96
	if n := strings.Count(usageText, "\n"); n > maxLines {
		t.Errorf("--help is %d lines, over the %d-line budget; move detail into a help topic", n, maxLines)
	}
}

// TestHelpAgentsNamesRealFields: every dotted path `help agents` offers to a
// reader exists in the JSON a run actually prints.
//
// The topic is a promise to a machine that will jq against it, and a field
// renamed in internal/tape would turn that promise into a null with no
// warning — the exact class of error this whole track exists to close. The
// summary is marshalled and the paths walked, so the check is against the
// bytes a caller receives rather than against a struct tag read by eye.
//
// 2026-09-14: marshalled through card.JSON rather than encoding/json, because
// that is what `--json` prints and the two are not the same object — card.JSON
// adds "caveats", the derived list that answers whether the headline may be
// quoted at all, which no struct tag in internal/tape carries. Checking the
// topic against the schema struct was checking it against the wrong bytes;
// this is the same assertion aimed at the ones a caller receives.
func TestHelpAgentsNamesRealFields(t *testing.T) {
	// A zero summary marshals every non-omitempty field; the ones that are
	// omitempty are given a value so they appear.
	s := tape.RunSummary{
		Placement: tape.PlacementSummary{Source: "gguf"},
		Sampling:  tape.SamplingSummary{Endpoint: tape.EndpointChat},
		Warnings:  []string{"cold run"},
	}
	s.Model.Name, s.Model.Quant, s.Model.Params = "Qwen3.5-35B-A3B", "Q4_K_M", 1
	s.Aggregate.StreamsFailed = 1
	// Both halves of the limit are omitempty, so a cut run is modelled here:
	// a budget that was asked for and a clock that spent it.
	s.Limit.For, s.Limit.CutAt = 20*time.Second, 21*time.Second
	b, err := card.JSON(&s)
	if err != nil {
		t.Fatalf("marshal a run summary: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}

	topic := agentsTopic()
	// Every dotted path the topic prints in its field list. They are spelled
	// out rather than scraped so that a path silently dropped from the topic
	// is as visible as one that is wrong.
	paths := []string{
		"id", "toktape_version", "concurrency",
		"model.name", "model.file_name", "model.quant", "model.file_bytes", "model.params",
		"timings.predicted_per_second", "timings.prompt_per_second", "timings.ttft_ms",
		"timings.client_predicted_per_second",
		"aggregate.aggregate_predicted_per_second", "aggregate.streams", "aggregate.streams_failed",
		// What was allowed to end the run, and what did (TTP-76): a reader
		// that cannot see limit.cut_at cannot tell a run the clock stopped
		// short from one that generated everything it was going to.
		"limit.for", "limit.cut_at",
		"placement", "sampling",
		// The two qualification fields, and the order they are named in is
		// the order a reader should read them: caveats is the complete list
		// and warnings is the recorder's own text inside it (TTP-74).
		"caveats", "warnings",
	}
	for _, p := range paths {
		if !strings.Contains(topic, p) {
			t.Errorf("`help agents` no longer names %q; the list and the topic have drifted", p)
			continue
		}
		node := any(doc)
		for _, seg := range strings.Split(p, ".") {
			obj, ok := node.(map[string]any)
			if !ok {
				t.Errorf("%s: %q is not an object", p, seg)
				node = nil
				break
			}
			node, ok = obj[seg]
			if !ok {
				t.Errorf("`help agents` promises %q, which the run summary does not have", p)
				node = nil
				break
			}
		}
	}

	// The two shape claims the topic makes about files rather than fields.
	if !strings.Contains(topic, `stores under "summary"`) {
		t.Error("the topic no longer says where the .tape keeps the summary")
	}
	tp, err := json.Marshal(&tape.Tape{Schema: tape.SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tp), `"summary"`) {
		t.Error("a tape does not carry the summary under \"summary\"")
	}
}

// TestEveryExitConstIsNamed closes the hole the AST guard cannot see: a new
// `const exitSomething` returned through cli.fail would get an exit code and a
// sentence but no name, and the JSON would carry "exit-7".
func TestEveryExitConstIsNamed(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}
	seen := map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				spec, ok := n.(*ast.ValueSpec)
				if !ok {
					return true
				}
				for _, name := range spec.Names {
					if strings.HasPrefix(name.Name, "exit") && name.Name != "exitCodeNames" {
						seen[name.Name] = true
					}
				}
				return true
			})
		}
	}
	if len(seen) == 0 {
		t.Fatal("no exit constants found; the scan is broken, not the code")
	}
	named := map[string]bool{}
	for code := range exitCodeNames {
		_ = code
	}
	// Map each constant back through its value: the names are what the help
	// and the JSON use, so the check is that every constant's value has one.
	for name, code := range map[string]int{
		"exitOK": exitOK, "exitUsage": exitUsage, "exitUnreachable": exitUnreachable,
		"exitStreams": exitStreams, "exitUnavailable": exitUnavailable,
	} {
		named[name] = true
		if exitCodeNames[code] == "" {
			t.Errorf("%s = %d has no name in exitCodeNames", name, code)
		}
	}
	for name := range seen {
		if !named[name] {
			t.Errorf("%s is an exit constant this contract does not know; add it to exitCodeNames, to --help and to the table in this file", name)
		}
	}
}

// exampleLine matches a command line in the Examples section: two spaces of
// indent, then the word toktape.
var exampleLine = regexp.MustCompile(`^  (toktape(?: .*)?)$`)

// TestExamplesInHelpParse: every invocation printed under Examples is one the
// CLI actually accepts (2026-09-14).
//
// An agent copies an example rather than assembling a flag list, so a stale
// one is worse than none — it is a command the tool taught the reader and then
// rejects. Each line is run with its verb's flags parsed and its arguments
// checked, but never executed: the record examples would generate load on
// whatever llama-server this box happens to run, which is the one thing the
// suite must not do.
func TestExamplesInHelpParse(t *testing.T) {
	_, block, ok := strings.Cut(usageText, "Examples:\n")
	if !ok {
		t.Fatal("--help has no \"Examples:\" section")
	}
	block, _, _ = strings.Cut(block, "\nExit codes:")

	var lines []string
	for _, line := range strings.Split(block, "\n") {
		if m := exampleLine.FindStringSubmatch(line); m != nil {
			lines = append(lines, m[1])
		}
	}
	if len(lines) < 5 {
		t.Fatalf("the Examples section has %d invocations, want the five worked ones: %q", len(lines), lines)
	}

	for _, line := range lines {
		t.Run(line, func(t *testing.T) {
			args := strings.Fields(line)
			if args[0] != "toktape" {
				t.Fatalf("not an invocation: %q", line)
			}
			args = args[1:]
			// A shell redirection is the shell's, not the CLI's.
			if i := indexOf(args, ">"); i >= 0 {
				args = args[:i]
			}
			verb, rest := splitVerb(args)
			fs, err := exampleFlagSet(verb)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if _, err := parseArgs(fs, rest); err != nil {
				t.Fatalf("the example does not parse: %v", err)
			}
		})
	}
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

// exampleFlagSet is the flag set the named verb really parses with.
//
// It calls the verb's own declare function rather than repeating the list:
// a copy here is exactly what goes stale, and a stale copy would let this
// test pass over an example the CLI rejects.
func exampleFlagSet(verb string) (*flag.FlagSet, error) {
	fs := newFlagSet(verb)
	switch verb {
	case "record":
		declareRecordFlags(fs)
	case "card":
		declareCardFlags(fs)
	case "render":
		declareRenderFlags(fs)
	case "ls":
		fs.String("out", defaultRunsDir(), "")
	default:
		return nil, fmt.Errorf("the Examples section uses verb %q; teach this test how it parses", verb)
	}
	return fs, nil
}
