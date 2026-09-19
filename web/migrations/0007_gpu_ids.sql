-- One id per card that took part (TTP-124, 2026-09-19).
--
-- gpu_id holds the shared id only when every card normalised alike; a mixed
-- rig keeps it empty and was reachable by no GPU at all. gpu_ids is the
-- membership axis beside it: the per-card ids as a JSON array in card order,
-- read with json_each, one entry per card.
--
-- The backfill only covers what needs no normalising: a single-kind row
-- stands as its one id (membership needs the set, so one entry stands for
-- 4x rtx-4090). Mixed rows cannot be backfilled in SQL — deriving their
-- members here would be the service normalising, which is the client's job
-- (§9.6) — so they stay NULL, and the lead backfills the 9 production rows
-- by hand.
ALTER TABLE runs ADD COLUMN gpu_ids TEXT;
UPDATE runs SET gpu_ids = json_array(gpu_id) WHERE gpu_id IS NOT NULL AND gpu_ids IS NULL;
