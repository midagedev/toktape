package placement

import (
	"path/filepath"
	"regexp"
	"strings"
)

// quantTokenRe matches one quantisation token as it appears in a GGUF file
// name: Q4_K_M, IQ4_NL, Q8_0, TQ2_0, MXFP4, F16, BF16, F32.
var quantTokenRe = regexp.MustCompile(`^(?i)(I?Q\d+(_[0-9A-Z]+)*|TQ\d+_\d+|MXFP\d+|BF16|F16|F32)$`)

// quantPrefixes are tokens that qualify the quant immediately after them and
// belong in the printed name. Unsloth's dynamic quants ship as
// "...-UD-Q4_K_M.gguf", and "Q4_K_M" alone would lose the distinction the
// card exists to settle.
var quantPrefixes = map[string]bool{"UD": true}

// shardSuffixRe matches the "-00001-of-00009" tail of a split GGUF.
var shardSuffixRe = regexp.MustCompile(`-\d{5}-of-\d{5}$`)

// QuantFromFileName extracts the exact quantisation sub-type from a GGUF file
// name, uppercased: "Qwen3.5-35B-A3B-UD-Q4_K_M.gguf" -> "UD-Q4_K_M".
//
// The file name is preferred over the header's general.file_type enum because
// the enum cannot express the richer tags the community argues about: an
// Unsloth UD-Q4_K_M and a plain Q4_K_M both store file_type 15.
//
// Returns "" when the name carries no quant token — never a guess.
func QuantFromFileName(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	base = strings.TrimSuffix(base, ".gguf")
	base = shardSuffixRe.ReplaceAllString(base, "")
	if base == "" {
		return ""
	}

	// Both "-" and "." separate fields in the wild ("Model-Q4_K_M.gguf" and
	// "Model.Q4_K_M.gguf"), so split on either.
	tokens := strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '.' })

	// Scan from the right: the quant tag sits at the end of the name, and an
	// earlier field could otherwise shadow it.
	for i := len(tokens) - 1; i >= 0; i-- {
		if !quantTokenRe.MatchString(tokens[i]) {
			continue
		}
		q := strings.ToUpper(tokens[i])
		if i > 0 && quantPrefixes[strings.ToUpper(tokens[i-1])] {
			q = strings.ToUpper(tokens[i-1]) + "-" + q
		}
		return q
	}
	return ""
}

// fileTypeQuant maps general.file_type (the LLAMA_FTYPE enum) to the quant
// name llama.cpp prints. Transcribed from the enum in llama.h; the values are
// a wire format and never renumbered.
//
// Written out rather than taken from gguf-parser-go's stringer so that the
// exact strings the card prints are pinned here and covered by a test.
var fileTypeQuant = map[uint32]string{
	0:  "F32",
	1:  "F16",
	2:  "Q4_0",
	3:  "Q4_1",
	4:  "Q4_1_SOME_F16",
	5:  "Q4_2",
	6:  "Q4_3",
	7:  "Q8_0",
	8:  "Q5_0",
	9:  "Q5_1",
	10: "Q2_K",
	11: "Q3_K_S",
	12: "Q3_K_M",
	13: "Q3_K_L",
	14: "Q4_K_S",
	15: "Q4_K_M",
	16: "Q5_K_S",
	17: "Q5_K_M",
	18: "Q6_K",
	19: "IQ2_XXS",
	20: "IQ2_XS",
	21: "Q2_K_S",
	22: "IQ3_XS",
	23: "IQ3_XXS",
	24: "IQ1_S",
	25: "IQ4_NL",
	26: "IQ3_S",
	27: "IQ3_M",
	28: "IQ2_S",
	29: "IQ2_M",
	30: "IQ4_XS",
	31: "IQ1_M",
	32: "BF16",
	33: "Q4_0_4_4",
	34: "Q4_0_4_8",
	35: "Q4_0_8_8",
	36: "TQ1_0",
	37: "TQ2_0",
	38: "MXFP4",
}

// QuantFromFileType maps a GGUF general.file_type enum value to its quant
// name. Returns "" for a value this build does not know, so the card prints
// "?" rather than a wrong sub-type.
func QuantFromFileType(ft uint32) string {
	return fileTypeQuant[ft]
}
