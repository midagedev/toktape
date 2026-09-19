#!/usr/bin/env bash
# The Worker's gate: the real client publishes to a real local Worker.
#
# This is the test that proves the direction of the contract. The Worker is
# written against the doc block on publish.Client and the request body
# internal/publish/client_test.go parses, so the only check that means
# anything is the actual `toktape publish` binary talking to an actual
# workerd, over miniflare's R2 and D1 — a hand-written curl of what we think
# the client sends would be this side grading its own homework.
#
# It is not part of ./scripts/check.sh: that gate is Go and must stay
# runnable without node. Run this before committing anything under web/.
#
# HOME is pointed at a temp directory for the same reason
# cmd/toktape/publish_test.go does it: the real ~/.toktape/config.toml holds
# a token, and a token for the hosted service has no business reaching a
# development server.
set -euo pipefail

cd "$(dirname "$0")"
repo="$(cd .. && pwd)"
port="${PORT:-8787}"
base="http://127.0.0.1:$port"
work="$(mktemp -d)"
dev_pid=""

cleanup() {
  # The pid we started, never a pgrep pattern: a pattern matches the shell
  # that is searching for it and the wait never ends.
  if [ -n "$dev_pid" ] && kill -0 "$dev_pid" 2>/dev/null; then
    kill "$dev_pid" 2>/dev/null || true
    wait "$dev_pid" 2>/dev/null || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

say() { printf '\n== %s\n' "$1"; }
# curl piped straight into grep -q is a race: grep exits on the first match,
# curl gets SIGPIPE writing the rest, and pipefail reads that as a failed
# check (2026-09-18: host.js grew past one buffer and "is not served"
# started firing on a file that was served). So a body is fetched to a
# file and the file is grepped.
die() { printf 'web/check.sh: %s\n' "$1" >&2; exit 1; }

say "building the client"
go build -o "$work/toktape" "$repo/cmd/toktape"

say "building the player"
# The player is served out of player/dist as static assets, and wrangler
# refuses to start without that directory. Building it here also puts the
# size budget and the node smoke in front of every run of this gate.
./player/build.sh >"$work/player.log" 2>&1 || { cat "$work/player.log"; die "the player did not build"; }
tail -n 12 "$work/player.log"

say "local D1"
npx wrangler d1 migrations apply toktape --local >"$work/migrate.log" 2>&1 ||
  { cat "$work/migrate.log"; die "migrations failed"; }
# The local D1 outlives one run of this gate, and the anonymous rate counter
# lives in it: two runs inside a minute made the second one's fourth
# anonymous publish a 429 (2026-09-19). The counter is state about the
# previous run, not this one, so it is cleared before the server starts.
npx wrangler d1 execute toktape --local --command "DELETE FROM upload_rate" >"$work/rate.log" 2>&1 ||
  { cat "$work/rate.log"; die "could not clear the local rate counter"; }

say "wrangler dev on $base"
npx wrangler dev --port "$port" --inspector-port 0 \
  --var "PUBLIC_BASE_URL:$base" --var "RATE_SALT:local-harness-salt" \
  >"$work/dev.log" 2>&1 &
dev_pid=$!
for _ in $(seq 1 60); do
  if curl -fsS "$base/healthz" >/dev/null 2>&1; then break; fi
  kill -0 "$dev_pid" 2>/dev/null || { cat "$work/dev.log"; die "wrangler dev exited"; }
  sleep 1
done
curl -fsS "$base/healthz" >"$work/fetched" && grep -q '"live":true' "$work/fetched" ||
  { cat "$work/dev.log"; die "the dev server never answered /healthz"; }

say "publish"
export HOME="$work/home"
mkdir -p "$HOME"
"$work/toktape" publish "$repo/assets/hero.tape" --url "$base" --yes \
  >"$work/receipt" 2>"$work/publish.err" ||
  { cat "$work/publish.err"; die "publish failed"; }
url="$(tr -d '\r\n' <"$work/receipt")"
id="${url##*/}"
[ -n "$id" ] || die "the receipt carried no link"
grep -q 'Delete token: dt_' "$work/publish.err" ||
  { cat "$work/publish.err"; die "an anonymous upload got no delete token"; }
printf 'published %s\n' "$url"
# The link is the whole product, so it is fetched rather than eyeballed: a
# receipt that names a host the run is not on looks exactly like one that
# works until somebody clicks it.
curl -fsS "$url" -o /dev/null || die "the receipt's own link does not open: $url"

say "the author and the note"
# The profile is set once per machine (web/static/favicon.png is a 64x64
# PNG, 7943 bytes — inside every limit) and travels with the publish; the
# note's title and body travel as their own parts beside it.
"$work/toktape" profile --name "Lab Rat" --link https://github.com/example --avatar "$repo/web/static/favicon.png" \
  >"$work/profile.txt" 2>&1 ||
  { cat "$work/profile.txt"; die "profile set failed"; }
for want in 'Lab Rat' 'https://github.com/example' '7943 bytes'; do
  grep -q "$want" "$work/profile.txt" || { cat "$work/profile.txt"; die "the profile save does not name $want"; }
done
"$work/toktape" publish "$repo/assets/hero.tape" --url "$base" \
  --title "First ik_llama sweep" --note "$(printf 'Trying -fa on.\n\nSecond paragraph.')" \
  >"$work/receipt3" 2>"$work/publish3.err" ||
  { cat "$work/publish3.err"; die "a profiled publish failed"; }
url2="$(tr -d '\r\n' <"$work/receipt3")"; id2="${url2##*/}"
[ -n "$id2" ] || die "the profiled receipt carried no link"
# The title becomes the h1 and the model moves to the sub line; the byline
# carries the avatar and the linked name; the note renders as paragraphs.
curl -fsS "$base/r/$id2" >"$work/page2.html"
grep -q '<h1>First ik_llama sweep</h1>' "$work/page2.html" || die "the note's title is not the h1"
grep -q 'class="avatar"' "$work/page2.html" || die "the byline carries no avatar"
grep -q 'href="https://github.com/example"' "$work/page2.html" || die "the byline does not link the author"
grep -q '<p>Second paragraph.</p>' "$work/page2.html" || die "the note's second paragraph is not rendered"
# 2026-09-19: `ugc` joined the rel. The assertion moved with the code rather
# than being widened — an author link is a stranger's URL typed into an
# anonymous upload, and `nofollow ugc` is how a page says so to a crawler.
grep -q 'rel="nofollow ugc noopener"' "$work/page2.html" || die "the author link carries no rel"
# The row and the API carry the author, the title and the note beside them.
curl -fsS "$base/api/v1/runs" >"$work/list2.json"
grep -q '"title":"First ik_llama sweep"' "$work/list2.json" || { cat "$work/list2.json"; die "the listing carries no title"; }
grep -q '"name":"Lab Rat"' "$work/list2.json" || die "the listing carries no author name"
# The avatar path out of the JSON, fetched to a file: really a PNG, served
# as image/png.
avapath="$(grep -o '/a/[0-9a-f]\{64\}\.png' "$work/list2.json" | head -n 1)"
[ -n "$avapath" ] || { cat "$work/list2.json"; die "the listing carries no avatar path"; }
ctype=$(curl -fsS -o "$work/avatar.png" -w '%{content_type}' "$base$avapath")
case "$ctype" in image/png*) ;; *) die "the avatar is served as $ctype" ;; esac
head -c 8 "$work/avatar.png" | od -An -tx1 | tr -d ' \n' | grep -q '^89504e470d0a1a0a$' ||
  die "what came back from $avapath is not a PNG"
# Negatives, each a hand curl because the client refuses first: a scheme
# that is not http(s), a title past 120 runes, an avatar past 65536 bytes.
# Distinct addresses, so the probes spend their own rate budget and not the
# binary publishes' one.
code=$(curl -s -o "$work/body" -w '%{http_code}' -X POST "$base/api/v1/runs" \
  -H "CF-Connecting-IP: 203.0.113.20" \
  -F 'tape=@'"$repo/assets/hero.tape"';filename=run.tape' -F 'index={"schema":1,"model_raw":"neg probe"}' \
  -F 'author={"link":"javascript:alert(1)"}')
[ "$code" = "400" ] || { cat "$work/body"; die "a javascript: author link was not refused (got $code)"; }
grep -q 'javascript' "$work/body" || { cat "$work/body"; die "the refusal does not name the scheme"; }
curl -fsS "$base/r/$id2" >"$work/fetched" && grep -q 'javascript:' "$work/fetched" && die "a scheme survived onto the page"
title121="$(printf 't%.0s' $(seq 1 121))"
code=$(curl -s -o "$work/body" -w '%{http_code}' -X POST "$base/api/v1/runs" \
  -H "CF-Connecting-IP: 203.0.113.21" \
  -F 'tape=@'"$repo/assets/hero.tape"';filename=run.tape' -F 'index={"schema":1,"model_raw":"neg probe"}' \
  -F "title=$title121")
[ "$code" = "400" ] || { cat "$work/body"; die "a 121-rune title was not refused (got $code)"; }
head -c 70000 /dev/zero >"$work/big.png"
code=$(curl -s -o "$work/body" -w '%{http_code}' -X POST "$base/api/v1/runs" \
  -H "CF-Connecting-IP: 203.0.113.22" \
  -F 'tape=@'"$repo/assets/hero.tape"';filename=run.tape' -F 'index={"schema":1,"model_raw":"neg probe"}' \
  -F 'author={"name":"Big"}' -F 'avatar=@'"$work/big.png"';filename=avatar.png')
case "$code" in 400|413) ;; *) cat "$work/body"; die "a 70000-byte avatar was not refused (got $code)" ;; esac
# One run travels without the profile: no avatar, no name anywhere.
"$work/toktape" publish "$repo/assets/hero.tape" --url "$base" --no-profile \
  >"$work/receipt4" 2>"$work/publish4.err" ||
  { cat "$work/publish4.err"; die "a --no-profile publish failed"; }
url4="$(tr -d '\r\n' <"$work/receipt4")"; id4="${url4##*/}"
curl -fsS "$base/r/$id4" >"$work/page4.html"
grep -q 'class="avatar"' "$work/page4.html" && die "a --no-profile page carries an avatar"
grep -q 'Lab Rat' "$work/page4.html" && die "a --no-profile page names the profile"

say "the record comes back as a run file"
curl -fsS "$base/r/$id.toktape" -o "$work/downloaded.tape"
"$work/toktape" card "$work/downloaded.tape" >"$work/card.txt" 2>&1 ||
  { cat "$work/card.txt"; die "the downloaded record does not load"; }
# The public view is what was uploaded, so the place the run happened must
# not have survived the round trip. Path shapes only: the field-by-field
# proof is TestNoPlaceSurvivesAnywhereInTheSummary, and a bare hostname
# pattern here would fire on any card that printed a word ending in it.
if grep -qE '(^| )(/|~/)[^ ]' "$work/card.txt"; then
  cat "$work/card.txt"
  die "the downloaded record still carries the place it ran"
fi

say "the card the link previews as"
# Unauthenticated, image/png, and really a PNG: get any of the three wrong
# and every preview everywhere renders nothing, with no error to notice.
ctype=$(curl -fsS -o "$work/card.png" -w '%{content_type}' "$base/r/$id.png")
case "$ctype" in image/png*) ;; *) die "the card is served as $ctype" ;; esac
head -c 8 "$work/card.png" | od -An -tx1 | tr -d ' \n' | grep -q '^89504e470d0a1a0a$' ||
  die "what came back from /r/<id>.png is not a PNG"
[ "$(wc -c <"$work/card.png")" -gt 10000 ] || die "the card is too small to be the card"

say "the page and the row"
curl -fsS "$base/r/$id" >"$work/page.html"
grep -q '<title>' "$work/page.html" || die "/r/<id> is not a page"
# TTP-126: the paperwork paragraph carries a run id inside inline code — one
# unbreakable token. Without a break rule scoped to the prose it pushes past
# the column (and over the margin figure on a wide viewport).
grep -q 'footer code.*overflow-wrap: anywhere' "$work/page.html" ||
  die "inline code in the run page's prose can overflow its column again (TTP-126)"
# An og:image must be absolute or a crawler will not fetch it.
grep -q "og:image\" content=\"$base/r/$id.png" "$work/page.html" ||
  { grep -o 'og:image[^>]*' "$work/page.html"; die "the page has no absolute og:image"; }
# What X, Slack and a search engine each read: the twitter pair beside the
# og pair, a title that leads with the figure, a description, a canonical.
# One missing tag renders as a bare link with no error anywhere.
for tag in 'name="twitter:card" content="summary_large_image"' 'name="twitter:image" content="'"$base/r/$id"'.png"' \
  'property="og:image:alt"' 'name="twitter:title"' 'name="description"' 'rel="canonical" href="'"$base/r/$id"'"' \
  'property="og:site_name" content="toktape"'; do
  grep -q "$tag" "$work/page.html" || die "the page's head lacks $tag"
done
grep -q '<meta property="og:title" content="[0-9.]* tok/s · ' "$work/page.html" ||
  { grep -o 'og:title[^>]*' "$work/page.html"; die "the share title does not lead with the figure"; }
grep -q 'content="noindex"' "$work/page.html" && die "a public run asks not to be indexed"
curl -fsS "$base/robots.txt" >"$work/fetched" && grep -q "^Sitemap: $base/sitemap.xml" "$work/fetched" || die "robots.txt does not name the sitemap"
curl -fsS "$base/sitemap.xml" >"$work/fetched" && grep -q "<loc>$base/r/$id</loc>" "$work/fetched" || die "the sitemap does not list the run"
curl -fsS "$base/r/$id.json" >"$work/row.json"
grep -q '"schema": *1' "$work/row.json" || { cat "$work/row.json"; die "the index row is not there"; }

say "the security headers"
# Set once around the router (web/src/index.js:secured), so the thing worth
# asserting is not "the front page has them" but "no routed path answers
# without them" — a header added per handler is a header the next handler
# forgets. Static assets are deliberately not in this list: they never reach
# the Worker, and the comment beside `secured` says why that is left alone.
# Every shape of response this service produces is probed: HTML, JSON, a
# PNG out of R2, the record itself, an API listing, and a 404, which is the
# one a per-handler approach always misses.
for p in "/" "/r/$id" "/r/$id.json" "/r/$id.png" "/r/$id.toktape" "/api/v1/runs" "/healthz" "/no/such/path"; do
  curl -sS -D "$work/headers" -o /dev/null "$base$p"
  grep -qi '^x-content-type-options: nosniff' "$work/headers" ||
    { cat "$work/headers"; die "$p answers without X-Content-Type-Options"; }
  grep -qi '^referrer-policy: strict-origin-when-cross-origin' "$work/headers" ||
    { cat "$work/headers"; die "$p answers without Referrer-Policy"; }
done

say "the actions under the stage"
# The copy button carries the public link (absolute, from PUBLIC_BASE_URL),
# and the mp4 is named after the run, so a downloads folder tells two apart.
grep -q 'class="act copy" type="button" data-url="'"$base"'/r/'"$id"'"' "$work/page.html" ||
  die "the copy button does not carry the run's public link"
grep -q 'class="act mp4" type="button" data-name="'"$id"'.mp4"' "$work/page.html" ||
  die "the mp4 button is not named after the run"
# The Details section: the transcript and the record's paperwork, read out of
# the tape in the browser by the wasm player. It finds its tape through the
# page's own player, never a data-tape of its own — host.js treats every
# [data-tape] as a stage — so it carries no such attribute.
grep -q '<section class="details">' "$work/page.html" ||
  die "the page has no Details section"
grep -q 'class="act load"' "$work/page.html" ||
  die "the Details section has no Load button"

say "the player is served"
grep -q 'data-tape="/r/'"$id"'.toktape"' "$work/page.html" || die "the page does not name the record for Replay"
grep -q 'src="/player/host.js"' "$work/page.html" || die "the page does not load the player"
# application/wasm is what lets the browser compile while it downloads; the
# wrong type is not an error, it is a slower page, so it is checked here.
ctype=$(curl -fsS -o "$work/player.wasm" -w '%{content_type}' "$base/player/toktape.wasm")
case "$ctype" in application/wasm*) ;; *) die "the wasm is served as $ctype" ;; esac
[ "$(wc -c <"$work/player.wasm")" -gt 1000000 ] || die "what came back from /player/toktape.wasm is too small to be the player"
curl -fsS "$base/player/wasm_exec.js" >"$work/fetched" && grep -q 'globalThis.Go' "$work/fetched" || die "wasm_exec.js is not served"
curl -fsS "$base/player/host.js" >"$work/fetched" && grep -q 'data-tape' "$work/fetched" || die "host.js is not served"

say "the mascot"
# Every page carries the avatar in its brand row, and the file it points at
# is a real PNG from web/static, copied into dist/ by build.sh — so a build
# that forgets the copy step fails here and not on the live site.
grep -q '<img class="mascot" src="/mascot.png"' "$work/page.html" || die "the run page has no mascot in the brand row"
ctype=$(curl -fsS -o "$work/mascot.png" -w '%{content_type}' "$base/mascot.png")
case "$ctype" in image/png*) ;; *) die "the mascot is served as $ctype" ;; esac
[ "$(head -c 4 "$work/mascot.png" | od -An -tx1 | tr -d ' ')" = "89504e47" ] || die "what came back from /mascot.png is not a PNG"
[ "$(wc -c <"$work/mascot.png")" -gt 10000 ] || die "the mascot is too small to be the image"
# The favicon is her too, and each page has its pose in the margin: the run
# page sleeps, the front page peeks. The poses are WebP with alpha, and a
# wrong content type here means the browser shows a broken image where the
# sticker should be.
grep -q '<link rel="icon" type="image/png" sizes="64x64" href="/favicon.png">' "$work/page.html" || die "the page does not link the PNG favicon"
grep -q '<img class="figure sleep" src="/mascot-sleep.webp"' "$work/page.html" || die "the run page has no sleeping figure"
curl -fsS "$base/" >"$work/front.html"
grep -q '<img class="figure peek" src="/mascot-peek.webp"' "$work/front.html" || die "the front page has no peeking figure"
grep -q '<img class="figure wave" src="/mascot-wave.webp"' "$work/front.html" || die "the front page has no waving figure"
for f in favicon.png apple-touch-icon.png; do
  ctype=$(curl -fsS -o "$work/asset" -w '%{content_type}' "$base/$f")
  case "$ctype" in image/png*) ;; *) die "$f is served as $ctype" ;; esac
done
for f in mascot-peek.webp mascot-sleep.webp mascot-sit.webp mascot-wave.webp; do
  ctype=$(curl -fsS -o "$work/asset" -w '%{content_type}' "$base/$f")
  case "$ctype" in image/webp*) ;; *) die "$f is served as $ctype" ;; esac
  [ "$(head -c 4 "$work/asset")" = "RIFF" ] || die "what came back from /$f is not a WebP"
done
# The empty state shows her sitting with the tape: a filter nothing matches.
curl -fsS "$base/?gpu=no-such-gpu-ever" >"$work/empty.html"
grep -q '<img class="empty-figure" src="/mascot-sit.webp"' "$work/empty.html" || die "the empty state has no sitting figure"
# A path under /player/ that is not an asset falls through to the Worker
# rather than into the asset handler's own 404, so a stale asset directory
# cannot swallow a route this code owns.
code=$(curl -s -o "$work/body" -w '%{http_code}' "$base/player/nothing-here")
grep -q 'no such path' "$work/body" || { cat "$work/body"; die "an unknown asset path did not reach the Worker (got $code)"; }

say "the search"
curl -fsS "$base/api/v1/runs" >"$work/list.json"
grep -q "\"id\":\"$id\"" "$work/list.json" || { cat "$work/list.json"; die "the run is not in the listing"; }
# The same listing from the terminal (TTP-129, 2026-09-19): `toktape runs`
# is the real binary reading the real Worker, and `show --save` round-trips
# the record so `toktape card` can draw it again from what came back.
"$work/toktape" runs --url "$base" --limit 5 >"$work/runs.txt" 2>"$work/runs.err" ||
  { cat "$work/runs.err"; die "toktape runs failed against the local Worker"; }
grep -q "^$id " "$work/runs.txt" || { cat "$work/runs.txt"; die "toktape runs does not list the hero"; }
grep -q 'DECODE tok/s' "$work/runs.txt" || die "toktape runs prints no table header"
"$work/toktape" runs --url "$base" --limit 2 -o json >"$work/runs.json" 2>"$work/runs.err" ||
  { cat "$work/runs.err"; die "toktape runs -o json failed"; }
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d["scope"]=="public" and len(d["runs"])==2, d' "$work/runs.json" ||
  die "toktape runs -o json is not the service body"
"$work/toktape" show "$base/r/$id" --url "$base" --save "$work/fetched.tape" >"$work/show.txt" 2>"$work/show.err" ||
  { cat "$work/show.err"; die "toktape show failed against the local Worker"; }
grep -q '^decode: ' "$work/show.txt" || { cat "$work/show.txt"; die "toktape show prints no decode line"; }
# Not the file on disk: publish uploads the view with hostnames and paths
# removed, so the saved record is compared with what the service serves.
curl -fsS "$base/r/$id.toktape" -o "$work/served.tape"
cmp -s "$work/fetched.tape" "$work/served.tape" || die "show --save did not return the bytes the service serves"
"$work/toktape" card "$work/fetched.tape" >"$work/fetched-card.txt" 2>&1 || { cat "$work/fetched-card.txt"; die "the fetched record does not draw a card"; }
# Every row carries the qualification the card prints (§9.4).
grep -q '"caveat_count"' "$work/list.json" || die "a listing row dropped its caveat count"
# A GPU filter that reaches a mixed rig (TTP-124, 2026-09-19). There is no
# mixed-rig tape in the repo, so this one row is inserted straight into the
# local D1 the way the harness mints tokens below — OR REPLACE, because the
# local D1 outlives one run of this gate.
npx wrangler d1 execute toktape --local --command \
  "INSERT OR REPLACE INTO runs (id, created_at, tape_key, tape_ext, tape_bytes, index_schema, index_json, gpus_raw, gpu_count, gpu_ids, decode_per_sec, caveat_count) VALUES ('mixedrig000000000000', '2026-01-02T00:00:00Z', 'none', '.tape', 1, 1, '{}', 'NVIDIA RTX A6000 / NVIDIA GeForce RTX 3090', 2, '[\"rtx-a6000\",\"rtx-3090\"]', 12.5, 0)" \
  >"$work/mixed.log" 2>&1 || { cat "$work/mixed.log"; die "could not insert the mixed-rig harness row"; }
# Membership, not equality: either card reaches the rig. The rig's created_at
# is old on purpose, so the a6000 fetch reads oldest-first: the local D1
# outlives one run of this gate (140 rows on 2026-09-19) and a newest-first
# page 1 ends long before the rig.
curl -fsS "$base/api/v1/runs?gpu=rtx-3090&limit=100" >"$work/mixed3090.json" && grep -q 'mixedrig000000000000' "$work/mixed3090.json" ||
  die "filtering by rtx-3090 missed the mixed rig"
curl -fsS "$base/api/v1/runs?gpu=rtx-a6000&sort=oldest&limit=100" >"$work/mixeda6000.json" && grep -q 'mixedrig000000000000' "$work/mixeda6000.json" ||
  die "filtering by rtx-a6000 missed the mixed rig"
# The hero under its own GPU: read off list.json, never hardcoded — and when
# that is one of the rig's cards the mixed rig rides along with it. The hero
# is this run's newest row, so page 1 has it.
hero_gpu="$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(next(((r.get("gpu_id") or "") for r in d["runs"] if r["id"]==sys.argv[2]), ""))' "$work/list.json" "$id")"
[ -n "$hero_gpu" ] || die "the hero row carries no gpu_id in list.json"
curl -fsS "$base/api/v1/runs?gpu=$hero_gpu" >"$work/fetched" && grep -q "\"id\":\"$id\"" "$work/fetched" ||
  die "filtering by $hero_gpu dropped the hero"
case "$hero_gpu" in
rtx-a6000) grep -q 'mixedrig000000000000' "$work/mixeda6000.json" || die "filtering by $hero_gpu missed the mixed rig" ;;
rtx-3090) grep -q 'mixedrig000000000000' "$work/mixed3090.json" || die "filtering by $hero_gpu missed the mixed rig" ;;
*) printf 'note: the hero runs on %s, outside the mixed rig; mixed reach is asserted above\n' "$hero_gpu" ;;
esac
# The dropdown lists both member ids, and the mixed row wears two GPU chips.
# Same old-row reason: the chips are read off an oldest-first page, where the
# rig is first. The dropdown counts the whole population on any page.
curl -fsS "$base/?sort=oldest&limit=10" >"$work/fetched" && grep -q '<option value="rtx-3090"' "$work/fetched" ||
  die "the GPU dropdown lists no rtx-3090"
grep -q '<option value="rtx-a6000"' "$work/fetched" || die "the GPU dropdown lists no rtx-a6000"
# A chip link accumulates the current URL's params, so the href carries the
# page's own sort/limit beside the gpu — match the filter link, not the URL.
grep -o '<a class="fact link" href="[^"]*"' "$work/fetched" | grep -q 'gpu=rtx-a6000' ||
  die "the mixed row wears no rtx-a6000 chip"
grep -o '<a class="fact link" href="[^"]*"' "$work/fetched" | grep -q 'gpu=rtx-3090' ||
  die "the mixed row wears no rtx-3090 chip"
# Harness row, harness cleanup: the listing below must see only real publishes.
npx wrangler d1 execute toktape --local --command "DELETE FROM runs WHERE id = 'mixedrig000000000000'" \
  >"$work/mixed-clean.log" 2>&1 || { cat "$work/mixed-clean.log"; die "could not clean up the mixed-rig harness row"; }

say "the figures reach the columns, the rows and the search"
# One harness row straight into the local D1 (TTP-130): there is no
# figures-carrying tape in the repo, so like the mixed rig this row is
# inserted the way the harness mints tokens — OR REPLACE, because the local
# D1 outlives one run of this gate. index_json carries the contract fields
# and the columns beside it match, the way a publish would have stored them.
npx wrangler d1 execute toktape --local --command \
  "INSERT OR REPLACE INTO runs (id, created_at, tape_key, tape_ext, tape_bytes, index_schema, index_json, model_id, model_raw, prompt_n, predicted_n, min_predicted_n, cache_hit_ratio, ctx_size, n_slots, fa, kv_cache, offload, active_params, params, moe, n_experts, n_experts_used, prefill_per_sec, throttled, power_w, power_limit_w) VALUES ('figsrow0000000000000', '2026-01-03T00:00:00Z', 'none', '.tape', 1, 1, '{\"schema\":1,\"model_id\":\"harness-figs\",\"model_raw\":\"harness-figs.gguf\",\"prompt_n\":512,\"predicted_n\":128,\"min_predicted_n\":12,\"cache_hit_ratio\":0.41,\"ctx_size\":32768,\"n_slots\":4,\"fa\":\"on\",\"kv_cache\":\"q8_0\",\"offload\":\"partial\",\"active_params\":3000000000,\"params\":35000000000,\"moe\":true,\"n_experts\":128,\"n_experts_used\":8,\"prefill_per_sec\":1234.5,\"throttled\":true,\"power_w\":281,\"power_limit_w\":300}', 'harness-figs', 'harness-figs.gguf', 512, 128, 12, 0.41, 32768, 4, 'on', 'q8_0', 'partial', 3000000000, 35000000000, 1, 128, 8, 1234.5, 1, 281, 300)" \
  >"$work/figs.log" 2>&1 || { cat "$work/figs.log"; die "could not insert the figures harness row"; }
# Refusals: a band that is not there, and a floor that is not a number.
# A search longer than SQLite's LIKE pattern limit is refused, not thrown
# (lead, 2026-09-19). Before this the 49th character reached the database and
# the page answered 500 — measured on production, where pasting a model path
# into the box was enough to do it. 48 passes, 49 is a 400 that names the
# number, and the box carries the same number as maxlength so a person cannot
# type their way into the refusal.
q48=$(python3 -c "print('a'*48)")
q49=$(python3 -c "print('a'*49)")
code=$(curl -s -o "$work/body" -w '%{http_code}' "$base/api/v1/runs?q=$q48")
[ "$code" = "200" ] || { cat "$work/body"; die "a 48-character search was refused (got $code)"; }
code=$(curl -s -o "$work/body" -w '%{http_code}' "$base/api/v1/runs?q=$q49")
[ "$code" = "400" ] || { cat "$work/body"; die "a search past the LIKE pattern limit was not refused (got $code)"; }
grep -q '48 is the most' "$work/body" || { cat "$work/body"; die "the search refusal does not name the limit"; }
code=$(curl -s -o "$work/body" -w '%{http_code}' "$base/?q=$q49")
[ "$code" = "400" ] || { cat "$work/body"; die "the page did not refuse an over-long search (got $code)"; }
curl -fsS "$base/" >"$work/body" && grep -q 'name="q" maxlength="48"' "$work/body" ||
  { die "the search box carries no maxlength, so a person can type past the limit"; }

# A numeric axis given something that is not a number refuses by name rather
# than binding NaN, which SQLite compares as NULL — so these used to answer
# "no runs", which is an answer to a question nobody asked (lead, 2026-09-19).
for bad in "sessions=abc" "min_vram=abc"; do
  code=$(curl -s -o "$work/body" -w '%{http_code}' "$base/api/v1/runs?$bad")
  [ "$code" = "400" ] || { cat "$work/body"; die "$bad was not refused (got $code)"; }
done
curl -fsS "$base/api/v1/runs?sessions=4&limit=1" >/dev/null || die "a real stream count was refused"
curl -fsS "$base/api/v1/runs?min_vram=24&limit=1" >/dev/null || die "a real VRAM floor was refused"

code=$(curl -s -o "$work/body" -w '%{http_code}' "$base/api/v1/runs?size=999")
[ "$code" = "400" ] || { cat "$work/body"; die "an unknown size was not refused (got $code)"; }
grep -q 'unknown size' "$work/body" || { cat "$work/body"; die "the size refusal does not name the size"; }
code=$(curl -s -o "$work/body" -w '%{http_code}' "$base/api/v1/runs?min_predicted=x")
[ "$code" = "400" ] || { cat "$work/body"; die "a non-integer min_predicted was not refused (got $code)"; }
# The bands are over active_params: 3B active sits in band 0, not band 35.
# Oldest-first, like the mixed rig, on every one of these: the harness row's
# created_at is old on purpose, and the local D1 outlives one run of this
# gate — each run publishes the hero again, and the hero is itself a 3B-active
# MoE, so a newest-first page of 100 fills with hero copies and never reaches
# the row under test (lead, 2026-09-19: moe=1 failed exactly this way once the
# local D1 had enough of them).
curl -fsS "$base/api/v1/runs?size=0&sort=oldest&limit=100" >"$work/figs0.json" && grep -q 'figsrow0000000000000' "$work/figs0.json" ||
  die "filtering by size=0 missed the figures row"
curl -fsS "$base/api/v1/runs?size=35&sort=oldest&limit=100" >"$work/figs35.json" && grep -q 'figsrow0000000000000' "$work/figs35.json" &&
  die "filtering by size=35 kept a 3B-active row"
curl -fsS "$base/api/v1/runs?moe=1&sort=oldest&limit=100" >"$work/figsmoe.json" && grep -q 'figsrow0000000000000' "$work/figsmoe.json" ||
  die "filtering by moe=1 missed the figures row"
curl -fsS "$base/api/v1/runs?min_predicted=200&sort=oldest&limit=100" >"$work/figs200.json" && grep -q 'figsrow0000000000000' "$work/figs200.json" &&
  die "a min_predicted=200 floor kept a 128-out row"
curl -fsS "$base/api/v1/runs?min_predicted=100&sort=oldest&limit=100" >"$work/figs100.json" && grep -q 'figsrow0000000000000' "$work/figs100.json" ||
  die "a min_predicted=100 floor dropped a 128-out row"
# The row's chips, read off an oldest-first page where the harness row sits
# near the front.
curl -fsS "$base/?sort=oldest&limit=100" >"$work/figsold.html"
for want in 'P512 · G128' 'cache 41%' 'ctx 32k' 'fa on · kv q8_0' 'offload partial' '35B · 3B active' 'prefill 1235<span class="u"> tok/s'; do  # since 2026-09-19 the prefill goes through fmt like decode (no decimals at or above 100) and the unit is its own dimmed span
  grep -q "$want" "$work/figsold.html" || die "the figures row carries no $want"
done
# The run page rows. tape_key is none like the mixed rig — the page is built
# from the index row, never the record, so it still renders.
curl -fsS "$base/r/figsrow0000000000000" >"$work/figs.html"
for want in '>workload<' '>context<' '>config<' '>machine<' '32768 window' '281 of 300 W' 'min 12'; do
  grep -q "$want" "$work/figs.html" || die "the figures run page carries no $want"
done
# The shortest stream is only worth a word when it differs from the mean
# (lead, 2026-09-19): a one-stream run had every page reading "238 out · min
# 238". Same row, patched to agree, and the clause must disappear.
npx wrangler d1 execute toktape --local --command \
  "UPDATE runs SET min_predicted_n = 128, index_json = json_set(index_json, '\$.min_predicted_n', 128) WHERE id = 'figsrow0000000000000'" \
  >"$work/figs-min.log" 2>&1 || { cat "$work/figs-min.log"; die "could not patch the figures harness row"; }
curl -fsS "$base/r/figsrow0000000000000" >"$work/figsmin.html"
grep -q 'min ' "$work/figsmin.html" && die "the run page printed a minimum equal to the mean"
# The new boxes on the front page.
curl -fsS "$base/" >"$work/fetched" && grep -q '<select name="model"' "$work/fetched" || die "the front page has no model box"
grep -q 'name="size"' "$work/fetched" || die "the front page has no size box"
grep -q 'name="moe"' "$work/fetched" || die "the front page has no MoE box"
# Harness row, harness cleanup: the listing below must see only real publishes.
npx wrangler d1 execute toktape --local --command "DELETE FROM runs WHERE id = 'figsrow0000000000000'" \
  >"$work/figs-clean.log" 2>&1 || { cat "$work/figs-clean.log"; die "could not clean up the figures harness row"; }
# The sort parameter (2026-09-19: the user reversed the no-sort decision; a
# sort parameter is now honoured and checked).
curl -fsS "$base/api/v1/runs?sort=decode&limit=50" >"$work/decode.json"
grep -q '"sort":"decode"' "$work/decode.json" || { cat "$work/decode.json"; die "the decode listing names no sort"; }
python3 - "$work/decode.json" <<'EOF' || die "the decode listing is not fastest-first with unknowns last"
import json, sys
rows = json.load(open(sys.argv[1]))["runs"]
vals = [r.get("decode_per_sec") for r in rows]
present = [v for v in vals if isinstance(v, (int, float)) and not isinstance(v, bool)]
if any(b - a > 0 for a, b in zip(present, present[1:])):
    sys.exit("not non-increasing: %r" % (present,))
seen_null = False
for v in vals:
    if v is None:
        seen_null = True
    elif seen_null:
        sys.exit("a null figure sorts before a present one")
EOF
curl -fsS "$base/api/v1/runs?sort=oldest&limit=50" >"$work/oldest.json"
python3 - "$work/oldest.json" <<'EOF' || die "the oldest listing is not oldest-first"
import json, sys
rows = json.load(open(sys.argv[1]))["runs"]
ts = [r["published_at"] for r in rows]
if any(b < a for a, b in zip(ts, ts[1:])):
    sys.exit("not non-decreasing: %r" % (ts,))
EOF
curl -fsS "$base/api/v1/runs?sort=newest&limit=50" >"$work/newest.json"
curl -fsS "$base/api/v1/runs?limit=50" >"$work/plain.json"
cmp -s "$work/newest.json" "$work/plain.json" || die "?sort=newest is not byte-identical to no param"
code=$(curl -s -o "$work/body" -w '%{http_code}' "$base/api/v1/runs?sort=bogus")
[ "$code" = "400" ] || { cat "$work/body"; die "an unknown sort was not refused (got $code)"; }
grep -q 'unknown sort' "$work/body" || { cat "$work/body"; die "the sort refusal does not name the sort"; }
code=$(curl -s -o "$work/body" -w '%{http_code}' "$base/?sort=bogus")
[ "$code" = "400" ] || { cat "$work/body"; die "an unknown sort on the page was not refused (got $code)"; }
# Paging under decode: two limit=1 pages tile the limit=2 page, in order.
curl -fsS "$base/api/v1/runs?sort=decode&limit=1" >"$work/p1.json"
eval "$(python3 - "$work/p1.json" <<'EOF'
import json, sys
d = json.load(open(sys.argv[1]))
print("id1=%s" % d["runs"][0]["id"])
print("nxt=%s" % d["next"])
EOF
)"
[ -n "${nxt:-}" ] || die "the first decode page carries no cursor"
curl -fsS --get --data-urlencode "cursor=$nxt" "$base/api/v1/runs?sort=decode&limit=1" >"$work/p2.json"
id2="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["runs"][0]["id"])' "$work/p2.json")"
[ "$id1" != "$id2" ] || die "paging under decode repeated a row"
curl -fsS "$base/api/v1/runs?sort=decode&limit=2" >"$work/p12.json"
python3 - "$work/p12.json" "$id1" "$id2" <<'EOF' || die "two limit=1 decode pages do not tile the limit=2 page"
import json, sys
ids = [r["id"] for r in json.load(open(sys.argv[1]))["runs"][:2]]
sys.exit(0 if ids == [sys.argv[2], sys.argv[3]] else "got %r" % (ids,))
EOF
# The sort box on the front page, and the order named above the rows.
curl -fsS "$base/" >"$work/fetched" && grep -q '<select name="sort"' "$work/fetched" || die "the front page has no sort box"
curl -fsS "$base/?sort=decode" >"$work/fetched" && grep -q 'class="total".*fastest decode first' "$work/fetched" ||
  die "the decode page does not name its order in the total line"
grep -q 'side by side' "$work/fetched" || die "the decode total carries no caveat sentence"
# Paging under oldest tiles the same way (the ASC cursor branch).
curl -fsS "$base/api/v1/runs?sort=oldest&limit=1" >"$work/o1.json"
eval "$(python3 - "$work/o1.json" <<'EOF'
import json, sys
d = json.load(open(sys.argv[1]))
print("oid1=%s" % d["runs"][0]["id"])
print("onxt=%s" % d["next"])
EOF
)"
[ -n "${onxt:-}" ] || die "the first oldest page carries no cursor"
curl -fsS --get --data-urlencode "cursor=$onxt" "$base/api/v1/runs?sort=oldest&limit=1" >"$work/o2.json"
oid2="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["runs"][0]["id"])' "$work/o2.json")"
[ "$oid1" != "$oid2" ] || die "paging under oldest repeated a row"
curl -fsS "$base/api/v1/runs?sort=oldest&limit=2" >"$work/o12.json"
python3 - "$work/o12.json" "$oid1" "$oid2" <<'EOF' || die "two limit=1 oldest pages do not tile the limit=2 page"
import json, sys
ids = [r["id"] for r in json.load(open(sys.argv[1]))["runs"][:2]]
sys.exit(0 if ids == [sys.argv[2], sys.argv[3]] else "got %r" % (ids,))
EOF
# A filter on a normalised axis, and the raw fallback beside it.
curl -fsS "$base/api/v1/runs?engine=ik_llama.cpp" >"$work/fetched" && grep -q "\"id\":\"$id\"" "$work/fetched" ||
  die "filtering by engine dropped the run"
curl -fsS "$base/api/v1/runs?engine=vllm" >"$work/fetched" && grep -q "\"runs\":\[\]" "$work/fetched" ||
  die "filtering by an engine nothing ran returned rows"
curl -fsS "$base/api/v1/runs?q=Qwen3.6" >"$work/fetched" && grep -q "\"id\":\"$id\"" "$work/fetched" ||
  die "free text did not fall back to the name as recorded"
curl -fsS "$base/" >"$work/fetched" && grep -q 'ik_llama.cpp' "$work/fetched" || die "the front page does not list the run"
# Each listed run carries its stage, so a phone can play it in place.
curl -fsS "$base/" >"$work/fetched" && grep -q 'class="stage feed" href="/r/'"$id"'" data-tape="/r/'"$id"'.toktape"' "$work/fetched" ||
  die "the front page row does not carry the run for the feed"
curl -fsS "$base/" >"$work/fetched" && grep -q 'src="/player/host.js"' "$work/fetched" || die "the front page does not load the player"
# The run leads its row and the words follow as its caption (2026-09-19):
# inside the row the stage comes before the heading, and the rows sit in one
# grid so a desktop can show two or three across.
grep -q 'class="rows"' "$work/fetched" || die "the front page rows are not in a grid"
awk '/<article class="row">/{n++; seen=0} /class="stage feed"/{seen=1} /class="rowhead"/{ if(!seen){bad=1} } END{exit bad}' "$work/fetched" ||
  die "a front page row puts its heading before its stage"
# A filter in the URL shows in its box even when it matches nothing.
curl -fsS "$base/?engine=vllm" >"$work/fetched" && grep -q '<option value="vllm" selected>vllm (0)</option>' "$work/fetched" ||
  die "a filter that matched nothing vanished from its dropdown"
# The empty state offers a way back: one link per active filter, each naming
# the population that filter hides.
grep -q 'without engine' "$work/fetched" || die "the empty state offers no way back"
# The total above the rows: a population size, not a ranking.
curl -fsS "$base/" >"$work/front.html" && grep -q 'class="total"' "$work/front.html" ||
  die "the front page carries no total"
grep -qE 'class="total"><b>[0-9]+</b> runs?,' "$work/front.html" || die "the total names no population size"
# The first screen (2026-09-19). Both halves of this section are here because
# the user found them by looking at the page, which is the signal that the
# gate had no axis for what a first-time visitor meets: it checked what the
# listing says and never what stands above it.
#
# A reader who arrives from a post has to be able to see that this is a thing
# they can run, without scrolling past the runs to the footer.
grep -q 'brew install midagedev/tap/toktape' "$work/front.html" ||
  die "the front page names no way to install toktape above the listing"
grep -q 'href="https://github.com/midagedev/toktape#install"' "$work/front.html" ||
  die "the front page links at no install instructions"
# And the eight axis selects stay folded until something is narrowing by
# them: eleven controls between the brand and the runs is the state the
# disclosure exists to end.
grep -q '<details class="axes">' "$work/front.html" ||
  die "the filter axes are not folded away on the bare front page"
grep -q '<details class="axes" open>' "$work/front.html" &&
  die "the filter axes are open on a page nothing is narrowing"
# The link preview. A run page's image is its own card; this page had none at
# all, so the one link that announces the service previewed as plain text.
grep -q '<meta property="og:image" content="[^"]*/og.png">' "$work/front.html" ||
  die "the front page offers no preview image"
curl -fsS "$base/og.png" >"$work/og.png" || die "og.png is not served"
python3 - "$work/og.png" <<'PY' || die "og.png is not a 1200x675 PNG"
import struct, sys
b = open(sys.argv[1], "rb").read()
assert b[:8] == b"\x89PNG\r\n\x1a\n", "not a PNG"
w, h = struct.unpack(">II", b[16:24])
assert (w, h) == (1200, 675), f"{w}x{h}"
PY
# The description has to survive the readers that show it: Google truncates
# around 150 characters and most social previews around 125.
python3 - "$work/front.html" <<'PY' || die "the front page description is too long to survive a preview"
import re, sys
html = open(sys.argv[1], encoding="utf-8").read()
m = re.search(r'<meta name="description" content="([^"]*)">', html)
assert m, "no description"
n = len(m.group(1))
print(f"note: the front page description is {n} characters")
assert n <= 150, n
PY
# The active line on a narrowed page, with its way out.
curl -fsS "$base/?engine=ik_llama.cpp" >"$work/eng.html" && grep -q 'Narrowed to' "$work/eng.html" ||
  die "the filtered page names no active filters"
# ...and on that page the axes are open, because a listing narrowed by a link
# has to show what narrowed it.
grep -q '<details class="axes" open>' "$work/eng.html" ||
  die "the filter axes stayed folded on a page narrowed by one of them"
grep -q 'engine: ik_llama.cpp' "$work/eng.html" || die "the active line does not name the engine filter"
grep -q 'class="clear" href="/"' "$work/eng.html" || die "the active line has no Clear all link"
# On that page the hero row's engine chip is marked active rather than
# linked, while the streams chip accumulates the current filters.
grep -q '<span class="fact on">ik_llama.cpp' "$work/eng.html" ||
  die "the hero row's engine chip is not marked active"
href="$(grep -o 'href="[^"]*sessions=[^"]*"' "$work/eng.html" | head -n 1)"
case "$href" in *engine=ik_llama.cpp*) ;; *) die "the streams chip did not accumulate the engine filter: $href";; esac
# The engine build rides inside the engine chip; the OS rides beside it.
grep -q 'class="v">c10fbbcc<' "$work/eng.html" || die "the engine chip carries no build"
grep -q '>linux</a>' "$work/eng.html" || die "the row carries no OS chip"
# The caveat chip links at the reasons; the run page carries the anchor.
#
# 2026-09-19: this used to read `grep '"caveat_count":1'` and to assert the
# absent case by grepping the whole front page for `#caveats`. Both halves
# were wrong for the same reason — they described the fixture rather than
# the rule. A new caveat rule (the card round's ragged_aggregate) moved the
# hero's count from 1 to 2 and the gate started asserting *absence* for a
# run that has two of them, while the listing holds every earlier run this
# gate ever published, so any one of them carrying a caveat failed it. The
# count is read, not matched, and the absent case asks about this run's own
# id. Not a loosened assertion: both branches still fail on the defect they
# were written for (a chip that does not link, an anchor that is missing,
# and now a chip that appears for a run with nothing to qualify).
caveats="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["index"].get("caveat_count", 0))' "$work/row.json")"
if [ "$caveats" -gt 0 ]; then
  grep -q "/r/$id#caveats" "$work/front.html" || die "the caveat chip does not link at the reasons"
  grep -q 'id="caveats"' "$work/page.html" || die "the run page carries no caveats anchor"
else
  grep -q "/r/$id#caveats" "$work/front.html" && die "a run with no caveats links at reasons"
  grep -q 'id="caveats"' "$work/page.html" && die "a run with no caveats carries the anchor"
  printf 'note: the hero currently has no caveats, so the caveat-link checks asserted absence\n'
fi
printf 'note: the hero run carries %s caveat(s)\n' "$caveats"
# The VRAM floor: impossible empties the listing, 1 GB keeps the hero (whose
# card says 48 GiB, and whose row must say so in bytes).
grep -q '"vram_bytes": *[1-9]' "$work/row.json" || { cat "$work/row.json"; die "the hero row carries no vram_bytes"; }
curl -fsS "$base/?min_vram=999999" >"$work/fetched" && grep -q 'No published run matches' "$work/fetched" ||
  die "an impossible VRAM floor still lists runs"
curl -fsS "$base/?min_vram=1" >"$work/fetched" && grep -q "/r/$id" "$work/fetched" ||
  die "a 1 GB VRAM floor dropped the run"
grep -q 'vram: 1 GB+' "$work/fetched" || die "the VRAM floor is not named in the active line"
code=$(curl -s -o "$work/body" -w '%{http_code}' "$base/api/v1/runs?scope=mine")
[ "$code" = "401" ] || { cat "$work/body"; die "the journal scope answered without a token (got $code)"; }

say "unlisted means unlisted"
HOME="$work/home" "$work/toktape" publish "$repo/assets/hero.tape" --url "$base" --private \
  >"$work/receipt2" 2>"$work/publish2.err" ||
  { cat "$work/publish2.err"; die "a private publish failed"; }
pid2="$(tr -d '\r\n' <"$work/receipt2")"; pid2="${pid2##*/}"
curl -fsS "$base/api/v1/runs" >"$work/fetched" && grep -q "$pid2" "$work/fetched" && die "a --private run is in the listing"
curl -fsS "$base/r/$pid2" >"$work/fetched" && grep -q 'unlisted' "$work/fetched" || die "the private run's page does not say it is unlisted"
curl -fsS "$base/r/$pid2" >"$work/fetched" && grep -q '<meta name="robots" content="noindex">' "$work/fetched" ||
  die "the private run's page does not ask to stay out of the index"
curl -fsS "$base/sitemap.xml" >"$work/fetched" && grep -q "$pid2" "$work/fetched" && die "a --private run is in the sitemap"

say "refusals"
code=$(curl -s -o "$work/body" -w '%{http_code}' -X POST "$base/api/v1/runs" \
  -H 'Authorization: Bearer tk_not_a_token' -F 'tape=@'"$work/downloaded.tape"';filename=run.tape' \
  -F 'index={"schema":1}')
[ "$code" = "401" ] || { cat "$work/body"; die "an unknown token was not refused (got $code)"; }
code=$(curl -s -o "$work/body" -w '%{http_code}' -X POST "$base/api/v1/runs" \
  -H "CF-Connecting-IP: 203.0.113.8" \
  -F 'tape=@'"$work/downloaded.tape"';filename=run.tape' -F 'index={"schema":99}')
[ "$code" = "400" ] || { cat "$work/body"; die "an unknown index schema was not refused (got $code)"; }
grep -q 'schema 1' "$work/body" || { cat "$work/body"; die "the refusal does not name the schema"; }
# An index with the right schema and no model is not a run. Before this
# check the rate-limit probes below listed as "a run · ? tok/s".
code=$(curl -s -o "$work/body" -w '%{http_code}' -X POST "$base/api/v1/runs" \
  -H "CF-Connecting-IP: 203.0.113.8" \
  -F 'tape=@'"$work/downloaded.tape"';filename=run.tape' -F 'index={"schema":1}')
[ "$code" = "400" ] || { cat "$work/body"; die "an index with no model was not refused (got $code)"; }
grep -q 'model_raw' "$work/body" || { cat "$work/body"; die "the refusal does not name the missing field"; }
code=$(curl -s -o "$work/body" -w '%{http_code}' "$base/r/aaaaaaaaaaaaaaaaaaaa.json")
[ "$code" = "404" ] || { cat "$work/body"; die "an id that does not exist was not a 404 (got $code)"; }

say "a journal token owns its runs"
# A token minted straight into the local D1 (there is no issuance endpoint
# — OR REPLACE, because the local D1 outlives one run of this gate and the
# second run hit the primary key, 2026-09-19)
# yet — TTP-114), then a publish with it: the page and the row wear no
# badge, the API says owned, and the token — not a delete token — takes the
# run down. The anonymous hero page and row wear the `anonymous` chip (user,
# 2026-09-19: mark the exception, not the common case).
jt="tk_local_harness_journal_token"
if command -v shasum >/dev/null 2>&1; then jh=$(printf '%s' "$jt" | shasum -a 256 | cut -d' ' -f1); else jh=$(printf '%s' "$jt" | sha256sum | cut -d' ' -f1); fi
npx wrangler d1 execute toktape --local --command \
  "INSERT OR REPLACE INTO tokens (id, hash, created_at, label) VALUES ('harness', '$jh', '2026-09-19T00:00:00Z', 'web/check.sh')" \
  >"$work/token.log" 2>&1 || { cat "$work/token.log"; die "could not mint the harness token"; }
mkdir -p "$work/home-journal/.toktape"
printf 'token = "%s"\nfirst_publish_warning_seen = true\n' "$jt" >"$work/home-journal/.toktape/config.toml"
HOME="$work/home-journal" "$work/toktape" publish "$repo/assets/hero.tape" --url "$base" --title "owned by a journal" \
  >"$work/receipt3" 2>"$work/publish3.err" || { cat "$work/publish3.err"; die "a token-owned publish failed"; }
grep -q 'Delete token' "$work/publish3.err" && die "a token-owned publish was handed a delete token"
jid="$(tr -d '\r\n' <"$work/receipt3")"; jid="${jid##*/}"
curl -fsS "$base/r/$jid" >"$work/fetched" && grep -q 'class="anon"' "$work/fetched" && die "the token-owned page wears the anonymous chip"
curl -fsS "$base/r/$jid.json" >"$work/fetched" && grep -q '"owned":true' "$work/fetched" || die "the API does not say the run is owned"
curl -fsS "$base/api/v1/runs" >"$work/fetched" && grep -q "\"id\":\"$jid\",\"url\":\"[^\"]*\",\"published_at\":\"[^\"]*\",\"owned\":true" "$work/fetched" ||
  die "the listing row does not say the run is owned"
curl -fsS "$base/r/$id" >"$work/fetched" && grep -q 'class="anon"' "$work/fetched" || die "the anonymous page wears no anonymous chip"
curl -fsS "$base/" >"$work/fetched" && grep -q 'class="anon"' "$work/fetched" || die "the anonymous row wears no anonymous chip"
say "the user home"
# The profile belongs to the token (TTP-127): setting it in the harness
# HOME and publishing again copies it onto the token's row, so /u/harness
# shows the newest name, link, avatar and bio — and only that token's runs.
# Both publishes below are token-owned, so they spend no anonymous rate
# budget (owned uploads are not rate-limited).
HOME="$work/home-journal" "$work/toktape" profile --name "Harness Owner" \
  --link https://example.com/h --avatar "$repo/web/static/favicon.png" \
  --bio "$(printf 'Line one.\n\nLine two.')" \
  >"$work/profile-journal.txt" 2>&1 ||
  { cat "$work/profile-journal.txt"; die "a profile set with a bio failed"; }
HOME="$work/home-journal" "$work/toktape" publish "$repo/assets/hero.tape" --url "$base" --yes \
  --title "second" \
  >"$work/receipt-journal2" 2>"$work/publish-journal2.err" ||
  { cat "$work/publish-journal2.err"; die "a second token-owned publish failed"; }
jid2="$(tr -d '\r\n' <"$work/receipt-journal2")"; jid2="${jid2##*/}"
[ -n "$jid2" ] || die "the second receipt carried no link"
curl -fsS "$base/u/harness" >"$work/home.html"
for want in '<h1>Harness Owner</h1>' '<p>Line two.</p>' 'href="https://example.com/h"' 'class="avatar"' 'owned by a journal' 'second'; do
  grep -q "$want" "$work/home.html" || die "the user home carries no $want"
done
grep -q "/r/$id\"" "$work/home.html" && die "the user home lists the anonymous hero"
# The home lists in the front page's grid, and its stages play too, so it
# loads the same player (2026-09-19: rows became a feed at every width).
grep -q 'class="rows"' "$work/home.html" || die "the user home rows are not in the grid"
grep -q 'src="/player/host.js"' "$work/home.html" || die "the user home does not load the player"
curl -fsS "$base/u/harness.json" >"$work/home.json"
# The local D1 outlives one run of this gate, so earlier runs' harness rows
# may still be listed here: every assertion below is contains, never a count.
for want in '"handle":"harness"' '"runs":\[' "\"id\":\"$jid\"" "\"id\":\"$jid2\""; do
  grep -q "$want" "$work/home.json" || { cat "$work/home.json"; die "the user home JSON carries no $want"; }
done
# The bio travels JSON-escaped: in the file it is backslash-n, which grep
# needs as \\n (a bare \n in a pattern means the letter n).
grep -q '"bio":"Line one.\\n\\nLine two."' "$work/home.json" ||
  { cat "$work/home.json"; die "the user home JSON carries no bio"; }
code=$(curl -s -o "$work/body" -w '%{http_code}' "$base/u/nobody")
[ "$code" = "404" ] || { cat "$work/body"; die "an unknown user was not a 404 (got $code)"; }
# A token minted before 0006 has every new column NULL: the home shows the
# handle as the name and no bio — absent, never null, the API's rule for
# unknown.
npx wrangler d1 execute toktape --local --command \
  "INSERT OR REPLACE INTO tokens (id, hash, created_at, label) VALUES ('stale', '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef', '2026-01-01T00:00:00Z', 'web/check.sh stale-schema probe')" \
  >"$work/stale.log" 2>&1 || { cat "$work/stale.log"; die "could not mint the stale-schema token"; }
curl -fsS "$base/u/stale" >"$work/stale.html" && grep -q '<h1>stale</h1>' "$work/stale.html" ||
  die "a profile-less home does not fall back to the handle"
grep -q 'class="avatar"' "$work/stale.html" && die "a profile-less home carries an avatar"
curl -fsS "$base/u/stale.json" >"$work/stale.json"
grep -q '"handle":"stale"' "$work/stale.json" || { cat "$work/stale.json"; die "a profile-less home JSON carries no handle"; }
grep -q '"name"' "$work/stale.json" && { cat "$work/stale.json"; die "a profile-less home JSON invents a name"; }
grep -q '"bio"' "$work/stale.json" && { cat "$work/stale.json"; die "a profile-less home JSON invents a bio"; }
code=$(curl -s -o "$work/body" -w '%{http_code}' "$base/u/../etc")
[ "$code" = "404" ] || { cat "$work/body"; die "a handle outside the route was not a 404 (got $code)"; }
# The byline on a token-owned run page links the home; the anonymous hero
# page links no home anywhere.
curl -fsS "$base/r/$jid2" >"$work/fetched" && grep -q 'href="/u/harness"' "$work/fetched" ||
  die "the token-owned page does not link the user home"
curl -fsS "$base/r/$id" >"$work/fetched" && grep -q '/u/' "$work/fetched" &&
  die "the anonymous page links a user home"
# A publish that carries no profile at all — what an old client sends —
# leaves the token's row untouched, so the home keeps the previous bio.
HOME="$work/home-journal" "$work/toktape" profile --clear >"$work/profile-clear.txt" 2>&1 ||
  { cat "$work/profile-clear.txt"; die "profile --clear failed"; }
HOME="$work/home-journal" "$work/toktape" publish "$repo/assets/hero.tape" --url "$base" --yes \
  --title "third" \
  >"$work/receipt-journal3" 2>"$work/publish-journal3.err" ||
  { cat "$work/publish-journal3.err"; die "a profile-less token-owned publish failed"; }
curl -fsS "$base/u/harness" >"$work/home.html"
for want in '<h1>Harness Owner</h1>' '<p>Line two.</p>' 'third'; do
  grep -q "$want" "$work/home.html" || die "a profile-less publish blanked the home: no $want"
done

say "the owner edit"
HOME="$work/home-journal" "$work/toktape" publish --edit "$jid" --url "$base" --title "edited" --note "new body" \
  >"$work/receipt-edit" 2>"$work/publish-edit.err" ||
  { cat "$work/publish-edit.err"; die "publish --edit failed"; }
[ "$(tr -d '\r\n' <"$work/receipt-edit")" = "$base/r/$jid" ] ||
  { cat "$work/receipt-edit"; die "publish --edit did not print the run's link"; }
curl -fsS "$base/r/$jid" >"$work/fetched"
grep -q '<h1>edited</h1>' "$work/fetched" || die "the edited title is not the h1"
grep -q '<p>new body</p>' "$work/fetched" || die "the edited note is not rendered"
HOME="$work/home-journal" "$work/toktape" publish --edit "$jid" --url "$base" --private \
  >"$work/receipt-edit" 2>"$work/publish-edit.err" ||
  { cat "$work/publish-edit.err"; die "publish --edit --private failed"; }
curl -fsS "$base/api/v1/runs" >"$work/fetched" && grep -q "$jid" "$work/fetched" &&
  die "an unlisted run is in the listing"
curl -fsS "$base/u/harness" >"$work/fetched" && grep -q "$jid" "$work/fetched" &&
  die "an unlisted run is on the user home"
HOME="$work/home-journal" "$work/toktape" publish --edit "$jid" --url "$base" --public \
  >"$work/receipt-edit" 2>"$work/publish-edit.err" ||
  { cat "$work/publish-edit.err"; die "publish --edit --public failed"; }
curl -fsS "$base/api/v1/runs" >"$work/fetched" && grep -q "$jid" "$work/fetched" ||
  die "a re-listed run is not back in the listing"
# Negatives by hand curl because the client refuses first: a delete token
# does not edit, an unknown key is named, no token is refused, a string for
# private is not a boolean, and a 121-rune title is past the limit.
dt=$(sed -n 's/^Delete token: //p' "$work/publish.err" | tr -d '\r\n')
[ -n "$dt" ] || die "no delete token was printed"
code=$(curl -s -o "$work/body" -w '%{http_code}' -X PATCH "$base/api/v1/runs/$jid" \
  -H "Authorization: Bearer $dt" -H 'Content-Type: application/json' -d '{"title":"x"}')
[ "$code" = "403" ] || { cat "$work/body"; die "a delete token edited a run (got $code)"; }
code=$(curl -s -o "$work/body" -w '%{http_code}' -X PATCH "$base/api/v1/runs/$jid" \
  -H "Authorization: Bearer $jt" -H 'Content-Type: application/json' -d '{"colour":"red"}')
[ "$code" = "400" ] || { cat "$work/body"; die "an unknown edit key was not refused (got $code)"; }
grep -q 'colour' "$work/body" || { cat "$work/body"; die "the refusal does not name the key"; }
code=$(curl -s -o "$work/body" -w '%{http_code}' -X PATCH "$base/api/v1/runs/$jid" \
  -H 'Content-Type: application/json' -d '{"title":"x"}')
[ "$code" = "401" ] || { cat "$work/body"; die "an edit with no token was not refused (got $code)"; }
# The card-replace route (PUT /api/v1/runs/<id>/card) is gated exactly like
# the edit path, so this probe is read-only on purpose: no token is a 401 —
# never a 404 or a 405, which would say the route or the method was the
# problem — and nothing is uploaded to the service. The behaviour gates
# (wrong token, a non-PNG, over-limit, a run gaining a card) are unit
# tests in test/card.test.mjs, because exercising them here would mean
# writing a card to a run.
code=$(curl -s -o "$work/body" -w '%{http_code}' -X PUT "$base/api/v1/runs/$jid/card" \
  -H 'Content-Type: image/png' --data-binary 'not a png')
[ "$code" = "401" ] || { cat "$work/body"; die "a card PUT with no token was not refused (got $code)"; }
code=$(curl -s -o "$work/body" -w '%{http_code}' -X PATCH "$base/api/v1/runs/$jid" \
  -H "Authorization: Bearer $jt" -H 'Content-Type: application/json' -d '{"private":"yes"}')
[ "$code" = "400" ] || { cat "$work/body"; die "a string for private was not refused (got $code)"; }
code=$(curl -s -o "$work/body" -w '%{http_code}' -X PATCH "$base/api/v1/runs/$jid" \
  -H "Authorization: Bearer $jt" -H 'Content-Type: application/json' -d "{\"title\":\"$title121\"}")
[ "$code" = "400" ] || { cat "$work/body"; die "a 121-rune edit title was not refused (got $code)"; }
curl -fsS "$base/sitemap.xml" >"$work/fetched" && grep -q "<loc>$base/u/harness</loc>" "$work/fetched" ||
  die "the sitemap does not list the user home"

say "PATCH index reindexes the run"
# The journal token replaces its run's index row wholesale (TTP-130): the
# object travels verbatim into index_json and every index column is
# rewritten from it in the same UPDATE. Combinable with the rest in one
# body; a schema that is not 1 is refused by name.
code=$(curl -s -o "$work/body" -w '%{http_code}' -X PATCH "$base/api/v1/runs/$jid" \
  -H "Authorization: Bearer $jt" -H 'Content-Type: application/json' -d '{"index":{"schema":1,"ctx_size":4096,"model_id":"harness-model"}}')
[ "$code" = "200" ] || { cat "$work/body"; die "PATCH index did not reindex the run (got $code)"; }
curl -fsS "$base/api/v1/runs?model=harness-model&limit=100" >"$work/fetched" && grep -q "\"id\":\"$jid\"" "$work/fetched" ||
  die "the reindexed run is not under its new model"
grep -q '"ctx_size":4096' "$work/fetched" || { cat "$work/fetched"; die "the reindexed row carries no ctx_size"; }
curl -fsS "$base/" >"$work/fetched" && grep -q 'harness-model' "$work/fetched" ||
  die "the model dropdown lists no harness-model"
code=$(curl -s -o "$work/body" -w '%{http_code}' -X PATCH "$base/api/v1/runs/$jid" \
  -H "Authorization: Bearer $jt" -H 'Content-Type: application/json' -d '{"index":{"schema":2}}')
[ "$code" = "400" ] || { cat "$work/body"; die "an index schema of 2 was not refused (got $code)"; }
grep -q 'schema' "$work/body" || { cat "$work/body"; die "the refusal does not name the schema"; }

code=$(curl -s -o "$work/body" -w '%{http_code}' -X DELETE "$base/api/v1/runs/$jid" -H "Authorization: Bearer $jt")
[ "$code" = "204" ] || { cat "$work/body"; die "the journal token did not take its own run down (got $code)"; }

say "the delete token is the only key"
dt=$(sed -n 's/^Delete token: //p' "$work/publish.err" | tr -d '\r\n')
[ -n "$dt" ] || die "no delete token was printed"
code=$(curl -s -o "$work/body" -w '%{http_code}' -X DELETE "$base/api/v1/runs/$id" \
  -H "Authorization: Bearer dt_not_the_right_one")
[ "$code" = "403" ] || { cat "$work/body"; die "a wrong delete token was accepted (got $code)"; }
code=$(curl -s -o "$work/body" -w '%{http_code}' -X DELETE "$base/api/v1/runs/$id" \
  -H "Authorization: Bearer $dt")
[ "$code" = "204" ] || { cat "$work/body"; die "the delete token did not open the run (got $code)"; }
# Gone means gone: the row and the record both.
code=$(curl -s -o /dev/null -w '%{http_code}' "$base/r/$id.json")
[ "$code" = "404" ] || die "a deleted run is still indexed (got $code)"
code=$(curl -s -o /dev/null -w '%{http_code}' "$base/r/$id.tape")
[ "$code" = "404" ] || die "a deleted run's record is still served (got $code)"

say "the anonymous limit"
# Six a minute; the seventh is refused.
#
# This is the check that caught Cloudflare's rate-limiting binding doing
# nothing in production while passing here, so it is also the reason the
# counter now lives in D1: the same code path runs in both places and the
# same probe below works against either.
#
# Under its own address: the counter outlives a `wrangler dev` process, so a
# loop that spends the budget on the same key would refuse the *next* run's
# publish and make this gate fail for a reason that is not a defect.
limited=no
for _ in $(seq 1 9); do
  code=$(curl -s -o "$work/body" -w '%{http_code}' -X POST "$base/api/v1/runs" \
    -H "CF-Connecting-IP: 203.0.113.9" \
    -F 'tape=@'"$work/downloaded.tape"';filename=run.tape' -F 'index={"schema":1,"model_raw":"rate-limit probe"}')
  if [ "$code" = "429" ]; then limited=yes; break; fi
done
[ "$limited" = yes ] || die "nine anonymous uploads in a row were all accepted"
grep -q 'a minute' "$work/body" || { cat "$work/body"; die "the 429 does not say what the limit is"; }

printf '\nweb/check.sh: OK\n'
