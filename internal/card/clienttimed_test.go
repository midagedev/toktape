package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// The generic OpenAI-compatible card (TTP-99, 2026-09-19): a client-timed run
// names its clock beside the decode headline, an uncounted one prints "?"
// where the rate would be, the engine line carries the claim, and the flags
// block is gone.

// openaiSummary is a clean run re-stamped as a client-timed OpenAI run with
// a usage count: the rendering tests below read fields, not the clock that
// filled them.
func openaiSummary(t *testing.T) *tape.RunSummary {
	t.Helper()
	s := clean(t)
	s.Server.Kind = tape.ServerOpenAI
	s.Server.Build, s.Server.Commit = "", ""
	s.Server.Flags = tape.ServerFlags{}
	s.Server.EngineClaim = "vLLM 0.11"
	s.Timings.Source = "client"
	s.Timings.PredictedNSource = "usage"
	s.Timings.PromptPerSecond = 0
	s.Timings.ClientAgreesWithServer = false
	return s
}

func TestDecodePartsClientTimed(t *testing.T) {
	s := openaiSummary(t)
	parts := decodeParts(s)
	if len(parts) < 2 {
		t.Fatalf("decodeParts = %q, want at least [rate client-timed]", parts)
	}
	// FAIL-first: without the label the parts were [rate bandwidth], so a
	// client-timed card read exactly like a server-timed one.
	if parts[1] != "client-timed" {
		t.Errorf("decodeParts = %q, want the rate first and client-timed second", parts)
	}
	if !strings.HasSuffix(parts[0], "tok/s") {
		t.Errorf("decodeParts[0] = %q, want the usage-counted rate", parts[0])
	}
	// The label sits after the rate and before the bandwidth, as its own
	// part: a wrapped card must not separate the figure from its condition.
	for i, p := range parts {
		if strings.HasPrefix(p, "≈") && i < 2 {
			t.Errorf("decodeParts = %q, want client-timed before the bandwidth", parts)
		}
	}
}

func TestDecodePartsChunksPrintsBareQuestion(t *testing.T) {
	s := openaiSummary(t)
	s.Timings.PredictedNSource = "chunks"
	s.Timings.PredictedN = 6
	s.Timings.PredictedPerSecond, s.Timings.ClientPredictedPerSecond = 0, 0
	parts := decodeParts(s)
	if len(parts) == 0 || parts[0] != "?" {
		t.Errorf("decodeParts = %q, want bare ? in place of the rate (not \"? tok/s\")", parts)
	}
	found := false
	for _, p := range parts {
		if p == "client-timed" {
			found = true
		}
		if strings.Contains(p, "tok/s") {
			t.Errorf("decodeParts = %q, want no rate at all on an uncounted run", parts)
		}
	}
	if !found {
		t.Errorf("decodeParts = %q, want the client-timed label beside the ?", parts)
	}
}

func TestClientTimedCaveatSentences(t *testing.T) {
	s := openaiSummary(t)
	ex := ExplainCaveats(s)
	if !strings.Contains(ex, CodeClientTimed) {
		t.Errorf("ExplainCaveats lacks %s:\n%s", CodeClientTimed, ex)
	}
	// The exact sentences, through the same path the card prints. The usage
	// run raises client_timed; the uncounted run raises both.
	var timed, uncounted string
	for _, c := range Caveats(s) {
		if c.Code == CodeClientTimed {
			timed = c.Text
		}
	}
	s.Timings.PredictedNSource = "chunks"
	s.Timings.PredictedN = 6
	ex = ExplainCaveats(s)
	if !strings.Contains(ex, CodeTokensUncounted) {
		t.Errorf("ExplainCaveats lacks %s:\n%s", CodeTokensUncounted, ex)
	}
	for _, c := range Caveats(s) {
		if c.Code == CodeTokensUncounted {
			uncounted = c.Text
		}
	}
	if timed != "client-timed: the server reported no timings, so every rate here is the recorder's clock over the stream — not the engine's own count; compare only with other client-timed runs" {
		t.Errorf("client_timed text = %q", timed)
	}
	if !strings.HasPrefix(uncounted, "tokens uncounted: the server sent no usage figure, so the ") ||
		!strings.HasSuffix(uncounted, " stream chunks are not a token count and no decode rate is printed") {
		t.Errorf("tokens_uncounted text = %q", uncounted)
	}
}

func TestClientTimedNeverDisagrees(t *testing.T) {
	s := openaiSummary(t)
	// Both rate fields set, agrees false — the shape that fires
	// client_disagrees on a server-timed run. One clock cannot disagree.
	s.Timings.PredictedPerSecond, s.Timings.ClientPredictedPerSecond = 48.2, 48.2
	for _, c := range Caveats(s) {
		if c.Code == CodeClientDisagrees {
			t.Errorf("a client-timed run raises client_disagrees: %+v", c)
		}
	}
	if line := lineFor(t, ExplainCaveats(s), CodeClientDisagrees); !strings.Contains(line, " no ") {
		t.Errorf("client_disagrees listed %q, want it unfired", line)
	}
}

func TestLlamaCPPFlagsKinds(t *testing.T) {
	for _, tc := range []struct {
		kind tape.ServerKind
		want bool
	}{
		// FAIL-first: the pre-change predicate (!SelfDeclared) was true for
		// ServerOpenAI, so an openai run printed the llama.cpp flag row.
		{tape.ServerOpenAI, false},
		{tape.ServerLlamaCPP, true},
		{tape.ServerIKLlama, true},
		{tape.ServerExLlamaV3, false},
		{tape.ServerUnknown, true},
	} {
		if got := LlamaCPPFlags(tape.ServerInfo{Kind: tc.kind}); got != tc.want {
			t.Errorf("LlamaCPPFlags(%q) = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

func TestOpenAIEngineLineAndNoFlags(t *testing.T) {
	s := openaiSummary(t)
	text := Text(s)
	if !strings.Contains(text, "openai · claim: vLLM 0.11") {
		t.Errorf("card engine line lacks the claim:\n%s", text)
	}
	if strings.Contains(text, "FLAGS") {
		t.Errorf("card prints a FLAGS block for a server with no argv:\n%s", text)
	}
	s.Server.EngineClaim = ""
	if text := Text(s); !strings.Contains(text, "openai · ?") {
		t.Errorf("card engine line lacks `openai · ?` without a claim:\n%s", text)
	}
}

func TestBenchClientTimedFootnote(t *testing.T) {
	s := openaiSummary(t)
	table := LlamaBenchTable(s)
	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	var tg string
	for _, ln := range lines {
		if strings.Contains(ln, "| tg") {
			tg = ln
		}
	}
	// FAIL-first: the tg cell read like any server-timed figure.
	if !strings.Contains(tg, " * |") && !strings.HasSuffix(strings.TrimRight(tg, " "), "* |") && !strings.Contains(tg, "*") {
		t.Errorf("tg row carries no star:\n%s", table)
	}
	if !strings.Contains(table, "* client-timed") {
		t.Errorf("bench table lacks the footnote:\n%s", table)
	}
	plain := LlamaBenchTable(clean(t))
	if strings.Contains(plain, "*") {
		t.Errorf("a server-timed table carries a star:\n%s", plain)
	}
}

func TestReproduceOpenAI(t *testing.T) {
	s := openaiSummary(t)
	s.Server.URL = "http://127.0.0.1:8000"
	r := Reproduce(s)
	if !strings.Contains(r, "--engine-kind openai") {
		t.Errorf("Reproduce lacks the mode flag:\n%s", r)
	}
	if !strings.Contains(r, "# engine claim: vLLM 0.11") {
		t.Errorf("Reproduce lacks the claim as a comment:\n%s", r)
	}
	if strings.Contains(r, "--engine vLLM") {
		t.Errorf("Reproduce passes the claim as an argv it never saw:\n%s", r)
	}
}

func TestOpenAINoBandwidthLine(t *testing.T) {
	s := openaiSummary(t)
	s.Timings.EffectiveBandwidthBytesPerSec = 0
	// With no bytes-per-token there is no line: assert absence, never
	// "0 GB/s".
	if bw := bandwidthString(s); bw != "" {
		t.Errorf("bandwidthString = %q, want empty", bw)
	}
	if text := Text(s); strings.Contains(text, "GB/s") {
		t.Errorf("card prints a bandwidth nobody measured:\n%s", text)
	}
}
