// Package publish derives the small index record a published run uploads
// beside its tape (docs/toktape-spec.ko.md §9). The server is a Cloudflare
// Worker that never parses a tape: the row is computed here, next to the
// schema, so knowledge of the tape's shape has one owner — the same reason
// internal/placement/gguf is the only side that reads GGUF files.
//
// Every normalised field keeps its raw input beside it, and search falls back
// to the raw string wherever the normalised value is empty (§9.4): an
// unnormalised field is a fact about the input, not a gap to be filled.
package publish

import (
	"math"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// IndexSchema is the version of the Index shape. The server rejects a version
// it does not know by name rather than mis-indexing it quietly.
const IndexSchema = 1

// Index is what the search reads: one row per published run. It is derived
// from a tape by IndexOf and uploaded beside it.
type Index struct {
	Schema         int       `json:"schema"`
	ToktapeVersion string    `json:"toktape_version,omitempty"`
	RecordedAt     time.Time `json:"recorded_at"`

	// What ran. Every normalised field has its raw input beside it.
	//
	// Repo and ModelID are two facets, not two attempts at one answer
	// (TTP-119). Repo is the exact Hugging Face repository, present only when
	// the tape proved it, and it answers "whose build of this" — the search
	// never carries an unproven one, which is why no source travels with it
	// here. ModelID is the naming convention's slug and answers "which model",
	// across every publisher who shipped it. Collapsing them into one field
	// would make the id mean different things in different rows, and an id
	// that does that cannot be searched on.
	Repo          string  `json:"repo,omitempty"`
	ModelRaw      string  `json:"model_raw,omitempty"`
	ModelID       string  `json:"model_id,omitempty"`
	ModelIDSource string  `json:"model_id_source,omitempty"` // "gguf" | "filename"
	ModelDir      string  `json:"model_dir,omitempty"`
	Params        int64   `json:"params,omitempty"`
	MoE           bool    `json:"moe,omitempty"`
	QuantRaw      string  `json:"quant_raw,omitempty"`
	QuantID       string  `json:"quant_id,omitempty"`
	QuantBits     float64 `json:"quant_bits,omitempty"`

	EngineKind    string `json:"engine_kind,omitempty"`
	EngineVersion string `json:"engine_version,omitempty"`

	// Where it ran.
	OS      string   `json:"os,omitempty"`
	GPUsRaw []string `json:"gpus_raw,omitempty"`
	GPUID   string   `json:"gpu_id,omitempty"`
	// GPUIDs is one id per card in card order, empty ids dropped: the
	// membership axis a mixed rig is reachable by (TTP-124). A name that
	// was only vendor noise is not a card.
	GPUIDs    []string `json:"gpu_ids,omitempty"`
	GPUCount  int      `json:"gpu_count,omitempty"`
	VRAMBytes int64    `json:"vram_bytes,omitempty"`
	RAMBytes  int64    `json:"ram_bytes,omitempty"`
	HostClass string   `json:"host_class,omitempty"`

	// What it did.
	Sessions      int     `json:"sessions,omitempty"`
	PromptSet     string  `json:"prompt_set,omitempty"`
	DecodePerSec  float64 `json:"decode_per_sec,omitempty"`
	PrefillPerSec float64 `json:"prefill_per_sec,omitempty"`
	TTFTp50Ms     float64 `json:"ttft_p50_ms,omitempty"`

	// The figures behind a rate (TTP-130). Every field is additive and
	// omitempty: absent means unknown, and the reader prints ? or omits
	// the chip. The JSON names are the contract with the Worker, which
	// only copies them — neither side may rename or re-derive.
	PromptN       int     `json:"prompt_n,omitempty"`
	PredictedN    int     `json:"predicted_n,omitempty"`
	MinPredictedN int     `json:"min_predicted_n,omitempty"`
	ReasoningN    int     `json:"reasoning_n,omitempty"`
	CacheHitRatio float64 `json:"cache_hit_ratio,omitempty"`
	CtxSize       int     `json:"ctx_size,omitempty"`
	NSlots        int     `json:"n_slots,omitempty"`
	FA            string  `json:"fa,omitempty"`
	KVCache       string  `json:"kv_cache,omitempty"`
	Batch         string  `json:"batch,omitempty"`
	UBatch        string  `json:"ubatch,omitempty"`
	NGL           string  `json:"ngl,omitempty"`
	Offload       string  `json:"offload,omitempty"`
	DraftModel    string  `json:"draft_model,omitempty"`
	DraftAccept   float64 `json:"draft_accept,omitempty"`
	Throttled     bool    `json:"throttled,omitempty"`
	Cold          bool    `json:"cold,omitempty"`
	PowerW        float64 `json:"power_w,omitempty"`
	PowerLimitW   float64 `json:"power_limit_w,omitempty"`
	FileBytes     int64   `json:"file_bytes,omitempty"`
	ActiveParams  int64   `json:"active_params,omitempty"`
	NExperts      int     `json:"n_experts,omitempty"`
	NExpertsUsed  int     `json:"n_experts_used,omitempty"`

	// How much to trust it. The card's caveats travel with the row: a search
	// result that drops them is a leaderboard with the sorting removed.
	Caveats     []string `json:"caveats,omitempty"`
	CaveatCount int      `json:"caveat_count,omitempty"`
}

// IndexOf derives the index of a tape. It reads the tape and nothing else.
func IndexOf(t *tape.Tape) Index {
	s := t.Summary
	idx := Index{
		Schema:         IndexSchema,
		ToktapeVersion: s.ToktapeVersion,
		// StartedAt, not FinishedAt: the moment the recording began is the
		// one the run's own ID encodes (<yyyymmdd>-<hhmmss>-...).
		RecordedAt: s.StartedAt,

		ModelRaw: s.Model.FileName,
		ModelDir: s.Model.Dir,
		Params:   s.Model.Params,
		MoE:      s.Model.NExperts > 0,
		QuantRaw: s.Model.Quant,

		// Kind is already a normalised vocabulary (tape.ServerKind) —
		// re-normalising it here would be a second owner of that set.
		EngineKind:    string(s.Server.Kind),
		EngineVersion: engineVersion(s.Server),

		OS:       s.Host.OS,
		RAMBytes: s.Host.RAMBytes,
		Sessions: s.Concurrency,
		// Passed through, never re-derived: the set stamps its own id and the
		// recorder only copies it, so a second opinion here would be a second
		// owner of the one field that decides what a run is comparable with.
		PromptSet: s.PromptSet,
	}
	// ModelRaw falls back to general.name when no file name was recorded —
	// the raw field always carries what was observed, even though a
	// general.name has no file-name shape and never normalises.
	if idx.ModelRaw == "" {
		idx.ModelRaw = s.Model.Name
	}
	// The repo only when the tape proved it. RepoSource is what says so; a
	// repo id with no source behind it was guessed, and a guess must never
	// reach a field the search treats as exact.
	if s.Model.RepoSource != "" {
		idx.Repo = s.Model.Repo
	}
	// The source travels with the id because the two are not equally strong:
	// "gguf" was read out of the header, "filename" out of a name, and a name
	// carries no fine-tune (the quantisation sits where one would be, which
	// is why internal/placement/gguf refuses to read one from a name). An
	// id that came from a name can therefore merge two fine-tunes of a model,
	// and a consumer that must not do that has the field to tell it.
	idx.ModelID, idx.ModelIDSource = ModelID(s.Model)
	idx.QuantID, idx.QuantBits = QuantID(s.Model.Quant)

	idx.GPUsRaw = make([]string, len(s.Host.GPUs))
	gpuIDs := make([]string, len(s.Host.GPUs))
	for i, g := range s.Host.GPUs {
		idx.GPUsRaw[i] = g.Name
		gpuIDs[i] = GPUID(g.Name)
		idx.VRAMBytes += g.VRAMBytes
	}
	idx.GPUCount = len(s.Host.GPUs)
	if idx.HostClass = HostClass(gpuIDs); idx.HostClass != "" {
		// One kind of card: the shared id is the row's gpu_id. A mixed rig
		// keeps it empty and search falls back to gpus_raw.
		idx.GPUID = gpuIDs[0]
	}
	// One id per card, in card order, empty ids dropped. GPUID/HostClass
	// above are exactly as they were — other consumers read them.
	for _, id := range gpuIDs {
		if id != "" {
			idx.GPUIDs = append(idx.GPUIDs, id)
		}
	}

	// The rate choice is the one the rest of the repo already makes: above
	// one stream the figure is the server-wide aggregate, at one stream the
	// stream's own — internal/tui/right.go headlineRate and the card's
	// decodeParts/prefillParts (TTP-59). decodeParts keeps the per-stream
	// figure when the aggregate was never measured (0), so the same fallback
	// is here. TTFT follows the card's split (TTP-110): the single stream's
	// own time at N=1, the median over the streams above it.
	if s.Concurrency > 1 {
		idx.DecodePerSec = s.Timings.PredictedPerSecond
		if r := s.Aggregate.AggregatePredictedPerSecond; r > 0 {
			idx.DecodePerSec = r
		}
		idx.PrefillPerSec = s.Aggregate.AggregatePromptPerSecond
		idx.TTFTp50Ms = s.Aggregate.TTFTp50Ms
	} else {
		idx.DecodePerSec = s.Timings.PredictedPerSecond
		idx.PrefillPerSec = s.Timings.PromptPerSecond
		idx.TTFTp50Ms = s.Timings.TTFTMs
	}

	// The figures behind a rate (TTP-130): the card's "in" figure, its
	// "out" figure, and everything that qualifies them. MinPredictedN lives
	// on the aggregate, not the timings: Timings.PredictedN is the
	// per-stream mean above one stream while the minimum is a whole-run
	// figure (tape.AggregateTimings), so it has no per-stream home.
	idx.PromptN = card.PromptTokens(&s)
	idx.PredictedN = s.Timings.PredictedN
	idx.MinPredictedN = s.Aggregate.MinPredictedN
	idx.ReasoningN = s.Timings.ReasoningN
	idx.CacheHitRatio = s.Cache.HitRatio
	idx.CtxSize = s.Server.CtxSize
	idx.NSlots = s.Server.NSlots
	idx.FA = s.Server.Flags.FlashAttn
	idx.KVCache = kvCache(s.Server.Flags.CacheTypeK, s.Server.Flags.CacheTypeV)
	idx.Batch = s.Server.Flags.Batch
	idx.UBatch = s.Server.Flags.UBatch
	idx.NGL = s.Server.Flags.NGL
	idx.Offload = offloadKind(s.Placement)
	idx.DraftModel = s.Server.Flags.DraftModel
	idx.DraftAccept = draftAcceptRate(s.Timings.DraftN, s.Timings.DraftNAccepted)
	// card.GPUThrottled, not g.Throttled: the card's verdict is the narrow
	// one (a card boosting into its own power cap is not "throttled"), and a
	// row that said yes under a card that says no would be the two
	// disagreeing about one run (lead, 2026-09-19 — the hero did exactly that).
	for _, g := range s.GPUsAtEnd {
		if card.GPUThrottled(g) {
			idx.Throttled = true
		}
		if g.PowerW > 0 {
			idx.PowerW += g.PowerW
		}
		if g.PowerLimitW > 0 {
			idx.PowerLimitW += g.PowerLimitW
		}
	}
	idx.Cold = s.Cache.Label == tape.CacheCold
	idx.FileBytes = s.Model.FileBytes
	idx.ActiveParams = activeParams(s.Model)
	idx.NExperts = s.Model.NExperts
	idx.NExpertsUsed = s.Model.NExpertsUsed

	// The codes only — the sentences live on the card, and a code is the
	// handle a consumer branches on (internal/card/caveat.go).
	for _, c := range card.Caveats(&s) {
		idx.Caveats = append(idx.Caveats, c.Code)
	}
	idx.CaveatCount = len(idx.Caveats)
	return idx
}

// kvCache joins the k/v cache types verbatim: the one when both agree, "k/v"
// when both are set and differ, the set one when only one is, "" when
// neither. Never a default: unknown prints as ? downstream.
func kvCache(k, v string) string {
	switch {
	case k != "" && v != "" && k == v:
		return k
	case k != "" && v != "":
		return k + "/" + v
	case k != "":
		return k
	default:
		return v
	}
}

// offloadKind names where the weights sit: full when GPUs exist and the CPU
// device holds no weight bytes, partial when GPUs exist and it does, cpu
// when no GPU device exists, "" when the placement names no device at all.
//
// Weight bytes are the Classes totals with bytes > 0, except the embedding
// table (lead, 2026-09-19): llama.cpp keeps token_embd on the host at every
// -ngl, as a lookup and not a matmul, so a fully offloaded run always shows
// a few hundred MB of embeddings on CPU — the hero does — and counting them
// would call every run "partial" and tell the reader nothing. The
// TensorClass list carries no kv-cache or compute-buffer class, so every
// other class present is weights.
func offloadKind(p tape.PlacementSummary) string {
	if len(p.Devices) == 0 {
		return ""
	}
	hasGPU := false
	var cpuWeights int64
	for _, d := range p.Devices {
		if d.Device == tape.DeviceCPU {
			for class, b := range d.Classes {
				if b > 0 && class != tape.ClassEmbed {
					cpuWeights += b
				}
			}
			continue
		}
		hasGPU = true
	}
	if !hasGPU {
		return "cpu"
	}
	if cpuWeights > 0 {
		return "partial"
	}
	return "full"
}

// draftAcceptRate is the speculative-decoding acceptance rate, 0..1, only
// when the server reported both halves and drafted at least one token:
// accepted/drafted over the pooled run-level totals.
func draftAcceptRate(draftN, accepted *int) float64 {
	if draftN == nil || accepted == nil || *draftN <= 0 {
		return 0
	}
	return float64(*accepted) / float64(*draftN)
}

// activeParams is the parameter count the run actually read per token:
// dense (no experts) is the whole model; MoE scales params by the share of
// weight bytes touched, because bytes per parameter is the same everywhere,
// and the tape's ActiveBytesPerToken already includes the shared and router
// weights. 0 when the tape cannot answer — never the used/experts fraction,
// which is a guess.
func activeParams(m tape.ModelInfo) int64 {
	if m.NExperts == 0 {
		return m.Params
	}
	if m.ActiveBytesPerToken > 0 && m.FileBytes > 0 && m.Params > 0 {
		return int64(math.Round(float64(m.Params) * float64(m.ActiveBytesPerToken) / float64(m.FileBytes)))
	}
	return 0
}

// engineVersion is the build when the server reported one, else the commit —
// exllamav3's engine block names a build with no commit to offer, and an
// empty one of those beats an invented version.
func engineVersion(s tape.ServerInfo) string {
	if s.Build != "" {
		return s.Build
	}
	return s.Commit
}
