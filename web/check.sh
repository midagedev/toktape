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
die() { printf 'web/check.sh: %s\n' "$1" >&2; exit 1; }

say "building the client"
go build -o "$work/toktape" "$repo/cmd/toktape"

say "local D1"
npx wrangler d1 migrations apply toktape --local >"$work/migrate.log" 2>&1 ||
  { cat "$work/migrate.log"; die "migrations failed"; }

say "wrangler dev on $base"
npx wrangler dev --port "$port" --inspector-port 0 --var "PUBLIC_BASE_URL:$base" \
  >"$work/dev.log" 2>&1 &
dev_pid=$!
for _ in $(seq 1 60); do
  if curl -fsS "$base/healthz" >/dev/null 2>&1; then break; fi
  kill -0 "$dev_pid" 2>/dev/null || { cat "$work/dev.log"; die "wrangler dev exited"; }
  sleep 1
done
curl -fsS "$base/healthz" | grep -q '"live":true' ||
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
# not have survived the round trip.
if grep -qi 'ws\b\|/home/\|192\.168\.' "$work/card.txt"; then
  cat "$work/card.txt"
  die "the downloaded record still carries the place it ran"
fi

say "the page and the row"
curl -fsS "$base/r/$id" | grep -q '<title>' || die "/r/<id> is not a page"
curl -fsS "$base/r/$id.json" >"$work/row.json"
grep -q '"schema": *1' "$work/row.json" || { cat "$work/row.json"; die "the index row is not there"; }

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
curl -fsS "$base/api/v1/runs?engine=ik_llama.cpp" | grep -q "\"id\":\"$id\"" ||
  die "filtering by engine dropped the run"
curl -fsS "$base/api/v1/runs?engine=vllm" | grep -q "\"runs\":\[\]" ||
  die "filtering by an engine nothing ran returned rows"
curl -fsS "$base/api/v1/runs?q=Qwen3.6" | grep -q "\"id\":\"$id\"" ||
  die "free text did not fall back to the name as recorded"
curl -fsS "$base/" | grep -q 'ik_llama.cpp' || die "the front page does not list the run"
code=$(curl -s -o "$work/body" -w '%{http_code}' "$base/api/v1/runs?scope=mine")
[ "$code" = "401" ] || { cat "$work/body"; die "the journal scope answered without a token (got $code)"; }

say "unlisted means unlisted"
HOME="$work/home" "$work/toktape" publish "$repo/assets/hero.tape" --url "$base" --private \
  >"$work/receipt2" 2>"$work/publish2.err" ||
  { cat "$work/publish2.err"; die "a private publish failed"; }
pid2="$(tr -d '\r\n' <"$work/receipt2")"; pid2="${pid2##*/}"
curl -fsS "$base/api/v1/runs" | grep -q "$pid2" && die "a --private run is in the listing"
curl -fsS "$base/r/$pid2" | grep -q 'unlisted' || die "the private run's page does not say it is unlisted"

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
# Six a minute; the seventh is refused. If this ever stops being enforced
# locally the check fails here rather than in production.
#
# Under its own address: the counter outlives a `wrangler dev` process, so a
# loop that spends the budget on the same key would refuse the *next* run's
# publish and make this gate fail for a reason that is not a defect.
limited=no
for _ in $(seq 1 9); do
  code=$(curl -s -o "$work/body" -w '%{http_code}' -X POST "$base/api/v1/runs" \
    -H "CF-Connecting-IP: 203.0.113.9" \
    -F 'tape=@'"$work/downloaded.tape"';filename=run.tape' -F 'index={"schema":1}')
  if [ "$code" = "429" ]; then limited=yes; break; fi
done
[ "$limited" = yes ] || die "nine anonymous uploads in a row were all accepted"
grep -q 'a minute' "$work/body" || { cat "$work/body"; die "the 429 does not say what the limit is"; }

printf '\nweb/check.sh: OK\n'
