# Contributing to toktape

Thanks for looking. This page is the practical half of contributing: how the
tree is laid out, what the gate is, and the few rules that keep the card
truthful. The design document is `docs/toktape-spec.ko.md` (Korean); the
research that led here is under `docs/research/`.

## Before you start

- **Bug reports:** attach the `.tape`. A run is fully described by its tape,
  so the maintainers can render the exact card you saw and step through the
  run. If the tape contains a prompt you would rather not share, say so and
  describe the card instead. `toktape version` and the llama-server build line
  from the card are the other two things every report needs.
- **Feature requests:** the north star is one sentence. Someone who sees a
  toktape card posted must be able to get their own with one command. Features that add a flag a first-time
  user has to learn need a strong reason.
- **Larger changes:** open an issue first so we can agree on the shape before
  you spend the time. Changes to the tape schema in particular are discussed
  in an issue, not proposed as a PR (see *The schema* below).

## Development setup

Go 1.26 and nothing else. No cgo anywhere in the tree, so the build is fully
static and cross-compiles without a toolchain.

```sh
git clone https://github.com/midagedev/toktape.git
cd toktape
go build ./cmd/toktape
./toktape --help
```

Linux is the primary target and every package must cross-compile to it:

```sh
GOOS=linux GOARCH=amd64 go build ./...
```

macOS builds with stubs for the `/proc` collector. You can develop the whole
TUI, card and render pipeline on a Mac against the example tape; only the
process-side collectors need a Linux box with a running llama-server.

## The gate

```sh
./scripts/check.sh
```

gofmt, `go build`, `go vet`, the Linux cross-build and `go test ./...`. CI
runs exactly this script on Ubuntu and macOS, so a green local run and a
green pipeline mean the same thing. Run it before opening a pull request.

While iterating, run the package you touched (`go test ./internal/tui
-count=1`) and leave the full script for the end.

## Layout

| path | what lives there |
| --- | --- |
| `cmd/toktape` | the CLI: verb dispatch, flags, progress lines |
| `internal/tape` | **the schema.** The `.tape` file format and every type a renderer reads |
| `internal/server` | llama-server client: `/props`, `/slots`, streaming chat completions, timing reduction |
| `internal/recorder` | drives a run: N streams, sampling, writes the tape |
| `internal/procmon` | `/proc` collector: RSS, page faults, cmdline (Linux; stubs elsewhere) |
| `internal/gpu` | `nvidia-smi` collector |
| `internal/placement` | where the model sits: weights / KV / compute per device, never-loaded bytes from GGUF headers |
| `internal/card` | the 72-column text card, `--md`, `--json`; `card/png` is the 1200×675 image |
| `internal/tui` | the live screen: tiles, right pane, sparklines, replay (`play`) |
| `internal/render` | headless clip pipeline: schedule, rasteriser, GIF, mp4, asciicast; `cmd/hero` regenerates `assets/hero.gif` |
| `internal/ledger` | `runs.tsv` and `toktape log` |
| `internal/compare` | `toktape compare` |
| `scripts/` | `check.sh` (the gate), `install.sh` (the curl installer) |
| `tools/` | development helpers, e.g. `ansi2png.py` for looking at a TUI frame as an image |
| `docs/` | Korean design docs and research |

## Rules the code follows

These are short because each one has a story behind it.

- **`internal/tape` is the schema.** Every renderer — card, PNG, TUI, clip,
  ledger — reads a `*tape.Tape` and nothing else, so a card is a pure
  function of the file. Adding a field is a schema change: open an issue,
  describe the field, its JSON tag and what is allowed to be unknown, and let
  it land as its own commit before the feature that uses it.
- **Server figures are the record; client figures are the check.** toktape
  measures the same rates from its own clock and stores both. A disagreement
  beyond `tape.RateTolerance` is recorded, never resolved by picking the nicer
  number.
- **Unknown is `""` / `0` and prints as `?`.** Never print a default you did
  not observe. A flag the server was started without prints as `default`
  only when the server's own props told us so.
- **Animation is a function of clip time.** Anything that moves on the live
  screen — cursor, bars, sparklines, spinner — takes `t` and never reads the
  wall clock, so a replay and a render draw identical frames.
- **Strings are English.** Code, comments, UI strings and the English README.
  `README.ko.md` and `README.ja.md` are maintained by the maintainer; when
  you change a command or flag in `README.md`, mention it in the PR so the
  other two can follow. Design documents are Korean.

## Goldens

Text output is pinned with golden files under each package's `testdata/`.
When you intentionally change what a card or a frame looks like, regenerate
and commit the goldens with the code:

```sh
go test ./internal/card -run . -update
go test ./internal/tui -run TestView -update
```

Read the diff of the golden before committing it; that diff *is* the review
of a visual change. Never loosen a threshold or an assertion to make a test
pass. If a limit genuinely has to move — a GIF size budget, a tolerance — the
change needs a dated comment saying why, and a FAIL-first check: confirm the
old code actually fails the new test.

## The hero clip

`assets/hero.gif` at the top of the README is committed and regenerated from
the example run by one command:

```sh
go run ./internal/render/cmd/hero
```

It prints the size, frame count and duration. `go run
./internal/render/cmd/rendershot` writes the same clip plus stills into
`scratch/` (gitignored), which is what a look-and-adjust round on the TUI
uses. `python3 tools/ansi2png.py <frame.txt> <out.png>` turns a dumped TUI
frame into an image you can open.

## Pull requests

- One change per PR. Keep visual changes (colours, layout) out of PRs that
  fix behaviour, so the golden diff says one thing.
- Commit subjects are `pkg: what changed`, e.g. `card: print default for
  flags the server reported unset`. Reference the issue in the body.
- Say in the description what you ran: the gate, and the package tests you
  added. If you changed anything visible, attach a still or a short clip.
- The maintainer merges with fast-forward onto `main`; expect a rebase
  request if `main` moved.

## Releasing

Maintainers only; see [`docs/RELEASING.md`](docs/RELEASING.md).

## License

By contributing you agree that your contribution is licensed under the MIT
license in [LICENSE](LICENSE).
