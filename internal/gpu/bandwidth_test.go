package gpu

import "testing"

func TestBandwidth(t *testing.T) {
	tests := []struct {
		name string
		want int64
	}{
		// The names below are what nvidia-smi --query-gpu=name actually prints.
		{"NVIDIA GeForce RTX 4090", 1_008_000_000_000},
		{"NVIDIA GeForce RTX 3090", 936_200_000_000},
		{"NVIDIA GeForce RTX 3060 Ti", 448_000_000_000},
		{"NVIDIA GeForce RTX 3060", 360_000_000_000},
		{"NVIDIA GeForce RTX 4070 Ti SUPER", 672_300_000_000},
		{"NVIDIA GeForce RTX 4070 Ti", 504_200_000_000},
		{"NVIDIA GeForce RTX 5080", 960_000_000_000},
		{"NVIDIA GeForce RTX 5090", 1_792_000_000_000},
		{"Tesla P40", 346_000_000_000},
		{"Tesla T4", 320_000_000_000},
		{"Tesla V100-SXM2-16GB", 900_000_000_000},
		{"NVIDIA L40S", 864_000_000_000},
		{"NVIDIA RTX A6000", 768_000_000_000},
		{"NVIDIA A100-SXM4-40GB", 1_555_000_000_000},
		{"NVIDIA A100-SXM4-80GB", 2_039_000_000_000},
		{"NVIDIA A100-PCIE-80GB", 1_935_000_000_000},
		{"NVIDIA H100 80GB HBM3", 3_350_000_000_000},
		{"NVIDIA H100 PCIe", 2_000_000_000_000},
		{"NVIDIA RTX 6000 Ada Generation", 960_000_000_000},
		{"AMD Radeon RX 7900 XTX", 960_000_000_000},
		{"AMD Instinct MI300X", 5_300_000_000_000},

		// Fuzzy: punctuation, case and brand words are ignored.
		{"rtx4090", 1_008_000_000_000},
		{"4090", 1_008_000_000_000},
		{"  NVIDIA   GeForce   RTX   4090  ", 1_008_000_000_000},

		// Unknown is 0 and prints as "?", never a guessed default.
		{"NVIDIA GeForce RTX 9090", 0},
		{"Intel Arc A770", 0},
		{"", 0},
		{"   ", 0},
		// A laptop part shares the die name but not the memory bus, so the
		// desktop figure must not be substituted.
		{"NVIDIA GeForce RTX 4090 Laptop GPU", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Bandwidth(tc.name); got != tc.want {
				t.Errorf("Bandwidth(%q) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestNormalizeName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"NVIDIA GeForce RTX 4090", "4090"},
		{"NVIDIA GeForce RTX 3060 Ti", "3060ti"},
		{"Tesla P40", "p40"},
		{"NVIDIA A100-SXM4-80GB", "a100sxm480gb"},
		{"AMD Radeon RX 7900 XTX", "7900xtx"},
		{"NVIDIA", ""},
	}
	for _, tc := range tests {
		if got := normalizeName(tc.in); got != tc.want {
			t.Errorf("normalizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
