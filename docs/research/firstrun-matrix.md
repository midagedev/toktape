# The first-run scenario matrix — what a first-time user actually sees

Audit track, 2026-09-21. The question (user, same day): *can people now get a
result first try without failing, and when a run fails or records something
useless, is the cause explained in detail and kindly?*

One real server combination is verified (`internal/recorder/firsttry_test.go`,
the ik_llama.cpp 4×8192-slot shape). Everything else a first-time user can
meet was unknown. This matrix runs the CLI **in-process, as that user types
it** — default command unless the scenario names a flag — against nineteen
fake servers shaped like llama.cpp, Ollama, LM Studio and vLLM, and records
two verdicts per row.

Reproduce: `go test ./cmd/toktape/ -run TestFirstRunMatrix -count=1 -v`
(~12 s wall). The rows that are already good are pinned in the test; the gaps
are printed into `knownGaps` and are **not** asserted — each becomes a
failing gate when fixed.

Re-audited 2026-09-21, later the same day (the `msgs` track): every
degraded run now says what happened and what to type next — `nextStep`
(`cmd/toktape/nextstep.go`) is the one table both the failed-stream block and
the all-streams-failed door read, the terse-model and serial-decode caveats
name their levers, and a finished-but-worthless run says so before the ✓
lines and is not invited to be posted. Rows 5, 11, 17 and 19's EXPLAINED
verdicts are pinned; the table and the gap list below are the new output.

## The verdict rules (mechanical, in the test)

- **USABLE**: `yes` iff exit 0 AND `StreamsFailed == 0` AND every stream
  `predicted_n >= 64` AND (the fakes report real prompt figures) every stream
  `prompt_n >= 256` — a prefix-cache hit reports 5 on these fakes.
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
and token counts vary between runs). Each row keeps its three most relevant
lines.

| # | scenario | exit | usable | explained | what the user literally saw |
|---|----------|------|--------|-----------|------------------------------|
| 1 | `llama-4slot-8k` (hero shape, `--sessions 4 --for 2s`) | 0 | **yes** | n/a | `· plan: 4 × 6,976-token prompts (slot-bound) · ~0 s prefill · cap 1,032 · slot 8,192` · `→ llama-server at http://127.0.0.1:PORT (b4321) · Qwen3.5-35B-A3B-UD-Q4_K_M · no /proc view` · `│ ! 6 caveats — model file not readable here, shape and placement │` |
| 2 | `llama-1slot-4k` (default command) | 0 | **yes** | n/a | `· plan: 1 × 2,880-token prompts (slot-bound) · ~0 s prefill · cap 1,054 · slot 4,096` · same header · same caveat line |
| 3 | `llama-4slot-2k` (`--sessions 4 --for 2s`) | 0 | **yes** | n/a | `· plan: 4 × 832-token prompts (slot-bound) · cap 1,024 · slot 2,048` · same header · same caveat line |
| 4 | `llama-sessions-gt-slots` (`--sessions 4`, 2 slots) | 1 | **refused-cleanly** | **yes** | `toktape: 4 sessions asked of http://127.0.0.1:PORT, which offers 2 slots; the 2 past its slots would wait in its queue, and the card cannot tell queue wait …` · `→ ask for --sessions 2, or restart llama-server with -np 4 to offer 4 slots` |
| 5 | `llama-no-slots-endpoint` (/slots 501, 4096 ctx enforced) | 3 | no | **yes** | `toktape: recorder: all streams failed: server: run: all 1 streams failed: server: stream error: context_length_exceeded: the request exceeds the available context size (code 500)` · `→ the prompt plus the answer did not fit the context: lower --n-predict, or raise the server's context — --max-model-len on vLLM, OLLAMA_CONTEXT_LENGTH on Ollama, -c on llama-server` · `→ llama-server at … (b4321) · …` |
| 6 | `llama-no-tokenize` (/tokenize 404) | 0 | **yes** | n/a | `· plan: 1 × 6,976-token prompts (slot-bound) · ~0.1 s prefill · cap 1,164 · slot 8,192` · same header · same caveat line |
| 7 | `llama-thinking-default` (reasoning-only stream, `--for 2s`) | 0 | **yes** | **yes** | `· plan: 1 × 6,976-token prompts (slot-bound) · ~0.1 s prefill · cap 1,100 · slot 8,192` · `│ ! 8 caveats — answer cut: all N predicted tokens were reasoning — │` (N ≈ 600–900) — the caveat continues `… the default budget did not hold the thinking; try --for or --think-budget` |
| 8 | `llama-slow-box` (30 tok/s prefill, `--for 2s`) | 0 | **yes** | n/a | `· plan: 1 × 6,976-token prompts (slot-bound) · cap 1,100 · slot 8,192` (no `~X s prefill`: the probe refused its long point, so no rate exists) · same header |
| 9 | `llama-rerun-same-server` (scenario 1, twice, exact-text prefix cache) | 0 | **yes** | n/a | two `· plan: 4 × 6,976-token prompts (slot-bound) …` lines · both takes' `prompt_n` 6,900 — the run salt defeated the cache; take 2 prefilled real tokens |
| 10 | `openai-only-32k` (vLLM-shaped, generous ctx) | 0 | no | no | `· plan: 1 × 0-token prompts (whole) · cap 8,000` · `→ openai at http://127.0.0.1:PORT (?) · mat-model · no /proc view` · `│ ! 5 caveats — client-timed: the server reported no timings, so every │` |
| 11 | `openai-only-4k` (vLLM 400 over 4096) | 3 | no | **yes** | `toktape: recorder: all streams failed: … POST /v1/chat/completions: 400 Bad Request: {"error":{"message":"This model's maximum context length is 4096 tokens. However, you requested 15421 tokens (7421 in your messages, 8000 in the completion). Please reduce the length of the messages o` (clipped at 200 chars) · `→ the prompt plus the answer did not fit the context: lower --n-predict, or raise the server's context — --max-model-len on vLLM, OLLAMA_CONTEXT_LENGTH on Ollama, -c on llama-server` |
| 12 | `ollama-shape` (silent truncation to 2048) | 0 | no | no | identical to row 10: `· plan: 1 × 0-token prompts (whole) · cap 8,000` · client-timed caveat · nothing anywhere says the prompt was truncated |
| 13 | `server-not-running` (connection refused) | 2 | **refused-cleanly** | **yes** | `toktape: recorder: cannot attach: server: unreachable: http://127.0.0.1:1: connection refused` · `→ check the host and port, or add --wait 30s to wait for a server that is still starting` |
| 14 | `server-loading` (503 Loading model, `--wait 1s`) | 2 | **refused-cleanly** | **yes** | `toktape: recorder: cannot attach: gave up after 1s: server: loading the model: http://127.0.0.1:PORT: Loading model` · `→ the server is there and was not ready in time; give it longer with --wait 30m` |
| 15 | `wrong-port-http-200-html` | 2 | **refused-cleanly** | **yes** | `toktape: recorder: cannot attach: server: unreachable: http://127.0.0.1:PORT: /props is not JSON: invalid character '<' looking for beginning of value` · `→ check the host and port, or add --wait 30s …` |
| 16 | `auth-required` (401 everywhere) | 2 | **refused-cleanly** | partial | `toktape: recorder: cannot attach: server: unreachable: http://127.0.0.1:PORT: HTTP 401: {"error":{"message":"Invalid API key"}}` · `→ check the host and port, or add --wait 30s to wait for a server that is still starting` — the lever answers a starting server, not authentication |
| 17 | `rate-limited` (429 on 2 of 4 chat streams) | 0 | no | **yes** | `✗ 2 of 4 streams failed: server: POST /v1/chat/completions: 429 Too Many Requests: {"error":{"message":"This server is rate limited; try again later","type…` (the ✗ line clips at 160 runes) · `→ the server refused the load: fewer --sessions` · `· plan: 4 × 6,976-token prompts (slot-bound) …` |
| 18 | `stream-dies-midway` (cut after 50 tokens) | 0 | no | partial | `✗ 1 of 4 streams failed: server: stream: read: unexpected EOF` · `→ the server may have crashed or been killed; its log says which, and out of memory is the usual cause` · `│ ! 8 caveats — 1 of 4 streams failed: the aggregate is over the ones that finished │` |
| 19 | `model-answers-instantly` (stop after 8 tokens) | 0 | no | **yes** | `✗ this run is not a usable measurement: short generation: 8 tokens is a sample, not a decode rate (under 32) — a longer answer needs a prompt that asks for one (--prompt), or a model that is not terse` · `→ a longer answer needs a prompt that asks for one: --prompt, or a model that is not terse` · `│ ! 7 caveats — short generation: 8 tokens is a sample, not a decode │` |

**Summary after the 2026-09-21 `msgs` pass: 7 rows in `knownGaps`** (was 8).
Every degraded llama.cpp shape now explains itself and names a lever that
fits; the terse model's run says it is not a usable measurement and is no
longer invited to be posted; the two exit-3 doors answer a context overflow
with the context lever instead of "check its slot count". The remaining gaps
are all owned elsewhere: the prompt-count and truncation holes, the
`--no-slots` and vLLM pre-flight caps and the 401 classification are the
recorder track's; the exit-0-with-dead-streams contract is the lead's call.
Before that pass: 11 of 19 rows fully good (7 usable-yes, 4 refused-cleanly
and explained), 8 gaps.

## The gaps, ranked by how likely a r/LocalLLaMA reader hits them

Status after the 2026-09-21 `msgs` pass, per gap: **closed here** (the row's
EXPLAINED verdict is pinned in the test), **half closed** (this track's side
is done; the recorder side owns the rest), or **open** with its owner named.

### 1. Every OpenAI-compatible run drops the server's own prompt count (rows 10, 12)

**Open — owner: recorder track.** (Partly moved since the audit: the usage
prompt count is now *taken* — `internal/server/sse.go:447-449` — but only for
the decode calibration's return value; the record's own `PromptN` stays 0, so
every symptom below still holds.)

**Likelihood: highest — every Ollama, LM Studio and vLLM user, on every run,
not only failures.** These are the engines a first-time reader most likely
has, and the miss is silent.

Root cause: the usage object is parsed wholesale
(`internal/server/sse.go:107` reads `prompt_tokens`), but `finish()` copies
only `completion_tokens` into the record (`internal/server/sse.go:430-431`).
So a client-timed stream's `Timings.PromptN` stays 0 forever: the Context
row, `Aggregate.TotalPromptN` and the cache verdict all lose their prompt
side, and the plan line prints nonsense — `plan: 1 × 0-token prompts (whole)`
— because `RunPlan.LongestPromptTokens` is filled only under a slot
(`internal/recorder/slotctx.go:118-121`, reached through
`internal/recorder/slotctx.go:106-108`, which returns early when slotCtx is
0, as it always is on this kind).

On the Ollama shape this is worse than missing information: the fake
truncated the ~7.4k-token prompt to 2048 and answered normally; the one
figure that would have exposed it — `usage.prompt_tokens` 2048 against the
~7.4k tokens of prompt text the tape says it sent — is exactly the figure
that is dropped. Nothing on the run contradicts the truncation.

Smallest changes (each one line-ish, none implemented):
- `sse.go` `finish()`: when `!r.sawTimings`, also take
  `rec.Timings.PromptN = r.usage.PromptTokens` (guarded `> 0`), the same way
  `PredictedN` is taken. That alone fills the Context row and the cache
  verdict's denominator.
- `plan.go` `planSet()`: fill `runPlan.LongestPromptTokens` from the counts
  it already computed, so the plan line stops printing "0-token prompts".
- Truncation detection (separate, second): warn when the server's reported
  prompt tokens are below half the priced count of the bytes actually sent —
  the recorder already has both numbers at reduce time.

### 2. A terse model's "sample, not a rate" names no lever (row 19)

**Closed here (2026-09-21).** The caveat ends "— a longer answer needs a
prompt that asks for one (--prompt), or a model that is not terse", and the
closing block says the run is not a usable measurement, names the same lever,
and holds the Markdown line back. Row 19's EXPLAINED verdict is pinned.
(`--for` is deliberately absent from the tail — the e.g. below oversold it:
the model stopped on its own finish, the clock did not cut it, so the budget
axis is not the one that ended the answer.)

**Likelihood: high — small instruct models answer the default prompt in a
handful of tokens, and those are the models first-time readers run.**

Root cause: the `short_generation` caveat sentence states the fact and stops
(`internal/card/caveat.go:748-752`). The project already solved this exact
shape for reasoning runs: `answerCutWarning`
(`internal/card/card.go:1709-1723`) ends with "try --for or --think-budget"
and was re-authored (TTP-139) to name the flag that matches what actually
ended the run.

Smallest change: give `short_generation` the same tail — e.g. `… (under 32) —
ask for more with --for 60s, or a prompt that asks for a longer answer`.

### 3. A `--no-slots` llama-server loses every stream (row 5)

**Half closed (2026-09-21).** The exit-3 door now reads the error's own words
through `nextStep` and answers a context overflow with the context lever (all
three engine spellings at that door — no tape exists, so no summary names the
engine); row 5's EXPLAINED verdict is pinned **yes**. Still open on the
recorder side: the run still loses every stream, because the plan cannot read
a context from a `--no-slots` server (owner: recorder track).

**Likelihood: medium-high — `--no-slots` is a documented llama-server flag
(memory-constrained boxes, exactly this tool's audience), and the failure is
total: exit 3, no tape.**

Root cause: `smallestSlotCtx` reads only `/slots`
(`internal/recorder/slotctx.go:83-98`); on 501 it returns 0, and
`capAnswersToSlot` treats unknown as no limit (`slotctx.go:106-108`) — while
`/props` had already reported `n_ctx` 4096 (`internal/server/client.go:430-
436`, `Props.CtxSize()` exists and is unused here). The run therefore sends
the whole ~7.4k-token prompt with the 8000-token runaway guard behind it, and
the server refuses all of it. The exit-3 door then offers no lever
(`cmd/toktape/fail.go:190-194`: "check its log and its slot count" — for a
`--no-slots` server, the slot count is exactly what the endpoint hid).

Smallest changes:
- `slotctx.go` `smallestSlotCtx()`: fall back to `props.CtxSize() /
  max(total_slots, 1)` (or bare `props.CtxSize()` when total_slots is
  unknown) when `/slots` refuses. Unknown stays unknown; a reported n_ctx is
  an observation.
- `fail.go` the `ErrAllStreamsFailed` door: when the error text carries the
  contextLength marker, print the lever `failureLever` already knows
  (`cmd/toktape/record.go:1021-1025`: "lower --n-predict, or raise the
  server's -c (which -np divides)") — today that lever exists only in the
  share block, which a total failure never reaches.

### 4. A stream cut mid-answer is reported in Go's words, with no lever (row 18)

**Half closed (2026-09-21).** `nextStep`'s transport class now answers
`unexpected EOF` / `connection reset` / `closed the connection` with "the
server may have crashed or been killed; its log says which, and out of memory
is the usual cause" — the cause in plain words, which is why the row's verdict
moved from `no` to `partial`. Still open on the recorder side: the ✗ line
still quotes the Go transport sentence verbatim, because the wrap site
(`internal/server/stream.go:284-288`) is the recorder's; and the verdict
stays `partial` honestly — there is no toktape flag for a crashed server, so
no lever the audit's rule would count (owner: recorder track, for the plain
sentence at the wrap site).

**Likelihood: medium — an OOM-killed llama-server mid-generation is the
classic big-model low-RAM story on r/LocalLLaMA.**

Root cause: the read failure is wrapped verbatim
(`internal/server/stream.go:284-288`) — "server: stream: read: unexpected
EOF" is Go transport jargon, not a sentence a user recognises; and
`failureLever`'s fallback deliberately offers nothing
(`cmd/toktape/record.go:1029`: "the server's words are above; the streams
that finished are what the card reports" — written for server error frames,
reached here by a transport error that has no words of its own).

Smallest change: translate mid-stream read failures at the wrap site into a
plain sentence with the cause kept in parentheses ("the connection to the
server was cut mid-answer (read: unexpected EOF)"), and give the transport
class a lever in `failureLever` ("re-run; if it repeats, check the server's
log — it may have crashed or been killed").

### 5. A small-context vLLM refuses everything, and the advice is llama-speak (row 11)

**Half closed (2026-09-21, same pass).** The exit-3 door reads the vLLM 400's
own words and answers with the context lever naming all three spellings
(`--max-model-len` first); row 11's EXPLAINED verdict is pinned **yes**. With
a summary that names the engine (`--engine "vLLM 0.11"`), the failed-stream
block's lever is the engine's own flag alone. Still open on the recorder
side: the OpenAI path still never lowers the 8000-token runaway guard, so a
small-context vLLM loses every stream before a tape exists (owner: recorder
track).

**Likelihood: medium-low — vLLM defaults to large contexts, but
`--max-model-len 4096` configs exist (VRAM-constrained, long-context
quantisations).**

Root cause: the OpenAI path has no slot to read, so `capAnswersToSlot`
no-ops and `DefaultMaxTokens` (8000, `internal/recorder/limit.go:94`) rides
on every request; the vLLM 400 arrives with the real numbers in it — 15421
requested = 7421 prompt + 8000 completion — and the user could fix it with
`--n-predict 512`, but no output says so. The exit-3 hint names "its slot
count", a concept a vLLM does not have (`cmd/toktape/fail.go:193`).

Smallest change: the same `ErrAllStreamsFailed` lever as gap 3 fixes this
row too ("lower --n-predict…" fits a vLLM user directly, since the error
text literally says "reduce the length of the messages or completion"). A
pre-flight cap for OpenAI-kind runs (lower the guard when the plan's priced
prompt is known large) would prevent the failure entirely, but the lever is
the one-line start.

### 6. 401 is answered with starting-server advice (row 16)

**Open — owner: recorder track.** (`nextStep` already has the 401/403/
credentials branch for stream-level refusals, and its sentence is honest
about the flag: "the server refused the request as unauthenticated: it wants
a key, and toktape has no flag for one yet" — grep over `internal/server`
and `cmd/toktape` finds no `Authorization`/`Bearer`/`api-key` flag to name.
But row 16 fails at *attach* time, through `classifyProps` mapping every
non-2xx refusal to `ErrUnreachable`, and that classification is the
recorder's to write.)

**Likelihood: low — `--api-key` llama-servers and auth-fronted proxies exist
but are rarely a first run.**

Root cause: `classifyProps` maps every non-2xx non-loading refusal to
`ErrUnreachable` (`internal/server/client.go:347-372`), and the door has a
single `urlGiven` hint for all of them (`cmd/toktape/fail.go:166-167`). The
server's own sentence does reach the user (the 401 body is quoted), so the
cause is stated; the lever is the mismatch.

Smallest change: a 401/403 branch in `classifyProps` returning a wrapped
error, plus its hint in `reportRecordError` — "the server refused
authentication; llama-server's --api-key, or the proxy in front of it, wants
a key — toktape sends none today".

### 7. Exit 0 with dead streams (row 17, residual)

**Open — the lead's call** (exit codes are out of this track's scope by
spec). The explanation is pinned good: the server's 429 words plus "fewer
--sessions" held through the share-block changes.

The explanation itself is good — the server's 429 words plus "fewer
--sessions" is exactly the contract — and the ✗ block holds the "Post it"
line back, so a human is told. What is not told is the process exit: a
wrapper script (or a coding agent) sees 0 and cannot distinguish this run
from a clean one. `Record` treats partial failure as success by design
(`internal/recorder/record.go:88-92`), and `recordPlain` exits 0
(`cmd/toktape/record.go:429-454`).

This row's USABLE verdict cannot honestly become yes — the streams really
did fail — so the smallest change is on the contract side, and it is the
lead's call: either a distinct exit code for "answered but degraded", or a
line in `toktape help agents` telling agents to branch on
`streams_failed > 0` rather than the exit code. The latter is one sentence
of documentation.

## Every user-facing sentence the record path can print

`usagef` / `failf` / `fail(failure{` under `cmd/toktape`, `r.warn(` under
`internal/recorder` — the record path only (card/ls/log/compare/publish have
their own). **no next action** marks sentences that state a fact or a
rejection without naming a flag, command or server setting.

The one door out (`cmd/toktape/fail.go:93-123`) guarantees every failure
carries code + sentence + hint, so the unmarked ones below are the ones whose
hint is empty or generic.

| file:line | sentence (shape) | next action? |
|---|---|---|
| `cmd/toktape/record.go:239` | `unexpected argument %q` | **no next action** (the argument is named; nothing says what to do with it) |
| `cmd/toktape/record.go:243` | `--for %s: a budget cannot be negative` | yes — `--for 30s …; --for 0 turns the clock off` |
| `cmd/toktape/record.go:253` | `--prompts and --prompt are alternatives, not a pair` | implied only — **no explicit lever** |
| `cmd/toktape/record.go:256` | `--prompts needs a file` | yes — the JSONL example |
| `cmd/toktape/record.go:260` | `--prompts %s: %v` (read error) | yes — the JSONL example |
| `cmd/toktape/record.go:272` | `--spec-n-max %s: %v` | names the flag |
| `cmd/toktape/record.go:286`, `:292` | sampling refusals (`--endpoint`, `--no-think`, `--think-budget`, `--param`) | yes — each names the flag or the alternative (`prompts.go` shapes, surfaced via `samplingOptions`) |
| `cmd/toktape/record.go:300-306` | `--engine-kind`, `--engine`, `--endpoint completion` refusals | yes — each names the fix |
| `cmd/toktape/record.go:316` | `--ram-*` refusals | yes — names the flag |
| `cmd/toktape/record.go:347` | `--grid` parse error | names the flag |
| `cmd/toktape/record.go:372-423` | `--sessions` / `--max-sessions` ceiling refusals (4 shapes) | yes — every hint names `--sessions`/`--max-sessions`, plus the llama-bench `-n` disambiguation at ≥32 |
| `cmd/toktape/record.go:443`, `:465` | `saving the run file: %v` / `renderRun` errors | **no next action** (disk/output errors, bare) |
| `cmd/toktape/fail.go:169` | `recorder: cannot attach: …` + three hints (discovery / loading-busy / urlGiven) | yes — ports to probe + `--url`; `--wait 30m`; `--wait 30s` |
| `cmd/toktape/fail.go:184` | `ErrMoreSessionsThanSlots` (`4 sessions asked of …, which offers 2 slots; …`) | yes — `--sessions 2` or `restart llama-server with -np 4` |
| `cmd/toktape/fail.go:190` | `recorder: all streams failed: …` | yes since 2026-09-21 — the door reads `nextStep` (same table as the failed-stream block): the context lever for the engine (all three spellings; no tape exists, so no summary names it), fewer `--sessions` on a 429, the no-flag-yet auth sentence, the crash sentence on a mid-answer cut. "Check its log and its slot count" remains only as the fallback for an error the table does not know |
| `cmd/toktape/fail.go:196` | default door `toktape: %v` | **no next action** (catch-all) |
| `internal/recorder/slotctx.go:70-74` | `ErrPromptOverflowsSlot` ("the prompt is ~N tokens … send a shorter prompt, or raise the slot's context (llama-server's -c, which -np divides)") | yes — both levers |
| `internal/recorder/slotctx.go:141` | `n-predict %d capped to %d: the slot's context is %d and the longest prompt is ~%d tokens` | states the numbers, names no lever |
| `internal/recorder/record.go:506` | `server did not report model_path, model shape unknown` | **no next action** |
| `internal/recorder/record.go:522` | `model file not readable here, shape and placement unknown` | **no next action** (expected on every remote attach) |
| `internal/recorder/record.go:589`, `:639` | `pid not found: no memory, page faults or flags` | **no next action** (expected on every non-local run) |
| `internal/recorder/record.go:621` | `server declared pid %d, which is not running here; searching for it instead` | **no next action** (progress note) |
| `internal/recorder/record.go:652-654` | `argv of pid %d unreadable[, server flags unknown]` | **no next action** |
| `internal/recorder/record.go:685` | engine commit read from the checkout (provenance note) | **no next action** (fact) |
| `internal/recorder/record.go:741` | `/proc not readable, CPU, RAM and kernel unknown` | **no next action** (expected on macOS/Windows) |
| `internal/recorder/record.go:757`, `:802` | GPU-open and placement-estimator warnings (passed through) | **no next action** |
| `internal/recorder/record.go:762` | `GPU inventory unreadable, device list empty` | **no next action** |
| `internal/recorder/record.go:838-873` | engine-placement mismatch notes (bytes vs classes; active bytes zeroed) | **no next action** (facts) |
| `internal/recorder/record.go:969` | `/apply-template failed for %d/%d streams, prompt unknown` | **no next action** — the fix for the common cause (llama-server without `--jinja` dropping `chat_template_kwargs`) is a server flag nobody names here |
| `internal/recorder/record.go:1062` | `process sampler unavailable, no memory or fault series` | **no next action** |
| `internal/recorder/record.go:1121` | `memory, faults and CPU summed over the server and its N child processes` | **no next action** (provenance) |
| `internal/recorder/reduce.go:421-425` | placement re-estimate notes | **no next action** (facts) |
| `internal/recorder/reduce.go:493` | `host load unknown, contention not judged` | **no next action** (fact) |
| `internal/recorder/shards.go:116` | `model: %d of %d shards not found` | **no next action** |
| `internal/recorder/rounds.go:269` | `the run's budget ran out after round %d of %d` | **no next action** — `--for` is the lever and is not named |
| `internal/recorder/rounds.go:272` | `run cancelled after round %d of %d` | **no next action** (fact) |
| `internal/recorder/sweep.go:121` | `no draft model: --spec-n-max %s ran only n_max %d` | names the flag (self-explaining) |

The share block carries the partial-failure machinery with its own levers
(`cmd/toktape/record.go`): the ✗ count with the server's own words (deduped,
×N, clipped at 160 runes) plus `failureLever` — which is `nextStep`'s table
(`cmd/toktape/nextstep.go`), the same one the exit-3 door reads: the context
lever for the engine the summary names, fewer `--sessions` on a rate limit,
the crash sentence on a mid-answer cut, and no invented advice otherwise.
Since 2026-09-21 it also carries the unusable-run block: a run that lost no
stream but has no rate (`tokens_uncounted`) or no stream long enough for one
(`short_generation`) opens with `✗ this run is not a usable measurement:
<sentence>` plus one `→` lever, and the Markdown line is held back exactly as
a failed-stream run's is.

## Notes on method

- The fakes' tokenizer is byte-compatible with `firsttry_test.go`'s (prose
  4.0 bytes/token for the first 6 kB, then 2.2), so the default set's prompts
  count ~7.4k tokens and the plan's arithmetic is exercised at real sizes.
- Decode timings on the fakes are measured, not planted (a planted constant
  disagrees with the client clock through flush overhead, and the card's 2 %
  identity checks flag the run as ragged — an artifact, not a finding).
- `server-loading` uses `--wait 1s` to stand in for the default 10-minute
  patience; the sentence, the give-up shape and the hint are the same.
- `llama-thinking-default`'s token count varies run to run (the clock cuts
  the stream); only `≥ 64` and the caveat's presence are pinned.
