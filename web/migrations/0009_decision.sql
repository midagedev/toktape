-- A run's mode and a decision run's figures (TTP-187, TTP-192).
--
-- Copies of `index_json` fields, like every column here (0001's rule): the
-- Go client derives them and the Worker never does. `mode` is NULL for a
-- benchmark, "chat" or "decision" otherwise; a decision row has no decode,
-- prefill or TTFT, and its figures are these four, every latency the
-- client's send-to-last-byte time.
ALTER TABLE runs ADD COLUMN mode TEXT;
ALTER TABLE runs ADD COLUMN decision_p50_ms REAL;
ALTER TABLE runs ADD COLUMN decision_engine_p50_ms REAL;
ALTER TABLE runs ADD COLUMN decision_cold_ms REAL;
ALTER TABLE runs ADD COLUMN decision_req_per_sec REAL;
UPDATE runs SET mode = json_extract(index_json, '$.mode') WHERE json_extract(index_json, '$.mode') IS NOT NULL;
