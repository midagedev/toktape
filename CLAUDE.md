# toktape — repo rules

Design document: `docs/toktape-spec.ko.md` (Korean). Research: `docs/research/`.
Backlog: gadak workspace `toktape`, project `TTP` (`/opt/homebrew/bin/gadak --workspace toktape list`).

## North star (every decision is measured against this)

When the money shot is posted to r/LocalLLaMA, a reader must want to run it
*right now*, and must be able to get their own result with one command. So:
zero-config first run, a card that settles arguments, no flag a first-time
user has to learn. Multi-session (N concurrent streams) is a first-class
mode, not an afterthought — agent workloads are the trend.

**Visual quality is the product** (user, 2026-09-13). The TUI, the PNG card
and the clips must be at least as polished as charmbracelet/crush and never
tacky. Every region must feel alive — eased bars, a breathing cursor,
scrolling sparklines, a spinner during prefill — while staying replayable:
animation is a pure function of clip time `t`, never of wall-clock. The lead
runs an E2E look-and-adjust loop on real renders after every visual track,
several rounds, viewing the images directly (this overrides the general
"lead does not read PNGs" rule for this repo).

## Contract

- `internal/tape` is the schema. Every renderer reads a `*tape.Tape` and
  nothing else. Changing it is the lead's job; propose in a report, don't edit.
- Server figures are the record; client figures are the check
  (`tape.RateTolerance`). Never "fix" a disagreement by picking the nicer number.
- Unknown is `""`/`0` and prints as `?`. Never print a default you did not observe.
- UI strings and README are English. Design docs are Korean.
- Linux is the primary target (`GOOS=linux GOARCH=amd64` must build); macOS
  builds with stubs. No cgo.

## Gate

`./scripts/check.sh` — gofmt, build, vet, linux cross-build, tests. Must pass
before any merge. Never loosen a test assertion or threshold without a dated
comment explaining why and a FAIL-first check that the old code fails the new test.

## Sub-agent rules

- No git state changes: no commit/checkout/stash/restore/add/push. Read-only
  git only. The lead commits.
- Stay inside the file boundary in your spec. Files owned by another track:
  read only. If an unrelated gate fails because of their work, report it, do
  not fix it.
- Do not run `check:full`-style whole-repo gates in a loop; run the package
  tests you touched, then `./scripts/check.sh` once at the end.
- Waiting on a process: hold the pid and `kill -0 $pid`, or use a pgrep
  pattern that can't match your own shell (`pgrep -f '[l]lama-server'`).
  `until ! pgrep -f "<pattern>"` matches the waiting shell itself and never ends.
- Final report format: changed files + one line each; the actual output of the
  verification commands you ran; paths of artifacts produced.
