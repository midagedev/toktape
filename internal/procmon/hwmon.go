package procmon

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The reading in this file is the temperature half of a contention witness
// (TTP-57, 2026-09-14). One number is enough: the card does not plot a thermal
// curve, it answers "was the box the same machine at the end as at the start".

// preferredChips are the hwmon drivers that publish the CPU package
// temperature — the one reading that explains a clock cap moving. Anything
// else on the bus (an NVMe controller, a motherboard sensor chip, a fan
// controller) is a temperature of something that is not the processor.
var preferredChips = map[string]bool{
	"k10temp":  true, // AMD Zen
	"zenpower": true, // the out-of-tree AMD driver
	"coretemp": true, // Intel
}

// preferredLabels are the package-level labels of those chips. Tctl is the
// control temperature the AMD boost algorithm reads (and so the one a watchdog
// acts on), Tdie the die temperature, "Package id 0" the Intel equivalent.
// A per-core or per-CCD label (Tccd1, "Core 0") is skipped: it is one corner
// of the part, not the operating point.
var preferredLabels = map[string]bool{
	"tctl":         true,
	"tdie":         true,
	"package id 0": true,
}

// ReadHwmonTemp returns one temperature from <fsRoot>/sys/class/hwmon, in
// degrees Celsius, with the name of the sensor it came from ("k10temp Tctl").
//
// Two passes, in this order: a chip in preferredChips with a label in
// preferredLabels, then — because a box whose CPU driver is not loaded still
// has a temperature worth recording — the first temp1_input of any chip. The
// chips and their temp*_input files are ordered by their number, not
// lexically, so "the first chip" means hwmon2 before hwmon10.
//
// The sensor name is what makes the reading comparable across the edges of a
// run: two temperatures from different chips say nothing when subtracted, so
// the caller (internal/card/conditions.go) compares them only when this string
// matches. An empty sensor means no reading at all and the temperature is then
// not a figure; the error wraps ErrNotFound so it degrades like every other
// witness reading.
func ReadHwmonTemp(fsRoot string) (c float64, sensor string, err error) {
	root := sysPath(fsRoot, "class", "hwmon")
	dirs, err := filepath.Glob(filepath.Join(root, "hwmon*"))
	if err != nil {
		return 0, "", fmt.Errorf("procmon: hwmon: glob %s: %w", root, err)
	}
	sortByIndex(dirs)

	for _, dir := range dirs {
		name := chipName(dir)
		if !preferredChips[strings.ToLower(name)] {
			continue
		}
		inputs, err := filepath.Glob(filepath.Join(dir, "temp*_input"))
		if err != nil {
			continue
		}
		sortByIndex(inputs)
		for _, in := range inputs {
			label := readTrimmed(strings.TrimSuffix(in, "_input") + "_label")
			if !preferredLabels[strings.ToLower(label)] {
				continue
			}
			v, err := readMilliDegrees(in)
			if err != nil {
				continue
			}
			return v, name + " " + label, nil
		}
	}

	for _, dir := range dirs {
		in := filepath.Join(dir, "temp1_input")
		v, err := readMilliDegrees(in)
		if err != nil {
			continue
		}
		label := readTrimmed(filepath.Join(dir, "temp1_label"))
		if label == "" {
			label = "temp1"
		}
		return v, chipName(dir) + " " + label, nil
	}

	return 0, "", fmt.Errorf("procmon: hwmon: no temperature under %s: %w", root, ErrNotFound)
}

// chipName is the hwmon chip's driver name, or the directory's own name when
// the chip publishes no name file — "hwmon3 temp1" is still a sensor that can
// be matched against itself at the other edge of the run.
func chipName(dir string) string {
	if n := readTrimmed(filepath.Join(dir, "name")); n != "" {
		return n
	}
	return filepath.Base(dir)
}

// readMilliDegrees reads a temp*_input file. hwmon publishes millidegrees
// Celsius, always as an integer.
func readMilliDegrees(path string) (float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("procmon: hwmon: %s: %w", path, err)
	}
	return float64(n) / 1000, nil
}

// readTrimmed reads a small sysfs string file. An unreadable one is "": every
// file under hwmon is optional, and a missing label is not an error.
func readTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// sortByIndex orders paths by the first number in their base name, so hwmon2
// sorts before hwmon10 and temp2_input before temp10_input. filepath.Glob
// returns a lexical order, in which "the first chip" would be a different chip
// on a box with ten of them.
func sortByIndex(paths []string) {
	sort.SliceStable(paths, func(i, j int) bool {
		a, b := baseIndex(paths[i]), baseIndex(paths[j])
		if a != b {
			return a < b
		}
		return paths[i] < paths[j]
	})
}

// baseIndex is the first run of digits in a path's base name, or -1.
func baseIndex(path string) int {
	base := filepath.Base(path)
	start := strings.IndexFunc(base, func(r rune) bool { return r >= '0' && r <= '9' })
	if start < 0 {
		return -1
	}
	end := start
	for end < len(base) && base[end] >= '0' && base[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(base[start:end])
	if err != nil {
		return -1
	}
	return n
}
