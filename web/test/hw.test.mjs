// The hub page per GPU model (TTP-204): /hw/<id>, routed through
// src/index.js with an in-memory D1 fake that understands just the shapes
// the page sends. Where the fake cannot express the SQL, the test asserts
// the SQL and its arguments instead, the way the search tests do.
//
// Run: node --test test/*.test.mjs   (from web/)

import assert from "node:assert/strict";
import test from "node:test";

import worker from "../src/index.js";

const BASE = "https://tape.midagedev.com";

function row(id, overrides = {}) {
  return {
    id,
    created_at: "2026-10-01T07:00:00Z",
    private: 0,
    engine_kind: "llama.cpp",
    engine_version: "b6000",
    model_id: "qwen3-8b",
    decode_per_sec: 40,
    gpus_raw: "NVIDIA GB10",
    gpu_ids: JSON.stringify(["gb10"]),
    gpu_count: 1,
    card_key: null,
    tape_ext: ".toktape",
    ...overrides,
  };
}

// A D1 that answers the page's four query shapes from an array of rows and
// records every statement it was handed.
function fakeDB(rows, calls = []) {
  const ids = (r) => JSON.parse(r.gpu_ids || "[]");
  const pick = (sql, args) => {
    // Membership: the one place a bound argument names a card.
    if (sql.includes("json_each(runs.gpu_ids) WHERE value = ?")) {
      const want = args.find((a) => typeof a === "string" && ids(rows[0] || row("x")).concat(...rows.map(ids)).includes(a));
      return rows.filter((r) => want !== undefined && ids(r).includes(want));
    }
    return rows;
  };
  return {
    prepare(sql) {
      return {
        bind: (...args) => {
          calls.push({ sql, args });
          const hit = () => pick(sql, args);
          return {
            all: async () => {
              if (/SELECT DISTINCT j\.value/.test(sql)) {
                return { results: [...new Set(rows.flatMap(ids))].map((v) => ({ v })) };
              }
              if (/owner_token AS h/.test(sql)) return { results: [] };
              if (sql.includes("json_each(runs.gpu_ids) j")) {
                const n = new Map();
                for (const r of hit()) for (const v of new Set(ids(r))) n.set(v, (n.get(v) || 0) + 1);
                return { results: [...n].map(([v, c]) => ({ v, n: c })) };
              }
              const g = /GROUP BY (\w+)/.exec(sql);
              if (g) {
                const n = new Map();
                for (const r of hit()) if (r[g[1]] != null) n.set(r[g[1]], (n.get(r[g[1]]) || 0) + 1);
                return { results: [...n].map(([v, c]) => ({ v, n: c })).sort((a, b) => b.n - a.n) };
              }
              return { results: hit() };
            },
            first: async () => {
              if (/COUNT\(\*\)/.test(sql)) return { n: hit().length };
              return hit()[0] || null;
            },
          };
        },
      };
    },
  };
}

async function get(path, rows, calls) {
  const resp = await worker.fetch(new Request(`${BASE}${path}`), { DB: fakeDB(rows, calls) });
  return { resp, html: resp.status === 200 ? await resp.text() : await resp.text() };
}

function tag(html, key) {
  const m = html.match(new RegExp(`<meta (?:property|name)="${key}" content="([^"]*)">`));
  return m ? m[1] : null;
}

const GB10_A = row("aaaaaaaa", { engine_kind: "llama.cpp" });
const GB10_B = row("bbbbbbbb", { engine_kind: "bloomery", model_id: "qwen3-8b" });
const R3090 = row("cccccccc", {
  gpus_raw: "NVIDIA GeForce RTX 3090",
  gpu_ids: JSON.stringify(["rtx-3090"]),
});

test("/hw/gb10 lists exactly the GB10 rows, names the card and says how many", async () => {
  const { resp, html } = await get("/hw/gb10", [GB10_A, GB10_B, R3090]);
  assert.equal(resp.status, 200);
  assert.ok(html.includes('href="/r/aaaaaaaa"'));
  assert.ok(html.includes('href="/r/bbbbbbbb"'));
  assert.ok(!html.includes("cccccccc"), "an RTX 3090 row is on the GB10 page");
  assert.match(html, /<h1>NVIDIA GB10<\/h1>/);
  assert.equal(tag(html, "og:title"), "2 tapes on NVIDIA GB10 — toktape");
  assert.equal(tag(html, "twitter:title"), "2 tapes on NVIDIA GB10 — toktape");
  assert.match(
    tag(html, "og:description"),
    /^(llama\.cpp 1 · bloomery 1|bloomery 1 · llama\.cpp 1) · qwen3-8b 2\. Every run replays in the browser\.$/,
  );
  assert.equal(tag(html, "twitter:card"), "summary");
  assert.equal(tag(html, "og:image"), null);
  assert.equal(tag(html, "robots"), null, "the hub is indexable");
  assert.ok(html.includes(`<link rel="canonical" href="${BASE}/hw/gb10">`));
});

test("one tape is singular", async () => {
  const { html } = await get("/hw/gb10", [GB10_A]);
  assert.equal(tag(html, "og:title"), "1 tape on NVIDIA GB10 — toktape");
});

test("the page sends the gpu membership filter and ignores a gpu in the query string", async () => {
  const calls = [];
  await get("/hw/gb10?gpu=rtx-3090", [GB10_A, R3090], calls);
  const listing = calls.find((c) => c.sql.trimStart().startsWith("SELECT id,"));
  assert.ok(listing, "no listing query");
  assert.ok(listing.sql.includes("json_each(runs.gpu_ids) WHERE value = ?"));
  assert.ok(listing.args.includes("gb10"));
  assert.ok(!listing.args.includes("rtx-3090"));
});

test("a mixed rig is on the page of each of its cards", async () => {
  const mixed = row("dddddddd", {
    gpus_raw: "NVIDIA GeForce RTX 3090 / NVIDIA RTX A6000",
    gpu_ids: JSON.stringify(["rtx-3090", "rtx-a6000"]),
    gpu_count: 2,
  });
  for (const id of ["rtx-3090", "rtx-a6000"]) {
    const { resp, html } = await get(`/hw/${id}`, [mixed, GB10_A]);
    assert.equal(resp.status, 200);
    assert.ok(html.includes('href="/r/dddddddd"'), `${id}: the mixed rig is missing`);
    assert.ok(!html.includes("aaaaaaaa"));
  }
  const { html } = await get("/hw/rtx-a6000", [mixed]);
  assert.match(html, /<h1>NVIDIA RTX A6000<\/h1>/);
});

test("the product alias redirects and keeps the query string", async () => {
  const { resp } = await get("/hw/dgx-spark?engine=vllm&sort=oldest", []);
  assert.equal(resp.status, 301);
  assert.equal(resp.headers.get("Location"), "/hw/gb10?engine=vllm&sort=oldest");
  const bare = await get("/hw/dgx-spark", []);
  assert.equal(bare.resp.status, 301);
  assert.equal(bare.resp.headers.get("Location"), "/hw/gb10");
});

test("an id outside [a-z0-9-] is a 404", async () => {
  for (const p of ["/hw/GB10", "/hw/../x", "/hw/a%20b", "/hw/", "/hw/a/b"]) {
    const { resp } = await get(p, [GB10_A]);
    assert.equal(resp.status, 404, p);
  }
});

test("a raw GPU name is escaped wherever it lands", async () => {
  const evil = row("eeeeeeee", { gpus_raw: "<b>x</b>", gpu_ids: JSON.stringify(["evil"]) });
  const { html } = await get("/hw/evil", [evil]);
  assert.ok(!html.includes("<b>x</b>"), "raw name reached the page");
  assert.ok(html.includes("&lt;b&gt;x&lt;/b&gt;"));
  for (const key of ["og:title", "twitter:title"]) {
    assert.ok(!tag(html, key).includes("<b>"), key);
  }
});

test("with no rows the page says so and points at the way to ask for a tape", async () => {
  const { resp, html } = await get("/hw/gb10", []);
  assert.equal(resp.status, 200);
  assert.ok(html.includes("No tape on this card yet."));
  assert.ok(
    html.includes('href="https://github.com/midagedev/toktape/blob/main/docs/publish.md#asking-someone-else-for-a-tape"'),
  );
  assert.match(html, /<h1>gb10<\/h1>/);
  assert.equal(tag(html, "og:title"), "0 tapes on gb10 — toktape");
});

test("the GPU chips on a row link to the hub, keeping the other filters", async () => {
  const { html } = await get("/?engine=llama.cpp", [GB10_A]);
  assert.ok(html.includes('href="/hw/gb10?engine=llama.cpp"'), "the gpu chip does not link to the hub");
  assert.ok(!html.includes("gpu=gb10"));
});

test("the run page links each card that has an id", async () => {
  const idx = { schema: 1, model_id: "qwen3-8b", gpus_raw: ["NVIDIA GB10", "Mystery <i>card</i>"], gpu_ids: ["gb10"] };
  // One id for two names: the pairing is unknowable, so nothing is linked.
  let html = await runPageWith(idx);
  assert.ok(!html.includes('href="/hw/'), "linked despite an unpairable list");
  assert.ok(html.includes("Mystery &lt;i&gt;card&lt;/i&gt;"));

  html = await runPageWith({ ...idx, gpus_raw: ["NVIDIA GB10"], gpu_ids: ["gb10"] });
  assert.ok(html.includes('<a href="/hw/gb10">NVIDIA GB10</a>'));
});

async function runPageWith(idx) {
  const r = {
    id: "abcdefgh",
    created_at: "2026-09-19T07:00:00Z",
    private: 0,
    owned: 0,
    card_key: null,
    tape_ext: ".toktape",
    tape_bytes: 100,
    index_json: JSON.stringify(idx),
  };
  const env = { DB: { prepare: () => ({ bind: () => ({ first: async () => r }) }) } };
  const resp = await worker.fetch(new Request(`${BASE}/r/abcdefgh`), env);
  assert.equal(resp.status, 200);
  return resp.text();
}

test("the sitemap lists one URL per GPU id among public runs", async () => {
  const calls = [];
  const db = fakeDB([GB10_A, GB10_B, R3090], calls);
  const resp = await worker.fetch(new Request(`${BASE}/sitemap.xml`), { DB: db });
  const xml = await resp.text();
  assert.equal([...xml.matchAll(/<loc>[^<]*\/hw\/gb10<\/loc>/g)].length, 1);
  assert.equal([...xml.matchAll(/<loc>[^<]*\/hw\/rtx-3090<\/loc>/g)].length, 1);
  const q = calls.find((c) => c.sql.includes("json_each"));
  assert.ok(q && q.sql.includes("private = 0"), "the query is not public-only");
});
