package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/jpeg" // registers the decoder DecodeConfig reads sizes with
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"

	_ "golang.org/x/image/webp"

	"github.com/midagedev/toktape/internal/server/images"
	"github.com/midagedev/toktape/internal/tape"
)

// The image input mode (TTP-208, 2026-10-12).
//
// This file is the one owner of what an image is on its way to a server: how a
// file becomes bytes plus a tape.ImageRef, how the bytes ride a request, and
// how a message turns into OpenAI content parts. The bytes live only in memory
// beside the request (StreamRequest.ImageData); the tape records the reference
// and never the pixels.

// MaxImageBytes is the largest image a run will send. A larger file is refused
// by name before the run, because a request that size is a measurement of the
// upload, not of the engine.
const MaxImageBytes = 20 << 20

// Image is one picture ready to send: its bytes and its record.
type Image struct {
	Ref  tape.ImageRef
	Data []byte
}

// LoadImage reads one --image value: "builtin" for the embedded test image,
// otherwise a path to a png, jpeg or webp file. Every refusal names the file.
//
// The format is read from the bytes, not the extension: a gif renamed .png is
// still a gif, and the server would answer it with an error that names
// neither file nor reason.
func LoadImage(spec string) (Image, error) {
	if spec == images.BuiltinName {
		return newImage(images.BuiltinName, images.Builtin())
	}
	name := filepath.Base(spec)
	info, err := os.Stat(spec)
	if err != nil {
		return Image{}, fmt.Errorf("--image %s: %w", name, unwrapPath(err))
	}
	if info.IsDir() {
		return Image{}, fmt.Errorf("--image %s: that is a directory, not an image file", name)
	}
	if info.Size() > MaxImageBytes {
		return Image{}, fmt.Errorf("--image %s: %.1f MB is over the 20 MB limit", name, float64(info.Size())/(1<<20))
	}
	data, err := os.ReadFile(spec)
	if err != nil {
		return Image{}, fmt.Errorf("--image %s: %w", name, unwrapPath(err))
	}
	return newImage(name, data)
}

// unwrapPath drops the path an *os.PathError repeats: the caller's sentence
// already names the file.
func unwrapPath(err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err
	}
	return err
}

func newImage(name string, data []byte) (Image, error) {
	mime := http.DetectContentType(data)
	switch mime {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return Image{}, fmt.Errorf("--image %s: not a png, jpeg or webp image (looks like %s)", name, mime)
	}
	sum := sha256.Sum256(data)
	ref := tape.ImageRef{Name: name, SHA256: hex.EncodeToString(sum[:]), Bytes: len(data), MIME: mime}
	// Dimensions are a nicety: a format DecodeConfig cannot size is still sent
	// and recorded, with 0 for "not decoded", which the card prints as "?".
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		ref.Width, ref.Height = cfg.Width, cfg.Height
	}
	return Image{Ref: ref, Data: data}, nil
}

// AttachImages puts imgs on the request's last user message, in order, and
// keeps their bytes for the wire. A request with no user message gets one that
// asks the fixed question. Messages is replaced, never written through: the
// slice may be shared with the options it was cloned from.
func (r *StreamRequest) AttachImages(imgs ...Image) {
	if len(imgs) == 0 {
		return
	}
	msgs := append([]tape.Message(nil), r.Messages...)
	at := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			at = i
			break
		}
	}
	if at < 0 {
		msgs = append(msgs, tape.Message{Role: "user", Content: images.Question})
		at = len(msgs) - 1
	}
	refs := append([]tape.ImageRef(nil), msgs[at].Images...)
	data := make(map[string][]byte, len(r.ImageData)+len(imgs))
	for k, v := range r.ImageData {
		data[k] = v
	}
	for _, img := range imgs {
		refs = append(refs, img.Ref)
		data[img.Ref.SHA256] = img.Data
	}
	msgs[at].Images = refs
	r.Messages = msgs
	r.ImageData = data
}

// HasImages reports whether any message of the request carries an image.
func (r StreamRequest) HasImages() bool {
	for _, m := range r.Messages {
		if len(m.Images) > 0 {
			return true
		}
	}
	return false
}

// wireMessages is the request's "messages" value. A request with no image
// returns the recorded messages untouched, so its body is byte for byte what
// it was before images existed. A message with images becomes OpenAI content
// parts, the text first and one image_url data: URL per image, which
// llama-server and bloomery both take.
func (r StreamRequest) wireMessages() any {
	if !r.HasImages() {
		return r.Messages
	}
	out := make([]any, len(r.Messages))
	for i, m := range r.Messages {
		if len(m.Images) == 0 {
			out[i] = m
			continue
		}
		parts := []map[string]any{{"type": "text", "text": m.Content}}
		for _, ref := range m.Images {
			data, ok := r.ImageData[ref.SHA256]
			if !ok {
				continue // unreachable through AttachImages; never invent bytes
			}
			parts = append(parts, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": "data:" + ref.MIME + ";base64," + base64.StdEncoding.EncodeToString(data)},
			})
		}
		out[i] = map[string]any{"role": m.Role, "content": parts}
	}
	return out
}

// TextMessages is the messages with their images removed, for the calls that
// take text only (/apply-template): a chat template without an image marker
// would drop the pictures anyway, and the record's references are not part of
// any server's request shape.
func TextMessages(msgs []tape.Message) []tape.Message {
	out := make([]tape.Message, len(msgs))
	for i, m := range msgs {
		m.Images = nil
		out[i] = m
	}
	return out
}
