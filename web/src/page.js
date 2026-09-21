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
/* TTP-126: a run id inside inline code is one unbreakable token; without a
   break rule it pushes the prose past main's 52rem column (and, on a wide
   viewport, over the margin figure). Scoped to prose wrappers only — the
   run page's footer is the paperwork paragraph — never pre/.screen, where a
   monospace block is meant to scroll. */
.sub code, .details code, .note code, footer code { overflow-wrap: anywhere; }
.brand { display: flex; align-items: center; gap: .6rem; margin-bottom: 2rem; }
.brand a { color: #eef1f5; font-weight: 600; letter-spacing: -.01em; }
.brand span { color: #6b727d; font-size: .8rem; }
/* The mascot: a small round avatar, the way a profile picture sits beside a
   name. Its PNG carries the page's own ground (#0e1014) so the circle needs
   no matting; the 1px ring is the card's border colour. */
.brand .mascot { width: 2.25rem; height: 2.25rem; border-radius: 50%; flex: none;
  border: 1px solid #1b1f26; background: #0e1014; }
/* The figure in the margin: the same character, sitting in the page's empty
   corner the way a sticker sits on a laptop lid. Fixed to the viewport so
   it stays put as the runs scroll past, behind everything, never a click
   target, and gone when the viewport is too narrow to have a margin
   (52rem of main plus the figure's width), so it never covers a card. */
.figure { position: fixed; right: 1.5rem; bottom: 0; z-index: -1; pointer-events: none;
  user-select: none; display: block; }
.figure.peek { width: 11rem; }
.figure.sleep { width: 15rem; bottom: 1.25rem; opacity: .92; }
/* Waving hello from the top-right corner of the front page (user,
   2026-09-19: "메인화면 우상단에도"). */
.figure.wave { width: 10rem; top: 1.5rem; bottom: auto; }
/* Without a margin to sit in (a phone, a narrow window) she moves to the
   foot of the page instead — in the flow, after the footer, so she never
   covers a card and is still there when the reader reaches the end. */
@media (max-width: 78rem) {
  .figure { position: static; margin: 1rem auto 0; }
  .figure.peek { display: block; width: 9rem; margin-bottom: -.5rem; }
  .figure.sleep { display: block; width: 12rem; margin-bottom: 1.5rem; }
  /* The wave stays a corner figure on a phone: small, beside the brand row,
     which leaves it room on the right so the tagline wraps under the name
     rather than behind her. */
  .figure.wave { position: absolute; top: .5rem; right: 1rem; width: 4.25rem; margin: 0; z-index: 0; }
  .brand { padding-right: 5rem; flex-wrap: wrap; margin-bottom: 2.75rem; }
}
/* The empty state gets her in person: sitting with the tape, above the line
   that says there is nothing here yet. */
.empty-figure { display: block; width: 11rem; margin: 2.5rem auto 0; }
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
   follows from 36 lines of it. At a 0.6em cell advance, 36 lines × 1.125
   over 120 × 0.6 is 16:9 exactly — the card's shape — so swapping the
   still for the frame moves nothing around it (2026-09-19; 1.25 made the
   frame a tenth taller than the card). The background is the theme's own. */
.screen { display: none; margin: 0; padding: 0; width: 100%; overflow: hidden;
  line-height: 1.125; background: #101412; border-radius: 8px;
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
/* The author beside a run: a small round avatar — the page's own ring
   colour — and the name under the sub line. The row's .who line reuses
   .avatar at its own size (search.js). */
.avatar { width: 32px; height: 32px; border-radius: 50%; flex: none;
  border: 1px solid #1b1f26; vertical-align: middle; }
.byline { display: flex; align-items: center; gap: .55rem; margin: -.75rem 0 1.5rem;
  font-size: .875rem; color: #7d848f; }
/* The lab-note: plain paragraphs, no markdown, no autolinking — what was
   typed is what is read, wrapped to the prose width. */
.anon { display: inline-block; font-size: .7rem; line-height: 1.4; padding: 0 .45rem;
  border: 1px solid #2a3038; border-radius: 999px; color: #6b727d; cursor: help; }
.note { max-width: 40rem; color: #b9bec7; }
.note p { margin: 0 0 .8rem; }
/* A user home (TTP-127): the profile at 96 px, the name, the one link, the
   bio as paragraphs, then the count and the rows in the front page's row
   format. */
.uhead { display: flex; gap: 1.25rem; align-items: center; margin: 0 0 .5rem; }
/* The home's avatar at 96 px: scoped to the header, so the 32 px byline
   and 20 px row avatars elsewhere keep their size. 128 px is the ceiling,
   and it is a fact about the stored image, not taste: the upload keeps
   avatars at most 256×256 (internal/publish/avatar.go, MaxAvatarWidth/
   MaxAvatarHeight), so past 128 px a 2× display is upsampling and the
   circle goes soft. Raising this past 128 means raising those first. */
.uhead .avatar { width: 96px; height: 96px; }
.uhead h1 { margin: 0; }
.ulink { margin: 0 0 1.5rem; font-size: .875rem; }
`;

// The mascot, as she appears around the site (web/static/README.md has her
// provenance). One character, four poses, each a static asset under dist/:
//
//   /mascot.png            her face on the page's ground — the brand row's
//                          avatar and the favicon's source
//   /favicon.png           64px of the same, /apple-touch-icon.png 180px
//   /mascot-sit.webp       sitting with the tape — the empty state
//   /mascot-peek.webp      peeking over the bottom edge — the front page's margin
//   /mascot-sleep.webp     asleep on a cassette — a run page's margin
//
// The figures are decoration and say so (empty alt, aria-hidden), so a
// screen reader never meets a sticker between the search and its results.
export const FIGURES = {
  peek: `<img class="figure peek" src="/mascot-peek.webp" alt="" aria-hidden="true">`,
  sleep: `<img class="figure sleep" src="/mascot-sleep.webp" alt="" aria-hidden="true">`,
  wave: `<img class="figure wave" src="/mascot-wave.webp" alt="" aria-hidden="true">`,
};
export const EMPTY_FIGURE = `<img class="empty-figure" src="/mascot-sit.webp" alt="" aria-hidden="true">`;

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
// card names the twitter:card for a page with an image: "summary_large_image"
// by default (the 1200×675 run card it was built for), "summary" where the
// image is square — a user home's round avatar under summary_large_image
// crops badly (TTP-127). Pages without an image always say "summary".
export function head({ title, description, url, image, imageAlt, imageWidth, imageHeight, noindex, type = "website", card = "summary_large_image" }) {
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
    tags.push(`<meta name="twitter:card" content="${card === "summary" ? "summary" : "summary_large_image"}">`);
    tags.push(`<meta name="twitter:image" content="${esc(image)}">`);
    if (imageAlt) tags.push(`<meta name="twitter:image:alt" content="${esc(imageAlt)}">`);
  } else {
    tags.push(`<meta name="twitter:card" content="summary">`);
  }
  tags.push(`<meta name="twitter:title" content="${esc(title)}">`);
  if (description) tags.push(`<meta name="twitter:description" content="${esc(description)}">`);
  return tags.join("\n");
}

// figure names the pose (or poses) in this page's margins: "peek" and
// "wave" on the front page, "sleep" on a run page, nothing on the rest.
export function layout({ title, meta = "", style = "", body, figure }) {
  const figures = [].concat(figure || []).map((f) => FIGURES[f] || "").join("");
  return `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${esc(title)}</title>
<link rel="icon" type="image/png" sizes="64x64" href="/favicon.png">
<link rel="apple-touch-icon" sizes="180x180" href="/apple-touch-icon.png">
<meta name="theme-color" content="#0e1014">
${meta}
<style>${STYLE}${style}</style>
</head><body><main>
<div class="brand"><a href="/"><img class="mascot" src="/mascot.png" width="36" height="36" alt="toktape mascot: a mint-haired chibi in headphones"></a><a href="/">toktape</a><span>the record, not a recording of it</span></div>
${body}
</main>${figures}</body></html>
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
