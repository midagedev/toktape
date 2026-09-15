package gpu

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// Runner executes nvidia-smi with the given arguments and returns its
// stdout. It is the only impure part of the backend, so every parser can
// be tested against fixture text and a remote host can be served by an SSH
// sidecar that satisfies the same signature.
type Runner func(ctx context.Context, args ...string) (string, error)

// RunTimeout caps a single nvidia-smi call when the caller's context has no
// deadline of its own. A driver in a bad state makes nvidia-smi block
// indefinitely, which would wedge the sampler.
const RunTimeout = 5 * time.Second

// ExecRunner runs the real nvidia-smi binary.
func ExecRunner(ctx context.Context, args ...string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, RunTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "nvidia-smi", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("nvidia-smi %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return "", fmt.Errorf("nvidia-smi %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

func queryGPUArgs() []string {
	return []string{
		"--query-gpu=" + strings.Join(QueryGPUFields, ","),
		"--format=csv,noheader,nounits",
	}
}

func queryComputeAppsArgs() []string {
	return []string{
		"--query-compute-apps=" + strings.Join(ComputeAppFields, ","),
		"--format=csv,noheader,nounits",
	}
}

// smiCollector reads devices by shelling out to nvidia-smi.
type smiCollector struct {
	run Runner
}

// Name identifies the backend.
func (c *smiCollector) Name() string { return "nvidia-smi" }

// Close is a no-op: nothing is held open between calls.
func (c *smiCollector) Close() {}

func (c *smiCollector) rows(ctx context.Context) ([]DeviceRow, error) {
	out, err := c.run(ctx, queryGPUArgs()...)
	if err != nil {
		return nil, fmt.Errorf("query-gpu: %w", err)
	}
	rows, err := ParseQueryGPU(out)
	if err != nil {
		return nil, fmt.Errorf("query-gpu: %w", err)
	}
	return rows, nil
}

// Devices returns the static inventory, with PeakBandwidthBytesPerSec
// filled from the name table (0 when the card is not in it).
func (c *smiCollector) Devices(ctx context.Context) ([]tape.GPUInfo, error) {
	rows, err := c.rows(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]tape.GPUInfo, 0, len(rows))
	for _, r := range rows {
		infos = append(infos, tape.GPUInfo{
			Index:                    r.Index,
			Name:                     r.Name,
			VRAMBytes:                r.MemoryTotalBytes,
			Driver:                   r.Driver,
			PCIe:                     r.PCIe(),
			PeakBandwidthBytesPerSec: Bandwidth(r.Name),
		})
	}
	return infos, nil
}

// Sample reads every device once and attributes the running compute
// processes. ProcBytes is the VRAM held by serverPID on that device and
// OtherProcs counts the compute processes that are not it.
//
// serverPID <= 0 means the server process was not identified. In that case
// nothing is attributed to the server and OtherProcs stays 0, because
// counting the server itself as a foreign process would flip the card's
// contention verdict on a perfectly quiet host.
func (c *smiCollector) Sample(ctx context.Context, serverPID int) ([]tape.GPUSample, error) {
	rows, err := c.rows(ctx)
	if err != nil {
		return nil, err
	}
	samples := make([]tape.GPUSample, 0, len(rows))
	byUUID := make(map[string]int, len(rows))
	for i, r := range rows {
		if r.UUID != "" {
			byUUID[r.UUID] = i
		}
		samples = append(samples, tape.GPUSample{
			Index:     r.Index,
			UsedBytes: r.MemoryUsedBytes,
			UtilPct:   r.UtilPct,
			TempC:     r.TempC,
			PowerW:    r.PowerW,
			// The limit rides beside the draw and the mask beside the verdict,
			// so a rendered card can print "281 of 300 W" and narrow its
			// throttle verdict without re-reading the box it was recorded on
			// (2026-09-15). maskCell already zeroes an unreadable cell, so the
			// raw mask is 0 exactly when it was not read.
			PowerLimitW:  r.PowerLimitW,
			ClockMHz:     r.ClockSMMHz,
			ThrottleMask: r.ThrottleMask,
			Throttled:    r.ThrottleKnown && Throttled(r.ThrottleMask),
		})
	}
	if serverPID <= 0 {
		return samples, nil
	}
	out, err := c.run(ctx, queryComputeAppsArgs()...)
	if err != nil {
		return nil, fmt.Errorf("query-compute-apps: %w", err)
	}
	apps, err := ParseComputeApps(out)
	if err != nil {
		return nil, fmt.Errorf("query-compute-apps: %w", err)
	}
	for _, app := range apps {
		if app.PID <= 0 {
			// An unreadable pid cell ("[N/A]", "[Insufficient
			// Permissions]") must not be counted as a foreign process:
			// that would flip the card's contention verdict on the
			// strength of a cell we could not read.
			continue
		}
		i, ok := byUUID[app.GPUUUID]
		if !ok {
			// No uuid column (two-column form) or a device that is not in
			// the query-gpu output. Attribution is only unambiguous on a
			// single-GPU host; anywhere else the row is dropped rather
			// than assigned to a guessed device.
			if len(samples) != 1 {
				continue
			}
			i = 0
		}
		if app.PID == serverPID {
			samples[i].ProcBytes += app.UsedBytes
			continue
		}
		samples[i].OtherProcs++
	}
	return samples, nil
}
