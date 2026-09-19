package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/midagedev/toktape/internal/config"
	"github.com/midagedev/toktape/internal/publish"
	"github.com/midagedev/toktape/internal/tape"
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
  --size B          active-params band: 0, 4, 10, 35 or 100 (billions, lower bound)
  --sessions N      streams sent at once
  --min-vram GB     VRAM floor in gigabytes
  --min-predicted N fewest tokens any stream generated, floor
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

// reindexUsage is what a mistyped reindex invocation prints (see runsUsage).
const reindexUsage = `toktape reindex — rewrite published rows from their tapes

Usage:
  toktape reindex <id|url>... [flags]

Flags:
  --url URL         the service to read from (default ` + publish.DefaultBaseURL + `)

Each run is downloaded, re-read and re-indexed with this build's figures,
then patched back with the journal token that owns it. A run that cannot
be reindexed is named and skipped; the exit is publish when any was.
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
	size := fs.String("size", "", "active-params band lower bound in billions: 0, 4, 10, 35, 100")
	sessions := fs.Int("sessions", 0, "streams sent at once")
	minVRAM := fs.Int("min-vram", 0, "VRAM floor in gigabytes")
	minPredicted := fs.Int("min-predicted", 0, "fewest tokens any stream generated, floor")
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
		Size: *size, Sessions: *sessions, MinVRAMGB: *minVRAM,
		MinPredicted: *minPredicted, Sort: *sort,
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
	header := []string{"ID", "DECODE tok/s", "PREFILL tok/s", "MODEL", "PARAMS", "QUANT", "STREAMS", "P/G", "CTX", "KV", "ENGINE", "GPU", "CAVEATS", "PUBLISHED", "TITLE"}
	var rows [][]string
	for _, r := range listing.Runs {
		rows = append(rows, []string{
			orUnknown(r.ID),
			rateCell(r.DecodePerS),
			rateCell(r.PrefillPerS),
			modelCell(r.ModelID, r.ModelRaw),
			paramsCell(r),
			orUnknown(firstSet(r.QuantID, r.QuantRaw)),
			countCell(r.Sessions),
			pgCell(r),
			ctxCell(r.CtxSize),
			orUnknown(r.KVCache),
			engineCell(r.EngineKind, r.EngineVer),
			gpuCell(r),
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
	if s := workloadLine(idx); s != "" {
		kv("workload", s)
	}
	if s := contextLine(idx); s != "" {
		kv("context", s)
	}
	if s := configLine(idx); s != "" {
		kv("config", s)
	}
	if s := draftLine(idx); s != "" {
		kv("draft", s)
	}
	if s := machineLine(idx); s != "" {
		kv("machine", s)
	}
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

// paramsCell is the params chip: the total with the active count beside it
// on an MoE when both are known, the known one when only one is, ? when
// neither is. The band the Worker filters on is over the active count.
func paramsCell(r publish.Row) string {
	var total, active int64
	if r.Params != nil {
		total = *r.Params
	}
	if r.ActiveParams != nil {
		active = *r.ActiveParams
	}
	return paramsChip(r.MoE != nil && *r.MoE, total, active)
}

// paramsChip renders a parameter count as the chip the contract names: 35B
// at and above 10B, one decimal below it, and on an MoE the total with the
// active count riding beside it when both are known.
func paramsChip(moe bool, total, active int64) string {
	switch {
	case moe && total > 0 && active > 0:
		return fmtParams(total) + " · " + fmtParams(active) + " active"
	case active > 0:
		return fmtParams(active)
	case total > 0:
		return fmtParams(total)
	}
	return "?"
}

// fmtParams is one parameter count in billions: whole billions above 10B,
// one decimal below it, with a bare .0 trimmed.
func fmtParams(v int64) string {
	if v >= 10_000_000_000 {
		return fmt.Sprintf("%.0fB", float64(v)/1e9)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(v)/1e9), ".0") + "B"
}

// pgCell is prompt_n over predicted_n, with the shortest stream riding
// beside them when one stream was shorter than the mean says.
func pgCell(r publish.Row) string {
	if r.PromptN == nil || r.PredictedN == nil {
		return "?"
	}
	s := fmt.Sprintf("%d/%d", *r.PromptN, *r.PredictedN)
	if r.MinPredictedN != nil && *r.MinPredictedN < *r.PredictedN {
		s += fmt.Sprintf("·min%d", *r.MinPredictedN)
	}
	return s
}

// ctxCell is the context window in k: whole ks bare, the rest one decimal.
func ctxCell(ctx *int) string {
	if ctx == nil {
		return "?"
	}
	k := float64(*ctx) / 1024
	if k == math.Trunc(k) {
		return fmt.Sprintf("%.0fk", k)
	}
	return fmt.Sprintf("%.1fk", k)
}

// pctCell is a 0..1 share as whole percent, one decimal when it is not whole.
func pctCell(r float64) string {
	p := r * 100
	if p == math.Trunc(p) {
		return fmt.Sprintf("%.0f%%", p)
	}
	return fmt.Sprintf("%.1f%%", p)
}

// watts is a power draw without its unit: whole watts bare, else one decimal.
func watts(f float64) string {
	if f == math.Trunc(f) {
		return fmt.Sprintf("%.0f", f)
	}
	return fmt.Sprintf("%.1f", f)
}

// workloadLine is what the run did: prompt tokens in, generated tokens out
// with the shortest stream beside them, the cache share and the stream
// count. Each part prints only when its figures were measured.
func workloadLine(idx publish.Index) string {
	var head string
	switch {
	case idx.PromptN > 0 && idx.PredictedN > 0:
		head = fmt.Sprintf("%d in / %d out", idx.PromptN, idx.PredictedN)
		if idx.MinPredictedN > 0 && idx.MinPredictedN < idx.PredictedN {
			head += fmt.Sprintf(" (min %d)", idx.MinPredictedN)
		}
	case idx.PromptN > 0:
		head = fmt.Sprintf("%d in", idx.PromptN)
	case idx.PredictedN > 0:
		head = fmt.Sprintf("%d out", idx.PredictedN)
	}
	var parts []string
	if head != "" {
		parts = append(parts, head)
	}
	if idx.PromptN > 0 {
		parts = append(parts, "cache "+pctCell(idx.CacheHitRatio))
	}
	if idx.Sessions == 1 {
		parts = append(parts, "1 stream")
	} else if idx.Sessions > 1 {
		parts = append(parts, fmt.Sprintf("%d streams", idx.Sessions))
	}
	return strings.Join(parts, " · ")
}

// contextLine is the reservation the run was given: the window and the
// slots. A measured length is never printed here; ctx_size is what was asked.
func contextLine(idx publish.Index) string {
	var parts []string
	if idx.CtxSize > 0 {
		parts = append(parts, fmt.Sprintf("%d window", idx.CtxSize))
	}
	if idx.NSlots > 0 {
		parts = append(parts, fmt.Sprintf("%d slots", idx.NSlots))
	}
	return strings.Join(parts, " · ")
}

// configLine is the server's launch shape, each flag only when it was read.
func configLine(idx publish.Index) string {
	var parts []string
	if idx.FA != "" {
		parts = append(parts, "fa "+idx.FA)
	}
	if idx.KVCache != "" {
		parts = append(parts, "kv "+idx.KVCache)
	}
	if idx.Batch != "" {
		parts = append(parts, "b "+idx.Batch)
	}
	if idx.UBatch != "" {
		parts = append(parts, "ub "+idx.UBatch)
	}
	if idx.NGL != "" {
		parts = append(parts, "ngl "+idx.NGL)
	}
	if idx.Offload != "" {
		parts = append(parts, "offload "+idx.Offload)
	}
	return strings.Join(parts, " · ")
}

// draftLine is the speculative decoder: the model with its acceptance rate
// riding beside it when the server reported both halves.
func draftLine(idx publish.Index) string {
	var parts []string
	if idx.DraftModel != "" {
		parts = append(parts, idx.DraftModel)
	}
	if idx.DraftAccept > 0 {
		parts = append(parts, pctCell(idx.DraftAccept)+" accepted")
	}
	return strings.Join(parts, " · ")
}

// machineLine is what the box drew and whether it throttled: the draw over
// its limit when both were read, the draw alone otherwise.
func machineLine(idx publish.Index) string {
	var parts []string
	switch {
	case idx.PowerW > 0 && idx.PowerLimitW > 0:
		parts = append(parts, fmt.Sprintf("%s of %s W", watts(idx.PowerW), watts(idx.PowerLimitW)))
	case idx.PowerW > 0:
		parts = append(parts, watts(idx.PowerW)+" W")
	case idx.PowerLimitW > 0:
		parts = append(parts, watts(idx.PowerLimitW)+" W limit")
	}
	if idx.Throttled {
		parts = append(parts, "throttled")
	}
	if idx.Cold {
		parts = append(parts, "cold cache")
	}
	return strings.Join(parts, " · ")
}

// runReindex rewrites published rows from their tapes with this build's
// figures (TTP-130): each run is downloaded, decoded, re-indexed and
// patched back. It needs the journal token exactly like publish --edit —
// an anonymous run cannot be edited. A run that fails is named on stderr
// and skipped, and the exit is publish when any was.
func runReindex(ctx context.Context, c *cli, args []string) int {
	fs := newFlagSet("reindex")
	output := declareOutputFlag(fs)
	svcURL := fs.String("url", "", "the service to read from (default "+publish.DefaultBaseURL+")")
	names, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("reindex", usageFor("reindex"), args, err)
	}
	if len(names) == 0 {
		return c.usageTextf(usageText, "toktape reindex: expected one run id or link")
	}
	format, refused := outputFor("reindex", *output)
	c.json = format.isJSON()
	if refused != nil {
		return c.fail(*refused)
	}

	cfg, err := config.Load()
	if err != nil {
		return c.usagef("toktape: %v", err)
	}
	if cfg.Token == "" {
		return c.usagef("toktape publish --edit needs a journal token in config.toml; an anonymous run cannot be edited")
	}
	client := &publish.Client{
		BaseURL:   *svcURL,
		Token:     cfg.Token,
		UserAgent: "toktape/" + version,
	}
	failed := false
	for _, name := range names {
		id, err := publish.RunID(name)
		if err != nil {
			fmt.Fprintf(c.stderr, "toktape reindex: %v\n", err)
			failed = true
			continue
		}
		var buf bytes.Buffer
		if _, err := client.Download(ctx, id, &buf); err != nil {
			fmt.Fprintf(c.stderr, "toktape reindex %s: %v\n", id, err)
			failed = true
			continue
		}
		tp, err := tape.Decode(bytes.NewReader(buf.Bytes()))
		if err != nil {
			fmt.Fprintf(c.stderr, "toktape reindex %s: %v\n", id, err)
			failed = true
			continue
		}
		idx := publish.IndexOf(tp)
		if _, err := client.Edit(ctx, id, publish.Edit{Index: &idx}); err != nil {
			fmt.Fprintf(c.stderr, "toktape reindex %s: %v\n", id, err)
			failed = true
			continue
		}
		ctxS := "?"
		if idx.CtxSize > 0 {
			ctxS = ctxCell(&idx.CtxSize)
		}
		fmt.Fprintf(c.stdout, "%s  reindexed · P%d/G%d · ctx %s · kv %s · %s\n",
			id, idx.PromptN, idx.PredictedN, ctxS, orUnknown(idx.KVCache),
			paramsChip(idx.MoE, idx.Params, idx.ActiveParams))
	}
	if failed {
		return c.fail(failure{
			code: exitPublish,
			msg:  "toktape reindex: some runs were not reindexed",
		})
	}
	return exitOK
}
