// Feeds are the lists, the counts, and the landing pages. A search stays
// live against D1. A search is a non-empty q, a narrowing filter, a sort
// other than newest, or scope=mine. Absent, empty, and newest are the
// default order and are not a search.
//
// A feed answer is stored in the Cache API for 120s (s-maxage). The browser
// is told 60s (max-age). On a hit the Worker does not touch D1. Cursor URLs
// are their own keys and are not deleted one by one; the TTL is what drops
// them. The keys a publish, a delete, or a public edit does delete are the
// front page, the sitemap, that owner's home and its JSON, and /hw/<id> for
// each card on the tape.

import { HW_PAGE, USER_JSON, USER_PAGE } from "./index.js";
import { GPU_ID_RE } from "./run.js";

// Narrowing filters. `gpu` is one of them on `/` and on a user home. On a
// hub the card is the page, so `gpu` in the query is not a search there.
const NARROWING = [
  "model",
  "model_raw",
  "repo",
  "quant",
  "engine",
  "gpu",
  "host",
  "os",
  "set",
  "sessions",
  "min_vram",
  "size",
  "moe",
  "min_predicted",
];

// Browser 60s, edge and the Cache API 120s. The Cache API takes s-maxage
// when it is present, and max-age otherwise.
export const FEED_CACHE_CONTROL = "public, max-age=60, s-maxage=120";

export function isSearch(url, { ignoreGpu = false } = {}) {
  if ((url.searchParams.get("scope") || "") === "mine") return true;
  if ((url.searchParams.get("q") || "").trim() !== "") return true;
  const sort = url.searchParams.get("sort");
  if (sort !== null && sort !== "" && sort !== "newest") return true;
  for (const p of NARROWING) {
    if (ignoreGpu && p === "gpu") continue;
    if ((url.searchParams.get(p) || "") !== "") return true;
  }
  return false;
}

// null when this URL is not a feed. ignoreGpu is the hub: the card in the
// path is the page, and a further filter is a search.
export function feedKind(url) {
  const path = url.pathname;
  if (path === "/" || path === "/sitemap.xml" || path === "/api/v1/runs") return { ignoreGpu: false };
  if (HW_PAGE.test(path)) return { ignoreGpu: true };
  if (USER_PAGE.test(path) || USER_JSON.test(path)) return { ignoreGpu: false };
  return null;
}

export function isCacheableFeed(url) {
  const kind = feedKind(url);
  if (!kind) return false;
  return !isSearch(url, kind);
}

export function gpuIdsOf(value) {
  let arr = value;
  if (typeof value === "string") {
    if (value === "") return [];
    try {
      arr = JSON.parse(value);
    } catch {
      return [];
    }
  }
  if (!Array.isArray(arr)) return [];
  const seen = new Set();
  const out = [];
  for (const id of arr) {
    if (typeof id === "string" && GPU_ID_RE.test(id) && !seen.has(id)) {
      seen.add(id);
      out.push(id);
    }
  }
  return out;
}

// The canonical keys. Cursor pages and default-query twins (`?sort=newest`)
// are left to the TTL.
export function feedDeleteURLs(origin, { owner, gpus } = {}) {
  const base = String(origin || "").replace(/\/+$/, "");
  if (base === "") return [];
  const urls = [`${base}/`, `${base}/sitemap.xml`];
  // USER_PAGE is the handle route. An owner that would 404 has no home key.
  if (typeof owner === "string" && USER_PAGE.test(`/u/${owner}`)) {
    urls.push(`${base}/u/${owner}`, `${base}/u/${owner}.json`);
  }
  for (const id of gpuIdsOf(gpus)) urls.push(`${base}/hw/${id}`);
  return urls;
}

export function defaultCache() {
  if (typeof caches === "undefined" || !caches || !caches.default) return null;
  return caches.default;
}

function cacheRequest(url) {
  return new Request(url, { method: "GET" });
}

export async function matchFeed(request, cache = defaultCache()) {
  if (!cache) return null;
  const hit = await cache.match(cacheRequest(request.url));
  return hit || null;
}

export function withFeedCache(resp) {
  const headers = new Headers(resp.headers);
  headers.set("Cache-Control", FEED_CACHE_CONTROL);
  return new Response(resp.body, { status: resp.status, statusText: resp.statusText, headers });
}

export function putFeed(request, resp, cache = defaultCache()) {
  if (!cache) return Promise.resolve(false);
  return cache.put(cacheRequest(request.url), resp).then(() => true);
}

// Awaited by the write path so the next read of these keys misses. A missing
// Cache API (the node tests) is a no-op.
export async function invalidateFeeds(origin, info, cache = defaultCache()) {
  const urls = feedDeleteURLs(origin, info);
  if (!cache) return urls;
  await Promise.all(urls.map((u) => cache.delete(cacheRequest(u))));
  return urls;
}
