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

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape recording two concurrent streams of a 444 GiB model, from the command being typed to the result"></p>

<p align="center"><em>A real run, not a mock-up: DeepSeek V4.1 Flash Q3_K_M, 444 GiB, most of its experts in host RAM on a two-card workstation. The command typed at a prompt, the server found and attached, two streams writing code at once, then the result. It is <code>assets/hero.tape</code> replayed through the same renderer <code>toktape render</code> uses — no terminal recorder involved, and <code>toktape card assets/hero.tape</code> prints the card below from the same file.</em></p>

```text
┌──────────────────────────────────────────────────────────────────────┐
│ toktape v0.2.0              20260914-115114-deepseek-v4-1-flash-q3-k │
├──────────────────────────────────────────────────────────────────────┤
│ MODEL    DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16-attnQ8      │
│          9 shards · Q3_K_M · 444.2 GiB                               │
│ ENGINE   llama-server b96 (e42d711e5) · linux 6.8.0-139-generic · ws │
│ RIG      RTX A6000 48G · RTX 3090 24G                                │
│          AMD Ryzen Threadripper PRO 5975WX 32-Cores · 252 GB         │
├──────────────────────────────────────────────────────────────────────┤
│ Decode        22.5 tok/s aggregate · 11.2 tok/s each                 │
│               ≈ 115 GB/s from RAM per verify step, 99% of peak       │
│ Prefill       51.8 tok/s aggregate · 25.9 tok/s each                 │
│               279 prompt tokens · engine prefill 10732 ms            │
│               queue 22 ms · TTFT p50 10754 ms                        │
│ Draft         DeepSeek-V4.1-Flash-Fp8-128x742M-MXFP4_MOE.tl37.gguf   │
│               n_max 3 · 52% accepted (260/504)                       │
│               170 verify steps of 4.0 tokens                         │
│ Context       16384 (279 in / 215 out · 64 thinking)                 │
│ Prefix cache  0% hit (0/279) · cold                                  │
│ Sampling      temp default · chat                                    │
│ Streams       2 × 11.2 tok/s = 22.5 tok/s aggregate                  │
│               TTFT p50 10754 ms p95 10754 ms · slots busy max 2      │
├──────────────────────────────────────────────────────────────────────┤
│ MEMORY   GPU0 [█████████░] 45.0/48.0 GiB                             │
│          GPU1 [█████████░] 21.4/24.0 GiB                             │
│          weights 50.2 | kv ? | compute ? GiB                         │
│          Host placed 394.1 GiB (200.8 in RAM / 193.3 on disk)        │
│          Host RSS 202.2 GiB (file 200.8 / anon 1.0)                  │
│          Page faults 4.4 maj/token (1869 during decode)              │
├──────────────────────────────────────────────────────────────────────┤
│ HOST     GPU0 64°C 116 W · GPU1 54°C 144 W · throttled: no           │
│          contended: no                                               │
│          conditions changed: k10temp Tctl 66 → 79 °C                 │
├──────────────────────────────────────────────────────────────────────┤
│ FLAGS    -ngl 99 -fa default -b 2048 -ub 512 -ctk default            │
│          -ctv default -t 32                                          │
│          -md DeepSeek-V4.1-Flash-Fp8-128x742M-MXFP4_MOE.tl37.gguf    │
│          --draft-max 3                                               │
│          -m /models/DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16… │
│          --alias DeepSeek-V4.1-Flash -c 16384 --lazy-mode auto       │
│          --spec-type draft-dspark -otd output_norm=CUDA0 --jinja     │
│          --reasoning-budget 64 --host 127.0.0.1 --port 8001          │
│          -ot blk\.[0-3]\.ffn_.*_exps=CUDA0 -ot blk\.6\.ffn_down_exp… │
├──────────────────────────────────────────────────────────────────────┤
│ ! 3 caveats — cold run: weights arrived from disk while it decoded,  │
│   4.4 maj faults/token · conditions_changed · run_cut_by_clock       │
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
toktape --sessions 8
```

Per-stream tok/s falls as N rises and that is expected; the aggregate is the
number that answers "can this rig serve eight agents", and a single-stream
benchmark cannot show it.

**Watch it live**, tiles per stream plus a machine pane. A fenced code block
in an answer is shaped as it arrives — keywords carry weight, comments and
punctuation step back — so a code answer reads as code without a second
colour on the screen:

```sh
toktape --sessions 4 --tui
toktape --sessions 8 --tui --grid 2x2     # four tiles per page, ←/→ to page
```

**Share it:**

```sh
toktape card ~/.toktape/runs/<id>.tape -o png          # 1200×675 image next to the tape
toktape card ~/.toktape/runs/<id>.tape -o md --copy    # card + llama-bench table, on the clipboard
toktape render                                          # the newest run as a GIF
```

**Replay it** on the live screen, at any speed:

```sh
toktape play ~/.toktape/runs/<id>.tape --speed 2
```

## What the card shows

The same fields in the same places on every card, so two cards can be read
side by side. Each field is there because it settles an argument.

- **Whether the number is quotable.** Every card ends with the reasons it
  might not be: `! 3 caveats — cold run: weights arrived from disk while it
  decoded, 4.4 maj faults/token · conditions_changed · run_cut_by_clock`. The
  most serious one is spelled out and the rest are named by their code, because
  a code is the thing you look up. `-o json` carries the same list as
  `caveats`, each with a severity: `figure` means one number is affected, `run`
  means the whole run is, `view` means the card could not see something it
  wanted. Read that field before quoting a rate anywhere.
- **Decode and prefill, never mixed.** Prefill is compute-bound, decode is
  memory-bandwidth bound; one "45 tok/s" says nothing. The card prints TTFT,
  prompt tok/s and decode tok/s separately, with both token counts. A prompt
  too short to be a prefill measurement is said to be one rather than averaged
  in — `30 prompt tokens — not a prefill measurement`. And on a concurrent run
  the wait for a free slot is split out of the engine's own work,
  `engine prefill 10732 ms · queue 22 ms`, so a queue is never read as a slow
  model.
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
- **Placed on the CPU is not the same as in RAM.** `-ot ... exps=CPU` is a
  backend assignment, not a residency: the card above places 394 GiB on the
  host and only 201 of it is resident, so 193 GiB is read back off the disk as
  the model decodes. `Host placed 394.1 GiB (200.8 in RAM / 193.3 on disk)`
  says which, and on the live screen the part that is not there is drawn in
  the warning colour beside the fault count that explains it.
- **Bandwidth against the bus the bytes crossed.** A model split between VRAM
  and host RAM has no single bandwidth: adding the three buses' traffic
  together produced `≈ 155 GB/s` on a machine whose host bus tops out at 116.
  The card names the side that is the wall —
  `≈ 115 GB/s from RAM per verify step, 99% of peak` — and prints a percentage
  only when the placement proves what the host reads. With a draft model the
  step is not a token: the target verifies a batch at a time, so the bytes are
  counted per verify step, which is what the bus actually saw.
- **What the request asked for.** Greedy against the server's default sampling
  against a thinking model left to think is an eleven per cent spread on one
  engine. `Sampling  temp default · chat` says which of them the rates belong
  to, and never invents a temperature nobody sent.
- **The draft, when there was one.** The model, the block size and how often
  the target agreed, with the counts: `n_max 3 · 52% accepted (260/504)`, and
  the shape of the work that acceptance rate produced,
  `170 verify steps of 4.0 tokens`.
- **Conditions changed.** The clock cap and a CPU temperature are read at the
  start and the end of every round. When a thermal watchdog moves the cap
  mid-run, the card says so instead of leaving a slow number unexplained.
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
| `record` | attach and record a run; the default verb | `toktape --sessions 4 --for 30s` |
| `card` | re-render a card from a tape | `toktape card <tape> -o png` |
| `play` | replay a run on the live screen | `toktape play <tape> --speed 4` |
| `render` | render a run as GIF, mp4, asciicast or PNG frames | `toktape render <tape> --mp4 clip.mp4` |
| `ls` | list recorded runs | `toktape ls` |
| `log` | the experiment ledger of every run | `toktape log --sort decode` |
| `compare` | diff two runs, metrics and flags | `toktape compare a.tape b.tape` |
| `version` | print the version | `toktape version` |

**How long a run is.** A recording ends on the clock: twenty seconds by
default, and `--for 30s` to aim it somewhere else. A token count is the wrong
unit for this — the same 256 tokens is under two seconds on a 7B on a fast GPU
and over two minutes on a large model with no GPU — so seconds are what you
name and `--n-predict` is the cap that also applies. Naming `--n-predict`
yourself is an answer on the same question, so it turns the clock off. Two
honest caveats: nothing is cut under 64 tokens, so a slow machine runs longer
than you asked rather than handing you a sample; and the model usually stops
before the budget does, which is your prompt's doing and not the machine's.

**Record:** `--for DURATION` (default `20s`; `--for 0` turns the clock off),
`--url` (default: discover), `--sessions N` (streams sent at once, default 1;
more than 8 needs `--max-sessions` naming the same number, and more than the
server has slots is refused), `--prompt`
(repeatable, cycled to fill `--sessions`), `--prompts`
(a JSONL file; each line is one round of `--sessions` streams, run in order into one
tape), `--spec-n-max LIST` (e.g. `3,5`: the prompt set once per
speculative `n_max`, all in one tape, with a card line per value),
`-n`/`--n-predict` (the token cap per stream, as llama-bench's `-n`; naming it
turns the clock off), `--out`
(default `~/.toktape/runs`), `--tag`, `--note`, `--wait`, `--tui`,
`--grid COLSxROWS` (default `2x4`, `0` fits the terminal), `--no-card`,
`-o FORMAT` (below), `--quiet`.

**Sampling and the endpoint.** How the request is shaped moves the number as
much as the flags the server was started with. On one reasoning model, twenty
prompts and one machine, greedy decoding on the raw endpoint ran 25.6 tok/s,
the server's default sampling on the same endpoint 24.5, and the chat endpoint
with thinking left on 22.4. So `record` names all three: `--temp N` sets the
sampling temperature (`--temp 0` is greedy; leave it off and the server's own
default stays in effect), `--no-think` asks a reasoning model not to think by
sending the engine's `enable_thinking` switch, `--endpoint chat|completion`
chooses between the templated chat route and posting the prompt verbatim to
`/completion` with no template around it, and `--param key=value` (repeatable)
merges anything else the build honours — `--param seed=7`, `--param
top_k=40`, `--param cache_prompt=false` — with the value read as JSON when it
is JSON and as a string otherwise. `--no-think` is a chat setting: thinking
belongs to the template, and a raw prompt has none. Whatever is sent is
recorded in the tape and named on the card, so two cards are comparable or
they say why they are not.

**Output formats.** `-o FORMAT`, or `--output FORMAT`, is llama-bench's `-o`
with llama-bench's words. `record` and `card` print `json` (the run summary),
`jsonl` (the same object on one line, so runs append to one file), `md`, and
`csv`, `tsv` or `sql` (the run as one ledger row); `log` prints every run in
the same six. `md` is not llama-bench's: on a card it is the card in a fence, a
llama-bench compatible table and a Reproduce block, and on `log` it is the
ledger as a Markdown table. `sql` creates the `runs` table when it is missing
and inserts one row per run, so `toktape log -o sql | sqlite3 runs.db` is a
database. A verb refuses a format it does not take and names the ones it does.

**Card:** `-o png [FILE]` (the 1200×675 share image, next to the tape unless a
file is named) besides the formats above, and `--copy` (also to the clipboard
over OSC 52).

**Render:** `--gif FILE`, `--mp4 FILE` (needs ffmpeg on `PATH`), `--cast FILE`
(asciicast v2), `--frames DIR` (PNG sequence), `--duration`, `--fps`,
`--size WxH`, `--open` (the command typed at a shell prompt in front of the
run). Name several outputs at once and they come out of the same frames. With
no tape named, the newest run is used.

**Log:** `--sort`, `--model`, `--tag`, `--limit N`, `-o FORMAT`, `--rebuild`,
`--out`.

## Running it from an agent

Most people who run toktape will not type the command: Claude Code or Codex
will, on their behalf. There is a page written for that reader —
[`docs/agents.md`](docs/agents.md) — and `toktape help agents` is the same
contract inside the binary, where an agent can find it without being told.
The short version:

- **Ask for a length in seconds, not in tokens.** `--for 20s` is the default.
  A token count means a different amount of time on every machine, which is
  the thing you are recording to find out.
- **Read `caveats` before quoting a figure.** The card qualifies every number
  it prints — a generation too short to be a rate, a prompt too short to be a
  prefill measurement, a busy machine — and `-o json` carries the same
  qualifications as one array. An agent that reads it cannot quote a number
  the card would have footnoted.
- **Branch on the exit code, never on the message.** Every verb ends on one
  of five documented codes, and `-o json` prints one object on stdout whether
  the run succeeded or failed, so there is one parse path and not two.
- **Two flags decide whether it fits your timeout.** `--wait` defaults to ten
  minutes, because a server loading a 450 GB model is worth waiting for;
  `--for` decides how long the generation itself runs.

## Experiment log

Every run appends a row to `runs.tsv` next to the tapes, so a sweep is one
table instead of a folder of cards. Label runs as you go with `--tag` and
`--note`; both are stored in the tape, so the ledger can always be rebuilt.

```sh
toktape --tag ngl=40 --note "fa on"     # record, labelled
toktape log --sort decode                # which setting won
toktape log --tag ngl -o md              # paste into an issue
toktape log --rebuild                    # regenerate from the tapes
```

Unknown is `?` on the terminal, an empty cell in every export and `NULL` in
`-o sql`, so the numeric columns import as numbers:

```sh
toktape log -o sql | sqlite3 runs.db
duckdb -c "select tag, decode_tok_s from read_csv('~/.toktape/runs/runs.tsv')"
```

## Clips

The image at the top of this page is not a screen recording. It is a tape,
replayed frame by frame, and any tape you have renders the same way:

```sh
toktape render ~/.toktape/runs/<id>.tape
toktape render ~/.toktape/runs/<id>.tape --mp4 clip.mp4 --cast clip.cast
```

A clip opens on the screen at the run's start, plays the whole run at real
speed, and holds on the result: the two rates drawn large enough to read at
feed size, the rig, the engine and where the model sits, over the screen you
were watching. `--open` puts the command being typed in front of it, the way
the clip at the top of this page starts.

Aim a clip's length when you **record** it, with `--for`: a clip is the run at
1:1 plus six seconds of framing, twelve with `--open`, so `--for 18s` makes a
thirty-second one. `render --duration` is a different flag and not a
substitute — it squeezes a run you already have into the time you name.
Nothing is compressed unless you ask for it that way: a clip that sped a run
up to fit a limit would be lying about the one number the page is about. The
same tape always renders the same clip.

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
- **Residency is derived, never recorded.** What is in RAM of a host
  placement is the process's file-backed resident set at that instant, which
  is the model mapping's pages and nothing else — so the pane, the card and
  the clip all print one split from one sample, and it moves during the run as
  the sample does. Without a `/proc` view the split is not printed at all
  rather than defaulted to zero.
- **A machine witness at every round edge.** Load average, IO pressure, the
  page cache, the live `llama-*` processes, the cpufreq cap and one hwmon
  temperature. A run whose machine changed under it was never one
  measurement, and the card says so.
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
renderer reads the tape and nothing else. Share your tape; anyone with
toktape renders the same card from it.

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
