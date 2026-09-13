package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// ikPropsFixture is the /props body of a real ik_llama.cpp 7b79b229
// llama-server, captured 2026-09-13. It has no build_info key, and nothing
// outside model_path and chat_template names the engine.
const ikPropsFixture = "testdata/props-ik-7b79b229.json"

// TestDetectKindIKPropsFixture: an ik server's /props alone does not say what
// it is. Reporting it as llama-server — which toktape did — prints a default
// nobody observed (TTP-33, 2026-09-13).
func TestDetectKindIKPropsFixture(t *testing.T) {
	body, err := os.ReadFile(ikPropsFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	p, err := New(srv.URL).Props(context.Background())
	if err != nil {
		t.Fatalf("Props: %v", err)
	}
	if got, want := DetectKind(p), tape.ServerUnknown; got != want {
		t.Errorf("DetectKind(ik /props) = %q, want %q", got, want)
	}
	if build, commit := BuildFromProps(p.BuildInfo); build != "" || commit != "" {
		t.Errorf("BuildFromProps(ik /props) = %q, %q; want both empty", build, commit)
	}
	// The fixture's model path and template are real text a user chose; the
	// engine is still not named by them.
	if p.ModelPath == "" || p.CtxSize() != 16384 {
		t.Errorf("fixture parsed wrong: model_path %q, n_ctx %d", p.ModelPath, p.CtxSize())
	}
}

// TestDetectKindBuildInfoKey: a build_info key is mainline's signature, with
// any value; a body without it and without an ik marker is unknown.
func TestDetectKindBuildInfoKey(t *testing.T) {
	raw := func(s string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	cases := []struct {
		name string
		p    *Props
		want tape.ServerKind
	}{
		{"build_info with a value", &Props{BuildInfo: "b4321-abcdef12", Raw: raw(`{"build_info":"b4321-abcdef12"}`)}, tape.ServerLlamaCPP},
		{"build_info present but empty", &Props{Raw: raw(`{"build_info":""}`)}, tape.ServerLlamaCPP},
		{"build_info present as null", &Props{Raw: raw(`{"build_info":null}`)}, tape.ServerLlamaCPP},
		{"no build_info key", &Props{ModelPath: "/m.gguf", Raw: raw(`{"model_path":"/m.gguf","total_slots":1}`)}, tape.ServerUnknown},
		{"an empty object", &Props{Raw: raw(`{}`)}, tape.ServerUnknown},
		{"an ik marker without build_info", &Props{Raw: raw(`{"system_info":"ik_llama.cpp"}`)}, tape.ServerIKLlama},
		{"an ik Server header without build_info", &Props{Raw: raw(`{}`), Headers: http.Header{"Server": []string{"ik_llama.cpp"}}}, tape.ServerIKLlama},
		{"a mainline Server header is not build_info", &Props{Raw: raw(`{}`), Headers: http.Header{"Server": []string{"llama.cpp"}}}, tape.ServerUnknown},
		{"ik in model_path alone is not a marker", &Props{Raw: raw(`{"model_path":"/src/ik_llama.cpp/m.gguf"}`)}, tape.ServerUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectKind(tc.p); got != tc.want {
				t.Errorf("DetectKind = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRefineKind: the process names the engine when /props did not.
func TestRefineKind(t *testing.T) {
	cases := []struct {
		name string
		k    tape.ServerKind
		exe  string
		argv []string
		want tape.ServerKind
	}{
		{"unknown, exe inside an ik checkout", tape.ServerUnknown, "/home/u/ik_llama.cpp/build/bin/llama-server", []string{"./llama-server"}, tape.ServerIKLlama},
		{"unknown, argv[0] names ik", tape.ServerUnknown, "", []string{"/opt/ik_llama.cpp/build/bin/llama-server", "-m", "x.gguf"}, tape.ServerIKLlama},
		{"markers are case-insensitive", tape.ServerUnknown, "/opt/IK_LLAMA.CPP/bin/llama-server", nil, tape.ServerIKLlama},
		{"the dash spelling", tape.ServerUnknown, "/opt/ik-llama/bin/llama-server", nil, tape.ServerIKLlama},
		{"mainline from build_info, exe inside an ik checkout", tape.ServerLlamaCPP, "/src/ik_llama.cpp/build/bin/llama-server", nil, tape.ServerIKLlama},
		{"already ik stays ik", tape.ServerIKLlama, "/usr/local/bin/llama-server", nil, tape.ServerIKLlama},
		// A mainline and an ik binary share the name: do not guess.
		{"unknown, a bare llama-server stays unknown", tape.ServerUnknown, "/usr/local/bin/llama-server", []string{"llama-server"}, tape.ServerUnknown},
		{"mainline stays mainline", tape.ServerLlamaCPP, "/usr/local/bin/llama-server", []string{"llama-server"}, tape.ServerLlamaCPP},
		{"nothing observed", tape.ServerUnknown, "", nil, tape.ServerUnknown},
		// A flag value is user text, not the binary: only argv[0] is read.
		{"an ik path in a later argument is not a marker", tape.ServerUnknown, "/usr/local/bin/llama-server", []string{"llama-server", "-m", "/src/ik_llama.cpp/models/x.gguf"}, tape.ServerUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RefineKind(tc.k, tc.exe, tc.argv); got != tc.want {
				t.Errorf("RefineKind(%q, %q, %q) = %q, want %q", tc.k, tc.exe, tc.argv, got, tc.want)
			}
		})
	}
}
