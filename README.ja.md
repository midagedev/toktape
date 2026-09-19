# toktape

**ローカル LLM サービングのためのブラックボックス・テープ。**

[English](README.md) · [한국어](README.ko.md) · 日本語

[![check](https://github.com/midagedev/toktape/actions/workflows/ci.yml/badge.svg)](https://github.com/midagedev/toktape/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/midagedev/toktape)](https://github.com/midagedev/toktape/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/midagedev/toktape)](go.mod)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

<img src="docs/mascot.png" align="right" width="150" alt="the toktape mascot: a small chibi in headphones, eyes closed, hugging a cassette tape">

toktape は、すでに起動している llama-server にアタッチし、1 回の実行を
`.toktape` ファイルに記録して、カードを 1 枚出力します。モデルがどこに載っているか、
プロセスが実際に何に触れたか、リクエストが本当にどれだけ速かったか。
それが 1 枚に収まります。1 ストリームでも同時 8 ストリームでも、記録の仕方は
同じです。

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape recording four concurrent streams of a 35B sparse MoE, from the command being typed to the result"></p>

<p align="center"><em>演出ではなく実際の実行です。Qwen3.6-35B-A3B UD-Q6_K、27.3 GiB のスパース MoE を RTX A6000 1 枚に丸ごと載せています。プロンプトにコマンドを打ち、サーバーを見つけてアタッチし、4 ストリームがそれぞれ別のコードレビュー課題に 42.1 tok/s で同時に答え、結果が出ます。各ストリームはモデルが言い終えた時点で止まります。152 トークンから 279 トークンの間で、トークン上限に切られたストリームはありません。カードの合計値 144 tok/s が 42.1 の 4 倍にならないのも同じ理由です。合計はデコード区間の全体で測っていて、その区間の最後の 2 秒にはストリームが 1 本しか残っていません。クリップは最初のトークンの 3 秒前から始まります（<code>--prefill-lead 3s</code>）。プリフィルを待った残りの時間は、テープと、クリップが開いた時点ですでに画面に出ている時計のほうに残っています。見えているものはすべて 1:1 です。<code>assets/hero.tape</code> を <code>toktape render</code> と同じレンダラーで描き直したもので、下のカードは同じファイルから <code>toktape card assets/hero.tape</code> で出ます。</em></p>

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
│   the streams did not all decode across the same window · recorded   │
├──────────────────────────────────────────────────────────────────────┤
│                toktape · github.com/midagedev/toktape                │
└──────────────────────────────────────────────────────────────────────┘
```

## なぜ作ったか

「この構成でこのモデルが何 tok/s」という投稿は、いつも同じ返信で終わります。
プロンプトはキャッシュされていなかったか、Flash Attention は有効か、同時
何ストリームか、量子化は正確には何か、そのとき他のプロセスは動いていなかったか。
toktape はそのすべてに、サーバー自身が報告した数値でカード 1 枚で答え、実行そのもの
をファイルとして残します。同じファイルからは誰でも同じカードを描き直せます。
静的バイナリ 1 つ、MIT ライセンス、テレメトリなし、アカウント不要です。

## インストール

**Homebrew**（macOS・Linux）:

```sh
brew install midagedev/tap/toktape
```

**シェルスクリプト。** OS と CPU に合ったリリースアーカイブを取得し、
`checksums.txt` で検証してから、バイナリ 1 つを `~/.local/bin` に置きます。

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

インストール先は `TOKTAPE_INSTALL`、バージョン固定は `TOKTAPE_VERSION` で
指定します。GitHub に出られないマシンでは `TOKTAPE_BASE_URL` にミラーか
`file://` ディレクトリを指定してください。`sh` の後ろに `-s -- --dry-run` を
付けると、何をするかだけを表示して実際にはインストールしません。

**Windows。** リリースページに `toktape_<version>_windows_amd64.zip` と
arm64 版の zip が並びます。`toktape.exe` を展開して `PATH` に置いてください。
上のシェルスクリプトは POSIX 専用で、この zip は取得しません。

**Go:**

```sh
go install github.com/midagedev/toktape/cmd/toktape@latest
```

**ソースからビルド**（Go 1.26、cgo なし）:

```sh
git clone https://github.com/midagedev/toktape.git
cd toktape
go build -o toktape ./cmd/toktape
```

`toktape version` でインストールを確認できます。実行時に解決すべき依存が
何もないバイナリなので、サーバーが動くマシンへ `scp` するのも正規の
インストール方法です。

**プラットフォーム。** Linux の x86_64・arm64 が主要ターゲットです。メモリ、
ページフォールト、サーバーフラグの行は `/proc` から読みますが、これはサーバーが
動いているホストにしかありません。macOS でもビルド・実行でき、`--url` で
リモートサーバーにアタッチします。その場合 `/proc` の行は `?` になります。
Windows は v0.2.5 から専用のバイナリを持ち、見えるものは macOS と同じです。
`/proc` ビューまで必要なら WSL2 で Linux バイナリを使います。GPU の行は
`nvidia-smi` から読みます。

## まず使ってみる

llama-server が動いているマシンで、一語だけ打ちます。

```sh
toktape
```

1. **探索** — `127.0.0.1:8080`、続いて `:8081`、`:8000`、`:5000` を叩き、
   `/props` に応答した最初のサーバーを選びます。
2. **アタッチ** — `/props` からモデルパス、コンテキスト長、スロット数を読み、
   そのパスを `/proc/*/cmdline` と照合してサーバーの PID を見つけ、`/proc`
   ビューを開きます。
3. **プロンプト** — 固定プロンプト集から 1 リクエストを `timings_per_token` と
   `return_progress` を有効にして送り、応答をストリーミングしながらメジャー
   フォールト、RSS、GPU 状態をサンプリングします。
4. **テープ** — 実行全体を `~/.toktape/runs/<id>.toktape` に書き出します。
5. **カード** — 72 桁のカードを出力し、テープの隣に保存して、共有方法を 1 行で
   示します。

ポートも PID も、覚えるべきフラグもありません。モデルがまだロード中なら待ちます
（`--wait`、既定は 10 分）。

**同時 8 ストリーム。** エージェントのワークロードがサーバーに与える負荷は
まさにこれです。

```sh
toktape --sessions 8
```

N が増えるとストリームあたりの tok/s が下がるのは正常です。「このマシンは
エージェント 8 体を捌けるか」に答える数字は合計値で、単一ストリームのベンチ
マークではこの数字は見えません。

**リアルタイムで見る。** ストリームごとにタイル 1 枚、右側にマシンのペインです。
回答の中のフェンス付きコードブロックは、届いたそばから形が整います。キーワードは
太さを得て、コメントと記号は一段下がる。画面に新しい色をひとつも足さずに、コードが
コードとして読めます。

```sh
toktape --sessions 4 --tui
toktape --sessions 8 --tui --grid 2x2     # four tiles per page, ←/→ to page
```

**共有する:**

```sh
toktape card ~/.toktape/runs/<id>.toktape -o png          # 1200×675 image next to the tape
toktape card ~/.toktape/runs/<id>.toktape -o md --copy    # card + llama-bench table, on the clipboard
toktape render                                          # the newest run as a GIF
```

**リプレイする。** ライブ画面で、好きな速度で再生します。

```sh
toktape play ~/.toktape/runs/<id>.toktape --speed 2
```

## カードに載るもの

すべてのカードで、同じ項目が同じ位置にあります。2 枚を並べて読めるように
するためです。各項目は、ひとつの議論を終わらせるために置かれています。

- **その数字を引用してよいかどうか。** どのカードも、引用できない理由の一覧で
  終わります。`! 3 caveats — cold run: weights arrived from disk while it
  decoded, 4.4 maj faults/token · conditions_changed · run_cut_by_clock`。
  いちばん重いものだけを文章にし、残りはコードで並べます。あとで引くときに手に
  取るのがコードだからです。`-o json` には同じ一覧が `caveats` として入り、
  項目ごとに深刻度が付きます。`figure` は数字ひとつが影響を受けたという意味、
  `run` は実行そのものが、`view` はカードが見るべきものを見られなかったという
  意味です。速度をどこかに書き写す前に読むフィールドは、これひとつです。
- **デコードとプリフィルを混ぜない。** プリフィルは演算律速、デコードはメモリ
  帯域律速なので、「45 tok/s」ひとつでは何もわかりません。TTFT、プロンプト
  tok/s、デコード tok/s を、それぞれのトークン数と一緒に別々に表示します。
  プリフィルの測定と呼ぶには短すぎるプロンプトは、平均に混ぜずに短いと書きます
  — `30 prompt tokens — not a prefill measurement`。同時実行では、空きスロットを
  待った時間をエンジン自身の仕事から切り離して
  `engine prefill 10732 ms · queue 22 ms` と並べます。待ち行列が遅いモデルとして
  読まれないように。
- **プレフィックスキャッシュのヒット率。** `0% hit (0/512)` または
  `78% hit (400/512)`。システムプロンプトが 1 文字違うだけでキャッシュを外し、
  プリフィルが理由もなく 10 倍遅く、あるいは速く見えます。
- **cold / warm。** mmap されたモデルへの最初のプロンプトは、NVMe から 4 KiB
  ページを数千枚引き上げるため、ウォーム時の何分の一かの速度しか出ません。この
  ラベルは推測ではなく、デコード中に実際に起きたメジャーフォールト数から決めます。
- **トークンあたりページフォールト。** 「止まって、また動く」を説明する唯一の
  数字です。ライブ画面ではトークンと同じ時間軸に描かれます。
- **RSS は「ロード済み」ではない。** mmap の下では、常駐サイズはプロセスが触れた
  分だけです。カードはホスト RSS を file/anon に、VRAM を weights/KV/compute に
  分け、未ロードのバイト数を「ファイルサイズ − RSS」ではなく GGUF テンソル
  ヘッダから計算します。
- **CPU に置かれたことと RAM にあることは別です。** `-ot ... exps=CPU` は
  バックエンドの割り当てであって常駐の話ではありません。上のカードはホストに
  394 GiB を割り当てていますが、RAM にあるのはそのうち 201 GiB だけで、残りの
  193 GiB はデコードのあいだずっとディスクから読み直されています。
  `Host placed 394.1 GiB (200.8 in RAM / 193.3 on disk)` がその境目を言い、
  ライブ画面では「ない側」が警告色で塗られて、すぐ下のフォールト数とつながります。
- **バイトが実際に渡ったバスで測ります。** VRAM とホスト RAM に分かれたモデルに
  単一の帯域幅はありません。三つのバスのトラフィックを足すと、ホストバスの上限が
  116 GB/s のマシンで `≈ 155 GB/s` が出ます。カードは壁になっている側を名指しして
  `≈ 115 GB/s from RAM per verify step, 99% of peak` と書き、配置がホストの
  読み出し量を証明できるときだけ割合を添えます。ドラフトモデルがあるとき、
  1 ステップは 1 トークンではありません。ターゲットは一度にバッチを検証するので、
  バイト数は verify step 単位で数えます。それがバスの実際に見た単位です。
- **リクエストが何を求めたか。** greedy、サーバー既定のサンプリング、思考させた
  ままの推論モデル — 同じエンジンで 11 % の開きが出ます。
  `Sampling  temp default · chat` がその速度はどれのものかを言い、送っていない
  温度を作り出しません。
- **ドラフトがあったならその内訳。** モデル、ブロックサイズ、ターゲットが同意した
  割合を分母つきで: `n_max 3 · 52% accepted (260/504)`。そしてその受理率が作った
  仕事のかたちも: `170 verify steps of 4.0 tokens`。
- **条件が変わったなら。** ラウンドの開始と終了ごとにクロック上限と CPU 温度を
  読みます。実行中にウォッチドッグが上限を下げたら、カードがそう書きます。
  説明のつかない遅い数字を残しません。
- **フラグはすべて。** `-ngl -fa -b -ub -ctk -ctv --load-mode -ot`。Flash
  Attention の有無とバッチサイズ、この 2 つが抜けると結果の投稿がコメント 50 件の
  スレッドになります。
- **正確な量子化。** `UD-Q4_K_M` を「Q4」と縮めません。
- **contended。** 同じマシンの別プロセスは、人が試す大半の変更よりも大きく
  デコード速度を動かします。カードはロードアベレージと他の GPU プロセスを読み、
  実行にラベルを付けます。
- **ストリーム。** 何本あったか、それぞれが最初のトークンを見たのはいつか
  （TTFT p50・p95）、同時にビジーだった最大スロット数。速度は Decode 行のもので
  ここには繰り返しません。ただし「ストリームあたり × N が本当に合計なのか」だけは
  はっきり書きます。終わる時刻がばらけたストリームは、最後の一本しか残っていない
  区間まで含めて合計を測ることになるので、その行は
  `4 streams · not all decoding at once` と読めます。

## コマンド

| 動詞 | 役割 | 例 |
| --- | --- | --- |
| `record` | アタッチして実行を記録する。既定の動詞 | `toktape --sessions 4 --for 30s` |
| `card` | テープからカードを描き直す | `toktape card <tape> -o png` |
| `play` | ライブ画面で実行をリプレイする | `toktape play <tape> --speed 4` |
| `render` | GIF、mp4、asciicast、PNG フレームとして書き出す | `toktape render <tape> --mp4 clip.mp4` |
| `ls` | 記録した実行の一覧 | `toktape ls` |
| `log` | 全実行の実験台帳 | `toktape log --sort decode` |
| `compare` | 2 つの実行の指標とフラグを比較する | `toktape compare a.toktape b.toktape` |
| `publish` | 実行をアップロードしてリンクを出す | `toktape publish <tape>` |
| `profile` | 公開のたびに付く作者情報を決める | `toktape profile --name NAME --link URL --avatar FILE --bio TEXT` |
| `runs` | 公開済みの実行を一覧する。サイトと同じフィルタと順序 | `toktape runs --gpu rtx-3090 --sort decode` |
| `show` | 公開済みの実行をひとつ読む。`--save` で記録も取る | `toktape show <id> --save run.toktape` |
| `reindex` | 公開済みの実行の検索行を記録から作り直す。新しい toktape が索引する数値が古い実行にも付く | `toktape reindex <id>` |
| `version` | バージョンを表示する | `toktape version` |

**1 回の記録は何秒か。** 実行は時計で終わります。既定は 20 秒で、
`--for 30s` のように長さを直接指定できます。この問いにトークン数は
単位として合いません。同じ 256 トークンが、速い GPU に載せた 7B では
2 秒もかからず、GPU なしの大きなモデルでは 2 分を超えます。だから秒を
指定し、`--n-predict` は一緒に効く上限として残ります。`--n-predict` を
自分で指定するのは同じ問いへの別の答えなので、指定した時点で時計は
切れます。正直に添えておくことが二つ。64 トークンを下回る間は切りません
——遅いマシンは頼んだより長く回りますが、代わりにサンプルを渡すことは
しません。そして多くの場合、予算より先にモデルのほうが止まります。それは
マシンではなくプロンプトの仕事です。

**record:** `--for DURATION`（既定 `20s`。`--for 0` で時計を切る）、
`--url`（既定は自動探索）、`--sessions N`（同時に送るストリーム数、既定 1。
8 を超えるには `--max-sessions` に同じ数をもう一度書く必要があり、サーバーの
スロット数より多いと断る）、`--prompt`（複数指定可、`--sessions` 分まで循環）、
`--prompts`（JSONL ファイル。1 行が `--sessions` 本の
ストリームからなる 1 ラウンドで、順番に 1 本のテープへ記録）、`--spec-n-max LIST`（例: `3,5`。プロンプト一式を
speculative `n_max` の値ごとに 1 回ずつ流して同じテープに記録し、カードには値ごとの行が
付く）、`-n`/`--n-predict`（ストリームごとのトークン上限。llama-bench の `-n` と
同じ意味で、指定すると時計が切れる）、`--out`（既定
`~/.toktape/runs`）、`--tag`、`--note`、`--wait`、`--tui`、
`--grid COLSxROWS`（既定 `2x4`、`0` で端末に合わせる）、`--no-card`、
`-o FORMAT`、`--quiet`。

**サンプリングとエンドポイント。** リクエストの形は、サーバーを起動したときの
フラグと同じくらい数値を動かします。同じ推論モデル、同じ 20 個のプロンプト、同じ
マシンで、raw エンドポイントに greedy で送ると 25.6 tok/s、同じエンドポイントで
サーバー既定のサンプリングだと 24.5、チャットエンドポイントで thinking を有効に
したままだと 22.4 でした。そこで `record` は三つとも名前を付けます。`--temp N` は
サンプリング温度（`--temp 0` が greedy。指定しなければサーバー自身の既定がそのまま
効きます）、`--no-think` はエンジンの `enable_thinking` スイッチを送って推論モデルに
考えないよう頼み、`--endpoint chat|completion` はテンプレートを適用するチャット経路と
プロンプトをそのまま `/completion` に送る経路を選び、`--param key=value`（繰り返し
可）はそのビルドが受け付ける残りをそのまま載せます — `--param seed=7`、
`--param top_k=40`、`--param cache_prompt=false`。値は JSON として読めれば JSON、
そうでなければ文字列として送ります。`--no-think` はチャット側の設定です。thinking は
テンプレートのものであり、raw プロンプトにテンプレートはありません。送ったものは
テープにそのまま残り、カードに名前が出るので、二枚のカードは比較できるか、なぜ
比較できないかを語ります。

**出力形式。** `-o FORMAT`（または `--output FORMAT`）は llama-bench の `-o` で、
形式の名前も llama-bench の言葉そのままです。`record` と `card` は `json`（実行
サマリー）、`jsonl`（同じオブジェクトを 1 行で。複数の実行を 1 つのファイルに
追記できます）、`md`、そして `csv`・`tsv`・`sql`（1 回の実行を台帳の 1 行として）を
出し、`log` はすべての実行を同じ 6 形式で出します。`md` だけは llama-bench と意味が
違います。カードではフェンス内のカード、llama-bench 互換の表、Reproduce ブロックで、
`log` では台帳をそのまま移した Markdown の表です。`sql` は `runs` テーブルがなければ
作り、実行ごとに 1 行ずつ入れるので、`toktape log -o sql | sqlite3 runs.db` の
1 行でデータベースになります。動詞が受け付けない形式を渡すと断り、受け付ける形式を
示します。

**card:** 上の形式に加えて `-o png [FILE]`（1200×675 の共有画像。ファイルを指定
しなければテープの隣に書きます）、`--copy`（クリップボードにもコピー。pbcopy・
wl-copy・xclip があればそれで、SSH 越しなら端末への OSC 52 で）。

**render:** `--gif FILE`、`--mp4 FILE`（`PATH` に ffmpeg が必要）、`--cast FILE`
（asciicast v2）、`--frames DIR`（PNG シーケンス。GIF が 13 px セルのところを 20 px
セルで描くので、GIF と同じキャンバスが要るなら `--font-size 13` を。✓ の各行に
描いたピクセルサイズが出ます）、`--duration`、`--fps`、`--size WxH`、`--open`（実行の前に、シェルプロンプトでコマンドを打つ場面を
付けます）、`--prefill-lead D`（実行の先頭ではなく最初のトークンの D 秒前から
クリップを始め、プリフィルを待った残りをクリップの外に置きます。残ったフレーム
はすべてそのまま 1:1 で、画面の時計が切った地点から始まるので、断りは要りません）。
複数の出力を一度に指定すると、同じフレームから一緒に生成されます。
テープを指定しなければ最新の実行が使われます。

**log:** `--sort`、`--model`、`--tag`、`--limit N`、`-o FORMAT`、`--rebuild`、
`--out`。

## エージェントから動かす

toktape を実際に叩くのは、多くの場合、人ではなく Claude Code や Codex です。
その読者のためのページが別にあり（[`docs/agents.md`](docs/agents.md)）、
同じ契約が `toktape help agents` としてバイナリの中にも入っています。誰かに
教わらなくてもエージェントが自分で見つけられるように。要点だけ：

- **長さはトークンではなく秒で頼む。** 既定が `--for 20s` です。同じトークン数
  はマシンごとに違う時間を意味し、その時間こそ今から測ろうとしているものです。
- **数字を引く前に `caveats` を読む。** カードは出す数字ごとに但し書きを付け
  ます。速度と呼ぶには短すぎる生成、prefill の測定と呼ぶには短すぎるプロンプト、
  忙しいマシン。`-o json` は同じ但し書きを一つの配列で一緒に返します。それを
  読むエージェントは、カードなら脚注を付けたはずの数字をそのまま引くことが
  できません。
- **終了コードで分岐する。メッセージでは分岐しない。** どの動詞も文書化された
  五つのコードのどれかで終わり、`-o json` は成功でも失敗でも stdout にオブジェクト
  を一つだけ出します。パース経路は二つではなく一つです。
- **タイムアウトに収まるかは二つのフラグが決める。** `--wait` の既定は 10 分。
  450 GB のモデルを読み込み中のサーバーは待つ価値があるからです。生成そのものの
  長さは `--for` が決めます。
- **読むのは無料、記録はそうではない。** `toktape runs -o json` と
  `toktape show <id> -o json` はサーバーに触れずに公開済みの記録を読みます。
  動詞なしの `toktape` は記録します。

## 実験台帳

実行ごとに、テープの隣の `runs.tsv` に 1 行追加されます。パラメータスイープが
カードの山ではなく 1 つの表になります。`--tag` と `--note` でその場でラベルを
付けておけば、どちらもテープに保存されるので、台帳はいつでも作り直せます。

```sh
toktape --tag ngl=40 --note "fa on"     # record, labelled
toktape log --sort decode                # which setting won
toktape log --tag ngl -o md              # paste into an issue
toktape log --rebuild                    # regenerate from the tapes
```

不明な値は端末では `?`、エクスポートでは空セル、`-o sql` では `NULL` です。
だから数値列が数値としてインポートされます。

```sh
toktape log -o sql | sqlite3 runs.db
duckdb -c "select tag, decode_tok_s from read_csv('~/.toktape/runs/runs.tsv')"
```

## クリップ

このページ冒頭の画像は画面録画ではありません。テープをフレーム単位で描き直した
もので、あなたのテープも同じように描き出せます。

```sh
toktape render ~/.toktape/runs/<id>.toktape
toktape render ~/.toktape/runs/<id>.toktape --mp4 clip.mp4 --cast clip.cast
```

クリップは実行が始まる画面から始まり、実行全体を実速度で再生して、結果で
止まります。見ていた画面の上に、二つの速度がフィードのサイズでも読める大きさで
出て、マシンとエンジンとモデルの置き場所が添えられます。`--open` を付けると、
このページ冒頭のクリップのようにコマンドを打つ場面が前に付きます。

クリップの長さは**記録するとき**に `--for` で狙います。クリップは実行を
1:1 で再生したものに前後 6 秒の枠が付いたもの、`--open` なら 12 秒なので、
`--for 18s` で 30 秒のものになります。`render --duration` は別のフラグで
代わりにはなりません——あれは手元にある実行を指定した長さに押し込めます。
頼まれない限り何も圧縮しません。長さに合わせて実行を早送りしたクリップは、
このページが語ろうとしているその数字を偽ることになるからです。同じテープ
からはいつも同じクリップになります。

## 公開する

テープは丸ごと渡せるほど小さく（ヒーローで 25 KB）、テープを持ったページは残りを
すべてそこから描けます。[tape.midagedev.com](https://tape.midagedev.com) が
そうしています。`toktape publish` が実行をアップロードしてリンクを出し、その
リンクひとつがカードであり、ブラウザで再生される実行であり、トランスクリプトで
あり、mp4 であり、記録そのものです。

```sh
toktape publish ~/.toktape/runs/<id>.toktape --dry-run
toktape publish ~/.toktape/runs/<id>.toktape
```

`--dry-run` は上がる内容をフィールドごとにそのまま出し、何もアップロードしません。
一度は読んでください。公開した実行は既定で**公開**で、**テキスト**——プロンプトと
モデルが書いた答え——を**含みます**。どのテキストで測った速度か分からなければ、
主張は半分だからです。最初に実際に公開するとき、その点を一度だけ確認します。
`--private` は検索から外し（リンクはそのまま開け、そのリンクだけが入口です）、
`--no-text` はプロンプトと答えを付けずに上げます。どちらも
`~/.toktape/config.toml` で既定にできます。

`toktape profile` はこのマシンの作者を一度だけ決めます。ニックネーム、リンク
ひとつ、アバターの PNG（65536 バイト・256×256 以下）で、以後の公開に毎回
付きます。`--dry-run` は "Who it says published it" の下に、その三つを他の
出ていくものと並べて出します。検証はありません。誰でもどんな名前でも書けます。
`publish --no-profile` はその一回だけ外して上げます。`--title TEXT` と
`--note TEXT`（または `--note-file FILE`）は実行ひとつに実験ノートを付けます。
何を試した実行かをそのまま文章で書く場所で、ページでは作者行と数字の間に、
一覧と API では作者の隣に載ります。

公開したあとでも直せます。`publish --edit <id>` に `--title`、`--note`（または
`--note-file`）、`--private`/`--public` を渡すとページがそのまま変わりますが、
自分のジャーナルトークンで公開した実行に限ります。トークンごとにユーザーホーム
`/u/<handle>` があり、プロフィールと自己紹介（`--bio`）、その人の公開実行だけを
並べます。プロフィールはトークンに付いて回るので、いちばん新しい公開で送った
名前とアバターがホームに出ます。

サイトは検索であって、ランキングではありません。既定は新しい順、どの行にも
カードが出すはずの caveat がそのまま付き、モデル・量子化・エンジン・GPU・ホスト・
VRAM で絞れます。GPU フィルタは二種類のカードを混ぜたマシンも、どちらのカード
からでも見つけます。順序は頼んだときだけ変わります。古い順、あるいは decode の
速い順。各行はその実行をその場でリプレイし、動くのは一度にひとつです。スマート
フォンでは一覧がフィードになり、デスクトップでは一行に二つか三つ並ぶグリッドで、
ポインタの下のものが動きます。これらはすべて端末からも読めます。

```sh
toktape runs --gpu rtx-3090 --sort decode      # what the site lists, as a table
toktape runs --mine -o json                     # your own runs, the API's body verbatim
toktape show <id> --save run.toktape               # one run's summary, and its record
toktape card run.toktape                           # the card, drawn locally from that record
```

`runs` はサイトのフィルタをフラグとして受け取り、`--user HANDLE` でホームを
ひとつ読みます。`show` は id だけでも、その実行のどのリンクでも受け付けます。
`-o json` ならどちらもサービスが返した本文をそのまま出すので、スクリプトが
パースする形はひとつです。

ホスト名と絶対パスは公開かどうかに関係なく取り除かれます。サーバーの argv は
フラグを残してパスだけ失い、モデルはファイル名を残してディレクトリを失います。
ページ冒頭のカードも同じビューから描くので、JSON が忘れたものがピクセルに
描き戻されることはありません。記録時刻の UTC オフセットはそのままです。そうで
あるべきかはまだ開いた問いです。

匿名アップロードのたびに**削除トークン**が一度だけ出ます。そのアップロードの
唯一の鍵で、どこにも保存されません。そのトークンで `DELETE /api/v1/runs/<id>`
を送ると、実行とカードが下がります。サービス自体はテープを開きません。ページの
数字はクライアントがスキーマの隣で導いたインデックス行で、それより詳しいものは
すべて、このバイナリと同じレンダラーを WebAssembly にビルドしてあなたの
ブラウザで動かしたものです。

## どう測っているか

- **サーバーの数値が記録で、クライアントの数値は検証です。** リクエストに
  `timings_per_token` を付けるので、チャンクごとにサーバー自身の
  `prompt_per_second`、`predicted_per_second`、トークン数が届きます。toktape は
  自分の時計で同じ速度を計算して両方を保持し、2 % 以内で一致したかをテープに
  残します。不一致を見栄えのよい数字で塗りつぶすことはしません。
- **クライアント側の速度はコンテンツ区間で測ります。** テキストを含む最初の
  トークンから最後のトークンまでの間隔であり、リクエスト送信からソケット切断まで
  の壁時計時間ではありません。
- **生成トークンが 32 を超えたときだけ「デコード」と呼びます。** それ未満は
  カードに `Sample` と記します。
- **リーズニングトークンも数えます。** 思考モデルの `reasoning_content` デルタは
  デコードトークンとして記録され、ライブ画面では薄く表示されます。TTFT は
  どちらの種類でも最初に届いたトークンです。
- **「未ロード」は GGUF テンソルヘッダから**、テンソル種別ごとに読みます。
- **常駐量は記録ではなく導出です。** ホスト配置のうち RAM にある分は、その瞬間の
  プロセスのファイル由来常駐集合であり、それはモデルのマッピングのページ以外の
  何物でもありません。だからペインもカードもクリップも、同じサンプルから出た
  一つの分割を表示し、実行中はサンプルに従って動きます。`/proc` が見えないときは
  ゼロで埋めるのではなく、分割そのものを出しません。
- **ラウンドの境目ごとにマシンの証人を残します。** ロードアベレージ、IO 圧、
  ページキャッシュ、生きている `llama-*` プロセス、cpufreq の上限、hwmon の温度
  ひとつ。実行の途中でマシンが変わったなら、その数値はそもそも一つの設定のもの
  ではなく、カードがそう言います。
- **PID がない場合**（リモートサーバー、中を覗けないコンテナ）でも、速度、
  プレフィックスキャッシュのヒット率、GPU 状態は記録されます。ホスト RSS、ページ
  フォールト、フラグは `?` になり、カードに `/proc` ビューが使えなかった旨が
  記されます。フォールトを測れなかった実行に cold のラベルは付けません。
- **GPU がない場合**、VRAM と温度の行が `?` になり、理由がカードに記されます。
- 不明な値は `?` です。カードは観測していない値を決して表示しません。

## よくある質問

**`llama-bench` があるのになぜ？** 両方使ってください。`llama-bench` はエンジンの
演算を切り離して測る道具で、その用途には最適です。toktape はサーバーを測ります。
HTTP スロット、プレフィックスキャッシュの再利用、キューが詰まった状態の TTFT、
4 本や 8 本のストリームが同時に来たときに起きること。エージェントのワークロードが
実際に見る数字はこちらです。

**プロンプトがキャッシュされていなかったとどう分かる？** カードに書いてあります。
`Prefix cache 0% hit (0/512)`、そしてフォールト数から出た `cold`/`warm`。カードを
疑う人がいればテープを送ってください。同じカードが出ます。

**どこかに何かを送信する？** しません。llama-server とだけ通信し、`~/.toktape`
の下にファイルを書きます。テレメトリもアカウントもありません。

## 対応範囲

| | |
| --- | --- |
| サーバー | llama-server（upstream llama.cpp）、ik_llama.cpp、`/props` に `engine` 情報を載せて返すサーバー |
| Linux | 主要ターゲット、x86_64・arm64、`/proc` ビュー完備 |
| macOS | ビルド・実行可、`--url` でアタッチ。`/proc` ビューがないためメモリ・フォールトの行は `?` |
| Windows | 専用バイナリ、`--url` でアタッチ。`/proc` ビューなし。完全なビューは WSL2 の Linux バイナリ |
| GPU | `nvidia-smi` 経由の NVIDIA |

サーバーは llama.cpp でなくても構いません。エンジン自身が名乗るサーバー、
つまりエンジン名とバージョン、モデルの形式と形状、どのバイトがどのデバイスに
載っているかを `/props` で返すサーバーであれば、toktape はその報告だけで
記録します。GGUF を開くことも、コマンドラインを解析することもありません。
別のプロセスに代わって答えるサーバー、つまり OpenAI API しか話せないエンジンの
前に立つ shim であれば、そのプロセスの pid も同じ報告に載せます。そうすれば
メモリ、ページフォールト、競合の行が、前に立つ代理ではなくサーバー自身を
表します。
[exl3-serve](https://github.com/midagedev/exl3-serve) が ExLlamaV3 に対して
それを行います。EXL3 モデル 1 つを toktape がすでに話せる llama-server の
インターフェースに載せるので、EXL3 モデルもそのまま記録できます。

それ以外の OpenAI 互換サーバー——vLLM、SGLang、TabbyAPI、LM Studio など——は
汎用モードで記録します。`toktape --engine-kind openai`、あるいは何も付けなくても
構いません。自動検出が `/props` を先に叩き、なければ `/v1/models` に切り替えて、
そこに答えるサーバーへアタッチします。こうしたサーバーは timings を報告しない
ので、レコーダー自身の時計が記録になり、カードはデコード速度の隣に
`client-timed` と書きます。比較は他の client-timed 実行とだけ。スロットも
フラグのブロックもなく、`--engine` はエンジン名を主張として受け取り、その言葉
とともに印字します。

ロードマップ: sudo 不要の macOS コレクター、`/api/ps` に基づく Ollama オフロード
カード、`toktape ab URL1 URL2`（サーバー 2 台、プロンプト 1 つ、並べて表示）。

## `.toktape` フォーマット

gzip された JSON、実行ごとに 1 ファイル、スキーマバージョン 1。プレーン JSON も
読めるので、`gunzip` した後も grep できます。カードを描くための実行サマリー、
トークンごとのタイムスタンプとテキスト、サンプル時系列、サーバーの生のタイミング、
フラグとビルド、配置の推定が入っています。より新しいスキーマで書かれたテープは、
推測せずに拒否します。すべてのレンダラーはテープだけを読みます。テープを
共有してください。toktape を持つ誰でも同じカードを描けます。

## コントリビュート

Issue と Pull Request を歓迎します。バグ報告にはテープを添えていただけると
いちばん助かります。実行は `.toktape` 1 つで完全に記述されるので、「このカードが
出た」と「ファイルはこれ」は同じことです。

ゲートは `./scripts/check.sh` です。gofmt、ビルド、vet、Linux クロスビルド、
テストを実行し、CI もまったく同じスクリプトを走らせます。ツリーの構成、ゴールデン
ファイルの更新方法、スキーマとカードが従う規則は [CONTRIBUTING.md](CONTRIBUTING.md)
（英語）にあります。

## ライセンス

MIT。[LICENSE](LICENSE) を参照してください。
