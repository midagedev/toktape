# How toktape measures, and how to read the card

[← README](../README.md)

## How it measures

- **Prefill is a machine rate plus a fixed cost, measured before your run.**
  Two raw `/completion` prompts — one 128 tokens, one as long as the short
  one's own observed cost allows — are fitted into a per-token rate and a
  per-request fixed cost, with nothing else in flight. A prefill figure that
  is mostly fixed cost is not a throughput, and the fit is what says which is
  which. Each run's probe prompts open with a per-run salt, so measuring the
  same warm server twice still measures.
- **The prompt set is the instrument.** The run does not send a prompt you
  typed; it sends a fixed set of 21 artifacts — code with real bugs and
  passing tests, a query plan, incident timelines, an ADR, Korean and
  Japanese prose — each 18–30 KB, longer than any one box should send whole,
  on purpose. A run sends a prefix of each, sized from the probe's fit so the
  fixed cost stays under a twentieth of the prefill it measures, and records
  how many characters it sent; the published record is verified against the
  set, prefix and all. The slot's context has the next word: the run's answer
  budget is lowered into what the slot holding that prompt can actually hold,
  and a prompt no slot can hold is refused before the first request, with
  both numbers.
- **The server's figures are the record, the client's are the check.** Every
  chunk brings the server's own rates and token counts; toktape recomputes
  them from its own clock and records whether the two agreed within 2
  percent. A disagreement is never resolved by taking the nicer number.
- **The client's rate is measured over the content window**, between the
  first and last token that carried text — not wall time. Below 32 generated
  tokens a rate is a `Sample`, not a "decode".
- **Reasoning tokens count.** `reasoning_content` deltas are decode tokens
  and TTFT is the first token of either kind.
- **cold / warm comes from the fault count**, never a guess; residency is
  derived from the process's own mappings, never recorded; "never loaded"
  is read from the GGUF tensor headers; a machine witness (load, IO, page
  cache, live `llama-*` processes, cpufreq, one temperature) is taken at
  every round edge.
- **Unknown prints as `?`.** The card never shows a value it did not observe,
  and a run without a fault measurement is never labelled cold.

## A number worth quoting

The card qualifies every figure it prints. These habits produce a card with
little to qualify — most are one flag, or none.

- **Run twice, quote the second.** The first run against a just-loaded model
  pulls its weights off disk while it decodes, and the card labels it `cold`
  from the fault count. The warm rate is the quotable one.
- **Length in seconds.** `--for 30s`. A token count is a different amount of
  time on every machine — which is the thing you are recording to find out —
  and naming `--n-predict` turns the clock off, so a run that names a cap is
  cut by it.
- **Reasoning models think on the clock.** A default budget can be spent
  before the answer starts: `--for 60s` to hold the thought, `--no-think`
  for a like-for-like with a non-reasoning model.
- **Read the caveats line before quoting anything.** A generation too short
  to be a rate, a busy machine, a clock cut — all of it is on the card before
  it is in your post, and `-o json` carries the same list with severities.
- **Compare like with like.** The prompt set id, sampling, endpoint and
  engine build are all on the card; two cards are comparable or they say why
  not.
- **As many streams as the question.** `--sessions 8` is what eight agents do
  to a server; a single stream answers a different question.
- **Leave the box alone.** A compile job moves decode by more than most of
  the changes people test; the witnesses are read at every round edge and the
  card says `contended` or `conditions_changed` when the ground moved.

## What the card shows

The same fields in the same places on every card, each one there because it
settles an argument: decode and prefill never mixed, with the queue split out
of the engine's work (`engine prefill 10732 ms · queue 22 ms`); the prefix
cache hit; the prefill fit beside the run's own figures; page faults per
token; where the model actually sits — placed against resident, weights
against KV against compute, never-loaded bytes from the tensor headers;
bandwidth named against the bus that is the wall, per verify step when a
draft model is in; the sampling actually sent; the draft's acceptance rate
and step shape; flags in full and the exact quantization; contended; and
whether N × per-stream really is the aggregate.

## FAQ

**Why not `llama-bench`?** Use both. `llama-bench` measures the engine's
compute in isolation; toktape measures the server — slots, prefix-cache
reuse, TTFT under queue pressure, what happens when eight streams arrive at
once. Those are the numbers an agent workload actually sees.

**How do I know the prompt wasn't cached?** The card says so:
`Prefix cache 0% hit (0/512)`, and `cold`/`warm` from the fault count. If
someone doubts a card, send them the tape; they render the same card.

**Does it send anything anywhere?** No. It talks to your llama-server and
writes files under `~/.toktape`. There is no telemetry and no account.

## The `.toktape` format

Gzipped JSON, one file per run, schema version 1 (plain JSON is read too). It
holds the summary, per-token timestamps and text, the sample series, the
server's raw timings, its flags and build, and the placement estimate. A
reader refuses a newer schema rather than guessing. Share the tape; anyone
with toktape renders the same card from it.
