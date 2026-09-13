package recorder_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/recorder"
)

// TestRecordReportsAndRecordsTheCap pins the two places the generation cap has
// to appear, because a tile's "12/320" is drawn from a different one live than
// on replay:
//
//   - EventStreamStarted carries it, so the live screen has the budget before
//     the first token rather than only once the tape is written;
//   - PromptRecord.MaxTokens keeps it, so a replayed tape needs no guess at
//     which parameter key the server honoured.
func TestRecordReportsAndRecordsTheCap(t *testing.T) {
	srv := fakeServer(t)
	const (
		streams = 2
		cap320  = 320
	)

	var (
		mu    sync.Mutex
		caps  []int
		count int
	)
	opts := recorder.Options{
		BaseURL:        srv.URL,
		Concurrency:    streams,
		MaxTokens:      cap320,
		SampleInterval: 20 * time.Millisecond,
		FSRoot:         absRoot(t),
		GPU:            fakeGPU(t),
		Clock:          fixedClock{time.Date(2026, 9, 13, 7, 15, 0, 0, time.UTC)},
		Version:        "0.1.0-test",
		Progress: func(ev recorder.Event) {
			if ev.Kind != recorder.EventStreamStarted {
				return
			}
			mu.Lock()
			caps = append(caps, ev.MaxTokens)
			count++
			mu.Unlock()
		},
	}

	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if count != streams {
		t.Fatalf("%d stream-start events, want %d", count, streams)
	}
	for i, got := range caps {
		if got != cap320 {
			t.Errorf("stream %d started with MaxTokens = %d, want %d", i, got, cap320)
		}
	}
	for _, req := range tp.Requests {
		if req.Prompt.MaxTokens != cap320 {
			t.Errorf("stream %d recorded MaxTokens = %d, want %d", req.Index, req.Prompt.MaxTokens, cap320)
		}
	}
}
