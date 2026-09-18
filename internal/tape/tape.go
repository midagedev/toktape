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
	// MinPrefillPromptTokens is the shortest prompt whose prefill rate the
	// card will present as a prefill measurement (TTP-65, 2026-09-14).
	//
	// Below it the figure is dominated by everything that is not prefill: the
	// batch the server was in the middle of, the slot it had to be given, the
	// first-token latency of a template it had already cached. A four-stream
	// recording printed "Prefill 5.6 tok/s · TTFT 11942 ms · 63 prompt tokens"
	// for a box whose honest prefill on the same model is 60 to 130 tok/s —
	// two orders of nothing, from four 63-token requests that arrived at once.
	//
	// 100 is the round number just above that 63 and an order of magnitude
	// under the 512-token prompt the fixtures use. The rule it encodes is
	// MinDecodeTokens' own, one step up the pipeline: a measurement too small
	// to be dominated by the thing it is measuring is not that measurement.
	MinPrefillPromptTokens = 100
	// MinCutTokens is the floor a run's wall-clock budget will not cut under
	// (TTP-76, 2026-09-14). A budget on a slow box can land a stream under
	// MinDecodeTokens, and a first run that reports "sample" instead of a
	// decode rate is the poor first run the budget exists to prevent, so the
	// clock waits: it cuts once every live stream has this many tokens or has
	// ended on its own. Twice MinDecodeTokens, so the rate is a rate with room
	// to spare rather than one sitting on the threshold.
	MinCutTokens = 2 * MinDecodeTokens
	// RateTolerance is the allowed relative difference between the client-side
	// rate and the server's predicted_per_second. (Lesson 1.)
	RateTolerance = 0.02
	// DefaultSampleInterval is how often RunSample rows are taken during a run.
	DefaultSampleInterval = 250 * time.Millisecond
	// ColdMajFaultsPerToken: at or above this average the run is labelled
	// cold (weights were being paged in from disk during decode).
	ColdMajFaultsPerToken = 1.0
	// ContendedIOSomeAvg10 is the /proc/pressure/io "some avg10" above which a
	// witness marks the run contended (TTP-36, 2026-09-13). A model load pins
	// IO pressure high from its first second, long before the load average
	// moves; the threshold is the one rig-log's quiet-machine protocol uses.
	ContendedIOSomeAvg10 = 5.0
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
	Index     int              `json:"index"`           // 0..Concurrency-1 within its round
	Round     int              `json:"round,omitempty"` // 0-based sequential round (TTP-31)
	Slot      int              `json:"slot"`            // server slot id when known, else -1
	StartedAt time.Duration    `json:"started_at"`      // since run start
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
	Timings   TimingsSummary   `json:"timings"`
	Aggregate AggregateTimings `json:"aggregate"`
	// Rounds is the number of sequential prompt rounds (TTP-31, 2026-09-13):
	// `record --prompts file.jsonl` sends each line as its own round of
	// Concurrency streams, all into this one tape. 0 or 1 = a single round,
	// and PerRound / Spread are then empty. Timings and Aggregate are over
	// every stream of every round; Aggregate.WallMs is the sum of the rounds'
	// own windows, never the gaps between them.
	Rounds   int            `json:"rounds,omitempty"`
	PerRound []RoundSummary `json:"per_round,omitempty"`
	// SpecNMax are the speculative.n_max values of a `record --spec-n-max`
	// sweep, in the order they ran (TTP-35, 2026-09-13). Each value runs the
	// whole prompt set, so Rounds = len(SpecNMax) x prompts, and BySpecNMax
	// is the spread per value. Empty = no sweep.
	SpecNMax   []int           `json:"spec_n_max,omitempty"`
	BySpecNMax []SpecNMaxGroup `json:"by_spec_n_max,omitempty"`
	Spread     *RoundSpread    `json:"spread,omitempty"` // nil for a single round
	Cache      CacheSummary    `json:"cache"`
	Contention ContentionInfo  `json:"contention"`
	Template   TemplateInfo    `json:"template"`
	// Sampling is how the run's requests were shaped (TTP-55, 2026-09-14).
	// One run sends one shape, so it is a run-level field even though it is
	// recorded per request; renderers read a RunSummary and nothing else.
	Sampling SamplingSummary `json:"sampling,omitempty"`
	// Limit is what could end this run's generation and what did (TTP-76).
	Limit     LimitSummary `json:"limit,omitempty"`
	GPUsAtEnd []GPUSample  `json:"gpus_at_end,omitempty"`

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
	// ServerExLlamaV3 is an ExLlamaV3 engine behind a llama-server-compatible
	// HTTP front (2026-09-15). exllamav3 ships no HTTP server of its own, so
	// the kind is only ever stamped from /props' engine.name, never guessed
	// from a process name. Its model, placement and flags come from that same
	// engine block (EngineSource), not from a GGUF or an argv.
	ServerExLlamaV3 ServerKind = "exllamav3"
	ServerUnknown   ServerKind = "unknown"
)

// SelfDeclared reports whether k was taken from a /props engine object — the
// engine named itself, in the one field that exists for saying so.
//
// The llama.cpp family is a closed set: mainline is ServerLlamaCPP, ik is
// ServerIKLlama, and a server that answered /props without saying what it is
// is ServerUnknown. Any other kind is a name an engine gave, which is why
// this is a test on the set rather than a flag: a tape recorded before this
// method existed answers it correctly, because "exllamav3" was already the
// engine's own name and was already outside the set.
//
// What follows from a true answer is all one fact — the engine, not the
// process and not llama.cpp's conventions, is the record: its version is the
// build with no commit to name, its args are flags.Other verbatim, and
// server.ParseFlags must not be run over them (2026-09-17, TTP-105).
func (k ServerKind) SelfDeclared() bool {
	switch k {
	case "", ServerUnknown, ServerLlamaCPP, ServerIKLlama:
		return false
	}
	return true
}

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
	// Speculative decoding (TTP-30, 2026-09-13). DraftModel is the -md /
	// --model-draft argument's base name; the block size and thresholds are
	// verbatim. "" = not passed (the server's default), and the card omits
	// the row when no draft was involved.
	DraftModel string   `json:"model_draft,omitempty"`
	DraftMax   string   `json:"draft_max,omitempty"`   // --draft-max / --draft / --draft-n
	DraftMin   string   `json:"draft_min,omitempty"`   // --draft-min / --draft-n-min
	DraftPMin  string   `json:"draft_p_min,omitempty"` // --draft-p-min
	Other      []string `json:"other,omitempty"`       // anything else worth printing, verbatim
}

// ModelInfo comes from the GGUF header (via /props model_path) and the file.
type ModelInfo struct {
	Path     string `json:"path"`
	FileName string `json:"file_name"`
	// Dir is the base name of the directory holding the file (TTP-32,
	// 2026-09-13). Variants of one model — a hard-linked shard set with a
	// different embedding quant — often share every file name and differ
	// only here, so the card keeps it when the file is sharded.
	Dir string `json:"dir,omitempty"`
	// Shards is N when FileName is one part of an -00001-of-0000N set;
	// FileBytes is then the sum of all N parts. 0 = a single file.
	Shards int `json:"shards,omitempty"`
	// Format is the weight format when it is not GGUF: "exl3" (2026-09-15).
	// "" is GGUF, which every tape before this field was. A non-GGUF model's
	// shape is what its engine reported, never a header this recorder read.
	Format       string `json:"format,omitempty"`
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
	// Hostname is the machine's name, and HostnameSource says whether anyone
	// read it off the machine. A tape is meant to be posted, and a hostname is
	// the one field in it that identifies a place rather than a measurement —
	// rig-log's rule is that nothing public carries one, and before
	// `record --host-label` their only way to honour it was to gunzip a tape,
	// rewrite two fields and re-render (TTP-93).
	//
	// A label is not an observation, which is why the source travels beside it
	// for the same reason RAMSource travels beside RAMBytesPerSec below: two
	// runs labelled "workstation" are not evidence they ran on one machine,
	// and nothing downstream may read them as such. internal/compare is where
	// that matters today.
	Hostname       string `json:"hostname,omitempty"`
	HostnameSource string `json:"hostname_source,omitempty"`
	OS             string `json:"os"` // linux | darwin
	Kernel         string `json:"kernel,omitempty"`
	CPU            string `json:"cpu,omitempty"` // model name
	CPUCores       int    `json:"cpu_cores,omitempty"`
	CPUThreads     int    `json:"cpu_threads,omitempty"`
	RAMBytes       int64  `json:"ram_bytes"`
	RAMSpeed       string `json:"ram_speed,omitempty"` // "DDR5-6000" when readable, else ""
	RAMChannels    int    `json:"ram_channels,omitempty"`
	// RAMBytesPerSec is the host's memory bandwidth, and RAMSource says where
	// the figure came from. Together they are the host leg of the bandwidth
	// ceiling, which decides whether the card can print an "of peak" ratio on
	// an offloaded run — the very case the ratio exists for.
	//
	// They are separate from RAMSpeed/RAMChannels because on Linux those two
	// are never readable: they live in the DMI tables and
	// /sys/firmware/dmi/tables/DMI is mode 0400 root-only, so a real
	// recording has no host ceiling at all (TTP-45). The way out is to let
	// the operator state it — and then the card must never present a stated
	// figure as an observed one, which is what RAMSource is for. Unknown
	// stays "" and 0 and prints as "?", as everywhere else.
	RAMBytesPerSec int64     `json:"ram_bytes_per_sec,omitempty"`
	RAMSource      string    `json:"ram_source,omitempty"`
	GPUs           []GPUInfo `json:"gpus,omitempty"`
}

// Where a hostname came from. Unknown stays "" — which is both "not recorded"
// and "this tape predates the field", and those are the same bytes, so only
// HostnameLabelled asserts anything.
const (
	// HostnameObserved: read off the machine, /proc/sys/kernel/hostname.
	HostnameObserved = "observed"
	// HostnameLabelled: the operator named it (record --host-label). It says
	// nothing about which machine this was.
	HostnameLabelled = "labelled"
)

// Where a host bandwidth figure came from. The card says which, because a
// theoretical peak, a number the operator typed and a STREAM run are three
// different claims and only the last one is a measurement of this machine.
const (
	// RAMSourceDMI: derived from RAMSpeed × RAMChannels read off the machine
	// — the theoretical peak of the modules that are actually fitted.
	RAMSourceDMI = "dmi"
	// RAMSourceStated: the operator named it (record --ram-gbs / --ram-speed
	// with --ram-channels). Observed from the user, not from the machine.
	RAMSourceStated = "stated"
	// RAMSourceMeasured: a benchmark figure the operator supplied, e.g. what
	// STREAM reports. Lower than the peak and the honest denominator.
	RAMSourceMeasured = "measured"
)

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
	Index     int     `json:"index"`
	UsedBytes int64   `json:"used_bytes"`
	ProcBytes int64   `json:"proc_bytes,omitempty"` // VRAM held by the server PID, when known
	UtilPct   float64 `json:"util_pct,omitempty"`
	TempC     float64 `json:"temp_c,omitempty"`
	PowerW    float64 `json:"power_w,omitempty"`
	ClockMHz  int     `json:"clock_mhz,omitempty"`
	// PowerLimitW is the board's enforced power limit (nvidia-smi
	// power.limit), the denominator the draw above only means anything
	// against (lead, 2026-09-15). A sweep on one A6000 drew 281 W of 300,
	// 199 of 200 and 150 of 150, and every run reported Throttled: a card
	// that says "throttled: yes" for all three has told the reader nothing,
	// while "281 of 300 W" and "150 of 150 W" are different sentences. 0 =
	// not read.
	PowerLimitW float64 `json:"power_limit_w,omitempty"`
	Throttled   bool    `json:"throttled,omitempty"`
	// ThrottleMask is gpu.Throttle* — the reasons behind Throttled, kept so a
	// surprising verdict can be explained from the tape instead of from the
	// box it was recorded on. 0 = none set, or a tape older than the field.
	ThrottleMask uint64 `json:"throttle_mask,omitempty"`
	OtherProcs   int    `json:"other_procs,omitempty"` // compute processes other than the server
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
	// ActiveBytesPerToken is what this device is actually read for on one
	// token — the same question ModelInfo.ActiveBytesPerToken asks of the
	// whole model, asked of one device's own tensors, so the per-device
	// figures sum to it exactly.
	//
	// It cannot be derived from Classes (TTP-68): a sparse MoE's router
	// (ffn_gate_inp) and its shared expert sit in ClassExperts and are read
	// in full every token, so applying the sparse fraction to the class total
	// under-counts the CPU by 831,160,320 bytes a token on the ws model —
	// 10.7 %, past bandwidth.SplitTolerance, which made the card print no
	// "of peak" ratio at all. Only the tensor names carry the distinction,
	// and they are gone by the time a tape is read. So the recorder answers
	// the question while it still has them.
	//
	// Zero means a tape recorded before this field existed; a reader falls
	// back to the class-proportion estimate rather than reporting nothing.
	ActiveBytesPerToken int64 `json:"active_bytes_per_token,omitempty"`
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
	// CPUSeconds is the server process's cumulative CPU time, user plus
	// system, /proc/<pid>/stat fields 14 and 15 over the clock tick (TTP-39,
	// 2026-09-13). Two samples' difference over their interval is the
	// process's CPU use in cores; 0 means it was not read (macOS, remote).
	CPUSeconds float64 `json:"cpu_s,omitempty"`
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
	// Per request: the final timings' figures. Run level (TTP-30): the SUM
	// over streams, so accepted/drafted is the pooled acceptance rate — unlike
	// the neighbouring per-stream means.
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
	Streams         int     `json:"streams"`
	StreamsFailed   int     `json:"streams_failed,omitempty"`
	WallMs          float64 `json:"wall_ms"` // first request sent → last token received
	TotalPromptN    int     `json:"total_prompt_n"`
	TotalPredictedN int     `json:"total_predicted_n"`
	// MinPredictedN is the fewest tokens any answered stream generated
	// (lead, 2026-09-14). Timings.PredictedN is the per-stream MEAN above one
	// stream, so two streams of 10 and 300 tokens average 155 and clear
	// MinDecodeTokens while one of them is a sample. A reader asking "was any
	// stream too short to be a rate" needs the minimum, and the summary is
	// all a renderer reads. 0 on a tape older than the field: unknown.
	MinPredictedN int `json:"min_predicted_n,omitempty"`
	// ShortStreams is how many answered streams generated fewer than
	// MinDecodeTokens (TTP-85, lead, 2026-09-14). MinPredictedN says the
	// shortest stream was too short to be a rate; this says how many were, so
	// a reader is told "2 of 4 streams" rather than only "the shortest". It is
	// only meaningful beside MinPredictedN: on a tape older than both it is 0
	// and so is MinPredictedN, which reads as unknown; with MinPredictedN
	// recorded, 0 here is an observed zero.
	ShortStreams                int     `json:"short_streams,omitempty"`
	AggregatePredictedPerSecond float64 `json:"aggregate_predicted_per_second"` // TotalPredictedN / decode window
	AggregatePromptPerSecond    float64 `json:"aggregate_prompt_per_second"`
	PerStreamPredictedPerSecond float64 `json:"per_stream_predicted_per_second"` // mean of streams
	TTFTp50Ms                   float64 `json:"ttft_p50_ms"`
	TTFTp95Ms                   float64 `json:"ttft_p95_ms"`
	// SlotsBusyMax is the highest number of busy slots observed via /slots.
	SlotsBusyMax int `json:"slots_busy_max,omitempty"`
	// PeakDecodingStreams is the most answered streams that were decoding at
	// one instant (lead, 2026-09-15). SlotsBusyMax is the server's word;
	// this is the token timeline's. They part when an engine takes N requests
	// into N slots but runs one job at a time. A 2-session ExLlamaV3 take did
	// that: slots busy 2, stream 1 decoded 4.3 s → 101.5 s, stream 0's
	// first token came at 105.2 s, and the aggregate was one stream behind
	// a queue. A stream's decode window runs from its second token to its
	// second-to-last (RequestRecord.StartedAt + TokenEvent.T), so a stream
	// starting on the token another one ends on is not overlap. A stream
	// with fewer than three tokens has no window. With rounds it is the lowest
	// per-round peak, because one serial round already makes the aggregate a
	// queue. 0 = unknown: a tape older than the field, or no stream had a
	// window.
	PeakDecodingStreams int `json:"peak_decoding_streams,omitempty"`
	// DisagreeingStreams is how many answered streams had a client rate the
	// server's own figure did not confirm within RateTolerance (lead,
	// 2026-09-15). The run-level TimingsSummary above one stream is a mean,
	// and a mean of agreeing and disagreeing streams can itself agree: a
	// two-stream take read 11.79 against 11.49 on one stream (2.6 %) and
	// 11.57 against 11.58 on the other, and the means agreed to 1.3 %. The
	// caveat used to fire on the AND of the per-stream flags while printing
	// those means, so the card contradicted its own numbers. This is the
	// count the caveat says out loud instead. 0 = every answered stream
	// agreed, or a tape older than the field.
	DisagreeingStreams int `json:"disagreeing_streams,omitempty"`
	// Scaling = AggregatePredictedPerSecond / (single-stream rate * Streams)
	// when a single-stream baseline exists in the same run; 0 when unknown.
	Scaling float64 `json:"scaling,omitempty"`
}

// RoundSummary is one sequential prompt round of a multi-prompt run (TTP-31).
// The figures are the same reductions the run-level Timings and Aggregate
// use, restricted to the round's streams, so a round is a run in miniature.
type RoundSummary struct {
	Index int `json:"index"`
	// SpecNMax is the speculative.n_max this round was sent with in a sweep
	// (TTP-35); 0 = not overridden.
	SpecNMax int `json:"spec_n_max,omitempty"`
	// Name is the JSONL line's "name" when it had one, else "" and the card
	// labels the round by its 1-based number.
	Name    string `json:"name,omitempty"`
	Streams int    `json:"streams"`
	// PerStreamPredictedPerSecond is the mean over the round's streams;
	// AggregatePredictedPerSecond the round's server-wide rate.
	PerStreamPredictedPerSecond float64 `json:"per_stream_predicted_per_second"`
	AggregatePredictedPerSecond float64 `json:"aggregate_predicted_per_second"`
	PredictedN                  int     `json:"predicted_n"` // sum over streams
	TTFTp50Ms                   float64 `json:"ttft_p50_ms"`
	// PromptN is the prompt tokens the server evaluated in this round, summed
	// over its streams (timings.prompt_n; a cached prefix is not in it), and
	// PromptPerSecond is those tokens over the server's own prompt_ms summed
	// the same way (TTP-64, lead, 2026-09-14).
	//
	// Server figures only, never send-to-first-token: a round of long prompts
	// is how prefill is measured at a given length, and the client's window
	// includes queue wait, template rendering and the first decode step. On a
	// round of several streams the sums pool the streams' own evaluations, so
	// the rate is the mean engine prefill a stream got, not the server's
	// throughput. 0 means the server reported no prompt timings.
	PromptN         int     `json:"prompt_n,omitempty"`
	PromptPerSecond float64 `json:"prompt_per_second,omitempty"`
	// CacheN is the prompt tokens the server took from its prefix cache in
	// this round instead of evaluating, summed over the round's streams
	// (timings.cache_n; TTP-66, lead, 2026-09-14). Beside PromptN it is the
	// measurement a coding agent's workload is mostly made of: a prompts file
	// whose second line repeats the first line's prefix shows the second round
	// evaluating only its new tail. 0 is a cold prefix or a server that did
	// not report it; PromptN beside it says which.
	CacheN int `json:"cache_n,omitempty"`
	// Speculative decoding totals over the round's streams; nil = not reported.
	DraftN         *int `json:"draft_n,omitempty"`
	DraftNAccepted *int `json:"draft_n_accepted,omitempty"`
}

// SpecNMaxGroup is one value of a speculative.n_max sweep (TTP-35): the rounds
// sent with it and the spread of their figures, reduced exactly as the
// run-level Spread is.
type SpecNMaxGroup struct {
	NMax   int         `json:"n_max"`
	Rounds int         `json:"rounds"`
	Spread RoundSpread `json:"spread"`
}

// Spread is a median with its range over the rounds of a run. Min == Max ==
// Median == 0 means unobserved.
type Spread struct {
	Median float64 `json:"median"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

// RoundSpread is how much the per-round figures moved across a multi-prompt
// run: one prompt's card is misleading when the same draft model is accepted
// 13 % on prose and 87 % on SQL, so the card prints the median with its range.
type RoundSpread struct {
	PerStreamPredictedPerSecond Spread `json:"per_stream_predicted_per_second"`
	// DraftAcceptRate is DraftNAccepted/DraftN per round, 0..1. All-zero when
	// no round reported a draft.
	DraftAcceptRate Spread `json:"draft_accept_rate"`
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
	// Witnesses are readings of the box taken at the start and the end of
	// every measurement round (TTP-36, 2026-09-13), so a suspect row carries
	// its own evidence instead of a timeline rebuilt from logs. Recorded only
	// when the server runs on this host (Server.PID found): the recording
	// machine's pressure says nothing about a remote server's.
	Witnesses []ContentionWitness `json:"witnesses,omitempty"`
}

// ContentionWitness is one reading of how busy the box was (TTP-36).
type ContentionWitness struct {
	T        time.Duration `json:"t"`     // since run start
	Round    int           `json:"round"` // 1-based (RequestRecord.Round+1) in a --prompts run; 0 for a single-round run
	Edge     string        `json:"edge"`  // "start" | "end"
	LoadAvg1 float64       `json:"loadavg1"`
	// IOSomeAvg10 is /proc/pressure/io "some avg10". nil = unreadable (no PSI
	// in the kernel, or not Linux); 0 is a real, quiet reading.
	IOSomeAvg10 *float64 `json:"io_some_avg10,omitempty"`
	// PageCacheBytes is /proc/meminfo Cached. 0 = unread.
	PageCacheBytes int64 `json:"page_cache_bytes,omitempty"`
	// ProcsRead is true when the process table was scanned; LlamaProcs is
	// then complete, and an empty list is a reading, not an unknown.
	ProcsRead  bool        `json:"procs_read"`
	LlamaProcs []LlamaProc `json:"llama_procs,omitempty"`
	// The machine's operating point (TTP-57, 2026-09-14): a thermal watchdog
	// that lowers the clock cap mid-run leaves a tape whose rate was never
	// one configuration, and these two readings at each edge make that
	// visible. CPUMaxKHz is the largest scaling_max_freq over the online
	// CPUs (Linux cpufreq); 0 = unread. TempC is one hwmon temperature,
	// TempSensor its chip and label ("k10temp Tctl", "nct6798 CPUTIN");
	// TempSensor "" means no sensor was read and TempC is not a reading.
	CPUMaxKHz  int64   `json:"cpu_max_khz,omitempty"`
	TempC      float64 `json:"temp_c,omitempty"`
	TempSensor string  `json:"temp_sensor,omitempty"`
}

// LlamaProc is one live llama-* process (llama-server, llama-bench, ...).
type LlamaProc struct {
	PID    int     `json:"pid"`
	Comm   string  `json:"comm"`
	AgeSec float64 `json:"age_s"`
	// Attached is true for the server this run measured; every other
	// llama-* process is contention.
	Attached bool `json:"attached,omitempty"`
}

// TemplateInfo records what was actually sent. Lesson 4.
type TemplateInfo struct {
	ChatTemplate          string            `json:"chat_template,omitempty"` // what /props reported: a name, or on llama-server the Jinja source itself
	ReasoningEffort       string            `json:"reasoning_effort,omitempty"`
	TemplateKwargs        map[string]string `json:"template_kwargs,omitempty"`
	RenderedHasThinkClose bool              `json:"rendered_has_think_close"` // "</think>" present in rendered prompt
	RenderedPromptSHA256  string            `json:"rendered_prompt_sha256,omitempty"`
	RenderedPromptTokens  int               `json:"rendered_prompt_tokens,omitempty"`
}

// SamplingSummary is the run-level view of what the requests asked for: the
// temperature, the thinking switch and the endpoint (TTP-55, 2026-09-14).
//
// Measured the same day on one model and one server, the three shapes are 11 %
// apart — greedy /completion 25.6 tok/s, server-default sampling 24.5, the
// chat path with thinking on 22.4 — so a card that does not say which one it
// recorded is not comparable with another card.
//
// Temperature is a pointer because 0 is greedy and nil is "nothing was sent,
// the server's own default applied". Printing llama.cpp's 0.8 for a request
// that never carried a temperature would be the invented default the schema
// forbids.
type SamplingSummary struct {
	Temperature *float64 `json:"temperature,omitempty"`
	Thinking    string   `json:"thinking,omitempty"` // "off" | "" (the server decided)
	Endpoint    string   `json:"endpoint,omitempty"` // EndpointCompletion; "" and EndpointChat are chat
	// ThoughtAnyway counts the answered streams whose output opened a
	// thinking block although Thinking says "off" (TTP-106, 2026-09-17).
	//
	// The request is not the outcome. llama-server drops a request's
	// chat_template_kwargs unless it was started with --jinja, and says
	// nothing: the switch is accepted, ignored, and the only witness left is
	// the run's own first tokens. A card that reads Thinking alone then
	// prints "thinking off" over a clip in which every stream reasons.
	//
	// It is counted at record time because the card reads this summary and
	// never Tape.Requests, where the token text lives — the same rule that
	// freezes the placement classification.
	//
	// Only a positive count asserts anything. Zero means "none seen", which
	// on a tape written before this field cannot be told from "never looked"
	// (TTP-103), so the row and the caveat speak when this is above zero and
	// stay silent otherwise.
	ThoughtAnyway int `json:"thought_anyway,omitempty"`
}

// LimitSummary is what was allowed to end the generation, and what did
// (TTP-76, 2026-09-14).
//
// A run stops for one of three reasons and the card must be able to say which:
// the model stopped (EOS), the token cap was reached, or the clock ran out.
// The first two are the server's own words in PromptRecord.FinishReason. The
// third has no server word at all — a clock-cut stream is cancelled mid-flight
// and no final chunk ever arrives — and inventing one would be the default the
// schema forbids. Hence CutAt here and PromptRecord.Cut per stream, with
// FinishReason left exactly as the server left it: empty.
type LimitSummary struct {
	// For is the wall-clock budget the run was recorded with (`--for 20s`),
	// measured from the first request going out and not from the first token:
	// a clip replays the run at 1:1, so this is the number that makes a clip's
	// length predictable on a machine nobody has measured. 0 = no clock.
	For time.Duration `json:"for,omitempty"`
	// MaxTokens is the per-stream token cap the requests actually carried. It
	// is always sent — a request with no cap generates unbounded, and a clock
	// that fails then has nothing behind it.
	MaxTokens int `json:"max_tokens,omitempty"`
	// MaxTokensNamed says the cap was the user's own (`--n-predict`) rather
	// than the recorder's runaway guard. It is what lets a reader reproduce
	// the table's "both" row: `--for 10s --n-predict 300` ends at whichever
	// comes first, and a command rebuilt from For alone would leave the cap
	// at whatever the reading version defaults to — a different run on a
	// fast box. False on a tape older than the field, which is read as
	// "not known to be named" (lead, 2026-09-14).
	MaxTokensNamed bool `json:"max_tokens_named,omitempty"`
	// MinTokens is the floor that was in force (MinCutTokens); 0 with no clock.
	MinTokens int `json:"min_tokens,omitempty"`
	// CutAt is when the clock actually cut, from the same origin as For. 0
	// means it never did: every stream ended on EOS or on the cap. CutAt
	// larger than For is honest and expected — the floor held the cut back
	// until the slowest live stream had MinTokens, so on that box the run is
	// longer than was asked for and the card should say so.
	CutAt time.Duration `json:"cut_at,omitempty"`
}

// PromptRecord is the request as sent and the answer as received.
type PromptRecord struct {
	Messages       []Message      `json:"messages"`
	Name           string         `json:"name,omitempty"`            // the JSONL line's name in a multi-prompt run (TTP-31)
	RenderedPrompt string         `json:"rendered_prompt,omitempty"` // from /apply-template
	Params         map[string]any `json:"params,omitempty"`          // temperature, n_predict, ...
	Completion     string         `json:"completion"`                // concatenated answer token text (reasoning excluded)
	Reasoning      string         `json:"reasoning,omitempty"`       // concatenated reasoning_content text
	ReasoningN     int            `json:"reasoning_n,omitempty"`     // reasoning tokens among the predicted ones
	MaxTokens      int            `json:"max_tokens,omitempty"`      // the generation cap the request was sent with (n_predict / max_tokens); 0 = not recorded
	FinishReason   string         `json:"finish_reason,omitempty"`
	// Cut marks a stream the run's clock ended (TTP-76, 2026-09-14). No final
	// chunk arrived for it, so FinishReason stays empty rather than carrying a
	// word the server never said; a reader asks Cut first and FinishReason
	// second. The timings on a cut stream are still the server's own up to the
	// last chunk received — internal/server/sse.go documents that they ride
	// every chunk under timings_per_token — so the rate is a measurement and
	// not an estimate. What truncation costs is the late-run behaviour
	// (thermal, cache growth), not the rate's validity.
	Cut bool `json:"cut,omitempty"`
	// Endpoint is the server path the request went to (TTP-55, 2026-09-14):
	// EndpointChat, the default, or EndpointCompletion for a raw
	// /completion request whose prompt was sent verbatim with no template.
	// The two paths cost a measurable tenth against each other on a
	// reasoning model, so the card names which one recorded the rate. ""
	// on a tape older than the field is chat, the only path that existed.
	Endpoint string `json:"endpoint,omitempty"`
	// Thinking says what the request asked of a reasoning model's thinking:
	// "off" when the recorder sent the engine's own switch to disable it
	// (--no-think), "" when it sent nothing and the server decided.
	Thinking string `json:"thinking,omitempty"`
}

// Values of PromptRecord.Endpoint.
const (
	EndpointChat       = "chat"       // /v1/chat/completions, the template applied by the server
	EndpointCompletion = "completion" // /completion, the prompt sent verbatim
)

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
