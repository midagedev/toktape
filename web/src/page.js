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
`;

export function layout({ title, meta = "", style = "", body }) {
  return `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${esc(title)}</title>
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
