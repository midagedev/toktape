package recorder_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// secretHost is the name planted in the fake /proc. It is distinctive on
// purpose: the assertion is a search of the written file's bytes, and a name
// like "host" would match something else and pass for the wrong reason.
const secretHost = "zzq-private-rig-7741"

// fsRootNamed is an FSRoot whose /proc/sys/kernel/hostname reads name.
func fsRootNamed(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "proc", "sys", "kernel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hostname"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatalf("write hostname: %v", err)
	}
	return root
}

// TestHostLabelKeepsTheNameOutOfTheFileBytes: with --host-label the machine's
// name is in no byte of the tape that gets written, and without it, it is.
//
// The assertion is over the file, decompressed, and not over the summary
// struct. Two fields copy the name onward today (record.go and reduce.go) and
// the substitution happens at neither of them — it happens once, where the
// host line is assembled. A third copy added later would pass a struct-level
// test and fail this one, which is the whole reason TTP-93 asked for it this
// way.
//
// FAIL-first: with recorder.Options.HostLabel dropped from collectHost, the
// labelled case finds the name in the file and fails.
func TestHostLabelKeepsTheNameOutOfTheFileBytes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		label   recorder.HostLabel
		inBytes bool
	}{
		{"no flag: the machine's name is in the file", recorder.HostLabel{}, true},
		{"a label: the name is in no byte of it", recorder.HostLabel{Set: true, Text: "workstation"}, false},
		{"an empty label: the same", recorder.HostLabel{Set: true, Text: ""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := rawServer(t)
			tp, err := recorder.Record(context.Background(), recorder.Options{
				BaseURL:     srv.URL,
				Prompts:     []server.StreamRequest{{Messages: []tape.Message{{Role: "user", Content: "Explain mmap."}}}},
				Concurrency: 1,
				MaxTokens:   320,
				Endpoint:    tape.EndpointCompletion,
				FSRoot:      fsRootNamed(t, secretHost),
				GPU:         gpu.Null{},
				HostLabel:   tc.label,
			})
			if err != nil {
				t.Fatalf("Record: %v", err)
			}
			path := filepath.Join(t.TempDir(), "run"+tape.Ext)
			if err := tape.Write(path, tp); err != nil {
				t.Fatalf("Write: %v", err)
			}
			// Read it back the way anyone else would, then search the JSON it
			// decompresses to — the gzip stream itself would hide the name
			// from a byte search and pass for the wrong reason.
			back, err := tape.Read(path)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			body, err := json.Marshal(back)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if got := bytes.Contains(body, []byte(secretHost)); got != tc.inBytes {
				t.Errorf("the name %q in the written tape = %v, want %v\nhostname=%q source=%q",
					secretHost, got, tc.inBytes, back.Summary.Host.Hostname, back.Summary.Host.HostnameSource)
			}
		})
	}
}
