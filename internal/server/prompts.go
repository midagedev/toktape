package server

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/midagedev/toktape/internal/tape"
)

// defaultPrompts is the zero-config prompt set. The north star asks for a
// first run with no flag to learn, so a user who types nothing still gets a
// real measurement, and a concurrent run needs one distinct prompt per stream.
// They are also the text a reader sees on opening a tape, so each is written
// as the request a careful engineer would send a coding assistant: real
// material to work on, then what to do with it (TTP-84, 2026-09-14).
//
// Their shape follows from what ends a run, and that changed with TTP-76: a
// run is aimed in seconds, not tokens. With nothing named it has a 20 s
// wall-clock budget, a floor of tape.MinCutTokens under the cut, and a
// runaway cap of 2048 tokens behind both (DefaultFor, DefaultMaxTokens in
// internal/recorder/limit.go). Keeping a slow rig's run short is the clock's
// job now, not the prompt's. So:
//
// The answers are long. 20 s at 140 tok/s (a 7B on a 3090) is about 2800
// tokens, and the set this one replaced, sized to answer in 150 to 300, ended
// on EOS in about two seconds: the zero-config clip was a two-second
// generation and the clock never mattered. Each prompt here asks for several
// parts — every bug explained, a rewrite, a test file, a plan to confirm the
// cause — which a capable model writes for thousands of tokens. On a 13.5
// tok/s rig the same prompt is cut at about 270 tokens, less what its prefill
// took of the 20 s, and on a 2 tok/s rig the floor holds the cut until 64: the
// prompt does not have to know which box it is on. EOS arriving first is still
// possible and still honest, and the tape says which happened; the aim is only
// that our own prompt is not the reason.
//
// The prompts are long enough to be a prefill measurement and no longer. A
// prompt under tape.MinPrefillPromptTokens is not one and the card says so.
// The ceiling is the newer half of the rule and it was measured, not reasoned
// (lead, 2026-09-14): on this repo's hero rig, two concurrent 357-token
// prompts spent 21 s before the first token — warm, at 16.8 tok/s of prefill
// per stream — which is the whole of a 20 s run. That rig's prefill is bound
// by paging a 445 GB model through 251 GB of RAM, and a box like it is
// precisely the box this tool exists for, so the set is sized for it rather
// than for the rig where prefill is free. prompts_test.go pins both ends in
// characters, with the conversion it assumes.
//
// The length comes from the material — a function with a real bug, a query
// plan, a log excerpt — never from filler: a model given filler writes filler,
// and a reader who opens the tape sees it. It is also the workload this
// project is aimed at, since coding agents send exactly this.
//
// They are unlike each other from the first word, so concurrent streams do
// not share a prefix and cannot hit each other's prompt cache, which would
// make the measured prefill meaningless. Material comes first and the
// instruction last, and nothing needs a tool, a file or the network, so any
// instruction-tuned model can answer.
//
// Five prompts joined the set on 2026-09-18 (TTP-112), because the set is now
// also the comparison set for published runs (spec §9.5) and what it leaves
// out is what nobody can compare. Fifteen of the sixteen were code, config or
// logs, and all sixteen were English. Two English prose prompts, two Korean
// and one Japanese close both gaps, and prompts_test.go holds them open.
//
// They are prose on purpose rather than English instructions wrapped around a
// code block: a decode rate measured on code does not transfer to prose (this
// set's densest code runs 3.3 characters to a token, its most prose-like 5.0),
// and Korean and Japanese run about 1.5 — the same token budget is a very
// different amount of screen, which is the case the card's east-asian width
// contract exists for and the one that never ran.
//
// The order is part of the set. A run sends the first Concurrency prompts, so
// `-n 1` is prompt 0 and only `-n 21` is all of them: the additions are placed
// where the common runs meet them rather than appended, with the first Korean
// at 6 and the Japanese at 14, and `-n 1` through `-n 4` left in English.
var defaultPrompts = loadPrompts()

// promptFiles holds the set as one file per prompt, in index order by name.
//
// They were Go raw strings until 2026-09-20 and moved out for two reasons
// (TTP-144). The set is about to grow to thousands of tokens per prompt —
// long enough that prefill is a throughput and not mostly fixed cost — and
// a few hundred kilobytes of prose and code inside a source file is a file
// nobody reads. And a prompt per file is a boundary: the material can be
// written, reviewed and diffed one artifact at a time, which a single var
// block of twenty-one raw strings cannot be.
//
// The contents are unchanged by the move — promptSetHash pins that, and it
// did not move — so every tape published under this id still describes the
// set it says it does.
//
//go:embed prompts/*.txt
var promptFiles embed.FS

// loadPrompts reads the set at startup, in the order the filenames sort in,
// which is the index order the set's own doc comment describes: a run sends
// the first Concurrency prompts, so which prompt is at which index is part
// of the set and not an implementation detail. A file that cannot be read is
// a build that shipped without its prompts, which is not a condition to
// degrade gracefully through.
func loadPrompts() []string {
	names, err := fs.Glob(promptFiles, "prompts/*.txt")
	if err != nil {
		panic("prompt set: " + err.Error())
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, n := range names {
		b, err := promptFiles.ReadFile(n)
		if err != nil {
			panic("prompt set: " + err.Error())
		}
		out = append(out, string(b))
	}
	return out
}

// PromptSetID names this set, and every request DefaultPrompts returns
// carries it (StreamRequest.Set). Two records carrying one id must have done
// the same work, so the id changes whenever the prompts do — prompts_test.go
// holds a hash of the set and fails when they drift apart, which is the only
// thing that keeps the promise true.
//
// The version tracks what a release shipped, not what main holds: a set that
// has never been in a release has nothing to be compared against yet, and
// bumping it before then would spend a version on nobody's tape.
const PromptSetID = "prompts@v1"

// DefaultPrompts returns n distinct chat requests in a deterministic order, so
// two runs on the same rig send the same work. n <= 0 yields nil.
//
// Beyond the built-in set the prompts repeat with a numbered lead sentence,
// which keeps them distinct from their second word — the one "Request" token
// in front of the number is all two streams can share — and so keeps every
// stream's prefix its own.
//
// No request carries a cap of its own. The run's cap is the recorder's to set
// (Options.MaxTokens, resolved in internal/recorder/limit.go and written onto
// every request by buildRequests and roundRequests), and a cap here would be a
// second number on that axis — one that wins wherever a request's own cap is
// allowed to, as it is in a multi-round run.
func DefaultPrompts(n int) []StreamRequest {
	if n <= 0 {
		return nil
	}
	out := make([]StreamRequest, 0, n)
	for i := 0; i < n; i++ {
		text := defaultPrompts[i%len(defaultPrompts)]
		if i >= len(defaultPrompts) {
			text = fmt.Sprintf("Request %d. %s", i+1, text)
		}
		out = append(out, StreamRequest{
			Messages: []tape.Message{{Role: "user", Content: text}},
			Set:      PromptSetID,
		})
	}
	return out
}
