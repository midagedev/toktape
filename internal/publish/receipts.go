package publish

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The anonymous-upload receipts (2026-09-20).
//
// An anonymous upload's delete token used to be printed once and stored
// nowhere, so losing the terminal scrollback meant losing the ability to take
// the run down. The user asked for take-down to be one command, and a key that
// exists only in scrollback is a key people lose. The file holds only the
// anonymous uploads from this machine — a token-owned upload is already
// deletable by its owner and never lands here — it is 0600 in the config
// directory (config.Dir(), the same place config.toml lives), and
// `toktape publish --delete <id>` removes the entry once the run is down.

// ReceiptsFile is the store's name inside the config directory.
const ReceiptsFile = "published.json"

// savedReceipt is one remembered anonymous upload.
type savedReceipt struct {
	ID          string    `json:"id"`
	URL         string    `json:"url,omitempty"`
	DeleteToken string    `json:"delete_token"`
	PublishedAt time.Time `json:"published_at"`
}

// receiptsFile is the whole store's shape.
type receiptsFile struct {
	Runs []savedReceipt `json:"runs"`
}

// ReceiptsPath is the store's full path inside dir.
func ReceiptsPath(dir string) string {
	return filepath.Join(dir, ReceiptsFile)
}

// loadReceipts reads the store. A missing file is an empty store and no error:
// a machine that has never published anonymously has no file, and that is a
// state. A file that will not parse IS an error — it holds the only keys to
// runs this machine put up, and silently treating it as empty would turn a
// truncated write into runs that can never be taken down.
func loadReceipts(dir string) (receiptsFile, error) {
	var rf receiptsFile
	b, err := os.ReadFile(ReceiptsPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return rf, nil
		}
		return rf, fmt.Errorf("publish: read %s: %w", ReceiptsPath(dir), err)
	}
	if err := json.Unmarshal(b, &rf); err != nil {
		return receiptsFile{}, fmt.Errorf("publish: %s is not readable: %w", ReceiptsPath(dir), err)
	}
	return rf, nil
}

// saveReceipts writes the store whole, through a temporary file in the same
// directory and a rename — the same discipline config.SaveTo keeps — so a
// store being read while it is written is either the old one or the new one.
// It is 0600 from creation: the file holds keys, and a key must never exist
// on disk world-readable, not even between the two calls.
func saveReceipts(dir string, rf receiptsFile) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("publish: create %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return fmt.Errorf("publish: write %s: %w", ReceiptsPath(dir), err)
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(dir, ".published-*.json")
	if err != nil {
		return fmt.Errorf("publish: write %s: %w", ReceiptsPath(dir), err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("publish: write %s: %w", ReceiptsPath(dir), err)
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("publish: write %s: %w", ReceiptsPath(dir), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("publish: write %s: %w", ReceiptsPath(dir), err)
	}
	if err := os.Rename(tmp.Name(), ReceiptsPath(dir)); err != nil {
		return fmt.Errorf("publish: write %s: %w", ReceiptsPath(dir), err)
	}
	return nil
}

// Remember records one anonymous upload's delete token, replacing any entry
// the same id already has. It is called only after the upload was accepted,
// so a failure to remember is reported by the caller as a warning and never
// unwinds the publish.
func Remember(dir string, r Receipt) error {
	if r.ID == "" || r.DeleteToken == "" {
		return fmt.Errorf("publish: nothing to remember without an id and a delete token")
	}
	rf, err := loadReceipts(dir)
	if err != nil {
		return err
	}
	entry := savedReceipt{ID: r.ID, URL: r.URL, DeleteToken: r.DeleteToken, PublishedAt: time.Now().UTC()}
	replaced := false
	for i := range rf.Runs {
		if rf.Runs[i].ID == r.ID {
			rf.Runs[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		rf.Runs = append(rf.Runs, entry)
	}
	return saveReceipts(dir, rf)
}

// TokenFor is the saved delete token for one run id, if this machine kept
// one. A corrupt store reads as "no token" rather than an error — the caller
// has two other keys to try (a --token flag, the journal token in the
// config), and the token resolution order must not die on a file it cannot
// parse. The corruption itself surfaces the moment Remember or Forget next
// touches the file, both of which do return it.
func TokenFor(dir, id string) (string, bool) {
	rf, err := loadReceipts(dir)
	if err != nil {
		return "", false
	}
	for _, r := range rf.Runs {
		if r.ID == id && r.DeleteToken != "" {
			return r.DeleteToken, true
		}
	}
	return "", false
}

// Forget drops one run's entry, for a run that is down — deleted, or already
// gone. An id the store never held is not an error: forgetting it is what the
// caller asked for either way. A corrupt store is, for the same reason as in
// Remember: a file full of keys that cannot be read is a fact to print, not
// one to quietly re-write as empty.
func Forget(dir, id string) error {
	rf, err := loadReceipts(dir)
	if err != nil {
		return err
	}
	kept := rf.Runs[:0]
	for _, r := range rf.Runs {
		if r.ID != id {
			kept = append(kept, r)
		}
	}
	if len(kept) == len(rf.Runs) {
		return nil
	}
	rf.Runs = kept
	return saveReceipts(dir, rf)
}
