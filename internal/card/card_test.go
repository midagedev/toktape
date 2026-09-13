package card

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (regenerate with: go test ./internal/card -run . -update): %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// unknownsSummary is a run where nothing was observed: the card must still
// print every argument-settling field, as "?", and must not claim "0 GiB".
func unknownsSummary() *tape.RunSummary {
	return &tape.RunSummary{Concurrency: 1}
}

// 2026-09-13 TTP-28: the example and example-concurrent goldens were
// re-baselined. The fixture is now a dense R1 Distill Llama 70B Q4_K_M fully offloaded
// to two 3090s, which is the rig the launch post is about; the derivation is
// the comment block in example.go. The other four fixtures are built in this
// file and did not move.
func TestTextGolden(t *testing.T) {
	cases := []struct {
		name string
		s    *tape.RunSummary
	}{
		{"example", Example()},
		{"example-concurrent", ExampleConcurrent()},
		{"unknowns", unknownsSummary()},
		// The two width stress fixtures are goldens as well, so a reviewer can
		// read what a Hangul rig and an oversized -ot actually render as.
		{"hangul", hangulSummary()},
		{"long-ot", longOTSummary()},
		// More streams than slots: the Streams block must show the queue.
		{"queued", queuedSummary()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			golden(t, c.name+".txt", []byte(Text(c.s)))
		})
	}
}

// 2026-09-13 TTP-28: re-baselined with TestTextGolden, and for the same
// reason — the same two fixtures, rendered as JSON.
func TestJSONGolden(t *testing.T) {
	cases := []struct {
		name string
		s    *tape.RunSummary
	}{
		{"example", Example()},
		{"example-concurrent", ExampleConcurrent()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := JSON(c.s)
			if err != nil {
				t.Fatalf("JSON: %v", err)
			}
			golden(t, c.name+".json", b)
		})
	}
}

// hangulSummary stresses the width layer: an East Asian model file name and
// hostname, plus a never-loaded class the card has to name.
func hangulSummary() *tape.RunSummary {
	s := Example()
	s.Model.FileName = "한글-모델-테스트-Q4_K_M.gguf"
	s.Model.Path = "/모델/한글-모델-테스트-Q4_K_M.gguf"
	s.Model.Name = "한글 모델 테스트"
	s.Host.Hostname = "워크스테이션"
	s.Server.Host = "워크스테이션"
	s.Host.CPU = "AMD 라이젠 9 7950X 16코어 프로세서"
	s.Placement.NeverLoadedBytes = 90823884800 // 84.6 GiB
	s.Placement.Devices = append(s.Placement.Devices, tape.DevicePlacement{
		Device:  tape.DeviceCPU,
		Bytes:   90823884800,
		Classes: map[tape.TensorClass]int64{tape.ClassNGram: 90823884800},
		Layers:  "ngram",
	})
	s.Contention = tape.ContentionInfo{
		Contended:     true,
		Reasons:       []string{"loadavg 12.3 > cores 8", "2 other GPU compute processes", "GPU1 클럭 스로틀 관측됨"},
		LoadAvg1:      12.3,
		OtherGPUProcs: 2,
	}
	s.Warnings = []string{"한글 경고 메시지: nvml 을 찾지 못해 nvidia-smi 로 VRAM 을 읽었다"}
	return s
}

func longOTSummary() *tape.RunSummary {
	s := Example()
	s.Server.Flags.OverrideTens = []string{
		`blk\.(1[2-9]|[2-9][0-9])\.ffn_(gate|up|down)_exps\.weight=CPU`,
		`blk\.([0-9]|1[0-1])\.ffn_(gate|up|down)_exps\.weight=CUDA0`,
		`blk\.[0-9]+\.attn_(q|k|v|output)\.weight=CUDA1`,
	}
	s.Server.Flags.Other = []string{"--no-mmap", "--parallel 8", "--cont-batching"}
	return s
}

// TestBorderColumn is the width gate from docs/toktape-spec.ko.md §8.3: the
// right border must sit on the same column on every line of every card.
func TestBorderColumn(t *testing.T) {
	cases := map[string]*tape.RunSummary{
		"example":            Example(),
		"example-concurrent": ExampleConcurrent(),
		"unknowns":           unknownsSummary(),
		"hangul":             hangulSummary(),
		"long-ot":            longOTSummary(),
		"queued":             queuedSummary(),
		"nil":                nil,
	}
	left := "┌│├└"
	right := "┐│┤┘"
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			out := Text(s)
			if strings.ContainsRune(out, 0x1b) {
				t.Fatalf("Text emitted an ANSI escape")
			}
			lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
			if len(lines) < 10 {
				t.Fatalf("card has only %d lines", len(lines))
			}
			for i, l := range lines {
				if w := Width(l); w != CardWidth {
					t.Errorf("line %d width = %d, want %d: %q", i+1, w, CardWidth, l)
				}
				r := []rune(l)
				if !strings.ContainsRune(left, r[0]) {
					t.Errorf("line %d starts with %q, want one of %q", i+1, r[0], left)
				}
				if !strings.ContainsRune(right, r[len(r)-1]) {
					t.Errorf("line %d ends with %q, want one of %q", i+1, r[len(r)-1], right)
				}
			}
		})
	}
}

// The five argument-starters are what end the comment threads, so no amount of
// -ot or Other flags may push them out of the card.
func TestFlagsAlwaysPrintTheFive(t *testing.T) {
	for name, s := range map[string]*tape.RunSummary{
		"long-ot":  longOTSummary(),
		"unknowns": unknownsSummary(),
	} {
		t.Run(name, func(t *testing.T) {
			out := Text(s)
			for _, want := range []string{"-fa ", "-b ", "-ub ", "-ctk ", "-ctv "} {
				if !strings.Contains(out, want) {
					t.Errorf("card is missing %q", want)
				}
			}
		})
	}
	out := Text(unknownsSummary())
	for _, want := range []string{"-fa ?", "-b ?", "-ub ?", "-ctk ?", "-ctv ?"} {
		if !strings.Contains(out, want) {
			t.Errorf("unknown flags card is missing %q", want)
		}
	}
	// The -ot group is the only part allowed to be cut, and it must be marked
	// as cut rather than dropped.
	long := Text(longOTSummary())
	if !strings.Contains(long, "…") {
		t.Error("a long -ot group should be truncated with an ellipsis")
	}
	if !strings.Contains(long, "-ot ") {
		t.Error("a long -ot group must still be shown, cut, not dropped")
	}
	// A -ot group that fits on a line of its own is never cut.
	if out := Text(Example()); strings.Contains(out, "…") {
		t.Errorf("the example's single -ot pattern fits and must not be truncated:\n%s", out)
	}
}

// Nothing may print a figure that was not observed as a plausible-looking zero.
func TestUnknownsNeverLie(t *testing.T) {
	out := Text(unknownsSummary())
	for _, bad := range []string{"0.0 GiB", "0 tok/s", "0.0 tok/s", "0 ms", "warm", "cold", "cached"} {
		if strings.Contains(out, bad) {
			t.Errorf("all-unknown card contains %q:\n%s", bad, out)
		}
	}
	for _, want := range []string{"MODEL", "ENGINE", "RIG", "Decode", "Prefill", "Context", "Prefix cache", "MEMORY", "HOST", "FLAGS", "?"} {
		if !strings.Contains(out, want) {
			t.Errorf("all-unknown card is missing %q", want)
		}
	}
}

// Lesson 2: below tape.MinDecodeTokens the rate exists but is not a decode rate.
func TestSampleLabelReplacesDecode(t *testing.T) {
	s := Example()
	s.Timings.PredictedN = tape.MinDecodeTokens - 1
	s.Timings.DecodeLabel = "sample"
	out := Text(s)
	if !strings.Contains(out, "Sample ") {
		t.Errorf("short generation should be labelled Sample:\n%s", out)
	}
	if strings.Contains(out, "Decode ") {
		t.Errorf("short generation must not be labelled Decode:\n%s", out)
	}
}

func TestConcurrentAddsStreamsLine(t *testing.T) {
	if strings.Contains(Text(Example()), "Streams") {
		t.Error("a single-stream run must not print the Streams line")
	}
	out := Text(ExampleConcurrent())
	for _, want := range []string{"Streams", "8 × 9.1 tok/s = 72.9 tok/s aggregate", "TTFT p50 810 ms p95 1050 ms", "slots busy max 8"} {
		if !strings.Contains(out, want) {
			t.Errorf("concurrent card is missing %q:\n%s", want, out)
		}
	}
}

func TestVersionFallsBackToPackageVar(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })

	Version = "1.2.3"
	if got := versionString(&tape.RunSummary{}); got != "toktape v1.2.3" {
		t.Errorf("versionString = %q, want %q", got, "toktape v1.2.3")
	}
	Version = "dev"
	if got := versionString(&tape.RunSummary{}); got != "toktape dev" {
		t.Errorf("versionString = %q, want %q", got, "toktape dev")
	}
	if got := versionString(&tape.RunSummary{ToktapeVersion: "0.1.0"}); got != "toktape v0.1.0" {
		t.Errorf("summary version should win, got %q", got)
	}
}

func TestMarkdownWrapsTextAndTable(t *testing.T) {
	md := Markdown(Example())
	if !strings.HasPrefix(md, "```text\n") {
		t.Error("Markdown must open a text fence")
	}
	if !strings.Contains(md, "```\n\n| model |") {
		t.Error("Markdown must close the fence and follow with the llama-bench table")
	}
	if !strings.Contains(md, Text(Example())) {
		t.Error("Markdown must contain the text card verbatim")
	}
}

func TestJSONIsSchemaOrdered(t *testing.T) {
	b, err := JSON(Example())
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	// Struct order is schema order. Read the top-level keys in document order
	// (a plain substring search would trip over the nested server.host key).
	dec := json.NewDecoder(bytes.NewReader(b))
	if _, err := dec.Token(); err != nil {
		t.Fatalf("open object: %v", err)
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatalf("read key: %v", err)
		}
		keys = append(keys, k.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("skip value: %v", err)
		}
	}
	want := []string{
		"id", "toktape_version", "started_at", "finished_at", "server", "model",
		"host", "placement", "memory", "concurrency", "timings", "aggregate",
		"cache", "contention", "template", "gpus_at_end",
	}
	if !slices.Equal(keys, want) {
		t.Errorf("top-level key order =\n%v\nwant\n%v", keys, want)
	}
	if _, err := JSON(nil); err != nil {
		t.Errorf("JSON(nil): %v", err)
	}
}

// TestUnknownNeverPrintsAsNo: "no" and "? GB" are claims about a machine, and
// a run that read neither the host load nor the RAM size has not made them.
// Both renderings of a summary must agree, so the rule is the PNG card's.
func TestUnknownNeverPrintsAsNo(t *testing.T) {
	t.Run("contended", func(t *testing.T) {
		cases := []struct {
			name string
			ci   tape.ContentionInfo
			want string
		}{
			{"nothing was read", tape.ContentionInfo{}, "contended: ?"},
			{"a quiet host was read", tape.ContentionInfo{LoadAvg1: 0.4}, "contended: no"},
			{"a foreign GPU job", tape.ContentionInfo{OtherGPUProcs: 1, Contended: true,
				Reasons: []string{"1 other GPU compute process"}}, "contended: yes"},
			{"a reason without a flag is still a reading",
				tape.ContentionInfo{Reasons: []string{"loadavg 9.0 > 8.0"}}, "contended: no"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				out := Text(&tape.RunSummary{Concurrency: 1, Contention: tc.ci})
				if !strings.Contains(out, tc.want) {
					t.Errorf("card does not contain %q:\n%s", tc.want, out)
				}
			})
		}
	})

	t.Run("ram", func(t *testing.T) {
		unknownRig := Text(&tape.RunSummary{Concurrency: 1})
		if strings.Contains(unknownRig, "? GB") {
			t.Errorf("an unmeasured RAM size printed as a quantity:\n%s", unknownRig)
		}
		known := Text(&tape.RunSummary{Concurrency: 1, Host: tape.HostInfo{RAMBytes: 128 * gib}})
		if !strings.Contains(known, "128 GB") {
			t.Errorf("a measured RAM size did not print:\n%s", known)
		}
	})
}

// TestUnsetFlagPrintsDefaultOnceTheArgvWasRead is the other half of the "?"
// rule. "?" means unobserved; a flag that is absent from an argv the recorder
// actually read is observed-as-absent, and what is in effect is the server's
// own default. Printing "?" there invites the comment thread the card exists
// to end ("was flash attention on?" — it was not set, and the card can say so).
func TestUnsetFlagPrintsDefaultOnceTheArgvWasRead(t *testing.T) {
	// The argv was read: -ngl and -b were given, the other four were not.
	read := &tape.RunSummary{Concurrency: 1, Server: tape.ServerInfo{
		PID:   4242,
		Args:  []string{"/usr/local/bin/llama-server", "-ngl", "99", "-b", "2048"},
		Flags: tape.ServerFlags{NGL: "99", Batch: "2048"},
	}}
	out := Text(read)
	for _, want := range []string{"-ngl 99", "-b 2048", "-fa default", "-ub default", "-ctk default", "-ctv default"} {
		if !strings.Contains(out, want) {
			t.Errorf("card with a read argv is missing %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"-fa ?", "-b ?", "-ub ?", "-ctk ?", "-ctv ?"} {
		if strings.Contains(out, bad) {
			t.Errorf("a flag the recorder observed as absent printed as %q:\n%s", bad, out)
		}
	}

	// The PID was found but /proc/<pid>/cmdline could not be read: nothing
	// about the flags was observed, so every one of the five stays "?".
	unread := &tape.RunSummary{Concurrency: 1, Server: tape.ServerInfo{PID: 4242}}
	out = Text(unread)
	for _, want := range []string{"-fa ?", "-b ?", "-ub ?", "-ctk ?", "-ctv ?"} {
		if !strings.Contains(out, want) {
			t.Errorf("card with an unreadable argv is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "default") {
		t.Errorf("a server default was claimed from an argv nobody read:\n%s", out)
	}

	// -ngl is not one of the five: it is omitted when unset, not defaulted.
	if strings.Contains(Text(read), "-ngl default") {
		t.Error("-ngl is printed only when observed, never as a default")
	}

	// The worst case is a server started with nothing but a model path, where
	// all five read "default". The row wraps; it is never cut, because the
	// five are exactly the fields the card exists to settle.
	bare := Text(&tape.RunSummary{Concurrency: 1, Server: tape.ServerInfo{
		PID:  4242,
		Args: []string{"/usr/local/bin/llama-server", "-m", "/models/model.gguf"},
	}})
	for _, want := range []string{"-fa default", "-b default", "-ub default", "-ctk default", "-ctv default"} {
		if !strings.Contains(bare, want) {
			t.Errorf("the all-default card is missing %q:\n%s", want, bare)
		}
	}
	if strings.Contains(bare, "…") {
		t.Errorf("the all-default flag row was truncated:\n%s", bare)
	}
}

// The llama-bench table renders the same fact as the FLAGS row and must not
// disagree with it: fa is one of the five.
func TestBenchTableFollowsTheDefaultRule(t *testing.T) {
	read := &tape.RunSummary{Concurrency: 1, Server: tape.ServerInfo{
		Args:  []string{"/usr/local/bin/llama-server", "-ngl", "99"},
		Flags: tape.ServerFlags{NGL: "99"},
	}}
	if got := LlamaBenchTable(read); !strings.Contains(got, "| default |") {
		t.Errorf("bench table does not carry the observed-as-absent fa:\n%s", got)
	}
	unread := &tape.RunSummary{Concurrency: 1}
	if got := LlamaBenchTable(unread); strings.Contains(got, "default") {
		t.Errorf("bench table claimed a default from an argv nobody read:\n%s", got)
	}
}

// queuedSummary is the case the real 8-stream run on a 4-slot server hit: the
// card printed "slots busy max 4" and said nothing about the other four
// streams waiting their turn, so the per-stream rate looked like a server that
// simply ran slowly.
func queuedSummary() *tape.RunSummary {
	s := ExampleConcurrent()
	s.Server.NSlots = 4
	s.Aggregate.SlotsBusyMax = 4
	return s
}

// TestQueuedStreamsAreNamed: a run that asked for more streams than the server
// has slots was partly serialised, and the Streams block has to say so. The
// per-stream figure of a queued run is not the per-stream figure of a run that
// fitted, and a reader comparing two cards cannot see the difference otherwise.
func TestQueuedStreamsAreNamed(t *testing.T) {
	out := Text(queuedSummary())
	if !strings.Contains(out, "4 slots, 4 queued") {
		t.Errorf("a run with more streams than slots does not say so:\n%s", out)
	}
	if !strings.Contains(out, "slots busy max 4") {
		t.Errorf("the slots-busy figure disappeared:\n%s", out)
	}

	// Streams that all fit are not queued, and the line stays as it was.
	if got := Text(ExampleConcurrent()); strings.Contains(got, "queued") {
		t.Errorf("8 streams on 8 slots reported a queue:\n%s", got)
	}
	// An unread /props has no slot count, and a queue would be a guess.
	noSlots := queuedSummary()
	noSlots.Server.NSlots = 0
	if got := Text(noSlots); strings.Contains(got, "queued") {
		t.Errorf("a run with no slot count claimed a queue:\n%s", got)
	}
	// A single-stream run has no Streams block at all.
	single := Example()
	single.Server.NSlots = 0
	if got := Text(single); strings.Contains(got, "queued") {
		t.Errorf("a single-stream run reported a queue:\n%s", got)
	}
}

// TestContextString pins the Context row's value, the thinking clause
// included. The clause is not reachable from a *tape.RunSummary yet — the
// count lives on tape.PromptRecord — so this tests the formatter directly and
// the row itself will start printing it the moment reasoningTokens can read a
// real figure (TTP-20, 2026-09-13).
func TestContextString(t *testing.T) {
	cases := []struct {
		name                   string
		ctx, in, out, thinking int
		want                   string
	}{
		{"no thinking", 16384, 43, 96, 0, "16384 (43 in / 96 out)"},
		{"all thinking", 16384, 43, 96, 96, "16384 (43 in / 96 out · 96 thinking)"},
		{"some thinking", 16384, 43, 240, 96, "16384 (43 in / 240 out · 96 thinking)"},
		{"unknown ctx", 0, 43, 96, 0, "? (43 in / 96 out)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := contextString(tc.ctx, tc.in, tc.out, tc.thinking); got != tc.want {
				t.Errorf("contextString = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAnswerCutWarning: a run whose every predicted token was reasoning
// produced no answer at all, and the card has to say why — otherwise the
// reader sees a healthy decode rate beside an empty completion and blames the
// tool (TTP-20, 2026-09-13).
func TestAnswerCutWarning(t *testing.T) {
	cut := func(predicted, reasoning int) *tape.RunSummary {
		return &tape.RunSummary{Timings: tape.TimingsSummary{
			PredictedN: predicted, ReasoningN: reasoning,
		}}
	}
	if got := answerCutWarning(cut(128, 128)); got == "" {
		t.Error("no warning for a run that was all reasoning")
	} else {
		want := "answer cut: all 128 predicted tokens were reasoning — raise --n-predict"
		if got != want {
			t.Errorf("warning = %q, want %q", got, want)
		}
	}
	for _, tc := range []struct {
		name                 string
		predicted, reasoning int
	}{
		{"answered after thinking", 128, 96},
		{"no thinking at all", 128, 0},
		{"nothing generated", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := answerCutWarning(cut(tc.predicted, tc.reasoning)); got != "" {
				t.Errorf("warning = %q, want none", got)
			}
		})
	}

	// The whole card prints it, wrapped by the warning section, inside the box.
	text := Text(cut(128, 128))
	if !strings.Contains(text, "! answer cut: all 128 predicted tokens were reasoning") {
		t.Errorf("Text() does not print the warning:\n%s", text)
	}
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if got := Width(line); got != CardWidth {
			t.Fatalf("line %q is %d columns, want %d", line, got, CardWidth)
		}
	}
}

// TestAnswerCutOnExampleConcurrent renders the warning on the real example
// summary, so the check covers a card that has every other section filled in
// and not just the two counts the rule reads.
func TestAnswerCutOnExampleConcurrent(t *testing.T) {
	s := *ExampleConcurrent()
	before := Text(&s)
	if strings.Contains(before, "answer cut") {
		t.Fatal("the example summary already warns; this test cannot show the change")
	}

	s.Timings.ReasoningN = s.Timings.PredictedN
	after := Text(&s)
	want := fmt.Sprintf("! answer cut: all %s predicted tokens were reasoning", formatInt(s.Timings.PredictedN))
	if !strings.Contains(after, want) {
		t.Errorf("card does not carry %q:\n%s", want, after)
	}
	// The fix is in the warning. The sentence is 71 columns and the warning
	// column is 66, so it wraps and "--n-predict" starts the continuation
	// line, indented under the "!" the way every wrapped card field is.
	if !strings.Contains(after, "raise") || !strings.Contains(after, "--n-predict") {
		t.Errorf("the warning does not say what to do:\n%s", after)
	}
	if !strings.Contains(after, "\n│   --n-predict") {
		t.Errorf("the continuation line is not indented under the warning:\n%s", after)
	}
	// The Context row says the same thing in its own voice.
	if !strings.Contains(after, formatInt(s.Timings.PredictedN)+" thinking") {
		t.Errorf("Context row has no thinking clause:\n%s", after)
	}
	for _, line := range strings.Split(strings.TrimRight(after, "\n"), "\n") {
		if got := Width(line); got != CardWidth {
			t.Fatalf("line %q is %d columns, want %d", line, got, CardWidth)
		}
	}
	// The warning wraps onto at most two lines inside the box.
	var warn int
	for _, line := range strings.Split(after, "\n") {
		if strings.Contains(line, "answer cut") || strings.Contains(line, "raise --n-predict") {
			warn++
		}
	}
	if warn > 2 {
		t.Errorf("the warning spans %d lines, want at most 2", warn)
	}
}
