package server

import (
	"path/filepath"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// ParseFlags reads a llama-server / ik_llama.cpp argv into the flags the card
// always prints. A leading program path is skipped.
//
// The five argument-starters (-ngl, -fa, -b/-ub, -ctk/-ctv, -ot) get their own
// fields because the card may not omit them. Everything else that was actually
// passed, including -c/--ctx-size and flags this parser does not know, is kept
// verbatim in Other so the card can print what was really on the command line.
// A flag that was not passed stays "" and prints as "?": never a default that
// was not observed.
func ParseFlags(argv []string) tape.ServerFlags {
	var f tape.ServerFlags
	args := argv
	// Skip the program path; the first token that starts with '-' begins the
	// flags.
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		args = args[1:]
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			f.Other = append(f.Other, arg)
			continue
		}
		name, inlineVal, hasInline := strings.Cut(arg, "=")
		// value returns the flag's argument, consuming the next token when the
		// value was not given inline.
		value := func() (string, bool) {
			if hasInline {
				return inlineVal, true
			}
			if i+1 < len(args) && looksLikeValue(args[i+1]) {
				i++
				return args[i], true
			}
			return "", false
		}

		switch name {
		case "-ngl", "--n-gpu-layers", "--gpu-layers":
			if v, ok := value(); ok {
				f.NGL = v
			}
		case "-fa", "--flash-attn":
			// Modern builds take on|off|auto; older ones are a bare boolean.
			if hasInline {
				f.FlashAttn = normalizeFA(inlineVal)
			} else if i+1 < len(args) && isFAValue(args[i+1]) {
				i++
				f.FlashAttn = normalizeFA(args[i])
			} else {
				f.FlashAttn = "on"
			}
		case "--no-flash-attn":
			f.FlashAttn = "off"
		case "-b", "--batch-size":
			if v, ok := value(); ok {
				f.Batch = v
			}
		case "-ub", "--ubatch-size":
			if v, ok := value(); ok {
				f.UBatch = v
			}
		case "-ctk", "--cache-type-k":
			if v, ok := value(); ok {
				f.CacheTypeK = v
			}
		case "-ctv", "--cache-type-v":
			if v, ok := value(); ok {
				f.CacheTypeV = v
			}
		case "--load-mode":
			if v, ok := value(); ok {
				f.LoadMode = v
			}
		case "--mmap":
			f.LoadMode = "mmap"
		case "--no-mmap":
			f.LoadMode = "direct"
		case "-ot", "--override-tensor":
			if v, ok := value(); ok {
				f.OverrideTens = append(f.OverrideTens, SplitOverrideTensor(v)...)
			}
		case "-cmoe", "--cpu-moe":
			f.CPUMoE = "all"
		case "-ncmoe", "--n-cpu-moe":
			if v, ok := value(); ok {
				f.CPUMoE = v
			}
		case "-t", "--threads":
			if v, ok := value(); ok {
				f.Threads = v
			}
		// Speculative decoding (TTP-30). The draft model gets its own field
		// because the card has to name which draft produced the acceptance
		// rate beside it, and the base name is what identifies it — the path
		// is the recording machine's, not the reader's. The block size and the
		// two thresholds are kept verbatim.
		//
		// The draft's own placement flags (-ngld, -devd, -ctkd, -ctvd) are
		// deliberately not named here: they stay in Other and print verbatim,
		// the way every flag this parser does not model does.
		case "-md", "--model-draft":
			if v, ok := value(); ok {
				f.DraftModel = filepath.Base(v)
			}
		// --spec-draft-n-max is the DSpark fork's spelling of the same
		// block size (TTP-58, 2026-09-14). The ws rig runs
		// "--spec-type draft-dspark --spec-draft-n-max 3" and the Draft row
		// printed "n_max ?" beside a 44 % acceptance rate: the figure the
		// rate has to be read against was on the command line and the parser
		// did not know the name. --spec-type stays in Other and prints on the
		// FLAGS line verbatim; it names which drafting scheme ran, which no
		// field on the schema holds, and the schema is not this parser's to
		// extend.
		case "--draft-max", "--draft", "--draft-n", "--spec-draft-n-max":
			if v, ok := value(); ok {
				f.DraftMax = v
			}
		case "--draft-min", "--draft-n-min":
			if v, ok := value(); ok {
				f.DraftMin = v
			}
		case "--draft-p-min":
			if v, ok := value(); ok {
				f.DraftPMin = v
			}
		default:
			// Everything else, -c/--ctx-size included, is printed verbatim.
			if v, ok := value(); ok {
				f.Other = append(f.Other, name+" "+v)
			} else {
				f.Other = append(f.Other, name)
			}
		}
	}
	return f
}

// SplitOverrideTensor splits one -ot argument into its individual
// pattern=device entries.
//
// llama.cpp accepts a comma-separated list, but the patterns are regular
// expressions that may themselves contain commas (a `{1,2}` repetition, for
// example). Splitting on every comma would cut those in half, so a segment is
// only ended by a comma once it contains the `=` that separates pattern from
// device; a segment without one is glued back to the segment that follows.
func SplitOverrideTensor(s string) []string {
	parts := strings.Split(s, ",")
	var out []string
	var acc string
	for _, p := range parts {
		if acc != "" {
			acc += "," + p
		} else {
			acc = p
		}
		if strings.Contains(acc, "=") {
			out = append(out, acc)
			acc = ""
		}
	}
	if acc != "" {
		out = append(out, acc)
	}
	return out
}

// looksLikeValue reports whether s is a flag's argument rather than the next
// flag. A leading '-' followed by a digit is a negative number (-ngl -1), not
// a flag.
func looksLikeValue(s string) bool {
	if !strings.HasPrefix(s, "-") {
		return true
	}
	return len(s) > 1 && s[1] >= '0' && s[1] <= '9'
}

func isFAValue(s string) bool {
	switch strings.ToLower(s) {
	case "on", "off", "auto", "0", "1", "true", "false", "enabled", "disabled":
		return true
	}
	return false
}

func normalizeFA(s string) string {
	switch strings.ToLower(s) {
	case "1", "true", "on", "enabled":
		return "on"
	case "0", "false", "off", "disabled":
		return "off"
	case "auto":
		return "auto"
	}
	return s
}
