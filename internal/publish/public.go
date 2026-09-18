package publish

import (
	"net/url"
	"path"
	"strings"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// TextPolicy says whether a published tape carries the prompts and the
// generated text. The default is to carry them (docs/toktape-spec.ko.md
// §9.3): replay without words is the service with its subject removed, and a
// default that ships an empty screen is a default nobody wants.
type TextPolicy int

const (
	// WithText is the default: the body travels.
	WithText TextPolicy = iota
	// WithoutText is the machine-wide opt-out (publish_text = false) and the
	// per-upload --no-text.
	WithoutText
)

// PublicView is the tape as it is published: the same run with the things
// nobody meant to share taken out of it.
//
// It is close to the identity function on purpose. The decision is that
// publishing is public by default, so this removes only what could not have
// been anyone's intent to publish — the machine's name, the URL's host, and
// the absolute paths in front of file names. What to strip is not this
// function's judgement: the table in §9.3 is the rule and this is its
// implementation. Anything not in that table is left alone, including the
// user's own tag and note.
//
// The input is never modified. A publisher holds a tape it may still render
// or write back, and a sanitiser that edited in place would quietly change
// the file on disk the next time it was saved.
func PublicView(t *tape.Tape, policy TextPolicy) *tape.Tape {
	if t == nil {
		return nil
	}
	out := *t
	s := &out.Summary

	// A label is something the user wrote in order to share it; a hostname
	// read off the machine is not (TTP-93). The source goes with the value,
	// because a source describing a field that is no longer there is worse
	// than no source at all.
	if s.Host.HostnameSource != tape.HostnameLabelled {
		s.Host.Hostname = ""
		s.Host.HostnameSource = ""
		s.Server.Host = ""
	}
	s.Server.URL = portOnly(s.Server.URL)
	s.Server.Args = sanitiseArgs(s.Server.Args)
	// The file name and the variant directory are the model's identity and
	// stay; everything in front of them is the publisher's disk.
	s.Model.Path = ""
	s.Warnings = sanitiseEach(s.Warnings)

	// The set id is a claim about what was sent, and this is the one place
	// that can check it. The recorder stamps it when it builds the requests
	// and nothing verifies it afterwards, so a tape written by an older
	// binary — or edited — can carry an id whose contents have moved. The
	// same rule as everywhere else in this package: the server never
	// verifies, so the client does, and a claim it cannot confirm is blanked
	// rather than forwarded.
	if s.PromptSet != "" && !sentTheSet(out.Requests, s.Concurrency, s.PromptSet) {
		s.PromptSet = ""
	}

	if policy == WithoutText {
		out.Requests = withoutText(out.Requests)
	} else {
		// Copied even when nothing changes: Requests is a slice, and a caller
		// that later edits the view must not reach through into the original.
		out.Requests = append([]tape.RequestRecord(nil), out.Requests...)
	}
	return &out
}

// sentTheSet reports whether these requests are the published prompt set,
// checked against the set this binary carries.
//
// An id this binary does not know fails: a future prompts@v2 is a set whose
// contents are not here to compare, and forwarding an unverifiable claim is
// the thing this exists to stop. A run recorded without text — one already
// published under publish_text = false, then published again — also fails,
// which is the same answer for the same reason.
//
// A multi-round run sends the same Concurrency prompts every round, so each
// record is compared against the prompt at its own index within its round.
func sentTheSet(recs []tape.RequestRecord, concurrency int, id string) bool {
	if id != server.PromptSetID || concurrency <= 0 || len(recs) == 0 {
		return false
	}
	want := server.DefaultPrompts(concurrency)
	if len(want) != concurrency {
		return false
	}
	for _, r := range recs {
		if r.Index < 0 || r.Index >= concurrency {
			return false
		}
		if len(r.Prompt.Messages) != 1 || len(want[r.Index].Messages) != 1 {
			return false
		}
		if r.Prompt.Messages[0].Content != want[r.Index].Messages[0].Content {
			return false
		}
	}
	return true
}

// withoutText drops the prompt and the answer, and nothing else. The timings,
// the token count and the per-token timestamps are measurements and stay: a
// run published without its words is still a run, and the sparkline that made
// this tool worth building is drawn from the timestamps, not the text.
func withoutText(recs []tape.RequestRecord) []tape.RequestRecord {
	out := make([]tape.RequestRecord, len(recs))
	for i, r := range recs {
		r.Prompt.Messages = nil
		r.Prompt.RenderedPrompt = ""
		r.Prompt.Completion = ""
		r.Prompt.Reasoning = ""
		toks := make([]tape.TokenEvent, len(r.Tokens))
		for j, ev := range r.Tokens {
			ev.Text = ""
			toks[j] = ev
		}
		r.Tokens = toks
		out[i] = r
	}
	return out
}

// portOnly reduces a server URL to its port, because the host is a place and
// the port is a setting. A URL it cannot parse a port out of becomes "":
// half a URL is not a safer URL.
func portOnly(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if p := u.Port(); p != "" {
		return ":" + p
	}
	return ""
}

// sanitiseArgs keeps the argv the card prints and takes the publisher's disk
// out of it. The flags are the record of how the run was configured — a
// stripped argv would take "what flags was this measured with" with it — so
// only the values that look like paths are shortened.
func sanitiseArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = sanitiseToken(a)
	}
	return out
}

func sanitiseEach(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = sanitiseWords(l)
	}
	return out
}

// sanitiseWords shortens every path-shaped word of a sentence. Warnings are
// printed on the card verbatim, so they are prose with a path in them rather
// than a path.
func sanitiseWords(s string) string {
	fields := strings.Fields(s)
	changed := false
	for i, f := range fields {
		// Trailing punctuation belongs to the sentence, not to the path.
		trimmed := strings.TrimRight(f, ".,;:")
		suffix := f[len(trimmed):]
		if clean := sanitiseToken(trimmed); clean != trimmed {
			fields[i], changed = clean+suffix, true
		}
	}
	if !changed {
		return s
	}
	return strings.Join(fields, " ")
}

// sanitiseToken reduces one path-shaped token to its last element. A token
// that names a place is absolute (or a home-relative "~/", or Windows'
// backslash form); a relative path says nothing about where it was, so it is
// left as it is — and a flag like "--flash-attn" starts with "-", never "/".
//
// A value written as --flag=/path keeps its flag and loses only the path, so
// the argv still reads as the argv.
func sanitiseToken(tok string) string {
	if eq := strings.IndexByte(tok, '='); eq > 0 && strings.HasPrefix(tok, "-") {
		return tok[:eq+1] + sanitiseToken(tok[eq+1:])
	}
	slashed := strings.ReplaceAll(tok, `\`, "/")
	switch {
	case strings.HasPrefix(slashed, "/"):
	case strings.HasPrefix(slashed, "~/"):
	default:
		return tok
	}
	base := path.Base(strings.TrimRight(slashed, "/"))
	if base == "/" || base == "." {
		return ""
	}
	return base
}
