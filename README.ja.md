# toktape

**ローカル LLM サービングのためのブラックボックステープ.**

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

<p align="center"><em>演出ではなく実際の実行です。4 ストリームがそれぞれ 42.1 tok/s で答え、トークン上限に切られたストリームはなく、見えているものはすべて 1:1 です。<code>assets/hero.tape</code> を <code>toktape render</code> と同じレンダラーで描き直したもので、下のカードは同じファイルから <code>toktape card assets/hero.tape</code> で出ます。</em></p>

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

## なぜ作ったか

「この構成でこのモデルが何 tok/s」という投稿は、いつも同じ返信で終わります。
プロンプトはキャッシュされていなかったか、Flash Attention は有効か、同時
何ストリームか、量子化は正確には何か、そのとき他のプロセスは動いていなかったか。
toktape はそのすべてに、サーバー自身が報告した数値でカード 1 枚で答え、実行そのもの
をファイルとして残します。同じファイルからは誰でも同じカードを描き直せます。
静的バイナリ 1 つ、MIT ライセンス、テレメトリなし、アカウント不要です。

## インストール

**Homebrew**（macOS・Linux 共通）:

```sh
brew install midagedev/tap/toktape
```

**シェルスクリプト** — OS と CPU に合うリリースを `checksums.txt` で検証して
からバイナリ 1 つを `~/.local/bin` に入れます（`TOKTAPE_INSTALL`・
`TOKTAPE_VERSION`・`TOKTAPE_BASE_URL` で向け先を変更、`-s -- --dry-run` で
確認）:

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

**Windows** はリリースページの zip を展開して `PATH` に置きます。**Go** は
`go install github.com/midagedev/toktape/cmd/toktape@latest`。**ソースから**
（Go 1.26、cgo 不要）:

```sh
git clone https://github.com/midagedev/toktape.git
cd toktape
go build -o toktape ./cmd/toktape
```

`toktape version` で確認します。実行時に何も解決しないバイナリなので、サーバーの
ホストへの `scp` も正式なインストールです。Linux x86_64・arm64が基本対象 —
メモリ・フォールト・フラグの行は `/proc` から読みます。macOS と Windows も
ビルドされて動き、`--url` でリモートサーバーに接続し（それらの行は `?`）、
WSL2 では Linux バイナリがフルビューを持ちます。GPU 行は `nvidia-smi` から。

## まず使ってみる

llama-server が動いているマシンで、単語をひとつ打ちます。

```sh
toktape
```

サーバーを見つけ（`127.0.0.1:8080`、続いて `:8081`、`:8000`、`:5000`）、
モデルパスを `/proc/*/cmdline` と照合してアタッチし、内蔵プロンプトセット —
21 の長いアーティファクト。このマシンが測ったプリフィルが払えるだけの先頭
部分に切り詰めて送ります — からリクエストを送りながらフォールト・RSS・GPU を
サンプリングし、実行全体を `~/.toktape/runs/<id>.toktape` に書き、共有の
方法が書かれたカードを出力します。ポートも PID もフラグも不要です。モデルの
ロード中なら待ちます（`--wait`、既定 10 分）。

**同時 8 ストリーム。** エージェントのワークロードがサーバーにすることです。
N が増えてストリームあたり tok/s が下がるのは結果であり、「このマシンは
エージェント 8 体を支えられるか」に答える数字は合計です。

```sh
toktape --sessions 8
```

**ライブで見る。** ストリームごとにタイル 1 つ、右にマシンパネル:

```sh
toktape --sessions 4 --tui
toktape --sessions 8 --tui --grid 2x2     # four tiles per page, ←/→ to page
```

**共有と再生:**

```sh
toktape card ~/.toktape/runs/<id>.toktape -o png          # 1200×675 image next to the tape
toktape card ~/.toktape/runs/<id>.toktape -o md --copy    # card + llama-bench table, on the clipboard
toktape render                                          # the newest run as a GIF
```

```sh
toktape play ~/.toktape/runs/<id>.toktape --speed 2
```

## どう測っているか

- **プリフィルは、マシンの速度と固定費として、実行の前に別個に測ります。**
  何も流れていない状態で `/completion` に生のプロンプトを 2 本 — 128
  トークンのものと、短い側が観測した費用が許すかぎり長いもの — を送り、
  トークンあたり速度とリクエストあたり固定費にフィットします。固定費が大半の
  プリフィルの数字はスループットではなく、どちらがどちらかを言うのがこの
  フィットです。プローブのプロンプトは実行ごとのソルトで始まるので、同じ
  暖かいサーバーを 2 度測ってもちゃんと測れます。
- **プロンプトセットが計測器です。** 実行はあなたが打ったプロンプトを送らず、
  固定セット 21 点 — 本物のバグと通るテストつきのコード、クエリプラン、障害の
  タイムライン、ADR、韓国語と日本語の文章 — を送ります。1 点あたり 18–30 KB
  で、どのマシンでも丸ごとは送らないよう、意図してその大きさです。実行は各
  プロンプトの先頭部分を、フィットが示す割合 — 固定費がプリフィルの 20 分の
  1 以下に収まるだけ — 切り出して送り、何文字送ったかを記録します。公開された
  記録はセットと突き合わせて検証され、切り詰めた接頭辞ごと。スロットの
  コンテキストが次の口を利きます。実行の回答予算はそのプロンプトを収めた
  スロットが耐えられるだけに削られ、どのスロットにも収まらないプロンプトは
  最初のリクエスト前に二つの数字とともに拒否されます。
- **サーバーの数字が記録、クライアントの数字は検証です。** チャンクごとに
  サーバー自身の速度とトークン数が届き、toktape は自分の時計で計算し直して
  2 % 以内で一致したかをテープに残します。不一致を見た目のよい数字で
  上書きすることはありません。
- **クライアントの速度はコンテンツ区間で測ります** — テキストを載せた最初の
  トークンと最後のトークンの間であって、経過時間ではありません。生成 32
  トークン未満は「デコード」ではなく `Sample` です。
- **推論トークンも数えます。** `reasoning_content` の差分はデコードトークンで、
  TTFT はどちらか早い方の最初のトークンです。
- **cold / warm はフォールト数から**、推測からではありません。常駐量は
  プロセスのマッピングから導出し、「未ロード」は GGUF テンソルヘッダから読み、
  ラウンドの境界ごとにマシンの証人（ロード、IO、ページキャッシュ、生きている
  `llama-*` プロセス、cpufreq、温度 1 つ）を残します。
- **不明な値は `?` です。** カードは観測していない値を決して表示せず、
  フォールトを測れなかった実行に cold のラベルは付きません。

## 引く価値のある数字

カードは出す数字のすべてに但し書きを付けます。ここに挙げる習慣は、但し書きの
ほとんどないカードを作ります。その大半はフラグひとつ、あるいは何もしない
ことです。

- **2 度走らせて、2 度目を引用する。** ロードした直後の最初の実行はデコード
  しながらディスクから重みを引き上げており、カードはフォールト数に基づいて
  `cold` と書きます。引用に値するのはウォーム時の速度です。
- **長さは秒で。** `--for 30s`。同じトークン数はマシンごとに違う時間を意味し、
  その時間こそ測ろうとしていたものです。`--n-predict` を自分で指定すると時計が
  消えるので、上限を指定した実行は上限で切れます。
- **推論モデルは時計を使って考えます。** 既定の予算は回答が始まる前に尽きる
  ことがあります。思考を載せるなら `--for 60s`、非推論モデルとの同条件比較なら
  `--no-think`。
- **何かを引用する前に caveats の行を読む。** 速度と呼ぶには短い生成、忙しい
  マシン、時計による切断 — すべて投稿する前にカードの上にあり、`-o json` が
  同じ一覧に深刻度を付けて運びます。
- **同じもの同士を比べる。** プロンプトセット id、サンプリング、エンドポイント、
  エンジンのビルドがすべてカードにあります。2 枚のカードは比較可能か、そうで
  ないなら理由を言います。
- **質問の数だけストリームを。** `--sessions 8` はエージェント 8 体がサーバーに
  することです。単一ストリームは別の質問に答えます。
- **マシンを放置する。** コンパイルひとつで、人が試す大半の設定差より大きく
  デコードが揺れます。地盤が動いていればカードが `contended` /
  `conditions_changed` と言います。

## カードに載るもの

すべてのカードで同じ項目が同じ位置にあり、各項目はひとつの議論を終わらせる
ために置かれています。デコードとプリフィルを混ぜず、待ち行列をエンジンの
仕事から切り離すこと（`engine prefill 10732 ms · queue 22 ms`）をはじめ、
プレフィックスキャッシュのヒット、プリフィルのフィット、トークンあたり
フォールト、モデルが実際にどこにいるか（置かれたもの対 常駐しているもの、
weights 対 KV 対 compute、テンソルヘッダから読んだ未ロードバイト）、壁に
なっているバスに大して測った帯域幅（ドラフトがあれば verify step 単位）、
実際に送ったサンプリング、ドラフトの受理率とステップのかたち、フラグすべてと
正確な量子化、contended、そして「ストリームあたり × N が本当に合計なのか」まで。

## コマンド

| 動詞 | すること | 例 |
| --- | --- | --- |
| `record` | アタッチして実行を記録。既定の動詞 | `toktape --sessions 4 --for 30s` |
| `card` | テープからカードを描き直す | `toktape card <tape> -o png` |
| `play` | ライブ画面で実行を再生 | `toktape play <tape> --speed 4` |
| `render` | GIF・mp4・asciicast・PNG フレームに描く | `toktape render <tape> --mp4 clip.mp4` |
| `ls` | 記録済み実行の一覧 | `toktape ls` |
| `log` | 全実行の実験台帳 | `toktape log --sort decode` |
| `compare` | 2 つの実行の指標とフラグを比べる | `toktape compare a.toktape b.toktape` |
| `publish` | 実行をアップロードしてリンクを出す | `toktape publish <tape>` |
| `profile` | 公開に付く作者名を決める | `toktape profile --name NAME --link URL --avatar FILE --bio TEXT` |
| `runs` | 公開済み実行の一覧。サイトと同じフィルタと順序 | `toktape runs --gpu rtx-3090 --sort decode` |
| `show` | 公開済み実行を 1 件読む。`--save` で記録も | `toktape show <id> --save run.toktape` |
| `reindex` | 公開済み実行の検索行を記録から再計算。新しい toktape が索引する数値を古い実行にも | `toktape reindex <id>` |
| `version` | バージョンを表示 | `toktape version` |

記録は時計で終わります — 既定 20 秒、`--for 30s` で狙いを定めます。同じ 256
トークンがマシンによって 2 秒にも 2 分にもなるからです。64 トークン未満では
切らず、`--n-predict` は一緒にかかる上限です（指定すると時計は消えます）。
`--prompt`（繰り返し可）や `--prompts`（JSONL ファイル、1 行が 1 ラウンド）が
内蔵セットを置き換え、`--spec-n-max 3,5` はセットを speculative `n_max` 値
ごとに 1 回ずつ走らせてテープ 1 本に収めます。

リクエストのかたちはサーバーのフラグと同じだけ数字を動かします。greedy、
サーバー既定のサンプリング、思考させたまま — 同じエンジンで 11 % の開きです。
なので `record` は 3 つとも名前を付けます: `--temp`、`--no-think`、
`--endpoint chat|completion`、ほかはそのビルドが受け付ける限り `--param
key=value`。送ったものはすべて記録され、カードに名前が付きます。

`-o FORMAT` は llama-bench の `-o` をその言葉のまま使います: `json`、`jsonl`、
`md`（フェンス内のカード、llama-bench 互換表、Reproduce ブロック）、`csv`、
`tsv`、`sql`。`toktape log -o sql | sqlite3 runs.db` がそのままデータベース
です。不明な値はターミナルで `?`、エクスポートで空欄、sql で `NULL` です。

実行ごとにテープの隣の `runs.tsv` に 1 行が追加されるので、スイープが表 1 枚に
なります。`--tag` と `--note`でラベルを付け、台帳はいつでも作り直せます:

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

クリップは画面収録ではなくテープから描かれ、1:1 で再生されます — 頼まれない
限り何も圧縮しません。再生速度を上げたクリップは、このページのただひとつの
数字について嘘をつくことになるからです。`--prefill-lead 3s` は最初のトークンの
直前から始めます:

```sh
toktape render ~/.toktape/runs/<id>.toktape
toktape render ~/.toktape/runs/<id>.toktape --mp4 clip.mp4 --cast clip.cast
```

## エージェントから

toktape を実際に叩くのは、多くの場合 Claude Code や Codex です。その読者のための
契約が [`docs/agents.md`](docs/agents.md) とバイナリ内の `toktape help agents` に
あります — 終了コード 5 つ、成否によらず `-o json` が stdout にオブジェクトを
1 つ、そして [引く価値のある数字](#引く価値のある数字) の習慣。読むのは無料で
（`toktape runs -o json`）、記録はそうではありません。

## 公開する

テープは丸ごと手渡せる大きさです — ヒーローは 25 KB。テープを持ったページは
残りすべてをそこから描けます。[tape.midagedev.com](https://tape.midagedev.com)
がそれをします。リンクがそのままカード、ブラウザで再生される実行、書き起こし、
mp4、記録そのものになります。

```sh
toktape publish ~/.toktape/runs/<id>.toktape --dry-run
toktape publish ~/.toktape/runs/<id>.toktape
```

`--dry-run` は何が上がるかを正確に示します。一度は読んでください。公開された
実行は**公開**であり、既定で**テキストを載せます** — プロンプトとモデルが
書いたものまで。測定に使ったテキストのない速度は半分の主張だからです。
`--private` は検索から外し、`--no-text` はテキストを家に置き、どちらも
`~/.toktape/config.toml` で既定にできます。`toktape profile` は作者名を
マシンごとに一度決め、`--title`/`--note` は実行 1 件に実験メモを付け、
`publish --edit <id>` はページを書き直します。

公開した実行はコマンド一つで取り下げられます: `toktape publish --delete <id>`。
ジャーナルトークンがあればそれが鍵です。匿名で上げた場合は**削除トークン**が
一度表示され、`~/.toktape/published.json` にも保存されるので同じコマンドが
そのまま使えます。実行を取り下げると、その項目も一緒に消えます。

ハブのアドレスは固定ではなく設定です。`~/.toktape/config.toml` に
`service = "https://tapes.example.com"` と書けば（`TOKTAPE_SERVICE` や
`--url` でも）、すべてのコマンドが自分で立てたハブを向きます。そのファイルの
トークンは、隣に書かれたサービスにしか送られません。立て方は
[`web/README.md`](web/README.md) にあります。

サイトはリーダーボードではなく検索です。新しい順、すべての行にカードが付ける
但し書き、モデル・量子化・エンジン・GPU・ホスト・VRAM のフィルタ。すべては
ターミナルからも読めます:

```sh
toktape runs --gpu rtx-3090 --sort decode      # what the site lists, as a table
toktape runs --mine -o json                     # your own runs, the API's body verbatim
toktape show <id> --save run.toktape               # one run's summary, and its record
toktape card run.toktape                           # the card, drawn locally from that record
```

ホスト名と絶対パスは公開設定にかかわらず取り除かれます。サービス自身はテープを
開きません。索引行より豊かなものはすべて、このバイナリと同じレンダラーを
WebAssembly にコンパイルしてブラウザで動かしたものです。

## よくある質問

**`llama-bench` があるのに?** 両方使ってください。`llama-bench` はエンジンの
演算を隔離して測り、toktape はサーバーを測ります — スロット、プレフィックス
キャッシュの再利用、待ち行列が一杯の TTFT、8 ストリームが同時に来たときに
起きること。エージェントのワークロードが実際に見る数字はこちらです。

**プロンプトがキャッシュされていないことをどう知る?** カードに書いてあります。
`Prefix cache 0% hit (0/512)` と、フォールト数に基づく `cold`/`warm`。カードを
疑う人がいたらテープを送ってください。同じカードが出ます。

**どこに何を送る?** いいえ。あなたの llama-server とだけ通信し、`~/.toktape` の
下にファイルを書きます。テレメトリもアカウントもありません。

## 対応範囲

| | |
| --- | --- |
| サーバー | llama-server（本流 llama.cpp）、ik_llama.cpp、`/props` に `engine` オブジェクトで答えるサーバー |
| Linux | 基本対象、x86_64・arm64、フル `/proc` ビュー |
| macOS | ビルド・実行、`--url` 接続、`/proc` ビューなし — メモリ・フォールト行は `?` |
| Windows | 専用バイナリ、`--url` 接続、`/proc` ビューなし。WSL2 は Linux バイナリでフルビュー |
| GPU | `nvidia-smi` 経由の NVIDIA |

その他の OpenAI 互換サーバー — vLLM、SGLang、TabbyAPI、LM Studio — は
汎用モード（`--engine-kind openai`、または自動検出）で記録されます。timings が
返ってこないので記録側の時計が記録になり、カードは速度の隣に `client-timed` と
書きます。他の client-timed 実行とだけ比べてください。

## `.toktape` フォーマット

実行 1 件につき 1 ファイルの Gzipped JSON、スキーマバージョン 1（平文 JSON も
読みます）。要約、トークンごとのタイムスタンプとテキスト、サンプル列、サーバーの
生の timings、フラグとビルド、配置推定が入ります。読む側は新しいスキーマを
推測せず拒否します。テープを共有してください。toktape を持つ誰もが同じカードを
描きます。

## コントリビュート

イシューとプルリクエストを歓迎します。バグ報告はテープを添えるのがいちばん
役に立ちます — 実行 1 件は `.toktape` で完全に記述されるからです。
`./scripts/check.sh` がゲートです — gofmt、ビルド、vet、Linux クロスビルド、
テスト — CI はまさにそれを走らせます。ツリーの構成とスキーマ・カードの規則は
[CONTRIBUTING.md](CONTRIBUTING.md) にあります。

## ライセンス

MIT。[LICENSE](LICENSE) を参照。
