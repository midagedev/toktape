package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/config"
	"github.com/midagedev/toktape/internal/publish"
)

// The credential-leak contract (track selfhost): the journal token in
// config.toml belongs to the service the config names — the hosted one when
// the `service` key is unset — and it is never sent to any other service.
//
// FAIL-first, 2026-09-20: on the tree before resolveService, every verb here
// built its client with `BaseURL: *f.url, Token: cfg.Token`, so a `--url`
// pointing anywhere carried the hosted service's journal token to that host
// in an Authorization header. These tests were written against that tree and
// shown failing there before the fix.

// leakServer answers any request well enough for the verb under test and
// records the Authorization header it saw, and how many requests arrived.
func leakServer(t *testing.T, reply func(w http.ResponseWriter)) (*httptest.Server, *string, *int) {
	t.Helper()
	auth := ""
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		auth = r.Header.Get("Authorization")
		reply(w)
	}))
	t.Cleanup(srv.Close)
	return srv, &auth, &calls
}

// uploadReply answers a publish the way the Worker does.
func uploadReply(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	io.WriteString(w, `{"id":"abc123","url":"https://tape.example/r/abc123"}`+"\n")
}

// runJSONReply answers the run shape `show` reads.
func runJSONReply(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"id":"fullrun","published_at":"2026-09-20 00:00:00","private":false,`+
		`"tape":"/r/fullrun.tape","tape_bytes":25,"card":null,"owned":false,"index":{"schema":1}}`+"\n")
}

// The mandated leak test: a config with a journal token and no `service` key
// means the token belongs to the hosted service, so a publish aimed at another
// service reaches it with no Authorization header at all — an anonymous
// upload, which is a supported path, not a degraded one.
func TestJournalTokenStaysHomeOnPublish(t *testing.T) {
	publishHome(t, "token = \"tk_journal\"\nfirst_publish_warning_seen = true\n")
	srv, auth, _ := leakServer(t, uploadReply)

	code, _, stderr := exec(t, "publish", publishTape(t), "--url", srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if *auth != "" {
		t.Errorf("another service saw Authorization %q; the journal token must stay home", *auth)
	}
	if !strings.Contains(stderr, "not sent to") {
		t.Errorf("stderr does not say the token was withheld:\n%s", stderr)
	}
}

// --edit needs the journal token, so against another service it is refused
// before any request rather than sent there.
func TestJournalTokenStaysHomeOnEdit(t *testing.T) {
	publishHome(t, "token = \"tk_journal\"\nfirst_publish_warning_seen = true\n")
	srv, auth, calls := leakServer(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"abc123"}`)
	})

	code, _, stderr := exec(t, "publish", "--edit", "abc123", "--title", "x", "--url", srv.URL)
	if code != exitUsage {
		t.Fatalf("exit %d, want %d: %s", code, exitUsage, stderr)
	}
	if *calls != 0 {
		t.Errorf("the edit reached the other service %d times with Authorization %q", *calls, *auth)
	}
	if !strings.Contains(stderr, "needs a journal token") {
		t.Errorf("stderr = %q, want the edit path's token sentence", stderr)
	}
}

// --delete falls back to the journal token only for the service the token
// belongs to; against another service there is no key to present.
func TestJournalTokenStaysHomeOnDelete(t *testing.T) {
	publishHome(t, "token = \"tk_journal\"\nfirst_publish_warning_seen = true\n")
	srv, auth, calls := leakServer(t, func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusNoContent)
	})

	code, _, stderr := exec(t, "publish", "--delete", "abc123", "--url", srv.URL)
	if code != exitUsage {
		t.Fatalf("exit %d, want %d: %s", code, exitUsage, stderr)
	}
	if *calls != 0 {
		t.Errorf("the delete reached the other service %d times with Authorization %q", *calls, *auth)
	}
	if !strings.Contains(stderr, "no key to present") {
		t.Errorf("stderr = %q, want it to name the missing key", stderr)
	}
}

// runs --mine is the journal scope: one token's own runs. Against another
// service it is refused before any request.
func TestJournalTokenStaysHomeOnMine(t *testing.T) {
	publishHome(t, "token = \"tk_journal\"\nfirst_publish_warning_seen = true\n")
	srv, auth, calls := leakServer(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"scope":"mine","runs":[],"next":null}`+"\n")
	})

	code, _, stderr := exec(t, "runs", "--mine", "--url", srv.URL)
	if code != exitUsage {
		t.Fatalf("exit %d, want %d: %s", code, exitUsage, stderr)
	}
	if *calls != 0 {
		t.Errorf("the journal scope reached the other service %d times with Authorization %q", *calls, *auth)
	}
	if !strings.Contains(stderr, "needs a journal token") {
		t.Errorf("stderr = %q, want the mine path's token sentence", stderr)
	}
}

// show works without a token — the run is public — so against another service
// the request still goes out, but with no Authorization header.
func TestJournalTokenStaysHomeOnShow(t *testing.T) {
	publishHome(t, "token = \"tk_journal\"\nfirst_publish_warning_seen = true\n")
	srv, auth, _ := leakServer(t, runJSONReply)

	code, _, stderr := exec(t, "show", "fullrun", "--url", srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if *auth != "" {
		t.Errorf("the read carried Authorization %q to another service", *auth)
	}
	if !strings.Contains(stderr, "not sent to") {
		t.Errorf("stderr does not say the token was withheld:\n%s", stderr)
	}
}

// config `service` is the resident form of --url: with it, the journal token
// belongs to that service and does reach it.
func TestConfigServiceReceivesTheToken(t *testing.T) {
	srv, auth, _ := leakServer(t, uploadReply)
	publishHome(t, "token = \"tk_journal\"\nservice = \""+srv.URL+"\"\nfirst_publish_warning_seen = true\n")

	code, _, stderr := exec(t, "publish", publishTape(t))
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if *auth != "Bearer tk_journal" {
		t.Errorf("Authorization = %q, want the journal token for the configured service", *auth)
	}
	if strings.Contains(stderr, "not sent to") {
		t.Errorf("a publish to the configured service still says the token was withheld:\n%s", stderr)
	}
}

// TOKTAPE_SERVICE is the one-shot form of the config key, and the verbs read
// the real environment: with it set, a publish with no --url goes there, and
// the config's journal token stays home.
func TestServiceEnvBeatsConfig(t *testing.T) {
	env, envAuth, _ := leakServer(t, uploadReply)
	cfgHome, cfgAuth, _ := leakServer(t, uploadReply)
	publishHome(t, "token = \"tk_journal\"\nservice = \""+cfgHome.URL+"\"\nfirst_publish_warning_seen = true\n")
	t.Setenv("TOKTAPE_SERVICE", env.URL)

	code, _, stderr := exec(t, "publish", publishTape(t))
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if *envAuth != "" {
		t.Errorf("the env service saw Authorization %q", *envAuth)
	}
	if *cfgAuth != "" {
		t.Errorf("the config service was reached anyway: Authorization %q", *cfgAuth)
	}
	if !strings.Contains(stderr, "not sent to "+env.URL) {
		t.Errorf("stderr does not say where the token stayed home from:\n%s", stderr)
	}
}

// --token is consent: typed alongside --url, it travels to that service —
// the case a self-hoster with a journal token for the hosted hub, or a token
// minted straight into their own D1, uses to publish owned runs anyway.
func TestTokenFlagReachesAnyService(t *testing.T) {
	srv, auth, _ := leakServer(t, uploadReply)
	publishHome(t, "first_publish_warning_seen = true\n")

	code, _, stderr := exec(t, "publish", publishTape(t), "--url", srv.URL, "--token", "tk_typed")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if *auth != "Bearer tk_typed" {
		t.Errorf("Authorization = %q, want the typed token", *auth)
	}
	if strings.Contains(stderr, "not sent to") {
		t.Errorf("a typed token still produced the withheld note:\n%s", stderr)
	}
}

// The resolver's contract, as one table. Precedence, normalisation, the token
// rule and the refusals all live here because they are one decision, and a
// field of the decision drifting from the others is exactly the leak class
// this file exists to close.
func TestResolveService(t *testing.T) {
	const tokenCfg = "tk_journal"
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	cfg := func(service string) *config.Config { return &config.Config{Token: tokenCfg, Service: service} }

	for _, tc := range []struct {
		name string
		// in
		flagURL, flagToken string
		cfgService         string
		env                func(string) string
		// want
		baseURL   string
		source    string
		token     string
		withheld  bool
		wantError string
	}{
		{
			name:    "nothing set is the hosted service, and its token travels",
			baseURL: publish.DefaultBaseURL, source: srcDefault,
			token: tokenCfg,
		},
		{
			name:    "the flag names another service and the token stays home",
			flagURL: "https://tapes.example.com", source: srcFlag,
			baseURL: "https://tapes.example.com", withheld: true,
		},
		{
			name:       "config service is the resident form, and the token is its own",
			cfgService: "https://tapes.example.com", source: srcConfig,
			baseURL: "https://tapes.example.com", token: tokenCfg,
		},
		{
			name:       "env beats config",
			cfgService: "https://config.example.com", flagURL: "",
			env:     env("TOKTAPE_SERVICE", "https://env.example.com"),
			baseURL: "https://env.example.com", source: srcEnv, withheld: true,
		},
		{
			name:    "the flag beats env",
			env:     env("TOKTAPE_SERVICE", "https://env.example.com"),
			flagURL: "https://flag.example.com", source: srcFlag,
			baseURL: "https://flag.example.com", withheld: true,
		},
		{
			name:    "TOKTAPE_TOKEN is the flag's equal and travels to any service",
			flagURL: "https://tapes.example.com", source: srcFlag,
			env:     env("TOKTAPE_TOKEN", "tk_typed"),
			baseURL: "https://tapes.example.com", token: "tk_typed",
		},
		{
			name:    "--token is consent: it travels to any service",
			flagURL: "https://tapes.example.com", flagToken: "tk_typed", source: srcFlag,
			baseURL: "https://tapes.example.com", token: "tk_typed",
		},
		{
			name:       "the token follows the config service, not the flag, when both are set",
			cfgService: "https://tapes.example.com", flagURL: "https://tapes.example.com",
			source: srcFlag, baseURL: "https://tapes.example.com", token: tokenCfg,
		},
		{
			name:       "a trailing slash and case fold away: same service, token travels",
			cfgService: "https://TAPES.example.com/", flagURL: "https://tapes.example.com",
			source: srcFlag, baseURL: "https://tapes.example.com", token: tokenCfg,
		},
		{
			name:       "an explicit default port is the same service (https:443)",
			cfgService: "https://tapes.example.com:443", flagURL: "https://tapes.example.com",
			source: srcFlag, baseURL: "https://tapes.example.com", token: tokenCfg,
		},
		{
			name:       "a different port is a different service",
			cfgService: "https://tapes.example.com", flagURL: "https://tapes.example.com:8443",
			source: srcFlag, baseURL: "https://tapes.example.com:8443", withheld: true,
		},
		{
			name:    "plain http is fine on loopback",
			flagURL: "http://127.0.0.1:8787", source: srcFlag,
			baseURL: "http://127.0.0.1:8787", withheld: true,
		},
		{
			name:    "plain http on localhost by name is fine too",
			flagURL: "http://localhost:8787", source: srcFlag,
			baseURL: "http://localhost:8787", withheld: true,
		},
		{
			name:    "plain http off loopback is refused, naming the clear-text token",
			flagURL: "http://tapes.example.com", wantError: "clear",
		},
		{
			name:    "a scheme that is not http(s) is refused, naming the value",
			flagURL: "ftp://tapes.example.com", wantError: "ftp://tapes.example.com",
		},
		{
			name:    "a service address with no host is refused",
			flagURL: "https://", wantError: "https://",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := tc.env
			if getenv == nil {
				getenv = func(string) string { return "" }
			}
			svc, err := resolveService(tc.flagURL, tc.flagToken, cfg(tc.cfgService), getenv)
			if tc.wantError != "" {
				if err == nil {
					t.Fatalf("resolveService accepted %q", tc.flagURL)
				}
				if !strings.Contains(err.Error(), tc.wantError) {
					t.Errorf("error = %q, want it to name %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveService: %v", err)
			}
			if svc.BaseURL != tc.baseURL {
				t.Errorf("BaseURL = %q, want %q", svc.BaseURL, tc.baseURL)
			}
			if svc.Source != tc.source {
				t.Errorf("Source = %q, want %q", svc.Source, tc.source)
			}
			if svc.Token != tc.token {
				t.Errorf("Token = %q, want %q", svc.Token, tc.token)
			}
			if svc.TokenWithheld != tc.withheld {
				t.Errorf("TokenWithheld = %v, want %v", svc.TokenWithheld, tc.withheld)
			}
		})
	}
}
