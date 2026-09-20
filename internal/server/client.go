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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// DefaultTimeout bounds the short control calls (Props, Slots, ApplyTemplate,
// Discover). It deliberately does not bound Stream, which runs as long as the
// model generates.
const DefaultTimeout = 10 * time.Second

// ErrUnreachable, ErrLoading and ErrBusy are the ways attaching can fail, and
// the caller must tell them apart: the first is a mistake the user can fix
// now, the other two are waits for different reasons.
//
// Measured 2026-09-13: llama-server opens its TCP port and then answers
// nothing on /props for minutes while it lazily loads a large model. Treating
// that as "unreachable" — which toktape did — sends a first-time user to fix a
// URL that was right all along, so Props classifies the three shapes the
// symptom takes: a refused connection, an explicit 503 or "Loading model"
// body, and a request that simply never answers.
var (
	// ErrUnreachable means nothing is serving there: the connection was
	// refused, the host is gone, or the answer was not a server we can use.
	ErrUnreachable = errors.New("server: unreachable")
	// ErrLoading means a server is there and is not ready yet. Waiting is
	// the correct response; the wrapped detail says how it announced itself.
	ErrLoading = errors.New("server: loading the model")
	// ErrBusy means /props did not answer while /health did: the model is
	// resident and another request is running. ik_llama.cpp holds /props
	// until a completion finishes (measured 2026-09-13: over two minutes), so
	// without this a busy ik server reads as one still loading its model.
	ErrBusy = errors.New("server: busy")
)

// DefaultCandidates are the base URLs Discover probes when given none, in the
// order llama-server users most often bind them. It is the one list: the
// failure a fruitless scan prints names these ports through DefaultPorts, so a
// port added here is a port the message offers.
//
// 8001 is here because of TTP-75 (2026-09-14): the rig this project develops
// against serves on it, and the first thing anyone there saw was "no server
// answered /props" while a server ran one port away — which reads as "there is
// no server", not as "pass --url".
//
// 11434 and 1234 are here because of TTP-158 (2026-09-21): a bare `toktape`
// did not find Ollama or LM Studio, and both are first-run servers, not
// specialist ones. Measured that day on Ollama 0.34.2: it answers 404 on
// /props ("404 page not found") and 200 on /v1/models, so Discover's second
// pass — the ErrNoProps candidates tried against /v1/models — is what finds
// it. They sit AFTER every llama port so a /props server still wins wherever
// it listens.
var DefaultCandidates = []string{
	"http://127.0.0.1:8080",  // llama-server's own default
	"http://127.0.0.1:8081",  // a second llama-server, numbered off 8080
	"http://127.0.0.1:8001",  // a second llama-server, numbered off 8000 (TTP-75)
	"http://127.0.0.1:8000",  // uvicorn/FastAPI-shaped wrappers
	"http://127.0.0.1:5000",  // the other wrapper convention
	"http://127.0.0.1:11434", // Ollama's default port: 404 /props, 200 /v1/models (TTP-158)
	"http://127.0.0.1:1234",  // LM Studio's default port, the same shape (TTP-158)
}

// DefaultPorts is the ports of DefaultCandidates as one human list, for the
// sentence a failed discovery prints. It is derived rather than written out a
// second time: a hint that names a stale set of ports sends the reader to look
// in the wrong place, which is the whole complaint TTP-75 came from.
func DefaultPorts() string {
	ports := make([]string, 0, len(DefaultCandidates))
	for _, c := range DefaultCandidates {
		if u, err := url.Parse(NormalizeBaseURL(c)); err == nil && u.Port() != "" {
			ports = append(ports, u.Port())
		}
	}
	return strings.Join(ports, ", ")
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
	// Engine is the one extra object an engine that is not llama.cpp reports
	// about itself (2026-09-15, ExLlamaV3 behind a llama-server-protocol shim).
	// nil when the body had no engine key — which is every llama-server and ik
	// /props there has ever been — so absent engine and empty engine stay
	// distinguishable.
	Engine *EngineProps `json:"engine,omitempty"`

	// Raw is the whole response, so a field this struct does not name is still
	// available to the caller and to DetectKind.
	Raw map[string]json.RawMessage `json:"-"`
	// Headers are the response headers; the Server header is one of the
	// signals DetectKind uses.
	Headers http.Header `json:"-"`
}

// EngineProps is the engine object of /props. Every key inside it is optional;
// an omitted key decodes to its zero value, which the recorder records as
// unknown ("?" on the card) rather than guessing at.
//
// The three members carry everything the card needs from an engine it cannot
// read out of a GGUF header and an argv: what it is (Name, Version), how it was
// launched (Args — the engine's own argv, one element per string, verbatim),
// what model it loaded (Model) and where it put the weights (Placement).
type EngineProps struct {
	Name      string          `json:"name"`
	Version   string          `json:"version"`
	Args      []string        `json:"args"`
	Model     EngineModel     `json:"model"`
	Placement EnginePlacement `json:"placement"`
	// Draft names the engine's speculative drafter when one ran: Model is
	// what drafts ("mtp" for a model's own MTP head, or a draft model's name)
	// and NMax the most tokens drafted per step. Both verbatim, both optional
	// (2026-09-15).
	Draft *EngineDraft `json:"draft,omitempty"`
	// ServerPID is the process doing the work, declared by the server itself
	// and optional (TTP-107, 2026-09-17).
	//
	// It exists because a proxy can answer /props truthfully and still be the
	// wrong subject. toktape finds the server by the model path in an argv, or
	// failing that by who holds the listening socket, and both answers name
	// the process it is talking to — which for a shim is the shim. Every
	// figure taken from a pid then describes the proxy: memory, page faults,
	// and the GPU processes that are "not ours", which is how eight mistral.rs
	// takes on an idle box all read "contended: yes".
	//
	// A server that fronts another process declares that process here. A
	// llama-server does not need to: its own argv names the model, which is
	// how it has always been found.
	ServerPID int `json:"server_pid,omitempty"`
}

// EngineDraft is the engine block's speculative-decoding identity. It fills
// the same two card fields -md and --draft-max fill for llama-server.
type EngineDraft struct {
	Model string `json:"model"`
	NMax  int    `json:"n_max"`
}

// EngineModel is the model an engine reported: the shape a GGUF header would
// have supplied, in the engine's own words. Quant is verbatim and may contain
// spaces and separators ("EXL3 4.05 bpw · head 6.0"); ActiveBytesPerToken is
// the same quantity tape.ModelInfo.ActiveBytesPerToken is.
type EngineModel struct {
	Format              string `json:"format"`
	Arch                string `json:"arch"`
	Quant               string `json:"quant"`
	Bytes               int64  `json:"bytes"`
	Files               int    `json:"files"`
	Params              int64  `json:"params"`
	NLayers             int    `json:"n_layers"`
	NExperts            int    `json:"n_experts"`
	NExpertsUsed        int    `json:"n_experts_used"`
	CtxTrain            int    `json:"ctx_train"`
	ActiveBytesPerToken int64  `json:"active_bytes_per_token"`
}

// EnginePlacement is where an engine put the weights, in the engine's own
// accounting: one row per device, the bytes each tensor class occupies there,
// and the KV cache's VRAM bytes.
type EnginePlacement struct {
	Devices     []EngineDevice `json:"devices"`
	VRAMKVBytes int64          `json:"vram_kv_bytes"`
}

// EngineDevice is one device of an engine placement. Device is tape.DeviceCPU
// or "GPU<n>" with n the nvidia-smi index. Bytes is the device's total, which
// the contract says is the sum of its classes. ActiveBytesPerToken is what the
// device is read for on one token — normally OMITTED: an engine that swaps
// experts between devices on the fly does not have a static answer.
type EngineDevice struct {
	Device              string           `json:"device"`
	Bytes               int64            `json:"bytes"`
	Classes             map[string]int64 `json:"classes"`
	Layers              string           `json:"layers"`
	ActiveBytesPerToken int64            `json:"active_bytes_per_token"`
}

// EngineName is the engine object's name, or "" when the body had none. It is
// the one accessor DetectKind and the recorder both branch on, so the string
// they compare is spelled once.
func (p *Props) EngineName() string {
	if p == nil || p.Engine == nil {
		return ""
	}
	return p.Engine.Name
}

// ServerPID is the pid the engine object declared, or 0 when the body
// declared none. Zero and negative are both "none": a pid is a positive
// number, and a body that sends 0 has said nothing rather than named init.
func (p *Props) ServerPID() int {
	if p == nil || p.Engine == nil || p.Engine.ServerPID <= 0 {
		return 0
	}
	return p.Engine.ServerPID
}

// Props reads GET /props.
//
// Every failure is classified as ErrUnreachable, ErrLoading or ErrBusy (see
// those variables); nothing else is returned, so a caller can branch on them
// with errors.Is and never on a message.
//
// A /props that answers nothing within the timeout is followed by one GET
// /health with the same timeout, because the two waits that look alike there
// — a model still loading and a completion holding /props — are told apart
// only by whether /health is ok.
func (c *Client) Props(ctx context.Context) (*Props, error) {
	body, hdr, status, err := c.getRaw(ctx, "/props")
	// 404/405 is not "nothing is serving there": something answered, and its
	// answer says it is not the llama-server protocol (TTP-99). It is still
	// ErrUnreachable — every existing errors.Is keeps holding — but under
	// ErrNoProps, so the generic OpenAI-compatible mode can try /v1/models
	// on exactly this candidate and no other failure shape.
	if err == nil && noPropsStatus(status) {
		return nil, fmt.Errorf("%w: %s: /props answered HTTP %d: %s",
			ErrNoProps, c.baseURL, status, clip(strings.TrimSpace(string(body)), 200))
	}
	health := 0
	if err != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		health = c.healthStatus(ctx)
	}
	if e := classifyProps(c.baseURL, status, body, err, ctx.Err(), health); e != nil {
		return nil, e
	}
	var p Props
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("%w: %s: /props is not JSON: %v", ErrUnreachable, c.baseURL, err)
	}
	if err := json.Unmarshal(body, &p.Raw); err != nil {
		return nil, fmt.Errorf("%w: %s: /props is not a JSON object: %v", ErrUnreachable, c.baseURL, err)
	}
	p.Headers = hdr
	return &p, nil
}

// classifyProps turns one /props attempt into a typed error, or nil when the
// attempt succeeded. It is a pure function of what the attempt produced so
// every branch is testable without a server.
//
// parentErr is the caller's own context error. A per-call deadline that fired
// while the caller is still interested is the "answers nothing" case; the same
// deadline after the caller gave up is just the cancellation and must not be
// dressed up as a server state.
//
// healthStatus is the HTTP status of the /health probe made after that
// deadline, or 0 when none was made or it got no answer. 200 means the server
// is up and busy (ErrBusy); anything else keeps the loading verdict, since
// /health is where llama-server says 503 while it loads.
func classifyProps(baseURL string, status int, body []byte, transportErr, parentErr error, healthStatus int) error {
	if parentErr != nil {
		return fmt.Errorf("%w: %s: %v", ErrUnreachable, baseURL, parentErr)
	}
	if transportErr != nil {
		if errors.Is(transportErr, context.DeadlineExceeded) {
			if healthStatus == http.StatusOK {
				return fmt.Errorf("%w: %s: /props did not answer while /health is ok — the server is busy with another request (ik_llama.cpp blocks /props during a completion)", ErrBusy, baseURL)
			}
			return fmt.Errorf("%w: %s: no answer on /props within the timeout", ErrLoading, baseURL)
		}
		return fmt.Errorf("%w: %s: %v", ErrUnreachable, baseURL, rootCause(transportErr))
	}
	if status >= 200 && status < 300 {
		return nil
	}
	detail := loadingDetail(body)
	if status == http.StatusServiceUnavailable || detail != "" {
		if detail == "" {
			detail = http.StatusText(status)
		}
		return fmt.Errorf("%w: %s: %s", ErrLoading, baseURL, detail)
	}
	return fmt.Errorf("%w: %s: HTTP %d: %s", ErrUnreachable, baseURL, status,
		clip(strings.TrimSpace(string(body)), 200))
}

// healthStatus is the status of one bounded GET /health, or 0 when it got no
// answer.
func (c *Client) healthStatus(ctx context.Context) int {
	_, _, status, err := c.getRaw(ctx, "/health")
	if err != nil {
		return 0
	}
	return status
}

// rootCause is the innermost error of a chain.
//
// A refused connection arrives wrapped four deep — the GET, net/http's URL
// error, the dial, the syscall — and printing all of it buries the two words
// that tell the user what to do ("connection refused") under a stack trace in
// prose. The layers add nothing a reader of this message needs: the operation
// and the URL are already in the sentence around it.
func rootCause(err error) error {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
}

// loadingDetail returns llama-server's own "not ready" sentence when the body
// is its error envelope and the message says so, else "".
//
// The envelope is {"error":{"message":"Loading model","type":...}}. Matching on
// the message rather than on the status alone is what catches the builds that
// answer 500 while a model loads.
func loadingDetail(body []byte) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return ""
	}
	msg := strings.TrimSpace(env.Error.Message)
	if msg == "" {
		return ""
	}
	low := strings.ToLower(msg)
	for _, marker := range []string{"loading model", "model is loading", "loading the model", "not ready"} {
		if strings.Contains(low, marker) {
			return msg
		}
	}
	return ""
}

// CtxSize is n_ctx from the default generation settings, or 0 when the server
// did not report it.
func (p *Props) CtxSize() int {
	if p == nil {
		return 0
	}
	return p.DefaultGenerationSettings.NCtx
}

// ikMarkers are the spellings by which ik_llama.cpp names itself, matched
// case-insensitively.
var ikMarkers = []string{"ik_llama", "ik-llama", "ikllama"}

// hasIKMarker reports whether s, already lower-cased, names ik_llama.cpp.
func hasIKMarker(s string) bool {
	for _, marker := range ikMarkers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// DetectKind names the serving engine from what /props returned.
//
// ik_llama.cpp serves the same routes as llama-server, so no field
// distinguishes them by contract. The rules, in order:
//
//   - an engine object that names an engine is that name, lowercased
//     (2026-09-15; generalised beyond exllamav3 2026-09-17, TTP-105): the
//     engine said what it is, in the one field that exists for saying it;
//   - an ik marker anywhere in the response or the Server header is
//     tape.ServerIKLlama;
//   - a build_info key, with any value, is tape.ServerLlamaCPP — mainline
//     stamps one on every build;
//   - anything else is tape.ServerUnknown: it answered /props and did not say
//     what it is.
//
// The engine rule comes first because the two under it scan every value in the
// body for a marker, and an engine object is full of user-chosen paths and
// version strings that must not be mistaken for either.
//
// The last rule is measured (TTP-33, 2026-09-13): a real ik_llama.cpp
// 7b79b229 /props has no build_info and no marker outside model_path and
// chat_template, and calling that llama-server printed a default nobody
// observed. RefineKind lets the local process settle it.
func DetectKind(p *Props) tape.ServerKind {
	if p == nil {
		return tape.ServerUnknown
	}
	if name := strings.TrimSpace(p.EngineName()); name != "" {
		return tape.ServerKind(strings.ToLower(name))
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
	if hasIKMarker(hay) {
		return tape.ServerIKLlama
	}
	if _, ok := p.Raw["build_info"]; ok || p.BuildInfo != "" {
		return tape.ServerLlamaCPP
	}
	return tape.ServerUnknown
}

// RefineKind corrects k with what the local process shows: the executable
// path (procmon.Exe) and argv[0]. A server whose binary sits in an ik_llama.cpp
// checkout or install is ik, whatever /props implied.
//
// Nothing else is inferred. A binary named plain llama-server leaves k as it
// was, Unknown included, because a mainline build and an ik build install the
// same name. Only argv[0] is read: later arguments are user text (a model under
// an ik_llama.cpp directory names no engine).
func RefineKind(k tape.ServerKind, exe string, argv []string) tape.ServerKind {
	if k == tape.ServerIKLlama || k.SelfDeclared() {
		// An engine block's kind is the name the engine gave
		// (tape.ServerKind.SelfDeclared doc); the process cannot outvote it.
		return k
	}
	hay := strings.ToLower(exe)
	if len(argv) > 0 {
		hay += " " + strings.ToLower(argv[0])
	}
	if hasIKMarker(hay) {
		return tape.ServerIKLlama
	}
	return k
}

// BuildFromProps splits a build_info string into the build number and the
// short commit hash. An empty string yields two empty strings, which print
// as "?".
//
// Mainline llama.cpp stamps "b4321-abcdef12" and the dash split is the whole
// of it. ik_llama.cpp has no bNNNN counter (TTP-33, 2026-09-13), so its
// build_info is whatever its own build stamped there, and the shapes below are
// the ones seen or plausibly produced by a tree without a release number:
//
//	b4321-abcdef12     mainline: build b4321, commit abcdef12
//	7b79b229           a bare short hash: no build, commit 7b79b229
//	3650 (abcdef12)    a counter with its hash in parentheses
//	build 3650         a counter and nothing else
//
// Whatever parses is kept and nothing is invented: a shape none of these match
// is returned whole as the build, which is what it was called in /props.
func BuildFromProps(buildInfo string) (build, commit string) {
	s := strings.TrimSpace(buildInfo)
	if s == "" {
		return "", ""
	}
	// "build 3650", possibly with a parenthesised hash after it.
	if rest := strings.TrimSpace(strings.TrimPrefix(s, "build ")); rest != s {
		s = rest
	}
	if s == "" {
		return "", ""
	}
	// "<n> (<hash>)". Either half may be empty, and an empty half stays empty
	// rather than borrowing the other.
	if i := strings.Index(s, "("); i >= 0 && strings.HasSuffix(s, ")") {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1 : len(s)-1])
	}
	if i := strings.Index(s, "-"); i >= 0 {
		return s[:i], s[i+1:]
	}
	// A bare short hash is a commit, not a build number. Mainline's "b4321" is
	// five characters and so cannot reach this rule.
	if isShortHash(s) {
		return "", s
	}
	return s, ""
}

// isShortHash reports whether s is a git object name as a build stamps it:
// 7 to 40 hexadecimal digits and nothing else.
func isShortHash(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
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

// Tokenize counts content's tokens with the server's own tokenizer: POST
// /tokenize, add_special off, the length of the returned token array. The
// count is the server's, never a client-side price, which is the whole
// reason to ask: the published set's four same-length prompts tokenized to
// 4901..7458 on one tokenizer (lead, 2026-09-20), and no byte-per-token
// constant can see that spread.
//
// Any failure is an error and the caller falls back to pricing: a non-200,
// a body that is not JSON, and a body with no tokens array — an error
// envelope that 200s would otherwise read as a count of zero. An empty
// array for empty content is a real answer, not a miss. Never called on a
// tape.ServerOpenAI server: the route is llama-server's own and an
// OpenAI-compatible server answering it with anything is not a promise this
// side can lean on.
func (c *Client) Tokenize(ctx context.Context, content string) (int, error) {
	body, err := c.post(ctx, "/tokenize", map[string]any{"content": content, "add_special": false})
	if err != nil {
		return 0, err
	}
	var out struct {
		Tokens *[]int `json:"tokens"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return 0, fmt.Errorf("server: decode /tokenize: %w", err)
	}
	if out.Tokens == nil {
		return 0, fmt.Errorf("server: decode /tokenize: no tokens array in the answer")
	}
	return len(*out.Tokens), nil
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
// costs its timeout — twice, since a /props that never answers is followed by
// the /health probe — rather than the whole scan. Discover does not modify c;
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

	var firstErr, loadingErr error
	var noProps []string
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
			// A candidate that is loading or busy outranks every refusal: it
			// is a server, and the caller can wait for it. Reporting the
			// first refused port instead would tell the user to fix a URL
			// while the server they meant is two seconds from ready.
			if loadingErr == nil && (errors.Is(err, ErrLoading) || errors.Is(err, ErrBusy)) {
				loadingErr = err
			}
			// A candidate that answered 404/405 on /props may still be an
			// OpenAI-compatible server (TTP-99). It is collected for the
			// second pass below rather than tried now, so a /props server
			// listed anywhere still wins over a /v1/models one.
			if errors.Is(err, ErrNoProps) {
				noProps = append(noProps, u)
			}
			continue
		}
		return u, nil
	}
	if loadingErr != nil {
		return "", fmt.Errorf("server: discover: %w", loadingErr)
	}
	// Second pass, in candidate order: the first candidate that 404'd /props
	// and answers /v1/models is found.
	for _, u := range noProps {
		if _, err := c.WithBaseURL(u).Models(ctx); err == nil {
			return u, nil
		}
	}
	if firstErr == nil {
		return "", fmt.Errorf("%w: server: discover: no candidates to probe", ErrUnreachable)
	}
	return "", fmt.Errorf("server: discover: no server answered /props at %s: %w", strings.Join(tried, ", "), firstErr)
}

// get performs a bounded GET and fails on any non-2xx status.
func (c *Client) get(ctx context.Context, path string) ([]byte, http.Header, error) {
	body, hdr, status, err := c.getRaw(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	if status < 200 || status >= 300 {
		return nil, nil, fmt.Errorf("server: GET %s: HTTP %d: %s", path, status, clip(strings.TrimSpace(string(body)), 200))
	}
	return body, hdr, nil
}

// getRaw performs a bounded GET and hands the status code back with the body
// instead of turning it into an error, so a caller that must tell a 503 from a
// 500 can. A transport failure is still an error; err and status are never
// both meaningful.
func (c *Client) getRaw(ctx context.Context, path string) ([]byte, http.Header, int, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("server: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, nil, 0, fmt.Errorf("server: GET %s: read body: %w", path, err)
	}
	return body, resp.Header, resp.StatusCode, nil
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
