#!/usr/bin/env python3
# Provenance for stream_reasoning_only.sse and .arrivals. Not run by the build
# or by CI; kept so the fixture can be regenerated deliberately rather than
# hand-edited.
#
# This fixture is the real defect of 2026-09-13 (llama-server b40,
# DeepSeek-V4.1-Flash, --jinja): every one of the 96 predicted tokens arrived
# as delta.reasoning_content and none as delta.content. Before TTP-20 that
# produced a tape with tokens=null, ttft_ms=0 and client_agrees_with_server
# false while the server reported predicted_n=96 -- so the numbers here are
# load-bearing: 96 is above tape.MinDecodeTokens (the run must label "decode",
# not "sample") and there is no content delta anywhere, so a reducer that
# still keys off delta.content produces an empty record rather than a wrong
# one.
"""Generate testdata/stream_reasoning_only.sse + .arrivals for internal/server.

Shapes follow llama.cpp tools/server/README.md (OpenAI-compatible chunks with
timings_per_token, return_progress and stream_options.include_usage), with
reasoning_content in place of content as a --jinja thinking model emits it.

Server decode clock and client arrival clock are deliberately INDEPENDENT:
arrival = NET + server_cumulative_ms + jitter. So the client-side rate is not
derived from the server figures and "client agrees with server" is a real
assertion rather than an identity.
"""
import json, os

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)))

CREATED = 1757741200
ID = "chatcmpl-9RtK4mBpXcQ2vNhZfWy6Ad"
MODEL = "DeepSeek-V4.1-Flash-Q4_K_M"
FP = "b40-0f1e2d3c"

PROMPT_N = 43
PROMPT_MS = 196.0
PROMPT_PS = PROMPT_N / PROMPT_MS * 1000.0
CACHE_N = 0  # cold prompt: the run that found the bug was the first request

NET = 199.0  # constant client-side offset: send -> server decode epoch
# (> PROMPT_MS: the client sees the whole prefill before the first token, and
#  the four prompt_progress rows below land before the role chunk.)

N = 96  # every predicted token is a reasoning token

# One long thinking monologue, chopped into 96 deltas. The text matters only
# in that Prompt.Reasoning must come back as the exact concatenation and
# Prompt.Completion must stay empty.
WORDS = ("Okay, the user is asking how many bytes a word is. I should be "
         "careful here: the answer depends on the architecture. On x86-64 a "
         "machine word is eight bytes, but the term word in the Intel manuals "
         "still means two bytes for historical reasons, and on a 32-bit ARM "
         "target it is four. I will give the short answer first and then note "
         "the ambiguity, because the user asked for a short answer and a wall "
         "of caveats would not serve them well here. So: eight bytes on "
         "x86-64, and I will say why briefly.").split(" ")
TOKENS = [(" " if i else "") + w for i, w in enumerate(WORDS)]
assert len(TOKENS) == N, len(TOKENS)

# Server-side inter-token gaps (ms). 95 gaps at a steady 25 ms with one 61 ms
# stall at index 40 so the ITL p95/p99 are not degenerate; the last gap is
# shaved so the cumulative decode time lands exactly on TARGET.
GAPS = [25] * 95
GAPS[40] = 61
TARGET = 2436.0
FIRST = 25.0
GAPS[-1] -= (FIRST + sum(GAPS)) - TARGET
assert GAPS[-1] > 0, GAPS[-1]
s = [FIRST]
for g in GAPS:
    s.append(s[-1] + g)
assert s[-1] == TARGET, s[-1]
assert len(s) == N

PREDICTED_N = N
PREDICTED_MS = TARGET
PREDICTED_PS = PREDICTED_N / PREDICTED_MS * 1000.0

# Client jitter (ms), cyclic; endpoints pinned so the client window is 4 ms
# longer than the server's -> a small, non-zero disagreement well inside
# tape.RateTolerance (0.02).
JIT = [1, 0, -1, 1, 2, 0, -1, 0, 1, -1, 2, -2, 1, 0]
jit = [JIT[i % len(JIT)] for i in range(N)]
jit[0] = -2
jit[N - 1] = 2
arr = [round(NET + s[i] + jit[i]) for i in range(N)]
for i in range(1, N):
    assert arr[i] > arr[i - 1], (i, arr[i - 1], arr[i])

lines = []
arrivals = []


def emit(at, obj):
    lines.append("data: " + json.dumps(obj, separators=(",", ":")))
    lines.append("")
    arrivals.append(at)


def base():
    return {"id": ID, "object": "chat.completion.chunk", "created": CREATED,
            "model": MODEL, "system_fingerprint": FP}


def timings(pn, pms, pps):
    return {"prompt_n": PROMPT_N, "prompt_ms": PROMPT_MS,
            "prompt_per_second": PROMPT_PS, "predicted_n": pn,
            "predicted_ms": pms, "predicted_per_second": pps,
            "cache_n": CACHE_N}


# 1. prefill progress chunks (return_progress). No choices at all.
for at, processed, tms in ((52, 12, 55.0), (108, 24, 111.0), (162, 36, 165.0),
                           (194, 43, 196.0)):
    o = base()
    o["choices"] = []
    o["prompt_progress"] = {"total": PROMPT_N, "cache": CACHE_N,
                            "processed": processed, "time_ms": tms}
    emit(at, o)

# 2. role-only chunk: no text, must never be counted as a token.
o = base()
o["choices"] = [{"index": 0, "delta": {"role": "assistant", "content": ""},
                 "finish_reason": None}]
emit(200, o)

# 3. the reasoning tokens. There is no content delta in this stream at all:
#    the model spent its whole budget thinking and was cut off by n_predict.
for i, tok in enumerate(TOKENS):
    o = base()
    o["choices"] = [{"index": 0, "delta": {"reasoning_content": tok},
                     "finish_reason": None}]
    o["timings"] = timings(i + 1, s[i], (i + 1) / s[i] * 1000.0)
    emit(arr[i], o)

# 4. finish chunk: "length", because the thinking ran into n_predict before a
#    single answer token was emitted. Arrives 48 ms after the last token.
o = base()
o["choices"] = [{"index": 0, "delta": {}, "finish_reason": "length"}]
o["timings"] = timings(PREDICTED_N, PREDICTED_MS, PREDICTED_PS)
emit(arr[N - 1] + 48, o)

# 5. usage-only chunk (stream_options.include_usage): empty choices. The
#    server counts the reasoning tokens in completion_tokens, which is the
#    whole argument for counting them as decode tokens on this side too.
o = base()
o["choices"] = []
o["usage"] = {"completion_tokens": PREDICTED_N, "prompt_tokens": PROMPT_N,
              "total_tokens": PROMPT_N + PREDICTED_N,
              "prompt_tokens_details": {"cached_tokens": CACHE_N}}
o["timings"] = timings(PREDICTED_N, PREDICTED_MS, PREDICTED_PS)
emit(arr[N - 1] + 49, o)

lines.append("data: [DONE]")
lines.append("")
arrivals.append(arr[N - 1] + 50)

with open(os.path.join(OUT, "stream_reasoning_only.sse"), "w") as f:
    f.write("\n".join(lines) + "\n")

hdr = [
    "# Arrival offset of every SSE `data:` event, in milliseconds since the",
    "# request was sent, one per event, in order (the final [DONE] included).",
    "# Blank lines and # comments are ignored.",
    "#",
    "# These are the CLIENT clock. The `timings` objects inside the fixture are",
    "# the SERVER clock; the two differ by a constant offset plus jitter, so a",
    "# client-vs-server agreement test is not circular.",
    "#",
    "# A thinking model that never reached an answer: all %d predicted tokens" % N,
    "# are reasoning_content. TTFT is the first of them, %d, because the server" % arr[0],
    "# counts it in predicted_n exactly like an answer token (TTP-20).",
    "# Landmarks: role chunk 200, first token %d, last token %d," % (arr[0], arr[N - 1]),
    "# finish chunk %d (48 ms later, on purpose)." % (arr[N - 1] + 48),
]
with open(os.path.join(OUT, "stream_reasoning_only.arrivals"), "w") as f:
    f.write("\n".join(hdr) + "\n" + "\n".join(str(a) for a in arrivals) + "\n")

cw = (arr[N - 1] - arr[0]) / 1000.0
print("events", len(arrivals), "ttft", arr[0], "last", arr[N - 1])
print("server pps %.6f" % PREDICTED_PS)
print("client (n-1)/w %.6f  diff %.4f%%" % ((N - 1) / cw,
      abs((N - 1) / cw - PREDICTED_PS) / PREDICTED_PS * 100))
print("trap n/w       %.6f  diff %.4f%%" % (N / cw,
      abs(N / cw - PREDICTED_PS) / PREDICTED_PS * 100))
w_fin = (arr[N - 1] + 48 - arr[0]) / 1000.0
print("trap finish    %.6f  diff %.4f%%" % (N / w_fin,
      abs(N / w_fin - PREDICTED_PS) / PREDICTED_PS * 100))
print("reasoning text len", len("".join(TOKENS)))
print("gaps client:", sorted(set(arr[i] - arr[i - 1] for i in range(1, N))))
