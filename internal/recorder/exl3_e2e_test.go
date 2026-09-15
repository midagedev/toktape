package recorder_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
)

// The ExLlamaV3 end-to-end runs (2026-09-15): a fake llama-server-protocol
// shim whose /props carries the engine object, over a fake /proc tree shaped
// like the engine's two processes, through the same recorded SSE stream the
// llama.cpp end-to-end uses.
//
// The engine paths landed before this file, so its failure mode was checked
// by mutation instead: with collectModel's engine gate disabled the
// end-to-end fails on exactly the rows that make an engine card — no format,
// arch, bytes, params or quant, a "model file not readable" warning, the
// model's active figure gone — and re-enabling the gate turns it green.

// exl3ModelPath is the engine contract's model: a directory of 30 EXL3 files,
// not a GGUF, so nothing may try to open it.
const exl3ModelPath = "/models/GLM-5.3-Flash-exl3-4.05"

// The engine object, verbatim from the contract, with the devices array and
// the model's active figure left to the case: every validation outcome is a
// variation of those two.
const exl3EngineHead = `{
  "model_path": "` + exl3ModelPath + `",
  "total_slots": 2,
  "default_generation_settings": {"n_ctx": 32768},
  "engine": {
    "name": "exllamav3", "version": "1.5.0",
    "args": ["-gs", "44,21", "-mcs", "185", "-mct", "32", "-mtp"],
    "draft": {"model": "mtp", "n_max": 1},
    "model": {
      "format": "exl3", "arch": "Glm5NextForConditionalGeneration",
      "quant": "EXL3 4.05 bpw · head 6.0", "bytes": 165151541665, "files": 30,
      "params": 320000000000, "n_layers": 45, "n_experts": 288, "n_experts_used": 8,
      "ctx_train": 1048576, "active_bytes_per_token": `

const exl3EngineTail = `
    },
    "placement": {
      "devices": [`
const exl3EngineAfterDevices = `],
      "vram_kv_bytes": 6000000000
    }
  }
}`

// The contract's three devices, without per-device active bytes — the normal
// case: dynamic expert placement has no static per-device answer.
const exl3DevicesNormal = `
        {"device": "GPU0", "bytes": 40000000000, "classes": {"attention": 8000000000, "experts": 30000000000, "embeddings": 2000000000}, "layers": "experts 0-20"},
        {"device": "GPU1", "bytes": 20000000000, "classes": {"experts": 19000000000, "output": 1000000000}, "layers": "experts 21-30"},
        {"device": "CPU",  "bytes": 105151541665, "classes": {"experts": 105151541665}, "layers": "experts 31-44"}`

func exl3Props(devices, modelActive string) string {
	return exl3EngineHead + modelActive + exl3EngineTail + devices + exl3EngineAfterDevices
}

// exl3Server is fakeServer with the /props body left to the case.
func exl3Server(t *testing.T, props string) *httptest.Server {
	t.Helper()
	sse, err := os.ReadFile(sseFile)
	if err != nil {
		t.Fatalf("read SSE fixture: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(props))
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":0,"is_processing":false,"n_ctx":2048,"n_past":0}]`))
	})
	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []tape.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var b strings.Builder
		for _, m := range in.Messages {
			b.WriteString("<|" + m.Role + "|>" + m.Content)
		}
		b.WriteString("<|assistant|>")
		_ = json.NewEncoder(w).Encode(map[string]string{"prompt": b.String()})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, ev := range bytes.SplitAfter(sse, []byte("\n\n")) {
			if len(bytes.TrimSpace(ev)) == 0 {
				continue
			}
			if _, err := w.Write(ev); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(time.Millisecond)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// exl3ProcTree builds the /proc the engine runs against: the host-level files
// copied from the shared fixture, the listener (8821) with the model path in
// its argv and the listening socket in its fd, and the CPU-expert child
// (8822) that makes the engine two processes. argvModel is the path FindPID
// is expected to match; when it differs from what /props names, the pid comes
// from the port instead.
func exl3ProcTree(t *testing.T, argvModel string, port int) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "proc"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cpuinfo", "loadavg", "meminfo", "version"} {
		b, err := os.ReadFile(filepath.Join(procRoot, "proc", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(root, "proc", name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, sub := range []string{filepath.Join("proc", "sys", "kernel", "hostname"), filepath.Join("sys", "devices", "system", "cpu", "cpu0", "cpufreq", "scaling_max_freq")} {
		b, err := os.ReadFile(filepath.Join(procRoot, sub))
		if err != nil {
			t.Fatalf("read %s: %v", sub, err)
		}
		dst := filepath.Join(root, sub)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The listener: RSS 60000 kB, its own faults and ticks; the socket.
	listener := "python\x00-m\x00exllamav3.server\x00--model\x00" + argvModel +
		"\x00--port\x00" + strconv.Itoa(port) + "\x00-gs\x0044,21\x00-mcs\x00185\x00-mct\x0032\x00-mtp\x00"
	writeExl3PID(t, root, 8821, 1, 60000, 10, 100, 1000, 100, listener, "40001")
	// The child doing the CPU expert work: RSS 40000 kB, its own counters,
	// no socket — its parent holds the port.
	writeExl3PID(t, root, 8822, 8821, 40000, 20, 200, 2000, 200,
		"python\x00-m\x00exllamav3.expert_worker\x00", "")

	// The listening socket: one LISTEN row on the fake server's port, held
	// by 8821's fd. The httptest port is only known once the server exists,
	// which is why the tree is written after it.
	row := fmt.Sprintf("   0: 0100007F:%04X 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 40001 1 0000000000000000 100 0 0 10 0\n", port)
	tcp := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrns   uid  timeout inode\n" + row
	if err := os.MkdirAll(filepath.Join(root, "proc", "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "proc", "net", "tcp"), []byte(tcp), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// writeExl3PID writes one fixture process of the engine's tree: status with
// the given RSS halves, stat with the given ppid/faults/ticks, the cmdline,
// and — when inode is not empty — an fd symlink holding that socket.
func writeExl3PID(t *testing.T, root string, pid, ppid, rss, maj, min, utime, stime int, cmdline, inode string) {
	t.Helper()
	dir := filepath.Join(root, "proc", strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	status := "Name:\tpython\nUmask:\t0022\nState:\tS (sleeping)\nTgid:\t" + strconv.Itoa(pid) +
		"\nPid:\t" + strconv.Itoa(pid) + "\nPPid:\t" + strconv.Itoa(ppid) +
		"\nVmPeak:\t 1048576 kB\nVmSize:\t 1048576 kB\nVmHWM:\t " + strconv.Itoa(rss) + " kB\nVmRSS:\t " + strconv.Itoa(rss) + " kB" +
		"\nRssAnon:\t " + strconv.Itoa(rss/2) + " kB\nRssFile:\t " + strconv.Itoa(rss/2) + " kB\nRssShmem:\t 0 kB\nVmSwap:\t 0 kB\nThreads:\t16\n"
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
	stat := strconv.Itoa(pid) + " (python) S " + strconv.Itoa(ppid) + " " + strconv.Itoa(pid) + " " + strconv.Itoa(pid) +
		" 0 -1 0 " + strconv.Itoa(min) + " 0 " + strconv.Itoa(maj) + " 0 " + strconv.Itoa(utime) + " " + strconv.Itoa(stime) +
		" 0 0 20 0 16 0 12345 0 0 0 1 0 0 0 0 0 0 0 0 0\n"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644); err != nil {
		t.Fatal(err)
	}
	if inode != "" {
		fd := filepath.Join(dir, "fd")
		if err := os.MkdirAll(fd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("socket:["+inode+"]", filepath.Join(fd, "3")); err != nil {
			t.Fatal(err)
		}
	}
}

// exl3ServerPort pulls the port out of an httptest URL for the net/tcp row.
func exl3ServerPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse %s: %v", srv.URL, err)
	}
	n, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("port in %s: %v", srv.URL, err)
	}
	return n
}

// TestRecordExLlamaV3EndToEnd: the whole engine run — /props with the engine
// object, the two-process tree, the streamed timings — and every wiring the
// card's engine rows depend on.
func TestRecordExLlamaV3EndToEnd(t *testing.T) {
	srv := exl3Server(t, exl3Props(exl3DevicesNormal, "7000000000"))
	opts := recorder.Options{
		BaseURL:        srv.URL,
		Concurrency:    2,
		MaxTokens:      256,
		SampleInterval: 10 * time.Millisecond,
		FSRoot:         exl3ProcTree(t, exl3ModelPath, exl3ServerPort(t, srv)),
		GPU:            fakeGPU(t),
		Clock:          fixedClock{time.Date(2026, 9, 15, 21, 8, 44, 0, time.UTC)},
		Version:        "0.1.0-test",
	}
	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary

	// The engine object is the record.
	if s.Server.Kind != tape.ServerExLlamaV3 {
		t.Errorf("Kind = %q, want %q", s.Server.Kind, tape.ServerExLlamaV3)
	}
	if s.Server.Build != "1.5.0" || s.Server.Commit != "" {
		t.Errorf("Build, Commit = %q, %q; want 1.5.0 and no commit", s.Server.Build, s.Server.Commit)
	}
	if s.Model.Format != "exl3" || s.Model.Arch != "Glm5NextForConditionalGeneration" {
		t.Errorf("Model.Format/Arch = %q/%q", s.Model.Format, s.Model.Arch)
	}
	if s.Model.FileBytes != 165151541665 || s.Model.Params != 320000000000 {
		t.Errorf("Model.FileBytes/Params = %d/%d", s.Model.FileBytes, s.Model.Params)
	}
	if s.Model.Quant != "EXL3 4.05 bpw · head 6.0" {
		t.Errorf("Model.Quant = %q, want it verbatim from the engine", s.Model.Quant)
	}
	if s.Model.Path != exl3ModelPath || s.Model.FileName != "GLM-5.3-Flash-exl3-4.05" {
		t.Errorf("Model.Path/FileName = %q/%q", s.Model.Path, s.Model.FileName)
	}
	// The model is a directory: no GGUF was opened, so the degraded-header
	// warning must not appear — the engine supplied every figure it stood for.
	if warnsAbout(s.Warnings, "model file not readable") {
		t.Errorf("Warnings mention an unreadable model: %q", s.Warnings)
	}

	// The placement, reported not replayed.
	if s.Placement.Source != "engine" {
		t.Errorf("Placement.Source = %q, want \"engine\"", s.Placement.Source)
	}
	if len(s.Placement.Devices) != 3 {
		t.Fatalf("Placement.Devices = %d, want 3", len(s.Placement.Devices))
	}
	for i, want := range []string{"GPU0", "GPU1", tape.DeviceCPU} {
		if got := s.Placement.Devices[i].Device; got != want {
			t.Errorf("device %d = %q, want %q", i, got, want)
		}
	}
	if got := s.Placement.Devices[0].Classes[tape.ClassAttention]; got != 8000000000 {
		t.Errorf("GPU0 attention class = %d, want 8000000000", got)
	}
	if s.Placement.VRAMWeightsBytes != 60000000000 || s.Placement.VRAMKVBytes != 6000000000 {
		t.Errorf("VRAM weights/KV = %d/%d", s.Placement.VRAMWeightsBytes, s.Placement.VRAMKVBytes)
	}
	// The normal case: no per-device active bytes, the model's figure kept,
	// and no warning for what the engine was right to omit.
	if s.Model.ActiveBytesPerToken != 7000000000 {
		t.Errorf("Model.ActiveBytesPerToken = %d, want the engine's 7000000000", s.Model.ActiveBytesPerToken)
	}
	if warnsAbout(s.Warnings, "active") {
		t.Errorf("the normal absent split drew a warning: %q", s.Warnings)
	}

	// The flags: the engine's argv, not llama.cpp's.
	wantArgs := []string{"-gs", "44,21", "-mcs", "185", "-mct", "32", "-mtp"}
	// 2026-09-15 (lead review): engine.draft fills the two fields -md and
	// --draft-max fill for llama-server. FAIL-first: both were "" and the
	// card printed "Draft ? · n_max ?".
	if s.Server.Flags.DraftModel != "mtp" || s.Server.Flags.DraftMax != "1" {
		t.Errorf("draft flags = %q / %q, want mtp / 1", s.Server.Flags.DraftModel, s.Server.Flags.DraftMax)
	}
	if len(s.Server.Flags.Other) != len(wantArgs) {
		t.Fatalf("Flags.Other = %q, want %q", s.Server.Flags.Other, wantArgs)
	}
	for i, a := range wantArgs {
		if s.Server.Flags.Other[i] != a {
			t.Errorf("Flags.Other[%d] = %q, want %q", i, s.Server.Flags.Other[i], a)
		}
	}
	if s.Server.Flags.FlashAttn != "" || s.Server.Flags.NGL != "" {
		t.Errorf("engine flags were parsed as llama.cpp's: %+v", s.Server.Flags)
	}

	// The two processes: pid by the model path in the argv, memory summed
	// over the tree, and the warning that says so.
	if s.Server.PID != 8821 {
		t.Errorf("PID = %d, want 8821 by the model path in the argv", s.Server.PID)
	}
	if len(s.Server.Args) == 0 || s.Server.Args[0] != "python" {
		t.Errorf("Server.Args = %q, want the listener's own argv", s.Server.Args)
	}
	if want := int64(100000) * 1024; s.Memory.AtEnd.RSSBytes != want {
		t.Errorf("AtEnd.RSSBytes = %d, want the tree sum %d", s.Memory.AtEnd.RSSBytes, want)
	}
	if !warnsAbout(s.Warnings, "memory, faults and CPU summed over 2 processes (pid 8821 and its children)") {
		t.Errorf("Warnings lack the tree warning: %q", s.Warnings)
	}
	// The model is a directory, so no single mapping identifies it and the
	// figure stays honestly absent.
	if s.Memory.MappedFileBytes != 0 {
		t.Errorf("MappedFileBytes = %d, want 0 for a directory model", s.Memory.MappedFileBytes)
	}

	// Warnings are printed verbatim on a 72-column card.
	for _, w := range s.Warnings {
		if len(w) > 120 || strings.Contains(w, ": open ") {
			t.Errorf("warning is not a short human sentence: %q", w)
		}
	}
	// The run itself recorded.
	if s.Timings.PredictedN == 0 || s.Aggregate.Streams != 2 {
		t.Errorf("timings = %+v, aggregate = %+v", s.Timings, s.Aggregate)
	}
}

// TestRecordExLlamaV3Validations: the recorder's checks over the engine's
// placement report, each leaving the honest data in place and saying what it
// found. The consistent split is the end-to-end's normal case run with the
// figures the engine does send when it has them.
func TestRecordExLlamaV3Validations(t *testing.T) {
	const consistent = `
        {"device": "GPU0", "bytes": 40000000000, "active_bytes_per_token": 3000000000, "classes": {"attention": 8000000000, "experts": 30000000000, "embeddings": 2000000000}, "layers": "experts 0-20"},
        {"device": "GPU1", "bytes": 20000000000, "active_bytes_per_token": 1500000000, "classes": {"experts": 19000000000, "output": 1000000000}, "layers": "experts 21-30"},
        {"device": "CPU",  "bytes": 105151541665, "active_bytes_per_token": 2500000000, "classes": {"experts": 105151541665}, "layers": "experts 31-44"}`
	cases := []struct {
		name            string
		devices         string
		modelActive     string
		wantWarning     string
		noWarning       string
		wantModelActive int64
		wantActiveOn    int // devices left carrying an active figure
	}{
		{
			name:            "consistent split is kept whole",
			devices:         consistent,
			modelActive:     "7000000000",
			noWarning:       "active",
			wantModelActive: 7000000000,
			wantActiveOn:    3,
		},
		{
			name: "class sum disagrees with the device bytes: both kept",
			devices: `
        {"device": "GPU0", "bytes": 41000000000, "classes": {"attention": 8000000000, "experts": 30000000000, "embeddings": 2000000000}, "layers": "experts 0-20"},
        {"device": "GPU1", "bytes": 20000000000, "classes": {"experts": 19000000000, "output": 1000000000}, "layers": "experts 21-30"},
        {"device": "CPU",  "bytes": 105151541665, "classes": {"experts": 105151541665}, "layers": "experts 31-44"}`,
			modelActive:     "7000000000",
			wantWarning:     "the device's own figure is kept",
			wantModelActive: 7000000000,
		},
		{
			name: "active split disagrees with the model: all zeroed",
			devices: `
        {"device": "GPU0", "bytes": 40000000000, "active_bytes_per_token": 3000000000, "classes": {"attention": 8000000000, "experts": 30000000000, "embeddings": 2000000000}, "layers": "experts 0-20"},
        {"device": "GPU1", "bytes": 20000000000, "active_bytes_per_token": 1500000000, "classes": {"experts": 19000000000, "output": 1000000000}, "layers": "experts 21-30"},
        {"device": "CPU",  "bytes": 105151541665, "active_bytes_per_token": 500000000, "classes": {"experts": 105151541665}, "layers": "experts 31-44"}`,
			modelActive: "7000000000",
			wantWarning: "all active figures zeroed",
		},
		{
			name: "partial active split: all zeroed",
			devices: `
        {"device": "GPU0", "bytes": 40000000000, "active_bytes_per_token": 3000000000, "classes": {"attention": 8000000000, "experts": 30000000000, "embeddings": 2000000000}, "layers": "experts 0-20"},
        {"device": "GPU1", "bytes": 20000000000, "active_bytes_per_token": 4000000000, "classes": {"experts": 19000000000, "output": 1000000000}, "layers": "experts 21-30"},
        {"device": "CPU",  "bytes": 105151541665, "classes": {"experts": 105151541665}, "layers": "experts 31-44"}`,
			modelActive: "7000000000",
			wantWarning: "all active figures zeroed",
		},
		{
			name: "unknown GPU index: kept and said",
			devices: exl3DevicesNormal + `,
        {"device": "GPU7", "bytes": 1000000000, "classes": {"output": 1000000000}, "layers": "output"}`,
			modelActive:     "7000000000",
			wantWarning:     "GPU7 is not among this host's 2 GPUs",
			wantModelActive: 7000000000,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := exl3Server(t, exl3Props(tc.devices, tc.modelActive))
			tp, err := recorder.Record(context.Background(), recorder.Options{
				BaseURL:        srv.URL,
				Concurrency:    1,
				MaxTokens:      256,
				SampleInterval: 10 * time.Millisecond,
				FSRoot:         exl3ProcTree(t, exl3ModelPath, exl3ServerPort(t, srv)),
				GPU:            fakeGPU(t),
				Clock:          fixedClock{time.Date(2026, 9, 15, 21, 8, 44, 0, time.UTC)},
				Version:        "0.1.0-test",
			})
			if err != nil {
				t.Fatalf("Record: %v", err)
			}
			s := tp.Summary
			if tc.wantWarning != "" && !warnsAbout(s.Warnings, tc.wantWarning) {
				t.Errorf("Warnings lack %q: %q", tc.wantWarning, s.Warnings)
			}
			if tc.noWarning != "" && warnsAbout(s.Warnings, tc.noWarning) {
				t.Errorf("Warnings wrongly mention %q: %q", tc.noWarning, s.Warnings)
			}
			if s.Model.ActiveBytesPerToken != tc.wantModelActive {
				t.Errorf("Model.ActiveBytesPerToken = %d, want %d", s.Model.ActiveBytesPerToken, tc.wantModelActive)
			}
			n := 0
			for _, d := range s.Placement.Devices {
				if d.ActiveBytesPerToken > 0 {
					n++
				}
			}
			if n != tc.wantActiveOn {
				t.Errorf("%d devices carry an active figure, want %d", n, tc.wantActiveOn)
			}
			// A zeroed split must not survive into the per-record bandwidth
			// line either: the requests were built after the zeroing.
			if tc.wantModelActive == 0 && s.Timings.EffectiveBandwidthBytesPerSec != 0 {
				t.Errorf("EffectiveBandwidthBytesPerSec = %d, want 0 from a zeroed split",
					s.Timings.EffectiveBandwidthBytesPerSec)
			}
		})
	}
}

// TestRecordExLlamaV3PIDByPort: the generic pid fallback. The /props names a
// model no argv carries (the shim's path, say), the URL is loopback, and the
// listening socket names the holder. The model-path match never runs, and the
// port match must not override it when both are available — that half is the
// end-to-end's, whose PID also came by the argv.
func TestRecordExLlamaV3PIDByPort(t *testing.T) {
	srv := exl3Server(t, exl3Props(exl3DevicesNormal, "7000000000"))
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:        srv.URL,
		Concurrency:    1,
		MaxTokens:      256,
		SampleInterval: 10 * time.Millisecond,
		// The argv names a directory the /props did not: no model-path match.
		FSRoot:  exl3ProcTree(t, "/models/some-other-checkpoint", exl3ServerPort(t, srv)),
		GPU:     fakeGPU(t),
		Clock:   fixedClock{time.Date(2026, 9, 15, 21, 8, 44, 0, time.UTC)},
		Version: "0.1.0-test",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary
	if s.Server.PID != 8821 {
		t.Errorf("PID = %d, want 8821 by the listening socket", s.Server.PID)
	}
	if warnsAbout(s.Warnings, "pid not found") {
		t.Errorf("Warnings say no pid was found: %q", s.Warnings)
	}
}

// TestRecordTreeOnlyForEngine: the same two-process tree under a llama.cpp
// run reads the parent alone. The tree sum is the engine's shape, not the
// recorder's default, and a llama-server whose children exist must not have
// them folded into its memory.
func TestRecordTreeOnlyForEngine(t *testing.T) {
	srv := exl3Server(t, propsJSON)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:        srv.URL,
		Concurrency:    1,
		MaxTokens:      256,
		SampleInterval: 10 * time.Millisecond,
		// The listener's argv names the llama model, so FindPID matches it
		// and the child is beside it, not part of it.
		FSRoot:  exl3ProcTree(t, modelPath, exl3ServerPort(t, srv)),
		GPU:     fakeGPU(t),
		Clock:   fixedClock{time.Date(2026, 9, 15, 21, 8, 44, 0, time.UTC)},
		Version: "0.1.0-test",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary
	if s.Server.Kind != tape.ServerLlamaCPP {
		t.Errorf("Kind = %q, want llama.cpp from build_info", s.Server.Kind)
	}
	if want := int64(60000) * 1024; s.Memory.AtEnd.RSSBytes != want {
		t.Errorf("AtEnd.RSSBytes = %d, want the parent's %d alone", s.Memory.AtEnd.RSSBytes, want)
	}
	if warnsAbout(s.Warnings, "summed over") {
		t.Errorf("a llama.cpp run drew the tree warning: %q", s.Warnings)
	}
}
