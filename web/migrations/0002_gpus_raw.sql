-- The GPU names as observed.
--
-- 0001 indexed the normalised gpu_id and forgot the raw string beside it,
-- which breaks the fallback the whole normalisation rests on: a rig with two
-- kinds of card has no shared gpu_id — one would be a claim about hardware
-- that is not there — and without this column such a run is findable by no
-- GPU at all (§9.4, rule 2).
ALTER TABLE runs ADD COLUMN gpus_raw TEXT;
