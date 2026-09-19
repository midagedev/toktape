package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/midagedev/toktape/internal/config"
	"github.com/midagedev/toktape/internal/publish"
)

// runsUsage is what a mistyped runs invocation prints, wired into usageFor
// the way renderUsage is: `help runs`, `runs --help` and a rejected
// `runs` invocation print the same bytes (TTP-92).
const runsUsage = `toktape runs — list published runs on the service

Usage:
  toktape runs [flags]

Flags:
  --url URL         the service to read from (default ` + publish.DefaultBaseURL + `)
  -q TEXT           free-text query
  --model TEXT      normalised model id
  --repo TEXT       exact repository, e.g. bartowski/model-GGUF
  --quant TEXT      quantisation id, e.g. q4_k_m
  --engine TEXT     engine kind, e.g. llama.cpp
  --gpu TEXT        gpu id, e.g. rtx-4090
  --host TEXT       host class, e.g. desktop
  --os TEXT         operating system, e.g. linux
  --set TEXT        prompt set id
  --sessions N      streams sent at once
  --min-vram GB     VRAM floor in gigabytes
  --sort ORDER      newest (the default), oldest or decode
  --limit N         runs per page (default 30)
  --cursor CURSOR   continue the page the last listing cut
  --mine            your own runs, public and unlisted (needs the journal token)
  --user HANDLE     one user home instead of the search
  -o, --output FORMAT  json or jsonl instead of the table

Only the filters given travel. --sort oldest and --sort decode are refused
by an older server with a 400, printed verbatim. --mine and --user narrow
to two different scopes and cannot be combined.
`

// showUsage is what a mistyped show invocation prints (see runsUsage).
const showUsage = `toktape show — read one published run

Usage:
  toktape show <id|url> [flags]

Flags:
  --url URL         the service to read from (default ` + publish.DefaultBaseURL + `)
  --save FILE       download the record to FILE
  --force           with --save: overwrite FILE when it already exists
  -o, --output FORMAT  json instead of the summary

The id is bare or one of its links: the page, the record, the card or the
JSON. Unknown prints as ? and is never filled in. --save refuses to
overwrite without --force.
`

// runRuns lists published runs: the search, the journal scope, or one user
// home. The product is the table on stdout; -o json prints the service's
// own body verbatim and -o jsonl one run object per line.
func runRuns(ctx context.Context, c *cli, args []string) int {
	fs := newFlagSet("runs")
	output := declareOutputFlag(fs)
	svcURL := fs.String("url", "", "the service to read from (default "+publish.DefaultBaseURL+")")
	q := fs.String("q", "", "free-text query")
	model := fs.String("model", "", "normalised model id")
	repo := fs.String("repo", "", "exact repository")
	quant := fs.String("quant", "", "quantisation id")
	engine := fs.String("engine", "", "engine kind")
	gpu := fs.String("gpu", "", "gpu id")
	host := fs.String("host", "", "host class")
	osName := fs.String("os", "", "operating system")
	set := fs.String("set", "", "prompt set id")
	sessions := fs.Int("sessions", 0, "streams sent at once")
	minVRAM := fs.Int("min-vram", 0, "VRAM floor in gigabytes")
	sort := fs.String("sort", "newest", "newest, oldest or decode")
	limit := fs.Int("limit", 30, "runs per page")
	cursor := fs.String("cursor", "", "continue the cut page")
	mine := fs.Bool("mine", false, "your own runs")
	user := fs.String("user", "", "one user home")
	extra, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("runs", usageFor("runs"), args, err)
	}
	if len(extra) > 0 {
		return c.usagef("toktape runs: unexpected argument %q", extra[0])
	}
	format, refused := outputFor("runs", *output)
	c.json = format.isJSON()
	if refused != nil {
		return c.fail(*refused)
	}
	if *sort != "newest" && *sort != "oldest" && *sort != "decode" {
		return c.usagef("toktape runs: --sort %q is not one of newest, oldest or decode", *sort)
	}
	if *mine && *user != "" {
		return c.usagef("toktape runs: --mine and --user narrow to two different scopes")
	}

	cfg, err := config.Load()
	if err != nil {
		return c.usagef("toktape: %v", err)
	}
	// Named before the request like publish --edit's: the journal scope is
	// one token's own runs, and without a token there is nothing to ask.
	if *mine && cfg.Token == "" {
		return c.usagef("toktape runs: --mine needs a journal token in config.toml")
	}
	client := &publish.Client{
		BaseURL:   *svcURL,
		Token:     cfg.Token,
		UserAgent: "toktape/" + version,
	}
	listing, err := client.List(ctx, publish.ListQuery{
		Text: *q, Model: *model, Repo: *repo, Quant: *quant,
		Engine: *engine, GPU: *gpu, Host: *host, OS: *osName, Set: *set,
		Sessions: *sessions, MinVRAMGB: *minVRAM, Sort: *sort,
		Limit: *limit, Cursor: *cursor, Mine: *mine, User: *user,
	})
	if err != nil {
		return c.fail(failure{
			code: exitPublish,
			msg:  fmt.Sprintf("toktape: %v", err),
			hint: "the search lives at " + clientBaseURL(client) + "; --url names another service",
		})
	}

	switch format {
	case outputJSON:
		fmt.Fprintf(c.stdout, "%s\n", trimmedJSON(listing.Raw))
		return exitOK
	case outputJSONL:
		for _, r := range listing.Runs {
			b, err := json.Marshal(r)
			if err != nil {
				return c.fail(failure{
					code: exitPublish,
					msg:  fmt.Sprintf("toktape: printing the listing: %v", err),
				})
			}
			fmt.Fprintf(c.stdout, "%s\n", b)
		}
		return exitOK
	}

	if len(listing.Runs) == 0 {
		fmt.Fprintln(c.stdout, "no runs match")
		return exitOK
	}
	if *user != "" {
		// Either half is dropped when unknown, so a nameless home
		// prints its link and not the dash alone.
		headline := listing.Name
		if listing.Link != "" {
			if headline != "" {
				headline += " — "
			}
			headline += listing.Link
		}
		if headline != "" {
			fmt.Fprintln(c.stdout, headline)
		}
		if bio := strings.SplitN(listing.Bio, "\n", 2)[0]; strings.TrimSpace(bio) != "" {
			fmt.Fprintln(c.stdout, strings.TrimSpace(bio))
		}
	}
	header := []string{"ID", "DECODE tok/s", "MODEL", "QUANT", "ENGINE", "GPU", "STREAMS", "CAVEATS", "PUBLISHED", "TITLE"}
	var rows [][]string
	for _, r := range listing.Runs {
		rows = append(rows, []string{
			orUnknown(r.ID),
			rateCell(r.DecodePerS),
			modelCell(r.ModelID, r.ModelRaw),
			orUnknown(firstSet(r.QuantID, r.QuantRaw)),
			engineCell(r.EngineKind, r.EngineVer),
			gpuCell(r),
			countCell(r.Sessions),
			countCell(r.CaveatCount),
			dateCell(r.PublishedAt),
			r.Title,
		})
	}
	fmt.Fprint(c.stdout, table(header, rows, nil))
	order := map[string]string{"newest": "newest first", "oldest": "oldest first", "decode": "fastest first"}[*sort]
	if listing.Next != "" {
		fmt.Fprintf(c.stdout, "%d runs shown · %s · next: toktape runs --cursor %s\n", len(rows), order, listing.Next)
	} else {
		fmt.Fprintf(c.stdout, "%d runs shown · %s\n", len(rows), order)
	}
	return exitOK
}

// runShow reads one published run and prints its summary, or downloads its
// record with --save. -o json prints the service's own body verbatim.
func runShow(ctx context.Context, c *cli, args []string) int {
	fs := newFlagSet("show")
	output := declareOutputFlag(fs)
	svcURL := fs.String("url", "", "the service to read from (default "+publish.DefaultBaseURL+")")
	save := fs.String("save", "", "download the record to FILE")
	force := fs.Bool("force", false, "overwrite FILE with --save")
	files, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("show", usageFor("show"), args, err)
	}
	if len(files) != 1 {
		return c.usageTextf(usageText, "toktape show: expected one run id or link")
	}
	format, refused := outputFor("show", *output)
	c.json = format.isJSON()
	if refused != nil {
		return c.fail(*refused)
	}
	id, err := publish.RunID(files[0])
	if err != nil {
		return c.usagef("toktape show: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return c.usagef("toktape: %v", err)
	}
	client := &publish.Client{
		BaseURL:   *svcURL,
		Token:     cfg.Token,
		UserAgent: "toktape/" + version,
	}
	detail, err := client.Run(ctx, id)
	if err != nil {
		return c.fail(failure{
			code: exitPublish,
			msg:  fmt.Sprintf("toktape: %v", err),
			hint: "the run lives at " + clientBaseURL(client) + "; --url names another service",
		})
	}

	if format == outputJSON {
		fmt.Fprintf(c.stdout, "%s\n", trimmedJSON(detail.Raw))
		return exitOK
	}

	base := clientBaseURL(client)
	idx := detail.Index
	var b strings.Builder
	if detail.Title != "" {
		b.WriteString(detail.Title + "\n")
	}
	if detail.Private {
		b.WriteString("private: unlisted\n")
	}
	kv := func(k, v string) { b.WriteString(k + ": " + v + "\n") }
	kv("model", modelCell(idx.ModelID, idx.ModelRaw))
	kv("repo", orUnknown(idx.Repo))
	kv("quantisation", quantCell(idx.QuantRaw, idx.QuantBits))
	kv("engine", engineCell(idx.EngineKind, idx.EngineVersion))
	kv("host", dotCell(idx.OS, idx.HostClass))
	kv("gpus", gpusCell(idx.GPUsRaw))
	kv("streams", intCell(idx.Sessions))
	kv("decode", tokCell(idx.DecodePerSec))
	kv("prefill", tokCell(idx.PrefillPerSec))
	kv("ttft p50", msCell(idx.TTFTp50Ms))
	kv("caveats", caveatsCell(detail.Caveats))
	kv("published", orUnknown(detail.PublishedAt))
	kv("by", byCell(detail))
	kv("page", base+"/r/"+detail.ID)
	kv("record", base+detail.Tape+", "+fmt.Sprintf("%d KB", detail.TapeBytes/1024))
	kv("card", cardCell(base, detail.Card))
	fmt.Fprint(c.stdout, b.String())

	if *save != "" {
		n, ferr := saveTape(ctx, client, detail.ID, *save, *force)
		if ferr != nil {
			return c.fail(*ferr)
		}
		fmt.Fprintf(c.stdout, "saved %d bytes to %s — toktape card %s draws its card\n", n, *save, *save)
	}

	if detail.Note != "" {
		fmt.Fprintf(c.stdout, "\n%s\n", detail.Note)
	}
	return exitOK
}

// saveTape downloads one record to path, refusing to overwrite without
// force. A refusal to write is a usage failure — the invocation, not the
// service, was wrong — while a download failure keeps the service's code.
func saveTape(ctx context.Context, client *publish.Client, id, path string, force bool) (int64, *failure) {
	flag := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return 0, &failure{
				code: exitUsage,
				msg:  fmt.Sprintf("toktape show: %s already exists", path),
				hint: "pass --force to overwrite it",
			}
		}
		return 0, &failure{code: exitUsage, msg: fmt.Sprintf("toktape show: %v", err)}
	}
	n, derr := client.Download(ctx, id, f)
	cerr := f.Close()
	if derr != nil {
		_ = os.Remove(path)
		return n, &failure{
			code: exitPublish,
			msg:  fmt.Sprintf("toktape: %v", derr),
			hint: "the half-written file was removed",
		}
	}
	if cerr != nil {
		return n, &failure{code: exitUsage, msg: fmt.Sprintf("toktape show: %v", cerr)}
	}
	return n, nil
}

// clientBaseURL is the service a client talks to, with the default filled
// in: the page and record links are built from the same base that was read.
func clientBaseURL(client *publish.Client) string {
	if client.BaseURL == "" {
		return publish.DefaultBaseURL
	}
	return strings.TrimRight(client.BaseURL, "/")
}

// trimmedJSON is the verbatim body with exactly one trailing newline: the
// service may or may not end its answer with one, and stdout always does.
func trimmedJSON(raw json.RawMessage) string {
	return string(bytes.TrimSpace(raw))
}

// rateCell is the table's decode figure: one decimal, no unit (the header
// carries tok/s), ? for a run that reported none.
func rateCell(rate *float64) string {
	if rate == nil {
		return "?"
	}
	return fmt.Sprintf("%.1f", *rate)
}

// countCell prints a known count — 0 included, which web/src/row.js keeps
// for the same reason — and ? for an absent one.
func countCell(n *int) string {
	if n == nil {
		return "?"
	}
	return fmt.Sprintf("%d", *n)
}

// modelCell is the model that ran: the normalised id, else the raw name
// without its .gguf suffix, never the GGUF header's general.name behind it
// (modelLabel in ls.go keeps the same rule for local runs).
func modelCell(id, raw string) string {
	if id != "" {
		return id
	}
	return orUnknown(strings.TrimSuffix(raw, ".gguf"))
}

// engineCell is the kind with its version riding beside it: a rate without
// its build is half-read.
func engineCell(kind, version string) string {
	if kind == "" {
		return orUnknown(version)
	}
	if version != "" {
		return kind + " " + version
	}
	return kind
}

// gpuCell is the rig in one cell: N× the shared id on a multi-GPU box, the
// id on a uniform one, the raw names a mixed rig keeps instead.
func gpuCell(r publish.Row) string {
	if r.GPUID != "" && r.GPUCount != nil && *r.GPUCount > 1 {
		return fmt.Sprintf("%d× %s", *r.GPUCount, r.GPUID)
	}
	if r.GPUID != "" {
		return r.GPUID
	}
	return orUnknown(r.GPUsRaw)
}

// dateCell is the date part of a published_at the service stamps as
// "2006-01-02 15:04:05": the table has no room for the clock.
func dateCell(ts string) string {
	if i := strings.IndexAny(ts, "T "); i > 0 {
		return ts[:i]
	}
	return orUnknown(ts)
}

// quantCell is the quantisation as the uploader measured it, with the bit
// width riding beside it when it is known.
func quantCell(raw string, bits float64) string {
	q := orUnknown(raw)
	if bits == 0 {
		if raw == "" {
			return "?"
		}
		return raw
	}
	return fmt.Sprintf("%s · %s bit", q, strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", bits), "0"), "."))
}

// dotCell joins two halves that are each omittable: os · host_class, with
// either half dropped when unknown and ? only when both are.
func dotCell(a, b string) string {
	switch {
	case a != "" && b != "":
		return a + " · " + b
	case a != "":
		return a
	case b != "":
		return b
	}
	return "?"
}

// gpusCell is the rig the index recorded, joined the way the card joins it.
func gpusCell(raw []string) string {
	var names []string
	for _, g := range raw {
		if strings.TrimSpace(g) != "" {
			names = append(names, g)
		}
	}
	if len(names) == 0 {
		return "?"
	}
	return strings.Join(names, " / ")
}

// intCell prints a measured int, ? for unmeasured (0 in the JSON).
func intCell(n int) string {
	if n == 0 {
		return "?"
	}
	return fmt.Sprintf("%d", n)
}

// tokCell is one rate as the summary prints it: one decimal with its unit.
func tokCell(rate float64) string {
	if rate == 0 {
		return "?"
	}
	return fmt.Sprintf("%.1f tok/s", rate)
}

// msCell is one latency in milliseconds.
func msCell(ms float64) string {
	if ms == 0 {
		return "?"
	}
	return fmt.Sprintf("%.1f ms", ms)
}

// caveatsCell is the trust line: 0 when the run carries none, else the
// count with the codes beside it.
func caveatsCell(codes []string) string {
	if len(codes) == 0 {
		return "0"
	}
	return fmt.Sprintf("%d: %s", len(codes), strings.Join(codes, ", "))
}

// byCell is the author the run was published with: the name with its link
// riding beside it. No author on an unowned run means nobody signed it —
// anonymous — while no author on an owned one is a gap, ?.
func byCell(d *publish.RunDetail) string {
	if d.Author != nil && (d.Author.Name != "" || d.Author.Link != "") {
		if d.Author.Name != "" && d.Author.Link != "" {
			return d.Author.Name + " <" + d.Author.Link + ">"
		}
		return firstSet(d.Author.Name, d.Author.Link)
	}
	if !d.Owned {
		return "anonymous"
	}
	return "?"
}

// cardCell is the share card's link, or none for a run published without one.
func cardCell(base, card string) string {
	if card == "" {
		return "none"
	}
	return base + card
}

// firstSet is the first non-empty string, or "" when all are empty.
func firstSet(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
