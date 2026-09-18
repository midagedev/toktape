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
// loud — on stderr, because stdout is the link a script captures.
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
	if !strings.Contains(stderr, "dt_once_only") || !strings.Contains(stderr, "not saved anywhere") {
		t.Errorf("stderr does not say what the delete token is:\n%s", stderr)
	}
	// Storing it would turn the config into a list of everything this machine
	// ever posted.
	body, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".toktape", "config.toml"))
	if err == nil && strings.Contains(string(body), "dt_once_only") {
		t.Errorf("the delete token was written to the config:\n%s", body)
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
