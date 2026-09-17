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
echo "check: OK"
