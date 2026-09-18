-- The author profile and the lab-note (TTP-125).
--
-- The profile is opt-in per machine and unverified — anyone may type any
-- name — and the note is plain text. Both travel as their own multipart
-- parts beside the record, never inside the tape and never in the index
-- row, so they are stored here as their own columns beside card_key: five
-- nullable columns, all NULL for a run an older client published, which is
-- why an old client still publishes exactly as today.
--
-- avatar_key is the R2 key `avatars/<sha256hex>.png`, content-addressed so
-- the same image from the same machine is stored once. It follows the
-- card_key pattern (a full key, not a hash) on purpose: the readers already
-- know how to turn a key into bytes.
ALTER TABLE runs ADD COLUMN author_name  TEXT;
ALTER TABLE runs ADD COLUMN author_link  TEXT;
ALTER TABLE runs ADD COLUMN avatar_key   TEXT;
ALTER TABLE runs ADD COLUMN title        TEXT;
ALTER TABLE runs ADD COLUMN note         TEXT;
