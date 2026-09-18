package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// DefaultBaseURL is the hosted service (docs/toktape-spec.ko.md §9.1).
const DefaultBaseURL = "https://tape.midagedev.com"

// UploadPath is where a run is posted. The version is in the path rather than
// in a header because a reader of a log or a proxy rule can see it there.
const UploadPath = "/api/v1/runs"

// The contract TTP-115 implements.
//
// The Worker never parses a tape (§9.6). This client sends two parts and the
// server stores the first and indexes the second:
//
//	POST {base}/api/v1/runs
//	Content-Type: multipart/form-data
//	Authorization: Bearer <token>        (only when the machine has one)
//	User-Agent: toktape/<version>
//
//	part "tape"   application/gzip, filename "run.tape" — exactly the bytes
//	              tape.Write puts on disk, so a download is a run file
//	part "index"  application/json — one publish.Index, schema IndexSchema
//	part "private" text/plain, "true" — present only for an unlisted upload
//
//	201 Created, application/json:
//	  {"id":"...","url":"https://.../r/<id>","delete_token":"..."}
//
// delete_token comes back only for an upload with no Authorization: a
// token-owned upload is already deletable by its owner (§9.2), and an
// anonymous one needs a key that is printed once and never stored.
//
// Anything but 201 is a failure the client reports verbatim. The server's
// body is shown when it is short and looks like text, because the first
// thing a publisher needs to know is whether the refusal was theirs — a rate
// limit, a tape too large — or the service's.
type Client struct {
	// BaseURL defaults to DefaultBaseURL when empty.
	BaseURL string
	// Token authorises the upload into a journal namespace. Empty is an
	// anonymous upload, which is a supported path and not a degraded one.
	Token string
	// UserAgent identifies the build. The service uses it to tell which
	// client versions are still in the field before changing IndexSchema.
	UserAgent string
	// HTTP is the transport. Nil means a client with a timeout: a publish
	// that hangs forever on a captive portal is the worst of the failures
	// here, because the user cannot tell it from a slow upload.
	HTTP *http.Client
}

// Receipt is what a successful upload returns.
type Receipt struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	// DeleteToken is the only key to an anonymous upload. It is printed once
	// and never written to the config file: storing it would make the config
	// a list of everything this machine ever posted.
	DeleteToken string `json:"delete_token,omitempty"`
}

// Options are the per-upload choices the CLI's flags resolve to.
type Options struct {
	// Text says whether the prompts and the generated text travel. The
	// default is that they do (§9.3).
	Text TextPolicy
	// Private keeps the run out of the search. It is still readable by
	// anyone with the link — an unguessable URL, not a secret.
	Private bool
}

// Upload posts one run. view must already be the public view: this sends what
// it is given, and a sanitiser that ran inside the uploader would be a second
// place that decides what is published.
func (c *Client) Upload(ctx context.Context, view *tape.Tape, idx Index, opts Options) (*Receipt, error) {
	if view == nil {
		return nil, fmt.Errorf("publish: nothing to upload")
	}
	body, contentType, err := uploadBody(view, idx, opts)
	if err != nil {
		return nil, err
	}

	base := strings.TrimRight(c.baseURL(), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+UploadPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("publish: %s: %w", base, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, uploadError(base, resp)
	}
	var r Receipt
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return nil, fmt.Errorf("publish: %s accepted the run but its answer was unreadable: %w", base, err)
	}
	if r.URL == "" {
		return nil, fmt.Errorf("publish: %s accepted the run without returning a link", base)
	}
	return &r, nil
}

func (c *Client) baseURL() string {
	if c.BaseURL == "" {
		return DefaultBaseURL
	}
	return c.BaseURL
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	// Long enough for a tape over a slow uplink, short enough that a captive
	// portal stops looking like an upload in progress.
	return &http.Client{Timeout: 2 * time.Minute}
}

// uploadBody builds the multipart body. The whole thing is assembled in
// memory on purpose: a tape is tens of kilobytes (the hero is 25 KB on disk),
// and a streaming body would cost a retry the ability to be a retry.
func uploadBody(view *tape.Tape, idx Index, opts Options) (body []byte, contentType string, err error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	w, err := mw.CreatePart(partHeader(`form-data; name="tape"; filename="run`+tape.Ext+`"`, "application/gzip"))
	if err != nil {
		return nil, "", fmt.Errorf("publish: %w", err)
	}
	if err := tape.Encode(w, view); err != nil {
		return nil, "", fmt.Errorf("publish: encode tape: %w", err)
	}

	w, err = mw.CreatePart(partHeader(`form-data; name="index"`, "application/json"))
	if err != nil {
		return nil, "", fmt.Errorf("publish: %w", err)
	}
	if err := json.NewEncoder(w).Encode(idx); err != nil {
		return nil, "", fmt.Errorf("publish: encode index: %w", err)
	}

	// Present or absent, never "false": the server reads absence as public,
	// which is the default the whole design turns on (§9.3).
	if opts.Private {
		if err := mw.WriteField("private", "true"); err != nil {
			return nil, "", fmt.Errorf("publish: %w", err)
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", fmt.Errorf("publish: %w", err)
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

func partHeader(disposition, contentType string) textproto.MIMEHeader {
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", disposition)
	h.Set("Content-Type", contentType)
	return h
}

// uploadError turns a refusal into a sentence a publisher can act on. The
// first thing they need to know is whose problem it is, so the status line
// and a short text body are both quoted rather than summarised.
func uploadError(base string, resp *http.Response) error {
	msg := strings.TrimSpace(readShort(resp.Body))
	// A server's own JSON error is passed through as it is: its wording is
	// more specific than anything that could be written here.
	if msg != "" {
		if len(msg) > 400 {
			msg = msg[:400] + "…"
		}
		return fmt.Errorf("publish: %s refused the run (%s): %s", base, resp.Status, msg)
	}
	return fmt.Errorf("publish: %s refused the run (%s)", base, resp.Status)
}

func readShort(r io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(r, 4<<10))
	if err != nil {
		return ""
	}
	if !isText(b) {
		return ""
	}
	return string(b)
}

// isText keeps a binary error page out of the terminal.
func isText(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return false
		}
	}
	return true
}
