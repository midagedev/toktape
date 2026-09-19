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
