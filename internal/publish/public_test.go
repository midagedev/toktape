package publish

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// fullTape is a run with one of everything the public view has a rule about.
func fullTape() *tape.Tape {
	s := *card.Example()
	s.Host.Hostname = "ws"
	s.Host.HostnameSource = tape.HostnameObserved
	s.Server.Host = "ws"
	s.Server.URL = "http://192.168.1.44:8080"
	s.Server.Args = []string{
		"/opt/llama.cpp/build/bin/llama-server",
		"-m", "/home/k/models/Qwen3-0.6B/Qwen3-0.6B-Q8_0.gguf",
		"--flash-attn",
		"-ngl=99",
		"--lora=/home/k/loras/style.gguf",
		"-c", "32768",
	}
	s.Model.Path = "/home/k/models/Qwen3-0.6B/Qwen3-0.6B-Q8_0.gguf"
	s.Model.FileName = "Qwen3-0.6B-Q8_0.gguf"
	s.Model.Dir = "Qwen3-0.6B"
	s.Warnings = []string{"model file not readable here: /home/k/models/Qwen3-0.6B/Qwen3-0.6B-Q8_0.gguf."}
	s.Tag = "ngl=99"
	s.Note = "fa on"
	return &tape.Tape{
		Summary: s,
		Requests: []tape.RequestRecord{{
			Index: 0,
			Prompt: tape.PromptRecord{
				Messages:   []tape.Message{{Role: "user", Content: "hello"}},
				Completion: "hi there",
			},
			Tokens: []tape.TokenEvent{{Index: 0, Text: "hi"}, {Index: 1, Text: " there"}},
		}},
	}
}

func TestPublicViewStripsThePlaceAndKeepsTheRun(t *testing.T) {
	in := fullTape()
	out := PublicView(in, WithText)
	s := out.Summary

	if s.Host.Hostname != "" || s.Host.HostnameSource != "" || s.Server.Host != "" {
		t.Errorf("an observed hostname survived: %q / %q / %q",
			s.Host.Hostname, s.Host.HostnameSource, s.Server.Host)
	}
	if s.Server.URL != ":8080" {
		t.Errorf("Server.URL = %q, want \":8080\" — the host is a place, the port is a setting", s.Server.URL)
	}
	if s.Model.Path != "" {
		t.Errorf("Model.Path = %q, want empty", s.Model.Path)
	}
	// The identity of the model is the file name and the variant directory,
	// and taking the path must not take them too.
	if s.Model.FileName != "Qwen3-0.6B-Q8_0.gguf" || s.Model.Dir != "Qwen3-0.6B" {
		t.Errorf("file name / dir = %q / %q, want them kept", s.Model.FileName, s.Model.Dir)
	}
	wantArgs := []string{"llama-server", "-m", "Qwen3-0.6B-Q8_0.gguf", "--flash-attn", "-ngl=99", "--lora=style.gguf", "-c", "32768"}
	if strings.Join(s.Server.Args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("Server.Args = %q,\n            want %q", s.Server.Args, wantArgs)
	}
	if got := s.Warnings[0]; got != "model file not readable here: Qwen3-0.6B-Q8_0.gguf." {
		t.Errorf("warning = %q: the path went, the sentence and its full stop should not have", got)
	}
	// The user's own words about their own experiment.
	if s.Tag != "ngl=99" || s.Note != "fa on" {
		t.Errorf("tag / note = %q / %q, want them kept", s.Tag, s.Note)
	}
	// Public by default: the body is the subject of the service.
	if out.Requests[0].Prompt.Completion != "hi there" || out.Requests[0].Tokens[0].Text != "hi" {
		t.Errorf("the text was dropped on a default publish: %+v", out.Requests[0].Prompt)
	}
}

// A label is what somebody chose to publish, so it is the one hostname that
// survives — the reason --host-label exists at all (TTP-93).
func TestPublicViewKeepsALabel(t *testing.T) {
	in := fullTape()
	in.Summary.Host.Hostname = "workstation"
	in.Summary.Host.HostnameSource = tape.HostnameLabelled
	in.Summary.Server.Host = "workstation"

	s := PublicView(in, WithText).Summary
	if s.Host.Hostname != "workstation" || s.Host.HostnameSource != tape.HostnameLabelled {
		t.Errorf("a label was stripped: %q / %q", s.Host.Hostname, s.Host.HostnameSource)
	}
	if s.Server.Host != "workstation" {
		t.Errorf("Server.Host = %q, want the label", s.Server.Host)
	}
}

// The opt-out drops the words and nothing else: the timings and the per-token
// timestamps are measurements, and the sparkline is drawn from those.
func TestPublicViewWithoutText(t *testing.T) {
	out := PublicView(fullTape(), WithoutText)
	r := out.Requests[0]

	if r.Prompt.Messages != nil || r.Prompt.Completion != "" || r.Prompt.Reasoning != "" {
		t.Errorf("text survived --no-text: %+v", r.Prompt)
	}
	for i, ev := range r.Tokens {
		if ev.Text != "" {
			t.Errorf("token %d kept its text %q", i, ev.Text)
		}
	}
	if len(r.Tokens) != 2 {
		t.Errorf("%d tokens, want 2: the timestamps are the measurement", len(r.Tokens))
	}
}

// The input is a tape the caller may still render or write back to disk.
func TestPublicViewDoesNotEditItsInput(t *testing.T) {
	in := fullTape()
	before := in.Summary.Model.Path
	firstArg := in.Summary.Server.Args[0]
	firstToken := in.Requests[0].Tokens[0].Text

	PublicView(in, WithoutText)

	if in.Summary.Model.Path != before || in.Summary.Server.Args[0] != firstArg {
		t.Errorf("PublicView edited its input: path %q, arg %q", in.Summary.Model.Path, in.Summary.Server.Args[0])
	}
	if in.Requests[0].Tokens[0].Text != firstToken {
		t.Errorf("PublicView cleared a token in its input: %q", in.Requests[0].Tokens[0].Text)
	}
}

// TestPublicViewVerifiesThePromptSet: the id is a claim about what was sent,
// and the recorder stamps it without anything checking it afterwards. This is
// the last place that can, so an unverifiable claim is blanked rather than
// forwarded to the index.
func TestPublicViewVerifiesThePromptSet(t *testing.T) {
	setTape := func(n int, mutate func([]tape.RequestRecord)) *tape.Tape {
		tp := &tape.Tape{}
		tp.Summary.Concurrency = n
		tp.Summary.PromptSet = server.PromptSetID
		for i, req := range server.DefaultPrompts(n) {
			tp.Requests = append(tp.Requests, tape.RequestRecord{
				Index:  i,
				Prompt: tape.PromptRecord{Messages: req.Messages},
			})
		}
		if mutate != nil {
			mutate(tp.Requests)
		}
		return tp
	}

	if got := PublicView(setTape(4, nil), WithText).Summary.PromptSet; got != server.PromptSetID {
		t.Errorf("PromptSet = %q on a run that did send the set, want %q", got, server.PromptSetID)
	}

	// A multi-round run sends the same prompts again, each record carrying
	// its index within its round.
	rounds := setTape(2, nil)
	rounds.Requests = append(rounds.Requests, rounds.Requests[0], rounds.Requests[1])
	rounds.Requests[2].Round, rounds.Requests[3].Round = 1, 1
	if got := PublicView(rounds, WithText).Summary.PromptSet; got != server.PromptSetID {
		t.Errorf("PromptSet = %q on a two-round run of the set, want %q", got, server.PromptSetID)
	}

	for _, c := range []struct {
		name string
		tp   *tape.Tape
	}{{
		name: "one prompt edited",
		tp: setTape(4, func(r []tape.RequestRecord) {
			r[2].Prompt.Messages[0].Content += " Answer in Korean."
		}),
	}, {
		name: "the prompts sent in another order",
		tp: setTape(4, func(r []tape.RequestRecord) {
			r[0].Prompt.Messages, r[1].Prompt.Messages = r[1].Prompt.Messages, r[0].Prompt.Messages
		}),
	}, {
		name: "the text already gone, so there is nothing to check against",
		tp: setTape(4, func(r []tape.RequestRecord) {
			for i := range r {
				r[i].Prompt.Messages = nil
			}
		}),
	}, {
		name: "an id from a version this binary does not carry",
		tp: func() *tape.Tape {
			tp := setTape(4, nil)
			tp.Summary.PromptSet = "prompts@v99"
			return tp
		}(),
	}, {
		name: "no requests at all",
		tp: func() *tape.Tape {
			tp := &tape.Tape{}
			tp.Summary.Concurrency = 4
			tp.Summary.PromptSet = server.PromptSetID
			return tp
		}(),
	}} {
		t.Run(c.name, func(t *testing.T) {
			if got := PublicView(c.tp, WithText).Summary.PromptSet; got != "" {
				t.Errorf("PromptSet = %q, want \"\": the claim could not be checked", got)
			}
		})
	}
}

// TestPublicViewOnTheHeroTape is the fixture check: the repo's own published
// tape, through the function that decides what a published tape looks like.
// It was recorded before prompts@v1 existed, so it is also the real case the
// verification above exists for.
func TestPublicViewOnTheHeroTape(t *testing.T) {
	path := filepath.Join("..", "..", "assets", "hero.tape")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("hero tape not present: %v", err)
	}
	tp, err := tape.Read(path)
	if err != nil {
		t.Fatalf("load hero tape: %v", err)
	}
	out := PublicView(tp, WithText)

	if out.Summary.PromptSet != "" {
		t.Errorf("PromptSet = %q: the hero predates the published set and belongs to no comparison set", out.Summary.PromptSet)
	}
	if out.Summary.Model.Path != "" {
		t.Errorf("Model.Path = %q", out.Summary.Model.Path)
	}
	if out.Summary.Host.HostnameSource == tape.HostnameObserved {
		t.Errorf("an observed hostname survived on the hero tape")
	}
	for i, a := range out.Summary.Server.Args {
		if strings.HasPrefix(a, "/") || strings.HasPrefix(a, "~/") {
			t.Errorf("arg %d is still an absolute path: %q", i, a)
		}
	}
	// The card must still render from the view — a sanitiser that took a
	// figure with the paths would be a different bug with the same commit.
	if len(out.Requests) != len(tp.Requests) {
		t.Errorf("%d requests, want %d", len(out.Requests), len(tp.Requests))
	}
	if IndexOf(out).ModelRaw == "" {
		t.Error("the published index lost the model's name along with its path")
	}
}

// TestNoPlaceSurvivesAnywhereInTheSummary walks every string in the published
// summary and fails on anything still shaped like a path.
//
// It exists because the field-by-field tests above only cover the fields
// somebody remembered. The defect that prompted it was exactly that: §9.3's
// table names `server.args`, and the recorder also keeps `server.flags.other`
// — the verbatim flags the card prints — which nothing sanitised. The
// publisher's model path travelled inside it and, worse, was drawn into the
// share card, which is the artifact most likely to be posted somewhere it
// cannot be taken back (found 2026-09-18, while looking at a rendered page).
//
// So the assertion is structural: a new string field on RunSummary is covered
// by this the day it is added, without anyone thinking of it. Requests are out
// of scope — the prompts and the generated text are the user's own words and
// §9.3 says they travel verbatim — and the two fields below are exempt for the
// same reason.
func TestNoPlaceSurvivesAnywhereInTheSummary(t *testing.T) {
	// The user's own words, kept verbatim by §9.3's table.
	exempt := map[string]bool{".Tag": true, ".Note": true}

	in := fullTape()
	in.Summary.Server.Flags.Other = []string{
		"-m /home/k/models/Qwen3-0.6B/Qwen3-0.6B-Q8_0.gguf",
		"--mmproj ~/models/mmproj.gguf",
	}
	in.Summary.Server.Flags.DraftModel = "/home/k/models/draft/Qwen3-0.6B-Q4_0.gguf"

	out := PublicView(in, WithText)
	walkStrings(reflect.ValueOf(out.Summary), "", func(path, s string) {
		if exempt[path] {
			return
		}
		for _, word := range strings.Fields(s) {
			if looksLikeAPlace(word) {
				t.Errorf("summary%s still carries a path: %q\n  in: %q", path, word, s)
			}
		}
	})
}

// looksLikeAPlace is §9.3's own rule, read back: a token that names a place
// is absolute, home-relative, or written with backslashes.
func looksLikeAPlace(word string) bool {
	if eq := strings.IndexByte(word, '='); eq > 0 && strings.HasPrefix(word, "-") {
		word = word[eq+1:]
	}
	word = strings.TrimRight(word, ".,;:")
	return strings.HasPrefix(word, "/") || strings.HasPrefix(word, "~/") || strings.Contains(word, `\`)
}

// loadHeroTape reads the repo's own published recording, which carries a
// +09:00 offset on its timestamps (TTP-120, 2026-09-19).
func loadHeroTape(t *testing.T) *tape.Tape {
	t.Helper()
	tp, err := tape.Read(filepath.Join("..", "..", "assets", "hero.tape"))
	if err != nil {
		t.Fatalf("load hero tape: %v", err)
	}
	return tp
}

// offsetPattern matches any JSON timestamp still carrying a numeric UTC
// offset — the thing §9.3 forbids a published tape to contain. "Z" does not
// match; "+09:00" and "-04:00" do.
var offsetPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T[^"]*[+-]\d{2}:\d{2}`)

// TestPublicViewCarriesNoTimezoneOffset is the recurrence gate for TTP-120:
// a published tape tells the instant, never the time zone. The assertion is
// structural — a regexp over the marshalled view — so a time.Time field
// added next year is covered the day it is added, without anyone thinking
// of it.
func TestPublicViewCarriesNoTimezoneOffset(t *testing.T) {
	tp := loadHeroTape(t)
	raw, err := json.Marshal(tp)
	if err != nil {
		t.Fatalf("marshal hero: %v", err)
	}
	if !strings.Contains(string(raw), "+09:00") {
		t.Fatalf("fixture carries no offset, so this gate proves nothing: %s", tp.Summary.StartedAt.Format(time.RFC3339))
	}

	view := PublicView(tp, WithText)
	out, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal public view: %v", err)
	}
	if m := offsetPattern.Find(out); m != nil {
		t.Errorf("published view still carries a UTC offset: %s", m)
	}

	// The search row is derived from the view (cmd/toktape/publish.go), so
	// recorded_at follows automatically — asserted, not assumed.
	idxRaw, err := json.Marshal(IndexOf(view))
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	if m := offsetPattern.Find(idxRaw); m != nil {
		t.Errorf("published index still carries a UTC offset: %s", m)
	}
}

// TestPublicViewDeepCopiesNestedSlices: the doc comment promises the input
// is never modified, and a shallow copy keeps every slice and map as the
// caller's memory. A timestamp walk writing into those would break it.
func TestPublicViewDeepCopiesNestedSlices(t *testing.T) {
	kst := time.FixedZone("KST", 9*3600)
	started := time.Date(2026, 9, 17, 14, 40, 56, 0, kst)
	in := fullTape()
	in.Summary.StartedAt = started
	in.Summary.FinishedAt = started.Add(time.Minute)
	in.Summary.ID = "20260917-144056-slug"
	in.Samples = []tape.RunSample{{
		T:        time.Second,
		LoadAvg1: 1.5,
		GPUs:     []tape.GPUSample{{Index: 0, UsedBytes: 7}},
	}}
	in.Summary.Host.GPUs = []tape.GPUInfo{{Index: 0, Name: "RTX 5090", VRAMBytes: 1}}
	in.Summary.GPUsAtEnd = []tape.GPUSample{{Index: 0, UsedBytes: 7}}
	in.Summary.Placement.Devices = []tape.DevicePlacement{{
		Device:  tape.DeviceCPU,
		Classes: map[tape.TensorClass]int64{tape.ClassFFN: 3},
	}}
	in.Summary.PerRound = []tape.RoundSummary{{Index: 0, Streams: 1}}

	view := PublicView(in, WithText)
	view.Requests[0].Prompt.Completion = "changed"
	view.Requests[0].Tokens[0].Text = "changed"
	view.Samples[0].GPUs[0].UsedBytes = 999
	view.Summary.Host.GPUs[0].Name = "changed"
	view.Summary.GPUsAtEnd[0].UsedBytes = 999
	view.Summary.Placement.Devices[0].Classes[tape.ClassFFN] = 999
	view.Summary.PerRound[0].Streams = 999

	if in.Requests[0].Prompt.Completion != "hi there" {
		t.Errorf("view write reached the input's requests: %q", in.Requests[0].Prompt.Completion)
	}
	if in.Requests[0].Tokens[0].Text != "hi" {
		t.Errorf("view write reached the input's tokens: %q", in.Requests[0].Tokens[0].Text)
	}
	if in.Samples[0].GPUs[0].UsedBytes != 7 {
		t.Errorf("view write reached the input's samples: %d", in.Samples[0].GPUs[0].UsedBytes)
	}
	if in.Summary.Host.GPUs[0].Name != "RTX 5090" {
		t.Errorf("view write reached the input's GPUs: %q", in.Summary.Host.GPUs[0].Name)
	}
	if in.Summary.GPUsAtEnd[0].UsedBytes != 7 {
		t.Errorf("view write reached the input's end GPUs: %d", in.Summary.GPUsAtEnd[0].UsedBytes)
	}
	if in.Summary.Placement.Devices[0].Classes[tape.ClassFFN] != 3 {
		t.Errorf("view write reached the input's placement map: %d", in.Summary.Placement.Devices[0].Classes[tape.ClassFFN])
	}
	if in.Summary.PerRound[0].Streams != 1 {
		t.Errorf("view write reached the input's per-round: %d", in.Summary.PerRound[0].Streams)
	}
	// And the input's own timestamps still carry their offset.
	if _, off := in.Summary.StartedAt.Zone(); off != 9*3600 {
		t.Errorf("the input's StartedAt lost its offset: %s", in.Summary.StartedAt.Format(time.RFC3339))
	}
}

// TestPublicViewKeepsTheInstantInUTC: normalising the zone must move the
// clock's label, never the moment it names.
func TestPublicViewKeepsTheInstantInUTC(t *testing.T) {
	kst := time.FixedZone("KST", 9*3600)
	in := fullTape()
	in.Summary.StartedAt = time.Date(2026, 9, 17, 14, 40, 56, 891876495, kst)
	in.Summary.FinishedAt = time.Date(2026, 9, 17, 14, 41, 8, 633343253, kst)

	view := PublicView(in, WithText)
	for _, c := range []struct {
		name string
		got  time.Time
		want time.Time
	}{
		{"started", view.Summary.StartedAt, in.Summary.StartedAt},
		{"finished", view.Summary.FinishedAt, in.Summary.FinishedAt},
	} {
		if !c.got.Equal(c.want) {
			t.Errorf("%s moved the instant: %s, want %s", c.name, c.got.Format(time.RFC3339Nano), c.want.Format(time.RFC3339Nano))
		}
		if c.got.Location() != time.UTC {
			t.Errorf("%s is in %s, want UTC", c.name, c.got.Location())
		}
	}
}

// TestPublicViewRewritesIDToUTC: the run id's leading time part is local
// wall-clock, which would publish the offset the timestamps just lost — a
// reader subtracts the id's 144056 from the UTC 05:40:56 and has +09:00.
func TestPublicViewRewritesIDToUTC(t *testing.T) {
	kst := time.FixedZone("KST", 9*3600)
	for _, c := range []struct {
		name    string
		id      string
		started time.Time
		want    string
	}{
		{
			name:    "the hero's own id",
			id:      "20260917-144056-qwen3-6-35b-a3b-ud-q6-k",
			started: time.Date(2026, 9, 17, 14, 40, 56, 0, kst),
			want:    "20260917-054056-qwen3-6-35b-a3b-ud-q6-k",
		},
		{
			name:    "UTC crosses midnight backwards, so the date moves too",
			id:      "20260917-083000-slug",
			started: time.Date(2026, 9, 17, 8, 30, 0, 0, kst),
			want:    "20260916-233000-slug",
		},
		{
			name:    "an id this function does not understand is untouched",
			id:      "my-custom-run",
			started: time.Date(2026, 9, 17, 14, 40, 56, 0, kst),
			want:    "my-custom-run",
		},
		{
			name:    "an empty id stays empty",
			id:      "",
			started: time.Date(2026, 9, 17, 14, 40, 56, 0, kst),
			want:    "",
		},
		{
			name:    "a zero StartedAt leaves the id alone",
			id:      "20260917-144056-slug",
			started: time.Time{},
			want:    "20260917-144056-slug",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := fullTape()
			in.Summary.ID = c.id
			in.Summary.StartedAt = c.started
			if got := PublicView(in, WithText).Summary.ID; got != c.want {
				t.Errorf("ID = %q, want %q", got, c.want)
			}
			if in.Summary.ID != c.id {
				t.Errorf("the input's ID moved: %q, want %q", in.Summary.ID, c.id)
			}
		})
	}
}

// TestDeepCopyIsFaithful: the JSON round trip the public view copies through
// must lose no field — a published tape already losing one would be the
// defect, not the test.
func TestDeepCopyIsFaithful(t *testing.T) {
	tp := loadHeroTape(t)
	if got := deepCopy(tp); !reflect.DeepEqual(tp, got) {
		t.Errorf("deepCopy lost a field of the hero tape")
	}
}

func walkStrings(v reflect.Value, path string, fn func(path, s string)) {
	switch v.Kind() {
	case reflect.String:
		if s := v.String(); s != "" {
			fn(path, s)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				walkStrings(v.Field(i), path+"."+v.Type().Field(i).Name, fn)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			walkStrings(v.Index(i), fmt.Sprintf("%s[%d]", path, i), fn)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			walkStrings(v.MapIndex(k), fmt.Sprintf("%s[%v]", path, k.Interface()), fn)
		}
	case reflect.Ptr, reflect.Interface:
		if !v.IsNil() {
			walkStrings(v.Elem(), path, fn)
		}
	}
}

// A run that sent a prefix of each set prompt is still the set, when the
// recorded count says exactly how much of it (TTP-144, lead, 2026-09-20).
// The verifier cuts its own copy by the same count and the byte-for-byte
// comparison stands on what was sent. Three cases: the honest trimmed run
// passes; a trimmed run whose summary claims 0 sent-whole fails; one
// character off either way fails. The first case is the FAIL this gate was
// written on — before the count reached the comparison, every trimmed run
// read as "not the set".
func TestPublicViewAcceptsATrimmedRunWithItsCountAndNothingElse(t *testing.T) {
	const n, trim = 4, 8000
	trimmed := &tape.Tape{}
	trimmed.Summary.Concurrency = n
	trimmed.Summary.PromptSet = server.PromptSetID
	trimmed.Summary.PromptTrimChars = trim
	for i, req := range server.DefaultPrompts(n) {
		trimmed.Requests = append(trimmed.Requests, tape.RequestRecord{
			Index:  i,
			Prompt: tape.PromptRecord{Messages: []tape.Message{{Role: "user", Content: string([]rune(req.Messages[0].Content)[:trim])}}},
		})
	}

	if got := PublicView(trimmed, WithText).Summary.PromptSet; got != server.PromptSetID {
		t.Errorf("PromptSet = %q on a run that sent the set's first %d characters and said so, want %q", got, trim, server.PromptSetID)
	}

	unrecorded := &tape.Tape{Summary: trimmed.Summary, Requests: append([]tape.RequestRecord(nil), trimmed.Requests...)}
	unrecorded.Summary.PromptTrimChars = 0
	if got := PublicView(unrecorded, WithText).Summary.PromptSet; got != "" {
		t.Errorf("PromptSet = %q on a trimmed run claiming 0, want blank: the count is the only way the verifier knows what to cut", got)
	}

	offByOne := &tape.Tape{Summary: trimmed.Summary, Requests: append([]tape.RequestRecord(nil), trimmed.Requests...)}
	offByOne.Summary.PromptTrimChars = trim + 1
	if got := PublicView(offByOne, WithText).Summary.PromptSet; got != "" {
		t.Errorf("PromptSet = %q on a run off by one character, want blank", got)
	}
}

// A run the plan salted and trimmed to tokens (2026-09-20, run plan): the
// verifier strips the recorded salt from the front, and what is left must be
// a non-empty rune-prefix of this binary's copy of the set prompt at that
// index — how long a prefix is the run's business, measured on the model's
// own tokenizer, and that it is a prefix of the published text is the claim.
// Four shapes fail: a text that does not start with the recorded salt, one
// that is not a prefix of the set's own text, an empty one, and a run whose
// summary names a salt it did not carry. A prompt the target already covered
// goes whole behind the salt, and the whole prompt is a prefix of itself.
func TestPublicViewAcceptsASaltedTokenTrimmedRunAndNothingElse(t *testing.T) {
	const n, trim = 2, 800
	salt := "[run dlk4h11s9o5c]\n\n"
	build := func(summarySalt string, content func(string) string) *tape.Tape {
		tp := &tape.Tape{}
		tp.Summary.Concurrency = n
		tp.Summary.PromptSet = server.PromptSetID
		tp.Summary.PromptSalt = summarySalt
		tp.Summary.PromptTrimTokens = trim
		for i, req := range server.DefaultPrompts(n) {
			tp.Requests = append(tp.Requests, tape.RequestRecord{
				Index:  i,
				Prompt: tape.PromptRecord{Messages: []tape.Message{{Role: "user", Content: content(req.Messages[0].Content)}}},
			})
		}
		return tp
	}
	prefix := func(s string, runes int) string { return string([]rune(s)[:runes]) }

	// The honest run: salt in front, a prefix behind it.
	honest := build(salt, func(s string) string { return salt + prefix(s, 4000) })
	if got := PublicView(honest, WithText).Summary.PromptSet; got != server.PromptSetID {
		t.Errorf("PromptSet = %q on a run that sent the salted prefix and said so, want %q", got, server.PromptSetID)
	}
	// A prompt the target covered goes whole behind the salt, and the whole
	// text is a prefix of itself.
	whole := build(salt, func(s string) string { return salt + s })
	if got := PublicView(whole, WithText).Summary.PromptSet; got != server.PromptSetID {
		t.Errorf("PromptSet = %q on a run whose prompts the target already covered, want %q", got, server.PromptSetID)
	}

	// Not the recorded salt on the front.
	noSalt := build(salt, func(s string) string { return prefix(s, 4000) })
	if got := PublicView(noSalt, WithText).Summary.PromptSet; got != "" {
		t.Errorf("PromptSet = %q on a run whose text does not start with the recorded salt, want blank", got)
	}
	// Not a prefix of the set's own text behind the salt.
	notPrefix := build(salt, func(s string) string { return salt + "x" + prefix(s, 100) })
	if got := PublicView(notPrefix, WithText).Summary.PromptSet; got != "" {
		t.Errorf("PromptSet = %q on a run that sent something other than the set's text behind the salt, want blank", got)
	}
	// Empty behind the salt: a stream that sent none of the set did not send
	// the set.
	empty := build(salt, func(string) string { return salt })
	if got := PublicView(empty, WithText).Summary.PromptSet; got != "" {
		t.Errorf("PromptSet = %q on a run that sent only the salt, want blank", got)
	}
	// A summary that names no salt while the texts carry one: the comparison
	// is then whole-prompt equality, and the salt breaks it.
	unrecorded := build("", func(s string) string { return salt + prefix(s, 4000) })
	if got := PublicView(unrecorded, WithText).Summary.PromptSet; got != "" {
		t.Errorf("PromptSet = %q on a run whose summary records no salt, want blank: the count is the only way the verifier knows what to strip", got)
	}
}
