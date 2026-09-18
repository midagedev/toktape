// GET /r/<id> — one published run, and /r/<id>.toktape — the record itself.
//
// The page is built from the index row the client uploaded, which is the
// only thing this side is allowed to read. The card and the replay are not
// here yet: the card is a PNG the client will upload beside the tape, and
// the replay is wasm (TTP-116). Until then the page is the figures and the
// download, and it says so rather than showing an empty frame.
//
// A private run is served here. Unlisted means out of the search, not behind
// a door — the id is the secret (§9.3).

import { fail, json } from "./http.js";
import { esc, fmt, layout } from "./page.js";

export async function serveTape(id, env) {
  const row = await env.DB.prepare("SELECT tape_key, tape_ext FROM runs WHERE id = ?")
    .bind(id)
    .first();
  if (!row) return fail(404, "no run with that id");

  const obj = await env.TAPES.get(row.tape_key);
  if (!obj) {
    // The row survived its record. Saying so is better than a 404, which
    // would tell the reader the run never existed.
    return fail(500, "that run's record is missing from storage");
  }
  return new Response(obj.body, {
    headers: {
      "Content-Type": "application/gzip",
      // R2's own size, not the one D1 recorded: the bytes being sent are
      // the object's, and a length copied from a second store is a claim
      // about them rather than a measurement of them.
      "Content-Length": String(obj.size),
      // The id, not the name it was uploaded under: two runs from one
      // machine are both `run.toktape` and would overwrite each other in a
      // downloads folder.
      "Content-Disposition": `attachment; filename="${id}${row.tape_ext}"`,
      "Cache-Control": "public, max-age=31536000, immutable",
    },
  });
}

// GET /r/<id>.png — the share card, unauthenticated and with the right
// content type, or Reddit and X render no preview at all. An unlisted run's
// card is served too: the id is the secret, and a preview that 404'd on a
// link somebody deliberately shared would break the sharing the link is for.
export async function serveCard(id, env) {
  const row = await env.DB.prepare("SELECT card_key FROM runs WHERE id = ?").bind(id).first();
  if (!row) return fail(404, "no run with that id");
  if (!row.card_key) {
    // Published by a client that did not send one. Saying so beats an image
    // this side would have to invent, which it cannot: drawing the card
    // means reading the tape.
    return fail(404, "that run was published without a card");
  }
  const obj = await env.TAPES.get(row.card_key);
  if (!obj) return fail(500, "that run's card is missing from storage");
  return new Response(obj.body, {
    headers: {
      "Content-Type": "image/png",
      "Content-Length": String(obj.size),
      // A run's card never changes: it was rendered once, before the upload.
      "Cache-Control": "public, max-age=31536000, immutable",
    },
  });
}

export async function serveRunJSON(id, env) {
  const row = await env.DB.prepare("SELECT id, created_at, private, index_json, tape_ext, tape_bytes, card_key FROM runs WHERE id = ?")
    .bind(id)
    .first();
  if (!row) return fail(404, "no run with that id");
  return json({
    id: row.id,
    published_at: row.created_at,
    private: row.private === 1,
    tape: `/r/${row.id}${row.tape_ext}`,
    tape_bytes: row.tape_bytes,
    card: row.card_key ? `/r/${row.id}.png` : null,
    index: JSON.parse(row.index_json),
  });
}

export async function serveRunPage(id, env, base) {
  const row = await env.DB.prepare("SELECT id, created_at, private, index_json, tape_ext, tape_bytes, card_key FROM runs WHERE id = ?")
    .bind(id)
    .first();
  if (!row) {
    return new Response(notFoundPage(), {
      status: 404,
      headers: { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" },
    });
  }
  const idx = JSON.parse(row.index_json);
  return new Response(runPage(row, idx, base), {
    headers: {
      "Content-Type": "text/html; charset=utf-8",
      // A published run does not change, but it can be deleted, so this is
      // short rather than immutable.
      "Cache-Control": "public, max-age=60",
      // An unlisted run is still a public URL, but asking the crawlers not
      // to keep it costs nothing and is what "out of the search" meant.
      ...(row.private === 1 ? { "X-Robots-Tag": "noindex" } : {}),
    },
  });
}

function runPage(row, idx, base) {
  const title = idx.model_id || idx.model_raw || "a toktape run";
  const rows = [
    ["model", idx.model_raw || "?"],
    ["model id", withSource(idx.model_id, idx.model_id_source)],
    ["repo", idx.repo || "—"],
    ["quantisation", idx.quant_raw ? `${idx.quant_raw}${idx.quant_bits ? ` · ${idx.quant_bits} bit` : ""}` : "?"],
    ["engine", [idx.engine_kind, idx.engine_version].filter(Boolean).join(" ") || "?"],
    ["host", [idx.os, idx.host_class].filter(Boolean).join(" · ") || "?"],
    ["gpus", idx.gpus_raw && idx.gpus_raw.length ? idx.gpus_raw.join(" / ") : "—"],
    ["streams", idx.sessions ? String(idx.sessions) : "?"],
    ["prompt set", idx.prompt_set || "not in a comparison set"],
    // The offset is left on, deliberately: whether recorded_at should be
    // normalised to UTC is still open (TTP-120), and hiding the offset while
    // the question is open would answer it by accident. Only the fractional
    // seconds go, which no reader of this row wanted.
    ["recorded", idx.recorded_at ? String(idx.recorded_at).replace(/\.\d+/, "") : "?"],
    ["toktape", idx.toktape_version || "?"],
  ];
  const figures = [
    ["decode", idx.decode_per_sec, "tok/s"],
    ["prefill", idx.prefill_per_sec, "tok/s"],
    ["ttft p50", idx.ttft_p50_ms, "ms"],
  ];

  return layout({
    title: `${title} — toktape`,
    // og:image is absolute, because a crawler does not resolve a relative
    // one — and it is emitted only when there really is a card, so a link
    // never promises a preview that 404s.
    meta: `<meta property="og:title" content="${esc(title)}">
<meta property="og:description" content="${esc(summaryLine(idx))}">
<meta property="og:type" content="website">
<meta property="og:url" content="${esc(base)}/r/${esc(row.id)}">${
      row.card_key
        ? `
<meta property="og:image" content="${esc(base)}/r/${esc(row.id)}.png">
<meta property="og:image:width" content="1200">
<meta property="og:image:height" content="675">
<meta name="twitter:card" content="summary_large_image">`
        : ""
    }`,
    style: PAGE_STYLE,
    body: `${
      row.card_key
        ? `<img class="card" src="/r/${esc(row.id)}.png" width="1200" height="675"
     alt="The toktape card for this run: ${esc(summaryLine(idx))}">`
        : ""
    }
<h1>${esc(title)}</h1>
<p class="sub">${esc(summaryLine(idx))}${row.private === 1 ? " · unlisted" : ""}</p>

<div class="figures">
${figures
  .map(
    ([k, v, u]) => `<div class="figure"><div class="k">${esc(k)}</div><div class="n num">${fmt(
      v,
    )}<span class="u">${esc(u)}</span></div></div>`,
  )
  .join("\n")}
</div>

<table>
${rows.map(([k, v]) => `<tr><td class="k">${esc(k)}</td><td class="v mono">${esc(v)}</td></tr>`).join("\n")}
</table>

${caveatBlock(idx)}

<footer>
<a href="/r/${esc(row.id)}${esc(row.tape_ext)}">Download the record</a> · ${row.tape_bytes} bytes ·
published ${esc(row.created_at)}<br>
The record is the original: everything on this page was derived from it before
it was uploaded, and <code>toktape card ${esc(row.id)}${esc(row.tape_ext)}</code>
draws the card from the same file.
</footer>`,
  });
}

const PAGE_STYLE = `
/* The card is the page's first sentence: it is what the link previews as,
   and it settles the argument before any of the table is read. */
.card { width: 100%; height: auto; display: block; border-radius: 8px;
  border: 1px solid #1b1f26; margin: 0 0 2rem; }
.figures { display: flex; flex-wrap: wrap; gap: 2.5rem; margin: 0 0 2.5rem; }
.figure .n { font: 600 1.9rem/1.1 ui-monospace, SFMono-Regular, Menlo, monospace;
  color: #eef1f5; }
.figure .u { color: #6b727d; font-size: .8rem; margin-left: .3rem; }
.figure .k { color: #6b727d; font-size: .72rem; text-transform: uppercase;
  letter-spacing: .09em; margin-bottom: .35rem; }
table { border-collapse: collapse; width: 100%; font-size: .9rem; }
td { padding: .45rem 0; border-bottom: 1px solid #1b1f26; vertical-align: top; }
td.k { color: #7d848f; width: 9.5rem; white-space: nowrap; }
@media (max-width: 30rem) {
  td.k { width: 6.5rem; white-space: normal; }
  .figures { gap: 1.5rem; }
}
td.v { word-break: break-word; }
.caveats { margin: 2rem 0 0; padding: .9rem 1.1rem; border: 1px solid #3a2f16;
  background: #1a160c; border-radius: 6px; font-size: .875rem; }
.caveats b { color: #e0b64a; font-weight: 600; }
.caveats code { color: #b9a06a; }
`;

function caveatBlock(idx) {
  if (!idx.caveat_count) return "";
  const codes = (idx.caveats || []).map((c) => `<code>${esc(c)}</code>`).join(", ");
  // The codes, not sentences. The sentences belong to the card, which is the
  // one place that writes them (internal/card/caveat.go); a second wording
  // here would be a second answer to what a caveat means.
  return `<div class="caveats"><b>! ${idx.caveat_count} caveat${
    idx.caveat_count === 1 ? "" : "s"
  }</b> — ${codes}. The card that comes with this record spells each one out.</div>`;
}

function summaryLine(idx) {
  const parts = [];
  if (idx.decode_per_sec) parts.push(`${fmt(idx.decode_per_sec)} tok/s decode`);
  if (idx.sessions) parts.push(`${idx.sessions} stream${idx.sessions === 1 ? "" : "s"}`);
  if (idx.quant_raw) parts.push(idx.quant_raw);
  if (idx.gpu_id) parts.push(idx.gpu_count > 1 ? `${idx.gpu_count}× ${idx.gpu_id}` : idx.gpu_id);
  return parts.join(" · ") || "a recorded run";
}

function withSource(id, source) {
  if (!id) return "—";
  return source ? `${id} (from the ${source})` : id;
}

function notFoundPage() {
  return layout({
    title: "Not found — toktape",
    body: `<h1>No run with that id</h1>
<p class="sub">Either the link is wrong, or the run was deleted. A deleted run
is gone: the delete token is the only key to a one-off upload and it takes the
record with it.</p>
<p><a href="/">Published runs</a></p>`,
  });
}
