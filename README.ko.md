# toktape

**로컬 LLM 서빙을 위한 블랙박스 테이프.**

[English](README.md) · 한국어 · [日本語](README.ja.md)

[![check](https://github.com/midagedev/toktape/actions/workflows/ci.yml/badge.svg)](https://github.com/midagedev/toktape/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/midagedev/toktape)](https://github.com/midagedev/toktape/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/midagedev/toktape)](go.mod)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

toktape는 이미 떠 있는 llama-server에 붙어서 한 번의 실행을 `.tape` 파일로
녹화하고, 카드 한 장을 출력합니다. 모델이 어디에 올라가 있는지, 프로세스가
실제로 무엇을 건드렸는지, 요청이 정말 얼마나 빨랐는지가 그 한 장에 담깁니다.
스트림 하나든 동시에 여덟이든 같은 방식으로 기록합니다.

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape recording 4 concurrent streams, from the command being typed to the result card"></p>

<p align="center"><em>세션 전체입니다. 프롬프트에 명령을 치고, 서버를 찾아 붙고, 네 스트림이 동시에 돌고, 카드가 뜹니다. 화면 녹화가 아니라 테이프를 <code>toktape render</code>와 같은 렌더러로 다시 그린 것입니다.</em></p>

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
│ Decode        9.1 tok/s · ≈ 410 GB/s, 22% of peak                    │
│ Prefill       610 tok/s · TTFT 810 ms · 512 prompt tokens            │
│ Context       16384 (512 in / 307 out)                               │
│ Prefix cache  25% hit (128/512) · warm                               │
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

**셸 스크립트.** OS와 CPU에 맞는 릴리스 아카이브를 받아 `checksums.txt`로
검증한 뒤 바이너리 하나를 `~/.local/bin`에 넣습니다.

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

설치 위치는 `TOKTAPE_INSTALL`, 버전 고정은 `TOKTAPE_VERSION`으로 바꿉니다.
GitHub에 나갈 수 없는 장비라면 `TOKTAPE_BASE_URL`에 미러나 `file://`
디렉터리를 지정하면 됩니다. `sh` 뒤에 `-s -- --dry-run`을 붙이면 실제로 설치하지
않고 무엇을 할지만 보여 줍니다.

**Go:**

```sh
go install github.com/midagedev/toktape/cmd/toktape@latest
```

**소스 빌드** (Go 1.26, cgo 없음):

```sh
git clone https://github.com/midagedev/toktape.git
cd toktape
go build -o toktape ./cmd/toktape
```

`toktape version`으로 설치를 확인합니다. 실행 시 의존하는 것이 전혀 없는
바이너리라, 서버가 도는 장비로 `scp` 하는 것도 정식 설치 방법입니다.

**플랫폼.** Linux x86_64·arm64가 기본 대상입니다. 메모리, 페이지 폴트, 서버
플래그 행은 `/proc`에서 읽는데 이것은 서버가 도는 호스트에만 있습니다. macOS도
빌드되고 실행되며 `--url`로 원격 서버에 붙습니다. 이때 `/proc` 행은 `?`로
찍힙니다. Windows는 WSL2에서 동작합니다. GPU 행은 `nvidia-smi`에서 읽습니다.

## 바로 써 보기

llama-server가 도는 장비에서 한 단어만 칩니다.

```sh
toktape
```

1. **탐색** — `127.0.0.1:8080`, 이어서 `:8081`, `:8000`, `:5000`을 찔러
   `/props`에 답하는 첫 서버를 잡습니다.
2. **부착** — `/props`에서 모델 경로, 컨텍스트 크기, 슬롯 수를 읽고, 그 경로를
   `/proc/*/cmdline`과 대조해 서버 PID를 찾은 뒤 `/proc` 뷰를 엽니다.
3. **프롬프트** — 고정 프롬프트 세트에서 요청 하나를 `timings_per_token`과
   `return_progress`를 켜서 보내고, 답이 스트리밍되는 동안 메이저 폴트, RSS,
   GPU 상태를 샘플링합니다.
4. **테이프** — 실행 전체를 `~/.toktape/runs/<id>.tape`에 씁니다.
5. **카드** — 72칸 카드를 출력하고 테이프 옆에 저장한 뒤, 공유 방법을 한 줄로
   알려 줍니다.

포트도, PID도, 외워야 할 플래그도 없습니다. 모델이 아직 로딩 중이면 기다립니다
(`--wait`, 기본 10분).

**동시 여덟 스트림.** 에이전트 워크로드가 서버에 하는 일이 바로 이것입니다.

```sh
toktape -n 8
```

N이 늘면 스트림당 tok/s는 떨어지는 게 정상입니다. "이 장비가 에이전트 여덟을
감당하느냐"에 답하는 숫자는 합계이고, 단일 스트림 벤치마크로는 이 숫자를
볼 수 없습니다.

**실시간으로 보기.** 스트림마다 타일 하나, 오른쪽에 머신 패널입니다.

```sh
toktape -n 4 --tui
toktape -n 8 --tui --grid 2x2     # four tiles per page, ←/→ to page
```

**공유하기:**

```sh
toktape card ~/.toktape/runs/<id>.tape --png           # 1200×675 image next to the tape
toktape card ~/.toktape/runs/<id>.tape --md --copy     # card + llama-bench table, on the clipboard
toktape render                                          # the newest run as a GIF
```

**다시 재생하기.** 라이브 화면에서 원하는 속도로 돌려 봅니다.

```sh
toktape play ~/.toktape/runs/<id>.tape --speed 2
```

## 카드에 담기는 것

모든 카드가 같은 자리에 같은 항목을 놓습니다. 두 장을 나란히 두고 읽을 수
있어야 하기 때문입니다. 각 항목은 논쟁 하나를 끝내기 위해 들어 있습니다.

- **디코드와 프리필을 섞지 않습니다.** 프리필은 연산 바운드, 디코드는 메모리
  대역폭 바운드라서 "45 tok/s" 하나로는 아무것도 알 수 없습니다. TTFT, 프롬프트
  tok/s, 디코드 tok/s를 각각 토큰 수와 함께 찍습니다.
- **프리픽스 캐시 히트.** `0% hit (0/512)` 또는 `78% hit (400/512)`. 시스템
  프롬프트가 한 글자만 달라도 캐시를 놓치고, 프리필이 이유 없이 열 배 느려지거나
  빨라져 보입니다.
- **cold / warm.** mmap된 모델에 첫 프롬프트를 보내면 NVMe에서 4 KiB 페이지
  수천 장을 끌어오느라 정상 속도의 몇 분의 일이 나옵니다. 이 라벨은 추측이
  아니라 디코드 중 실제로 발생한 메이저 폴트 수로 정합니다.
- **토큰당 페이지 폴트.** "멈췄다가 다시 간다"를 설명하는 단 하나의 숫자입니다.
  라이브 화면에서는 토큰과 같은 시간축 위에 그려집니다.
- **RSS는 "로드됨"이 아닙니다.** mmap 아래에서 상주 메모리는 프로세스가
  건드린 만큼입니다. 카드는 호스트 RSS를 file/anon으로, VRAM을 weights/KV/
  compute로 나누고, 아직 로드되지 않은 바이트를 파일 크기에서 RSS를 빼는 대신
  GGUF 텐서 헤더에서 계산합니다.
- **플래그 전부.** `-ngl -fa -b -ub -ctk -ctv --load-mode -ot`. 플래시 어텐션
  여부와 배치 크기, 이 둘이 빠지면 결과 글이 댓글 오십 개짜리 스레드가 됩니다.
- **정확한 양자화.** `UD-Q4_K_M`을 "Q4"로 줄이지 않습니다.
- **contended.** 같은 장비의 다른 프로세스는 사람들이 시험하는 대부분의 변경보다
  디코드 속도를 더 크게 흔듭니다. 카드는 로드 애버리지와 다른 GPU 프로세스를
  읽어 실행에 라벨을 붙입니다.
- **스트림.** 스트림당 속도 × N = 합계를 카드에 그대로 적고, TTFT p50·p95와
  동시에 바빴던 최대 슬롯 수를 함께 둡니다.

## 명령

| 동사 | 하는 일 | 예 |
| --- | --- | --- |
| `record` | 붙어서 실행을 녹화합니다. 기본 동사 | `toktape -n 4 --n-predict 512` |
| `card` | 테이프에서 카드를 다시 그립니다 | `toktape card <tape> --png` |
| `play` | 라이브 화면에서 실행을 재생합니다 | `toktape play <tape> --speed 4` |
| `render` | GIF, mp4, asciicast, PNG 프레임으로 렌더합니다 | `toktape render <tape> --mp4 clip.mp4` |
| `ls` | 녹화된 실행 목록 | `toktape ls` |
| `log` | 모든 실행의 실험 장부 | `toktape log --sort decode` |
| `compare` | 두 실행의 지표와 플래그를 비교합니다 | `toktape compare a.tape b.tape` |
| `version` | 버전 출력 | `toktape version` |

**record:** `--url`(기본은 자동 탐색), `-n`/`--concurrency`, `--prompt`(반복
가능, `-n`만큼 순환), `--prompts`(JSONL 파일. 한 줄이 `-n`개 스트림의
한 라운드이고, 순서대로 돌려 테이프 하나에 담습니다), `--n-predict`(기본 256), `--out`(기본
`~/.toktape/runs`), `--tag`, `--note`, `--wait`, `--tui`,
`--grid COLSxROWS`(기본 `2x4`, `0`이면 터미널에 맞춤), `--no-card`,
`--json`, `--quiet`.

**card:** `--md`(펜스 안의 카드와 llama-bench 호환 표), `--json`(실행 요약),
`--png [FILE]`(1200×675 공유 이미지), `--copy`(OSC 52로 클립보드에도 복사).
`--md`와 `--json`은 둘 중 하나만 씁니다.

**render:** `--gif FILE`, `--mp4 FILE`(`PATH`에 ffmpeg 필요), `--cast FILE`
(asciicast v2), `--frames DIR`(PNG 시퀀스), `--duration`, `--fps`,
`--size WxH`. 여러 출력을 한 번에 지정하면 같은 프레임에서 함께 나옵니다.
테이프를 지정하지 않으면 가장 최근 실행을 씁니다.

**log:** `--sort`, `--model`, `--tag`, `-n`, `--tsv`, `--csv`, `--json`,
`--md`, `--rebuild`, `--out`.

## 실험 장부

실행마다 테이프 옆 `runs.tsv`에 한 행이 붙습니다. 파라미터 스윕이 카드
폴더가 아니라 표 하나가 됩니다. `--tag`와 `--note`로 실행에 라벨을 붙여 두면
둘 다 테이프에 저장되므로 장부는 언제든 다시 만들 수 있습니다.

```sh
toktape --tag ngl=40 --note "fa on"     # record, labelled
toktape log --sort decode                # which setting won
toktape log --tag ngl --md               # paste into an issue
toktape log --rebuild                    # regenerate from the tapes
```

모르는 값은 터미널에서 `?`, 내보내기에서는 빈 칸입니다. 숫자 열이 숫자로
들어옵니다.

```sh
sqlite3 runs.db ".import --tsv ~/.toktape/runs/runs.tsv runs"
duckdb -c "select tag, decode_tok_s from read_csv('~/.toktape/runs/runs.tsv')"
```

## 클립

이 페이지 맨 위의 이미지는 화면 녹화가 아닙니다. 테이프를 프레임 단위로 다시
그린 것이고, 여러분의 테이프도 똑같이 렌더됩니다.

```sh
toktape render ~/.toktape/runs/<id>.tape
toktape render ~/.toktape/runs/<id>.tape --mp4 clip.mp4 --cast clip.cast
```

클립은 명령을 치는 장면으로 열리고, 서버에 붙고, 실행을 실제 속도로 재생한 뒤
(30초를 넘는 실행은 압축), 카드에서 멈춥니다. 모든 프레임이 클립 시간의 순수
함수라서 같은 테이프는 늘 같은 클립이 됩니다.

## 어떻게 측정하나

- **서버 수치가 기록이고, 클라이언트 수치는 검증입니다.** 요청에
  `timings_per_token`을 켜므로 청크마다 서버 자신의 `prompt_per_second`,
  `predicted_per_second`, 토큰 수가 옵니다. toktape는 자기 시계로 같은 속도를
  다시 계산해 둘 다 보관하고, 2 % 안에서 일치했는지를 테이프에 남깁니다.
  불일치를 더 보기 좋은 숫자를 골라서 덮는 일은 없습니다.
- **클라이언트 속도는 콘텐츠 구간에서 잽니다.** 텍스트를 실은 첫 토큰부터 마지막
  토큰까지의 간격이지, 요청 발송부터 소켓 종료까지의 벽시계 시간이 아닙니다.
- **생성 토큰 32개를 넘어야 "디코드"라고 부릅니다.** 그 아래는 카드에 `Sample`로
  적습니다.
- **리즈닝 토큰도 셉니다.** 씽킹 모델의 `reasoning_content` 델타는 디코드
  토큰으로 기록되고 라이브 화면에서 흐리게 표시됩니다. TTFT는 두 종류 중 먼저
  온 토큰입니다.
- **"아직 로드되지 않음"은 GGUF 텐서 헤더에서** 텐서 종류별로 읽습니다.
- **PID가 없으면**(원격 서버, 들여다볼 수 없는 컨테이너) 속도, 프리픽스 캐시
  히트, GPU 상태는 그대로 기록됩니다. 호스트 RSS, 페이지 폴트, 플래그는 `?`로
  찍히고 카드에 `/proc` 뷰를 쓸 수 없었다고 적힙니다. 폴트를 측정하지 못한
  실행에는 cold 라벨을 붙이지 않습니다.
- **GPU가 없으면** VRAM과 온도 행이 `?`가 되고 이유가 카드에 적힙니다.
- 모르는 값은 `?`입니다. 카드는 관측하지 않은 값을 절대 보여 주지 않습니다.

## 자주 묻는 것

**`llama-bench`가 있는데 왜?** 둘 다 쓰면 됩니다. `llama-bench`는 엔진 연산을
격리해서 재는 도구고, 그 용도로는 그것이 맞습니다. toktape는 서버를 잽니다.
HTTP 슬롯, 프리픽스 캐시 재사용, 대기열이 찬 상태의 TTFT, 스트림 넷이나 여덟이
동시에 들어올 때 벌어지는 일. 에이전트 워크로드가 실제로 겪는 숫자는 이쪽입니다.

**프롬프트가 캐시되지 않았다는 걸 어떻게 아나?** 카드에 적혀 있습니다.
`Prefix cache 0% hit (0/512)`, 그리고 폴트 수에서 나온 `cold`/`warm`. 카드를
의심하는 사람이 있으면 테이프를 보내면 됩니다. 같은 카드가 나옵니다.

**어디로 무엇을 보내나?** 아니요. llama-server와만 통신하고 `~/.toktape` 아래에
파일을 씁니다. 텔레메트리도 계정도 없습니다.

## 지원 범위

| | |
| --- | --- |
| 서버 | llama-server(upstream llama.cpp), ik_llama.cpp |
| Linux | 기본 대상, x86_64·arm64, `/proc` 뷰 전체 |
| macOS | 빌드·실행 가능, `--url`로 부착. `/proc` 뷰가 없어 메모리·폴트 행은 `?` |
| Windows | WSL2를 통해 |
| GPU | `nvidia-smi`를 통한 NVIDIA |

로드맵: sudo 없는 macOS 수집기, `/api/ps` 기반 Ollama 오프로드 카드,
`toktape ab URL1 URL2`(서버 둘, 프롬프트 하나, 나란히).

## `.tape` 형식

gzip된 JSON, 실행당 파일 하나, 스키마 버전 1. 평문 JSON도 읽으므로 `gunzip`
뒤에도 grep이 됩니다. 카드를 그리는 실행 요약, 토큰별 타임스탬프와 텍스트, 샘플
시계열, 서버의 원본 타이밍, 플래그와 빌드, 배치 추정이 들어 있습니다. 더 새로운
스키마로 쓰인 테이프는 추측하지 않고 거부합니다. 모든 렌더러는 테이프만 읽으므로
카드는 파일의 순수 함수입니다. 테이프를 공유하세요. toktape가 있는 누구나 같은
카드를 그립니다.

## 기여하기

이슈와 풀 리퀘스트를 환영합니다. 버그 리포트에는 테이프를 붙여 주시면 가장
좋습니다. 실행은 `.tape` 하나로 완전히 기술되므로 "이런 카드가 나왔다"와 "파일은
이것이다"가 같은 말입니다.

게이트는 `./scripts/check.sh`입니다. gofmt, 빌드, vet, Linux 크로스 빌드,
테스트를 돌리고 CI도 정확히 같은 스크립트를 실행합니다. 트리 구조, 골든 파일
갱신법, 스키마와 카드가 따르는 규칙은 [CONTRIBUTING.md](CONTRIBUTING.md)
(영문)에 있습니다. 설계 문서는 [docs/toktape-spec.ko.md](docs/toktape-spec.ko.md)
입니다.

## 라이선스

MIT. [LICENSE](LICENSE)를 보세요.
