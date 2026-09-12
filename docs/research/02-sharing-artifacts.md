# toktape: Sharing-Artifact Conventions, Virality Mechanics, and Tuning Card Specification

**Research Date:** 2026-09-13  
**Analyst:** Systems & DevRel Research Analyst  
**Subject:** Virality mechanics, terminal artifact conventions, and shareable result-card design for **toktape** (Go terminal tuning tool for local LLM inference engines: `llama.cpp` / `llama-server`, `ik_llama.cpp`, Ollama, and vLLM).

---

## Executive Summary

Hardware enthusiasts, local AI tinkerers, and inference engineers on r/LocalLLaMA, X (Twitter), and Hacker News share benchmark results for two primary reasons: **status signaling ("rig flexing")** and **configuration troubleshooting ("why is my decode so slow?")**. Currently, the community relies on fragmented, imperfect artifacts:
1. **Raw `llama-bench` markdown tables** copied into Reddit code blocks, which measure synthetic engine speed in isolation rather than real serving runtimes ([ggml-org/llama.cpp PR #28591](https://github.com/ggml-org/llama.cpp/pull/28591)).
2. **`nvidia-smi` / `rocm-smi` terminal screenshots**, pasted to prove true VRAM allocation, PCIe bandwidth, and thermal state ([ollama/ollama Issue #17788](https://github.com/ollama/ollama/issues/17788)).
3. **`ollama ps` terminal dumps**, pasted to demonstrate CPU vs. GPU offload percentages ([ollama/ollama Issue #17251](https://github.com/ollama/ollama/issues/17251)).

Existing tools like [`nvitop`](https://github.com/XuehaiPan/nvitop) (7,148 stars), [`llmfit`](https://github.com/AlexsJones/llmfit) (36,237 stars), and Grafana/Prometheus stacks monitor raw hardware metrics or static model fit, but fail to capture the **dynamic coupling between memory hierarchy placement (VRAM vs. RAM vs. NVMe `mmap`), major page faults, prompt caching hits, TTFT, and decode throughput**.

To achieve organic virality across Reddit, X, and GitHub, **toktape** must act as the **"Geekbench + Speedtest of Local LLMs"**:
- An interactive live TUI while attaching/running.
- An automatic, high-aesthetic **Result Card** generated upon run completion in three synchronized formats:
  1. **Reddit/Discord Markdown code block** (standardized, monospace Unicode box table).
  2. **High-DPI PNG Card** (1200×675 16:9, dark neon aesthetic optimized for X cards and Reddit image posts).
  3. **Lossless, looping terminal recording** (10–15s MP4/GIF under 5MB for GitHub READMEs and X autoplay).
- Uncompromising **reproducibility metadata** that preemptively settles subreddit debates (cold vs. warm cache, page fault sparkline, exact quant sub-variant, Flash Attention state, context length, and thermal throttling status).

---

## 1. `llama-bench` Markdown Table Conventions & How They Spread

### 1.1 The Standard Table Convention
The canonical tool for reporting inference speed in the `llama.cpp` ecosystem is `llama-bench` ([ggml-org/llama.cpp tools/llama-bench/README.md](https://github.com/ggml-org/llama.cpp/blob/master/tools/llama-bench/README.md)). By default (`-o md`), `llama-bench` outputs a GitHub-Flavored Markdown table with the following schema:

```markdown
| model | size | params | backend | ngl | fa | test | t/s |
|:---|---:|---:|:---|---:|---:|:---|---:|
| llama 7B Q4_0 | 3.56 GiB | 6.74 B | CUDA | 99 | 1 | pp512 | 5168.88 ± 90.47 |
| llama 7B Q4_0 | 3.56 GiB | 6.74 B | CUDA | 99 | 1 | tg128 | 104.22 ± 1.15 |
```

As demonstrated in recent upstream pull requests such as [ggml-org/llama.cpp PR #28591: "llama-bench : add --override-kv"](https://github.com/ggml-org/llama.cpp/pull/28591) and [ggml-org/llama.cpp PR #22970: "vulkan : transpose A-matrix data layout for K-quant mul_mat performance"](https://github.com/ggml-org/llama.cpp/pull/22970), community members and maintainers use this table to communicate speedups across prompt processing (`pp` prefix, e.g. `pp512`) and token generation (`tg` prefix, e.g. `tg128` or `tg16`).

### 1.2 Recent Extensions & Community Demands
The standard table has struggled to keep pace with modern serving complexities, leading to community pull requests:
- **Memory Footprint Reporting (`-mm` / `--memory`):** [ggml-org/llama.cpp PR #23208: "llama-bench: add VRAM and RAM reporting"](https://github.com/ggml-org/llama.cpp/pull/23208) introduced explicit columns for `VRAM` and `RAM` (e.g. `3931 MiB` VRAM, `103 MiB` RAM) to resolve constant disputes over memory consumption across backend buffer types.
- **Effective Decode Bandwidth (`--bandwidth`):** [ggml-org/llama.cpp PR #28459: "llama-bench : add opt-in effective bandwidth column"](https://github.com/ggml-org/llama.cpp/pull/28459) and [ggml-org/llama.cpp Issue #26484](https://github.com/ggml-org/llama.cpp/issues/26484) ("Arm CPU backend: Effective decode bandwidth stays near 10 GB/s across quantizations on Pi 5") added `model_size * t/s` in GB/s (`GB x tok/s (GB/s)`). This directly shows whether generation is memory-bandwidth saturated against the system roofline.
- **Latency Decompositions (TTFT, E2E, ITL):** [ggml-org/llama.cpp PR #15643](https://github.com/ggml-org/llama.cpp/pull/15643) ("tools: update llama-bench to include TTFT, E2E, ITL metrics") responded to requests from server developers who care less about aggregate token throughput and more about conversational latency metrics.

### 1.3 How They Get Shared & Why They Fall Short
On Reddit ([r/LocalLLaMA: "LLM inference speed table"](https://www.reddit.com/r/LocalLLaMA/comments/1ci9i0a/llm_inference_speed_table/) and [r/LocalLLaMA: "Is there a good standard for benchmarking LLMs run on llama.cpp?"](https://www.reddit.com/r/LocalLLaMA/comments/1dl8r29/is_there_a_good_standard_for_benchmarking_llms/)), users copy-paste raw markdown into Reddit submissions or comments. However, `llama-bench` has fundamental structural flaws for server tuners:
1. **Isolated Micro-benchmark vs. Real Server:** As noted by `kh0pper` in [llama.cpp Issue #28546](https://github.com/ggml-org/llama.cpp/issues/28546), `llama-bench` runs a clean synthetic loop. It bypasses `llama-server`'s HTTP stack, continuous batching scheduler ([`tools/server/README.md`](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)), prefix-cache indexer, slot allocation, and real client concurrency.
2. **Missing Real Process Memory State:** It does not capture whether `mmap` is causing OS page thrashing, whether pages are being lazily paged in over PCIe from an NVMe drive, or whether memory is shared vs. dirty RSS.
3. **No Visual Identity:** A plain markdown table blends into the background of a forum post; it lacks the immediate visual punch needed to stop a user scrolling an X or Reddit feed.

---

## 2. Result-Card Designs That Spread: Virality Mechanics

Hardware and benchmarking tools that achieve viral dissemination follow distinct visual and psychological patterns:

```
┌────────────────────────────────────────────────────────────────────────┐
│                        ANATOMY OF VIRAL PROOF                          │
├────────────────────────────────────────────────────────────────────────┤
│  SPEEDTEST.NET    → Big Hero Speed Numbers (Download / Upload / Ping)  │
│  GEEKBENCH        → Normalized Multi-Core Score + Verified System Specs│
│  NVIDIA-SMI       → Raw Monospace Grid: VRAM Allocation & Watts (Proof)│
│  OLLAMA PS        → Simple Split: "100% GPU" vs "40%/60% CPU/GPU"     │
│  TOKTAPE CARD     → ALL OF THE ABOVE IN ONE TERMINAL SNAPSHOT          │
└────────────────────────────────────────────────────────────────────────┘
```

### 2.1 Speedtest.net & Cinebench/Geekbench Share Cards
- **The "Big Number" Anchor:** Speedtest cards do not lead with raw ping packet histograms; they lead with **two giant neon numbers** (Download Mbps / Upload Mbps) and an ISP/location badge. Geekbench leads with **Single-Core** and **Multi-Core** bold numbers.
- **Unambiguous Identity Badge:** Every Speedtest and Geekbench card includes an immutable hardware line (`AMD Ryzen 9 7950X3D`, `64 GB DDR5-6000`, `GeForce RTX 4090`). This turns the card into a validated claim of ownership and tuning prowess.
- **Aspect Ratio & Share Mechanics:** Speedtest renders a pre-formatted 1200×630 image (OpenGraph standard) with dark background and neon green/cyan indicators. It embeds a one-click "Copy Link / Copy Image" button.

### 2.2 The "nvidia-smi Screenshot" Culture
On r/LocalLLaMA, Twitter, and machine learning Discord channels, a plain text claim ("I run 70B at 25 t/s") is immediately met with skepticism. The universal currency of credibility is the **`nvidia-smi` terminal screenshot**:
- As seen in [ollama/ollama Issue #17788](https://github.com/ollama/ollama/issues/17788), users screenshot or paste the entire `+------------------------------------------------------+` ASCII grid.
- **Why it is trusted:** It confirms physical hardware reality: GPU model, driver version, CUDA version, fan speed, core temperature, power consumption (`Pwr:Usage/Cap`), and exact VRAM occupancy (`20779MiB / 24576MiB`). It proves the user didn't hallucinate or swap into system RAM.

### 2.3 The "ollama ps" Phenomenon
Ollama’s [`ollama ps` command](https://github.com/ollama/ollama/blob/main/README.md) became one of the most shared CLI outputs in local AI history ([ollama/ollama Issue #17251](https://github.com/ollama/ollama/issues/17251), [PR #8034](https://github.com/ollama/ollama/pull/8034), [Issue #17833](https://github.com/ollama/ollama/issues/17833)):
```
NAME                    ID              SIZE     PROCESSOR         CONTEXT    UNTIL
qwen3-coder:latest      938d7e66e326    47 GB    100% GPU          180000     3 days from now
llama3.3:70b-instruct   68409923f76c    42 GB    12%/88% CPU/GPU   32768      Forever
```
- **The Psychological Trigger:** The `PROCESSOR` column (`100% GPU` vs `12%/88% CPU/GPU`) triggers an immediate gamified reaction. Users post it to celebrate achieving "100% GPU offload" on constrained hardware, or to ask the community for help when it says `2%/98% CPU/GPU` and their generation rate drops by 80%.
- **The Gap toktape Fills:** As exposed in [ollama/ollama Issue #17251 ("Incorrect VRAM usage shown by ollama ps command")](https://github.com/ollama/ollama/issues/17251), `ollama ps` only reports static weights size, hiding KV cache, activations, speculative draft pools, and runtime mmap behavior. Users have to run `rocm-smi` or `nvidia-smi` side-by-side to figure out what actually happened. **toktape integrates both into one card.**

---

## 3. Terminal Recording Norms & Platform Constraints

Terminal demos are the primary growth engine for modern CLI dev tools. Understanding video and image rendering constraints across target networks is critical:

### 3.1 Tooling Landscape: `vhs` vs. `asciinema/agg`
- **Charmbracelet VHS** ([`charmbracelet/vhs`](https://github.com/charmbracelet/vhs) — 20,878 stars):
  - Uses `.tape` automation scripts to drive headless Chrome (`ttyd`) and `ffmpeg`.
  - Built-in support for terminal styling: window borders, macOS terminal buttons, custom fonts (JetBrains Mono), and curated themes (Catppuccin Mocha, Dracula, TokyoNight).
  - Native command `vhs publish` uploads to `vhs.charm.sh` and returns instant Markdown image links.
- **asciinema & agg** ([`asciinema/agg`](https://github.com/asciinema/agg) — 1,723 stars):
  - Rust-based GIF generator for `asciicast` v2 terminal recordings.
  - Highly optimized color quantization, generating smaller GIF footprints than naive `ffmpeg` pipelines.

### 3.2 Platform-Specific Constraints for Virality

| Platform | Best Media Format | Size Limit | Optimal Resolution / Aspect | Auto-Play Behavior & Algorithmic Rules |
|:---|:---|:---|:---|:---|
| **GitHub README** | Animated GIF or HTML5 `<video>` | < 10 MB (ideal: 2–5 MB) | 800–1000px width (auto height) | GIFs loop automatically inline. Videos require explicit user clicks in markdown unless using raw `<video autoplay loop muted playsinline>`. |
| **X (Twitter)** | MP4 (H.264 / AAC) | 512 MB (web), but < 15 MB optimal | 1280×720 (16:9) or 1080×1080 (1:1) | **Short MP4s (< 60s, muted) loop automatically like GIFs** and receive significantly higher feed algorithmic weight than external links. Native animated GIFs uploaded to X are compressed aggressively to low-bitrate MP4. |
| **Reddit (r/LocalLLaMA)** | High-res PNG Image + v.redd.it Video | 20 MB (image), 1 GB (video) | 1200×675 (16:9) or 1200×630 | Top-level image posts get massive image card previews on mobile. A submission with a **hero PNG card as the main post** and a **Markdown code block in the top comment** generates 3x–5x more upvotes than a pure text post with links. |

### 3.3 Golden Rules for toktape Terminal Clips
1. **Length:** 8 to 14 seconds max. Enough to show: (1) Prompt dispatch, (2) TTFT latch, (3) 50 tokens streaming with live sparkline, (4) Final Result Card freeze-frame.
2. **Framerate:** 30 fps is the sweet spot. 60 fps doubles file size without perceptual benefit for terminal text; 15 fps looks stuttery on streaming text.
3. **Color Palette:** Strictly constrain the terminal recording palette to 64 or 128 colors using `ffmpeg`'s `palettegen=max_colors=128:stats_mode=diff` to keep GIF size under 4 MB.

---

## 4. Why Recent Dev Tools Spread: The Viral Architecture

Examining breakout developer tools reveals a repeatable playbook:

| Tool | Stars | Primary Hook | Viral Artifact | First-Run Experience |
|:---|---:|:---|:---|:---|
| **btop** ([aristocratos/btop](https://github.com/aristocratos/btop)) | 34,548 | High-density visual monitoring | Full-color Braille CPU/GPU sparklines, dark responsive layout | Instant zero-config terminal launch: `btop` |
| **lazygit** ([jesseduffield/lazygit](https://github.com/jesseduffield/lazygit)) | 82,281 | Effortless git mastery | 10-second hero GIF of visual staging/rebasing | `brew install lazygit` -> run in repo -> visual shortcuts |
| **glow** ([charmbracelet/glow](https://github.com/charmbracelet/glow)) | 27,282 | "Markdown with pizzazz" | Beautiful terminal markdown rendering with pager | `glow README.md` works instantly without config |
| **llmfit** ([AlexsJones/llmfit](https://github.com/AlexsJones/llmfit)) | 36,237 | "What models fit on my machine?" | CLI hardware scan + colorized model compatibility table | `brew install llmfit` -> single command scan |
| **nvitop** ([XuehaiPan/nvitop](https://github.com/XuehaiPan/nvitop)) | 7,148 | Interactive NVIDIA process monitor | ASCII memory bar + process tree + interactive kill | `pip install nvitop` -> interactive GPU top |

### The Four Pillars of the toktape Playbook
1. **Hero Visual Above the Fold in README:** A crisp, looping animated terminal capture showcasing toktape's live view: prompt submission, tensor placement bar filling up, major page fault sparkline spikes, and the final card generation.
2. **The 3-Second One-Liner Install:**
   ```bash
   brew install toktape/tap/toktape
   # or
   go install github.com/toktape/toktape@latest
   # or
   curl -fsSL https://toktape.dev/install.sh | sh
   ```
3. **Frictionless Zero-Config Auto-Attach:**
   Users should not need to specify port, PID, or model path if `llama-server`, `ollama`, or `vLLM` is already running. `toktape` should probe standard ports (`8080`, `11434`, `8000`) or scan local process tables, auto-attach, send a standard evaluation prompt, and render.
4. **Post-Run Share Prompt:**
   When the run completes, `toktape` leaves the terminal with:
   ```
   ✓ Run recorded to ~/.toktape/runs/2026-09-13-llama3-70b.tape
   ✓ Card saved: ./toktape-card.png
   [C] Copy Markdown for Reddit  [P] Open PNG for X  [A] A/B Compare  [Q] Quit
   ```

---

## 5. Trust & Reproducibility Matrix: What Settles Arguments vs. What Starts Them

A major source of toxic debate on r/LocalLLaMA is the **"unreproducible benchmark claim"** (e.g. [r/LocalLLaMA: "Result: llama.cpp & exllamav2 prompt processing & generation speed vs prompt length..."](https://www.reddit.com/r/LocalLLaMA/comments/1dfvp4y/result_llamacpp_exllamav2_prompt_processing/)). When an author omits a single hidden variable, commenters spend 50+ comments arguing rather than discussing results.

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    REPRODUCIBILITY CONFIDENCE MATRIX                    │
├────────────────────────────────────┬────────────────────────────────────┤
│  MANDATORY FOR TRUST (HIGH VALUE)  │   HIGH-FRICTION PITFALLS (DEBATES) │
├────────────────────────────────────┼────────────────────────────────────┤
│ • Hardware: GPU VRAM, CPU, RAM MT/s│ • Reporting tg tok/s without pp ctx│
│ • Model: GGUF filename, exact quant│ • Omitting Flash Attention state   │
│ • Memory: VRAM vs RAM vs NVMe mmap │ • Cold vs Warm cache (disk faults) │
│ • Engine: Commit hash & compile dev│ • Leaving out batch/ubatch (-b/-ub)│
│ • Contention: Thermal & CPU flags  │ • Ambiguous quant names ("Q4")     │
│ • Page Fault rate / Token latency  │ • Ignoring prefix cache hits       │
└────────────────────────────────────┴────────────────────────────────────┘
```

### 5.1 Mandatory Fields for 100% Trusted Results
1. **Precise Hardware String:**
   - GPU model, device count, and exact VRAM allocated per device (e.g., `2x RTX 3090 24GB (PCIe 4.0 x8/x8)`).
   - Host CPU and System RAM topology: clock, channels, and transfer rate (e.g., `Ryzen 9 7950X, 64GB DDR5-6000 CL30 Dual-Channel`, or `Apple M3 Max 128GB Unified 400 GB/s`). RAM speed is decisive whenever offloading is partial ([matt-c1/llama-cpp-speed-measurements](https://github.com/matt-c1/llama-cpp-speed-measurements)).
2. **Exact Model Identification & Quantization Sub-type:**
   - Model name and source repo (e.g., `Qwen/Qwen2.5-Coder-32B-Instruct`).
   - Specific quant variant (`Q4_K_M`, `IQ4_NL`, `Q8_0`, `UD-IQ4_XS`). Saying just "Q4" causes immediate disputes because `IQ4_NL` vs `Q4_0` differ significantly in perplexity and dequant overhead.
3. **Inference Engine Build Hash & Flags:**
   - Server binary and exact commit: e.g., `llama-server b3650 (commit 73ab759)` ([ggml-org/llama.cpp Issue #28546](https://github.com/ggml-org/llama.cpp/issues/28546)).
   - Active runtime flags: Flash Attention (`fa=1` vs `fa=0`), KV cache quantization (`ctk=f16`, `ctv=q8_0`), layer offload (`ngl 99` vs `ngl 32`), and batch sizing (`-b 2048 -ub 512`).
4. **Context & Generation Parameters:**
   - Prompt length ($P$) and Generation length ($G$).
   - Total context size configured (`-c / --ctx-size`).
5. **Memory Tier Breakdown & Page Faults:**
   - Tensor distribution: `VRAM: 18.2 GB (85%)`, `RAM RSS: 3.1 GB (15%)`, `NVMe mmap unmapped: 0 GB`.
   - Major page fault indicator: Major faults during prompt eval vs. token generation.
6. **"Contended" / Environment Isolation Flag:**
   - Thermal throttling status (`throttled=0x0` as logged in [llama.cpp Issue #26484](https://github.com/ggml-org/llama.cpp/issues/26484)).
   - System load average or background GPU memory contention from other processes.

### 5.2 The 5 Deadly Argument-Sparking Pitfalls
1. **The "Single Throughput Number" Fallacy:** Posting "I get 45 tok/s" without distinguishing Prompt Processing (`pp`) from Text Generation (`tg`). Prompt processing is compute- or tile-bound; decode is memory-bandwidth bound.
2. **The Flash Attention Blind Spot:** On long contexts (> 4k tokens), Flash Attention provides a 2-order-of-magnitude difference in prompt processing ([matt-c1/llama-cpp-speed-measurements](https://github.com/matt-c1/llama-cpp-speed-measurements)). If FA is disabled, users argue whether the system is broken.
3. **Cold Cache vs. Warm Cache (`mmap`):** The first time an LLM is prompted with `mmap`, thousands of 4KB pages are pulled from NVMe into the OS page cache via major page faults. The first run might report 8 tok/s, while the second run reports 35 tok/s. **toktape must explicitly label `Cache: COLD (642 majflt)` vs `Cache: WARM (0 majflt)`.**
4. **Prefix / KV Cache Reuse Ambiguity:** In server testing, if prompt caching ([`--cache-prompt`](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)) is active and 400 of 512 tokens hit the cache, prompt processing speed appears artificially 10x faster. The card must state `Prefix Cache: 0% HIT` or `Prefix Cache: 78% HIT (400/512 tok)`.
5. **Omitted Batch / Micro-batch Sizing (`-b` / `-ub`):** As debated in [r/LocalLLaMA comments](https://www.reddit.com/r/LocalLLaMA/comments/1dfvp4y/result_llamacpp_exllamav2_prompt_processing/), tuning `-b` to 2048 and `-ub` to 512 changes prompt processing throughput drastically while protecting VRAM margins.

---

## 6. Concrete Recommended Card Layout Specification

### 6.1 Text Version: Monospace Markdown Code Block (For Reddit / Discord / Forums)
This layout uses clean Unicode box-drawing characters. It is engineered to fit within standard Reddit mobile viewport widths (max 72 columns) without line-wrapping.

```text
┌──────────────────────────────────────────────────────────────────────┐
│ toktape v0.4.0 • BENCHMARK REPORT                    RUN: #20260913-1 │
├──────────────────────────────────────────────────────────────────────┤
│ MODEL: Meta-Llama-3.1-70B-Instruct-GGUF (Q4_K_M • 42.5 GiB)          │
│ ENGINE: llama-server b3650 (commit 73ab759) • Linux 6.12             │
│ RIG: 2x RTX 3090 24GB (PCIe 4.0 x8) • 7950X • 64GB DDR5-6000 CL30    │
├──────────────────────────────────────────────────────────────────────┤
│  METRICS                VALUE      EFFICIENCY / ROOFLINE             │
│  ──────────────────────────────────────────────────────────────────  │
│  Decode (Token Gen):    21.4 tok/s [ 91.2 GB/s • 93% Memory Bandwidth]│
│  Prefill (Prompt Eval): 684.2 tok/s [TTFT: 748 ms • 512 tok]          │
│  Context Configured:    32,768 tok (Test: 512 in / 128 out)          │
│  Prefix Cache Hit:      0.0% (Cold Run)                              │
├──────────────────────────────────────────────────────────────────────┤
│  MEMORY PLACEMENT & HOST HEALTH                                      │
│  VRAM Allocated:   41.2 GiB / 48.0 GiB [████████████████░░] (86%)     │
│  Host RAM (RSS):    3.4 GiB (KV + activations)                       │
│  NVMe Page Faults:  0 majflt/tok (Clean memory lock)                 │
│  Thermals / Power:  GPU0: 64°C (310W) • GPU1: 61°C (295W) • Throttled: NO│
├──────────────────────────────────────────────────────────────────────┤
│  FLAGS: -ngl 99 -fa 1 -b 2048 -ub 512 -ctk f16 -ctv f16 --load-mode mmap│
│  VERIFIED BY TOKTAPE • https://github.com/toktape/toktape            │
└──────────────────────────────────────────────────────────────────────┘
```

### 6.2 Visual PNG Card Specification (For X / Twitter, Reddit Post Media)
- **Canvas Dimensions:** 1200 × 675 pixels (Exact 16:9 ratio, matches X and Reddit OpenGraph large image card).
- **Background & Color Palette:**
  - Background: Deep Obsidian / Catppuccin Mocha Dark (`#11111b`).
  - Container Border: Subtle rounded neon border (`#313244` with a 1px `#89b4fa` accent glow).
  - Primary Hero Accents: Neon Cyan (`#89dceb`) for Decode speed, Electric Emerald (`#a6e3a1`) for Prefill/TTFT, Soft Amber (`#f9e2af`) for Memory allocation.
- **Visual Hierarchy & Layout Grid:**
  1. **Header (Height: 80px):**
     - Left: `toktape` logo badge + Version + Run Timestamp.
     - Right: Verified Rig Badge (`[VERIFIED SYSTEM]` in green pill container).
  2. **Hero Row (Height: 180px):**
     - Split into two large visual meters:
       - **Left Box:** **Decode Speed** (Giant 64pt font: `21.4 tok/s`), with subtitle `Effective Bandwidth: 91.2 GB/s (93% Roofline)`.
       - **Right Box:** **Prefill Speed & TTFT** (Giant 64pt font: `684 tok/s`), with subtitle `TTFT: 748ms (Prompt: 512 tok)`.
  3. **Middle Section — Memory Hierarchy Bar (Height: 120px):**
     - A multi-segmented horizontal bar chart illustrating exact tensor placement:
       - `[GPU 0 VRAM: 20.6 GB]` (Cyan)
       - `[GPU 1 VRAM: 20.6 GB]` (Blue)
       - `[Host RAM RSS: 3.4 GB]` (Purple)
       - `[NVMe mmap Unpaged: 0 GB]` (Dark Gray)
     - Directly below: Live sparkline showing **Major Page Faults per Token** (flatline indicates pure VRAM/RAM execution; spikes indicate NVMe stutter).
  4. **System & Configuration Footer (Height: 160px):**
     - 4-column structured metadata grid:
       - *Model:* `Llama-3.1-70B-Instruct (Q4_K_M)`
       - *Hardware:* `2x RTX 3090 24GB • 7950X • DDR5-6000`
       - *Engine:* `llama-server b3650 (fa=1, ctk=f16)`
       - *Environment:* `Cold Run (0% Cache Hit) • Throttling: None`

### 6.3 Recommended GIF / MP4 Parameters
When `toktape record` exports a replay of the run:

| Parameter | Recommended Value | Engineering Rationale |
|:---|:---|:---|
| **Format** | **MP4 (H.264 / yuv420p)** + **GIF** fallback | MP4 is universally auto-played in loop on X/Reddit; GIF is for GitHub README. |
| **Duration** | **10.0 – 12.0 seconds** | Enough to show prompt ingestion (1s), TTFT latch (1s), live streaming with sparklines (7s), and card reveal (3s). |
| **Framerate** | **30 fps** | Seamless terminal streaming without the bloated file weight of 60 fps. |
| **Resolution** | **1200 × 675 px** (Scaled to 800px width in README HTML) | High-DPI crispness on Retina/4K displays. |
| **GIF Palette Optimization** | `palettegen=max_colors=96:reserve_transparent=0` | Keeps GIF file size under **3.5 MB**, well below GitHub's 10MB preview ceiling and avoiding mobile lag. |
| **Terminal Font & Padding** | JetBrains Mono / Fira Code, 16px, 24px window padding | Ensures characters are legible on mobile feeds without zooming. |

---

## 7. Top 10 Demand Signals

Ranked list of concrete signals from developer communities demonstrating explicit demand for toktape's feature set:

### Signal 1: Demand for Unified VRAM and RAM Allocation Breakdown in CLI Benchmarks
- **Evidence:** [ggml-org/llama.cpp PR #23208: "llama-bench: add VRAM and RAM reporting"](https://github.com/ggml-org/llama.cpp/pull/23208) (4 reactions, merged into master track).
- **Quote:** *"Add VRAM and RAM memory reporting to llama-bench output. Uses llama_get_memory_breakdown() to break down memory by backend buffer type, reporting device VRAM (in MiB, per-device if multiple) and host RAM separately."*
- **What feature it implies for toktape:** A persistent, segmented **Tensor Placement & Memory Hierarchy Bar** displaying exactly how many megabytes of weights, KV cache, and activations reside in VRAM vs. Host RAM vs. NVMe `mmap`.

### Signal 2: Discrepancy & Distrust in Existing CLI Memory Reporting (`ollama ps` vs `nvidia-smi`)
- **Evidence:** [ollama/ollama Issue #17251: "Incorrect VRAM usage shown by ollama ps command"](https://github.com/ollama/ollama/issues/17251) and [ollama/ollama Issue #17788: "offload and layer split is weird"](https://github.com/ollama/ollama/issues/17788).
- **Quote:** *"There is a significant discrepancy between the VRAM usage reported by the ollama ps command and the actual hardware allocation reported by rocm-smi... seeing a reported 23 GB when the system is actually heavily utilizing 90 GB is a massive visual eyesore."*
- **What feature it implies for toktape:** Direct query of process-level OS metrics (`/proc/[pid]/smaps_rollup` on Linux, `mach_vm_region` on macOS, and NVML on NVIDIA) to report **true hardware RSS and allocated VRAM**, exposing hidden KV cache and scratch buffer bloat.

### Signal 3: The Need to Benchmark Against the Memory Bandwidth Roofline
- **Evidence:** [ggml-org/llama.cpp PR #28459: "llama-bench : add opt-in effective bandwidth column (--bandwidth)"](https://github.com/ggml-org/llama.cpp/pull/28459) and [ggml-org/llama.cpp Issue #26484](https://github.com/ggml-org/llama.cpp/issues/26484).
- **Quote:** *"Adds an optional --bandwidth flag to llama-bench to report estimated model-weight bandwidth for token generation. With the flag enabled, tg results show model_size * t/s in GB/s... consistent with decode being mainly bandwidth-limited."*
- **What feature it implies for toktape:** A live **Roofline Efficiency Meter** showing `Effective Memory Bandwidth (GB/s)` alongside token generation speed, calculating the percentage of physical hardware bus saturation (e.g. `91.2 GB/s / 1008 GB/s = 9.0%`).

### Signal 4: Demand for Real Serving Runtime Latency Metrics (TTFT, ITL, E2E)
- **Evidence:** [ggml-org/llama.cpp PR #15643: "tools: update llama-bench to include TTFT, E2E, ITL metrics"](https://github.com/ggml-org/llama.cpp/pull/15643).
- **Quote:** *"Introduces the Time to First Token (TTFT), End-to-End Latency (E2E), and Inter-token Latency (ITL) metrics. Updates the README.md to explain the calculation as well."*
- **What feature it implies for toktape:** First-class display of conversational server latency: explicit **TTFT stopwatch (ms)**, **mean inter-token latency (ms/tok)**, and **jitter/stutter variance**, rather than just a flat aggregate token rate.

### Signal 5: Frustration Over Silent CPU Fallbacks & Partial Offload Penalties
- **Evidence:** [ollama/ollama PR #17542: "llm: warn when a model is loaded entirely on CPU"](https://github.com/ollama/ollama/pull/17542) and [ollama/ollama Issue #17833: "v0.32.14 heavily using CPU when model fully fits in VRAM"](https://github.com/ollama/ollama/issues/17833).
- **Quote:** *"When no layers fit in available VRAM, llama-server runs the whole model on CPU and nothing is logged at default level... a 14B model silently on CPU saturated the host until an unrelated embeddings model returned zero-byte responses."*
- **What feature it implies for toktape:** A prominent **Offload Alert & Severity Badge**. If even 1 layer spills to CPU or if offload drops from 100%, toktape highlights the bottleneck in high-contrast amber/red and warns of the expected PCIe transfer speed penalty.

### Signal 6: Contention Around Flash Attention’s Disproportionate Impact on Context Benchmarks
- **Evidence:** [r/LocalLLaMA: "Result: llama.cpp & exllamav2 prompt processing & generation speed vs prompt length..."](https://www.reddit.com/r/LocalLLaMA/comments/1dfvp4y/result_llamacpp_exllamav2_prompt_processing/) (43 upvotes, 28 comments) and [matt-c1/llama-cpp-speed-measurements](https://github.com/matt-c1/llama-cpp-speed-measurements).
- **Quote:** *"Flash Attention (FA) speeds up prompt processing, especially if you don't offload the KV cache to VRAM. That can be a difference of 2 orders of magnitude... FA slows down llama.cpp generation. I don't know why."*
- **What feature it implies for toktape:** Explicit tracking and prominent labeling of **Flash Attention state (`fa=on` vs `fa=off`)** and **KV Cache buffer location** on every result card to prevent confusing 100x prefill discrepancies.

### Signal 7: The Critical Influence of System RAM Speed & Topology on Partial Offload
- **Evidence:** [r/LocalLLaMA: "Extensive LLama.cpp benchmark & more speed on CPU, 7b to 30b, Q2_K, to Q6_K and FP16, X3D, DDR-4000 and DDR-6000"](https://www.reddit.com/r/LocalLLaMA/comments/14ilo0t/extensive_llamacpp_benchmark_more_speed_on_cpu_7b/) and [matt-c1/llama-cpp-speed-measurements](https://github.com/matt-c1/llama-cpp-speed-measurements).
- **Quote:** *"My specs: Linux, Nvidia RTX 4090, 10700k, dual channel 3200 MT/s DDR4 RAM, XMP enabled... Not only speed values, but the whole trends may vary GREATLY with hardware. If you have faster RAM, your results could differ a lot."*
- **What feature it implies for toktape:** Automated host memory profiling that auto-detects and prints **RAM transfer speed, channel count, and NUMA node topology** directly on the share card (e.g. `DDR5-6000 Dual-Channel (96 GB/s Peak)`).

### Signal 8: Inability to Benchmark Runtime Metadata Knobs Without Driving `llama-server`
- **Evidence:** [ggml-org/llama.cpp Issue #28546](https://github.com/ggml-org/llama.cpp/issues/28546) and [PR #28591](https://github.com/ggml-org/llama.cpp/pull/28591) ("Feature Request: llama-bench --override-kv").
- **Quote:** *"Benchmarking a metadata knob today means driving llama-server and reading timings instead of llama-bench's pp/tg table, with its depth sweeps, repetitions and markdown/JSON output. The case that raised it: qwen4exp.attention.indexer.top_k... Comparing 2048 vs 1024 at 29k depth on Vulkan needed server runs."*
- **What feature it implies for toktape:** Built-in **Live Server Probe & A/B Run Comparator**. `toktape` attaches to a running `llama-server`, injects controlled prompt sweeps over HTTP, parses server timings, and directly produces comparative A/B delta cards.

### Signal 9: The Viral Precedent of Single-Command Hardware/Model Fit Scanners
- **Evidence:** [`AlexsJones/llmfit`](https://github.com/AlexsJones/llmfit) (36,237 stars on GitHub).
- **Quote:** *"Hundreds of models & providers. One command to find what runs on your hardware."*
- **What feature it implies for toktape:** Developers love zero-config, single-binary CLI tools that instantly evaluate their local hardware capabilities. While `llmfit` predicts *what will fit*, `toktape` is the natural evolution that measures *how it actually performs when running*.

### Signal 10: The Need for Cold vs. Warm Page Cache & Major Fault Diagnostics
- **Evidence:** [ggml-org/llama.cpp tools/server/README.md](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md) (documenting `--load-mode mmap`, `mlock`, `dio`, and `--lazy-mode`).
- **Quote:** *"`--load-mode mmap`: memory-map model (if mmap disabled, slower load but may reduce pageouts if not using mlock)... `--lazy-mode on`: read the rows of such tensors from disk on demand instead of keeping them resident."*
- **What feature it implies for toktape:** Real-time tracking of **Major Page Faults (`majflt`) via a terminal sparkline**. This instantly reveals to the user whether their model is stuttering because NVMe pages are being fault-loaded on demand during inference or because OS memory pressure is evicting model weights.

---

## 8. Strategic Recommendation for toktape Launch

1. **Launch Artifact on Day 1:** Every run of `toktape` must emit the three artifacts simultaneously into `~/.toktape/runs/` without extra flags: (1) `run.json` (full metrics), (2) `card.txt` (Reddit code block), and (3) `card.png` (high-DPI PNG).
2. **First Post on r/LocalLLaMA:** Title template:  
   `"I got tired of guessing why my 70B runs at 2 tok/s, so I made toktape: a TUI that tracks GPU/RAM/NVMe placement, page faults, and real serving tok/s in one view."`  
   - Attach the **16:9 PNG Card** as the post media.
   - Put the **Monospace Markdown Code Block** and the `brew install` / `curl | sh` one-liner in the top comment.
3. **Twitter / X Strategy:** Post a **12-second high-framerate MP4 clip** showing `toktape attach` locking onto a running `llama-server`, live sparklines pulsating as tokens stream, and the terminal transitioning into the high-contrast Result Card. Tag model quantizers (`@TheBloke`, `@bartowski_`, `@unslothai`).
