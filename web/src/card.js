// PUT /api/v1/runs/<id>/card — the owner replacing a published run's card.
//
// The card is derived from the tape, and the tape is still on the service,
// so the information to redraw one is always there: `toktape reindex`
// re-derives the index row, and this route is the other half of that act —
// the client redraws the PNG from the same public view the index came from
// and PUTs it, and the run's page, the listing and the link preview show
// the new render. The bytes are stored and never opened: this side copies
// (§9.6), and the PNG and size checks are the upload path's own
// (upload.js) — one refusal in one place, so a card this side accepts is
// the same image whichever door it came in by.
//
// Auth is exactly the edit path's, through the preamble the two share
// (edit.js): the journal token that owns the run, never a delete token —
// a delete token takes a run down, it does not rewrite what it says.
//
//   Body: the PNG itself, Content-Type image/png. 400 for a body that is
//   not a PNG, 413 for one over MAX_CARD_BYTES. 200 answers the same shape
//   as /r/<id>.json, like PATCH does, so a caller holds the run as the API
//   describes it everywhere else.

import { requireJournalOwner } from "./edit.js";
import { fail } from "./http.js";
import { serveRunJSON } from "./run.js";
import { MAX_CARD_BYTES, isPNG } from "./upload.js";

export async function replaceCard(id, request, env) {
  const row = await requireJournalOwner(id, request, env, "replacing a run's card");
  if (row instanceof Response) return row;

  // Declared length before the body is read, the way the upload path
  // refuses a flood at the headers rather than the bytes.
  const declared = Number(request.headers.get("Content-Length") || 0);
  if (declared > MAX_CARD_BYTES) {
    return fail(413, `a card of ${declared} bytes is larger than this service accepts (${MAX_CARD_BYTES})`);
  }
  const card = new Uint8Array(await request.arrayBuffer());
  if (card.byteLength > MAX_CARD_BYTES) {
    return fail(413, `a card of ${card.byteLength} bytes is larger than this service accepts (${MAX_CARD_BYTES})`);
  }
  if (!isPNG(card)) {
    return fail(400, "the body is not a PNG");
  }

  // The same key and the same metadata the upload path writes, so a
  // replaced card is served exactly like a first one — immutable included:
  // a published card is a record, and refreshing the edge is a one-time
  // operational step, not this route's business.
  const key = `runs/${id}/card.png`;
  // The object first, the row second, the order the upload path uses: a
  // failure between the two leaves either the object unreferenced (a run
  // still published without a card, which it was before) or the row
  // pointing at bytes that are already the new card — never a row pointing
  // at an object that is not there.
  await env.TAPES.put(key, card, {
    httpMetadata: { contentType: "image/png", cacheControl: "public, max-age=31536000, immutable" },
  });
  // A run published without a card gains one. A run that already had one
  // already points here — the key is the run's, not the image's — so the
  // row is touched only when this changes it.
  await env.DB.prepare("UPDATE runs SET card_key = ? WHERE id = ? AND card_key IS NULL").bind(key, id).run();
  return serveRunJSON(id, env);
}
