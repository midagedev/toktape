// Unit tests for the worker's card-replace route (src/card.js), routed
// through src/index.js the way a request really arrives.
//
// web/ has no test runner and keeps to no dependencies by choice
// (player/smoke.mjs makes the same argument), so this is node:test —
// built into the Node that already builds the player — with in-memory
// D1/R2 fakes. The deep checks (a real publish, a real PNG round trip)
// stay in check.sh, which runs the real binary against a real workerd;
// what only a unit test can pin is the refusal ladder — the 401/404/403
// order the edit path set, and the size and signature refusals — without
// writing anything to a service.
//
// Run: node --test test/   (from web/)

import assert from "node:assert/strict";
import test from "node:test";

import worker from "../src/index.js";
import { sha256Hex } from "../src/ids.js";

// The PNG signature, the same eight bytes upload.js checks.
const PNG_SIG = Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);

// A body that passes the signature check: the eight signature bytes and a
// little more, so a size refusal is about size and a type refusal about type.
function pngBody(extra = 16) {
  return new Uint8Array([...PNG_SIG, ...new Array(extra).fill(0)]);
}

const BASE = "https://tape.midagedev.com";

// The two stores the route touches, shaped like the statements it makes.
// An unknown statement throws rather than returning undefined rows, so a
// handler change fails these tests loudly instead of passing vacuously.
function fakeEnv({ runs, tokens }) {
  const objects = new Map();
  const db = {
    async first(sql, args) {
      if (sql.includes("SELECT owner_token FROM runs")) {
        return runs.find((r) => r.id === args[0]) || null;
      }
      if (sql.includes("SELECT id FROM tokens")) {
        const id = tokens[args[0]];
        return id ? { id } : null;
      }
      if (sql.includes("owner_token IS NOT NULL AS owned")) {
        const row = runs.find((r) => r.id === args[0]);
        if (!row) return null;
        return { ...row, owned: row.owner_token === null ? 0 : 1 };
      }
      throw new Error(`card.test.mjs: unmocked first() ${JSON.stringify(sql)}`);
    },
    async run(sql, args) {
      if (sql.includes("UPDATE runs SET card_key")) {
        const row = runs.find((r) => r.id === args[1]);
        if (row && row.card_key === null) row.card_key = args[0];
        return;
      }
      throw new Error(`card.test.mjs: unmocked run() ${JSON.stringify(sql)}`);
    },
  };
  return {
    objects,
    env: {
      DB: {
        prepare(sql) {
          return {
            bind(...args) {
              return {
                first: () => db.first(sql, args),
                run: () => db.run(sql, args),
              };
            },
          };
        },
      },
      TAPES: {
        put(key, value, opts) {
          objects.set(key, { value, opts });
        },
        get(key) {
          return objects.get(key) || null;
        },
        async delete(key) {
          objects.delete(key);
        },
      },
    },
  };
}

// One published run owned by the journal token, with a card already.
async function ownedRun(overrides = {}) {
  const hash = await sha256Hex("tk_journal");
  const row = {
    id: "abcdefgh",
    created_at: "2026-09-19T07:00:00Z",
    private: 0,
    owner_token: "t_journal",
    card_key: "runs/abcdefgh/card.png",
    tape_key: "runs/abcdefgh/run.toktape",
    tape_ext: ".toktape",
    tape_bytes: 25600,
    author_name: null,
    author_link: null,
    avatar_key: null,
    title: null,
    note: null,
    index_json: JSON.stringify({ schema: 1, model_raw: "test" }),
    ...overrides,
  };
  const fake = fakeEnv({ runs: [row], tokens: { [hash]: "t_journal" } });
  return { ...fake, row };
}

function put(env, { path = "/api/v1/runs/abcdefgh/card", token = "tk_journal", body = pngBody(), headers = {} } = {}) {
  const h = { "Content-Type": "image/png", ...headers };
  if (token) h.Authorization = `Bearer ${token}`;
  return worker.fetch(new Request(BASE + path, { method: "PUT", headers: h, body }), env);
}

test("the owner replaces the card and is answered the run", async () => {
  const { env, objects } = await ownedRun();
  const bytes = pngBody(64);
  const resp = await put(env, { body: bytes });
  if (resp.status !== 200) assert.fail(`status ${resp.status}: ${await resp.text()}`);
  const stored = objects.get("runs/abcdefgh/card.png");
  assert.ok(stored, "no object at runs/<id>/card.png");
  assert.deepEqual(Buffer.from(stored.value).subarray(0, bytes.length), Buffer.from(bytes), "the stored object is not the sent bytes");
  // The same metadata the upload path writes, so a replaced card is served
  // like a first one — immutable included.
  assert.equal(stored.opts.httpMetadata.contentType, "image/png");
  assert.equal(stored.opts.httpMetadata.cacheControl, "public, max-age=31536000, immutable");
  const out = await resp.json();
  assert.equal(out.id, "abcdefgh");
  assert.equal(out.card, "/r/abcdefgh.png");
});

test("a run published without a card gains one", async () => {
  const { env, row, objects } = await ownedRun({ card_key: null });
  const resp = await put(env);
  if (resp.status !== 200) assert.fail(`status ${resp.status}: ${await resp.text()}`);
  assert.equal(row.card_key, "runs/abcdefgh/card.png", "card_key stayed NULL");
  assert.ok(objects.has("runs/abcdefgh/card.png"));
  const out = await resp.json();
  assert.equal(out.card, "/r/abcdefgh.png");
});

test("no Authorization is a 401 that says nothing about the run", async () => {
  const { env } = await ownedRun();
  const resp = await put(env, { token: null });
  assert.equal(resp.status, 401);
  const out = await resp.json();
  assert.match(out.error, /replacing a run's card/);
  assert.match(out.error, /Bearer/);
});

test("no token is a 401 even for a run that does not exist", async () => {
  // The edit path's order: the missing key is answered before the run is
  // looked up, so the route's existence probe learns nothing.
  const { env } = await ownedRun();
  const resp = await put(env, { path: "/api/v1/runs/notaruno/card", token: null });
  assert.equal(resp.status, 401);
});

test("a token that does not own the run is a 403", async () => {
  // A second journal token this service issued, for a journal that is not
  // this run's: recognised, and still not the owner.
  const hash = await sha256Hex("tk_other");
  const row = { id: "abcdefgh", owner_token: "t_journal", card_key: "runs/abcdefgh/card.png" };
  const { env } = fakeEnv({ runs: [row], tokens: { [hash]: "t_other" } });
  const resp = await put(env);
  assert.equal(resp.status, 403);
  const out = await resp.json();
  assert.match(out.error, /journal token/);
});

test("a run with no owner cannot have its card replaced", async () => {
  const { env } = await ownedRun({ owner_token: null });
  const resp = await put(env);
  assert.equal(resp.status, 403);
});

test("an unknown id is a 404", async () => {
  const { env } = await ownedRun();
  const resp = await put(env, { path: "/api/v1/runs/notaruno/card" });
  assert.equal(resp.status, 404);
});

test("a body that is not a PNG is a 400", async () => {
  const { env } = await ownedRun();
  const resp = await put(env, { body: new Uint8Array([0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08]) });
  assert.equal(resp.status, 400);
  const out = await resp.json();
  assert.match(out.error, /PNG/);
});

test("a body over MAX_CARD_BYTES is a 413", async () => {
  const { env } = await ownedRun();
  // One mebibyte plus one byte, signature-prefixed so only the size refuses.
  const body = new Uint8Array(1024 * 1024 + 9);
  body.set(PNG_SIG);
  const resp = await put(env, { body });
  assert.equal(resp.status, 413);
  const out = await resp.json();
  assert.match(out.error, /larger than/);
});

test("the card route answers non-PUT methods with 405 and Allow", async () => {
  const { env } = await ownedRun();
  const resp = await worker.fetch(new Request(BASE + "/api/v1/runs/abcdefgh/card"), env);
  assert.equal(resp.status, 405);
  assert.equal(resp.headers.get("Allow"), "PUT");
});
