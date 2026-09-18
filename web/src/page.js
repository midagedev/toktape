// The chrome every page shares.
//
// One stylesheet, inline, because the pages are small and a published run
// should render on the first response — the whole argument in §9.1 is that
// the record is the small thing, and a page that needs three round trips to
// show it would be arguing the other way.

export const STYLE = `
:root { color-scheme: dark; }
* { box-sizing: border-box; }
body { margin: 0; background: #0e1014; color: #d7dae0;
  font: 15px/1.6 ui-sans-serif, -apple-system, "Segoe UI", sans-serif;
  -webkit-font-smoothing: antialiased; }
main { max-width: 52rem; margin: 0 auto; padding: 2.5rem 1.25rem 5rem; }
a { color: #7aa2f7; text-decoration: none; }
a:hover { text-decoration: underline; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .9em; }
.brand { display: flex; align-items: baseline; gap: .6rem; margin-bottom: 2rem; }
.brand a { color: #eef1f5; font-weight: 600; letter-spacing: -.01em; }
.brand span { color: #6b727d; font-size: .8rem; }
h1 { font-size: 1.35rem; margin: 0 0 .25rem; font-weight: 600; letter-spacing: -.01em;
  word-break: break-word; }
.sub { color: #7d848f; font-size: .875rem; margin: 0 0 2.25rem; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.num { font-variant-numeric: tabular-nums; }
footer { margin-top: 3rem; padding-top: 1.5rem; border-top: 1px solid #1b1f26;
  font-size: .85rem; color: #6b727d; line-height: 1.7; }

/* The stage: a run's card as the still, and the same run replayed over it
   by web/player/host.js. Shared by /r/<id> (one stage, with a Replay button
   and a transport) and the front page (one per row, playing as it scrolls
   into view). */
.stage { position: relative; display: block; }
.card { width: 100%; height: auto; display: block; border-radius: 8px;
  border: 1px solid #1b1f26; }
.nocard { aspect-ratio: 16 / 9; background: #0a0c10; display: flex;
  align-items: flex-end; justify-content: center; padding-bottom: 1.2rem;
  color: #4d545f; font-size: .8rem; }
/* The terminal, sized by host.js so 120 columns fill the stage; the height
   follows from 36 lines of it. The background is the theme's own. */
.screen { display: none; margin: 0; padding: 0; width: 100%; overflow: hidden;
  line-height: 1.25; background: #101412; border-radius: 8px;
  border: 1px solid #1b1f26; color: #e6e2d8;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  white-space: pre; user-select: none; }
/* A wide (CJK, Hangul, emoji) character occupies exactly two cells, as the
   renderer assumed when it laid the line out; see host.js. */
.screen .w { display: inline-block; width: 2ch; font-weight: inherit;
  text-align: center; overflow: hidden; vertical-align: bottom; }
.stage.live .card { display: none; }
.stage.live .screen { display: block; }
/* On a phone the stage bleeds to the screen's edges: every pixel of width
   is a bigger cell, and 120 columns need all of them (user, 2026-09-18:
   "좌우 여백 없이 딱 붙게"). The frame's own box border is its edge. */
@media (max-width: 48rem) {
  .stage { margin-left: -1.25rem; margin-right: -1.25rem; }
  .card, .screen { border-radius: 0; border-left: 0; border-right: 0; }
}
`;

// The favicon is inline — one request fewer, and a service with two pages
// does not need an asset pipeline for a 300-byte glyph. A tape reel, drawn
// in the card's mint on the page's ground.
const FAVICON =
  "data:image/svg+xml," +
  encodeURIComponent(
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="7" fill="#0e1014"/><circle cx="16" cy="16" r="9.5" fill="none" stroke="#86c2b3" stroke-width="3"/><circle cx="16" cy="16" r="2.5" fill="#86c2b3"/><path d="M16 6.5v5M16 20.5v5M6.5 16h5M20.5 16h5" stroke="#86c2b3" stroke-width="2.4" stroke-linecap="round"/></svg>`,
  );

export const SITE_NAME = "toktape";

// head builds the tags a link preview and a search engine read, from the
// facts a page has, so every page says the same things in the same order
// and a crawler is never handed half a card (an og:image with no
// description, a twitter:card with no title to put under it).
//
// X reads the og:* tags and falls back to them for everything but
// twitter:card, but its validator and some clients still want the twitter:*
// pair beside them, and Slack, Discord, Telegram and iMessage each read a
// slightly different subset; emitting the union costs a few hundred bytes
// and removes the guessing. The image is only promised when there is one.
export function head({ title, description, url, image, imageAlt, imageWidth, imageHeight, noindex, type = "website" }) {
  const tags = [];
  if (description) tags.push(`<meta name="description" content="${esc(description)}">`);
  if (url) tags.push(`<link rel="canonical" href="${esc(url)}">`);
  if (noindex) tags.push(`<meta name="robots" content="noindex">`);
  tags.push(`<meta property="og:site_name" content="${SITE_NAME}">`);
  tags.push(`<meta property="og:type" content="${esc(type)}">`);
  tags.push(`<meta property="og:locale" content="en_US">`);
  tags.push(`<meta property="og:title" content="${esc(title)}">`);
  if (description) tags.push(`<meta property="og:description" content="${esc(description)}">`);
  if (url) tags.push(`<meta property="og:url" content="${esc(url)}">`);
  if (image) {
    tags.push(`<meta property="og:image" content="${esc(image)}">`);
    tags.push(`<meta property="og:image:secure_url" content="${esc(image)}">`);
    tags.push(`<meta property="og:image:type" content="image/png">`);
    if (imageWidth) tags.push(`<meta property="og:image:width" content="${imageWidth}">`);
    if (imageHeight) tags.push(`<meta property="og:image:height" content="${imageHeight}">`);
    if (imageAlt) tags.push(`<meta property="og:image:alt" content="${esc(imageAlt)}">`);
    tags.push(`<meta name="twitter:card" content="summary_large_image">`);
    tags.push(`<meta name="twitter:image" content="${esc(image)}">`);
    if (imageAlt) tags.push(`<meta name="twitter:image:alt" content="${esc(imageAlt)}">`);
  } else {
    tags.push(`<meta name="twitter:card" content="summary">`);
  }
  tags.push(`<meta name="twitter:title" content="${esc(title)}">`);
  if (description) tags.push(`<meta name="twitter:description" content="${esc(description)}">`);
  return tags.join("\n");
}

export function layout({ title, meta = "", style = "", body }) {
  return `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${esc(title)}</title>
<link rel="icon" href="${FAVICON}">
<meta name="theme-color" content="#0e1014">
${meta}
<style>${STYLE}${style}</style>
</head><body><main>
<div class="brand"><a href="/">toktape</a><span>the record, not a recording of it</span></div>
${body}
</main></body></html>
`;
}

export function esc(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  })[c]);
}

// Figures are printed the way the card prints them, and an unmeasured one
// prints `?` rather than a zero (CLAUDE.md: unknown is "" / 0 and prints as
// `?`; never print a default you did not observe).
export function fmt(n) {
  if (!n) return "?";
  return n >= 100 ? n.toFixed(0) : n.toFixed(1);
}
