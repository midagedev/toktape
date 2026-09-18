# web — the toktape hub

The Cloudflare Worker behind `tape.midagedev.com`, where a published run
lives. The design is `docs/toktape-spec.ko.md` §9; the work is TTP-115.

What is here today is a placeholder that refuses. It exists for one reason:
`midagedev.com` carries a wildcard `A` record, so until a Worker claims this
hostname it resolves to an unrelated host, and a client that published to it
would be uploading somebody's run to a stranger. The custom domain in
`wrangler.jsonc` is what overrides the wildcard.

**The Worker never parses a tape.** The Go client derives a versioned index
row next to the schema (`internal/publish`) and uploads it beside the record,
so knowledge of the tape's shape has exactly one owner. The upload contract is
the doc block on `publish.Client` in `internal/publish/client.go`, and
`internal/publish/client_test.go` parses a request body the way this side will
have to — the Worker is written against those, never the other way round.

```sh
cd web
wrangler dev      # locally
wrangler deploy   # production; the lead does this, with the user's approval
```

Deploying uses the scoped token `toktape-hub-deploy`, never the account's
global API key.
