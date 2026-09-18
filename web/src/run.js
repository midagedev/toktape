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

export async function serveRunJSON(id, env) {
  const row = await env.DB.prepare("SELECT id, created_at, private, index_json, tape_ext, tape_bytes FROM runs WHERE id = ?")
    .bind(id)
    .first();
  if (!row) return fail(404, "no run with that id");
  return json({
    id: row.id,
    published_at: row.created_at,
    private: row.private === 1,
    tape: `/r/${row.id}${row.tape_ext}`,
    tape_bytes: row.tape_bytes,
    index: JSON.parse(row.index_json),
  });
}

export async function serveRunPage(id, env) {
  const row = await env.DB.prepare("SELECT id, created_at, private, index_json, tape_ext, tape_bytes FROM runs WHERE id = ?")
    .bind(id)
    .first();
  if (!row) {
    return new Response(notFoundPage(), {
      status: 404,
      headers: { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" },
    });
  }
  const idx = JSON.parse(row.index_json);
  return new Response(runPage(row, idx), {
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

function runPage(row, idx) {
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
    ["recorded", idx.recorded_at || "?"],
    ["toktape", idx.toktape_version || "?"],
  ];
  const figures = [
    ["decode", idx.decode_per_sec, "tok/s"],
    ["prefill", idx.prefill_per_sec, "tok/s"],
    ["ttft p50", idx.ttft_p50_ms, "ms"],
  ];

  return `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${esc(title)} — toktape</title>
<meta property="og:title" content="${esc(title)}">
<meta property="og:description" content="${esc(summaryLine(idx))}">
<meta property="og:type" content="website">
<style>
:root { color-scheme: dark; }
body { margin: 0; background: #0e1014; color: #d7dae0;
  font: 15px/1.6 ui-sans-serif, -apple-system, "Segoe UI", sans-serif; }
main { max-width: 46rem; margin: 0 auto; padding: 3rem 1.25rem 5rem; }
h1 { font-size: 1.4rem; margin: 0 0 .25rem; font-weight: 600; letter-spacing: -.01em; }
.sub { color: #7d848f; font-size: .875rem; margin: 0 0 2.5rem; }
.figures { display: flex; flex-wrap: wrap; gap: 2.5rem; margin: 0 0 2.5rem; }
.figure .n { font: 600 1.9rem/1.1 ui-monospace, SFMono-Regular, Menlo, monospace;
  color: #eef1f5; font-variant-numeric: tabular-nums; }
.figure .u { color: #7d848f; font-size: .8rem; margin-left: .3rem; }
.figure .k { color: #7d848f; font-size: .75rem; text-transform: uppercase;
  letter-spacing: .08em; margin-bottom: .35rem; }
table { border-collapse: collapse; width: 100%; font-size: .9rem; }
td { padding: .45rem 0; border-bottom: 1px solid #1b1f26; vertical-align: top; }
td.k { color: #7d848f; width: 9.5rem; white-space: nowrap; }
td.v { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; word-break: break-word; }
.caveats { margin: 2rem 0 0; padding: .9rem 1.1rem; border: 1px solid #3a2f16;
  background: #1a160c; border-radius: 6px; font-size: .875rem; }
.caveats b { color: #e0b64a; font-weight: 600; }
.caveats code { color: #b9a06a; }
footer { margin-top: 3rem; padding-top: 1.5rem; border-top: 1px solid #1b1f26;
  font-size: .85rem; color: #7d848f; }
a { color: #7aa2f7; }
</style>
</head><body><main>
<h1>${esc(title)}</h1>
<p class="sub">${esc(summaryLine(idx))}${row.private === 1 ? " · unlisted" : ""}</p>

<div class="figures">
${figures
  .map(
    ([k, v, u]) => `<div class="figure"><div class="k">${esc(k)}</div><div class="n">${
      v ? fmt(v) : "?"
    }<span class="u">${esc(u)}</span></div></div>`,
  )
  .join("\n")}
</div>

<table>
${rows.map(([k, v]) => `<tr><td class="k">${esc(k)}</td><td class="v">${esc(v)}</td></tr>`).join("\n")}
</table>

${caveatBlock(idx)}

<footer>
<a href="/r/${esc(row.id)}${esc(row.tape_ext)}">Download the record</a> · ${row.tape_bytes} bytes ·
published ${esc(row.created_at)}<br>
The record is the original. <code>toktape card ${esc(row.id)}${esc(row.tape_ext)}</code> draws
the card from it, and everything on this page was derived from it before it was uploaded.
</footer>
</main></body></html>
`;
}

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

function fmt(n) {
  return n >= 100 ? n.toFixed(0) : n.toFixed(1);
}

function esc(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  })[c]);
}

function notFoundPage() {
  return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Not found — toktape</title>
<style>body{margin:0;background:#0e1014;color:#d7dae0;font:15px/1.6 ui-sans-serif,-apple-system,sans-serif}
main{max-width:34rem;margin:0 auto;padding:5rem 1.25rem}a{color:#7aa2f7}</style>
</head><body><main><h1>No run with that id</h1>
<p>Either the link is wrong or the run was deleted. A deleted run is gone —
the delete token is the only key to a one-off upload and it takes the record
with it.</p>
<p><a href="https://github.com/midagedev/toktape">toktape</a></p>
</main></body></html>
`;
}
