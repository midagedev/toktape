# Community Demand Signals for Local LLM Serving Observability: A Field Research Report for "toktape"

**Date of Research:** September 13, 2026  
**Analyst:** Antigravity Research / AI Systems & Local Inference Infrastructure  
**Target Product:** `toktape` (Go TUI & recording observer for `llama.cpp`, `ik_llama.cpp`, Ollama, and vLLM)

---

## 1. Executive Summary & Ecosystem Landscape (2026)

By late 2026, the local open-weights LLM landscape has fundamentally shifted from small dense models (7B/14B) to massive, multi-tiered **Mixture-of-Experts (MoE)** architectures (e.g., DeepSeek-R1/V3 671B, Qwen 3.5/3.6 MoE series, and Kimi K2). Because state-of-the-art models exceed the physical VRAM capacity of single or dual consumer GPUs (16 GB on RTX 5080, 24 GB on RTX 3090/4090/5090), practitioners no longer operate in a binary "fits in VRAM" or "runs on CPU" paradigm. 

Instead, local inference has become an exercise in **heterogeneous tiered-memory engineering**:
1. **GPU VRAM:** Latency-critical attention layers, routing gates, and first $N$ expert layers.
2. **System DRAM:** Active expert weights offloaded across high-bandwidth multi-channel memory (or unified memory on Apple Silicon and AMD Ryzen AI Max platforms).
3. **NVMe / Persistent Storage:** Inactive expert slices accessed on-demand via virtual memory file mappings (`mmap`).

Despite this architectural complexity, the observability tooling available to developers and home-lab tuners remains fragmented and primitive. Practitioners tune multi-gigabyte models using a chaotic combination of `nvidia-smi`, `htop`, `curl`, and manual stopwatches. This disconnect leads to pervasive tuning mistakes, false throughput claims, and hours lost diagnosing silent performance degradations (such as compute buffer allocations silently kicking MoE layers out of VRAM, or invisible NVMe major page faults dragging decode speeds down to sub-1 token/sec).

`toktape` targets this exact operational gap: a dedicated, zero-overhead Go terminal observer that attaches to a running inference server, runs reproducible probe prompts, inspects low-level process memory and hardware residency, and captures the entire telemetry run into a portable, diffable artifact.

---

## 2. Competitive & Tooling Ecosystem Audit: The Observability Gap

An audit of existing open-source tools across GitHub reveals high community demand for hardware sizing and monitoring, but a complete absence of unified runtime observability that correlates OS memory paging with LLM token streaming.

```
+---------------------------------------------------------------------------------------------------+
|                                 The Local LLM Observability Gap                                    |
+------------------------------------+--------------------------------------------------------------+
| Tool Category                      | Shortcomings for LLM Tuners                                  |
+------------------------------------+--------------------------------------------------------------+
| Hardware-Level Monitors            | Blind to token generation, TTFT, KV cache allocation,         |
| (nvitop, btop, nvtop)              | context length, or GGUF tensor-to-device mapping.            |
+------------------------------------+--------------------------------------------------------------+
| Static Calculators                 | Sizing based on static weights; completely ignores dynamic   |
| (llmfit, can-it-run)               | compute scratch buffers, KV expansion, and MoE routing.      |
+------------------------------------+--------------------------------------------------------------+
| Community Launchers                | Automate CLI flags but lack live runtime telemetry, process  |
| (ggrun, llama-server-launcher)     | memory audit, or page-fault detection.                       |
+------------------------------------+--------------------------------------------------------------+
| High-Level Web/TUI Dashboards      | Parse basic stdout/server metrics, but lack deep memory      |
| (zerollama, LLMtop, localtok)      | residency insight (RSS vs mmap) or A/B run diffing.           |
+------------------------------------+--------------------------------------------------------------+
```

### 2.1 Hardware-Level Monitors: Blind to LLM Semantics
* **[XuehaiPan/nvitop](https://github.com/XuehaiPan/nvitop) (7,148 stars):** The de facto standard interactive GPU process viewer. While `nvitop` excels at displaying VRAM usage and Streaming Multiprocessor (SM) utilization percentage, it operates strictly at the driver/CUDA level. It cannot distinguish between model weights, KV cache buffers, and temporary compute graph scratch space, nor can it report token generation speeds, Time-To-First-Token (TTFT), or context window saturation.
* **[weby-homelab/LLMtop](https://github.com/weby-homelab/LLMtop) (4 stars):** An early attempt at a Rust-based TUI monitoring Ollama, `llama.cpp`, and vLLM. However, it only displays coarse top-level CPU/GPU metrics without process-level internal inspection.

### 2.2 Static Sizing Calculators: Great at Estimation, Blind at Runtime
* **[AlexsJones/llmfit](https://github.com/AlexsJones/llmfit) (36,237 stars):** The viral success of `llmfit` proves an enormous community hunger for answering: *"Will model X fit on my hardware configuration?"* However, `llmfit` is purely a static offline calculator. It computes theoretical parameter footprints based on quantization bit-depth, but cannot inspect actual runtime behavior, such as CUDA memory fragmentation, dynamic activation spikes, or OS swap thrashing.
* **[raketenkater/ggrun](https://github.com/raketenkater/ggrun) (272 stars):** A Go CLI wrapper for `llama.cpp` and `ik_llama.cpp` created specifically because calculating split tensor offloading across multi-GPU rigs (e.g., dual RTX 3090s) is math-heavy and error-prone. While it automates launch flags (`-sm`, `-ts`, `--fit`), it ceases to provide insight once the process starts.
* **[thad0ctor/llama-server-launcher](https://github.com/thad0ctor/llama-server-launcher) (126 stars):** A GUI wrapper reflecting the steep learning curve of `llama-server` flags, yet offering no real-time telemetry once the server is listening.

### 2.3 Early LLM-Specific Dashboards: Surface Metrics Without Memory Realities
* **[jungrok5/zerollama-dashboard](https://github.com/jungrok5/zerollama-dashboard) (26 stars):** A single-HTML lightweight dashboard interacting with the `llama.cpp` server `/slots` endpoint to visualize per-slot token state. It highlights that tuners care deeply about slot allocation and context caching, but it lacks any memory residency or OS-level diagnostic engine.
* **[amanzainal/localtok](https://github.com/amanzainal/localtok) (Launched mid-2026):** Positioned as *"htop for local LLM inference: tokens/sec, VRAM, loaded models"*. It confirms direct community demand for a dedicated CLI monitor, but relies solely on polling standard high-level engine endpoints without linking performance to kernel memory paging.
* **[TiniLLM/ollama-token-monitor](https://github.com/TiniLLM/ollama-token-monitor) (3 stars) & [abhiFSD/llama.cpp-Monitor-Dashboard](https://github.com/abhiFSD/llama.cpp-Monitor-Dashboard) (8 stars):** Real-time monitoring scripts capturing streaming tokens/sec, but completely ignoring memory tier placement (GPU vs RAM vs NVMe).

---

## 3. Deep Dive: Community Demand Signals & Tuning Bottlenecks

### 3.1 The MoE Tensor Placement & VRAM Cannibalization Crisis

The dominant hardware configuration in the local LLM community is an asymmetric multi-tier setup (e.g., one or two 16 GB/24 GB GPUs paired with 64 GB–128 GB of DDR4/DDR5 system RAM). In the extensive benchmark report **["Follow-up: Qwen3.5-35B-A3B — 7 community-requested experiments on RTX 5080 16GB" by u/gaztrab](https://www.reddit.com/r/LocalLLaMA/comments/1rg4zqv/followup_qwen3535ba3b_7_communityrequested/) (592 upvotes, 194 comments)**, several critical hardware interactions were uncovered:

1. **The Compute Buffer vs. Layer Offload Trap:**
   When running `llama-server` with `--fit on`, the engine automatically offloads as many layers to VRAM as will fit. However, when users specify high batch and micro-batch parameters (e.g., `-b 4096 -ub 4096`), `llama.cpp` reserves several gigabytes of VRAM for compute scratch buffers (`compute_buffer`). 
   * As u/gaztrab documented, setting `-b 4096` on a 16 GB RTX 5080 consumed VRAM that otherwise would have housed expert layers, silently dropping offloaded layers back to system RAM and causing a **44% drop in throughput** (from 68 tok/s down to 38 tok/s).
   * Removing explicit `-b`/`-ub` flags allowed `--fit on` to retain all critical layers on the GPU, regaining peak performance.
   * **The Tuner Pain:** Current tools only show "VRAM is 95% full." The tuner cannot see *what* is occupying that VRAM (model weights vs KV cache vs compute scratch buffers).
2. **The Ollama Abstraction Penalty:**
   In the same study, running the exact same model weights on Ollama resulted in a **3x performance deficit** compared to fine-tuned `llama-server`. The investigation identified that Ollama offloads strictly on a monolithic per-layer basis rather than supporting expert-only CPU offload (referencing open architectural issues like `ollama/ollama#12333`), while defaulting to uncompressed `f16` KV caches.
3. **Fine-Grained Tensor Splitting:**
   Advanced tuners actively use forks like **[ikawrakow/ik_llama.cpp](https://github.com/ikawrakow/ik_llama.cpp) (3,219 stars, 116 open issues)** to unlock experimental flags such as `-cmoe` / `--cpu-moe` and `-ncmoe` / `--n-cpu-moe` (offloading only routing and active attention to GPU while directing feed-forward expert matrices to CPU memory), or utilizing `-ot` / `--override-tensor` within standard `llama.cpp` ([llama.cpp tools/server documentation](https://github.com/ggerganov/llama.cpp/tree/master/tools/server)). 
   * Without an attached observer showing tensor layer placement across physical devices, configuring these flags is pure trial-and-error.

---

### 3.2 The "mmap Illusion", Major Page Faults, and NVMe Thrashing

One of the most viral technical discussions in r/LocalLLaMA history occurred in **["DeepSeek R1 671B over 2 tok/sec *without* GPU on local gaming rig!" by u/VoidAlchemy](https://www.reddit.com/r/LocalLLaMA/comments/1idseqb/deepseek_r1_671b_over_2_toksec_without_gpu_on/) (1,332 upvotes, 320 comments)**. 

The author demonstrated running a 400 GB+ MoE model on a gaming PC equipped with only 64 GB of physical RAM by exploiting Linux memory-mapped files (`mmap` via fast PCIe 4.0/5.0 NVMe SSDs). This triggered intense debate and confusion across the community regarding how OS paging affects inference:

* **The Virtual Memory Misunderstanding:**
  Users inspect `top` or Task Manager, see `VIRT = 400 GB`, and mistakenly believe the model is "loaded in RAM." In reality, `mmap` merely maps the file descriptors into the virtual address space. The actual resident physical memory (`RES` / `RSS`) is only 40–50 GB, while the rest remains unread on the NVMe disk.
* **Autoregressive Major Page Faults (`majflt`):**
  During prompt prefill, sequential weights can be streamed or cached. But during autoregressive decode, different MoE experts are randomly activated token-by-token. If an expert's weights are not in the OS page cache, the CPU thread immediately blocks on a **major page fault**, pausing execution while the kernel fetches 4 KB/2 MB pages synchronously from the SSD over the PCIe bus.
* **Performance Stalls:**
  Whenever active expert working sets exceed available RAM, generation speed abruptly collapses from 10+ tok/s down to 0.2–1.5 tok/s. Tuners frequently ask: *"Why did my generation suddenly freeze mid-sentence?"*
* **The `toktape` Opportunity:**
  By sampling `getrusage(RUSAGE_SELF)` or `/proc/[pid]/stat` to plot a **real-time sparkline of major page faults per generated token**, `toktape` would immediately make disk-thrashing visible. If a token incurs 400 `majflt`, the tuner instantly knows: *Generation is bound by NVMe random-read IOPS, not CPU compute.*

```
+---------------------------------------------------------------------------------------------------+
|               Simulated toktape Live Sparkline: Paging Bottleneck Detection                       |
+---------------------------------------------------------------------------------------------------+
| Token Stream: "The architecture of distributed expert routing requires..."                       |
| Decode Speed: [ 18.2 tok/s | 17.9 tok/s |  1.1 tok/s |  0.8 tok/s | 18.0 tok/s ]                  |
| Maj Page Flts: _ _ _ _ _ _ _ _ _ _ _ _ _ █ █ █ █ █ █ █ █ _ _ _ _ _ _                             |
|               ^ (Warm DRAM/GPU)          ^ (NVMe Page Fault Stall)  ^ (Back in Cache)             |
+---------------------------------------------------------------------------------------------------+
```

---

### 3.3 Tiered Memory & Exotic Builds: Optane PMem, CXL, and Unified UMA

Beyond standard desktops, community members build specialized hardware rigs to conquer large model inference:
* **Intel Optane Persistent Memory:** In **["Computer build using Intel Optane Persistent Memory - Can run 1 trillion parameter model at over 4 tokens/sec" by u/APFrisco](https://www.reddit.com/r/LocalLLaMA/comments/1taeg8h/computer_build_using_intel_optane_persistent/) (973 upvotes, 184 comments)**, the author coupled 24 GB VRAM with App Direct Optane PMem. The author highlighted the necessity of using `--override-tensor` (`-ot`) to pinpoint attention layers to VRAM while routing MoE experts to PMem.
* **Unified Memory (Apple Silicon & AMD Strix Halo):** In **["Qwen3 235B-A22B on a Windows tablet @ ~11.1t/s on AMD Ryzen AI Max 395+ 128GB RAM" by u/Invuska](https://www.reddit.com/r/LocalLLaMA/comments/1kd5rua/qwen3_235ba22b_on_a_windows_tablet_111ts_on_amd/) (504 upvotes)**, benchmarks proved that unified architectures sidestep PCIe bus bottlenecks, but introduce memory bandwidth contention between CPU and iGPU. Tuners in these threads obsess over `--no-mmap` vs `mmap`, `--mlock`, and memory interleaving configurations.
* **Extreme Multi-GPU Enclosures:** In **["768Gb Fully Enclosed 10x GPU Mobile AI Build" by u/qsivg40l](https://www.reddit.com/r/LocalLLaMA/comments/1qi4uj2/768gb_fully_enclosed_10x_gpu_mobile_ai_build/) (964 upvotes)**, tuning across 10 discrete GPUs required precise tracking of inter-card P2P PCIe transfers, thermal throttling under sustained context evaluation, and tensor distribution across buses.

---

### 3.4 Disentangling Latency: TTFT, Prefill vs. Decode, and KV Cache Compaction

A recurring theme in tuning threads is the severe conflation of prompt processing (prefill) and token generation (decode). In **["Running a 1 Trillion Parameter Model on a PC with 128 GB RAM + 24 GB VRAM" by u/pulse77](https://www.reddit.com/r/LocalLLaMA/comments/1ow0jj0/running_a_1_trillion_parameter_model_on_a_pc_with/) (343 upvotes)**, commenters pointed out multiple flawed benchmarking attempts where users divided total elapsed time by total output tokens, reporting artificially deflated generation rates.

Key dynamics tuners must monitor independently:
1. **Time-To-First-Token (TTFT) / Prompt Processing Rate (PP tok/s):** Prefill is compute- and memory-bandwidth bound over large matrix-matrix operations (`GEMM`).
2. **Autoregressive Generation Rate (TG tok/s):** Decode is strictly memory-bandwidth bound over vector-matrix operations (`GEMV`).
3. **KV Cache Footprint & Compression:** As context lengths expand to 32k, 64k, or 128k, uncompressed `f16` KV caches rapidly devour 8 GB–16 GB of VRAM. Community benchmarks like **[janvitos's Qwen3.6 35B MTP evaluation](https://www.reddit.com/r/LocalLLaMA/comments/1t82zxv/80_toksec_and_128k_context_on_12gb_vram_with/) (674 upvotes)** demonstrate that quantizing the KV cache (`-ctk q8_0 -ctv q8_0`) recovers vital gigabytes of VRAM to offload more model layers, directly boosting decode speed without sacrificing accuracy.

---

### 3.5 Speculative Decoding & Multi-Token Prediction (MTP) Verification

In **["80 tok/sec and 128K context on 12GB VRAM with Qwen3.6 35B A3B and llama.cpp MTP" by u/janvitos](https://www.reddit.com/r/LocalLLaMA/comments/1t82zxv/80_toksec_and_128k_context_on_12gb_vram_with/) (674 upvotes, 169 comments)**, the author detailed achieving an extraordinary 80 tok/s on an entry-level RTX GPU using native Multi-Token Prediction (MTP) and speculative drafting.

The major operational hurdle with speculative decoding (`-md draft_model.gguf` or native MTP heads):
* The user cannot easily observe the **draft acceptance rate** ($\alpha$). If the draft model acceptance rate drops below 40–50%, speculative decoding actually runs *slower* than standard autoregressive decode due to the verification overhead.
* Tuners currently must enable verbose engine logging (`--verbose`) and manually parse stdout after completion to see accepted vs rejected token tallies.
* **`toktape` Implication:** Real-time visual display of speculative draft acceptance rates per token and rolling acceptance efficiency percentages.

---

## 4. Recurring Community Measurement Mistakes (What `toktape` Prevents by Design)

Local LLM tuning discussions are filled with unreproducible numbers caused by standard measurement pitfalls. `toktape` can eliminate these systemic errors:

| Mistake | What Happens in the Community | How `toktape` Solves It |
| :--- | :--- | :--- |
| **1. The Blended Metric Trap** | Users time a request with `time curl ...` and divide `total_seconds / output_tokens`. A 4,000-token prompt with 50 tokens of output appears to run at 2 tok/s, obscuring a 400 tok/s prefill and a 35 tok/s decode. | Separates TTFT (ms), Prompt Evaluation (PP tok/s), and Autoregressive Decode (TG tok/s) into dedicated, distinct panels. |
| **2. Cold OS Cache vs. Warm Cache Distortion** | A user tests Model A after a fresh boot (cold disk read, heavy IO wait), then tests Model B immediately after (warm Linux buffer cache). Model B appears 4x faster, leading to false conclusions about quants or flags. | Inspects kernel process memory (`minflt` vs `majflt`) and flags runs as **COLD (high disk I/O)** or **WARM (in-memory)** in the recorded run card. |
| **3. Virtual vs. Resident Memory Misreading** | Users report: *"DeepSeek 671B runs completely in my 64GB RAM!"* because `mmap` allocated 350 GB virtual address space without throwing an OOM error, unaware that only 40 GB is resident and the rest is faulting per token. | Displays a strict breakdown: **VIRT** (total address space), **RSS** (resident in physical RAM), **Mapped/Unloaded** (backed by disk/NVMe), and **Swap** usage. |
| **4. Silent Prompt-Cache Invalidation** | A user benchmarks context processing speed assuming prompt caching is active (`--cache-prompt`), unaware that a slight variation in system prompt or BOS token caused a complete cache miss. | Queries `/slots` or inspects server response metrics to display an explicit **Cache Hit % / Prefix Reused Tokens** badge on every probe run. |
| **5. Thermal & Power Throttling Skew** | A GPU runs hot during a 10-minute long-context test; clock speeds throttle from 2,800 MHz to 1,900 MHz. Subsequent quant tests run slower, misattributed to the quantization scheme. | Records GPU core temperature, power draw (W), and clock speeds alongside token throughput throughout the run. |

---

## 5. Anatomy of the "Money Shot": What Makes a Benchmark Shareable & Viral

Across viral benchmark posts on r/LocalLLaMA (e.g., [gaztrab's RTX 5080 study](https://www.reddit.com/r/LocalLLaMA/comments/1rg4zqv/followup_qwen3535ba3b_7_communityrequested/), [VoidAlchemy's R1 post](https://www.reddit.com/r/LocalLLaMA/comments/1idseqb/deepseek_r1_671b_over_2_toksec_without_gpu_on/), and [qsivg40l's 10x GPU build](https://www.reddit.com/r/LocalLLaMA/comments/1qi4uj2/768gb_fully_enclosed_10x_gpu_mobile_ai_build/)), high-engagement submissions invariably follow a consistent visual and technical anatomy.

To maximize community adoption, every `toktape` probe run should generate a standardized, shareable **Run Card** (renderable as UTF-8 terminal text, Markdown table, or ANSI/SVG image):

```
+===================================================================================================+
| TOKTAPE RUN CARD #042: Qwen3.5-35B-A3B (UD-Q4_K_M) on llama-server (b3620)                       |
+===================================================================================================+
| HARDWARE SPEC:                                                                                    |
|  * GPU: NVIDIA GeForce RTX 5080 (16,376 MB VRAM) @ PCIe 4.0 x16                                  |
|  * CPU: AMD Ryzen 9 9950X (16C/32T) | RAM: 64 GB DDR5-6000 (CL30)                                |
|  * Storage: Samsung 990 PRO 2TB NVMe (Direct I/O mmap)                                           |
+---------------------------------------------------------------------------------------------------+
| TENSOR PLACEMENT & RESIDENCY:                                                                     |
|  * Layers Offloaded: 40/40 (Attention & Routing on sm_120 GPU | MoE Experts Tiered)              |
|  * VRAM Allocation: 14,812 MB Total [Weights: 11.2 GB | KV Cache: 1.8 GB (q8_0) | Buf: 1.8 GB]   |
|  * Process Memory:  VIRT: 24.1 GB | RSS: 18.2 GB | Mapped Unloaded: 5.9 GB | Swap: 0 MB          |
+---------------------------------------------------------------------------------------------------+
| PROMPT PROBE BENCHMARK (Context: 8,192 tokens | Generation: 512 tokens):                          |
|  * Prompt Cache Status: HIT (7,680 / 8,192 tokens reused - 93.8% prefix hit)                     |
|  * TTFT:                142.6 ms                                                                  |
|  * Prompt Processing:   2,450.2 tok/s                                                             |
|  * Autoregressive Gen:  68.4 tok/s (P50: 68.9 | P95: 66.2 | P99: 58.1)                           |
|  * Memory Stalls:       0 major page faults / token (0.0% IO wait stall)                         |
|  * Speculative MTP:     Accepted: 382/512 (74.6% draft head acceptance rate)                      |
+---------------------------------------------------------------------------------------------------+
| LAUNCH REPRODUCTION FLAGS:                                                                        |
|  $ llama-server -m Qwen3.5-35B-A3B.gguf --fit on -ctk q8_0 -ctv q8_0 --cache-reuse 256            |
+===================================================================================================+
```

### Why This Format Drives Viral Adoption:
1. **Self-Contained Reproducibility:** Contains the exact model tag, quantization format, engine build number, and launch flags.
2. **Disentangled Metrics:** Clear separation of prompt cache reuse, prefill speed, and sustained decode speed with percentiles ($P_{50}, P_{95}, P_{99}$).
3. **Hardware Truth:** Shows whether memory was genuinely resident in VRAM/RAM or silently paging off disk.
4. **Instant A/B Diffing:** Enables users to run `toktape diff run_041.tape run_042.tape` and immediately output a Git-style side-by-side terminal comparison showing the impact of changing `-ctk q8_0` or adjusting batch buffers.

---

## 6. Top 10 Community Demand Signals (Ranked List)

Below is the prioritized ranking of community demand signals, backed by real community evidence, indicating the exact features `toktape` must implement:

### Rank 1: The "Why is My Decode Slow?" Paging & NVMe Thrashing Indicator
* **Signal:** Tuners running large MoE models on constrained RAM platforms experience severe mid-sentence token stalls, misdiagnosing disk thrashing as CPU or GPU driver failures.
* **Evidence:** **[DeepSeek R1 671B over 2 tok/sec *without* GPU on local gaming rig by u/VoidAlchemy](https://www.reddit.com/r/LocalLLaMA/comments/1idseqb/deepseek_r1_671b_over_2_toksec_without_gpu_on/) (1,332 upvotes, 320 comments)**.
* **Feature Implied for `toktape`:** **Real-time Page Fault & IO Wait Sparkline.** Sample OS kernel counters (`getrusage` / `/proc/[pid]/stat` `majflt`) synchronously per generated token, visually flagging when an autoregressive decode pause is caused by an NVMe page fault.

### Rank 2: Visual Model Tensor Placement & Memory Breakdown
* **Signal:** Users lack visibility into how model layers are distributed across physical hardware (GPU VRAM vs CPU RAM vs NVMe), leading to accidental CPU offloads.
* **Evidence:** **[ikawrakow/ik_llama.cpp](https://github.com/ikawrakow/ik_llama.cpp) (3,219 stars, 116 issues)** (community demand for `-cmoe`, `-ncmoe`, and expert splitting) and **[ggrun by raketenkater](https://github.com/raketenkater/ggrun) (272 stars)** (Go tool created solely to compute multi-GPU VRAM layer splits).
* **Feature Implied for `toktape`:** **Interactive Tensor Residency Map.** Inspect server startup logs and process address spaces to render a live block diagram showing exactly which layers/experts reside in VRAM, System RAM, or Disk `mmap`.

### Rank 3: VRAM Cannibalization & Buffer Attribution
* **Signal:** High batch sizes (`-b 4096 -ub 4096`) allocate massive compute scratch buffers in VRAM, silently forcing `--fit on` to evict MoE layers to CPU and causing massive 44%+ performance drops.
* **Evidence:** **[Qwen3.5-35B-A3B — 7 community-requested experiments on RTX 5080 by u/gaztrab](https://www.reddit.com/r/LocalLLaMA/comments/1rg4zqv/followup_qwen3535ba3b_7_communityrequested/) (592 upvotes, 194 comments)**.
* **Feature Implied for `toktape`:** **VRAM Segmented Utilization Gauge.** Separate GPU VRAM into three distinct visual categories: Model Weights, KV Cache Buffers, and Compute Scratch Space (`compute_buffer`).

### Rank 4: Disentangled Latency Observability (TTFT vs. Prefill vs. Decode)
* **Signal:** Home-lab users regularly post flawed benchmarks conflating TTFT and generation rate by computing simple end-to-end averages over variable prompt sizes.
* **Evidence:** **[Running a 1 Trillion Parameter Model on a PC with 128 GB RAM + 24 GB VRAM by u/pulse77](https://www.reddit.com/r/LocalLLaMA/comments/1ow0jj0/running_a_1_trillion_parameter_model_on_a_pc_with/) (343 upvotes)**.
* **Feature Implied for `toktape`:** **Three-Stage Performance Metrics Panel.** Explicitly isolate Time-To-First-Token (ms), Prompt Processing Rate (PP tok/s), and Token Generation Rate (TG tok/s with jitter and percentiles).

### Rank 5: Live Prompt Cache Hit & Slot Reuse Telemetry
* **Signal:** Tuners rely heavily on prompt caching (`--cache-prompt`, `--cache-reuse`) for long agentic and multi-turn workflows, but cannot tell if a prompt actually hit the cache or suffered a silent prefix miss.
* **Evidence:** **[llama.cpp tools/server source & documentation](https://github.com/ggerganov/llama.cpp/tree/master/tools/server)** (`/slots` endpoint and `/props`) and **[zerollama-dashboard by jungrok5](https://github.com/jungrok5/zerollama-dashboard) (26 stars)**.
* **Feature Implied for `toktape`:** **Prompt Cache / Prefix Hit Inspector.** Query `llama-server`'s `/slots` before and during probe execution to report exact cache hit ratio, number of prompt tokens reused, and slot eviction events.

### Rank 6: Speculative Decoding & Multi-Token Prediction (MTP) Verification
* **Signal:** Tuners configuring speculative drafting or MTP heads cannot verify whether the draft model is actually accelerating decode or wasting compute due to poor acceptance.
* **Evidence:** **[80 tok/sec and 128K context on 12GB VRAM with Qwen3.6 35B A3B and llama.cpp MTP by u/janvitos](https://www.reddit.com/r/LocalLLaMA/comments/1t82zxv/80_toksec_and_128k_context_on_12gb_vram_with/) (674 upvotes, 169 comments)**.
* **Feature Implied for `toktape`:** **Draft Acceptance Tracker.** Stream speculative token validation stats in real time, displaying the rolling draft acceptance rate ($\alpha$) and net speedup multiplier.

### Rank 7: Process Memory Reality Check (RSS vs. VIRT vs. Never-Loaded)
* **Signal:** Deep confusion persists between virtual memory size, resident memory, and disk-backed file pages when loading models via `mmap` or `--no-mmap`.
* **Evidence:** **[Qwen3 235B-A22B on AMD Ryzen AI Max 395+ by u/Invuska](https://www.reddit.com/r/LocalLLaMA/comments/1kd5rua/qwen3_235ba22b_on_amd_ryzen_ai_max_395_128gb_ram/) (504 upvotes)**.
* **Feature Implied for `toktape`:** **Kernel Memory Residency Breakdown.** Display process memory metrics via OS APIs: Total Virtual (`VIRT`), Resident Set Size (`RSS`), Shared/File-backed Memory, and Untouched/Never-Loaded Pages.

### Rank 8: A/B Run Comparison & Diffing
* **Signal:** Tuners spend hours testing different quantization quants (`Q4_K_M` vs `UD-Q4_K_M`), context sizes, and thread counts (`-t 8` vs `-t 16`), struggling to manually track and compare performance deltas across runs.
* **Evidence:** **[club-3090 multi-engine community benchmarks by noonghunna](https://github.com/noonghunna/club-3090) (2,229 stars)**.
* **Feature Implied for `toktape`:** **Recorded Run Tape & `toktape diff` CLI.** Record every test run into a structured `.tape` file (JSON + run metadata), enabling a native `toktape diff runA.tape runB.tape` command that outputs delta percentages for TTFT, tok/s, VRAM, and page faults.

### Rank 9: High-Context KV Cache Bloat & Quantization Observer
* **Signal:** As users test 32k to 128k context windows, uncompressed KV caches consume all remaining VRAM, causing sudden out-of-memory crashes or silent layer offload degradation.
* **Evidence:** **[Qwen3.5-35B-A3B KV experiments by u/gaztrab](https://www.reddit.com/r/LocalLLaMA/comments/1rg4zqv/followup_qwen3535ba3b_7_communityrequested/)** (benchmarking `-ctk q8_0 -ctv q8_0` vs `f16`).
* **Feature Implied for `toktape`:** **Dynamic KV Cache Expansion Monitor.** Graph the exact memory consumed by the KV cache as context scales, tracking KV quantization format (`f16`, `q8_0`, `q4_0`) and remaining context headroom before VRAM saturation.

### Rank 10: Standardized "Shareable Run Card" Generation
* **Signal:** Community members love sharing benchmark achievements, but post screenshots of messy terminal outputs, partial specs, and incomplete flags, making reproduction nearly impossible.
* **Evidence:** **[768Gb Fully Enclosed 10x GPU Mobile AI Build by u/qsivg40l](https://www.reddit.com/r/LocalLLaMA/comments/1qi4uj2/768gb_fully_enclosed_10x_gpu_mobile_ai_build/) (964 upvotes)** and **[llmfit by AlexsJones](https://github.com/AlexsJones/llmfit) (36,237 stars)**.
* **Feature Implied for `toktape`:** **Single-Key Export of Verified Run Cards.** An export command (`toktape export --format [ascii|markdown|png]`) that outputs a clean, standardized summary card containing hardware specs, launch flags, memory breakdown, and performance percentiles.

---

## 7. Strategic Recommendations for `toktape` Product Architecture

1. **Zero-Invasive Attachment via Non-Destructive Probing:**
   `toktape` should operate in two distinct attachment modes:
   * **Sidecar Mode:** Attaches via PID inspection (`/proc/[pid]` on Linux, Mach task APIs on macOS) combined with HTTP polling of engine endpoints (`/props`, `/slots`, `/metrics` on `llama-server`).
   * **Probe Runner Mode:** Dispatches a standardized synthetic or user-defined prompt sequence over the local OpenAI-compatible or `llama.cpp` native endpoint, measuring TTFT, streaming tokens, and sampling OS memory counters synchronously.
2. **First-Class Support for `llama-server` & `ik_llama.cpp` First:**
   While vLLM and Ollama have significant user bases, the hardcore tuning community that cares about byte-level memory placement, MoE layer splitting, and quantization tricks overwhelmingly runs `llama.cpp` server and `ik_llama.cpp`. Securing this vocal enthusiast demographic will establish `toktape` as the benchmark gold standard.
3. **The Portable `.tape` File Format:**
   Implement `.tape` files as self-contained gzip-compressed JSON bundles containing:
   * System hardware profile (CPU, GPU VRAM, RAM, PCIe generation).
   * Engine version and CLI launch arguments.
   * Time-series telemetry arrays (token timestamps, memory residency, page fault events, GPU clock/temp).
   * Raw prompt and completion text with per-token latency markers.
   This transforms `toktape` from just a live TUI monitor into the **reproducibility standard** for the local LLM community.
