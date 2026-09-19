// The toktape hub (docs/toktape-spec.ko.md §9).
//
// tape.midagedev.com hosts the record, not pixels: a published run is the
// same structured file `toktape record` wrote, so a card, a GIF and a cast
// can all still be drawn from it. The measured argument for that is in §9.1
// — one hero clip is 55 KB as a record, 4.1 MB as a GIF, and only one of
// those two can be turned back into the other.
//
// **The Worker never parses a tape.** The Go client derives a versioned
// index row next to the schema and uploads it beside the record, so
// knowledge of the tape's shape has exactly one owner. The upload contract
// is the doc block on publish.Client in internal/publish/client.go and the
// request body internal/publish/client_test.go parses; this side is written
// against those, never the other way round.

import { serveAvatar } from "./author.js";
import { deleteRun } from "./del.js";
import { editRun } from "./edit.js";
import { fail, json, publicBase } from "./http.js";
import { serveCard, serveRunJSON, serveRunPage, serveTape } from "./run.js";
import { searchAPI, searchPage } from "./search.js";
import { robots, sitemap } from "./seo.js";
import { uploadRun } from "./upload.js";
import { serveUserJSON, serveUserPage } from "./user.js";

// Both extensions are served: `.toktape` is where the format is going
// (§9.7), `.tape` is what today's client uploads under, and a run is served
// back as whichever it arrived as.
const TAPE_SUFFIX = /^\/r\/([a-z0-9]{8,64})(\.toktape|\.tape)$/;
const RUN_JSON = /^\/r\/([a-z0-9]{8,64})\.json$/;
const RUN_CARD = /^\/r\/([a-z0-9]{8,64})\.png$/;
const RUN_PAGE = /^\/r\/([a-z0-9]{8,64})$/;
const RUN_API = /^\/api\/v1\/runs\/([a-z0-9]{8,64})$/;
// A user home. The handle is URL-safe by construction on this side: a token
// id that does not match has no home page, and the lookup 404s it.
const USER_JSON = /^\/u\/([a-z0-9][a-z0-9-]{1,31})\.json$/;
const USER_PAGE = /^\/u\/([a-z0-9][a-z0-9-]{1,31})$/;
// An avatar's name is its bytes: 64 hex digits, and anything else is a 404
// before touching R2.
const AVATAR = /^\/a\/([0-9a-f]{64})\.png$/;

// The two headers every response *this Worker writes* carries, set in one
// place because a header added at each `return` is a header missing from the
// next one somebody writes. Static assets (`/player/*`, the mascot, the
// favicons) are served by Cloudflare's asset layer before `fetch` runs, so
// they do not pass through here — measured 2026-09-19. That is left alone:
// each of those carries its real content type and a Referrer-Policy on a
// subresource means nothing, and routing every asset hit through the Worker
// to add two headers is a cost with no matching benefit.
//
// Both are answers to a question this service does actually raise:
//
//   nosniff — anonymous uploads become bytes we serve back. An avatar is
//     stored only after its PNG signature matches (upload.js:isPNG) and a
//     record is served as application/json, but a browser that sniffs a
//     content type reintroduces the hole the signature check closed.
//   Referrer-Policy — a run page's URL carries a run id, and the search
//     page's carries whatever the visitor typed into `q`. Neither belongs
//     in an outbound Referer when a reader clicks an author's link, which
//     is a stranger's URL on a page full of strangers' URLs.
//
// No Content-Security-Policy yet: every filter <select> on the search page
// calls an inline `onchange`, so a policy strict enough to be worth having
// would need 'unsafe-hashes' or those handlers moved to addEventListener.
// That is a change with a breakage surface, and it is filed rather than
// rushed (TTP-132). Without CSP these two still do their own jobs.
const SECURITY_HEADERS = {
  "X-Content-Type-Options": "nosniff",
  "Referrer-Policy": "strict-origin-when-cross-origin",
};

// Headers on a Response this Worker constructed are mutable; the guard is
// for a response that ever comes from somewhere else (a cache, a subrequest),
// where set() throws and the answer is a copy rather than a dropped header.
function secured(resp) {
  try {
    for (const [k, v] of Object.entries(SECURITY_HEADERS)) resp.headers.set(k, v);
    return resp;
  } catch {
    const headers = new Headers(resp.headers);
    for (const [k, v] of Object.entries(SECURITY_HEADERS)) headers.set(k, v);
    return new Response(resp.body, { status: resp.status, statusText: resp.statusText, headers });
  }
}

export default {
  async fetch(request, env) {
    return secured(await route(request, env));
  },
};

async function route(request, env) {
  const url = new URL(request.url);
  const path = url.pathname;

  // "Is the hostname ours" and "is the service up" are separate questions,
  // so this answers before anything that needs a binding.
  if (path === "/healthz") {
    return json({ ok: true, service: "toktape-hub", live: true });
  }

  if (path === "/api/v1/runs") {
    if (request.method === "POST") return uploadRun(request, env);
    if (request.method === "GET") return searchAPI(request, env);
    return fail(405, "a run is published with POST and the listing is a GET", { Allow: "GET, POST" });
  }

  let m;
  if ((m = RUN_API.exec(path))) {
    if (request.method === "DELETE") return deleteRun(m[1], request, env);
    if (request.method === "PATCH") return editRun(m[1], request, env);
    return fail(405, "a run is read at /r/<id>, changed with PATCH and deleted with DELETE here", {
      Allow: "DELETE, PATCH",
    });
  }
  if ((m = TAPE_SUFFIX.exec(path))) return serveTape(m[1], env);
  if ((m = RUN_JSON.exec(path))) return serveRunJSON(m[1], env);
  if ((m = RUN_CARD.exec(path))) return serveCard(m[1], env);
  if ((m = AVATAR.exec(path))) return serveAvatar(m[1], env);
  if ((m = RUN_PAGE.exec(path))) return serveRunPage(m[1], env, publicBase(request, env));
  if ((m = USER_JSON.exec(path))) return serveUserJSON(m[1], request, env);
  if ((m = USER_PAGE.exec(path))) return serveUserPage(m[1], request, env);

  // The front page is the search: the thing a visitor came for is other
  // people's runs, not an explanation of the service. Amended 2026-09-19 —
  // the runs still lead, but they are now preceded by two lines and an
  // install command, because a reader arriving from a post could not tell
  // this was a thing they could run without scrolling past twenty runs to
  // the footer (user: "깃헙링크나 인스톨 안내가 너무 눈에 안띈다"). The rule
  // that survives is the ordering, not the absence: no landing page above
  // the listing, and nothing there a visitor has to read to use the search.
  if (path === "/") return searchPage(request, env);
  if (path === "/robots.txt") return robots(publicBase(request, env));
  if (path === "/sitemap.xml") return sitemap(env, publicBase(request, env));
  return fail(404, "no such path on this service");
}
