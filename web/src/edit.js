// PATCH /api/v1/runs/<id> — an owner changing a run's title, note and
// visibility (TTP-127).
//
// Auth is exactly the delete path's (del.js), narrowed to the journal token
// only: a delete token is a one-time key for taking a run down, not for
// rewriting what it says, and an anonymous run has no title to defend —
// either way the answer is 403, never a downgrade to something weaker.
//
//   Body: JSON {title?, note?, private?, index?}. title/note are validated
//   with the upload's own limits (upload.js) — `null` clears the field,
//   absent leaves it. private must be a boolean and becomes 0/1. index is a
//   full index row (TTP-130): its schema must be the supported one, and it
//   is stored verbatim with every index column rewritten from it. Unknown
//   keys are refused by name, and an empty body is refused outright.
//
//   200 answers the same shape as /r/<id>.json, so a reader of the response
//   holds the run as the API describes it everywhere else.

import { tokenOwns } from "./del.js";
import { fail } from "./http.js";
import { sha256Hex } from "./ids.js";
import { INDEX_COLUMNS, SUPPORTED_INDEX_SCHEMA, columnValues } from "./row.js";
import { serveRunJSON } from "./run.js";
import { checkNote, checkTitle } from "./upload.js";

const KNOWN_KEYS = ["title", "note", "private", "index"];

// The journal-token preamble the owner paths that are not a delete share —
// this route and the card-replace one (card.js): 401 without a bearer
// token, 404 without the run, 403 unless the token owns it, checked in
// that order so a caller without the key learns nothing about whether the
// run exists. `what` names the act for the refusals ("editing a run",
// "replacing a run's card"). Exported rather than copied, for the same
// reason tokenOwns is: two copies of this ladder would answer "whose run
// is this" differently one day. Returns the run's row, or the refusal.
export async function requireJournalOwner(id, request, env, what) {
  const auth = request.headers.get("Authorization") || "";
  if (!auth.startsWith("Bearer ")) {
    return fail(401, `${what} needs the journal token that owns the run as \`Authorization: Bearer <token>\``);
  }
  const presented = auth.slice("Bearer ".length).trim();
  if (presented === "") {
    return fail(401, "the Authorization header carried no token");
  }

  const row = await env.DB.prepare("SELECT owner_token FROM runs WHERE id = ?").bind(id).first();
  if (!row) {
    return fail(404, "no run with that id");
  }

  const hash = await sha256Hex(presented);
  // Journal token only: a delete token opens the delete path, never one of
  // these, and a run with no owner has nothing to defend.
  if (row.owner_token === null || !(await tokenOwns(env, hash, row.owner_token))) {
    return fail(403, `${what} needs the journal token that owns this run`);
  }
  return row;
}

export async function editRun(id, request, env) {
  const row = await requireJournalOwner(id, request, env, "editing a run");
  if (row instanceof Response) return row;

  let body;
  try {
    body = await request.json();
  } catch (e) {
    return fail(400, `the edit was not readable as JSON: ${e.message}`);
  }
  if (body === null || typeof body !== "object" || Array.isArray(body)) {
    return fail(400, "the edit is not an object");
  }
  const keys = Object.keys(body);
  if (keys.length === 0) {
    return fail(400, "the edit names nothing to change: title, note, private or index");
  }
  for (const k of keys) {
    if (!KNOWN_KEYS.includes(k)) {
      return fail(400, `the edit names no field ${JSON.stringify(k)}: only title, note, private and index travel`);
    }
  }

  const sets = [];
  const args = [];
  if ("title" in body) {
    if (body.title === null) {
      sets.push("title = NULL");
    } else {
      if (typeof body.title !== "string") return fail(400, "the title is not a string");
      const c = checkTitle(body.title);
      if (c.error) return fail(400, c.error);
      sets.push("title = ?");
      args.push(c.value);
    }
  }
  if ("note" in body) {
    if (body.note === null) {
      sets.push("note = NULL");
    } else {
      if (typeof body.note !== "string") return fail(400, "the note is not a string");
      const c = checkNote(body.note);
      if (c.error) return fail(400, c.error);
      sets.push("note = ?");
      args.push(c.value);
    }
  }
  if ("private" in body) {
    // A string that reads as a boolean is still a string: "yes" would
    // otherwise unlist a run its owner meant to keep listed, or the other
    // way round, with no error to notice.
    if (typeof body.private !== "boolean") return fail(400, "private is not a boolean: true unlists the run, false lists it");
    sets.push("private = ?");
    args.push(body.private ? 1 : 0);
  }
  if ("index" in body) {
    // A reindex (TTP-130): the owner replaces the run's index row wholesale
    // — PATCH /api/v1/runs/<id> with {"index": {...full Index JSON...}}.
    // The object is stored verbatim as index_json and every INDEX_COLUMNS
    // column is rewritten from it in the same UPDATE, so the JSON and the
    // columns cannot drift apart. Combinable with title/note/private in one
    // body. Auth is unchanged: the journal token that owns the run.
    const idx = body.index;
    if (idx === null || typeof idx !== "object" || Array.isArray(idx)) {
      return fail(400, "the index is not an object: PATCH the full index row under the `index` key");
    }
    if (idx.schema !== SUPPORTED_INDEX_SCHEMA) {
      return fail(400, `index schema ${JSON.stringify(idx.schema)} is not ${SUPPORTED_INDEX_SCHEMA}`);
    }
    sets.push("index_json = ?");
    args.push(JSON.stringify(idx));
    for (let i = 0; i < INDEX_COLUMNS.length; i++) {
      sets.push(`${INDEX_COLUMNS[i]} = ?`);
      args.push(columnValues(idx)[i]);
    }
  }

  await env.DB.prepare(`UPDATE runs SET ${sets.join(", ")} WHERE id = ?`).bind(...args, id).run();
  // The same shape as /r/<id>.json — and its no-store, because an edited
  // run is a run that changes.
  return serveRunJSON(id, env);
}
