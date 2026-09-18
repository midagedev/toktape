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

// One gigabyte, because the min_vram box offers gigabytes and the column
// holds bytes (row.js: vram_bytes is bytes straight from the client).
const GB = 1024 ** 3;

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

// The VRAM tiers the box offers, in GB. A threshold, not a category, so it
// has no facet counts — just these fixed options.
const VRAM_TIERS = [8, 12, 16, 24, 48, 80];

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

  const limit = pageSize(url);
  const q = await query(env, url, scope, limit);
  const facets = await distinctFacets(env, url, scope);
  const active = activeFilters(url);
  // With 0 rows the empty state speaks instead of the total: a bare
  // "0 runs" is a dead end, and the way back (§12 in the task spec) is the
  // useful thing on that path.
  const mid =
    q.rows.length === 0
      ? await emptyState(env, url, scope)
      : `${await totalLine(env, url, scope, limit, q.next)}
${q.rows.map((r) => resultRow(r, url)).join("\n")}`;

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
${active}
${mid}
${q.next ? `<p class="more"><a href="${esc(withParam(url, "cursor", q.next))}">Older runs →</a></p>` : ""}
<footer>
Newest first, and that is the only order there is — no rank column, by
decision. Every row carries the caveats the card would print, because a
result set without them is a leaderboard with the sorting taken out.<br>
<code>toktape publish &lt;run.tape&gt;</code> puts one here.
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
function whereFor(url, scope, skipParam) {
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
    if (param === skipParam) continue;
    const v = url.searchParams.get(param);
    if (v) {
      where.push(`${column} = ?`);
      args.push(column === "sessions" ? Number(v) : v);
    }
  }
  if (skipParam !== "min_vram" && url.searchParams.get("min_vram")) {
    where.push("vram_bytes >= ?");
    args.push(Number(url.searchParams.get("min_vram")) * GB);
  }
  // Free text falls back across the raw strings, which is the whole reason
  // they are stored: a run whose model never normalised is still findable by
  // the file name that was observed (§9.4, rule 2).
  const q = (url.searchParams.get("q") || "").trim();
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

async function query(env, url, scope, limit) {
  const { where, args } = whereFor(url, scope, null);
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

// One COUNT over the same WHERE: a population size for the page, not a
// ranking of anything.
async function countTotal(env, url, scope) {
  const { where, args } = whereFor(url, scope, null);
  const row = await env.DB.prepare(
    `SELECT COUNT(*) AS n FROM runs WHERE ${where.join(" AND ")}`,
  )
    .bind(...args)
    .first();
  return row.n;
}

async function totalLine(env, url, scope, limit, hasNext) {
  const n = await countTotal(env, url, scope);
  return `<p class="total"><b>${n}</b> run${n === 1 ? "" : "s"}, newest first${hasNext ? ` · showing the first ${limit}` : ""}</p>`;
}

async function distinctFacets(env, url, scope) {
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
  const [engine, quant, gpu, host] = await Promise.all([
    one("engine", "engine_kind"),
    one("quant", "quant_id"),
    one("gpu", "gpu_id"),
    one("host", "host_class"),
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

function resultRow(r, url) {
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
    {
      shown: r.gpu_id ? (r.gpu_count > 1 ? `${r.gpu_count}× ${r.gpu_id}` : r.gpu_id) : r.gpus_raw,
      param: r.gpu_id ? "gpu" : null,
      value: r.gpu_id,
    },
    { shown: r.os, param: "os", value: r.os },
    {
      shown: r.sessions ? `${r.sessions} stream${r.sessions === 1 ? "" : "s"}` : null,
      param: "sessions",
      value: r.sessions ? String(r.sessions) : null,
    },
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
  <div class="facts">${facts.filter((f) => f.shown).map((f) => chip(f, url)).join("")}${caveatChip(r)}</div>
  <div class="when">${esc(String(r.created_at).slice(0, 10))}${r.repo ? ` · ${esc(r.repo)}` : ""}</div>
</article>`;
}

// A fact that is also a filter is a link to that filter, so narrowing a
// search is reading rather than a form to fill in. The link copies the
// current URL's params and sets just this one, so chips accumulate instead
// of replacing each other; a chip whose filter is already active renders as
// plain text, so the active narrowing is visible in the rows.
function chip(f, url) {
  const inner = esc(f.shown) + (f.suffix ? `<span class="v">${esc(f.suffix)}</span>` : "");
  if (!f.param || !f.value) return `<span class="fact">${inner}</span>`;
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
  return out;
}

function filterLabel(param, url) {
  if (param === "q") return `\u201c${(url.searchParams.get("q") || "").trim()}\u201d`;
  if (param === "min_vram") return `vram: ${url.searchParams.get("min_vram")} GB+`;
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

function activeFilters(url) {
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

function filterForm(url, facets) {
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
  return `<form class="filters" method="get" action="/">
  <input type="search" name="q" value="${esc(url.searchParams.get("q") || "")}"
    placeholder="model, repo, GPU, engine…" autocomplete="off">
  <kbd title="press / to search">/</kbd>
  ${sel("engine", "any engine", facets.engine)}
  ${sel("quant", "any quantisation", facets.quant)}
  ${sel("gpu", "any GPU", facets.gpu)}
  ${sel("host", "any host", facets.host)}
  ${vram}
  <button type="submit">Search</button>
</form>`;
}

async function emptyState(env, url, scope) {
  const filtered = [...url.searchParams.keys()].some((k) => k !== "cursor");
  if (!filtered) {
    return `<p class="empty">No runs published yet. <code>toktape publish &lt;run.tape&gt;</code> puts the first one here.</p>`;
  }
  const back = (await wayBack(env, url, scope)).join(" · ");
  return `<p class="empty">No published run matches that. The filters are exact; the search box falls back to the names as they were recorded.${back ? `<br>Try ${back}.` : ""}</p>`;
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

const PAGE_STYLE = `
.filters { display: flex; flex-wrap: wrap; gap: .5rem; margin: 0 0 2rem; }
.filters input, .filters select, .filters button {
  background: #14171d; color: #d7dae0; border: 1px solid #242932; border-radius: 6px;
  padding: .45rem .6rem; font: inherit; font-size: .875rem; }
.filters kbd { align-self: center; }
/* The box gets its own row so the selects and the button share the next one
   rather than leaving one of them stranded below on a narrow window. */
.filters input { flex: 1 1 100%; min-width: 0; }
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
.row { padding: 1rem 0; border-bottom: 1px solid #1b1f26; }
.rowhead { display: flex; align-items: baseline; gap: 1rem; justify-content: space-between; }
.name { color: #eef1f5; font-weight: 600; font-size: 1rem; word-break: break-word; }
.rate { font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  color: #9ece6a; white-space: nowrap; }
.rate .u { color: #6b727d; font-size: .78rem; }
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
