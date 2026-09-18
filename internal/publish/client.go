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

	"github.com/midagedev/toktape/internal/card/png"
	"github.com/midagedev/toktape/internal/tape"
)

// DefaultBaseURL is the hosted service (docs/toktape-spec.ko.md §9.1).
const DefaultBaseURL = "https://tape.midagedev.com"

// UploadPath is where a run is posted. The version is in the path rather than
// in a header because a reader of a log or a proxy rule can see it there.
const UploadPath = "/api/v1/runs"

// The contract TTP-115 implements.
//
// The Worker never parses a tape (§9.6), so everything it needs is derived
// here and sent beside the record: the row it indexes and the image it puts
// on the link.
//
//	POST {base}/api/v1/runs
//	Content-Type: multipart/form-data
//	Authorization: Bearer <token>        (only when the machine has one)
//	User-Agent: toktape/<version>
//
//	part "tape"   application/gzip, filename "run.tape" — exactly the bytes
//	              tape.Write puts on disk, so a download is a run file
//	part "index"  application/json — one publish.Index, schema IndexSchema
//	part "card"   image/png, filename "card.png" — the 1200×675 share card,
//	              drawn from the public view so that what the JSON had taken
//	              out is not baked back into the pixels. The server serves it
//	              as the link's preview image and never renders one: drawing
//	              a card needs the whole tape, and this side is the only one
//	              allowed to read it
//	part "private" text/plain, "true" — present only for an unlisted upload
//	part "author"  application/json — present only when a profile is sent:
//	               {"name":"…","link":"…"}; either field may be absent
//	part "avatar"  image/png, filename "avatar.png" — present only with "author";
//	               ≤ 65536 bytes, ≤ 256×256, PNG signature checked by the server
//	part "title"   text/plain — the note's title, present only when given
//	part "note"    text/plain — the note's body, present only when given
//	part "bio"     text/plain — the author's bio, ≤ 600 runes after TrimSpace,
//	               \r\n normalised to \n; present only on an upload with an
//	               Authorization header, and stored on the token, never the run
//
//	PATCH {base}/api/v1/runs/<id>
//	Authorization: Bearer <token>        (the journal token that owns the run)
//	Content-Type: application/json — {"title"?, "note"?, "private"?}; null
//	clears a field, absent leaves it, unknown keys are refused. 200 answers
//	the same shape as /r/<id>.json.
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
	// Author is the profile one publish carries (TTP-125). Nil means no
	// profile travels — the default, and what --no-profile asks for even
	// when the config names one.
	Author *Author
	// Title and Note are the lab-note: what this run was trying, in plain
	// text. Empty means that part is not sent — absent, never an empty
	// part.
	Title string
	Note  string
	// Bio is the user-home bio (TTP-127): plain paragraphs shown on
	// /u/<handle> and stored on the token, never the run. Empty means no
	// bio part is sent. It is sent only on a token-owned upload — Upload
	// drops it when there is no token — because an anonymous run has no
	// home to show it on.
	Bio string
}

// Edit is one owner edit of a run's title, note and visibility (TTP-127).
// A nil field is left alone; a non-nil Title or Note replaces the field,
// and Private replaces the visibility. There is no way to clear a field
// to empty here — an empty title is refused the way an upload refuses it.
type Edit struct {
	Title   *string
	Note    *string
	Private *bool
}

// authorWire is the "author" part's shape. Either field may be absent, so
// both omit when empty — and a profile that is somehow neither is sent as
// {}, which the server reads as "a profile with nothing in it".
type authorWire struct {
	Name string `json:"name,omitempty"`
	Link string `json:"link,omitempty"`
}

// Upload posts one run. view must already be the public view: this sends what
// it is given, and a sanitiser that ran inside the uploader would be a second
// place that decides what is published.
func (c *Client) Upload(ctx context.Context, view *tape.Tape, idx Index, opts Options) (*Receipt, error) {
	if view == nil {
		return nil, fmt.Errorf("publish: nothing to upload")
	}
	body, contentType, err := uploadBody(view, idx, opts, c.Token)
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

// Edit changes one run's title, note and visibility (TTP-127). Only the
// fields e sets travel — nil means leave it — and the run is named by id.
// It needs the journal token that owns the run in c.Token: an anonymous run
// cannot be edited and a delete token cannot edit. Anything but 200 is a
// failure reported verbatim like Upload's, and 200 answers the same shape
// as /r/<id>.json.
func (c *Client) Edit(ctx context.Context, id string, e Edit) (*Receipt, error) {
	body := map[string]any{}
	if e.Title != nil {
		title, err := ValidateTitle(*e.Title)
		if err != nil {
			return nil, err
		}
		body["title"] = title
	}
	if e.Note != nil {
		note, err := ValidateNote(*e.Note)
		if err != nil {
			return nil, err
		}
		body["note"] = note
	}
	if e.Private != nil {
		body["private"] = *e.Private
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("publish: nothing to change: name --title, --note or --private/--public")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}

	base := strings.TrimRight(c.baseURL(), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, base+"/api/v1/runs/"+id, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
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

	if resp.StatusCode != http.StatusOK {
		return nil, editError(base, resp)
	}
	// The answer is the run's own JSON shape — the same object /r/<id>.json
	// serves, with no link in it — so the link is built from the service
	// that was just talked to, the way Upload reads it out of the receipt.
	var got struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&got); err != nil {
		return nil, fmt.Errorf("publish: %s accepted the edit but its answer was unreadable: %w", base, err)
	}
	if got.ID == "" {
		return nil, fmt.Errorf("publish: %s accepted the edit without naming the run", base)
	}
	return &Receipt{ID: got.ID, URL: base + "/r/" + got.ID}, nil
}

// editError quotes a refused edit the way uploadError quotes a refused
// upload: the status and the server's short text body.
func editError(base string, resp *http.Response) error {
	msg := strings.TrimSpace(readShort(resp.Body))
	if msg != "" {
		if len(msg) > 400 {
			msg = msg[:400] + "…"
		}
		return fmt.Errorf("publish: %s refused the edit (%s): %s", base, resp.Status, msg)
	}
	return fmt.Errorf("publish: %s refused the edit (%s)", base, resp.Status)
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
//
// token is the client's journal token, or "" for an anonymous upload. The
// bio part is written only when a token is present: the bio belongs to a
// home page and an anonymous run has none, so it is dropped rather than
// refused and a profile with a bio still publishes anonymously.
func uploadBody(view *tape.Tape, idx Index, opts Options, token string) (body []byte, contentType string, err error) {
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

	// Drawn here rather than sent in, so it cannot be forgotten: a run
	// published without a card is a link that previews as nothing, and
	// nothing about that looks wrong from this side. It is drawn from the
	// view, never the tape it came from — a card rendered from the original
	// would paint the hostname back into an image after the JSON had taken
	// it out.
	w, err = mw.CreatePart(partHeader(`form-data; name="card"; filename="card.png"`, "image/png"))
	if err != nil {
		return nil, "", fmt.Errorf("publish: %w", err)
	}
	if err := png.Encode(w, &view.Summary); err != nil {
		return nil, "", fmt.Errorf("publish: render card: %w", err)
	}

	// The profile and the note travel as their own parts, beside the
	// record — never inside the tape and never in the index row. Each is
	// validated here with the same limits the verbs refused first, because
	// a hand-edited config reaches this function without passing a verb.
	if opts.Author != nil {
		// Either field of the wire object may be absent, so only a field
		// that is set is validated — and a profile that is neither is {}
		// on the wire rather than a refusal.
		var name, link string
		if opts.Author.Name != "" {
			var err error
			if name, err = ValidateAuthorName(opts.Author.Name); err != nil {
				return nil, "", err
			}
		}
		if opts.Author.Link != "" {
			var err error
			if link, err = ValidateAuthorLink(opts.Author.Link); err != nil {
				return nil, "", err
			}
		}
		w, err = mw.CreatePart(partHeader(`form-data; name="author"`, "application/json"))
		if err != nil {
			return nil, "", fmt.Errorf("publish: %w", err)
		}
		if err := json.NewEncoder(w).Encode(authorWire{Name: name, Link: link}); err != nil {
			return nil, "", fmt.Errorf("publish: encode author: %w", err)
		}
		if len(opts.Author.Avatar) > 0 {
			if _, _, err := ValidateAvatarBytes(opts.Author.Avatar); err != nil {
				return nil, "", err
			}
			w, err = mw.CreatePart(partHeader(`form-data; name="avatar"; filename="avatar.png"`, "image/png"))
			if err != nil {
				return nil, "", fmt.Errorf("publish: %w", err)
			}
			if _, err := w.Write(opts.Author.Avatar); err != nil {
				return nil, "", fmt.Errorf("publish: encode avatar: %w", err)
			}
		}
	}
	if opts.Title != "" {
		title, err := ValidateTitle(opts.Title)
		if err != nil {
			return nil, "", err
		}
		w, err = mw.CreatePart(partHeader(`form-data; name="title"`, "text/plain"))
		if err != nil {
			return nil, "", fmt.Errorf("publish: %w", err)
		}
		if _, err := io.WriteString(w, title); err != nil {
			return nil, "", fmt.Errorf("publish: encode title: %w", err)
		}
	}
	if opts.Note != "" {
		note, err := ValidateNote(opts.Note)
		if err != nil {
			return nil, "", err
		}
		w, err = mw.CreatePart(partHeader(`form-data; name="note"`, "text/plain"))
		if err != nil {
			return nil, "", fmt.Errorf("publish: %w", err)
		}
		if _, err := io.WriteString(w, note); err != nil {
			return nil, "", fmt.Errorf("publish: encode note: %w", err)
		}
	}
	// Only with a token (see above), and validated here like every other
	// part, so a hand-edited config cannot smuggle past the verb.
	if opts.Bio != "" && token != "" {
		bio, err := ValidateBio(opts.Bio)
		if err != nil {
			return nil, "", err
		}
		w, err = mw.CreatePart(partHeader(`form-data; name="bio"`, "text/plain"))
		if err != nil {
			return nil, "", fmt.Errorf("publish: %w", err)
		}
		if _, err := io.WriteString(w, bio); err != nil {
			return nil, "", fmt.Errorf("publish: encode bio: %w", err)
		}
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
