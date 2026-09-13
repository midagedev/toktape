```text
┌──────────────────────────────────────────────────────────────────────┐
│ toktape v0.1.0                       20260913-150210-qwen3.5-35b-a3b │
├──────────────────────────────────────────────────────────────────────┤
│ MODEL    Qwen3.5-35B-A3B-UD-Q4_K_M.gguf · UD-Q4_K_M · 19.8 GiB       │
│ ENGINE   llama-server b3650 (a1b2c3d) · linux 6.8.0-45-generic       │
│          workstation                                                 │
│ RIG      2× RTX 3090 24G · AMD Ryzen 9 7950X · 64 GB DDR5-6000       │
├──────────────────────────────────────────────────────────────────────┤
│ Decode        12.1 tok/s · ≈ 16 GB/s, 1% of peak                     │
│ Prefill       1980 tok/s · TTFT 210 ms · 512 prompt tokens           │
│ Context       32768 (512 in / 128 out)                               │
│ Prefix cache  0% hit (0/512) · cold                                  │
│ Streams       8 × 12.1 tok/s = 96.8 tok/s aggregate                  │
│               TTFT p50 210 ms p95 480 ms · slots busy max 8          │
├──────────────────────────────────────────────────────────────────────┤
│ MEMORY   GPU0 [████░░░░░░] 9.6/24.0 GiB                              │
│          GPU1 [████░░░░░░] 9.2/24.0 GiB                              │
│          weights 13.5 | kv 3.0 | compute 0.9 GiB                     │
│          Host RSS 3.4 GiB (file 2.9 / anon 0.5)                      │
│          Page faults 1.4 maj/token (1420 during decode)              │
├──────────────────────────────────────────────────────────────────────┤
│ HOST     GPU0 71°C 340 W · GPU1 69°C 330 W · throttled: no           │
│          contended: no                                               │
├──────────────────────────────────────────────────────────────────────┤
│ FLAGS    -ngl 99 -fa on -b 2048 -ub 512 -ctk q8_0 -ctv q8_0          │
│          --load-mode mmap -ncmoe 12 -t 16                            │
│          -ot blk\.(3[6-9]|4[0-7])\.ffn_.*_exps=CPU                   │
├──────────────────────────────────────────────────────────────────────┤
│ ! cold run: 1.4 major faults per token during decode                 │
├──────────────────────────────────────────────────────────────────────┤
│                toktape · github.com/midagedev/toktape                │
└──────────────────────────────────────────────────────────────────────┘
```

| model | size | params | backend | ngl | fa | test | t/s |
| --- | ---: | ---: | --- | ---: | --- | --- | ---: |
| qwen3moe UD-Q4_K_M | 19.83 GiB | 35.00 B | ? | 99 | on | pp512 | 1980.00 |
| qwen3moe UD-Q4_K_M | 19.83 GiB | 35.00 B | ? | 99 | on | tg128 | 12.10 |

<details><summary>Reproduce</summary>

Server (as seen from /proc/48213/cmdline):

    /usr/local/bin/llama-server -m /models/Qwen3.5-35B-A3B-UD-Q4_K_M.gguf -c 32768 --parallel 8 -ngl 99 -fa on -b 2048 -ub 512 -ctk q8_0 -ctv q8_0 --load-mode mmap -ncmoe 12 -t 16 -ot blk\.(3[6-9]|4[0-7])\.ffn_.*_exps=CPU

Recorded with:

    toktape --url http://127.0.0.1:8080 -n 8 --n-predict 128

Tape: `20260913-150210-qwen3.5-35b-a3b.tape` (attach it and anyone can `toktape play` it)
</details>
