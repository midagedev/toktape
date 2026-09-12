# NVML: why this package shells out to nvidia-smi

Investigated 2026-09-13 for TTP-4. Conclusion: **v1 ships the nvidia-smi
backend only.** No NVML binding is linked.

## `github.com/NVIDIA/go-nvml` is out

It is the obvious binding, and it does not fit the repo's `no cgo` rule.
`go-nvml` does not link `libnvidia-ml.so` at build time — it `dlopen`s it at
runtime through its own `pkg/dl` package — but `dlopen`/`dlsym` themselves
are reached through cgo, and the generated `nvml/nvml.h` bindings are cgo
too. Building it with `CGO_ENABLED=0` fails; there is no pure-Go build tag.
So it would break `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...`,
which `scripts/check.sh` enforces, and it would make the macOS build depend
on a stub that has nothing to stub.

## What a pure-Go (purego) backend would have to do

`github.com/ebitengine/purego` can `dlopen` and call C functions without
cgo on linux/amd64, so a `//go:build linux` backend is *possible*. It would
need, at minimum:

- `dlopen("libnvidia-ml.so.1")`, falling through to `libnvidia-ml.so`, and
  degrading to the nvidia-smi backend when neither is present. The `Open`
  ordering in `gpu.go` already has the slot for this: `nvml -> nvidia-smi ->
  none`.
- `nvmlInit_v2` / `nvmlShutdown` (`Collector.Close` becomes real work).
- Inventory: `nvmlDeviceGetCount_v2`, `nvmlDeviceGetHandleByIndex_v2`,
  `nvmlDeviceGetName`, `nvmlDeviceGetMemoryInfo` (or `_v2`),
  `nvmlSystemGetDriverVersion`, `nvmlDeviceGetCurrPcieLinkGeneration`,
  `nvmlDeviceGetCurrPcieLinkWidth`, `nvmlDeviceGetUUID`.
- Samples: `nvmlDeviceGetUtilizationRates`, `nvmlDeviceGetTemperature`,
  `nvmlDeviceGetPowerUsage`, `nvmlDeviceGetClockInfo`,
  `nvmlDeviceGetCurrentClocksThrottleReasons` (the mask this package
  already decodes in `throttle.go`).
- Process attribution: `nvmlDeviceGetComputeRunningProcesses_v3`.

## The hard part, and why it is not worth it yet

Three of those calls do not fit purego's calling convention cleanly:

1. **`nvmlDeviceGetComputeRunningProcesses_v3` returns an array of
   `nvmlProcessInfo_t` by pointer with a caller-supplied count.** The struct
   changed shape between `_v1`, `_v2` and `_v3` (v3 adds `gpuInstanceId` and
   `computeInstanceId`), so the layout has to be hand-laid and kept in sync
   with the driver's, and the two-pass "call with count=0 to learn the
   size" idiom has to be written by hand. Getting the layout wrong reads
   garbage VRAM figures rather than failing loudly — exactly the class of
   bug the card must not print.
2. **`nvmlDeviceGetHandleByIndex_v2` yields an opaque handle** that must be
   carried as an `uintptr` across calls without the GC moving anything.
3. **purego cannot pass or return structs by value**, so anything like
   `nvmlMemory_t` and `nvmlUtilization_t` must go through out-pointers into
   pinned Go memory.

Against that: there is no NVIDIA GPU on the development machine, so a
purego backend would ship with **no test surface at all** — every parser in
this package is covered by fixture text, and none of that coverage would
transfer. nvidia-smi already answers every field the card prints.

## What NVML would actually buy us later

Worth revisiting only if one of these becomes the bottleneck:

- **Sample cost.** Each `Sample` is one or two `nvidia-smi` executions,
  20-200 ms on a multi-GPU box. `tape.DefaultSampleInterval` is 250 ms, so
  a synchronous sampler can miss its tick. NVML reads are microseconds.
- **Per-process VRAM without the uuid dance.** NVML returns the device
  handle with the process list, so `ProcBytes` attribution stops depending
  on the `gpu_uuid` column (see `QueryGPUFields` for why that column was
  added to the query).
- **Fields nvidia-smi does not expose per sample**, e.g. per-process
  utilisation via `nvmlDeviceGetProcessUtilization`.

## AMD

`rocm-smi` has the same shape (a CSV/JSON subprocess) and would be a third
backend behind the same `Collector` interface. It is explicitly Could-tier
in `docs/toktape-spec.ko.md` §3.3 because the interface differs between
distributions, so it is not attempted here. `Bandwidth` already carries the
common Radeon and Instinct parts so that an AMD backend only has to fill
the readings.
