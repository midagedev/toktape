-- The figures reach the columns (TTP-130).
--
-- Every column here is a copy of one field of `index_json`, put in its own
-- column only because SQLite cannot index into JSON cheaply (0001's rule).
-- The Worker still never derives: the Go client owns every number, and a
-- column this side filled in by hand would be this side forming an opinion
-- about a tape it is not allowed to open (§9.6).
--
-- All nullable: absent means unknown, and the reader prints `?` or omits
-- the chip. A zero that travelled is stored — except through row.js's num(),
-- which keeps the old "0 prints as ?" rule and so NULLs it (cache_hit_ratio
-- documents the one place that matters: a nulled zero reads as `cache 0%`
-- only beside a prompt_n, per the contract).
--
-- The backfill only covers what needs no judgement: the five fields the
-- index already had, copied out of `index_json` the way 0007 copied
-- gpu_ids. `moe` becomes 1 only when the JSON says true (the client omits
-- it when false, so false and unknown share the NULL). `active_params` is
-- filled only for rows with no `moe` true — that is a copy of a dense
-- model's own number, not a derivation: for a dense model the active
-- parameters ARE the parameters, so params under a second name says
-- nothing new. MoE rows keep NULL until the lead reindexes; ratioing out
-- their active count here would be deriving. Everything else stays NULL
-- until the lead reindexes.
ALTER TABLE runs ADD COLUMN prompt_n INTEGER;
ALTER TABLE runs ADD COLUMN predicted_n INTEGER;
ALTER TABLE runs ADD COLUMN min_predicted_n INTEGER;
ALTER TABLE runs ADD COLUMN reasoning_n INTEGER;
ALTER TABLE runs ADD COLUMN cache_hit_ratio REAL;
ALTER TABLE runs ADD COLUMN ctx_size INTEGER;
ALTER TABLE runs ADD COLUMN n_slots INTEGER;
ALTER TABLE runs ADD COLUMN fa TEXT;
ALTER TABLE runs ADD COLUMN kv_cache TEXT;
ALTER TABLE runs ADD COLUMN batch TEXT;
ALTER TABLE runs ADD COLUMN ubatch TEXT;
ALTER TABLE runs ADD COLUMN ngl TEXT;
ALTER TABLE runs ADD COLUMN offload TEXT;
ALTER TABLE runs ADD COLUMN draft_model TEXT;
ALTER TABLE runs ADD COLUMN draft_accept REAL;
ALTER TABLE runs ADD COLUMN throttled INTEGER;
ALTER TABLE runs ADD COLUMN cold INTEGER;
ALTER TABLE runs ADD COLUMN power_w REAL;
ALTER TABLE runs ADD COLUMN power_limit_w REAL;
ALTER TABLE runs ADD COLUMN file_bytes INTEGER;
ALTER TABLE runs ADD COLUMN active_params INTEGER;
ALTER TABLE runs ADD COLUMN n_experts INTEGER;
ALTER TABLE runs ADD COLUMN n_experts_used INTEGER;
ALTER TABLE runs ADD COLUMN params INTEGER;
ALTER TABLE runs ADD COLUMN moe INTEGER;
ALTER TABLE runs ADD COLUMN quant_bits REAL;
ALTER TABLE runs ADD COLUMN prefill_per_sec REAL;
ALTER TABLE runs ADD COLUMN ttft_p50_ms REAL;
UPDATE runs SET params = CAST(json_extract(index_json, '$.params') AS INTEGER) WHERE json_extract(index_json, '$.params') IS NOT NULL;
UPDATE runs SET moe = 1 WHERE json_extract(index_json, '$.moe') = 1;
UPDATE runs SET quant_bits = json_extract(index_json, '$.quant_bits') WHERE json_extract(index_json, '$.quant_bits') IS NOT NULL;
UPDATE runs SET prefill_per_sec = json_extract(index_json, '$.prefill_per_sec') WHERE json_extract(index_json, '$.prefill_per_sec') IS NOT NULL;
UPDATE runs SET ttft_p50_ms = json_extract(index_json, '$.ttft_p50_ms') WHERE json_extract(index_json, '$.ttft_p50_ms') IS NOT NULL;
UPDATE runs SET active_params = CAST(json_extract(index_json, '$.params') AS INTEGER) WHERE json_extract(index_json, '$.params') IS NOT NULL AND COALESCE(json_extract(index_json, '$.moe'), 0) != 1;
