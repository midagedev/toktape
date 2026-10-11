package recorder

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/placement/gguf"
)

// modelFromArgv is the value of -m / --model in argv, "" when there is none.
// Both spellings, with the value as the next token or after "=".
func modelFromArgv(argv []string) string {
	for i, a := range argv {
		name, val, inline := strings.Cut(a, "=")
		if name != "-m" && name != "--model" {
			continue
		}
		if inline {
			return val
		}
		if i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// resolveModelFromArgv is the one owner of "the server's argv names the model
// file" for an engine that named itself (v0.7.4). Such an engine's /props
// gives the file name alone (bloomery) or its own shape, never a path this
// host can open, so the size and the header — and with them the quant — were
// "?" on a model that sat on the same disk. The argv's -m is read here, a
// relative path is resolved against the server process's working directory
// (the same procfs root as every other process read), and the file is then
// treated as a llama-server record treats /props model_path.
//
// It runs when collectProcess returns, whatever the outcome, because the
// pid and the argv are only known then. What the engine reported is the
// record: a model with bytes or a non-GGUF format is left untouched, and the
// header only fills what the engine left empty. A step that fails leaves the
// fields as they were and says which step it was.
func (r *run) resolveModelFromArgv() {
	if !r.kind.SelfDeclared() || r.model.Format != "" || r.model.FileBytes != 0 {
		return
	}
	argv := r.args
	if len(argv) == 0 {
		argv = r.flags.Other
	}
	arg := modelFromArgv(argv)
	if arg == "" || !strings.EqualFold(filepath.Ext(arg), ".gguf") {
		return
	}
	name := filepath.Base(arg)
	if r.pid == 0 {
		r.warn("model file %s not located: the server process is not on this host, size and quant unknown", name)
		return
	}
	path := arg
	if !filepath.IsAbs(path) {
		cwd, err := os.Readlink(filepath.Join(procRoot(r.opts.FSRoot), strconv.Itoa(r.pid), "cwd"))
		if err != nil || !filepath.IsAbs(cwd) {
			r.warn("model file %s not located: working directory of the server process unreadable", name)
			return
		}
		path = filepath.Join(cwd, path)
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		r.warn("model file %s not found where the server process points, size and quant unknown", name)
		return
	}
	r.model.Path, r.model.FileBytes = path, fi.Size()
	if r.model.FileName == "" {
		r.model.FileName = name
	}
	r.fillShardSet(path, true)
	if repo := hfCacheRepo(path); repo != "" {
		r.model.Repo, r.model.RepoSource = repo, "hf-cache"
	}
	mi, _, err := gguf.ModelInfo(path)
	if err != nil {
		r.warn("model file %s found but its GGUF header is unreadable, quant unknown", name)
		return
	}
	// The placement is the engine's own (enginePlacement), so the tensors are
	// not kept; only the header's identity fields fill what the engine left.
	m := &r.model
	fill := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	fill(&m.Name, mi.Name)
	fill(&m.Arch, mi.Arch)
	fill(&m.Quant, mi.Quant)
	fill(&m.BaseName, mi.BaseName)
	fill(&m.SizeLabel, mi.SizeLabel)
	fill(&m.FineTune, mi.FineTune)
	fill(&m.NameSource, mi.NameSource)
	fill(&m.QuantizedBy, mi.QuantizedBy)
	fill(&m.RepoURL, mi.RepoURL)
	if m.Params == 0 {
		m.Params = mi.Params
	}
	if m.NLayers == 0 {
		m.NLayers = mi.NLayers
	}
	if m.NExperts == 0 {
		m.NExperts, m.NExpertsUsed = mi.NExperts, mi.NExpertsUsed
	}
	if m.CtxTrain == 0 {
		m.CtxTrain = mi.CtxTrain
	}
	if m.ActiveBytesPerToken == 0 {
		m.ActiveBytesPerToken = mi.ActiveBytesPerToken
	}
}

// procRoot is the procfs directory under fsRoot ("" is the live root).
func procRoot(fsRoot string) string {
	if fsRoot == "" {
		fsRoot = "/"
	}
	return filepath.Join(fsRoot, "proc")
}
