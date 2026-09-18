package publish

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

func previewOf(t *testing.T, in *tape.Tape, opts Options) string {
	t.Helper()
	view := PublicView(in, opts.Text)
	return Preview(view, IndexOf(view), opts)
}

// TestPreviewAnswersTheQuestionItIsAskedFor: the preview is the argument for a
// structured record — it can name the hostname and the model path where a byte
// stream could only say "the recording" — so every row of §9.3's table has to
// appear in it, with what became of that field.
func TestPreviewAnswersTheQuestionItIsAskedFor(t *testing.T) {
	out := previewOf(t, fullTape(), Options{})

	for _, want := range []string{
		"visibility",
		"public",
		"hostname",
		"removed", // the observed hostname
		":8080",   // the url, reduced to its port
		"model path     removed",
		"Qwen3-0.6B-Q8_0.gguf", // the file name that travels in its place
		"paths shortened to file names",
		"characters of prompt", // how much text, not that there is some
		// The card leaves the machine too, and it is the one thing on the
		// published page that the service could not have drawn itself.
		"1200×675",
		`"schema": 1`, // the row itself
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the preview does not mention %q:\n%s", want, out)
		}
	}
	// What it must NOT contain is the point of the whole exercise.
	for _, forbidden := range []string{"/home/k", "192.168.1.44", "/opt/llama.cpp"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the preview shows %q, which means the view still carries it:\n%s", forbidden, out)
		}
	}
}

func TestPreviewPrivateAndNoText(t *testing.T) {
	out := previewOf(t, fullTape(), Options{Private: true, Text: WithoutText})
	if !strings.Contains(out, "private") || !strings.Contains(out, "out of the search") {
		t.Errorf("the preview does not say the run is unlisted:\n%s", out)
	}
	if !strings.Contains(out, "the timings stay") {
		t.Errorf("the preview does not say what --no-text keeps:\n%s", out)
	}
	if strings.Contains(out, "characters of prompt") {
		t.Errorf("the preview counted text that is not travelling:\n%s", out)
	}
}

// A label is the one hostname that travels, and the preview says so rather
// than listing it under what was removed.
func TestPreviewSaysWhenALabelTravels(t *testing.T) {
	in := fullTape()
	in.Summary.Host.Hostname = "workstation"
	in.Summary.Host.HostnameSource = tape.HostnameLabelled

	out := previewOf(t, in, Options{})
	if !strings.Contains(out, "workstation · your own label") {
		t.Errorf("the preview hides that a label travels:\n%s", out)
	}
}

// Whether the run is in a comparison set is the one thing a publisher cannot
// work out by looking at the card, so the preview states it either way.
func TestPreviewSaysWhetherTheRunIsComparable(t *testing.T) {
	out := previewOf(t, fullTape(), Options{})
	if !strings.Contains(out, "not in a comparison set") {
		t.Errorf("a run with no prompt set is not said to be uncomparable:\n%s", out)
	}

	in := &tape.Tape{}
	in.Summary.Concurrency = 2
	in.Summary.PromptSet = server.PromptSetID
	for i, req := range server.DefaultPrompts(2) {
		in.Requests = append(in.Requests, tape.RequestRecord{
			Index:  i,
			Prompt: tape.PromptRecord{Messages: req.Messages},
		})
	}
	out = previewOf(t, in, Options{})
	if !strings.Contains(out, server.PromptSetID) || !strings.Contains(out, "comparable with") {
		t.Errorf("a run of the published set is not said to be comparable:\n%s", out)
	}
}

// The profile and the note are something that leaves the machine, so the
// listing names them the same way it names everything else that does — and
// says "none" when nothing about the author travels.
func TestPreviewNamesWhoPublishedIt(t *testing.T) {
	out := previewOf(t, fullTape(), Options{
		Author: &Author{Name: "Lab Rat", Link: "https://github.com/example", Avatar: tinyPNG(t, 16, 16)},
		Title:  "First ik_llama sweep",
		Note:   "Trying -fa on.\n\nSecond paragraph.",
	})
	for _, want := range []string{
		"Who it says published it",
		"Lab Rat",
		"https://github.com/example",
		"16×16 PNG",
		"The note",
		"First ik_llama sweep",
		"Trying -fa on. … (",
		"characters)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the preview does not mention %q:\n%s", want, out)
		}
	}

	bare := previewOf(t, fullTape(), Options{})
	if !strings.Contains(bare, "nothing about you travels") {
		t.Errorf("a run with no profile does not say so:\n%s", bare)
	}
}

// A tape with nothing in it must still produce a preview: the dry run is what
// somebody reaches for when they do not trust what is about to happen, and it
// failing is the worst moment for it to fail.
func TestPreviewOnAnEmptyTape(t *testing.T) {
	out := previewOf(t, &tape.Tape{}, Options{})
	if !strings.Contains(out, "a run with no id") {
		t.Errorf("the preview of an empty tape:\n%s", out)
	}
	if !strings.Contains(out, "recorded no text") {
		t.Errorf("the preview claims text on a tape with none:\n%s", out)
	}
}
