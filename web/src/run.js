// GET /r/<id> — one published run, and /r/<id>.toktape — the record itself.
//
// The page is built from the index row the client uploaded, which is the
// only thing this side is allowed to read. The card is the PNG the client
// uploaded beside the tape, and Replay is the renderer compiled to wasm
// (web/player), which fetches the record and draws it in the page — this
// side still never opens a tape.
//
// A private run is served here. Unlisted means out of the search, not behind
// a door — the id is the secret (§9.3).

import { avatarPath } from "./author.js";
import { fail, json } from "./http.js";
import { esc, fmt, head, layout } from "./page.js";

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

// authorOf is the profile and the note as the API prints them. Each is
// present only when set — the API's existing rule is absent, not
// null/empty, for unknown — and the avatar is the /a/<hash>.png path, or
// absent with the rest. The profile and the note never pass through the
// public view; they come off the row beside it.
export function authorOf(row) {
  const author = {};
  if (row.author_name) author.name = row.author_name;
  if (row.author_link) author.link = row.author_link;
  const avatar = avatarPath(row.avatar_key);
  if (avatar) author.avatar = avatar;
  return {
    author: Object.keys(author).length ? author : null,
    title: row.title || null,
    note: row.note || null,
  };
}

export async function serveRunJSON(id, env) {
  const row = await env.DB.prepare(
    "SELECT id, created_at, private, index_json, tape_ext, tape_bytes, card_key, author_name, author_link, avatar_key, title, note, owner_token, owner_token IS NOT NULL AS owned FROM runs WHERE id = ?",
  )
    .bind(id)
    .first();
  if (!row) return fail(404, "no run with that id");
  const out = {
    id: row.id,
    published_at: row.created_at,
    private: row.private === 1,
    tape: `/r/${row.id}${row.tape_ext}`,
    tape_bytes: row.tape_bytes,
    card: row.card_key ? `/r/${row.id}.png` : null,
    owned: row.owned === 1,
    index: JSON.parse(row.index_json),
  };
  const { author, title, note } = authorOf(row);
  if (author) out.author = author;
  if (title) out.title = title;
  if (note) out.note = note;
  return json(out);
}

export async function serveRunPage(id, env, base) {
  const row = await env.DB.prepare(
    "SELECT id, created_at, private, index_json, tape_ext, tape_bytes, card_key, author_name, author_link, avatar_key, title, note, owner_token, owner_token IS NOT NULL AS owned FROM runs WHERE id = ?",
  )
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
    ["quantisation", idx.quant_raw ? `${idx.quant_raw}${idx.quant_bits ? ` · ${bits(idx.quant_bits)} bit` : ""}` : "?"],
    ["size", sizeRow(idx)],
    ["engine", [idx.engine_kind, idx.engine_version].filter(Boolean).join(" ") || "?"],
    ["host", [idx.os, idx.host_class].filter(Boolean).join(" · ") || "?"],
    ["gpus", idx.gpus_raw && idx.gpus_raw.length ? idx.gpus_raw.join(" / ") : "—"],
    ["streams", idx.sessions ? String(idx.sessions) : "?"],
    ["workload", workloadRow(idx)],
    ["context", contextRow(idx)],
    ["config", configRow(idx)],
    ["draft", draftRow(idx)],
    ["machine", machineRow(idx)],
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

  const pageURL = `${base}/r/${row.id}`;
  // When the note's title is set it becomes the h1 and the model name moves
  // to the sub line; when unset the page is as today.
  const heading = row.title || title;
  const sub = row.title ? `${title} · ${summaryLine(idx)}` : summaryLine(idx);
  return layout({
    title: `${shareTitle(idx, title, row.title)} — toktape`,
    // The preview is the card. Its title leads with the figure, because the
    // figure is what the post is about and X truncates a title after two
    // lines on a phone; the description carries the rest of the sentence.
    // og:image is absolute, because a crawler does not resolve a relative
    // one — and it is emitted only when there really is a card, so a link
    // never promises a preview that 404s. An unlisted run asks the index to
    // leave it out, in the head as well as in the header (§9.3).
    meta: head({
      title: shareTitle(idx, title, row.title),
      description: shareDescription(idx, row),
      url: pageURL,
      image: row.card_key ? `${pageURL}.png` : "",
      imageAlt: row.card_key ? `The toktape card for this run: ${summaryLine(idx)}` : "",
      imageWidth: 1200,
      imageHeight: 675,
      noindex: row.private === 1,
    }),
    style: PAGE_STYLE,
    figure: "sleep",
    body: `<div class="stage" data-tape="/r/${esc(row.id)}${esc(row.tape_ext)}">
${
      row.card_key
        ? `<img class="card" src="/r/${esc(row.id)}.png" width="1200" height="675"
     alt="The toktape card for this run: ${esc(summaryLine(idx))}">`
        : `<div class="card nocard"><span>published without a card — the run itself is still here</span></div>`
    }
<button class="replay" type="button">▶ Replay</button>
<pre class="screen"></pre>
<div class="controls">
  <button class="toggle" type="button">Pause</button>
  <input class="scrub" type="range" min="0" max="0" value="0" step="1" aria-label="position">
  <span class="clock num">0:00 / 0:00</span>
</div>
<p class="status" hidden></p>
</div>
<div class="actions">
  <button class="act copy" type="button" data-url="${esc(base)}/r/${esc(row.id)}">Copy link</button>
  <button class="act mp4" type="button" data-name="${esc(row.id)}.mp4">Download mp4</button>
  <a class="act" href="/r/${esc(row.id)}${esc(row.tape_ext)}">Download the record</a>
</div>
<h1>${esc(heading)}</h1>
<p class="sub">${esc(sub)}${row.private === 1 ? " · unlisted" : ""}</p>
${byline(row)}
${noteSection(row.note)}
<div class="figures">
${figures
  .map(
    ([k, v, u]) => `<div class="stat"><div class="k">${esc(k)}</div><div class="n num">${fmt(
      v,
    )}<span class="u">${esc(u)}</span></div></div>`,
  )
  .join("\n")}
</div>

<table>
${rows
  // A row whose value is "—" is one that does not apply to this run (no
  // repo, no normalised id); it says nothing and is left out. "?" stays: it
  // is a value that was not observed, and a reader should see that it wasn't.
  .filter(([, v]) => v !== "—")
  .map(([k, v]) => `<tr><td class="k">${esc(k)}</td><td class="v mono">${esc(v)}</td></tr>`)
  .join("\n")}
</table>

<section class="details">
  <h2>Details</h2>
  <p class="sub">The transcript, the card as text, and why each caveat fired — read out of the record in your browser, the same way Replay draws it.</p>
  <button class="act load" type="button">Load details</button>
  <div class="body" hidden></div>
</section>

${caveatBlock(idx)}

<footer>
<a href="/r/${esc(row.id)}${esc(row.tape_ext)}">Download the record</a> · ${kb(row.tape_bytes)} ·
published ${esc(when(row.created_at))}<br>
The record is the original: everything on this page was derived from it before
it was uploaded, Replay draws it again in your browser with the same renderer
the terminal uses, and <code>toktape card ${esc(row.id)}${esc(row.tape_ext)}</code>
draws the card from the same file.<br>
Your own: <code>toktape record</code> against a running llama-server, then
<code>toktape publish</code>. No flags to learn.
</footer>
<script src="/player/wasm_exec.js"></script>
<script src="/player/host.js"></script>`,
  });
}

const PAGE_STYLE = `
/* The card is the page's first sentence: it is what the link previews as,
   and it settles the argument before any of the table is read. */
.stage { margin: 0 0 .75rem; }
/* Replay sits on the card the way a play button sits on a poster: the card
   is the still, the run is the motion, and one click swaps them. */
.replay { position: absolute; left: 50%; top: 50%; transform: translate(-50%, -50%);
  font: 600 1rem/1 ui-sans-serif, -apple-system, "Segoe UI", sans-serif;
  color: #eef1f5; background: rgba(14, 16, 20, .82); border: 1px solid #3a4150;
  border-radius: 999px; padding: .85rem 1.5rem; cursor: pointer;
  backdrop-filter: blur(4px); letter-spacing: .01em; white-space: nowrap; }
.replay:hover { background: rgba(30, 34, 42, .92); border-color: #7aa2f7; }
.replay:disabled { opacity: .6; cursor: default; }
.screen { cursor: pointer; }
/* Progress is written into the button itself; this line under the stage
   is for what went wrong, in the player's own words. */
.status { margin: .6rem 0 0; font-size: .85rem; color: #e0b64a; }
.controls { display: none; align-items: center; gap: .8rem; margin-top: .6rem;
  font-size: .8rem; color: #7d848f; }
.controls .toggle { font: inherit; color: #d7dae0; background: #161a21;
  border: 1px solid #262b35; border-radius: 6px; padding: .3rem .7rem; cursor: pointer;
  min-width: 4.5rem; }
.controls .toggle:hover { border-color: #7aa2f7; }
.controls .scrub { flex: 1; accent-color: #86c2b3; }
/* What a reader does with a run once they have seen it: pass it on, keep a
   video of it, keep the record. The mp4 is drawn in the browser from the
   same frames Replay paints (host.js), so the service still never opens a
   tape and there is no render queue anywhere. */
.actions { display: flex; flex-wrap: wrap; gap: .5rem; margin: 0 0 2.25rem; }
.act { font: inherit; font-size: .85rem; color: #d7dae0; background: #161a21;
  border: 1px solid #262b35; border-radius: 6px; padding: .4rem .8rem; cursor: pointer;
  text-decoration: none; line-height: 1.3; }
.act:hover { border-color: #7aa2f7; text-decoration: none; }
.act:disabled { color: #8a919c; cursor: progress; }
.act.done { border-color: #86c2b3; color: #86c2b3; }
@media (max-width: 48rem) { .actions { margin-top: .9rem; } }
.stage.live .replay { display: none; }
.stage.live .controls { display: flex; }
.figures { display: flex; flex-wrap: wrap; gap: 2.5rem; margin: 0 0 2.5rem; }
.stat .n { font: 600 1.9rem/1.1 ui-monospace, SFMono-Regular, Menlo, monospace;
  color: #eef1f5; }
.stat .u { color: #6b727d; font-size: .8rem; margin-left: .3rem; }
.stat .k { color: #6b727d; font-size: .72rem; text-transform: uppercase;
  letter-spacing: .09em; margin-bottom: .35rem; }
table { border-collapse: collapse; width: 100%; font-size: .9rem; }
td { padding: .45rem 0; border-bottom: 1px solid #1b1f26; vertical-align: top; }
td.k { color: #7d848f; width: 9.5rem; white-space: nowrap; }
/* On a phone the three figures share one line as three equal columns rather
   than wrapping two-and-one with TTFT stranded beneath (seen at 390 px,
   2026-09-19); the digits shrink to fit. The actions do the same. */
@media (max-width: 30rem) {
  td.k { width: 6.5rem; white-space: normal; }
  .figures { display: grid; grid-template-columns: repeat(3, 1fr); gap: .75rem; }
  .stat .n { font-size: 1.45rem; }
  .stat .u { display: block; margin: .15rem 0 0; }
  .actions { display: grid; grid-template-columns: repeat(3, 1fr); }
  .act { text-align: center; padding: .45rem .3rem; font-size: .8rem; white-space: nowrap; }
}
td.v { word-break: break-word; }
.caveats { margin: 2rem 0 0; padding: .9rem 1.1rem; border: 1px solid #3a2f16;
  background: #1a160c; border-radius: 6px; font-size: .875rem; }
.caveats b { color: #e0b64a; font-weight: 600; }
.caveats code { color: #b9a06a; }
/* Details: the transcript and the record's own paperwork, read out of the
   tape in the browser by web/player (host.js) — this side still never opens
   a tape. Long prose wraps; the 80-column card keeps its shape and scrolls
   sideways inside its own pre, so the page body never scrolls. */
.details { margin-top: 2.5rem; }
.details h2 { color: #6b727d; font-size: .72rem; text-transform: uppercase;
  letter-spacing: .09em; margin: 0 0 .35rem; font-weight: 600; }
.details .sub { margin-bottom: 1rem; }
.details .body[hidden] { display: none; }
.block { border: 1px solid #1b1f26; border-radius: 6px; margin: .6rem 0; }
.block > summary { padding: .6rem .9rem; cursor: pointer; color: #d7dae0; }
.block .inner { padding: 0 .9rem .9rem; }
.stream { border-top: 1px solid #1b1f26; padding-top: .8rem; margin-top: .8rem; }
.stream:first-child { border-top: 0; padding-top: 0; margin-top: 0; }
.stream h3 { font-size: .9rem; font-weight: 600; display: flex; flex-wrap: wrap;
  gap: .6rem; align-items: baseline; margin: 0 0 .2rem; }
.ended { color: #86c2b3; }
.ended.bad { color: #e0b64a; }
.msg { display: grid; grid-template-columns: 5.5rem 1fr; gap: .6rem; margin: .5rem 0; }
.msg .role { color: #7d848f; font-size: .75rem; text-transform: uppercase;
  letter-spacing: .08em; padding-top: .2rem; }
.msg pre, .reasoning pre, .cardtext, .details pre { white-space: pre-wrap;
  word-break: break-word;
  font: .85rem/1.5 ui-monospace, SFMono-Regular, Menlo, monospace;
  margin: 0; color: #d7dae0; }
/* The card is 80 columns of box art: it must not wrap. */
.cardtext { white-space: pre; overflow-x: auto; }
.msg.err pre { color: #e0b64a; }
.reasoning { margin: .4rem 0 .4rem 6.1rem; color: #8a919c; }
.reasoning > summary { cursor: pointer; font-size: .85rem; }
.details p { margin: .6rem 0; font-size: .9rem; }
.details code { color: #b9a06a; }
@media (max-width: 30rem) {
  .msg { grid-template-columns: 1fr; }
  .reasoning { margin-left: 0; }
}
`;

// The byline under the sub line: the avatar (24 px, round, only when one
// travels) and the name — a link with rel="nofollow noopener" when the one
// link is set, plain otherwise. When no author travels there is no byline
// element at all. Everything stored was accepted verbatim and is escaped on
// output; nothing stored is HTML.
// anonBadge marks a run nobody owns. The common case — a publish from a
// machine with a journal token — wears nothing (user, 2026-09-19: a
// "token-owned" chip on every row was too much); the one-off upload is the
// exception and is the one that gets a word. It says what that means for
// the reader — nobody can take it down but its one-time delete token — and
// nothing about who: the byline's name stays the unverified label it is.
export function anonBadge(owned) {
  if (owned) return "";
  return ` <span class="anon" title="Uploaded without a journal token: only its one-time delete token can take it down, and no user page lists it.">anonymous</span>`;
}

function byline(row) {
  const avatar = avatarPath(row.avatar_key);
  if (!row.author_name && !row.author_link && !avatar && row.owned === 1) return "";
  const img = avatar ? `<img class="avatar" src="${esc(avatar)}" width="24" height="24" alt="">` : "";
  // On a run a journal token owns, the name links at the owner's home
  // (/u/<handle>, TTP-127) and the external link moves there — it is not
  // printed here. Read off the run's own columns (what that upload said),
  // never off the token.
  let who = "";
  if (row.owner_token) {
    if (row.author_name) who = `<a href="/u/${esc(row.owner_token)}">${esc(row.author_name)}</a>`;
  } else if (row.author_name && row.author_link) {
    who = `<a rel="nofollow noopener" href="${esc(row.author_link)}">${esc(row.author_name)}</a>`;
  } else if (row.author_name) {
    who = esc(row.author_name);
  } else if (row.author_link) {
    who = `<a rel="nofollow noopener" href="${esc(row.author_link)}">${esc(row.author_link)}</a>`;
  }
  return `<p class="byline">${img}${img && who ? " " : ""}${who}${anonBadge(row.owned === 1)}</p>`;
}

// The lab-note as paragraphs split on blank lines, each escaped — no
// markdown, no autolinking. Exported for the user home (user.js), whose bio
// renders the same way: plain paragraphs, nothing else.
export function noteSection(note) {
  if (!note) return "";
  const paras = String(note)
    .split(/\n\n+/)
    .map((p) => p.trim())
    .filter(Boolean);
  if (!paras.length) return "";
  return `<section class="note">\n${paras.map((p) => `<p>${esc(p)}</p>`).join("\n")}\n</section>`;
}

function caveatBlock(idx) {
  if (!idx.caveat_count) return "";
  const codes = (idx.caveats || []).map((c) => `<code>${esc(c)}</code>`).join(", ");
  // The codes, not sentences. The sentences belong to the card, which is the
  // one place that writes them (internal/card/caveat.go); a second wording
  // here would be a second answer to what a caveat means.
  return `<div class="caveats" id="caveats"><b>! ${idx.caveat_count} caveat${
    idx.caveat_count === 1 ? "" : "s"
  }</b> — ${codes}. The card that comes with this record spells each one out.</div>`;
}

// The preview's first line: the figure, then the model. "196 tok/s ·
// Qwen2.5-7B-Instruct Q3_K_M" is a sentence a thread reader can act on;
// the file name alone was not. The note's title, when set, is appended
// after a dash — never leading, because X truncates after two lines and
// the figure stays first.
function shareTitle(idx, fallback, noteTitle) {
  const parts = [];
  if (idx.decode_per_sec) parts.push(`${fmt(idx.decode_per_sec)} tok/s`);
  // The file name stands in when the model never normalised, without its
  // extension: ".gguf" is not part of what the run measured.
  const name = idx.model_id || String(idx.model_raw || fallback).replace(/\.gguf$/i, "");
  parts.push(name);
  // The quantisation only when the name does not already carry it: a raw
  // "Qwen2.5-7B-Instruct-Q3_K_M" followed by "Q3_K_M" said it twice.
  if (idx.quant_raw && !name.toLowerCase().includes(String(idx.quant_raw).toLowerCase())) parts.push(idx.quant_raw);
  const head = parts.join(" · ");
  return noteTitle ? `${head} — ${noteTitle}` : head;
}

// The preview's second line: where and how, then what the reader can do
// with it. Every figure is the index row's, printed the way the card prints
// it, and an unobserved one is left out rather than printed as a zero.
function shareDescription(idx, row) {
  const parts = [];
  if (idx.sessions) parts.push(`${idx.sessions} concurrent stream${idx.sessions === 1 ? "" : "s"}`);
  // The GPU as it named itself, not the index slug: "RTX A6000" is a
  // sentence, "rtx-a6000" is a key. The slug is for the filter, not the tweet.
  if (idx.gpus_raw && idx.gpus_raw.length) parts.push(`on ${gpuNames(idx.gpus_raw)}`);
  else if (idx.gpu_id) parts.push(`on ${idx.gpu_count > 1 ? `${idx.gpu_count}× ` : ""}${idx.gpu_id}`);
  const engine = [idx.engine_kind, idx.engine_version].filter(Boolean).join(" ");
  if (engine) parts.push(`with ${engine}`);
  const figures = [];
  if (idx.prefill_per_sec) figures.push(`prefill ${fmt(idx.prefill_per_sec)} tok/s`);
  if (idx.ttft_p50_ms) figures.push(`ttft p50 ${fmt(idx.ttft_p50_ms)} ms`);
  if (idx.caveat_count) figures.push(`${idx.caveat_count} caveat${idx.caveat_count === 1 ? "" : "s"}`);
  let s = parts.join(" ");
  if (figures.length) s += (s ? " · " : "") + figures.join(" · ");
  s += (s ? ". " : "") + "Replay the run in the browser, read the transcript, or download the record.";
  return s;
}

// Four identical cards read as "4× NVIDIA RTX A6000", a mixed rig as the
// list it is.
function gpuNames(raw) {
  const names = raw.map(String);
  if (names.length > 1 && names.every((n) => n === names[0])) return `${names.length}× ${names[0]}`;
  return names.join(" / ");
}

function summaryLine(idx) {
  const parts = [];
  if (idx.decode_per_sec) parts.push(`${fmt(idx.decode_per_sec)} tok/s decode`);
  if (idx.sessions) parts.push(`${idx.sessions} stream${idx.sessions === 1 ? "" : "s"}`);
  if (idx.quant_raw) parts.push(idx.quant_raw);
  if (idx.gpu_id) parts.push(idx.gpu_count > 1 ? `${idx.gpu_count}× ${idx.gpu_id}` : idx.gpu_id);
  return parts.join(" · ") || "a recorded run";
}

// Bits per weight as the card would print it: two decimals at most, and a
// figure like 6.5625 (a real UD-Q6_K measurement) does not pretend to four.
function bits(b) {
  return Number(b).toFixed(2).replace(/\.?0+$/, "");
}

// The figure rows (TTP-130). Every part is copied from the index the client
// derived — unknown parts are omitted, and a row with nothing known reads
// `?` (size/workload/context/config) or is left out by the table's `—`
// filter (draft/machine, which apply only sometimes).
function sizeRow(idx) {
  const parts = [];
  if (idx.params) parts.push(`${fmtB(idx.params)} params`);
  if (idx.moe === true && idx.active_params) parts.push(`${fmtB(idx.active_params)} active`);
  if (idx.file_bytes) parts.push(`${(idx.file_bytes / 1e9).toFixed(1)} GB`);
  if (idx.n_experts || idx.n_experts_used) {
    parts.push(`MoE ${idx.n_experts || "?"}${idx.n_experts_used ? `/${idx.n_experts_used}` : ""}`);
  }
  return parts.length ? parts.join(" · ") : "?";
}

function workloadRow(idx) {
  const parts = [];
  const io = [];
  if (idx.prompt_n) io.push(`${idx.prompt_n} in`);
  if (idx.predicted_n) io.push(`${idx.predicted_n} out`);
  if (io.length) parts.push(io.join(" / "));
  if (idx.min_predicted_n) parts.push(`min ${idx.min_predicted_n}`);
  if (idx.reasoning_n) parts.push(`${idx.reasoning_n} thinking`);
  const cache = cacheLabel(idx.cache_hit_ratio, idx.prompt_n);
  if (cache) parts.push(cache);
  if (!parts.length) return "?";
  let s = parts.join(" · ");
  if (idx.sessions > 1) s += " · per stream";
  return s;
}

function contextRow(idx) {
  const parts = [];
  if (idx.ctx_size) parts.push(`${idx.ctx_size} window`);
  if (idx.n_slots) parts.push(`${idx.n_slots} slot${idx.n_slots === 1 ? "" : "s"}`);
  return parts.length ? parts.join(" · ") : "?";
}

function configRow(idx) {
  const parts = [];
  if (idx.fa) parts.push(`fa ${idx.fa}`);
  if (idx.kv_cache) parts.push(`kv ${idx.kv_cache}`);
  if (idx.batch) parts.push(`b ${idx.batch}`);
  if (idx.ubatch) parts.push(`ub ${idx.ubatch}`);
  if (idx.ngl) parts.push(`ngl ${idx.ngl}`);
  if (idx.offload) parts.push(`offload ${idx.offload}`);
  return parts.length ? parts.join(" · ") : "?";
}

function draftRow(idx) {
  if (!idx.draft_model) return "—";
  if (idx.draft_accept) return `${idx.draft_model} · ${Math.round(idx.draft_accept * 100)}% accepted`;
  return idx.draft_model;
}

function machineRow(idx) {
  const parts = [];
  if (idx.power_w && idx.power_limit_w) parts.push(`${trimNum(idx.power_w)} of ${trimNum(idx.power_limit_w)} W`);
  else if (idx.power_w) parts.push(`${trimNum(idx.power_w)} W`);
  if (idx.throttled === true) parts.push("throttled");
  if (idx.cold === true) parts.push("cold cache");
  return parts.length ? parts.join(" · ") : "—";
}

// fmtB and cacheLabel are duplicated from search.js on purpose: search.js
// imports this file (anonBadge, authorOf), so the helpers cannot live in
// only one place without a cycle. The two copies print the same strings.
function fmtB(n) {
  const b = n / 1e9;
  return b >= 10 ? `${Math.round(b)}B` : `${Math.round(b * 10) / 10}B`;
}

function cacheLabel(ratio, prompt_n) {
  if (ratio !== null && ratio !== undefined) return `cache ${Math.round(ratio * 100)}%`;
  if (prompt_n > 0) return "cache 0%";
  return null;
}

function trimNum(n) {
  return Number.isInteger(n) ? String(n) : String(Math.round(n * 10) / 10);
}

function kb(n) {
  if (!n) return "? bytes";
  return n < 1024 ? `${n} bytes` : `${(n / 1024).toFixed(1)} KB`;
}

// The upload time to the minute, in UTC, because that is what the server
// recorded. The tape's own recorded_at is a different clock and stays in
// the table above with its offset (TTP-120).
function when(iso) {
  const m = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2})/.exec(String(iso));
  return m ? `${m[1]} ${m[2]} UTC` : String(iso);
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
