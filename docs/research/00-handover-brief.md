# Brief: a live "cockpit" for people who tune local LLM serving

*Written 2026-09-13 as a hand-off to a separate session. Everything under
"What this machine taught us" was measured here; the survey was done the
same day with web search and may be stale in a month.*

## The idea

A tool for the person who spends evenings moving expert layers between
GPUs, RAM and NVMe, and wants to *see* what a change did: the machine's
spec and the model's shape, live resource use, prompt and decode tok/s,
and the actual tokens coming out — all in one view, and exportable as a
recording or a card that can be posted. `tools/v41-demo.py` in this repo is
the prototype: a two-pane terminal, chat on the left, machine on the right,
recorded with VHS. It was built for one demo and it is already the thing
people react to in the clip, which is the signal this is worth a real
project.

## Survey: what exists (2026-09-13)

| tool | what it does | what it lacks for this use |
| --- | --- | --- |
| [otop / ollama-token-monitor](https://github.com/TiniLLM/ollama-token-monitor) | htop-style TUI: live tok/s, CPU, Apple GPU, VRAM, loaded models, per-request table with sparkline | Ollama only; no llama.cpp/ik server; no model shape, no page-cache/NVMe view, no token text |
| [localtok](https://github.com/amanzainal/localtok) | same idea across Ollama and OpenAI-compatible servers | metrics only, no tokens shown, no per-layer placement, no export |
| [llama.cpp-Monitor-Dashboard](https://github.com/abhiFSD/llama.cpp-Monitor-Dashboard) | single HTML file over `/metrics` and `/slots`, tok/s, slots, optional GPU sidecar | browser, not terminal; no token stream; no RSS/major-fault view |
| [llama-cpp-dashboard](https://github.com/rsesha/llama-cpp-dashboard) | dark web dashboard over llama.cpp built-in metrics + `gpu_metrics_server.py` | same |
| Prometheus + Grafana guides ([gist](https://gist.github.com/Cirius1792/4ae126689db857cc8c2c26f1425c2692), [flox](https://github.com/flox/llamacpp-monitoring), [glukhov](https://www.glukhov.org/observability/monitoring-llm-inference-prometheus-grafana/)) | production observability, per-model throughput panels | a stack to install; averages over windows; nothing about *this* request's tokens |
| [nvitop](https://github.com/XuehaiPan/nvitop), [nvtop](https://github.com/Syllo/nvtop) | GPU process monitors | know nothing about tokens or the model |
| [llmfit](https://github.com/AlexsJones/llmfit) | detects hardware, predicts what fits and projected tok/s | a planner, not a live view |
| [LlamaStash](https://deepu.tech/introducing-llamastash/) | terminal launcher for llama.cpp | launches, does not watch |
| [OpenBenchmarking llama.cpp](https://openbenchmarking.org/test/pts/llama-cpp), community scoreboards ([knightli](https://knightli.com/en/2026/04/23/llama-cpp-gpu-benchmark-cuda-rocm-vulkan-scoreboard/)) | comparable benchmark numbers | a number, not a run |
| [tok/s visualizers](https://kamilstanuch.github.io/LLM-token-generation-simulator/) | show what N tok/s *feels* like | simulated |

Nothing found combines, for one live run: the model's placement (which
tensors sit where), the process's real memory picture (RSS vs. mapped vs.
never loaded, major faults per token), the request's own prompt/decode
rates, and the token text — in a terminal, recordable, with a shareable
summary. The closest are otop and localtok, both Ollama-first metric
dashboards. That is the gap.

## What this machine taught us (measured, and each one cost a re-take)

These are the things a naive implementation gets wrong. Every one is a
comment in `tools/v41-demo.py` or an entry in `log/`.

1. **tok/s must be measured over the content window only.** Count tokens
   that carried text, from the first to the last of them. Counting the
   empty role chunk and the finish chunk, dividing by wall time that keeps
   running after generation stops, or a render throttle whose `continue`
   skips the finish check — each dragged 18 tok/s down to a recorded 4.5.
   Better: read the server's own `timings` object (`predicted_per_second`,
   `prompt_per_second`, `predicted_n`, `prompt_n`) from the final stream
   event and show both; the client-side figure is a cross-check.
2. **Warm and cold are different numbers.** A prompt repeated in the same
   session hits the prompt cache and reads 0 page faults; a 19-token
   sample is not a decode rate. Show the prompt-cache hit (`cached_tokens`
   in `usage.prompt_tokens_details`) next to the rate, and refuse to label
   a rate "decode" below a token count.
3. **RSS is not "loaded".** With mmap, "in RAM" is the page cache, RSS is
   what the process has touched, and RSS is also short by whatever was
   copied to VRAM and dropped. "Never loaded" (the engram tables, 84.6 GB
   here) came from the tensor headers, not from total-minus-RSS, which
   overstated it by ~50 GB. Read the GGUF header for tensor sizes and
   placement; read `/proc/<pid>/status` (VmRSS, RssFile, RssAnon,
   RssShmem) for the memory picture; read `/proc/<pid>/stat` field 12
   (majflt) for the NVMe traffic per token — that sparkline is the most
   watched thing in the clip.
4. **The chat template decides what you are measuring.** `reasoning_effort:
   none` vs. thinking changed the answer from 60 tokens to 4,000. Whatever
   is sent, the view must show it (effort, template kwargs, `</think>`
   presence in the rendered prompt via `/apply-template`).
5. **CJK is two columns.** Aligning a box-drawn right panel next to Korean
   text requires `unicodedata.east_asian_width` and stripping ANSI before
   measuring; forgetting the second half shifted every coloured line 15
   columns. Font choice matters for recordings (D2Coding renders Hangul at
   exactly 2× Latin advance; most don't).
6. **A busy machine's numbers are void.** Another harness on the box moved
   decode by more than the effect being measured. The tool should read the
   load average and other GPU processes and *label* the run as contended.
7. **Recording has its own arithmetic.** VHS at 10 fps, 122 s for 2,000
   tokens; the GIF is 10× the mp4. Framerate and length should be derived
   from the expected token count and rate, not typed.
8. **Two servers, one file, one page cache.** Running a second server at
   `-ngl 0` beside the first shares the mapping and costs no extra RAM;
   the cockpit should be able to watch more than one server for A/B.

## Requirements

**Must**
- Attach to a running OpenAI-compatible server (llama.cpp `llama-server`,
  ik_llama.cpp, later Ollama/vLLM) by URL; find its PID locally for the
  `/proc` view when it is on the same host, or run a small sidecar on the
  host over SSH when it is not (the demo runs *on* the workstation for this
  reason).
- Send a prompt and stream the answer in a chat pane, with the rendered
  prompt visible on demand.
- Right pane, live: model name and file size; tensor placement summary
  (per device: GB, which layers/tensor classes — from the GGUF header +
  the server's log line or `-ot` args); RSS split; major faults per
  interval as a sparkline; VRAM per GPU; CPU load and thread count;
  prompt tok/s, decode tok/s, prompt-cache hits, time to first token.
- A run summary card at the end: hardware (CPU, RAM, GPUs), server build
  and flags, model + quant, context, the four rates, and a one-line
  "contended: yes/no" — as text, as an SVG/PNG, and as JSON.
- Deterministic terminal rendering so VHS can record it (no timing-based
  animation the recorder can't reproduce).

**Should**
- Watch two servers side by side with the same prompt (A/B for a flag).
- Read `/metrics` and `/slots` when `--metrics` is on, fall back to
  stream timings when it is not.
- Per-token timeline: a strip where each token's width is its latency,
  so a stall (expert page-in, engram fault burst) is visible as a gap.
- History: append every run's JSON to a local file; a `compare` command
  that diffs two runs.

**Could**
- A web view of the same data for a browser tab.
- Publish a card to a gist or the rig-log repo from the CLI.

## Architecture sketch

- One Python package, `rich`/`textual` TUI (the prototype is 275 lines of
  raw ANSI and it is already at the limit of what that can carry). A
  Rust/ratatui rewrite is an option once the data model is fixed; do not
  start there.
- `collectors/`: `server.py` (OpenAI stream + `/props`, `/slots`,
  `/metrics`, `/apply-template`), `proc.py` (`/proc` on Linux; `psutil`
  fallback), `gpu.py` (`nvidia-smi --query-gpu` CSV; `nvml` if present;
  Apple `powermetrics` later), `gguf.py` (header only, via `gguf-py`, to
  size tensors and classify them: attention / experts / embeddings /
  n-gram tables).
- `model.py`: one `RunSample` per interval and one `RunSummary` per
  request; JSON-serialisable; the card renders from `RunSummary` only.
- `cli`: `cockpit watch URL [--pid|--ssh host]`, `cockpit ask URL
  "prompt"`, `cockpit ab URL1 URL2 "prompt"`, `cockpit card run.json`.

## Verification plan for the implementing session

- Unit: the tok/s reducer against a recorded stream fixture with the three
  known traps (role chunk, finish chunk, throttle) — must produce the
  server's own `predicted_per_second` within 2 %.
- Unit: width function against a fixture of Hangul/Latin/ANSI lines — the
  right border column must be constant.
- Integration: against `llama-server` on this machine with the serving
  script in `configs/`; the summary card's four rates must match the
  server log's timings line for the same request.
- Recording: one VHS tape in `tools/`, 30 s, checked into `assets/` once,
  re-recorded only when the layout changes.

## Names to avoid

Not "monitor", "dashboard" or "top" — those are the tools above. The
thing is closer to a flight recorder with a live cockpit; pick something
in that direction.
