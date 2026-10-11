package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/server/images"
	"github.com/midagedev/toktape/internal/tape"
)

// The text-only request body is part of the record of every tape ever made:
// TTP-208 adds image parts and must not move one byte of it. These two strings
// were taken from the code before the change.
const (
	textOnlyLlamaBody  = `{"max_tokens":64,"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hello\n\"q\""}],"model":"m","return_progress":true,"seed":7,"stream":true,"stream_options":{"include_usage":true},"temperature":0,"timings_per_token":true}`
	textOnlyOpenAIBody = `{"max_tokens":64,"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hello\n\"q\""}],"model":"m","seed":7,"stream":true,"stream_options":{"include_usage":true},"temperature":0}`
)

func textOnlyRequest() StreamRequest {
	return StreamRequest{
		Messages:  []tape.Message{{Role: "system", Content: "be brief"}, {Role: "user", Content: "hello\n\"q\""}},
		Model:     "m",
		MaxTokens: 64,
		Params:    map[string]any{"temperature": 0.0, "seed": 7},
	}
}

func TestTextOnlyBodyIsByteIdentical(t *testing.T) {
	r := textOnlyRequest()
	if b, _ := json.Marshal(r.Body()); string(b) != textOnlyLlamaBody {
		t.Errorf("llama body moved:\n got %s\nwant %s", b, textOnlyLlamaBody)
	}
	r.Protocol = tape.ServerOpenAI
	if b, _ := json.Marshal(r.Body()); string(b) != textOnlyOpenAIBody {
		t.Errorf("openai body moved:\n got %s\nwant %s", b, textOnlyOpenAIBody)
	}
}

func TestLoadBuiltinImage(t *testing.T) {
	img, err := LoadImage(images.BuiltinName)
	if err != nil {
		t.Fatal(err)
	}
	png := images.Builtin()
	sum := sha256.Sum256(png)
	want := tape.ImageRef{Name: "builtin", SHA256: hex.EncodeToString(sum[:]), Bytes: len(png), MIME: "image/png", Width: 640, Height: 480}
	if img.Ref != want {
		t.Errorf("ref = %+v, want %+v", img.Ref, want)
	}
	if !bytes.Equal(img.Data, png) {
		t.Error("data is not the embedded PNG")
	}
}

func TestLoadImageFilesAndRefusals(t *testing.T) {
	a, err := LoadImage("testdata/image-a.png")
	if err != nil {
		t.Fatal(err)
	}
	if a.Ref.Name != "image-a.png" || a.Ref.MIME != "image/png" || a.Ref.Width != 32 || a.Ref.Height != 24 {
		t.Errorf("png ref = %+v", a.Ref)
	}
	b, err := LoadImage("testdata/image-b.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if b.Ref.MIME != "image/jpeg" || b.Ref.Width != 48 || b.Ref.Height != 36 {
		t.Errorf("jpeg ref = %+v", b.Ref)
	}

	dir := t.TempDir()
	gif := filepath.Join(dir, "anim.gif")
	if err := os.WriteFile(gif, []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadImage(gif); err == nil || !strings.Contains(err.Error(), "anim.gif") || !strings.Contains(err.Error(), "png, jpeg or webp") {
		t.Errorf("gif: err = %v, want a refusal naming anim.gif and the three formats", err)
	}
	big := filepath.Join(dir, "huge.png")
	if err := os.WriteFile(big, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 21<<20)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadImage(big); err == nil || !strings.Contains(err.Error(), "huge.png") || !strings.Contains(err.Error(), "20 MB") {
		t.Errorf("21 MB: err = %v, want a refusal naming huge.png and the limit", err)
	}
	if _, err := LoadImage(filepath.Join(dir, "nope.png")); err == nil || !strings.Contains(err.Error(), "nope.png") {
		t.Errorf("missing: err = %v, want a refusal naming nope.png", err)
	}
}

// A webp the stdlib family cannot size is still sent and recorded: the
// dimensions are 0, which the card prints as "?".
func TestLoadImageWebPWithoutSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.webp")
	if err := os.WriteFile(path, []byte("RIFF\x04\x00\x00\x00WEBPVP8 "), 0o644); err != nil {
		t.Fatal(err)
	}
	img, err := LoadImage(path)
	if err != nil {
		t.Fatal(err)
	}
	if img.Ref.MIME != "image/webp" || img.Ref.Width != 0 || img.Ref.Height != 0 {
		t.Errorf("ref = %+v", img.Ref)
	}
}

func TestImageBodyHasOneContentPartPerImageInOrder(t *testing.T) {
	a, _ := LoadImage("testdata/image-a.png")
	b, _ := LoadImage("testdata/image-b.jpg")
	r := StreamRequest{Messages: []tape.Message{{Role: "system", Content: "s"}, {Role: "user", Content: "what is this?"}}}
	r.AttachImages(a, b)

	for _, proto := range []tape.ServerKind{"", tape.ServerOpenAI} {
		r.Protocol = proto
		raw, _ := json.Marshal(r.Body())
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
				Images  json.RawMessage `json:"images"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) != 2 {
			t.Fatalf("%q: %d messages", proto, len(body.Messages))
		}
		if string(body.Messages[0].Content) != `"s"` {
			t.Errorf("%q: the system message changed: %s", proto, body.Messages[0].Content)
		}
		if body.Messages[1].Images != nil {
			t.Errorf("%q: the record's images key leaked onto the wire: %s", proto, body.Messages[1].Images)
		}
		var parts []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		if err := json.Unmarshal(body.Messages[1].Content, &parts); err != nil {
			t.Fatalf("%q: content is not parts: %v", proto, err)
		}
		if len(parts) != 3 || parts[0].Type != "text" || parts[0].Text != "what is this?" || parts[1].Type != "image_url" || parts[2].Type != "image_url" {
			t.Fatalf("%q: parts = %+v", proto, parts)
		}
		for i, img := range []Image{a, b} {
			want := "data:" + img.Ref.MIME + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
			if parts[1+i].ImageURL.URL != want {
				t.Errorf("%q: part %d url differs from the file's bytes", proto, i)
			}
		}
	}
}

func TestAttachImagesNeverMutatesTheCallersMessages(t *testing.T) {
	a, _ := LoadImage("testdata/image-a.png")
	msgs := []tape.Message{{Role: "user", Content: "q"}}
	r := StreamRequest{Messages: msgs}
	r.AttachImages(a)
	if len(msgs[0].Images) != 0 {
		t.Error("AttachImages wrote through the shared slice")
	}
	if len(r.Messages[0].Images) != 1 || r.Messages[0].Images[0] != a.Ref {
		t.Errorf("request messages = %+v", r.Messages)
	}
}

// An image with no user message to ride on gets one, with the fixed question.
func TestAttachImagesWithoutAUserMessageAddsOne(t *testing.T) {
	a, _ := LoadImage("testdata/image-a.png")
	r := StreamRequest{}
	r.AttachImages(a)
	if len(r.Messages) != 1 || r.Messages[0].Role != "user" || r.Messages[0].Content != images.Question {
		t.Errorf("messages = %+v", r.Messages)
	}
}

func TestPropsReadsTheVisionModality(t *testing.T) {
	cases := map[string]string{
		`{"modalities":{"vision":true}}`:  "true",
		`{"modalities":{"vision":false}}`: "false",
		`{"modalities":{}}`:               "unknown",
		`{"total_slots":1}`:               "unknown",
	}
	for body, want := range cases {
		var p Props
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatal(err)
		}
		got := "unknown"
		if v, ok := p.Vision(); ok {
			got = map[bool]string{true: "true", false: "false"}[v]
		}
		if got != want {
			t.Errorf("%s: vision = %s, want %s", body, got, want)
		}
	}
	var nilProps *Props
	if _, ok := nilProps.Vision(); ok {
		t.Error("a nil Props reported a vision answer")
	}
}
