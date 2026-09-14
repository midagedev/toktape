package card

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// TestReproduceGolden freezes the block a reader copies out of a post. It is a
// golden because its value is in the exact words: the three questions the
// launch research says kill a benchmark thread ("what was your batch", "was
// the prompt cached", "paste your command") are answered by this text and by
// nothing else on the card.
//
// 2026-09-13 TTP-28: re-baselined for the new example — R1 Distill Llama 70B dense,
// eight streams at 320 tokens. The words the test exists for are unchanged;
// what moved is the model, the flags and the figures quoted inside them.
func TestReproduceGolden(t *testing.T) {
	golden(t, "example-concurrent.md", []byte(Markdown(ExampleConcurrent())))
}

// TestReproduceIsPartOfMarkdownOnly: the 72-column text card and the JSON
// rendering are untouched by the block; only --md grows.
func TestReproduceIsPartOfMarkdownOnly(t *testing.T) {
	s := ExampleConcurrent()
	if strings.Contains(Text(s), "Reproduce") {
		t.Error("the text card must not carry the Reproduce block")
	}
	md := Markdown(s)
	if !strings.Contains(md, "<details><summary>Reproduce</summary>") {
		t.Errorf("Markdown is missing the Reproduce block:\n%s", md)
	}
	if !strings.Contains(md, "</details>") {
		t.Error("the Reproduce block is not closed")
	}
	// The block comes after the llama-bench table, so a reader scrolling the
	// comment meets the figures first and the provenance last.
	if strings.Index(md, "| model |") > strings.Index(md, "<details>") {
		t.Error("the Reproduce block must follow the llama-bench table")
	}
}

// TestReproduceQuotesTheArgvVerbatim: the server line is the process's own
// command line, joined with single spaces and redacted nowhere. A reader who
// cannot see the real flags cannot reproduce the run, and a card that hides
// them is the card the thread argues with.
func TestReproduceQuotesTheArgvVerbatim(t *testing.T) {
	s := ExampleConcurrent()
	block := Reproduce(s)
	want := strings.Join(s.Server.Args, " ")
	if !strings.Contains(block, want) {
		t.Errorf("Reproduce does not carry the argv verbatim:\nwant %q\n--- got ---\n%s", want, block)
	}
	if !strings.Contains(block, "/proc/48213/cmdline") {
		t.Errorf("the server line does not say where the argv came from:\n%s", block)
	}
	// Long -ot values stay on one line: the block is a command to paste.
	for _, line := range strings.Split(block, "\n") {
		if strings.Contains(line, "-ot ") && strings.Contains(line, "…") {
			t.Errorf("the -ot value was truncated in a block meant to be pasted: %q", line)
		}
	}
}

// TestReproduceRecordedWithLine: every flag in the toktape command line is a
// value that was observed. Concurrency 1 is the default and is not printed.
func TestReproduceRecordedWithLine(t *testing.T) {
	cases := []struct {
		name string
		s    *tape.RunSummary
		want string
		not  []string
	}{
		{
			name: "concurrent run names every option",
			s:    ExampleConcurrent(),
			want: "toktape --url http://127.0.0.1:8080 -n 8 --n-predict 307",
		},
		{
			name: "a single stream does not print -n",
			s:    Example(),
			want: "toktape --url http://127.0.0.1:8080 --n-predict 320",
			not:  []string{"-n 1"},
		},
		{
			name: "nothing observed is a bare command",
			s:    &tape.RunSummary{Concurrency: 1},
			want: "toktape\n",
			not:  []string{"--url", "--n-predict", "-n "},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			block := Reproduce(c.s)
			if !strings.Contains(block, c.want) {
				t.Errorf("block does not contain %q:\n%s", c.want, block)
			}
			for _, bad := range c.not {
				if strings.Contains(block, bad) {
					t.Errorf("block contains %q, which was not observed:\n%s", bad, block)
				}
			}
		})
	}
}

// TestRecordCommandNamesWhatEndedTheRun: the rebuilt command re-asks for the
// limit the run was recorded with, one row per row of the table
// internal/recorder.Options.limit resolves (TTP-76, 2026-09-14).
//
// Before this, the block appended "--n-predict <Timings.PredictedN>" to every
// run. Both halves of that were wrong once the clock existed: a clock-bound run
// was reproduced by token count, which turns the clock off and is a different
// run on a different box; and PredictedN is the per-stream MEAN above
// Concurrency 1, so a concurrent run printed a number no stream produced and
// no flag ever set. The last case below is that one, and it is the reason the
// streams are deliberately unequal.
func TestRecordCommandNamesWhatEndedTheRun(t *testing.T) {
	cases := []struct {
		name string
		s    *tape.RunSummary
		want string
		not  []string
	}{
		{
			name: "a clock run re-asks for the budget",
			s: &tape.RunSummary{
				Concurrency: 1,
				Limit:       tape.LimitSummary{For: 20 * time.Second, MaxTokens: 2048, MinTokens: 64},
				Timings:     tape.TimingsSummary{PredictedN: 271},
			},
			want: "toktape --for 20s",
			// The runaway guard is not what the run was aimed at, and the
			// count that came back is not a flag anybody set.
			not: []string{"--n-predict", "271", "2048"},
		},
		{
			name: "a cut run re-asks for the budget, not for how long the cut took",
			s: &tape.RunSummary{
				Concurrency: 1,
				Limit: tape.LimitSummary{For: 20 * time.Second, MaxTokens: 2048,
					MinTokens: 64, CutAt: 22 * time.Second},
				Timings: tape.TimingsSummary{PredictedN: 64},
			},
			want: "toktape --for 20s",
			not:  []string{"22s", "--n-predict"},
		},
		{
			name: "a token-capped run names the cap that was sent",
			s: &tape.RunSummary{
				Concurrency: 1,
				Limit:       tape.LimitSummary{MaxTokens: 256},
				Timings:     tape.TimingsSummary{PredictedN: 187}, // EOS came first
			},
			want: "toktape --n-predict 256",
			not:  []string{"--for", "187"},
		},
		{
			name: "a concurrent run names the cap, never the per-stream mean",
			s: &tape.RunSummary{
				Concurrency: 2,
				Limit:       tape.LimitSummary{MaxTokens: 512},
				// 200 and 300 tokens: the mean is 250, which is what the old
				// code printed and what no stream produced.
				Timings: tape.TimingsSummary{PredictedN: 250},
			},
			want: "toktape -n 2 --n-predict 512",
			not:  []string{"250", "--for"},
		},
		{
			name: "a tape older than the limit field falls back to the count",
			s: &tape.RunSummary{
				Concurrency: 1,
				Timings:     tape.TimingsSummary{PredictedN: 320},
			},
			want: "toktape --n-predict 320",
			not:  []string{"--for"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := recordCommand(c.s)
			if got != c.want {
				t.Errorf("recordCommand = %q, want %q", got, c.want)
			}
			for _, bad := range c.not {
				if strings.Contains(got, bad) {
					t.Errorf("recordCommand = %q, which names %q; that is not what the run was aimed at", got, bad)
				}
			}
		})
	}
}

// TestReproduceWithoutAnArgvSaysSo: a run that never read the server's process
// says the command line was not observed rather than reconstructing one. A
// reconstructed argv would invent an order and a set of flags nobody saw.
func TestReproduceWithoutAnArgvSaysSo(t *testing.T) {
	block := Reproduce(&tape.RunSummary{Concurrency: 1})
	if !strings.Contains(block, "Server command line not observed") {
		t.Errorf("block does not admit the missing argv:\n%s", block)
	}
	if strings.Contains(block, "llama-server") {
		t.Errorf("block invented a server command line:\n%s", block)
	}
	// No tape ID means no file name to attach; a "?.tape" would be a lie.
	if strings.Contains(block, ".tape") {
		t.Errorf("block named a tape file that has no name:\n%s", block)
	}
}

// TestReproduceNamesTheTape: the tape is the artifact that makes the post
// checkable, so the block names the file and the verb that replays it.
func TestReproduceNamesTheTape(t *testing.T) {
	block := Reproduce(ExampleConcurrent())
	want := "20260913-150210-r1-distill-llama-70b" + tape.Ext
	if !strings.Contains(block, want) {
		t.Errorf("block does not name the tape %q:\n%s", want, block)
	}
	if !strings.Contains(block, "toktape play") {
		t.Errorf("block does not say how to replay the tape:\n%s", block)
	}
}

// TestExampleArgvParsesToItsFlags keeps the fixture honest: the argv the
// Reproduce block quotes and the flags the FLAGS row prints are two views of
// one command line, and a reader who pastes the first must get the second.
//
// Only the named fields are compared. ServerFlags.Other collects every token
// this parser does not name (-m, -c, --parallel above); the fixture leaves it
// empty so the golden card's FLAGS row stays the named set, which is the one
// thing this fixture is not a faithful recording of.
func TestExampleArgvParsesToItsFlags(t *testing.T) {
	s := Example()
	got := server.ParseFlags(s.Server.Args)
	got.Other = nil
	if !reflect.DeepEqual(got, s.Server.Flags) {
		t.Errorf("ParseFlags(Args) =\n%+v\nwant\n%+v", got, s.Server.Flags)
	}
	// The argv is also what the recorder would have found: a command line with
	// no flags at all parses to no flags, which is the "default" case the
	// FLAGS row prints.
	if f := server.ParseFlags([]string{"/usr/local/bin/llama-server", "-m", "/m.gguf"}); f.FlashAttn != "" {
		t.Errorf("a bare command line produced fa = %q, want unset", f.FlashAttn)
	}
}
