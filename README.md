# toktape

**The black-box tape for local LLM serving.**

English · [한국어](README.ko.md) · [日本語](README.ja.md)

[![check](https://github.com/midagedev/toktape/actions/workflows/ci.yml/badge.svg)](https://github.com/midagedev/toktape/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/midagedev/toktape)](https://github.com/midagedev/toktape/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/midagedev/toktape)](go.mod)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

<img src="docs/mascot.png" align="right" width="150" alt="the toktape mascot: a small chibi in headphones, eyes closed, hugging a cassette tape">

toktape attaches to a llama-server you already have running, records one run
into a `.toktape` file, and prints a card that says where the model sits, what the
process actually touched, and how fast the request really was — for one
stream or for eight at once.

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape recording four concurrent streams of a 35B sparse MoE, from the command being typed to the result"></p>

<p align="center"><em>A real run, not a mock-up: the default command with <code>--sessions 4</code>, four streams answering at 37.3 tok/s each until the 20-second clock ends them, everything playing at 1:1. The prompts are 800 tokens because toktape measured what four prefills at once cost on this box before it chose the length. It is <code>assets/hero.tape</code> replayed through the same renderer <code>toktape render</code> uses — no terminal recorder involved, and <code>toktape card assets/hero.tape</code> prints the card below from the same file.</em></p>

```text
┌──────────────────────────────────────────────────────────────────────┐
│ toktape v0.3.0-46-g5dd7301   20260921-044059-qwen3-6-35b-a3b-ud-q6-k │
├──────────────────────────────────────────────────────────────────────┤
│ MODEL    Qwen3.6-35B-A3B-UD-Q6_K.gguf · UD-Q6_K · 27.3 GiB           │
│ ENGINE   ik_llama.cpp c10fbbcc · linux 6.8.0-139-generic             │
│          workstation                                                 │
│ RIG      RTX 3090 24G · RTX A6000 48G                                │
│          AMD Ryzen Threadripper PRO 5975WX 32-Cores · 252 GB         │
├──────────────────────────────────────────────────────────────────────┤
│ Decode        149 tok/s aggregate · 37.3 tok/s each                  │
│               ≈ 110–203 GB/s, 14–26% of peak                         │
│ Prefill       380 tok/s aggregate · 141 tok/s each                   │
│               802 prompt tokens · engine prefill 6646 ms             │
│               queue 1702 ms                                          │
│               probe 3006 tok/s on one stream · 86 ms fixed           │
│ Context       8192 (802 in / 433 out)                                │
│ Prefix cache  0% hit (0/802) · warm                                  │
│ Sampling      temp default · thinking off · chat                     │
│ Streams       4 streams · TTFT p50 8247 ms p95 8451 ms               │
│               slots busy max ?                                       │
├──────────────────────────────────────────────────────────────────────┤
│ MEMORY   GPU0 [░░░░░░░░░░] 0.0/24.0 GiB                              │
│          GPU1 [██████░░░░] 28.5/48.0 GiB                             │
│          weights 26.8 | kv ? | compute ? GiB                         │
│          Host placed 0.5 GiB (all in RAM)                            │
│          Host RSS 5.6 GiB (file 0.7 / anon 4.8)                      │
│          Page faults 0.0 maj/token (0 during decode)                 │
├──────────────────────────────────────────────────────────────────────┤
│ HOST     GPU0 32°C 31 of 420 W · GPU1 74°C 298 of 300 W              │
│          throttled: no · contended: no                               │
├──────────────────────────────────────────────────────────────────────┤
│ FLAGS    -ngl 99 -fa on -b 2048 -ub 512 -ctk default -ctv default    │
│          -t 32                                                       │
│          -m /models/Qwen3.6-35B-A3B/Qwen3.6-35B-A3B-UD-Q6_K.gguf     │
│          -c 32768 --jinja -np 4 --host 127.0.0.1 --port 8012         │
├──────────────────────────────────────────────────────────────────────┤
│ ! 4 caveats — engine commit c10fbbcc read from the checkout next to  │
│   the binary, not from the binary · recorded ×2 · run_cut_by_clock   │
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

**Shell script** — downloads the release for your OS and CPU, verifies it
against `checksums.txt`, installs one binary to `~/.local/bin`
(`TOKTAPE_INSTALL`, `TOKTAPE_VERSION`, `TOKTAPE_BASE_URL` to aim it; `--dry-run` to watch):

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

**Windows:** the release page carries zips; unpack `toktape.exe` anywhere on
`PATH`. **Go:** `go install github.com/midagedev/toktape/cmd/toktape@latest`.
**From source** (Go 1.26, no cgo):

```sh
git clone https://github.com/midagedev/toktape.git
cd toktape
go build -o toktape ./cmd/toktape
```

Check with `toktape version`; the binary resolves nothing at runtime, so
`scp` to the server's host is also an install. Linux is the primary target —
the memory, fault and flag rows are read from `/proc`. macOS and Windows
build and attach to a remote server with `--url`; those rows print `?` there,
and WSL2 runs the Linux binary for the full view. GPU rows come from
`nvidia-smi`.

## Quick start

Type one word on the machine that runs llama-server:

```sh
toktape
```

It discovers the server (`127.0.0.1:8080`, then `:8081`, `:8000`, `:5000`),
attaches by matching the model path against `/proc/*/cmdline`, sends requests
from the built-in prompt set — 21 long artifacts, trimmed to a prefix this
machine's measured prefill can pay for — streams the answers while sampling
faults, RSS and GPU state, writes `~/.toktape/runs/<id>.toktape`, and prints
the card with the line that tells you how to share it. No port, no PID, no
flag; a model still loading is waited for (`--wait`, default ten minutes).

**Eight streams at once**, which is what an agent workload does to a server —
per-stream tok/s falls as N rises and that is the finding, and the aggregate
is the number that answers "can this rig serve eight agents":

```sh
toktape --sessions 8
```

**Watch it live**, tiles per stream plus a machine pane:

```sh
toktape --sessions 4 --tui
toktape --sessions 8 --tui --grid 2x2     # four tiles per page, ←/→ to page
```

**Share or replay it:**

```sh
toktape card ~/.toktape/runs/<id>.toktape -o png          # 1200×675 image next to the tape
toktape card ~/.toktape/runs/<id>.toktape -o md --copy    # card + llama-bench table, on the clipboard
toktape render                                          # the newest run as a GIF
```

```sh
toktape play ~/.toktape/runs/<id>.toktape --speed 2
```

## How it measures

- **Prefill is a machine rate plus a fixed cost, measured before your run.**
  Two raw `/completion` prompts — one 128 tokens, one as long as the short
  one's own observed cost allows — are fitted into a per-token rate and a
  per-request fixed cost, with nothing else in flight. A prefill figure that
  is mostly fixed cost is not a throughput, and the fit is what says which is
  which. Each run's probe prompts open with a per-run salt, so measuring the
  same warm server twice still measures.
- **The prompt set is the instrument.** The run does not send a prompt you
  typed; it sends a fixed set of 21 artifacts — code with real bugs and
  passing tests, a query plan, incident timelines, an ADR, Korean and
  Japanese prose — each 18–30 KB, longer than any one box should send whole,
  on purpose. A run sends a prefix of each, sized from the probe's fit so the
  fixed cost stays under a twentieth of the prefill it measures, and records
  how many characters it sent; the published record is verified against the
  set, prefix and all. The slot's context has the next word: the run's answer
  budget is lowered into what the slot holding that prompt can actually hold,
  and a prompt no slot can hold is refused before the first request, with
  both numbers.
- **The server's figures are the record, the client's are the check.** Every
  chunk brings the server's own rates and token counts; toktape recomputes
  them from its own clock and records whether the two agreed within 2
  percent. A disagreement is never resolved by taking the nicer number.
- **The client's rate is measured over the content window**, between the
  first and last token that carried text — not wall time. Below 32 generated
  tokens a rate is a `Sample`, not a "decode".
- **Reasoning tokens count.** `reasoning_content` deltas are decode tokens
  and TTFT is the first token of either kind.
- **cold / warm comes from the fault count**, never a guess; residency is
  derived from the process's own mappings, never recorded; "never loaded"
  is read from the GGUF tensor headers; a machine witness (load, IO, page
  cache, live `llama-*` processes, cpufreq, one temperature) is taken at
  every round edge.
- **Unknown prints as `?`.** The card never shows a value it did not observe,
  and a run without a fault measurement is never labelled cold.

## A number worth quoting

The card qualifies every figure it prints. These habits produce a card with
little to qualify — most are one flag, or none.

- **Run twice, quote the second.** The first run against a just-loaded model
  pulls its weights off disk while it decodes, and the card labels it `cold`
  from the fault count. The warm rate is the quotable one.
- **Length in seconds.** `--for 30s`. A token count is a different amount of
  time on every machine — which is the thing you are recording to find out —
  and naming `--n-predict` turns the clock off, so a run that names a cap is
  cut by it.
- **Reasoning models think on the clock.** A default budget can be spent
  before the answer starts: `--for 60s` to hold the thought, `--no-think`
  for a like-for-like with a non-reasoning model.
- **Read the caveats line before quoting anything.** A generation too short
  to be a rate, a busy machine, a clock cut — all of it is on the card before
  it is in your post, and `-o json` carries the same list with severities.
- **Compare like with like.** The prompt set id, sampling, endpoint and
  engine build are all on the card; two cards are comparable or they say why
  not.
- **As many streams as the question.** `--sessions 8` is what eight agents do
  to a server; a single stream answers a different question.
- **Leave the box alone.** A compile job moves decode by more than most of
  the changes people test; the witnesses are read at every round edge and the
  card says `contended` or `conditions_changed` when the ground moved.

## What the card shows

The same fields in the same places on every card, each one there because it
settles an argument: decode and prefill never mixed, with the queue split out
of the engine's work (`engine prefill 10732 ms · queue 22 ms`); the prefix
cache hit; the prefill fit beside the run's own figures; page faults per
token; where the model actually sits — placed against resident, weights
against KV against compute, never-loaded bytes from the tensor headers;
bandwidth named against the bus that is the wall, per verify step when a
draft model is in; the sampling actually sent; the draft's acceptance rate
and step shape; flags in full and the exact quantization; contended; and
whether N × per-stream really is the aggregate.

## Commands

| verb | what it does | example |
| --- | --- | --- |
| `record` | attach and record a run; the default verb | `toktape --sessions 4 --for 30s` |
| `card` | re-render a card from a tape | `toktape card <tape> -o png` |
| `play` | replay a run on the live screen | `toktape play <tape> --speed 4` |
| `render` | render a run as GIF, mp4, asciicast or PNG frames | `toktape render <tape> --mp4 clip.mp4` |
| `ls` | list recorded runs | `toktape ls` |
| `log` | the experiment ledger of every run | `toktape log --sort decode` |
| `compare` | diff two runs, metrics and flags | `toktape compare a.toktape b.toktape` |
| `publish` | upload a run and print its link | `toktape publish <tape>` |
| `profile` | name the author every publish carries | `toktape profile --name NAME --link URL --avatar FILE --bio TEXT` |
| `runs` | list what is published, filtered and ordered like the site | `toktape runs --gpu rtx-3090 --sort decode` |
| `show` | read one published run; `--save` fetches its record | `toktape show <id> --save run.toktape` |
| `reindex` | recompute a published run's search row from its record — the figures a newer toktape indexes | `toktape reindex <id>` |
| `version` | print the version | `toktape version` |

A recording ends on the clock — twenty seconds by default, `--for 30s` to aim
it — because the same 256 tokens is two seconds on one machine and two
minutes on another; nothing is cut under 64 tokens, and `--n-predict` is the
cap that also applies (naming it turns the clock off). `--sessions` past
eight needs `--max-sessions` naming the same number; `--prompt` (repeatable)
or `--prompts` (a JSONL file, one round per line) replace the built-in set;
`--spec-n-max 3,5` runs the set once per speculative `n_max` into one tape.

How the request is shaped moves the number as much as the server's flags:
greedy against the server's default sampling against thinking left on is an
eleven per cent spread on one engine, so `record` names all three — `--temp`,
`--no-think`, `--endpoint chat|completion`, and `--param key=value` for
anything else the build honours. Whatever is sent is recorded and named on
the card.

`-o FORMAT` is llama-bench's `-o` with llama-bench's words: `json`, `jsonl`,
`md` (the card in a fence, a llama-bench table, a Reproduce block), `csv`,
`tsv`, `sql`. `toktape log -o sql | sqlite3 runs.db` is a database. Unknown
is `?` on the terminal, empty in exports, `NULL` in sql.

Runs append to `runs.tsv` beside the tapes, so a sweep is one table; label
runs with `--tag` and `--note`, and `log` takes `--sort`, `--model`, `--tag`
and `--limit N` to read it back:

```sh
toktape --tag ngl=40 --note "fa on"     # record, labelled
toktape log --sort decode                # which setting won
toktape log --tag ngl -o md              # paste into an issue
toktape log --rebuild                    # regenerate from the tapes
```

```sh
toktape log -o sql | sqlite3 runs.db
duckdb -c "select tag, decode_tok_s from read_csv('~/.toktape/runs/runs.tsv')"
```

A clip renders from the tape, never a screen recording, and plays at 1:1 —
nothing is compressed unless you ask, because a clip that sped a run up would
be lying about the one number the page is about. `--prefill-lead 3s` opens it
just before the first token:

```sh
toktape render ~/.toktape/runs/<id>.toktape
toktape render ~/.toktape/runs/<id>.toktape --mp4 clip.mp4 --cast clip.cast
```

## From an agent

Most people who run toktape will not type the command: Claude Code or Codex
will. The contract for that reader is
[`docs/agents.md`](docs/agents.md) and `toktape help agents` inside the
binary — five exit codes, `-o json` printing one object on stdout whether the
run succeeded or failed, and the habits in
[A number worth quoting](#a-number-worth-quoting). Reading is free
(`toktape runs -o json`); recording is not.

## Publish

A tape is small enough to hand over whole — the hero is 25 KB — and a page
that has the tape can draw everything else from it. That is what
[tape.midagedev.com](https://tape.midagedev.com) does: the link is the card,
the run replayed in the browser, the transcript, the mp4 and the record
itself.

```sh
toktape publish ~/.toktape/runs/<id>.toktape --dry-run
toktape publish ~/.toktape/runs/<id>.toktape
```

`--dry-run` prints exactly what would go up; read it once, because a
published run is **public** and carries **its text** — the prompts and what
the model wrote — by default: a rate without the text it was measured on is
half a claim. `--private` keeps a run out of the search, `--no-text` leaves
the text home, and both can be defaulted in `~/.toktape/config.toml`.
`toktape profile` names the author once per machine; `--title`/`--note`
attach a lab-note to one run; `publish --edit <id>` rewrites what the page
shows.

Taking a run down is one command, `toktape publish --delete <id>`. With a
journal token it is your token that opens it; an anonymous upload's **delete
token** is printed once and kept in `~/.toktape/published.json`, so the
command works there too, and the entry goes when the run does.

The hub is a setting, not a fixture. `service = "https://tapes.example.com"`
in `~/.toktape/config.toml` (or `TOKTAPE_SERVICE`, or `--url`) points every
command at your own, and the token in that file is only ever sent to the
service named beside it. [`web/README.md`](web/README.md) is how to host one.

The site is a search, not a leaderboard: newest first, every row with the
caveats its card would print, filters for model, quantisation, engine, GPU,
host and VRAM. All of it is readable from the terminal too:

```sh
toktape runs --gpu rtx-3090 --sort decode      # what the site lists, as a table
toktape runs --mine -o json                     # your own runs, the API's body verbatim
toktape show <id> --save run.toktape               # one run's summary, and its record
toktape card run.toktape                           # the card, drawn locally from that record
```

Hostnames and absolute paths are removed whatever the visibility is. The
service itself never opens a tape: everything richer than the index row is
the same renderer this binary uses, compiled to WebAssembly and run in your
browser.

## FAQ

**Why not `llama-bench`?** Use both. `llama-bench` measures the engine's
compute in isolation; toktape measures the server — slots, prefix-cache
reuse, TTFT under queue pressure, what happens when eight streams arrive at
once. Those are the numbers an agent workload actually sees.

**How do I know the prompt wasn't cached?** The card says so:
`Prefix cache 0% hit (0/512)`, and `cold`/`warm` from the fault count. If
someone doubts a card, send them the tape; they render the same card.

**Does it send anything anywhere?** No. It talks to your llama-server and
writes files under `~/.toktape`. There is no telemetry and no account.

## Supported

| | |
| --- | --- |
| Servers | llama-server (upstream llama.cpp), ik_llama.cpp, any server that answers `/props` with an `engine` object |
| Linux | primary target, x86_64 and arm64, full `/proc` view |
| macOS | builds and runs; attach with `--url`; no `/proc` view, so memory and fault rows are `?` |
| Windows | own binary, `--url` attach, no `/proc` view; WSL2 runs the Linux binary for the full view |
| GPU | NVIDIA through `nvidia-smi` |

Any other OpenAI-compatible server — vLLM, SGLang, TabbyAPI, LM Studio —
records in a generic mode (`--engine-kind openai`, or auto-detected): no
timings come back, so the recorder's own clock is the record, the card says
`client-timed` beside the rate, and you compare it only with other
client-timed runs.

Ollama (port 11434) and LM Studio (1234) are found without `--url`, and
`--model <id>` picks one when the server lists several. These servers count
tokens only in a closing `usage` message, which a stream cut by the clock
never sends — so toktape first sends two short requests to measure decode
and prefill, then sizes the prompts and the answer cap so every stream ends
on its own inside the clock. vLLM's `max_model_len` is read as the context,
and its token count is requested on every chunk. Measured on Ollama 0.34 and
vLLM 0.29: the default command finishes in about twenty seconds with a rate
on the card.

## The `.toktape` format

Gzipped JSON, one file per run, schema version 1 (plain JSON is read too). It
holds the summary, per-token timestamps and text, the sample series, the
server's raw timings, its flags and build, and the placement estimate. A
reader refuses a newer schema rather than guessing. Share the tape; anyone
with toktape renders the same card from it.

## Contributing

Issues and pull requests are welcome; a bug report is most useful with the
tape attached, because a run is fully described by its `.toktape`.
`./scripts/check.sh` is the gate — gofmt, build, vet, a Linux cross-build,
the tests — and CI runs exactly that. See
[CONTRIBUTING.md](CONTRIBUTING.md) for the layout and the rules the schema
and the card follow.

## License

MIT. See [LICENSE](LICENSE).
