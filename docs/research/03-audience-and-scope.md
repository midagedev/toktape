# Comprehensive Research Report: Market & Technical Feasibility for `toktape`
**Date of Research:** September 13, 2026  
**Subject:** Market sizing, telemetry ingestion, operating system feasibility, Go ecosystem libraries, and product architecture for `toktape` (a terminal observability tool for local LLM serving).

---

## 1. Audience Sizing and Activity Matrix

The local Large Language Model (LLM) serving landscape is segmented into distinct user archetypes: casual desktop consumers, hobbyist/power tuners, high-throughput self-hosters, and enterprise/batch infrastructure engineers. 

To determine the initial target persona for `toktape`, we quantify the scale, community activity, and serving characteristics across key engines:

| Engine / Framework | GitHub Stars | Container / Binary Scale | Primary Community Hub | Subreddit / Hub Size | Primary User Archetype | Fit for `toktape` v1 |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **[Ollama](https://github.com/ollama/ollama)** | [180,757 stars](https://github.com/ollama/ollama) | [174,307,197 Docker pulls](https://hub.docker.com/v2/repositories/ollama/ollama) | [r/ollama](https://www.reddit.com/r/ollama/about.json) | 137,272 subscribers | Developers wanting zero-config background daemon; consumers running 7B–14B models. | **Tier 2** (abstracted backend; high user count, low tuning flexibility) |
| **[llama.cpp](https://github.com/ggml-org/llama.cpp)** (`llama-server`) | [128,004 stars](https://github.com/ggml-org/llama.cpp) | 7,173 releases; default runtime engine | [r/LocalLLaMA](https://www.reddit.com/r/LocalLLaMA/about.json) | 822,597 subscribers | Power tuners, multi-GPU/split-offload experimenters, quantized model benchmarkers. | **Tier 1 (Core Target)** |
| **[vLLM](https://github.com/vllm-project/vllm)** | [91,589 stars](https://github.com/vllm-project/vllm) | [35,093,808 Docker pulls](https://hub.docker.com/v2/repositories/vllm/vllm-openai) | [r/vLLM](https://www.reddit.com/r/vLLM/about.json) | 5,846 subscribers | Production infrastructure engineers, continuous batching, multi-node enterprise deployments. | **Tier 2** (batch/production focus rather than interactive single-stream tuning) |
| **[SGLang](https://github.com/sgl-project/sglang)** | [35,900 stars](https://github.com/sgl-project/sglang) | [13,186,999 Docker pulls](https://hub.docker.com/v2/repositories/lmsysorg/sglang) | [sgl-project GitHub](https://github.com/sgl-project/sglang) | High GitHub issue/PR velocity | High-throughput serving, structured decoding, RadixAttention cache benchmarking. | **Tier 2** |
| **[MLX / mlx-lm](https://github.com/ml-explore/mlx)** | [28,397 stars](https://github.com/ml-explore/mlx) | Native Apple Silicon pip package | [r/LocalLLaMA](https://www.reddit.com/r/LocalLLaMA/about.json) (Apple Silicon threads) | Shared across r/LocalLLaMA | Apple Silicon Mac Studio/Pro enthusiasts running native FP16/4-bit Metal graphs. | **Tier 2** (Python-centric; unified memory fits `toktape` thesis) |
| **[KoboldCpp](https://github.com/LostRuins/koboldcpp)** | [11,675 stars](https://github.com/LostRuins/koboldcpp) | [662 releases](https://github.com/LostRuins/koboldcpp/releases) | [r/KoboldAI](https://www.reddit.com/r/KoboldAI/about.json) | 24,506 subscribers | Creative writers, RP gamers, desktop users needing GUI bundle with context shift/mmap. | **Tier 3** |
| **[ik_llama.cpp](https://github.com/ikawrakow/ik_llama.cpp)** | [3,219 stars](https://github.com/ikawrakow/ik_llama.cpp) | Specialized fork of llama.cpp | [r/LocalLLaMA](https://www.reddit.com/r/LocalLLaMA/about.json) | Featured in top quant discussions | Bleeding-edge quants (IQ1_S to IQ4_NL), custom AVX-512/CUDA kernels, extreme memory squeezers. | **Tier 1 (Core Target)** |
| **[LM Studio](https://lmstudio.ai/)** | Closed source GUI | Millions of desktop installs | [LM Studio Docs](https://github.com/lmstudio-ai/docs) | [r/LocalLLaMA](https://www.reddit.com/r/LocalLLaMA/about.json) | GUI-first local LLM users wanting local OpenAI-compatible REST server. | **Tier 2** |

### Key Audience Insights
1. **The Epicenter of Local Tuning is `r/LocalLLaMA`:** With [822,597 subscribers](https://www.reddit.com/r/LocalLLaMA/about.json), `r/LocalLLaMA` is roughly 6x larger than `r/ollama` ([137,272 subscribers](https://www.reddit.com/r/ollama/about.json)) and 33x larger than `r/KoboldAI` ([24,506 subscribers](https://www.reddit.com/r/KoboldAI/about.json)). The primary discourse centers on squeezing models into hardware constraints: VRAM budgeting, split-tensor offloading (`--n-gpu-layers`), KV cache quantization, and mmap swapping.
2. **The "Tuner" vs. "Consumer" Split:** Ollama boasts staggering reach ([174M+ Docker pulls](https://hub.docker.com/v2/repositories/ollama/ollama)), but intentionally abstracts layer offloading, context swapping, and memory mapping behind an opaque CLI. `llama.cpp` ([128k stars](https://github.com/ggml-org/llama.cpp)) and its performance-pushing forks like `ik_llama.cpp` ([3,219 stars](https://github.com/ikawrakow/ik_llama.cpp)) are the primary tools used by power tuners who manually adjust flags (`-ngl`, `-fa`, `-ctk`, `-ctv`, `--mmap`, `--no-mmap`).
3. **Primary Beachhead:** The initial audience for `toktape` is the **power tuner running `llama-server` or `ik_llama.cpp` on Linux or Apple Silicon**.

---

## 2. Telemetry and API Streaming Protocols

To deliver a real-time TUI displaying prompt/decode speeds, TTFT, prompt cache hits, memory pressure, and streaming tokens, `toktape` must ingest data from two sources: **HTTP/SSE server telemetry** and **OS-level kernel process introspection**.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                                 toktape TUI                                 │
└──────────────────────┬───────────────────────────────┬──────────────────────┘
                       │                               │
       [1. HTTP / SSE Telemetry]             [2. Process Introspection]
                       │                               │
                       ▼                               ▼
       ┌───────────────────────────────┐     ┌─────────────────────────────────┐
       │     llama-server / Engine     │     │    Host Kernel & Hardware OS    │
       │                               │     │                                 │
       │ • GET /props                  │     │ • /proc/$PID/smaps_rollup       │
       │   (model path, n_ctx, slots)  │     │   (RSS, PSS, Mapped, Lazy)      │
       │ • POST /completion (SSE)      │     │ • mincore(2)                    │
       │   (return_progress: true)     │     │   (in-RAM vs disk-page status)  │
       │   (timings_per_token: true)   │     │ • /proc/$PID/stat (maj_flt)     │
       │ • GET /slots (slot state)     │     │ • NVIDIA NVML / Apple IOReport  │
       │ • GET /metrics (Prometheus)   │     │   (VRAM usage, unified memory)  │
       └───────────────────────────────┘     └─────────────────────────────────┘
```

### 2.1 `llama-server` Native Telemetry (The Gold Standard for `toktape`)

As detailed in the [llama.cpp HTTP Server documentation](https://raw.githubusercontent.com/ggml-org/llama.cpp/master/tools/server/README.md) and [Issue #9291](https://github.com/github.com/ggml-org/llama.cpp/issues/9291):

1. **Static Metadata Discovery (`GET /props`):**
   - Returns JSON containing `model_path`, `total_slots`, `default_generation_settings` (`n_ctx`, `n_predict`, `temperature`), and server parameters.
   - Allows `toktape` to immediately resolve the absolute path to the `.gguf` file on disk, examine file size, and extract embedded tensor architecture without user intervention.

2. **Real-time TTFT & Prompt Cache Progress (`return_progress: true`):**
   - When calling `POST /completion` or `POST /v1/chat/completions`, passing `"return_progress": true` instructs `llama-server` to emit intermediate Server-Sent Events (SSE) *during prompt evaluation* before the first generated token:
     ```json
     data: {"prompt_progress": {"total": 4096, "cache": 2048, "processed": 3072, "time_ms": 312.4}}
     ```
   - Note (lead, 2026-09-13): `processed` *includes* the cached prefix — upstream sets
     `progress.processed = slot.prompt.tokens.size()` after `keep_first(n_past)`, so it never
     drops below `cache`. Wall progress is `processed/total`; the evaluated fraction is
     `(processed-cache)/(total-cache)`. The original example here had 1024 and misled a round.
   - **Feature Enablement:** This enables a real-time progress bar for Time-To-First-Token (TTFT), visually tracking how many prompt tokens were served directly from the KV cache versus evaluated cold.

3. **Per-Token Generation Latencies (`timings_per_token: true`):**
   - Requesting `"timings_per_token": true` includes generation speed within each SSE chunk:
     ```json
     data: {"content": " hello", "timings": {"prompt_n": 128, "prompt_ms": 84.2, "prompt_per_second": 1520.1, "predicted_n": 14, "predicted_ms": 280.5, "predicted_per_second": 49.9}}
     ```
   - **Feature Enablement:** `toktape` can compute rolling tok/s, jitter, and generation step time per token without relying solely on client-side wall clocks.

4. **Multi-Tenant State (`GET /slots`) & Prometheus (`GET /metrics`):**
   - `GET /slots` reveals the state of each processing slot (`IDLE`, `PROCESSING`), current cache tokens, and active context lengths.
   - `GET /metrics` exports standard Prometheus metrics:
     - `llamacpp:prompt_tokens_total`
     - `llamacpp:tokens_predicted_total`
     - `llamacpp:n_tokens_max`
     - `llamacpp:prompt_tokens_seconds`

### 2.2 Telemetry Ingestion from Other Engines

- **[Ollama REST API](https://raw.githubusercontent.com/ollama/ollama/main/docs/api.md):**
  - Streaming endpoints (`POST /api/generate` and `POST /api/chat`) omit per-token timings during generation, but provide a final JSON payload containing high-precision nanosecond timings:
    - `total_duration`, `load_duration`, `prompt_eval_count`, `prompt_eval_duration` (yielding prompt tok/s), `eval_count`, `eval_duration` (yielding generation tok/s).
  - Model residency is exposed via `GET /api/ps`:
    ```json
    {
      "models": [{
        "name": "llama3:70b",
        "size": 42000000000,
        "size_vram": 28000000000
      }]
    }
    ```
    Dividing `size_vram` by `size` reveals the exact VRAM offload ratio.
- **[LM Studio v1 REST API](https://raw.githubusercontent.com/lmstudio-ai/docs/main/1_developer/2_rest/streaming-events.md):**
  - Emits explicit SSE event steps: `model_load.start` $\to$ `model_load.progress` $\to$ `prompt_processing.progress` $\to$ `reasoning.delta` $\to$ `message.delta` $\to$ `chat.end`.
  - Highly compatible with `toktape`'s multi-stage visual lifecycle.
- **[vLLM](https://github.com/vllm-project/vllm) & [SGLang](https://github.com/sgl-project/sglang):**
  - Use standard OpenAI endpoints with `"stream_options": {"include_usage": true}` for prompt and completion token counts.
  - Expose deep Prometheus metrics via `GET /metrics`:
    - `vllm:avg_prompt_throughput_tok_per_s`, `vllm:avg_generation_throughput_tok_per_s`
    - `vllm:time_to_first_token_seconds_bucket`
    - `vllm:gpu_cache_usage_factor`
    - `sglang:cache_hit_rate` (tracking RadixAttention prefix tree reuse).

### 2.3 Kernel Process Memory & Page Fault Introspection (Linux)

To show whether model layers reside in physical VRAM, pinned system RAM, or swapped/lazy mmap storage, `toktape` inspects the server process directly:

1. **`/proc/$PID/smaps_rollup`:**
   - Reading this pseudofile gives near-zero-overhead process memory metrics:
     - `Rss`: Total Resident Set Size currently resident in RAM.
     - `Pss`: Proportional Set Size (accounting for shared libraries).
     - `Shared_Clean` vs. `Private_Dirty`: Separates clean file-backed model weights (`mmap`) from dynamically allocated KV cache buffers (`malloc`/`cudaHostAlloc`).
     - `LazyFree`: Pages freed by madvise but not yet reclaimed.
2. **`mincore(2)` Vector Scanning:**
   - By querying `/proc/$PID/maps` for the memory region mapped to the `.gguf` file descriptor, `toktape` can call `mincore(void *addr, size_t length, unsigned char *vec)`.
   - Each byte in `vec` indicates whether the corresponding 4KB virtual page currently resides in physical RAM.
   - **Feature Enablement:** Enables an exact "layer-by-layer cold vs. warm" residency bar showing what percentage of the model was faulted into RAM during prefill.
3. **Major Page Faults per Token Sparkline:**
   - From `/proc/$PID/stat`, field 12 (`majflt`) records the number of page faults that required physical disk I/O (e.g., retrieving an evicted GGUF page from NVMe).
   - Alternatively, `getrusage(RUSAGE_CHILDREN, &usage)` returns `ru_majflt`.
   - By sampling `majflt` delta synchronously with every streaming token emitted over SSE, `toktape` plots a **real-time sparkline of NVMe read stalls per token** ($ \Delta \text{majflt} / \Delta t $).

---

## 3. macOS / Apple Silicon Feasibility & Viral Potential

### 3.1 Unified Memory Architecture (UMA) Telemetry Without `sudo`

A central requirement for `toktape` is running without elevated privileges (`sudo`). On Apple Silicon (M1–M5), memory architecture is unified between the CPU and Metal GPU, but macOS manages Metal device buffers differently from standard anonymous memory:

1. **The Privileged Roadblock (`powermetrics`):**
   - Traditional Apple Silicon monitoring tools use `sudo powermetrics --samplers gpu_power,thermal`. Requiring `sudo` degrades developer trust and breaks frictionless adoption.
2. **The Sudoless Solution (`IOReport`):**
   - Projects such as [vladkens/macmon](https://github.com/vladkens/macmon), [tlrmpt/socpowerbud](https://github.com/tlrmpt/socpowerbud), and [Aristocratos/btop](https://github.com/Aristocratos/btop) demonstrate that Apple's private `IOReport` framework (`IOReportCreateSubscription`, `IOReportCopyChannelsInGroup`) can be queried by standard user processes.
   - Non-root processes can retrieve GPU residency, Apple Neural Engine (ANE) utilization, package power (watts), and memory bandwidth.
3. **Process Memory & Paging APIs:**
   - Instead of `/proc`, macOS provides Mach task APIs via `task_info()`:
     - `mach_task_basic_info`: returns `resident_size`.
     - `task_vm_info_data_t` (with `TASK_VM_INFO`): returns `phys_footprint` (the true metric enforced by the macOS Jetsam memory killer).
     - `pageins`: In Mach VM, `task_vm_info.pageins` tracks disk/swap page-in events, providing the exact macOS equivalent to Linux's `majflt`.

### 3.2 The "Viral Flex" Aesthetic & Community Psychology

On [r/LocalLLaMA](https://www.reddit.com/r/LocalLLaMA/), local model serving is as much a competitive benchmarking sport as an engineering task. Posts sharing hardware rigs and generation speeds routinely dominate all-time upvotes:

1. **Mac Studio UMA Rigs:**
   - High-profile community posts such as *"Apple introduces new Mac Studio with M5 Max and M5 Ultra - up to 512GB of unified memory"* ([1,651 upvotes](https://www.reddit.com/r/LocalLLaMA/comments/1vxzg6v/apple_introduces_new_mac_studio_with_m5_max_and/)) and discussions comparing the RTX 5090 ($2,000–$3,000 street price) against M5 Ultra 256GB Mac Studios ([1,640 upvotes](https://www.reddit.com/r/LocalLLaMA/comments/1w05kbt/5090_now_officially_cost_5090/)) highlight the huge user base leveraging Apple Silicon for 70B–405B models.
   - Running 70B models on M4 Max 128GB at 11 tok/s is an active showcase topic ([629 upvotes](https://www.reddit.com/r/LocalLLaMA/comments/1gw9ufb/m4_max_128gb_running_qwen_72b_q4_mlx_at_11tokenssecond/)).
2. **Multi-GPU PC Rigs:**
   - Enthusiasts assemble multi-GPU mining frame rigs, such as the *"8x RTX 3090 open rig"* ([1,586 upvotes](https://www.reddit.com/r/LocalLLaMA/comments/1iqpzpk/8x_rtx_3090_open_rig/)) and *"My 160GB local LLM rig"* (4x Tesla V100 32GB + 4x RTX 3090 24GB) ([1,374 upvotes](https://www.reddit.com/r/LocalLLaMA/comments/1l5wxoa/my_160gb_local_llm_rig/)).
   - Meanwhile, users with standard 24GB cards who feel left behind by massive MoE models make posts like *"Enough already. If I can't run it in my 3090, I don't want to hear about it."* ([3,616 upvotes](https://www.reddit.com/r/LocalLLaMA/comments/1ffv39d/enough_already_if_i_cant_run_it_in_my_3090_i_dont/)).

**Product Opportunity:**  
When an engineer successfully splits DeepSeek-R1 or Llama-3-70B across 2x RTX 3090s and system RAM, or achieves 18 tok/s on an M4 Max Studio, they want to post proof. 

Currently, users post crude terminal screenshots or screen recordings. If `toktape` automatically generates a clean, compact terminal summary card—showing model architecture, tensor distribution (VRAM vs RAM vs NVMe), TTFT, steady tok/s, and a page fault sparkline—it provides the ideal "spec sheet" for community sharing.

---

## 4. Windows Local LLM Tuning Share and Go TUI Pitfalls

### 4.1 Demographic Breakdown & Developer Reality

While Windows accounts for the majority of consumer desktop installations running prepackaged frontends like LM Studio or KoboldCpp, the **tuning audience** shifts away from native Windows:
- High-end multi-GPU rigs (e.g., dual 3090/4090 or P40 clusters) predominantly run Ubuntu/Debian or WSL2 due to NVIDIA P2P / peer-to-peer PCIe memory mapping limitations and complex C++ build chains on native Windows.
- Windows users who do tune models frequently run inside **WSL2** to access Linux shell scripts and native Docker configurations. In WSL2, Linux `/proc` filesystem semantics apply directly.

### 4.2 Technical Hurdles for Native Windows Go TUI & Telemetry

Targeting native Windows (`windows/amd64`) in Go introduces several technical friction points:

1. **Console Emulation & ANSI Virtual Terminal Sequences:**
   - Traditional `conhost.exe` lacks full 24-bit TrueColor and modern ANSI escape handling unless Virtual Terminal Processing is explicitly activated via the Win32 API:
     ```go
     kernel32.NewProc("SetConsoleMode").Call(handle, mode | ENABLE_VIRTUAL_TERMINAL_PROCESSING)
     ```
   - Windows Terminal supports ANSI well, but high-frequency TUI repainting (sparklines refreshed at 30–60 FPS) through ConPTY can suffer from visible cursor tearing and dropped frames compared to native Unix pseudo-terminals (`pty`).
2. **Terminal Resize & Window Signals:**
   - On Unix, window dimension changes trigger a standard POSIX `SIGWINCH` signal caught by Bubble Tea via Go's `os/signal`.
   - On Windows, `SIGWINCH` does not exist. Window size updates arrive as `WINDOW_BUFFER_SIZE_RECORD` events inside `ReadConsoleInputW`, requiring custom event loop bridging.
3. **Absence of `/proc` for Memory Introspection:**
   - Windows does not provide `/proc/$PID/smaps_rollup`.
   - To inspect a running process's memory, `toktape` must import `KERNEL32.DLL` and `PSAPI.DLL`, acquire process handles via `OpenProcess(PROCESS_QUERY_INFORMATION | PROCESS_VM_READ)`, and invoke:
     - `GetProcessMemoryInfo`: retrieves `WorkingSetSize`, `PeakWorkingSetSize`, and `PageFaultCount`.
     - `QueryWorkingSetEx`: probes virtual address ranges to determine whether pages reside in physical RAM or the paging file.
   - Paging faults on Windows cannot easily be distinguished between soft faults (minor) and hard disk faults (major) on a per-token basis without Event Tracing for Windows (ETW), which requires administrator elevation.

**Strategic Recommendation:** Support **Linux** and **macOS** natively in Tier 1. Direct Windows users to run `toktape` within **WSL2**, deferring native Win32 API implementation to v2.

---

## 5. Go Ecosystem Libraries Evaluation

Building `toktape` in Go is supported by a mature collection of open-source libraries:

### 5.1 GGUF Inspection: `gpustack/gguf-parser-go`
- **Repository:** [gpustack/gguf-parser-go](https://github.com/gpustack/gguf-parser-go)
- **Role:** Pure Go parser for GGUF model files.
- **Capabilities:** Directly extracts architecture metadata, context length, tensor counts, layer counts, and quantitative types (`q4_k_m`, `iq3_xxs`). Critically, it includes built-in calculation functions estimating memory requirements for weights, context buffers, and KV caches across different hardware offload configurations.
- **Integration:** When `toktape` resolves `model_path` via `llama-server`'s `GET /props`, it can read the local GGUF header to verify tensor offload alignment against actual runtime VRAM usage.

### 5.2 NVIDIA GPU Telemetry: `NVIDIA/go-nvml`
- **Repository:** [NVIDIA/go-nvml](https://github.com/NVIDIA/go-nvml)
- **Role:** Official NVIDIA Management Library (NVML) Go bindings.
- **Capabilities:** Interrogates per-GPU VRAM utilization, PCIe throughput, clock speeds, and temperature.
- **Advantage:** `go-nvml` uses dynamic runtime loading (`dlopen` of `libnvidia-ml.so.1` on Linux, `nvml.dll` on Windows). It compiles **without CGo**, allowing `toktape` to remain a pure, cross-compilable Go binary that gracefully degrades if no NVIDIA driver is present.

### 5.3 TUI Framework: `charmbracelet/bubbletea` & `lipgloss`
- **Repositories:** [charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea), [lipgloss](https://github.com/charmbracelet/lipgloss), [bubbles](https://github.com/charmbracelet/bubbles)
- **Role:** The Elm architecture in Go for high-performance terminal applications.
- **Capabilities:**
  - `bubbles/progress`: smooth progress bars for TTFT and prompt-cache loading.
  - `bubbles/viewport`: scrollable token stream window.
  - `lipgloss`: declarative layout, TrueColor styles, border formatting, and unified grid presentation.
  - Sparklines: easily constructed using unicode block characters (` `, `▂`, `▃`, `▄`, `▅`, `▆`, `▇`, `█`) to display page faults and generation jitter over time.

### 5.4 Run Recording & Headless Shareable Artifact Generation
To satisfy the requirement that "every run is recorded to a run file" and exportable to PNG/GIF/text cards:

1. **Run Recording (`.tape` / Asciinema v2):**
   - The [Asciinema v2 format specification](https://github.com/asciinema/asciinema/blob/develop/doc/asciicast-v2.md) is a lightweight, line-delimited JSON (JSONL) protocol:
     - Header line: `{"version": 2, "width": 120, "height": 36, "timestamp": 1726200000, "env": {"SHELL": "/bin/zsh"}}`
     - Event lines: `[0.1245, "o", "\u001b[32mStreaming token...\u001b[0m"]`
   - `toktape` can serialize the live TUI stream and telemetry points into an open, auditable run file.
2. **Headless PNG / Social Card Rendering:**
   - **Approach A (Pure Go SVG $\to$ PNG):** Render the terminal layout to SVG using Lip Gloss ANSI-to-SVG converters, then rasterize to PNG using [tdewolff/canvas](https://github.com/tdewolff/canvas) or [fogleman/gg](https://github.com/fogleman/gg). This keeps the binary self-contained with no external dependencies.
   - **Approach B (Scriptable VHS Engine):** [charmbracelet/vhs](https://github.com/charmbracelet/vhs) provides the gold standard for rendering terminal recordings into synthetic PNG/GIF cards using an embedded headless Chromium instance.

---

## 6. Recommended v1 Scope

To achieve immediate utility and maximize viral reach without overextending development complexity, `toktape` should adhere to the following scope boundaries:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                            RECOMMENDED V1 SCOPE                             │
├─────────────────────────────────────────────────────────────────────────────┤
│ • Primary Engine:   llama-server (native) & ik_llama.cpp                   │
│ • Telemetry Feeds:  GET /props, POST /completion (SSE with                  │
│                     timings_per_token & return_progress), GET /slots        │
│ • OS Target:        Linux (x86_64/ARM64) & macOS (Apple Silicon M-series)   │
│ • Hardware:         NVIDIA GPUs (via go-nvml) & Apple Silicon (via IOReport)│
│ • Memory Profiling: /proc/$PID/smaps_rollup, mincore, majflt sparkline     │
│ • Output:           Interactive Bubble Tea TUI + Asciinema JSONL run record │
│ • Export:           Static summary card rendered to UTF-8 text and PNG      │
└─────────────────────────────────────────────────────────────────────────────┘
```

### In-Scope (v1 Priorities)
1. **Tier-1 Servers:** `llama-server` (upstream `llama.cpp`) and `ik_llama.cpp`. Native support for `/props`, `timings_per_token`, and `return_progress` gives instant, rich telemetry out of the box.
2. **Tier-1 Operating Systems:**
   - **Linux:** `/proc/$PID/smaps_rollup`, `/proc/$PID/stat` major page fault sampling, `mincore()` file mapping scan.
   - **macOS (Apple Silicon):** `mach_vm`/`task_info` footprint tracking, sudoless `IOReport` for GPU power/memory bandwidth.
3. **Hardware Introspection:**
   - NVIDIA Ampere/Ada/Blackwell/Hopper/Pascal GPUs via pure Go NVML.
   - Apple Silicon M-series Unified Memory.
4. **Interactive TUI Core:**
   - Upper panel: Model architecture, layer placement breakdown (GPU VRAM vs. System RAM vs. NVMe mmap).
   - Middle panel: TTFT timer, prompt cache hit percentage, rolling tok/s, and major page faults per token sparkline.
   - Lower panel: Streaming token generation viewport.
5. **Shareable Run Card Export:**
   - Single-command output: `toktape export --run latest.tape --out card.png` or `card.txt`.

### Deferred to Post-v1
1. **Native Windows (`windows/amd64`):** Direct Windows users to WSL2 for v1. Native Win32 ETW and memory APIs require significant specialized plumbing.
2. **AMD ROCm / Intel Arc:** ROCm (`rocm-smi`) driver interfaces are fragmented across distributions compared to NVML's stable ABI.
3. **Ollama Engine Abstraction:** Ollama does not expose per-token generation latencies during streaming, limiting sparkline granularity to post-hoc averages.
4. **vLLM / Distributed Multi-Node:** Enterprise continuous batching requires a multi-tenant dashboard architecture rather than single-run prompt tuning.

---

## 7. Top 10 Demand Signals Ranked List

The following real-world community discussions, bug reports, and pull requests demonstrate demand for the specific telemetry features planned for `toktape`:

| Rank | Demand Signal | Evidence Source URL | What Feature It Implies |
| :---: | :--- | :--- | :--- |
| **1** | **Frustration with VRAM limits and model fit uncertainty**<br>Community demands clear clarity on what runs on 24GB hardware: *"Enough already. If I can't run it in my 3090, I don't want to hear about it."* (3,616 upvotes). | [Reddit Post: r/LocalLLaMA (3,616 upvotes)](https://www.reddit.com/r/LocalLLaMA/comments/1ffv39d/enough_already_if_i_cant_run_it_in_my_3090_i_dont/) | **Model Layer Allocation View:** A live visual breakdown showing exactly how many layers fit into 24GB VRAM versus spilling into system RAM and NVMe. |
| **2** | **Desire to benchmark massive Apple Silicon Unified Memory**<br>Extreme enthusiasm around high-memory Macs: *"Apple introduces new Mac Studio with M5 Max and M5 Ultra - up to 512GB of unified memory"* (1,651 upvotes) and *"5090 now officially cost 5090 / perhaps I am much better off getting an M5 Ultra Mac Studio with 256gb of ram"* (1,640 upvotes). | [Reddit Post: r/LocalLLaMA (1,651 upvotes)](https://www.reddit.com/r/LocalLLaMA/comments/1vxzg6v/apple_introduces_new_mac_studio_with_m5_max_and/)<br>[Reddit Post: r/LocalLLaMA (1,640 upvotes)](https://www.reddit.com/r/LocalLLaMA/comments/1w05kbt/5090_now_officially_cost_5090/) | **Sudoless Apple Silicon Telemetry:** Seamless Unified Memory tracking (resident footprint vs. wired Metal buffer memory) without requiring `sudo powermetrics`. |
| **3** | **Enthusiasm for sharing multi-GPU rig specs and performance**<br>Tuning enthusiasts build custom multi-GPU hardware rigs and actively share benchmark results: *"8x RTX 3090 open rig"* (1,586 upvotes) and *"My 160GB local LLM rig"* (1,374 upvotes). | [Reddit Post: r/LocalLLaMA (1,586 upvotes)](https://www.reddit.com/r/LocalLLaMA/comments/1iqpzpk/8x_rtx_3090_open_rig/)<br>[Reddit Post: r/LocalLLaMA (1,374 upvotes)](https://www.reddit.com/r/LocalLLaMA/comments/1l5wxoa/my_160gb_local_llm_rig/) | **Shareable Performance Spec Cards:** PNG/GIF/text exportable summary cards displaying exact GPU distribution, TTFT, tok/s, and memory pressure for community sharing. |
| **4** | **Demand for real-time streaming latency & prompt evaluation progress**<br>Upstream `llama.cpp` implemented explicit telemetry flags (`timings_per_token` and `return_progress`) because clients were blind during long prompt prefill phases. | [llama.cpp Issue #9291: "server: add timings_per_token and return_progress"](https://github.com/ggml-org/llama.cpp/issues/9291)<br>[llama.cpp Server Docs](https://raw.githubusercontent.com/ggml-org/llama.cpp/master/tools/server/README.md) | **Dual Progress & Latency Gauges:** Real-time prefill progress indicator for TTFT, followed by a rolling per-token generation speed curve. |
| **5** | **Running massive models via NVMe SSD mmap offloading**<br>Users experiment with running models larger than RAM (e.g., DeepSeek-R1 671B on 96GB RAM) by swapping against high-speed NVMe drives at ~2 tok/s, producing frequent page faults. | [Reddit Post: r/LocalLLaMA "DeepSeek-R1 671B on SSD mmap"](https://www.reddit.com/r/LocalLLaMA/comments/1j4jpij/r1_671b_on_ssd/) | **Major Page Faults per Token Sparkline:** A live sparkline visualizing $ \Delta \text{majflt} $ to instantly diagnose when generation stalls are caused by NVMe paging bottlenecks. |
| **6** | **Need to verify VRAM offload ratios in abstracted backends (Ollama)**<br>Ollama users repeatedly ask whether their model is actually running on GPU or silently falling back to CPU, prompting Ollama to add the `/api/ps` endpoint reporting `size_vram` vs `size`. | [Ollama API Documentation: Model Management](https://raw.githubusercontent.com/ollama/ollama/main/docs/api.md#list-running-models) | **VRAM vs. System RAM Offload Gauge:** Explicit verification of whether weights are fully loaded into GPU memory, split across buses, or executing on CPU. |
| **7** | **Optimizing prompt prefill via KV cache and prefix reuse**<br>Prompt processing time dominates long-context interactions (RAG, agent loops). SGLang and llama.cpp users heavily monitor prompt-cache hits to avoid re-evaluating tokens. | [SGLang RadixAttention & Cache Documentation](https://github.com/sgl-project/sglang)<br>[llama.cpp Server Slot State Docs](https://raw.githubusercontent.com/ggml-org/llama.cpp/master/tools/server/README.md) | **Prompt-Cache Hit Indicator:** Real-time visual display showing cached tokens reused versus new tokens evaluated, explaining variances in TTFT. |
| **8** | **Adoption of bleeding-edge quantization kernels (ik_llama.cpp)**<br>Tuning enthusiasts adopt specialized forks like `ik_llama.cpp` (3,219 stars) to run ultra-low quants (IQ1_S, IQ2_XXS) and custom AVX-512/CUDA kernels for maximum token speed under tight memory constraints. | [GitHub Repository: ikawrakow/ik_llama.cpp (3,219 stars)](https://github.com/ikawrakow/ik_llama.cpp) | **Custom Fork & Layer Quantization Detection:** Parsing GGUF headers to inspect heterogeneous quantization types per tensor layer. |
| **9** | **Real process memory monitoring vs. virtual memory allocation myths**<br>Users frequently confuse virtual memory size (`VIRT`), mapped file size (`mmap`), and resident set size (`RSS`), causing confusion when troubleshooting out-of-memory (OOM) crashes. | [Btop System Monitor GitHub (19,000+ stars)](https://github.com/Aristocratos/btop)<br>[macmon Apple Silicon Monitor (1,800+ stars)](https://github.com/vladkens/macmon) | **Three-Tier Memory Inspector:** Parsing `/proc/$PID/smaps_rollup` or Mach `task_vm_info` to distinguish between RSS, mapped file memory, and never-loaded pages. |
| **10** | **Reproducible benchmarking runs for local model updates**<br>Tuning engineers need to verify whether updating `llama.cpp` or adjusting context window parameters improved or degraded generation performance over time. | [Asciinema v2 Record Format Specification](https://github.com/asciinema/asciinema/blob/develop/doc/asciicast-v2.md)<br>[Charmbracelet VHS Terminal Recorder](https://github.com/charmbracelet/vhs) | **Run File Recording (`.tape`):** Recording prompt, token stream, latencies, and memory metrics into an auditable JSONL format for historical comparison. |

---

## 8. Summary Conclusion

The data confirms a strong market opening for `toktape`. While the mass consumer base utilizes automated runtimes like Ollama, the **820,000+ members of `r/LocalLLaMA`** represent an active, vocal cohort of tuners grappling with VRAM budgets, KV-cache allocations, mmap disk thrashing, and split-offload balancing. 

By building in **Go** using `gpustack/gguf-parser-go`, `go-nvml`, and `charmbracelet/bubbletea`, `toktape` can deliver a zero-dependency binary that connects directly to `llama-server` and `ik_llama.cpp`. By capturing real-time telemetry (`timings_per_token`, `return_progress`), kernel memory metrics (`smaps_rollup`, `mincore`, major page fault sparklines), and sudoless Apple Silicon metrics (`IOReport`), `toktape` delivers the exact introspection local LLM tuners currently lack—complete with shareable performance cards that naturally fuel organic community adoption.
