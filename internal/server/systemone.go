package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// The client for a decision model's endpoint (TTP-192, 2026-10-02): POST
// /v1/systemone sends a state and typed questions and gets one probability
// per option back, with no tokens in between.
//
// Two things here are not what they look like. The request and the response
// are objects whose KEY ORDER is part of what is shown (the questions are
// drawn in the order the caller wrote them, the options in the order the
// criteria were written), and a Go map forgets it; every shown figure is
// therefore decoded with a token walk, never into a map. And a decision
// server's /props is not llama-server's: bloomery names its engine as a
// string, which Props (an engine OBJECT) cannot decode, so the identity is
// read by its own tolerant probe and Props is left as it was.

// SystemOnePath is the endpoint a decision run posts to.
const SystemOnePath = "/v1/systemone"

// systemOneClient keeps connections alive for as many lanes as a burst
// uses. http.DefaultTransport keeps two idle connections per host, so a
// four-lane burst would redial on most requests and bill the dial to the
// engine's latency.
var systemOneClient = &http.Client{Transport: func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConnsPerHost = 64
	return t
}()}

// PostSystemOne posts body to baseURL's /v1/systemone and returns the
// response bytes with the two instants the latency is made of: sent is taken
// just before the request is handed to the transport, answered once the last
// byte of the body has been read. A non-2xx status returns the body and an
// error carrying both, so a recorder keeps the server's own words.
//
// Latency is answered - sent and nothing else; a caller's idle time between
// requests never enters it.
func PostSystemOne(ctx context.Context, baseURL string, body []byte) (resp []byte, sent, answered time.Time, err error) {
	base := NormalizeBaseURL(baseURL)
	if base == "" {
		return nil, time.Time{}, time.Time{}, errors.New("server: no base URL for /v1/systemone")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+SystemOnePath, bytes.NewReader(body))
	if err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("server: POST %s: %w", SystemOnePath, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "toktape")
	sent = time.Now()
	r, err := systemOneClient.Do(req)
	if err != nil {
		return nil, sent, time.Time{}, fmt.Errorf("server: POST %s: %w", SystemOnePath, err)
	}
	defer r.Body.Close()
	resp, err = io.ReadAll(io.LimitReader(r.Body, 8<<20))
	answered = time.Now()
	if err != nil {
		return resp, sent, time.Time{}, fmt.Errorf("server: POST %s: read body: %w", SystemOnePath, err)
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return resp, sent, time.Time{}, fmt.Errorf("server: POST %s: %s: %s", SystemOnePath, r.Status, clip(strings.TrimSpace(string(resp)), 200))
	}
	return resp, sent, answered, nil
}

// member is one key of a JSON object with its value's raw bytes, in the
// order the object wrote them.
type member struct {
	Key string
	Raw json.RawMessage
}

// orderedMembers walks a JSON object's tokens and returns its members in
// order. A repeated key is kept as many times as it was written; deciding
// what that means is the caller's.
func orderedMembers(raw []byte) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	var out []member
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kt.(string)
		if !ok {
			return nil, errors.New("not a JSON object")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, member{Key: key, Raw: v})
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("trailing data after the JSON object")
	}
	return out, nil
}

// SystemOneRequest is a request body parsed for what a recorder shows.
type SystemOneRequest struct {
	// Model is the request's "model" string, "" when it named none.
	Model string
	// State is the request's "state" value, verbatim.
	State json.RawMessage
	// Questions are in the order the request wrote them.
	Questions []tape.DecisionQuestion
}

// ParseSystemOneRequest turns a request body into its questions, keeping the
// order the question ids and each question's criteria were written in. A body
// the endpoint would refuse is refused here with the reason: not an object,
// no state, no questions, an unknown question type, a choice or a score with
// no criteria.
func ParseSystemOneRequest(body []byte) (*SystemOneRequest, error) {
	top, err := orderedMembers(body)
	if err != nil {
		return nil, fmt.Errorf("request is not a JSON object: %w", err)
	}
	var out SystemOneRequest
	var qraw json.RawMessage
	for _, m := range top {
		switch m.Key {
		case "model":
			_ = json.Unmarshal(m.Raw, &out.Model)
		case "state":
			out.State = m.Raw
		case "questions":
			qraw = m.Raw
		}
	}
	if len(out.State) == 0 {
		return nil, errors.New(`request has no "state"`)
	}
	if len(qraw) == 0 {
		return nil, errors.New(`request has no "questions"`)
	}
	qs, err := orderedMembers(qraw)
	if err != nil {
		return nil, fmt.Errorf(`"questions" is not an object: %w`, err)
	}
	if len(qs) == 0 {
		return nil, errors.New(`"questions" is empty`)
	}
	seen := map[string]bool{}
	for _, m := range qs {
		if seen[m.Key] {
			return nil, fmt.Errorf("question %q is written twice", m.Key)
		}
		seen[m.Key] = true
		q, err := parseQuestion(m.Key, m.Raw)
		if err != nil {
			return nil, fmt.Errorf("question %q: %w", m.Key, err)
		}
		out.Questions = append(out.Questions, q)
	}
	return &out, nil
}

func parseQuestion(id string, raw json.RawMessage) (tape.DecisionQuestion, error) {
	q := tape.DecisionQuestion{ID: id}
	fields, err := orderedMembers(raw)
	if err != nil {
		return q, err
	}
	var criteria json.RawMessage
	for _, f := range fields {
		switch f.Key {
		case "type":
			if err := json.Unmarshal(f.Raw, &q.Type); err != nil {
				return q, errors.New(`"type" is not a string`)
			}
		case "instructions":
			_ = json.Unmarshal(f.Raw, &q.Instructions)
		case "criteria":
			criteria = f.Raw
		}
	}
	switch q.Type {
	case tape.DecisionNoul:
		return q, nil
	case tape.DecisionChoice, tape.DecisionScore:
	default:
		return q, fmt.Errorf("unknown type %q (choice, score or noul)", q.Type)
	}
	if len(criteria) == 0 {
		return q, fmt.Errorf("a %s question needs criteria", q.Type)
	}
	switch criteria[0] {
	case '{':
		if q.Type == tape.DecisionScore {
			return q, errors.New("a score's criteria are a list of levels")
		}
		ms, err := orderedMembers(criteria)
		if err != nil {
			return q, err
		}
		for _, m := range ms {
			var text string
			if err := json.Unmarshal(m.Raw, &text); err != nil {
				return q, fmt.Errorf("criterion %q is not a string", m.Key)
			}
			q.Options = append(q.Options, tape.DecisionOption{Key: m.Key, Text: text})
		}
	case '[':
		var items []string
		if err := json.Unmarshal(criteria, &items); err != nil {
			return q, errors.New("criteria are not a list of strings")
		}
		for i, text := range items {
			key := text
			if q.Type == tape.DecisionScore {
				key = fmt.Sprint(i)
			}
			q.Options = append(q.Options, tape.DecisionOption{Key: key, Text: text})
		}
	default:
		return q, errors.New("criteria are neither an object nor a list")
	}
	if len(q.Options) == 0 {
		return q, fmt.Errorf("a %s question needs at least one criterion", q.Type)
	}
	return q, nil
}

// SystemOneResponse is a response body parsed against its request.
type SystemOneResponse struct {
	Answers     []tape.DecisionAnswer // in the request's question order
	InputTokens int
	Model       string
	// Timings is the engine's own "timings" key, nil when the response had
	// none (the official servers send none).
	Timings *tape.DecisionServerTimings
}

// ParseSystemOneResponse reads a response body for the request's questions.
// A body that does not answer every question with the shape its type asks
// for is a schema mismatch and an error: a recorder keeps the request, marks
// it failed and goes on, and no figure is made from a half-answer.
//
// Probabilities come back in the question's option order (the request's),
// whatever order the server wrote them in.
func ParseSystemOneResponse(body []byte, questions []tape.DecisionQuestion) (*SystemOneResponse, error) {
	top, err := orderedMembers(body)
	if err != nil {
		return nil, fmt.Errorf("response is not a JSON object: %w", err)
	}
	var (
		out        SystemOneResponse
		answersRaw json.RawMessage
		usageRaw   json.RawMessage
		timingsRaw json.RawMessage
		haveModel  bool
	)
	for _, m := range top {
		switch m.Key {
		case "model":
			if err := json.Unmarshal(m.Raw, &out.Model); err != nil {
				return nil, errors.New(`response "model" is not a string`)
			}
			haveModel = true
		case "answers":
			answersRaw = m.Raw
		case "usage":
			usageRaw = m.Raw
		case "timings":
			timingsRaw = m.Raw
		}
	}
	if !haveModel || len(answersRaw) == 0 || len(usageRaw) == 0 {
		return nil, errors.New(`response lacks one of "model", "answers", "usage"`)
	}
	var usage struct {
		InputTokens *int `json:"input_tokens"`
	}
	if err := json.Unmarshal(usageRaw, &usage); err != nil || usage.InputTokens == nil {
		return nil, errors.New(`response "usage.input_tokens" is missing`)
	}
	out.InputTokens = *usage.InputTokens

	answers, err := orderedMembers(answersRaw)
	if err != nil {
		return nil, fmt.Errorf(`response "answers" is not an object: %w`, err)
	}
	byID := make(map[string]json.RawMessage, len(answers))
	for _, a := range answers {
		byID[a.Key] = a.Raw
	}
	for _, q := range questions {
		raw, ok := byID[q.ID]
		if !ok {
			return nil, fmt.Errorf("no answer for question %q", q.ID)
		}
		a, err := parseAnswer(q, raw)
		if err != nil {
			return nil, fmt.Errorf("answer %q: %w", q.ID, err)
		}
		out.Answers = append(out.Answers, a)
	}
	if len(timingsRaw) > 0 {
		var t struct {
			PromptN  *int     `json:"prompt_n"`
			PromptMs *float64 `json:"prompt_ms"`
			HeadMs   float64  `json:"head_ms"`
			CacheN   int      `json:"cache_n"`
		}
		// A timings key that is not the engine's shape is no timing: the
		// card then says "client", never a half-reading of it.
		if json.Unmarshal(timingsRaw, &t) == nil && t.PromptN != nil && t.PromptMs != nil {
			out.Timings = &tape.DecisionServerTimings{
				PromptN: *t.PromptN, PromptMs: *t.PromptMs, HeadMs: t.HeadMs, CacheN: t.CacheN,
			}
		}
	}
	return &out, nil
}

func parseAnswer(q tape.DecisionQuestion, raw json.RawMessage) (tape.DecisionAnswer, error) {
	a := tape.DecisionAnswer{QuestionID: q.ID, Type: q.Type}
	var f struct {
		Type          string          `json:"type"`
		Choice        *string         `json:"choice"`
		Score         *float64        `json:"score"`
		Confidence    *float64        `json:"confidence"`
		Noul          *float64        `json:"noul"`
		Probabilities json.RawMessage `json:"probabilities"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return a, err
	}
	if f.Type != q.Type {
		return a, fmt.Errorf("type %q, asked as %q", f.Type, q.Type)
	}
	if q.Type == tape.DecisionNoul {
		if f.Noul == nil {
			return a, errors.New(`no "noul"`)
		}
		if !validP(*f.Noul) {
			return a, fmt.Errorf("noul %v is not a probability", *f.Noul)
		}
		a.Noul = f.Noul
		return a, nil
	}
	if f.Confidence == nil || len(f.Probabilities) == 0 {
		return a, errors.New(`lacks "confidence" or "probabilities"`)
	}
	a.Confidence = *f.Confidence
	probs, err := orderedMembers(f.Probabilities)
	if err != nil {
		return a, fmt.Errorf(`"probabilities" is not an object: %w`, err)
	}
	pByKey := make(map[string]float64, len(probs))
	for _, m := range probs {
		var p float64
		if err := json.Unmarshal(m.Raw, &p); err != nil || !validP(p) {
			return a, fmt.Errorf("probability of %q is not a number in [0, 1]", m.Key)
		}
		pByKey[m.Key] = p
	}
	for _, o := range q.Options {
		p, ok := pByKey[o.Key]
		if !ok {
			return a, fmt.Errorf("no probability for option %q", o.Key)
		}
		a.Probabilities = append(a.Probabilities, tape.DecisionProb{Key: o.Key, P: p})
		delete(pByKey, o.Key)
	}
	if len(pByKey) > 0 {
		return a, errors.New("probabilities for options the question did not offer")
	}
	switch q.Type {
	case tape.DecisionChoice:
		if f.Choice == nil {
			return a, errors.New(`no "choice"`)
		}
		found := false
		for _, o := range q.Options {
			found = found || o.Key == *f.Choice
		}
		if !found {
			return a, fmt.Errorf("choice %q is not an option", *f.Choice)
		}
		a.Choice = *f.Choice
	case tape.DecisionScore:
		if f.Score == nil {
			return a, errors.New(`no "score"`)
		}
		a.Score = *f.Score
	}
	return a, nil
}

func validP(p float64) bool { return !math.IsNaN(p) && p >= 0 && p <= 1 }

// EngineIdentity is what a decision server says about itself.
type EngineIdentity struct {
	// Kind is the engine's own name, lower-cased, the way DetectKind takes
	// an engine object's name; "" when the server named none.
	Kind tape.ServerKind
	// Build is the engine's version string verbatim, Commit the short hash
	// when a llama-style build_info carried one.
	Build, Commit string
	ModelPath     string // /props model_path, "" when absent
	ModelFile     string // base name of ModelPath, else the first /v1/models id
	Quant         string // "" when the server named none; never derived
}

// ProbeEngine reads GET /props and GET /v1/models for the identity. Both are
// optional: a server that answers neither with a 2xx returns the empty
// identity and no error, because the endpoint may still serve. Only a
// transport failure on both (nothing listening) is an error, wrapping
// ErrUnreachable.
//
// /props is read as a raw object, not as Props: a decision engine writes
// "engine" as a plain string ("engine":"bloomery"), and Props' engine object
// would reject the whole body. Both spellings are read.
func (c *Client) ProbeEngine(ctx context.Context) (EngineIdentity, error) {
	var id EngineIdentity
	propsBody, _, propsStatus, propsErr := c.getRaw(ctx, "/props")
	if propsErr == nil && propsStatus >= 200 && propsStatus < 300 {
		fillIdentity(&id, propsBody)
	}
	modelsBody, _, modelsStatus, modelsErr := c.getRaw(ctx, "/v1/models")
	if modelsErr == nil && modelsStatus >= 200 && modelsStatus < 300 && id.ModelFile == "" {
		var m ModelsResponse
		if json.Unmarshal(modelsBody, &m) == nil {
			id.ModelFile = m.FirstID()
		}
	}
	if propsErr != nil && modelsErr != nil {
		return id, fmt.Errorf("%w: %s: %v", ErrUnreachable, c.baseURL, propsErr)
	}
	return id, nil
}

func fillIdentity(id *EngineIdentity, body []byte) {
	var p struct {
		Engine    json.RawMessage `json:"engine"`
		Build     string          `json:"build"`
		BuildInfo string          `json:"build_info"`
		ModelPath string          `json:"model_path"`
		Model     json.RawMessage `json:"model"`
		Quant     string          `json:"quant"`
	}
	if json.Unmarshal(body, &p) != nil {
		return
	}
	var name, version, engineQuant string
	if len(p.Engine) > 0 {
		if json.Unmarshal(p.Engine, &name) != nil { // not a string: the object spelling
			var o EngineProps
			if json.Unmarshal(p.Engine, &o) == nil {
				name, version, engineQuant = o.Name, o.Version, o.Model.Quant
			}
		}
	}
	if name = strings.TrimSpace(name); name != "" {
		id.Kind = tape.ServerKind(strings.ToLower(name))
	}
	// A body that named no engine but stamps a build_info is mainline
	// llama-server — DetectKind's rule: every mainline build stamps one, and
	// ik_llama.cpp, which stamps neither an engine nor a build, is left for
	// the process to name (RefineKind, TTP-33).
	if name == "" && p.BuildInfo != "" {
		id.Kind = tape.ServerLlamaCPP
	}
	switch {
	case p.Build != "":
		id.Build = p.Build
	case version != "":
		id.Build = version
	case p.BuildInfo != "":
		id.Build, id.Commit = BuildFromProps(p.BuildInfo)
	}
	id.Quant = p.Quant
	if id.Quant == "" {
		id.Quant = engineQuant
	}
	if p.ModelPath != "" {
		id.ModelPath = p.ModelPath
		id.ModelFile = filepath.Base(p.ModelPath)
		return
	}
	// bloomery names the file alone, as a string "model"; any other
	// spelling of that key (an object) says nothing about the file.
	var file string
	if json.Unmarshal(p.Model, &file) == nil && strings.TrimSpace(file) != "" {
		id.ModelFile = filepath.Base(strings.TrimSpace(file))
	}
}

// DiscoverSystemOne finds a server by the same candidate list Discover scans,
// but accepts any candidate that answers /props or /v1/models with 2xx: a
// decision engine's /props may not be llama-server's shape. A candidate list
// of nil means DefaultCandidates.
func (c *Client) DiscoverSystemOne(ctx context.Context, candidates []string) (string, error) {
	if candidates == nil {
		candidates = DefaultCandidates
	}
	var firstErr error
	seen := map[string]bool{}
	for _, raw := range candidates {
		u := NormalizeBaseURL(raw)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		for _, path := range []string{"/props", "/v1/models"} {
			_, _, status, err := c.WithBaseURL(u).getRaw(ctx, path)
			if err == nil && status >= 200 && status < 300 {
				return u, nil
			}
			if firstErr == nil && err != nil {
				firstErr = err
			}
		}
	}
	if firstErr == nil {
		firstErr = errors.New("no candidate answered")
	}
	return "", fmt.Errorf("%w: server: discover: no server answered at %s: %v",
		ErrUnreachable, DefaultPorts(), firstErr)
}
