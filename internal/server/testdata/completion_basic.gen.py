#!/usr/bin/env python3
# Provenance for completion_basic.sse + completion_basic.arrivals (TTP-55,
# 2026-09-14). Not run by the build or by CI; kept so the fixture can be
# regenerated deliberately rather than hand-edited.
#
# This fixture is stream_basic.sse retold in llama-server's own /completion
# shape: the SAME generation, the same server timings, the same client arrival
# times, the same forty-four token texts. That is the whole point of it.
# TestCompletionStreamReducesLikeChat replays both and asserts the records
# agree, so the claim "the recorder does not care which endpoint fed it" is
# measured against two files rather than asserted in a comment.
#
# Do not regenerate this from anything but stream_basic.sse. A fixture invented
# independently would still pass its own assertions while the two timelines
# quietly drifted apart, which is exactly the bug the test exists to catch.
#
# Chunk shape follows llama.cpp tools/server/README.md:
#   content   "In case of streaming mode, will contain the next token as a
#             string."
#   stop      "Boolean for use with stream to check whether the generation has
#             stopped"
#   stop_type "Possible values are: none, eos, limit, word"
#   timings / prompt_progress   the same objects the chat stream carries.
# There is no usage object and no [DONE] on this path: the stream ends with
# the chunk whose "stop" is true.
import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))


def read_events(name):
    """Every `data:` payload of an SSE fixture, in order."""
    out = []
    with open(os.path.join(HERE, name)) as f:
        for line in f:
            line = line.rstrip("\n")
            if line.startswith("data: "):
                out.append(line[len("data: "):])
    return out


def read_arrivals(name):
    out = []
    with open(os.path.join(HERE, name)) as f:
        for line in f:
            line = line.strip()
            if line and not line.startswith("#"):
                out.append(int(line))
    return out


chat = read_events("stream_basic.sse")
arrivals = read_arrivals("stream_basic.arrivals")
assert len(chat) == len(arrivals), (len(chat), len(arrivals))

lines = []
out_arrivals = []


def emit(at, obj):
    lines.append("data: " + json.dumps(obj, separators=(",", ":")))
    lines.append("")
    out_arrivals.append(at)


for payload, at in zip(chat, arrivals):
    if payload.strip() == "[DONE]":
        # The chat path's end marker. /completion has none: its last chunk
        # says so itself with "stop": true, which the chunk below already did.
        continue
    c = json.loads(payload)
    if c.get("usage") is not None and not c.get("choices"):
        # stream_options.include_usage is an OpenAI-compatibility field. The
        # raw path reports its cache in `timings.cache_n`, which every chunk
        # here already carries, so there is nothing to translate.
        continue

    o = {"index": 0, "content": "", "stop": False}
    if c.get("prompt_progress") is not None:
        o["prompt_progress"] = c["prompt_progress"]
    for ch in c.get("choices", []):
        delta = ch.get("delta") or {}
        if delta.get("content"):
            o["content"] = delta["content"]
        if ch.get("finish_reason"):
            # The chat vocabulary is stop / length; this endpoint's is
            # eos / limit / word / none. stream_basic ends on an end-of-
            # sequence token, so this one says eos and the Go side records
            # that word verbatim rather than renaming it.
            o["stop"] = True
            o["stop_type"] = "eos"
    if c.get("timings") is not None:
        o["timings"] = c["timings"]
    # No id_slot: stream_basic.sse carries none, and this fixture must stay a
    # faithful retelling of it so the two records compare as equals. The slot
    # id of a real /completion chunk is covered by its own test.
    emit(at, o)

with open(os.path.join(HERE, "completion_basic.sse"), "w") as f:
    f.write("\n".join(lines) + "\n")

hdr = [
    "# Arrival offset of every SSE `data:` event of completion_basic.sse, in",
    "# milliseconds since the request was sent, one per event, in order.",
    "# Blank lines and # comments are ignored.",
    "#",
    "# Taken verbatim from stream_basic.arrivals, minus the two events that",
    "# path does not have (the usage chunk and [DONE]). The two fixtures are",
    "# the same generation seen through two endpoints, so the client clock",
    "# must be identical or the reduction could not be compared.",
]
with open(os.path.join(HERE, "completion_basic.arrivals"), "w") as f:
    f.write("\n".join(hdr) + "\n" + "\n".join(str(a) for a in out_arrivals) + "\n")

print("events", len(out_arrivals), "first", out_arrivals[0], "last", out_arrivals[-1])
