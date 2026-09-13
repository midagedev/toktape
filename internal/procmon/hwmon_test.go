package procmon_test

import (
	"errors"
	"testing"

	"github.com/midagedev/toktape/internal/procmon"
)

// TestReadHwmonTemp (TTP-57): the CPU package sensor wins over whatever else
// sits on the bus. The busy fixture puts an NVMe controller at hwmon0 and the
// k10temp at hwmon1 with a per-CCD temp3 next to Tctl, so a reader that takes
// the first chip, or the first temp*_input of the right chip, fails here.
func TestReadHwmonTemp(t *testing.T) {
	c, sensor, err := procmon.ReadHwmonTemp(busyRoot)
	if err != nil {
		t.Fatalf("ReadHwmonTemp: %v", err)
	}
	if c != 50 || sensor != "k10temp Tctl" {
		t.Errorf("ReadHwmonTemp(busy) = %v, %q; want 50, \"k10temp Tctl\" (not the nvme at hwmon0, not Tccd1)", c, sensor)
	}
}

func TestReadHwmonTempFallbacks(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		wantC      float64
		wantSensor string
	}{
		{
			name: "no CPU driver: the first chip's temp1",
			files: map[string]string{
				"sys/class/hwmon/hwmon0/name":        "nvme\n",
				"sys/class/hwmon/hwmon0/temp1_label": "Composite\n",
				"sys/class/hwmon/hwmon0/temp1_input": "43850\n",
			},
			wantC:      43.85,
			wantSensor: "nvme Composite",
		},
		{
			name: "chips are ordered by number, not lexically",
			files: map[string]string{
				"sys/class/hwmon/hwmon10/name":        "nvme\n",
				"sys/class/hwmon/hwmon10/temp1_input": "43850\n",
				"sys/class/hwmon/hwmon2/name":         "nct6798\n",
				"sys/class/hwmon/hwmon2/temp1_label":  "CPUTIN\n",
				"sys/class/hwmon/hwmon2/temp1_input":  "39000\n",
			},
			wantC:      39,
			wantSensor: "nct6798 CPUTIN",
		},
		{
			name: "no label: the input's own name",
			files: map[string]string{
				"sys/class/hwmon/hwmon0/name":        "acpitz\n",
				"sys/class/hwmon/hwmon0/temp1_input": "27800\n",
			},
			wantC:      27.8,
			wantSensor: "acpitz temp1",
		},
		{
			name: "no name file: the directory is the chip",
			files: map[string]string{
				"sys/class/hwmon/hwmon3/temp1_input": "31000\n",
			},
			wantC:      31,
			wantSensor: "hwmon3 temp1",
		},
		{
			name: "Intel: Package id 0 over the cores",
			files: map[string]string{
				"sys/class/hwmon/hwmon0/name":        "coretemp\n",
				"sys/class/hwmon/hwmon0/temp1_label": "Package id 0\n",
				"sys/class/hwmon/hwmon0/temp1_input": "58000\n",
				"sys/class/hwmon/hwmon0/temp2_label": "Core 0\n",
				"sys/class/hwmon/hwmon0/temp2_input": "61000\n",
			},
			wantC:      58,
			wantSensor: "coretemp Package id 0",
		},
		{
			name: "preferred chip, unpreferred labels: falls back to its temp1",
			files: map[string]string{
				"sys/class/hwmon/hwmon0/name":        "k10temp\n",
				"sys/class/hwmon/hwmon0/temp1_label": "Tccd1\n",
				"sys/class/hwmon/hwmon0/temp1_input": "61250\n",
			},
			wantC:      61.25,
			wantSensor: "k10temp Tccd1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, sensor, err := procmon.ReadHwmonTemp(writeTree(t, tc.files))
			if err != nil {
				t.Fatalf("ReadHwmonTemp: %v", err)
			}
			if c != tc.wantC || sensor != tc.wantSensor {
				t.Errorf("ReadHwmonTemp = %v, %q; want %v, %q", c, sensor, tc.wantC, tc.wantSensor)
			}
		})
	}
}

// TestReadHwmonTempNoReading: a box with no hwmon at all, and one whose chips
// publish no temperature, leave the sensor "" — the witness then carries no
// temperature and the card prints nothing about it, rather than a 0 °C that
// was never observed.
func TestReadHwmonTempNoReading(t *testing.T) {
	// The base fixture tree has a cpufreq cap but no hwmon.
	for _, root := range []string{"testdata", t.TempDir()} {
		c, sensor, err := procmon.ReadHwmonTemp(root)
		if !errors.Is(err, procmon.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", root, err)
		}
		if c != 0 || sensor != "" {
			t.Errorf("%s: = %v, %q; want 0, \"\"", root, c, sensor)
		}
	}
	// A fan controller with no temp*_input is a chip with nothing to say.
	root := writeTree(t, map[string]string{"sys/class/hwmon/hwmon0/name": "nzxt_smart2\n"})
	if _, sensor, err := procmon.ReadHwmonTemp(root); sensor != "" || !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("chip without temperatures: = %q, %v; want \"\", ErrNotFound", sensor, err)
	}
}
