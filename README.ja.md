# toktape

**ローカル LLM サービングのためのブラックボックステープ.**

[English](README.md) · [한국어](README.ko.md) · 日本語 · [简体中文](README.zh-CN.md) · [繁體中文](README.zh-TW.md)

[![check](https://github.com/midagedev/toktape/actions/workflows/ci.yml/badge.svg)](https://github.com/midagedev/toktape/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/midagedev/toktape)](https://github.com/midagedev/toktape/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/midagedev/toktape)](go.mod)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

<img src="docs/mascot.png" align="right" width="150" alt="the toktape mascot: a small chibi in headphones, eyes closed, hugging a cassette tape">

toktape は、すでに起動しているローカル LLM サーバー(llama-server、Ollama、vLLM、
LM Studio)にアタッチし、1 回の実行を `.toktape` ファイルに記録して、カードを
1 枚出力します。プロンプトはキャッシュされていたのか、何ストリームだったのか、
量子化は正確には何か、モデルはどこに載っているのか、そのマシンで他に何か動いて
いなかったか。ベンチマークのスレッドで毎回繰り返される問いに、その 1 枚が
答えます。静的バイナリ 1 つ、MIT、テレメトリもアカウントもありません。

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape recording four concurrent streams of a 35B sparse MoE, from the command being typed to the result"></p>

<p align="center"><em>演出ではなく、1:1 で再生した実際の実行です。<code>toktape --sessions 4</code>、4 ストリームがそれぞれ 37.3 tok/s。画面録画ではなく <code>assets/hero.tape</code> を描き直したもので、下のカードは同じファイルから <code>toktape card assets/hero.tape</code> で出ます。</em></p>

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

## インストール

```sh
brew install midagedev/tap/toktape
```

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

Windows 用 zip、`go install`、ソースからのビルド、OS ごとに何が見えるかは
[docs/install.md](docs/install.md) にあります。

## 使い方

サーバーが動いているマシンで、1 語打つだけです。

```sh
toktape
```

サーバーを見つけ、内蔵プロンプトを 20 秒間送り、`~/.toktape/runs/<id>.toktape` を
書き出してカードを表示します。ポートも PID もフラグも要りません。

同時 8 ストリーム(エージェントのワークロードがサーバーにかける負荷です)と、
同じ実行をライブで見る方法:

```sh
toktape --sessions 8
toktape --sessions 4 --tui
```

画像、Markdown、クリップ、リンクのどれでも共有できます。

```sh
toktape card ~/.toktape/runs/<id>.toktape -o png
toktape card ~/.toktape/runs/<id>.toktape -o md --copy
toktape render
toktape publish ~/.toktape/runs/<id>.toktape --dry-run
```

公開した実行は誰でも見られ、テキストも一緒に載ります。何が上がるかは `--dry-run` が
そのまま表示します。公開された実行は
[tape.midagedev.com](https://tape.midagedev.com) で検索できます。

## ドキュメント

詳細ドキュメントは英語です。

- [どう測り、カードをどう読むか](docs/measurement.md) — 各数値の意味、引用に
  値する数字を得るための習慣、FAQ、ファイルフォーマット
- [コマンド](docs/commands.md) — すべての動詞、出力形式、実行台帳、クリップの
  レンダリング
- [公開する](docs/publish.md) — 何が上がるか、取り下げ方、ハブの自前運用
- [インストールとプラットフォーム](docs/install.md) — すべての導入方法、対応サーバー、
  Linux・macOS・Windows それぞれで見えるもの
- [エージェント向け](docs/agents.md) — Claude Code や Codex が toktape を代わりに
  実行するときの契約。`toktape help agents` でも読めます

## コントリビュート

Issue も Pull Request も歓迎します。バグ報告にはテープを添付してもらえると
いちばん助かります。ゲートは `./scripts/check.sh` で、詳しくは
[CONTRIBUTING.md](CONTRIBUTING.md) にあります。

## ライセンス

MIT。[LICENSE](LICENSE) を参照してください。
