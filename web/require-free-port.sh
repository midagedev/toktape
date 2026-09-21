#!/usr/bin/env bash
# Refuse to run a local-server gate on a port somebody else is holding.
#
# The gate this guards starts its own `wrangler dev` and then waits for
# /healthz to answer. Those are two different questions, and on 2026-09-21
# they gave different answers for two hours: an orphaned workerd from an
# interrupted run still held 127.0.0.1:8787, the gate's own wrangler died
# with `::bind(): Address already in use`, and /healthz answered anyway — so
# the gate ran its whole suite against a stranger's server with a two-hour-old
# view of the database. What it reported was "filtering by size=0 missed the
# figures row": a row it had just inserted and which was really there. A gate
# that cannot tell its own server from somebody else's does not fail, it lies
# (TTP-175).
#
# So the port is checked BEFORE anything is built or started. If nothing is
# listening when we look and our own process is alive afterwards, the server
# answering is ours.
#
# Usage: require-free-port.sh <port> [what-for]
set -euo pipefail

port="${1:?usage: require-free-port.sh <port> [what-for]}"
what="${2:-this gate}"

# lsof is on every macOS and in every distro's default image; -t prints bare
# pids. A port nobody holds makes it exit non-zero with no output, which is
# the case we want, so its failure must not trip set -e.
pids="$(lsof -nP -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true)"
[ -z "$pids" ] && exit 0

{
  printf 'require-free-port: 127.0.0.1:%s is already held, so %s cannot start its own server.\n' "$port" "$what"
  printf 'Holding it:\n'
  # The pid alone is not actionable. Elapsed time is what tells a leftover
  # from the dev server somebody is deliberately using in another terminal.
  ps -o pid,etime,command -p $pids | sed 's/^/  /'
  printf 'If that is a leftover, take it down:\n'
  printf '  kill %s\n' "$(echo "$pids" | tr '\n' ' ' | sed 's/ $//')"
  printf 'If it is yours on purpose, run on another port:\n'
  printf '  PORT=%s %s\n' "$((port + 1))" "${GATE_HINT:-./check.sh}"
} >&2
exit 1
