// Answers.
//
// Every refusal this Worker makes is read out loud by somebody: `toktape
// publish` quotes a server's body verbatim into the terminal, on the
// reasoning that the first thing a publisher needs to know is whose problem
// it is. So an error body here is one sentence a person can act on, never a
// code and never a stack.

export function json(body, status = 200, extra = {}) {
  return new Response(JSON.stringify(body) + "\n", {
    status,
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      "Cache-Control": "no-store",
      ...extra,
    },
  });
}

// fail is the shape the Go client parses and prints.
export function fail(status, sentence, extra = {}) {
  return json({ error: sentence }, status, extra);
}

export function text(body, status = 200, extra = {}) {
  return new Response(body, {
    status,
    headers: {
      "Content-Type": "text/plain; charset=utf-8",
      "Cache-Control": "no-store",
      ...extra,
    },
  });
}

// publicBase is where this service answers, which is not the same question
// as where the request came from. `wrangler dev` simulates the configured
// route, so a request to a dev server arrives claiming to be
// tape.midagedev.com — a link or an og:image derived from it would point a
// developer, and every crawler, at production.
export function publicBase(request, env) {
  if (env.PUBLIC_BASE_URL) return env.PUBLIC_BASE_URL.replace(/\/+$/, "");
  return new URL(request.url).origin;
}
