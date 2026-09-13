# toktape

**ローカル LLM サービングのためのブラックボックス・テープ。**

[English](README.md) · [한국어](README.ko.md) · 日本語

[![check](https://github.com/midagedev/toktape/actions/workflows/ci.yml/badge.svg)](https://github.com/midagedev/toktape/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/midagedev/toktape)](https://github.com/midagedev/toktape/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/midagedev/toktape)](go.mod)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

toktape は、すでに起動している llama-server にアタッチし、1 回の実行を
`.tape` ファイルに記録して、カードを 1 枚出力します。モデルがどこに載っているか、
プロセスが実際に何に触れたか、リクエストが本当にどれだけ速かったか。
それが 1 枚に収まります。1 ストリームでも同時 8 ストリームでも、記録の仕方は
同じです。

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape recording 4 concurrent streams, from the command being typed to the result card"></p>

<p align="center"><em>セッションの全体です。プロンプトにコマンドを打ち、サーバーを見つけてアタッチし、4 ストリームが同時に流れ、カードが出ます。画面録画ではなく、テープを <code>toktape render</code> と同じレンダラーで描き直したものです。</em></p>

```text
┌──────────────────────────────────────────────────────────────────────┐
│ toktape v0.1.0                  20260913-150210-r1-distill-llama-70b │
├──────────────────────────────────────────────────────────────────────┤
│ MODEL    DeepSeek-R1-Distill-Llama-70B-Q4_K_M.gguf · Q4_K_M          │
│          42.5 GiB                                                    │
│ ENGINE   llama-server b3650 (a1b2c3d) · linux 6.8.0-45-generic       │
│          workstation                                                 │
│ RIG      2× RTX 3090 24G · AMD Ryzen 9 7950X · 64 GB DDR5-6000       │
├──────────────────────────────────────────────────────────────────────┤
│ Decode        72.9 tok/s aggregate · 9.1 tok/s each                  │
│               ≈ 410 GB/s, 44% of peak                                │
│ Prefill       2927 tok/s aggregate · 610 tok/s each                  │
│               TTFT p50 810 ms · 512 prompt tokens                    │
│ Context       16384 (512 in / 307 out)                               │
│ Prefix cache  25% hit (128/512) · warm                               │
│ Sampling      temp default · chat                                    │
│ Streams       8 × 9.1 tok/s = 72.9 tok/s aggregate                   │
│               TTFT p50 810 ms p95 1050 ms · slots busy max 8         │
├──────────────────────────────────────────────────────────────────────┤
│ MEMORY   GPU0 [██████████] 23.8/24.0 GiB                             │
│          GPU1 [██████████] 22.8/24.0 GiB                             │
│          weights 42.5 | kv 2.6 | compute 1.5 GiB                     │
│          Host RSS 1.2 GiB (file 0.8 / anon 0.4)                      │
│          Page faults 0.0 maj/token (0 during decode)                 │
├──────────────────────────────────────────────────────────────────────┤
│ HOST     GPU0 71°C 348 W · GPU1 67°C 318 W · throttled: no           │
│          contended: no                                               │
├──────────────────────────────────────────────────────────────────────┤
│ FLAGS    -ngl 99 -fa on -b 2048 -ub 512 -ctk q8_0 -ctv q8_0 -t 16    │
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
Windows は WSL2 で動作します。GPU の行は `nvidia-smi` から読みます。

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
4. **テープ** — 実行全体を `~/.toktape/runs/<id>.tape` に書き出します。
5. **カード** — 72 桁のカードを出力し、テープの隣に保存して、共有方法を 1 行で
   示します。

ポートも PID も、覚えるべきフラグもありません。モデルがまだロード中なら待ちます
（`--wait`、既定は 10 分）。

**同時 8 ストリーム。** エージェントのワークロードがサーバーに与える負荷は
まさにこれです。

```sh
toktape -n 8
```

N が増えるとストリームあたりの tok/s が下がるのは正常です。「このマシンは
エージェント 8 体を捌けるか」に答える数字は合計値で、単一ストリームのベンチ
マークではこの数字は見えません。

**リアルタイムで見る。** ストリームごとにタイル 1 枚、右側にマシンのペインです。

```sh
toktape -n 4 --tui
toktape -n 8 --tui --grid 2x2     # four tiles per page, ←/→ to page
```

**共有する:**

```sh
toktape card ~/.toktape/runs/<id>.tape --png           # 1200×675 image next to the tape
toktape card ~/.toktape/runs/<id>.tape --md --copy     # card + llama-bench table, on the clipboard
toktape render                                          # the newest run as a GIF
```

**リプレイする。** ライブ画面で、好きな速度で再生します。

```sh
toktape play ~/.toktape/runs/<id>.tape --speed 2
```

## カードに載るもの

すべてのカードで、同じ項目が同じ位置にあります。2 枚を並べて読めるように
するためです。各項目は、ひとつの議論を終わらせるために置かれています。

- **デコードとプリフィルを混ぜない。** プリフィルは演算律速、デコードはメモリ
  帯域律速なので、「45 tok/s」ひとつでは何もわかりません。TTFT、プロンプト
  tok/s、デコード tok/s を、それぞれのトークン数と一緒に別々に表示します。
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
- **フラグはすべて。** `-ngl -fa -b -ub -ctk -ctv --load-mode -ot`。Flash
  Attention の有無とバッチサイズ、この 2 つが抜けると結果の投稿がコメント 50 件の
  スレッドになります。
- **正確な量子化。** `UD-Q4_K_M` を「Q4」と縮めません。
- **contended。** 同じマシンの別プロセスは、人が試す大半の変更よりも大きく
  デコード速度を動かします。カードはロードアベレージと他の GPU プロセスを読み、
  実行にラベルを付けます。
- **ストリーム。** ストリームあたりの速度 × N = 合計をカードにそのまま書き、TTFT の
  p50・p95 と、同時にビジーだった最大スロット数を並べます。

## コマンド

| 動詞 | 役割 | 例 |
| --- | --- | --- |
| `record` | アタッチして実行を記録する。既定の動詞 | `toktape -n 4 --n-predict 512` |
| `card` | テープからカードを描き直す | `toktape card <tape> --png` |
| `play` | ライブ画面で実行をリプレイする | `toktape play <tape> --speed 4` |
| `render` | GIF、mp4、asciicast、PNG フレームとして書き出す | `toktape render <tape> --mp4 clip.mp4` |
| `ls` | 記録した実行の一覧 | `toktape ls` |
| `log` | 全実行の実験台帳 | `toktape log --sort decode` |
| `compare` | 2 つの実行の指標とフラグを比較する | `toktape compare a.tape b.tape` |
| `version` | バージョンを表示する | `toktape version` |

**record:** `--url`（既定は自動探索）、`-n`/`--concurrency`、`--prompt`（複数
指定可、`-n` 分まで循環）、`--prompts`（JSONL ファイル。1 行が `-n` 本の
ストリームからなる 1 ラウンドで、順番に 1 本のテープへ記録）、`--spec-n-max LIST`（例: `3,5`。プロンプト一式を
speculative `n_max` の値ごとに 1 回ずつ流して同じテープに記録し、カードには値ごとの行が
付く）、`--n-predict`（既定 256）、`--out`（既定
`~/.toktape/runs`）、`--tag`、`--note`、`--wait`、`--tui`、
`--grid COLSxROWS`（既定 `2x4`、`0` で端末に合わせる）、`--no-card`、
`--json`、`--quiet`。

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

**card:** `--md`（フェンス内のカードと llama-bench 互換の表）、`--json`（実行
サマリー）、`--png [FILE]`（1200×675 の共有画像）、`--copy`（OSC 52 で
クリップボードにもコピー）。`--md` と `--json` はどちらか一方です。

**render:** `--gif FILE`、`--mp4 FILE`（`PATH` に ffmpeg が必要）、`--cast FILE`
（asciicast v2）、`--frames DIR`（PNG シーケンス）、`--duration`、`--fps`、
`--size WxH`。複数の出力を一度に指定すると、同じフレームから一緒に生成されます。
テープを指定しなければ最新の実行が使われます。

**log:** `--sort`、`--model`、`--tag`、`-n`、`--tsv`、`--csv`、`--json`、
`--md`、`--rebuild`、`--out`。

## 実験台帳

実行ごとに、テープの隣の `runs.tsv` に 1 行追加されます。パラメータスイープが
カードの山ではなく 1 つの表になります。`--tag` と `--note` でその場でラベルを
付けておけば、どちらもテープに保存されるので、台帳はいつでも作り直せます。

```sh
toktape --tag ngl=40 --note "fa on"     # record, labelled
toktape log --sort decode                # which setting won
toktape log --tag ngl --md               # paste into an issue
toktape log --rebuild                    # regenerate from the tapes
```

不明な値は端末では `?`、エクスポートでは空セルです。数値列は数値として
インポートされます。

```sh
sqlite3 runs.db ".import --tsv ~/.toktape/runs/runs.tsv runs"
duckdb -c "select tag, decode_tok_s from read_csv('~/.toktape/runs/runs.tsv')"
```

## クリップ

このページ冒頭の画像は画面録画ではありません。テープをフレーム単位で描き直した
もので、あなたのテープも同じように描き出せます。

```sh
toktape render ~/.toktape/runs/<id>.tape
toktape render ~/.toktape/runs/<id>.tape --mp4 clip.mp4 --cast clip.cast
```

クリップは実行が始まる画面から始まり、実行全体を実速度で再生して、結果で
止まります。見ていた画面の上に、二つの速度とマシン、モデルの置き場所が出ます。
`--open` を付けると、このページ冒頭のクリップのようにコマンドを打つ場面が前に
付き、`--duration` で好きな長さに収められます。同じテープからはいつも同じ
クリップになります。

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
| サーバー | llama-server（upstream llama.cpp）、ik_llama.cpp |
| Linux | 主要ターゲット、x86_64・arm64、`/proc` ビュー完備 |
| macOS | ビルド・実行可、`--url` でアタッチ。`/proc` ビューがないためメモリ・フォールトの行は `?` |
| Windows | WSL2 経由 |
| GPU | `nvidia-smi` 経由の NVIDIA |

ロードマップ: sudo 不要の macOS コレクター、`/api/ps` に基づく Ollama オフロード
カード、`toktape ab URL1 URL2`（サーバー 2 台、プロンプト 1 つ、並べて表示）。

## `.tape` フォーマット

gzip された JSON、実行ごとに 1 ファイル、スキーマバージョン 1。プレーン JSON も
読めるので、`gunzip` した後も grep できます。カードを描くための実行サマリー、
トークンごとのタイムスタンプとテキスト、サンプル時系列、サーバーの生のタイミング、
フラグとビルド、配置の推定が入っています。より新しいスキーマで書かれたテープは、
推測せずに拒否します。すべてのレンダラーはテープだけを読みます。テープを
共有してください。toktape を持つ誰でも同じカードを描けます。

## コントリビュート

Issue と Pull Request を歓迎します。バグ報告にはテープを添えていただけると
いちばん助かります。実行は `.tape` 1 つで完全に記述されるので、「このカードが
出た」と「ファイルはこれ」は同じことです。

ゲートは `./scripts/check.sh` です。gofmt、ビルド、vet、Linux クロスビルド、
テストを実行し、CI もまったく同じスクリプトを走らせます。ツリーの構成、ゴールデン
ファイルの更新方法、スキーマとカードが従う規則は [CONTRIBUTING.md](CONTRIBUTING.md)
（英語）にあります。

## ライセンス

MIT。[LICENSE](LICENSE) を参照してください。
