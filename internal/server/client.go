// Package server attaches to a running llama-server or ik_llama.cpp instance:
// it reads what the server says about itself, sends one or many concurrent
// chat requests, and records each stream into a tape.RequestRecord.
//
// Two rules from docs/research/00-handover-brief.md shape the whole package.
// The server's own timings object is the record and the client-side figures
// are a cross-check that must agree within tape.RateTolerance (lesson 1); and
// anything not observed stays "" or 0 rather than becoming a plausible
// default.
//
// The parsing and reducing halves are pure functions over bytes and records
// (ReplayStream, Reduce, Aggregate, ParseFlags), so every rule is testable
// without a server; Client is the thin I/O wrapper that calls them.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// DefaultTimeout bounds the short control calls (Props, Slots, ApplyTemplate,
// Discover). It deliberately does not bound Stream, which runs as long as the
// model generates.
const DefaultTimeout = 10 * time.Second

// DefaultCandidates are the base URLs Discover probes when given none, in the
// order llama-server users most often bind them.
var DefaultCandidates = []string{
	"http://127.0.0.1:8080",
	"http://127.0.0.1:8081",
	"http://127.0.0.1:8000",
	"http://127.0.0.1:5000",
}

// Client talks to one server. It is safe for concurrent use: RunConcurrent
// drives several Stream calls through the same Client.
type Client struct {
	baseURL   string
	hc        *http.Client
	timeout   time.Duration
	userAgent string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient injects the http.Client, for tests and for proxy or TLS
// settings. Its Timeout must be zero or long enough for a whole generation:
// http.Client.Timeout bounds the entire request including the streamed body,
// so a short one truncates a normal decode.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.hc = hc
		}
	}
}

// WithTimeout sets the deadline for the short control calls only. Stream
// honours the caller's context and nothing else.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.userAgent = ua }
}

// New returns a Client for baseURL. A URL without a scheme is assumed to be
// http, and a trailing slash is dropped. baseURL may be empty when the caller
// intends to call Discover first.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL:   NormalizeBaseURL(baseURL),
		hc:        &http.Client{},
		timeout:   DefaultTimeout,
		userAgent: "toktape",
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// NormalizeBaseURL adds a missing http scheme and drops trailing slashes.
func NormalizeBaseURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	return strings.TrimRight(s, "/")
}

// BaseURL is the server this client talks to.
func (c *Client) BaseURL() string { return c.baseURL }

// WithBaseURL returns a copy of c pointed at another server, sharing the same
// http.Client and options. Used after Discover.
func (c *Client) WithBaseURL(baseURL string) *Client {
	cp := *c
	cp.baseURL = NormalizeBaseURL(baseURL)
	return &cp
}

// Props is GET /props: what the server says about itself.
type Props struct {
	ModelPath                 string `json:"model_path"`
	BuildInfo                 string `json:"build_info"`
	ChatTemplate              string `json:"chat_template"`
	TotalSlots                int    `json:"total_slots"`
	DefaultGenerationSettings struct {
		NCtx int `json:"n_ctx"`
	} `json:"default_generation_settings"`

	// Raw is the whole response, so a field this struct does not name is still
	// available to the caller and to DetectKind.
	Raw map[string]json.RawMessage `json:"-"`
	// Headers are the response headers; the Server header is one of the
	// signals DetectKind uses.
	Headers http.Header `json:"-"`
}

// Props reads GET /props.
func (c *Client) Props(ctx context.Context) (*Props, error) {
	var p Props
	body, hdr, err := c.get(ctx, "/props")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("server: decode /props: %w", err)
	}
	if err := json.Unmarshal(body, &p.Raw); err != nil {
		return nil, fmt.Errorf("server: decode /props raw: %w", err)
	}
	p.Headers = hdr
	return &p, nil
}

// CtxSize is n_ctx from the default generation settings, or 0 when the server
// did not report it.
func (p *Props) CtxSize() int {
	if p == nil {
		return 0
	}
	return p.DefaultGenerationSettings.NCtx
}

// DetectKind names the serving engine from what /props returned.
//
// ik_llama.cpp serves the same routes as llama-server, so there is no field
// that distinguishes them by contract; the only reliable markers are the
// engine naming itself somewhere in the response or in the Server header. The
// heuristic is therefore deliberately conservative: anything that answered
// /props without an ik marker is reported as tape.ServerLlamaCPP, and only a
// server that did not answer at all is tape.ServerUnknown.
func DetectKind(p *Props) tape.ServerKind {
	if p == nil {
		return tape.ServerUnknown
	}
	hay := strings.ToLower(p.BuildInfo)
	for k, v := range p.Raw {
		// model_path and chat_template carry user-chosen text: a model stored
		// under an ik_llama.cpp directory would otherwise misdetect the engine.
		if k == "model_path" || k == "chat_template" {
			continue
		}
		hay += " " + strings.ToLower(string(v))
	}
	if p.Headers != nil {
		hay += " " + strings.ToLower(p.Headers.Get("Server"))
	}
	for _, marker := range []string{"ik_llama", "ik-llama", "ikllama"} {
		if strings.Contains(hay, marker) {
			return tape.ServerIKLlama
		}
	}
	return tape.ServerLlamaCPP
}

// BuildFromProps splits a build_info string such as "b4321-abcdef12" into the
// build number and the short commit hash. A string without a separator is all
// build; an empty string yields two empty strings, which print as "?".
func BuildFromProps(buildInfo string) (build, commit string) {
	s := strings.TrimSpace(buildInfo)
	if s == "" {
		return "", ""
	}
	if i := strings.Index(s, "-"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// Slot is one entry of GET /slots.
type Slot struct {
	ID           int  `json:"id"`
	IsProcessing bool `json:"is_processing"`
	NCtx         int  `json:"n_ctx"`
	NPast        int  `json:"n_past"`
	// Raw keeps the fields this struct does not name (cache_tokens, prompt,
	// per-build extras).
	Raw map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON fills the named fields and Raw from the same object.
func (s *Slot) UnmarshalJSON(b []byte) error {
	type alias Slot
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*s = Slot(a)
	return json.Unmarshal(b, &s.Raw)
}

// Slots reads GET /slots. The server returns 501 when it was started with
// --no-slots, which surfaces here as an error rather than an empty list.
func (c *Client) Slots(ctx context.Context) ([]Slot, error) {
	body, _, err := c.get(ctx, "/slots")
	if err != nil {
		return nil, err
	}
	var out []Slot
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("server: decode /slots: %w", err)
	}
	return out, nil
}

// BusyCount is the number of slots currently processing, for
// tape.RunSample.SlotsBusy.
func BusyCount(slots []Slot) int {
	n := 0
	for _, s := range slots {
		if s.IsProcessing {
			n++
		}
	}
	return n
}

// ApplyTemplate renders messages through the server's chat template and
// returns the prompt as the model will actually see it (lesson 4: the template
// decides what is being measured).
func (c *Client) ApplyTemplate(ctx context.Context, msgs []tape.Message) (string, error) {
	body, err := c.post(ctx, "/apply-template", map[string]any{"messages": msgs})
	if err != nil {
		return "", err
	}
	var out struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("server: decode /apply-template: %w", err)
	}
	return out.Prompt, nil
}

// Discover returns the first base URL that answers /props: this client's own
// base URL when it has one, then each candidate in order. Passing nil
// candidates uses DefaultCandidates.
//
// Each probe gets its own short deadline, so a port that black-holes packets
// costs one timeout rather than the whole scan. Discover does not modify c;
// use WithBaseURL with the result.
func (c *Client) Discover(ctx context.Context, candidates []string) (string, error) {
	if candidates == nil {
		candidates = DefaultCandidates
	}
	var tried []string
	seen := map[string]bool{}
	var order []string
	if c.baseURL != "" {
		order = append(order, c.baseURL)
	}
	order = append(order, candidates...)

	var firstErr error
	for _, raw := range order {
		u := NormalizeBaseURL(raw)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		tried = append(tried, u)
		probe := c.WithBaseURL(u)
		if _, err := probe.Props(ctx); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		return u, nil
	}
	if firstErr == nil {
		return "", fmt.Errorf("server: discover: no candidates to probe")
	}
	return "", fmt.Errorf("server: discover: no server answered /props at %s: %w", strings.Join(tried, ", "), firstErr)
}

// get performs a bounded GET and returns the body and response headers.
func (c *Client) get(ctx context.Context, path string) ([]byte, http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("server: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("server: GET %s: read body: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("server: GET %s: %s: %s", path, resp.Status, clip(strings.TrimSpace(string(body)), 200))
	}
	return body, resp.Header, nil
}

// post performs a bounded JSON POST and returns the body.
func (c *Client) post(ctx context.Context, path string, payload any) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("server: POST %s: encode: %w", path, err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, path, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("server: POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("server: POST %s: read body: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("server: POST %s: %s: %s", path, resp.Status, clip(strings.TrimSpace(string(body)), 200))
	}
	return body, nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("server: no base URL (call Discover first)")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("server: %s %s: %w", method, path, err)
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	return req, nil
}
