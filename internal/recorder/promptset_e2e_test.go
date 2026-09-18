package recorder_test

import (
	"context"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// TestRecordStampsThePromptSet: a run that sent the published set says so, and
// a run that sent anything else says nothing (TTP-112). The second half is the
// one that matters — "" means "not in a comparison set", and a run that
// wrongly claimed the id would put its figures beside runs that did different
// work.
func TestRecordStampsThePromptSet(t *testing.T) {
	own := []server.StreamRequest{
		{Messages: []tape.Message{{Role: "user", Content: "alpha"}}},
		{Messages: []tape.Message{{Role: "user", Content: "beta"}}},
	}
	// Half the published set, half the user's: no single set ran, so there is
	// no set to name.
	mixed := append(server.DefaultPrompts(1), own[0])

	cases := []struct {
		name    string
		prompts []server.StreamRequest
		streams int
		want    string
	}{
		{"the default set, one stream", nil, 1, server.PromptSetID},
		{"the default set, several streams", nil, 4, server.PromptSetID},
		{"the user's own prompts", own, 4, ""},
		{"a mix", mixed, 2, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := fakeServer(t)
			tp, err := recorder.Record(context.Background(), recorder.Options{
				BaseURL:        srv.URL,
				Concurrency:    c.streams,
				Prompts:        c.prompts,
				FSRoot:         t.TempDir(),
				GPU:            gpu.Null{},
				SampleInterval: 50 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("Record: %v", err)
			}
			if got := tp.Summary.PromptSet; got != c.want {
				t.Errorf("PromptSet = %q, want %q", got, c.want)
			}
		})
	}
}
