package publish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// fullTape is a run with one of everything the public view has a rule about.
func fullTape() *tape.Tape {
	s := *card.Example()
	s.Host.Hostname = "ws"
	s.Host.HostnameSource = tape.HostnameObserved
	s.Server.Host = "ws"
	s.Server.URL = "http://192.168.1.44:8080"
	s.Server.Args = []string{
		"/opt/llama.cpp/build/bin/llama-server",
		"-m", "/home/k/models/Qwen3-0.6B/Qwen3-0.6B-Q8_0.gguf",
		"--flash-attn",
		"-ngl=99",
		"--lora=/home/k/loras/style.gguf",
		"-c", "32768",
	}
	s.Model.Path = "/home/k/models/Qwen3-0.6B/Qwen3-0.6B-Q8_0.gguf"
	s.Model.FileName = "Qwen3-0.6B-Q8_0.gguf"
	s.Model.Dir = "Qwen3-0.6B"
	s.Warnings = []string{"model file not readable here: /home/k/models/Qwen3-0.6B/Qwen3-0.6B-Q8_0.gguf."}
	s.Tag = "ngl=99"
	s.Note = "fa on"
	return &tape.Tape{
		Summary: s,
		Requests: []tape.RequestRecord{{
			Index: 0,
			Prompt: tape.PromptRecord{
				Messages:   []tape.Message{{Role: "user", Content: "hello"}},
				Completion: "hi there",
			},
			Tokens: []tape.TokenEvent{{Index: 0, Text: "hi"}, {Index: 1, Text: " there"}},
		}},
	}
}

func TestPublicViewStripsThePlaceAndKeepsTheRun(t *testing.T) {
	in := fullTape()
	out := PublicView(in, WithText)
	s := out.Summary

	if s.Host.Hostname != "" || s.Host.HostnameSource != "" || s.Server.Host != "" {
		t.Errorf("an observed hostname survived: %q / %q / %q",
			s.Host.Hostname, s.Host.HostnameSource, s.Server.Host)
	}
	if s.Server.URL != ":8080" {
		t.Errorf("Server.URL = %q, want \":8080\" — the host is a place, the port is a setting", s.Server.URL)
	}
	if s.Model.Path != "" {
		t.Errorf("Model.Path = %q, want empty", s.Model.Path)
	}
	// The identity of the model is the file name and the variant directory,
	// and taking the path must not take them too.
	if s.Model.FileName != "Qwen3-0.6B-Q8_0.gguf" || s.Model.Dir != "Qwen3-0.6B" {
		t.Errorf("file name / dir = %q / %q, want them kept", s.Model.FileName, s.Model.Dir)
	}
	wantArgs := []string{"llama-server", "-m", "Qwen3-0.6B-Q8_0.gguf", "--flash-attn", "-ngl=99", "--lora=style.gguf", "-c", "32768"}
	if strings.Join(s.Server.Args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("Server.Args = %q,\n            want %q", s.Server.Args, wantArgs)
	}
	if got := s.Warnings[0]; got != "model file not readable here: Qwen3-0.6B-Q8_0.gguf." {
		t.Errorf("warning = %q: the path went, the sentence and its full stop should not have", got)
	}
	// The user's own words about their own experiment.
	if s.Tag != "ngl=99" || s.Note != "fa on" {
		t.Errorf("tag / note = %q / %q, want them kept", s.Tag, s.Note)
	}
	// Public by default: the body is the subject of the service.
	if out.Requests[0].Prompt.Completion != "hi there" || out.Requests[0].Tokens[0].Text != "hi" {
		t.Errorf("the text was dropped on a default publish: %+v", out.Requests[0].Prompt)
	}
}

// A label is what somebody chose to publish, so it is the one hostname that
// survives — the reason --host-label exists at all (TTP-93).
func TestPublicViewKeepsALabel(t *testing.T) {
	in := fullTape()
	in.Summary.Host.Hostname = "workstation"
	in.Summary.Host.HostnameSource = tape.HostnameLabelled
	in.Summary.Server.Host = "workstation"

	s := PublicView(in, WithText).Summary
	if s.Host.Hostname != "workstation" || s.Host.HostnameSource != tape.HostnameLabelled {
		t.Errorf("a label was stripped: %q / %q", s.Host.Hostname, s.Host.HostnameSource)
	}
	if s.Server.Host != "workstation" {
		t.Errorf("Server.Host = %q, want the label", s.Server.Host)
	}
}

// The opt-out drops the words and nothing else: the timings and the per-token
// timestamps are measurements, and the sparkline is drawn from those.
func TestPublicViewWithoutText(t *testing.T) {
	out := PublicView(fullTape(), WithoutText)
	r := out.Requests[0]

	if r.Prompt.Messages != nil || r.Prompt.Completion != "" || r.Prompt.Reasoning != "" {
		t.Errorf("text survived --no-text: %+v", r.Prompt)
	}
	for i, ev := range r.Tokens {
		if ev.Text != "" {
			t.Errorf("token %d kept its text %q", i, ev.Text)
		}
	}
	if len(r.Tokens) != 2 {
		t.Errorf("%d tokens, want 2: the timestamps are the measurement", len(r.Tokens))
	}
}

// The input is a tape the caller may still render or write back to disk.
func TestPublicViewDoesNotEditItsInput(t *testing.T) {
	in := fullTape()
	before := in.Summary.Model.Path
	firstArg := in.Summary.Server.Args[0]
	firstToken := in.Requests[0].Tokens[0].Text

	PublicView(in, WithoutText)

	if in.Summary.Model.Path != before || in.Summary.Server.Args[0] != firstArg {
		t.Errorf("PublicView edited its input: path %q, arg %q", in.Summary.Model.Path, in.Summary.Server.Args[0])
	}
	if in.Requests[0].Tokens[0].Text != firstToken {
		t.Errorf("PublicView cleared a token in its input: %q", in.Requests[0].Tokens[0].Text)
	}
}

// TestPublicViewVerifiesThePromptSet: the id is a claim about what was sent,
// and the recorder stamps it without anything checking it afterwards. This is
// the last place that can, so an unverifiable claim is blanked rather than
// forwarded to the index.
func TestPublicViewVerifiesThePromptSet(t *testing.T) {
	setTape := func(n int, mutate func([]tape.RequestRecord)) *tape.Tape {
		tp := &tape.Tape{}
		tp.Summary.Concurrency = n
		tp.Summary.PromptSet = server.PromptSetID
		for i, req := range server.DefaultPrompts(n) {
			tp.Requests = append(tp.Requests, tape.RequestRecord{
				Index:  i,
				Prompt: tape.PromptRecord{Messages: req.Messages},
			})
		}
		if mutate != nil {
			mutate(tp.Requests)
		}
		return tp
	}

	if got := PublicView(setTape(4, nil), WithText).Summary.PromptSet; got != server.PromptSetID {
		t.Errorf("PromptSet = %q on a run that did send the set, want %q", got, server.PromptSetID)
	}

	// A multi-round run sends the same prompts again, each record carrying
	// its index within its round.
	rounds := setTape(2, nil)
	rounds.Requests = append(rounds.Requests, rounds.Requests[0], rounds.Requests[1])
	rounds.Requests[2].Round, rounds.Requests[3].Round = 1, 1
	if got := PublicView(rounds, WithText).Summary.PromptSet; got != server.PromptSetID {
		t.Errorf("PromptSet = %q on a two-round run of the set, want %q", got, server.PromptSetID)
	}

	for _, c := range []struct {
		name string
		tp   *tape.Tape
	}{{
		name: "one prompt edited",
		tp: setTape(4, func(r []tape.RequestRecord) {
			r[2].Prompt.Messages[0].Content += " Answer in Korean."
		}),
	}, {
		name: "the prompts sent in another order",
		tp: setTape(4, func(r []tape.RequestRecord) {
			r[0].Prompt.Messages, r[1].Prompt.Messages = r[1].Prompt.Messages, r[0].Prompt.Messages
		}),
	}, {
		name: "the text already gone, so there is nothing to check against",
		tp: setTape(4, func(r []tape.RequestRecord) {
			for i := range r {
				r[i].Prompt.Messages = nil
			}
		}),
	}, {
		name: "an id from a version this binary does not carry",
		tp: func() *tape.Tape {
			tp := setTape(4, nil)
			tp.Summary.PromptSet = "prompts@v99"
			return tp
		}(),
	}, {
		name: "no requests at all",
		tp: func() *tape.Tape {
			tp := &tape.Tape{}
			tp.Summary.Concurrency = 4
			tp.Summary.PromptSet = server.PromptSetID
			return tp
		}(),
	}} {
		t.Run(c.name, func(t *testing.T) {
			if got := PublicView(c.tp, WithText).Summary.PromptSet; got != "" {
				t.Errorf("PromptSet = %q, want \"\": the claim could not be checked", got)
			}
		})
	}
}

// TestPublicViewOnTheHeroTape is the fixture check: the repo's own published
// tape, through the function that decides what a published tape looks like.
// It was recorded before prompts@v1 existed, so it is also the real case the
// verification above exists for.
func TestPublicViewOnTheHeroTape(t *testing.T) {
	path := filepath.Join("..", "..", "assets", "hero.tape")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("hero tape not present: %v", err)
	}
	tp, err := tape.Read(path)
	if err != nil {
		t.Fatalf("load hero tape: %v", err)
	}
	out := PublicView(tp, WithText)

	if out.Summary.PromptSet != "" {
		t.Errorf("PromptSet = %q: the hero predates the published set and belongs to no comparison set", out.Summary.PromptSet)
	}
	if out.Summary.Model.Path != "" {
		t.Errorf("Model.Path = %q", out.Summary.Model.Path)
	}
	if out.Summary.Host.HostnameSource == tape.HostnameObserved {
		t.Errorf("an observed hostname survived on the hero tape")
	}
	for i, a := range out.Summary.Server.Args {
		if strings.HasPrefix(a, "/") || strings.HasPrefix(a, "~/") {
			t.Errorf("arg %d is still an absolute path: %q", i, a)
		}
	}
	// The card must still render from the view — a sanitiser that took a
	// figure with the paths would be a different bug with the same commit.
	if len(out.Requests) != len(tp.Requests) {
		t.Errorf("%d requests, want %d", len(out.Requests), len(tp.Requests))
	}
	if IndexOf(out).ModelRaw == "" {
		t.Error("the published index lost the model's name along with its path")
	}
}
