package recorder_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/recorder"
)

// slotServer is fakeServer with its /props body replaced, counting every
// request that is not /props — so a test can say no stream was sent.
func slotServer(t *testing.T, props string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	backend := fakeServer(t)
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // the SSE stream must arrive as it is written
	var others atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/props" {
			w.Header().Set("Server", "llama.cpp")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(props))
			return
		}
		if strings.Contains(r.URL.Path, "completion") {
			others.Add(1)
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &others
}

func slotOptions(t *testing.T, url string, sessions int) recorder.Options {
	return recorder.Options{
		BaseURL:        url,
		Concurrency:    sessions,
		MaxTokens:      16,
		SampleInterval: 20 * time.Millisecond,
		FSRoot:         absRoot(t),
		GPU:            fakeGPU(t),
		Clock:          fixedClock{time.Date(2026, 9, 14, 7, 15, 0, 0, time.UTC)},
		Version:        "0.1.0-test",
	}
}

// TestMoreSessionsThanSlotsIsRefused: five streams at a server whose /props
// says four slots is refused after attach and before any request, because the
// fifth stream would measure queue wait rather than concurrency (2026-09-14).
func TestMoreSessionsThanSlotsIsRefused(t *testing.T) {
	srv, streams := slotServer(t, propsJSON) // total_slots: 4
	_, err := recorder.Record(context.Background(), slotOptions(t, srv.URL, 5))
	if !errors.Is(err, recorder.ErrMoreSessionsThanSlots) {
		t.Fatalf("Record = %v, want ErrMoreSessionsThanSlots", err)
	}
	if errors.Is(err, recorder.ErrUnreachable) {
		t.Error("a refused run is reported as an unreachable server")
	}
	var se *recorder.SlotsError
	if !errors.As(err, &se) || se.Sessions != 5 || se.Slots != 4 || se.URL != srv.URL {
		t.Errorf("SlotsError = %+v, want 5 sessions, 4 slots at %s", se, srv.URL)
	}
	if n := streams.Load(); n != 0 {
		t.Errorf("%d completion requests were sent before the refusal", n)
	}
}

// TestSessionsAtTheSlotCountRun: exactly as many streams as slots is the
// whole point of --sessions and is not refused.
func TestSessionsAtTheSlotCountRun(t *testing.T) {
	srv, _ := slotServer(t, propsJSON)
	tp, err := recorder.Record(context.Background(), slotOptions(t, srv.URL, 4))
	if err != nil {
		t.Fatalf("Record at the slot count: %v", err)
	}
	if tp.Summary.Concurrency != 4 {
		t.Errorf("Concurrency = %d, want 4", tp.Summary.Concurrency)
	}
}

// TestUnknownSlotsRefuseNothing: a /props without total_slots is NSlots 0,
// which is unknown and not zero. There is no limit to enforce, so the check
// must not fire and invent one.
func TestUnknownSlotsRefuseNothing(t *testing.T) {
	props := strings.Replace(propsJSON, `"total_slots": 4,`, "", 1)
	if props == propsJSON {
		t.Fatal("the fixture no longer carries total_slots; this test checks nothing")
	}
	srv, _ := slotServer(t, props)
	tp, err := recorder.Record(context.Background(), slotOptions(t, srv.URL, 5))
	if err != nil {
		t.Fatalf("Record with unknown slots: %v", err)
	}
	if tp.Summary.Server.NSlots != 0 || tp.Summary.Concurrency != 5 {
		t.Errorf("NSlots %d, Concurrency %d; want 0 (unknown) and the 5 asked for",
			tp.Summary.Server.NSlots, tp.Summary.Concurrency)
	}
}
