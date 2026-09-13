package procmon_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/midagedev/toktape/internal/procmon"
)

// busyRoot is the fixture tree of a box loading a model from disk while a
// llama-bench runs next to the attached server (TTP-36).
const busyRoot = "testdata/busy"

func TestParseIOPressure(t *testing.T) {
	busy, err := os.ReadFile(filepath.Join(busyRoot, "proc", "pressure", "io"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		in      string
		want    float64
		wantErr bool
	}{
		{name: "model loading from NVMe", in: string(busy), want: 12.3},
		{
			name: "quiet box: 0 is a reading",
			in:   "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n",
			want: 0,
		},
		{
			name: "full line first is not read",
			in:   "full avg10=88.00 avg60=0.00 avg300=0.00 total=0\nsome avg10=91.25 avg60=40.10 avg300=12.00 total=77\n",
			want: 91.25,
		},
		{name: "no some line", in: "full avg10=11.87 avg60=8.02 avg300=2.87 total=887766554\n", wantErr: true},
		{name: "empty file", in: "", wantErr: true},
		{name: "some line without avg10", in: "some avg60=8.41 avg300=3.02 total=918273645\n", wantErr: true},
		{name: "unparseable avg10", in: "some avg10=n/a avg60=8.41 avg300=3.02 total=1\n", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := procmon.ParseIOPressure([]byte(tc.in))
			if tc.wantErr {
				if err == nil {
					t.Errorf("ParseIOPressure = %v, nil; want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseIOPressure: %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseIOPressure = %v, want %v", got, tc.want)
			}
		})
	}
	if _, err := procmon.ParseIOPressure([]byte("full avg10=1.00 avg60=0 avg300=0 total=0\n")); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("no some line: err = %v, want ErrNotFound", err)
	}
}

func TestReadIOPressure(t *testing.T) {
	got, err := procmon.ReadIOPressure(busyRoot)
	if err != nil {
		t.Fatalf("ReadIOPressure: %v", err)
	}
	if got != 12.3 {
		t.Errorf("ReadIOPressure = %v, want 12.3", got)
	}
	// A kernel without CONFIG_PSI has no /proc/pressure.
	if _, err := procmon.ReadIOPressure(t.TempDir()); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("no pressure file: err = %v, want ErrNotFound", err)
	}
}

func TestParseMeminfoCached(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "proc", "meminfo"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := procmon.ParseMeminfoCached(data)
	if err != nil {
		t.Fatalf("ParseMeminfoCached: %v", err)
	}
	if want := int64(456789012) * 1024; got != want {
		t.Errorf("ParseMeminfoCached = %d, want %d", got, want)
	}
	// SwapCached is a different figure and must not be taken for Cached.
	if _, err := procmon.ParseMeminfoCached([]byte("MemTotal: 10 kB\nSwapCached:  12345 kB\n")); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("only SwapCached: err = %v, want ErrNotFound", err)
	}
	if _, err := procmon.ParseMeminfoCached([]byte("Cached: lots\n")); err == nil {
		t.Error("unparseable Cached: want an error")
	}

	fromTree, err := procmon.ReadPageCache(busyRoot)
	if err != nil || fromTree != got {
		t.Errorf("ReadPageCache = %d, %v; want %d, nil", fromTree, err, got)
	}
	if _, err := procmon.ReadPageCache(t.TempDir()); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("no meminfo: err = %v, want ErrNotFound", err)
	}
}
