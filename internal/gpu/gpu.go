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
	"fmt"

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
	out, err := c.run(ctx, queryGPUArgs()...)
	if err != nil {
		return Null{}, []string{fmt.Sprintf("nvidia-smi unavailable (%v), GPU metrics disabled", err)}
	}
	if _, err := ParseQueryGPU(out); err != nil {
		return Null{}, []string{fmt.Sprintf("nvidia-smi output not understood (%v), GPU metrics disabled", err)}
	}
	return c, []string{WarnNoNVML}
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
