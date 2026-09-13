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

| model | size | params | backend | ngl | fa | test | t/s |
| --- | ---: | ---: | --- | ---: | --- | --- | ---: |
| llama Q4_K_M | 42.52 GiB | 70.55 B | ? | 99 | on | pp384 | 610.00 |
| llama Q4_K_M | 42.52 GiB | 70.55 B | ? | 99 | on | tg307 | 9.10 |

<details><summary>Reproduce</summary>

Server (as seen from /proc/48213/cmdline):

    /usr/local/bin/llama-server -m /models/DeepSeek-R1-Distill-Llama-70B-Q4_K_M.gguf -c 16384 --parallel 8 -ngl 99 -fa on -b 2048 -ub 512 -ctk q8_0 -ctv q8_0 -t 16

Recorded with:

    toktape --url http://127.0.0.1:8080 -n 8 --n-predict 307

Tape: `20260913-150210-r1-distill-llama-70b.tape` (attach it and anyone can `toktape play` it)
</details>
