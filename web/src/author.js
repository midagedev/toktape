// GET /a/<hash>.png — one author's avatar, by content hash.
//
// The avatar is content-addressed (`avatars/<sha256hex>.png`, the digest of
// the bytes), so the same image from the same machine is stored once — and
// so it may be shared by other runs, which is why neither the failure
// cleanup in upload.js nor the delete path removes it. A COUNT-and-delete
// is a later ticket.
//
// The serving pattern copies serveCard's headers: an avatar never changes,
// so it is immutable, and the content type is what makes a preview render
// instead of download.

import { fail } from "./http.js";

// avatarPath turns a stored avatar_key (`avatars/<hash>.png`) into the path
// the page and the API print (`/a/<hash>.png`), or null when there is none.
// Both run.js and search.js read through this, so the two cannot disagree
// about what an avatar_key means.
export function avatarPath(avatarKey) {
  const m = /^avatars\/([0-9a-f]{64})\.png$/.exec(avatarKey || "");
  return m ? `/a/${m[1]}.png` : null;
}

export async function serveAvatar(hash, env) {
  // A hash that is not 64 hex digits is a 404 before touching R2: nothing
  // else could be an avatar's name. (The route's own regex already holds
  // this shape; this is the second check for callers that do not come
  // through it.)
  if (!/^[0-9a-f]{64}$/.test(hash)) return fail(404, "no avatar with that id");
  const obj = await env.TAPES.get(`avatars/${hash}.png`);
  if (!obj) return fail(404, "no avatar with that id");
  return new Response(obj.body, {
    headers: {
      "Content-Type": "image/png",
      "Content-Length": String(obj.size),
      // An avatar never changes: its name is its bytes.
      "Cache-Control": "public, max-age=31536000, immutable",
    },
  });
}
