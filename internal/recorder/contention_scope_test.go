package recorder

import (
	"context"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// openBackend is a collector that is not gpu.Null: the contention verdict
// declines to judge at all when nothing looked at the GPUs (gpuViewTaken),
// and what is under test here is what it does once something has.
type openBackend struct{}

func (openBackend) Devices(context.Context) ([]tape.GPUInfo, error)       { return nil, nil }
func (openBackend) Sample(context.Context, int) ([]tape.GPUSample, error) { return nil, nil }
func (openBackend) Name() string                                          { return "test" }
func (openBackend) Close()                                                {}

// A foreign process only counts against a run on a device the run's weights
// are on (TTP-153, 2026-09-21).
//
// Take 20260920-201746: a 281 MB process appeared on the idle 3090 for about
// half a second at t=44.5 of 45 s -- a card the server held nothing on -- and
// the whole run came back machine_contended. The reading was real; the verdict
// was not about this run. "Contended" claims something else was competing for
// what this run needed, and a device holding none of its weights is not that.
//
// FAIL-first: on the pre-change source the first sub-test reports
// Contended = true with "1 other GPU compute process".
func TestForeignProcessOnlyCountsWhereTheWeightsAre(t *testing.T) {
	// GPU0 holds the server's weights; GPU1 holds none of them. The busy one
	// is GPU1 in the first case and GPU0 in the second, and nothing else
	// differs -- so whatever the verdict does, it does because of the device.
	const (
		serverGPU = 0
		idleGPU   = 1
	)
	weights := int64(20) << 30

	samples := func(busy int) []tape.RunSample {
		return []tape.RunSample{
			{GPUs: []tape.GPUSample{
				{Index: serverGPU, ProcBytes: weights},
				{Index: idleGPU},
			}},
			// One sample out of two carries the sighting, the way the real
			// take carried it in 2 of 131.
			{GPUs: []tape.GPUSample{
				{Index: serverGPU, ProcBytes: weights, OtherProcs: b2i(busy == serverGPU)},
				{Index: idleGPU, OtherProcs: b2i(busy == idleGPU)},
			}},
		}
	}
	// lastGPUs' answer for the samples above: the end-of-run picture the
	// verdict scopes itself by.
	atEnd := func(busy int) []tape.GPUSample {
		return samples(busy)[1].GPUs
	}

	r := &run{gpus: openBackend{}}

	t.Run("a card the server does not use", func(t *testing.T) {
		got := r.contention(samples(idleGPU), atEnd(idleGPU))
		if got.Contended {
			t.Errorf("Contended = true for a process on a card holding none of the weights; reasons %q", got.Reasons)
		}
		if got.OtherGPUProcs != 0 {
			t.Errorf("OtherGPUProcs = %d, want 0: the summary counts the devices in play", got.OtherGPUProcs)
		}
	})

	t.Run("the card the server is on", func(t *testing.T) {
		got := r.contention(samples(serverGPU), atEnd(serverGPU))
		if !got.Contended {
			t.Error("Contended = false for a process on the card holding the weights")
		}
		if got.OtherGPUProcs != 1 {
			t.Errorf("OtherGPUProcs = %d, want 1", got.OtherGPUProcs)
		}
	})

	t.Run("no device named the server, so nothing can be dismissed", func(t *testing.T) {
		// gpusInPlay returns nil when no device reported ProcBytes at all:
		// the server's process was never identified, so a sighting cannot be
		// placed -- and a contention that cannot be placed is not one that
		// can be waved off. Every device counts, which is the conservative
		// reading and the one the old code gave unconditionally.
		blind := []tape.RunSample{{GPUs: []tape.GPUSample{
			{Index: serverGPU},
			{Index: idleGPU, OtherProcs: 1},
		}}}
		got := r.contention(blind, blind[0].GPUs)
		if !got.Contended {
			t.Error("Contended = false although no device identified the server's process")
		}
	})
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
