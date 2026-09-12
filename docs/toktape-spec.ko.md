# toktape 설계·스펙 문서

*2026-09-13. 인계 문서 `docs/research/00-handover-brief.md`(rig-log에서 측정한 교훈)와 같은 날 Gemini로 돌린 조사 세 편(`docs/research/01~03`)을 바탕으로 리드가 정리했다. 조사 보고의 별 개수·업보트 수는 2026-09-13 기준이고 한 달이면 낡는다. 결정은 사용자가 "toktape가자 이 방향으로"로 수용한 것이며, 아래 §1이 그 방향의 원문이다.*

## 0. 한 줄 정의

**toktape는 로컬 LLM 서빙의 블랙박스 테이프다.** 돌고 있는 llama-server에 붙어 프롬프트 하나를 보내고, 그 한 번의 실행에서 모델이 어디에 놓였는지(VRAM·RAM·NVMe), 프로세스가 실제로 뭘 만졌는지(RSS·매핑·미적재·토큰당 major fault), 요청이 얼마나 빨랐는지(prompt/decode tok/s·TTFT·캐시 히트), 그리고 토큰 텍스트 자체를 한 화면에 보여 준다. 실행은 전부 `.tape` 런 파일로 남고, 그 파일에서 결과 카드·GIF/mp4·비교 diff를 몇 번이든 다시 뽑는다.

세 목표와 각각의 한 줄 해법:

| 목표 | 해법 |
|---|---|
| 누구나 자기 리그의 머니샷을 쉽게 | 녹화 산술(VHS·fps·sleep)을 사용자에게 시키지 않는다. 런 파일에서 헤드리스로 렌더한다 |
| 바이럴이 쉽게 | 터미널 미학이 곧 신뢰의 신호다. 고정 레이아웃의 결과 카드가 GIF보다 오래 산다 |
| 실제 튜닝에 유용 | 비교가 일급 기능이다. `ab`(두 서버 동시)와 `compare`(두 런 사후 diff) |

## 1. 이미 내린 결정

이전 세션(tokenlab 디렉터리)에서 내려졌고 디스크에는 없던 결정이다. 여기가 원본이다.

1. **언어는 Go, 화면은 TUI.** 정적 바이너리 하나를 scp 하면 끝나야 "누구나"의 절반(설치 비용)이 사라진다. charm 생태계(bubbletea·lipgloss)가 east-asian width를 처리하고, VHS도 같은 집안이라 폴백 녹화 경로가 자연스럽다. GGUF 헤더 파서와 배치 추정(gpustack/gguf-parser-go), nvml 바인딩(NVIDIA/go-nvml, cgo 없이 dlopen)이 이미 Go에 있다.
2. **제품의 본체는 UI가 아니라 런 파일이다.** 런 중에 토큰별 타임스탬프와 샘플을 전부 기록하고, 카드·GIF·mp4·리포트는 그 파일에서 사후에 렌더한다. 남의 런 파일도 같은 룩으로 다시 그릴 수 있어야 비교 문화가 생긴다.
3. **웹은 조작 UI가 아니라 산출물이다.** v1에는 서버 띄우는 웹 앱이 없다. 여러 런을 훑는 용도는 런 파일에서 뽑는 단일 `report.html`로 충분하다.
4. **카드가 바이럴의 단위다.** Speedtest 결과 카드처럼 한 눈에 같은 도구인 걸 알아보는 고정 레이아웃. 텍스트판(Reddit 코드블록)·PNG(X)·JSON(비교)이 같은 데이터에서 나온다.
5. **헤드리스 렌더는 asciicast 경유가 기본.** 런 파일 → asciicast v2 → agg(GIF) 또는 Go에서 직접 프레임 PNG → ffmpeg(mp4). 프레임 수와 길이는 토큰 수와 속도에서 유도한다. VHS는 남의 테이프 호환용 폴백이다.
6. **토큰별 타임라인은 서버 수치로 그린다.** llama-server 요청에 `timings_per_token: true`를 주면 매 청크에 timings가 실린다. 클라이언트 벽시계는 교차 검증용이다.
7. **이름은 toktape.** 동명 GitHub 저장소 0, `.dev`·`.com` 비어 있음(2026-09-13 조회). `record / play / card / ab / compare` 동사가 이름에서 바로 나온다. tokenlab은 검색이 크립토·상용 API로 새서 버렸다.
8. **구현 순서**: ① 런 파일 스키마와 레코더 → ② 카드 렌더 → ③ TUI live 화면(v41-demo 패리티가 첫 게이트) → ④ 헤드리스 GIF/mp4 → ⑤ A/B·compare·HTML 리포트.
9. **동시 세션은 처음부터 일급이다** (사용자 지시 2026-09-13 "멀티 세션을 감당해내는 것도 트렌드, 처음부터 고려"). 에이전트 워크로드는 서버를 여러 세션이 동시에 때린다. 런은 "요청 하나"가 아니라 "동시 요청 N개 + 집계"이고, N=1이 특수 사례다. 스키마(`tape.Tape.Requests`, `Summary.Concurrency`, `Summary.Aggregate`), TUI(스트림 N개 표시), 카드("N × 스트림당 tok/s = 합계 tok/s", TTFT p50/p95, 슬롯 점유 최대), CLI(`--concurrency N`, 기본 프롬프트 세트)가 모두 이 형태를 따른다. 머니샷 후보: "내 리그가 에이전트 8개를 동시에 몇 tok/s로 받아내는가".

10. **시각 품질이 제품이다** (사용자 지시 2026-09-13 "얼마나 고져스할지가.. 최소 crush 이상이면서 촌스럽지는 않아야"). TUI·PNG 카드·클립은 charmbracelet/crush 이상의 완성도를 기준으로 삼고, 모든 영역이 살아 숨쉰다(막대 이징, 커서 호흡, 스파크라인 스크롤, prefill 스피너, 활성 스트림 강조). 단 결정 10과 충돌하지 않게 **애니메이션은 벽시계가 아니라 클립 시간 t의 순수 함수**다 — 재생·GIF에서 그대로 재현된다. 시각 트랙이 끝날 때마다 리드가 실제 렌더 이미지를 직접 보며 조정하는 E2E 루프를 여러 차례 돈다.

**북극성**(사용자 지시 2026-09-13): 모든 결정은 "머니샷을 LocalLLaMA 커뮤니티에 올렸을 때 당장 써보고 싶어져야 하고, 실제로 쉽게 써서 결과물이 나와야 한다"로 잰다. 열린 질문의 답도 이 기준으로 리드가 정했다(§6).

## 2. 조사가 말하는 것 — 왜 사람들이 쓰고 싶어할 것인가

세 조사가 독립적으로 같은 곳을 가리킨다. 요약하면 **"왜 내 decode가 느린가"에 답하는 도구가 없다**는 것이다.

**수요 쪽(`01-demand-signals.md`)**. 2026년의 로컬 서빙은 "VRAM에 들어가나"의 이분법이 아니라 GPU·RAM·NVMe 3층 배치의 공학이 됐다. 가장 많이 반복되는 질문과 그 근거는 이렇다.

- **페이지 폴트로 인한 멈춤.** DeepSeek R1 671B를 64 GB RAM에서 mmap으로 돌린 글(1,332 업보트)이 촉발한 혼란: VIRT 400 GB를 보고 "RAM에 다 올렸다"고 믿고, 문장 중간에 얼어붙는 이유를 모른다. 토큰당 major fault 스파크라인이 이 질문에 한 눈에 답한다. 인계 문서도 "클립에서 가장 많이 본 것"이 이 스파크라인이라고 적었다.
- **VRAM 잠식.** `-b 4096 -ub 4096`이 컴퓨트 버퍼로 VRAM을 먹어 `--fit on`이 expert 레이어를 조용히 RAM으로 내렸고 68 → 38 tok/s가 됐다(RTX 5080 실험 글, 592 업보트). 지금 도구는 "VRAM 95%"만 보여 준다. 가중치·KV·컴퓨트 버퍼를 나눠 보여야 한다.
- **prefill과 decode의 혼동.** 전체 시간을 출력 토큰으로 나눠 올린 잘못된 벤치가 흔하다. TTFT·prompt tok/s·decode tok/s를 분리하는 것 자체가 기능이다.
- **캐시 히트 불명.** 시스템 프롬프트 한 글자 차이로 prefix 캐시가 미스 나도 사용자는 모른다. 히트 비율 배지가 필요하다.
- **투기 디코딩·MTP 수락률.** Qwen3.6 35B MTP로 12 GB에서 80 tok/s를 낸 글(674 업보트)의 핵심 고민은 draft 수락률이 보이지 않는다는 것이다. 수락률이 40~50% 아래면 오히려 느려진다.
- **A/B.** 양자화·컨텍스트·스레드 수를 바꿔 가며 손으로 표를 만드는 사람들(club-3090, 2,229 스타). `compare`가 이걸 대신한다.

**산출물 쪽(`02-sharing-artifacts.md`)**. 지금 커뮤니티가 쓰는 증거는 셋이다: llama-bench markdown 표, nvidia-smi 스크린샷, `ollama ps` 덤프. 각각 한 조각씩만 증명한다. llama-bench는 서버 HTTP 스택·연속 배칭·prefix 캐시를 우회하고 프로세스 메모리를 모르며, 업스트림 PR들이 VRAM/RAM 열(#23208), 유효 대역폭 열(#28459), TTFT/ITL(#15643)을 따로따로 덧붙이고 있다. 즉 사람들은 이미 llama-bench 표에 toktape 카드의 필드를 하나씩 끼워 넣으려 하고 있다. 논쟁을 부르는 누락 5종은 pp/tg 미분리, Flash Attention 상태 누락, cold/warm 캐시 미표기, prefix 캐시 히트 미표기, `-b/-ub` 누락이다. 카드가 이 다섯을 항상 인쇄하면 댓글 50개짜리 싸움이 사라진다.

**범위 쪽(`03-audience-and-scope.md`)**. Ollama가 사용자 수는 압도적(스타 18만, Docker pull 1.7억)이지만 배치와 `/proc` 그림이 빈약해 카드가 심심해진다. 튜닝 담론의 진앙은 r/LocalLLaMA(82만 구독, r/ollama의 6배)이고 거기서 플래그를 만지는 사람은 llama-server와 ik_llama.cpp를 쓴다. Apple Silicon은 별도의 큰 바이럴 벡터다(M5 Ultra 512 GB 글 1,651 업보트, "5090이 5090달러" 글 1,640 업보트). `sudo powermetrics` 없이 IOReport로 읽는 선례(macmon·btop)가 있고 `task_vm_info.pageins`가 majflt의 짝이다. Windows 튜너는 대부분 WSL2라 `/proc` 의미론이 그대로 통한다.

## 3. 스펙 — 우선순위 붙인 기능 목록

각 항목은 "왜 원하는가(근거)"와 "어떻게 구현하는가(훅)"를 함께 적는다. 근거가 없는 기능은 넣지 않았다. llama-server 필드명(`timings_per_token`, `return_progress`/`prompt_progress`의 `total`·`cache`·`processed`·`time_ms`, `timings.cache_n`, `/apply-template`)은 2026-09-13 업스트림 `tools/server/README.md`에서 확인했다.

### 3.1 Must (v1에 있어야 물건이 성립한다)

| # | 기능 | 왜 원하는가 | 기술 훅 |
|---|---|---|---|
| M1 | **URL로 attach.** 돌고 있는 llama-server / ik_llama.cpp에 붙는다. 같은 호스트면 PID를 찾아 `/proc` 뷰를 켜고, 원격이면 SSH 사이드카 | 인계 문서 Must. 데모가 워크스테이션 위에서 도는 이유 | `GET /props`로 `model_path`·`n_ctx`·slots. 포트 8080 자동 탐색. PID는 `/proc/*/cmdline`에서 model_path 매칭 |
| M2 | **프롬프트 전송과 토큰 스트리밍 채팅 패널.** 렌더된 프롬프트를 요청 시 보여 준다 | 인계 교훈 4: `reasoning_effort`·템플릿이 60 vs 4,000 토큰을 가른다. 뭘 보냈는지 화면에 있어야 한다 | `POST /completion` SSE, `timings_per_token: true`, `return_progress: true`. `/apply-template`로 렌더 프롬프트·`</think>` 유무 확인 |
| M3 | **세 속도의 분리 표시.** TTFT · prompt tok/s · decode tok/s, 여기에 prefix 캐시 히트(재사용 토큰 / 전체) | 조사 01 §4 "혼합 지표의 함정", 조사 02 논쟁 5종의 1·4번 | 서버 `timings`(`prompt_n`·`prompt_per_second`·`predicted_n`·`predicted_per_second`)를 원본으로, 클라이언트 계산은 2% 내 교차 검증(인계 교훈 1). `cache_n`·`prompt_progress.cache`로 히트. 토큰 수 미달이면 "decode"라고 부르지 않는다(교훈 2) |
| M4 | **토큰당 major fault 스파크라인.** 토큰 텍스트와 같은 시간축 | 조사 01 순위 1, 인계 문서 "클립에서 가장 많이 본 것" | Linux `/proc/<pid>/stat` 12번 필드(majflt) 델타를 토큰 도착마다 샘플. macOS `task_vm_info.pageins` |
| M5 | **프로세스 메모리 실사.** VIRT / RSS(File·Anon·Shmem) / 매핑됐지만 미적재 / swap | 조사 01 순위 7 "mmap 착시", 인계 교훈 3 "RSS는 loaded가 아니다" | `/proc/<pid>/status`(VmRSS·RssFile·RssAnon·RssShmem)와 `smaps_rollup`. "미적재"는 총량-RSS가 아니라 GGUF 텐서 헤더에서 계산(교훈 3, 50 GB 과대평가 사례) |
| M6 | **텐서 배치 요약.** 디바이스별 GB와 어떤 레이어·텐서 클래스(attention / experts / embeddings / n-gram 테이블)가 어디 있는지 | 조사 01 순위 2·3, ik_llama.cpp의 `-cmoe`/`-ncmoe`, `-ot` 사용자는 배치가 안 보이면 시행착오뿐 | gguf-parser-go로 텐서 크기·분류. 서버 기동 로그의 `-ot`·`-ngl`·`--fit` 인자와 대조. VRAM은 가중치 / KV 캐시 / 컴퓨트 버퍼 3분할(조사 01 순위 3) |
| M7 | **호스트 상태.** GPU별 VRAM·온도·전력·클럭, CPU load·스레드 수, 다른 GPU 프로세스 유무 → **contended: yes/no** 라벨 | 인계 교훈 6 "바쁜 머신의 숫자는 무효", 조사 01 §4 열 스로틀 왜곡 | go-nvml(dlopen, cgo 없음), `nvidia-smi` CSV 폴백. loadavg·`nvmlDeviceGetComputeRunningProcesses` |
| M8 | **런 파일 `.tape`.** 모든 실행을 기본으로 기록. 하드웨어 프로필·서버 빌드와 플래그·모델과 양자화·토큰별 타임스탬프와 텍스트·샘플 시계열·서버 timings 원본 | §1 결정 2. 조사 03 순위 10 | JSON(gzip). 카드는 `RunSummary`만으로 렌더 가능해야 하고, 재생·GIF는 `RunSample[]`을 쓴다. 인계 문서의 8교훈을 필드와 상수로 박는다 |
| M9 | **결과 카드.** 텍스트(72열, Reddit 코드블록)·PNG(1200×675)·JSON을 같은 데이터에서. 레이아웃은 §4 | §1 결정 4. 조사 02 §2·§5 | Go에서 직접 그린다(fogleman/gg 또는 tdewolff/canvas). 폰트는 바이너리에 번들(D2Coding OFL — 한글 정확히 2배 폭, 교훈 5) |
| M10 | **결정적 렌더.** 타이머 애니메이션 없이, 이벤트(토큰·샘플) 기준으로만 화면이 바뀐다 | 인계 문서 Must. 녹화기가 재현 못 하는 움직임을 만들지 않는다 | 화면 = f(런 파일 상태). live와 replay가 같은 렌더 함수를 쓴다 |

### 3.2 Should (v1 안에 넣고 싶고, 없으면 "튜닝 도구"라는 세 번째 목표가 약해진다)

| # | 기능 | 왜 원하는가 | 기술 훅 |
|---|---|---|---|
| S1 | **`compare run1.tape run2.tape`.** TTFT·두 tok/s·VRAM·majflt·캐시 히트의 델타를 나란히 | 조사 01 순위 8, 조사 02 순위 8(메타데이터 knob 하나 바꿔 서버를 돌려야 하는 고통) | 두 `RunSummary`의 diff. 플래그 diff도 같이 인쇄 |
| S2 | **`ab URL1 URL2 "prompt"`.** 두 서버에 같은 프롬프트, 화면 2분할 | 인계 교훈 8: `-ngl 0` 두 번째 서버는 페이지 캐시를 공유해 공짜다 | attach 두 개를 동시에. 두 런 파일이 나오고 S1로 이어진다 |
| S3 | **토큰별 지연 스트립.** 각 토큰 폭 = 그 토큰의 지연. 멈춤이 틈으로 보인다 | 인계 Should, 조사 01 순위 1의 시각화 | `timings_per_token`의 `predicted_ms` 델타. M4 스파크라인과 같은 시간축 |
| S4 | **cold / warm 라벨.** 런 중 majflt 합계와 `/slots` 캐시 상태로 판정해 카드에 인쇄 | 조사 02 논쟁 3번, 인계 교훈 2 | majflt > 임계면 COLD, prefix 히트 100%면 WARM(cached). 임계값은 실측 후 상수로 |
| S5 | **투기 디코딩 / MTP 수락률.** draft 수락 토큰 / 전체, 순 가속 배율 | 조사 01 순위 6 | 서버 timings의 draft 필드(`draft_n`·`draft_n_accepted`)가 있으면 표시, 없으면 패널 숨김. **확인 필요**: 이 두 필드는 서버 README(2026-09-13 확인)에 문서화돼 있지 않다. 구현 전 `tools/server` 소스의 timings 직렬화에서 실제 이름을 확인할 것 |
| S6 | **유효 대역폭.** decode tok/s × 활성 가중치 크기 = GB/s, 하드웨어 대역폭 대비 % | 조사 02 순위 3(llama-bench `--bandwidth` PR) | MoE는 활성 expert만 센다. 대역폭 상한은 GPU 스펙 테이블 + RAM 채널 추정. 추정치는 "≈"로 표시 |
| S7 | **`/metrics`·`/slots` 읽기.** 켜져 있으면 쓰고 아니면 스트림 timings로 폴백 | 인계 Should | Prometheus 텍스트 파싱. slot별 `n_ctx`·`cache_tokens` |
| S8 | **런 히스토리.** `~/.toktape/runs/`에 자동 저장, `toktape ls` | 조사 02 §8 "Day 1 산출물 셋" | 파일명 = 날짜-모델-짧은해시 |
| S9 | **헤드리스 replay → GIF/mp4.** `toktape render run.tape --gif --mp4` | §1 결정 5. 조사 02 §3 파라미터 | 런 파일 → asciicast v2 → agg, 또는 Go 프레임 PNG → ffmpeg. 30 fps, 10~12초, 96색 팔레트, 1200×675 |
| S10 | **완료 후 공유 안내.** "카드 저장됨, [C] Reddit용 복사 [P] PNG 열기 [A] A/B" | 조사 02 §4 네 번째 기둥 | 클립보드는 OSC 52 + 폴백 |

### 3.3 Could (v1 이후, 근거는 있지만 지금 넣으면 범위가 샌다)

- **Ollama 어댑터.** 사용자 수는 가장 크지만 스트림 중 토큰별 timings가 없어 스파크라인이 사후 평균으로 퇴화한다. `/api/ps`의 `size_vram / size`로 오프로드 비율 카드까지는 싸다(조사 03 §2.2·순위 6). **열린 질문(§6)**.
- **vLLM / SGLang.** `/metrics`의 `vllm:time_to_first_token_seconds`·`sglang:cache_hit_rate`가 있어 어댑터는 쉽지만, 연속 배칭 다중 테넌트라 "한 런"의 의미가 다르다.
- **LM Studio.** SSE 이벤트 단계(`model_load.progress → prompt_processing.progress → message.delta`)가 우리 라이프사이클과 잘 맞는다.
- **`report.html`.** 여러 런을 브라우저에서 훑는 정적 단일 파일. §1 결정 3.
- **gist·rig-log 게시.** 인계 Could.
- **네이티브 Windows.** `/proc` 없음, `SIGWINCH` 없음, ETW는 관리자 권한. WSL2 안내로 대신한다(조사 03 §4).
- **AMD ROCm / Intel Arc.** `rocm-smi` 인터페이스가 배포판마다 갈린다.
- **KV 캐시 팽창 그래프.** 컨텍스트 증가에 따른 KV 메모리와 여유(조사 01 순위 9). M6의 3분할이 먼저다.

## 4. 결과 카드 계약

카드는 "논쟁을 끝내는 필드"를 빠짐없이, 같은 자리에 인쇄한다. 조사 02 §5의 필수 6종과 논쟁 5종을 그대로 계약으로 삼는다.

**항상 인쇄하는 필드(순서 고정)**

1. 헤더: `toktape vX.Y` · 런 ID(날짜-순번)
2. 모델: 파일명 · 정확한 양자화 서브타입(`Q4_K_M`, `IQ4_NL`, `UD-Q4_K_M` — "Q4"로 줄이지 않는다) · 파일 크기
3. 엔진: `llama-server` 빌드 번호 + 커밋 해시 · OS 커널
4. 리그: GPU 수×모델·VRAM · CPU · RAM 용량과 속도(`DDR5-6000`) — RAM 속도는 부분 오프로드에서 결정적(조사 02 순위 7)
5. 속도 3종: decode tok/s(+ 유효 GB/s, S6) · prefill tok/s(+ TTFT ms, 프롬프트 토큰 수) · 설정 컨텍스트와 테스트 in/out 토큰 수
6. prefix 캐시 히트 % (`0.0% (Cold Run)` 또는 `78% (400/512)`)
7. 메모리 배치: VRAM 막대(가중치|KV|버퍼) · 호스트 RSS · NVMe 미적재 GB · **majflt/token**
8. 호스트 상태: GPU별 온도·전력 · throttled · **contended: yes/no**
9. 플래그 한 줄: `-ngl -fa -b -ub -ctk -ctv --load-mode -ot ...` (Flash Attention과 `-b/-ub`는 생략 불가)
10. 푸터: `VERIFIED BY TOKTAPE` · 저장소 URL

**텍스트판**: 유니코드 박스, 72열 고정(Reddit 모바일에서 줄바꿈 안 나는 폭). 한글 포함 시 east-asian width 2로 계산하고 ANSI를 벗긴 뒤 잰다(교훈 5). 하단에 llama-bench 호환 markdown 표 한 블록을 붙여 기존 문화에 얹는다.

**PNG판**: 1200×675(16:9, X·Reddit OpenGraph). 어두운 배경, 큰 숫자 둘(decode·prefill)이 히어로, 그 아래 메모리 계층 막대와 majflt 스파크라인, 하단 4열 메타데이터 그리드. 색 계약은 구현 스펙에서 hex로 못박는다.

**JSON판**: `RunSummary` 그대로. `compare`의 입력.

## 5. 녹화 파라미터(헤드리스 렌더 기본값)

| 항목 | 값 | 근거(조사 02 §3) |
|---|---|---|
| 길이 | 10~12초. 프롬프트 전송 1s → TTFT 1s → 스트리밍+스파크라인 7s → 카드 정지 3s | X·GitHub에서 자동 루프되는 길이 |
| fps | 30 | 60은 크기만 2배, 15는 텍스트 스트리밍이 끊겨 보임 |
| 해상도 | 1200×675, README에서는 800px로 축소 표시 | Retina에서 선명 |
| GIF 팔레트 | `palettegen=max_colors=96` | 3.5 MB 아래 유지 |
| mp4 | H.264 yuv420p, 무음 | X는 짧은 무음 mp4를 GIF처럼 루프, 외부 링크보다 가중치 높음 |
| 폰트 | 번들 D2Coding(한글) + JetBrains Mono(라틴), 16px, 패딩 24px | 모바일에서 확대 없이 읽힘 |

토큰 수가 클립 길이를 넘으면 스트리밍 구간을 시간 압축한다. 압축률은 카드에 표기하지 않지만 런 파일에는 남긴다(재현 가능성).

## 6. v1 범위와 열린 질문

**v1 범위(결정)**

- 서버: llama-server(업스트림), ik_llama.cpp. 둘 다 `/props`·`timings_per_token`·`return_progress`가 있어 공짜로 풍부하다.
- OS: Linux(x86_64·arm64), macOS Apple Silicon. Windows는 WSL2 안내.
- 하드웨어: NVIDIA(go-nvml), Apple Silicon 통합 메모리(IOReport, sudo 없이).
- 산출물: TUI live · `.tape` · 카드 3종 · GIF/mp4 · compare.

**열린 질문 → 결정(2026-09-13, 북극성 기준으로 리드가 정함. 사용자가 뒤집을 수 있다)**

1. **Ollama는 v1 제외.** 카드가 심심해져 "당장 써보고 싶다"를 못 만든다. 자동 탐지 시 안내 문구만. `/api/ps` 오프로드 비율 카드는 v1.1(TTP-13).
2. **README·카드·UI 문자열은 영어, 설계 문서는 한국어.**
3. **macOS 수집기는 v1.1(TTP-15).** 스키마·카드 필드는 v1부터 존재.
4. **compare는 v1(TTP-10), ab는 v1.1(TTP-14).**
5. **동시 세션 모드는 v1(TTP-16).** §1 결정 9.

## 7. 첫 공개 시나리오(목표 상태)

r/LocalLLaMA 글 하나로 판단할 수 있게 목표를 시나리오로 적는다.

- 제목: "70B가 왜 2 tok/s인지 추측하기 지쳐서 만들었다 — GPU/RAM/NVMe 배치, 페이지 폴트, 실제 서빙 tok/s를 한 화면에 보여 주는 TUI"(조사 02 §8 템플릿의 번역).
- 본문 미디어: 1200×675 PNG 카드 1장.
- 최상단 댓글: 텍스트 카드(코드블록) + 설치 한 줄(`go install` 또는 `curl | sh`).
- X: 12초 mp4(attach → 스파크라인 → 카드 전환).
- 첫 실행 경험: `toktape` 한 단어. 8080을 찾아 붙고, 기본 프롬프트를 보내고, 카드를 저장하고, 공유 안내를 띄운다. 플래그 없이.

## 8. 다음 라운드

1. **런 파일 스키마 초안**(`docs/tape-schema.md`): `RunSummary`·`RunSample`·토큰 이벤트. 인계 8교훈을 필드·상수로. 리드가 쓴다.
2. **레코더 구현 스펙**: llama-server 수집기(SSE·`/props`·`/slots`·`/apply-template`), `/proc` 수집기, nvml 수집기. GLM 위임 가능(비전 불필요). 검증은 인계 문서의 tok/s 리듀서 픽스처(세 함정) 2% 게이트.
3. **카드 텍스트판 구현 스펙**: 72열 계약, 한글 폭 픽스처(우측 테두리 열 불변 게이트).
4. TUI는 그 뒤. v41-demo 패리티가 첫 게이트.
