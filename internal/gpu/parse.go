package gpu

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// QueryGPUFields is the --query-gpu field list this package parses, in order.
// ParseQueryGPU requires exactly this many columns; a mismatch means the
// query changed and is reported as an error rather than silently mis-parsed.
//
// The trailing "uuid" is not in the original track spec. It is required
// because --query-compute-apps carries no device index: without the
// uuid <-> index join, per-device ProcBytes and OtherProcs cannot be filled
// on a multi-GPU host, which is the case the feature exists for.
var QueryGPUFields = []string{
	"index",
	"name",
	"memory.total",
	"memory.used",
	"utilization.gpu",
	"temperature.gpu",
	"power.draw",
	"clocks.sm",
	"clocks_throttle_reasons.active",
	"driver_version",
	"pcie.link.gen.current",
	"pcie.link.width.current",
	"uuid",
}

// ComputeAppFields is the --query-compute-apps field list this package parses.
var ComputeAppFields = []string{"gpu_uuid", "pid", "used_memory"}

// mib is the unit of every "memory.*" and "used_memory" cell under
// --format=nounits. nvidia-smi reports MiB, not MB.
const mib = int64(1024 * 1024)

// DeviceRow is one row of `nvidia-smi --query-gpu=<QueryGPUFields>
// --format=csv,noheader,nounits`. It carries both the static device facts
// (Devices) and the per-sample readings (Sample) so one exec call serves
// both. Unknown cells ("[N/A]", "[Not Supported]", ...) are the zero value.
type DeviceRow struct {
	Index            int
	Name             string
	MemoryTotalBytes int64
	MemoryUsedBytes  int64
	UtilPct          float64
	TempC            float64
	PowerW           float64
	ClockSMMHz       int
	// ThrottleMask is the clocks_throttle_reasons.active bitmask.
	// ThrottleKnown is false when the device did not report it.
	ThrottleMask  uint64
	ThrottleKnown bool
	Driver        string
	PCIeGen       string
	PCIeWidth     string
	UUID          string
}

// PCIe renders the link as "4.0 x16", or "" when either half is unknown.
func (r DeviceRow) PCIe() string {
	if r.PCIeGen == "" || r.PCIeWidth == "" {
		return ""
	}
	return r.PCIeGen + ".0 x" + r.PCIeWidth
}

// ComputeApp is one row of `nvidia-smi --query-compute-apps=<ComputeAppFields>
// --format=csv,noheader,nounits`. GPUUUID is "" for the two-column form
// (pid,used_memory), which older call sites may produce.
type ComputeApp struct {
	GPUUUID   string
	PID       int
	UsedBytes int64
}

// ParseQueryGPU parses --query-gpu CSV text. It is a pure function of the
// text so it is testable without a GPU. Empty input yields an empty slice.
func ParseQueryGPU(s string) ([]DeviceRow, error) {
	records, err := readCSV(s)
	if err != nil {
		return nil, err
	}
	rows := make([]DeviceRow, 0, len(records))
	for i, rec := range records {
		line := i + 1
		if len(rec) != len(QueryGPUFields) {
			return nil, fmt.Errorf("query-gpu row %d: got %d columns, want %d (%s)", line, len(rec), len(QueryGPUFields), strings.Join(QueryGPUFields, ","))
		}
		var row DeviceRow
		var err error
		if row.Index, err = intCell(rec[0], line, QueryGPUFields[0]); err != nil {
			return nil, err
		}
		row.Name = stringCell(rec[1])
		mt, err := intCell(rec[2], line, QueryGPUFields[2])
		if err != nil {
			return nil, err
		}
		row.MemoryTotalBytes = int64(mt) * mib
		mu, err := intCell(rec[3], line, QueryGPUFields[3])
		if err != nil {
			return nil, err
		}
		row.MemoryUsedBytes = int64(mu) * mib
		if row.UtilPct, err = floatCell(rec[4], line, QueryGPUFields[4]); err != nil {
			return nil, err
		}
		if row.TempC, err = floatCell(rec[5], line, QueryGPUFields[5]); err != nil {
			return nil, err
		}
		if row.PowerW, err = floatCell(rec[6], line, QueryGPUFields[6]); err != nil {
			return nil, err
		}
		if row.ClockSMMHz, err = intCell(rec[7], line, QueryGPUFields[7]); err != nil {
			return nil, err
		}
		if row.ThrottleMask, row.ThrottleKnown, err = maskCell(rec[8], line, QueryGPUFields[8]); err != nil {
			return nil, err
		}
		row.Driver = stringCell(rec[9])
		row.PCIeGen = stringCell(rec[10])
		row.PCIeWidth = stringCell(rec[11])
		row.UUID = stringCell(rec[12])
		rows = append(rows, row)
	}
	return rows, nil
}

// ParseComputeApps parses --query-compute-apps CSV text. Rows of three
// columns (gpu_uuid,pid,used_memory) and of two (pid,used_memory) are both
// accepted; the two-column form leaves GPUUUID empty, which means the row
// can only be attributed to a device on a single-GPU host.
func ParseComputeApps(s string) ([]ComputeApp, error) {
	records, err := readCSV(s)
	if err != nil {
		return nil, err
	}
	apps := make([]ComputeApp, 0, len(records))
	for i, rec := range records {
		line := i + 1
		var app ComputeApp
		var pidCell, memCell string
		switch len(rec) {
		case 3:
			app.GPUUUID = stringCell(rec[0])
			pidCell, memCell = rec[1], rec[2]
		case 2:
			pidCell, memCell = rec[0], rec[1]
		default:
			return nil, fmt.Errorf("compute-apps row %d: got %d columns, want 3 (%s) or 2 (pid,used_memory)", line, len(rec), strings.Join(ComputeAppFields, ","))
		}
		if app.PID, err = intCell(pidCell, line, "pid"); err != nil {
			return nil, err
		}
		used, err := intCell(memCell, line, "used_memory")
		if err != nil {
			return nil, err
		}
		app.UsedBytes = int64(used) * mib
		apps = append(apps, app)
	}
	return apps, nil
}

// readCSV splits nvidia-smi's ", "-separated output into records. Blank
// lines are skipped and leading spaces trimmed, matching the real format.
func readCSV(s string) ([][]string, error) {
	r := csv.NewReader(strings.NewReader(s))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1
	r.ReuseRecord = false
	var out [][]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("nvidia-smi csv: %w", err)
		}
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// unknownCell reports whether a cell carries no value. nvidia-smi brackets
// every such cell: "[N/A]", "[Not Supported]", "[Unknown Error]",
// "[Insufficient Permissions]". A bare "N/A" is accepted too.
func unknownCell(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "N/A") {
		return true
	}
	return strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")
}

func stringCell(s string) string {
	if unknownCell(s) {
		return ""
	}
	return strings.TrimSpace(s)
}

func intCell(s string, line int, field string) (int, error) {
	if unknownCell(s) {
		return 0, nil
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("row %d field %s: %q is not a number: %w", line, field, strings.TrimSpace(s), err)
	}
	return int(v), nil
}

func floatCell(s string, line int, field string) (float64, error) {
	if unknownCell(s) {
		return 0, nil
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("row %d field %s: %q is not a number: %w", line, field, strings.TrimSpace(s), err)
	}
	return v, nil
}

// maskCell parses a clocks_throttle_reasons.active cell, which nvidia-smi
// prints as hex ("0x0000000000000004"). Decimal is accepted too.
func maskCell(s string, line int, field string) (uint64, bool, error) {
	if unknownCell(s) {
		return 0, false, nil
	}
	t := strings.TrimSpace(s)
	base := 10
	if lower := strings.ToLower(t); strings.HasPrefix(lower, "0x") {
		t, base = lower[2:], 16
	}
	v, err := strconv.ParseUint(t, base, 64)
	if err != nil {
		return 0, false, fmt.Errorf("row %d field %s: %q is not a bitmask: %w", line, field, strings.TrimSpace(s), err)
	}
	return v, true, nil
}
