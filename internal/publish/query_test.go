package publish

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serveQuery records what the client asked and answers one canned listing.
type serveQuery struct {
	method string
	path   string
	query  string
	auth   string
	accept string
}

func queryServer(t *testing.T, got *serveQuery, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		got.query = r.URL.RawQuery
		got.auth = r.Header.Get("Authorization")
		got.accept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func queryHas(query, key, want string) bool {
	for _, kv := range strings.Split(query, "&") {
		if kv == key+"="+want {
			return true
		}
	}
	return false
}

// TestListQueryStrings: every field of ListQuery produces its exact query
// parameter, and nothing unset travels.
func TestListQueryStrings(t *testing.T) {
	full := ListQuery{
		Text: "llama", Model: "llama-3", Repo: "bartowski/repo",
		Quant: "q4_k_m", Engine: "llama.cpp", GPU: "rtx-4090",
		Host: "desktop", OS: "linux", Set: "sql",
		Sessions: 4, MinVRAMGB: 24, Sort: "decode",
		Limit: 10, Cursor: "abc",
	}
	for _, c := range []struct {
		name   string
		query  ListQuery
		want   []string // each "k=v" present in the raw query
		absent []string // each "k=" prefix absent from the raw query
	}{
		{"empty sends nothing", ListQuery{}, nil, []string{"q=", "model=", "sort=", "scope=", "limit=", "cursor="}},
		{"text", ListQuery{Text: "llama 3"}, []string{"q=llama+3"}, nil},
		{"newest travels as nothing", ListQuery{Sort: "newest"}, nil, []string{"sort="}},
		{"empty sort travels as nothing", ListQuery{}, nil, []string{"sort="}},
		{"oldest travels", ListQuery{Sort: "oldest"}, []string{"sort=oldest"}, nil},
		{"decode travels", ListQuery{Sort: "decode"}, []string{"sort=decode"}, nil},
		{"mine scopes", ListQuery{Mine: true}, []string{"scope=mine"}, nil},
		{
			"full",
			full,
			[]string{
				"q=llama", "model=llama-3", "repo=bartowski%2Frepo",
				"quant=q4_k_m", "engine=llama.cpp", "gpu=rtx-4090",
				"host=desktop", "os=linux", "set=sql",
				"sessions=4", "min_vram=24", "sort=decode",
				"limit=10", "cursor=abc",
			},
			[]string{"scope="},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got serveQuery
			srv := queryServer(t, &got, `{"scope":"public","runs":[],"next":null}`, 200)
			defer srv.Close()
			client := &Client{BaseURL: srv.URL, Token: "tk_test"}
			if _, err := client.List(context.Background(), c.query); err != nil {
				t.Fatalf("List: %v", err)
			}
			if got.path != "/api/v1/runs" {
				t.Errorf("path = %q, want /api/v1/runs", got.path)
			}
			for _, w := range c.want {
				kv := strings.SplitN(w, "=", 2)
				if !queryHas(got.query, kv[0], kv[1]) {
					t.Errorf("query %q lacks %q", got.query, w)
				}
			}
			for _, a := range c.absent {
				for _, kv := range strings.Split(got.query, "&") {
					if strings.HasPrefix(kv, a) {
						t.Errorf("query %q carries unset %q", got.query, kv)
					}
				}
			}
		})
	}
}

// TestListMineSendsBearer: scope=mine authorises with the journal token.
func TestListMineSendsBearer(t *testing.T) {
	var got serveQuery
	srv := queryServer(t, &got, `{"scope":"mine","runs":[],"next":null}`, 200)
	defer srv.Close()
	client := &Client{BaseURL: srv.URL, Token: "tk_live_abc"}
	out, err := client.List(context.Background(), ListQuery{Mine: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got.auth != "Bearer tk_live_abc" {
		t.Errorf("Authorization = %q, want the journal token", got.auth)
	}
	if out.Scope != "mine" {
		t.Errorf("Scope = %q, want mine", out.Scope)
	}
}

// TestListMineWithoutToken: the refusal happens before any request —
// there is no server to ask, so the test points at one that would fail
// the test if reached.
func TestListMineWithoutToken(t *testing.T) {
	client := &Client{BaseURL: "http://127.0.0.1:1"}
	if _, err := client.List(context.Background(), ListQuery{Mine: true}); err == nil {
		t.Fatal("List with --mine and no token succeeded")
	} else if !strings.Contains(err.Error(), "--mine needs a journal token") {
		t.Errorf("error = %q, want the token sentence", err)
	}
}

// TestListUser: a handle reads /u/<handle>.json with the profile beside
// the rows.
func TestListUser(t *testing.T) {
	var got serveQuery
	body := `{"handle":"labrat","name":"Lab Rat","link":"https://example.com","bio":"line one\nline two",` +
		`"runs":[{"id":"abc","decode_per_sec":24.2,"caveat_count":0}],"next":"cur1"}`
	srv := queryServer(t, &got, body, 200)
	defer srv.Close()
	client := &Client{BaseURL: srv.URL}
	out, err := client.List(context.Background(), ListQuery{User: "labrat", Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got.path != "/u/labrat.json" {
		t.Errorf("path = %q, want /u/labrat.json", got.path)
	}
	if !queryHas(got.query, "limit", "10") {
		t.Errorf("query %q lacks limit=10", got.query)
	}
	if out.Handle != "labrat" || out.Name != "Lab Rat" || out.Bio != "line one\nline two" {
		t.Errorf("profile = %+v, want the served one", out)
	}
	if len(out.Runs) != 1 || out.Next != "cur1" {
		t.Fatalf("runs/next = %+v", out)
	}
	if out.Runs[0].DecodePerS == nil || *out.Runs[0].DecodePerS != 24.2 {
		t.Errorf("decode = %+v, want 24.2", out.Runs[0].DecodePerS)
	}
	if out.Runs[0].CaveatCount == nil || *out.Runs[0].CaveatCount != 0 {
		t.Errorf("caveats = %+v, want a known 0", out.Runs[0].CaveatCount)
	}
	// Unknown stays unknown: no decode, no caveats, no model.
	var got2 serveQuery
	srv2 := queryServer(t, &got2, `{"scope":"public","runs":[{"id":"x"}]`+",\"next\":null}", 200)
	defer srv2.Close()
	out2, err := (&Client{BaseURL: srv2.URL}).List(context.Background(), ListQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if out2.Runs[0].DecodePerS != nil || out2.Runs[0].CaveatCount != nil || out2.Runs[0].ModelID != "" {
		t.Errorf("unknowns = %+v, want nils", out2.Runs[0])
	}
}

// TestListRefusal: a 401's sentence surfaces in the error, in Edit's voice.
func TestListRefusal(t *testing.T) {
	var got serveQuery
	srv := queryServer(t, &got, "the journal scope is one token's own runs", 401)
	defer srv.Close()
	client := &Client{BaseURL: srv.URL}
	_, err := client.List(context.Background(), ListQuery{Mine: true, Limit: 5})
	if err == nil {
		// Mine without a token fails before the request; give it a token so
		// the refusal is the server's.
		t.Fatal("expected the pre-request token error")
	}
	client.Token = "tk_bad"
	_, err = client.List(context.Background(), ListQuery{Mine: true})
	if err == nil {
		t.Fatal("401 succeeded")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "journal scope") {
		t.Errorf("error = %q, want the status and the server's line", err)
	}
}

// TestRunDetail: the run JSON decodes into the client's own Index with the
// schema and caveats beside it, and the author/title/note beside that.
func TestRunDetail(t *testing.T) {
	body := `{"id":"abc","published_at":"2026-09-19 07:04:58","private":false,` +
		`"tape":"/r/abc.tape","tape_bytes":25600,"card":"/r/abc.png","owned":true,` +
		`"index":{"schema":1,"model_id":"llama-3","quant_raw":"Q4_K_M","quant_bits":4.5,` +
		`"decode_per_sec":24.232,"caveats":["cold_cache"],"caveat_count":1},` +
		`"author":{"name":"Lab Rat","link":"https://example.com"},"title":"smoke","note":"p1\n\np2"}`
	var got serveQuery
	srv := queryServer(t, &got, body, 200)
	defer srv.Close()
	out, err := (&Client{BaseURL: srv.URL}).Run(context.Background(), "abc")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.path != "/r/abc.json" {
		t.Errorf("path = %q, want /r/abc.json", got.path)
	}
	if out.Index.ModelID != "llama-3" || out.Schema != 1 {
		t.Errorf("index = %+v, want the served one", out.Index)
	}
	if len(out.Caveats) != 1 || out.Caveats[0] != "cold_cache" {
		t.Errorf("caveats = %q", out.Caveats)
	}
	if out.Author == nil || out.Author.Name != "Lab Rat" {
		t.Errorf("author = %+v", out.Author)
	}
	if out.Title != "smoke" || out.Note != "p1\n\np2" || out.Tape != "/r/abc.tape" || out.Card != "/r/abc.png" {
		t.Errorf("detail = %+v", out)
	}
	if string(out.Raw) != body {
		t.Error("Raw is not the served body verbatim")
	}
}

// TestRunID: the four spellings land on the id, and anything else is refused.
func TestRunID(t *testing.T) {
	id := "3haj592rjrbdu7vg8hpb"
	for _, s := range []string{
		id,
		"https://tape.midagedev.com/r/" + id,
		"https://tape.midagedev.com/r/" + id + ".json",
		"https://tape.midagedev.com/r/" + id + ".tape",
		"https://tape.midagedev.com/r/" + id + ".png",
		"http://localhost:8787/r/" + id + ".json",
	} {
		if got, err := RunID(s); err != nil || got != id {
			t.Errorf("RunID(%q) = %q, %v; want %q", s, got, err, id)
		}
	}
	for _, s := range []string{
		"",
		"https://tape.midagedev.com/u/labrat",
		"https://tape.midagedev.com/r/",
		"https://tape.midagedev.com/r/a/b",
		"ftp://tape.midagedev.com/r/" + id,
		"not an id!!",
		"abc/def",
	} {
		if got, err := RunID(s); err == nil {
			t.Errorf("RunID(%q) = %q, want a refusal", s, got)
		}
	}
}

// TestDownload: the bytes land in the writer and the count comes back.
func TestDownload(t *testing.T) {
	want := bytes.Repeat([]byte{0x1f, 0x8b, 0x08, 0x00}, 512)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/r/abc.tape" {
			t.Errorf("path = %q, want /r/abc.tape", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(want)
	}))
	defer srv.Close()
	var buf bytes.Buffer
	n, err := (&Client{BaseURL: srv.URL}).Download(context.Background(), "abc", &buf)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if n != int64(len(want)) || !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("n = %d, bytes match = %v; want %d and a match", n, bytes.Equal(buf.Bytes(), want), len(want))
	}
}

// TestDownloadRefusal: a 404 writes nothing to the file-to-be.
func TestDownloadRefusal(t *testing.T) {
	var got serveQuery
	srv := queryServer(t, &got, "no run with that id", 404)
	defer srv.Close()
	var buf bytes.Buffer
	if _, err := (&Client{BaseURL: srv.URL}).Download(context.Background(), "nope", &buf); err == nil {
		t.Fatal("404 succeeded")
	} else if !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "no run with that id") {
		t.Errorf("error = %q, want the status and the server's line", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d refusal bytes to the download", buf.Len())
	}
}
