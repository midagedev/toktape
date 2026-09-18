// DELETE /api/v1/runs/<id> — taking a run down.
//
// "지우는 길이 항상 있어야 한다" (§9.3). There are two keys and one rule:
// present the one you have, as a bearer token, and it is checked by hash
// against what was stored.
//
//   - a journal token, for a run that token owns (§9.2)
//   - the run's own delete token, which was printed once when an anonymous
//     upload was accepted and stored nowhere, here included
//
// One header rather than two mechanisms because they are the same act with
// different proof, and a caller holds exactly one of them. The `dt_` prefix
// is what makes a delete token recognisable in an issue or a log; it is not
// what makes it work.

import { fail } from "./http.js";
import { sha256Hex } from "./ids.js";

export async function deleteRun(id, request, env) {
  const auth = request.headers.get("Authorization") || "";
  if (!auth.startsWith("Bearer ")) {
    return fail(401, "deleting a run needs its delete token, or the journal token that owns it, as `Authorization: Bearer <token>`");
  }
  const presented = auth.slice("Bearer ".length).trim();
  if (presented === "") {
    return fail(401, "the Authorization header carried no token");
  }

  const row = await env.DB.prepare("SELECT tape_key, owner_token, delete_hash FROM runs WHERE id = ?")
    .bind(id)
    .first();
  if (!row) {
    // Already gone reads as gone. A delete that has to be retried after a
    // dropped connection should not report a failure the second time.
    return fail(404, "no run with that id; if you just deleted it, it is gone");
  }

  const hash = await sha256Hex(presented);
  const ownsIt =
    (row.delete_hash !== null && timingSafeEqual(row.delete_hash, hash)) ||
    (row.owner_token !== null && (await tokenOwns(env, hash, row.owner_token)));
  if (!ownsIt) {
    return fail(403, "that token does not open this run");
  }

  // The row first: while both exist the run is reachable, and an object with
  // no row is invisible, so failing between the two leaves nothing served
  // from a half-deleted state. The orphan is then cleaned up, and if that
  // fails the object is unreferenced rather than exposed.
  await env.DB.prepare("DELETE FROM runs WHERE id = ?").bind(id).run();
  await env.TAPES.delete(row.tape_key).catch(() => {});
  return new Response(null, { status: 204 });
}

async function tokenOwns(env, hash, ownerID) {
  const t = await env.DB.prepare("SELECT id FROM tokens WHERE hash = ?").bind(hash).first();
  return t !== null && t.id === ownerID;
}

// Both values are hex digests of the same length, so this is a comparison
// that does not return early on the first differing character. It costs
// nothing and removes the question.
function timingSafeEqual(a, b) {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}
