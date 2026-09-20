package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// publishHome points HOME at a temporary directory, so a test never reads or
// writes the developer's own ~/.toktape/config.toml — the file holds a token.
// It returns the config path.
func publishHome(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".toktape", "config.toml")
	if body != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// publishTape writes a run file with text in it, so the preview has something
// to count.
func publishTape(t *testing.T) string {
	t.Helper()
	s := *card.Example()
	s.Model.Path = "/home/somebody/models/DeepSeek/DeepSeek-R1-Q4_K_M.gguf"
	s.Server.URL = "http://10.0.0.7:8080"
	s.Host.Hostname = "somebodys-box"
	s.Host.HostnameSource = tape.HostnameObserved
	tp := &tape.Tape{
		Schema:  tape.SchemaVersion,
		Summary: s,
		Requests: []tape.RequestRecord{{
			Index: 0,
			Prompt: tape.PromptRecord{
				Messages:   []tape.Message{{Role: "user", Content: "twelve chars"}},
				Completion: "seven c",
			},
		}},
	}
	path := filepath.Join(t.TempDir(), "run"+tape.Ext)
	if err := tape.Write(path, tp); err != nil {
		t.Fatalf("tape.Write: %v", err)
	}
	return path
}

// acceptingServer is a stand-in for the Worker: it answers 201 with a receipt
// and records that it was called.
func acceptingServer(t *testing.T, deleteToken string) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		body := map[string]string{"id": "abc123", "url": "https://tape.example/r/abc123"}
		if deleteToken != "" {
			body["delete_token"] = deleteToken
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// A dry run is the product, so it goes to stdout, and it touches no network.
func TestPublishDryRun(t *testing.T) {
	publishHome(t, "")
	path := publishTape(t)
	srv, calls := acceptingServer(t, "")

	code, stdout, stderr := exec(t, "publish", path, "--dry-run", "--url", srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if *calls != 0 {
		t.Errorf("a dry run reached the server %d times", *calls)
	}
	for _, want := range []string{"visibility", "public", "hostname", "removed", ":8080", "12 characters of prompt"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the dry run does not mention %q:\n%s", want, stdout)
		}
	}
	for _, forbidden := range []string{"somebodys-box", "10.0.0.7", "/home/somebody"} {
		if strings.Contains(stdout, forbidden) {
			t.Errorf("the dry run shows %q, so the view still carries it:\n%s", forbidden, stdout)
		}
	}
	// A dry run must not record that the warning was seen — nothing was
	// published, so the next real publish is still the first one.
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".toktape", "config.toml")); err == nil {
		t.Error("a dry run wrote the config file")
	}
}

// With a profile in the home directory the dry run lists it — the name, the
// link and the avatar's size — because it is something that leaves the
// machine. With --no-profile the listing says `none`, and the tape's own
// secrets still stay out of it either way.
func TestPublishDryRunProfiles(t *testing.T) {
	pic := profileAvatar(t, 32, 32)
	publishHome(t, "profile_name = \"Lab Rat\"\nprofile_link = \"https://github.com/example\"\nprofile_avatar = \""+pic+"\"\n")
	path := publishTape(t)
	srv, calls := acceptingServer(t, "")

	code, stdout, stderr := exec(t, "publish", path, "--dry-run", "--url", srv.URL,
		"--title", "First ik_llama sweep", "--note", "Trying -fa on.\n\nSecond paragraph.")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if *calls != 0 {
		t.Errorf("a dry run reached the server %d times", *calls)
	}
	for _, want := range []string{"Lab Rat", "https://github.com/example", "32×32 PNG", "First ik_llama sweep", "Trying -fa on."} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the dry run does not mention %q:\n%s", want, stdout)
		}
	}
	for _, forbidden := range []string{"somebodys-box", "10.0.0.7", "/home/somebody"} {
		if strings.Contains(stdout, forbidden) {
			t.Errorf("the dry run shows %q:\n%s", forbidden, stdout)
		}
	}

	code, stdout, stderr = exec(t, "publish", path, "--dry-run", "--url", srv.URL, "--no-profile")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "none — nothing about you travels") {
		t.Errorf("a --no-profile dry run does not say so:\n%s", stdout)
	}
	if strings.Contains(stdout, "Lab Rat") {
		t.Errorf("a --no-profile dry run names the profile anyway:\n%s", stdout)
	}
}

// The link is the product: one line on stdout, so `url=$(toktape publish ...)`
// is the whole integration.
func TestPublishPrintsTheLink(t *testing.T) {
	publishHome(t, "first_publish_warning_seen = true\n")
	path := publishTape(t)
	srv, calls := acceptingServer(t, "")

	code, stdout, stderr := exec(t, "publish", path, "--url", srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if *calls != 1 {
		t.Errorf("the server was called %d times, want 1", *calls)
	}
	if strings.TrimSpace(stdout) != "https://tape.example/r/abc123" {
		t.Errorf("stdout = %q, want the link and nothing else", stdout)
	}
}

// A delete token is the only key to an anonymous upload, so it is said out
// loud — on stderr, because stdout is the link a script captures — and since
// 2026-09-20 it is also filed under ~/.toktape/published.json, 0600, so
// `toktape publish --delete` can find it after the scrollback is gone.
func TestPublishSaysWhatTheDeleteTokenIs(t *testing.T) {
	publishHome(t, "first_publish_warning_seen = true\n")
	srv, _ := acceptingServer(t, "dt_once_only")

	code, stdout, stderr := exec(t, "publish", publishTape(t), "--url", srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if strings.Contains(stdout, "dt_once_only") {
		t.Errorf("the delete token is on stdout, where a script capturing the link would swallow it:\n%s", stdout)
	}
	for _, want := range []string{"dt_once_only", "published.json", "--delete"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not say %q:\n%s", want, stderr)
		}
	}
	// The key lands in the receipts file, mode 0600 — never in the config,
	// which is not a list of everything this machine ever posted.
	receipts := filepath.Join(os.Getenv("HOME"), ".toktape", "published.json")
	body, err := os.ReadFile(receipts)
	if err != nil {
		t.Fatalf("the delete token was not saved: %v", err)
	}
	for _, want := range []string{"abc123", "dt_once_only", "delete_token"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("published.json is missing %q:\n%s", want, body)
		}
	}
	fi, err := os.Stat(receipts)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("published.json mode = %v (%v), want 0600", fi, err)
	}
	cfg, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".toktape", "config.toml"))
	if err == nil && strings.Contains(string(cfg), "dt_once_only") {
		t.Errorf("the delete token was written to the config:\n%s", cfg)
	}
}

// TestPublishFirstWarning: the one-time warning shows the same listing the dry
// run does, --yes answers it without a terminal, and answering it is recorded
// so it happens once per machine.
func TestPublishFirstWarning(t *testing.T) {
	cfgPath := publishHome(t, "")
	path := publishTape(t)
	srv, calls := acceptingServer(t, "")

	code, _, stderr := exec(t, "publish", path, "--url", srv.URL, "--yes")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if *calls != 1 {
		t.Errorf("the server was called %d times, want 1", *calls)
	}
	for _, want := range []string{"first run published from this machine", "12 characters of prompt", "not be asked again"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the warning does not say %q:\n%s", want, stderr)
		}
	}

	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("the config was not written: %v", err)
	}
	if !strings.Contains(string(body), "first_publish_warning_seen = true") {
		t.Errorf("the config does not record the answer:\n%s", body)
	}
	// Once per machine: the second publish says nothing.
	_, _, stderr = exec(t, "publish", path, "--url", srv.URL)
	if strings.Contains(stderr, "first run published from this machine") {
		t.Errorf("the warning came back on the second publish:\n%s", stderr)
	}
}

// TestPublishTextPolicy: --no-text beats --with-text beats publish_text beats
// the default, which is that the body travels (§9.3).
func TestPublishTextPolicy(t *testing.T) {
	const travels = "characters of prompt"
	const removed = "the prompts and the generated text are removed"

	for _, c := range []struct {
		name, config string
		flags        []string
		want         string
	}{
		{"the default publishes the text", "", nil, travels},
		{"--no-text on a default machine", "", []string{"--no-text"}, removed},
		{"the machine opted out", "publish_text = false\n", nil, removed},
		{"--with-text overrides the opt-out", "publish_text = false\n", []string{"--with-text"}, travels},
		{"an explicit publish_text = true is the default said out loud", "publish_text = true\n", nil, travels},
	} {
		t.Run(c.name, func(t *testing.T) {
			publishHome(t, c.config)
			args := append([]string{"publish", publishTape(t), "--dry-run"}, c.flags...)
			code, stdout, stderr := exec(t, args...)
			if code != exitOK {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			if !strings.Contains(stdout, c.want) {
				t.Errorf("want %q in:\n%s", c.want, stdout)
			}
		})
	}
}

func TestPublishRefusals(t *testing.T) {
	t.Run("two flags that say opposite things", func(t *testing.T) {
		publishHome(t, "")
		code, _, stderr := exec(t, "publish", publishTape(t), "--no-text", "--with-text", "--dry-run")
		if code != exitUsage {
			t.Errorf("exit %d, want %d", code, exitUsage)
		}
		if !strings.Contains(stderr, "opposite") {
			t.Errorf("stderr = %q", stderr)
		}
	})

	t.Run("a config file this build cannot read stops the publish", func(t *testing.T) {
		// The file holds the opt-out. Publishing past a file we could not
		// read would be publishing past a decision somebody made.
		publishHome(t, "publish_txt = false\n")
		code, _, stderr := exec(t, "publish", publishTape(t), "--dry-run")
		if code != exitUsage {
			t.Errorf("exit %d, want %d", code, exitUsage)
		}
		if !strings.Contains(stderr, "unknown key") {
			t.Errorf("stderr = %q", stderr)
		}
	})

	t.Run("a service that refuses exits publish, not unreachable", func(t *testing.T) {
		publishHome(t, "first_publish_warning_seen = true\n")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":"one upload a minute without a token"}`)
		}))
		defer srv.Close()

		code, _, stderr := exec(t, "publish", publishTape(t), "--url", srv.URL)
		if code != exitPublish {
			t.Errorf("exit %d, want %d: a publishing service that refuses is not a llama-server that did not answer", code, exitPublish)
		}
		if !strings.Contains(stderr, "one upload a minute") {
			t.Errorf("the service's own words are not in:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--dry-run") {
			t.Errorf("the refusal offers no next step:\n%s", stderr)
		}
	})

	t.Run("no tape named", func(t *testing.T) {
		publishHome(t, "")
		if code, _, _ := exec(t, "publish"); code != exitUsage {
			t.Errorf("exit %d, want %d", code, exitUsage)
		}
	})
}

// With -o json the receipt is one object, which is the contract every other
// verb keeps.
func TestPublishJSON(t *testing.T) {
	publishHome(t, "first_publish_warning_seen = true\n")
	srv, _ := acceptingServer(t, "dt_once_only")

	code, stdout, stderr := exec(t, "publish", publishTape(t), "--url", srv.URL, "-o", "json")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var r struct {
		ID, URL, DeleteToken string
	}
	if err := json.Unmarshal([]byte(stdout), &struct {
		ID          *string `json:"id"`
		URL         *string `json:"url"`
		DeleteToken *string `json:"delete_token"`
	}{&r.ID, &r.URL, &r.DeleteToken}); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
	}
	if r.ID != "abc123" || r.URL == "" || r.DeleteToken != "dt_once_only" {
		t.Errorf("receipt = %+v", r)
	}
}

// --edit changes one owned run instead of uploading (TTP-127): the usage
// errors refuse an empty change, opposite visibilities and a missing token,
// and a successful edit prints the run's link on stdout.
func TestPublishEditUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		args   []string
		want   string
	}{
		{"no flags", "token = \"tk_journal\"\nfirst_publish_warning_seen = true\n",
			[]string{"publish", "--edit", "abc123"}, "--title, --note or --private/--public"},
		{"both visibilities", "token = \"tk_journal\"\nfirst_publish_warning_seen = true\n",
			[]string{"publish", "--edit", "abc123", "--title", "x", "--private", "--public"}, "opposite"},
		{"no token", "first_publish_warning_seen = true\n",
			[]string{"publish", "--edit", "abc123", "--title", "x"}, "needs a journal token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publishHome(t, tc.config)
			code, _, stderr := exec(t, tc.args...)
			if code == exitOK {
				t.Fatalf("%v was accepted", tc.args)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want it to name %q", stderr, tc.want)
			}
		})
	}
}

func TestPublishEditPrintsTheLink(t *testing.T) {
	var method, auth, path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, auth, path = r.Method, r.Header.Get("Authorization"), r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"abc123","published_at":"2026-09-19T00:00:00Z","private":false,"tape":"/r/abc123.tape","tape_bytes":25,"card":null,"owned":true,"index":{"schema":1}}`)
	}))
	defer srv.Close()
	// The service key names this test's hub, so the journal token belongs to
	// it and travels (cmd/toktape/service.go's rule).
	publishHome(t, "token = \"tk_journal\"\nservice = \""+srv.URL+"\"\nfirst_publish_warning_seen = true\n")

	code, stdout, stderr := exec(t, "publish", "--edit", "abc123", "--title", "edited", "--url", srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if strings.TrimSpace(stdout) != srv.URL+"/r/abc123" {
		t.Errorf("stdout = %q, want the run's link and nothing else", stdout)
	}
	if method != http.MethodPatch || path != "/api/v1/runs/abc123" {
		t.Errorf("the edit was %s %s, want PATCH /api/v1/runs/abc123", method, path)
	}
	if !strings.HasPrefix(auth, "Bearer ") || len(body) != 1 || body["title"] != "edited" {
		t.Errorf("auth = %q body = %v, want the journal token and only the title", auth, body)
	}
}

// The run is named by id or by link: a /r/<id> URL gives up its path and
// one trailing extension, a bare id travels as-is.
func TestEditTargetID(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"abc123", "abc123"},
		{"https://tape.midagedev.com/r/abc123", "abc123"},
		{"https://tape.midagedev.com/r/abc123.json", "abc123"},
		{"http://127.0.0.1:8787/r/abc123.tape", "abc123"},
		{"http://127.0.0.1:8787/r/abc123.png", "abc123"},
		{"", ""},
	} {
		if got := editTargetID(tc.in); got != tc.want {
			t.Errorf("editTargetID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The verb is in the usage text, and its flags parse — the same contract the
// other verbs keep.
func TestPublishIsInTheUsageText(t *testing.T) {
	code, stdout, _ := exec(t, "--help")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"toktape publish", "--dry-run", "--private", "publish      nothing was published"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("--help does not mention %q", want)
		}
	}
}

// deletingServer answers DELETE /api/v1/runs/<id> the way the Worker does and
// records the Authorization it saw.
func deletingServer(t *testing.T, status int, reply string) (*httptest.Server, *string) {
	t.Helper()
	auth := ""
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		auth = r.Header.Get("Authorization")
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &auth
}

// publishAnonymously uploads once against acceptingServer and returns the id
// it got, leaving the delete token filed in HOME's published.json.
func publishAnonymously(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	code, _, stderr := exec(t, "publish", publishTape(t), "--url", srv.URL)
	if code != exitOK {
		t.Fatalf("the setup publish failed: %d: %s", code, stderr)
	}
	return "abc123"
}

// hubServer is both halves of the anonymous flow on one service: a POST
// answered with a receipt carrying a delete token, and a DELETE answered with
// the status given, recording the key it was presented.
func hubServer(t *testing.T, deleteToken string, deleteStatus int, deleteReply string) (*httptest.Server, *string) {
	t.Helper()
	auth := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			body := map[string]string{"id": "abc123", "url": r.Host + "/r/abc123"}
			if deleteToken != "" {
				body["delete_token"] = deleteToken
			}
			_ = json.NewEncoder(w).Encode(body)
		case http.MethodDelete:
			auth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(deleteStatus)
			io.WriteString(w, deleteReply)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &auth
}

// Taking a run down is one command: the anonymous upload from this machine
// left its key — and the service it was issued on — in published.json, and
// `--delete` with no --url follows that service, presents the key, and
// removes the entry once the run is down. The token itself never reaches
// stdout.
func TestPublishDeleteWithSavedToken(t *testing.T) {
	publishHome(t, "first_publish_warning_seen = true\n")
	srv, auth := hubServer(t, "dt_saved_key", http.StatusNoContent, "")
	id := publishAnonymously(t, srv)

	code, stdout, stderr := exec(t, "publish", "--delete", id)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if *auth != "Bearer dt_saved_key" {
		t.Errorf("Authorization = %q, want the saved delete token", *auth)
	}
	if strings.TrimSpace(stdout) != "deleted "+id {
		t.Errorf("stdout = %q, want %q", stdout, "deleted "+id)
	}
	if strings.Contains(stdout, "dt_saved_key") {
		t.Errorf("the delete token is on stdout:\n%s", stdout)
	}
	body, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".toktape", "published.json"))
	if err != nil {
		t.Fatalf("published.json: %v", err)
	}
	if strings.Contains(string(body), id) {
		t.Errorf("the deleted run's entry survived:\n%s", body)
	}
}

// A run this machine never uploaded anonymously is still deletable with the
// journal token from the config, on the service the token belongs to.
func TestPublishDeleteWithJournalToken(t *testing.T) {
	srv, auth := deletingServer(t, http.StatusNoContent, "")
	publishHome(t, "token = \"tk_journal\"\nservice = \""+srv.URL+"\"\nfirst_publish_warning_seen = true\n")

	code, stdout, stderr := exec(t, "publish", "--delete", "abc123", "--url", srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if *auth != "Bearer tk_journal" {
		t.Errorf("Authorization = %q, want the journal token", *auth)
	}
	if !strings.Contains(stdout, "deleted abc123") {
		t.Errorf("stdout = %q", stdout)
	}
}

// A full URL names the run the same way an id does.
func TestPublishDeleteTakesAURL(t *testing.T) {
	srv, _ := deletingServer(t, http.StatusNoContent, "")
	publishHome(t, "token = \"tk_journal\"\nservice = \""+srv.URL+"\"\nfirst_publish_warning_seen = true\n")

	code, stdout, stderr := exec(t, "publish", "--delete", srv.URL+"/r/abc123", "--url", srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "deleted abc123") {
		t.Errorf("stdout = %q, want the id", stdout)
	}
}

// A wrong key is the server's refusal, quoted verbatim, and a failure — the
// run is still up.
func TestPublishDeleteRefusalIsQuoted(t *testing.T) {
	publishHome(t, "first_publish_warning_seen = true\n")
	srv, _ := deletingServer(t, http.StatusForbidden, `{"error":"that token does not open this run"}`)

	code, stdout, stderr := exec(t, "publish", "--delete", "abc123", "--token", "dt_wrong", "--url", srv.URL)
	if code != exitPublish {
		t.Fatalf("exit %d, want %d: a run that is still up is not a success", code, exitPublish)
	}
	if !strings.Contains(stderr, "that token does not open this run") {
		t.Errorf("the service's own words are not in:\n%s", stderr)
	}
	if strings.Contains(stdout, "deleted") {
		t.Errorf("stdout says deleted:\n%s", stdout)
	}
}

// Already-gone is a success shape (the server reads it that way, web/src/del.js)
// and still cleans the saved entry up.
func TestPublishDeleteAlreadyGone(t *testing.T) {
	publishHome(t, "first_publish_warning_seen = true\n")
	srv, _ := hubServer(t, "dt_saved_key", http.StatusNotFound, `{"error":"no run with that id; if you just deleted it, it is gone"}`)
	id := publishAnonymously(t, srv)

	code, stdout, stderr := exec(t, "publish", "--delete", id)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "no run with that id") || strings.Contains(stdout, "deleted "+id) {
		t.Errorf("stdout = %q, want it to say the run is already gone", stdout)
	}
	body, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".toktape", "published.json"))
	if err != nil {
		t.Fatalf("published.json: %v", err)
	}
	if strings.Contains(string(body), id) {
		t.Errorf("the gone run's entry survived:\n%s", body)
	}
}

// The usage errors: --delete is its own operation, and a delete without any
// of the three keys is refused with the three sources named.
func TestPublishDeleteUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		args   []string
		want   string
	}{
		{"with a tape file", "first_publish_warning_seen = true\n",
			[]string{"publish", "run.toktape", "--delete", "abc123"}, "not a tape file"},
		{"with --edit", "token = \"tk\"\nfirst_publish_warning_seen = true\n",
			[]string{"publish", "--delete", "abc123", "--edit", "abc123", "--title", "x"}, "name one"},
		{"no key anywhere", "first_publish_warning_seen = true\n",
			[]string{"publish", "--delete", "abc123"}, "--token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publishHome(t, tc.config)
			code, _, stderr := exec(t, tc.args...)
			if code != exitUsage {
				t.Fatalf("exit %d, want %d: %s", code, exitUsage, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want it to name %q", stderr, tc.want)
			}
		})
	}

	// The no-key refusal names all three sources.
	publishHome(t, "first_publish_warning_seen = true\n")
	_, _, stderr := exec(t, "publish", "--delete", "abc123")
	for _, want := range []string{"--token", "published.json", "config.toml"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the no-key refusal does not name %q:\n%s", want, stderr)
		}
	}
}

// --dry-run --delete shows which key source would be used — never the key
// itself — and sends nothing.
func TestPublishDeleteDryRun(t *testing.T) {
	publishHome(t, "first_publish_warning_seen = true\n")
	srv, auth := hubServer(t, "dt_saved_key", http.StatusNoContent, "")
	publishAnonymously(t, srv)

	code, stdout, stderr := exec(t, "publish", "--delete", "abc123", "--dry-run")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if *auth != "" {
		t.Errorf("a dry run reached the server with Authorization %q", *auth)
	}
	if !strings.Contains(stdout, "saved delete token") || strings.Contains(stdout, "dt_saved_key") {
		t.Errorf("stdout = %q, want the source and not the token", stdout)
	}
}
