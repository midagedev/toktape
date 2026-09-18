// POST /api/v1/runs — the one endpoint `toktape publish` talks to.
//
// The contract is the doc block on publish.Client in
// internal/publish/client.go, and internal/publish/client_test.go parses a
// request body the way this file does. Both were written first: this side is
// written against them, never the other way round.
//
// The tape is stored and never opened. The gzip magic is checked, which is a
// type check and not a parse — a part that is not a run file should be
// refused at the door rather than discovered later by a downloader. Beyond
// those two bytes the record is opaque here, and that is the design (§9.6):
// the judgement about what a measured zero means versus an unobserved field
// lives in internal/tape, and a second copy of it in JavaScript would be two
// places that are wrong differently.

import { fail, json, publicBase } from "./http.js";
import { newDeleteToken, newRunID, sha256Hex } from "./ids.js";
import { INDEX_COLUMNS, SUPPORTED_INDEX_SCHEMA, columnValues } from "./row.js";

// A tape is tens of kilobytes — the hero is 25 KB on disk, 55 KB for a long
// multi-stream run — so this is two orders of magnitude of headroom and
// still small enough that a body this size is a mistake, not a run.
const MAX_UPLOAD_BYTES = 8 * 1024 * 1024;

// The extensions a run file is uploaded under. `.toktape` is where the
// format is going (§9.7) and `.tape` is what this build of the client still
// writes, so both are accepted and the one that arrived is what a download
// is served as: the file format has one owner and it is the Go side.
const TAPE_EXTS = [".toktape", ".tape"];

// The card is 1200×675 and comes out of internal/card/png at around 92 KB.
// A megabyte is room for it to grow and still not be something else.
const MAX_CARD_BYTES = 1024 * 1024;

export async function uploadRun(request, env) {
  const declared = Number(request.headers.get("Content-Length") || 0);
  if (declared > MAX_UPLOAD_BYTES) {
    return fail(413, `a run of ${declared} bytes is larger than this service accepts (${MAX_UPLOAD_BYTES})`);
  }

  // An unknown token is refused, never quietly downgraded to an anonymous
  // upload: that would file the run outside the journal it was meant for,
  // and because the client prints no delete token for a token-owned upload,
  // nobody would be left holding the key to take it down.
  const owner = await ownerOf(request, env);
  if (owner === UNKNOWN_TOKEN) {
    return fail(401, "that token is not one this service issued; publish without it for a one-off upload");
  }

  // Before the body is read, so a flood costs this service the headers and
  // not the bytes. Anonymous only: a journal token was issued by us and is
  // revocable, which is a better answer to abuse than a counter.
  if (!owner) {
    if (!env.ANON_UPLOADS) {
      // Closed beats unlimited. A deploy that lost the binding would
      // otherwise serve an anonymous endpoint with no limit at all, and
      // nothing about it would look wrong.
      return fail(503, "this deployment has no upload limit configured and will not take anonymous uploads");
    }
    const { success } = await env.ANON_UPLOADS.limit({ key: request.headers.get("CF-Connecting-IP") || "unknown" });
    if (!success) {
      return fail(
        429,
        "six uploads a minute is the limit for an address with no token; wait a minute, or publish with a journal token",
        { "Retry-After": "60" },
      );
    }
  }

  let form;
  try {
    form = await request.formData();
  } catch (e) {
    return fail(400, `the upload was not readable as multipart/form-data: ${e.message}`);
  }

  const tapePart = form.get("tape");
  if (!tapePart || typeof tapePart === "string") {
    return fail(400, "the upload carried no tape part");
  }
  const ext = TAPE_EXTS.find((e) => (tapePart.name || "").endsWith(e));
  if (!ext) {
    return fail(400, `the tape part is named ${JSON.stringify(tapePart.name || "")}; it must end in .toktape or .tape`);
  }

  const bytes = new Uint8Array(await tapePart.arrayBuffer());
  if (bytes.byteLength === 0) {
    return fail(400, "the tape part was empty");
  }
  if (bytes.byteLength > MAX_UPLOAD_BYTES) {
    return fail(413, `a run of ${bytes.byteLength} bytes is larger than this service accepts (${MAX_UPLOAD_BYTES})`);
  }
  if (bytes[0] !== 0x1f || bytes[1] !== 0x8b) {
    return fail(400, "the tape part is not a run file: a run file is gzip and this does not begin like one");
  }

  // The index part has no filename, so it arrives as a string however it was
  // labelled. Reading it as a file would work today and break the day a
  // client sets one.
  const indexPart = form.get("index");
  if (typeof indexPart !== "string" || indexPart === "") {
    return fail(400, "the upload carried no index part");
  }
  let idx;
  try {
    idx = JSON.parse(indexPart);
  } catch (e) {
    return fail(400, `the index part was not readable as JSON: ${e.message}`);
  }
  if (idx === null || typeof idx !== "object" || Array.isArray(idx)) {
    return fail(400, "the index part is not an index");
  }
  // By name, never by guess (§9.6). Indexing an unknown shape into these
  // columns would put values under the wrong headings and look like it
  // worked.
  if (idx.schema !== SUPPORTED_INDEX_SCHEMA) {
    return fail(
      400,
      `this service indexes index schema ${SUPPORTED_INDEX_SCHEMA} and the upload says ${JSON.stringify(idx.schema)}; upgrade whichever of the two is older`,
    );
  }

  // The card the link previews as. Optional, because a run published by an
  // older client has none and a page without a preview is better than a
  // refusal — but checked when it is there, because an image served as
  // image/png that is not one is a broken preview everywhere at once.
  const cardPart = form.get("card");
  let card = null;
  if (cardPart && typeof cardPart !== "string") {
    card = new Uint8Array(await cardPart.arrayBuffer());
    if (card.byteLength > MAX_CARD_BYTES) {
      return fail(413, `a card of ${card.byteLength} bytes is larger than this service accepts (${MAX_CARD_BYTES})`);
    }
    if (!isPNG(card)) {
      return fail(400, "the card part is not a PNG");
    }
  }

  const isPrivate = form.get("private") === "true";

  const id = newRunID();
  const key = `runs/${id}/run${ext}`;
  const cardKey = card ? `runs/${id}/card.png` : null;
  await env.TAPES.put(key, bytes, {
    httpMetadata: { contentType: "application/gzip" },
  });
  if (card) {
    await env.TAPES.put(cardKey, card, {
      httpMetadata: { contentType: "image/png", cacheControl: "public, max-age=31536000, immutable" },
    });
  }

  // Only anonymous uploads get a delete token. A token-owned run is already
  // deletable by its owner, and inventing a second key for it would be one
  // more secret in the world for no gain (§9.2).
  const deleteToken = owner ? null : newDeleteToken();

  try {
    await env.DB.prepare(
      `INSERT INTO runs (id, created_at, private, owner_token, delete_hash,
                         tape_key, tape_ext, tape_bytes, card_key,
                         index_schema, index_json, ${INDEX_COLUMNS.join(", ")})
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ${INDEX_COLUMNS.map(() => "?").join(", ")})`,
    )
      .bind(
        id,
        new Date().toISOString(),
        isPrivate ? 1 : 0,
        owner,
        deleteToken ? await sha256Hex(deleteToken) : null,
        key,
        ext,
        bytes.byteLength,
        cardKey,
        idx.schema,
        indexPart,
        ...columnValues(idx),
      )
      .run();
  } catch (e) {
    // The object is already in R2 and the row is not, so the record exists
    // with nothing pointing at it. Removing it keeps the two stores from
    // drifting apart in the one direction that is silent.
    await env.TAPES.delete(key).catch(() => {});
    if (cardKey) await env.TAPES.delete(cardKey).catch(() => {});
    throw e;
  }

  const receipt = { id, url: `${publicBase(request, env)}/r/${id}` };
  if (deleteToken) receipt.delete_token = deleteToken;
  return json(receipt, 201);
}

// The PNG signature. Two bytes settle gzip; a PNG's is eight and all eight
// are checked because this one is handed to browsers as an image.
function isPNG(b) {
  const sig = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
  if (b.byteLength < sig.length) return false;
  return sig.every((v, i) => b[i] === v);
}

// UNKNOWN_TOKEN is distinct from null: null is an anonymous upload, which is
// a supported path, and this is a token that was offered and not recognised.
const UNKNOWN_TOKEN = Symbol("unknown token");

async function ownerOf(request, env) {
  const auth = request.headers.get("Authorization") || "";
  if (!auth.startsWith("Bearer ")) return null;
  const presented = auth.slice("Bearer ".length).trim();
  if (presented === "") return null;

  const row = await env.DB.prepare("SELECT id FROM tokens WHERE hash = ?")
    .bind(await sha256Hex(presented))
    .first();
  return row ? row.id : UNKNOWN_TOKEN;
}
