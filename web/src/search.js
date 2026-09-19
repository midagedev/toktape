// The search (docs/toktape-spec.ko.md §9.4).
//
// **It is not a leaderboard, and the code says so in one place:** there is no
// rank column and no score. The listing does have a sort parameter — newest
// first unless asked otherwise (`oldest`, `decode`) — because filters plus an
// order are how runs get found. Turning every published run into a ranked
// entry is the one thing the user ruled out twice ("리더보드 만들고 싶은건 아냐
// 다만 사양과 모델 등으로 검색해 볼 수 있게는 하고 싶어", 2026-09-18), and the
// sort itself arrived when the user reversed the no-sort decision on
// 2026-09-19 ("리스트에 정렬 옵션 넣자 필터랑 정렬로 쉽게 볼 수 있게").
//
// The second half of the same decision: every row carries its caveat count.
// The card qualifies its own numbers with `! 3 caveats`, and a result set
// that drops the qualification is a leaderboard with the sorting removed.
//
// Two scopes: everything public, and — with a journal token — mine, which is
// the outward half of the local ledger (§9.2).

import { avatarPath } from "./author.js";
import { fail, json, publicBase } from "./http.js";
import { EMPTY_FIGURE, esc, fmt, head, layout } from "./page.js";
import { anonBadge, authorOf } from "./run.js";
import { sha256Hex } from "./ids.js";

const PAGE_SIZE = 30;
const MAX_PAGE_SIZE = 100;

// One gigabyte, because the min_vram box offers gigabytes and the column
// holds bytes (row.js: vram_bytes is bytes straight from the client).
const GB = 1024 ** 3;

// The columns a result row is drawn from. index_json is not read here: these
// are the axes the schema flattened for exactly this query, and going back to
// the JSON would be the search deciding it knows better than its own index.
// owner_token travels beside owned: the row's `.who` line links a
// token-owned run's name at its user home (/u/<handle>, TTP-127), and that
// needs the handle, not just the fact of ownership. apiRow leaves it out
// the way it leaves out every column the API does not print.
const SELECT = `SELECT id, created_at, recorded_at, model_id, model_raw, repo,
    quant_id, quant_raw, engine_kind, engine_version, os, gpu_id, gpu_ids, gpus_raw,
    gpu_count, vram_bytes, host_class, sessions, prompt_set, decode_per_sec,
    caveat_count, prompt_n, predicted_n, min_predicted_n, reasoning_n,
    cache_hit_ratio, ctx_size, n_slots, fa, kv_cache, batch, ubatch, ngl,
    offload, draft_model, draft_accept, throttled, cold, power_w, power_limit_w,
    file_bytes, active_params, n_experts, n_experts_used, params, moe,
    quant_bits, prefill_per_sec, ttft_p50_ms,
    tape_ext, card_key, author_name, author_link, avatar_key,
    title, note, owner_token, owner_token IS NOT NULL AS owned
  FROM runs`;

// The params bands (TTP-130): over active_params, in billions. A row with
// NULL active_params matches no band — every comparison below is false on
// NULL, so that falls out without a special case. Both sides use the same
// table; the Worker filters `active_params >= ? AND active_params < ?`.
const SIZE_BANDS = [
  [0, "≤4B", 0, 4e9],
  [4, "4–10B", 4e9, 10e9],
  [10, "10–35B", 10e9, 35e9],
  [35, "35–100B", 35e9, 100e9],
  [100, "100B+", 100e9, Infinity],
];

function sizeBandOf(n) {
  for (const [lower, , lo, hi] of SIZE_BANDS) if (n >= lo && n < hi) return lower;
  return null;
}

function sizeBandLabel(lower) {
  return (SIZE_BANDS.find((b) => b[0] === lower) || [])[1] || null;
}

// A params figure the way the chip prints it: integer at 10B and above,
// one decimal below (`35B`, `3.8B`).
function fmtB(n) {
  const b = n / 1e9;
  return b >= 10 ? `${Math.round(b)}B` : `${Math.round(b * 10) / 10}B`;
}

// A context size the way the chip prints it: kilobytes when even (`32k`).
function fmtK(n) {
  return n >= 1024 && n % 1024 === 0 ? `${n / 1024}k` : String(n);
}

// The cache suffix, on a row or a run page alike: the ratio when it
// travelled, else `cache 0%` only beside a prompt_n — 0 is "no hit or
// unknown, the tape does not distinguish", so without a workload it prints
// nothing rather than a zero nobody measured.
function cacheLabel(ratio, prompt_n) {
  if (ratio !== null && ratio !== undefined) return `cache ${Math.round(ratio * 100)}%`;
  if (prompt_n > 0) return "cache 0%";
  return null;
}

// The filters §9.4 names, each mapped to the column it narrows. A raw column
// is listed beside its normalised one so a run whose normalisation came back
// empty is still reachable by what was observed.
const FILTERS = [
  ["model", "model_id"],
  ["model_raw", "model_raw"],
  ["repo", "repo"],
  ["quant", "quant_id"],
  ["engine", "engine_kind"],
  // The gpu axis is membership, not equality (TTP-124): which card models
  // took part. A null column marks it for the special case in whereFor; it
  // stays in this list so the active line and the facet keep their order.
  ["gpu", null],
  ["host", "host_class"],
  ["os", "os"],
  ["set", "prompt_set"],
  ["sessions", "sessions"],
];

// The longest free-text search that reaches the database (lead, 2026-09-19).
// SQLite refuses a LIKE pattern over 50 characters —
// SQLITE_MAX_LIKE_PATTERN_LENGTH, which D1 ships at its default — and the
// pattern this builds is the text wrapped in two `%`. Before this, a 49th
// character threw inside the query and the page answered 500: measured on
// production, where pasting a model path into the box was enough. The box
// carries the same number as `maxlength`, so a person cannot type their way
// into the refusal and only a hand-written URL or an API caller sees it.
const MAX_Q = 48;

// The VRAM tiers the box offers, in GB. A threshold, not a category, so it
// has no facet counts — just these fixed options.
const VRAM_TIERS = [8, 12, 16, 24, 48, 80];

// The axes that live behind the form's disclosure — every param the selects
// write, and nothing else. `q` is deliberately absent: it is the control a
// visitor actually reaches for, and if it counted here the panel would open
// itself on every text search, which is the state this closes.
const FACET_PARAMS = [...FILTERS.map(([param]) => param), "min_vram", "size", "moe", "min_predicted"];

// Whether any of those is set, which is what decides the disclosure's `open`.
// An empty string is not set (`?engine=` is what an unchanged select posts),
// and `moe=0` is — "dense only" narrows as much as "MoE only" does.
function facetsNarrowed(url) {
  return FACET_PARAMS.some((p) => (url.searchParams.get(p) || "") !== "");
}

export async function searchAPI(request, env) {
  const url = new URL(request.url);
  const scope = await scopeOf(request, env, url);
  if (scope.error) return scope.error;

  const q = await query(env, url, scope, pageSize(url));
  if (q.error) return q.error;
  return json({
    scope: scope.name,
    sort: q.sort,
    runs: q.rows.map(apiRow),
    // A cursor rather than a page number: rows arrive newest first and new
    // ones land at the front, so an offset would show the same run twice
    // while somebody is reading.
    next: q.next,
  });
}

// The front page's own title and description, one copy for the tab, the
// search result and the link preview. Both are cut to the length the
// readers actually show: a description over ~150 characters is truncated by
// Google and by most social previews, and the old one was 174 and lost its
// last clause everywhere (measured 2026-09-19). The title is the other
// direction — "published runs" was 24 characters in a 50-60 character slot,
// so it says what kind of run and what travels with it.
const SOCIAL_TITLE = "toktape — published LLM inference runs and their caveats";
const SOCIAL_DESCRIPTION =
  "Recorded llama.cpp and vLLM runs: the tok/s, the card that says when a figure is not quotable, and a replay in the browser.";

export async function searchPage(request, env) {
  const url = new URL(request.url);
  const scope = await scopeOf(request, env, url);
  if (scope.error) return scope.error;

  const limit = pageSize(url);
  const q = await query(env, url, scope, limit);
  if (q.error) return q.error;
  const facets = await distinctFacets(env, url, scope);
  const active = activeFilters(url);
  // With 0 rows the empty state speaks instead of the total: a bare
  // "0 runs" is a dead end, and the way back (§12 in the task spec) is the
  // useful thing on that path.
  const mid =
    q.rows.length === 0
      ? await emptyState(env, url, scope)
      : `${await totalLine(env, url, scope, limit, q.next)}
${rowGrid(q.rows, url)}`;

  return new Response(
    layout({
      title: SOCIAL_TITLE,
      // The front page is a search, and a filtered search is the same page:
      // the canonical is the bare front page so a crawler indexes it once,
      // and the run pages carry the model names into the index themselves.
      //
      // A run page's preview is its own card. This one had no image at all,
      // so the link a person posts to announce the service — the one link
      // that matters most — previewed as plain text (user, 2026-09-19, meta
      // inspector). /og.png is the hero run's card, rendered by `toktape
      // card` from assets/hero.tape and committed under web/static, so the
      // preview is the artifact the site is about rather than a logo.
      meta: head({
        title: SOCIAL_TITLE,
        description: SOCIAL_DESCRIPTION,
        url: `${publicBase(request, env)}/`,
        image: `${publicBase(request, env)}/og.png`,
        imageAlt:
          "a toktape card: 144 tok/s aggregate decode over four streams of a 35B sparse MoE on one RTX A6000, with the rig, the engine and the flags under it",
        imageWidth: 1200,
        imageHeight: 675,
      }),
      style: PAGE_STYLE,
      figure: ["peek", "wave"],
      // The runs still lead — but a reader who arrives from a post has no
      // way to know this is a thing they can run until they have scrolled
      // past twenty of them to the footer (user, 2026-09-19: "깃헙링크나
      // 인스톨 안내가 너무 눈에 안띈다"). Two lines and a command, above the
      // search rather than in place of it.
      body: `
<div class="intro">
<p>Every run on this page is the file <code>toktape</code> wrote on somebody's
own server: the card, the caveats that say when a figure is not quotable, and
a replay, all drawn from those same bytes. One word on the machine already
running llama-server records yours.</p>
<p class="get"><code class="cmd">brew install midagedev/tap/toktape</code>
<a href="https://github.com/midagedev/toktape#install">other ways to install</a>
<a href="https://github.com/midagedev/toktape">GitHub</a></p>
</div>
${filterForm(url, facets)}
${active}
${mid}
${q.next ? `<p class="more"><a href="${esc(withParam(url, "cursor", q.next))}">Older runs →</a></p>` : ""}
<footer>
Newest first unless you choose another order; there is still no rank column
and no score. Every row carries the caveats the card would print, because a
result set without them is a leaderboard with the sorting taken out.<br>
<code>toktape publish &lt;run.toktape&gt;</code> puts one here.
<a href="https://github.com/midagedev/toktape">toktape on GitHub</a>
</footer>
<script src="/player/wasm_exec.js"></script>
<script src="/player/host.js"></script>
<script>addEventListener("keydown",function(e){if(e.key!=="/"||e.defaultPrevented)return;var t=e.target;if(t&&(t.tagName==="INPUT"||t.tagName==="TEXTAREA"||t.tagName==="SELECT"||t.isContentEditable))return;var box=document.querySelector("input[name=q]");if(box){box.focus();e.preventDefault();}});</script>`,
    }),
    { headers: { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" } },
  );
}

// ---------------------------------------------------------------- querying

// The WHERE behind the listing, shared by the listing itself, the total, the
// facet counts and the empty-state way back so the four cannot drift apart.
// skipParam is one URL param name whose filter is left out: a facet column's
// counts are computed with the current WHERE minus that column's own filter
// (the standard faceted-search rule, so the dropdown still lists the
// alternatives on its own axis). The cursor is not part of this WHERE at
// all: it windows the page, while every consumer here describes the whole
// filtered population.
export function whereFor(url, scope, skipParam) {
  const where = [];
  const args = [];

  // The user scope (TTP-127) is modelled on the journal scope: one owner's
  // runs — but public ones only, because the home is public and unlisted
  // runs never appear on it, even the owner's (the owner sees those under
  // scope=mine).
  if (scope.name === "mine" || scope.name === "user") {
    where.push("owner_token = ?");
    args.push(scope.owner);
  }
  if (scope.name !== "mine") {
    // Unlisted is unlisted in every scope but its owner's.
    where.push("private = 0");
  }

  for (const [param, column] of FILTERS) {
    if (param === skipParam) continue;
    if (column === null) continue; // the gpu axis: membership, special-cased below
    const v = url.searchParams.get(param);
    if (v) {
      // sessions is the one numeric axis in this list, and a value that is
      // not a number used to bind NaN — which SQLite compares as NULL, so
      // `?sessions=abc` answered "no runs" instead of "that is not a stream
      // count" (lead, 2026-09-19). An empty answer to a question nobody
      // asked is the silent fallback this file refuses everywhere else.
      if (column === "sessions") {
        if (!/^\d+$/.test(v)) {
          return { where, args, error: fail(400, `bad sessions: ${JSON.stringify(v)}; a whole number of streams travels`) };
        }
        where.push(`${column} = ?`);
        args.push(Number(v));
        continue;
      }
      where.push(`${column} = ?`);
      args.push(v);
    }
  }
  // The GPU filter is membership in the per-card ids (TTP-124): a mixed rig
  // is reachable by either of its cards. Skipped under its own name like
  // every other axis, so the facet still lists the alternatives.
  if (skipParam !== "gpu" && url.searchParams.get("gpu")) {
    where.push("EXISTS (SELECT 1 FROM json_each(runs.gpu_ids) WHERE value = ?)");
    args.push(url.searchParams.get("gpu"));
  }
  if (skipParam !== "min_vram" && url.searchParams.get("min_vram")) {
    // The same rule as sessions: NaN * GB is NaN, and a NaN threshold
    // matches nothing rather than saying so (lead, 2026-09-19).
    const raw = url.searchParams.get("min_vram");
    if (!/^\d+(\.\d+)?$/.test(raw)) {
      return { where, args, error: fail(400, `bad min_vram: ${JSON.stringify(raw)}; a number of gigabytes travels`) };
    }
    where.push("vram_bytes >= ?");
    args.push(Number(raw) * GB);
  }
  // The params band (TTP-130): `size=<lower bound in B>` over
  // active_params. Anything outside the five bounds is a 400, never a
  // silent fallback — a caller asking for a band that is not there must
  // hear so. Skipped under its own name like min_vram.
  if (skipParam !== "size" && url.searchParams.has("size")) {
    const raw = url.searchParams.get("size");
    const band = SIZE_BANDS.find((b) => String(b[0]) === raw);
    if (!band) return { where, args, error: fail(400, `unknown size: ${raw}; one of 0, 4, 10, 35, 100`) };
    if (band[3] === Infinity) {
      where.push("active_params >= ?");
      args.push(band[2]);
    } else {
      where.push("active_params >= ? AND active_params < ?");
      args.push(band[2], band[3]);
    }
  }
  // MoE only vs dense only (TTP-130): `moe` travelled as 1/NULL, so dense
  // is `moe IS NULL` — absent, never 0, the API's rule for unknown.
  if (skipParam !== "moe" && url.searchParams.has("moe")) {
    const raw = url.searchParams.get("moe");
    if (raw === "1") where.push("moe = 1");
    else if (raw === "0") where.push("moe IS NULL");
    else return { where, args, error: fail(400, `unknown moe: ${raw}; one of 0, 1`) };
  }
  // The generated-tokens floor (TTP-130): a non-integer is a 400, the same
  // way an unknown sort is. No `min_ctx` beside it: ctx_size is a
  // reservation (-c), not a measured length, and research ruled the filter
  // out.
  if (skipParam !== "min_predicted" && url.searchParams.has("min_predicted")) {
    const raw = url.searchParams.get("min_predicted");
    if (!/^\d+$/.test(raw || "")) {
      return { where, args, error: fail(400, `bad min_predicted: ${JSON.stringify(raw)}; an integer number of tokens travels`) };
    }
    where.push("predicted_n >= ?");
    args.push(Number(raw));
  }
  // Free text falls back across the raw strings, which is the whole reason
  // they are stored: a run whose model never normalised is still findable by
  // the file name that was observed (§9.4, rule 2).
  const q = (url.searchParams.get("q") || "").trim();
  if (q.length > MAX_Q) {
    return { where, args, error: fail(400, `the search text is ${q.length} characters; ${MAX_Q} is the most that travels`) };
  }
  if (q && skipParam !== "q") {
    const cols = ["model_raw", "model_id", "repo", "gpus_raw", "engine_kind", "quant_raw"];
    // One placeholder per column rather than a numbered one reused. SQLite
    // numbers a bare `?` as one past the highest index seen so far, so a
    // single `?1` in the middle of positional placeholders silently makes
    // the parameters after it collide with the ones before.
    where.push("(" + cols.map((c) => `${c} LIKE ?`).join(" OR ") + ")");
    const like = `%${q.replace(/[%_]/g, "")}%`;
    for (const _ of cols) args.push(like);
  }
  return { where, args };
}

// The listing order. Absent means newest: today's order, unchanged. Anything
// outside the three named values is a 400, never a silent fallback — a caller
// asking for an order that is not there must hear so.
const SORT_LABEL = { newest: "newest first", oldest: "oldest first", decode: "fastest decode first" };

export function sortOf(url) {
  const v = url.searchParams.get("sort");
  // Absent and empty both mean "not asked": a form that submits `sort=`
  // with nothing chosen is not asking for an order that does not exist.
  if (v === null || v === "" || v === "newest") return { sort: "newest" };
  if (v === "oldest" || v === "decode") return { sort: v };
  return { error: fail(400, `unknown sort: ${v}; one of newest, oldest, decode`) };
}

export async function query(env, url, scope, limit) {
  const s = sortOf(url);
  if (s.error) return s;
  const { sort } = s;
  // whereFor can refuse a filter the way sortOf refuses an order; the
  // refusal is a response, returned the same way. (countTotal and
  // distinctFacets run only after query() succeeded on the same URL, so
  // they never meet it there.)
  const w = whereFor(url, scope, null);
  if (w.error) return { error: w.error };
  const { where, args } = w;
  const cursor = decodeCursor(url.searchParams.get("cursor"));
  // created_at, never recorded_at: ordering on a field out of the upload
  // would make the listing trust the uploader's clock. (The offset question
  // beside it is settled — a published tape carries the instant in UTC,
  // §9.3/TTP-120 — but whose clock it came from has not changed.)
  let order;
  if (sort === "oldest") {
    if (cursor) {
      where.push("(created_at > ? OR (created_at = ? AND id > ?))");
      args.push(cursor.created_at, cursor.created_at, cursor.id);
    }
    order = "ORDER BY created_at ASC, id ASC";
  } else if (sort === "decode") {
    // NULLs are one well-defined value: -1 sorts below every measured
    // figure, so a run with no figure comes last, never first — in the
    // ORDER BY and in the cursor comparison alike.
    if (cursor) {
      const raw = cursor.sortKey === "" ? -1 : Number(cursor.sortKey);
      const key = Number.isFinite(raw) ? raw : -1;
      where.push(
        "(COALESCE(decode_per_sec, -1) < ? OR (COALESCE(decode_per_sec, -1) = ? AND " +
          "(created_at < ? OR (created_at = ? AND id < ?))))",
      );
      args.push(key, key, cursor.created_at, cursor.created_at, cursor.id);
    }
    order = "ORDER BY COALESCE(decode_per_sec, -1) DESC, created_at DESC, id DESC";
  } else {
    if (cursor) {
      where.push("(created_at < ? OR (created_at = ? AND id < ?))");
      args.push(cursor.created_at, cursor.created_at, cursor.id);
    }
    order = "ORDER BY created_at DESC, id DESC";
  }

  const sql = `${SELECT}
    ${where.length ? "WHERE " + where.join(" AND ") : ""}
    ${order}
    LIMIT ?`;

  // One extra row, only to learn whether there is a next page.
  const { results } = await env.DB.prepare(sql).bind(...args, limit + 1).all();
  const rows = results.slice(0, limit);
  const last = rows[rows.length - 1];
  return {
    rows,
    next: results.length > limit && last ? encodeCursor(last, sort) : null,
    sort,
  };
}

// One COUNT over the same WHERE: a population size for the page, not a
// ranking of anything.
// Exported for the user home (user.js), whose head description and header
// count the same population the listing pages: one COUNT over the same
// WHERE, or the two drift apart.
export async function countTotal(env, url, scope) {
  const { where, args } = whereFor(url, scope, null);
  const row = await env.DB.prepare(
    `SELECT COUNT(*) AS n FROM runs WHERE ${where.join(" AND ")}`,
  )
    .bind(...args)
    .first();
  return row.n;
}

export async function totalLine(env, url, scope, limit, hasNext) {
  const n = await countTotal(env, url, scope);
  // The sort was validated by query() before this runs; an unknown value
  // here falls back to the label only and never to an order.
  const sort = url.searchParams.get("sort") || "newest";
  const label = SORT_LABEL[sort] || SORT_LABEL.newest;
  const caveat =
    sort === "decode"
      ? " — different models, streams and quants side by side; every row keeps its caveats."
      : "";
  return `<p class="total"><b>${n}</b> run${n === 1 ? "" : "s"}, ${label}${hasNext ? ` · showing the first ${limit}` : ""}${caveat}</p>`;
}

export async function distinctFacets(env, url, scope) {
  const one = async (param, column) => {
    const { where, args } = whereFor(url, scope, param);
    // One row past what is shown, only to learn whether the tail was
    // folded: a rare GPU should read as folded, not absent.
    const { results } = await env.DB.prepare(
      `SELECT ${column} AS v, COUNT(*) AS n FROM runs
       WHERE ${where.join(" AND ")} AND ${column} IS NOT NULL
       GROUP BY ${column} ORDER BY n DESC, v ASC LIMIT 21`,
    )
      .bind(...args)
      .all();
    return { rows: results.slice(0, 20), more: results.length > 20 };
  };
  // The gpu facet counts rows per member id (TTP-124): a mixed rig raises
  // the count of each of its cards. Rows with no gpu_ids take no part —
  // they are what the fallback chip is for, not this dropdown.
  const gpuFacet = (async () => {
    const { where, args } = whereFor(url, scope, "gpu");
    const { results } = await env.DB.prepare(
      `SELECT j.value AS v, COUNT(DISTINCT runs.id) AS n FROM runs, json_each(runs.gpu_ids) j
       WHERE ${where.join(" AND ")}
       GROUP BY v ORDER BY n DESC, v ASC LIMIT 21`,
    )
      .bind(...args)
      .all();
    return { rows: results.slice(0, 20), more: results.length > 20 };
  })();
  const [model, engine, quant, gpu, host, set] = await Promise.all([
    one("model", "model_id"),
    one("engine", "engine_kind"),
    one("quant", "quant_id"),
    gpuFacet,
    one("host", "host_class"),
    one("set", "prompt_set"),
  ]);
  return { model, engine, quant, gpu, host, set };
}

async function scopeOf(request, env, url) {
  if (url.searchParams.get("scope") !== "mine") return { name: "public" };

  // The header only. A token in a query string is a credential in a URL, and
  // a URL is the one thing here that gets logged, pasted and shared — which
  // is why the journal scope is API-only until there is a sign-in that can
  // set a cookie (TTP-114's remainder).
  const auth = request.headers.get("Authorization") || "";
  const presented = auth.startsWith("Bearer ") ? auth.slice(7).trim() : "";
  if (!presented) {
    return { error: fail(401, "the journal scope is one token's own runs; present it as `Authorization: Bearer <token>`") };
  }
  const row = await env.DB.prepare("SELECT id FROM tokens WHERE hash = ?").bind(await sha256Hex(presented)).first();
  if (!row) return { error: fail(401, "that token is not one this service issued") };
  return { name: "mine", owner: row.id };
}

export function pageSize(url) {
  const n = Number(url.searchParams.get("limit"));
  if (!Number.isFinite(n) || n <= 0) return PAGE_SIZE;
  return Math.min(Math.floor(n), MAX_PAGE_SIZE);
}

// The keyset cursor: (sortKey, created_at, id), where sortKey is the sort
// column's value on the last row — "" outside the decode order, which has no
// other column of its own. Always three parts, so a cursor minted before the
// sort parameter still decodes (two parts: newest's window).
function encodeCursor(row, sort) {
  const key = sort === "decode" ? String(row.decode_per_sec ?? -1) : "";
  return btoa(`${key}|${row.created_at}|${row.id}`).replace(/=+$/, "");
}

function decodeCursor(s) {
  if (!s) return null;
  try {
    const parts = atob(s).split("|");
    if (parts.length === 2) {
      const [created_at, id] = parts;
      if (!created_at || !id) return null;
      return { sortKey: "", created_at, id };
    }
    if (parts.length !== 3) return null;
    const [sortKey, created_at, id] = parts;
    if (!created_at || !id) return null;
    return { sortKey, created_at, id };
  } catch {
    return null;
  }
}

// ------------------------------------------------------------------ shapes

export function apiRow(r) {
  // The profile and the note ride beside the index columns, each present
  // only when set — absent, not null/empty, for unknown.
  const out = {
    id: r.id,
    url: `/r/${r.id}`,
    published_at: r.created_at,
    // Whether a journal token owns the run: its holder can take it down, so
    // a reader knows the run has an owner and was not a one-off drop.
    owned: r.owned === 1,
    recorded_at: r.recorded_at,
    model_id: r.model_id,
    model_raw: r.model_raw,
    repo: r.repo,
    quant_id: r.quant_id,
    quant_raw: r.quant_raw,
    engine_kind: r.engine_kind,
    engine_version: r.engine_version,
    os: r.os,
    gpu_id: r.gpu_id,
    gpus_raw: r.gpus_raw,
    gpu_count: r.gpu_count,
    ...(parseGpuIds(r.gpu_ids).length ? { gpu_ids: parseGpuIds(r.gpu_ids) } : {}),
    vram_bytes: r.vram_bytes,
    host_class: r.host_class,
    sessions: r.sessions,
    prompt_set: r.prompt_set,
    decode_per_sec: r.decode_per_sec,
    caveat_count: r.caveat_count,
    // The figures (TTP-130), each under its contract name and present only
    // when set — absent, not null, for unknown. Bools travelled as 1/NULL
    // and read back as true/absent.
    ...copyNum(r, [
      "prompt_n",
      "predicted_n",
      "min_predicted_n",
      "reasoning_n",
      "cache_hit_ratio",
      "ctx_size",
      "n_slots",
      "draft_accept",
      "power_w",
      "power_limit_w",
      "file_bytes",
      "active_params",
      "n_experts",
      "n_experts_used",
      "params",
      "quant_bits",
      "prefill_per_sec",
      "ttft_p50_ms",
    ]),
    ...copyStr(r, ["fa", "kv_cache", "batch", "ubatch", "ngl", "offload", "draft_model"]),
    ...(r.throttled === 1 ? { throttled: true } : {}),
    ...(r.cold === 1 ? { cold: true } : {}),
    ...(r.moe === 1 ? { moe: true } : {}),
  };
  const { author, title, note } = authorOf(r);
  if (author) out.author = author;
  if (title) out.title = title;
  if (note) out.note = note;
  return out;
}

// Absent, never null, for whatever is unset: the API's existing rule for
// unknown, applied to the figures beside the older columns.
function copyNum(r, names) {
  const out = {};
  for (const n of names) if (r[n] !== null && r[n] !== undefined) out[n] = r[n];
  return out;
}

function copyStr(r, names) {
  const out = {};
  for (const n of names) if (typeof r[n] === "string" && r[n] !== "") out[n] = r[n];
  return out;
}

// The rows as one grid: two or three across on a desktop (the column count
// falls out of the width — .rows in PAGE_STYLE), one down a phone. Shared
// with the user home so the two listings cannot drift apart.
export function rowGrid(rows, url) {
  return `<div class="rows">\n${rows.map((r) => resultRow(r, url)).join("\n")}\n</div>`;
}

export function resultRow(r, url) {
  // Each fact is {shown, param, value}: what a reader sees and, where the
  // axis is one the index normalised, the exact value that narrows to it.
  // The two are never derived from each other here — turning `UD-Q6_K` into
  // `q6_k` on this side would be a second normaliser, and the client already
  // owns that one (§9.4, rule 1). `suffix` is display-only: the engine
  // version rides inside the engine chip (a rate without its build is
  // half-read) while the link still narrows on the engine alone.
  const facts = [
    { shown: r.engine_kind, suffix: r.engine_version || null, param: "engine", value: r.engine_kind },
    { shown: r.quant_raw || r.quant_id, param: r.quant_id ? "quant" : null, value: r.quant_id },
    // The figures (TTP-130), after the quant chip in contract order. Each
    // omitted when unknown; only the params chip narrows (to its band).
    ...figFacts(r),
    ...gpuFacts(r),
    { shown: r.os, param: "os", value: r.os },
    {
      shown: r.sessions ? `${r.sessions} stream${r.sessions === 1 ? "" : "s"}` : null,
      param: "sessions",
      value: r.sessions ? String(r.sessions) : null,
    },
    { shown: r.prompt_set, param: "set", value: r.prompt_set },
  ];
  // When the note's title is set it is the row's heading and the model
  // name moves beneath in the small style; when unset the row is as today.
  const model = r.model_id || r.model_raw || "a run";
  // The run leads the row and the words are its caption — the feed idiom
  // (user, 2026-09-19: the frame sitting between the heading and the chips
  // read as a banner stuck into the text, and its full-bleed edge on a phone
  // fought the caption's own margin). DOM order, not CSS order, so a screen
  // reader and the IntersectionObserver see the same row.
  return `<article class="row">
  <a class="stage feed" href="/r/${esc(r.id)}" data-tape="/r/${esc(r.id)}${esc(r.tape_ext || ".tape")}"
     aria-label="open this run">${
       r.card_key
         ? `<img class="card" src="/r/${esc(r.id)}.png" width="1200" height="675" loading="lazy" alt="">`
         : `<div class="card nocard"></div>`
     }<pre class="screen"></pre></a>
  <div class="rowhead">
    <a class="name" href="/r/${esc(r.id)}">${esc(r.title || model)}</a>
    <span class="rates"><span class="rate num">${fmt(r.decode_per_sec)}<span class="u"> tok/s</span></span>${
      r.prefill_per_sec ? `<span class="rate sub num">prefill ${fmt(r.prefill_per_sec)}<span class="u"> tok/s</span></span>` : ""
    }</span>
  </div>
  ${r.title ? `<div class="model">${esc(model)}</div>` : ""}
  <div class="facts">${facts.filter((f) => f.shown).map((f) => chip(f, url)).join("")}${caveatChip(r)}</div>
  ${whoLine(r)}
  <div class="when">${esc(String(r.created_at).slice(0, 10))}${r.repo ? ` · ${esc(r.repo)}` : ""}</div>
</article>`;
}

// The row's `.who` line: the avatar at 16 px and the name, linked the way
// the run page links it. Absent entirely when no author travels. On a run
// a journal token owns, the name links at the owner's home (/u/<handle>,
// TTP-127) — the external link moved there, so it is not printed here —
// and what is shown is still read off the run's own columns (the record of
// what that upload said), never off the token.
function whoLine(r) {
  const avatar = avatarPath(r.avatar_key);
  if (!r.author_name && !avatar && r.owned === 1) return "";
  const img = avatar ? `<img class="avatar" src="${esc(avatar)}" width="16" height="16" alt="">` : "";
  let who = "";
  if (r.owner_token) {
    if (r.author_name) who = `<a href="/u/${esc(r.owner_token)}">${esc(r.author_name)}</a>`;
  } else if (r.author_name && r.author_link) {
    who = `<a rel="nofollow ugc noopener" href="${esc(r.author_link)}">${esc(r.author_name)}</a>`;
  } else {
    who = esc(r.author_name || "");
  }
  return `<div class="who">${img}${img && who ? " " : ""}${who}${anonBadge(r.owned === 1)}</div>`;
}

// The figure chips (TTP-130): params, workload, context, config, offload,
// after the quant chip in this order, each omitted when unknown. None is a
// link except params, which narrows to the band its active_params falls in
// (no link when active_params is unknown). None of these is derived: every
// part is a column the client filled in.
function figFacts(r) {
  const out = [];
  if (r.params !== null && r.params !== undefined) {
    let shown = fmtB(r.params);
    if (r.moe === 1 && r.active_params !== null && r.active_params !== undefined) {
      shown += ` · ${fmtB(r.active_params)} active`;
    }
    const band = r.active_params !== null && r.active_params !== undefined ? sizeBandOf(r.active_params) : null;
    out.push({ shown, param: band !== null ? "size" : null, value: band !== null ? String(band) : null });
  } else if (r.active_params !== null && r.active_params !== undefined) {
    const band = sizeBandOf(r.active_params);
    out.push({
      shown: fmtB(r.active_params),
      param: band !== null ? "size" : null,
      value: band !== null ? String(band) : null,
    });
  }
  const load = [];
  if (r.prompt_n !== null && r.prompt_n !== undefined) load.push(`P${r.prompt_n}`);
  if (r.predicted_n !== null && r.predicted_n !== undefined) load.push(`G${r.predicted_n}`);
  if (load.length) {
    let shown = load.join(" · ");
    const cache = cacheLabel(r.cache_hit_ratio, r.prompt_n);
    if (cache) shown += ` · ${cache}`;
    if (
      r.min_predicted_n !== null &&
      r.min_predicted_n !== undefined &&
      r.predicted_n !== null &&
      r.predicted_n !== undefined &&
      r.min_predicted_n < r.predicted_n
    ) {
      shown += ` · min ${r.min_predicted_n}`;
    }
    out.push({ shown, param: null, value: null });
  }
  if (r.ctx_size !== null && r.ctx_size !== undefined) {
    // De-emphasised: the reservation, not a measured length.
    out.push({ shown: `ctx ${fmtK(r.ctx_size)}`, param: null, value: null, dim: true });
  }
  const cfg = [];
  if (r.fa) cfg.push(`fa ${r.fa}`);
  if (r.kv_cache) cfg.push(`kv ${r.kv_cache}`);
  if (cfg.length) out.push({ shown: cfg.join(" · "), param: null, value: null });
  // `full` is omitted: every GPU running the whole model is the default
  // reading, and a chip for it would say nothing.
  if (r.offload === "partial" || r.offload === "cpu") {
    out.push({ shown: `offload ${r.offload}`, param: null, value: null });
  }
  return out;
}

// The GPU chips: one per card model that took part (TTP-124). A single kind
// keeps today's count prefix (`2× rtx-4090`); a mixed rig gets one chip per
// distinct id with no prefix. Without gpu_ids, today's fallback: the shared
// id's chip, else the raw names unlinked. Each chip is a filter link through
// the existing chip(), so the active one renders as .fact.on like the rest.
function gpuFacts(r) {
  const ids = parseGpuIds(r.gpu_ids);
  if (ids.length) {
    const distinct = [...new Set(ids)];
    if (distinct.length === 1) {
      const shown = r.gpu_count > 1 ? `${r.gpu_count}× ${distinct[0]}` : distinct[0];
      return [{ shown, param: "gpu", value: distinct[0] }];
    }
    return distinct.map((id) => ({ shown: id, param: "gpu", value: id }));
  }
  return [
    {
      shown: r.gpu_id ? (r.gpu_count > 1 ? `${r.gpu_count}× ${r.gpu_id}` : r.gpu_id) : r.gpus_raw,
      param: r.gpu_id ? "gpu" : null,
      value: r.gpu_id,
    },
  ];
}

// gpu_ids arrives as the TEXT the migration stored (a JSON array); a row
// from before the migration has NULL and keeps the fallback above.
function parseGpuIds(v) {
  if (Array.isArray(v)) return v.filter((x) => typeof x === "string" && x !== "");
  if (typeof v !== "string" || v === "") return [];
  try {
    const a = JSON.parse(v);
    return Array.isArray(a) ? a.filter((x) => typeof x === "string" && x !== "") : [];
  } catch {
    return [];
  }
}

// A fact that is also a filter is a link to that filter, so narrowing a
// search is reading rather than a form to fill in. The link copies the
// current URL's params and sets just this one, so chips accumulate instead
// of replacing each other; a chip whose filter is already active renders as
// plain text, so the active narrowing is visible in the rows.
function chip(f, url) {
  const cls = f.dim ? "fact dim" : "fact";
  const inner = esc(f.shown) + (f.suffix ? `<span class="v">${esc(f.suffix)}</span>` : "");
  if (!f.param || !f.value) return `<span class="${cls}">${inner}</span>`;
  if (url.searchParams.get(f.param) === String(f.value)) return `<span class="fact on">${inner}</span>`;
  const u = new URL(url.href);
  u.searchParams.set(f.param, String(f.value));
  u.searchParams.delete("cursor");
  return `<a class="fact link" href="${esc(u.pathname + u.search)}">${inner}</a>`;
}

function caveatChip(r) {
  if (!r.caveat_count) return "";
  return `<a class="fact caveat" href="/r/${esc(r.id)}#caveats">! ${r.caveat_count} caveat${r.caveat_count === 1 ? "" : "s"}</a>`;
}

// The params that count as narrowing, in display order: free text first,
// then the normalised axes, then the VRAM threshold. scope/limit are page
// furniture, never filters.
function activeParams(url) {
  const out = [];
  if ((url.searchParams.get("q") || "").trim()) out.push("q");
  for (const [param] of FILTERS) if (url.searchParams.get(param)) out.push(param);
  if (url.searchParams.get("min_vram")) out.push("min_vram");
  // The figure filters (TTP-130) narrow like the rest, so they read in the
  // active line and offer the same way back.
  if (url.searchParams.has("size")) out.push("size");
  if (url.searchParams.has("moe")) out.push("moe");
  if (url.searchParams.has("min_predicted")) out.push("min_predicted");
  return out;
}

function filterLabel(param, url) {
  if (param === "q") return `\u201c${(url.searchParams.get("q") || "").trim()}\u201d`;
  if (param === "min_vram") return `vram: ${url.searchParams.get("min_vram")} GB+`;
  if (param === "size") return `size: ${sizeBandLabel(Number(url.searchParams.get("size"))) || url.searchParams.get("size")}`;
  if (param === "moe") return url.searchParams.get("moe") === "1" ? "MoE" : "dense";
  if (param === "min_predicted") return `\u2265 ${url.searchParams.get("min_predicted")} generated`;
  return `${param}: ${url.searchParams.get(param)}`;
}

// The URL without one filter (and without the page window): every state
// stays a shareable GET link, no JS. scope/limit are kept — they are not
// filters and these links never strip them.
function withoutParam(url, param) {
  const u = new URL(url.href);
  u.searchParams.delete(param);
  u.searchParams.delete("cursor");
  return u.pathname + u.search;
}

export function activeFilters(url) {
  const params = activeParams(url);
  if (!params.length) return "";
  const links = params
    .map(
      (p) =>
        `<a class="fact on" href="${esc(withoutParam(url, p))}" title="remove">${esc(filterLabel(p, url))} ×</a>`,
    )
    .join("");
  return `<div class="active"><span class="k">Narrowed to</span>${links}<a class="clear" href="/">Clear all</a></div>`;
}

export function filterForm(url, facets) {
  const sel = (name, label, facet) => {
    let rows = facet.rows;
    const current = url.searchParams.get(name) || "";
    // A filter that is in the URL is in the box, even when nothing matches
    // it: a <select> whose value is not among its options shows the first
    // one, so `?engine=vllm` over a listing with no vllm runs looked like
    // "any engine" with an empty result and no visible reason (2026-09-18,
    // seen on the page). The count says 0, which is the reason.
    if (current && !rows.some((o) => o.v === current)) rows = [{ v: current, n: 0 }, ...rows];
    if (!rows.length) return "";
    const opts = rows
      .map(
        (o) =>
          `<option value="${esc(o.v)}"${o.v === current ? " selected" : ""}>${esc(o.v)} (${o.n})</option>`,
      )
      .join("");
    const more = facet.more ? `<option disabled>+ more — use the search box</option>` : "";
    return `<select name="${name}" onchange="this.form.submit()"><option value="">${esc(label)}</option>${opts}${more}</select>`;
  };
  const vram = (() => {
    const current = url.searchParams.get("min_vram") || "";
    const opts = [`<option value="">any VRAM</option>`]
      .concat(
        VRAM_TIERS.map(
          (g) => `<option value="${g}"${String(g) === current ? " selected" : ""}>${g} GB+</option>`,
        ),
      )
      .join("");
    return `<select name="min_vram" onchange="this.form.submit()">${opts}</select>`;
  })();
  // The order is page furniture, not a filter: it never joins the active
  // line, and the form carries it the way the URL does.
  const sortSel = (() => {
    const current = url.searchParams.get("sort") || "newest";
    const opts = [
      ["newest", "newest first"],
      ["oldest", "oldest first"],
      ["decode", "fastest decode first"],
    ]
      .map(([v, label]) => `<option value="${v}"${v === current ? " selected" : ""}>${label}</option>`)
      .join("");
    return `<select name="sort" onchange="this.form.submit()">${opts}</select>`;
  })();
  // The params band (TTP-130): fixed options mirroring the VRAM box, over
  // active_params — the same table both sides filter by.
  const size = (() => {
    const current = url.searchParams.get("size") || "";
    const opts = [`<option value="">any size</option>`]
      .concat(
        SIZE_BANDS.map(
          ([v, label]) => `<option value="${v}"${String(v) === current ? " selected" : ""}>${esc(label)}</option>`,
        ),
      )
      .join("");
    return `<select name="size" onchange="this.form.submit()">${opts}</select>`;
  })();
  // MoE only vs dense only (TTP-130): fixed options mirroring the VRAM box.
  const moe = (() => {
    const current = url.searchParams.get("moe") || "";
    const opts = [
      ["", "dense or MoE"],
      ["1", "MoE only"],
      ["0", "dense only"],
    ]
      .map(([v, label]) => `<option value="${v}"${v === current ? " selected" : ""}>${esc(label)}</option>`)
      .join("");
    return `<select name="moe" onchange="this.form.submit()">${opts}</select>`;
  })();
  // The current path, not "/": on /u/<handle> the filters used to submit to
  // the front page (2026-09-19).
  //
  // Three controls stand, eight fold away. All eleven at once read as an
  // admin console standing between a visitor and the runs they came for
  // (user, 2026-09-19: "검색 필터가 너무 다 펼쳐져 있고") — and the two rows
  // they filled were the whole first screen. The box, the order and the
  // button are what a visitor uses; the axes are what they go looking for,
  // so the summary names them rather than saying "Filters", which would
  // hide the fact that this search knows what a quantisation is.
  //
  // <details> and not a script: the panel is open in the markup or it is
  // not, so a listing narrowed by a link arrives showing why, and this adds
  // no inline handler to the ones TTP-132 is already about.
  return `<form class="filters" method="get" action="${esc(url.pathname)}">
  <input type="search" name="q" maxlength="${MAX_Q}" value="${esc(url.searchParams.get("q") || "")}"
    placeholder="model, repo, GPU, engine…" autocomplete="off">
  <kbd title="press / to search">/</kbd>
  ${sortSel}
  <button type="submit">Search</button>
  <details class="axes"${facetsNarrowed(url) ? " open" : ""}>
  <summary>Narrow by model, size, engine, quantisation, GPU, host, VRAM, prompt set</summary>
  <div class="axrow">
  ${sel("model", "any model", facets.model)}
  ${size}
  ${sel("engine", "any engine", facets.engine)}
  ${sel("quant", "any quantisation", facets.quant)}
  ${sel("gpu", "any GPU", facets.gpu)}
  ${sel("host", "any host", facets.host)}
  ${vram}
  ${sel("set", "any prompt set", facets.set)}
  ${moe}
  </div>
  </details>
</form>`;
}

async function emptyState(env, url, scope) {
  const filtered = [...url.searchParams.keys()].some((k) => k !== "cursor");
  if (!filtered) {
    return `${EMPTY_FIGURE}<p class="empty">No runs published yet. <code>toktape publish &lt;run.toktape&gt;</code> puts the first one here.</p>`;
  }
  const back = (await wayBack(env, url, scope)).join(" · ");
  return `${EMPTY_FIGURE}<p class="empty">No published run matches that. The filters are exact; the search box falls back to the names as they were recorded.${back ? `<br>Try ${back}.` : ""}</p>`;
}

// One link per active filter, each naming the population that filter hides:
// a COUNT with that one param removed. Only runs on the empty path, at most
// five queries, and a removal that still matches nothing is not offered.
async function wayBack(env, url, scope) {
  const out = [];
  for (const p of activeParams(url).slice(0, 5)) {
    const { where, args } = whereFor(url, scope, p);
    const row = await env.DB.prepare(`SELECT COUNT(*) AS n FROM runs WHERE ${where.join(" AND ")}`)
      .bind(...args)
      .first();
    if (row.n) out.push(`<a href="${esc(withoutParam(url, p))}">without ${esc(p)} · ${row.n} runs</a>`);
  }
  return out;
}

function withParam(url, key, value) {
  const u = new URL(url);
  u.searchParams.set(key, value);
  return u.pathname + u.search;
}

// Exported for the user home (user.js), whose rows are the front page's
// rows: same stylesheet, or the same row reads differently there.
export const PAGE_STYLE = `
/* The listing is wider than a run page: it is a grid of runs, and a run
   replayed at 120 columns needs every pixel a column can give it. 84rem
   puts two across on a laptop (1280–1400 px, ~4.9 px a cell) and three on a
   monitor (~3.5 px a cell) — the cell size, not taste, decides where the
   third column appears (user, 2026-09-19: "한 행에 두 개나 세 개"). */
main { max-width: 84rem; }
/* Without the margin a 52rem page had, the fixed figures would sit on the
   grid: below 106rem (the grid plus a figure either side) they take the
   places the phone gives them — the peek at the foot, the wave small in
   the top-right corner beside the brand. */
@media (min-width: 48rem) and (max-width: 106rem) {
  .figure { position: static; margin: 1rem auto 0; }
  .figure.peek { width: 9rem; margin-bottom: -.5rem; }
  .figure.wave { position: absolute; top: .75rem; right: 1.25rem; width: 5.5rem; margin: 0; z-index: 0; }
  /* The filter row stops short of her corner, or she stands on Search
     (main-scoped so it outranks the shorthand margin declared below). The
     intro sits in the same top-right zone and needs the same clearance, or
     its first line runs under her. */
  main .filters, main .intro { margin-right: 6.5rem; }
}
/* The intro: what this is and how to get it, in the place a reader's eye
   already is. Kept to a prose column even though the grid below is 84rem —
   a sentence measured across a monitor is a sentence nobody reads — and the
   command is the loud thing in it, because it is the thing to copy. */
.intro { max-width: 46rem; margin: 0 0 1.75rem; color: #99a0aa; font-size: .9rem; }
.intro p { margin: 0 0 .7rem; }
.intro .get { display: flex; flex-wrap: wrap; align-items: center; gap: .5rem .9rem;
  margin: 0; font-size: .85rem; }
/* The command box takes the links' own blue for its border rather than the
   page's hairline grey: at #242932 the box sat at the ground's luminance and
   the blue link text beside it was the brightest thing in the band, which
   put the eye on "other ways to install" before the command itself (vision,
   2026-09-19). The fill rises with it so the box reads as raised, not
   outlined. */
.intro .cmd { background: #171c26; border: 1px solid #38507a; border-radius: 6px;
  padding: .4rem .65rem; color: #e6eaf1; font-size: .85rem; user-select: all; }
.filters { display: flex; flex-wrap: wrap; gap: .5rem; margin: 0 0 2rem; }
/* The folded axes take a row of their own inside the form's flex, or the
   selects flow in beside the button when the panel opens. */
.filters details { flex: 1 0 100%; margin: .15rem 0 0; }
/* The summary is a control, so it is shaped like the controls beside it
   rather than like a caption — inline-block so the chip hugs its text
   instead of running the width of the row it owns. */
.filters summary { display: inline-block; list-style: none; cursor: pointer;
  background: #14171d; border: 1px solid #242932; border-radius: 6px;
  color: #7d848f; font-size: .8rem; padding: .4rem .6rem; }
.filters details[open] summary { color: #a8aeb8; }
.filters summary::-webkit-details-marker { display: none; }
.filters summary::before { content: "\\25B8\\00a0"; }
.filters details[open] summary::before { content: "\\25BE\\00a0"; }
.filters summary:hover { color: #d7dae0; }
.filters .axrow { display: flex; flex-wrap: wrap; gap: .5rem; padding: .5rem 0 .1rem; }
.filters input, .filters select, .filters button {
  background: #14171d; color: #d7dae0; border: 1px solid #242932; border-radius: 6px;
  padding: .45rem .6rem; font: inherit; font-size: .875rem; }
.filters kbd { align-self: center; }
/* The box grows to fill the row it shares with the order and the button, but
   stops: with the axes folded away there is nothing left to take the rest of
   a wide row, and a search field run to 60rem for "model, repo, GPU" is a
   field that looks like it wants a paragraph (2026-09-19). */
.filters input { flex: 1 1 14rem; min-width: 0; max-width: 34rem; }
.filters select { flex: 0 1 auto; min-width: 0; }
.filters button { background: #1f2630; border-color: #2c3542; cursor: pointer; }
.filters button:hover { background: #262f3b; }
.active { display: flex; flex-wrap: wrap; gap: .4rem; align-items: center; margin: -1rem 0 1.25rem; font-size: .8rem; }
.active .k { color: #6b727d; margin-right: .2rem; }
.clear { color: #7d848f; margin-left: .4rem; }
.total { color: #7d848f; font-size: .8rem; margin: 0 0 .5rem; }
.total b { color: #d7dae0; font-weight: 600; }
.fact .v { color: #6b727d; margin-left: .35rem; }
kbd { font: .7rem ui-monospace, Menlo, monospace; color: #6b727d; border: 1px solid #242932; border-radius: 4px; padding: 0 .35rem; }
@media (max-width: 48rem) { .filters kbd { display: none; } }
/* On a phone the brand row wraps its tagline to a second line, so its 2.75rem
   bottom margin plus five lines of intro pushed the first run clean off the
   screen: the whole viewport was header (vision, 2026-09-19). The gap halves
   and the prose steps down one size, which brings the run count and the top
   of the first tile back above the fold without shortening what it says. */
@media (max-width: 48rem) {
  main .brand { margin-bottom: 1.75rem; }
  .intro { font-size: .85rem; margin-bottom: 1.5rem; }
  .intro p { margin-bottom: .6rem; }
}
/* The grid: as many 26rem columns as fit, so a laptop gets two and a
   monitor three; each row is its run on top and the words as its caption. */
.rows { display: grid; grid-template-columns: repeat(auto-fill, minmax(26rem, 1fr));
  gap: 2.25rem 1.5rem; margin-top: .75rem; }
.row { display: flex; flex-direction: column; min-width: 0; }
.rowhead { display: flex; align-items: baseline; gap: 1rem; justify-content: space-between; }
/* The title yields to the figures but never to one letter a line: it wraps at
   word boundaries with a floor of ten characters, and the figures stack
   under each other on the right instead of pushing it (lead, 2026-09-19 —
   the first prefill span squeezed a phone title into a column of letters). */
.name { color: #eef1f5; font-weight: 600; font-size: 1rem; overflow-wrap: anywhere; min-width: 10ch; flex: 1 1 auto; }
.rates { display: flex; flex-direction: column; align-items: flex-end; flex: 0 0 auto; }
/* The model under a titled row, and the author under that: both small, both
   beside the date line's colour, so a titled row still reads as one run. */
.model { font-size: .78rem; color: #6b727d; }
.who { display: flex; align-items: center; gap: .35rem; margin: .35rem 0 0;
  font-size: .78rem; color: #7d848f; }
.who .avatar { width: 16px; height: 16px; }
.rate { font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  color: #9ece6a; white-space: nowrap; }
.rate .u { color: #6b727d; font-size: .78rem; }
/* The prefill beside the decode: the same font, smaller and dimmer via
   opacity — no new colour (TTP-130). */
.rate.sub { font-size: .78rem; opacity: .7; line-height: 1.2; }
/* The context chip: de-emphasised the same way — a reservation, not a
   measured length, so it sits back (TTP-130). */
.fact.dim { opacity: .7; }
.facts { display: flex; flex-wrap: wrap; gap: .4rem; margin: .5rem 0 .35rem; }
.fact { font-size: .78rem; color: #99a0ab; background: #171b22; border: 1px solid #222832;
  border-radius: 999px; padding: .1rem .55rem; }
.fact.on { color: #eef1f5; border-color: #3a4150; background: #1f2630; }
a.fact.link { color: #99a0ab; }
a.fact.link:hover { color: #d7dae0; border-color: #39414f; text-decoration: none; }
a.fact.on:hover { border-color: #e0b64a; text-decoration: none; }
.fact.caveat { color: #e0b64a; background: #1a160c; border-color: #3a2f16; }
a.fact.caveat:hover { color: #e0b64a; text-decoration: none; }
.when { font-size: .78rem; color: #6b727d; }
/* Every row carries its run and the one most in view plays; on a desktop
   the one under the pointer does (host.js). The frame is the row's opening
   image, the heading sits right under it. */
.feed { display: block; margin: 0 0 .7rem; }
/* On a phone the grid is one column of rows with a rule between them, and
   the frame bleeds to the screen's edges (every pixel is a bigger cell) with
   no rule of its own above or below — the row's rule and the heading are
   its edges, so the full-bleed frame and the inset caption read as the feed
   idiom rather than as two margins. */
@media (max-width: 48rem) {
  .rows { display: block; margin-top: 0; }
  .row { padding: 1.1rem 0 1.25rem; border-bottom: 1px solid #1b1f26; }
  .feed { margin: 0 -1.25rem .9rem; }
  .feed .card, .feed .screen { border-top: 0; border-bottom: 0; }
}
.empty { color: #7d848f; padding: 1rem 0 2rem; text-align: center; }
.more { margin: 1.75rem 0 0; }
`;
