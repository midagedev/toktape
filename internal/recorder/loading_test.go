package recorder_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
)

// loadingBody is the envelope llama-server answers with while a model is being
// read off disk.
const loadingBody = `{"error":{"code":503,"message":"Loading model","type":"unavailable_error"}}`

// collector gathers the events of one run for assertions.
type collector struct {
	mu     sync.Mutex
	events []recorder.Event
}

func (c *collector) handle(ev recorder.Event) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
}

func (c *collector) of(kind recorder.EventKind) []recorder.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []recorder.Event
	for _, ev := range c.events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// loadingServer answers /props with 503 for the first `n` requests and with
// the real payload afterwards, which is the shape of a server whose model is
// still being paged in. Every other route behaves as usual.
func loadingServer(t *testing.T, n int32) *httptest.Server {
	t.Helper()
	base := fakeServer(t)
	var seen int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/props" && atomic.AddInt32(&seen, 1) <= n {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(loadingBody))
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Scheme, r2.URL.Host = "http", strings.TrimPrefix(base.URL, "http://")
		r2.RequestURI = ""
		resp, err := http.DefaultClient.Do(r2)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = copyFlushing(w, resp.Body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestRecordWaitsForLoadingModel is TTP-18: a server that says "Loading model"
// twice and then answers must produce a tape, not an "unreachable" failure.
func TestRecordWaitsForLoadingModel(t *testing.T) {
	srv := loadingServer(t, 2)
	var c collector

	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:        srv.URL,
		MaxTokens:      64,
		SampleInterval: 20 * time.Millisecond,
		FSRoot:         t.TempDir(),
		GPU:            gpu.Null{},
		LoadingPoll:    10 * time.Millisecond,
		Progress:       c.handle,
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(tp.Requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(tp.Requests))
	}

	loading := c.of(recorder.EventLoading)
	if len(loading) != 2 {
		t.Fatalf("EventLoading fired %d times, want 2 (one per 503)", len(loading))
	}
	for i, ev := range loading {
		if ev.Message != recorder.ReasonLoading {
			t.Errorf("loading event %d: Message = %q, want %q", i, ev.Message, recorder.ReasonLoading)
		}
	}
	// Elapsed must grow: it is what the CLI's counter prints.
	if loading[1].Elapsed <= loading[0].Elapsed {
		t.Errorf("Elapsed did not advance: %v then %v", loading[0].Elapsed, loading[1].Elapsed)
	}
}

// A server that is loading and never finishes fails with ErrUnreachable once
// the budget runs out, so the CLI's exit code contract is unchanged.
func TestRecordGivesUpAfterWaitForModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(loadingBody))
	}))
	t.Cleanup(srv.Close)

	var c collector
	_, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:      srv.URL,
		FSRoot:       t.TempDir(),
		GPU:          gpu.Null{},
		WaitForModel: 60 * time.Millisecond,
		LoadingPoll:  10 * time.Millisecond,
		Progress:     c.handle,
	})
	if !errors.Is(err, recorder.ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	if !strings.Contains(err.Error(), "gave up after") {
		t.Errorf("err = %q, want it to say the wait was given up", err)
	}
	if len(c.of(recorder.EventLoading)) == 0 {
		t.Error("no EventLoading was emitted while waiting")
	}
}

// --wait 0 is fail fast: a loading server must not cost the user a poll.
func TestRecordNoWaitFailsImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(loadingBody))
	}))
	t.Cleanup(srv.Close)

	var c collector
	start := time.Now()
	_, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:      srv.URL,
		FSRoot:       t.TempDir(),
		GPU:          gpu.Null{},
		WaitForModel: recorder.NoWait,
		LoadingPoll:  time.Hour,
		Progress:     c.handle,
	})
	if !errors.Is(err, recorder.ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	if len(c.of(recorder.EventLoading)) != 0 {
		t.Error("NoWait still emitted a loading event")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("NoWait took %v", d)
	}
}

// A refused connection is an immediate failure by default — a mistyped --url
// must not hang — and a wait the user asked for by setting WaitForStart.
func TestRecordRefusedConnection(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	t.Run("default is immediate", func(t *testing.T) {
		var c collector
		start := time.Now()
		_, err := recorder.Record(context.Background(), recorder.Options{
			BaseURL:     deadURL,
			FSRoot:      t.TempDir(),
			GPU:         gpu.Null{},
			LoadingPoll: time.Hour,
			Progress:    c.handle,
		})
		if !errors.Is(err, recorder.ErrUnreachable) {
			t.Fatalf("err = %v, want ErrUnreachable", err)
		}
		if len(c.of(recorder.EventLoading)) != 0 {
			t.Error("a refused connection waited without being asked to")
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("a refused connection took %v", d)
		}
	})

	t.Run("WaitForStart waits", func(t *testing.T) {
		var c collector
		_, err := recorder.Record(context.Background(), recorder.Options{
			BaseURL:      deadURL,
			FSRoot:       t.TempDir(),
			GPU:          gpu.Null{},
			WaitForStart: true,
			WaitForModel: 60 * time.Millisecond,
			LoadingPoll:  10 * time.Millisecond,
			Progress:     c.handle,
		})
		if !errors.Is(err, recorder.ErrUnreachable) {
			t.Fatalf("err = %v, want ErrUnreachable", err)
		}
		evs := c.of(recorder.EventLoading)
		if len(evs) == 0 {
			t.Fatal("WaitForStart did not wait")
		}
		if evs[0].Message != recorder.ReasonStarting {
			t.Errorf("Message = %q, want %q", evs[0].Message, recorder.ReasonStarting)
		}
	})
}

// A cancelled context must not be mistaken for a server that is loading: the
// run returns at once rather than polling out its whole budget.
func TestRecordCancelledWhileWaiting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(loadingBody))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := recorder.Record(ctx, recorder.Options{
			BaseURL:     srv.URL,
			FSRoot:      t.TempDir(),
			GPU:         gpu.Null{},
			LoadingPoll: time.Hour,
		})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, recorder.ErrUnreachable) {
			t.Fatalf("err = %v, want ErrUnreachable", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a cancelled run kept waiting")
	}
}

// With no /proc to read a load average from and no GPU backend to count
// foreign processes with, the run must not claim the host was quiet. Both
// cards print a zero ContentionInfo as "?".
func TestContentionUnknownWithoutAnyReading(t *testing.T) {
	srv := fakeServer(t)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:        srv.URL,
		MaxTokens:      64,
		SampleInterval: 20 * time.Millisecond,
		FSRoot:         t.TempDir(), // an empty tree: no /proc, no loadavg
		GPU:            gpu.Null{},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	got := tp.Summary.Contention
	if got.Contended || got.LoadAvg1 != 0 || got.OtherGPUProcs != 0 || len(got.Reasons) != 0 {
		t.Errorf("Contention = %+v, want the zero value (unknown)", got)
	}
	if !warnsAbout(tp.Summary.Warnings, "contention not judged") {
		t.Errorf("no warning about the unjudged contention: %q", tp.Summary.Warnings)
	}
}

// Every warning must fit one line of the card, which is what keeps the
// figures visible on a degraded run.
func TestWarningsFitOneCardLine(t *testing.T) {
	const maxWarning = 58
	srv := fakeServer(t)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:        srv.URL,
		MaxTokens:      64,
		SampleInterval: 20 * time.Millisecond,
		FSRoot:         t.TempDir(),
		GPU:            gpu.Null{},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(tp.Summary.Warnings) == 0 {
		t.Fatal("a fully degraded run produced no warnings")
	}
	for _, w := range tp.Summary.Warnings {
		if len(w) > maxWarning {
			t.Errorf("warning is %d chars, want <= %d: %q", len(w), maxWarning, w)
		}
	}
}

// copyFlushing proxies a body through, flushing every chunk so a streamed SSE
// response reaches the client as it is produced rather than at the end.
func copyFlushing(w http.ResponseWriter, r interface{ Read([]byte) (int, error) }) (int64, error) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	var n int64
	for {
		c, err := r.Read(buf)
		if c > 0 {
			written, werr := w.Write(buf[:c])
			n += int64(written)
			if flusher != nil {
				flusher.Flush()
			}
			if werr != nil {
				return n, werr
			}
		}
		if err != nil {
			return n, nil
		}
	}
}
