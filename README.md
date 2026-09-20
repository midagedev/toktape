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

<p align="center"><em>A real run, not a mock-up: Qwen3.6-35B-A3B UD-Q6_K, 27.3 GiB of sparse MoE sitting whole on one RTX A6000. The command typed at a prompt, the server found and attached, four streams answering four different code-review questions at once at 42.1 tok/s each, then the result. Each stream stops when the model is finished, between 152 and 279 tokens; nothing was cut by a token cap. That is also why the card's 144 tok/s aggregate is not four times 42.1: it is measured over the whole decode window, and the last two seconds of that window have one stream left in them. The clip joins the run three seconds before its first token (<code>--prefill-lead 3s</code>): the rest of the wait for prefill is in the tape and on the clock the clip opens on, not in the clip. Everything you see plays at 1:1. It is <code>assets/hero.tape</code> replayed through the same renderer <code>toktape render</code> uses — no terminal recorder involved, and <code>toktape card assets/hero.tape</code> prints the card below from the same file.</em></p>

```text
┌──────────────────────────────────────────────────────────────────────┐
│ toktape v0.2.4               20260917-144056-qwen3-6-35b-a3b-ud-q6-k │
├──────────────────────────────────────────────────────────────────────┤
│ MODEL    Qwen3.6-35B-A3B-UD-Q6_K.gguf · UD-Q6_K · 27.3 GiB           │
│ ENGINE   ik_llama.cpp c10fbbcc · linux 6.8.0-139-generic             │
│          workstation                                                 │
│ RIG      RTX A6000 48G · AMD Ryzen Threadripper PRO 5975WX 32-Cores  │
│          252 GB DDR4-3600                                            │
├──────────────────────────────────────────────────────────────────────┤
│ Decode        144 tok/s aggregate · 42.1 tok/s each                  │
│               ≈ 125–230 GB/s, 16–30% of peak                         │
│ Prefill       167 tok/s aggregate · 42.1 tok/s each                  │
│               238 prompt tokens · engine prefill 5649 ms             │
│               queue 56 ms                                            │
│ Context       8192 (238 in / 214 out)                                │
│ Prefix cache  0% hit (0/238) · warm                                  │
│ Sampling      greedy (temp 0) · thinking off · chat                  │
│ Streams       4 streams · not all decoding at once                   │
│               TTFT p50 5704 ms p95 5707 ms · slots busy max ?        │
├──────────────────────────────────────────────────────────────────────┤
│ MEMORY   GPU0 [██████░░░░] 28.5/48.0 GiB                             │
│          weights 26.8 | kv ? | compute ? GiB                         │
│          Host placed 0.5 GiB (all in RAM)                            │
│          Host RSS 1.9 GiB (file 0.7 / anon 1.1)                      │
│          Page faults 0.0 maj/token (0 during decode)                 │
├──────────────────────────────────────────────────────────────────────┤
│ HOST     GPU0 68°C 281 of 300 W · throttled: no · contended: no      │
├──────────────────────────────────────────────────────────────────────┤
│ FLAGS    -ngl 99 -fa on -b 2048 -ub 512 -ctk default -ctv default    │
│          -t 32                                                       │
│          -m /models/Qwen3.6-35B-A3B/Qwen3.6-35B-A3B-UD-Q6_K.gguf     │
│          -c 32768 --jinja -np 4 --jinja --host 127.0.0.1 --port 8012 │
├──────────────────────────────────────────────────────────────────────┤
│ ! 2 caveats — ragged run: 4 × 42.1 is 168, not the 144 aggregate —   │
│   the run had a tail on fewer streams, and this tape cannot say how  │
│   long it was · recorded                                             │
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

**Windows.** The release page carries `toktape_<version>_windows_amd64.zip`
and an arm64 zip beside it; unpack `toktape.exe` anywhere on `PATH`. The
shell script above is POSIX and does not fetch them.

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
with `--url`; the `/proc` rows print `?` there. Windows has had a binary of
its own since v0.2.5 and sees what macOS sees; WSL2 runs the Linux binary
instead and keeps the `/proc` view. GPU rows come from `nvidia-smi`.

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
3. **Prompt** — send a request from the built-in prompt set (21 long
   artifacts, trimmed to a prefix this machine's measured prefill can pay
   for) with `timings_per_token` and `return_progress` on, and stream the
   answer while sampling major faults, RSS and GPU state.
4. **Tape** — write the whole run to `~/.toktape/runs/<id>.toktape`.
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
toktape card ~/.toktape/runs/<id>.toktape -o png          # 1200×675 image next to the tape
toktape card ~/.toktape/runs/<id>.toktape -o md --copy    # card + llama-bench table, on the clipboard
toktape render                                          # the newest run as a GIF
```

**Replay it** on the live screen, at any speed:

```sh
toktape play ~/.toktape/runs/<id>.toktape --speed 2
```

## How it measures

- **Prefill is a machine rate plus a fixed cost, measured before your run.**
  Two raw `/completion` prompts — one 128 tokens, one as long as the short
  one's own observed cost says the budget allows — are fitted into a
  per-token rate and a per-request fixed cost, with nothing else in flight.
  A prefill figure that is mostly fixed cost is not a throughput, and the
  fit is what says which is which; the card carries both. Each run's probe
  prompts open with a per-run salt, so measuring the same warm server twice
  still measures — the prefix cache never hands the probe a free point.
- **The prompt set is the instrument.** The run does not send a prompt you
  typed; it sends a fixed set of 21 artifacts — code with real bugs and
  passing tests, a query plan, incident timelines, an ADR, Korean and
  Japanese prose — each 18–30 KB, longer than any one box should send
  whole, on purpose: a prefill rate needs the fixed cost to be a small
  share of what it measures. A run sends a prefix of each, sized from the
  probe's fit so that share stays under a twentieth, and records how many
  characters it sent. Two boxes that trimmed differently still ran the same
  work — the published record is verified against the set, prefix and all.
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
  placement is the process's file-backed resident set at that instant;
  without a `/proc` view the split is not printed at all rather than
  defaulted to zero.
- **A machine witness at every round edge.** Load average, IO pressure, the
  page cache, the live `llama-*` processes, the cpufreq cap and one hwmon
  temperature. A run whose machine changed under it was never one
  measurement, and the card says so.
- **Without a PID** (a remote server, a container you cannot see into) the
  rates, prefix-cache hit and GPU state are still recorded; host RSS, page
  faults and flags print as `?`. **Without a GPU** the VRAM and thermal rows
  do. A run with no fault measurement is never labelled cold.
- **Unknown prints as `?`.** The card never shows a value it did not observe.

## A number worth quoting

The card qualifies every figure it prints. These are the habits that produce
a card with little to qualify — most of them are one flag, or none.

- **Run twice, quote the second.** The first run against a just-loaded model
  pulls its weights off disk while it decodes, and the card labels it `cold`
  from the fault count, not from a guess. The warm rate is the quotable one;
  quoting the cold one is fine if you say so.
- **Length in seconds.** `--for 30s`. A token count is a different amount of
  time on every machine — which is the thing you are recording to find out —
  and naming `--n-predict` yourself turns the clock off, so a run that names
  a cap is cut by it.
- **Reasoning models think on the clock.** Thinking tokens are decode
  tokens, so a default budget can be spent before the answer starts:
  `--for 60s` to hold the thought, or `--no-think` for a like-for-like with
  a non-reasoning model. The card names the sampling and the switch it
  measured.
- **Read the caveats line before quoting anything.** Every card ends with
  the reasons it might not be quotable, spelled out by code; `-o json`
  carries the same list with severities. A generation too short to be a
  rate, a busy machine, a clock cut — all of it is on the card before it is
  in your post.
- **Compare like with like.** The prompt set id, the sampling, the endpoint
  and the engine build are all on the card; two cards are comparable or they
  say why not.
- **As many streams as the question.** `--sessions 8` is what eight agents
  do to a server — queueing, slot contention, the aggregate under load. A
  single stream answers a different question, and per-stream tok/s falling
  as N rises is the finding, not a defect.
- **Leave the box alone.** A compile job or a second harness moves decode by
  more than most of the changes people test; the witnesses are read at every
  round edge and the card says `contended` or `conditions_changed` when the
  ground moved.

## What the card shows

The same fields in the same places on every card, so two cards can be read
side by side. Each field is there because it settles an argument.

- **Whether the number is quotable.** The caveats line, described above.
- **Decode and prefill, never mixed.** TTFT, prompt tok/s and decode tok/s
  separately with both token counts, the machine's prefill fit beside them,
  and — on a concurrent run — the wait for a free slot split out of the
  engine's own work (`engine prefill 10732 ms · queue 22 ms`), so a queue is
  never read as a slow model.
- **Prefix cache hit.** `0% hit (0/512)` or `78% hit (400/512)`. A system
  prompt that differs by one character misses the cache, and prefill then
  looks ten times slower or faster for no visible reason.
- **cold / warm.** From the major faults taken during decode, not from a
  guess.
- **Page faults per token.** The one number that explains "it freezes, then
  continues".
- **Where the model actually sits.** Host RSS split into file and anon, VRAM
  split into weights, KV cache and compute buffers, never-loaded bytes from
  the GGUF tensor headers, and the difference between placed and resident:
  `-ot ... exps=CPU` places bytes on the host without putting them in RAM,
  and the card says how much is read back off disk as the model decodes.
- **Bandwidth against the bus the bytes crossed.** A model split between
  VRAM and host RAM has no single bandwidth; the card names the side that is
  the wall (`≈ 115 GB/s from RAM per verify step, 99% of peak`) and prints a
  percentage only when the placement proves what the host reads. With a
  draft model the bytes are counted per verify step, which is what the bus
  actually saw.
- **What the request asked for.** Greedy against the server's default
  sampling against a thinking model left to think is an eleven per cent
  spread on one engine; `Sampling  temp default · chat` says which, and
  never invents a temperature nobody sent.
- **The draft, when there was one.** `n_max 3 · 52% accepted (260/504)` and
  the shape of the work it produced, `170 verify steps of 4.0 tokens`.
- **Flags, in full, and the exact quantization.** `-ngl -fa -b -ub -ctk
  -ctv --load-mode -ot`; `UD-Q4_K_M`, never shortened to "Q4". Flash
  attention state and batch sizing are the two omissions that reliably turn
  a results post into a fifty-comment thread.
- **contended.** The load average and the other GPU processes, labelled.
- **Streams.** TTFT p50 and p95, the most slots busy at once, and whether
  N × per-stream really is the aggregate — streams that stopped at
  different times leave it measured over a window whose tail held one of
  them, and the row says `4 streams · not all decoding at once`.

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
file is named) besides the formats above, and `--copy` (also to the clipboard:
pbcopy, wl-copy or xclip when one is there, OSC 52 to the terminal over SSH).

**Render:** `--gif FILE`, `--mp4 FILE` (needs ffmpeg on `PATH`), `--cast FILE`
(asciicast v2), `--frames DIR` (PNG sequence; 20 px cells where the GIF draws 13, so
pass `--font-size 13` to get the GIF's exact canvas — every ✓ line prints the
pixel size it drew), `--duration`, `--fps`, `--size WxH`, `--open` (the command typed at a shell prompt in front of the
run), `--prefill-lead D` (open the clip D before the first token, leaving the
rest of the wait out of it — every frame that is in it is still 1:1, and the
clock on screen starts where it was cut). Name several outputs at once and they
come out of the same frames. With no tape named, the newest run is used.

**Log:** `--sort`, `--model`, `--tag`, `--limit N`, `-o FORMAT`, `--rebuild`,
`--out`.

## Running it from an agent

Most people who run toktape will not type the command: Claude Code or Codex
will, on their behalf. There is a page written for that reader —
[`docs/agents.md`](docs/agents.md) — and `toktape help agents` is the same
contract inside the binary, where an agent can find it without being told.
The measurement habits are in [A number worth quoting](#a-number-worth-quoting);
what is left is the calling convention:

- **Branch on the exit code, never on the message.** Every verb ends on one
  of five documented codes, and `-o json` prints one object on stdout whether
  the run succeeded or failed, so there is one parse path and not two.
- **Two flags decide whether it fits your timeout.** `--wait` defaults to ten
  minutes, because a server loading a 450 GB model is worth waiting for;
  `--for` decides how long the generation itself runs.
- **Reading is free; recording is not.** `toktape runs -o json` and
  `toktape show <id> -o json` read the published record without touching a
  server; the bare verb records.

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
toktape render ~/.toktape/runs/<id>.toktape
toktape render ~/.toktape/runs/<id>.toktape --mp4 clip.mp4 --cast clip.cast
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

## Publish

A tape is small enough to hand over whole — the hero is 25 KB — and a page
that has the tape can draw everything else from it. That is what
[tape.midagedev.com](https://tape.midagedev.com) does: `toktape publish`
uploads the run and prints its link, and the link is the card, the run
replayed in the browser, the transcript, the mp4 and the record itself.

```sh
toktape publish ~/.toktape/runs/<id>.toktape --dry-run
toktape publish ~/.toktape/runs/<id>.toktape
```

`--dry-run` prints, field by field, exactly what would go up, and uploads
nothing. Read it once: a published run is **public** and carries **its text**
— the prompts and what the model wrote — by default, because a rate without
the text it was measured on is half a claim. The first real publish asks you
to confirm that, once. `--private` keeps a run out of the search (the link
still works and is the only way in); `--no-text` uploads the run without the
prompts and the answers; both can be made the default in
`~/.toktape/config.toml`.

`toktape profile` names the author once per machine — a nickname, one link
and an avatar PNG (at most 65536 bytes and 256×256) — and every publish
carries it, listed by `--dry-run` under "Who it says published it" with
anything else that leaves. The profile is unverified: anyone may type any
name. `publish --no-profile` leaves it off one run. `--title TEXT` and
`--note TEXT` (or `--note-file FILE`) attach a lab-note to one run — what
it was trying, in plain text — shown on the page between the byline and the
figures and carried on the row and the API beside the author.
A run you own can be changed after it is up: `publish --edit <id>`
with `--title`, `--note` (or `--note-file`) or `--private`/`--public`
rewrites what the page shows. Every journal token has a user home at
`/u/<handle>` showing the profile, the bio and that user's public runs;
the profile follows the token, so the newest publish's name and avatar are
what the home shows.

The site is a search, not a leaderboard: newest first, every row with the
caveats its card would print, filters for model, quantisation, engine, GPU,
host and VRAM — a GPU filter reaches a rig with two kinds of card by either
of them — and, when you ask, an order: oldest first or fastest decode first.
Each row replays its run in place, one at a time; on a phone the list is a
feed, on a desktop a grid of two or three across with the one under the
pointer playing. All of it is readable from the terminal too:

```sh
toktape runs --gpu rtx-3090 --sort decode      # what the site lists, as a table
toktape runs --mine -o json                     # your own runs, the API's body verbatim
toktape show <id> --save run.toktape               # one run's summary, and its record
toktape card run.toktape                           # the card, drawn locally from that record
```

`runs` takes the site's filters as flags and `--user HANDLE` for one home;
`show` takes a bare id or any of the run's links. Under `-o json` both print
the service's own body, so a script has one shape to parse.

Hostnames and absolute paths are removed whatever the visibility is — the
server's argv keeps its flags and loses its paths, the model keeps its file
name and loses its directory — and the same view is what the card at the top
of the page is drawn from, so nothing the JSON forgets is painted back in
pixels. The offset on the recording's timestamp is left as it was; whether it
should be is still an open question.

Every anonymous upload prints a **delete token** once. It is the only key to
that upload and is not saved anywhere; with it, `DELETE /api/v1/runs/<id>`
takes the run and its card down. The service itself never opens a tape: the
figures on its pages are the index row the client derived next to the
schema, and everything richer is the same renderer this binary uses,
compiled to WebAssembly and run in your browser.

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
| Servers | llama-server (upstream llama.cpp), ik_llama.cpp, any server that answers `/props` with an `engine` object |
| Linux | primary target, x86_64 and arm64, full `/proc` view |
| macOS | builds and runs; attach with `--url`; no `/proc` view, so memory and fault rows are `?` |
| Windows | own binary, `--url` attach, no `/proc` view; WSL2 runs the Linux binary for the full view |
| GPU | NVIDIA through `nvidia-smi` |

The server does not have to be llama.cpp. One that reports its own engine —
its name and version, the model's format and shape, and which bytes sit on
which device — is recorded from that report, with no GGUF opened and no
command line parsed. A server that answers for another process — a shim in
front of an engine that speaks only the OpenAI API — names that process's pid
in the same report, so the memory, page-fault and contention rows describe
the server and not the proxy in front of it.
[exl3-serve](https://github.com/midagedev/exl3-serve) does that for
ExLlamaV3: it presents one EXL3 model on the llama-server surface toktape
already speaks, so an EXL3 model records unchanged.

Any other OpenAI-compatible server — vLLM, SGLang, TabbyAPI, LM Studio and
the rest — records in a generic mode: `toktape --engine-kind openai` (or
nothing, since auto-detection tries `/props` first and falls back to
`/v1/models`) attaches to whatever answers `/v1/models`. Such a server
reports no timings, so the recorder's own clock is the record and the card
says `client-timed` beside the decode rate; compare only with other
client-timed runs. There are no slots and no flags block, and `--engine`
names the engine as a claim the card prints with that word.

Roadmap: a macOS collector without sudo, an Ollama offload card from
`/api/ps`, and `toktape ab URL1 URL2` — two servers, one prompt, side by
side.

## The `.toktape` format

Gzipped JSON, one file per run, schema version 1; plain JSON is read too, so a
tape stays greppable after `gunzip`. It holds the run summary the card is
rendered from, per-token timestamps and text, the sample series, the server's
raw timings, its flags and build, and the placement estimate. A reader
refuses a tape written by a newer schema instead of guessing at it. Every
renderer reads the tape and nothing else. Share your tape; anyone with
toktape renders the same card from it.

## Contributing

Issues and pull requests are welcome. Bug reports are most useful with the
tape attached: a run is fully described by its `.toktape`, so "here is the card
I got" and "here is the file" are the same thing.

`./scripts/check.sh` is the gate — gofmt, build, vet, a Linux cross-build,
the tests — and CI runs exactly that. See [CONTRIBUTING.md](CONTRIBUTING.md)
for the layout of the tree, how goldens are updated, and the rules the
schema and the card follow.

## License

MIT. See [LICENSE](LICENSE).
