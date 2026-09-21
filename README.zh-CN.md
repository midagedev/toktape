# toktape

**本地 LLM 推理服务的黑匣子磁带。**

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md) · 简体中文 · [繁體中文](README.zh-TW.md)

[![check](https://github.com/midagedev/toktape/actions/workflows/ci.yml/badge.svg)](https://github.com/midagedev/toktape/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/midagedev/toktape)](https://github.com/midagedev/toktape/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/midagedev/toktape)](go.mod)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

<img src="docs/mascot.png" align="right" width="150" alt="toktape 的吉祥物：戴着耳机、闭着眼睛、抱着一盘磁带的 Q 版小人">

toktape 接到你已经在跑的本地 LLM 服务上——llama-server、Ollama、vLLM、
LM Studio——把一次运行录进一个 `.toktape` 文件，然后打印一张卡片。提示词有没有
被缓存、跑了几路流、量化到底是哪一种、模型放在哪里、机器上当时有没有别的东西
在跑——每个跑分帖子里都要吵一遍的问题，这张卡片一次答完。一个静态二进制，
MIT，没有遥测，不需要账号。

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape 录制一个 35B 稀疏 MoE 的四路并发流，从敲下命令到出结果"></p>

<p align="center"><em>这是一次真实运行，按 1:1 播放，不是示意图：<code>toktape --sessions 4</code>，四路流各 37.3 tok/s。画面不是录屏，而是 <code>assets/hero.tape</code> 的回放；<code>toktape card assets/hero.tape</code> 会从同一个文件打印出下面这张卡片。</em></p>

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

## 安装

```sh
brew install midagedev/tap/toktape
```

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

Windows 的 zip、`go install`、从源码构建，以及各个系统上能看到什么，见
[docs/install.md](docs/install.md)。

## 使用

在跑着服务的那台机器上，敲一个词：

```sh
toktape
```

它会找到服务，用内置提示词发送二十秒，写出 `~/.toktape/runs/<id>.toktape`，
然后打印卡片。不用端口，不用 PID，不用任何参数。

八路同时——智能体负载对服务做的就是这件事——以及实时观看同一次运行：

```sh
toktape --sessions 8
toktape --sessions 4 --tui
```

可以分享成图片、Markdown、短片，或者一个链接：

```sh
toktape card ~/.toktape/runs/<id>.toktape -o png
toktape card ~/.toktape/runs/<id>.toktape -o md --copy
toktape render
toktape publish ~/.toktape/runs/<id>.toktape --dry-run
```

发布出去的运行是公开的，并且带着文本；`--dry-run` 会原样打印将要上传的内容。
已发布的运行可以在 [tape.midagedev.com](https://tape.midagedev.com) 上搜索。

## 文档

详细文档为英文。

- [它是怎么测的，卡片怎么读](docs/measurement.md)——每个数字的含义、得到一个值得
  引用的数字的习惯、常见问题、文件格式
- [命令](docs/commands.md)——所有动词、输出格式、运行台账、渲染短片
- [发布](docs/publish.md)——会上传什么、如何撤下、自己搭一个 hub
- [安装与平台](docs/install.md)——所有安装方式、支持的服务、Linux、macOS、
  Windows 各自能看到什么
- [给智能体](docs/agents.md)——Claude Code 或 Codex 替你运行 toktape 时的约定；
  也可以用 `toktape help agents` 查看

## 参与贡献

欢迎提 issue 和 pull request；报 bug 时附上磁带最有帮助。门禁是
`./scripts/check.sh`，详见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 许可证

MIT。见 [LICENSE](LICENSE)。
