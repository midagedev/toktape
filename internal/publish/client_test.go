package publish

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// received is one upload as the server saw it, parsed the way the Worker will
// have to parse it. The Worker does not exist yet (TTP-115), so this test is
// where its contract is pinned: it is written against the doc block on
// Client, and the Worker is written against this.
type received struct {
	auth        string
	userAgent   string
	tape        *tape.Tape
	tapeName    string
	tapeType    string
	index       Index
	indexType   string
	privateSeen bool
	extraParts  []string
}

func serve(t *testing.T, status int, reply string) (*httptest.Server, *received) {
	t.Helper()
	got := &received{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != UploadPath {
			t.Errorf("%s %s, want POST %s", r.Method, r.URL.Path, UploadPath)
		}
		got.auth = r.Header.Get("Authorization")
		got.userAgent = r.Header.Get("User-Agent")

		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("content type: %v", err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("next part: %v", err)
			}
			switch p.FormName() {
			case "tape":
				got.tapeName, got.tapeType = p.FileName(), p.Header.Get("Content-Type")
				tp, err := tape.Decode(p)
				if err != nil {
					t.Fatalf("decode tape part: %v", err)
				}
				got.tape = tp
			case "index":
				got.indexType = p.Header.Get("Content-Type")
				if err := json.NewDecoder(p).Decode(&got.index); err != nil {
					t.Fatalf("decode index part: %v", err)
				}
			case "private":
				b, _ := io.ReadAll(p)
				if string(b) != "true" {
					t.Errorf("private part = %q, want \"true\" or absent", b)
				}
				got.privateSeen = true
			default:
				got.extraParts = append(got.extraParts, p.FormName())
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func viewFixture() (*tape.Tape, Index) {
	tp := &tape.Tape{Summary: *card.Example()}
	tp.Requests = []tape.RequestRecord{{
		Index:  0,
		Prompt: tape.PromptRecord{Messages: []tape.Message{{Role: "user", Content: "hello"}}, Completion: "hi"},
	}}
	view := PublicView(tp, WithText)
	return view, IndexOf(view)
}

func TestUpload(t *testing.T) {
	srv, got := serve(t, http.StatusCreated,
		`{"id":"abc123","url":"https://tape.example/r/abc123","delete_token":"dt_xyz"}`)
	view, idx := viewFixture()

	c := &Client{BaseURL: srv.URL, UserAgent: "toktape/0.2.6"}
	r, err := c.Upload(context.Background(), view, idx, Options{})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if r.ID != "abc123" || r.URL != "https://tape.example/r/abc123" || r.DeleteToken != "dt_xyz" {
		t.Errorf("receipt = %+v", r)
	}
	// An anonymous upload carries no Authorization at all, rather than an
	// empty one: "no token" and "an empty token" are different requests.
	if got.auth != "" {
		t.Errorf("Authorization = %q on an anonymous upload", got.auth)
	}
	if got.userAgent != "toktape/0.2.6" {
		t.Errorf("User-Agent = %q", got.userAgent)
	}
	if got.tapeName != "run"+tape.Ext || got.tapeType != "application/gzip" {
		t.Errorf("tape part = %q / %q, want run%s / application/gzip", got.tapeName, got.tapeType, tape.Ext)
	}
	if got.indexType != "application/json" {
		t.Errorf("index part content type = %q", got.indexType)
	}
	if got.privateSeen {
		t.Error("a public upload sent a private part")
	}
	if len(got.extraParts) != 0 {
		t.Errorf("unexpected parts %v: the Worker's contract is tape, index and private", got.extraParts)
	}

	// The part the Worker stores has to be a run file: it is handed back to
	// downloaders as one, and the whole design rests on the Worker never
	// having to parse it.
	if got.tape == nil || got.tape.Summary.ID != view.Summary.ID {
		t.Fatalf("the tape part did not survive the round trip: %+v", got.tape)
	}
	if got.tape.Summary.Model.Path != "" {
		t.Errorf("the uploaded tape carried a model path: %q", got.tape.Summary.Model.Path)
	}
	if got.index.Schema != IndexSchema {
		t.Errorf("index schema = %d, want %d", got.index.Schema, IndexSchema)
	}
	if got.index.ModelRaw != idx.ModelRaw {
		t.Errorf("index part = %+v, want the row the client derived", got.index)
	}
}

func TestUploadTokenAndPrivate(t *testing.T) {
	srv, got := serve(t, http.StatusCreated, `{"id":"a","url":"https://tape.example/r/a"}`)
	view, idx := viewFixture()

	c := &Client{BaseURL: srv.URL, Token: "tk_live_abc"}
	r, err := c.Upload(context.Background(), view, idx, Options{Private: true})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got.auth != "Bearer tk_live_abc" {
		t.Errorf("Authorization = %q", got.auth)
	}
	if !got.privateSeen {
		t.Error("--private did not reach the server")
	}
	// A token-owned upload is already deletable by its owner, so there is no
	// one-time key to print and none should be invented.
	if r.DeleteToken != "" {
		t.Errorf("DeleteToken = %q on a token-owned upload", r.DeleteToken)
	}
}

func TestUploadFailures(t *testing.T) {
	view, idx := viewFixture()

	t.Run("a refusal is quoted, so the publisher can tell whose problem it is", func(t *testing.T) {
		srv, _ := serve(t, http.StatusTooManyRequests, `{"error":"one upload a minute from an address with no token"}`)
		_, err := (&Client{BaseURL: srv.URL}).Upload(context.Background(), view, idx, Options{})
		if err == nil {
			t.Fatal("a 429 was accepted")
		}
		if !strings.Contains(err.Error(), "one upload a minute") || !strings.Contains(err.Error(), "429") {
			t.Errorf("error = %q, want the status and the server's own words", err)
		}
	})

	t.Run("a success with no link is not a success", func(t *testing.T) {
		srv, _ := serve(t, http.StatusCreated, `{"id":"a"}`)
		_, err := (&Client{BaseURL: srv.URL}).Upload(context.Background(), view, idx, Options{})
		if err == nil || !strings.Contains(err.Error(), "without returning a link") {
			t.Errorf("error = %v, want a complaint about the missing link", err)
		}
	})

	t.Run("an unreadable answer names what happened", func(t *testing.T) {
		srv, _ := serve(t, http.StatusCreated, `<html>`)
		_, err := (&Client{BaseURL: srv.URL}).Upload(context.Background(), view, idx, Options{})
		if err == nil || !strings.Contains(err.Error(), "unreadable") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("nothing to upload", func(t *testing.T) {
		_, err := (&Client{BaseURL: "https://example.invalid"}).Upload(context.Background(), nil, idx, Options{})
		if err == nil {
			t.Fatal("a nil tape was accepted")
		}
	})
}

// The default is the hosted service, so a publish with no --url reaches it
// rather than nothing.
func TestClientDefaultBaseURL(t *testing.T) {
	if (&Client{}).baseURL() != DefaultBaseURL {
		t.Errorf("baseURL() = %q, want %q", (&Client{}).baseURL(), DefaultBaseURL)
	}
	if !strings.HasPrefix(DefaultBaseURL, "https://") {
		t.Errorf("DefaultBaseURL = %q: a tape is uploaded over TLS or not at all", DefaultBaseURL)
	}
}
