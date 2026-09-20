package main

import (
	"context"
	"image"
	_ "image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
	"github.com/midagedev/toktape/internal/tui"
)

// checkShareImage asserts that path is a decodable 1200x675 PNG — the exact
// frame X and Reddit crop an OpenGraph image to.
func checkShareImage(t *testing.T, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open the image card: %v", err)
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("the image card does not decode: %v", err)
	}
	if format != "png" {
		t.Errorf("image card format = %q, want png", format)
	}
	if cfg.Width != 1200 || cfg.Height != 675 {
		t.Errorf("image card is %dx%d, want 1200x675", cfg.Width, cfg.Height)
	}
}

// TestCardVerbWritesPNG covers both spellings: the default destination next to
// the tape, and an explicit one.
func TestCardVerbWritesPNG(t *testing.T) {
	dir := t.TempDir()
	tapePath := writeTape(t, dir, &tape.RunSummary{ID: "20260913-101500-qwen3", Concurrency: 1})

	t.Run("default destination", func(t *testing.T) {
		code, stdout, stderr := exec(t, "card", tapePath, "-o", "png")
		if code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		want := filepath.Join(dir, "20260913-101500-qwen3.card.png")
		if strings.TrimSpace(stdout) != want {
			t.Errorf("stdout = %q, want the written path %q", stdout, want)
		}
		checkShareImage(t, want)
	})

	t.Run("explicit destination", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "post.png")
		code, _, stderr := exec(t, "card", tapePath, "-o", "png", out)
		if code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		checkShareImage(t, out)
	})
}

// TestShareHintCompareLine: the compare line appears only when there is an
// earlier run of the same model to compare with.
func TestShareHintCompareLine(t *testing.T) {
	dir := t.TempDir()
	// The ID's model part is the slug the recorder derives, so a sibling run
	// of the same model is found by name alone.
	const fileName = "Qwen3.5-35B-A3B-UD-Q4_K_M.gguf"
	slug := tape.SlugFromModel(fileName)
	tp := &tape.Tape{Schema: tape.SchemaVersion, Summary: tape.RunSummary{
		ID:    "20260913-120000-" + slug,
		Model: tape.ModelInfo{FileName: fileName},
	}}
	arts := artifacts{tape: filepath.Join(dir, tp.Summary.ID+tape.Ext)}

	if got := shareHint(dir, tp, arts); strings.Contains(got, "→ Compare:") {
		t.Errorf("a lone run offered a comparison:\n%s", got)
	}

	// An older run of the same model, one of another model, and one from
	// later that must not be offered as a predecessor.
	for _, id := range []string{
		"20260901-090000-" + slug,
		"20260902-090000-" + slug,
		"20260913-130000-" + slug,
		"20260912-090000-" + tape.SlugFromModel("DeepSeek-V3-0324-UD-Q4_K_XL.gguf"),
	} {
		writeTape(t, dir, &tape.RunSummary{ID: id})
	}
	got := shareHint(dir, tp, arts)
	if !strings.Contains(got, "→ Compare:") {
		t.Fatalf("no compare line with an earlier run present:\n%s", got)
	}
	if !strings.Contains(got, "20260902-090000-"+slug) {
		t.Errorf("compare line does not name the newest earlier run:\n%s", got)
	}
	if strings.Contains(got, "deepseek") {
		t.Errorf("compare line crossed models:\n%s", got)
	}
	if strings.Contains(got, "20260913-130000") {
		t.Errorf("compare line offered a later run as the predecessor:\n%s", got)
	}
}

// TestWaitFlag: --wait 0 fails fast on a server that is still loading, and a
// --wait DURATION waits and then gives up with the same exit code.
func TestWaitFlag(t *testing.T) {
	hermetic(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"Loading model"}}`))
	}))
	t.Cleanup(srv.Close)

	t.Run("--wait 0 is fail fast", func(t *testing.T) {
		start := time.Now()
		code, _, stderr := exec(t, "--url", srv.URL, "--out", t.TempDir(), "--wait", "0")
		if code != exitUnreachable {
			t.Fatalf("exit %d, want %d\n%s", code, exitUnreachable, stderr)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("--wait 0 took %v", d)
		}
		if strings.Contains(stderr, "loading the model …") {
			t.Errorf("--wait 0 still printed a waiting line:\n%s", stderr)
		}
	})

	t.Run("a budget is spent and then reported", func(t *testing.T) {
		code, _, stderr := exec(t, "--url", srv.URL, "--out", t.TempDir(), "--wait", "1s")
		if code != exitUnreachable {
			t.Fatalf("exit %d, want %d\n%s", code, exitUnreachable, stderr)
		}
		if !strings.Contains(stderr, "server is loading the model") {
			t.Errorf("no waiting line on stderr:\n%s", stderr)
		}
		if !strings.Contains(stderr, "gave up after") {
			t.Errorf("stderr does not say the wait was given up:\n%s", stderr)
		}
	})
}

// TestLoadingLine pins the line a user watches. It is a pure function, so the
// spinner and the counter are checked without a terminal.
func TestLoadingLine(t *testing.T) {
	cases := []struct {
		reason  string
		elapsed time.Duration
		want    string
	}{
		{recorder.ReasonLoading, 0, "⠋ server is loading the model … 0s"},
		{recorder.ReasonLoading, 72 * time.Second, "⠹ server is loading the model … 1m12s"},
		{recorder.ReasonLoading, 3 * time.Second, "⠸ server is loading the model … 3s"},
		{recorder.ReasonStarting, 4 * time.Second, "⠼ waiting for the server to come up … 4s"},
	}
	for _, tc := range cases {
		if got := loadingLine(tc.reason, tc.elapsed); got != tc.want {
			t.Errorf("loadingLine(%q, %v) = %q, want %q", tc.reason, tc.elapsed, got, tc.want)
		}
	}
	// The frame must actually advance, or the line reads as a stuck program.
	if loadingLine(recorder.ReasonLoading, time.Second) == loadingLine(recorder.ReasonLoading, 2*time.Second) {
		t.Error("the spinner does not turn between seconds")
	}
}

// Off a terminal the waiting line is repeated rather than redrawn: a log that
// gains one line every ten seconds is readable, a log full of \r is not.
func TestWaitingLineOffTerminal(t *testing.T) {
	var out strings.Builder
	p := newProgress(&out, false)
	for _, e := range []time.Duration{0, 2 * time.Second, 4 * time.Second, 12 * time.Second, 21 * time.Second} {
		p.handle(recorder.Event{Kind: recorder.EventLoading, Message: recorder.ReasonLoading, Elapsed: e})
	}
	p.handle(recorder.Event{Kind: recorder.EventWarning, Message: "done waiting"})

	got := out.String()
	if strings.Contains(got, "\r") {
		t.Errorf("a non-terminal writer got a redrawn line:\n%q", got)
	}
	if n := strings.Count(got, "server is loading the model"); n != 3 {
		t.Errorf("waiting line printed %d times, want 3 (one per ten seconds):\n%s", n, got)
	}
	if !strings.Contains(got, "! done waiting") {
		t.Errorf("the event after the wait was not printed:\n%s", got)
	}
}

// TestBridgeEvent is the --tui contract: which recorder observations reach the
// screen, and as what. It is a pure function, so no PTY is involved.
func TestBridgeEvent(t *testing.T) {
	const at = 1500 * time.Millisecond
	summary := &tape.RunSummary{
		Server:    tape.ServerInfo{URL: "http://127.0.0.1:8080", Kind: tape.ServerLlamaCPP},
		Model:     tape.ModelInfo{FileName: "Qwen3.5-35B-A3B-UD-Q4_K_M.gguf"},
		Host:      tape.HostInfo{OS: "Linux", CPU: "EPYC 9354"},
		Placement: tape.PlacementSummary{Source: "gguf"},
		// What may end the run (TTP-76). The live tiles draw progress against
		// it, and it only reaches the screen on this one event.
		Limit: tape.LimitSummary{For: 20 * time.Second, MaxTokens: 2048, MinTokens: 64},
	}

	t.Run("mapped kinds", func(t *testing.T) {
		cases := []struct {
			name string
			in   recorder.Event
			want tui.EventKind
		}{
			{"discovered", recorder.Event{Kind: recorder.EventDiscovered, Message: "http://127.0.0.1:8080"}, tui.EventDiscovered},
			{"attached", recorder.Event{Kind: recorder.EventAttached, Summary: summary}, tui.EventProps},
			{"pid", recorder.Event{Kind: recorder.EventPIDFound, Message: "1234"}, tui.EventPID},
			{"stream started", recorder.Event{Kind: recorder.EventStreamStarted, Stream: 2}, tui.EventStreamStart},
			{"token", recorder.Event{Kind: recorder.EventToken, Stream: 1,
				Token: tape.TokenEvent{Text: "hi", MajFaultsDelta: 3}}, tui.EventToken},
			{"sample", recorder.Event{Kind: recorder.EventSample,
				Sample: tape.RunSample{LoadAvg1: 2.5}}, tui.EventSample},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, ok := bridgeEvent(tc.in, at)
				if !ok {
					t.Fatalf("%v was dropped", tc.in.Kind)
				}
				if got.Kind != tc.want {
					t.Errorf("Kind = %v, want %v", got.Kind, tc.want)
				}
				if got.T != at {
					t.Errorf("T = %v, want %v", got.T, at)
				}
			})
		}
	})

	t.Run("payloads", func(t *testing.T) {
		got, _ := bridgeEvent(recorder.Event{Kind: recorder.EventDiscovered, Message: "http://h:8080"}, at)
		if got.Server.URL != "http://h:8080" {
			t.Errorf("discovered carried %q", got.Server.URL)
		}

		got, _ = bridgeEvent(recorder.Event{Kind: recorder.EventAttached, Summary: summary}, at)
		if got.Server.URL != summary.Server.URL || got.Model.FileName != summary.Model.FileName ||
			got.Host.CPU != summary.Host.CPU || got.Placement.Source != summary.Placement.Source {
			t.Errorf("attached lost part of the static picture: %+v", got)
		}
		// 2026-09-14: without the limit the live tiles fall back to drawing
		// progress against the token cap, which on a clock run is the runaway
		// guard and not what the run will end on.
		if got.Limit != summary.Limit {
			t.Errorf("attached lost the run's limit: %+v, want %+v", got.Limit, summary.Limit)
		}

		got, _ = bridgeEvent(recorder.Event{Kind: recorder.EventPIDFound, Message: "1234"}, at)
		if got.PID != 1234 {
			t.Errorf("PID = %d, want 1234", got.PID)
		}

		got, _ = bridgeEvent(recorder.Event{Kind: recorder.EventToken, Stream: 1,
			Token: tape.TokenEvent{Text: "hi", MajFaultsDelta: 3}}, at)
		if got.Stream != 1 || got.Token.Text != "hi" || got.Token.MajFaultsDelta != 3 {
			t.Errorf("token lost its payload: %+v", got)
		}
	})

	t.Run("dropped kinds", func(t *testing.T) {
		// Done is re-sent by the caller once the tape has been written: only
		// then are the tape and its path both known, and the frozen final
		// frame is built from them.
		for _, ev := range []recorder.Event{
			{Kind: recorder.EventDone, Summary: summary},
			{Kind: recorder.EventProps, Message: "b4321"},
			{Kind: recorder.EventWarning, Message: "pid not found"},
			{Kind: recorder.EventPIDNotFound},
			{Kind: recorder.EventLoading, Elapsed: time.Second},
			{Kind: recorder.EventAttached}, // no summary to carry
			{Kind: recorder.EventPIDFound, Message: "not a number"},
		} {
			if _, ok := bridgeEvent(ev, at); ok {
				t.Errorf("%v reached the screen", ev.Kind)
			}
		}
	})
}

// TestPlayModel: a replay frame is a function of the tape, the wall time and
// the speed, which is what makes another machine's tape reproducible here.
func TestPlayModel(t *testing.T) {
	tp := tui.ExampleTape()
	end := tapeDuration(tp)
	if end <= 0 {
		t.Fatal("the example tape has no tokens")
	}

	t.Run("speed scales clip time", func(t *testing.T) {
		_, clip := playModel(tp, time.Second, 4)
		if clip != 4*time.Second {
			t.Errorf("clip at 4x after 1s = %v, want 4s", clip)
		}
		_, clip = playModel(tp, time.Second, 1)
		if clip != time.Second {
			t.Errorf("clip at 1x after 1s = %v, want 1s", clip)
		}
	})

	t.Run("the run fills in over time", func(t *testing.T) {
		early, _ := playModel(tp, end/4, 1)
		late, _ := playModel(tp, end, 1)
		if tokenCount(early) == 0 {
			t.Error("a quarter of the way in, nothing had arrived")
		}
		if tokenCount(early) >= tokenCount(late) {
			t.Errorf("token count did not grow: %d then %d", tokenCount(early), tokenCount(late))
		}
		if !late.Done {
			t.Error("the replay never reached the end of the run")
		}
	})

	t.Run("the same instant renders the same frame", func(t *testing.T) {
		m1, c1 := playModel(tp, end/2, 1)
		m2, c2 := playModel(tp, end/2, 1)
		a := tui.View(m1, c1, tui.MinWidth, tui.MinHeight)
		b := tui.View(m2, c2, tui.MinWidth, tui.MinHeight)
		if a != b {
			t.Error("two renders of one instant differ")
		}
		if strings.TrimSpace(a) == "" {
			t.Error("the replay frame is blank")
		}
	})
}

func tokenCount(m tui.Model) int {
	n := 0
	for _, s := range m.Streams {
		n += len(s.Tokens)
	}
	return n
}

// TestPlayVerbRejects: the argument errors are reported rather than dropping
// the user into a screen that cannot work.
func TestPlayVerbRejects(t *testing.T) {
	dir := t.TempDir()
	tapePath := writeTape(t, dir, &tape.RunSummary{ID: "20260913-101500-qwen3"})

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no file", []string{"play"}, "expected one tape file"},
		{"missing file", []string{"play", filepath.Join(dir, "nope.tape")}, "toktape:"},
		{"negative speed", []string{"play", tapePath, "--speed", "-1"}, "must be positive"},
		// Off a terminal the screen cannot run, and the message names the
		// verb that does work instead.
		{"not a terminal", []string{"play", tapePath}, "needs a terminal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := exec(t, tc.args...)
			if code != exitUsage {
				t.Fatalf("exit %d, want %d\n%s", code, exitUsage, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tc.want)
			}
		})
	}
}

// --tui off a terminal falls back to the plain lines and says so, instead of
// writing escape sequences into a pipe.
func TestTUIFallsBackOffTerminal(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	dir := t.TempDir()

	code, stdout, stderr := exec(t, "--url", srv.URL, "--out", dir, "--tui", "--n-predict", "64")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "--tui needs a terminal") {
		t.Errorf("no fallback note on stderr:\n%s", stderr)
	}
	if strings.ContainsRune(stdout, 0x1b) {
		t.Error("the fallback wrote escape sequences to stdout")
	}
	if !strings.Contains(stderr, "✓ Tape   ") {
		t.Errorf("the fallback run did not finish:\n%s", stderr)
	}
}

// A saved tape must be replayable by the play verb's own reader, which is the
// path a person takes when they are handed someone else's run.
func TestRecordedTapeIsPlayable(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	dir := t.TempDir()
	if code, _, stderr := exec(t, "--url", srv.URL, "--out", dir, "--n-predict", "64", "--quiet"); code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	tapes, _ := filepath.Glob(filepath.Join(dir, "*"+tape.Ext))
	if len(tapes) != 1 {
		t.Fatalf("run files = %v", tapes)
	}
	tp, err := tape.Read(tapes[0])
	if err != nil {
		t.Fatalf("tape.Read: %v", err)
	}
	m, clip := playModel(tp, tapeDuration(tp), 1)
	frame := tui.View(m, clip, tui.MinWidth, tui.MinHeight)
	if strings.TrimSpace(frame) == "" {
		t.Error("the recorded tape replays as a blank frame")
	}
}

// A cancelled --tui run must not deadlock on the channel between the recorder
// and the screen.
func TestTUIRunHonoursCancellation(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan int, 1)
	go func() {
		done <- runRecordFor(ctx, srv.URL, t.TempDir())
	}()
	select {
	case code := <-done:
		if code == exitOK {
			t.Error("a cancelled run reported success")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a cancelled run did not return")
	}
}

// runRecordFor drives recordTUI's non-screen half: the same wiring without a
// terminal, so the cancellation path is exercised without a PTY.
func runRecordFor(ctx context.Context, url, dir string) int {
	opts := pinCollectors(recorder.Options{BaseURL: url, MaxTokens: 16})
	var sink strings.Builder
	return recordPlain(ctx, &cli{stdout: &sink, stderr: &sink}, opts, recordConfig{outDir: dir, card: true, quiet: true})
}

// A run whose output directory cannot be written still reports the failure
// rather than claiming a tape that is not there.
func TestSaveRunReportsAFailedDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(dir, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	tp := &tape.Tape{Schema: tape.SchemaVersion, Summary: tape.RunSummary{ID: "20260913-101500-x"}}
	a, err := saveRun(dir, tp, true)
	if err == nil {
		t.Fatal("saving into a file-as-directory succeeded")
	}
	if a.tape != "" {
		t.Errorf("a failed save still reported a tape at %q", a.tape)
	}
	if !strings.Contains(err.Error(), "saving the run file") {
		t.Errorf("err = %v, want it to name the step that failed", err)
	}
}

// TestRecordTUIWiring drives the --tui path's non-terminal half: the recorder
// goroutine, the bridged channel and the hand-off after the screen closes.
//
// There is no PTY here, so bubbletea's input reaches EOF at once and the screen
// exits immediately — which is exactly the case worth pinning, because it is
// the one where the recorder is still running when the screen goes away. The
// run must still finish, save and print rather than deadlock on a channel
// nobody is draining.
func TestRecordTUIWiring(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	dir := t.TempDir()

	var stdout, stderr strings.Builder
	opts := pinCollectors(recorder.Options{BaseURL: srv.URL, MaxTokens: 64, Version: version})
	cfg := recordConfig{outDir: dir, card: true}

	done := make(chan int, 1)
	go func() {
		done <- recordTUI(context.Background(), &cli{stdout: &stdout, stderr: &stderr}, opts, cfg)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the --tui path did not return")
	}

	// Whether the run completed or was cut short by the closing screen, it
	// must not have left the two goroutines waiting on each other.
	if tapes, _ := filepath.Glob(filepath.Join(dir, "*"+tape.Ext)); len(tapes) == 1 {
		if images, _ := filepath.Glob(filepath.Join(dir, "*.card.png")); len(images) != 1 {
			t.Errorf("a finished --tui run saved no image card: %v", images)
		}
		if !strings.Contains(stderr.String(), "✓ Tape   ") {
			t.Errorf("a finished --tui run printed no share block:\n%s", stderr.String())
		}
	}
}

// TestRecordTUIToQuit is the --tui path a person actually takes: the run
// finishes, the screen holds the result, the user presses q, and the card and
// the share block land in the scrollback behind the closed screen.
//
// The quit key is delivered through a pipe rather than a PTY. Nothing here
// asserts what the screen drew — that is the vision round's job; this pins the
// hand-off, which is the half that can deadlock or lose the artefacts.
func TestRecordTUIToQuit(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	dir := t.TempDir()

	keys, typist := io.Pipe()
	prev := tuiInput
	tuiInput = keys
	t.Cleanup(func() { tuiInput = prev })

	var stdout, stderr syncBuffer
	opts := pinCollectors(recorder.Options{BaseURL: srv.URL, MaxTokens: 64, Version: version})
	cfg := recordConfig{outDir: dir, card: true}

	done := make(chan int, 1)
	go func() {
		done <- recordTUI(context.Background(), &cli{stdout: &stdout, stderr: &stderr}, opts, cfg)
	}()

	// Wait for the run to have produced its tape, then quit the screen. The
	// deadline is the test's, not a sleep: a slow box takes longer, a fast
	// one does not wait.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if tapes, _ := filepath.Glob(filepath.Join(dir, "*"+tape.Ext)); len(tapes) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the --tui run never saved a tape")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := typist.Write([]byte("q")); err != nil {
		t.Fatalf("sending the quit key: %v", err)
	}

	select {
	case code := <-done:
		if code != exitOK {
			t.Fatalf("exit %d\n%s", code, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the screen did not close on q")
	}
	_ = typist.Close()

	if images, _ := filepath.Glob(filepath.Join(dir, "*.card.png")); len(images) != 1 {
		t.Errorf("image cards = %v, want one", images)
	}
	// The card goes to stdout after the alt screen is gone, so it survives in
	// the scrollback exactly as a plain run's would.
	if !strings.Contains(stdout.String(), "toktape") {
		t.Errorf("no card on stdout after the screen closed:\n%s", stdout.String())
	}
	for _, want := range []string{"✓ Tape   ", "✓ Card   ", "→ Markdown: "} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr.String())
		}
	}
}

// syncBuffer is a strings.Builder that may be written by the screen's
// goroutines while the test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestShareHintNamesTheTape: the tape is what turns a posted card from a claim
// into something a reader can check, so the hint says to attach it and names
// the verb that replays it. A run that saved no tape offers neither.
func TestShareHintNamesTheTape(t *testing.T) {
	dir := t.TempDir()
	tp := &tape.Tape{Schema: tape.SchemaVersion, Summary: tape.RunSummary{
		ID:    "20260913-120000-qwen3.5-35b-a3b",
		Model: tape.ModelInfo{FileName: "Qwen3.5-35B-A3B-UD-Q4_K_M.gguf"},
	}}
	arts := artifacts{tape: filepath.Join(dir, tp.Summary.ID+tape.Ext)}

	got := shareHint(dir, tp, arts)
	// 2026-09-21: the line was "Attach the .tape when you post — reviewers can
	// replay it with: toktape play <basename>". The user asked for the sales
	// tone to go, so it is a plain command now, and it names the path the
	// other lines name, so it runs as pasted (a basename only ran from the
	// runs directory).
	if !strings.Contains(got, "→ Replay:   toktape play "+tildePath(arts.tape)) {
		t.Errorf("hint does not give the replay command for the tape it saved:\n%s", got)
	}
	for _, loud := range []string{"Reddit", "Post it", "when you post"} {
		if strings.Contains(got, loud) {
			t.Errorf("the hint advertises (%q); it names commands and nothing else:\n%s", loud, got)
		}
	}

	// No tape on disk: there is nothing to attach and nothing to replay.
	if bare := shareHint(dir, tp, artifacts{}); strings.Contains(bare, "toktape play") {
		t.Errorf("a run that saved no tape offered a replay:\n%s", bare)
	}
}

// TestShareHintFailedStreams: a run where streams died is not a run to post.
// The block a finished run ends with must say so before the ✓ lines — the
// server's own words, deduplicated, and the one lever that fits them — and
// must not invite the user to post a broken tape. The tape and card lines
// stay: the run did happen and its files are on disk.
func TestShareHintFailedStreams(t *testing.T) {
	dir := t.TempDir()
	const serverErr = "context_length_exceeded: the request exceeds the available context size; context shift is disabled"
	tp := &tape.Tape{Schema: tape.SchemaVersion, Summary: tape.RunSummary{
		ID:          "20260913-120000-qwen3.5-35b-a3b",
		Model:       tape.ModelInfo{FileName: "Qwen3.5-35B-A3B-UD-Q4_K_M.gguf"},
		Concurrency: 4,
		Aggregate:   tape.AggregateTimings{Streams: 4, StreamsFailed: 2},
	}, Requests: []tape.RequestRecord{
		{Index: 0}, {Index: 1},
		{Index: 2, Error: serverErr},
		{Index: 3, Error: serverErr},
	}}
	arts := artifacts{tape: filepath.Join(dir, tp.Summary.ID+tape.Ext)}

	got := shareHint(dir, tp, arts)
	if !strings.Contains(got, "✗ 2 of 4 streams failed") {
		t.Errorf("the failure count is not said out loud:\n%s", got)
	}
	if !strings.Contains(got, serverErr) {
		t.Errorf("the server's own words are not quoted:\n%s", got)
	}
	// Two streams, one sentence: the second copy folds into a ×2, so the
	// error text appears once and the count rides beside it.
	if n := strings.Count(got, "context_length_exceeded"); n != 1 {
		t.Errorf("the error text appears %d times, want 1 (deduplicated with ×N):\n%s", n, got)
	}
	if !strings.Contains(got, "×2") {
		t.Errorf("the deduplicated count is not shown:\n%s", got)
	}
	if !strings.Contains(got, "--n-predict") || !strings.Contains(got, "-c") {
		t.Errorf("the lever for a context error does not name the flags to move:\n%s", got)
	}
	if strings.Contains(got, "→ Markdown:") {
		t.Errorf("a run with failed streams was invited to be posted:\n%s", got)
	}
	for _, want := range []string{"✓ Tape   ", "→ Replay:   "} {
		if !strings.Contains(got, want) {
			t.Errorf("the block is missing %q, which a failed run keeps:\n%s", want, got)
		}
	}

	// A clean run keeps the invitation: the block is an addition for failed
	// runs, not a change to the good ones.
	tp.Summary.Aggregate.StreamsFailed = 0
	tp.Requests = tp.Requests[:2]
	clean := shareHint(dir, tp, arts)
	if !strings.Contains(clean, "→ Markdown:") {
		t.Errorf("a clean run lost its post line:\n%s", clean)
	}
	if strings.Contains(clean, "streams failed") {
		t.Errorf("a clean run talks about failures:\n%s", clean)
	}
}

// TestFailureLever: the one line of advice under a failed-streams block is
// matched from the server's own words, and an error this table does not know
// gets no invented advice.
func TestFailureLever(t *testing.T) {
	for _, tc := range []struct {
		name, errText, want string
	}{
		{"the measured context error", "context_length_exceeded: the request exceeds the available context size", "--n-predict"},
		{"a context error in other words", "Requested tokens exceed context limit", "-c"},
		{"a rate limit", "429 Too Many Requests", "--sessions"},
		{"a rate limit in other words", "rate limit exceeded, retry later", "--sessions"},
		{"an unknown error", "connection reset by peer", "the server's words are above"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := failureLever(tc.errText); !strings.Contains(got, tc.want) {
				t.Errorf("failureLever(%q) = %q, want it to name %q", tc.errText, got, tc.want)
			}
		})
	}
}

// TestCardMarkdownCarriesReproduce walks the real path a poster takes: a tape
// written to disk, read back, and rendered with -o md. The Reproduce block is
// built from fields that survive the gzip'd JSON round trip, so the argv a
// reader pastes is the one the recorder read off /proc, not a re-derivation.
func TestCardMarkdownCarriesReproduce(t *testing.T) {
	dir := t.TempDir()
	s := card.ExampleConcurrent()
	path := writeTape(t, dir, s)

	code, out, stderr := exec(t, "card", path, "-o", "md")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{
		"<details><summary>Reproduce</summary>",
		"Server (as seen from /proc/48213/cmdline):",
		strings.Join(s.Server.Args, " "),
		"toktape --url http://127.0.0.1:8080 --sessions 8 --n-predict 307",
		"Tape: `" + s.ID + tape.Ext + "`",
		"</details>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("-o md output is missing %q:\n%s", want, out)
		}
	}
}
