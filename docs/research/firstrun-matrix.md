# The first-run scenario matrix — what a first-time user actually sees

Audit track, 2026-09-21. The question (user, same day): *can people now get a
result first try without failing, and when a run fails or records something
useless, is the cause explained in detail and kindly?*

One real server combination is verified (`internal/recorder/firsttry_test.go`,
the ik_llama.cpp 4×8192-slot shape). Everything else a first-time user can
meet was unknown. This matrix runs the CLI **in-process, as that user types
it** — default command unless the scenario names a flag — against twenty-one
fake servers shaped like llama.cpp, Ollama, LM Studio and vLLM, and records
two verdicts per row.

Reproduce: `go test ./cmd/toktape/ -run TestFirstRunMatrix -count=1 -v`
(~34 s wall — the Ollama row's default 20 s clock is a real wait now). The
rows that are already good are pinned in the test; the gaps are printed into
`knownGaps` and are **not** asserted — each becomes a failing gate when fixed.

Re-audited 2026-09-21, later the same day (the `msgs` track): every degraded
run now says what happened and what to type next — `nextStep`
(`cmd/toktape/nextstep.go`) is the one table both the failed-stream block and
the all-streams-failed door read, the terse-model and serial-decode caveats
name their levers, and a finished-but-worthless run says so before the ✓
lines and is not invited to be posted.

Re-audited again 2026-09-21 (the `empty` track, TTP-172), against a HEAD the
old table had drifted from — three commits had moved rows 5, 10 and 18
without the doc noticing. Two rows changed shape and one was added:

- **`ollama-empty` is new.** Measured on real Windows 11 + Ollama 0.33.3 with
  no model pulled: the run sends no model id (the listing carried none to
  pick), the server answers `model is required`, and the old lever at that
  door said "check its log and its slot count" — a llama.cpp concept an
  empty Ollama does not have. `nextStep` now answers the no-model 400 with
  `ollama pull` when the engine claimed Ollama, else `--model <id>` and the
  server's own model list. EXPLAINED is pinned **yes**.
- **`ollama-shape` got honest, went bad, and was then fixed.** The fake
  stopped at 80 tokens at full speed and always sent `usage`, so it always
  ended on its own and could never lose the race real Ollama loses — real
  Ollama counts tokens ONLY in a closing usage message (a stream cut by the
  clock never gets one, so a cut costs the entire measurement) and decodes
  slower at a long prompt (~110 tok/s at ~59 tokens, ~85 at ~6,000). The
  fake now does both. Against the recorder as it then stood the row flipped
  run to run — 5 of 7 cut and lost — which is what the real server was doing
  the same day, 3 of 6. TTP-170 re-priced the cap and the row is now pinned
  **usable yes**. The two measurements came from opposite ends, a fake
  written from the real server's shape and the real server itself, and they
  agreed; that agreement is what says the fake is honest rather than harsh.
- **`stream-dies-midway`'s ✗ line is plain words now** (the wrap-site
  translation landed): "the connection closed mid-answer … the server may
  have crashed or been killed". What keeps the verdict `partial` is only
  that no lever the rule counts exists for a crashed server.

## The verdict rules (mechanical, in the test)

- **USABLE**: `yes` iff exit 0 AND `StreamsFailed == 0` AND every stream
  `predicted_n >= 64` AND (the fakes report real prompt figures) every stream
  `prompt_n >= 256` — a prefix-cache hit reports 5 on these fakes, and a
  stream cut before its closing usage reports 0.
  `refused-cleanly` iff exit != 0 before any stream was sent. Else `no`.
- **EXPLAINED**, assessed only when USABLE != yes or the tape carries a
  figure-severity caveat: `yes` iff the user-facing output (stdout + stderr)
  contains BOTH (a) the cause in words a user would recognise — the server's
  own sentence or a plain statement of what was wrong — and (b) at least one
  concrete next action (a flag, a command, a server setting). A bare caveat
  code, or advice naming a lever the user's server does not have, is
  `partial`. The search is scoped to the places a user reads at a failure:
  the `toktape: …` sentence and its `→` hint, the `✗` share-block lines, and
  the card's caveat sentences.

## The table

Sight lines are verbatim from the run (stderr unless a `│` card line; ports
and token counts vary between runs — the Ollama row's verdict does too, see
gap 1). Each row keeps its three most relevant lines.

| # | scenario | exit | usable | explained | what the user literally saw |
|---|----------|------|--------|-----------|------------------------------|
| 1 | `llama-4slot-8k` (hero shape, `--sessions 4 --for 2s`) | 0 | **yes** | n/a | `· plan: 4 × 6,976-token prompts (slot-bound) · ~0 s prefill · cap 1,032 · slot 8,192` · `→ llama-server at http://127.0.0.1:PORT (b4321) · Qwen3.5-35B-A3B-UD-Q4_K_M · no /proc view` · `│ ! 6 caveats — model file not readable here, shape and placement │` |
| 2 | `llama-1slot-4k` (default command) | 0 | **yes** | n/a | `· plan: 1 × 2,880-token prompts (slot-bound) · ~0 s prefill · cap 1,054 · slot 4,096` · same header · same caveat line |
| 3 | `llama-4slot-2k` (`--sessions 4 --for 2s`) | 0 | **yes** | n/a | `· plan: 4 × 832-token prompts (slot-bound) · cap 1,024 · slot 2,048` · same header · same caveat line |
| 4 | `llama-sessions-gt-slots` (`--sessions 4`, 2 slots) | 1 | **refused-cleanly** | **yes** | `toktape: 4 sessions asked of http://127.0.0.1:PORT, which offers 2 slots; the 2 past its slots would wait in its queue, and the card cannot tell queue wait …` · `→ ask for --sessions 2, or restart llama-server with -np 4 to offer 4 slots` |
| 5 | `llama-no-slots-endpoint` (/slots 501, 4096 ctx enforced) | 0 | **yes** | n/a | `· plan: 1 × 2,880-token prompts (slot-bound) · ~0 s prefill · cap 1,054 · slot 4,096` — identical to row 2: the recorder reads n_ctx from /props when /slots refuses, so the run finishes (was exit 3, every stream lost, before the fallback landed) |
| 6 | `llama-no-tokenize` (/tokenize 404) | 0 | **yes** | n/a | `· plan: 1 × 6,976-token prompts (slot-bound) · ~0.1 s prefill · cap 1,164 · slot 8,192` · same header · same caveat line |
| 7 | `llama-thinking-default` (reasoning-only stream, `--for 2s`) | 0 | **yes** | **yes** | `· plan: 1 × 6,976-token prompts (slot-bound) · ~0.1 s prefill · cap 1,100 · slot 8,192` · `│ ! 8 caveats — answer cut: all 718 predicted tokens were reasoning — │` (the count varies run to run, ~600–900) — the caveat continues `… the default budget did not hold the thinking; try --for or --think-budget` |
| 8 | `llama-slow-box` (30 tok/s prefill, `--for 2s`) | 0 | **yes** | n/a | `· plan: 1 × 6,976-token prompts (slot-bound) · cap 1,100 · slot 8,192` (no `~X s prefill`: the probe refused its long point, so no rate exists) · same header |
| 9 | `llama-rerun-same-server` (scenario 1, twice, exact-text prefix cache) | 0 | **yes** | n/a | two `· plan: 4 × 6,976-token prompts (slot-bound) …` lines · both takes' `prompt_n` 6,900 — the run salt defeated the cache; take 2 prefilled real tokens |
| 10 | `openai-only-32k` (vLLM-shaped, generous ctx) | 0 | **yes** | n/a | `· plan: 1 × 7,937-token prompts (whole) · cap 8,000` · `→ openai at http://127.0.0.1:PORT (?) · mat-model · no /proc view` · `│ ! 5 caveats — client-timed: the server reported no timings, so every │` (usage.prompt_tokens 7,431 is on the tape now; was usable no before the recorder took the count) |
| 11 | `openai-only-4k` (vLLM 400 over 4096) | 3 | no | **yes** | `toktape: recorder: all streams failed: … POST /v1/chat/completions: 400 Bad Request: {"error":{"message":"This model…` · `→ the prompt plus the answer did not fit the context: lower --n-predict, or raise the server's context — --max-model-len on vLLM, OLLAMA_CONTEXT_LENGTH o…` |
| 12 | `vllm-4k-advertised` (max_model_len in /v1/models) | 0 | **yes** | n/a | `· plan: 1 × 2,880-token prompts (slot-bound) · cap 1,082 · ctx 4,096` — the listing states the context, so the plan trims and caps inside it and the default command finishes |
| 13 | `ollama-shape` (silent truncation to 2048, honest pacing) | 0 | **yes** | n/a | `· plan: 1 × 7,937-token prompts (clock-bound) · cap 840` · `→ openai at http://127.0.0.1:PORT (?) · mat-model · no /proc view` · `│ ! 5 caveats — client-timed: the server reported no timings, so every │` — pinned after TTP-170; before it this row flipped, 5 of 7 runs cut and lost |
| 14 | `ollama-empty` (installed, nothing pulled) | 3 | no | **yes** | `toktape: recorder: all streams failed: server: run: all 1 streams failed: server: POST /v1/chat/completions: 400 Bad Request: {"error":{"message":"model is r…` · `→ the server answered that no usable model was named: load a model on the server, or name one it carries with --model <id> (its /v1/models list is what tok…` · `→ openai at http://127.0.0.1:PORT (?) · ? · no /proc view` (the model prints `?`: the listing carried none) |
| 15 | `server-not-running` (connection refused) | 2 | **refused-cleanly** | **yes** | `toktape: recorder: cannot attach: server: unreachable: http://127.0.0.1:1: connection refused` · `→ check the host and port, or add --wait 30s to wait for a server that is still starting` |
| 16 | `server-loading` (503 Loading model, `--wait 1s`) | 2 | **refused-cleanly** | **yes** | `toktape: recorder: cannot attach: gave up after 1s: server: loading the model: http://127.0.0.1:PORT: Loading model` · `→ the server is there and was not ready in time; give it longer with --wait 30m` |
| 17 | `wrong-port-http-200-html` | 2 | **refused-cleanly** | **yes** | `toktape: recorder: cannot attach: server: unreachable: http://127.0.0.1:PORT: /props is not JSON: invalid character '<' looking for beginning of value` · `→ check the host and port, or add --wait 30s …` |
| 18 | `auth-required` (401 everywhere) | 2 | **refused-cleanly** | partial | `toktape: recorder: cannot attach: server: unauthorized: server: unreachable: the server at http://127.0.0.1:PORT wants credentials (HTTP 401): {"error":{"me…` · `→ the server refused the request as unauthenticated: it wants a key, and toktape has no flag for one yet` — the 401 is classified and the sentence is honest; the lever is an absence (gap 5) |
| 19 | `rate-limited` (429 on 2 of 4 chat streams) | 0 | no | **yes** | `✗ 2 of 4 streams failed: server: POST /v1/chat/completions: 429 Too Many Requests: {"error":{"message":"This server is rate limited; try again later","type…` · `→ the server refused the load: fewer --sessions` · `· plan: 4 × 6,976-token prompts (slot-bound) …` |
| 20 | `stream-dies-midway` (cut after 50 tokens) | 0 | no | partial | `✗ 1 of 4 streams failed: server: the connection closed mid-answer after 50 tokens — the server may have crashed or been killed (out of memory is the usua…` · `→ the server may have crashed or been killed; its log says which, and out of memory is the usual cause` · `│ ! 8 caveats — 1 of 4 streams failed: the aggregate is over the ones that finished │` |
| 21 | `model-answers-instantly` (stop after 8 tokens) | 0 | no | **yes** | `✗ this run is not a usable measurement: short generation: 8 tokens is a sample, not a decode rate (under 32) — a longer answer needs a prompt that asks f…` · `→ a longer answer needs a prompt that asks for one: --prompt, or a model that is not terse` · `│ ! 8 caveats — short generation: 8 tokens is a sample, not a decode │` |

**Summary after TTP-170 and TTP-172, both on 2026-09-21: 4 rows in
`knownGaps`** (5 before TTP-170 landed, 7 at the `msgs` pass, 8 at the first
audit). Of 21 rows, **11 are usable-yes, 5 refuse cleanly and are explained,
and 5 are usable-no** — two of those five pinned as honestly-not-a-measurement
(`ollama-empty`, `model-answers-instantly`), the other three in the gap list.

The four remaining gaps are all owned elsewhere, and none is a launch
blocker: the small-context pre-flight cap is the recorder track's, the exit-0
contract on dead streams is the lead's call, and two are levers that do not
exist yet (a crashed server has no flag; an API key has no flag, which the
run says rather than inventing one).

**What moved this day, and the order matters.** The `msgs` pass made every
degraded run name a lever that fits its engine. TTP-172 then made the
`ollama-shape` fake fail the way the real server fails, and the row went from
green to flipping — the honest verdict, and the one that showed the matrix
had been scoring the most common engine as fine while it failed three real
runs in six. TTP-170 re-priced the cap against that, and the row is now
pinned. Rows 5, 10 and 12 turned usable-yes earlier under work from other
tracks, which is what this re-run caught up with; `ollama-empty` is new.

## The gaps, ranked by how likely a r/LocalLLaMA reader hits them

Status per gap, after the 2026-09-21 `empty` pass: **closed here** (the row's
EXPLAINED verdict is pinned in the test), **half closed** (this track's side
is done; the named owner holds the rest), or **open** with its owner.

### 1. The default Ollama run was a coin flip between the cap and the clock (row 13)

**Closed 2026-09-21 (TTP-170).** Found by making the fake fail the way the
real server fails (TTP-172): before that, the fake stopped at 80 tokens at
full speed and always sent `usage`, so this row read usable-yes while real
Ollama was failing three runs out of six. **The instrument was the defect
for as long as the code was**, and it is the reason this gap is written up
after it is closed rather than deleted.

**Likelihood, while it was open: highest — every Ollama user, on the default
command, every run.** Ollama is the engine a first-time reader most likely
has.

The shape, both halves measured on real Ollama (2026-09-21): tokens are
counted ONLY in a closing `usage` message, which a stream cut by the clock
never sends — so a cut costs the entire measurement, not part of it — and
decode runs slower at a long prompt than at a short one (~110 tok/s at a
~59-token prompt, ~85 at a ~6,000-token one; the KV cache the prompt leaves
behind is paid on every token).

The race: the calibrated cap (`internal/recorder/probe.go`,
`applyCalibratedCap`) was `0.8 × the calibration's short-prompt rate × the
clock`, and the 0.8 was set at the CENTRE of the measured long-context
slowdown — a factor at a centre loses half the time. Six real runs on one
Mac all needed 20.3–24.4 s of a 20 s clock; the three that produced a card
were the three where the model happened to stop on its own first. Here the
row flipped 5 of 7. Two further faults compounded it: no prefill was
reserved when the prefill calibration had no point, and that point was
silently dropped exactly on a cold model, because the span subtracted a
decode calibration's TTFT that had paid the model's load and went negative.

The fix was three layers, not a constant:

- **sizing** — the derate is now `calibrationDecodeShare` = 0.65, below the
  measured floor of 0.729 rather than at the centre of 0.729–0.808;
  `planPrefillShare` of the clock is reserved when no prefill point exists;
  the span is clamped so a cold calibration cannot destroy the point;
- **structural** — the clock's cut is held back, bounded at half the budget
  again, on `ServerOpenAI` runs only: where the floor already holds a cut
  back to keep a measurement VALID, the grace holds it to keep the
  measurement AT ALL. On every other kind the behaviour is unchanged, which
  is asserted;
- **debuggability** — the run now says what the cap predicted, as a band
  (`cap 1,158: 112 tok/s calibrated, 4.1 s prefill reserved, so the run
  finishes in 14.5-20.0 s of the 20 s clock`). A single figure would have
  printed the clock every time: the cap is chosen as share × rate × budget,
  so on one stream the quotient is the budget back again.

Confirmed from both ends: this row pinned usable-yes over five consecutive
runs, and the real cold Mac run that produced `Sample ?` before now prints
`Decode 87.2 tok/s`.

Still open beside it, owner: recorder track — the silent-truncation hole
this row carries. `usage.prompt_tokens` is 2048 against the ~7.4k tokens the
tape says it sent, and nothing compares the two, so a truncating server is
not contradicted by anything on the run.

### 2. A small-context vLLM refuses every stream up front (row 11)

**Half closed.** The exit-3 door reads the vLLM 400's own words and answers
with the context lever naming all three spellings (`--max-model-len` first);
row 11's EXPLAINED verdict is pinned **yes**. With a summary that names the
engine (`--engine "vLLM 0.11"`), the failed-stream block's lever is the
engine's own flag alone. Still open on the recorder side: the OpenAI path
never lowers the 8000-token runaway guard on a server that advertises no
`max_model_len` (no slots to read), so a small-context vLLM loses every
stream before a tape exists (owner: recorder track). A server that does
advertise it — row 12 — finishes.

**Likelihood: medium-low — vLLM defaults to large contexts, but
`--max-model-len 4096` configs exist (VRAM-constrained, long-context
quantisations).**

### 3. Exit 0 with dead streams (row 19, residual)

**Open — the lead's call** (exit codes are out of this track's scope by
spec). The explanation is pinned good: the server's 429 words plus "fewer
--sessions" is exactly the contract, and the ✗ block holds the "Post it"
line back, so a human is told. What is not told is the process exit: a
wrapper script (or a coding agent) sees 0 and cannot distinguish this run
from a clean one. The smallest change is one sentence of documentation
(`toktape help agents`: branch on `streams_failed > 0`, not the exit code),
or a distinct exit code for "answered but degraded".

**Likelihood: medium — a loaded box rate-limiting a 4-stream run.**

### 4. A stream cut mid-answer has no lever the rule counts (row 20)

**Half closed.** The ✗ line now says what happened in plain words at the
wrap site (`internal/server/stream.go` midStreamReadError: "the connection
closed mid-answer after N tokens — the server may have crashed or been
killed (out of memory is the usual cause); its log says which") and the `→`
lever under it repeats the cause. The verdict stays `partial` honestly:
there is no toktape flag or command for a crashed server, so the mechanical
rule's "concrete next action" cannot exist until one is invented — whether
one ever should is the lead's call, not the recorder's.

**Likelihood: medium — an OOM-killed llama-server mid-generation is the
classic big-model low-RAM story on r/LocalLLaMA.**

### 5. 401 is explained honestly, but the lever is an absence (row 18)

**Half closed.** The 401 is classified now (`server: unauthorized` reaches
the user with the server's own body quoted) and `nextStep`'s sentence is
honest about the flag: "the server refused the request as unauthenticated:
it wants a key, and toktape has no flag for one yet" — grep over
`internal/server` and `cmd/toktape` finds no `Authorization`/`Bearer`/
`api-key` flag to name. The open half is the flag itself (`--api-key`, or
`--header`), and it is a feature decision, not a sentence.

**Likelihood: low — `--api-key` llama-servers and auth-fronted proxies exist
but are rarely a first run.**

### Closed since the first audit, kept for the record

- **Every OpenAI-compatible run dropped the server's own prompt count**
  (was gap 1, rows 10 and 12): closed by the recorder track —
  `usage.prompt_tokens` is recorded (`PredictNSource "usage"`), the plan
  prices real lengths (`plan: 1 × 7,937-token prompts`), and row 10 is
  usable-yes and pinned.
- **A `--no-slots` llama-server lost every stream** (was gap 3, row 5):
  closed by the recorder track — the plan reads `n_ctx` from `/props` when
  `/slots` refuses, and the row is usable-yes and pinned.
- **A terse model's "sample, not a rate" named no lever** (was gap 2, row
  21): closed at the `msgs` pass — the caveat ends "— a longer answer needs
  a prompt that asks for one (--prompt), or a model that is not terse", the
  closing block says the run is not a usable measurement, and the Markdown
  line is held back. Pinned.

## Every user-facing sentence the record path can print

`usagef` / `failf` / `fail(failure{` under `cmd/toktape`, `r.warn(` under
`internal/recorder` — the record path only (card/ls/log/compare/publish have
their own). **no next action** marks sentences that state a fact or a
rejection without naming a flag, command or server setting.

The one door out (`cmd/toktape/fail.go`) guarantees every failure carries
code + sentence + hint, so the unmarked ones below are the ones whose hint
is empty or generic.

| file:line | sentence (shape) | next action? |
|---|---|---|
| `cmd/toktape/record.go` | `unexpected argument %q` | **no next action** (the argument is named; nothing says what to do with it) |
| `cmd/toktape/record.go` | `--for %s: a budget cannot be negative` | yes — `--for 30s …; --for 0 turns the clock off` |
| `cmd/toktape/record.go` | `--prompts and --prompt are alternatives, not a pair` | implied only — **no explicit lever** |
| `cmd/toktape/record.go` | `--prompts needs a file` | yes — the JSONL example |
| `cmd/toktape/record.go` | `--prompts %s: %v` (read error) | yes — the JSONL example |
| `cmd/toktape/record.go` | `--spec-n-max %s: %v` | names the flag |
| `cmd/toktape/record.go` | sampling refusals (`--endpoint`, `--no-think`, `--think-budget`, `--param`) | yes — each names the flag or the alternative |
| `cmd/toktape/record.go` | `--engine-kind`, `--engine`, `--endpoint completion` refusals | yes — each names the fix |
| `cmd/toktape/record.go` | `--ram-*` refusals | yes — names the flag |
| `cmd/toktape/record.go` | `--grid` parse error | names the flag |
| `cmd/toktape/record.go` | `--sessions` / `--max-sessions` ceiling refusals | yes — every hint names `--sessions`/`--max-sessions` |
| `cmd/toktape/record.go` | `saving the run file: %v` / render errors | **no next action** (disk/output errors, bare) |
| `cmd/toktape/fail.go` | `recorder: cannot attach: …` + its hints (discovery / loading-busy / urlGiven / unauthorized) | yes — ports to probe + `--url`; `--wait 30m`; `--wait 30s`; the no-flag-yet auth sentence |
| `cmd/toktape/fail.go` | `ErrMoreSessionsThanSlots` (`4 sessions asked of …, which offers 2 slots; …`) | yes — `--sessions 2` or `restart llama-server with -np 4` |
| `cmd/toktape/fail.go` | `recorder: all streams failed: …` | yes — the door reads `nextStep` (same table as the failed-stream block): the context lever for the engine (all three spellings at this door — no tape exists, so no summary names it), `ollama pull`/`--model` on the no-model 400 (TTP-172), fewer `--sessions` on a 429, the no-flag-yet auth sentence, the crash sentence on a mid-answer cut. "Check its log and its slot count" remains only as the fallback for an error the table does not know |
| `cmd/toktape/fail.go` | default door `toktape: %v` | **no next action** (catch-all) |
| `internal/recorder/slotctx.go` | `ErrPromptOverflowsSlot` ("the prompt is ~N tokens … send a shorter prompt, or raise the slot's context (llama-server's -c, which -np divides)") | yes — both levers |
| `internal/recorder/slotctx.go` | `n-predict %d capped to %d …` | states the numbers, names no lever |
| `internal/recorder/record.go` | model/shape/pid//proc/GPU warnings and provenance notes | **no next action** (facts and expected-on-remote notes) |
| `internal/recorder/rounds.go` | `the run's budget ran out after round %d of %d` | **no next action** — `--for` is the lever and is not named |
| `internal/recorder/rounds.go` | `run cancelled after round %d of %d` | **no next action** (fact) |
| `internal/recorder/sweep.go` | `no draft model: --spec-n-max %s ran only n_max %d` | names the flag (self-explaining) |

The share block carries the partial-failure machinery with its own levers
(`cmd/toktape/record.go`): the ✗ count with the server's own words (deduped,
×N, clipped at 160 runes) plus `failureLever` — which is `nextStep`'s table
(`cmd/toktape/nextstep.go`), the same one the exit-3 door reads: the context
lever for the engine the summary names, `ollama pull`/`--model` on the
no-model 400, fewer `--sessions` on a rate limit, the crash sentence on a
mid-answer cut, and no invented advice otherwise. Since the `msgs` pass it
also carries the unusable-run block: a run that lost no stream but has no
rate (`tokens_uncounted`) or no stream long enough for one
(`short_generation`) opens with `✗ this run is not a usable measurement:
<sentence>` plus one `→` lever, and the Markdown line is held back exactly
as a failed-stream run's is.

## Notes on method

- The fakes' tokenizer is byte-compatible with `firsttry_test.go`'s (prose
  4.0 bytes/token for the first 6 kB, then 2.2), so the default set's prompts
  count ~7.4k tokens and the plan's arithmetic is exercised at real sizes.
- Decode timings on the llama fakes are measured, not planted (a planted
  constant disagrees with the client clock through flush overhead, and the
  card's 2 % identity checks flag the run as ragged — an artifact, not a
  finding).
- The Ollama fake paces decode at the measured long-prompt curve
  (`matOllamaTokPerSec`: 110 tok/s at 59 prompt tokens falling to 85 at
  6,000) and sends `usage` only on a stream that ended on the cap or EOS —
  which is why row 13 is the matrix's one real 20 s wait, and why its
  verdict flips inside the cap/clock margin instead of being planted.
- `server-loading` uses `--wait 1s` to stand in for the default 10-minute
  patience; the sentence, the give-up shape and the hint are the same.
- `llama-thinking-default`'s token count varies run to run (the clock cuts
  the stream); only `≥ 64` and the caveat's presence are pinned.
