// A short prompt is not a prefill measurement (card.ShortPrompt, tape.MinPrefillPromptTokens).
// The run page shows the probe, or says so; the search prints no prefill for it.
//
// Run: node --test test/*.test.mjs   (from web/)

import assert from "node:assert/strict";
import test from "node:test";

import { serveRunPage } from "../src/run.js";
import { resultRow } from "../src/search.js";

const BASE = "https://tape.midagedev.com";

const STAT_PROBE = `<div class="stat"><div class="k">prefill</div><div class="n num">679<span class="u">tok/s probe</span></div></div>`;
const STAT_SHORT = `<div class="stat"><div class="k">prefill</div><div class="n num">—<span class="u">short prompt</span></div></div>`;
const STAT_NORMAL = `<div class="stat"><div class="k">prefill</div><div class="n num">610<span class="u">tok/s</span></div></div>`;

const ROW_BOTH = `<tr><td class="k">prefill</td><td class="v mono">not measured: 33-token prompt · probe 679 tok/s on one stream · 134 ms fixed</td></tr>`;
const ROW_SHORT = `<tr><td class="k">prefill</td><td class="v mono">not measured: 33-token prompt</td></tr>`;
const ROW_PROBE = `<tr><td class="k">prefill</td><td class="v mono">probe 679 tok/s on one stream · 134 ms fixed</td></tr>`;
const ROW_ZERO = `<tr><td class="k">prefill</td><td class="v mono">probe 679 tok/s on one stream · 0 ms fixed</td></tr>`;

const REPLAY = "Replay the run in the browser, read the transcript, or download the record.";

function envFor(idx) {
  const row = {
    id: "abcdefgh",
    created_at: "2026-09-19T07:00:00Z",
    private: 0,
    owned: 1,
    card_key: null,
    tape_ext: ".toktape",
    tape_bytes: 100,
    index_json: JSON.stringify({
      schema: 1,
      recorded_at: "2026-09-13T14:25:30Z",
      model_raw: "demo.gguf",
      decode_per_sec: 17.4,
      ...idx,
    }),
  };
  return { DB: { prepare: () => ({ bind: () => ({ first: async () => row }) }) } };
}

async function page(idx) {
  const resp = await serveRunPage("abcdefgh", envFor(idx), BASE);
  return resp.text();
}

// Both description metas are the same string (head() writes it twice).
function description(html) {
  const meta = /<meta name="description" content="([^"]*)">/.exec(html);
  const og = /<meta property="og:description" content="([^"]*)">/.exec(html);
  assert.ok(meta, "no description meta");
  assert.ok(og, "no og:description");
  assert.equal(og[1], meta[1]);
  return meta[1];
}

test("short prompt with a probe: the stat is the probe and the row says both", async () => {
  const html = await page({
    short_prompt: true,
    prompt_n: 33,
    prefill_per_sec: 206,
    probe_prefill_per_sec: 679,
    probe_fixed_ms: 134,
  });
  assert.ok(html.includes(STAT_PROBE), "prefill stat");
  assert.equal(html.includes("206"), false, "the short-prompt rate is on the page");
  assert.ok(html.includes(ROW_BOTH), "prefill facts row");
  assert.equal(description(html), `probe prefill 679 tok/s. ${REPLAY}`);
});

test("5673-token prompt: the stat is the run's prefill and the row is the probe only", async () => {
  const html = await page({
    prompt_n: 5673,
    prefill_per_sec: 610,
    probe_prefill_per_sec: 679,
    probe_fixed_ms: 134,
  });
  assert.ok(html.includes(STAT_NORMAL), "prefill stat");
  assert.equal(html.includes("tok/s probe"), false);
  assert.equal(html.includes("short prompt"), false);
  assert.equal(html.includes("not measured"), false);
  assert.ok(html.includes(ROW_PROBE), "prefill facts row");
  assert.equal(description(html), `prefill 610 tok/s. ${REPLAY}`);
});

test("short prompt with no probe: a dash, and no prefill clause in the summary", async () => {
  const html = await page({
    short_prompt: true,
    prompt_n: 33,
    prefill_per_sec: 206,
  });
  assert.ok(html.includes(STAT_SHORT), "prefill stat");
  assert.equal(html.includes("206"), false, "the short-prompt rate is on the page");
  assert.ok(html.includes(ROW_SHORT), "prefill facts row");
  assert.equal(html.includes("probe"), false);
  assert.equal(description(html), REPLAY);
});

test("a normal run with no probe has no prefill facts row", async () => {
  const html = await page({ prompt_n: 5673, prefill_per_sec: 610 });
  assert.ok(html.includes(STAT_NORMAL), "prefill stat");
  assert.equal(html.includes(`<td class="k">prefill</td>`), false);
});

test("a measured probe fixed cost of 0 ms is printed, not dropped", async () => {
  const html = await page({
    prompt_n: 5673,
    prefill_per_sec: 610,
    probe_prefill_per_sec: 679,
    probe_fixed_ms: 0,
  });
  assert.ok(html.includes(ROW_ZERO), "prefill facts row");
});

test("a short-prompt search row prints no prefill: its D1 column is NULL", () => {
  // The index omits prefill_per_sec on a short prompt, so row.js stores NULL;
  // the search draws from its columns and never reads index_json.
  const html = resultRow(
    {
      id: "abcdefgh",
      created_at: "2026-09-19T07:00:00Z",
      owned: 1,
      decode_per_sec: 17.4,
      prefill_per_sec: null,
    },
    new URL(`${BASE}/`),
  );
  assert.equal(html.includes("prefill"), false);
});

test("a normal search row still prints its prefill", () => {
  const html = resultRow(
    {
      id: "abcdefgh",
      created_at: "2026-09-19T07:00:00Z",
      owned: 1,
      decode_per_sec: 17.4,
      prefill_per_sec: 610,
      probe_prefill_per_sec: 679,
    },
    new URL(`${BASE}/`),
  );
  assert.ok(html.includes(`<span class="rate sub num">prefill 610<span class="u"> tok/s</span></span>`));
  assert.equal(html.includes("679"), false);
  assert.equal(html.includes("—"), false);
});
