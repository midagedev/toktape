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
  "gpu_count",
  "vram_bytes",
  "host_class",
  "sessions",
  "prompt_set",
  "decode_per_sec",
  "caveat_count",
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
    num(idx.gpu_count),
    num(idx.vram_bytes),
    str(idx.host_class),
    num(idx.sessions),
    str(idx.prompt_set),
    num(idx.decode_per_sec),
    // Not omitted when zero: "no caveats" is a measured fact about a run and
    // reads differently from "we do not know", which is what NULL says.
    Number.isFinite(idx.caveat_count) ? idx.caveat_count : 0,
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
