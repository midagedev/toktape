# toktape

**本機 LLM 推論服務的黑盒子磁帶。**

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md) · [简体中文](README.zh-CN.md) · 繁體中文

[![check](https://github.com/midagedev/toktape/actions/workflows/ci.yml/badge.svg)](https://github.com/midagedev/toktape/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/midagedev/toktape)](https://github.com/midagedev/toktape/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/midagedev/toktape)](go.mod)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

<img src="docs/mascot.png" align="right" width="150" alt="toktape 的吉祥物：戴著耳機、閉著眼睛、抱著一卷磁帶的 Q 版小人">

toktape 會接上你已經在跑的本機 LLM 伺服器——llama-server、Ollama、vLLM、
LM Studio——把一次執行錄進一個 `.toktape` 檔案，然後印出一張卡片。提示詞有沒有
被快取、跑了幾路串流、量化到底是哪一種、模型放在哪裡、機器上當時有沒有別的
東西在跑——每一串跑分討論都要再吵一次的問題，這張卡片一次答完。一個靜態
執行檔，MIT，沒有遙測，不需要帳號。

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape 錄製一個 35B 稀疏 MoE 的四路並行串流，從輸入指令到結果出爐"></p>

<p align="center"><em>這是一次真實的執行，以 1:1 播放，不是示意圖：<code>toktape --sessions 4</code>，四路串流各 37.3 tok/s。畫面不是螢幕錄影，而是 <code>assets/hero.tape</code> 的重播；<code>toktape card assets/hero.tape</code> 會從同一個檔案印出下面這張卡片。</em></p>

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

## 安裝

```sh
brew install midagedev/tap/toktape
```

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

Windows 的 zip、`go install`、從原始碼建置，以及各個作業系統上看得到什麼，請見
[docs/install.md](docs/install.md)。

## 使用

在跑著伺服器的那台機器上，打一個字：

```sh
toktape
```

它會找到伺服器，用內建提示詞送出二十秒，寫出 `~/.toktape/runs/<id>.toktape`，
然後印出卡片。不用連接埠，不用 PID，不用任何旗標。

八路同時——代理工作負載對伺服器做的就是這件事——以及即時觀看同一次執行：

```sh
toktape --sessions 8
toktape --sessions 4 --tui
```

可以分享成圖片、Markdown、短片，或是一個連結：

```sh
toktape card ~/.toktape/runs/<id>.toktape -o png
toktape card ~/.toktape/runs/<id>.toktape -o md --copy
toktape render
toktape publish ~/.toktape/runs/<id>.toktape --dry-run
```

發布出去的執行是公開的，而且帶著文字；`--dry-run` 會原樣印出將要上傳的內容。
已發布的執行可以在 [tape.midagedev.com](https://tape.midagedev.com) 上搜尋。

## 文件

詳細文件為英文。

- [它是怎麼量的，卡片怎麼讀](docs/measurement.md)——每個數字的意義、得到一個值得
  引用的數字的習慣、常見問題、檔案格式
- [指令](docs/commands.md)——所有動詞、輸出格式、執行帳本、算繪短片
- [發布](docs/publish.md)——會上傳什麼、如何撤下、自己架一個 hub
- [安裝與平台](docs/install.md)——所有安裝方式、支援的伺服器、Linux、macOS、
  Windows 各自看得到什麼
- [給代理](docs/agents.md)——Claude Code 或 Codex 替你執行 toktape 時的約定；
  也可以用 `toktape help agents` 查看

## 參與貢獻

歡迎提 issue 和 pull request；回報 bug 時附上磁帶最有幫助。把關的是
`./scripts/check.sh`，詳見 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 授權

MIT。請見 [LICENSE](LICENSE)。
