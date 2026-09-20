package publish

import (
	"encoding/json"
	"net/url"
	"path"
	"reflect"
	"regexp"
	"strings"
	"time"

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
// been anyone's intent to publish — the machine's name, the URL's host, the
// absolute paths in front of file names, and the UTC offset off every
// timestamp (a published tape tells the instant in UTC, TTP-120). What to strip is not this
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
	out := deepCopy(t)
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
	// The parsed flags, which are a second copy of the same argv and the one
	// the card actually prints. `Other` holds whole flag-and-value strings
	// verbatim ("-m /home/k/models/…"), so it is shortened word by word the
	// way a warning is. Missing this is what put a publisher's model path
	// into the share card — the one artifact that cannot be taken back once
	// it is posted (2026-09-18).
	s.Server.Flags.Other = sanitiseEach(s.Server.Flags.Other)
	// Documented as a base name already, and shortened anyway: sanitiseToken
	// leaves a base name alone, so this costs nothing and stops depending on
	// every engine's parser having got it right.
	s.Server.Flags.DraftModel = sanitiseToken(s.Server.Flags.DraftModel)
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
	if s.PromptSet != "" && !sentTheSet(out.Requests, s.Concurrency, s.PromptSet, s.PromptTrimChars) {
		s.PromptSet = ""
	}

	if policy == WithoutText {
		out.Requests = withoutText(out.Requests)
	}
	// A published tape tells the instant, never the time zone (§9.3,
	// TTP-120): every time.Time becomes UTC, and the run id's leading local
	// wall-clock gives way to the same UTC instant. Both run on the copy.
	normaliseTimesToUTC(reflect.ValueOf(out))
	rewritePublicID(out)
	return out
}

// deepCopy duplicates a tape through its JSON shape: the struct is defined
// by its JSON, so a marshal round trip copies every slice and map, and a
// field added next year is copied the day it is added without anyone
// thinking of it.
//
// PublicView cannot return an error, so on a marshal failure this falls
// back to the shallow copy the function used to do and records nothing — a
// tape that cannot marshal cannot be uploaded either, and the upload path
// is where that error belongs.
func deepCopy(t *tape.Tape) *tape.Tape {
	raw, err := json.Marshal(t)
	if err != nil {
		out := *t
		return &out
	}
	var out tape.Tape
	if err := json.Unmarshal(raw, &out); err != nil {
		shallow := *t
		return &shallow
	}
	return &out
}

// utcTimeType is what the walk below recognises: the time.Time struct
// itself, not an alias, so a Duration (an int64) never matches.
var utcTimeType = reflect.TypeOf(time.Time{})

// normaliseTimesToUTC rewrites every time.Time under v to UTC, in place.
// The walk is structural — structs, pointers, interfaces, slices, arrays
// and map values — so Summary.StartedAt, per-request and per-round stamps,
// witness edges, GPU samples and anything added next year are all one rule,
// and no hand-written field list can go stale. A map value is not
// addressable, so the entry is rebuilt (m.SetMapIndex). Unexported fields
// are skipped (CanSet).
func normaliseTimesToUTC(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		if v.Type() == utcTimeType {
			if v.CanSet() {
				v.Set(reflect.ValueOf(v.Interface().(time.Time).UTC()))
			}
			return
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath == "" {
				normaliseTimesToUTC(v.Field(i))
			}
		}
	case reflect.Ptr:
		if !v.IsNil() {
			normaliseTimesToUTC(v.Elem())
		}
	case reflect.Interface:
		if !v.IsNil() {
			if e := v.Elem(); e.Type() == utcTimeType && v.CanSet() {
				v.Set(reflect.ValueOf(e.Interface().(time.Time).UTC()))
			} else {
				normaliseTimesToUTC(e)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			normaliseTimesToUTC(v.Index(i))
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			mv := v.MapIndex(k)
			cp := reflect.New(mv.Type()).Elem()
			cp.Set(mv)
			normaliseTimesToUTC(cp)
			v.SetMapIndex(k, cp)
		}
	}
}

// publicIDPrefix matches the leading "<yyyymmdd>-<hhmmss>-" of a recorder
// run id (internal/tape/tape.go:114).
var publicIDPrefix = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-`)

// rewritePublicID moves the run id's leading time part from local
// wall-clock to the UTC instant (§9.3, TTP-120): leaving it would publish
// the offset the timestamps just lost — a reader subtracts the id's
// 144056 from the UTC 05:40:56 and has +09:00. Both parts are derived from
// Summary.StartedAt.UTC(), never by arithmetic on the string, so a UTC
// date across midnight moves the date too. Only the two leading groups
// change; the slug tail travels byte for byte. An id this function does
// not understand — including "" — and a zero StartedAt leave the id
// exactly as it is.
func rewritePublicID(out *tape.Tape) {
	s := &out.Summary
	if s.StartedAt.IsZero() || len(s.ID) <= 16 || !publicIDPrefix.MatchString(s.ID) {
		return
	}
	s.ID = s.StartedAt.UTC().Format("20060102-150405") + s.ID[15:]
}

// sentTheSet reports whether these requests are the published prompt set,
// checked against the set this binary carries.
//
// A run may have sent a prefix of each set prompt rather than the whole
// thing (RunSummary.PromptTrimChars, TTP-144): the machine's prefill
// measured, the length chosen from the measurement. So the comparison is
// against this binary's own copy trimmed the same way — the recorded count
// says how much of its own copy the verifier cuts, and what was sent must
// be exactly that, byte for byte, still. A count that overruns this
// binary's copy is the same answer as a mismatch: a claim the verifier
// cannot reproduce.
//
// An id this binary does not know fails: a future prompts@v3 is a set whose
// contents are not here to compare, and forwarding an unverifiable claim is
// the thing this exists to stop. A run recorded without text — one already
// published under publish_text = false, then published again — also fails,
// which is the same answer for the same reason.
//
// A multi-round run sends the same Concurrency prompts every round, so each
// record is compared against the prompt at its own index within its round.
func sentTheSet(recs []tape.RequestRecord, concurrency int, id string, trimChars int) bool {
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
		wantText := want[r.Index].Messages[0].Content
		if trimChars > 0 {
			runes := []rune(wantText)
			if trimChars > len(runes) {
				return false
			}
			wantText = string(runes[:trimChars])
		}
		if r.Prompt.Messages[0].Content != wantText {
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
