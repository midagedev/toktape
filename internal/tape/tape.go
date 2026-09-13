// Package tape defines the run file: the product's core artifact.
//
// A Tape is everything one recorded request produced — the machine, the
// model, its placement, the process memory picture, the server's own
// timings, every generated token with its timestamp, and periodic samples
// of the host. Every renderer (card, TUI replay, GIF/mp4, compare) reads a
// Tape and nothing else, so a tape recorded on someone else's rig renders
// with the same look here.
//
// Design rules (each one came from a measured mistake — see
// docs/research/00-handover-brief.md, "What this machine taught us"):
//
//  1. Rates come from the server's timings object. Client-side rates are a
//     cross-check and must agree within RateTolerance.
//  2. A decode rate is not reported as "decode" below MinDecodeTokens.
//     Prompt-cache hits are recorded next to every rate.
//  3. RSS is not "loaded". NeverLoadedBytes comes from the GGUF header, never
//     from total-minus-RSS.
//  4. Whatever was sent (reasoning effort, template kwargs, </think>
//     presence) is recorded in Template.
//  5. Text width is measured with east-asian width; renderers, not the tape,
//     own that — but token Text is stored raw, ANSI-free.
//  6. A busy machine is labelled: Contention is always filled in.
//  7. Recording length is derived from token count and rate, so tokens carry
//     timestamps.
//  8. One tape per server; A/B is two tapes.
//  9. A run is N concurrent requests, not one. Agent workloads hit a server
//     with several sessions at once, so the tape records every request's
//     own stream and the aggregate (Concurrency, Aggregate). N=1 is the
//     special case, not the design.
package tape

import "time"

// SchemaVersion is bumped on any incompatible change to the JSON shape.
const SchemaVersion = 1

// Constants that encode the handover lessons. Change only with an attributed
// comment and a FAIL-first test.
const (
	// MinDecodeTokens: below this many generated tokens the rate is shown
	// but labelled "sample", never "decode". (Lesson 2: a 19-token sample is
	// not a decode rate.)
	MinDecodeTokens = 32
	// RateTolerance is the allowed relative difference between the client-side
	// rate and the server's predicted_per_second. (Lesson 1.)
	RateTolerance = 0.02
	// DefaultSampleInterval is how often RunSample rows are taken during a run.
	DefaultSampleInterval = 250 * time.Millisecond
	// ColdMajFaultsPerToken: at or above this average the run is labelled
	// cold (weights were being paged in from disk during decode).
	ColdMajFaultsPerToken = 1.0
)

// Tape is the run file. Serialised as gzip'd JSON by Write; read by Read.
type Tape struct {
	Schema  int        `json:"schema"`
	Summary RunSummary `json:"summary"`
	// Requests are the concurrent streams of this run, in start order.
	// len(Requests) == Summary.Concurrency. A single-stream run has one.
	Requests []RequestRecord `json:"requests"`
	Samples  []RunSample     `json:"samples"`
}

// RequestRecord is one stream: its prompt, every token, and its own timings.
type RequestRecord struct {
	Index     int              `json:"index"`      // 0..Concurrency-1
	Slot      int              `json:"slot"`       // server slot id when known, else -1
	StartedAt time.Duration    `json:"started_at"` // since run start
	Prompt    PromptRecord     `json:"prompt"`
	Tokens    []TokenEvent     `json:"tokens"`
	Progress  []PromptProgress `json:"progress,omitempty"`
	Timings   TimingsSummary   `json:"timings"`
	Cache     CacheSummary     `json:"cache"`
	Error     string           `json:"error,omitempty"` // non-empty when the stream failed
}

// RunSummary is everything the card needs. Renderers of the card must not
// need Tokens or Samples.
type RunSummary struct {
	ID             string    `json:"id"` // <yyyymmdd>-<hhmmss>-<short model slug>
	ToktapeVersion string    `json:"toktape_version"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`

	Server    ServerInfo       `json:"server"`
	Model     ModelInfo        `json:"model"`
	Host      HostInfo         `json:"host"`
	Placement PlacementSummary `json:"placement"`
	Memory    MemorySummary    `json:"memory"`
	// Concurrency is the number of streams sent at once. 1 = single session.
	Concurrency int `json:"concurrency"`
	// Timings is the representative single stream: Requests[0] when
	// Concurrency == 1, otherwise the per-stream mean. Aggregate is the
	// whole-server view.
	Timings    TimingsSummary   `json:"timings"`
	Aggregate  AggregateTimings `json:"aggregate"`
	Cache      CacheSummary     `json:"cache"`
	Contention ContentionInfo   `json:"contention"`
	Template   TemplateInfo     `json:"template"`
	GPUsAtEnd  []GPUSample      `json:"gpus_at_end,omitempty"`

	// Tag and Note label the experiment this run belongs to (`--tag ngl=40
	// --note "fa on"`). They are the user's words, recorded so the run ledger
	// (`toktape log`) can group a sweep; empty when not given.
	Tag  string `json:"tag,omitempty"`
	Note string `json:"note,omitempty"`
	// Warnings are human-readable caveats the card prints verbatim
	// ("cold run: 1.4 major faults per token", "pid not found, no /proc view").
	Warnings []string `json:"warnings,omitempty"`
}

// ServerKind identifies the serving engine.
type ServerKind string

const (
	ServerLlamaCPP ServerKind = "llama-server"
	ServerIKLlama  ServerKind = "ik_llama.cpp"
	ServerUnknown  ServerKind = "unknown"
)

// ServerInfo is what /props, the process command line and the build report.
type ServerInfo struct {
	Kind    ServerKind  `json:"kind"`
	URL     string      `json:"url"`
	Build   string      `json:"build,omitempty"`  // e.g. "b3650"
	Commit  string      `json:"commit,omitempty"` // short hash
	PID     int         `json:"pid,omitempty"`    // 0 when not found locally
	Host    string      `json:"host,omitempty"`   // hostname where the server runs
	Args    []string    `json:"args,omitempty"`   // full argv when the PID was found
	Flags   ServerFlags `json:"flags"`
	NSlots  int         `json:"n_slots,omitempty"`
	CtxSize int         `json:"ctx_size,omitempty"` // n_ctx from /props
}

// ServerFlags are the flags the card must always print (the five
// argument-starters from docs/research/02-sharing-artifacts.md §5.2).
// Empty string / zero means "unknown", and the card prints "?" — never a
// default it did not observe.
type ServerFlags struct {
	NGL          string   `json:"ngl,omitempty"`
	FlashAttn    string   `json:"fa,omitempty"` // "on" | "off" | "auto" | ""
	Batch        string   `json:"b,omitempty"`
	UBatch       string   `json:"ub,omitempty"`
	CacheTypeK   string   `json:"ctk,omitempty"`
	CacheTypeV   string   `json:"ctv,omitempty"`
	LoadMode     string   `json:"load_mode,omitempty"` // mmap | direct | dio ...
	OverrideTens []string `json:"ot,omitempty"`        // -ot patterns, verbatim
	CPUMoE       string   `json:"cpu_moe,omitempty"`   // -cmoe / -ncmoe N
	Threads      string   `json:"t,omitempty"`
	Other        []string `json:"other,omitempty"` // anything else worth printing, verbatim
}

// ModelInfo comes from the GGUF header (via /props model_path) and the file.
type ModelInfo struct {
	Path         string `json:"path"`
	FileName     string `json:"file_name"`
	Name         string `json:"name,omitempty"`  // general.name
	Arch         string `json:"arch,omitempty"`  // general.architecture
	Quant        string `json:"quant,omitempty"` // exact sub-type: Q4_K_M, IQ4_NL, UD-Q4_K_M — never "Q4"
	FileBytes    int64  `json:"file_bytes"`
	Params       int64  `json:"params,omitempty"`
	NLayers      int    `json:"n_layers,omitempty"`
	NExperts     int    `json:"n_experts,omitempty"`
	NExpertsUsed int    `json:"n_experts_used,omitempty"`
	CtxTrain     int    `json:"ctx_train,omitempty"`
	// ActiveBytesPerToken is the estimated bytes of weights touched per
	// decoded token (dense: all weights; MoE: attention + used experts).
	// Effective bandwidth = ActiveBytesPerToken * decode tok/s.
	ActiveBytesPerToken int64 `json:"active_bytes_per_token,omitempty"`
}

// HostInfo is the hardware line of the card.
type HostInfo struct {
	Hostname    string    `json:"hostname,omitempty"`
	OS          string    `json:"os"` // linux | darwin
	Kernel      string    `json:"kernel,omitempty"`
	CPU         string    `json:"cpu,omitempty"` // model name
	CPUCores    int       `json:"cpu_cores,omitempty"`
	CPUThreads  int       `json:"cpu_threads,omitempty"`
	RAMBytes    int64     `json:"ram_bytes"`
	RAMSpeed    string    `json:"ram_speed,omitempty"` // "DDR5-6000" when readable, else ""
	RAMChannels int       `json:"ram_channels,omitempty"`
	GPUs        []GPUInfo `json:"gpus,omitempty"`
}

// GPUInfo is static per-device info.
type GPUInfo struct {
	Index     int    `json:"index"`
	Name      string `json:"name"`
	VRAMBytes int64  `json:"vram_bytes"`
	Driver    string `json:"driver,omitempty"`
	PCIe      string `json:"pcie,omitempty"` // "4.0 x16" when readable
	// PeakBandwidthBytesPerSec is a table lookup by name; 0 when unknown.
	PeakBandwidthBytesPerSec int64 `json:"peak_bw_bps,omitempty"`
}

// GPUSample is one reading of one device.
type GPUSample struct {
	Index      int     `json:"index"`
	UsedBytes  int64   `json:"used_bytes"`
	ProcBytes  int64   `json:"proc_bytes,omitempty"` // VRAM held by the server PID, when known
	UtilPct    float64 `json:"util_pct,omitempty"`
	TempC      float64 `json:"temp_c,omitempty"`
	PowerW     float64 `json:"power_w,omitempty"`
	ClockMHz   int     `json:"clock_mhz,omitempty"`
	Throttled  bool    `json:"throttled,omitempty"`
	OtherProcs int     `json:"other_procs,omitempty"` // compute processes other than the server
}

// TensorClass buckets tensors for the placement view.
type TensorClass string

const (
	ClassAttention TensorClass = "attention"
	ClassExperts   TensorClass = "experts"
	ClassFFN       TensorClass = "ffn" // dense feed-forward
	ClassEmbed     TensorClass = "embeddings"
	ClassOutput    TensorClass = "output"
	ClassNGram     TensorClass = "ngram" // n-gram / engram tables
	ClassOther     TensorClass = "other"
)

// Device names used in placement.
const (
	DeviceCPU = "CPU" // host RAM
	// GPUs are "GPU0", "GPU1", ...
)

// DevicePlacement is the bytes of each tensor class on one device.
type DevicePlacement struct {
	Device  string                `json:"device"`
	Bytes   int64                 `json:"bytes"`
	Classes map[TensorClass]int64 `json:"classes"`
	Layers  string                `json:"layers,omitempty"` // "0-31" / "0-15,attn 16-31" — human summary
}

// PlacementSummary is the model's placement across devices.
type PlacementSummary struct {
	Devices []DevicePlacement `json:"devices"`
	// NeverLoadedBytes are tensors the server never reads (e.g. n-gram
	// tables under lazy mode). From the GGUF header. Lesson 3.
	NeverLoadedBytes int64 `json:"never_loaded_bytes"`
	// Source says how the placement was derived:
	// "gguf+args" (header + -ngl/-ot), "server-log", "estimate", "unknown".
	Source string `json:"source"`
	// VRAM breakdown when derivable: weights / kv cache / compute buffers.
	VRAMWeightsBytes int64 `json:"vram_weights_bytes,omitempty"`
	VRAMKVBytes      int64 `json:"vram_kv_bytes,omitempty"`
	VRAMComputeBytes int64 `json:"vram_compute_bytes,omitempty"`
}

// MemSample is the process memory picture at one instant (Linux /proc).
type MemSample struct {
	VirtBytes     int64  `json:"virt_bytes"`
	RSSBytes      int64  `json:"rss_bytes"`
	RSSFileBytes  int64  `json:"rss_file_bytes"`
	RSSAnonBytes  int64  `json:"rss_anon_bytes"`
	RSSShmemBytes int64  `json:"rss_shmem_bytes"`
	SwapBytes     int64  `json:"swap_bytes"`
	MajFaults     uint64 `json:"maj_faults"` // cumulative, /proc/<pid>/stat field 12
	MinFaults     uint64 `json:"min_faults"` // cumulative
}

// MemorySummary reduces the samples for the card.
type MemorySummary struct {
	AtEnd             MemSample `json:"at_end"`
	PeakRSSBytes      int64     `json:"peak_rss_bytes"`
	MappedFileBytes   int64     `json:"mapped_file_bytes"` // size of the model mapping(s)
	MajFaultsTotal    uint64    `json:"maj_faults_total"`  // during the run
	MajFaultsPrompt   uint64    `json:"maj_faults_prompt"` // before first token
	MajFaultsDecode   uint64    `json:"maj_faults_decode"` // first token → last
	MajFaultsPerToken float64   `json:"maj_faults_per_token"`
}

// TimingsSummary: server figures are the record, client figures the check.
type TimingsSummary struct {
	// From the server's final timings object.
	PromptN            int     `json:"prompt_n"`
	CacheN             int     `json:"cache_n"`
	PromptMs           float64 `json:"prompt_ms"`
	PromptPerSecond    float64 `json:"prompt_per_second"`
	PredictedN         int     `json:"predicted_n"`
	ReasoningN         int     `json:"reasoning_n,omitempty"` // reasoning tokens among PredictedN (thinking models)
	PredictedMs        float64 `json:"predicted_ms"`
	PredictedPerSecond float64 `json:"predicted_per_second"`
	// Speculative decoding, when the server reports it. nil = not reported.
	DraftN         *int `json:"draft_n,omitempty"`
	DraftNAccepted *int `json:"draft_n_accepted,omitempty"`

	// Client-side.
	TTFTMs                   float64 `json:"ttft_ms"` // request sent → first token (reasoning or answer)
	ClientPromptPerSecond    float64 `json:"client_prompt_per_second"`
	ClientPredictedPerSecond float64 `json:"client_predicted_per_second"` // decode window, reasoning tokens included
	ClientAgreesWithServer   bool    `json:"client_agrees_with_server"`   // within RateTolerance
	// DecodeLabel is "decode" when PredictedN >= MinDecodeTokens, else "sample".
	DecodeLabel string `json:"decode_label"`
	// Per-token latency percentiles from the token timeline (ms).
	ITLp50Ms float64 `json:"itl_p50_ms,omitempty"`
	ITLp95Ms float64 `json:"itl_p95_ms,omitempty"`
	ITLp99Ms float64 `json:"itl_p99_ms,omitempty"`
	// EffectiveBandwidthBytesPerSec = ActiveBytesPerToken * PredictedPerSecond.
	EffectiveBandwidthBytesPerSec int64 `json:"effective_bw_bps,omitempty"`
}

// AggregateTimings is the server-wide view of a concurrent run. With
// Concurrency == 1 it repeats Timings. The card prints
// "N × <per-stream> tok/s = <aggregate> tok/s" when N > 1.
type AggregateTimings struct {
	Streams                     int     `json:"streams"`
	StreamsFailed               int     `json:"streams_failed,omitempty"`
	WallMs                      float64 `json:"wall_ms"` // first request sent → last token received
	TotalPromptN                int     `json:"total_prompt_n"`
	TotalPredictedN             int     `json:"total_predicted_n"`
	AggregatePredictedPerSecond float64 `json:"aggregate_predicted_per_second"` // TotalPredictedN / decode window
	AggregatePromptPerSecond    float64 `json:"aggregate_prompt_per_second"`
	PerStreamPredictedPerSecond float64 `json:"per_stream_predicted_per_second"` // mean of streams
	TTFTp50Ms                   float64 `json:"ttft_p50_ms"`
	TTFTp95Ms                   float64 `json:"ttft_p95_ms"`
	// SlotsBusyMax is the highest number of busy slots observed via /slots.
	SlotsBusyMax int `json:"slots_busy_max,omitempty"`
	// Scaling = AggregatePredictedPerSecond / (single-stream rate * Streams)
	// when a single-stream baseline exists in the same run; 0 when unknown.
	Scaling float64 `json:"scaling,omitempty"`
}

// CacheLabel is the cold/warm verdict printed on the card.
type CacheLabel string

const (
	CacheCold   CacheLabel = "cold"   // maj faults during decode ≥ threshold
	CacheWarm   CacheLabel = "warm"   // no faulting, prompt evaluated
	CacheCached CacheLabel = "cached" // prompt (mostly) served from prefix cache
)

// CacheSummary is the prompt-cache picture.
type CacheSummary struct {
	HitTokens   int        `json:"hit_tokens"`   // cache_n
	PromptTotal int        `json:"prompt_total"` // cache_n + prompt_n
	HitRatio    float64    `json:"hit_ratio"`
	Label       CacheLabel `json:"label"`
}

// ContentionInfo labels the run as contended or not. Lesson 6.
type ContentionInfo struct {
	Contended     bool     `json:"contended"`
	Reasons       []string `json:"reasons,omitempty"` // "loadavg 12.3 > cores 8", "2 other GPU procs"
	LoadAvg1      float64  `json:"loadavg1"`
	OtherGPUProcs int      `json:"other_gpu_procs"`
}

// TemplateInfo records what was actually sent. Lesson 4.
type TemplateInfo struct {
	ChatTemplate          string            `json:"chat_template,omitempty"` // name or "custom"
	ReasoningEffort       string            `json:"reasoning_effort,omitempty"`
	TemplateKwargs        map[string]string `json:"template_kwargs,omitempty"`
	RenderedHasThinkClose bool              `json:"rendered_has_think_close"` // "</think>" present in rendered prompt
	RenderedPromptSHA256  string            `json:"rendered_prompt_sha256,omitempty"`
	RenderedPromptTokens  int               `json:"rendered_prompt_tokens,omitempty"`
}

// PromptRecord is the request as sent and the answer as received.
type PromptRecord struct {
	Messages       []Message      `json:"messages"`
	RenderedPrompt string         `json:"rendered_prompt,omitempty"` // from /apply-template
	Params         map[string]any `json:"params,omitempty"`          // temperature, n_predict, ...
	Completion     string         `json:"completion"`                // concatenated answer token text (reasoning excluded)
	Reasoning      string         `json:"reasoning,omitempty"`       // concatenated reasoning_content text
	ReasoningN     int            `json:"reasoning_n,omitempty"`     // reasoning tokens among the predicted ones
	FinishReason   string         `json:"finish_reason,omitempty"`
}

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// TokenEvent is one generated token. T is time since the request was sent.
type TokenEvent struct {
	T     time.Duration `json:"t"`
	Index int           `json:"i"`
	Text  string        `json:"text"`
	// Reasoning marks a token the server emitted as reasoning_content
	// (thinking models: DeepSeek, Qwen3 …). The server counts it in
	// predicted_n exactly like an answer token, so it is a decode token for
	// every rate and for TTFT; only the transcript keeps it apart.
	Reasoning bool `json:"r,omitempty"`
	// Server-side cumulative figures from timings_per_token, when present.
	PredictedN  int     `json:"pn,omitempty"`
	PredictedMs float64 `json:"pms,omitempty"`
	// Host counters read when this token arrived.
	MajFaultsDelta uint64 `json:"mf,omitempty"` // since previous token
}

// RunSample is one periodic host reading. T is time since the request was sent.
type RunSample struct {
	T           time.Duration `json:"t"`
	Mem         MemSample     `json:"mem"`
	GPUs        []GPUSample   `json:"gpus,omitempty"`
	LoadAvg1    float64       `json:"loadavg1"`
	TokensSoFar int           `json:"tokens_so_far"`        // across all streams
	SlotsBusy   int           `json:"slots_busy,omitempty"` // from /slots, when polled
}

// PromptProgress is one return_progress event from the server.
type PromptProgress struct {
	T         time.Duration `json:"t"`
	Total     int           `json:"total"`
	Cache     int           `json:"cache"`
	Processed int           `json:"processed"`
	TimeMs    float64       `json:"time_ms"`
}
