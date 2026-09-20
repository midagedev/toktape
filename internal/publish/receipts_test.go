package publish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The store round-trips: what Remember wrote is what EntryFor finds, the file
// is mode 0600 in a 0700 directory (it holds keys), and re-remembering the
// same id replaces rather than duplicates.
func TestReceiptsRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", ".toktape")

	if svc, tok, ok := EntryFor(dir, "abc123"); ok || tok != "" || svc != "" {
		t.Fatalf("EntryFor on a missing store = %q, %q, %v, want nothing", svc, tok, ok)
	}
	if err := Remember(dir, Receipt{ID: "abc123", URL: "https://tape.example/r/abc123", DeleteToken: "dt_1"}, "https://tapes.example.com"); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	svc, tok, ok := EntryFor(dir, "abc123")
	if !ok || tok != "dt_1" || svc != "https://tapes.example.com" {
		t.Fatalf("EntryFor = %q, %q, %v, want the service and dt_1", svc, tok, ok)
	}
	if _, _, ok := EntryFor(dir, "other"); ok {
		t.Error("EntryFor found an entry for an id that was never remembered")
	}

	fi, err := os.Stat(ReceiptsPath(dir))
	if err != nil {
		t.Fatalf("the store was not written: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("published.json mode = %v, want 0600 — it holds delete tokens", fi.Mode().Perm())
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("the store's directory: %v", err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %v, want 0700", di.Mode().Perm())
	}

	// The same id again replaces: one run, one entry, the newer key.
	if err := Remember(dir, Receipt{ID: "abc123", DeleteToken: "dt_2"}, "https://tapes.example.com"); err != nil {
		t.Fatalf("Remember again: %v", err)
	}
	if _, tok, _ := EntryFor(dir, "abc123"); tok != "dt_2" {
		t.Errorf("EntryFor after a re-remember = %q, want dt_2", tok)
	}
	if err := Remember(dir, Receipt{ID: "def456", DeleteToken: "dt_3"}, ""); err != nil {
		t.Fatalf("Remember a second run: %v", err)
	}

	// Forget removes the one entry and keeps the other.
	if err := Forget(dir, "abc123"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if _, _, ok := EntryFor(dir, "abc123"); ok {
		t.Error("the forgotten run still has a token")
	}
	if _, tok, ok := EntryFor(dir, "def456"); !ok || tok != "dt_3" {
		t.Errorf("Forget took def456 with it: %q, %v", tok, ok)
	}
}

// An entry with no service is one written before services were recorded
// (2026-09-20): it means the default service, and EntryFor keeps that
// distinguishable from "no entry" by its ok.
func TestReceiptsEntryWithoutService(t *testing.T) {
	dir := t.TempDir()
	if err := Remember(dir, Receipt{ID: "abc123", DeleteToken: "dt_1"}, ""); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	svc, tok, ok := EntryFor(dir, "abc123")
	if !ok || tok != "dt_1" || svc != "" {
		t.Fatalf("EntryFor = %q, %q, %v, want an entry with no service", svc, tok, ok)
	}
	// A hand edit of the older shape — no service key — reads the same way.
	if err := os.WriteFile(ReceiptsPath(dir), []byte(`{"runs":[{"id":"def456","delete_token":"dt_2","published_at":"2026-09-01T00:00:00Z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, tok, ok = EntryFor(dir, "def456")
	if !ok || tok != "dt_2" || svc != "" {
		t.Errorf("EntryFor on the old shape = %q, %q, %v, want the token and no service", svc, tok, ok)
	}
}

// Forgetting an id the store never held is a no-op success, and it does not
// create the file: a delete against a machine that never published
// anonymously has nothing to clean up and nothing to write.
func TestReceiptsForgetAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := Forget(dir, "nope"); err != nil {
		t.Fatalf("Forget of an absent id: %v", err)
	}
	if _, err := os.Stat(ReceiptsPath(dir)); !os.IsNotExist(err) {
		t.Errorf("Forget of an absent id wrote the store: %v", err)
	}

	if err := Remember(dir, Receipt{ID: "abc123", DeleteToken: "dt_1"}, ""); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	before, err := os.ReadFile(ReceiptsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := Forget(dir, "nope"); err != nil {
		t.Fatalf("Forget of an absent id: %v", err)
	}
	after, err := os.ReadFile(ReceiptsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("Forget of an absent id rewrote the store:\n%s\n%s", before, after)
	}
}

// A corrupt store is an error the caller prints, never a silent truncation:
// Remember and Forget refuse rather than rewrite it as empty, because the
// file holds the only keys to runs this machine put up.
func TestReceiptsCorruptFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(ReceiptsPath(dir), []byte(`{"runs":[{"id":`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Remember(dir, Receipt{ID: "x", DeleteToken: "dt"}, ""); err == nil {
		t.Error("Remember wrote past a corrupt store")
	} else if !strings.Contains(err.Error(), "not readable") {
		t.Errorf("error = %q, want it to name the file", err)
	}
	if err := Forget(dir, "x"); err == nil {
		t.Error("Forget wrote past a corrupt store")
	}
	// The corrupt bytes are still there, not truncated to a fresh store.
	b, err := os.ReadFile(ReceiptsPath(dir))
	if err != nil || string(b) != `{"runs":[{"id":` {
		t.Errorf("the corrupt store was rewritten: %q, %v", b, err)
	}
	// EntryFor cannot error (its caller has two other keys to try), so it
	// reads as no entry — the corruption itself surfaces on the next
	// Remember or Forget, which do return it.
	if _, tok, ok := EntryFor(dir, "x"); ok || tok != "" {
		t.Errorf("EntryFor on a corrupt store = %q, %v, want nothing", tok, ok)
	}
}

// Nothing to remember is refused rather than stored: an entry with no key
// would answer EntryFor with a token that opens nothing.
func TestReceiptsRefuseEmptyReceipt(t *testing.T) {
	dir := t.TempDir()
	for name, r := range map[string]Receipt{
		"no id":    {DeleteToken: "dt"},
		"no token": {ID: "abc123"},
	} {
		if err := Remember(dir, r, ""); err == nil {
			t.Errorf("Remember accepted a receipt with %s", name)
		}
	}
	if _, err := os.Stat(ReceiptsPath(dir)); !os.IsNotExist(err) {
		t.Errorf("a refused receipt still wrote the store: %v", err)
	}
}

// The on-disk shape is pinned: an id, a url, a delete token, the service and
// a timestamp under "runs", so a hand edit and a future schema change can see
// what this build meant.
func TestReceiptsFileShape(t *testing.T) {
	dir := t.TempDir()
	before := time.Now().UTC()
	if err := Remember(dir, Receipt{ID: "abc123", URL: "https://tape.example/r/abc123", DeleteToken: "dt_1"}, "https://tapes.example.com"); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	b, err := os.ReadFile(ReceiptsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"runs"`, `"id": "abc123"`, `"url": "https://tape.example/r/abc123"`, `"delete_token": "dt_1"`, `"service": "https://tapes.example.com"`, `"published_at"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the store is missing %q:\n%s", want, b)
		}
	}
	rf, err := loadReceipts(dir)
	if err != nil {
		t.Fatalf("loadReceipts: %v", err)
	}
	if len(rf.Runs) != 1 || rf.Runs[0].PublishedAt.Before(before) {
		t.Errorf("store = %+v, want one run stamped after the Remember", rf.Runs)
	}
}
