# Commands

[← README](../README.md)

| verb | what it does | example |
| --- | --- | --- |
| `record` | attach and record a run; the default verb | `toktape --sessions 4 --for 30s` |
| `card` | re-render a card from a tape | `toktape card <tape> -o png` |
| `play` | replay a run on the live screen | `toktape play <tape> --speed 4` |
| `render` | render a run as GIF, mp4, asciicast or PNG frames | `toktape render <tape> --mp4 clip.mp4` |
| `ls` | list recorded runs | `toktape ls` |
| `log` | the experiment ledger of every run | `toktape log --sort decode` |
| `compare` | diff two runs, metrics and flags | `toktape compare a.toktape b.toktape` |
| `publish` | upload a run and print its link | `toktape publish <tape>` |
| `profile` | name the author every publish carries | `toktape profile --name NAME --link URL --avatar FILE --bio TEXT` |
| `runs` | list what is published, filtered and ordered like the site | `toktape runs --gpu rtx-3090 --sort decode` |
| `show` | read one published run; `--save` fetches its record | `toktape show <id> --save run.toktape` |
| `reindex` | recompute a published run's search row from its record — the figures a newer toktape indexes | `toktape reindex <id>` |
| `version` | print the version | `toktape version` |

A recording ends on the clock — twenty seconds by default, `--for 30s` to aim
it — because the same 256 tokens is two seconds on one machine and two
minutes on another; nothing is cut under 64 tokens, and `--n-predict` is the
cap that also applies (naming it turns the clock off). `--sessions` past
eight needs `--max-sessions` naming the same number; `--prompt` (repeatable)
or `--prompts` (a JSONL file, one round per line) replace the built-in set;
`--spec-n-max 3,5` runs the set once per speculative `n_max` into one tape.

How the request is shaped moves the number as much as the server's flags:
greedy against the server's default sampling against thinking left on is an
eleven per cent spread on one engine, so `record` names all three — `--temp`,
`--no-think`, `--endpoint chat|completion`, and `--param key=value` for
anything else the build honours. Whatever is sent is recorded and named on
the card.

`-o FORMAT` is llama-bench's `-o` with llama-bench's words: `json`, `jsonl`,
`md` (the card in a fence, a llama-bench table, a Reproduce block), `csv`,
`tsv`, `sql`. `toktape log -o sql | sqlite3 runs.db` is a database. Unknown
is `?` on the terminal, empty in exports, `NULL` in sql.

Runs append to `runs.tsv` beside the tapes, so a sweep is one table; label
runs with `--tag` and `--note`, and `log` takes `--sort`, `--model`, `--tag`
and `--limit N` to read it back:

```sh
toktape --tag ngl=40 --note "fa on"     # record, labelled
toktape log --sort decode                # which setting won
toktape log --tag ngl -o md              # paste into an issue
toktape log --rebuild                    # regenerate from the tapes
```

```sh
toktape log -o sql | sqlite3 runs.db
duckdb -c "select tag, decode_tok_s from read_csv('~/.toktape/runs/runs.tsv')"
```

A clip renders from the tape, never a screen recording, and plays at 1:1 —
nothing is compressed unless you ask, because a clip that sped a run up would
be lying about the one number the page is about. `--prefill-lead 3s` opens it
just before the first token:

```sh
toktape render ~/.toktape/runs/<id>.toktape
toktape render ~/.toktape/runs/<id>.toktape --mp4 clip.mp4 --cast clip.cast
```

