package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// shareHub is a stand-in for the Worker that numbers its receipts, so two
// uploads in one run come back with two links. failOn is the 1-based upload
// that answers 500 (0 for none).
type shareHub struct {
	mu     sync.Mutex
	calls  int
	failOn int
	// owned answers without a delete token, as a token-owned upload does.
	owned bool
	srv   *httptest.Server
}

func newShareHub(t *testing.T, failOn int) *shareHub {
	t.Helper()
	h := &shareHub{failOn: failOn}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// share's check of a remembered link (TTP-207): a public run,
			// and not an upload, so calls still counts uploads only.
			fmt.Fprint(w, `{"id":"x","private":false}`)
			return
		}
		h.mu.Lock()
		h.calls++
		n := h.calls
		h.mu.Unlock()
		if n == h.failOn {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		body := map[string]string{
			"id":  fmt.Sprintf("id%d", n),
			"url": fmt.Sprintf("https://tape.example/r/id%d", n),
		}
		if !h.owned {
			body["delete_token"] = fmt.Sprintf("dt_%d", n)
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *shareHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

// shareTape writes one run into dir. The id sorts as the ls rule sorts it, so
// a later id is a newer tape. host is the label the tape was recorded with.
func shareTape(t *testing.T, dir, id, host string, mut func(*tape.RunSummary)) string {
	t.Helper()
	s := *card.Example()
	s.ID = id
	s.Host.Hostname = host
	s.Host.HostnameSource = tape.HostnameLabelled
	if host == "" {
		s.Host.HostnameSource = ""
	}
	if mut != nil {
		mut(&s)
	}
	return writeTape(t, dir, &s)
}

// shareTerminal fakes a person at a terminal: share's own hooks, restored
// after the test. Nothing else in the process is a terminal under `go test`.
func shareTerminal(t *testing.T, interactive bool, input string) {
	t.Helper()
	oldIn, oldTTY := shareStdin, shareInteractive
	shareStdin = strings.NewReader(input)
	shareInteractive = func(*cli) bool { return interactive }
	t.Cleanup(func() { shareStdin, shareInteractive = oldIn, oldTTY })
}

func blockLines(stdout string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.HasPrefix(l, "- [") {
			out = append(out, l)
		}
	}
	return out
}

func TestSharePicksTheNewestByTheLsRule(t *testing.T) {
	publishHome(t, "")
	shareTerminal(t, false, "")
	hub := newShareHub(t, 0)
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-a", "rig", nil)
	shareTape(t, dir, "20260913-110000-b", "rig", nil)
	shareTape(t, dir, "20260913-120000-c", "rig", nil)

	code, stdout, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--yes", "-n", "2")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if hub.count() != 2 {
		t.Fatalf("-n 2 uploaded %d tapes, want 2", hub.count())
	}
	if got := blockLines(stdout); len(got) != 2 || !strings.Contains(got[0], "/r/id1") || !strings.Contains(got[1], "/r/id2") {
		t.Errorf("block:\n%s", stdout)
	}
	// c then b: the newest first, which is the order ls lists them in.
	if !strings.Contains(stderr, "20260913-120000-c") || strings.Contains(stderr, "20260913-100000-a") {
		t.Errorf("the disclosures are not for the two newest:\n%s", stderr)
	}
}

func TestShareNMoreThanTapesPublishesWhatThereIs(t *testing.T) {
	publishHome(t, "")
	shareTerminal(t, false, "")
	hub := newShareHub(t, 0)
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-a", "rig", nil)
	shareTape(t, dir, "20260913-110000-b", "rig", nil)

	code, _, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--yes", "-n", "3")
	if code != exitOK || hub.count() != 2 {
		t.Fatalf("exit %d, %d uploads, want 2: %s", code, hub.count(), stderr)
	}
}

func TestShareBadN(t *testing.T) {
	publishHome(t, "")
	shareTerminal(t, false, "")
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-a", "rig", nil)
	for _, n := range []string{"0", "21", "-1"} {
		code, _, stderr := exec(t, "share", "--out", dir, "--yes", "-n", n)
		if code != exitUsage || !strings.Contains(stderr, "toktape share: -n is 1 to 20") {
			t.Errorf("-n %s: exit %d: %s", n, code, stderr)
		}
	}
}

func TestShareSecondRunSkipsAndStillPrintsTheBlock(t *testing.T) {
	publishHome(t, "")
	shareTerminal(t, false, "")
	hub := newShareHub(t, 0)
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-a", "rig", nil)
	shareTape(t, dir, "20260913-110000-b", "rig", nil)
	args := []string{"share", "--out", dir, "--url", hub.srv.URL, "--yes", "-n", "2"}

	_, first, _ := exec(t, args...)
	code, second, stderr := exec(t, args...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if hub.count() != 2 {
		t.Errorf("the second share uploaded again: %d calls", hub.count())
	}
	if second != first {
		t.Errorf("the remembered block differs.\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if !strings.Contains(stderr, "already published") || !strings.Contains(stderr, "Nothing new") {
		t.Errorf("stderr does not say why nothing went up:\n%s", stderr)
	}
}

func TestShareProfilePrompt(t *testing.T) {
	cfgPath := publishHome(t, "")
	hub := newShareHub(t, 0)
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-a", "rig", nil)

	// A bad name is asked again once; the Enter at the end answers the
	// [Y/n] question with its default.
	long := strings.Repeat("x", 41)
	shareTerminal(t, true, long+"\nLab Rat\nhttps://lab.example/me\n\n")
	code, _, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"name (Enter to skip):", "link (Enter to skip):", "publish 1 tape(s) to 127.0.0.1:", "[Y/n]"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	body, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(body), "Lab Rat") || !strings.Contains(string(body), "https://lab.example/me") {
		t.Errorf("the profile was not saved through config:\n%s", body)
	}
	if hub.count() != 1 {
		t.Errorf("%d uploads, want 1", hub.count())
	}
}

func TestShareProfilePromptSkipsAndHonoursYes(t *testing.T) {
	publishHome(t, "")
	hub := newShareHub(t, 0)
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-a", "rig", nil)

	// --yes on a terminal: no prompt, and the line that says how to set one.
	shareTerminal(t, true, "")
	code, _, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--yes")
	if code != exitOK || strings.Contains(stderr, "Enter to skip") || !strings.Contains(stderr, "toktape profile --name") {
		t.Fatalf("exit %d: %s", code, stderr)
	}

	// Enter twice skips both questions; nothing is saved.
	dir2 := t.TempDir()
	shareTape(t, dir2, "20260913-110000-b", "rig", nil)
	shareTerminal(t, true, "\n\n\n")
	code, _, stderr = exec(t, "share", "--out", dir2, "--url", hub.srv.URL)
	if code != exitOK || !strings.Contains(stderr, "name (Enter to skip):") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".toktape", "config.toml")); err == nil {
		// The first-publish acknowledgement may create it; the profile must not be in it.
		b, _ := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".toktape", "config.toml"))
		if strings.Contains(string(b), "profile_name") {
			t.Errorf("skipped questions saved a profile:\n%s", b)
		}
	}
}

func TestShareWithoutTerminalNeedsYes(t *testing.T) {
	publishHome(t, "")
	shareTerminal(t, false, "")
	hub := newShareHub(t, 0)
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-a", "rig", nil)

	code, _, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL)
	if code != exitUsage || !strings.Contains(stderr, "--yes") {
		t.Fatalf("exit %d, want %d naming --yes: %s", code, exitUsage, stderr)
	}
	if hub.count() != 0 {
		t.Errorf("a pipe without --yes uploaded %d tapes", hub.count())
	}
	if strings.Contains(stderr, "Enter to skip") {
		t.Errorf("a pipe was asked a question:\n%s", stderr)
	}

	code, _, stderr = exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--yes")
	if code != exitOK || !strings.Contains(stderr, "without a name") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func TestShareDeclineUploadsNothing(t *testing.T) {
	publishHome(t, "")
	hub := newShareHub(t, 0)
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-a", "rig", nil)
	shareTerminal(t, true, "\n\nn\n")
	code, stdout, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL)
	if code != exitPublish || hub.count() != 0 || stdout != "" {
		t.Fatalf("exit %d, %d uploads, stdout %q: %s", code, hub.count(), stdout, stderr)
	}
}

func TestShareDryRunUploadsNothing(t *testing.T) {
	cfgPath := publishHome(t, "")
	shareTerminal(t, false, "")
	hub := newShareHub(t, 0)
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-a", "rig", nil)

	code, stdout, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--dry-run")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if hub.count() != 0 {
		t.Errorf("a dry run uploaded %d tapes", hub.count())
	}
	if !strings.Contains(stderr, "Publishing ") || !strings.Contains(stderr, "hostname") {
		t.Errorf("no disclosure:\n%s", stderr)
	}
	if got := blockLines(stdout); len(got) != 1 || !strings.Contains(got[0], "](<link>)") {
		t.Errorf("block with a placeholder link wanted:\n%s", stdout)
	}
	if _, err := os.Stat(cfgPath); err == nil {
		t.Error("a dry run wrote the config")
	}
}

func TestShareOneUploadFails(t *testing.T) {
	publishHome(t, "")
	shareTerminal(t, false, "")
	hub := newShareHub(t, 2)
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-a", "rig", nil)
	shareTape(t, dir, "20260913-110000-b", "rig", nil)

	code, stdout, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--yes", "-n", "2")
	if code != exitPublish {
		t.Fatalf("exit %d, want %d: %s", code, exitPublish, stderr)
	}
	// b is the newest and went first; a is the second upload and failed.
	if got := blockLines(stdout); len(got) != 1 || !strings.Contains(got[0], "/r/id1") {
		t.Errorf("block:\n%s", stdout)
	}
	if !strings.Contains(stderr, "20260913-100000-a") || !strings.Contains(stderr, "500") {
		t.Errorf("stderr does not name the failed tape:\n%s", stderr)
	}
}

// The exact line for a token tape and for a decision tape. The strings are
// derived from the fixture: card.Example's index row (model_id, quant_raw,
// gpus_raw twice, engine llama-server b3650, decode 17.4) and the hub
// preview's rounding (fmt in web/src/page.js); the decision tape's p50 is the
// 38.8 the test sets.
func TestShareBlockLines(t *testing.T) {
	publishHome(t, "")
	shareTerminal(t, false, "")
	hub := newShareHub(t, 0)
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-token", "rig-a", nil)
	shareTape(t, dir, "20260913-110000-decide", "rig-a", func(s *tape.RunSummary) {
		s.Mode = tape.ModeDecision
		s.Decision = &tape.DecisionSummary{Endpoint: "/v1/systemone", Requests: 3, WarmP50Ms: 38.8}
	})

	code, stdout, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--yes", "-n", "2")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	want := "toktape runs from rig-a:\n" +
		"- [38.8 ms p50 decision · deepseek-r1-distill-llama-70b · Q4_K_M](https://tape.example/r/id1) — 2× NVIDIA GeForce RTX 3090 · llama-server b3650\n" +
		"- [17.4 tok/s · deepseek-r1-distill-llama-70b · Q4_K_M](https://tape.example/r/id2) — 2× NVIDIA GeForce RTX 3090 · llama-server b3650\n"
	if stdout != want {
		t.Errorf("block differs.\ngot:\n%s\nwant:\n%s", stdout, want)
	}
}

func TestShareNeverUploadsAChat(t *testing.T) {
	publishHome(t, "")
	shareTerminal(t, false, "")
	hub := newShareHub(t, 0)
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-bench", "rig", nil)
	shareTape(t, dir, "20260913-110000-chat", "rig", func(s *tape.RunSummary) { s.Mode = tape.ModeChat })

	code, stdout, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--yes")
	if code != exitOK || hub.count() != 1 {
		t.Fatalf("exit %d, %d uploads, want the bench alone: %s", code, hub.count(), stderr)
	}
	if !strings.Contains(stderr, "skipped 1 chat tape") || strings.Contains(stderr, "20260913-110000-chat\n  visibility") {
		t.Errorf("stderr:\n%s", stderr)
	}
	if len(blockLines(stdout)) != 1 {
		t.Errorf("block:\n%s", stdout)
	}

	// Only a chat left: nothing to publish, exit 0, nothing uploaded.
	dir2 := t.TempDir()
	shareTape(t, dir2, "20260913-120000-chat", "rig", func(s *tape.RunSummary) { s.Mode = tape.ModeChat })
	code, stdout, stderr = exec(t, "share", "--out", dir2, "--url", hub.srv.URL, "--yes")
	if code != exitOK || hub.count() != 1 || stdout != "" {
		t.Fatalf("exit %d, %d uploads, stdout %q: %s", code, hub.count(), stdout, stderr)
	}
	if !strings.Contains(stderr, "chat") || !strings.Contains(stderr, "Nothing to publish") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestShareWarnsAboutReceiptsWithoutAHash(t *testing.T) {
	home := publishHome(t, "")
	shareTerminal(t, false, "")
	hub := newShareHub(t, 0)
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-a", "rig", nil)
	// An earlier upload from before the hash was recorded.
	store := filepath.Join(filepath.Dir(home), "published.json")
	old := fmt.Sprintf(`{"runs":[{"id":"old1","url":"https://tape.example/r/old1","delete_token":"dt","published_at":"2026-09-20T00:00:00Z","service":%q}]}`, hub.srv.URL)
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--dry-run")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "1 earlier upload from this machine cannot be matched to files") {
		t.Errorf("no warning:\n%s", stderr)
	}
	// And with none, no line.
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	_, _, stderr = exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--dry-run")
	if strings.Contains(stderr, "cannot be matched") {
		t.Errorf("a warning with nothing to warn about:\n%s", stderr)
	}
}

func TestShareHeaderComesFromThePublicView(t *testing.T) {
	publishHome(t, "")
	shareTerminal(t, false, "")
	hub := newShareHub(t, 0)

	cases := []struct {
		name  string
		hosts []string
		want  string
	}{
		{"labelled", []string{"rig-a"}, "toktape runs from rig-a:"},
		{"label set to empty", []string{""}, "toktape runs:"},
		{"two rigs", []string{"rig-a", "rig-b"}, "toktape runs:"},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		for i, h := range tc.hosts {
			shareTape(t, dir, fmt.Sprintf("20260913-1%d0000-x", i), h, func(s *tape.RunSummary) {
				// The local tape names a real machine; only a label may travel.
				s.Server.Host = "workstation"
			})
		}
		code, stdout, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--dry-run", "-n", "2")
		if code != exitOK {
			t.Fatalf("%s: exit %d: %s", tc.name, code, stderr)
		}
		if first, _, _ := strings.Cut(stdout, "\n"); first != tc.want {
			t.Errorf("%s: header %q, want %q", tc.name, first, tc.want)
		}
		if strings.Contains(stdout, "?") || strings.Contains(stdout, "workstation") {
			t.Errorf("%s: the block leaks or prints ?:\n%s", tc.name, stdout)
		}
	}
}

func TestShareJSON(t *testing.T) {
	publishHome(t, "")
	shareTerminal(t, false, "")
	hub := newShareHub(t, 2)
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-a", "rig", nil)
	shareTape(t, dir, "20260913-110000-b", "rig", nil)
	args := []string{"share", "--out", dir, "--url", hub.srv.URL, "--yes", "-n", "2", "-o", "json"}

	code, stdout, _ := exec(t, args...)
	if code != exitPublish {
		t.Fatalf("exit %d", code)
	}
	var rows []struct {
		File    string `json:"file"`
		URL     string `json:"url"`
		Skipped bool   `json:"skipped"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("stdout is not a two-row array: %v\n%s", err, stdout)
	}
	if rows[0].URL == "" || rows[0].Error != "" || rows[1].Error == "" || rows[1].URL != "" {
		t.Errorf("rows: %+v", rows)
	}
	// The first is remembered; a rerun marks it skipped and retries the other.
	code, stdout, _ = exec(t, args...)
	if code != exitOK {
		t.Fatalf("rerun exit %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil || !rows[0].Skipped || rows[0].URL == "" || rows[1].Skipped {
		t.Errorf("rerun rows: %+v (%v)", rows, err)
	}
}

func TestShareIsAVerbWithHelp(t *testing.T) {
	code, out, _ := exec(t, "help", "share")
	if code != exitOK || !strings.Contains(out, "toktape share") || !strings.Contains(out, "--yes") {
		t.Fatalf("help share: exit %d\n%s", code, out)
	}
	if _, usage, _ := exec(t, "--help"); !strings.Contains(usage, "toktape share") {
		t.Error("the usage text does not list share")
	}
}

// A token-owned upload leaves a hash and a link and no key, and the next share
// skips the file all the same.
func TestShareRemembersATokenOwnedUpload(t *testing.T) {
	hub := newShareHub(t, 0)
	hub.owned = true
	home := publishHome(t, fmt.Sprintf("token = \"tk_owner\"\nservice = %q\n", hub.srv.URL))
	shareTerminal(t, false, "")
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-a", "rig", nil)

	if code, _, stderr := exec(t, "share", "--out", dir, "--yes"); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	store, err := os.ReadFile(filepath.Join(filepath.Dir(home), "published.json"))
	if err != nil || !strings.Contains(string(store), `"tape_sha256"`) || !strings.Contains(string(store), `"owned": true`) {
		t.Fatalf("no owned entry with a hash (%v):\n%s", err, store)
	}
	code, stdout, _ := exec(t, "share", "--out", dir, "--yes")
	if code != exitOK || hub.count() != 1 || !strings.Contains(stdout, "/r/id1") {
		t.Errorf("the second share: exit %d, %d uploads\n%s", code, hub.count(), stdout)
	}
}

// publish writes the tape's hash too, so a tape published by hand is not
// published again by share.
func TestPublishLetsShareSkipTheFile(t *testing.T) {
	publishHome(t, "")
	shareTerminal(t, false, "")
	hub := newShareHub(t, 0)
	dir := t.TempDir()
	path := shareTape(t, dir, "20260913-100000-a", "rig", nil)

	if code, _, stderr := exec(t, "publish", path, "--url", hub.srv.URL, "--yes"); code != exitOK {
		t.Fatalf("publish exit %d: %s", code, stderr)
	}
	code, stdout, _ := exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--yes")
	if code != exitOK || hub.count() != 1 || !strings.Contains(stdout, "/r/id1") {
		t.Errorf("share after publish: exit %d, %d uploads\n%s", code, hub.count(), stdout)
	}
}

// TTP-207: share checks a remembered link once before it puts it in the
// block. runHub numbers its uploads (up1, up2, ...) and answers GET
// /r/<id>.json with whatever the test set.
type runHub struct {
	mu      sync.Mutex
	uploads int
	gets    map[string]int
	status  int
	private bool
	srv     *httptest.Server
}

func newRunHub(t *testing.T) *runHub {
	t.Helper()
	h := &runHub{gets: map[string]int{}, status: 200}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if r.Method == http.MethodGet {
			h.gets[r.URL.Path]++
			if h.status != 200 {
				http.Error(w, "nope", h.status)
				return
			}
			fmt.Fprintf(w, `{"id":"x","private":%v}`, h.private)
			return
		}
		h.uploads++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id":           fmt.Sprintf("up%d", h.uploads),
			"url":          fmt.Sprintf("https://tape.example/r/up%d", h.uploads),
			"delete_token": "dt",
		})
	}))
	t.Cleanup(h.srv.Close)
	return h
}

// seeded publishes one tape through share, so a receipt (up1) is remembered,
// then lets the test set how the hub answers the check.
func seeded(t *testing.T) (*runHub, string, string) {
	t.Helper()
	home := publishHome(t, "")
	shareTerminal(t, false, "")
	hub := newRunHub(t)
	dir := t.TempDir()
	shareTape(t, dir, "20260913-100000-a", "rig", nil)
	if code, _, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--yes"); code != exitOK {
		t.Fatalf("seed: exit %d: %s", code, stderr)
	}
	if hub.uploads != 1 || len(hub.gets) != 0 {
		t.Fatalf("seed: %d uploads, gets %v", hub.uploads, hub.gets)
	}
	return hub, dir, filepath.Join(filepath.Dir(home), "published.json")
}

func TestShareRemembersARunThatIsGone(t *testing.T) {
	hub, dir, store := seeded(t)
	hub.status = 404
	code, stdout, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--yes")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if hub.uploads != 2 {
		t.Errorf("uploads = %d, want 2 (published again)", hub.uploads)
	}
	if !strings.Contains(stderr, "toktape share: 20260913-100000-a is no longer on "+serviceHost(hub.srv.URL)+"; publishing it again") {
		t.Errorf("no 'no longer on' line:\n%s", stderr)
	}
	if strings.Contains(stdout, "/r/up1") || !strings.Contains(stdout, "/r/up2") {
		t.Errorf("the block carries the old link or lacks the new one:\n%s", stdout)
	}
	b, _ := os.ReadFile(store)
	if strings.Contains(string(b), `"up1"`) {
		t.Errorf("the dead receipt is still remembered:\n%s", b)
	}
	if hub.gets["/r/up1.json"] != 1 {
		t.Errorf("gets = %v, want one for up1", hub.gets)
	}
}

// A private run is unlisted: out of the search, and its link still opens
// (web/src/run.js), so its link stays in the block and nothing is
// published again (lead, 2026-10-12: the first cut of TTP-207 left it out).
func TestShareKeepsAPrivateRunsLink(t *testing.T) {
	hub, dir, _ := seeded(t)
	hub.private = true
	code, stdout, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--yes")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if hub.uploads != 1 {
		t.Errorf("uploads = %d, want 1 (a private run is up)", hub.uploads)
	}
	if !strings.Contains(stdout, "/r/up1") || !strings.Contains(stderr, "already published") {
		t.Errorf("the private run's link left the block:\n%s\n%s", stdout, stderr)
	}
	if strings.Contains(stderr, "is private on") {
		t.Errorf("a private run was treated as gone:\n%s", stderr)
	}
}

func TestShareKeepsTheLinkWhenTheCheckFails(t *testing.T) {
	hub, dir, _ := seeded(t)
	hub.status = 500
	for _, extra := range [][]string{{"--yes"}, {"--dry-run"}} {
		args := append([]string{"share", "--out", dir, "--url", hub.srv.URL}, extra...)
		code, stdout, stderr := exec(t, args...)
		if code != exitOK {
			t.Fatalf("%v: exit %d: %s", extra, code, stderr)
		}
		if hub.uploads != 1 {
			t.Errorf("%v: uploads = %d, want 1", extra, hub.uploads)
		}
		if !strings.Contains(stdout, "/r/up1") {
			t.Errorf("%v: the old link is not in the block:\n%s", extra, stdout)
		}
		if !strings.Contains(stderr, "toktape share: could not check 20260913-100000-a (") || !strings.Contains(stderr, "); keeping its link") {
			t.Errorf("%v: no 'could not check' line:\n%s", extra, stderr)
		}
	}
}

func TestSharePublicRememberedRunIsCheckedOnce(t *testing.T) {
	hub, dir, _ := seeded(t)
	code, stdout, stderr := exec(t, "share", "--out", dir, "--url", hub.srv.URL, "--yes")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if hub.uploads != 1 || hub.gets["/r/up1.json"] != 1 || len(hub.gets) != 1 {
		t.Errorf("uploads %d, gets %v; want 1 upload and one GET for up1", hub.uploads, hub.gets)
	}
	if !strings.Contains(stdout, "/r/up1") || !strings.Contains(stderr, "already published") {
		t.Errorf("unchanged behaviour lost:\n%s\n%s", stdout, stderr)
	}
}
