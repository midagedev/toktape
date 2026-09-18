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
import { allowAnonymousUpload } from "./ratelimit.js";
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

// The author profile and the lab-note (TTP-125). Every limit mirrors the
// client's refusal in internal/publish/avatar.go — the client refusing
// first is courtesy, this refusing is the defence.
export const MAX_AUTHOR_NAME_RUNES = 40;
export const MAX_AUTHOR_LINK_BYTES = 200;
export const MAX_TITLE_RUNES = 120;
export const MAX_NOTE_RUNES = 4000;
export const MAX_BIO_RUNES = 600;
const MAX_AVATAR_BYTES = 65536;

// Runes, not UTF-16 units: [...s] splits a surrogate pair the way Go's
// []rune does, so the two sides count the same thing.
function runes(s) {
  return [...s].length;
}

// The lab-note validators as pure functions, exported so the owner-edit
// endpoint (edit.js) refuses exactly what the upload refuses: one refusal
// text in one place, or the two drift apart the way two copies always do.
// Each returns { value } or { error } — the sentence only, never the
// status, because the callers already know a bad field is a 400.
export function checkTitle(t) {
  const v = t.trim();
  if (v === "") return { error: `the title is empty: 1–${MAX_TITLE_RUNES} runes travel` };
  if (runes(v) > MAX_TITLE_RUNES) {
    return { error: `the title is ${runes(v)} runes, over the ${MAX_TITLE_RUNES}-rune limit` };
  }
  if (/[\n\r]/.test(v)) return { error: "the title must be a single line" };
  return { value: v };
}

export function checkNote(n) {
  const v = n.replace(/\r\n/g, "\n").trim();
  if (v === "") return { error: `the note is empty: 1–${MAX_NOTE_RUNES} runes travel` };
  if (runes(v) > MAX_NOTE_RUNES) {
    return { error: `the note is ${runes(v)} runes, over the ${MAX_NOTE_RUNES}-rune limit` };
  }
  return { value: v };
}

// The user-home bio (TTP-127): paragraphs like the note, with its own
// limit. Stored on the token, never the run.
export function checkBio(b) {
  const v = b.replace(/\r\n/g, "\n").trim();
  if (v === "") return { error: `the bio is empty: 1–${MAX_BIO_RUNES} runes travel` };
  if (runes(v) > MAX_BIO_RUNES) {
    return { error: `the bio is ${runes(v)} runes, over the ${MAX_BIO_RUNES}-rune limit` };
  }
  return { value: v };
}

const CONTROL_RE = /[\u0000-\u001F\u007F]/;

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
    const verdict = await allowAnonymousUpload(request, env);
    if (!verdict.ok) return fail(verdict.status, verdict.why, verdict.headers || {});
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

  // A run has a model, or it is not a run. The client always sends
  // model_raw (the name as the server reported it), so its absence means
  // the index is not one the client derived — and a row without it lists
  // as "a run · ? tok/s", which is exactly what the check.sh rate-limit
  // probes put in a local listing before this check existed (2026-09-18).
  if (typeof idx.model_raw !== "string" || idx.model_raw.trim() === "") {
    return fail(400, "the index carries no model_raw; a run without a model name is not indexed");
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

  // The author profile and the lab-note (TTP-125). Both travel as their
  // own parts, beside the record — never inside the tape and never in the
  // index row — and an older client sends none of them, which publishes
  // exactly as today. Every limit here mirrors the client's refusal in
  // internal/publish/avatar.go: the client refusing first is courtesy, this
  // refusing is the defence. The page escapes on output (esc), so what is
  // stored is exactly what was accepted — never HTML.
  const parsed = (() => {
    const bad = (status, why) => ({ failed: { status, why } });
    const authorPart = form.get("author");
    let authorName = null;
    let authorLink = null;
    if (authorPart !== null) {
      if (typeof authorPart !== "string") return bad(400, "the author part must be JSON, not a file");
      let author;
      try {
        author = JSON.parse(authorPart);
      } catch (e) {
        return bad(400, `the author part was not readable as JSON: ${e.message}`);
      }
      if (author === null || typeof author !== "object" || Array.isArray(author)) {
        return bad(400, "the author part is not an object");
      }
      if (author.name !== undefined && author.name !== null) {
        if (typeof author.name !== "string") return bad(400, "the author name is not a string");
        const name = author.name.trim();
        if (name === "") return bad(400, `the author name is empty: 1–${MAX_AUTHOR_NAME_RUNES} runes travel`);
        if (runes(name) > MAX_AUTHOR_NAME_RUNES) {
          return bad(400, `the author name is ${runes(name)} runes, over the ${MAX_AUTHOR_NAME_RUNES}-rune limit`);
        }
        if (CONTROL_RE.test(name)) return bad(400, "the author name carries a control character: names print on one line");
        authorName = name;
      }
      if (author.link !== undefined && author.link !== null) {
        if (typeof author.link !== "string") return bad(400, "the author link is not a string");
        const link = author.link.trim();
        if (link === "") return bad(400, `the author link is empty: 1–${MAX_AUTHOR_LINK_BYTES} bytes travel`);
        if (new TextEncoder().encode(link).length > MAX_AUTHOR_LINK_BYTES) {
          return bad(
            400,
            `the author link is ${new TextEncoder().encode(link).length} bytes, over the ${MAX_AUTHOR_LINK_BYTES}-byte limit`,
          );
        }
        let scheme = "(none)";
        let host = "";
        try {
          const u = new URL(link);
          scheme = u.protocol;
          host = u.host;
        } catch {
          return bad(400, `the author link is not a URL: ${JSON.stringify(link)}`);
        }
        // The scheme is refused by name — javascript:, data:, file: and
        // anything else — because this is where a stored XSS would enter.
        if (scheme !== "http:" && scheme !== "https:") {
          return bad(400, `the author link uses scheme ${JSON.stringify(scheme)}: only http and https travel`);
        }
        if (!host) return bad(400, "the author link has no host: only http and https URLs with a host travel");
        authorLink = link;
      }
    }

    const titlePart = form.get("title");
    let title = null;
    if (titlePart !== null) {
      if (typeof titlePart !== "string") return bad(400, "the title part must be text, not a file");
      const c = checkTitle(titlePart);
      if (c.error) return bad(400, c.error);
      title = c.value;
    }

    const notePart = form.get("note");
    let note = null;
    if (notePart !== null) {
      if (typeof notePart !== "string") return bad(400, "the note part must be text, not a file");
      const c = checkNote(notePart);
      if (c.error) return bad(400, c.error);
      note = c.value;
    }

    // The user-home bio (TTP-127). On a token-owned upload it is validated
    // like the rest and stored on the token below, never the run. On an
    // anonymous upload it is dropped with no error — not even type-checked
    // — because an anonymous run has no home to show it on.
    const bioPart = form.get("bio");
    let bio = null;
    let bioPresent = false;
    if (bioPart !== null && owner) {
      if (typeof bioPart !== "string") return bad(400, "the bio part must be text, not a file");
      const c = checkBio(bioPart);
      if (c.error) return bad(400, c.error);
      bio = c.value;
      bioPresent = true;
    }

    return { authorName, authorLink, title, note, bio, bioPresent, authorPresent: form.get("author") !== null };
  })();
  // Either the fields or {failed}: one early return, so the refusal reads
  // next to the parsing it came from.
  if (parsed.failed) return fail(parsed.failed.status, parsed.failed.why);
  const { authorName, authorLink, title, note, bio, bioPresent, authorPresent } = parsed;

  const avatarPart = form.get("avatar");
  let avatar = null;
  if (avatarPart !== null && typeof avatarPart === "string") {
    return fail(400, "the avatar part must be a PNG file, not text");
  }
  if (avatarPart) {
    if (form.get("author") === null) {
      return fail(400, "the upload carried an avatar part with no author part");
    }
    avatar = new Uint8Array(await avatarPart.arrayBuffer());
    if (avatar.byteLength === 0) {
      return fail(400, "the avatar part was empty");
    }
    if (avatar.byteLength > MAX_AVATAR_BYTES) {
      return fail(413, `an avatar of ${avatar.byteLength} bytes is larger than this service accepts (${MAX_AVATAR_BYTES})`);
    }
    if (!isPNG(avatar)) {
      return fail(400, "the avatar part is not a PNG");
    }
    // The bytes are stored, never decoded: dimensions were checked by the
    // client at set time, and decoding here would be a second opinion about
    // an image this side only serves.
  }

  const isPrivate = form.get("private") === "true";

  const id = newRunID();
  const key = `runs/${id}/run${ext}`;
  const cardKey = card ? `runs/${id}/card.png` : null;
  // Content-addressed, so the same image from the same machine is stored
  // once — and so it may be shared by other runs, which is why neither the
  // failure cleanup below nor the delete path removes it.
  const avatarKey = avatar ? `avatars/${await sha256HexBytes(avatar)}.png` : null;
  await env.TAPES.put(key, bytes, {
    httpMetadata: { contentType: "application/gzip" },
  });
  if (card) {
    await env.TAPES.put(cardKey, card, {
      httpMetadata: { contentType: "image/png", cacheControl: "public, max-age=31536000, immutable" },
    });
  }
  if (avatar) {
    await env.TAPES.put(avatarKey, avatar, {
      httpMetadata: { contentType: "image/png", cacheControl: "public, max-age=31536000, immutable" },
    });
  }

  // Only anonymous uploads get a delete token. A token-owned run is already
  // deletable by its owner, and inventing a second key for it would be one
  // more secret in the world for no gain (§9.2).
  const deleteToken = owner ? null : newDeleteToken();

  try {
    // The six new columns are bound explicitly beside card_key — they are
    // not index columns (row.js is unchanged), because they were never in
    // the index row the client derived.
    await env.DB.prepare(
      `INSERT INTO runs (id, created_at, private, owner_token, delete_hash,
                         tape_key, tape_ext, tape_bytes, card_key,
                         author_name, author_link, avatar_key, title, note,
                         index_schema, index_json, ${INDEX_COLUMNS.join(", ")})
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ${INDEX_COLUMNS.map(() => "?").join(", ")})`,
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
        authorName,
        authorLink,
        avatarKey,
        title,
        note,
        idx.schema,
        indexPart,
        ...columnValues(idx),
      )
      .run();
  } catch (e) {
    // The object is already in R2 and the row is not, so the record exists
    // with nothing pointing at it. Removing it keeps the two stores from
    // drifting apart in the one direction that is silent. The avatar is
    // NOT removed here: it is content-addressed and may belong to other
    // runs, so deleting it could take down someone else's byline.
    await env.TAPES.delete(key).catch(() => {});
    if (cardKey) await env.TAPES.delete(cardKey).catch(() => {});
    throw e;
  }

  // The profile follows the token, not each run (TTP-127): on a token-owned
  // publish the token's row is overwritten with what this upload carried,
  // so the home always shows the latest profile. An `author` part rewrites
  // all three of name/link/avatar_key — a field it did not carry becomes
  // NULL — while no `author` part at all leaves the three untouched, which
  // is what keeps an old client's publish from blanking the home. The bio
  // is rewritten only when its part was present. updated_at moves whenever
  // anything above did. The per-run columns above stay as the record of
  // what this upload said, and anonymous runs have nothing else.
  if (owner && (authorPresent || bioPresent)) {
    const sets = [];
    const args = [];
    if (authorPresent) {
      sets.push("name = ?", "link = ?", "avatar_key = ?");
      args.push(authorName, authorLink, avatarKey);
    }
    if (bioPresent) {
      sets.push("bio = ?");
      args.push(bio);
    }
    sets.push("updated_at = ?");
    args.push(new Date().toISOString());
    await env.DB.prepare(`UPDATE tokens SET ${sets.join(", ")} WHERE id = ?`).bind(...args, owner).run();
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

// sha256Hex over bytes rather than a string: ids.js's sha256Hex encodes its
// input as UTF-8, which would hash the encoding of the image rather than
// the image. The avatar's R2 key is this digest.
async function sha256HexBytes(bytes) {
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
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
