package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/midagedev/toktape/internal/server/images"
	"github.com/midagedev/toktape/internal/tape"
)

// visionServer is an httptest server for the image mode (TTP-208). /props
// answers with the given modalities fragment ("" leaves the key out, which is
// what an older build or another engine does), every request is counted and
// every chat body is kept, and the reply is the recorded stream with timings.
// Nothing here reaches a real service.
type visionServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests int
	bodies   [][]byte
}

func newVisionServer(t *testing.T, modalities string) *visionServer {
	t.Helper()
	sse, err := os.ReadFile(cliSSE)
	if err != nil {
		t.Fatal(err)
	}
	props := cliProps
	if modalities != "" {
		props = strings.Replace(cliProps, `"total_slots": 4,`, `"total_slots": 4, "modalities": `+modalities+`,`, 1)
	}
	vs := &visionServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "llama.cpp")
		_, _ = w.Write([]byte(props))
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":0,"is_processing":true,"n_ctx":32768,"n_past":12}]`))
	})
	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if bytes.Contains(b, []byte("image_url")) || bytes.Contains(b, []byte("sha256")) {
			http.Error(w, "the template call must carry text only", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"prompt": "<|user|>hi<|assistant|>"})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		vs.mu.Lock()
		vs.bodies = append(vs.bodies, b)
		vs.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, ev := range bytes.SplitAfter(sse, []byte("\n\n")) {
			if len(bytes.TrimSpace(ev)) > 0 {
				_, _ = w.Write(ev)
			}
		}
	})
	vs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vs.mu.Lock()
		vs.requests++
		vs.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(vs.Close)
	return vs
}

func (vs *visionServer) chatBodies() [][]byte {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return append([][]byte(nil), vs.bodies...)
}

func (vs *visionServer) total() int {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return vs.requests
}

// wireMessage is one message of a posted body with its content left raw.
type wireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func messagesOf(t *testing.T, body []byte) []wireMessage {
	t.Helper()
	var b struct {
		Messages []wireMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	return b.Messages
}

// imageURLs returns the data: URLs of a message's image parts, in order.
func imageURLs(t *testing.T, m wireMessage) []string {
	t.Helper()
	var parts []struct {
		Type     string `json:"type"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(m.Content, &parts); err != nil {
		return nil // a plain string content: no image parts
	}
	var out []string
	for _, p := range parts {
		if p.Type == "image_url" {
			out = append(out, p.ImageURL.URL)
		}
	}
	return out
}

func decodeDataURL(t *testing.T, u string) (mime string, data []byte) {
	t.Helper()
	rest, ok := strings.CutPrefix(u, "data:")
	if !ok {
		t.Fatalf("not a data: URL: %.40s", u)
	}
	mime, b64, ok := strings.Cut(rest, ";base64,")
	if !ok {
		t.Fatalf("no ;base64, in %.40s", u)
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	return mime, data
}

// readOnlyTape finds the one tape in dir and reads it.
func readOnlyTape(t *testing.T, dir string) (*tape.Tape, []byte) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*"+tape.Ext))
	if len(files) != 1 {
		t.Fatalf("%d tapes in %s, want 1", len(files), dir)
	}
	tp, err := tape.Read(files[0])
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if zr, err := gzip.NewReader(bytes.NewReader(raw)); err == nil {
		if plain, err := io.ReadAll(zr); err == nil {
			raw = plain
		}
	}
	return tp, raw
}

const (
	testPNG  = "../../internal/server/testdata/image-a.png"
	testJPEG = "../../internal/server/testdata/image-b.jpg"
)

// Gate 1: --image builtin sends one image_url part carrying the embedded PNG,
// records its reference, and keeps the pixels out of the tape.
func TestImageBuiltinSendsThePartAndRecordsOnlyTheReference(t *testing.T) {
	hermetic(t)
	vs := newVisionServer(t, `{"vision":true}`)
	dir := t.TempDir()

	code, _, stderr := exec(t, "--url", vs.URL, "--out", dir, "--n-predict", "64", "--image", "builtin")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	bodies := vs.chatBodies()
	if len(bodies) != 1 {
		t.Fatalf("%d chat requests, want 1", len(bodies))
	}
	msgs := messagesOf(t, bodies[0])
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("messages = %+v", msgs)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(msgs[0].Content, &parts); err != nil || len(parts) != 2 {
		t.Fatalf("content is not two parts: %s (%v)", msgs[0].Content, err)
	}
	if parts[0].Type != "text" || parts[0].Text != images.Question {
		t.Errorf("the text part = %+v, want the fixed question", parts[0])
	}
	urls := imageURLs(t, msgs[0])
	if len(urls) != 1 {
		t.Fatalf("%d image parts, want 1", len(urls))
	}
	mime, data := decodeDataURL(t, urls[0])
	if mime != "image/png" || !bytes.Equal(data, images.Builtin()) {
		t.Errorf("the sent image (%s, %d bytes) is not the embedded PNG", mime, len(data))
	}

	tp, raw := readOnlyTape(t, dir)
	sum := sha256.Sum256(images.Builtin())
	want := tape.ImageRef{Name: "builtin", SHA256: hex.EncodeToString(sum[:]), Bytes: len(images.Builtin()), MIME: "image/png", Width: 640, Height: 480}
	got := tp.Requests[0].Prompt.Messages
	if len(got) != 1 || len(got[0].Images) != 1 || got[0].Images[0] != want {
		t.Fatalf("recorded messages = %+v, want one ref %+v", got, want)
	}
	if got[0].Content != images.Question {
		t.Errorf("recorded text = %q", got[0].Content)
	}
	b64 := base64.StdEncoding.EncodeToString(images.Builtin())
	if bytes.Contains(raw, []byte(b64[100:140])) || bytes.Contains(raw, []byte("data:image")) {
		t.Error("pixels (base64 of the image) are in the tape")
	}
	// The vision answer was yes, so there is nothing to caveat about that.
	for _, w := range tp.Summary.Warnings {
		if strings.Contains(w, "did not say whether") {
			t.Errorf("unexpected vision caveat: %q", w)
		}
	}
	// The summary lists the picture once, for the card.
	if len(tp.Summary.Images) != 1 || tp.Summary.Images[0] != want {
		t.Errorf("summary images = %+v, want [%+v]", tp.Summary.Images, want)
	}
}

// RunSummary.Images is de-duplicated by sha256 across streams, in first-seen
// order (lead, 2026-10-12): two streams sending one picture list it once.
func TestSummaryImagesAreDistinctAndOrdered(t *testing.T) {
	hermetic(t)
	vs := newVisionServer(t, `{"vision":true}`)
	dir := t.TempDir()
	code, _, stderr := exec(t, "--url", vs.URL, "--out", dir, "--n-predict", "64", "--sessions", "2",
		"--image", "builtin", "--image", testPNG)
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	tp, _ := readOnlyTape(t, dir)
	got := tp.Summary.Images
	if len(got) != 2 || got[0].Name != "builtin" || got[1].Name != "image-a.png" {
		t.Errorf("summary images = %+v, want builtin then image-a.png once each", got)
	}
}

// The rendered prompt comes from /apply-template, which takes text only, so a
// run that sent pictures says the recorded text leaves them out (lead,
// 2026-10-12).
func TestRenderedPromptCaveatNamesTheOmittedImages(t *testing.T) {
	hermetic(t)
	vs := newVisionServer(t, `{"vision":true}`)
	dir := t.TempDir()
	code, _, stderr := exec(t, "--url", vs.URL, "--out", dir, "--n-predict", "64", "--image", "builtin", "--image", testPNG)
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	tp, _ := readOnlyTape(t, dir)
	want := "the rendered prompt omits the 2 image(s) the model also saw"
	found := false
	for _, w := range tp.Summary.Warnings {
		found = found || w == want
	}
	if !found {
		t.Errorf("warnings %q lack %q", tp.Summary.Warnings, want)
	}

	dir2 := t.TempDir()
	if code, _, stderr := exec(t, "--url", vs.URL, "--out", dir2, "--n-predict", "64", "--prompt", "hi"); code != exitOK {
		t.Fatalf("text-only exit %d\n%s", code, stderr)
	}
	tp2, _ := readOnlyTape(t, dir2)
	for _, w := range tp2.Summary.Warnings {
		if strings.Contains(w, "omits") {
			t.Errorf("a text-only run carries %q", w)
		}
	}
}

// --prompt text rides with the image; the question is only the default.
func TestImageWithAPromptKeepsThePrompt(t *testing.T) {
	hermetic(t)
	vs := newVisionServer(t, `{"vision":true}`)
	code, _, stderr := exec(t, "--url", vs.URL, "--out", t.TempDir(), "--n-predict", "64", "--image", "builtin", "--prompt", "what colour is the circle?")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if !bytes.Contains(vs.chatBodies()[0], []byte("what colour is the circle?")) {
		t.Errorf("the prompt is not in the body: %s", vs.chatBodies()[0])
	}
}

// Gate 2, at the verb: a run with no image sends the body it always sent.
func TestTextOnlyRunSendsNoImagePart(t *testing.T) {
	hermetic(t)
	vs := newVisionServer(t, "")
	code, _, stderr := exec(t, "--url", vs.URL, "--out", t.TempDir(), "--n-predict", "64", "--prompt", "hi")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	body := string(vs.chatBodies()[0])
	if !strings.Contains(body, `"messages":[{"role":"user","content":"hi"}]`) || strings.Contains(body, "image") {
		t.Errorf("a text-only body changed: %s", body)
	}
}

// Gate 3: a server that says it takes no images is refused before any POST.
func TestImageRefusedWhenTheServerHasNoVision(t *testing.T) {
	hermetic(t)
	vs := newVisionServer(t, `{"vision":false}`)
	code, _, stderr := exec(t, "--url", vs.URL, "--out", t.TempDir(), "--n-predict", "64", "--image", "builtin")
	if code == exitOK {
		t.Fatalf("exit 0, want a refusal\n%s", stderr)
	}
	want := "toktape: the server at " + vs.URL + " takes no images (llama-server: start it with --mmproj <file>)"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr)
	}
	if n := len(vs.chatBodies()); n != 0 {
		t.Errorf("%d chat requests were sent, want none", n)
	}
}

// Gate 4: no modalities in /props means vision is unknown, not no. The image
// is sent, and the run says it was not confirmed.
func TestImageSentAndCaveatedWhenVisionIsNotReported(t *testing.T) {
	hermetic(t)
	vs := newVisionServer(t, "")
	dir := t.TempDir()
	code, _, stderr := exec(t, "--url", vs.URL, "--out", dir, "--n-predict", "64", "--image", "builtin")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if len(vs.chatBodies()) != 1 || len(imageURLs(t, messagesOf(t, vs.chatBodies()[0])[0])) != 1 {
		t.Fatal("the image was not sent")
	}
	tp, _ := readOnlyTape(t, dir)
	found := false
	for _, w := range tp.Summary.Warnings {
		if strings.Contains(w, "did not say whether it takes images") {
			found = true
		}
	}
	if !found {
		t.Errorf("no vision caveat in %q", tp.Summary.Warnings)
	}
}

// Gate 5: two --image flags are two parts in order and two references.
func TestTwoImagesAreTwoPartsInOrder(t *testing.T) {
	hermetic(t)
	vs := newVisionServer(t, `{"vision":true}`)
	dir := t.TempDir()
	code, _, stderr := exec(t, "--url", vs.URL, "--out", dir, "--n-predict", "64", "--image", testPNG, "--image", testJPEG)
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	urls := imageURLs(t, messagesOf(t, vs.chatBodies()[0])[0])
	if len(urls) != 2 {
		t.Fatalf("%d image parts, want 2", len(urls))
	}
	mimeA, dataA := decodeDataURL(t, urls[0])
	mimeB, dataB := decodeDataURL(t, urls[1])
	wantA, _ := os.ReadFile(testPNG)
	wantB, _ := os.ReadFile(testJPEG)
	if mimeA != "image/png" || !bytes.Equal(dataA, wantA) || mimeB != "image/jpeg" || !bytes.Equal(dataB, wantB) {
		t.Error("the parts are not the two files in the order given")
	}
	tp, _ := readOnlyTape(t, dir)
	refs := tp.Requests[0].Prompt.Messages[0].Images
	if len(refs) != 2 || refs[0].Name != "image-a.png" || refs[0].Width != 32 || refs[1].Name != "image-b.jpg" || refs[1].Height != 36 {
		t.Errorf("refs = %+v", refs)
	}
}

// Gate 6: a file the run cannot send is refused by name, before any request.
func TestUnsendableImagesAreRefusedByNameBeforeAnyRequest(t *testing.T) {
	hermetic(t)
	dir := t.TempDir()
	gif := filepath.Join(dir, "anim.gif")
	huge := filepath.Join(dir, "huge.png")
	if err := os.WriteFile(gif, []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(huge, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 21<<20)...), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"anim.gif": gif, "huge.png": huge} {
		vs := newVisionServer(t, `{"vision":true}`)
		code, _, stderr := exec(t, "--url", vs.URL, "--out", t.TempDir(), "--image", path)
		if code == exitOK {
			t.Errorf("%s: exit 0, want a refusal", name)
		}
		if !strings.Contains(stderr, name) {
			t.Errorf("%s: the refusal does not name the file:\n%s", name, stderr)
		}
		if n := vs.total(); n != 0 {
			t.Errorf("%s: %d requests reached the server, want 0", name, n)
		}
	}
}

// Gate 9: a prompts line may name its images, and they ride on that line's
// user message only.
func TestPromptsFileLineCarriesImages(t *testing.T) {
	hermetic(t)
	vs := newVisionServer(t, `{"vision":true}`)
	file := writePrompts(t, `{"name":"pic","prompt":"what is in it?","images":["builtin"]}`+"\n"+
		`{"name":"plain","prompt":"say hi"}`+"\n")
	dir := t.TempDir()
	code, _, stderr := exec(t, "--url", vs.URL, "--out", dir, "--n-predict", "64", "--prompts", file)
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	bodies := vs.chatBodies()
	if len(bodies) != 2 {
		t.Fatalf("%d requests, want 2 (one per round)", len(bodies))
	}
	var withImage, without int
	for _, b := range bodies {
		m := messagesOf(t, b)
		if len(m) != 1 {
			t.Fatalf("messages = %+v", m)
		}
		if urls := imageURLs(t, m[0]); len(urls) == 1 {
			withImage++
			if !bytes.Contains(m[0].Content, []byte("what is in it?")) {
				t.Errorf("the image part lost its text: %s", m[0].Content[:80])
			}
		} else if string(m[0].Content) == `"say hi"` {
			without++
		}
	}
	if withImage != 1 || without != 1 {
		t.Errorf("image round x%d, plain round x%d; want one each", withImage, without)
	}
}

func TestPromptsFileImageErrorsNameTheLine(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"prompt":"a","images":"builtin"}`, "line 1: not a prompt object"},
		{`{"prompt":"a","images":[""]}`, `line 1: "images" has an empty entry`},
		{`{"messages":[{"role":"user","content":"a","images":[{"name":"x"}]}]}`, `line 1: message 1 has "images"`},
	} {
		if _, err := parsePromptsJSONL(strings.NewReader(tc.in)); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want prefix %q", tc.in, err, tc.want)
		}
	}
}

func TestImageAndPromptsFileAreAlternatives(t *testing.T) {
	file := writePrompts(t, `{"prompt":"a"}`+"\n")
	code, _, stderr := exec(t, "--url", "http://127.0.0.1:1", "--image", "builtin", "--prompts", file)
	if code == exitOK || !strings.Contains(stderr, "--image") || !strings.Contains(stderr, "--prompts") {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
}
