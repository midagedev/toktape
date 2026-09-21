# toktape

**로컬 LLM 서빙을 위한 블랙박스 테이프.**

[English](README.md) · 한국어 · [日本語](README.ja.md) · [简体中文](README.zh-CN.md) · [繁體中文](README.zh-TW.md)

[![check](https://github.com/midagedev/toktape/actions/workflows/ci.yml/badge.svg)](https://github.com/midagedev/toktape/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/midagedev/toktape)](https://github.com/midagedev/toktape/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/midagedev/toktape)](go.mod)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

<img src="docs/mascot.png" align="right" width="150" alt="the toktape mascot: a small chibi in headphones, eyes closed, hugging a cassette tape">

toktape는 이미 떠 있는 로컬 LLM 서버(llama-server, Ollama, vLLM, LM Studio)에
붙어서 한 번의 실행을 `.toktape` 파일로 녹화하고, 카드 한 장을 출력합니다.
프롬프트가 캐시됐는지, 스트림이 몇 개였는지, 양자화가 정확히 무엇인지, 모델이
어디에 올라가 있는지, 그 기계에서 다른 게 돌고 있었는지 — 벤치마크 글마다
되풀이되는 질문에 그 한 장이 답합니다. 정적 바이너리 하나, MIT, 텔레메트리도
계정도 없습니다.

<p align="center"><img src="assets/hero.gif" width="800" alt="toktape recording four concurrent streams of a 35B sparse MoE, from the command being typed to the result"></p>

<p align="center"><em>연출이 아니라 1:1로 재생한 실제 실행입니다. <code>toktape --sessions 4</code>, 네 스트림이 각각 37.3 tok/s. 화면 녹화가 아니라 <code>assets/hero.tape</code>를 다시 그린 것이고, 아래 카드는 같은 파일에서 <code>toktape card assets/hero.tape</code>로 나옵니다.</em></p>

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

## 설치

```sh
brew install midagedev/tap/toktape
```

```sh
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
```

Windows용 zip, `go install`, 소스 빌드, 그리고 OS마다 무엇이 보이는지는
[docs/install.md](docs/install.md)에 있습니다.

## 사용

서버가 도는 기계에서 한 단어만 치면 됩니다.

```sh
toktape
```

서버를 찾아 내장 프롬프트를 20초 동안 보내고, `~/.toktape/runs/<id>.toktape`를
쓴 뒤 카드를 출력합니다. 포트도, PID도, 플래그도 필요 없습니다.

동시에 여덟 스트림 — 에이전트 워크로드가 서버에 하는 일입니다 — 그리고 같은
실행을 실시간으로 보기:

```sh
toktape --sessions 8
toktape --sessions 4 --tui
```

이미지로, Markdown으로, 클립으로, 링크로 공유합니다.

```sh
toktape card ~/.toktape/runs/<id>.toktape -o png
toktape card ~/.toktape/runs/<id>.toktape -o md --copy
toktape render
toktape publish ~/.toktape/runs/<id>.toktape --dry-run
```

올린 실행은 공개되고 텍스트도 함께 실립니다. `--dry-run`이 무엇이 올라갈지
그대로 보여 줍니다. 올라온 실행은
[tape.midagedev.com](https://tape.midagedev.com)에서 검색할 수 있습니다.

## 문서

세부 문서는 영어입니다.

- [어떻게 측정하고 카드를 어떻게 읽나](docs/measurement.md) — 숫자 하나하나의
  뜻, 인용할 만한 숫자를 얻는 습관, FAQ, 파일 형식
- [명령](docs/commands.md) — 모든 동사, 출력 형식, 실행 장부, 클립 렌더
- [올리기](docs/publish.md) — 무엇이 올라가는지, 내리는 법, 허브 직접 운영
- [설치와 플랫폼](docs/install.md) — 모든 설치 경로, 지원 서버, Linux·macOS·Windows에서
  각각 보이는 것
- [에이전트용](docs/agents.md) — Claude Code나 Codex가 toktape를 대신 돌릴 때의
  계약. `toktape help agents`로도 볼 수 있습니다

## 기여하기

이슈와 풀 리퀘스트를 환영합니다. 버그 리포트에는 테이프를 첨부해 주시면 가장
도움이 됩니다. 게이트는 `./scripts/check.sh`이고, 자세한 것은
[CONTRIBUTING.md](CONTRIBUTING.md)에 있습니다.

## 라이선스

MIT. [LICENSE](LICENSE)를 보세요.
