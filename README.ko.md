# toktape

**로컬 LLM 서빙을 위한 블랙박스 테이프.**

[English](README.md) · 한국어 · [日本語](README.ja.md) · [简体中文](README.zh-CN.md) · [繁體中文](README.zh-TW.md)

[![check](https://github.com/midagedev/toktape/actions/workflows/ci.yml/badge.svg)](https://github.com/midagedev/toktape/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/midagedev/toktape)](https://github.com/midagedev/toktape/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/midagedev/toktape)](go.mod)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

<img src="docs/mascot.png" align="right" width="150" alt="the toktape mascot: a small chibi in headphones, eyes closed, hugging a cassette tape">

toktape는 이미 떠 있는 llama-server에 붙어서 한 번의 실행을 `.toktape` 파일로
녹화하고, 카드 한 장을 출력합니다. 모델이 어디에 올라가 있는지, 프로세스가
실제로 무엇을 건드렸는지, 요청이 정말 얼마나 빨랐는지가 그 한 장에 담깁니다.
스트림 하나든 동시에 여덟이든 같은 방식으로 기록합니다.

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape recording four concurrent streams of a 35B sparse MoE, from the command being typed to the result"></p>

<p align="center"><em>연출이 아니라 실제 실행입니다. 기본 명령에 <code>--sessions 4</code>만 붙였고, 네 스트림이 각각 37.3 tok/s로 답하다가 20초 시계에서 끝나며, 보이는 것은 전부 1:1입니다. 프롬프트가 800토큰인 것은 toktape가 이 기계에서 프리필 넷이 동시에 돌 때의 비용을 먼저 재고 길이를 골랐기 때문입니다. 화면 녹화가 아니라 <code>assets/hero.tape</code>를 <code>toktape render</code>와 같은 렌더러로 다시 그린 것이고, 아래 카드는 같은 파일에서 <code>toktape card assets/hero.tape</code>로 나옵니다.</em></p>

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

## 왜 만들었나

"이 장비에서 이 모델이 몇 tok/s" 글은 늘 같은 댓글로 끝납니다. 프롬프트가
캐시된 건 아니냐, 플래시 어텐션은 켰냐, 동시 몇 스트림이냐, 양자화는 정확히
뭐냐, 그때 다른 프로세스는 안 돌았냐. toktape는 그 질문 전부를 서버가 직접
보고한 수치로 카드 한 장에 답하고, 실행 자체를 파일로 남깁니다. 누구든 같은
파일에서 같은 카드를 다시 그릴 수 있습니다. 정적 바이너리 하나, MIT 라이선스,
텔레메트리 없음, 계정 없음입니다.

## 설치

**Homebrew** (macOS·Linux 공통):

```sh
brew install midagedev/tap/toktape
```

**셸 스크립트** — OS와 CPU에 맞는 릴리스를 받아 `checksums.txt`로 검증한 뒤
바이너리 하나를 `~/.local/bin`에 넣습니다 (`TOKTAPE_INSTALL`·`TOKTAPE_VERSION`·
`TOKTAPE_BASE_URL`로 방향을 바꾸고, `-s -- --dry-run`으로 미리 보기):

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

**Windows**는 릴리스 페이지의 zip을 풀어 `PATH`에 두면 됩니다. **Go**는
`go install github.com/midagedev/toktape/cmd/toktape@latest`. **소스 빌드**
(Go 1.26, cgo 없음):

```sh
git clone https://github.com/midagedev/toktape.git
cd toktape
go build -o toktape ./cmd/toktape
```

`toktape version`으로 확인합니다. 실행 시 의존하는 것이 없는 바이너리라 서버가
도는 장비로 `scp`하는 것도 정식 설치입니다. Linux x86_64·arm64가 기본 대상 —
메모리·폴트·플래그 행은 `/proc`에서 읽습니다. macOS와 Windows도 빌드되고
`--url`로 원격 서버에 붙으며 그 행들은 `?`로 찍히고, WSL2에서는 Linux 바이너리가
전체 뷰를 씁니다. GPU 행은 `nvidia-smi`에서 읽습니다.

## 바로 써 보기

llama-server가 도는 장비에서 한 단어만 칩니다.

```sh
toktape
```

서버를 찾고(`127.0.0.1:8080`, 이어서 `:8081`, `:8000`, `:5000`), 모델 경로를
`/proc/*/cmdline`과 대조해 붙은 뒤, 내장 프롬프트 세트 — 21개의 긴 아티팩트,
이 머신이 잰 프리필이 감당할 만큼만 앞부분을 잘라 보냅니다 — 로 요청을 보내는
동안 폴트·RSS·GPU를 샘플링하고, `~/.toktape/runs/<id>.toktape`에 실행 전체를
쓰고, 공유 방법이 적힌 카드를 출력합니다. 포트도, PID도, 플래그도 없습니다.
모델이 로딩 중이면 기다립니다(`--wait`, 기본 10분).

**동시 여덟 스트림.** 에이전트 워크로드가 서버에 하는 일이 바로 이것입니다.
N이 늘면 스트림당 tok/s는 떨어지는 게 결과이고, "이 장비가 에이전트 여덟을
감당하느냐"에 답하는 숫자는 합계입니다.

```sh
toktape --sessions 8
```

**실시간으로 보기.** 스트림마다 타일 하나, 오른쪽에 머신 패널:

```sh
toktape --sessions 4 --tui
toktape --sessions 8 --tui --grid 2x2     # four tiles per page, ←/→ to page
```

**공유하거나 다시 재생하기:**

```sh
toktape card ~/.toktape/runs/<id>.toktape -o png          # 1200×675 image next to the tape
toktape card ~/.toktape/runs/<id>.toktape -o md --copy    # card + llama-bench table, on the clipboard
toktape render                                          # the newest run as a GIF
```

```sh
toktape play ~/.toktape/runs/<id>.toktape --speed 2
```

## 어떻게 측정하나

- **프리필은 머신의 속도와 고정 비용으로, 실행 전에 따로 잽니다.** 아무것도
  밀려들지 않은 상태에서 `/completion`에 날것 프롬프트 둘 — 128토큰짜리와,
  짧은 쪽이 직접 보인 비용이 허락하는 만큼 긴 것 — 을 보내 토큰당 속도와
  요청당 고정 비용으로 피팅합니다. 고정 비용이 대부분인 프리필 수치는
  throughput이 아닌데, 어느 쪽이 어느 쪽인지 말하는 것이 이 피팅입니다.
  프로브 프롬프트는 실행마다 소금(salt)으로 시작하므로 같은 따뜻한 서버를
  두 번 재도 측정이 됩니다.
- **프롬프트 세트가 계측기입니다.** 실행은 당신이 친 프롬프트를 보내지
  않습니다. 고정 세트 21개 — 진짜 버그와 통과하는 테스트가 있는 코드, 쿼리
  플랜, 장애 타임라인, ADR, 한국어와 일본어 산문 — 를 보내고, 하나하나
  18–30 KB입니다. 어떤 장비도 통째로 보내면 안 되도록 일부러 그렇게 컸습니다.
  실행은 각 프롬프트의 앞부분을, 피팅이 말하는 몫 — 고정 비용이 프리필의
  스무분의 일 이하로 묶이는 — 만큼 잘라 보내고 몇 글자를 보냈는지 기록합니다.
  올라간 기록은 세트와 대조해 검증되고, 잘린 접두어까지 같이. 슬롯의 컨텍스트가
  다음 말을 합니다. 실행의 답변 예산은 그 프롬프트를 담은 슬롯이 감당할 만큼
  깎이고, 어떤 슬롯에도 안 담기는 프롬프트는 첫 요청 전에 두 수를 붙여
  거부됩니다.
- **서버 수치가 기록이고, 클라이언트 수치는 검증입니다.** 청크마다 서버
  자신의 속도와 토큰 수가 오고, toktape는 자기 시계로 다시 계산해 2 % 안에서
  일치했는지 테이프에 남깁니다. 불일치를 더 보기 좋은 숫자로 덮는 일은
  없습니다.
- **클라이언트 속도는 콘텐츠 구간에서 잽니다** — 텍스트를 실은 첫 토큰과
  마지막 토큰 사이이지 벽시계가 아닙니다. 생성 32토큰 아래는 "디코드"가
  아니라 `Sample`입니다.
- **리즈닝 토큰도 셉니다.** `reasoning_content` 델타는 디코드 토큰이고 TTFT는
  두 종류 중 먼저 온 토큰입니다.
- **cold / warm은 폴트 수에서**, 추측에서가 아닙니다. 상주량은 프로세스의
  매핑에서 파생하고, "아직 로드되지 않음"은 GGUF 텐서 헤더에서 읽고, 라운드
  경계마다 기계의 증인(로드, IO, 페이지 캐시, 살아 있는 `llama-*` 프로세스,
  cpufreq, 온도 하나)을 남깁니다.
- **모르는 값은 `?`입니다.** 카드는 관측하지 않은 값을 절대 보여 주지 않고,
  폴트를 측정하지 못한 실행에는 cold 라벨을 붙이지 않습니다.

## 인용할 가치가 있는 숫자

카드는 찍는 숫자마다 단서를 답니다. 아래 습관들이 단서가 거의 없는 카드를
만듭니다. 대부분 플래그 하나, 혹은 아무것도 아닙니다.

- **두 번 돌리고 두 번째를 인용하세요.** 막 로딩한 모델에 대한 첫 실행은
  디코드하는 동안 디스크에서 가중치를 끌어오고, 카드가 폴트 수을 근거로
  `cold`라고 적습니다. 인용할 가치가 있는 쪽은 warm 속도입니다.
- **길이는 초로.** `--for 30s`. 같은 토큰 수는 기계마다 다른 시간을 뜻하고,
  그 시간이야말로 지금 재려던 것입니다. `--n-predict`를 직접 대는 순간 시계는
  꺼지므로, 상한을 댄 실행은 상한에 잘립니다.
- **추론 모델은 시계를 쓰며 생각합니다.** 기본 예산은 답이 시작되기도 전에
  소진될 수 있습니다. 생각을 담으려면 `--for 60s`, 추론 아닌 모델과의 동등
  비교는 `--no-think`.
- **뭔가 인용하기 전에 caveats 줄을 읽으세요.** 속도라 부르기엔 짧은 생성,
  바쁜 머신, 시계에 잘림 — 전부 글을 올리기 전에 카드에 먼저 있고 `-o json`이
  같은 목록에 심각도까지 실어 보냅니다.
- **같은 것끼리 비교하세요.** 프롬프트 세트 id, 샘플링, 엔드포인트, 엔진 빌드가
  전부 카드에 있습니다. 두 카드는 비교 가능하거나, 왜 아닌지 말합니다.
- **질문만큼의 스트림 수.** `--sessions 8`은 에이전트 여덟이 서버에 하는
  일입니다. 단일 스트림은 다른 질문에 답합니다.
- **장비를 가만히 두세요.** 컴파일 하나이면 사람들이 시험하는 대부분의 설정
  차이보다 디코드가 더 흔들립니다. 바닥이 움직였으면 카드가 `contended` /
  `conditions_changed`라고 말합니다.

## 카드에 담기는 것

모든 카드가 같은 자리에 같은 항목을 놓고, 각 항목은 논쟁 하나를 끝내기 위해
들어 있습니다. 디코드와 프리필을 섞지 않고 대기줄을 엔진의 일에서 떼어 놓는
것(`engine prefill 10732 ms · queue 22 ms`)에서 시작해, 프리픽스 캐시 히트,
프리필 피팅, 토큰당 페이지 폴트, 모델이 실제로 어디에 있나(놓인 것 대 상주한
것, weights 대 KV 대 compute, 텐서 헤더에서 읽은 미로드 바이트), 벽이 되는
버스에 대고 잰 대역폭(드래프트가 있으면 verify step 단위), 실제 보낸 샘플링,
드래프트의 수락률과 스텝 모양, 플래그 전부와 정확한 양자화, contended, 그리고
"스트림당 × N이 정말 합계인가"까지.

## 명령

| 동사 | 하는 일 | 예 |
| --- | --- | --- |
| `record` | 붙어서 실행을 녹화합니다. 기본 동사 | `toktape --sessions 4 --for 30s` |
| `card` | 테이프에서 카드를 다시 그립니다 | `toktape card <tape> -o png` |
| `play` | 라이브 화면에서 실행을 재생합니다 | `toktape play <tape> --speed 4` |
| `render` | GIF, mp4, asciicast, PNG 프레임으로 렌더합니다 | `toktape render <tape> --mp4 clip.mp4` |
| `ls` | 녹화된 실행 목록 | `toktape ls` |
| `log` | 모든 실행의 실험 장부 | `toktape log --sort decode` |
| `compare` | 두 실행의 지표와 플래그를 비교합니다 | `toktape compare a.toktape b.toktape` |
| `publish` | 실행을 올리고 링크를 찍습니다 | `toktape publish <tape>` |
| `profile` | 올릴 때마다 붙는 작성자 정보를 정합니다 | `toktape profile --name NAME --link URL --avatar FILE --bio TEXT` |
| `runs` | 올라간 실행 목록. 사이트와 같은 필터와 순서 | `toktape runs --gpu rtx-3090 --sort decode` |
| `show` | 올라간 실행 하나를 읽습니다. `--save`로 기록도 받습니다 | `toktape show <id> --save run.toktape` |
| `reindex` | 올라간 실행의 검색 행을 기록에서 다시 계산합니다. 새 toktape가 색인하는 수치가 옛 실행에도 붙습니다 | `toktape reindex <id>` |
| `version` | 버전 출력 | `toktape version` |

녹화는 시계로 끝납니다 — 기본 20초, `--for 30s`로 조준 — 같은 256토큰이
기계마다 2초일 수도 2분일 수도 있어서입니다. 64토큰 아래로는 자르지 않고,
`--n-predict`는 함께 걸리는 상한입니다(대는 순간 시계가 꺼집니다). `--prompt`
(반복 가능)나 `--prompts`(JSONL 파일, 한 줄이 한 라운드)가 내장 세트를 대체하고,
`--spec-n-max 3,5`는 세트를 speculative `n_max` 값마다 한 번씩 돌려 테이프
하나에 담습니다.

요청의 모양은 서버 플래그만큼 수치를 움직입니다. 그리디, 서버 기본 샘플링,
생각 켠 채로 — 같은 엔진에서 11 %까지 벌어집니다. 그래서 `record`가 셋 다
이름을 붙입니다: `--temp`, `--no-think`, `--endpoint chat|completion`, 그리고
그 빌드가 받는 나머지는 `--param key=value`. 보낸 것은 전부 기록되고 카드에
이름이 붙습니다.

`-o FORMAT`은 llama-bench의 `-o`를 그 단어 그대로 씁니다: `json`, `jsonl`,
`md`(펜스 안의 카드, llama-bench 표, Reproduce 블록), `csv`, `tsv`, `sql`.
`toktape log -o sql | sqlite3 runs.db`가 곧 데이터베이스입니다. 모르는 값은
터미널에서 `?`, 내보내기에서 빈 칸, sql에서 `NULL`입니다.

실행마다 테이프 옆 `runs.tsv`에 한 행이 붙으므로 스윕이 표 하나가 됩니다.
`--tag`와 `--note`로 라벨을 붙이고 장부는 언제든 다시 만들 수 있습니다:

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

클립은 화면 녹화가 아니라 테이프에서 나오고 1:1로 재생됩니다 — 달라고 하기
전까지 아무것도 압축하지 않습니다. 재생 속도를 끌어올린 클립은 이 페이지의
단 하나의 숫자에 대해 거짓말을 하는 셈이니까요. `--prefill-lead 3s`는 첫
토큰 직전에서 시작합니다:

```sh
toktape render ~/.toktape/runs/<id>.toktape
toktape render ~/.toktape/runs/<id>.toktape --mp4 clip.mp4 --cast clip.cast
```

## 에이전트에서

toktape를 실제로 돌리는 쪽은 대개 사람이 아니라 Claude Code나 Codex입니다.
그 독자를 위한 계약이 [`docs/agents.md`](docs/agents.md)와 바이너리 안의
`toktape help agents`에 있습니다 — 종료 코드 다섯, 성공이든 실패든 `-o json`이
stdout에 객체 하나, 그리고 [인용할 가치가 있는 숫자](#인용할-가치가-있는-숫자)의
습관. 읽기는 공짜이고(`toktape runs -o json`) 녹화는 아닙니다.

## 올리기

테이프는 통째로 건넬 만큼 작습니다 — 히어로가 25 KB입니다. 테이프를 가진
페이지는 나머지를 전부 그려낼 수 있고 [tape.midagedev.com](https://tape.midagedev.com)가
그것을 합니다. 링크가 곧 카드, 브라우저에서 재생되는 실행, 전문, mp4, 기록
자체입니다.

```sh
toktape publish ~/.toktape/runs/<id>.toktape --dry-run
toktape publish ~/.toktape/runs/<id>.toktape
```

`--dry-run`은 무엇이 올라갈지 정확히 보여 줍니다. 한 번은 읽어 보세요.
올라간 실행은 **공개**이고 기본적으로 **텍스트를 싣습니다** — 프롬프트와
모델이 쓴 것까지. 재 텍스트 없는 속도는 반쪽짜리 주장이기 때문입니다.
`--private`는 검색에서 빼고 `--no-text`는 텍스트를 집에 두며, 둘 다
`~/.toktape/config.toml`에서 기본으로 정할 수 있습니다. `toktape profile`은
작성자를 기계마다 한 번 정하고, `--title`/`--note`는 실행 하나에 실험 노트를
붙이고, `publish --edit <id>`는 페이지를 다시 씁니다.

올린 실행은 명령 하나로 내립니다: `toktape publish --delete <id>`. 저널
토큰이 있으면 그 토큰이 열쇠입니다. 익명으로 올렸다면 **삭제 토큰**이 한 번
출력되면서 `~/.toktape/published.json`에도 남으니 같은 명령이 그대로 통하고,
실행이 내려가면 그 항목도 같이 지워집니다.

허브 주소는 고정이 아니라 설정입니다. `~/.toktape/config.toml`에
`service = "https://tapes.example.com"`을 적으면(또는 `TOKTAPE_SERVICE`,
`--url`) 모든 명령이 직접 띄운 허브를 봅니다. 그 파일의 토큰은 옆에 적힌
서비스로만 나갑니다. 띄우는 법은 [`web/README.md`](web/README.md)에 있습니다.

사이트는 리더보드가 아니라 검색입니다. 최신순, 모든 행에 카드가 달 주의사항,
모델·양자화·엔진·GPU·호스트·VRAM 필터. 전부 터미널에서도 읽힙니다:

```sh
toktape runs --gpu rtx-3090 --sort decode      # what the site lists, as a table
toktape runs --mine -o json                     # your own runs, the API's body verbatim
toktape show <id> --save run.toktape               # one run's summary, and its record
toktape card run.toktape                           # the card, drawn locally from that record
```

호스트명과 절대 경로는 공개 여부와 무관하게 지워집니다. 서비스는 테이프를
열어 보지 않습니다. 색인 행보다 풍부한 것은 전부 이 바이너리와 같은 렌더러를
WebAssembly로 컴파일해 브라우저에서 돌린 것입니다.

## 자주 묻는 것

**`llama-bench`가 있는데 왜?** 둘 다 쓰면 됩니다. `llama-bench`는 엔진 연산을
격리해서 재고 toktape는 서버를 잽니다 — 슬롯, 프리픽스 캐시 재사용, 대기열이
찬 TTFT, 스트림 여덟이 동시에 들어올 때 벌어지는 일. 에이전트 워크로드가
실제로 겪는 숫자는 이쪽입니다.

**프롬프트가 캐시되지 않았다는 걸 어떻게 아나?** 카드에 적혀 있습니다.
`Prefix cache 0% hit (0/512)`, 그리고 폴트 수에서 나온 `cold`/`warm`. 카드를
의심하는 사람이 있으면 테이프를 보내면 됩니다. 같은 카드가 나옵니다.

**어디로 무엇을 보내나?** 아니요. llama-server와만 통신하고 `~/.toktape` 아래에
파일을 씁니다. 텔레메트리도 계정도 없습니다.

## 지원 범위

| | |
| --- | --- |
| 서버 | llama-server(상류 llama.cpp), ik_llama.cpp, `/props`에 `engine` 객체로 답하는 서버 |
| Linux | 기본 대상, x86_64·arm64, 전체 `/proc` 뷰 |
| macOS | 빌드·실행, `--url` 부착, `/proc` 뷰 없음 — 메모리·폴트 행은 `?` |
| Windows | 자체 바이너리. 서버 탐색과 `nvidia-smi` GPU 뷰는 다른 곳과 똑같이 동작하므로 `--url`은 탐색이 훑지 않는 포트에만 필요합니다. `/proc` 뷰는 없어서 메모리·폴트 행은 `?`. WSL2는 Linux 바이너리로 전체 뷰 |
| GPU | `nvidia-smi`를 통한 NVIDIA |

그 밖의 OpenAI 호환 서버 — vLLM, SGLang, TabbyAPI, LM Studio — 는 일반
모드(`--engine-kind openai`, 또는 자동 감지)로 기록됩니다. timings가 오지
않으므로 기록자의 시계가 기록이 되고, 카드가 속도 옆에 `client-timed`라고
적으며, 다른 client-timed 실행과만 비교하면 됩니다.

Ollama(포트 11434)와 LM Studio(1234)는 `--url` 없이도 찾고, 서버에 모델이
여럿이면 `--model <id>`로 고릅니다. 이런 서버는 스트림이 끝날 때 보내는
`usage` 메시지에만 토큰 수를 싣는데, 시계에 끊긴 스트림은 그 메시지를 받지
못합니다. 그래서 toktape는 짧은 요청 두 번으로 디코드와 프리필 속도를 먼저
재고, 모든 스트림이 시계 안에서 스스로 끝나도록 프롬프트 길이와 답변 상한을
정합니다. vLLM은 `max_model_len`을 컨텍스트로 읽고, 토큰 수를 청크마다
받습니다. Ollama 0.34와 vLLM 0.29에서 확인한 결과, 기본 명령은 20초 남짓에
끝나고 카드에 속도가 찍힙니다.

## `.toktape` 형식

실행 하나당 한 파일인 Gzipped JSON, 스키마 버전 1(일반 JSON도 읽습니다).
요약, 토큰별 타임스탬프와 텍스트, 표본 계열, 서버의 날것 timings, 플래그와
빌드, 배치 추정이 들어 있습니다. 읽는 쪽은 새 스키마를 추측하는 대신
거부합니다. 테이프를 공유하세요. toktape가 있는 누구나 같은 카드를 그립니다.

## 기여하기

이슈와 풀 리퀘스트를 환영합니다. 버그 리포트는 테이프를 붙여 주면 가장
쓸모가 있습니다 — 실행 하나는 `.toktape`로 완전히 기술되니까요.
`./scripts/check.sh`가 게이트입니다 — gofmt, 빌드, vet, Linux 크로스빌드,
테스트 — 그리고 CI가 정확히 그것을 돌립니다. 트리 구성과 스키마·카드 규칙은
[CONTRIBUTING.md](CONTRIBUTING.md)에 있습니다.

## 라이선스

MIT. [LICENSE](LICENSE)를 보세요.
