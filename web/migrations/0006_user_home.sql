-- The user home (TTP-127).
--
-- The profile belongs to the token, not to each run: on a token-owned
-- publish the Worker copies the author parts (name, link, avatar) and the
-- bio onto the token's row, so /u/<handle> always shows the latest profile.
-- The per-run author_*/title/note columns stay as the record of what that
-- upload said, and anonymous runs have nothing else.
--
-- All five columns are nullable, so a token minted before this migration
-- reads as "no profile": the home shows the handle as the name and no bio,
-- the API's absent-not-null rule for unknown. updated_at moves whenever a
-- publish rewrites any of the four profile columns.
ALTER TABLE tokens ADD COLUMN name       TEXT;
ALTER TABLE tokens ADD COLUMN link       TEXT;
ALTER TABLE tokens ADD COLUMN avatar_key TEXT;
ALTER TABLE tokens ADD COLUMN bio        TEXT;
ALTER TABLE tokens ADD COLUMN updated_at TEXT;
