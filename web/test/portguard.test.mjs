// The port guard: a local-server gate must not run against somebody else's
// server (TTP-175, 2026-09-21).
//
// What happened: an orphaned workerd from an interrupted run held
// 127.0.0.1:8787 for two hours. web/check.sh started its own `wrangler dev`,
// which died with `::bind(): Address already in use`, then probed /healthz —
// which the orphan answered. The gate ran its whole suite against a database
// view two hours stale and reported "filtering by size=0 missed the figures
// row" for a row it had inserted and which was really in the store. A gate
// that cannot tell its own server from a stranger's does not fail, it lies.
//
// FAIL-first: before require-free-port.sh existed there was nothing to run
// here, and the behaviour it pins was the defect — check.sh would carry on.
//
// Run: node --test test/*.test.mjs   (from web/; a bare test/
// directory is resolved as a module by node 24 and does not run)

import assert from "node:assert/strict";
import { test } from "node:test";
import { createServer } from "node:net";
import { execFile } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const guard = join(here, "..", "require-free-port.sh");

// The script's own exit code and stderr are the contract; nothing here
// inspects its innards.
function run(port) {
  return new Promise((resolve) => {
    execFile(guard, [String(port), "the test"], (err, stdout, stderr) =>
      resolve({ code: err ? err.code : 0, stdout, stderr }));
  });
}

// A listener on an ephemeral port, so this test never collides with a real
// dev server the way the gate did.
function listenSomewhere() {
  return new Promise((resolve) => {
    const srv = createServer(() => {});
    srv.listen(0, "127.0.0.1", () => resolve({ srv, port: srv.address().port }));
  });
}

test("a held port stops the gate before it starts", async () => {
  const { srv, port } = await listenSomewhere();
  try {
    const { code, stderr } = await run(port);
    assert.notEqual(code, 0, "the guard let the gate run on a held port");
    assert.match(stderr, new RegExp(`127\\.0\\.0\\.1:${port} is already held`));
    // The message has to be actionable: which process, and how to end it.
    assert.match(stderr, /kill \d+/, "the guard does not say what to kill");
    assert.match(stderr, /ELAPSED/, "the guard does not say how old the holder is");
    // And a way out that is not killing someone else's work.
    assert.match(stderr, new RegExp(`PORT=${port + 1}`));
  } finally {
    srv.close();
  }
});

test("a free port is not an obstacle", async () => {
  // Taken and released: a port that WAS held must not stay refused, or the
  // guard would outlive the problem it reports.
  const { srv, port } = await listenSomewhere();
  await new Promise((r) => srv.close(r));
  const { code, stderr } = await run(port);
  assert.equal(code, 0, `the guard refused a free port: ${stderr}`);
  assert.equal(stderr, "");
});
