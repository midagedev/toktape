// GET /hw/<id> — the hub page for one GPU model (TTP-204).
//
// The front page's listing with the gpu filter fixed to <id>, and a header
// that says what the page is. The point is a link that grows: a post about a
// card links one stable URL, and every tape published on that card lands on
// it. Everything else (the filters, the order, the cursor) is the search's,
// reused, not copied: the WHERE, the facets, the rows and the total come from
// search.js so this page cannot count a different population than `/?gpu=`.
//
// There is no ratio, ranking or comparison across engines in this page's own
// text (§9.4). It names how many tapes there are and which engines and models
// are among them; the figures are the rows', each with its caveats.

import { publicBase } from "./http.js";
import { EMPTY_FIGURE, esc, head, layout } from "./page.js";
import {
  activeFilters,
  chip,
  countTotal,
  distinctFacets,
  emptyState,
  filterForm,
  PAGE_STYLE,
  pageSize,
  query,
  rowGrid,
  totalLine,
  whereFor,
} from "./search.js";

// A product name that stands for one GPU model. An alias exists only where a
// product has exactly one GPU model: "dgx-spark" is one GB10, while a name
// like "dgx-station" or "mac-studio" that spans several is not an alias, it
// is a different page and has to be asked for as one.
export const GPU_ALIASES = { "dgx-spark": "gb10" };

const ASK_FOR_A_TAPE =
  "https://github.com/midagedev/toktape/blob/main/docs/publish.md#asking-someone-else-for-a-tape";

const HUB_STYLE = `
.hubhead { align-items: center; margin: .5rem 0 1.5rem; }
.hubhead .total { margin: 0 .35rem 0 0; font-size: .875rem; }
`;

// How many of the newest tapes are read to learn what the card is called.
const NAME_SAMPLE = 200;

export async function serveHubPage(id, request, env) {
  const url = new URL(request.url);
  if (GPU_ALIASES[id]) {
    return new Response(null, {
      status: 301,
      headers: { Location: `/hw/${GPU_ALIASES[id]}${url.search}`, "Cache-Control": "public, max-age=3600" },
    });
  }
  const base = publicBase(request, env);
  const scope = { name: "public" };

  // Two URLs. `eff` carries the fixed gpu and is what every query reads; a
  // gpu= in the request is overwritten, not honoured. `shown` is the same
  // request without it, which is what every link on the page is built from,
  // so no link grows a redundant `gpu=`.
  const eff = new URL(url.href);
  eff.searchParams.set("gpu", id);
  const shown = new URL(url.href);
  shown.searchParams.delete("gpu");
  // The hub as a whole, whatever else is narrowed: what the title, the
  // preview and the header's chips describe.
  const whole = new URL(url.origin + url.pathname);
  whole.searchParams.set("gpu", id);

  const limit = pageSize(url);
  const q = await query(env, eff, scope, limit);
  if (q.error) return q.error;
  const narrowed = [...shown.searchParams.keys()].some((k) => k !== "cursor");
  const [n, wholeFacets, nameRows] = await Promise.all([
    countTotal(env, whole, scope),
    distinctFacets(env, whole, scope),
    sampleNames(env, whole, scope),
  ]);
  const facets = narrowed ? await distinctFacets(env, eff, scope) : wholeFacets;
  const name = cardName(id, nameRows);

  const title = `${n} tape${n === 1 ? "" : "s"} on ${name} — toktape`;
  const description = describe(wholeFacets);
  const pageURL = `${base}/hw/${id}`;
  const formFacets = { ...facets, gpu: { rows: [], more: false } };

  let mid;
  if (q.rows.length === 0) {
    // No tape on the card at all says so and points at the way to get one;
    // a filter that matches nothing on a card that has tapes is the search's
    // own empty state, with its way back.
    mid =
      n === 0
        ? `${EMPTY_FIGURE}<p class="empty">No tape on this card yet. <a href="${ASK_FOR_A_TAPE}">Ask someone who has one to publish a tape.</a></p>`
        : await emptyState(env, eff, scope, ["gpu"]);
  } else {
    mid = `${await totalLine(env, eff, scope, limit, q.next)}
${rowGrid(q.rows, shown)}`;
  }
  const more = (() => {
    if (!q.next) return "";
    const u = new URL(shown.href);
    u.searchParams.set("cursor", q.next);
    return `<p class="more"><a href="${esc(u.pathname + u.search)}">Older runs →</a></p>`;
  })();

  const chips = [
    ...wholeFacets.engine.rows.map((r) => ({ shown: r.v, suffix: String(r.n), param: "engine", value: r.v })),
    ...wholeFacets.model.rows.map((r) => ({ shown: r.v, suffix: String(r.n), param: "model", value: r.v })),
  ]
    .map((f) => chip(f, shown))
    .join("");

  return new Response(
    layout({
      title,
      meta: head({ title, description, url: pageURL }),
      style: PAGE_STYLE + HUB_STYLE,
      figure: "peek",
      body: `
<h1>${esc(name)}</h1>
<div class="facts hubhead"><span class="total"><b>${n}</b> tape${n === 1 ? "" : "s"}</span>${chips}</div>
${filterForm(shown, formFacets)}
${activeFilters(shown, `/hw/${id}`)}
${mid}
${more}
<footer>
<a href="/">Published runs</a> · every tape on this page was recorded on
this card and carries its own caveats; the page does not rank them.
<code>toktape publish &lt;run.toktape&gt;</code> puts one here.
</footer>
<script src="/player/wasm_exec.js"></script>
<script src="/player/host.js"></script>`,
    }),
    { headers: { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" } },
  );
}

// The og:description: the engines present by count, then the models, as far
// as a preview shows (a description past ~150 characters is truncated by the
// readers that matter). The sentence after them is fixed.
function describe(facets) {
  const tail = "Every run replays in the browser.";
  const parts = [...facets.engine.rows, ...facets.model.rows].map((r) => `${r.v} ${r.n}`);
  if (!parts.length) return `No tape yet. ${tail}`;
  let list = "";
  for (const p of parts) {
    const next = list ? `${list} · ${p}` : p;
    if (next.length + tail.length + 2 > 150) break;
    list = next;
  }
  return `${list}. ${tail}`;
}

// The newest tapes' names for the card, for cardName. Public tapes only and
// the same membership filter as the listing, nothing else narrowing it, so a
// filter on the page never renames the card.
async function sampleNames(env, whole, scope) {
  const { where, args } = whereFor(whole, scope, null);
  const { results } = await env.DB.prepare(
    `SELECT gpus_raw, gpu_ids FROM runs WHERE ${where.join(" AND ")} ORDER BY created_at DESC LIMIT ?`,
  )
    .bind(...args, NAME_SAMPLE)
    .all();
  return results || [];
}

// The card as the tapes call it: the most common gpus_raw member whose id is
// this one. The Worker never normalises a name (§9.6), so a member is tied to
// its id by position, which holds only when both lists have the same length
// (gpu_ids drops empty ids); a row where they differ says nothing here. A
// card seen once per tape, however many of it a rig holds, so an eight-card
// rig does not outvote eight single-card tapes. With no usable row the id is
// all there is.
function cardName(id, rows) {
  const seen = new Map();
  for (const r of rows) {
    const names = String(r.gpus_raw || "").split(" / ");
    let ids;
    try {
      ids = JSON.parse(r.gpu_ids || "[]");
    } catch {
      continue;
    }
    if (!Array.isArray(ids) || ids.length !== names.length) continue;
    for (const nm of new Set(names.filter((_, i) => ids[i] === id))) {
      seen.set(nm, (seen.get(nm) || 0) + 1);
    }
  }
  let best = id;
  let top = 0;
  for (const [nm, c] of seen) {
    if (c > top || (c === top && nm < best)) {
      best = nm;
      top = c;
    }
  }
  return best;
}
