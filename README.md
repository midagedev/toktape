# toktape

*the black-box tape for local LLM serving*

toktape attaches to a llama-server you already have running, records one run
into a `.tape` file, and prints a card that says where the model sits, what the
process actually touched, and how fast the request really was.

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape recording 8 concurrent streams"></p>

<p align="center"><em>Eight streams at once, replayed from a tape through the same renderer <code>toktape render</code> uses — no terminal recorder involved.</em></p>

```text
┌──────────────────────────────────────────────────────────────────────┐
│ toktape v0.1.0                       20260913-150210-qwen3.5-35b-a3b │
├──────────────────────────────────────────────────────────────────────┤
│ MODEL    Qwen3.5-35B-A3B-UD-Q4_K_M.gguf · UD-Q4_K_M · 19.8 GiB       │
│ ENGINE   llama-server b3650 (a1b2c3d) · linux 6.8.0-45-generic       │
│          workstation                                                 │
│ RIG      2× RTX 3090 24G · AMD Ryzen 9 7950X · 64 GB DDR5-6000       │
├──────────────────────────────────────────────────────────────────────┤
│ Decode        12.1 tok/s · ≈ 16 GB/s, 1% of peak                     │
│ Prefill       1980 tok/s · TTFT 210 ms · 512 prompt tokens           │
│ Context       32768 (512 in / 128 out)                               │
│ Prefix cache  0% hit (0/512) · cold                                  │
│ Streams       8 × 12.1 tok/s = 96.8 tok/s aggregate                  │
│               TTFT p50 210 ms p95 480 ms · slots busy max 8          │
├──────────────────────────────────────────────────────────────────────┤
│ MEMORY   GPU0 [████░░░░░░] 9.6/24.0 GiB                              │
│          GPU1 [████░░░░░░] 9.2/24.0 GiB                              │
│          weights 13.5 | kv 3.0 | compute 0.9 GiB                     │
│          Host RSS 3.4 GiB (file 2.9 / anon 0.5)                      │
│          Page faults 1.4 maj/token (1420 during decode)              │
├──────────────────────────────────────────────────────────────────────┤
│ HOST     GPU0 71°C 340 W · GPU1 69°C 330 W · throttled: no           │
│          contended: no                                               │
├──────────────────────────────────────────────────────────────────────┤
│ FLAGS    -ngl 99 -fa on -b 2048 -ub 512 -ctk q8_0 -ctv q8_0          │
│          --load-mode mmap -ncmoe 12 -t 16                            │
│          -ot blk\.(3[6-9]|4[0-7])\.ffn_.*_exps=CPU                   │
├──────────────────────────────────────────────────────────────────────┤
│ ! nvml unavailable, VRAM read from nvidia-smi                        │
│ ! cold run: 1.4 major faults per token during decode                 │
├──────────────────────────────────────────────────────────────────────┤
│                toktape · github.com/midagedev/toktape                │
└──────────────────────────────────────────────────────────────────────┘
```

## Install

```sh
brew install midagedev/tap/toktape
```

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

```sh
go install github.com/midagedev/toktape/cmd/toktape@latest
```

The script verifies the download against the release checksums and installs to
`~/.local/bin`; set `TOKTAPE_INSTALL` to put it elsewhere and `TOKTAPE_VERSION`
to pin a release. One static binary with nothing to resolve, so `scp` to the
box that runs the server is also a valid install.

Linux is the primary target. macOS attaches to a remote llama-server with
`--url`, since the `/proc` view the card is built on only exists on the server's
own host.

<details>
<summary>Cutting a release</summary>

Push a `v*` tag and `.github/workflows/release.yml` runs GoReleaser. It needs
one repository secret, `HOMEBREW_TAP_GITHUB_TOKEN`: a token with write access to
`midagedev/homebrew-tap`, because the built-in `GITHUB_TOKEN` cannot write to
another repository. Set it before the first tag, or the tap will not be
updated.

</details>

## First run

Type one word.

```sh
toktape
```

1. **Discover** — probe `127.0.0.1:8080`, then `:8081`, `:8000`, `:5000`, and
   take the first that answers `/props`.
2. **Attach** — read the model path, context size and slots from `/props`, then
   find the server's PID by matching that model path against `/proc/*/cmdline`
   and open the `/proc` view on it.
3. **Prompt** — send one request from a fixed prompt set with
   `timings_per_token` and `return_progress` on, and stream the answer while
   sampling major faults, RSS and GPU state.
4. **Tape** — write the whole run to `~/.toktape/runs/<id>.tape`: the server's
   own timings, per-token timestamps and text, the sample series, the server's
   flags and build, the placement estimate.
5. **Card** — print the 72-column card, save it beside the tape as
   `<id>.card.txt`, and print the line that tells you how to share it.

No port to type, no PID to find, no flag to learn.

### The multi-session shot

```sh
toktape -n 8
```

Eight streams at once, which is what an agent workload does to a server. The
card then carries both figures. Per-stream tok/s falls as N rises and that is
expected; the aggregate is the number that answers "can this rig serve eight
agents at once", and a single-stream benchmark cannot show it.

## What you see

The same fields in the same places on every card, so two cards can be read side
by side. Each field is there because it settles an argument.

- **Decode and prefill, never mixed.** One "45 tok/s" says nothing: prefill is
  compute-bound, decode is memory-bandwidth bound. The card prints TTFT, prompt
  tok/s and decode tok/s separately, with the prompt and generated token counts
  beside them.
- **Prefix cache hit.** `0% hit (0/512)` or `78% hit (400/512)`. A system prompt
  that differs by one character misses the cache, and prefill then looks ten
  times slower or ten times faster for no visible reason.
- **cold / warm.** The first prompt against an mmap'd model pulls thousands of
  4 KiB pages off NVMe and reports a fraction of the warm rate. The label comes
  from the major faults taken during decode, not from a guess.
- **Page faults per token.** The live view draws them as a sparkline on the
  same time axis as the tokens, and the card keeps the figure: major faults per
  token during decode is the one number that explains "it freezes, then
  continues".
- **RSS is not loaded.** Under mmap, resident set is what the process has
  touched, and it is short by whatever was copied to VRAM and dropped. The card
  splits host RSS into file and anon, splits VRAM into weights, KV cache and
  compute buffers, and computes never-loaded bytes from the GGUF tensor
  headers — not from file size minus RSS, which overstates it by tens of GB.
- **Flags, in full.** `-ngl -fa -b -ub -ctk -ctv --load-mode -ot`. Flash
  attention state and batch sizing are the two omissions that reliably turn a
  results post into a fifty-comment thread.
- **The exact quantization.** `UD-Q4_K_M`, never shortened to "Q4". `IQ4_NL` and
  `Q4_0` are not the same measurement.
- **contended.** Another harness on the box moves decode by more than most of
  the changes people are testing. The card reads the load average and the other
  GPU processes and labels the run, so a contended number is not quoted as a
  clean one.
- **Streams.** `8 × 12.1 tok/s = 96.8 tok/s aggregate`, with TTFT p50 and p95
  and the highest number of slots busy at once.

## Verbs

| verb | what it does | example |
| --- | --- | --- |
| `record` | attach and record a run; the default verb | `toktape record -n 4 --n-predict 512` |
| `card` | re-render a card from a tape | `toktape card ~/.toktape/runs/<id>.tape` |
| `ls` | list the runs you have recorded | `toktape ls --out ~/.toktape/runs` |
| `log` | the experiment ledger of every run | `toktape log --sort decode` |
| `compare` | diff two runs, metrics and flags | `toktape compare a.tape b.tape` |
| `render` | render a run as a clip | `toktape render ~/.toktape/runs/<id>.tape` |

Record flags: `--url`, `-n` / `--concurrency`, `--prompt` (repeatable, cycled to
fill `-n`), `--n-predict`, `--out`, `--tag`, `--note`, `--no-card`, `--json`,
`--quiet`.

Card flags: `--md` renders the card in a fence plus a llama-bench compatible
table, `--json` renders the run summary, `--copy` sends the output to the
clipboard over OSC 52. `--md` and `--json` are alternatives, not a pair.

```sh
toktape card ~/.toktape/runs/<id>.tape --md --copy
```

## Experiment log

Every run appends a row to `runs.tsv` next to the tapes, so a sweep is one
table instead of a folder of cards. Label the runs as you go with `--tag` and
`--note`; both are stored in the tape, so the ledger can always be rebuilt.

```sh
toktape --tag ngl=40 --note "fa on"          # record, labelled
toktape log --sort decode                     # which setting won
toktape log --tag ngl --md                    # paste into an issue
toktape log --rebuild                         # regenerate from the tapes
```

`--tsv`, `--csv`, `--json` and `--md` write every column; `--model`, `--tag`
and `-n` narrow the table. Unknown is `?` on the terminal and an empty cell in
every export, so the numeric columns import as numbers:

```sh
sqlite3 runs.db ".import --tsv ~/.toktape/runs/runs.tsv runs"
duckdb -c "select tag, decode_tok_s from read_csv('~/.toktape/runs/runs.tsv')"
```

### Make your own clip

The image at the top of this page is not a screen recording. It is a tape,
replayed frame by frame, and any tape you have renders the same way.

```sh
toktape render ~/.toktape/runs/<id>.tape
```

That writes `<id>.gif` beside the tape and prints the path. With no tape named
at all, the newest run in `~/.toktape/runs` is used, so `toktape render` on its
own turns the run you just recorded into something you can attach to a post.

Render flags: `--gif FILE`, `--mp4 FILE` (needs ffmpeg on `PATH`), `--cast
FILE` for an asciicast v2 recording, `--frames DIR` for the PNG sequence.
`--duration`, `--fps` and `--size WxH` shape the clip; the defaults are a ten
to twelve second clip at 30 fps on a 120×36 screen. Name several outputs at
once and they all come out of the same frames.

```sh
toktape render ~/.toktape/runs/<id>.tape --mp4 clip.mp4 --cast clip.cast
```

## How it measures

- **The server's figures are the record, the client's are the check.** Requests
  carry `timings_per_token`, so every chunk brings the server's own
  `prompt_per_second`, `predicted_per_second`, `prompt_n` and `predicted_n`.
  toktape computes the same rates from its own clock and keeps both; the tape
  records whether they agreed within 2 percent. A disagreement is never
  resolved by taking the nicer number.
- **The client's rate is measured over the content window** — the gaps between
  the first token that carried text and the last one, not the wall time from
  the request going out to the socket closing. The role-only chunk and the
  finish chunk are outside the window, and counting n tokens over n-1 gaps
  would inflate the rate past the 2 percent tolerance on its own.
- **A rate is called "decode" only above 32 generated tokens.** Below that the
  card says `Sample`. A nineteen-token answer is not a decode rate.
- **"Never loaded" is read from the GGUF tensor headers**, per tensor class.
- **Without a PID** — a remote server, or a container whose process table you
  cannot see — the run still records the rates, the prefix cache hit and the
  GPU state. Host RSS, page faults and the server's flags print as `?`, and the
  card carries a line saying the `/proc` view was unavailable. A run with no
  fault measurement is never labelled cold: the label falls back to warm or
  cached, because a cold claim that was not measured is worse than no claim.
- **Without a GPU** the VRAM and thermal rows print as `?` and the reason goes
  on the card. Where `nvidia-smi` answers but NVML does not, the card says VRAM
  was read from `nvidia-smi`, which is the coarser source.
- Unknown prints as `?`. The card never shows a value that was not observed.

## Supported

| | |
| --- | --- |
| Servers | llama-server (upstream llama.cpp), ik_llama.cpp |
| Linux | primary target, x86_64 and arm64, full `/proc` view |
| macOS | builds and runs; no `/proc` view yet, so the memory and fault fields are `?` |
| Windows | through WSL2, where `/proc` means what it means on Linux |
| GPU | NVIDIA through `nvidia-smi` |

Roadmap:

- macOS collector: `task_vm_info.pageins` and IOReport, without sudo.
- Ollama offload card from `/api/ps` (`size_vram / size`).
- `ab URL1 URL2` — two servers, one prompt, side by side.
- PNG card renderer is in the tree (`internal/card/png`, 1200x675, bundled
  fonts); the CLI flag is coming.

## The `.tape` format

- Gzipped JSON, one file per run, schema version 1; plain JSON is read too, so
  a tape stays greppable after `gunzip`.
- A reader refuses a tape written by a newer schema instead of guessing at it.
- It holds the run summary the card is rendered from, plus per-token timestamps
  and text, the sample series, the server's raw timings, its flags and build,
  and the placement estimate.
- Every renderer reads the tape and nothing else, so a card is a pure function
  of the file.
- Share your tape. Anyone with toktape renders the same card from it.

## Contributing

`./scripts/check.sh` is the gate: gofmt, build, vet, a Linux cross-build, and
the tests. Run it before opening a pull request. Code, comments and every
user-facing string are English; the design documents under `docs/` are Korean.

The hero clip at the top of this page is committed, and it is regenerated from
the example run by one command:

```sh
go run ./internal/render/cmd/hero
```

It writes `assets/hero.gif` with the library's defaults and prints the size and
frame count. `go run ./internal/render/cmd/rendershot` writes the same clip
plus a few stills into `scratch/`, which is what a look-and-adjust round on the
TUI uses.

## License

MIT. See [LICENSE](LICENSE).
