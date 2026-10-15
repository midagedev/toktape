// goFixed is strconv.FormatFloat(v, 'f', digits, 64). toFixed already rounds
// the exact binary value; the one difference is an exact tie, which toFixed
// takes away from zero and Go takes to even. A tie is a multiple of
// 2^-(digits+1), and that test is exact (a power-of-two scale).
//
// It lives here so the page chrome and the run route can both print with it.
// page.js must not import run.js: run.js imports the page.
export function goFixed(v, digits) {
  const m = 10 ** digits;
  const x = v * m;
  if (Number.isInteger(v * 2 ** (digits + 1)) && !Number.isInteger(x)) {
    const f = Math.floor(x);
    return ((f % 2 === 0 ? f : f + 1) / m).toFixed(digits);
  }
  return v.toFixed(digits);
}
