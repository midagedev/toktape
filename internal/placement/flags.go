package placement

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// NGLAll is the ParseNGL result for "-ngl auto", "-ngl all" and "-ngl -1":
// every offloadable slot goes to the GPUs.
const NGLAll = -1

// ParseNGL reads tape.ServerFlags.NGL.
//
// It returns the number of offloadable slots llama.cpp was told to place on
// the GPUs, or NGLAll for the "everything" spellings, and whether the flag was
// observed at all. An unparsable or empty value returns ok == false: the
// placement is then not derivable and must not be guessed.
//
// Accepted grammar (llama.cpp common/arg.cpp, option -ngl/--gpu-layers):
//
//	""            -> not observed
//	"auto" | "-1" -> NGLAll  (llama.cpp's own default)
//	"all"         -> NGLAll
//	"0".."N"      -> that many slots
func ParseNGL(s string) (n int, ok bool) {
	v := strings.TrimSpace(s)
	if v == "" {
		return 0, false
	}
	switch strings.ToLower(v) {
	case "auto", "all", "max":
		return NGLAll, true
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	if i < 0 {
		// llama_model::n_gpu_layers(): a negative params.n_gpu_layers means
		// n_layer_all + 1, i.e. everything.
		return NGLAll, true
	}
	return i, true
}

// ParseCPUMoE reads tape.ServerFlags.CPUMoE, which carries either the -cmoe
// switch or the -ncmoe/--n-cpu-moe count.
//
// all is true for -cmoe (every layer's expert weights on the CPU). firstN is
// the N of -ncmoe (the expert weights of layers 0..N-1 on the CPU). ok is
// false when the field was not observed or could not be read.
//
// Accepted spellings: "-cmoe", "--cpu-moe", "cmoe"; "-ncmoe 12",
// "--n-cpu-moe 12", "-ncmoe=12", "ncmoe 12", and a bare "12".
func ParseCPUMoE(s string) (all bool, firstN int, ok bool) {
	v := strings.TrimSpace(s)
	if v == "" {
		return false, 0, false
	}
	v = strings.ReplaceAll(v, "=", " ")
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return false, 0, false
	}

	name := strings.ToLower(strings.TrimLeft(fields[0], "-"))
	switch name {
	case "cmoe", "cpu-moe", "cpu_moe":
		return true, 0, true
	case "ncmoe", "n-cpu-moe", "n_cpu_moe":
		if len(fields) < 2 {
			return false, 0, false
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil || n < 0 {
			return false, 0, false
		}
		return false, n, true
	}

	// A bare count, e.g. CPUMoE == "12".
	if n, err := strconv.Atoi(fields[0]); err == nil && n >= 0 {
		return false, n, true
	}
	return false, 0, false
}

// Override is one resolved -ot rule: a regex over tensor names and the device
// the matching tensors are pinned to.
type Override struct {
	// Pattern is the regex source, verbatim from the command line.
	Pattern string
	// Device is tape.DeviceCPU or "GPU<N>".
	Device string

	re *regexp.Regexp
}

// Matches reports whether the rule applies to a tensor name. llama.cpp uses
// std::regex_search, so the pattern is unanchored (llama-model-loader.cpp,
// the "check overrides" loop).
func (o Override) Matches(name string) bool { return o.re.MatchString(name) }

// backendDeviceRe splits a ggml buffer-type name such as "CUDA0", "Vulkan1"
// or "ROCm0" into its backend and index.
var backendDeviceRe = regexp.MustCompile(`^([A-Za-z_]+)(\d+)$`)

// ParseOverrides resolves the verbatim -ot entries of tape.ServerFlags into
// ordered rules, plus a warning for every entry it had to drop.
//
// One entry may hold several comma-separated rules, each "<regex>=<buffer
// type>", split on the first "=" (llama.cpp common/arg.cpp,
// parse_tensor_buffer_overrides). The buffer type is mapped to a toktape
// device: "CPU" -> tape.DeviceCPU, and any "<Backend><N>" -> "GPU<N>".
//
// Rules keep command-line order: llama.cpp takes the first match and stops.
func ParseOverrides(entries []string) (rules []Override, warnings []string) {
	for _, entry := range entries {
		for _, part := range strings.Split(entry, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			eq := strings.Index(part, "=")
			if eq < 0 {
				warnings = append(warnings, fmt.Sprintf("-ot %q: no '=', rule ignored", part))
				continue
			}
			pattern, bufType := part[:eq], strings.TrimSpace(part[eq+1:])

			device, ok := deviceForBufferType(bufType)
			if !ok {
				warnings = append(warnings, fmt.Sprintf("-ot %q: unknown buffer type %q, rule ignored", part, bufType))
				continue
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				// Go's RE2 rejects the backtracking constructs std::regex
				// accepts (lookahead, backreferences). Dropping the rule
				// keeps the rest of the placement usable.
				warnings = append(warnings, fmt.Sprintf("-ot %q: pattern not supported by RE2 (%v), rule ignored", pattern, err))
				continue
			}
			rules = append(rules, Override{Pattern: pattern, Device: device, re: re})
		}
	}
	return rules, warnings
}

// deviceForBufferType maps a ggml buffer-type name to a toktape device name.
//
// Every host buffer type resolves to the CPU, not just the plain "CPU": ggml
// also exposes "CPU_REPACK", "CPU_AARCH64" and the pinned-host types
// "CUDA_Host" / "ROCm_Host". Dropping one of those would push the tensors the
// user deliberately kept in RAM back onto a GPU, which is the wrong direction
// to be wrong in.
func deviceForBufferType(bufType string) (string, bool) {
	b := strings.TrimSpace(bufType)
	if b == "" {
		return "", false
	}
	upper := strings.ToUpper(b)
	if upper == "CPU" || strings.HasPrefix(upper, "CPU_") || strings.HasSuffix(upper, "_HOST") {
		return tape.DeviceCPU, true
	}
	m := backendDeviceRe.FindStringSubmatch(b)
	if m == nil {
		return "", false
	}
	idx, err := strconv.Atoi(m[2])
	if err != nil {
		return "", false
	}
	return gpuDevice(idx), true
}

// gpuDevice is the tape's name for GPU index i.
func gpuDevice(i int) string { return "GPU" + strconv.Itoa(i) }

// cpuMoEOverrides expands -cmoe / -ncmoe into the synthetic -ot rules
// llama.cpp itself appends for them.
//
// Transcribed from common/common.h:
//
//	LLM_FFN_EXPS_REGEX = "\\.ffn_(up|down|gate|gate_up)_(ch|)exps"
//	-cmoe    -> one rule with that pattern            -> CPU
//	-ncmoe N -> "blk\\.<i>" + that pattern, i in 0..N-1 -> CPU
//
// Note what the pattern does NOT match: the MoE router (ffn_gate_inp) and the
// shared expert (*_shexp) stay wherever -ngl put them. The toktape design
// note says -cmoe moves "all expert-class tensors"; upstream moves only the
// stacked per-expert weights, and upstream is what the server did.
func cpuMoEOverrides(all bool, firstN int) []Override {
	const expsPattern = `\.ffn_(up|down|gate|gate_up)_(ch|)exps`

	if all {
		return []Override{{
			Pattern: expsPattern,
			Device:  tape.DeviceCPU,
			re:      regexp.MustCompile(expsPattern),
		}}
	}
	rules := make([]Override, 0, firstN)
	for i := 0; i < firstN; i++ {
		p := `blk\.` + strconv.Itoa(i) + expsPattern
		rules = append(rules, Override{
			Pattern: p,
			Device:  tape.DeviceCPU,
			re:      regexp.MustCompile(p),
		})
	}
	return rules
}
