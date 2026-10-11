package publish

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// imageTape is fullTape whose one request carried two pictures: the built-in
// one and a file named after its owner.
func imageTape() *tape.Tape {
	tp := fullTape()
	tp.Requests[0].Prompt.Messages = []tape.Message{{
		Role:    "user",
		Content: "what is in these?",
		Images: []tape.ImageRef{
			{Name: "builtin", SHA256: "ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12", Bytes: 7646, MIME: "image/png", Width: 640, Height: 480},
			{Name: "alice-passport.jpg", SHA256: "99aa99aa99aa99aa99aa99aa99aa99aa99aa99aa99aa99aa99aa99aa99aa99aa", Bytes: 51234, MIME: "image/jpeg", Width: 1024, Height: 768},
		},
	}}
	return tp
}

// Gate 8: the references travel, the pixels never exist here, and with
// --no-text a file's name goes while the built-in's stays.
func TestPublicViewKeepsImageRefsAndNeverPixels(t *testing.T) {
	in := imageTape()
	out := PublicView(in, WithText)
	got := out.Requests[0].Prompt.Messages[0].Images
	if len(got) != 2 || got[0] != in.Requests[0].Prompt.Messages[0].Images[0] || got[1] != in.Requests[0].Prompt.Messages[0].Images[1] {
		t.Errorf("with text, refs = %+v", got)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "base64") || strings.Contains(string(raw), "data:image") {
		t.Error("pixels in the public view")
	}
}

func TestPublicViewNoTextKeepsRefsAndCutsAFileName(t *testing.T) {
	in := imageTape()
	out := PublicView(in, WithoutText)
	msgs := out.Requests[0].Prompt.Messages
	if len(msgs) != 1 || msgs[0].Content != "" {
		t.Fatalf("the text must go and the carrier stay: %+v", msgs)
	}
	got := msgs[0].Images
	if len(got) != 2 {
		t.Fatalf("refs = %+v", got)
	}
	if got[0].Name != "builtin" {
		t.Errorf("builtin renamed to %q", got[0].Name)
	}
	if got[1].Name != "image" {
		t.Errorf("a file name travelled: %q", got[1].Name)
	}
	want := in.Requests[0].Prompt.Messages[0].Images[1]
	want.Name = "image"
	if got[1] != want {
		t.Errorf("only the name may change: %+v vs %+v", got[1], want)
	}
	// The summary's list for the card follows the same rule.
	in.Summary.Images = append([]tape.ImageRef(nil), in.Requests[0].Prompt.Messages[0].Images...)
	if sum := PublicView(in, WithoutText).Summary.Images; len(sum) != 2 || sum[0].Name != "builtin" || sum[1].Name != "image" {
		t.Errorf("summary images = %+v", sum)
	}
	// The input is not touched.
	if in.Requests[0].Prompt.Messages[0].Images[1].Name != "alice-passport.jpg" {
		t.Error("PublicView wrote through to the recorded tape")
	}
	if strings.Contains(string(mustJSON(t, out)), "alice-passport") {
		t.Error("the file name is still in the view")
	}
}

func TestNoTextWithoutImagesStillDropsEveryMessage(t *testing.T) {
	out := PublicView(fullTape(), WithoutText)
	if out.Requests[0].Prompt.Messages != nil {
		t.Errorf("messages = %+v, want none", out.Requests[0].Prompt.Messages)
	}
}

func TestPreviewListsTheImagesAndSaysNoPixels(t *testing.T) {
	out := previewOf(t, imageTape(), Options{})
	if !strings.Contains(out, "2 images · 640×480, 1024×768 (sha256 ab12…, 99aa…), no pixels") {
		t.Errorf("no images line:\n%s", out)
	}
	one := imageTape()
	one.Requests[0].Prompt.Messages[0].Images = one.Requests[0].Prompt.Messages[0].Images[:1]
	if out := previewOf(t, one, Options{}); !strings.Contains(out, "1 × 640×480 PNG (sha256 ab12…), no pixels") {
		t.Errorf("no single-image line:\n%s", out)
	}
	if out := previewOf(t, fullTape(), Options{}); strings.Contains(out, "no pixels") {
		t.Errorf("a text-only run lists images:\n%s", out)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
