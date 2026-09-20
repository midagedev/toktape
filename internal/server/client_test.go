package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

const propsJSON = `{
  "model_path": "/models/gpt-oss-20b-UD-Q4_K_M.gguf",
  "build_info": "b4321-abcdef12",
  "chat_template": "{% for message in messages %}...{% endfor %}",
  "total_slots": 4,
  "default_generation_settings": {"n_ctx": 8192, "n_predict": -1}
}`

// propsServer answers the control routes. No test may probe
// DefaultCandidates: this box runs a real llama-server on 8080.
func propsServer(t *testing.T, serverHeader string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		if serverHeader != "" {
			w.Header().Set("Server", serverHeader)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(propsJSON))
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
		  {"id":0,"is_processing":true,"n_ctx":2048,"n_past":312,"cache_tokens":300},
		  {"id":1,"is_processing":false,"n_ctx":2048,"n_past":0},
		  {"id":2,"is_processing":true,"n_ctx":2048,"n_past":40}
		]`))
	})
	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []tape.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var b strings.Builder
		for _, m := range in.Messages {
			b.WriteString("<|" + m.Role + "|>" + m.Content)
		}
		b.WriteString("<|assistant|></think>")
		_ = json.NewEncoder(w).Encode(map[string]string{"prompt": b.String()})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPropsAndDetectKind(t *testing.T) {
	srv := propsServer(t, "llama.cpp")
	c := New(srv.URL)
	p, err := c.Props(context.Background())
	if err != nil {
		t.Fatalf("Props: %v", err)
	}
	if got, want := p.ModelPath, "/models/gpt-oss-20b-UD-Q4_K_M.gguf"; got != want {
		t.Errorf("ModelPath = %q, want %q", got, want)
	}
	if got, want := p.CtxSize(), 8192; got != want {
		t.Errorf("CtxSize = %d, want %d", got, want)
	}
	if got, want := p.TotalSlots, 4; got != want {
		t.Errorf("TotalSlots = %d, want %d", got, want)
	}
	if _, ok := p.Raw["default_generation_settings"]; !ok {
		t.Error("Raw did not keep the whole response")
	}
	if got, want := DetectKind(p), tape.ServerLlamaCPP; got != want {
		t.Errorf("DetectKind = %q, want %q", got, want)
	}
}

func TestDetectKindIKLlama(t *testing.T) {
	srv := propsServer(t, "ik_llama.cpp")
	p, err := New(srv.URL).Props(context.Background())
	if err != nil {
		t.Fatalf("Props: %v", err)
	}
	if got, want := DetectKind(p), tape.ServerIKLlama; got != want {
		t.Errorf("DetectKind = %q, want %q (Server header says ik_llama.cpp)", got, want)
	}
}

func TestDetectKindTable(t *testing.T) {
	cases := []struct {
		name string
		p    *Props
		want tape.ServerKind
	}{
		{"no props at all", nil, tape.ServerUnknown},
		{"plain build info", &Props{BuildInfo: "b4321-abcdef12"}, tape.ServerLlamaCPP},
		{"ik in the build info", &Props{BuildInfo: "ik_llama.cpp-b3500"}, tape.ServerIKLlama},
		{"ik in a raw field", &Props{Raw: map[string]json.RawMessage{"system_info": json.RawMessage(`"IK_LLAMA build"`)}}, tape.ServerIKLlama},
		{"a hex commit is not an ik marker", &Props{BuildInfo: "b9999-deadbeef"}, tape.ServerLlamaCPP},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectKind(tc.p); got != tc.want {
				t.Errorf("DetectKind = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildFromProps(t *testing.T) {
	cases := []struct{ in, build, commit string }{
		{"b4321-abcdef12", "b4321", "abcdef12"},
		{"b3650-1a2b3c4d", "b3650", "1a2b3c4d"},
		{"b4321", "b4321", ""},
		{"", "", ""},
		{"  b1-aa  ", "b1", "aa"},
		// TTP-33: ik_llama.cpp has no bNNNN release counter, so its build_info
		// is not the mainline shape. These are the shapes the parser tolerates;
		// the real one is whatever the lead measures off an ik server.
		{"7b79b229", "", "7b79b229"},
		{"  7b79b229  ", "", "7b79b229"},
		{"3650 (a1b2c3d)", "3650", "a1b2c3d"},
		{"b3650 (7b79b229)", "b3650", "7b79b229"},
		{"build 3650", "3650", ""},
		{"build 3650 (7b79b229)", "3650", "7b79b229"},
		// A mainline build number is five characters and every one of them is
		// a hex digit; the bare-hash rule must not claim it as a commit.
		{"b3650", "b3650", ""},
		{"abcdef", "abcdef", ""}, // six characters: too short to be a hash
		// Not a hash and not a known shape: kept whole rather than guessed at.
		{"ik_llama.cpp", "ik_llama.cpp", ""},
	}
	for _, tc := range cases {
		build, commit := BuildFromProps(tc.in)
		if build != tc.build || commit != tc.commit {
			t.Errorf("BuildFromProps(%q) = %q, %q; want %q, %q", tc.in, build, commit, tc.build, tc.commit)
		}
	}
}

func TestSlots(t *testing.T) {
	srv := propsServer(t, "")
	slots, err := New(srv.URL).Slots(context.Background())
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	if got, want := len(slots), 3; got != want {
		t.Fatalf("len = %d, want %d", got, want)
	}
	if got, want := BusyCount(slots), 2; got != want {
		t.Errorf("BusyCount = %d, want %d", got, want)
	}
	if got, want := slots[0].NPast, 312; got != want {
		t.Errorf("slots[0].NPast = %d, want %d", got, want)
	}
	if _, ok := slots[0].Raw["cache_tokens"]; !ok {
		t.Error("Slot.Raw dropped cache_tokens")
	}
}

func TestSlotsDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"slots endpoint is disabled"}}`, http.StatusNotImplemented)
	}))
	t.Cleanup(srv.Close)
	_, err := New(srv.URL).Slots(context.Background())
	if err == nil {
		t.Fatal("Slots on a --no-slots server returned no error")
	}
	if !strings.Contains(err.Error(), "501") {
		t.Errorf("err = %v, want the status kept", err)
	}
}

func TestApplyTemplate(t *testing.T) {
	srv := propsServer(t, "")
	got, err := New(srv.URL).ApplyTemplate(context.Background(), []tape.Message{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatalf("ApplyTemplate: %v", err)
	}
	want := "<|system|>be brief<|user|>hello<|assistant|></think>"
	if got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
	if !strings.Contains(got, "</think>") {
		t.Error("the rendered prompt is what lesson 4 asks the card to show")
	}
}

// TestDiscover probes only httptest servers. Reaching DefaultCandidates from a
// test would hit whatever llama-server is running on the developer's machine
// and make the result depend on it.
func TestDiscover(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(dead.Close)
	alive := propsServer(t, "")

	got, err := New("").Discover(context.Background(), []string{dead.URL, alive.URL})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got != alive.URL {
		t.Errorf("Discover = %q, want %q (first candidate that answers /props)", got, alive.URL)
	}
}

// TestDefaultCandidatesEndWithOllamaAndLMStudio (2026-09-21, TTP-158): a bare
// `toktape` did not find Ollama (its default port is 11434) nor LM Studio
// (1234). Both answer /v1/models and 404 /props — measured on Ollama 0.34.2,
// `curl http://127.0.0.1:11434/props` → 404 "404 page not found" — so
// Discover's second pass over the ErrNoProps candidates is what finds them.
// They sit AFTER the llama ports so a /props server still wins wherever it
// listens (TestDiscoverPrefersPropsServerListedEarlier).
func TestDefaultCandidatesEndWithOllamaAndLMStudio(t *testing.T) {
	want := []string{"http://127.0.0.1:11434", "http://127.0.0.1:1234"}
	got := DefaultCandidates[len(DefaultCandidates)-len(want):]
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("DefaultCandidates end with %v, want %v after the llama ports", got, want)
		}
	}
	for _, llama := range []string{"http://127.0.0.1:8080", "http://127.0.0.1:8001"} {
		if !slices.Contains(DefaultCandidates, llama) {
			t.Errorf("DefaultCandidates lost the llama port %s", llama)
		}
	}
	if ports := DefaultPorts(); !strings.Contains(ports, "11434") || !strings.Contains(ports, "1234") {
		t.Errorf("DefaultPorts = %q, want it to name 11434 and 1234 (the failed-scan hint offers what was probed)", ports)
	}
}

func TestDiscoverPrefersOwnBaseURL(t *testing.T) {
	alive := propsServer(t, "")
	other := propsServer(t, "")
	got, err := New(alive.URL).Discover(context.Background(), []string{other.URL})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got != alive.URL {
		t.Errorf("Discover = %q, want the client's own URL %q tried first", got, alive.URL)
	}
}

func TestDiscoverNoneAnswer(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := closed.URL
	closed.Close() // nothing listens there any more

	_, err := New("").Discover(context.Background(), []string{url})
	if err == nil {
		t.Fatal("Discover found a server that is not there")
	}
	if !strings.Contains(err.Error(), url) {
		t.Errorf("err = %v, want it to name what was tried", err)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"http://127.0.0.1:8080/", "http://127.0.0.1:8080"},
		{"http://host:8080///", "http://host:8080"},
		{"https://example.com", "https://example.com"},
		{"  ", ""},
	}
	for _, tc := range cases {
		if got := NormalizeBaseURL(tc.in); got != tc.want {
			t.Errorf("NormalizeBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNoBaseURL(t *testing.T) {
	_, err := New("").Props(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Discover") {
		t.Fatalf("err = %v, want it to point at Discover", err)
	}
}

// loadingBody is the envelope llama-server answers with while a model is
// still being read off disk.
const loadingBody = `{"error":{"code":503,"message":"Loading model","type":"unavailable_error"}}`

// TestPropsClassifiesLoading pins the distinction the first-run experience
// depends on: a server that is not there yet is a mistake, a server that is
// loading is a wait. Every row is a shape observed or documented upstream.
func TestPropsClassifiesLoading(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		want    error
		wantMsg string
	}{
		{"loading 503 envelope", http.StatusServiceUnavailable, loadingBody, ErrLoading, "Loading model"},
		{"loading 503 empty body", http.StatusServiceUnavailable, "", ErrLoading, "Service Unavailable"},
		{"loading message on 500", http.StatusInternalServerError,
			`{"error":{"message":"the model is loading"}}`, ErrLoading, "loading"},
		{"plain 500 is unreachable", http.StatusInternalServerError, "nope", ErrUnreachable, "HTTP 500"},
		{"404 is unreachable", http.StatusNotFound, "not found", ErrUnreachable, "HTTP 404"},
		{"ok", http.StatusOK, propsJSON, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := New(srv.URL).Props(context.Background())
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Props: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Props error = %v, want %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("Props error = %q, want it to carry %q", err, tc.wantMsg)
			}
		})
	}
}

// A server that accepted the connection and then said nothing is the 450 GB
// model case: the port is open, the answer never comes. That is loading, not
// unreachable — the whole point of the classification.
func TestPropsHangIsLoading(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer func() { close(block); srv.Close() }()

	_, err := New(srv.URL, WithTimeout(50*time.Millisecond)).Props(context.Background())
	if !errors.Is(err, ErrLoading) {
		t.Fatalf("Props error = %v, want ErrLoading", err)
	}
}

// A cancelled caller is not a server state. Without this the wait loop would
// read its own cancellation as "still loading" and keep polling.
func TestPropsCancelledIsNotLoading(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer func() { close(block); srv.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(srv.URL).Props(ctx)
	if errors.Is(err, ErrLoading) {
		t.Fatalf("a cancelled Props reported ErrLoading: %v", err)
	}
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Props error = %v, want ErrUnreachable", err)
	}
}

// A refused port is unreachable, which is what keeps a typo an immediate
// failure instead of a ten-minute wait.
func TestPropsRefusedIsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	if _, err := New(url).Props(context.Background()); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Props error = %v, want ErrUnreachable", err)
	}
}

// Discover reports the loading server rather than the first refused port: a
// user who launched the server and toktape together must be told to wait.
func TestDiscoverPrefersLoadingOverRefused(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	loading := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(loadingBody))
	}))
	defer loading.Close()

	_, err := New("").Discover(context.Background(), []string{deadURL, loading.URL})
	if !errors.Is(err, ErrLoading) {
		t.Fatalf("Discover error = %v, want ErrLoading", err)
	}
}

func TestLoadingDetail(t *testing.T) {
	cases := []struct{ body, want string }{
		{loadingBody, "Loading model"},
		{`{"error":{"message":"Loading model"}}`, "Loading model"},
		{`{"error":{"message":"model is loading"}}`, "model is loading"},
		{`{"error":{"message":"context window exceeded"}}`, ""},
		{`{"error":{"message":""}}`, ""},
		{`not json`, ""},
		{``, ""},
	}
	for _, tc := range cases {
		if got := loadingDetail([]byte(tc.body)); got != tc.want {
			t.Errorf("loadingDetail(%q) = %q, want %q", tc.body, got, tc.want)
		}
	}
}

// busyServer is an ik_llama.cpp server in the middle of a completion: /props
// blocks until the request finishes (measured 2026-09-13: over two minutes)
// while /health answers at once with the given status.
func busyServer(t *testing.T, healthStatus int, healthBody string) *httptest.Server {
	t.Helper()
	block := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(healthStatus)
		_, _ = w.Write([]byte(healthBody))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { close(block); srv.Close() })
	return srv
}

// TestPropsBusyIsNotLoading: a /props that does not answer while /health does
// is a busy server, not a loading one. Printing "loading" there sends the user
// to wait for a model that is already resident (TTP-33, 2026-09-13).
func TestPropsBusyIsNotLoading(t *testing.T) {
	srv := busyServer(t, http.StatusOK, `{"status":"ok"}`)
	_, err := New(srv.URL, WithTimeout(50*time.Millisecond)).Props(context.Background())
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("Props error = %v, want ErrBusy", err)
	}
	if errors.Is(err, ErrLoading) || errors.Is(err, ErrUnreachable) {
		t.Fatalf("a busy server was also reported as loading or unreachable: %v", err)
	}
	if !strings.Contains(err.Error(), "busy with another request") {
		t.Errorf("Props error = %q, want it to say the server is busy", err)
	}
}

// TestPropsHangWithHealth503IsLoading: /health is how llama-server says it is
// loading, so a 503 there keeps the loading verdict.
func TestPropsHangWithHealth503IsLoading(t *testing.T) {
	srv := busyServer(t, http.StatusServiceUnavailable, loadingBody)
	_, err := New(srv.URL, WithTimeout(50*time.Millisecond)).Props(context.Background())
	if !errors.Is(err, ErrLoading) {
		t.Fatalf("Props error = %v, want ErrLoading", err)
	}
	if errors.Is(err, ErrBusy) {
		t.Fatalf("a loading server was reported as busy: %v", err)
	}
}

// TestClassifyPropsHealth pins the pure half of the busy rule.
func TestClassifyPropsHealth(t *testing.T) {
	deadline := fmt.Errorf("server: GET /props: %w", context.DeadlineExceeded)
	cases := []struct {
		name      string
		status    int
		transport error
		health    int
		want      error
	}{
		{"no answer, /health ok", 0, deadline, http.StatusOK, ErrBusy},
		{"no answer, /health 503", 0, deadline, http.StatusServiceUnavailable, ErrLoading},
		{"no answer, /health not probed or failed", 0, deadline, 0, ErrLoading},
		{"no answer, /health 404", 0, deadline, http.StatusNotFound, ErrLoading},
		{"answered: /health is not consulted", http.StatusOK, nil, http.StatusServiceUnavailable, nil},
		{"refused: /health is not consulted", 0, errors.New("connection refused"), http.StatusOK, ErrUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := classifyProps("http://x", tc.status, nil, tc.transport, nil, tc.health)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("classifyProps = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("classifyProps = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestDiscoverPrefersBusyOverRefused: a busy server is a server, and it
// outranks a refused port exactly as a loading one does.
func TestDiscoverPrefersBusyOverRefused(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	busy := busyServer(t, http.StatusOK, `{"status":"ok"}`)

	_, err := New("", WithTimeout(50*time.Millisecond)).Discover(context.Background(), []string{deadURL, busy.URL})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("Discover error = %v, want ErrBusy", err)
	}
}

// Tokenize is the server's own count, asked for without the special tokens
// so the number is the content's, and any failure is an error the caller
// prices around: a non-200, a body that is not the token array, and a route
// that is not there at all (2026-09-20, run plan).
func TestTokenizeCountsTheServersOwnTokens(t *testing.T) {
	var gotAddSpecial *bool
	var gotContent string
	mux := http.NewServeMux()
	mux.HandleFunc("/tokenize", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Content     string `json:"content"`
			AddSpecial  *bool  `json:"add_special"`
			NotAContent string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gotContent, gotAddSpecial = in.Content, in.AddSpecial
		tokens := make([]int, 0, len(in.Content)/4)
		for i := 0; i < len(in.Content)/4; i++ {
			tokens = append(tokens, i)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tokens": tokens})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	n, err := New(srv.URL).Tokenize(context.Background(), "sixteen chars here!")
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	if want := len("sixteen chars here!") / 4; n != want {
		t.Errorf("Tokenize = %d tokens, want %d (the route's own arithmetic)", n, want)
	}
	if gotContent != "sixteen chars here!" {
		t.Errorf("content = %q, want the string asked for", gotContent)
	}
	if gotAddSpecial == nil || *gotAddSpecial {
		t.Error("add_special not sent as false; the count must be the content's own")
	}

	// A body that is not a token array is an error, never a count.
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/tokenize", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	})
	srv2 := httptest.NewServer(mux2)
	t.Cleanup(srv2.Close)
	if _, err := New(srv2.URL).Tokenize(context.Background(), "x"); err == nil {
		t.Error("Tokenize on a body with no tokens array returned nil error")
	}

	// A server without the route (every OpenAI-compatible one) is an error
	// too: the caller prices, it does not guess a zero.
	mux3 := http.NewServeMux()
	srv3 := httptest.NewServer(mux3)
	t.Cleanup(srv3.Close)
	if _, err := New(srv3.URL).Tokenize(context.Background(), "x"); err == nil {
		t.Error("Tokenize on a server with no /tokenize returned nil error")
	}
}

// TestPropsUnauthorizedIsNotUnreachableAlone is the TTP-169 client-half gate
// (matrix gap 6, 2026-09-21): a 401 on /props used to classify as plain
// ErrUnreachable, which the CLI answers with starting-server advice — a
// lever that answers a server that is not there, printed at one that is
// running and wants a key. The sentinel must be distinguishable and its
// sentence plain, with the server's own body still quoted.
//
// FAIL-first (2026-09-21, pre-change source): errors.Is(err, ErrUnauthorized)
// did not compile (no sentinel existed) and the message read
// "server: unreachable: http://…: HTTP 401: {…}" — no credentials sentence.
func TestPropsUnauthorizedIsNotUnreachableAlone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"Invalid API key"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := New(srv.URL).Props(context.Background())
	if err == nil {
		t.Fatal("Props accepted a 401")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("errors.Is(err, ErrUnauthorized) = false: %v", err)
	}
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("errors.Is(err, ErrUnreachable) = false; the sentinel must keep wrapping it: %v", err)
	}
	for _, want := range []string{"wants credentials", "(HTTP 401)", "Invalid API key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err, want)
		}
	}
}

// TestPropsForbiddenIsUnauthorizedToo: 403 is the same fact about the same
// lever with a different status word.
func TestPropsForbiddenIsUnauthorizedToo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()
	_, err := New(srv.URL).Props(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("errors.Is(err, ErrUnauthorized) = false: %v", err)
	}
	if !strings.Contains(err.Error(), "(HTTP 403)") {
		t.Errorf("err = %q, want the status in the sentence", err)
	}
}
