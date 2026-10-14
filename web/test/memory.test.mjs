// The run page's memory row, in the card's wording
// (internal/card/card.go vramLine, format.go formatGiB / formatGiBNum).
// GiB is 2^30 with one decimal. Unknown is "?".
//
// Run: node --test test/*.test.mjs   (from web/)

import assert from "node:assert/strict";
import test from "node:test";

import { memoryRow, serveRunPage } from "../src/run.js";

const EXAMPLE = {
  gpu_mem: [{ index: 0, used_bytes: 11164188672, vram_bytes: 12884901888 }],
  rss_bytes: 12456681472,
  rss_file_bytes: 11947323392,
  rss_anon_bytes: 263991296,
};

test("the memory row prints the card's GiB line", () => {
  assert.equal(
    memoryRow(EXAMPLE),
    "GPU0 10.4/12.0 GiB · host RSS 11.6 GiB (file 11.1 / anon 0.2)",
  );
});

test("a memory row with none of the fields is ?", () => {
  assert.equal(memoryRow({ schema: 1, model_raw: "x" }), "?");
});

test("a gpu with no capacity prints used GiB only", () => {
  assert.equal(memoryRow({ gpu_mem: [{ index: 0, used_bytes: 11164188672 }] }), "GPU0 10.4 GiB");
});

test("the run page puts memory after gpus", async () => {
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
      gpus_raw: ["NVIDIA GeForce RTX 3060"],
      ...EXAMPLE,
    }),
  };
  const env = { DB: { prepare: () => ({ bind: () => ({ first: async () => row }) }) } };
  const resp = await serveRunPage("abcdefgh", env, "https://tape.midagedev.com");
  const html = await resp.text();
  const gpus = html.indexOf(">gpus<");
  const memory = html.indexOf(">memory<");
  assert.ok(gpus !== -1 && memory > gpus, "memory does not follow gpus");
  assert.ok(html.includes("GPU0 10.4/12.0 GiB · host RSS 11.6 GiB (file 11.1 / anon 0.2)"));
});
