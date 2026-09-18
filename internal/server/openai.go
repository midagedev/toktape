package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// The generic OpenAI-compatible mode (TTP-99, 2026-09-19): vLLM, SGLang,
// TabbyAPI, LM Studio, mlx-lm and the rest answer GET /v1/models with 200 and
// stream POST /v1/chat/completions, but none of them answers /props, reports
// a timings object, or numbers slots. This file is that mode's discovery and
// nothing else; the request shape lives in stream.go (StreamRequest.Protocol)
// and the clock-as-record reduction in reduce.go and sse.go.

// ErrNoProps means /props answered 404 or 405: something is serving there,
// but it is not the llama-server protocol. It wraps ErrUnreachable so every
// existing errors.Is(err, ErrUnreachable) still holds; a caller that wants
// the second pass — trying /v1/models — branches on ErrNoProps, which fires
// only for those two statuses and never for a refusal, a timeout or a server
// that is still loading.
var ErrNoProps = fmt.Errorf("server: no /props: %w", ErrUnreachable)

// noPropsStatus reports whether status is the "not our protocol" answer:
// 404 (no such route) or 405 (route exists for another method). Anything
// else — a refusal, 503 while loading, a timeout — is a different verdict
// and must not become a second pass.
func noPropsStatus(status int) bool {
	return status == http.StatusNotFound || status == http.StatusMethodNotAllowed
}

// ModelsResponse is GET /v1/models: the models the server will generate as.
// Only id is read; owned_by rides along because servers send it and dropping
// a field nobody reads yet is how a later "which org" question goes
// unanswered. Anything else in the entries is ignored.
type ModelsResponse struct {
	Data []ModelEntry `json:"data"`
}

// ModelEntry is one entry of a /v1/models listing.
type ModelEntry struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
}

// FirstID is the model name the server itself gave: the first listing id.
// "" when the server listed nothing, which is an observation (it answered
// with an empty list) and not a crash.
func (m *ModelsResponse) FirstID() string {
	if m == nil || len(m.Data) == 0 {
		return ""
	}
	return m.Data[0].ID
}

// Models reads GET /v1/models. A non-2xx or non-JSON answer is ErrUnreachable
// with the URL and status: the candidate is not an OpenAI-compatible server,
// and the sentence says which URL failed and how.
//
// A data key that is not an array (a string, an object, null) is the same
// verdict, not a decode of half a listing: a stale or foreign schema must not
// become a model name nobody observed.
func (c *Client) Models(ctx context.Context) (*ModelsResponse, error) {
	body, _, status, err := c.getRaw(ctx, "/v1/models")
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUnreachable, c.baseURL, rootCause(err))
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("%w: %s: GET /v1/models: HTTP %d: %s",
			ErrUnreachable, c.baseURL, status, clip(strings.TrimSpace(string(body)), 200))
	}
	var out ModelsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: %s: /v1/models is not JSON: %v", ErrUnreachable, c.baseURL, err)
	}
	if !isJSONArrayField(body, "data") {
		return nil, fmt.Errorf("%w: %s: /v1/models data is not an array: %s",
			ErrUnreachable, c.baseURL, clip(strings.TrimSpace(string(body)), 200))
	}
	return &out, nil
}

// isJSONArrayField reports whether the top-level object in body carries key
// as a JSON array. Missing is valid (decodes to nil); null decodes to nil as
// well and is therefore valid too — only a present non-array is refused.
func isJSONArrayField(body []byte, key string) bool {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return false
	}
	raw, ok := top[key]
	if !ok {
		return true
	}
	trimmed := strings.TrimSpace(string(raw))
	// null decodes to a nil slice, which is the empty listing — an
	// observation, not a schema violation.
	return strings.HasPrefix(trimmed, "[") || trimmed == "null"
}
