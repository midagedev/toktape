package tape

import "testing"

// TestUnifiedNamesTheHostsWithOnePool (TTP-178, 2026-10-11). Whether a host is
// unified is a pure function of fields a tape already carries, so a tape
// recorded before this existed reads the same as one recorded after.
func TestUnifiedNamesTheHostsWithOnePool(t *testing.T) {
	gpu := func(name string) HostInfo { return HostInfo{GPUs: []GPUInfo{{Name: name}}} }
	cpu := func(name string) HostInfo { return HostInfo{CPU: name} }
	for name, tc := range map[string]struct {
		h    HostInfo
		want string
	}{
		"spark, nvidia-smi name":     {gpu("NVIDIA GB10"), UnifiedName},
		"spark, bare":                {gpu("GB10"), UnifiedName},
		"spark, second device":       {HostInfo{GPUs: []GPUInfo{{Name: "NVIDIA RTX A6000"}, {Name: "NVIDIA GB10"}}}, UnifiedName},
		"apple m1 pro":               {cpu("Apple M1 Pro"), UnifiedName},
		"apple m4 max":               {cpu("Apple M4 Max"), UnifiedName},
		"rtx 3090":                   {gpu("NVIDIA GeForce RTX 3090"), ""},
		"a6000":                      {gpu("NVIDIA RTX A6000"), ""},
		"ryzen":                      {cpu("AMD Ryzen 9 7950X"), ""},
		"xeon":                       {cpu("Intel(R) Xeon(R)"), ""},
		"a model that contains gb10": {gpu("NVIDIA GB100"), ""},
		"empty":                      {HostInfo{}, ""},
	} {
		if got := Unified(tc.h); got != tc.want {
			t.Errorf("%s: Unified = %q, want %q", name, got, tc.want)
		}
	}
}
