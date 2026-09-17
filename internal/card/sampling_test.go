package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

func tempOf(v float64) *float64 { return &v }

// TestSamplingRow pins the three rows TTP-55 names, and the rules underneath
// them: a temperature is printed only when it was sent, thinking is claimed
// only when it was turned off, and the endpoint is always named.
func TestSamplingRow(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Sampling
		want string
	}{{
		name: "greedy, no thinking, raw path",
		in:   Sampling{Temp: tempOf(0), Thinking: "off", Endpoint: tape.EndpointCompletion},
		want: "greedy (temp 0) · thinking off · /completion",
	}, {
		name: "a temperature on chat",
		in:   Sampling{Temp: tempOf(0.7), Endpoint: tape.EndpointChat},
		want: "temp 0.7 · chat",
	}, {
		name: "nothing was sent",
		in:   Sampling{},
		want: "temp default · chat",
	}, {
		// A tape written before the field existed. Chat was the only path
		// there was, so naming it is a reading and not a guess.
		name: "a tape older than the field",
		in:   Sampling{Temp: tempOf(1), Endpoint: ""},
		want: "temp 1 · chat",
	}, {
		// The server decided, and the card does not speak for it: there is no
		// "thinking on" here, because nothing observed it.
		name: "thinking left to the server",
		in:   Sampling{Temp: tempOf(0.8), Thinking: "", Endpoint: tape.EndpointChat},
		want: "temp 0.8 · chat",
	}, {
		// TTP-106: the switch was sent and the run reasoned anyway, which is
		// what llama-server does with chat_template_kwargs when it was started
		// without --jinja. The row says what happened, not what was asked.
		name: "thinking off, and the server thought anyway",
		in:   Sampling{Temp: tempOf(0), Thinking: "off", ThoughtAnyway: 4, Endpoint: tape.EndpointChat},
		want: "greedy (temp 0) · thinking off (ignored) · chat",
	}, {
		// Only a positive count asserts anything: a zero is both "no stream
		// reasoned" and "this tape predates the field", and they serialise
		// identically (TTP-103).
		name: "thinking off on a tape older than the count",
		in:   Sampling{Temp: tempOf(0), Thinking: "off", ThoughtAnyway: 0, Endpoint: tape.EndpointChat},
		want: "greedy (temp 0) · thinking off · chat",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(samplingRow(tc.in), " · ")
			if got != tc.want {
				t.Fatalf("samplingRow() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSamplingRowFits: the row lives in the speed section, whose text starts
// after a 14-column label gutter inside a 68-column box. A row that does not
// fit is wrapped or truncated by field(), and a truncated "/completion" would
// leave the reader unable to tell which path recorded the rate.
func TestSamplingRowFits(t *testing.T) {
	avail := innerWidth - speedLabelW
	widest := Sampling{Temp: tempOf(0), Thinking: "off", ThoughtAnyway: 8, Endpoint: tape.EndpointCompletion}
	got := strings.Join(samplingRow(widest), " · ")
	if w := Width(got); w > avail {
		t.Fatalf("row %q is %d columns, the gutter leaves %d", got, w, avail)
	}
}

// TestSamplingTempNeverInventsANumber is the repo's unknown rule applied to
// the sampler: a run that sent no temperature must print the card's word for
// "the server's default is in effect" and never a figure. llama.cpp's own
// default is 0.8, and printing that for a request that never carried it is
// exactly the number nobody observed.
func TestSamplingTempNeverInventsANumber(t *testing.T) {
	got := samplingTemp(nil)
	if got != "temp default" {
		t.Fatalf("samplingTemp(nil) = %q, want %q", got, "temp default")
	}
	if strings.ContainsAny(got, "0123456789") {
		t.Fatalf("samplingTemp(nil) = %q: it names a number that was not sent", got)
	}
	// Zero is a sent value, not an absent one. --temp 0 is greedy decoding,
	// which is the setting a benchmark most wants to state.
	if got := samplingTemp(tempOf(0)); got != "greedy (temp 0)" {
		t.Fatalf("samplingTemp(0) = %q, want %q", got, "greedy (temp 0)")
	}
}

// TestTrimFloat: a temperature reads as the user typed it, with no trailing
// zeros suggesting a precision the flag did not carry.
func TestTrimFloat(t *testing.T) {
	for in, want := range map[float64]string{
		0.7:  "0.7",
		1:    "1",
		0.25: "0.25",
		1.5:  "1.5",
	} {
		if got := trimFloat(in); got != want {
			t.Fatalf("trimFloat(%v) = %q, want %q", in, got, want)
		}
	}
}

// TestSamplingPartsReadsTheSummary: what the card says from a RunSummary
// alone. tape.SamplingSummary is the record; the template-kwargs fallback
// covers a tape whose thinking switch reached only TemplateInfo.
//
// The row is absent entirely without a recorded endpoint (2026-09-14): on a
// summary nobody filled, "temp default · chat" would claim a default from a
// request nobody read and a path from the fact that chat was once the only
// one. TestUnsetFlagPrintsDefaultOnceTheArgvWasRead is the gate that found it.
func TestSamplingPartsReadsTheSummary(t *testing.T) {
	off := &tape.RunSummary{
		Sampling: tape.SamplingSummary{Endpoint: tape.EndpointChat},
		Template: tape.TemplateInfo{TemplateKwargs: map[string]string{"enable_thinking": "false"}},
	}
	if got := strings.Join(samplingParts(off), " · "); got != "temp default · thinking off · chat" {
		t.Fatalf("samplingParts(--no-think tape) = %q", got)
	}

	on := &tape.RunSummary{
		Sampling: tape.SamplingSummary{Endpoint: tape.EndpointChat},
		Template: tape.TemplateInfo{TemplateKwargs: map[string]string{"enable_thinking": "true"}},
	}
	if got := strings.Join(samplingParts(on), " · "); got != "temp default · chat" {
		t.Fatalf("samplingParts(thinking on) = %q: the card must not claim what the server decided", got)
	}

	greedy := 0.0
	raw := &tape.RunSummary{Sampling: tape.SamplingSummary{
		Temperature: &greedy, Thinking: "off", Endpoint: tape.EndpointCompletion,
	}}
	if got := strings.Join(samplingParts(raw), " · "); got != "greedy (temp 0) · thinking off · /completion" {
		t.Fatalf("samplingParts(raw greedy tape) = %q", got)
	}

	// A tape that recorded no endpoint says nothing rather than guessing.
	old := &tape.RunSummary{Template: tape.TemplateInfo{
		TemplateKwargs: map[string]string{"enable_thinking": "false"},
	}}
	if got := samplingParts(old); got != nil {
		t.Fatalf("samplingParts(a tape with no endpoint) = %v, want nil", got)
	}
	if got := samplingParts(nil); got != nil {
		t.Fatalf("samplingParts(nil) = %v, want nil", got)
	}
}
