// The search (docs/toktape-spec.ko.md §9.4).
//
// **It is not a leaderboard, and the code says so in one place:** there is no
// sort parameter. Newest first is the only order, here and in the API, and a
// caller cannot ask for anything else. Adding one would take five minutes and
// would turn every published run into an entry — which is the one thing the
// user ruled out twice ("리더보드 만들고 싶은건 아냐 다만 사양과 모델 등으로
// 검색해 볼 수 있게는 하고 싶어", 2026-09-18).
//
// The second half of the same decision: every row carries its caveat count.
// The card qualifies its own numbers with `! 3 caveats`, and a result set
// that drops the qualification is a leaderboard with the sorting removed.
//
// Two scopes: everything public, and — with a journal token — mine, which is
// the outward half of the local ledger (§9.2).

import { fail, json, publicBase } from "./http.js";
import { esc, fmt, head, layout } from "./page.js";
import { sha256Hex } from "./ids.js";

const PAGE_SIZE = 30;
const MAX_PAGE_SIZE = 100;

// The columns a result row is drawn from. index_json is not read here: these
// are the axes the schema flattened for exactly this query, and going back to
// the JSON would be the search deciding it knows better than its own index.
const SELECT = `SELECT id, created_at, recorded_at, model_id, model_raw, repo,
    quant_id, quant_raw, engine_kind, engine_version, os, gpu_id, gpus_raw,
    gpu_count, vram_bytes, host_class, sessions, prompt_set, decode_per_sec,
    caveat_count, tape_ext, card_key
  FROM runs`;

// The filters §9.4 names, each mapped to the column it narrows. A raw column
// is listed beside its normalised one so a run whose normalisation came back
// empty is still reachable by what was observed.
const FILTERS = [
  ["model", "model_id"],
  ["model_raw", "model_raw"],
  ["repo", "repo"],
  ["quant", "quant_id"],
  ["engine", "engine_kind"],
  ["gpu", "gpu_id"],
  ["host", "host_class"],
  ["os", "os"],
  ["set", "prompt_set"],
  ["sessions", "sessions"],
];

export async function searchAPI(request, env) {
  const url = new URL(request.url);
  const scope = await scopeOf(request, env, url);
  if (scope.error) return scope.error;

  const q = await query(env, url, scope, pageSize(url));
  return json({
    scope: scope.name,
    runs: q.rows.map(apiRow),
    // A cursor rather than a page number: rows arrive newest first and new
    // ones land at the front, so an offset would show the same run twice
    // while somebody is reading.
    next: q.next,
  });
}

export async function searchPage(request, env) {
  const url = new URL(request.url);
  const scope = await scopeOf(request, env, url);
  if (scope.error) return scope.error;

  const q = await query(env, url, scope, pageSize(url));
  const facets = await distinctFacets(env, scope);

  return new Response(
    layout({
      title: "toktape — published runs",
      // The front page is a search, and a filtered search is the same page:
      // the canonical is the bare front page so a crawler indexes it once,
      // and the run pages carry the model names into the index themselves.
      meta: head({
        title: "toktape — published LLM inference runs",
        description:
          "Recorded llama.cpp and vLLM runs with their tok/s, the card that qualifies each figure, and a replay in the browser. Search by model, quant, GPU and engine.",
        url: `${publicBase(request, env)}/`,
      }),
      style: PAGE_STYLE,
      body: `
${filterForm(url, facets)}
${q.rows.length === 0 ? emptyState(url) : q.rows.map(resultRow).join("\n")}
${q.next ? `<p class="more"><a href="${esc(withParam(url, "cursor", q.next))}">Older runs →</a></p>` : ""}
<footer>
Newest first, and that is the only order there is — no rank column, by
decision. Every row carries the caveats the card would print, because a
result set without them is a leaderboard with the sorting taken out.<br>
<code>toktape publish &lt;run.tape&gt;</code> puts one here.
<a href="https://github.com/midagedev/toktape">toktape on GitHub</a>
</footer>
<script src="/player/wasm_exec.js"></script>
<script src="/player/host.js"></script>`,
    }),
    { headers: { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" } },
  );
}

// ---------------------------------------------------------------- querying

async function query(env, url, scope, limit) {
  const where = [];
  const args = [];

  if (scope.name === "mine") {
    where.push("owner_token = ?");
    args.push(scope.owner);
  } else {
    // Unlisted is unlisted in every scope but its owner's.
    where.push("private = 0");
  }

  for (const [param, column] of FILTERS) {
    const v = url.searchParams.get(param);
    if (v) {
      where.push(`${column} = ?`);
      args.push(column === "sessions" ? Number(v) : v);
    }
  }
  if (url.searchParams.get("min_vram")) {
    where.push("vram_bytes >= ?");
    args.push(Number(url.searchParams.get("min_vram")));
  }
  // Free text falls back across the raw strings, which is the whole reason
  // they are stored: a run whose model never normalised is still findable by
  // the file name that was observed (§9.4, rule 2).
  const q = (url.searchParams.get("q") || "").trim();
  if (q) {
    const cols = ["model_raw", "model_id", "repo", "gpus_raw", "engine_kind", "quant_raw"];
    // One placeholder per column rather than a numbered one reused. SQLite
    // numbers a bare `?` as one past the highest index seen so far, so a
    // single `?1` in the middle of positional placeholders silently makes
    // the parameters after it collide with the ones before.
    where.push("(" + cols.map((c) => `${c} LIKE ?`).join(" OR ") + ")");
    const like = `%${q.replace(/[%_]/g, "")}%`;
    for (const _ of cols) args.push(like);
  }
  const cursor = decodeCursor(url.searchParams.get("cursor"));
  if (cursor) {
    where.push("(created_at < ? OR (created_at = ? AND id < ?))");
    args.push(cursor.created_at, cursor.created_at, cursor.id);
  }

  // created_at, never recorded_at: ordering on a field out of the upload
  // would make the listing trust the uploader's clock, and its UTC offset is
  // still an open question (TTP-120).
  const sql = `${SELECT}
    ${where.length ? "WHERE " + where.join(" AND ") : ""}
    ORDER BY created_at DESC, id DESC
    LIMIT ?`;

  // One extra row, only to learn whether there is a next page.
  const { results } = await env.DB.prepare(sql).bind(...args, limit + 1).all();
  const rows = results.slice(0, limit);
  const last = rows[rows.length - 1];
  return {
    rows,
    next: results.length > limit && last ? encodeCursor(last) : null,
  };
}

async function distinctFacets(env, scope) {
  const scopeWhere = scope.name === "mine" ? "owner_token = ?" : "private = 0";
  const arg = scope.name === "mine" ? [scope.owner] : [];
  const one = async (column) => {
    const { results } = await env.DB.prepare(
      `SELECT ${column} AS v, COUNT(*) AS n FROM runs
       WHERE ${scopeWhere} AND ${column} IS NOT NULL
       GROUP BY ${column} ORDER BY n DESC LIMIT 20`,
    )
      .bind(...arg)
      .all();
    return results;
  };
  const [engine, quant, gpu, host] = await Promise.all([
    one("engine_kind"),
    one("quant_id"),
    one("gpu_id"),
    one("host_class"),
  ]);
  return { engine, quant, gpu, host };
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

function pageSize(url) {
  const n = Number(url.searchParams.get("limit"));
  if (!Number.isFinite(n) || n <= 0) return PAGE_SIZE;
  return Math.min(Math.floor(n), MAX_PAGE_SIZE);
}

function encodeCursor(row) {
  return btoa(`${row.created_at}|${row.id}`).replace(/=+$/, "");
}

function decodeCursor(s) {
  if (!s) return null;
  try {
    const [created_at, id] = atob(s).split("|");
    if (!created_at || !id) return null;
    return { created_at, id };
  } catch {
    return null;
  }
}

// ------------------------------------------------------------------ shapes

function apiRow(r) {
  return {
    id: r.id,
    url: `/r/${r.id}`,
    published_at: r.created_at,
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
    vram_bytes: r.vram_bytes,
    host_class: r.host_class,
    sessions: r.sessions,
    prompt_set: r.prompt_set,
    decode_per_sec: r.decode_per_sec,
    caveat_count: r.caveat_count,
  };
}

function resultRow(r) {
  // Each fact is {shown, param, value}: what a reader sees and, where the
  // axis is one the index normalised, the exact value that narrows to it.
  // The two are never derived from each other here — turning `UD-Q6_K` into
  // `q6_k` on this side would be a second normaliser, and the client already
  // owns that one (§9.4, rule 1).
  const facts = [
    { shown: r.engine_kind, param: "engine", value: r.engine_kind },
    { shown: r.quant_raw || r.quant_id, param: r.quant_id ? "quant" : null, value: r.quant_id },
    {
      shown: r.gpu_id ? (r.gpu_count > 1 ? `${r.gpu_count}× ${r.gpu_id}` : r.gpu_id) : r.gpus_raw,
      param: r.gpu_id ? "gpu" : null,
      value: r.gpu_id,
    },
    { shown: r.sessions ? `${r.sessions} stream${r.sessions === 1 ? "" : "s"}` : null },
    { shown: r.prompt_set, param: "set", value: r.prompt_set },
  ];
  return `<article class="row">
  <div class="rowhead">
    <a class="name" href="/r/${esc(r.id)}">${esc(r.model_id || r.model_raw || "a run")}</a>
    <span class="rate num">${fmt(r.decode_per_sec)}<span class="u"> tok/s</span></span>
  </div>
  <a class="stage feed" href="/r/${esc(r.id)}" data-tape="/r/${esc(r.id)}${esc(r.tape_ext || ".tape")}"
     aria-label="open this run">${
       r.card_key
         ? `<img class="card" src="/r/${esc(r.id)}.png" width="1200" height="675" loading="lazy" alt="">`
         : `<div class="card nocard"></div>`
     }<pre class="screen"></pre></a>
  <div class="facts">${facts.filter((f) => f.shown).map(chip).join("")}${caveatChip(r)}</div>
  <div class="when">${esc(String(r.created_at).slice(0, 10))}${r.repo ? ` · ${esc(r.repo)}` : ""}</div>
</article>`;
}

// A fact that is also a filter is a link to that filter, so narrowing a
// search is reading rather than a form to fill in.
function chip(f) {
  if (!f.param || !f.value) return `<span class="fact">${esc(f.shown)}</span>`;
  return `<a class="fact link" href="/?${f.param}=${encodeURIComponent(f.value)}">${esc(f.shown)}</a>`;
}

function caveatChip(r) {
  if (!r.caveat_count) return "";
  return `<span class="fact caveat">! ${r.caveat_count} caveat${r.caveat_count === 1 ? "" : "s"}</span>`;
}

function filterForm(url, facets) {
  const sel = (name, label, rows) => {
    const current = url.searchParams.get(name) || "";
    // A filter that is in the URL is in the box, even when nothing matches
    // it: a <select> whose value is not among its options shows the first
    // one, so `?engine=vllm` over a listing with no vllm runs looked like
    // "any engine" with an empty result and no visible reason (2026-09-18,
    // seen on the page). The count says 0, which is the reason.
    if (current && !rows.some((o) => o.v === current)) rows = [{ v: current, n: 0 }, ...rows];
    if (!rows.length) return "";
    return `<select name="${name}"><option value="">${esc(label)}</option>${rows
      .map(
        (o) =>
          `<option value="${esc(o.v)}"${o.v === current ? " selected" : ""}>${esc(o.v)} (${o.n})</option>`,
      )
      .join("")}</select>`;
  };
  return `<form class="filters" method="get" action="/">
  <input type="search" name="q" value="${esc(url.searchParams.get("q") || "")}"
    placeholder="model, repo, GPU, engine…" autocomplete="off">
  ${sel("engine", "any engine", facets.engine)}
  ${sel("quant", "any quantisation", facets.quant)}
  ${sel("gpu", "any GPU", facets.gpu)}
  ${sel("host", "any host", facets.host)}
  <button type="submit">Search</button>
</form>`;
}

function emptyState(url) {
  const filtered = [...url.searchParams.keys()].some((k) => k !== "cursor");
  return `<p class="empty">${
    filtered
      ? "No published run matches that. The filters are exact; the search box falls back to the names as they were recorded."
      : "No runs published yet. <code>toktape publish &lt;run.tape&gt;</code> puts the first one here."
  }</p>`;
}

function withParam(url, key, value) {
  const u = new URL(url);
  u.searchParams.set(key, value);
  return u.pathname + u.search;
}

const PAGE_STYLE = `
.filters { display: flex; flex-wrap: wrap; gap: .5rem; margin: 0 0 2rem; }
.filters input, .filters select, .filters button {
  background: #14171d; color: #d7dae0; border: 1px solid #242932; border-radius: 6px;
  padding: .45rem .6rem; font: inherit; font-size: .875rem; }
/* The box gets its own row so the selects and the button share the next one
   rather than leaving one of them stranded below on a narrow window. */
.filters input { flex: 1 1 100%; min-width: 0; }
.filters select { flex: 0 1 auto; min-width: 0; }
.filters button { background: #1f2630; border-color: #2c3542; cursor: pointer; }
.filters button:hover { background: #262f3b; }
.row { padding: 1rem 0; border-bottom: 1px solid #1b1f26; }
.rowhead { display: flex; align-items: baseline; gap: 1rem; justify-content: space-between; }
.name { color: #eef1f5; font-weight: 600; font-size: 1rem; word-break: break-word; }
.rate { font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  color: #9ece6a; white-space: nowrap; }
.rate .u { color: #6b727d; font-size: .78rem; }
.facts { display: flex; flex-wrap: wrap; gap: .4rem; margin: .5rem 0 .35rem; }
.fact { font-size: .78rem; color: #99a0ab; background: #171b22; border: 1px solid #222832;
  border-radius: 999px; padding: .1rem .55rem; }
a.fact.link { color: #99a0ab; }
a.fact.link:hover { color: #d7dae0; border-color: #39414f; text-decoration: none; }
.fact.caveat { color: #e0b64a; background: #1a160c; border-color: #3a2f16; }
.when { font-size: .78rem; color: #6b727d; }
/* On a phone the listing is a feed: each row carries its run, and the one
   in view plays. On a wide screen it stays a list — twenty stages down a
   desktop page is a wall, and the card is a click away. */
.feed { display: none; }
@media (max-width: 48rem) {
  .feed { display: block; margin-block: .6rem .5rem; }
  .row { padding: 1.25rem 0; }
}
.empty { color: #7d848f; padding: 2rem 0; }
.more { margin: 1.75rem 0 0; }
`;
