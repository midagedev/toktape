#!/usr/bin/env bash
# tools/cli-contract.sh — print the CLI's machine contract as it actually is.
#
# Every terminal outcome toktape can produce, driven against the real binary:
# the invocation, the exit code it returned, and the "code" its --json error
# object carried. This is what had to be done by hand to find TTP-71's five
# gaps (a failure that printed nothing on stdout under --json, exit codes
# documented nowhere), so it is a tool rather than a transcript.
#
# It reaches no server: the unreachable and streams rows point at a port
# nothing listens on and at a stub served from this script.
#
# Usage:
#   tools/cli-contract.sh                # builds a fresh binary into a tmpdir
#   TOKTAPE=/path/to/toktape tools/cli-contract.sh
#
# Read it as: anything under "exit" that is not in `toktape --help`'s exit-code
# table, or any row whose json-code is "-" while --json was asked for, is a
# hole in the contract.
set -uo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

if [[ -z ${TOKTAPE:-} ]]; then
	TOKTAPE="$work/toktape"
	echo "building $TOKTAPE" >&2
	(cd "$root" && go build -o "$TOKTAPE" ./cmd/toktape) || exit 1
fi

runs="$work/runs"
mkdir -p "$runs"

# A port with nothing behind it. Port 1 is never a llama-server.
dead="http://127.0.0.1:1"

printf '%-62s %5s %-13s %s\n' INVOCATION EXIT JSON-CODE STDOUT
printf '%-62s %5s %-13s %s\n' "$(printf '%.0s-' {1..62})" ----- ------------- ------

row() {
	local label=$1
	shift
	local out rc code shape wants_json=no
	for a in "$@"; do [[ $a == --json ]] && wants_json=yes; done
	out=$("$TOKTAPE" "$@" 2>"$work/err")
	rc=$?
	if [[ $wants_json == no ]]; then
		code="n/a"
		shape="$(printf '%s' "$out" | head -c 40 | tr '\n' ' ')"
		[[ -z $out ]] && shape="(empty)"
	elif [[ -n $out ]]; then
		code=$(printf '%s' "$out" | python3 -c \
			'import json,sys
try:
    o=json.load(sys.stdin)
except Exception:
    print("not-json"); raise SystemExit
print(o.get("error",{}).get("code","-") if isinstance(o,dict) else "not-object")' 2>/dev/null)
		shape="$(printf '%s' "$out" | head -c 40 | tr '\n' ' ')"
	else
		code="-"
		shape="(empty)"
	fi
	printf '%-62s %5d %-13s %s\n' "$label" "$rc" "${code:-?}" "$shape"
}

row "version"                         version
row "help agents"                     help agents
row "help nosuchtopic --json"         help nosuchtopic --json
row "<unknown verb> --json"           frobnicate --json
row "--nosuchflag --json"             --nosuchflag --json
row "--wait 0 --url <dead> --json"    --url "$dead" --wait 0 --out "$runs" --json
row "--endpoint bogus --json"         --endpoint bogus --json
row "--think-budget -1 --json"        --think-budget -1 --json
row "--ram-gbs 0 --json"              --ram-gbs 0 --json
row "card <missing> --json"           card "$work/nope.tape" --json
row "log --tsv --json"                log --out "$runs" --tsv --json
row "render <missing>"                render "$work/nope.tape"

cat <<'EOF'

Reading it
  exit 0 ok · 1 usage · 2 unreachable · 3 streams · 4 unavailable
  A non-zero row whose json-code is "-" asked for JSON and got none: that is
  the TTP-71 bug. A row whose json-code is "not-json" is worse.
  exit 3 (streams) and exit 4 (unavailable) need a server that answers and a
  box without ffmpeg; both are covered by cmd/toktape/agent_test.go's
  TestEveryExitCodeIsReachable, which fakes them.
EOF
