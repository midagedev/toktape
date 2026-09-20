#!/usr/bin/env bash
# Merge gate. Run from repo root. Exit non-zero on any failure.
set -euo pipefail
cd "$(dirname "$0")/.."
gofmt_out=$(find . -name '*.go' -not -path './vendor/*' -not -path './.git/*' -print0 | xargs -0 gofmt -l)
if [ -n "$gofmt_out" ]; then echo "gofmt: unformatted files:"; echo "$gofmt_out"; exit 1; fi
go build ./...
go vet ./...
GOOS=linux GOARCH=amd64 go build ./...   # every package must cross-compile to the primary target
GOOS=windows GOARCH=amd64 go build ./... # shipped since v0.2.5, so it is gated: procmon's !linux stubs carry it
go test ./... -count=1
# The renderers again, a long way from here (2026-09-21): the hero was
# recorded at 04:40 +09:00, the result modal drew the viewer's day, and the
# frame goldens passed in Seoul and failed on the UTC CI box. A replay may not
# depend on where it is watched, so the packages that draw are run in a zone
# on the other side of midnight from both.
TZ=Pacific/Honolulu go test ./internal/tui/... ./internal/card/... ./internal/render/... -count=1
echo "check: OK"
