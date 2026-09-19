// The index row, as columns.
//
// Everything here is a copy. `index_json` is the record of what was indexed;
// these columns exist only because SQLite cannot index into JSON cheaply, so
// there is no judgement in this file and there must never be one — a value
// that is not already in the row the Go client derived would be this side
// forming an opinion about a tape it is not allowed to open (§9.6).

// The index schema this build knows. A row that says anything else is
// refused by name rather than indexed into the wrong columns.
export const SUPPORTED_INDEX_SCHEMA = 1;

// The column names, in the order columnValues returns them.
export const INDEX_COLUMNS = [
  "recorded_at",
  "toktape_version",
  "repo",
  "model_id",
  "model_raw",
  "quant_id",
  "quant_raw",
  "engine_kind",
  "engine_version",
  "os",
  "gpus_raw",
  "gpu_id",
  // One id per card that took part (TTP-124, 2026-09-19): the membership
  // axis beside the shared gpu_id.
  "gpu_ids",
  "gpu_count",
  "vram_bytes",
  "host_class",
  "sessions",
  "prompt_set",
  "decode_per_sec",
  "caveat_count",
  // The figures (TTP-130), in contract order. Additive and omitempty: absent
  // is unknown, and the reader prints `?` or omits the chip. Neither side
  // may rename or re-derive.
  "prompt_n",
  "predicted_n",
  "min_predicted_n",
  "reasoning_n",
  "cache_hit_ratio",
  "ctx_size",
  "n_slots",
  "fa",
  "kv_cache",
  "batch",
  "ubatch",
  "ngl",
  "offload",
  "draft_model",
  "draft_accept",
  "throttled",
  "cold",
  "power_w",
  "power_limit_w",
  "file_bytes",
  "active_params",
  "n_experts",
  "n_experts_used",
  "params",
  "moe",
  "quant_bits",
  "prefill_per_sec",
  "ttft_p50_ms",
];

export function columnValues(idx) {
  return [
    str(idx.recorded_at),
    str(idx.toktape_version),
    str(idx.repo),
    str(idx.model_id),
    str(idx.model_raw),
    str(idx.quant_id),
    str(idx.quant_raw),
    str(idx.engine_kind),
    str(idx.engine_version),
    str(idx.os),
    // The names as observed, joined. A rig with two kinds of card has no
    // gpu_id — one shared id would be a claim about hardware that is not
    // there — so this is the only thing such a row can be found by (§9.4).
    Array.isArray(idx.gpus_raw) && idx.gpus_raw.length ? idx.gpus_raw.join(" / ") : null,
    str(idx.gpu_id),
    // Copy only: the array the client derived, stored as JSON text for
    // json_each. Never derived here (§9.6).
    Array.isArray(idx.gpu_ids) && idx.gpu_ids.length ? JSON.stringify(idx.gpu_ids) : null,
    num(idx.gpu_count),
    num(idx.vram_bytes),
    str(idx.host_class),
    num(idx.sessions),
    str(idx.prompt_set),
    num(idx.decode_per_sec),
    // Not omitted when zero: "no caveats" is a measured fact about a run and
    // reads differently from "we do not know", which is what NULL says.
    Number.isFinite(idx.caveat_count) ? idx.caveat_count : 0,
    // The figures (TTP-130): copies in contract order, same order as
    // INDEX_COLUMNS above. Bools travel as 1/NULL; numbers go through num(),
    // so a travelled zero reads as unknown (the old "0 prints as ?" rule).
    // cache_hit_ratio 0 is a real "no hit", but num() NULLs it anyway — the
    // chip shows `cache 0%` only from prompt_n > 0 beside a null ratio, per
    // the contract (0 = no hit or unknown; the tape does not distinguish).
    num(idx.prompt_n),
    num(idx.predicted_n),
    num(idx.min_predicted_n),
    num(idx.reasoning_n),
    num(idx.cache_hit_ratio),
    num(idx.ctx_size),
    num(idx.n_slots),
    str(idx.fa),
    str(idx.kv_cache),
    str(idx.batch),
    str(idx.ubatch),
    str(idx.ngl),
    str(idx.offload),
    str(idx.draft_model),
    num(idx.draft_accept),
    idx.throttled === true ? 1 : null,
    idx.cold === true ? 1 : null,
    num(idx.power_w),
    num(idx.power_limit_w),
    num(idx.file_bytes),
    num(idx.active_params),
    num(idx.n_experts),
    num(idx.n_experts_used),
    num(idx.params),
    idx.moe === true ? 1 : null,
    num(idx.quant_bits),
    num(idx.prefill_per_sec),
    num(idx.ttft_p50_ms),
  ];
}

// An absent field stays absent. The client omits what it could not derive —
// "정규화가 실패하면 그 필드는 빈 값이지 추측이 아니다" (§9.4) — and turning
// that into an empty string here would make a gap look like a measurement.
function str(v) {
  return typeof v === "string" && v !== "" ? v : null;
}

function num(v) {
  return typeof v === "number" && Number.isFinite(v) && v !== 0 ? v : null;
}
