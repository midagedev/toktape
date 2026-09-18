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

say "the record comes back as a run file"
curl -fsS "$base/r/$id.tape" -o "$work/downloaded.tape"
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
grep -q 'data-tape="/r/'"$id"'.tape"' "$work/page.html" || die "the page does not name the record for Replay"
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
for f in favicon.png apple-touch-icon.png; do
  ctype=$(curl -fsS -o "$work/asset" -w '%{content_type}' "$base/$f")
  case "$ctype" in image/png*) ;; *) die "$f is served as $ctype" ;; esac
done
for f in mascot-peek.webp mascot-sleep.webp mascot-sit.webp; do
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
# Every row carries the qualification the card prints (§9.4).
grep -q '"caveat_count"' "$work/list.json" || die "a listing row dropped its caveat count"
# There is no sort parameter and asking for one changes nothing: newest
# first is the only order, by decision, and this is where that is enforced.
curl -fsS "$base/api/v1/runs?sort=decode_per_sec" >"$work/sorted.json"
cmp -s "$work/list.json" "$work/sorted.json" || die "a sort parameter changed the listing"
# A filter on a normalised axis, and the raw fallback beside it.
curl -fsS "$base/api/v1/runs?engine=ik_llama.cpp" >"$work/fetched" && grep -q "\"id\":\"$id\"" "$work/fetched" ||
  die "filtering by engine dropped the run"
curl -fsS "$base/api/v1/runs?engine=vllm" >"$work/fetched" && grep -q "\"runs\":\[\]" "$work/fetched" ||
  die "filtering by an engine nothing ran returned rows"
curl -fsS "$base/api/v1/runs?q=Qwen3.6" >"$work/fetched" && grep -q "\"id\":\"$id\"" "$work/fetched" ||
  die "free text did not fall back to the name as recorded"
curl -fsS "$base/" >"$work/fetched" && grep -q 'ik_llama.cpp' "$work/fetched" || die "the front page does not list the run"
# Each listed run carries its stage, so a phone can play it in place.
curl -fsS "$base/" >"$work/fetched" && grep -q 'class="stage feed" href="/r/'"$id"'" data-tape="/r/'"$id"'.tape"' "$work/fetched" ||
  die "the front page row does not carry the run for the feed"
curl -fsS "$base/" >"$work/fetched" && grep -q 'src="/player/host.js"' "$work/fetched" || die "the front page does not load the player"
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
# The active line on a narrowed page, with its way out.
curl -fsS "$base/?engine=ik_llama.cpp" >"$work/eng.html" && grep -q 'Narrowed to' "$work/eng.html" ||
  die "the filtered page names no active filters"
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
if grep -q '"caveat_count":1' "$work/row.json"; then
  grep -q "/r/$id#caveats" "$work/front.html" || die "the caveat chip does not link at the reasons"
  grep -q 'id="caveats"' "$work/page.html" || die "the run page carries no caveats anchor"
else
  grep -q '#caveats' "$work/front.html" && die "a run with no caveats links at reasons"
  grep -q 'id="caveats"' "$work/page.html" && die "a run with no caveats carries the anchor"
  printf 'note: the hero currently has no caveats, so the caveat-link checks asserted absence\n'
fi
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
