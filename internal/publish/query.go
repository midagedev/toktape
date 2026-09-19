package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// ListQuery is one search over the published runs. Only the fields that are
// set travel: every filter is optional on the server, and a request that
// names nothing is the front page. Sort travels only when it is not "" or
// "newest", so a reader against an older server sends byte-for-byte what it
// sent before sort existed — newest first being the only order there.
type ListQuery struct {
	// Text is the free-text query (q).
	Text string
	// Model, Repo, Quant, Engine, GPU, Host, OS narrow on one axis each.
	// Model matches the normalised id, Repo the exact repository.
	Model, Repo, Quant, Engine, GPU, Host, OS string
	// Set is the prompt set id.
	Set string
	// Sessions narrows on the stream count; MinVRAMGB is a VRAM floor in
	// gigabytes, the unit the service's own box offers.
	Sessions  int
	MinVRAMGB int
	// Size is the active-params band's lower bound in billions — one of
	// 0, 4, 10, 35, 100, the Worker's own table (search.js SIZE_BANDS); MinPredicted is a floor on the fewest tokens
	// any stream generated. 0 and "" both mean absent and travel as nothing.
	Size         string
	MinPredicted int
	// Sort is "", "newest", "oldest" or "decode". "" and "newest" both
	// travel as nothing: newest first is the server's default order.
	Sort string
	// Limit caps the page; 0 means the server's default. Cursor continues
	// a page the server cut with its next cursor.
	Limit  int
	Cursor string
	// Mine reads scope=mine: the token's own runs, public and unlisted.
	// It needs c.Token and fails before any request without one.
	Mine bool
	// User is a handle whose home page is read instead
	// (/u/<handle>.json): that token's public runs with its profile.
	User string
}

// AuthorProfile is the author as the read API prints it: each part present
// only when set. It is separate from Author, which is the per-machine
// profile a publish carries — one is read, the other is written, and the
// two have different field names on the wire.
type AuthorProfile struct {
	Name   string `json:"name,omitempty"`
	Link   string `json:"link,omitempty"`
	Avatar string `json:"avatar,omitempty"`
}

// Row is one published run as the search prints it. Numbers are pointers
// because unknown is null or absent on the wire and 0 can be a measured
// fact: caveat_count 0 means "no caveats" while absent means "not known"
// (web/src/row.js keeps the same rule). Strings are "" when unknown.
type Row struct {
	ID          string `json:"id"`
	URL         string `json:"url,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	Owned       bool   `json:"owned,omitempty"`
	RecordedAt  string `json:"recorded_at,omitempty"`
	ModelID     string `json:"model_id,omitempty"`
	ModelRaw    string `json:"model_raw,omitempty"`
	Repo        string `json:"repo,omitempty"`
	QuantID     string `json:"quant_id,omitempty"`
	QuantRaw    string `json:"quant_raw,omitempty"`
	EngineKind  string `json:"engine_kind,omitempty"`
	EngineVer   string `json:"engine_version,omitempty"`
	OS          string `json:"os,omitempty"`
	GPUID       string `json:"gpu_id,omitempty"`
	// GPUsRaw is the one " / "-joined string the API serves, not the
	// client's []string: the join already happened server-side.
	GPUsRaw     string   `json:"gpus_raw,omitempty"`
	GPUCount    *int     `json:"gpu_count,omitempty"`
	VRAMBytes   *int64   `json:"vram_bytes,omitempty"`
	HostClass   string   `json:"host_class,omitempty"`
	Sessions    *int     `json:"sessions,omitempty"`
	PromptSet   string   `json:"prompt_set,omitempty"`
	DecodePerS  *float64 `json:"decode_per_sec,omitempty"`
	PrefillPerS *float64 `json:"prefill_per_sec,omitempty"`
	TTFTp50Ms   *float64 `json:"ttft_p50_ms,omitempty"`
	QuantBits   *float64 `json:"quant_bits,omitempty"`
	// The figures behind a rate (TTP-130): the same JSON names the index
	// carries, read here as pointers so unknown stays null or absent.
	PromptN       *int           `json:"prompt_n,omitempty"`
	PredictedN    *int           `json:"predicted_n,omitempty"`
	MinPredictedN *int           `json:"min_predicted_n,omitempty"`
	ReasoningN    *int           `json:"reasoning_n,omitempty"`
	CtxSize       *int           `json:"ctx_size,omitempty"`
	NSlots        *int           `json:"n_slots,omitempty"`
	NExperts      *int           `json:"n_experts,omitempty"`
	NExpertsUsed  *int           `json:"n_experts_used,omitempty"`
	CacheHitRatio *float64       `json:"cache_hit_ratio,omitempty"`
	DraftAccept   *float64       `json:"draft_accept,omitempty"`
	PowerW        *float64       `json:"power_w,omitempty"`
	PowerLimitW   *float64       `json:"power_limit_w,omitempty"`
	FileBytes     *int64         `json:"file_bytes,omitempty"`
	ActiveParams  *int64         `json:"active_params,omitempty"`
	Params        *int64         `json:"params,omitempty"`
	FA            string         `json:"fa,omitempty"`
	KVCache       string         `json:"kv_cache,omitempty"`
	Batch         string         `json:"batch,omitempty"`
	UBatch        string         `json:"ubatch,omitempty"`
	NGL           string         `json:"ngl,omitempty"`
	Offload       string         `json:"offload,omitempty"`
	DraftModel    string         `json:"draft_model,omitempty"`
	Throttled     *bool          `json:"throttled,omitempty"`
	Cold          *bool          `json:"cold,omitempty"`
	MoE           *bool          `json:"moe,omitempty"`
	CaveatCount   *int           `json:"caveat_count,omitempty"`
	Author        *AuthorProfile `json:"author,omitempty"`
	Title         string         `json:"title,omitempty"`
	Note          string         `json:"note,omitempty"`
}

// Listing is one page of the search, or one page of a user home. The
// user-home fields travel only on /u/<handle>.json; the search answers
// scope instead ("public" or "mine").
type Listing struct {
	Scope string `json:"scope,omitempty"`
	Runs  []Row  `json:"runs"`
	Next  string `json:"next,omitempty"`
	// The home page's profile, absent on a search listing.
	Handle string `json:"handle,omitempty"`
	Name   string `json:"name,omitempty"`
	Link   string `json:"link,omitempty"`
	Avatar string `json:"avatar,omitempty"`
	Bio    string `json:"bio,omitempty"`
	// Raw is the service's body verbatim, so a reader that prints JSON
	// writes what the service said rather than a re-encoding of it.
	Raw json.RawMessage `json:"-"`
}

// RunDetail is one published run as /r/<id>.json prints it. Index is the
// client's own Index (internal/publish/index.go) with schema and caveats
// riding inside it, the way the uploader wrote it.
type RunDetail struct {
	ID          string
	PublishedAt string
	Private     bool
	Owned       bool
	Tape        string
	Card        string
	TapeBytes   int64
	Index       Index
	Schema      int
	Caveats     []string
	Author      *AuthorProfile
	Title       string
	Note        string
	// Raw is the service's body verbatim (see Listing.Raw).
	Raw json.RawMessage `json:"-"`
}

// runWire is /r/<id>.json on the wire. The index arrives as raw JSON and
// is unmarshalled into Index separately, so an index the server stored
// verbatim is read by the same shape that wrote it.
type runWire struct {
	ID          string          `json:"id"`
	PublishedAt string          `json:"published_at"`
	Private     bool            `json:"private"`
	Owned       bool            `json:"owned"`
	Tape        string          `json:"tape"`
	Card        *string         `json:"card"`
	TapeBytes   int64           `json:"tape_bytes"`
	Index       json.RawMessage `json:"index"`
	Author      *AuthorProfile  `json:"author"`
	Title       string          `json:"title"`
	Note        string          `json:"note"`
}

// List reads one page of published runs: the search, the journal scope, or
// one user home. Anything but 200 is a failure reported verbatim like
// Edit's, because the first thing a reader needs to know is whether the
// refusal was theirs — a bad cursor, a missing token — or the service's.
func (c *Client) List(ctx context.Context, q ListQuery) (*Listing, error) {
	if q.Mine && c.Token == "" {
		return nil, fmt.Errorf("publish: --mine needs a journal token in config.toml")
	}
	if q.Mine && q.User != "" {
		return nil, fmt.Errorf("publish: --mine and --user narrow to two different scopes")
	}

	base := strings.TrimRight(c.baseURL(), "/")
	vals := url.Values{}
	set := func(k, v string) {
		if v != "" {
			vals.Set(k, v)
		}
	}
	set("q", q.Text)
	set("model", q.Model)
	set("repo", q.Repo)
	set("quant", q.Quant)
	set("engine", q.Engine)
	set("gpu", q.GPU)
	set("host", q.Host)
	set("os", q.OS)
	set("set", q.Set)
	set("size", q.Size)
	if q.MinPredicted > 0 {
		vals.Set("min_predicted", fmt.Sprintf("%d", q.MinPredicted))
	}
	if q.Sessions > 0 {
		vals.Set("sessions", fmt.Sprintf("%d", q.Sessions))
	}
	if q.MinVRAMGB > 0 {
		vals.Set("min_vram", fmt.Sprintf("%d", q.MinVRAMGB))
	}
	if q.Sort != "" && q.Sort != "newest" {
		vals.Set("sort", q.Sort)
	}
	if q.Limit > 0 {
		vals.Set("limit", fmt.Sprintf("%d", q.Limit))
	}
	set("cursor", q.Cursor)

	path := "/api/v1/runs"
	if q.User != "" {
		path = "/u/" + q.User + ".json"
	} else if q.Mine {
		vals.Set("scope", "mine")
	}
	target := base + path
	if s := vals.Encode(); s != "" {
		target += "?" + s
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("publish: %s: %w", base, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, refusedBody(base, "the listing", resp, body)
	}
	var wire struct {
		Scope  string  `json:"scope"`
		Runs   []Row   `json:"runs"`
		Next   *string `json:"next"`
		Handle string  `json:"handle"`
		Name   string  `json:"name"`
		Link   string  `json:"link"`
		Avatar string  `json:"avatar"`
		Bio    string  `json:"bio"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("publish: %s answered the listing but its answer was unreadable: %w", base, err)
	}
	out := &Listing{
		Scope: wire.Scope, Runs: wire.Runs,
		Handle: wire.Handle, Name: wire.Name, Link: wire.Link,
		Avatar: wire.Avatar, Bio: wire.Bio, Raw: append(json.RawMessage(nil), body...),
	}
	if wire.Next != nil {
		out.Next = *wire.Next
	}
	if out.Runs == nil {
		out.Runs = []Row{}
	}
	return out, nil
}

// Run reads one published run. The id is bare — RunID takes the link —
// and anything but 200 is a failure reported verbatim like Edit's.
func (c *Client) Run(ctx context.Context, id string) (*RunDetail, error) {
	base := strings.TrimRight(c.baseURL(), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/r/"+id+".json", nil)
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("publish: %s: %w", base, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, refusedBody(base, "the run", resp, body)
	}
	var wire runWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("publish: %s answered the run but its answer was unreadable: %w", base, err)
	}
	var idx Index
	if len(wire.Index) > 0 {
		if err := json.Unmarshal(wire.Index, &idx); err != nil {
			return nil, fmt.Errorf("publish: %s answered the run but its index was unreadable: %w", base, err)
		}
	}
	out := &RunDetail{
		ID: wire.ID, PublishedAt: wire.PublishedAt,
		Private: wire.Private, Owned: wire.Owned,
		Tape: wire.Tape, TapeBytes: wire.TapeBytes,
		Index: idx, Schema: idx.Schema, Caveats: idx.Caveats,
		Author: wire.Author, Title: wire.Title, Note: wire.Note,
		Raw: append(json.RawMessage(nil), body...),
	}
	if wire.Card != nil {
		out.Card = *wire.Card
	}
	if out.Caveats == nil {
		out.Caveats = []string{}
	}
	return out, nil
}

// Download fetches the record itself: GET /r/<id>.toktape, the gzip bytes
// tape.Write puts on disk, so a download is a run file. It returns the
// byte count. A refusal writes nothing to w — the status is checked first,
// because a 404's sentence must never land in a file that reads as a tape.
func (c *Client) Download(ctx context.Context, id string, w io.Writer) (int64, error) {
	base := strings.TrimRight(c.baseURL(), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/r/"+id+tape.Ext, nil)
	if err != nil {
		return 0, fmt.Errorf("publish: %w", err)
	}
	req.Header.Set("Accept", "application/gzip")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.http().Do(req)
	if err != nil {
		return 0, fmt.Errorf("publish: %s: %w", base, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return 0, refusedBody(base, "the download", resp, body)
	}
	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return n, fmt.Errorf("publish: %s: %w", base, err)
	}
	return n, nil
}

// refusedBody quotes a refused read the way uploadError quotes a refused
// upload: the status and the server's short text body.
func refusedBody(base, what string, resp *http.Response, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if msg != "" && isText([]byte(msg)) {
		if len(msg) > 400 {
			msg = msg[:400] + "…"
		}
		return fmt.Errorf("publish: %s refused %s (%s): %s", base, what, resp.Status, msg)
	}
	return fmt.Errorf("publish: %s refused %s (%s)", base, what, resp.Status)
}

// RunID takes a bare run id or one of its links — the page, the record,
// the card or the JSON — and returns the id. Anything else is refused:
// guessing an id out of a user-home link or a search URL would fetch a
// run the caller never named.
func RunID(s string) (string, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return "", fmt.Errorf("publish: no run id: name a run id or its /r/<id> link")
	}
	if i := strings.LastIndex(t, "/r/"); i >= 0 {
		u, err := url.Parse(t)
		if err != nil {
			return "", fmt.Errorf("publish: %q is not a run id or /r/<id> link", s)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return "", fmt.Errorf("publish: %q is not a run id or /r/<id> link", s)
		}
		if u.Host == "" {
			return "", fmt.Errorf("publish: %q is not a run id or /r/<id> link", s)
		}
		rest := strings.TrimPrefix(u.EscapedPath(), "/r/")
		if rest == "" || strings.Contains(rest, "/") {
			return "", fmt.Errorf("publish: %q is not a run id or /r/<id> link", s)
		}
		for _, ext := range []string{".json", ".toktape", ".tape", ".png"} {
			rest = strings.TrimSuffix(rest, ext)
		}
		if !validRunID(rest) {
			return "", fmt.Errorf("publish: %q is not a run id or /r/<id> link", s)
		}
		return rest, nil
	}
	if strings.ContainsAny(t, "/?#") || strings.Contains(t, "://") {
		return "", fmt.Errorf("publish: %q is not a run id or /r/<id> link", s)
	}
	if !validRunID(t) {
		return "", fmt.Errorf("publish: %q is not a run id or /r/<id> link", s)
	}
	return t, nil
}

// validRunID is deliberately looser than the server's alphabet (ids.js):
// the client refuses what is clearly not an id — paths, sentences,
// punctuation — without re-owning the exact alphabet the server mints.
func validRunID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}
