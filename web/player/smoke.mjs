// Does the player actually run, on a real recorded run?
//
// The size gate above proves the file is small enough to send. This proves it
// is worth sending: assets/hero.tape is a real recording — the one the
// README's card is drawn from — and it goes through the same load() and
// frame() a browser calls. A wasm binary that builds and exports nothing
// usable looks exactly like one that works, right up until a page tries it.
//
// No dependencies, by choice. web/ has one npm dependency (wrangler) and the
// argument for keeping it at one is that every other one is a thing that can
// break a build nobody changed.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const dist = join(here, "dist", "player");

let failures = 0;
function check(name, ok, detail) {
  console.log(`  ${ok ? "ok  " : "FAIL"}  ${name}${detail ? " — " + detail : ""}`);
  if (!ok) failures++;
}

// wasm_exec.js is a script, not a module, and it installs globalThis.Go.
const { createRequire } = await import("node:module");
const require = createRequire(import.meta.url);
require(join(dist, "wasm_exec.js"));

const go = new globalThis.Go();
const wasm = await WebAssembly.instantiate(readFileSync(join(dist, "toktape.wasm")), go.importObject);
// Not awaited: main() ends in `select {}` and never returns, which is what
// keeps the exported functions callable. Awaiting it would hang here.
go.run(wasm.instance);

const api = globalThis.toktape;
check("the module exported toktape", !!api, api ? `version ${api.version}` : "nothing on globalThis");
if (!api) process.exit(1);

const bytes = readFileSync(join(here, "..", "..", "assets", "hero.tape"));
const loaded = api.load(new Uint8Array(bytes));
check("hero.tape loads", loaded.ok === true, loaded.error || `${bytes.length} bytes in`);
check("the run has a duration", loaded.durationMs > 0, `${loaded.durationMs} ms`);
check("the run has streams", loaded.streams > 0, `${loaded.streams}`);

// A frame at the start, the middle and the end. The middle is the one that
// matters: it is the only one that cannot be right by accident.
const COLS = 120;
const ROWS = 36;
for (const [label, at] of [
  ["start", 0],
  ["middle", Math.floor(loaded.durationMs / 2)],
  ["end", loaded.durationMs],
]) {
  const frame = api.frame(at, COLS, ROWS);
  const lines = frame.split("\n");
  check(`frame at ${label} is ${ROWS} lines`, lines.length === ROWS, `${lines.length} lines, ${frame.length} chars`);
}

// The frame carries the terminal's own colour. Without this the player would
// render the right text in the wrong medium — and it would still be exactly
// 36 lines, so the check above would not notice.
const mid = api.frame(Math.floor(loaded.durationMs / 2), COLS, ROWS);
check("the frame is styled", mid.includes("["), `${(mid.match(/\[/g) || []).length} escapes`);

// The clip ends on the result card, held for render.CardHold, exactly as a
// GIF does. The first build stopped at the last token and the card never
// came; a duration equal to the run's is how that reads from here.
const last = api.frame(loaded.durationMs, COLS, ROWS);
const stripped = last.replace(/\x1b\[[0-9;]*m/g, "");
check("the clip is longer than the run", loaded.durationMs >= 11000 + 5000, `${loaded.durationMs} ms for an ~11.6 s run`);
check("the last frame is the card", /decode · \d+ streams?/.test(stripped), stripped.split("\n").find((l) => /decode · \d+ stream/.test(l))?.trim().slice(0, 70) || "no card text in the last frame");

// A tape it cannot read must say so rather than throw: a page that fetched a
// truncated file has to be able to tell its reader what happened.
const bad = api.load(new Uint8Array([1, 2, 3, 4]));
check("a file that is not a tape is refused by name", bad.ok === false && bad.error.length > 0, bad.error);

// And the refusal must not leave the previous run loaded, or a failed load
// would silently keep replaying whatever was there before.
check("a failed load clears what was loaded", api.frame(0, COLS, ROWS) === "", "empty frame");

process.exit(failures === 0 ? 0 : 1);
