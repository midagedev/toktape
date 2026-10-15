// goFixed prints what Go's strconv.FormatFloat(v, 'f', d, 64) prints, so the
// page's figures read the same as the card's: an exact tie goes to even.
//
// Run: node --test test/*.test.mjs   (from web/)

import assert from "node:assert/strict";
import test from "node:test";

import { fmt } from "../src/page.js";
import { goFixed } from "../src/gofixed.js";
import { serveRunPage } from "../src/run.js";

test("an exact tie rounds to even, as Go does", () => {
  assert.equal(goFixed(0.25, 1), "0.2");
  assert.equal(goFixed(0.75, 1), "0.8");
  assert.equal(goFixed(2.5, 0), "2");
  assert.equal(goFixed(3.5, 0), "4");
  assert.equal(goFixed(-0.25, 1), "-0.2");
});

test("fmt uses that tie rule for the figures the page shares with the card", () => {
  assert.equal(fmt(0.25), "0.2");
  assert.equal(fmt(0.75), "0.8");
  assert.equal(fmt(150.5), "150");
  assert.equal(fmt(0), "?");
});

test("bits per weight uses that tie rule and keeps at most two decimals", async () => {
  const row = {
    id: "abcdefgh",
    created_at: "2026-09-19T07:00:00Z",
    private: 0,
    owned: 1,
    card_key: null,
    tape_ext: ".toktape",
    tape_bytes: 100,
  };
  async function quant(bits) {
    const env = {
      DB: {
        prepare: () => ({
          bind: () => ({
            first: async () => ({
              ...row,
              index_json: JSON.stringify({ schema: 1, quant_raw: "Q6_K", quant_bits: bits }),
            }),
          }),
        }),
      },
    };
    const html = await (await serveRunPage("abcdefgh", env, "https://tape.midagedev.com")).text();
    const cell = /quantisation<\/td><td class="v mono">([^<]*)/.exec(html);
    assert.ok(cell, "no quantisation row");
    return cell[1];
  }
  assert.equal(await quant(6.5625), "Q6_K · 6.56 bit");
  assert.equal(await quant(6.125), "Q6_K · 6.12 bit");
  assert.equal(await quant(4.5), "Q6_K · 4.5 bit");
});

test("a value that only looks like a tie rounds by its exact value", () => {
  // 0.15 and 1.45 are stored just under the tie (0.1499…, 1.4499…): Go prints 0.1 and 1.4.
  assert.equal(goFixed(0.15, 1), "0.1");
  assert.equal(goFixed(1.45, 1), "1.4");
  assert.equal(goFixed(11164188672 / 2 ** 30, 1), "10.4");
  assert.equal(goFixed(141.918, 0), "142");
});
