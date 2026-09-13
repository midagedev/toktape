// Package gpu reads the GPU side of a run: the static device inventory
// (tape.GPUInfo), periodic per-device samples (tape.GPUSample) and the
// contention verdict (tape.ContentionInfo).
//
// The backend is chosen at Open time and never fails: a host with no
// nvidia-smi gets a Null collector and a warning the card prints verbatim,
// so the rest of the recorder does not branch on GPU availability.
//
// Everything that interprets nvidia-smi output is a pure function of text
// (ParseQueryGPU, ParseComputeApps, Bandwidth, Contention, Throttled); only
// a thin, injectable Runner touches the process.
//
// No cgo: NVML is deliberately not linked. See nvml_notes.md.
package gpu

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// Collector reads one GPU backend.
type Collector interface {
	// Devices returns the static inventory, in index order.
	Devices(ctx context.Context) ([]tape.GPUInfo, error)
	// Sample returns one reading per device. serverPID is the llama-server
	// process whose VRAM is reported as GPUSample.ProcBytes; pass 0 when it
	// is unknown, and no process is attributed to the server.
	Sample(ctx context.Context, serverPID int) ([]tape.GPUSample, error)
	// Name identifies the backend for the card ("nvidia-smi", "none").
	Name() string
	// Close releases the backend. Safe to call more than once.
	Close()
}

// Warning strings Open can return. They are printed on the card verbatim,
// so they are sentences a reader understands without the source.
const (
	// WarnNoNVML is emitted whenever the nvidia-smi backend is in use: the
	// figures are a subprocess reading, not NVML's.
	WarnNoNVML = "nvml unavailable, VRAM from nvidia-smi"
)

// Open picks the best available backend and never fails. The order is
// nvidia-smi, then the Null collector. The returned warnings are appended
// to RunSummary.Warnings.
func Open(ctx context.Context) (Collector, []string) {
	return OpenWith(ctx, ExecRunner)
}

// OpenWith is Open with an injected Runner, for tests and for a remote
// (SSH sidecar) nvidia-smi.
func OpenWith(ctx context.Context, run Runner) (Collector, []string) {
	c := &smiCollector{run: run}
	args := queryGPUArgs()
	out, err := c.run(ctx, args...)
	if err != nil {
		return Null{}, []string{OpenFailureWarning(args, err)}
	}
	if _, err := ParseQueryGPU(out); err != nil {
		return Null{}, []string{fmt.Sprintf("nvidia-smi output not understood (%v), GPU metrics disabled", err)}
	}
	return c, []string{WarnNoNVML}
}

// OpenFailureWarning is the sentence the card prints when the nvidia-smi
// backend could not be opened.
//
// A machine with no NVIDIA driver is the ordinary case, not a fault, and the
// old copy reported it by pasting the whole failed command line onto the card:
// "nvidia-smi unavailable (nvidia-smi --query-gpu=index,name,... : exec: ...)".
// A reader cannot act on that. The two cases are told apart instead: a missing
// binary says so in five words, and a real failure keeps one line of the
// reason, with the argv this package itself chose stripped back off.
func OpenFailureWarning(args []string, err error) string {
	if errors.Is(err, exec.ErrNotFound) {
		return "nvidia-smi not found, GPU metrics disabled"
	}
	return fmt.Sprintf("nvidia-smi failed (%s), GPU metrics disabled", failureReason(args, err))
}

// failureReason is the first line of err with the command prefix ExecRunner
// added ("nvidia-smi <args>: ") removed, clipped so one warning cannot take
// three lines of a card that exists to show figures.
func failureReason(args []string, err error) string {
	if err == nil {
		return "unknown"
	}
	msg := err.Error()
	if prefix := "nvidia-smi " + strings.Join(args, " ") + ": "; strings.HasPrefix(msg, prefix) {
		msg = msg[len(prefix):]
	}
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "unknown"
	}
	const maxReason = 60
	if len(msg) > maxReason {
		msg = strings.TrimSpace(msg[:maxReason-1]) + "…"
	}
	return msg
}

// Null is the collector for a host with no readable GPU. Every method
// succeeds and returns nothing, so callers need no GPU-present branch.
type Null struct{}

// Devices returns no devices.
func (Null) Devices(context.Context) ([]tape.GPUInfo, error) { return nil, nil }

// Sample returns no samples.
func (Null) Sample(context.Context, int) ([]tape.GPUSample, error) { return nil, nil }

// Name identifies the backend.
func (Null) Name() string { return "none" }

// Close is a no-op.
func (Null) Close() {}

var _ Collector = Null{}
var _ Collector = (*smiCollector)(nil)
