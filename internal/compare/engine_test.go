package compare_test

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/compare"
	"github.com/midagedev/toktape/internal/tape"
)

// TestEngineUnknownPrintsQuestionMark (TTP-37): an engine toktape could not
// identify prints "?" on the compare's run section, never the word "unknown",
// and "" and tape.ServerUnknown are the same unknown rather than a change.
func TestEngineUnknownPrintsQuestionMark(t *testing.T) {
	a, b := card.Example(), card.Example()
	a.Server.Kind = tape.ServerUnknown
	text := compare.Text(compare.Diff(a, b))
	var line string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "engine") {
			line = l
		}
	}
	if !strings.Contains(line, "? → llama-server") {
		t.Errorf("engine line = %q, want ? → llama-server\n%s", line, text)
	}
	if strings.Contains(text, "unknown") {
		t.Errorf("the compare prints the word unknown:\n%s", text)
	}

	a, b = card.Example(), card.Example()
	a.Server.Kind, b.Server.Kind = "", tape.ServerUnknown
	// A change line starts with its label; "(same model, build and engine)"
	// also carries the word, and is what an unchanged run prints.
	text = compare.Text(compare.Diff(a, b))
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "engine") {
			t.Errorf("two unknown engines were reported as a change: %q\n%s", l, text)
		}
	}
}
