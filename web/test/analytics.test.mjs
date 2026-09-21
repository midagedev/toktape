// The page-view counter's off switch (2026-09-21).
//
// web/check.sh pins what a configured hub does: one beacon per page, the
// sentence beside it, nothing on JSON. This pins the other half, which the
// gate's single dev server cannot show: a hub with no token — every
// self-hosted one — sends its pages out exactly as it made them, and a
// mistyped token is treated as no token rather than pasted into an attribute.
//
// Run: node --test test/*.test.mjs   (from web/)

import assert from "node:assert/strict";
import test from "node:test";

import { beaconScript, beaconToken, counted } from "../src/analytics.js";

const html = () => new Response("<html><body><main></main></body></html>", {
  headers: { "Content-Type": "text/html; charset=utf-8" },
});

test("no token: the response is returned untouched, not a rewritten copy", () => {
  for (const env of [{}, { CF_BEACON_TOKEN: "" }, undefined]) {
    const resp = html();
    assert.equal(counted(resp, env), resp);
  }
});

test("a token that is not a site token prints nothing", () => {
  for (const bad of ["abc", "0123456789abcdef0123456789abcdeg", `x"'><script>`, 42, null]) {
    assert.equal(beaconToken({ CF_BEACON_TOKEN: bad }), "");
    const resp = html();
    assert.equal(counted(resp, { CF_BEACON_TOKEN: bad }), resp);
  }
});

test("a response that is not a page is never rewritten", () => {
  const resp = new Response("{}", { headers: { "Content-Type": "application/json" } });
  assert.equal(counted(resp, { CF_BEACON_TOKEN: "0123456789abcdef0123456789abcdef" }), resp);
});

test("the script carries the token and nothing else configurable", () => {
  const t = "0123456789abcdef0123456789abcdef";
  assert.equal(beaconToken({ CF_BEACON_TOKEN: ` ${t}\n` }), t);
  assert.equal(
    beaconScript(t),
    `<script defer src="https://static.cloudflareinsights.com/beacon.min.js" data-cf-beacon='{"token":"${t}"}'></script>`,
  );
});
