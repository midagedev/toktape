package recorder

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// An embedding-model run (2026-10-15): every input of the set is sent on its
// own, one after another, to /v1/embeddings (OpenAI shape) or /embedding
// (llama-server's shape), timed send to the last byte, and the returned
// vector is kept on the tape. One pass, no concurrency: the tape is for
// checking what the vectors are, and the latency beside them is a note.

//go:embed embedding_inputs.jsonl
var defaultEmbeddingInputs []byte

// EmbeddingOptions configures one embedding run.
type EmbeddingOptions struct {
	BaseURL    string
	Candidates []string
	// API is tape.EmbeddingAPIOpenAI (the default) or tape.EmbeddingAPILlama.
	API string
	// Inputs is a JSONL set ({"id":..., "input":...} per line), InputsName
	// its file name; nil is the built-in set.
	Inputs     []byte
	InputsName string
	// Model, Dimensions and EncodingFormat are sent as given on the OpenAI
	// shape ("" / 0 = not sent; EncodingFormat "" is "float").
	Model          string
	Dimensions     int
	EncodingFormat string
	// VectorCap keeps at most this many values per vector; 0 keeps all.
	VectorCap int
	// Gap is idle time after each answer, so a replay at 1:1 shows the
	// inputs arrive one by one; it is on the timestamps and in no latency.
	Gap time.Duration

	Tag, Note string
	Version   string
	Clock     Clock
	FSRoot    string
	GPU       gpu.Collector
	HostLabel HostLabel
	HTTP      *http.Client
	Progress  func(tape.EmbeddingRecord)
}

type embeddingInput struct{ ID, Input string }

func loadEmbeddingInputs(raw []byte) ([]embeddingInput, error) {
	var out []embeddingInput
	seen := map[string]bool{}
	for n, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var row struct {
			ID    string `json:"id"`
			Input string `json:"input"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, fmt.Errorf("inputs line %d: %w", n+1, err)
		}
		if row.Input == "" {
			return nil, fmt.Errorf(`inputs line %d: no "input"`, n+1)
		}
		if row.ID == "" {
			row.ID = fmt.Sprintf("input-%d", len(out)+1)
		}
		if seen[row.ID] {
			return nil, fmt.Errorf("inputs line %d: id %q is used twice", n+1, row.ID)
		}
		seen[row.ID] = true
		out = append(out, embeddingInput{row.ID, row.Input})
	}
	if len(out) == 0 {
		return nil, errors.New("the input set is empty")
	}
	return out, nil
}

// RecordEmbedding runs one embedding pass and returns its tape.
func RecordEmbedding(ctx context.Context, opts EmbeddingOptions) (*tape.Tape, error) {
	if opts.API == "" {
		opts.API = tape.EmbeddingAPIOpenAI
	}
	if opts.API != tape.EmbeddingAPIOpenAI && opts.API != tape.EmbeddingAPILlama {
		return nil, fmt.Errorf("api %q: use openai or llama", opts.API)
	}
	if opts.EncodingFormat == "" {
		opts.EncodingFormat = "float"
	}
	if opts.EncodingFormat != "float" && opts.EncodingFormat != "base64" {
		return nil, fmt.Errorf("encoding format %q: use float or base64", opts.EncodingFormat)
	}
	if opts.Clock == nil {
		opts.Clock = systemClock{}
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: 5 * time.Minute}
	}
	raw := opts.Inputs
	if raw == nil {
		raw = defaultEmbeddingInputs
	}
	inputs, err := loadEmbeddingInputs(raw)
	if err != nil {
		return nil, err
	}

	client := server.New(opts.BaseURL)
	base := client.BaseURL()
	if base == "" {
		if base, err = client.Discover(ctx, opts.Candidates); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
		}
	}
	id, err := client.WithBaseURL(base).ProbeEngine(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	host, warnings := collectDecisionHost(ctx, DecisionOptions{FSRoot: opts.FSRoot, GPU: opts.GPU, HostLabel: opts.HostLabel}, base)

	endpoint := "/v1/embeddings"
	if opts.API == tape.EmbeddingAPILlama {
		endpoint = "/embedding"
	}
	startedAt := opts.Clock.Now()
	t0 := time.Now()
	var recs []tape.EmbeddingRecord
	for i, in := range inputs {
		if ctx.Err() != nil {
			break
		}
		rec := sendEmbedding(ctx, opts, base+endpoint, t0, in)
		rec.Index = i
		recs = append(recs, rec)
		if opts.Progress != nil {
			opts.Progress(rec)
		}
		if opts.Gap > 0 && i < len(inputs)-1 && !sleepCtx(ctx, opts.Gap) {
			break
		}
	}
	finishedAt := opts.Clock.Now()

	sum := &tape.EmbeddingSummary{
		API: opts.API, Endpoint: endpoint, Model: opts.Model, Dimensions: opts.Dimensions,
		EncodingFormat: opts.EncodingFormat, Inputs: len(recs), VectorCap: opts.VectorCap,
	}
	if opts.API == tape.EmbeddingAPILlama {
		sum.EncodingFormat = "" // the llama shape has no such field
	}
	if opts.Inputs != nil {
		sum.InputSet = filepath.Base(opts.InputsName)
	}
	var lat []float64
	firstErr := ""
	for _, r := range recs {
		if r.Error != "" {
			sum.Errors++
			if firstErr == "" {
				firstErr = r.Error
			}
			continue
		}
		if sum.Dim == 0 {
			sum.Dim = r.Dim
		} else if r.Dim != sum.Dim {
			warnings = append(warnings, fmt.Sprintf("%s answered %d values, the first answer had %d", r.ID, r.Dim, sum.Dim))
		}
		sum.PromptTokens += r.PromptTokens
		lat = append(lat, float64(r.Latency())/float64(time.Millisecond))
	}
	if len(lat) == 0 {
		if firstErr == "" {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %s", ErrAllStreamsFailed, firstErr)
	}
	sort.Float64s(lat)
	sum.LatencyP50Ms, sum.LatencyMaxMs = lat[len(lat)/2], lat[len(lat)-1]
	if sum.Errors > 0 {
		warnings = append(warnings, fmt.Sprintf("%d of %d inputs failed; first: %s", sum.Errors, len(recs), firstErr))
	}

	slug := id.ModelFile
	if slug == "" {
		slug = opts.Model
	}
	if slug == "" {
		slug = "embedding"
	}
	summary := tape.RunSummary{
		ID:             runID(startedAt, slug),
		ToktapeVersion: opts.Version,
		StartedAt:      startedAt,
		FinishedAt:     finishedAt,
		Server:         tape.ServerInfo{Kind: id.Kind, URL: base, Host: host.Hostname, Build: id.Build, Commit: id.Commit},
		Model:          tape.ModelInfo{Path: id.ModelPath, FileName: id.ModelFile, Quant: id.Quant},
		Host:           host,
		Concurrency:    1,
		Warnings:       warnings,
		Mode:           tape.ModeEmbedding,
		Embedding:      sum,
		Tag:            opts.Tag,
		Note:           opts.Note,
	}
	return &tape.Tape{Schema: tape.SchemaVersion, Summary: summary, Embeddings: recs}, nil
}

func sendEmbedding(ctx context.Context, opts EmbeddingOptions, url string, t0 time.Time, in embeddingInput) tape.EmbeddingRecord {
	rec := tape.EmbeddingRecord{ID: in.ID, Input: in.Input}
	var body map[string]any
	if opts.API == tape.EmbeddingAPILlama {
		body = map[string]any{"content": in.Input}
	} else {
		body = map[string]any{"input": in.Input, "encoding_format": opts.EncodingFormat}
		if opts.Model != "" {
			body["model"] = opts.Model
		}
		if opts.Dimensions > 0 {
			body["dimensions"] = opts.Dimensions
		}
	}
	buf, _ := json.Marshal(body)
	rec.SentAt = time.Since(t0)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		rec.Error = err.Error()
		return rec
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := opts.HTTP.Do(req)
	if err != nil {
		rec.Error = err.Error()
		return rec
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	answered := time.Since(t0)
	if err != nil {
		rec.Error = err.Error()
		return rec
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rec.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 200)])))
		return rec
	}
	vec, tokens, err := parseEmbedding(raw)
	if err != nil {
		rec.Error = err.Error()
		return rec
	}
	rec.AnsweredAt = answered
	rec.PromptTokens = tokens
	rec.Dim = len(vec)
	rec.Norm = tape.L2Norm(vec)
	rec.Head = append([]float32(nil), vec[:min(len(vec), tape.EmbeddingHead)]...)
	keep := vec
	if opts.VectorCap > 0 && len(keep) > opts.VectorCap {
		keep = keep[:opts.VectorCap]
	}
	rec.Vector, rec.Stored = tape.EncodeVector(keep), len(keep)
	return rec
}

// parseEmbedding reads the first vector out of either response shape:
// OpenAI {"data":[{"embedding":[...] or "base64"}],"usage":{...}}, or
// llama-server's /embedding [{"index":0,"embedding":[[...]]}] (a pooled
// vector is one row) and its older {"embedding":[...]}.
func parseEmbedding(raw []byte) ([]float32, int, error) {
	var oa struct {
		Data []struct {
			Embedding json.RawMessage `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &oa) == nil && len(oa.Data) > 0 {
		v, err := vectorOf(oa.Data[0].Embedding)
		return v, oa.Usage.PromptTokens, err
	}
	var arr []struct {
		Embedding json.RawMessage `json:"embedding"`
	}
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
		v, err := vectorOf(arr[0].Embedding)
		return v, 0, err
	}
	var one struct {
		Embedding json.RawMessage `json:"embedding"`
	}
	if json.Unmarshal(raw, &one) == nil && len(one.Embedding) > 0 {
		v, err := vectorOf(one.Embedding)
		return v, 0, err
	}
	return nil, 0, errors.New("the response holds no embedding")
}

// vectorOf reads a vector written as floats, as base64 float32, or as a
// one-row matrix of floats.
func vectorOf(m json.RawMessage) ([]float32, error) {
	var s string
	if json.Unmarshal(m, &s) == nil {
		return tape.DecodeVector(s)
	}
	var f []float64
	if json.Unmarshal(m, &f) != nil {
		var rows [][]float64
		if err := json.Unmarshal(m, &rows); err != nil || len(rows) == 0 {
			return nil, errors.New("embedding is neither a vector nor base64")
		}
		if len(rows) > 1 {
			return nil, fmt.Errorf("embedding has %d rows: the server is not pooling (start it with --pooling)", len(rows))
		}
		f = rows[0]
	}
	if len(f) == 0 {
		return nil, errors.New("embedding is empty")
	}
	v := make([]float32, len(f))
	for i, x := range f {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, fmt.Errorf("embedding value %d is %v", i, x)
		}
		v[i] = float32(x)
	}
	return v, nil
}
