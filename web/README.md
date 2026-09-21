# web — the toktape hub

The Cloudflare Worker behind `tape.midagedev.com`, where a published run
lives. The design is `docs/toktape-spec.ko.md` §9; the work is TTP-115.

**The Worker never parses a tape.** The Go client derives a versioned index
row next to the schema (`internal/publish`) and uploads it beside the record,
so knowledge of the tape's shape has exactly one owner. The upload contract is
the doc block on `publish.Client` in `internal/publish/client.go`, and
`internal/publish/client_test.go` parses a request body the way this side
does — the Worker is written against those, never the other way round.

The same rule decides where the share card is drawn. Drawing it means reading
the whole tape, so the client renders it from the public view and uploads it;
this side stores an image it treats as opaque.

## What it answers

| | |
|---|---|
| `POST /api/v1/runs` | publish one run: parts `tape`, `index`, `card`, `private` |
| `DELETE /api/v1/runs/<id>` | take it down, with its delete token or its owner's |
| `GET /api/v1/runs` | the search, as JSON |
| `GET /` | the search, as a page |
| `GET /r/<id>` | one run |
| `GET /r/<id>.png` | the share card, which is the link's preview image |
| `GET /r/<id>.toktape`, `.tape` | the record, as it was uploaded |
| `GET /r/<id>.json` | the index row |
| `GET /healthz` | whether the service is up, separately from whether the name is ours |

There is no sort parameter, and that is a decision rather than an omission:
newest first is the only order, because a service that lets you sort by
tok/s is a leaderboard however it is labelled (§9.4). Every row carries the
caveats the card would print, for the other half of the same reason.

## Running it

```sh
cd web
npm install
npm run migrate:local   # the miniflare D1 the dev server and the harness use
npm run dev             # locally, on http://127.0.0.1:8787
npm run deploy          # production; the lead does this, with the user's approval
./check.sh              # the gate — run it before committing anything here
```

`check.sh` is the only test that means anything on this side: it starts a real
`workerd`, publishes `assets/hero.tape` with the real `toktape` binary, and
checks the record comes back as a run file, the card comes back as a PNG, the
row is in the search, `--private` stays out of it, the delete token opens the
run and nothing else does, and the anonymous limit is really enforced. A
hand-written curl of what we believe the client sends would be this side
grading its own homework.

`migrations/` is the D1 schema, applied to the local database first and to the
remote one only as part of a deploy, which is outward-facing. **Migrations go
first**: deploying code that inserts into a table the remote database does not
have yet is a window where every upload fails. Deploying uses the scoped token
`toktape-hub-deploy`, never the account's global API key.

The Worker needs one secret, `RATE_SALT` (`wrangler secret put RATE_SALT`, 32
random bytes). Without it anonymous uploads are refused rather than served
unlimited. It salts the per-address hash the upload counter is keyed on, so
that nothing here has to store an address: an unsalted hash of an IPv4 address
is reversible by enumeration, and this service strips the place a run happened
out of every tape it publishes.

Cloudflare's rate-limiting binding was tried first and measured not to count
in production — see `src/ratelimit.js`.

## Hosting your own

The Worker is host-independent: every link it builds comes from the
`PUBLIC_BASE_URL` var and nothing else, so the same code serves any hostname.
A team can run its own hub with the steps below; `npm run dev` (which serves
`http://127.0.0.1:8787` with a local `RATE_SALT`) is the cheap way to see them
work before paying for any of it.

**Prerequisites.** A Cloudflare account, a clone of this repository, Node.js
(npm) and Go for the player build, and `brotli` on PATH — the wasm size gate
in `player/build.sh` refuses to pass without it.

**1. Create the stores.** D1 holds the index rows, R2 holds the records and
cards:

```sh
npx wrangler d1 create toktape     # prints the database_id
npx wrangler r2 bucket create toktape
```

`wrangler.jsonc` expects binding `DB` on the D1 database and binding `TAPES`
on the R2 bucket — the binding names are what the code reads and stay as they
are; the database and bucket names can be your own, in which case change them
in `wrangler.jsonc` (`d1_databases`, `r2_buckets`) along with the
`database_id` the create command printed.

**2. Apply the migrations** (`migrations/`, eight of them today):

```sh
npm run migrate:local    # the dev server's miniflare D1
npm run migrate:remote   # the real one, before the first deploy
```

Migrations go first: deploying code that inserts into a table the remote
database does not have yet is a window where every upload fails.

**3. Name the service, and remove our page-view counter.** Delete
`vars.CF_BEACON_TOKEN` from `wrangler.jsonc`: it is tape.midagedev.com's
Cloudflare Web Analytics site, and with it gone no page carries a beacon or
the sentence about one (`src/analytics.js`). Put your own site's token there
if you want the count. Then set `vars.PUBLIC_BASE_URL` in `wrangler.jsonc` to
the URL your Worker will answer on — every receipt's link, every `og:image`
and the sitemap are built from it, and it is declared rather than derived
from the request because `wrangler dev` simulates the configured route
(`src/http.js`). Put that hostname in `routes` as a custom domain, the shape
the file ships with: the Worker owns the whole hostname and Cloudflare
manages its DNS record. The two must agree.

**4. Set the one secret.** `npx wrangler secret put RATE_SALT` — 32 random
bytes. Without it anonymous uploads are refused rather than served unlimited
(see above).

**5. Deploy.** `npm run deploy` builds the browser player (Go to wasm,
size-gated) and runs `wrangler deploy`.

**6. Mint a journal token.** There is no issuance endpoint and no script for
this yet — the repo's own harness mints its token with a direct INSERT
(`web/check.sh`), and that is the real mechanism today:

```sh
token="tk_$(openssl rand -hex 16)"
hash=$(printf '%s' "$token" | shasum -a 256 | cut -d' ' -f1)   # sha256sum on Linux
npx wrangler d1 execute toktape --remote --command \
  "INSERT INTO tokens (id, hash, created_at, label) VALUES ('my-handle', '$hash', datetime('now'), 'manual mint')"
printf 'journal token, shown once: %s\n' "$token"
```

The service stores only the sha256; the string is not recoverable, so keep
what you printed. The `id` becomes the handle the token's user home lives at
(`/u/my-handle`) and must be 2–32 characters of lowercase letters, digits and
dashes, starting with a letter or digit (`src/user.js`'s route).

**7. Point toktape at it** on each machine, in `~/.toktape/config.toml`:

```toml
service = "https://tapes.example.com"
token   = "tk_…"   # the one you minted — token and service are a pair
```

or `TOKTAPE_SERVICE=https://tapes.example.com` for one shell, or `--url` per
command (`toktape help service` has the whole order, and the normalisation
rules). The `token` belongs to `service`: aimed at any other host, the journal
token stays home — a publish goes anonymous, `runs --mine` and
`publish --edit` refuse — unless `--token` says otherwise. Anonymous
publishes need no setup at all, and each one's delete token is filed under
`~/.toktape/published.json` together with the service that issued it, so
`toktape publish --delete <id>` takes it down with no `--url`.

## If the database is lost

R2 holds every record and card, so the bytes a publisher uploaded are never
the thing at risk. D1 holds what R2 cannot say: that a row exists, who owns
it (the delete token), and when it was published. Two paths back, in order:

```sh
npx wrangler d1 time-travel info toktape                  # the current bookmark
npx wrangler d1 time-travel restore toktape --bookmark=<bookmark>
npx wrangler d1 export toktape --remote --output <file>   # a snapshot, before anything risky
```

Time Travel is on by default and reaches back 30 days, so it answers a bad
migration or a wrong `DELETE` — the failures that actually happen. It does
not answer a loss older than that, which is what the export is for: take one
before a migration, and keep it out of the repo, because the `runs` table
carries owner tokens.

Restoring rows recovers existence and ownership. The index columns beside
them are derived, not authored, so a publisher can rebuild their own with
`toktape reindex <id>...`: it reads each record back, re-derives every index
field through the same public view the client publishes, and patches the row
with the journal token that owns it.
