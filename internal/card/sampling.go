package card

import (
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// The Sampling row (TTP-55, 2026-09-14).
//
// Measured on DeepSeek-V4.1-Flash Q3_K_M with a DSpark draft, twenty prompts,
// one server: greedy /completion 25.6 tok/s median, server-default sampling on
// /completion 24.5, and the chat path with thinking on 22.4. Three numbers for
// one model on one machine, eleven per cent apart, and nothing on the card
// said which of the three it was showing. The row exists to close that: it
// names the sampling temperature, what was asked of the model's thinking, and
// which endpoint recorded the rate.
//
// Nothing here is inferred from a default. A temperature the recorder did not
// send is the server's own, and the row says "temp default" rather than the
// number that build happens to use — printing 0.8 for a request that never
// carried it is exactly the invented figure the repo's unknown rule forbids.
// Thinking is claimed only when it was turned off, because that is the only
// state the recorder can witness: a request that said nothing leaves the
// decision with the server, and "thinking on" would be our guess about it.

// Sampling is what one run asked of the sampler, the thinking and the path.
// It is the card's own view of the request, small enough that every rule below
// is a pure function of it.
type Sampling struct {
	// Temp is the temperature that was sent, or nil when none was: nil is the
	// server's default, and 0 is greedy.
	Temp *float64
	// Thinking is tape.PromptRecord.Thinking: "off" when the request carried
	// the engine's switch to disable it, "" when it said nothing.
	Thinking string
	// Endpoint is tape.PromptRecord.Endpoint: tape.EndpointCompletion for the
	// raw path, anything else (including "", a tape older than the field) for
	// chat.
	Endpoint string
	// ThoughtAnyway is how many answered streams opened a thinking block
	// after the request asked for thinking off. Above zero it contradicts
	// Thinking, and the row says so rather than repeating the request.
	ThoughtAnyway int
}

// samplingParts is the Sampling row of the speed section: the parts a caller
// joins with " · ".
//
// The lead wires the one call in card.go's speedSection.
func samplingParts(s *tape.RunSummary) []string {
	// No row at all unless the run recorded its endpoint. Every tape this
	// version writes does, chat included; a tape written before the field, or
	// a summary nobody filled, would otherwise have "temp default · chat"
	// invented for it — the endpoint claimed from the fact that chat was once
	// the only path, and the default claimed from a request nobody read. That
	// is the rule this repo breaks least willingly (CLAUDE.md: unknown prints
	// as "?", and never a default you did not observe).
	if s == nil || s.Sampling.Endpoint == "" {
		return nil
	}
	return samplingRow(samplingOf(s))
}

// samplingOf reads the run's sampling out of the summary.
//
// tape.SamplingSummary is the record: the recorder lifts it off the first
// request, since one run sends one shape. The template-kwargs fallback below
// is for a tape written before that field existed — the recorder has always
// copied a request's chat_template_kwargs into TemplateInfo, and that is the
// same switch --no-think sends, so an older --no-think tape still reads
// correctly here.
func samplingOf(s *tape.RunSummary) Sampling {
	out := Sampling{
		Temp:          s.Sampling.Temperature,
		Thinking:      s.Sampling.Thinking,
		Endpoint:      s.Sampling.Endpoint,
		ThoughtAnyway: s.Sampling.ThoughtAnyway,
	}
	if out.Thinking == "" {
		if v, ok := s.Template.TemplateKwargs["enable_thinking"]; ok && v == "false" {
			out.Thinking = "off"
		}
	}
	return out
}

// samplingRow turns one Sampling into the row's parts.
//
// The parts are ordered by how much they change the number: the sampler first,
// then the thinking, then the path. A reader comparing two cards scans down the
// same column, so a part that is omitted is omitted rather than replaced by a
// placeholder — the endpoint is always printed because there is always one, and
// it is the part that makes the other two make sense.
func samplingRow(sm Sampling) []string {
	parts := []string{samplingTemp(sm.Temp)}
	if sm.Thinking == "off" {
		// What was asked, and — when the run's own output disagrees — that it
		// was not honoured. Printing the request alone over a run that
		// visibly reasoned is the card saying something the clip beside it
		// contradicts (TTP-106).
		//
		// "(ignored)" and not a sentence: with the widest temperature and the
		// raw path this row already fills the 54 columns the speed section's
		// gutter leaves it (TestSamplingRowFits), and a row that overflows is
		// wrapped or truncated. The sentence is the caveat's job, and the
		// caveat has the whole width of the card.
		if sm.ThoughtAnyway > 0 {
			parts = append(parts, "thinking off (ignored)")
		} else {
			parts = append(parts, "thinking off")
		}
	}
	parts = append(parts, samplingEndpoint(sm.Endpoint))
	return parts
}

// samplingTemp names the temperature that was sent.
//
// Zero is not "unset" here: `--temp 0` is greedy decoding, the fastest and most
// reproducible setting, and it is the one a benchmark most wants to state. The
// distinction is carried by the pointer, so a run that sent nothing prints the
// card's own word for "the server's default is in effect" and never a number.
func samplingTemp(temp *float64) string {
	if temp == nil {
		return "temp " + serverDefault
	}
	if *temp == 0 {
		return "greedy (temp 0)"
	}
	return "temp " + trimFloat(*temp)
}

// samplingEndpoint names the path that recorded the rate. A blank Endpoint is
// chat: it is a tape written before the field existed, and chat was the only
// path there was.
func samplingEndpoint(endpoint string) string {
	if endpoint == tape.EndpointCompletion {
		return "/" + tape.EndpointCompletion
	}
	return tape.EndpointChat
}

// trimFloat renders a temperature the way the user typed it: 0.7 stays "0.7"
// and 1 stays "1", with no trailing zeros to suggest a precision the flag did
// not carry.
func trimFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
