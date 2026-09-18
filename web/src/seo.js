// What a crawler asks for before it reads anything: robots.txt and the
// sitemap.
//
// The sitemap lists the public runs and nothing else. An unlisted run is
// "out of the search" (§9.3) and its page already asks not to be indexed,
// so listing it here would take back with one hand what the page gives
// with the other. The front page is in it once; filtered searches are the
// same page and are not.

import { text } from "./http.js";
import { esc } from "./page.js";

// Sitemaps allow 50,000 URLs; the service is nowhere near, and a second
// file is a decision to make when the first one fills.
const SITEMAP_MAX = 50000;

export function robots(base) {
  return text(`User-agent: *\nAllow: /\nDisallow: /api/\n\nSitemap: ${base}/sitemap.xml\n`, 200, {
    "Cache-Control": "public, max-age=3600",
  });
}

export async function sitemap(env, base) {
  const q = await env.DB.prepare(
    "SELECT id, created_at FROM runs WHERE private = 0 ORDER BY created_at DESC LIMIT ?",
  )
    .bind(SITEMAP_MAX - 1)
    .all();
  const rows = q.results || [];
  const urls = [`<url><loc>${esc(base)}/</loc>${rows[0] ? `<lastmod>${esc(day(rows[0].created_at))}</lastmod>` : ""}</url>`];
  for (const r of rows) {
    urls.push(`<url><loc>${esc(base)}/r/${esc(r.id)}</loc><lastmod>${esc(day(r.created_at))}</lastmod></url>`);
  }
  // The user homes (TTP-127): one per token that owns at least one public
  // run. A token whose runs are all unlisted has no home worth indexing —
  // the home itself would be empty — and an anonymous upload has no home at
  // all. Budgeted with the runs under the same SITEMAP_MAX.
  const owners = await env.DB.prepare(
    "SELECT DISTINCT owner_token AS h FROM runs WHERE private = 0 AND owner_token IS NOT NULL LIMIT ?",
  )
    .bind(Math.max(0, SITEMAP_MAX - 1 - rows.length))
    .all();
  for (const o of owners.results || []) {
    urls.push(`<url><loc>${esc(base)}/u/${esc(o.h)}</loc></url>`);
  }
  return new Response(
    `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls.join("\n")}\n</urlset>\n`,
    {
      headers: {
        "Content-Type": "application/xml; charset=utf-8",
        // New runs should reach the index within the hour; the page itself
        // is what changes, and it is cached for a minute.
        "Cache-Control": "public, max-age=3600",
      },
    },
  );
}

// A run's publish time to the day, in W3C date form. The time of day is a
// precision the sitemap does not need and a crawler does not use.
function day(iso) {
  const m = /^(\d{4}-\d{2}-\d{2})/.exec(String(iso));
  return m ? m[1] : String(iso).slice(0, 10);
}
