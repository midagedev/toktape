# toktape

**The black-box tape for local LLM serving.**

English · [한국어](README.ko.md) · [日本語](README.ja.md)

[![check](https://github.com/midagedev/toktape/actions/workflows/ci.yml/badge.svg)](https://github.com/midagedev/toktape/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/midagedev/toktape)](https://github.com/midagedev/toktape/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/midagedev/toktape)](go.mod)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

toktape attaches to a llama-server you already have running, records one run
into a `.tape` file, and prints a card that says where the model sits, what the
process actually touched, and how fast the request really was — for one
stream or for eight at once.

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape recording 4 concurrent streams, from the command being typed to the result card"></p>

<p align="center"><em>The whole session: the command typed at a prompt, the server found and attached, four streams at once, then the card — replayed from a tape through the same renderer <code>toktape render</code> uses, with no terminal recorder involved.</em></p>

```text
┌──────────────────────────────────────────────────────────────────────┐
│ toktape v0.1.0                          20260913-150210-llama3.3-70b │
├──────────────────────────────────────────────────────────────────────┤
│ MODEL    Llama-3.3-70B-Instruct-Q4_K_M.gguf · Q4_K_M · 42.5 GiB      │
│ ENGINE   llama-server b3650 (a1b2c3d) · linux 6.8.0-45-generic       │
│          workstation                                                 │
│ RIG      2× RTX 3090 24G · AMD Ryzen 9 7950X · 64 GB DDR5-6000       │
├──────────────────────────────────────────────────────────────────────┤
│ Decode        9.1 tok/s · ≈ 410 GB/s, 22% of peak                    │
│ Prefill       610 tok/s · TTFT 810 ms · 512 prompt tokens            │
│ Context       16384 (512 in / 307 out)                               │
│ Prefix cache  25% hit (128/512) · warm                               │
│ Streams       8 × 9.1 tok/s = 72.9 tok/s aggregate                   │
│               TTFT p50 810 ms p95 1050 ms · slots busy max 8         │
├──────────────────────────────────────────────────────────────────────┤
│ MEMORY   GPU0 [██████████] 23.8/24.0 GiB                             │
│          GPU1 [██████████] 22.8/24.0 GiB                             │
│          weights 42.5 | kv 2.6 | compute 1.5 GiB                     │
│          Host RSS 1.2 GiB (file 0.8 / anon 0.4)                      │
│          Page faults 0.0 maj/token (0 during decode)                 │
├──────────────────────────────────────────────────────────────────────┤
│ HOST     GPU0 71°C 348 W · GPU1 67°C 318 W · throttled: no           │
│          contended: no                                               │
├──────────────────────────────────────────────────────────────────────┤
│ FLAGS    -ngl 99 -fa on -b 2048 -ub 512 -ctk q8_0 -ctv q8_0 -t 16    │
├──────────────────────────────────────────────────────────────────────┤
│                toktape · github.com/midagedev/toktape                │
└──────────────────────────────────────────────────────────────────────┘
```

## Why

Every "X tok/s on Y" thread ends the same way: was the prompt cached, was
flash attention on, how many streams, which quant exactly, was something else
running on the box. toktape answers all of that on one card, from the
server's own figures, and stores the run so anyone can re-render the same card
from the same file. It is one static binary, MIT, no telemetry, no account.

## Install

**Homebrew** (macOS and Linux):

```sh
brew install midagedev/tap/toktape
```

**Shell script.** Downloads the release archive for your OS and CPU, verifies
it against the release's `checksums.txt`, and installs a single binary to
`~/.local/bin`:

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

Set `TOKTAPE_INSTALL` to install elsewhere, `TOKTAPE_VERSION` to pin a
release, and `TOKTAPE_BASE_URL` to a mirror or a `file://` directory for a
box with no route to GitHub. Add `-s -- --dry-run` after `sh` to see what it
would do.

**Go:**

```sh
go install github.com/midagedev/toktape/cmd/toktape@latest
```

**From source** (Go 1.26, no cgo):

```sh
git clone https://github.com/midagedev/toktape.git
cd toktape
go build -o toktape ./cmd/toktape
```

Check the install with `toktape version`. The binary has nothing to resolve
at runtime, so `scp` onto the machine that runs the server is also a valid
install.

**Platforms.** Linux x86_64 and arm64 are the primary target: the memory,
page-fault and flag rows are read from `/proc`, which only exists on the
server's own host. macOS builds and runs, and attaches to a remote server
with `--url`; the `/proc` rows print `?` there. Windows works through WSL2.
GPU rows come from `nvidia-smi`.

## Quick start

Type one word on the machine that runs llama-server:

```sh
toktape
```

1. **Discover** — probe `127.0.0.1:8080`, then `:8081`, `:8000`, `:5000`, and
   take the first that answers `/props`.
2. **Attach** — read the model path, context size and slots from `/props`,
   find the server's PID by matching that path against `/proc/*/cmdline`, and
   open the `/proc` view on it.
3. **Prompt** — send one request from a fixed prompt set with
   `timings_per_token` and `return_progress` on, and stream the answer while
   sampling major faults, RSS and GPU state.
4. **Tape** — write the whole run to `~/.toktape/runs/<id>.tape`.
5. **Card** — print the 72-column card, save it beside the tape, and print the
   line that tells you how to share it.

No port to type, no PID to find, no flag to learn. If the model is still
loading, toktape waits for it (`--wait`, default ten minutes).

**Eight streams at once**, which is what an agent workload does to a server:

```sh
toktape -n 8
```

Per-stream tok/s falls as N rises and that is expected; the aggregate is the
number that answers "can this rig serve eight agents", and a single-stream
benchmark cannot show it.

**Watch it live**, tiles per stream plus a machine pane:

```sh
toktape -n 4 --tui
toktape -n 8 --tui --grid 2x2     # four tiles per page, ←/→ to page
```

**Share it:**

```sh
toktape card ~/.toktape/runs/<id>.tape --png           # 1200×675 image next to the tape
toktape card ~/.toktape/runs/<id>.tape --md --copy     # card + llama-bench table, on the clipboard
toktape render                                          # the newest run as a GIF
```

**Replay it** on the live screen, at any speed:

```sh
toktape play ~/.toktape/runs/<id>.tape --speed 2
```

## What the card shows

The same fields in the same places on every card, so two cards can be read
side by side. Each field is there because it settles an argument.

- **Decode and prefill, never mixed.** Prefill is compute-bound, decode is
  memory-bandwidth bound; one "45 tok/s" says nothing. The card prints TTFT,
  prompt tok/s and decode tok/s separately, with both token counts.
- **Prefix cache hit.** `0% hit (0/512)` or `78% hit (400/512)`. A system
  prompt that differs by one character misses the cache, and prefill then
  looks ten times slower or faster for no visible reason.
- **cold / warm.** The first prompt against an mmap'd model pulls thousands of
  4 KiB pages off NVMe and reports a fraction of the warm rate. The label comes
  from the major faults taken during decode, not from a guess.
- **Page faults per token.** The one number that explains "it freezes, then
  continues". The live view draws them on the same time axis as the tokens.
- **RSS is not "loaded".** Under mmap, resident set is what the process has
  touched. The card splits host RSS into file and anon, splits VRAM into
  weights, KV cache and compute buffers, and computes never-loaded bytes from
  the GGUF tensor headers rather than from file size minus RSS.
- **Flags, in full.** `-ngl -fa -b -ub -ctk -ctv --load-mode -ot`. Flash
  attention state and batch sizing are the two omissions that reliably turn a
  results post into a fifty-comment thread.
- **The exact quantization.** `UD-Q4_K_M`, never shortened to "Q4".
- **contended.** Another harness on the box moves decode by more than most of
  the changes people test. The card reads the load average and the other GPU
  processes and labels the run.
- **Streams.** Per-stream rate × N = aggregate, spelled out on the card, with
  TTFT p50 and p95 and the most slots busy at once.

## Commands

| verb | what it does | example |
| --- | --- | --- |
| `record` | attach and record a run; the default verb | `toktape -n 4 --n-predict 512` |
| `card` | re-render a card from a tape | `toktape card <tape> --png` |
| `play` | replay a run on the live screen | `toktape play <tape> --speed 4` |
| `render` | render a run as GIF, mp4, asciicast or PNG frames | `toktape render <tape> --mp4 clip.mp4` |
| `ls` | list recorded runs | `toktape ls` |
| `log` | the experiment ledger of every run | `toktape log --sort decode` |
| `compare` | diff two runs, metrics and flags | `toktape compare a.tape b.tape` |
| `version` | print the version | `toktape version` |

**Record:** `--url` (default: discover), `-n`/`--concurrency`, `--prompt`
(repeatable, cycled to fill `-n`), `--n-predict` (default 256), `--out`
(default `~/.toktape/runs`), `--tag`, `--note`, `--wait`, `--tui`,
`--grid COLSxROWS` (default `2x4`, `0` fits the terminal), `--no-card`,
`--json`, `--quiet`.

**Card:** `--md` (the card in a fence plus a llama-bench compatible table),
`--json` (the run summary), `--png [FILE]` (1200×675 share image), `--copy`
(also to the clipboard over OSC 52). `--md` and `--json` are alternatives.

**Render:** `--gif FILE`, `--mp4 FILE` (needs ffmpeg on `PATH`), `--cast FILE`
(asciicast v2), `--frames DIR` (PNG sequence), `--duration`, `--fps`,
`--size WxH`. Name several outputs at once and they come out of the same
frames. With no tape named, the newest run is used.

**Log:** `--sort`, `--model`, `--tag`, `-n`, `--tsv`, `--csv`, `--json`,
`--md`, `--rebuild`, `--out`.

## Experiment log

Every run appends a row to `runs.tsv` next to the tapes, so a sweep is one
table instead of a folder of cards. Label runs as you go with `--tag` and
`--note`; both are stored in the tape, so the ledger can always be rebuilt.

```sh
toktape --tag ngl=40 --note "fa on"     # record, labelled
toktape log --sort decode                # which setting won
toktape log --tag ngl --md               # paste into an issue
toktape log --rebuild                    # regenerate from the tapes
```

Unknown is `?` on the terminal and an empty cell in every export, so the
numeric columns import as numbers:

```sh
sqlite3 runs.db ".import --tsv ~/.toktape/runs/runs.tsv runs"
duckdb -c "select tag, decode_tok_s from read_csv('~/.toktape/runs/runs.tsv')"
```

## Clips

The image at the top of this page is not a screen recording. It is a tape,
replayed frame by frame, and any tape you have renders the same way:

```sh
toktape render ~/.toktape/runs/<id>.tape
toktape render ~/.toktape/runs/<id>.tape --mp4 clip.mp4 --cast clip.cast
```

A clip opens on the command being typed, attaches, plays the run at real
speed (runs past 30 s are compressed to fit), and holds on the card. Every
frame is a pure function of clip time, so the same tape always renders the
same clip.

## How it measures

- **The server's figures are the record, the client's are the check.**
  Requests carry `timings_per_token`, so every chunk brings the server's own
  `prompt_per_second`, `predicted_per_second` and token counts. toktape
  computes the same rates from its own clock and keeps both; the tape records
  whether they agreed within 2 percent. A disagreement is never resolved by
  taking the nicer number.
- **The client's rate is measured over the content window** — the gaps
  between the first token that carried text and the last — not over the wall
  time from request to socket close.
- **A rate is called "decode" only above 32 generated tokens.** Below that
  the card says `Sample`.
- **Reasoning tokens count.** A thinking model's `reasoning_content` deltas
  are recorded as decode tokens, shown dimmed on the live screen, and TTFT is
  the first token of either kind.
- **"Never loaded" is read from the GGUF tensor headers**, per tensor class.
- **Without a PID** (a remote server, a container you cannot see into) the
  rates, prefix-cache hit and GPU state are still recorded. Host RSS, page
  faults and flags print as `?`, and the card says the `/proc` view was
  unavailable. A run with no fault measurement is never labelled cold.
- **Without a GPU** the VRAM and thermal rows print as `?` with the reason on
  the card.
- Unknown prints as `?`. The card never shows a value it did not observe.

## FAQ

**Why not `llama-bench`?** Use both. `llama-bench` measures the engine's
compute in isolation and is the right tool for that. toktape measures the
server: HTTP slots, prefix-cache reuse, TTFT under queue pressure, and what
happens when four or eight streams arrive at once. Those are the numbers an
agent workload actually sees.

**How do I know the prompt wasn't cached?** The card says so:
`Prefix cache 0% hit (0/512)`, and `cold` or `warm` from the fault count. If
someone doubts a card, send them the tape; they render the same card.

**Does it send anything anywhere?** No. It talks to your llama-server and
writes files under `~/.toktape`. There is no telemetry and no account.

## Supported

| | |
| --- | --- |
| Servers | llama-server (upstream llama.cpp), ik_llama.cpp |
| Linux | primary target, x86_64 and arm64, full `/proc` view |
| macOS | builds and runs; attach with `--url`; no `/proc` view, so memory and fault rows are `?` |
| Windows | through WSL2 |
| GPU | NVIDIA through `nvidia-smi` |

Roadmap: a macOS collector without sudo, an Ollama offload card from
`/api/ps`, and `toktape ab URL1 URL2` — two servers, one prompt, side by
side.

## The `.tape` format

Gzipped JSON, one file per run, schema version 1; plain JSON is read too, so a
tape stays greppable after `gunzip`. It holds the run summary the card is
rendered from, per-token timestamps and text, the sample series, the server's
raw timings, its flags and build, and the placement estimate. A reader
refuses a tape written by a newer schema instead of guessing at it. Every
renderer reads the tape and nothing else, so a card is a pure function of the
file. Share your tape; anyone with toktape renders the same card from it.

## Contributing

Issues and pull requests are welcome. Bug reports are most useful with the
tape attached: a run is fully described by its `.tape`, so "here is the card
I got" and "here is the file" are the same thing.

`./scripts/check.sh` is the gate — gofmt, build, vet, a Linux cross-build,
the tests — and CI runs exactly that. See [CONTRIBUTING.md](CONTRIBUTING.md)
for the layout of the tree, how goldens are updated, and the rules the
schema and the card follow.

## License

MIT. See [LICENSE](LICENSE).
