#!/usr/bin/env bash
# Build the browser player, measure it, and prove it runs.
#
# The size is the whole reason this is a script and not a `go build` line.
# A Replay click downloads this file, so a build that quietly doubles is a
# feature that quietly stops being worth pressing — and the number is only
# ever noticed if something fails on it. Measured on the way in:
#
#   2026-09-18, before chroma went behind a build tag   2.01 MB brotli
#   the same build with chroma out                      ~1.36 MB brotli
#
# The budget below is that, with headroom. Raising it is a decision about
# what a share link costs its reader, so it is made deliberately and with a
# dated comment, not by whoever's change first exceeded it.
set -euo pipefail

cd "$(dirname "$0")"
repo="$(cd ../.. && pwd)"
out="dist"
budget=1600000

version="$(cd "$repo" && git describe --tags --always --dirty 2>/dev/null || echo dev)"

mkdir -p "$out"
GOOS=js GOARCH=wasm go build \
  -ldflags "-s -w -X main.version=$version" \
  -o "$out/toktape.wasm" .

# Copied out of the toolchain at build time, never committed: wasm_exec.js is
# the loader for the exact Go version that produced the binary, and a stale
# copy fails in ways that read like the program's fault.
goroot="$(go env GOROOT)"
cp "$goroot/lib/wasm/wasm_exec.js" "$out/wasm_exec.js"

raw=$(wc -c <"$out/toktape.wasm" | tr -d ' ')
gz=$(gzip -9 -c "$out/toktape.wasm" | wc -c | tr -d ' ')
printf 'toktape.wasm  raw %s  gzip %s' "$raw" "$gz"

if command -v brotli >/dev/null 2>&1; then
  br=$(brotli -q 11 -c "$out/toktape.wasm" | wc -c | tr -d ' ')
  printf '  brotli %s  (budget %s)\n' "$br" "$budget"
  if [ "$br" -gt "$budget" ]; then
    printf '\nweb/player/build.sh: %s bytes of brotli is over the %s budget.\n' "$br" "$budget" >&2
    printf 'Brotli is what the reader actually downloads, so this is the number that matters.\n' >&2
    exit 1
  fi
else
  # Fail closed. Brotli is what Cloudflare sends, so a build that cannot
  # measure it has not measured the thing the budget is about, and a size
  # gate that silently stops measuring is worse than no size gate.
  printf '\nweb/player/build.sh: brotli is not installed, so the size budget cannot be checked.\n' >&2
  printf 'Install it (brew install brotli) — the gate will not pass on gzip alone.\n' >&2
  exit 1
fi

printf '\n== smoke\n'
node smoke.mjs
