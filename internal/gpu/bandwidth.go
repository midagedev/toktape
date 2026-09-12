package gpu

import "strings"

// Peak memory bandwidth by device, in bytes per second (decimal GB/s x 1e9,
// the unit every vendor spec sheet uses). Each row carries its GB/s figure
// and source so the number can be checked without running the card.
//
// Keys are normalizeName() output: lower case, brand words removed, all
// non-alphanumeric characters dropped. "NVIDIA GeForce RTX 4090" -> "4090".
var bandwidthExact = map[string]int64{
	// GeForce RTX 30 series (NVIDIA product spec sheets).
	"3060":   360_000_000_000,   // 360 GB/s, 192-bit GDDR6 @15 Gbps
	"3060ti": 448_000_000_000,   // 448 GB/s
	"3070":   448_000_000_000,   // 448 GB/s
	"3070ti": 608_000_000_000,   // 608 GB/s, GDDR6X
	"3080":   760_300_000_000,   // 760.3 GB/s, 10 GB 320-bit GDDR6X
	"3080ti": 912_400_000_000,   // 912.4 GB/s
	"3090":   936_200_000_000,   // 936.2 GB/s
	"3090ti": 1_008_000_000_000, // 1008 GB/s

	// GeForce RTX 40 series.
	"4060":        272_000_000_000,   // 272 GB/s
	"4060ti":      288_000_000_000,   // 288 GB/s
	"4070":        504_200_000_000,   // 504.2 GB/s
	"4070super":   504_200_000_000,   // 504.2 GB/s
	"4070ti":      504_200_000_000,   // 504.2 GB/s
	"4070tisuper": 672_300_000_000,   // 672.3 GB/s, 256-bit
	"4080":        716_800_000_000,   // 716.8 GB/s
	"4080super":   736_300_000_000,   // 736.3 GB/s
	"4090":        1_008_000_000_000, // 1008 GB/s, 384-bit GDDR6X @21 Gbps

	// GeForce RTX 50 series (GDDR7).
	"5060ti": 448_000_000_000,   // 448 GB/s
	"5070":   672_000_000_000,   // 672 GB/s
	"5070ti": 896_000_000_000,   // 896 GB/s
	"5080":   960_000_000_000,   // 960 GB/s, 256-bit GDDR7 @30 Gbps
	"5090":   1_792_000_000_000, // 1792 GB/s, 512-bit GDDR7 @28 Gbps

	// Professional / datacenter cards with a stable one-token name.
	"t4":    320_000_000_000, // 320 GB/s, Tesla T4
	"l4":    300_000_000_000, // 300 GB/s
	"l40":   864_000_000_000, // 864 GB/s
	"l40s":  864_000_000_000, // 864 GB/s
	"a40":   696_000_000_000, // 696 GB/s
	"a4000": 448_000_000_000, // 448 GB/s, RTX A4000
	"a5000": 768_000_000_000, // 768 GB/s, RTX A5000
	"a6000": 768_000_000_000, // 768 GB/s, RTX A6000
	"p40":   346_000_000_000, // 346 GB/s, Tesla P40

	// AMD.
	"7900xt":  800_000_000_000,   // 800 GB/s, Radeon RX 7900 XT
	"7900xtx": 960_000_000_000,   // 960 GB/s, Radeon RX 7900 XTX
	"mi300x":  5_300_000_000_000, // 5.3 TB/s, Instinct MI300X
	"mi250x":  3_277_000_000_000, // 3.2 TB/s, Instinct MI250X
}

// bandwidthRules match device families whose reported name carries a
// variable suffix ("A100-SXM4-80GB", "H100 80GB HBM3", "RTX 6000 Ada
// Generation"). The first rule whose substrings are all present wins, so
// the more specific variant must come first.
var bandwidthRules = []struct {
	contains []string
	bps      int64
}{
	{[]string{"h200"}, 4_800_000_000_000},                 // 4.8 TB/s, HBM3e
	{[]string{"h100", "pcie"}, 2_000_000_000_000},         // 2.0 TB/s, H100 PCIe (HBM2e)
	{[]string{"h100"}, 3_350_000_000_000},                 // 3.35 TB/s, H100 SXM (HBM3)
	{[]string{"a100", "pcie", "80gb"}, 1_935_000_000_000}, // 1935 GB/s, A100 PCIe 80 GB
	{[]string{"a100", "80gb"}, 2_039_000_000_000},         // 2039 GB/s, A100 SXM4 80 GB
	{[]string{"a100"}, 1_555_000_000_000},                 // 1555 GB/s, A100 40 GB
	{[]string{"v100"}, 900_000_000_000},                   // 900 GB/s, Tesla V100
	{[]string{"p100"}, 732_000_000_000},                   // 732 GB/s, Tesla P100 16 GB
	{[]string{"p40"}, 346_000_000_000},                    // 346 GB/s
	{[]string{"6000ada"}, 960_000_000_000},                // 960 GB/s, RTX 6000 Ada
	{[]string{"5000ada"}, 576_000_000_000},                // 576 GB/s, RTX 5000 Ada
	{[]string{"4500ada"}, 432_000_000_000},                // 432 GB/s, RTX 4500 Ada
	{[]string{"titanrtx"}, 672_000_000_000},               // 672 GB/s
}

// brandTokens are dropped from a device name before lookup: they identify
// the vendor or the product line, never the part.
var brandTokens = map[string]bool{
	"nvidia":   true,
	"geforce":  true,
	"tesla":    true,
	"rtx":      true,
	"gtx":      true,
	"quadro":   true,
	"amd":      true,
	"radeon":   true,
	"instinct": true,
	"rx":       true,
}

// Bandwidth returns the device's peak memory bandwidth in bytes per second,
// or 0 when the name is not in the table. Matching is fuzzy: case, vendor
// and product-line words, and punctuation are ignored, so "NVIDIA GeForce
// RTX 4090", "rtx4090" and "4090" all resolve.
//
// 0 means unknown and must print as "?", never as a guessed default.
func Bandwidth(name string) int64 {
	key := normalizeName(name)
	if key == "" {
		return 0
	}
	if bps, ok := bandwidthExact[key]; ok {
		return bps
	}
	// Laptop and mobile parts share a die name with a much slower memory
	// bus, so a substring match on them would report the desktop figure.
	if strings.Contains(key, "laptop") || strings.Contains(key, "mobile") {
		return 0
	}
	for _, rule := range bandwidthRules {
		all := true
		for _, sub := range rule.contains {
			if !strings.Contains(key, sub) {
				all = false
				break
			}
		}
		if all {
			return rule.bps
		}
	}
	return 0
}

// normalizeName reduces a reported GPU name to its lookup key.
func normalizeName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	var out strings.Builder
	for _, tok := range strings.Fields(b.String()) {
		// Split letter and digit runs so a name written without spaces
		// ("rtx4090") drops its brand word just like "RTX 4090" does.
		for _, run := range splitAlphaDigit(tok) {
			if brandTokens[run] {
				continue
			}
			out.WriteString(run)
		}
	}
	return out.String()
}

// splitAlphaDigit breaks "rtx4090" into "rtx", "4090".
func splitAlphaDigit(s string) []string {
	var out []string
	start := 0
	digit := func(i int) bool { return s[i] >= '0' && s[i] <= '9' }
	for i := 1; i < len(s); i++ {
		if digit(i) != digit(i-1) {
			out = append(out, s[start:i])
			start = i
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
