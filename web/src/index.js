// The toktape hub, before it does anything.
//
// This exists so that tape.midagedev.com resolves to something we control:
// midagedev.com carries a wildcard A record, so until a Worker claims this
// hostname it silently resolves to an unrelated host, and a client that
// published to it would be uploading somebody's run to a stranger. Refusing
// is the honest state, and refusing with the right status is what makes the
// refusal legible to `toktape publish`, which quotes a server's own words.
//
// The real service is TTP-115: R2 for the records, D1 for the index rows,
// POST /api/v1/runs to upload, /r/<id> to read one back, and the search.
// Its contract is the doc block on publish.Client in
// internal/publish/client.go — the Worker is written against that, never the
// other way round, because the client derives the index next to the schema
// and this side never parses a tape.

const NOT_LIVE =
  "toktape hub is not live yet.\n" +
  "The service that will live here is described in docs/toktape-spec.ko.md section 9.\n" +
  "https://github.com/midagedev/toktape\n";

export default {
  async fetch(request) {
    const url = new URL(request.url);

    // A health check that answers before anything else exists, so "is the
    // hostname ours" and "is the service up" are separate questions.
    if (url.pathname === "/healthz") {
      return json({ ok: true, service: "toktape-hub", live: false });
    }

    // The upload path answers 503 rather than 404: 404 would tell a client it
    // had the wrong address, and it does not. The body is what the client
    // prints, so it is a sentence a person can act on.
    if (url.pathname === "/api/v1/runs") {
      return json(
        { error: "toktape hub is not accepting uploads yet" },
        503,
        { "Retry-After": "86400" },
      );
    }

    return new Response(NOT_LIVE, {
      status: 503,
      headers: {
        "Content-Type": "text/plain; charset=utf-8",
        "Cache-Control": "no-store",
      },
    });
  },
};

function json(body, status = 200, extra = {}) {
  return new Response(JSON.stringify(body) + "\n", {
    status,
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      "Cache-Control": "no-store",
      ...extra,
    },
  });
}
