// The toktape hub (docs/toktape-spec.ko.md §9).
//
// tape.midagedev.com hosts the record, not pixels: a published run is the
// same structured file `toktape record` wrote, so a card, a GIF and a cast
// can all still be drawn from it. The measured argument for that is in §9.1
// — one hero clip is 55 KB as a record, 4.1 MB as a GIF, and only one of
// those two can be turned back into the other.
//
// **The Worker never parses a tape.** The Go client derives a versioned
// index row next to the schema and uploads it beside the record, so
// knowledge of the tape's shape has exactly one owner. The upload contract
// is the doc block on publish.Client in internal/publish/client.go and the
// request body internal/publish/client_test.go parses; this side is written
// against those, never the other way round.

import { deleteRun } from "./del.js";
import { fail, json, publicBase } from "./http.js";
import { serveCard, serveRunJSON, serveRunPage, serveTape } from "./run.js";
import { searchAPI, searchPage } from "./search.js";
import { uploadRun } from "./upload.js";

// Both extensions are served: `.toktape` is where the format is going
// (§9.7), `.tape` is what today's client uploads under, and a run is served
// back as whichever it arrived as.
const TAPE_SUFFIX = /^\/r\/([a-z0-9]{8,64})(\.toktape|\.tape)$/;
const RUN_JSON = /^\/r\/([a-z0-9]{8,64})\.json$/;
const RUN_CARD = /^\/r\/([a-z0-9]{8,64})\.png$/;
const RUN_PAGE = /^\/r\/([a-z0-9]{8,64})$/;
const RUN_API = /^\/api\/v1\/runs\/([a-z0-9]{8,64})$/;

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    const path = url.pathname;

    // "Is the hostname ours" and "is the service up" are separate questions,
    // so this answers before anything that needs a binding.
    if (path === "/healthz") {
      return json({ ok: true, service: "toktape-hub", live: true });
    }

    if (path === "/api/v1/runs") {
      if (request.method === "POST") return uploadRun(request, env);
      if (request.method === "GET") return searchAPI(request, env);
      return fail(405, "a run is published with POST and the listing is a GET", { Allow: "GET, POST" });
    }

    let m;
    if ((m = RUN_API.exec(path))) {
      if (request.method !== "DELETE") {
        return fail(405, "a run is read at /r/<id> and deleted with DELETE here", { Allow: "DELETE" });
      }
      return deleteRun(m[1], request, env);
    }
    if ((m = TAPE_SUFFIX.exec(path))) return serveTape(m[1], env);
    if ((m = RUN_JSON.exec(path))) return serveRunJSON(m[1], env);
    if ((m = RUN_CARD.exec(path))) return serveCard(m[1], env);
    if ((m = RUN_PAGE.exec(path))) return serveRunPage(m[1], env, publicBase(request, env));

    // The front page is the search: the thing a visitor came for is other
    // people's runs, not an explanation of the service.
    if (path === "/") return searchPage(request, env);
    return fail(404, "no such path on this service");
  },
};
