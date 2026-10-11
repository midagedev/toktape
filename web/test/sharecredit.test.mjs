// The link preview names who recorded a run (TTP-199). X shows the title
// over the image on summary_large_image and often drops the description,
// so the credit rides in the title, after the figure, model and note title.
// Routed through src/index.js GET /r/<id>, with an in-memory D1 fake.
//
// Run: node --test test/*.test.mjs   (from web/)

import assert from "node:assert/strict";
import test from "node:test";

import worker from "../src/index.js";

const BASE = "https://tape.midagedev.com";

function runRow(overrides = {}) {
  return {
    id: "abcdefgh",
    created_at: "2026-09-19T07:00:00Z",
    private: 0,
    owner_token: null,
    owned: 0,
    card_key: "runs/abcdefgh/card.png",
    tape_key: "runs/abcdefgh/run.toktape",
    tape_ext: ".toktape",
    tape_bytes: 25600,
    author_name: null,
    author_link: null,
    avatar_key: null,
    title: null,
    note: null,
    index_json: JSON.stringify({
      schema: 1,
      model_raw: "Qwen3-8B-Q4_K_M.gguf",
      model_id: "qwen3-8b",
      quant_raw: "Q4_K_M",
      decode_per_sec: 52.1,
      engine_kind: "llama.cpp",
      engine_version: "b6000",
      gpu_id: "dgx-spark",
      gpu_count: 1,
    }),
    ...overrides,
  };
}

async function page(overrides) {
  const row = runRow(overrides);
  const env = {
    DB: {
      prepare() {
        return { bind: () => ({ first: async () => row }) };
      },
    },
  };
  const resp = await worker.fetch(new Request(`${BASE}/r/${row.id}`), env);
  assert.equal(resp.status, 200);
  return resp.text();
}

function tag(html, key) {
  const m = html.match(new RegExp(`<meta (?:property|name)="${key}" content="([^"]*)">`));
  return m ? m[1] : null;
}

test("a run with an author names them at the end of the title and the start of the description", async () => {
  const html = await page({ author_name: "Lab Rat" });
  assert.match(tag(html, "og:title"), /^52\.1 tok\/s · qwen3-8b · Q4_K_M · by Lab Rat$/);
  assert.equal(tag(html, "twitter:title"), tag(html, "og:title"));
  assert.match(tag(html, "og:description"), /^Recorded by Lab Rat on \S/);
  assert.equal(tag(html, "twitter:description"), tag(html, "og:description"));
  assert.match(html, /<title>52\.1 tok\/s · qwen3-8b · Q4_K_M · by Lab Rat — toktape<\/title>/);
});

test("the credit comes after the lab-note title", async () => {
  const html = await page({ author_name: "Lab Rat", title: "Spark at idle" });
  assert.equal(tag(html, "og:title"), "52.1 tok/s · qwen3-8b · Q4_K_M — Spark at idle · by Lab Rat");
});

test("a typed name is escaped in every tag", async () => {
  const html = await page({ author_name: "<b>x</b>" });
  for (const key of ["og:title", "twitter:title", "og:description", "twitter:description"]) {
    const v = tag(html, key);
    assert.ok(v.includes("&lt;b&gt;x&lt;/b&gt;"), `${key}: ${v}`);
    assert.ok(!v.includes("<b>"), `${key} carries raw markup: ${v}`);
  }
  assert.ok(!html.includes("<b>x</b>"), "raw name reached the page");
});

test("a run with no author, or an owned run with no name, keeps the head it had", async () => {
  for (const overrides of [{}, { owner_token: "t_journal", owned: 1 }]) {
    const html = await page(overrides);
    assert.equal(tag(html, "og:title"), "52.1 tok/s · qwen3-8b · Q4_K_M");
    assert.equal(tag(html, "twitter:title"), "52.1 tok/s · qwen3-8b · Q4_K_M");
    assert.equal(
      tag(html, "og:description"),
      "on dgx-spark with llama.cpp b6000. Replay the run in the browser, read the transcript, or download the record.",
    );
    assert.ok(!html.includes("Recorded by"));
    assert.ok(!html.includes("· by "));
  }
});

test("when the streams lead the sentence, the credit joins it with a comma", async () => {
  const html = await page({
    author_name: "Lab Rat",
    index_json: JSON.stringify({ schema: 1, model_id: "qwen3-8b", decode_per_sec: 52.1, sessions: 2, gpus_raw: ["NVIDIA GB10"] }),
  });
  assert.match(tag(html, "og:description"), /^Recorded by Lab Rat, 2 concurrent streams on NVIDIA GB10\. /);
});
