//go:build js && wasm

// The browser player: the same renderer the terminal runs, compiled to wasm.
//
// A published run is hosted as the record, not as a video of it
// (docs/toktape-spec.ko.md §9.1), which is only worth anything if the record
// can be drawn again. This is what draws it. The page opens on the PNG card
// and the figures and downloads none of this; pressing Replay fetches it.
//
// It exports a frame, not a picture. `tui.View` returns exactly rows lines of
// exactly cols display columns with ANSI escapes in them, and that string
// crosses into JavaScript untouched — the host paints it. Parsing the escapes
// here would put a second terminal emulator in the repo next to the one
// internal/render already has, and turning the frame into HTML here would
// make this package decide how the player looks, which is not its job.
//
// Nothing in here reads a tape's fields. It decodes with internal/tape, asks
// internal/tui for a frame and internal/render for when the run ended, and
// hands back what they say. The schema keeps one owner.
//
// details() is the same rule, one crossing wider: it asks internal/card for
// the text card and the summary JSON and internal/transcript for the
// per-stream projection, internal/card and internal/bandwidth for the
// reproduce block and the explainers, marshals the bundle to one JSON
// string, and hands that back. Shaping the answers — laying
// out the streams, drawing the card text — is the page's work, not this
// package's, for the same reason painting the frame is the host's.
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"syscall/js"

	"github.com/midagedev/toktape/internal/bandwidth"
	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/render"
	"github.com/midagedev/toktape/internal/tape"
	"github.com/midagedev/toktape/internal/transcript"
)

// version is set at build time with -ldflags "-X main.version=...", the same
// way cmd/toktape does it, so a page can say which build drew the frames.
var version = "dev"

// loaded is the tape the last successful load put here. One at a time: a
// player shows one run, and keeping every tape a page ever opened would grow
// without anything having decided to.
var loaded *tape.Tape

// sched is the clip the loaded tape plays as — the same render.Schedule a GIF
// or an mp4 is cut on, so the browser shows what a clip shows: a second on
// the pre-run screen, the run at 1:1, and the result card held at the end.
// The first build of this player stopped at RunEnd and never reached the
// card (user, 2026-09-18: "리플레이가 마지막 서머리 카드가 누락되네"); the fix
// is not a card branch of its own here but the schedule that already owns
// when the card comes on.
//
// At 1000 frames a second, a frame index is a millisecond of clip time, so
// frame(atMs) is one lookup and nothing here re-derives the phases.
var sched render.Schedule

const schedFPS = 1000

func main() {
	// The header line prints the build's version when the summary carries
	// none, the same way cmd/toktape does before it renders anything.
	card.Version = version
	api := js.Global().Get("Object").New()
	api.Set("load", js.FuncOf(load))
	api.Set("frame", js.FuncOf(frame))
	api.Set("details", js.FuncOf(details))
	api.Set("version", version)
	js.Global().Set("toktape", api)

	// The exported functions have to outlive main, or the first call lands on
	// a program that has already exited.
	select {}
}

// load decodes one run file. The argument is a Uint8Array of the bytes as
// they were downloaded — the same bytes `toktape record` wrote, which is what
// /r/<id>.toktape serves.
//
// It answers with an object rather than throwing: a page that fetched a
// truncated file should be able to say so in its own words, and an exception
// crossing the wasm boundary carries less than this does.
func load(_ js.Value, args []js.Value) any {
	if len(args) != 1 {
		return result(false, "load takes one Uint8Array", 0, 0)
	}
	buf := make([]byte, args[0].Get("length").Int())
	if n := js.CopyBytesToGo(buf, args[0]); n != len(buf) {
		return result(false, "the bytes did not cross into wasm intact", 0, 0)
	}

	tp, err := tape.Decode(bytes.NewReader(buf))
	if err != nil {
		loaded = nil
		// The decoder's own words. It is the side that knows whether this was
		// a truncated gzip, a schema from a newer build, or not a tape at all.
		return result(false, err.Error(), 0, 0)
	}
	loaded = tp
	// A whole-run clip without the cold open: the page already opened on the
	// card, so the typed command would be a second introduction.
	sched = render.NewSchedule(0, render.RunEnd(tp), schedFPS, 0, false)
	return result(true, "", sched.Duration.Milliseconds(), tp.Summary.Concurrency)
}

// frame draws one instant. atMs is clip time, not wall time: the frame is a
// pure function of it, which is what lets the same tape replay to the same
// frames in a browser as in a terminal (§1 decision 10). The schedule maps
// clip time onto run time and mode; render.FrameText draws it exactly as a
// GIF frame is drawn, theme and all.
func frame(_ js.Value, args []js.Value) any {
	if loaded == nil || len(args) != 3 {
		return ""
	}
	f := sched.Frame(int(args[0].Float()))
	cols, rows := args[1].Int(), args[2].Int()
	return render.FrameText(loaded, render.Options{Width: cols, Height: rows}, f)
}

// details answers with everything the run page's Details section shows, as
// one JSON string so the tape's fields cross into JavaScript exactly once.
// With nothing loaded it answers "": a page that asks before pressing Replay
// has no record yet, and an empty string is falsy enough to say so.
//
// The shape is card (the text card, Unicode box, no ANSI), summary (the
// card's JSON as an object, not a string), reproduce (the Markdown block the
// card closes with), explain (the caveat and bandwidth listings) and streams
// (the transcript projection). Nothing here is shaped for display: numbers cross unrounded,
// text crosses unescaped, and the page formats both.
func details(_ js.Value, _ []js.Value) any {
	if loaded == nil {
		return ""
	}
	sum := loaded.Summary
	summary, err := card.JSON(&sum)
	if err != nil {
		return ""
	}
	// The explainers go first in cmd/toktape/card.go explainCard's order:
	// the engine placement when there is one, then every caveat check with
	// its verdict, then the bandwidth clauses.
	var explain strings.Builder
	if e := card.ExplainEnginePlacement(&sum); e != "" {
		explain.WriteString(e)
		explain.WriteString("\n")
	}
	explain.WriteString(card.ExplainCaveats(&sum))
	explain.WriteString("\n")
	explain.WriteString(bandwidth.Explain(&sum).String())
	out, err := json.Marshal(detailsDoc{
		Card:      card.Text(&sum),
		Summary:   json.RawMessage(summary),
		Reproduce: card.Reproduce(&sum),
		Explain:   explain.String(),
		Streams:   transcript.Streams(loaded),
	})
	if err != nil {
		return ""
	}
	return string(out)
}

// detailsDoc is the bundle details() hands the page. A struct, so the field
// order is fixed in source and the JSON key order with it.
type detailsDoc struct {
	Card      string              `json:"card"`
	Summary   json.RawMessage     `json:"summary"`
	Reproduce string              `json:"reproduce"`
	Explain   string              `json:"explain"`
	Streams   []transcript.Stream `json:"streams"`
}

// result is load's answer. Every field is set on every path, failures
// included, so a caller never has to tell "absent" from "zero" — the same
// rule the tape schema keeps.
func result(ok bool, why string, durationMs int64, streams int) any {
	return map[string]any{
		"ok":         ok,
		"error":      why,
		"durationMs": durationMs,
		"streams":    streams,
	}
}
