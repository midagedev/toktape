# toktape

**The black-box tape for local LLM serving.**

English · [한국어](README.ko.md) · [日本語](README.ja.md) · [简体中文](README.zh-CN.md) · [繁體中文](README.zh-TW.md)

[![check](https://github.com/midagedev/toktape/actions/workflows/ci.yml/badge.svg)](https://github.com/midagedev/toktape/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/midagedev/toktape)](https://github.com/midagedev/toktape/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/midagedev/toktape)](go.mod)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

<img src="docs/mascot.png" align="right" width="150" alt="the toktape mascot: a small chibi in headphones, eyes closed, hugging a cassette tape">

toktape attaches to the local LLM server you already have running —
llama-server, Ollama, vLLM, LM Studio — records one run into a `.toktape`
file, and prints a card that settles the usual arguments: was the prompt
cached, how many streams, which quant exactly, where the model sits, was
something else running on the box. One static binary, MIT, no telemetry, no
account.

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape recording four concurrent streams of a 35B sparse MoE, from the command being typed to the result"></p>

<p align="center"><em>A real run at 1:1, not a mock-up: <code>toktape --sessions 4</code>, four streams at 37.3 tok/s each. It is <code>assets/hero.tape</code> replayed, and <code>toktape card assets/hero.tape</code> prints the card below from the same file.</em></p>

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

## Install

```sh
brew install midagedev/tap/toktape
```

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

Windows zips, `go install`, building from source, and what each OS can see:
[docs/install.md](docs/install.md).

## Use

Type one word on the machine that runs the server:

```sh
toktape
```

It finds the server, sends its built-in prompts for twenty seconds, writes
`~/.toktape/runs/<id>.toktape` and prints the card. No port, no PID, no flag.

Eight streams at once — what an agent workload does to a server — and the
same run watched live:

```sh
toktape --sessions 8
toktape --sessions 4 --tui
```

Share it as an image, as Markdown, as a clip, or as a link:

```sh
toktape card ~/.toktape/runs/<id>.toktape -o png
toktape card ~/.toktape/runs/<id>.toktape -o md --copy
toktape render
toktape publish ~/.toktape/runs/<id>.toktape --dry-run
```

A published run is public and carries its text; `--dry-run` prints exactly
what would go up. Published runs are searchable at
[tape.midagedev.com](https://tape.midagedev.com).

## Docs

- [How it measures, and how to read the card](docs/measurement.md) — what
  each figure is, habits for a number worth quoting, FAQ, the file format
- [Commands](docs/commands.md) — every verb, output formats, the run ledger,
  rendering clips
- [Publishing](docs/publish.md) — what goes up, taking a run down, hosting
  your own hub
- [Install and platforms](docs/install.md) — every install path, supported
  servers, what Linux, macOS and Windows each see
- [For agents](docs/agents.md) — the contract for Claude Code or Codex
  running toktape for you; also `toktape help agents`

## Contributing

Issues and pull requests are welcome; a bug report is most useful with the
tape attached. `./scripts/check.sh` is the gate. See
[CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT. See [LICENSE](LICENSE).
