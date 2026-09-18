// Ids and the one-time delete key.

// The alphabet leaves out 0/1/l/o so an id read off a screen and typed back
// in lands on the run somebody meant.
const ALPHABET = "23456789abcdefghijkmnpqrstuvwxyz";

// A run id is 20 characters out of a 32-symbol alphabet: 100 bits.
//
// Unguessable is not a nicety here: it is the whole of what `--private`
// means. A private run is not access-controlled — the spec says so out loud
// ("추측 불가능한 URL", §9.3) — so the id is the only thing between it and a
// crawler, and 100 bits is far past what a crawler can walk.
export function newRunID() {
  return randomString(20);
}

// The delete token is the only key to an anonymous upload (§9.3), and it is
// printed once and stored nowhere — not by the client, and not here, where
// only its hash is kept. It is longer than a run id because losing it costs
// the ability to take a run down, and it carries a prefix so that a token
// pasted into an issue is recognisable as one.
export function newDeleteToken() {
  return "dt_" + randomString(32);
}

// One character per random byte. It throws away three of the eight bits, and
// the length above already counts only the five that survive — the trade is
// that there is no bit-packing arithmetic here to read carefully before
// believing the entropy. 256 is exactly eight times the alphabet, so the
// remainder is uniform and the discarded bits cost no bias.
function randomString(n) {
  const b = new Uint8Array(n);
  crypto.getRandomValues(b);
  let out = "";
  for (const v of b) out += ALPHABET[v % ALPHABET.length];
  return out;
}

// sha256Hex is how a secret is recognised without being kept. Both the
// journal tokens and the delete tokens are stored this way.
export async function sha256Hex(s) {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(s));
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
}
