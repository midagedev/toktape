package server

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// scanAll drives the scanner with reads of size step, which is what a real
// socket does: a `data:` line arrives split across two packets more often than
// not.
func scanAll(b []byte, step int) [][]byte {
	var sc sseScanner
	var out [][]byte
	for i := 0; i < len(b); i += step {
		j := i + step
		if j > len(b) {
			j = len(b)
		}
		out = append(out, sc.feed(b[i:j])...)
	}
	return append(out, sc.close()...)
}

// TestScannerReadBoundaries: the event payloads must not depend on where the
// reads fell. A single big write, which is all an httptest server gives, would
// never catch a scanner that assumes a read is a line.
func TestScannerReadBoundaries(t *testing.T) {
	sse := readFixture(t, "stream_basic.sse")
	want := scanAll(sse, len(sse))
	if len(want) != 54 {
		t.Fatalf("whole-buffer scan gave %d events, want 54", len(want))
	}
	for _, step := range []int{1, 3, 7, 13, 64, 511, 4096} {
		got := scanAll(sse, step)
		if len(got) != len(want) {
			t.Fatalf("step %d: %d events, want %d", step, len(got), len(want))
		}
		for i := range got {
			if !bytes.Equal(got[i], want[i]) {
				t.Fatalf("step %d: event %d differs:\n got %q\nwant %q", step, i, got[i], want[i])
			}
		}
	}
}

// TestScannerCRLF: a proxy in front of the server may rewrite the line
// endings.
func TestScannerCRLF(t *testing.T) {
	sse := readFixture(t, "stream_basic.sse")
	crlf := bytes.ReplaceAll(sse, []byte("\n"), []byte("\r\n"))
	want := scanAll(sse, len(sse))
	got := scanAll(crlf, 7)
	if len(got) != len(want) {
		t.Fatalf("CRLF scan gave %d events, want %d", len(got), len(want))
	}
	for i := range got {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("CRLF event %d differs:\n got %q\nwant %q", i, got[i], want[i])
		}
	}
}

func TestScannerIgnoresCommentsAndOtherFields(t *testing.T) {
	in := []byte(": keep-alive\n" +
		"event: message\n" +
		"id: 7\n" +
		"data: {\"a\":1}\n" +
		"\n" +
		": another\n" +
		"data: {\"b\":2}\n" +
		"\n")
	got := scanAll(in, 5)
	want := []string{`{"a":1}`, `{"b":2}`}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %q", len(got), len(want), got)
	}
	for i := range got {
		if string(got[i]) != want[i] {
			t.Errorf("event %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestScannerFlushesUnterminatedEvent: a server that drops the connection
// right after writing the last chunk leaves no blank line behind it.
func TestScannerFlushesUnterminatedEvent(t *testing.T) {
	got := scanAll([]byte("data: {\"a\":1}\n\ndata: {\"b\":2}"), 4)
	if len(got) != 2 || string(got[1]) != `{"b":2}` {
		t.Fatalf("got %q, want the trailing event flushed", got)
	}
}

func TestScannerMultiLineData(t *testing.T) {
	got := scanAll([]byte("data: line one\ndata: line two\n\n"), 3)
	if len(got) != 1 || string(got[0]) != "line one\nline two" {
		t.Fatalf("got %q, want the data lines joined with a newline", got)
	}
}

// TestReplayErrorFixture: llama-server reports a mid-stream failure as an
// error frame rather than an HTTP status, because the status went out with the
// headers. The tokens already received are kept, and the failure lands in
// rec.Error where RunConcurrent expects it.
func TestReplayErrorFixture(t *testing.T) {
	rec, _, err := replayFixture(t, "stream_error", StreamHooks{})
	if err == nil {
		t.Fatal("ReplayStream returned no error for a stream that carried an error frame")
	}
	if rec == nil {
		t.Fatal("ReplayStream discarded the partial record")
	}
	if !strings.Contains(rec.Error, "context shift is disabled") {
		t.Errorf("rec.Error = %q, want the server's message", rec.Error)
	}
	if !strings.Contains(rec.Error, "500") {
		t.Errorf("rec.Error = %q, want the error code kept", rec.Error)
	}
	if got, want := len(rec.Tokens), 2; got != want {
		t.Errorf("tokens = %d, want %d (what arrived before the failure is still evidence)", got, want)
	}
	if got, want := rec.Prompt.Completion, "The context"; got != want {
		t.Errorf("Completion = %q, want %q", got, want)
	}
}

// TestReplayTruncatedStream: a connection that dies without an error frame,
// without a finish chunk and without [DONE] must not be reported as a
// completed run.
func TestReplayTruncatedStream(t *testing.T) {
	sse := readFixture(t, "stream_basic.sse")
	cut := bytes.Index(sse, []byte(`"finish_reason":"stop"`))
	if cut < 0 {
		t.Fatal("fixture has no finish chunk")
	}
	cut = bytes.LastIndex(sse[:cut], []byte("data: "))
	rec, _, err := ReplayStream(sse[:cut], nil, StreamHooks{})
	if err == nil {
		t.Fatal("a truncated stream was reported as complete")
	}
	if rec == nil || len(rec.Tokens) != 44 {
		t.Fatalf("partial record lost: %v", rec)
	}
	if !strings.Contains(rec.Error, "without a finish chunk") {
		t.Errorf("rec.Error = %q", rec.Error)
	}
}

// TestReplayRejectsArrivalMismatch keeps a fixture and its sidecar in step: a
// silently reused timestamp would corrupt every timing derived from it.
func TestReplayRejectsArrivalMismatch(t *testing.T) {
	sse := readFixture(t, "stream_basic.sse")
	_, _, err := ReplayStream(sse, []time.Duration{time.Millisecond}, StreamHooks{})
	if err == nil {
		t.Fatal("ReplayStream accepted 1 arrival time for 54 events")
	}
	if !strings.Contains(err.Error(), "arrival times") {
		t.Errorf("err = %v, want it to name the mismatch", err)
	}
}

func TestReplayRejectsMalformedChunk(t *testing.T) {
	_, _, err := ReplayStream([]byte("data: {not json}\n\n"), nil, StreamHooks{})
	if err == nil || !strings.Contains(err.Error(), "decode stream chunk") {
		t.Fatalf("err = %v, want a decode error", err)
	}
}

// TestReplaySynthesisesArrivals covers the sidecar-free path: a stream
// reprocessed from bytes alone gets its times from the server's own timings.
func TestReplaySynthesisesArrivals(t *testing.T) {
	sse := readFixture(t, "stream_basic.sse")
	rec, _, err := ReplayStream(sse, nil, StreamHooks{})
	if err != nil {
		t.Fatalf("ReplayStream: %v", err)
	}
	if got, want := len(rec.Tokens), 44; got != want {
		t.Fatalf("tokens = %d, want %d", got, want)
	}
	// prompt_ms 168 + predicted_ms 22 for the first token.
	if got, want := rec.Timings.TTFTMs, 190.0; got != want {
		t.Errorf("TTFTMs = %v, want %v", got, want)
	}
}

// TestHooksFireInOrderDuringReplay: the recorder must hand each event to the
// hook as it is consumed, because the process recorder samples a host counter
// there and a buffered replay would attribute every fault to the last token.
func TestHooksFireInOrderDuringReplay(t *testing.T) {
	var (
		tokens   []tape.TokenEvent
		progress []tape.PromptProgress
	)
	rec, _ := loadFixture(t, "stream_basic", StreamHooks{
		OnToken:    func(ev tape.TokenEvent) { tokens = append(tokens, ev) },
		OnProgress: func(ev tape.PromptProgress) { progress = append(progress, ev) },
	})
	if len(tokens) != len(rec.Tokens) {
		t.Fatalf("OnToken fired %d times, want %d", len(tokens), len(rec.Tokens))
	}
	for i := range tokens {
		if tokens[i] != rec.Tokens[i] {
			t.Fatalf("hook token %d = %+v, record has %+v", i, tokens[i], rec.Tokens[i])
		}
		if tokens[i].Index != i {
			t.Fatalf("hook token %d has Index %d", i, tokens[i].Index)
		}
	}
	if len(progress) != len(rec.Progress) {
		t.Errorf("OnProgress fired %d times, want %d", len(progress), len(rec.Progress))
	}
}

// TestReplayNilHooks: every hook is optional.
func TestReplayNilHooks(t *testing.T) {
	if _, _, err := replayFixture(t, "stream_basic", StreamHooks{}); err != nil {
		t.Fatalf("ReplayStream with no hooks: %v", err)
	}
}

func TestParseArrivals(t *testing.T) {
	got, err := ParseArrivals([]byte("# a comment\n\n42\n 88.5 \n#another\n166\n"))
	if err != nil {
		t.Fatalf("ParseArrivals: %v", err)
	}
	want := []time.Duration{42 * time.Millisecond, 88500 * time.Microsecond, 166 * time.Millisecond}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	if _, err := ParseArrivals([]byte("42\nnope\n")); err == nil {
		t.Error("ParseArrivals accepted a non-numeric line")
	}
}
