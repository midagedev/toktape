-- The index the search reads (docs/toktape-spec.ko.md §9.4, §9.6).
--
-- Two rules shape every column here.
--
-- The Worker never parses a tape, so nothing in this file may require one to
-- be opened: `index_json` arrives already derived by the Go client next to
-- the schema, and every other column is a copy of one of its fields, put in
-- its own column only because SQLite cannot index into JSON cheaply. If a
-- column is ever needed that `index_json` does not carry, the fix is a new
-- IndexSchema on the client — never a gunzip here.
--
-- And the record is the original: R2 keeps the tape as it was uploaded, so a
-- normalisation that turns out to be wrong is re-derived rather than lost.
-- That is why the raw strings are stored beside the normalised ones — search
-- falls back to them wherever the normalised value is empty, which is a fact
-- about the input and not a gap.

CREATE TABLE runs (
  id            TEXT PRIMARY KEY,
  -- The server's own clock, and what the listing sorts on. Not recorded_at:
  -- ordering on a field out of the tape would make the Worker trust the
  -- uploader's clock and its UTC offset (TTP-120 is still open on that).
  created_at    TEXT    NOT NULL,
  private       INTEGER NOT NULL DEFAULT 0,
  -- The journal's owner (tokens.id), or NULL for a one-off upload. Anonymous
  -- is a supported path, not a degraded one (§9.2).
  owner_token   TEXT,
  -- sha256 of the one-time delete token, never the token. It is printed once
  -- by the client and stored nowhere, here included: what is kept is only
  -- enough to recognise it when it comes back.
  delete_hash   TEXT,

  -- The record in R2. tape_ext is the extension the client uploaded under
  -- (`.tape` today, `.toktape` after TTP-111) rather than one chosen here:
  -- the file format has one owner and it is the Go side.
  tape_key      TEXT    NOT NULL,
  tape_ext      TEXT    NOT NULL,
  tape_bytes    INTEGER NOT NULL,

  -- The client's index row, verbatim. This is the record of what was
  -- indexed; the columns below are derived from it on insert.
  index_schema  INTEGER NOT NULL,
  index_json    TEXT    NOT NULL,

  -- The filter axes (§9.4). Normalised and raw both, because a row whose
  -- model_id is empty is still findable by the name that was observed.
  recorded_at     TEXT,
  toktape_version TEXT,
  repo            TEXT,
  model_id        TEXT,
  model_raw       TEXT,
  quant_id        TEXT,
  quant_raw       TEXT,
  engine_kind     TEXT,
  engine_version  TEXT,
  os              TEXT,
  gpu_id          TEXT,
  gpu_count       INTEGER,
  vram_bytes      INTEGER,
  host_class      TEXT,
  sessions        INTEGER,
  prompt_set      TEXT,
  decode_per_sec  REAL,
  -- Not a quality score and not sortable in the UI: it travels so a result
  -- row can say `! 3 caveats` the way the card does. A search that drops the
  -- qualification is a leaderboard with the sorting removed (§9.4).
  caveat_count    INTEGER
);

-- Newest first over the public scope is the default listing, so it is the
-- one index that has to exist.
CREATE INDEX runs_public_recent ON runs (private, created_at DESC);
-- The journal scope: one owner's runs, newest first.
CREATE INDEX runs_owner_recent ON runs (owner_token, created_at DESC);
-- The two model facets are different questions and are filtered separately
-- (§9.4), so they are indexed separately.
CREATE INDEX runs_model_id ON runs (model_id);
CREATE INDEX runs_repo ON runs (repo);

-- A journal token. Issuing them is TTP-114's remainder; the table exists now
-- because the Worker has to be able to say no to an unknown one from the
-- first upload it ever accepts. Downgrading an unknown token to an anonymous
-- upload would be worse than refusing: the run would land outside the
-- journal it was meant for and, because the client prints no delete token
-- for a token-owned upload, nobody would hold the key to take it down.
CREATE TABLE tokens (
  id         TEXT PRIMARY KEY,
  -- sha256 of the bearer token. The token itself is shown once, to its
  -- owner, and is not recoverable from here.
  hash       TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL,
  label      TEXT
);
