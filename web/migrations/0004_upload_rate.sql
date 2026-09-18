-- The anonymous upload counter.
--
-- This replaces Cloudflare's rate-limiting binding, which was measured not
-- to count on this deployment (2026-09-18): the binding was present,
-- `limit()` was called on every request with the same key, and it answered
-- `{"success":true}` ten times in two seconds against a declared six per
-- sixty. Logged from production with `wrangler tail`, so this is a
-- measurement and not a suspicion.
--
-- A limit that only exists in an emulator is worse than none, because the
-- deployment looks protected. So the counter moved to the one store whose
-- behaviour the gate can see in both places.
--
-- `bucket` is sha256(secret || address) truncated, plus the minute it counts
-- for. The address is never stored: with IPv4 an unsalted hash is reversible
-- by enumeration in seconds, which would make this table a log of who
-- uploaded from where — the same fact this repo strips out of every tape it
-- publishes, kept anyway through the side door.
--
-- expires_at lets old rows be swept in the same statement that writes new
-- ones, so nothing accumulates and nothing has to be scheduled.
CREATE TABLE upload_rate (
  bucket     TEXT    PRIMARY KEY,
  n          INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);

CREATE INDEX upload_rate_expiry ON upload_rate (expires_at);
