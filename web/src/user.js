// GET /u/<handle> — one journal token's user home (TTP-127).
//
// The profile (avatar, name, link, bio) and only that user's public runs,
// in the front page's row format with the same filters and cursor. The
// profile belongs to the token, not to each run: every token-owned publish
// copies the author parts onto the token's row (upload.js), so this page
// always shows the latest profile, while each row keeps showing its run's
// own columns — the record of what that upload said.
//
// /u/<handle>.json answers {handle, name?, link?, avatar?, bio?, runs, next}:
// absent, never null, for whatever is unset, the API's existing rule for
// unknown (run.js). A token id that is not URL-safe by construction has no
// home: the route holds ^[a-z0-9][a-z0-9-]{1,31}$ and anything else is a 404
// ("no such user").

import { avatarPath } from "./author.js";
import { fail, json, publicBase } from "./http.js";
import { EMPTY_FIGURE, esc, head, layout } from "./page.js";
import { noteSection } from "./run.js";
import {
  activeFilters,
  apiRow,
  countTotal,
  distinctFacets,
  filterForm,
  PAGE_STYLE,
  pageSize,
  query,
  rowGrid,
  totalLine,
} from "./search.js";

export async function serveUserJSON(handle, request, env) {
  const t = await tokenProfile(env, handle);
  if (!t) return fail(404, "no such user");
  const url = new URL(request.url);
  const scope = { name: "user", owner: handle };
  const q = await query(env, url, scope, pageSize(url));
  if (q.error) return q.error;
  const out = { handle };
  if (t.name) out.name = t.name;
  if (t.link) out.link = t.link;
  const avatar = avatarPath(t.avatar_key);
  if (avatar) out.avatar = avatar;
  if (t.bio) out.bio = t.bio;
  out.runs = q.rows.map(apiRow);
  out.next = q.next;
  return json(out);
}

export async function serveUserPage(handle, request, env) {
  const t = await tokenProfile(env, handle);
  if (!t) {
    return new Response(notFoundPage(), {
      status: 404,
      headers: { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" },
    });
  }
  const url = new URL(request.url);
  const base = publicBase(request, env);
  const scope = { name: "user", owner: handle };
  const limit = pageSize(url);
  const q = await query(env, url, scope, limit);
  if (q.error) return q.error;
  const facets = await distinctFacets(env, url, scope);
  const active = activeFilters(url);
  const n = await countTotal(env, url, scope);

  const name = t.name || handle;
  const avatar = avatarPath(t.avatar_key);
  const pageURL = `${base}/u/${handle}`;
  const description = t.bio ? firstLine(t.bio) : `${n} published runs`;
  const mid =
    q.rows.length === 0
      ? `${EMPTY_FIGURE}<p class="empty">No public runs here yet.</p>`
      : `${await totalLine(env, url, scope, limit, q.next)}
${rowGrid(q.rows, url)}`;
  const more = (() => {
    if (!q.next) return "";
    const u = new URL(url.href);
    u.searchParams.set("cursor", q.next);
    return `<p class="more"><a href="${esc(u.pathname + u.search)}">Older runs →</a></p>`;
  })();

  return new Response(
    layout({
      title: `${name} — toktape`,
      meta: head({
        title: `${name} — toktape`,
        description,
        url: pageURL,
        // The avatar as the preview image only beside a summary card: a
        // square avatar under summary_large_image crops badly.
        ...(avatar ? { image: `${base}${avatar}`, imageAlt: `${name} on toktape`, card: "summary" } : {}),
      }),
      style: PAGE_STYLE,
      figure: "peek",
      body: `
<div class="uhead">${avatar ? `<img class="avatar" src="${esc(avatar)}" width="64" height="64" alt="">` : ""}<h1>${esc(name)}</h1></div>
${t.link ? `<p class="ulink"><a rel="nofollow noopener" href="${esc(t.link)}">${esc(t.link)}</a></p>` : ""}
${t.bio ? noteSection(t.bio) : ""}
${filterForm(url, facets)}
${active}
${mid}
${more}
<footer>
<a href="/">Published runs</a> · the profile above follows the token, so the
newest publish's name and avatar are what this page shows.
</footer>
<script src="/player/wasm_exec.js"></script>
<script src="/player/host.js"></script>`,
    }),
    { headers: { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" } },
  );
}

// The token's row, or null. A token minted before 0006 has every new column
// NULL: the home then shows the handle as the name and no bio, the same
// absent-not-null rule the run API keeps for unknown.
async function tokenProfile(env, handle) {
  const t = await env.DB.prepare("SELECT id, name, link, avatar_key, bio FROM tokens WHERE id = ?")
    .bind(handle)
    .first();
  return t || null;
}

function firstLine(bio) {
  const line = String(bio).split("\n")[0].trim();
  return line || "A toktape user home.";
}

function notFoundPage() {
  return layout({
    title: "Not found — toktape",
    body: `<h1>No such user</h1>
<p class="sub">Either the link is wrong, or that token never published a run.
A user home appears once its token owns a run.</p>
<p><a href="/">Published runs</a></p>`,
  });
}
