// The page-view counter, and the one place it is attached.
//
// Cloudflare Web Analytics, and nothing else: no cookie, no fingerprint, no
// cross-site identifier — it counts page views and their timings. It is here
// because the zone's automatic injection does not reach this service
// (measured 2026-09-21: midagedev.com has had auto_install with host `*`
// since 2023, and the HTML this Worker writes arrived without the beacon).
// A Worker's own response is not an origin response the edge rewrites, so
// the snippet has to be written by the Worker.
//
// Three rules, each closing a way this could go wrong:
//
//   - The token is a setting (CF_BEACON_TOKEN in wrangler.jsonc), never a
//     literal. Unset means no script and no sentence, so somebody hosting
//     their own hub from this repository is not counted in ours.
//   - It is attached in one place, to whatever HTML leaves the Worker,
//     rather than threaded through layout()'s five callers: a page added
//     later cannot forget it, and cannot disagree with the others about it.
//   - The page says so. A project whose binary promises "no telemetry" does
//     not put a counter on its site silently; the sentence is appended with
//     the script and cannot be shipped without it, or it without the sentence.
//
// The token is not a secret — it is printed in the HTML of every page — so
// it lives in `vars`, not in a secret.

const BEACON_SRC = "https://static.cloudflareinsights.com/beacon.min.js";

// A Cloudflare site token is 32 hex characters. Anything else is a mistyped
// setting, and a mistyped setting prints nothing rather than being pasted
// into an attribute.
const TOKEN = /^[a-f0-9]{32}$/;

export function beaconToken(env) {
  const t = env && typeof env.CF_BEACON_TOKEN === "string" ? env.CF_BEACON_TOKEN.trim() : "";
  return TOKEN.test(t) ? t : "";
}

export function beaconScript(token) {
  return `<script defer src="${BEACON_SRC}" data-cf-beacon='{"token":"${token}"}'></script>`;
}

export const DISCLOSURE =
  `<p class="counted" style="margin-top:2rem;color:#6b727d;font-size:.8rem">` +
  `Page views are counted with Cloudflare Web Analytics: no cookies, no fingerprinting. ` +
  `The <code>toktape</code> binary itself sends nothing anywhere.</p>`;

// counted returns resp with the beacon and its sentence when there is a
// token and resp is HTML; otherwise resp itself, untouched. JSON, tapes,
// cards and avatars never pass through the rewriter.
export function counted(resp, env) {
  const token = beaconToken(env);
  if (!token) return resp;
  const type = resp.headers.get("Content-Type") || "";
  if (!type.startsWith("text/html")) return resp;
  return new HTMLRewriter()
    .on("main", { element(el) { el.append(DISCLOSURE, { html: true }); } })
    .on("body", { element(el) { el.append(beaconScript(token), { html: true }); } })
    .transform(resp);
}
