#!/usr/bin/env bash
# Render the site at real viewport widths, side by side.
#
# This exists because headless Chrome lied to us once (2026-09-19) and the
# lie was convincing. `--window-size=390,844 --screenshot` writes a 390-pixel
# PNG, but the page inside it is laid out at Chrome's minimum headless
# viewport — 500 CSS pixels on this machine — and the screenshot is simply
# cropped to 390. Every row then appears to run off the right edge, which
# reads exactly like a page that overflows on a phone. It does not: measured
# at a true 390, both this build and production fit. A stylesheet fix was
# written for that phantom and reverted.
#
# So the width here is a real one: each page is loaded in an iframe of the
# given CSS width, which is the one way to fix a viewport without driving
# the DevTools protocol. Chrome's window only has to be wide enough to hold
# the frames.
#
#   ./shot.sh out.png 390 430                 # one origin at two widths
#   ./shot.sh out.png 390 --also https://tape.midagedev.com
#   BASE=http://127.0.0.1:8787 ./shot.sh out.png 1400
#
# BASE defaults to the local dev server. Start it first (npm run dev), or
# pass --also with a second origin to put before/after side by side.
set -euo pipefail

chrome="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
[ -x "$chrome" ] || { printf 'shot.sh: no Chrome at %s (set CHROME)\n' "$chrome" >&2; exit 1; }

out="${1:-}"
[ -n "$out" ] || { printf 'usage: ./shot.sh <out.png> <width>... [--also <origin>]\n' >&2; exit 64; }
shift

base="${BASE:-http://127.0.0.1:8787}"
also=""
widths=()
while [ $# -gt 0 ]; do
  case "$1" in
    --also) also="$2"; shift 2;;
    *) widths+=("$1"); shift;;
  esac
done
[ "${#widths[@]}" -gt 0 ] || widths=(390 1400)

# The frames are built into a temp page next to the output, never in the
# repo: this is a viewing tool, and nothing it writes is an artifact.
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
page="$work/frames.html"

tall=0
{
  printf '<!doctype html><meta charset="utf-8">\n'
  printf '<body style="margin:0;background:#888;font:12px ui-monospace,monospace">\n'
  printf '<div style="display:flex;gap:10px;padding:8px;align-items:flex-start">\n'
  for w in "${widths[@]}"; do
    h=$(( w > 700 ? 900 : 820 ))
    [ "$h" -gt "$tall" ] && tall=$h
    for origin in $([ -n "$also" ] && printf '%s %s' "$also" "$base" || printf '%s' "$base"); do
      printf '<div><div style="color:#fff">%s %s</div>' "$origin" "$w"
      printf '<iframe src="%s/" style="width:%spx;height:%spx;border:2px solid #0f0"></iframe></div>\n' \
        "$origin" "$w" "$h"
    done
  done
  printf '</div></body>\n'
} >"$page"

# Wide enough for every frame plus its border and the flex gap, and tall
# enough for the tallest — the window only frames the frames.
total=40
for w in "${widths[@]}"; do
  n=$([ -n "$also" ] && echo 2 || echo 1)
  total=$(( total + (w + 24) * n ))
done

"$chrome" --headless --disable-gpu --hide-scrollbars \
  --window-size="$total,$(( tall + 40 ))" --virtual-time-budget=8000 \
  --screenshot="$out" "file://$page" 2>/dev/null

printf 'shot.sh: %s — %s at %s\n' "$out" "${also:+$also vs }$base" "${widths[*]}"
