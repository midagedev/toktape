package publish

import (
	"fmt"
	"strings"

	"github.com/midagedev/toktape/internal/placement"
	"github.com/midagedev/toktape/internal/placement/gguf"
	"github.com/midagedev/toktape/internal/tape"
)

// The normalisers behind the index's *_id fields. One rule governs all of
// them, the schema's own law (CLAUDE.md): unknown is "" and never a default
// nobody observed. Each function returns the zero value for an input it does
// not recognise — a plausible-looking value for an input the normaliser did
// not actually understand is the one failure that matters, because it is
// invisible: it produces a search filter that silently groups unrelated runs
// (docs/toktape-spec.ko.md §9.4). Search falls back to the raw string that
// always travels beside the normalised value.

// quantBits is the weight bits per quantisation type, keyed by the lowercase
// id QuantID returns. Two kinds of entry, each with its source:
//
//   - The single-block formats are arithmetic on the block layouts asserted
//     in llama.cpp's ggml/src/ggml-common.h (read 2026-09-18):
//     bits = sizeof(block) * 8 / weights-per-block, with the asserted sizes
//     q4_0 18 B/32, q4_1 20 B/32, q5_0 22 B/32, q5_1 24 B/32, q8_0 34 B/32,
//     iq4_nl 18 B/32, mxfp4 17 B/32, q2_k 84 B/256, q3_k 110 B/256,
//     q4_k 144 B/256, q5_k 176 B/256, q6_k 210 B/256, iq1_s 50 B/256,
//     iq1_m 56 B/256, iq2_xxs 66 B/256, iq2_xs 74 B/256, iq2_s 82 B/256,
//     iq3_xxs 98 B/256, iq3_s 110 B/256, iq4_xs 136 B/256.
//     The llama.cpp wiki's tensor-encoding table agrees with every derived
//     value it lists (q3_k 3.4375, q4_k 4.5, q5_k 5.5, q6_k 6.5625,
//     iq1_m 1.75, iq2_xxs 2.0625, iq3_xxs 3.0625, iq4_xs 4.25, iq4_nl 4.5);
//     its legacy entries are the payload bits alone (q8_0 "8", q4_0 "4"),
//     which is why the derived figures — scale bytes included, 8.5 and 4.5 —
//     are used here instead. F16/BF16/F32 are their IEEE widths.
//   - The k-quant S/M/L ids, iq2_m and iq3_m are MIXES: llama.cpp assigns a
//     finer type to attention and output tensors, so the effective bits
//     depend on the model's tensor split and no single number is sourceable.
//     They stay recognised — the id is still a filter value — with honest
//     0 bits, which print as ? like any unknown.
//
// Deliberately absent: the general.file_type enum's exotic values
// (Q4_1_SOME_F16, Q4_2, Q4_3, the Q4_0_4_4/4_8/8_8 layout variants, TQ1_0,
// TQ2_0). They are outside the standard vocabulary, no block layout for them
// is sourced here, and an id nobody can check is a guess wearing a name — so
// they do not normalise and search keeps the raw string.
var quantBits = map[string]float64{
	// Legacy block formats, 32 weights per block.
	"q4_0": 4.5,
	"q4_1": 5,
	"q5_0": 5.5,
	"q5_1": 6,
	"q8_0": 8.5,

	// K-quants, 256-weight super-blocks.
	"q2_k": 2.625,
	"q3_k": 3.4375,
	"q4_k": 4.5,
	"q5_k": 5.5,
	"q6_k": 6.5625,

	// i-quants, 256-weight super-blocks.
	"iq1_s":   1.5625,
	"iq1_m":   1.75,
	"iq2_xxs": 2.0625,
	"iq2_xs":  2.3125,
	"iq2_s":   2.5625,
	"iq3_xxs": 3.0625,
	"iq3_s":   3.4375,
	"iq4_xs":  4.25,
	"iq4_nl":  4.5, // 32-weight block

	// MXFP4: E8M0 scale byte + 16 packed nibbles per 32 weights.
	"mxfp4": 4.25,

	// Float formats.
	"f16":  16,
	"bf16": 16,
	"f32":  32,

	// Recognised mixes; 0 bits = not sourceable, see the comment above.
	"q2_k_s": 0,
	"q3_k_s": 0,
	"q3_k_m": 0,
	"q3_k_l": 0,
	"q4_k_s": 0,
	"q4_k_m": 0,
	"q5_k_s": 0,
	"q5_k_m": 0,
	"iq2_m":  0,
	"iq3_m":  0,
}

// QuantID normalises a quantisation label to llama.cpp's lowercase id plus
// the weight bits, or ("", 0) for an input outside the vocabulary. The
// recognition itself is placement.QuantFromFileName — the repo's existing
// owner of that rule, so the token shape, the case-insensitivity and the
// shard/extension tolerance cannot drift between the recorder and the index.
//
// Unsloth's dynamic prefix is dropped from the id and never makes the input
// unrecognised: UD-Q6_K and Q6_K are the same quantisation to a search, and
// quant_raw keeps the distinction.
func QuantID(raw string) (id string, bits float64) {
	q := placement.QuantFromFileName(raw)
	if q == "" {
		return "", 0
	}
	id = strings.ToLower(strings.TrimPrefix(q, "UD-"))
	bits, ok := quantBits[id]
	if !ok {
		// A token with a quant's shape but not its name ("Q4"): not in the
		// vocabulary, so not normalised.
		return "", 0
	}
	return id, bits
}

// vendorNoise are the tokens a GPU name carries that say nothing about the
// card. Exactly the three the contract names — adding more (AMD, Radeon,
// Intel) would be a taste call, and this file makes none.
var vendorNoise = map[string]bool{
	"nvidia":      true,
	"geforce":     true,
	"corporation": true,
}

// ModelID assembles the model's search id from the GGUF naming convention
// the tape recorded (ggml docs/gguf.md, TTP-119): base name, size label and
// fine-tune, in the convention's own order, lower-cased and hyphenated.
//
// The only rule that is ours is the skip, and it exists because quantizers
// disagree about how much of the convention they fold into general.basename.
// Measured 2026-09-18 on one model: unsloth's basename is "Qwen3-0.6B" and
// bartowski's is "Qwen3", with both writing size label "0.6B". A part the
// basename already carries is therefore not appended again, and both land on
// "qwen3-0.6b" — the same id for the same model from two publishers, which is
// the whole point of having one.
//
// It is a slug, not a name: a model whose fields are empty — every non-GGUF
// engine, and every file whose name is outside the convention — gets "" and
// search falls back to the raw file name beside it.
//
// The source comes back with the id because the two are not equally strong:
// "gguf" was read out of the header and "filename" out of a name, and a name
// carries no fine-tune. A tape that recorded no convention parts at all is
// read one more time here, through the same parser the recorder uses (lead,
// 2026-09-19): every run published before TTP-119 landed carries a file name
// and nothing else, so without this the model dropdown is empty on exactly
// the runs the site already has. Reading a name that is in the record is
// observing, not guessing — the parser refuses a name outside the convention
// — and it is what makes `toktape reindex` able to repair the model axis of
// a run recorded by an older binary.
func ModelID(m tape.ModelInfo) (id, source string) {
	base, size, fine, src := slug(m.BaseName), slug(m.SizeLabel), slug(m.FineTune), m.NameSource
	if base == "" && m.FileName != "" {
		b, s := gguf.NameFromFileName(m.FileName)
		base, size, fine, src = slug(b), slug(s), "", "filename"
	}
	if base == "" {
		return "", ""
	}
	for _, part := range []string{size, fine} {
		if part != "" && !hasComponent(base, part) {
			base += "-" + part
		}
	}
	return base, src
}

// hasComponent reports whether want appears in s as a whole run of
// hyphen-separated components, so "qwen3-0.6b" carries "0.6b" but
// "qwen3-10.6b" does not.
func hasComponent(s, want string) bool {
	return s == want ||
		strings.HasPrefix(s, want+"-") ||
		strings.HasSuffix(s, "-"+want) ||
		strings.Contains(s, "-"+want+"-")
}

// slug lower-cases a naming-convention part and reduces it to [a-z0-9.],
// with every other run becoming a single "-". The dot survives because it is
// a version's own punctuation ("Meta Llama 3.1", "0.6B"), and the parser
// hands base names back with their hyphens as spaces, which is what puts
// "gpt oss" back together as "gpt-oss".
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	return b.String()
}

// GPUID normalises a GPU name to its model id: vendor noise dropped,
// lower-cased, whitespace collapsed to "-". "NVIDIA GeForce RTX 4090"
// becomes "rtx-4090", "NVIDIA H100 80GB HBM3" becomes "h100-80gb-hbm3".
// A name that is empty or nothing but vendor noise is not a card model, and
// returns "".
func GPUID(name string) string {
	var keep []string
	for _, f := range strings.Fields(strings.ToLower(name)) {
		if vendorNoise[f] {
			continue
		}
		keep = append(keep, f)
	}
	if len(keep) == 0 {
		return ""
	}
	return strings.Join(keep, "-")
}

// HostClass is the row's host label, "N x <gpu id>", when every GPU
// normalised to the same non-empty id. A mixed rig, an empty id in the list
// or no GPUs at all return "": a host with two kinds of card has no single
// class, and giving it one would group machines that are not the same.
func HostClass(gpuIDs []string) string {
	if len(gpuIDs) == 0 {
		return ""
	}
	id := gpuIDs[0]
	if id == "" {
		return ""
	}
	for _, g := range gpuIDs[1:] {
		if g != id {
			return ""
		}
	}
	return fmt.Sprintf("%dx %s", len(gpuIDs), id)
}
