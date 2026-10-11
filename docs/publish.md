# Publishing a run

[← README](../README.md)

A tape is small enough to hand over whole — the hero is 25 KB — and a page
that has the tape can draw everything else from it. That is what
[tape.midagedev.com](https://tape.midagedev.com) does: the link is the card,
the run replayed in the browser, the transcript, the mp4 and the record
itself.

```sh
toktape publish ~/.toktape/runs/<id>.toktape --dry-run
toktape publish ~/.toktape/runs/<id>.toktape
```

`--dry-run` prints exactly what would go up; read it once, because a
published run is **public** and carries **its text** — the prompts and what
the model wrote — by default: a rate without the text it was measured on is
half a claim. `--private` keeps a run out of the search, `--no-text` leaves
the text home, and both can be defaulted in `~/.toktape/config.toml`.
`toktape profile` names the author once per machine; `--title`/`--note`
attach a lab-note to one run; `publish --edit <id>` rewrites what the page
shows.

Taking a run down is one command, `toktape publish --delete <id>`. With a
journal token it is your token that opens it; an anonymous upload's **delete
token** is printed once and kept in `~/.toktape/published.json`, so the
command works there too, and the entry goes when the run does.

The hub is a setting, not a fixture. `service = "https://tapes.example.com"`
in `~/.toktape/config.toml` (or `TOKTAPE_SERVICE`, or `--url`) points every
command at your own, and the token in that file is only ever sent to the
service named beside it. [`web/README.md`](../web/README.md) is how to host one.

The site is a search, not a leaderboard: newest first, every row with the
caveats its card would print, filters for model, quantisation, engine, GPU,
host and VRAM. All of it is readable from the terminal too:

```sh
toktape runs --gpu rtx-3090 --sort decode      # what the site lists, as a table
toktape runs --mine -o json                     # your own runs, the API's body verbatim
toktape show <id> --save run.toktape               # one run's summary, and its record
toktape card run.toktape                           # the card, drawn locally from that record
```

Hostnames and absolute paths are removed whatever the visibility is. The
service itself never opens a tape: everything richer than the index row is
the same renderer this binary uses, compiled to WebAssembly and run in your
browser.


## Asking someone else for a tape

A maintainer can only answer "how fast is it on your card?" with a number the
other person measured. The block below is the whole job. Paste it into the
issue as it is. The answer comes back as one link.

```sh
# 1. install: pick one line
brew install midagedev/tap/toktape
curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh

# 2. the credit on the page, the listing and the preview
toktape profile --name "<your name>" --link "<your page>"

# 3. on the machine that serves the model, run one:
#    decide for a decision model, record for a chat model
toktape decide --url http://127.0.0.1:<port> --host-label "<machine>"
toktape record --url http://127.0.0.1:<port> --host-label "<machine>"

# 4. look, then publish (<tape> is the file step 3 printed)
toktape publish --dry-run <tape>
toktape publish <tape>

# 5. paste the link it prints in the issue
```

- Run step 3 on the machine that serves the model. A remote URL records the
  machine you typed it on as the rig, not the one that did the work.
- `<tape>` is the file step 3 printed, in `~/.toktape/runs`.
- `--host-label` names the machine on the card and the page. The hub groups
  runs by GPU name, so `https://tape.midagedev.com/?gpu=<id>` lists every tape
  made on one card model.
- `publish --no-text` keeps the prompts, the answers and a decision run's
  request and response bodies at home. The timings still go up.
- The card is drawn when you publish. Publish with the newest release.
