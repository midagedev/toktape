#!/usr/bin/env python3
# Provenance for stream_basic.sse and stream_basic.arrivals. Not run by the
# build or by CI; kept so the fixture can be regenerated deliberately rather
# than hand-edited.
#
# The numbers are load-bearing. Each of the four reducer traps in
# reduce_test.go only bites because of a specific property of this timeline:
# the role chunk arriving before the first content token, the finish chunk
# arriving 54 ms after the last one, and the client clock differing from the
# server clock by a constant offset plus jitter. Changing the timeline without
# re-reading the diffs this script prints can leave a trap test green while
# the trap is no longer exercised.
"""Generate testdata/stream_basic.sse + .arrivals for internal/server.

Shapes follow llama.cpp tools/server/README.md (OpenAI-compatible chunks with
timings_per_token, return_progress and stream_options.include_usage).

Server decode clock and client arrival clock are deliberately INDEPENDENT:
arrival = NET + server_cumulative_ms + jitter. So the client-side rate is not
derived from the server figures, and "client agrees with server" is a real
assertion rather than an identity.
"""
import json, os

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)))
os.makedirs(OUT, exist_ok=True)

CREATED = 1757740800
ID = "chatcmpl-7Qw2ZbKcT4mVaXpLdRn3Fs"
MODEL = "gpt-oss-20b-UD-Q4_K_M"
FP = "b4321-abcdef12"

PROMPT_N = 38
PROMPT_MS = 168.0
PROMPT_PS = PROMPT_N / PROMPT_MS * 1000.0

NET = 171.0  # constant client-side offset: send -> server decode epoch

TOKENS = [" Memory", "-mapped", " files", " let", " the", " kernel", " serve",
          " model", " weights", " straight", " from", " the", " page", " cache",
          ",", " so", " the", " process", " never", " copies", " them", " into",
          " its", " own", " heap", ".", " That", " is", " why", " RSS", " under",
          "states", " what", " is", " actually", " resident", ":", " it",
          " counts", " only", " the", " pages", " already", " touched"]
assert len(TOKENS) == 44, len(TOKENS)

# Server-side inter-token gaps (ms). 43 gaps; one stall at index 20 (expert
# page-in) so the ITL p95/p99 are not degenerate. Last gap fixed up so the
# cumulative decode time lands exactly on 968.0 ms.
GAPS = [21, 23, 22, 20, 24, 22, 21, 23, 22, 22,
        19, 25, 22, 21, 23, 22, 22, 20, 24, 22,
        48, 21, 23, 22, 20, 19, 22, 21, 22, 23,
        22, 22, 21, 23, 22, 20, 24, 22, 22, 21,
        23, 22, 0]
TARGET = 968.0
GAPS[-1] = 22
# Balance the cumulative decode time onto TARGET by shaving whole milliseconds
# off the earliest gaps, so every gap stays a realistic integer.
deficit = int(TARGET) - (22 + sum(GAPS))
assert deficit < 0 and -deficit <= 30, deficit
for i in range(-deficit):
    GAPS[i] -= 1
s = [22.0]
for g in GAPS:
    s.append(s[-1] + g)
assert s[-1] == TARGET, s[-1]
assert len(s) == 44

PREDICTED_N = 44
PREDICTED_MS = TARGET
PREDICTED_PS = PREDICTED_N / PREDICTED_MS * 1000.0

# Client jitter (ms), cyclic; endpoints pinned so the client window is 3 ms
# shorter than the server's -> a small, non-zero disagreement well inside
# tape.RateTolerance.
JIT = [1, 0, -1, 1, 2, 0, -1, 0, 1, -1, 2, -2, 1, 0]
jit = [JIT[i % len(JIT)] for i in range(44)]
jit[0] = 1
jit[43] = -2
arr = [round(NET + s[i] + jit[i]) for i in range(44)]
for i in range(1, 44):
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


def timings(pn, pms, pps, cn=0):
    return {"prompt_n": PROMPT_N, "prompt_ms": PROMPT_MS,
            "prompt_per_second": PROMPT_PS, "predicted_n": pn,
            "predicted_ms": pms, "predicted_per_second": pps, "cache_n": cn}


# 1. prefill progress chunks (return_progress). No choices at all.
for at, processed, tms in ((42, 10, 44.0), (88, 20, 90.0), (134, 30, 135.0),
                           (166, 38, 168.0)):
    o = base()
    o["choices"] = []
    o["prompt_progress"] = {"total": PROMPT_N, "cache": 0,
                            "processed": processed, "time_ms": tms}
    emit(at, o)

# 2. role-only chunk: no text, must never be counted as a token.
o = base()
o["choices"] = [{"index": 0, "delta": {"role": "assistant", "content": ""},
                 "finish_reason": None}]
emit(185, o)

# 3. two empty content deltas.
for at in (187, 189):
    o = base()
    o["choices"] = [{"index": 0, "delta": {"content": ""}, "finish_reason": None}]
    emit(at, o)

# 4. the content tokens.
for i, tok in enumerate(TOKENS):
    o = base()
    o["choices"] = [{"index": 0, "delta": {"content": tok}, "finish_reason": None}]
    o["timings"] = timings(i + 1, s[i], (i + 1) / s[i] * 1000.0)
    emit(arr[i], o)

# 5. finish chunk. Arrives 54 ms after the last token: the server stamps the
#    final timings and flushes. Counting it as a token is trap (b) and the gap
#    is what makes that trap measurable -- do not "tidy" it away.
o = base()
o["choices"] = [{"index": 0, "delta": {}, "finish_reason": "stop"}]
o["timings"] = timings(PREDICTED_N, PREDICTED_MS, PREDICTED_PS)
emit(arr[43] + 54, o)

# 6. usage-only chunk (stream_options.include_usage): empty choices.
o = base()
o["choices"] = []
o["usage"] = {"completion_tokens": PREDICTED_N, "prompt_tokens": PROMPT_N,
              "total_tokens": PROMPT_N + PREDICTED_N,
              "prompt_tokens_details": {"cached_tokens": 0}}
o["timings"] = timings(PREDICTED_N, PREDICTED_MS, PREDICTED_PS)
emit(arr[43] + 55, o)

lines.append("data: [DONE]")
lines.append("")
arrivals.append(arr[43] + 56)

with open(os.path.join(OUT, "stream_basic.sse"), "w") as f:
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
    "# Landmarks: role chunk 185, empty deltas 187/189, first content token %d," % arr[0],
    "# last content token %d, finish chunk %d (54 ms later, on purpose)." % (arr[43], arr[43] + 54),
]
with open(os.path.join(OUT, "stream_basic.arrivals"), "w") as f:
    f.write("\n".join(hdr) + "\n" + "\n".join(str(a) for a in arrivals) + "\n")

cw = (arr[43] - arr[0]) / 1000.0
print("events", len(arrivals), "ttft", arr[0], "last", arr[43])
print("server pps %.6f" % PREDICTED_PS)
print("client (n-1)/w %.6f  diff %.4f%%" % (43 / cw, abs(43 / cw - PREDICTED_PS) / PREDICTED_PS * 100))
print("trap a n/w      %.6f  diff %.4f%%" % (44 / cw, abs(44 / cw - PREDICTED_PS) / PREDICTED_PS * 100))
w_role = (arr[43] - 185) / 1000.0
print("trap role       %.6f  diff %.4f%%" % (46 / w_role, abs(46 / w_role - PREDICTED_PS) / PREDICTED_PS * 100))
w_fin = (arr[43] + 54 - arr[0]) / 1000.0
print("trap finish     %.6f  diff %.4f%%" % (44 / w_fin, abs(44 / w_fin - PREDICTED_PS) / PREDICTED_PS * 100))
w_now = (arr[43] + 856 - arr[0]) / 1000.0
print("trap wall       %.6f  diff %.4f%%" % (43 / w_now, abs(43 / w_now - PREDICTED_PS) / PREDICTED_PS * 100))
print("gaps client:", sorted(set(arr[i] - arr[i - 1] for i in range(1, 44))))
