# toktape

**本機 LLM 推論服務的黑盒子磁帶。**

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md) · [简体中文](README.zh-CN.md) · 繁體中文

[![check](https://github.com/midagedev/toktape/actions/workflows/ci.yml/badge.svg)](https://github.com/midagedev/toktape/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/midagedev/toktape)](https://github.com/midagedev/toktape/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/midagedev/toktape)](go.mod)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

<img src="docs/mascot.png" align="right" width="150" alt="toktape 的吉祥物：戴著耳機、閉著眼睛、抱著一卷磁帶的 Q 版小人">

toktape 會接上你已經在跑的 llama-server，把一次執行錄進一個 `.toktape` 檔案，
然後印出一張卡片：模型放在哪裡、行程實際碰過什麼、這次請求到底有多快——
一路串流可以，八路同時也可以。

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape 錄製一個 35B 稀疏 MoE 的四路並行串流，從輸入指令到結果出爐"></p>

<p align="center"><em>這是一次真實的執行，不是示意圖：預設指令加上 <code>--sessions 4</code>，四路串流各以 37.3 tok/s 作答，直到 20 秒的時鐘把它們截斷，全程以 1:1 播放。提示詞是 800 個 token，因為 toktape 先在這台機器上量過「四個預填同時進行」要花多少時間，才決定長度。畫面是 <code>assets/hero.tape</code> 透過 <code>toktape render</code> 所用的同一個算繪器重播出來的——沒有用任何終端機錄影工具；<code>toktape card assets/hero.tape</code> 會從同一個檔案印出下面這張卡片。</em></p>

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

## 為什麼

每一串「X tok/s on Y」的討論最後都會吵到同一個地方：提示詞是不是被快取了、
flash attention 有沒有開、幾路並行、到底是哪個量化、機器上是不是還跑著別的
東西。toktape 用伺服器自己回報的數字，在一張卡片上把這些一次回答完，並且把
這次執行存下來，任何人都能從同一個檔案算繪出同一張卡片。它是一個靜態二進位檔，
MIT 授權，沒有遙測，不需要帳號。

## 安裝

**Homebrew**（macOS 與 Linux）：

```sh
brew install midagedev/tap/toktape
```

**Shell 指令稿**——依你的作業系統和 CPU 下載對應的發行檔，用 `checksums.txt`
驗證，再把一個二進位檔裝到 `~/.local/bin`（用 `TOKTAPE_INSTALL`、
`TOKTAPE_VERSION`、`TOKTAPE_BASE_URL` 調整；`--dry-run` 只看不裝）：

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

**Windows：** 發行頁面上有 zip，把 `toktape.exe` 解壓縮到 `PATH` 裡的任何位置即可。
**Go：** `go install github.com/midagedev/toktape/cmd/toktape@latest`。
**從原始碼建置**（Go 1.26，不需要 cgo）：

```sh
git clone https://github.com/midagedev/toktape.git
cd toktape
go build -o toktape ./cmd/toktape
```

用 `toktape version` 確認。二進位檔在執行時不會解析任何外部相依，所以用 `scp`
複製到伺服器所在的機器上也算安裝。Linux 是首要目標——記憶體、分頁錯誤和啟動
參數這幾列讀自 `/proc`。macOS 和 Windows 可以建置，並用 `--url` 接上遠端
伺服器；那幾列在這兩個系統上會印出 `?`，想要完整檢視可以在 WSL2 裡跑 Linux
二進位檔。GPU 那幾列來自 `nvidia-smi`。

## 快速上手

在跑 llama-server 的那台機器上輸入一個字：

```sh
toktape
```

它會自己找到伺服器（`127.0.0.1:8080`，接著是 `:8081`、`:8000`、`:5000`），
拿模型路徑去比對 `/proc/*/cmdline` 來認出行程，從內建的提示詞集送出請求——
21 篇長文件，各自裁成這台機器實測的預填速度負擔得起的前綴——一邊以串流接收
回答，一邊取樣分頁錯誤、RSS 和 GPU 狀態，寫出
`~/.toktape/runs/<id>.toktape`，最後印出卡片，以及告訴你怎麼分享它的那一行。
不用給連接埠，不用給 PID，不用任何參數；模型還在載入的話它會等（`--wait`，
預設十分鐘）。

**八路同時**，這正是代理（agent）工作負載對伺服器做的事——N 越大，每路的
tok/s 越低，這本身就是結論；而合計值回答的是「這台機器能不能同時服務八個
代理」：

```sh
toktape --sessions 8
```

**即時觀看**，每路串流一格，外加一格機器狀態：

```sh
toktape --sessions 4 --tui
toktape --sessions 8 --tui --grid 2x2     # four tiles per page, ←/→ to page
```

**分享，或者重播：**

```sh
toktape card ~/.toktape/runs/<id>.toktape -o png          # 1200×675 image next to the tape
toktape card ~/.toktape/runs/<id>.toktape -o md --copy    # card + llama-bench table, on the clipboard
toktape render                                          # the newest run as a GIF
```

```sh
toktape play ~/.toktape/runs/<id>.toktape --speed 2
```

## 它是怎麼量的

- **預填是「機器速率加固定成本」，在你的執行之前就先量好。** 兩個直接送往
  `/completion` 的提示詞——一個 128 token，另一個的長度取決於短的那個實際花了
  多少時間——被擬合成每 token 的速率和每個請求的固定成本，期間沒有任何其他請求
  在跑。一個大半是固定成本的預填數字不是吞吐量，而擬合正是用來分清這兩者的。
  每次執行的探測提示詞都以一段本次專屬的鹽值開頭，所以對同一台已經熱起來的
  伺服器量兩次，第二次仍然是真的在量。
- **提示詞集就是量測儀器。** 執行時送出去的不是你打的提示詞，而是固定的 21 篇
  文件——帶有真實 bug 而且測試全過的程式碼、一份查詢計畫、事故時間軸、一篇
  ADR、韓文和日文散文——每篇 18–30 KB，刻意比任何一台機器應該完整送出的都長。
  每次執行只送每篇的一段前綴，長度由探測的擬合結果決定，讓固定成本不超過所量
  預填的二十分之一，並記下送了多少字元；發布出去的紀錄會連同前綴一起對照提示詞
  集驗證。插槽的上下文說了算：回答的額度會被壓到「裝著這則提示詞的插槽實際還
  裝得下」的範圍內；任何插槽都裝不下的提示詞，會在第一個請求送出之前就被拒絕，
  並附上兩個數字。
- **伺服器的數字是紀錄，用戶端的數字是核對。** 每個區塊都帶著伺服器自己的速率和
  token 數；toktape 用自己的時鐘重算一遍，並記下兩者是否在 2% 以內一致。
  出現分歧時，絕不會靠「挑那個好看的數字」來解決。
- **用戶端的速率是在內容視窗上量的**，也就是第一個和最後一個帶文字的 token
  之間，而不是掛鐘時間。產生不到 32 個 token 時，這個速率叫 `Sample`，不叫
  「decode」。
- **推理 token 也算數。** `reasoning_content` 的增量是解碼 token，TTFT 指的是
  任一種 token 的第一個。
- **cold / warm 來自分頁錯誤計數**，從不用猜的；常駐情況從行程自己的對映推導，
  從不直接採信；「從未載入」讀自 GGUF 的張量標頭；每一輪的頭尾都會取一次機器
  見證（負載、IO、分頁快取、活著的 `llama-*` 行程、cpufreq、一個溫度）。
- **未知就印 `?`。** 卡片從不顯示它沒有觀測到的值，沒有分頁錯誤量測的執行也絕
  不會被標成 cold。

## 一個值得引用的數字

卡片會替它印出的每個數字加上限定條件。下面這些習慣能讓卡片幾乎沒什麼需要限定
的——大多數只是一個參數，甚至不用參數。

- **跑兩次，引用第二次。** 對一個剛載入的模型跑第一次時，它一邊解碼一邊還在從
  磁碟讀權重，卡片會依分頁錯誤計數把它標成 `cold`。值得引用的是熱起來之後的
  速率。
- **長度用秒來說。** `--for 30s`。同樣的 token 數在每台機器上是不同長度的時間
  ——而這正是你錄製想要弄清楚的事——而且一旦指定 `--n-predict`，時鐘就關掉了，
  執行會被這個上限截斷。
- **推理模型的思考也算在計時之內。** 預設的額度可能在答案開始之前就花完了：用
  `--for 60s` 留出思考的時間，或者用 `--no-think` 和非推理模型做同條件比較。
- **引用任何數字之前，先讀 caveats 那一行。** 產生得太短算不上速率、機器很忙、
  被時鐘截斷——這些在進到你的貼文之前都已經寫在卡片上了，`-o json` 裡也有同一份
  清單，還附嚴重程度。
- **同類才能比。** 提示詞集 id、取樣設定、端點和引擎建置版本都在卡片上；兩張
  卡片要嘛可以比，要嘛會說明為什麼不能比。
- **問題問幾路，就開幾路。** `--sessions 8` 是八個代理對伺服器做的事；單路串流
  回答的是另一個問題。
- **別動那台機器。** 一個編譯工作對解碼速度的影響，比大家測試的多數改動都大；
  每一輪的頭尾都會讀取見證，地面動了的話，卡片會寫上 `contended` 或
  `conditions_changed`。

## 卡片上有什麼

每張卡片都是同樣的欄位放在同樣的位置，每一項都是因為它能平息一場爭論才在那裡：
解碼和預填從不混在一起，排隊時間從引擎的工作裡單獨拆出來
（`engine prefill 10732 ms · queue 22 ms`）；前綴快取命中率；預填擬合結果和
本次執行自己的數字並列；每 token 的分頁錯誤數；模型實際放在哪裡——配置量對
常駐量，權重對 KV 對運算緩衝，來自張量標頭的「從未載入」位元組數；頻寬，以
真正構成瓶頸的那條匯流排為基準，有草稿模型時按每個驗證步來算；實際送出的取樣
參數；草稿的接受率和步形；完整的啟動參數和精確的量化；是否有爭用；以及
N × 單路是否真的等於合計值。

## 指令

| 指令 | 作用 | 範例 |
| --- | --- | --- |
| `record` | 接上伺服器並錄製一次執行；預設指令 | `toktape --sessions 4 --for 30s` |
| `card` | 從磁帶重新算繪卡片 | `toktape card <tape> -o png` |
| `play` | 在即時畫面上重播一次執行 | `toktape play <tape> --speed 4` |
| `render` | 把一次執行算繪成 GIF、mp4、asciicast 或 PNG 影格 | `toktape render <tape> --mp4 clip.mp4` |
| `ls` | 列出錄過的執行 | `toktape ls` |
| `log` | 所有執行的實驗紀錄簿 | `toktape log --sort decode` |
| `compare` | 比較兩次執行的指標和參數 | `toktape compare a.toktape b.toktape` |
| `publish` | 上傳一次執行並印出連結 | `toktape publish <tape>` |
| `profile` | 設定每次發布都會帶上的作者資訊 | `toktape profile --name NAME --link URL --avatar FILE --bio TEXT` |
| `runs` | 列出已發布的執行，篩選和排序方式與網站一致 | `toktape runs --gpu rtx-3090 --sort decode` |
| `show` | 讀取一筆已發布的執行；`--save` 取回它的紀錄 | `toktape show <id> --save run.toktape` |
| `reindex` | 從紀錄重新計算已發布執行的搜尋列——新版 toktape 才會建立索引的那些數字 | `toktape reindex <id>` |
| `version` | 印出版本 | `toktape version` |

錄製依時鐘結束——預設二十秒，用 `--for 30s` 來調——因為同樣的 256 個 token，
在一台機器上是兩秒，在另一台上是兩分鐘；不到 64 個 token 時不會被截斷，
`--n-predict` 是同時生效的上限（一旦指定，時鐘就關掉）。`--sessions` 超過八路時，
需要用 `--max-sessions` 寫上同一個數字；`--prompt`（可重複）或 `--prompts`
（JSONL 檔，每行一輪）可以取代內建提示詞集；`--spec-n-max 3,5` 會對每個推測解碼
的 `n_max` 各跑一遍提示詞集，寫進同一卷磁帶。

請求怎麼組，對數字的影響不亞於伺服器的啟動參數：貪婪取樣、伺服器預設取樣、
保持思考開啟，這三者在同一個引擎上可以差到百分之十一，所以 `record` 把這三樣
都寫明——`--temp`、`--no-think`、`--endpoint chat|completion`，其餘該建置版本
支援的參數用 `--param key=value`。送出去的是什麼，就記錄什麼，並寫在卡片上。

`-o FORMAT` 就是 llama-bench 的 `-o`，用的也是 llama-bench 的詞：`json`、
`jsonl`、`md`（程式碼區塊裡的卡片、一張 llama-bench 表格、一段 Reproduce）、
`csv`、`tsv`、`sql`。`toktape log -o sql | sqlite3 runs.db` 就是一個資料庫。
未知值在終端機上是 `?`，在匯出裡是空的，在 sql 裡是 `NULL`。

每次執行都會附加到磁帶旁邊的 `runs.tsv`，所以一輪參數掃描就是一張表；用
`--tag` 和 `--note` 替執行加上標籤，`log` 支援 `--sort`、`--model`、`--tag` 和
`--limit N` 把它讀回來：

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

影片片段是從磁帶算繪的，從來不是螢幕錄影，而且以 1:1 播放——除非你要求，否則
不做任何壓縮，因為把一次執行加速播放的片段，等於在這個頁面唯一在乎的那個數字上
說謊。`--prefill-lead 3s` 讓片段從第一個 token 出現前一點點開始：

```sh
toktape render ~/.toktape/runs/<id>.toktape
toktape render ~/.toktape/runs/<id>.toktape --mp4 clip.mp4 --cast clip.cast
```

## 由代理來呼叫

跑 toktape 的人多半不會親手打指令：打指令的是 Claude Code 或 Codex。給這位讀者的
契約是 [`docs/agents.md`](docs/agents.md) 和二進位檔裡的 `toktape help agents`
——五個結束代碼；不管執行成功還是失敗，`-o json` 都在 stdout 上印出一個物件；
以及[一個值得引用的數字](#一個值得引用的數字)裡的那些習慣。讀取不用成本
（`toktape runs -o json`）；錄製要。

## 發布

一卷磁帶小到可以整個交出去——hero 只有 25 KB——而拿到磁帶的頁面可以從它畫出
其餘的一切。[tape.midagedev.com](https://tape.midagedev.com) 做的就是這件事：
一個連結裡有卡片、在瀏覽器裡重播的執行、逐字紀錄、mp4，以及紀錄本身。

```sh
toktape publish ~/.toktape/runs/<id>.toktape --dry-run
toktape publish ~/.toktape/runs/<id>.toktape
```

`--dry-run` 會原樣印出即將上傳的內容；請讀一遍，因為發布出去的執行是**公開**
的，而且預設帶著**它的文字**——提示詞和模型寫出來的東西：一個速率如果不帶著
量它時用的文字，只能算半個主張。`--private` 讓執行不出現在搜尋裡，
`--no-text` 把文字留在本機，兩者都可以在 `~/.toktape/config.toml` 裡設成預設。
`toktape profile` 在每台機器上設定一次作者；`--title`/`--note` 替單次執行附上
實驗筆記；`publish --edit <id>` 修改頁面上顯示的內容。

撤下一筆執行只要一道指令：`toktape publish --delete <id>`。有 journal 權杖時，
用的就是你的權杖；匿名上傳的**刪除權杖**只會印出一次，並存在
`~/.toktape/published.json` 裡，所以這道指令在匿名上傳時同樣有效，執行刪掉後
那筆紀錄也會一併清除。

站台是一項設定，不是寫死的。在 `~/.toktape/config.toml` 裡寫
`service = "https://tapes.example.com"`（或者用 `TOKTAPE_SERVICE`、`--url`），
所有指令就都指向你自己的服務；那個檔案裡的權杖只會送給寫在它旁邊的那個服務。
怎麼自己架設，見 [`web/README.md`](web/README.md)。

這個網站是搜尋，不是排行榜：最新的排最前面，每一列都帶著它的卡片會印出的那些
caveat，可以依模型、量化、引擎、GPU、主機和 VRAM 篩選。這些在終端機裡同樣讀得到：

```sh
toktape runs --gpu rtx-3090 --sort decode      # what the site lists, as a table
toktape runs --mine -o json                     # your own runs, the API's body verbatim
toktape show <id> --save run.toktape               # one run's summary, and its record
toktape card run.toktape                           # the card, drawn locally from that record
```

無論可見性如何，主機名稱和絕對路徑都會被移除。服務本身從不打開磁帶：比索引列
更豐富的一切內容，都是這個二進位檔所用的同一個算繪器，編譯成 WebAssembly 後在
你的瀏覽器裡執行。

## 常見問題

**為什麼不用 `llama-bench`？** 兩個都用。`llama-bench` 量的是引擎在隔離狀態下的
運算能力；toktape 量的是伺服器——插槽、前綴快取重用、排隊壓力下的 TTFT、八路
串流同時到達時會怎樣。代理工作負載真正碰到的是這些數字。

**我怎麼知道提示詞沒有被快取？** 卡片上寫著：`Prefix cache 0% hit (0/512)`，
還有依分頁錯誤計數得出的 `cold`/`warm`。誰要是懷疑一張卡片，就把磁帶寄給他；
他會算繪出同一張卡片。

**它會往外傳送什麼東西嗎？** 不會。它只跟你的 llama-server 通訊，只在
`~/.toktape` 底下寫檔案。沒有遙測，也沒有帳號。

## 支援範圍

| | |
| --- | --- |
| 伺服器 | llama-server（上游 llama.cpp）、ik_llama.cpp，以及任何在 `/props` 裡回傳 `engine` 物件的伺服器 |
| Linux | 首要目標，x86_64 和 arm64，完整的 `/proc` 檢視 |
| macOS | 可建置、可執行；用 `--url` 接入；沒有 `/proc` 檢視，所以記憶體和分頁錯誤那幾列是 `?` |
| Windows | 獨立的二進位檔。伺服器探索和 `nvidia-smi` 的 GPU 檢視與別處一樣可用，所以 `--url` 只用於探索不會探測的連接埠；沒有 `/proc` 檢視，記憶體和缺頁那幾列是 `?`；WSL2 可以跑 Linux 二進位檔取得完整檢視 |
| GPU | 透過 `nvidia-smi` 支援 NVIDIA |

其他相容 OpenAI 介面的伺服器——vLLM、SGLang、TabbyAPI、LM Studio——以通用模式
錄製（`--engine-kind openai`，或自動辨識）：伺服器不回傳 timings，所以錄製端
自己的時鐘就是紀錄，卡片會在速率旁邊寫上 `client-timed`，這樣的執行只應該和
其他 client-timed 的執行比較。

Ollama（連接埠 11434）和 LM Studio（1234）不用 `--url` 也找得到；伺服器上有多個
模型時，用 `--model <id>` 來選。這類伺服器只在串流結束時的 `usage` 訊息裡給出
token 數，而被時鐘截斷的串流收不到這則訊息——所以 toktape 會先送兩個很短的
請求，量出解碼和預填的速度，再據此決定提示詞長度和回答上限，讓每一路串流都在
時鐘之內自己結束。對 vLLM，`max_model_len` 會被當作上下文長度讀取，token 數則
要求隨每個區塊回傳。在 Ollama 0.34 和 vLLM 0.29 上實測：預設指令二十秒左右
跑完，卡片上有速率。

## `.toktape` 格式

Gzip 壓縮的 JSON，每次執行一個檔案，schema 版本 1（也能讀未壓縮的 JSON）。裡面
有摘要、每個 token 的時間戳記和文字、取樣序列、伺服器的原始 timings、它的啟動
參數和建置版本，以及配置估算。讀取端遇到較新的 schema 會拒絕，而不是用猜的。
把磁帶分享出去；任何裝了 toktape 的人都能從它算繪出同一張卡片。

## 參與貢獻

歡迎提 issue 和 pull request；回報 bug 時附上磁帶最有用，因為一次執行完全由它的
`.toktape` 描述。`./scripts/check.sh` 是把關的門檻——gofmt、建置、vet、Linux
交叉建置、測試——CI 跑的就是它。目錄結構，以及 schema 和卡片遵循的規則，見
[CONTRIBUTING.md](CONTRIBUTING.md)。

## 授權

MIT。見 [LICENSE](LICENSE)。
