# Research: which run-configuration facts explain tok/s variance (same model, same GPU)

Question: two runs of the same model on the same GPU differ a lot "depending on the context settings" and the site shows nothing why. Rank the explanatory factors, map each to tape/index fields, and recommend the minimum additive index fields.

## 1. What the tape already carries (config/workload facts, not results)

Flags (the "five argument-starters", `internal/tape/tape.go:250-274`): `ngl` (:255), `fa` (:256), `b` (:257), `ub` (:258), `ctk` (:259), `ctv` (:260), `load_mode` (:261), `ot` (:262), `cpu_moe` (:263), `t` (:264), draft block `model_draft/draft_max/draft_min/draft_p_min` (:269-272), `other` (:273). Plus `n_slots` (:241), `ctx_size` (:242), full `args` (:239), engine `kind/build/commit` (:233-236), `engine_claim` (:247, OpenAI-protocol servers).

Model: `file_name` (:279), `dir` (:284), `quant` exact sub-type (:294), `file_bytes` (:295), `params` (:296), `n_experts` (:298), `n_experts_used` (:299), `active_bytes_per_token` (:304), `format` (:291, exl3), naming `base_name/size_label/finetune/name_source` (:337-340), `repo/repo_source` (:323-324).

Placement: per-device `bytes/classes/layers/active_bytes_per_token` (:474-494), `never_loaded_bytes` (:502), `source` (:505), VRAM split weights/KV/compute (:507-509).

Workload: `concurrency` (:125), `prompt_n` (:543), `cache_n` (:544), `predicted_n` (:547), `reasoning_n` (:548), `min_predicted_n` (:601), `short_streams` (:609), per-round `prompt_n/prompt_per_second/cache_n` (:666-685), `prompt_set` (:165), sampling `temperature/thinking/endpoint` (:809-811), `max_tokens` (:851), draft `draft_n/draft_n_accepted` (:555-556, run level = SUM over streams, :552-554).

Machine state: `continent` `contended/reasons/loadavg1/other_gpu_procs` (:737-740), `witnesses` per round edge (:746) with `io_some_avg10` (:757), `cpu_max_khz` (:771), `temp_c/sensor` (:772-773); GPU `power_w/clock_mhz/power_limit_w/throttled/throttle_mask` (:435-448); memory `maj_faults_per_token` (:537), cache `hit_tokens/prompt_total/hit_ratio/label` (:729-732).

Key semantics: above one stream, `Timings` is the **per-stream mean** (:126-130, :633-634); `Aggregate` is the server-wide view (:589-594). Run-level draft sums pool streams; every neighbouring rate is a mean.

## 2. What the index carries today (`internal/publish/index.go:25-78`)

Identity: `repo` (:40), `model_raw` (:41), `model_id` + source (:42-43), `model_dir` (:44), `params` (:45), `moe` (:46), `quant_raw/quant_id/quant_bits` (:47-49), `engine_kind/engine_version` (:51-52). Hardware: `os` (:55), `gpus_raw/gpu_id/gpu_ids/gpu_count/vram_bytes/ram_bytes/host_class` (:56-65). Work: `sessions` (:68), `prompt_set` (:69), `decode_per_sec/prefill_per_sec/ttft_p50_ms` (:70-72). Trust: `caveats/caveat_count` (:76-77). Above one stream the row uses the **aggregate** rate, falling back to per-stream mean (:160-166). Nothing about `-c`, prompt length, cache hit, flags, draft, clocks, or throttling — i.e. none of the variance explainers — is indexed.

## 3. What the project already decided a reader must see

Card contract (spec §4, items 5-9): decode+effective-GB/s, prefill+TTFT **with prompt token count**, configured ctx **with test in/out counts**, prefix-cache %, VRAM bar (weights|KV|buffer)+RSS+unloaded+majflt, thermals+contended, and the one-line flags `-ngl -fa -b -ub -ctk -ctv --load-mode -ot` (FA and `-b/-ub` "생략 불가"). Sharing research §5.1 mandates hardware string, exact quant sub-type, engine hash+flags, P/G lengths, placement+faults, contention; §5.2's five pitfalls are exactly the misread pairs in §6 below. Search is newest-first, no rank column, caveats ride every row (§9.4).

## 4. Ranked factors

Effect classes: **dominant** (>2x possible), **significant** (tens of %), **minor** (<~10%), **negligible-when-short**.

### (a) Dense GGUF, fully on GPU, decode tok/s

| Rank | Factor | Class | In tape? | In index? | First-time reader asks? |
|---|---|---|---|---|---|
| 1 | Concurrent streams/slots (per-stream vs aggregate; N streams share bandwidth) | dominant | `concurrency` (:125), `aggregate_*` (:610-612), `slots_busy_max` (:616), `peak_decoding_streams` (:630) | `sessions` only; rate is aggregate (:160-164) | yes — 01 §4 mistake #1 (blended metric) |
| 2 | Generated length: short runs are warmup-dominated (<32 toks is a "sample", :48/:564) | dominant-when-short | `predicted_n` (:547), `min_predicted_n` (:601), `short_streams` (:609) | no | yes — 01 §3.4 |
| 3 | Spill: any layer/KV/compute evicted to RAM (silent partial offload) | dominant when present | placement devices/classes (:474-494), VRAM split (:507-509) | no | yes — 01 §3.1 (44% drop), signal #5 |
| 4 | Weight quant bits (decode ≈ bandwidth/bytes; Q8 vs Q4 ≈ 1.5-2x) | significant | `quant` (:294), `file_bytes` (:295) | `quant_id/quant_bits` | yes — 02 §5.2 ("Q4" ambiguity) |
| 5 | Speculative decoding / MTP (accept <~50% ⇒ slower) | significant ± | draft fields (:269-272, :555-556), `by_spec_n_max` | no | yes — 01 §3.5, 02 signal #6 |
| 6 | Power limit/clocks/thermal throttle | significant | `power_w/power_limit_w/clock_mhz/throttled/throttle_mask` (:435-448), witness `cpu_max_khz/temp_c` (:771-773) | no | yes — 01 §4 mistake #5 |
| 7 | Cold mmap (first run page-faults from NVMe) | dominant-when-cold, else negligible | `maj_faults_per_token` (:537), `load_mode` (:261), cache label (:724) | no (only via caveats) | yes — 01 §3.2, rank 1 demand |
| 8 | Engine version/build, driver/CUDA | minor-significant (few %) | `build/commit` (:235-236), GPU `driver` (:422) | `engine_version` | sometimes — 02 §5.1 |
| 9 | `-b/-ub` single-stream decode | minor (VRAM crowding aside) | `b/ub` (:257-258) | no | yes — misattributed; 01 §3.1 |
| 10 | `-c` reservation size, short prompt | negligible-when-short on speed | `ctx_size` (:242) | no | yes — but as a *misreading* (see §6) |
| 11 | KV quant on full-GPU decode | minor (helps via occupancy, not bandwidth) | `ctk/ctv` (:259-260) | no | yes — 01 §3.4, 02 signal #9 |
| 12 | Flash attention on decode | minor (community reports slight slowdown; medium confidence) | `fa` (:256) | no | yes — 01 §3.4, 02 signal #6 |
| 13 | Thread count (full-GPU) | minor | `t` (:264) | no | rarely |
| 14 | Sampling path/reasoning (10% apart measured, :799-802) | significant when thinking models | `sampling` (:808-811), `reasoning_n` (:548) | no | yes when reasoning models |

### (b) MoE / partial offload (`-ot`, `-cmoe`) decode — same ranking with these overrides

Placement split (which classes on which device, per-device active bytes) is rank 1, dominant: attention-on-GPU + experts-on-RAM vs evicted experts differ by multiples (tape: :474-494; demand: 01 §3.1, §3.3). RAM speed/channels/bandwidth move up to significant (tape: :372-387; 02 signal #7 — note Linux DMI unreadable, `ramsource` distinguishes stated/measured). `-cmoe`/`-ot` themselves are significant (tape: :262-263). KV quant becomes significant (frees VRAM for experts; 01 §3.4 janvitos case). File-vs-VRAM spill is the cliff: `file_bytes` (:295) vs VRAM + `maj_faults_per_token` (:537) tells NVMe-bound (<2 tok/s class) apart from RAM-bound. PCIe generation is minor (tape: :423). I'm confident on placement/RAM ranking; exact MoE-vs-RAM-bandwidth slopes vary by router/topology — medium confidence.

### (c) Prefill tok/s

Prompt length defines the measurement (dominant artifact): below ~100 tokens it is queue/slot/template noise, not prefill (tape constant :63; fixtures use 512). Prefix-cache hit is the dominant confounder — a 78%-hit run reads 10x "faster" (tape: :678-685, :729-732; 02 pitfall #4). Flash attention is dominant at long context (up to 2 orders of magnitude claimed; medium confidence — community-sourced via 02 signal #6). `-b/-ub` significant; concurrency significant (shared compute); offload placement significant; CPU threads significant on CPU-side prefill; quant minor; `-c` negligible-when-short except via VRAM crowding.

## 5. Recommendations (all additive, schema 1)

Minimum index fields, ordered by value:
1. `prompt_n` (per-stream mean) + `predicted_n` (mean) + `min_predicted_n` — workload identity; without these no two tok/s are comparable.
2. `cache_hit_ratio` — the single biggest prefill/TTFT confounder.
3. `ctx_size` — reservation context; always paired with (1) (see misread warning).
4. `fa`, `ctk`, `ctv` — one-char/short strings; settle the two most-argued flags.
5. `batch`, `ubatch` — short strings; prefill + buffer-crowding story.
6. `ngl` + `offload_note` (e.g. "46/48", "partial", "full") — spill verdict in one token.
7. `draft_model` + `draft_accept_rate` — pooled accepted/drafted.
8. `throttled` (bool) + `cold` (bool, from majflt threshold) — trust in one bit each.
9. `power_w`/`power_limit_w` and `clock_mhz` — run-page detail; index only the two one-bit verdicts above.

Row chips (max 3 new): `P512·G128·cache0%` (workload), `ctx32k` (reservation, grey/de-emphasised next to workload), `fa·KVq8` (config). Run-page rows: full flags line verbatim; placement table (device × class bytes + layers string); workload line (prompt Set id, per-stream P/G, min-G, short-stream count); machine line (power of limit, clock, temp, throttle, contention reasons); draft acceptance. Search filters worth having: `model_id` dropdown (exact), param-size bands (with MoE caveat below), `quant_id`, `engine`, `gpu` membership (exists), `prompt_set` (comparability gate), MoE toggle, min `predicted_n` (rate-validity). Misleading as filters: `ctx_size` minimum (reservation ≠ measured length — filter on `prompt_n` instead; show ctx as context, not a filter), `sessions`-blind decode sort (aggregate vs per-stream must be labelled; TTP-128 already notes this), raw `params` band across MoE/dose boundaries.

Per-stream vs totals: above one stream the tape's `Timings` fields and the index rate split — index `decode_per_sec` is the **total** (aggregate) while `prompt_n/predicted_n` should be indexed as **per-stream means** with `min_predicted_n` beside them, else "10+300 avg 155" clears validity while one stream is a sample (:596-600).

Misread pairs to flag in UI: `-c 32768` beside a 200-token prompt (reads as "32k speed"; fix: always print `ctx32k · measured P200`); aggregate tok/s beside single-stream rows in `decode` sort (label `×N total`); prefill with cache hit >0 (badge `cached`); `Q4` without sub-type (index already splits `quant_id`); short-G runs (`sample` label rides the row).

## 6. MoE size band: yes, total-params bands mislead; use active params

Decode is bandwidth over *touched* bytes, so an MoE's band should be active params when known. The tape has enough: `params` (:296) + `n_experts` (:298) + `n_experts_used` (:299) + `active_bytes_per_token` (:304) + exact `quant` (:294, bits derivable) — active params ≈ `active_bytes_per_token` / bytes-per-param, cross-checkable against `(attention + experts_used/experts × expert_params)`. Caveats: `n_experts_used` may be empty on older tapes (unknown, not 1); shared experts/router are read in full every token (the :482-494 comment — naive sparse-fraction math undercounts ~10%); dense models just use `params`. Recommend indexing `active_params` (omitted when not computable) and banding on it with a `moe` marker, keeping `params` as the total.

