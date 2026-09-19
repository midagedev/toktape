{
  "schema": 1,
  "summary": {
    "id": "20260913-071500-old",
    "toktape_version": "0.1.0-test",
    "started_at": "2026-09-13T07:15:00Z",
    "finished_at": "2026-09-13T07:15:21Z",
    "server": {"kind": "llama-server", "url": "http://127.0.0.1:8080", "flags": {}},
    "model": {"path": "/models/gguf/DeepSeek-V3-0324-UD-Q4_K_XL-00001-of-00009.gguf", "file_name": "DeepSeek-V3-0324-UD-Q4_K_XL-00001-of-00009.gguf", "file_bytes": 0},
    "host": {"os": "linux", "ram_bytes": 269611614208},
    "placement": {"devices": [], "never_loaded_bytes": 0, "source": "unknown"},
    "memory": {
      "at_end": {"virt_bytes": 451201683456, "rss_bytes": 687525888, "rss_file_bytes": 650117120, "rss_anon_bytes": 37408768, "rss_shmem_bytes": 0, "swap_bytes": 0, "maj_faults": 169612, "min_faults": 483712},
      "peak_rss_bytes": 687525888,
      "mapped_file_bytes": 445245972480,
      "maj_faults_total": 22,
      "maj_faults_prompt": 3,
      "maj_faults_decode": 19,
      "maj_faults_per_token": 0.03
    },
    "concurrency": 1,
    "timings": {
      "prompt_n": 283,
      "cache_n": 0,
      "prompt_ms": 4079.0,
      "prompt_per_second": 69.3,
      "predicted_n": 32,
      "predicted_ms": 2871.0,
      "predicted_per_second": 11.1,
      "ttft_ms": 4096.0,
      "client_prompt_per_second": 69.0,
      "client_predicted_per_second": 11.1,
      "client_agrees_with_server": true,
      "decode_label": "decode"
    },
    "aggregate": {
      "streams": 1,
      "wall_ms": 6960.0,
      "total_prompt_n": 283,
      "total_predicted_n": 32,
      "aggregate_predicted_per_second": 11.1,
      "aggregate_prompt_per_second": 69.3,
      "per_stream_predicted_per_second": 11.1,
      "ttft_p50_ms": 4096.0,
      "ttft_p95_ms": 4096.0
    },
    "cache": {"hit_tokens": 0, "prompt_total": 283, "hit_ratio": 0, "label": "warm"},
    "contention": {"contended": false, "loadavg1": 1.42, "other_gpu_procs": 0},
    "template": {"rendered_has_think_close": false}
  },
  "requests": [],
  "samples": []
}
