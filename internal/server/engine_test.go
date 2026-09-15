package server

import (
	"encoding/json"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// enginePropsJSON is the engine block contract (2026-09-15, ExLlamaV3) as one
// whole /props body: the llama-server-protocol keys a shim keeps, plus the one
// extra object an engine that is not llama.cpp reports about itself. Every key
// inside it is optional; the figures are illustrative, the shapes are not.
const enginePropsJSON = `{
  "model_path": "/models/GLM-5.3-Flash-exl3-4.05",
  "total_slots": 2,
  "default_generation_settings": {"n_ctx": 32768},
  "engine": {
    "name": "exllamav3", "version": "1.5.0",
    "args": ["-gs", "44,21", "-mcs", "185", "-mct", "32", "-mtp"],
    "model": {
      "format": "exl3", "arch": "Glm5NextForConditionalGeneration",
      "quant": "EXL3 4.05 bpw · head 6.0", "bytes": 165151541665, "files": 30,
      "params": 320000000000, "n_layers": 45, "n_experts": 288, "n_experts_used": 8,
      "ctx_train": 1048576, "active_bytes_per_token": 7000000000
    },
    "placement": {
      "devices": [
        {"device": "GPU0", "bytes": 40000000000, "classes": {"attention": 8000000000, "experts": 30000000000, "embeddings": 2000000000}, "layers": "experts 0-20"},
        {"device": "GPU1", "bytes": 20000000000, "classes": {"experts": 19000000000, "output": 1000000000}, "layers": "experts 21-30"},
        {"device": "CPU",  "bytes": 105151541665, "classes": {"experts": 105151541665}, "layers": "experts 31-44"}
      ],
      "vram_kv_bytes": 6000000000
    }
  }
}`

// decodeProps is what Client.Props does to a body, minus the HTTP: the named
// fields and the Raw map, so DetectKind sees the same Props a run would.
func decodeProps(t *testing.T, body string) *Props {
	t.Helper()
	var p Props
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("decode props: %v", err)
	}
	if err := json.Unmarshal([]byte(body), &p.Raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	return &p
}

// TestDetectKindEngineBlock: the engine object names the engine outright, and
// it is read before the ik markers and the build_info key — those scan every
// value in the body, and an engine block full of user-chosen paths must not be
// mistaken for one of them. An engine that is not exllamav3 changes nothing:
// detection falls through to the rules that ran before any engine existed.
func TestDetectKindEngineBlock(t *testing.T) {
	cases := []struct {
		name string
		body string
		want tape.ServerKind
	}{
		{"the contract body", enginePropsJSON, tape.ServerExLlamaV3},
		{"engine wins over a build_info key", `{"build_info":"b4321-abcdef12","engine":{"name":"exllamav3"}}`, tape.ServerExLlamaV3},
		{"engine wins over an ik marker elsewhere", `{"system_info":"ik_llama.cpp","engine":{"name":"exllamav3"}}`, tape.ServerExLlamaV3},
		{"another engine name keeps the old rules", `{"build_info":"b4321-abcdef12","engine":{"name":"other","version":"9"}}`, tape.ServerLlamaCPP},
		{"another engine name with no other signal stays unknown", `{"model_path":"/m.gguf","engine":{"name":"other"}}`, tape.ServerUnknown},
		{"an engine object with no name is not an engine claim", `{"engine":{"version":"1.0"}}`, tape.ServerUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectKind(decodeProps(t, tc.body)); got != tc.want {
				t.Errorf("DetectKind = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPropsEngineDecode: the contract round-trips. Every key inside the engine
// object is optional, and an engine the contract did not describe must decode
// with zeros rather than errors; a body with no engine key decodes Engine == nil,
// which is what "absent" has meant for every tape before this field existed.
func TestPropsEngineDecode(t *testing.T) {
	p := decodeProps(t, enginePropsJSON)
	if p.Engine == nil {
		t.Fatal("Engine = nil, want the engine object decoded")
	}
	e := p.Engine
	if e.Name != "exllamav3" || e.Version != "1.5.0" {
		t.Errorf("engine name/version = %q/%q", e.Name, e.Version)
	}
	if want := []string{"-gs", "44,21", "-mcs", "185", "-mct", "32", "-mtp"}; len(e.Args) != len(want) {
		t.Errorf("engine args = %q, want %q", e.Args, want)
	} else {
		for i := range want {
			if e.Args[i] != want[i] {
				t.Errorf("engine args = %q, want %q", e.Args, want)
				break
			}
		}
	}
	m := e.Model
	if m.Format != "exl3" || m.Arch != "Glm5NextForConditionalGeneration" ||
		m.Quant != "EXL3 4.05 bpw · head 6.0" || m.Bytes != 165151541665 || m.Files != 30 ||
		m.Params != 320000000000 || m.NLayers != 45 || m.NExperts != 288 ||
		m.NExpertsUsed != 8 || m.CtxTrain != 1048576 || m.ActiveBytesPerToken != 7000000000 {
		t.Errorf("engine model decoded wrong: %+v", m)
	}
	pl := e.Placement
	if pl.VRAMKVBytes != 6000000000 || len(pl.Devices) != 3 {
		t.Fatalf("engine placement decoded wrong: %+v", pl)
	}
	if d := pl.Devices[0]; d.Device != "GPU0" || d.Bytes != 40000000000 ||
		d.Classes["attention"] != 8000000000 || d.Classes["experts"] != 30000000000 ||
		d.Classes["embeddings"] != 2000000000 || d.Layers != "experts 0-20" {
		t.Errorf("engine device 0 decoded wrong: %+v", d)
	}
	// Per-device active bytes are the one key the engine normally omits.
	if d := pl.Devices[2]; d.ActiveBytesPerToken != 0 {
		t.Errorf("engine device 2 active bytes = %d, want 0 (omitted)", d.ActiveBytesPerToken)
	}
	// Every key optional: a bare engine still decodes, with zeros.
	min := decodeProps(t, `{"engine":{"name":"exllamav3"}}`)
	if min.Engine == nil || min.Engine.Name != "exllamav3" || min.Engine.Version != "" ||
		len(min.Engine.Args) != 0 || len(min.Engine.Placement.Devices) != 0 {
		t.Errorf("minimal engine decoded wrong: %+v", min.Engine)
	}
	// No engine key: nil, never a zero-valued struct, so the recorder can tell
	// "the engine said nothing" from "the engine said it plainly".
	if bare := decodeProps(t, `{"model_path":"/m.gguf","build_info":"b4321"}`); bare.Engine != nil {
		t.Errorf("Engine = %+v, want nil with no engine key", bare.Engine)
	}
}
