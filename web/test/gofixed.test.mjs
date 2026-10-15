// goFixed prints what Go's strconv.FormatFloat(v, 'f', d, 64) prints, so the
// page's figures read the same as the card's: an exact tie goes to even.
//
// Run: node --test test/*.test.mjs   (from web/)

import assert from "node:assert/strict";
import test from "node:test";

import { goFixed } from "../src/gofixed.js";

test("an exact tie rounds to even, as Go does", () => {
  assert.equal(goFixed(0.25, 1), "0.2");
  assert.equal(goFixed(0.75, 1), "0.8");
  assert.equal(goFixed(2.5, 0), "2");
  assert.equal(goFixed(3.5, 0), "4");
  assert.equal(goFixed(-0.25, 1), "-0.2");
});

test("a value that only looks like a tie rounds by its exact value", () => {
  // 0.15 and 1.45 are stored just under the tie (0.1499…, 1.4499…): Go prints 0.1 and 1.4.
  assert.equal(goFixed(0.15, 1), "0.1");
  assert.equal(goFixed(1.45, 1), "1.4");
  assert.equal(goFixed(11164188672 / 2 ** 30, 1), "10.4");
  assert.equal(goFixed(141.918, 0), "142");
});
