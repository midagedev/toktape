# toktape

**本地 LLM 推理服务的黑匣子磁带。**

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md) · 简体中文 · [繁體中文](README.zh-TW.md)

[![check](https://github.com/midagedev/toktape/actions/workflows/ci.yml/badge.svg)](https://github.com/midagedev/toktape/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/midagedev/toktape)](https://github.com/midagedev/toktape/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/midagedev/toktape)](go.mod)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

<img src="docs/mascot.png" align="right" width="150" alt="toktape 的吉祥物：戴着耳机、闭着眼睛、抱着一盘磁带的 Q 版小人">

toktape 接到你已经在跑的 llama-server 上，把一次运行录进一个 `.toktape`
文件，然后打印一张卡片：模型放在哪里，进程实际碰过什么，这次请求到底有多快
——一路流也行，八路同时也行。

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape 录制一个 35B 稀疏 MoE 的四路并发流，从敲下命令到出结果"></p>

<p align="center"><em>这是一次真实运行，不是示意图：默认命令加上 <code>--sessions 4</code>，四路流各以 37.3 tok/s 作答，直到 20 秒的时钟把它们截断，全程 1:1 播放。提示词是 800 个 token，因为 toktape 先在这台机器上量过“四个预填充同时进行”要花多少时间，再决定长度。画面是 <code>assets/hero.tape</code> 经由 <code>toktape render</code> 所用的同一个渲染器回放出来的——没有用任何终端录屏工具；<code>toktape card assets/hero.tape</code> 会从同一个文件打印出下面这张卡片。</em></p>

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

## 为什么

每一个“X tok/s on Y”的帖子最后都会吵到同一个地方：提示词是不是被缓存了，
flash attention 开了没有，几路并发，到底是哪个量化，机器上是不是还跑着别的
东西。toktape 用服务器自己报告的数字，在一张卡片上把这些全部回答掉，并且把
这次运行存下来，任何人都能从同一个文件渲染出同一张卡片。它是一个静态二进制，
MIT 许可，没有遥测，不需要账号。

## 安装

**Homebrew**（macOS 和 Linux）：

```sh
brew install midagedev/tap/toktape
```

**Shell 脚本**——按你的操作系统和 CPU 下载对应的发布包，用 `checksums.txt`
校验，然后把一个二进制装到 `~/.local/bin`（用 `TOKTAPE_INSTALL`、
`TOKTAPE_VERSION`、`TOKTAPE_BASE_URL` 调整；`--dry-run` 只看不装）：

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

**Windows：** 发布页上有 zip，把 `toktape.exe` 解压到 `PATH` 里的任意位置即可。
**Go：** `go install github.com/midagedev/toktape/cmd/toktape@latest`。
**从源码构建**（Go 1.26，不需要 cgo）：

```sh
git clone https://github.com/midagedev/toktape.git
cd toktape
go build -o toktape ./cmd/toktape
```

用 `toktape version` 确认。二进制运行时不解析任何外部依赖，所以 `scp` 到服务器
所在的机器上也算安装。Linux 是首要目标——内存、缺页和启动参数这几行读自
`/proc`。macOS 和 Windows 可以构建，并用 `--url` 接到远程服务器；那几行在
这两个系统上会打印 `?`，想要完整视图可以在 WSL2 里跑 Linux 二进制。GPU 那几行
来自 `nvidia-smi`。

## 快速开始

在跑 llama-server 的那台机器上敲一个词：

```sh
toktape
```

它会自己找到服务器（`127.0.0.1:8080`，然后是 `:8081`、`:8000`、`:5000`），
拿模型路径去比对 `/proc/*/cmdline` 来认出进程，从内置提示词集里发请求——21 篇
长文档，各自裁成这台机器实测的预填充速度负担得起的前缀——一边流式接收回答，
一边采样缺页、RSS 和 GPU 状态，写出 `~/.toktape/runs/<id>.toktape`，最后打印
卡片，以及告诉你怎么分享它的那一行。不用给端口，不用给 PID，不用任何参数；
模型还在加载的话它会等（`--wait`，默认十分钟）。

**八路同时**，这正是智能体负载对服务器做的事——N 越大，每路的 tok/s 越低，
这本身就是结论；而聚合值回答的是“这台机器能不能同时伺候八个智能体”：

```sh
toktape --sessions 8
```

**实时观看**，每路流一块面板，外加一块机器状态面板：

```sh
toktape --sessions 4 --tui
toktape --sessions 8 --tui --grid 2x2     # four tiles per page, ←/→ to page
```

**分享，或者回放：**

```sh
toktape card ~/.toktape/runs/<id>.toktape -o png          # 1200×675 image next to the tape
toktape card ~/.toktape/runs/<id>.toktape -o md --copy    # card + llama-bench table, on the clipboard
toktape render                                          # the newest run as a GIF
```

```sh
toktape play ~/.toktape/runs/<id>.toktape --speed 2
```

## 它是怎么测的

- **预填充是“机器速率加固定开销”，在你的运行之前先测好。** 两个直接发往
  `/completion` 的提示词——一个 128 token，另一个的长度取决于短的那个实际花了
  多少时间——被拟合成每 token 的速率和每个请求的固定开销，期间没有任何其他请求
  在跑。一个大半是固定开销的预填充数字不是吞吐量，而拟合正是用来分清这两者的。
  每次运行的探测提示词都以一段本次专属的盐值开头，所以对同一台已经热起来的
  服务器测两次，第二次仍然是真的在测。
- **提示词集就是测量仪器。** 运行时发出去的不是你敲的提示词，而是固定的 21 篇
  文档——带真实 bug 且测试全过的代码、一份查询计划、事故时间线、一篇 ADR、
  韩文和日文散文——每篇 18–30 KB，故意比任何一台机器应该完整发送的都长。每次
  运行只发每篇的一段前缀，长度由探测的拟合结果决定，使固定开销不超过所测预填充
  的二十分之一，并记下发了多少字符；发布出去的记录会连同前缀一起对照提示词集
  校验。槽位的上下文说了算：回答的额度会被压到“装着这条提示词的槽位实际还装
  得下”的范围内；任何槽位都装不下的提示词，会在第一个请求发出之前就被拒绝，
  并给出两个数字。
- **服务器的数字是记录，客户端的数字是核对。** 每个分块都带着服务器自己的速率和
  token 数；toktape 用自己的时钟重新算一遍，并记下两者是否在 2% 以内一致。
  出现分歧时，绝不会靠“挑那个好看的数”来解决。
- **客户端的速率是在内容窗口上测的**，即第一个和最后一个带文本的 token 之间，
  而不是墙上时间。生成不到 32 个 token 时，这个速率叫 `Sample`，不叫 “decode”。
- **推理 token 也算数。** `reasoning_content` 的增量是解码 token，TTFT 指的是
  任意一种 token 的第一个。
- **cold / warm 来自缺页计数**，从不靠猜；驻留情况从进程自己的映射推导，从不
  直接采信；“从未加载”读自 GGUF 的张量头；每一轮的首尾都会取一次机器见证
  （负载、IO、页缓存、活着的 `llama-*` 进程、cpufreq、一个温度）。
- **未知就打印 `?`。** 卡片从不显示它没有观测到的值，没有缺页测量的运行也绝不会
  被标成 cold。

## 一个值得引用的数字

卡片会给它打印的每个数字加上限定条件。下面这些习惯能让卡片几乎没什么需要限定
的——大多数只是一个参数，甚至不用参数。

- **跑两次，引用第二次。** 对一个刚加载的模型跑第一次时，它一边解码一边还在从
  磁盘读权重，卡片会根据缺页计数把它标成 `cold`。值得引用的是热起来之后的速率。
- **长度用秒来说。** `--for 30s`。同样的 token 数在每台机器上是不同长度的时间
  ——而这恰恰是你录制想要弄清楚的东西——而且一旦指定 `--n-predict`，时钟就
  关掉了，运行会被这个上限截断。
- **推理模型的思考也在计时之内。** 默认的额度可能在答案开始之前就花完了：用
  `--for 60s` 留出思考的时间，或者用 `--no-think` 和非推理模型做同条件对比。
- **引用任何数字之前，先读 caveats 那一行。** 生成太短算不上速率、机器很忙、
  被时钟截断——这些在进入你的帖子之前都已经写在卡片上了，`-o json` 里也有同一份
  列表，还带严重程度。
- **同类才能比。** 提示词集 id、采样设置、端点和引擎构建版本都在卡片上；两张
  卡片要么可比，要么会说明为什么不可比。
- **问题问几路，就开几路。** `--sessions 8` 是八个智能体对服务器做的事；单路流
  回答的是另一个问题。
- **别碰那台机器。** 一个编译任务对解码速度的影响，比人们测试的大多数改动都
  大；每一轮的首尾都会读取见证，地面动了的话，卡片会写上 `contended` 或
  `conditions_changed`。

## 卡片上有什么

每张卡片都是同样的字段放在同样的位置，每一项都是因为它能平息一场争论才在那里：
解码和预填充从不混在一起，排队时间从引擎的工作里单独拆出来
（`engine prefill 10732 ms · queue 22 ms`）；前缀缓存命中率；预填充拟合结果和
本次运行自己的数字并排；每 token 的缺页数；模型实际放在哪里——放置量对驻留量，
权重对 KV 对计算缓冲，来自张量头的“从未加载”字节数；带宽，以真正构成瓶颈的那条
总线为基准，有草稿模型时按每个验证步来算；实际发送的采样参数；草稿的接受率和
步形；完整的启动参数和精确的量化；是否有争用；以及 N × 单路是否真的等于聚合值。

## 命令

| 命令 | 作用 | 示例 |
| --- | --- | --- |
| `record` | 接上服务器并录制一次运行；默认命令 | `toktape --sessions 4 --for 30s` |
| `card` | 从磁带重新渲染卡片 | `toktape card <tape> -o png` |
| `play` | 在实时界面上回放一次运行 | `toktape play <tape> --speed 4` |
| `render` | 把一次运行渲染成 GIF、mp4、asciicast 或 PNG 帧 | `toktape render <tape> --mp4 clip.mp4` |
| `ls` | 列出录过的运行 | `toktape ls` |
| `log` | 所有运行的实验台账 | `toktape log --sort decode` |
| `compare` | 对比两次运行的指标和参数 | `toktape compare a.toktape b.toktape` |
| `publish` | 上传一次运行并打印链接 | `toktape publish <tape>` |
| `profile` | 设置每次发布都带上的作者信息 | `toktape profile --name NAME --link URL --avatar FILE --bio TEXT` |
| `runs` | 列出已发布的运行，筛选和排序方式与网站一致 | `toktape runs --gpu rtx-3090 --sort decode` |
| `show` | 读取一条已发布的运行；`--save` 取回它的记录 | `toktape show <id> --save run.toktape` |
| `reindex` | 从记录重新计算已发布运行的搜索行——新版 toktape 才会索引的那些数字 | `toktape reindex <id>` |
| `version` | 打印版本 | `toktape version` |

录制按时钟结束——默认二十秒，用 `--for 30s` 来调——因为同样的 256 个 token，
在一台机器上是两秒，在另一台上是两分钟；不足 64 个 token 时不会被截断，
`--n-predict` 是同时生效的上限（一旦指定，时钟就关掉）。`--sessions` 超过八路时，
需要用 `--max-sessions` 写上同一个数；`--prompt`（可重复）或 `--prompts`（JSONL
文件，每行一轮）可以替换内置提示词集；`--spec-n-max 3,5` 会对每个投机解码的
`n_max` 各跑一遍提示词集，写进同一盘磁带。

请求怎么构造，对数字的影响不亚于服务器的启动参数：贪心采样、服务器默认采样、
保持思考开启，这三者在同一个引擎上能差出百分之十一，所以 `record` 把这三样都
写明——`--temp`、`--no-think`、`--endpoint chat|completion`，其余该构建版本支持
的参数用 `--param key=value`。发出去的是什么，就记录什么，并写在卡片上。

`-o FORMAT` 就是 llama-bench 的 `-o`，用的也是 llama-bench 的词：`json`、
`jsonl`、`md`（代码围栏里的卡片、一张 llama-bench 表格、一段 Reproduce）、
`csv`、`tsv`、`sql`。`toktape log -o sql | sqlite3 runs.db` 就是一个数据库。
未知值在终端上是 `?`，在导出里是空，在 sql 里是 `NULL`。

每次运行都会追加到磁带旁边的 `runs.tsv`，所以一轮扫参就是一张表；用 `--tag`
和 `--note` 给运行贴标签，`log` 支持 `--sort`、`--model`、`--tag` 和
`--limit N` 把它读回来：

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

视频片段是从磁带渲染的，从来不是录屏，并且按 1:1 播放——除非你要求，否则不做
任何压缩，因为把一次运行加速播放的片段，等于在这个页面唯一关心的那个数字上
撒谎。`--prefill-lead 3s` 让片段从第一个 token 出现前一点点开始：

```sh
toktape render ~/.toktape/runs/<id>.toktape
toktape render ~/.toktape/runs/<id>.toktape --mp4 clip.mp4 --cast clip.cast
```

## 由智能体来调用

跑 toktape 的人多半不会亲手敲命令：敲命令的是 Claude Code 或 Codex。给这位读者的
契约是 [`docs/agents.md`](docs/agents.md) 和二进制里的 `toktape help agents`
——五个退出码；不管运行成功还是失败，`-o json` 都在 stdout 上打印一个对象；
以及[一个值得引用的数字](#一个值得引用的数字)里的那些习惯。读取不花钱
（`toktape runs -o json`）；录制要花。

## 发布

一盘磁带小到可以整个交出去——hero 只有 25 KB——而拿到磁带的页面可以从它画出
其余的一切。[tape.midagedev.com](https://tape.midagedev.com) 做的就是这件事：
一个链接里有卡片、在浏览器里回放的运行、文字记录、mp4，以及记录本身。

```sh
toktape publish ~/.toktape/runs/<id>.toktape --dry-run
toktape publish ~/.toktape/runs/<id>.toktape
```

`--dry-run` 会原样打印将要上传的内容；请读一遍，因为发布出去的运行是**公开**
的，而且默认带着**它的文本**——提示词和模型写出来的东西：一个速率如果不带着
测它时用的文本，只能算半个论断。`--private` 让运行不出现在搜索里，
`--no-text` 把文本留在本地，两者都可以在 `~/.toktape/config.toml` 里设成默认。
`toktape profile` 在每台机器上设置一次作者；`--title`/`--note` 给单次运行附上
实验笔记；`publish --edit <id>` 修改页面上显示的内容。

撤下一条运行只要一条命令：`toktape publish --delete <id>`。有 journal 令牌时，
用的就是你的令牌；匿名上传的**删除令牌**只打印一次，并保存在
`~/.toktape/published.json` 里，所以这条命令在匿名上传时同样有效，运行删掉后
那条记录也随之清除。

站点是一项设置，不是写死的。在 `~/.toktape/config.toml` 里写
`service = "https://tapes.example.com"`（或者用 `TOKTAPE_SERVICE`、`--url`），
所有命令就都指向你自己的服务；那个文件里的令牌只会发给写在它旁边的那个服务。
怎么自己部署，见 [`web/README.md`](web/README.md)。

这个网站是搜索，不是排行榜：最新的排在最前，每一行都带着它的卡片会打印的那些
caveat，可以按模型、量化、引擎、GPU、主机和显存筛选。这些在终端里同样读得到：

```sh
toktape runs --gpu rtx-3090 --sort decode      # what the site lists, as a table
toktape runs --mine -o json                     # your own runs, the API's body verbatim
toktape show <id> --save run.toktape               # one run's summary, and its record
toktape card run.toktape                           # the card, drawn locally from that record
```

无论可见性如何，主机名和绝对路径都会被去掉。服务本身从不打开磁带：比索引行更
丰富的一切内容，都是这个二进制所用的同一个渲染器，编译成 WebAssembly 后在你的
浏览器里运行。

## 常见问题

**为什么不用 `llama-bench`？** 两个都用。`llama-bench` 测的是引擎孤立状态下的
计算能力；toktape 测的是服务器——槽位、前缀缓存复用、排队压力下的 TTFT、八路流
同时到达时会怎样。智能体负载真正碰到的是这些数字。

**我怎么知道提示词没有被缓存？** 卡片上写着：`Prefix cache 0% hit (0/512)`，
还有根据缺页计数得出的 `cold`/`warm`。谁要是怀疑一张卡片，就把磁带发给他；
他会渲染出同一张卡片。

**它会往外发送什么东西吗？** 不会。它只跟你的 llama-server 通信，只在
`~/.toktape` 下写文件。没有遥测，也没有账号。

## 支持范围

| | |
| --- | --- |
| 服务器 | llama-server（上游 llama.cpp）、ik_llama.cpp，以及任何在 `/props` 里返回 `engine` 对象的服务器 |
| Linux | 首要目标，x86_64 和 arm64，完整的 `/proc` 视图 |
| macOS | 可构建、可运行；用 `--url` 接入；没有 `/proc` 视图，所以内存和缺页那几行是 `?` |
| Windows | 独立的二进制，用 `--url` 接入，没有 `/proc` 视图；WSL2 可以跑 Linux 二进制获得完整视图 |
| GPU | 通过 `nvidia-smi` 支持 NVIDIA |

其他兼容 OpenAI 接口的服务器——vLLM、SGLang、TabbyAPI、LM Studio——以通用模式
录制（`--engine-kind openai`，或自动识别）：服务器不返回 timings，所以录制方
自己的时钟就是记录，卡片会在速率旁边写上 `client-timed`，这样的运行只应该和
其他 client-timed 的运行比较。

Ollama（端口 11434）和 LM Studio（1234）不用 `--url` 也能找到；服务器上有多个
模型时，用 `--model <id>` 来选。这类服务器只在流结束时的 `usage` 消息里给出
token 数，而被时钟截断的流收不到这条消息——所以 toktape 会先发两个很短的请求，
量出解码和预填充的速度，再据此决定提示词长度和回答上限，让每一路流都在时钟
之内自己结束。对 vLLM，`max_model_len` 会被当作上下文长度读取，token 数则要求
随每个分块返回。在 Ollama 0.34 和 vLLM 0.29 上实测：默认命令二十秒左右跑完，
卡片上有速率。

## `.toktape` 格式

Gzip 压缩的 JSON，每次运行一个文件，schema 版本 1（也能读未压缩的 JSON）。里面
有摘要、每个 token 的时间戳和文本、采样序列、服务器的原始 timings、它的启动参数
和构建版本，以及放置估算。读取方遇到更新的 schema 会拒绝，而不是去猜。把磁带
分享出去；任何装了 toktape 的人都能从它渲染出同一张卡片。

## 参与贡献

欢迎提 issue 和 pull request；报 bug 时附上磁带最有用，因为一次运行完全由它的
`.toktape` 描述。`./scripts/check.sh` 是门禁——gofmt、构建、vet、Linux 交叉
构建、测试——CI 跑的就是它。目录结构，以及 schema 和卡片遵循的规则，见
[CONTRIBUTING.md](CONTRIBUTING.md)。

## 许可证

MIT。见 [LICENSE](LICENSE)。
