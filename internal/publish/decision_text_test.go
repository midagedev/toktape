package publish

import (
	"reflect"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

func decisionExample(t *testing.T) *tape.Tape {
	t.Helper()
	tp, err := tape.Read("../tape/testdata/decision-example.tape")
	if err != nil {
		t.Fatal(err)
	}
	if len(tp.Decisions) == 0 {
		t.Fatal("the decision example has no decision records")
	}
	return tp
}

func bodyBytes(tp *tape.Tape) int {
	n := 0
	for _, d := range tp.Decisions {
		n += len(d.Request) + len(d.Response)
		for _, q := range d.Questions {
			n += len(q.Instructions)
			for _, o := range q.Options {
				n += len(o.Text)
			}
		}
	}
	return n
}

// TestPreviewNamesTheDecisionBodies (TTP-197): a decision tape carries every
// request and response body verbatim, so the text row says how many and how
// big instead of "nothing".
func TestPreviewNamesTheDecisionBodies(t *testing.T) {
	out := previewOf(t, decisionExample(t), Options{})
	if !strings.Contains(out, "request bodies") || !strings.Contains(out, "response bodies") {
		t.Errorf("the preview does not name the bodies that travel:\n%s", out)
	}
	if strings.Contains(out, "this run recorded no text") {
		t.Errorf("the preview says a decision run has no text:\n%s", out)
	}
}

// --no-text takes the bodies home and nothing else: the index is the same.
func TestNoTextStripsDecisionBodies(t *testing.T) {
	tp := decisionExample(t)
	with := PublicView(tp, WithText)
	without := PublicView(tp, WithoutText)
	if bodyBytes(with) == 0 {
		t.Fatal("the fixture has no bodies to strip")
	}
	if n := bodyBytes(without); n != 0 {
		t.Errorf("%d body bytes survive --no-text", n)
	}
	if len(without.Decisions) != len(with.Decisions) {
		t.Errorf("records %d, want %d", len(without.Decisions), len(with.Decisions))
	}
	for i, d := range without.Decisions {
		w := with.Decisions[i]
		if d.SentAt != w.SentAt || d.AnsweredAt != w.AnsweredAt || d.Error != w.Error ||
			d.InputTokens != w.InputTokens || len(d.Answers) != len(w.Answers) || len(d.Questions) != len(w.Questions) {
			t.Errorf("record %d lost a figure: %+v vs %+v", i, d, w)
		}
	}
	if a, b := IndexOf(with), IndexOf(without); !reflectEqual(a, b) {
		t.Errorf("the index changed under --no-text:\n%+v\n%+v", a, b)
	}
	// The input is never modified.
	if bodyBytes(tp) == 0 {
		t.Error("PublicView stripped the caller's tape")
	}
	out := previewOf(t, tp, Options{Text: WithoutText})
	if !strings.Contains(out, "bodies stay") {
		t.Errorf("the preview does not say the bodies stay home:\n%s", out)
	}
}

// A token tape's text row is what it was.
func TestTokenTapeTextLineUnchanged(t *testing.T) {
	out := previewOf(t, fullTape(), Options{})
	if !strings.Contains(out, "characters of prompt and") || strings.Contains(out, "bodies") {
		t.Errorf("a token tape's text row changed:\n%s", out)
	}
	out = previewOf(t, fullTape(), Options{Text: WithoutText})
	want := "  text           nothing · the prompts and the generated text are removed, the timings stay\n"
	if !strings.Contains(out, want) {
		t.Errorf("a token tape's no-text row changed:\n%s", out)
	}
}

func reflectEqual(a, b any) bool { return reflect.DeepEqual(a, b) }
