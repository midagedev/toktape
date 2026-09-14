# Driving toktape from a coding agent

This page is for Claude Code, Codex, and anything else that will run toktape
on someone's behalf. `toktape help agents` is the reference — the exit codes,
the JSON field names, the prompts-file schema. This page is the judgement:
which invocation to reach for, and the four ways a number from this tool
stops being true.

## Before you run anything

**Recording is not a probe.** `toktape` with no verb records a run: it sends
real requests to a real server and makes the machine work for as long as the
generation takes. Do not run it to find out what it does — run `toktape
--help`, `toktape ls`, or `toktape card <tape>` on a file that already
exists.

**Do not benchmark a machine you are also using.** If you are compiling,
indexing, or running another model in the same session, the numbers describe
a busy machine and not the server. toktape takes a witness of the machine at
every round edge and the card says `contended: yes` when it saw one, but the
honest move is not to measure at all until the box is quiet.

**One run is one sample.** Serving rates move with cache state, thermal
state, and what else is resident. If you are about to report a number as
"the" speed of a model, record at least twice and say which run you are
quoting — `toktape log` lists every run recorded on that machine.

## The invocations worth knowing

```sh
toktape                                  # discover a server, record 20s, print a card
toktape --url http://host:8001 --sessions 4   # a server elsewhere, four streams at once
toktape --for 60s --tag ngl=40           # a longer run, labelled for the ledger
toktape -o json --quiet > run.json       # the summary on stdout and nothing else
toktape --wait 0                         # fail fast instead of waiting for a load
toktape card <tape> -o json              # re-read a recording; touches no server
toktape log -o jsonl                     # every run recorded here, one object per line
toktape log -o sql | sqlite3 runs.db     # the same runs as a SQLite table
```

Two flags decide whether an invocation fits inside your own timeout.
`--wait` defaults to **10m**, because a server loading a 450 GB model is the
case worth waiting for — which is longer than most harnesses allow a command
to run. Pass `--wait 0` to fail fast, or a budget like `--wait 30s`. And
`--for` decides how long the generation itself lasts; see the next section.

**`-n` is tokens, not streams.** `-n` is `--n-predict`, the token cap per
stream, which is what llama-bench's `-n` means — so `-n 128` from a
llama-bench command line asks for 128 tokens here too. The number of streams
sent at once is `--sessions N`, with no short form; `--concurrency` no longer
exists. `--sessions` stops at 8 unless `--max-sessions` names the same
number, and a run that asks for more sessions than the server has slots is
refused before a request goes out, because the streams past the slots would
measure queue wait rather than concurrency. Both refusals exit 1 with a hint
that names the flag to use.

## Asking for a run of a given length

The length of a recording is what an agent most often gets wrong, because
the obvious lever is the wrong one. `--n-predict` asks for a number of
tokens, and how long that takes depends on the machine's tok/s — which is
the thing you are recording to find out. The same 256 tokens is under two
seconds on a 7B on a fast GPU and over two minutes on a large model with no
GPU.

So ask in seconds. `--for 20s` is the default and `--for 60s` aims it
somewhere else. `--n-predict` is an answer to the same question, so naming
it turns the clock off and the token cap becomes the only limit. Name both
and whichever arrives first ends the run. The tape records which did:
`limit.cut_at` is non-zero exactly when the clock was what stopped it.

Two things to know before you rely on it:

- **The model usually stops first.** A run is often shorter than the budget
  because the answer ended. That is a fact about your prompt, not about the
  machine — a longer run needs a prompt that writes longer.
- **A very slow machine runs past the budget.** The clock will not cut a
  stream under 64 tokens, because a run under 32 is reported as a `Sample`
  and not a decode rate. When that happens `limit.cut_at` is larger than
  `limit.for`, and that is the honest outcome rather than an error.

For a clip: a rendered clip is the run replayed at 1:1 plus six seconds of
framing, twelve with `--open`, so `--for 18s` makes a thirty-second one.
This is not `render --duration`, which squeezes a run you already have into
the time you name.

## Reading the output

Branch on the **exit code**, never on the message text:

| code | name | means |
|---|---|---|
| 0 | ok | |
| 1 | usage | the invocation or its inputs were rejected |
| 2 | unreachable | no server answered, or one never finished loading |
| 3 | streams | the server answered and every stream failed |
| 4 | unavailable | this machine lacks something the output needs (ffmpeg) |

With `-o json` or `-o jsonl`, stdout carries exactly one JSON object whether the run
succeeded or failed, so there is one parse path and not two. A failure
object has an `error` key; a success object does not. `error.hint` is a
sentence saying what to do next, and it is worth surfacing to the user
verbatim — it is where "no server answered" becomes "pass `--url`".

**Read `caveats` before you quote any figure.** The card exists to qualify
numbers — `Sample` under 32 generated tokens, a prompt too short to be a
prefill measurement, a contended machine, a 0 % prefix-cache hit — and a
person reads those because they sit next to the figure. Pulling
`timings.predicted_per_second` out of the JSON skips every one of them.
`caveats` is the derived, complete list, each entry a stable code, a
sentence, and a severity. An empty array means the figures are quotable as
they stand. An entry with severity `figure` means the headline number does
not mean what it looks like.

`toktape help agents` has the full field list. The two worth knowing here:
server figures are the record and client figures are the check, so quote
`timings.predicted_per_second` and not the `client_` one; and unknown is
never guessed, so a `0` means "not measured", not "measured as zero".

## The four ways the number stops being true

1. **A short generation is not a decode rate.** Under 32 generated tokens
   the card labels the figure `Sample`. Prefill and the first tokens
   dominate and the steady-state rate is not in there. The default is a
   wall-clock budget with a 64-token floor, so you land above the line on
   any machine without choosing a number; if you set `--n-predict`
   yourself, set it high enough that a slow box still clears 32.
2. **A short prompt is not a prefill measurement.** A 30-token prompt costs
   nearly decode price per token. On one real workstation the same server
   evaluates a 4.8k-token prompt at 60 tok/s and a 30-token one at 17–53 —
   the small figure is not the engine's prefill, and a reader who takes it
   that way concludes the box is unusable. Under 100 prompt tokens the card
   says so outright.
3. **TTFT under concurrency is queue wait plus prefill.** With N streams
   arriving at once, the time to the first token includes waiting for a
   slot. The card splits the two on a concurrent run — engine prefill from
   the server's own `prompt_ms`, never from send-to-first-token. That is a
   real number for an agent workload and a wrong one for "how fast does
   this model start".
4. **A warm prefix is a different machine.** After one pass over a long
   prefix, the same prefix plus new tokens can evaluate a fraction of the
   tokens. The card prints the prefix-cache hit ratio; if it says 0 % you
   measured the cold path, and a coding agent's real workload is mostly the
   warm one.

## What toktape will not do

- It will not print a figure it did not observe. Unknown is `?`, everywhere,
  including the memory rows when there is no `/proc` view.
- It will not resolve a disagreement between the server's figures and its
  own by picking the nicer one. The tape records that they disagreed.
- It will not send anything anywhere. It talks to your server and writes
  files under `~/.toktape`.
