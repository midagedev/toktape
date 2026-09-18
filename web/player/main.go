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
package main

import (
	"bytes"
	"syscall/js"
	"time"

	"github.com/midagedev/toktape/internal/render"
	"github.com/midagedev/toktape/internal/tape"
	"github.com/midagedev/toktape/internal/tui"
)

// version is set at build time with -ldflags "-X main.version=...", the same
// way cmd/toktape does it, so a page can say which build drew the frames.
var version = "dev"

// loaded is the tape the last successful load put here. One at a time: a
// player shows one run, and keeping every tape a page ever opened would grow
// without anything having decided to.
var loaded *tape.Tape

func main() {
	api := js.Global().Get("Object").New()
	api.Set("load", js.FuncOf(load))
	api.Set("frame", js.FuncOf(frame))
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
	return result(true, "", render.RunEnd(tp).Milliseconds(), tp.Summary.Concurrency)
}

// frame draws one instant. atMs is clip time, not wall time: View is a pure
// function of it, which is what lets the same tape replay to the same frames
// in a browser as in a terminal (§1 decision 10).
func frame(_ js.Value, args []js.Value) any {
	if loaded == nil || len(args) != 3 {
		return ""
	}
	at := time.Duration(args[0].Float()) * time.Millisecond
	cols, rows := args[1].Int(), args[2].Int()

	m := tui.ModelAt(loaded, at)
	// The terminal's own theme. A browser that chose a different palette
	// would be a second answer to what the product looks like.
	m.Theme = tui.ColourTheme()
	return tui.View(m, at, cols, rows)
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
