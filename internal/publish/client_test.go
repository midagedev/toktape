package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	_ "image/png"
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
	card        []byte
	cardName    string
	cardType    string
	privateSeen bool
	author      map[string]string
	authorType  string
	avatar      []byte
	avatarName  string
	avatarType  string
	title       string
	titleType   string
	note        string
	noteType    string
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
			case "card":
				got.cardName, got.cardType = p.FileName(), p.Header.Get("Content-Type")
				got.card, _ = io.ReadAll(p)
			case "private":
				b, _ := io.ReadAll(p)
				if string(b) != "true" {
					t.Errorf("private part = %q, want \"true\" or absent", b)
				}
				got.privateSeen = true
			case "author":
				got.authorType = p.Header.Get("Content-Type")
				if err := json.NewDecoder(p).Decode(&got.author); err != nil {
					t.Fatalf("decode author part: %v", err)
				}
			case "avatar":
				got.avatarName, got.avatarType = p.FileName(), p.Header.Get("Content-Type")
				got.avatar, _ = io.ReadAll(p)
			case "title":
				got.titleType = p.Header.Get("Content-Type")
				b, _ := io.ReadAll(p)
				got.title = string(b)
			case "note":
				got.noteType = p.Header.Get("Content-Type")
				b, _ := io.ReadAll(p)
				got.note = string(b)
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
		t.Errorf("unexpected parts %v: the Worker's contract is tape, index, card and private", got.extraParts)
	}

	// The card is what the link previews as. The server cannot draw one —
	// drawing it needs the whole tape — so an upload without this part is a
	// run that shows up as a blank rectangle wherever it is posted.
	if got.cardName != "card.png" || got.cardType != "image/png" {
		t.Errorf("card part = %q / %q, want card.png / image/png", got.cardName, got.cardType)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(got.card))
	if err != nil {
		t.Fatalf("the card part is not an image: %v", err)
	}
	// 1200x675: X and Reddit crop an OpenGraph large image to exactly 16:9,
	// which is why internal/card/png fixes the canvas (see its doc comment).
	if cfg.Width != 1200 || cfg.Height != 675 {
		t.Errorf("card is %dx%d, want 1200x675 — the size the preview crops to", cfg.Width, cfg.Height)
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

// The profile and the note travel as their own parts: author JSON with
// either field omittable, the avatar bytes unchanged beside it, the title
// and the body as text. The Worker parses the body the same way serve()
// does above, so this is where the shape is pinned.
func TestUploadAuthorAndNote(t *testing.T) {
	srv, got := serve(t, http.StatusCreated, `{"id":"a","url":"https://tape.example/r/a"}`)
	view, idx := viewFixture()
	avatar := tinyPNG(t, 16, 16)

	_, err := (&Client{BaseURL: srv.URL}).Upload(context.Background(), view, idx, Options{
		Author: &Author{Name: "Lab Rat", Link: "https://github.com/example", Avatar: avatar},
		Title:  "First ik_llama sweep",
		Note:   "Trying -fa on.\n\nSecond paragraph.",
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if got.authorType != "application/json" {
		t.Errorf("author part content type = %q, want application/json", got.authorType)
	}
	if got.author["name"] != "Lab Rat" || got.author["link"] != "https://github.com/example" {
		t.Errorf("author part = %v, want the name and the link", got.author)
	}
	if got.avatarName != "avatar.png" || got.avatarType != "image/png" {
		t.Errorf("avatar part = %q / %q, want avatar.png / image/png", got.avatarName, got.avatarType)
	}
	if !bytes.Equal(got.avatar, avatar) {
		t.Error("the avatar bytes did not survive: they must travel unchanged, never re-encoded")
	}
	if got.titleType != "text/plain" || got.title != "First ik_llama sweep" {
		t.Errorf("title part = %q / %q", got.title, got.titleType)
	}
	if got.noteType != "text/plain" || got.note != "Trying -fa on.\n\nSecond paragraph." {
		t.Errorf("note part = %q / %q", got.note, got.noteType)
	}
	if len(got.extraParts) != 0 {
		t.Errorf("unexpected parts %v", got.extraParts)
	}
}

// Absent, never empty: a run without a profile or a note sends no such
// parts, and an old server that never heard of them reads the rest the way
// it always did.
func TestUploadOmitsProfileAndNote(t *testing.T) {
	srv, got := serve(t, http.StatusCreated, `{"id":"a","url":"https://tape.example/r/a"}`)
	view, idx := viewFixture()

	if _, err := (&Client{BaseURL: srv.URL}).Upload(context.Background(), view, idx, Options{}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got.author != nil || got.avatar != nil || got.title != "" || got.note != "" {
		t.Errorf("a plain upload carried author=%v avatar=%dB title=%q note=%q",
			got.author, len(got.avatar), got.title, got.note)
	}
}

// The client refuses the contract's limits before sending, with the limit
// named — a hand-edited config reaches uploadBody without passing a verb.
func TestUploadRefusesBadProfile(t *testing.T) {
	view, idx := viewFixture()
	srv, _ := serve(t, http.StatusCreated, `{"id":"a","url":"https://tape.example/r/a"}`)
	c := &Client{BaseURL: srv.URL}

	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{"name too long", Options{Author: &Author{Name: strings.Repeat("n", 41)}}, "41 runes, over the 40-rune limit"},
		{"bad link scheme", Options{Author: &Author{Link: "javascript:alert(1)"}}, `"javascript:"`},
		{"bad avatar", Options{Author: &Author{Name: "x", Avatar: []byte("nope")}}, "not a PNG"},
		{"title too long", Options{Title: strings.Repeat("t", 121)}, "121 runes, over the 120-rune limit"},
		{"note too long", Options{Note: strings.Repeat("n", 4001)}, "4001 runes, over the 4000-rune limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.Upload(context.Background(), view, idx, tc.opts); err == nil {
				t.Fatalf("Upload accepted %s", tc.name)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to name %q", err, tc.want)
			}
		})
	}
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
