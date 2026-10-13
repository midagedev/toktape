// The 120s feed cache. A search stays on D1; a feed is stored and a second
// GET does not query. Publish, delete, and a public edit drop the canonical
// keys. Cursor URLs are not in that set.
//
// Run: node --test test/*.test.mjs   (from web/)

import assert from "node:assert/strict";
import test from "node:test";

import { deleteRun } from "../src/del.js";
import { editRun } from "../src/edit.js";
import {
  FEED_CACHE_CONTROL,
  feedDeleteURLs,
  gpuIdsOf,
  isCacheableFeed,
  isSearch,
} from "../src/feedcache.js";
import { sha256Hex } from "../src/ids.js";
import worker from "../src/index.js";

const ORIGIN = "https://tape.midagedev.com";

function u(path) {
  return new URL(path, ORIGIN);
}

test("a feed is the default list, and a search is q, a filter, a non-default sort, or scope=mine", () => {
  assert.equal(isCacheableFeed(u("/")), true);
  assert.equal(isCacheableFeed(u("/?sort=newest")), true);
  assert.equal(isCacheableFeed(u("/?sort=")), true);
  assert.equal(isCacheableFeed(u("/?cursor=abc")), true);
  assert.equal(isCacheableFeed(u("/?limit=10")), true);
  assert.equal(isCacheableFeed(u("/sitemap.xml")), true);
  assert.equal(isCacheableFeed(u("/api/v1/runs")), true);
  assert.equal(isCacheableFeed(u("/hw/gb10")), true);
  assert.equal(isCacheableFeed(u("/hw/gb10?gpu=gb10")), true);
  assert.equal(isCacheableFeed(u("/hw/gb10?cursor=abc")), true);
  assert.equal(isCacheableFeed(u("/u/ab")), true);
  assert.equal(isCacheableFeed(u("/u/ab.json")), true);

  assert.equal(isSearch(u("/?q=glm")), true);
  assert.equal(isCacheableFeed(u("/?q=glm")), false);
  assert.equal(isCacheableFeed(u("/?q=")), true);
  assert.equal(isCacheableFeed(u("/?engine=llama.cpp")), false);
  assert.equal(isCacheableFeed(u("/?engine=")), true);
  assert.equal(isCacheableFeed(u("/?sort=decode")), false);
  assert.equal(isCacheableFeed(u("/?sort=nope")), false);
  assert.equal(isCacheableFeed(u("/?scope=mine")), false);
  assert.equal(isCacheableFeed(u("/hw/gb10?model=qwen")), false);
  assert.equal(isCacheableFeed(u("/u/ab?gpu=gb10")), false);
  assert.equal(isCacheableFeed(u("/u/ab.json?q=x")), false);
  assert.equal(isCacheableFeed(u("/r/abcd1234")), false);
  assert.equal(isCacheableFeed(u("/healthz")), false);
});

test("invalidation names the front page, the sitemap, the owner, and each card", () => {
  assert.deepEqual(feedDeleteURLs(ORIGIN, { owner: "ab", gpus: ["gb10", "gb10", "NOPE", "a6000"] }), [
    `${ORIGIN}/`,
    `${ORIGIN}/sitemap.xml`,
    `${ORIGIN}/u/ab`,
    `${ORIGIN}/u/ab.json`,
    `${ORIGIN}/hw/gb10`,
    `${ORIGIN}/hw/a6000`,
  ]);
  assert.deepEqual(feedDeleteURLs(ORIGIN, { owner: null, gpus: '["gb10"]' }), [
    `${ORIGIN}/`,
    `${ORIGIN}/sitemap.xml`,
    `${ORIGIN}/hw/gb10`,
  ]);
  assert.deepEqual(gpuIdsOf("not-json"), []);
});

function memCache() {
  const store = new Map();
  return {
    store,
    async match(req) {
      const hit = store.get(req.url);
      return hit ? hit.clone() : undefined;
    },
    async put(req, resp) {
      store.set(req.url, resp.clone());
    },
    async delete(req) {
      const had = store.has(req.url);
      store.delete(req.url);
      return had;
    },
  };
}

function listingDB(calls) {
  return {
    prepare(sql) {
      return {
        bind() {
          calls.push(sql);
          return {
            all: async () => ({ results: [] }),
            first: async () => (/COUNT\(\*\)/.test(sql) ? { n: 0 } : null),
            run: async () => ({ success: true }),
          };
        },
      };
    },
  };
}

test("a default feed is stored and the second GET does not touch D1", async () => {
  const cache = memCache();
  const prev = globalThis.caches;
  globalThis.caches = { default: cache };
  try {
    const calls = [];
    const env = { DB: listingDB(calls) };
    const first = await worker.fetch(new Request(`${ORIGIN}/`), env, {});
    assert.equal(first.status, 200);
    assert.equal(first.headers.get("Cache-Control"), FEED_CACHE_CONTROL);
    const n = calls.length;
    assert.ok(n > 0);
    calls.length = 0;
    const second = await worker.fetch(new Request(`${ORIGIN}/`), env, {});
    assert.equal(second.status, 200);
    assert.equal(calls.length, 0);
    assert.match(await second.text(), /published runs|Every run on this page/);
  } finally {
    globalThis.caches = prev;
  }
});

test("a search is not stored", async () => {
  const cache = memCache();
  const prev = globalThis.caches;
  globalThis.caches = { default: cache };
  try {
    const calls = [];
    const env = { DB: listingDB(calls) };
    const req = () => new Request(`${ORIGIN}/?q=glm`);
    const first = await worker.fetch(req(), env, {});
    assert.equal(first.status, 200);
    assert.equal(first.headers.get("Cache-Control"), "no-store");
    const n = calls.length;
    await worker.fetch(req(), env, {});
    assert.equal(calls.length, n * 2);
    assert.equal(cache.store.size, 0);
  } finally {
    globalThis.caches = prev;
  }
});

test("the sitemap is a feed and loses the hour-long header", async () => {
  const cache = memCache();
  const prev = globalThis.caches;
  globalThis.caches = { default: cache };
  try {
    const calls = [];
    const env = { DB: listingDB(calls) };
    const res = await worker.fetch(new Request(`${ORIGIN}/sitemap.xml`), env, {});
    assert.equal(res.status, 200);
    assert.equal(res.headers.get("Cache-Control"), FEED_CACHE_CONTROL);
    assert.match(await res.text(), /<urlset/);
    const n = calls.length;
    await worker.fetch(new Request(`${ORIGIN}/sitemap.xml`), env, {});
    assert.equal(calls.length, n);
  } finally {
    globalThis.caches = prev;
  }
});

test("deleting a public run drops the feed keys and a private run does not", async () => {
  const token = "dt_testtoken";
  const hash = await sha256Hex(token);
  const deleted = [];
  const prev = globalThis.caches;
  globalThis.caches = {
    default: {
      async match() { return undefined; },
      async put() {},
      async delete(req) { deleted.push(req.url); return true; },
    },
  };
  try {
    const envFor = (row) => ({
      DB: {
        prepare(sql) {
          return {
            bind() {
              return {
                first: async () => (sql.includes("FROM runs") ? row : null),
                run: async () => ({ success: true }),
              };
            },
          };
        },
      },
      TAPES: { async delete() {} },
    });
    const request = new Request(`${ORIGIN}/api/v1/runs/abcd1234`, {
      method: "DELETE",
      headers: { Authorization: `Bearer ${token}` },
    });
    const pub = await deleteRun("abcd1234", request, envFor({
      tape_key: "runs/abcd1234/run.toktape",
      card_key: null,
      owner_token: "ab",
      delete_hash: hash,
      private: 0,
      gpu_ids: '["gb10"]',
    }));
    assert.equal(pub.status, 204);
    assert.deepEqual(deleted, [
      `${ORIGIN}/`,
      `${ORIGIN}/sitemap.xml`,
      `${ORIGIN}/u/ab`,
      `${ORIGIN}/u/ab.json`,
      `${ORIGIN}/hw/gb10`,
    ]);
    deleted.length = 0;
    const priv = await deleteRun("abcd1234", request, envFor({
      tape_key: "runs/abcd1234/run.toktape",
      card_key: null,
      owner_token: "ab",
      delete_hash: hash,
      private: 1,
      gpu_ids: '["gb10"]',
    }));
    assert.equal(priv.status, 204);
    assert.deepEqual(deleted, []);
  } finally {
    globalThis.caches = prev;
  }
});

test("a public edit drops the feed keys, including a card the index just left", async () => {
  const token = "jt_testtoken";
  const hash = await sha256Hex(token);
  const deleted = [];
  const prev = globalThis.caches;
  globalThis.caches = {
    default: {
      async match() { return undefined; },
      async put() {},
      async delete(req) { deleted.push(req.url); return true; },
    },
  };
  const row = {
    id: "abcd1234",
    created_at: "2026-10-01T00:00:00Z",
    private: 0,
    owner_token: "ab",
    gpu_ids: '["gb10"]',
    tape_ext: ".toktape",
    tape_bytes: 12,
    card_key: null,
    owned: 1,
    index_json: "{}",
    author_name: null,
    author_link: null,
    avatar_key: null,
    title: "t",
    note: null,
  };
  const env = {
    DB: {
      prepare(sql) {
        return {
          bind() {
            return {
              first: async () => {
                if (sql.includes("FROM tokens")) return { id: "ab", hash };
                if (sql.includes("FROM runs")) return row;
                return null;
              },
              run: async () => ({ success: true }),
            };
          },
        };
      },
    },
  };
  try {
    const res = await editRun("abcd1234", new Request(`${ORIGIN}/api/v1/runs/abcd1234`, {
      method: "PATCH",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify({ title: "renamed", index: { schema: 1, gpu_ids: ["a6000"] } }),
    }), env);
    assert.equal(res.status, 200);
    assert.ok(deleted.includes(`${ORIGIN}/hw/gb10`));
    assert.ok(deleted.includes(`${ORIGIN}/hw/a6000`));
    assert.ok(deleted.includes(`${ORIGIN}/u/ab.json`));
  } finally {
    globalThis.caches = prev;
  }
});
