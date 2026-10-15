package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/midagedev/toktape/internal/config"
	"github.com/midagedev/toktape/internal/publish"
	"github.com/midagedev/toktape/internal/tape"
)

// shareMax is the most tapes one share publishes: a paste block of twenty
// links is already more than an issue thread wants.
const shareMax = 20

// shareUsage is the share verb's own text, wired into usageFor like the other
// verbs' (TTP-92): `help share`, `share --help` and a rejected invocation
// print the same bytes.
const shareUsage = `toktape share — publish the newest run and print a block to paste

Usage:
  toktape share [-n N] [--out DIR] [--url URL] [--yes] [--no-text] [--dry-run]

One command from a recorded run to a link in an issue: it picks the newest
tape in the runs directory, shows what would leave this machine, asks once,
publishes through the same path as toktape publish, and prints one Markdown
line per tape (the figure, the model, the link, the card and the engine).
On a terminal the block is also copied.

  -n N          the newest N tapes, 1 to 20 (default 1)
  --out DIR     the runs directory (default ~/.toktape/runs), as for ls
  --url URL     the service to publish to, as for publish
  --yes         publish without the question, and without asking for a name
  --no-text     upload without the prompts and the generated text
  --dry-run     show what would be uploaded and the block, upload nothing
  -o json       one array of {file, url, skipped, error} instead of the block

A tape this machine already published to the same service is skipped, and its
link is still in the block. A chat tape is never shared: it holds the
conversation, and toktape publish --include-conversation is the way to send
one on purpose. With no terminal and no --yes, share refuses; it never
uploads from a pipe unasked. Without a saved profile a terminal is asked for a
name and one link, each skippable; toktape profile sets them any time.

Examples:
  toktape share
  toktape share -n 2
  toktape share --dry-run
`

// shareStdin and shareInteractive are share's two seams to a person. The
// questions are read from stdin and written to stderr, and they are asked only
// when both are terminals; a test replaces them because nothing under
// go test is one.
var shareStdin io.Reader = os.Stdin

var shareInteractive = func(c *cli) bool { return isTTY(os.Stdin) && isTTY(c.stderr) }

// sharePick is one tape share looked at.
type sharePick struct {
	path string
	id   string
	sha  string
	tp   *tape.Tape
	view *tape.Tape
	idx  publish.Index
	// link is the remembered link of a tape already up, "" for one to upload.
	link string
	// rid is the run id of that remembered receipt.
	rid string
}

// shareResult is one row of what happened, in pick order.
type shareResult struct {
	File    string `json:"file"`
	URL     string `json:"url"`
	Skipped bool   `json:"skipped"`
	Error   string `json:"error"`

	pick *sharePick
}

// runShare publishes the newest tapes and prints the block to paste (TTP-206).
//
// It adds no way to publish that toktape publish lacks: the same public view,
// the same disclosure, the same client and the same receipts. What it adds is
// the walk — pick, ask once, upload each, print one block — so the flow a
// maintainer asks a stranger for is install, share, paste.
func runShare(ctx context.Context, c *cli, args []string) int {
	fs := newFlagSet("share")
	output := declareOutputFlag(fs)
	n := fs.Int("n", 1, "the newest N tapes, 1 to 20")
	outDir := fs.String("out", defaultRunsDir(), "the runs directory")
	urlFlag := fs.String("url", "", "the service to publish to")
	yes := fs.Bool("yes", false, "publish without the question")
	noText := fs.Bool("no-text", false, "upload without the prompts and the generated text")
	dryRun := fs.Bool("dry-run", false, "print what would be uploaded and upload nothing")
	extra, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("share", usageFor("share"), fs, args, err)
	}
	format, refused := outputFor("share", *output)
	c.json = format.isJSON()
	if refused != nil {
		return c.fail(*refused)
	}
	if len(extra) > 0 {
		return c.usageTextf(shareUsage, "toktape share: unexpected argument %q; share picks the tapes itself", extra[0])
	}
	if *n < 1 || *n > shareMax {
		return c.usageTextf(shareUsage, "toktape share: -n is 1 to %d, got %d", shareMax, *n)
	}

	cfg, err := config.Load()
	if err != nil {
		return c.usagef("toktape: %v", err)
	}
	svc, err := resolveService(*urlFlag, "", cfg, os.Getenv)
	if err != nil {
		return c.usagef("toktape share: %v", err)
	}
	sayWithheldToken(c, cfg, svc)

	// Settled first, before anything is read or shown: a pipe without --yes
	// is refused, never asked.
	interactive := shareInteractive(c)
	if !*dryRun && !*yes && !interactive {
		return c.fail(failure{
			code: exitUsage,
			msg:  "toktape share: there is no terminal to ask on, and nothing is uploaded unasked",
			hint: "pass --yes to publish, or --dry-run to see what would go",
		})
	}

	picks, chats, unreadable, err := sharePicks(*outDir, *n)
	if err != nil {
		return c.usagef("toktape: %v", err)
	}
	if chats > 0 {
		fmt.Fprintf(c.stderr, "toktape share: skipped %d chat %s; a chat tape holds the conversation and share never publishes it (publish --include-conversation does)\n",
			chats, noun(chats, "tape", "tapes"))
	}
	if unreadable > 0 {
		fmt.Fprintf(c.stderr, "toktape share: skipped %d %s that could not be read; toktape ls lists them\n", unreadable, noun(unreadable, "file", "files"))
	}
	if len(picks) == 0 {
		switch {
		case chats > 0:
			fmt.Fprintln(c.stderr, "Nothing to publish: the tapes here are chat tapes. `toktape publish <tape> --include-conversation` sends one on purpose.")
		default:
			fmt.Fprint(c.stderr, "Nothing to publish. "+noRunsMessage(*outDir))
		}
		return c.shareOutput(nil, false)
	}

	// The public view, whatever happens next: the disclosure, the index and
	// the header of the block are all read from what would be sent.
	receiptsDir, err := config.Dir()
	if err != nil {
		return c.usagef("toktape: %v", err)
	}
	saved, err := publish.Saved(receiptsDir)
	if err != nil {
		// The file says which tapes are up; publishing past one that could
		// not be read would publish them twice.
		return c.usagef("toktape: %v", err)
	}
	legacy := 0
	for _, s := range saved {
		if !sameService(svc.BaseURL, firstSet(s.Service, publish.DefaultBaseURL)) {
			continue
		}
		if s.TapeSHA256 == "" {
			legacy++
		}
	}
	if legacy > 0 {
		fmt.Fprintf(c.stderr, "toktape share: %d earlier %s from this machine cannot be matched to files, so share may publish those tapes again\n",
			legacy, noun(legacy, "upload", "uploads"))
	}

	policy := publish.WithText
	switch {
	case *noText:
		policy = publish.WithoutText
	case !cfg.WantsText():
		policy = publish.WithoutText
	}
	var todo []*sharePick
	client := &publish.Client{BaseURL: svc.BaseURL, Token: svc.Token, UserAgent: "toktape/" + version}
	host := serviceHost(svc.BaseURL)
	for _, p := range picks {
		for _, s := range saved {
			if s.TapeSHA256 != "" && s.TapeSHA256 == p.sha && sameService(svc.BaseURL, firstSet(s.Service, publish.DefaultBaseURL)) {
				p.link = firstSet(s.URL, svc.BaseURL+"/r/"+s.ID)
				p.rid = s.ID
				break
			}
		}
		if p.link != "" {
			// One read per remembered link: the run may have been deleted or
			// taken down since, and the block must not hand out a dead link
			// (TTP-207). A private run stays: private is unlisted, out of the
			// search, and its link still opens (web/src/run.js).
			_, rerr := client.Run(ctx, p.rid)
			switch {
			case errors.Is(rerr, publish.ErrRunNotFound):
				if ferr := publish.Forget(receiptsDir, p.rid); ferr != nil {
					fmt.Fprintf(c.stderr, "toktape: could not drop the receipt for %s: %v\n", p.rid, ferr)
				}
				fmt.Fprintf(c.stderr, "toktape share: %s is no longer on %s; publishing it again\n", p.id, host)
				p.link, p.rid = "", ""
			case rerr != nil:
				fmt.Fprintf(c.stderr, "toktape share: could not check %s (%s); keeping its link\n", p.id, shortReason(rerr))
			}
		}
		if p.link == "" {
			todo = append(todo, p)
		} else {
			fmt.Fprintf(c.stderr, "toktape share: %s is already published at %s\n", p.id, p.link)
		}
	}

	if len(todo) > 0 {
		c.shareProfile(cfg, *yes, *dryRun, interactive)
	}
	author, err := authorFor(cfg, false)
	if err != nil {
		return c.usagef("toktape share: %v", err)
	}
	var bio string
	if svc.Token != "" && cfg.ProfileBio != "" {
		if bio, err = publish.ValidateBio(cfg.ProfileBio); err != nil {
			return c.usagef("toktape share: %v", err)
		}
	}
	opts := publish.Options{Text: policy, Author: author, Bio: bio}
	for _, p := range picks {
		p.view = publish.PublicView(p.tp, opts.Text)
		p.idx = publish.IndexOf(p.view)
	}

	results := make([]shareResult, 0, len(picks))
	switch {
	case len(todo) == 0:
		fmt.Fprintf(c.stderr, "Nothing new to publish: the newest %s already published.\n", noun(len(picks), "tape is", "tapes are"))
	case *dryRun:
		for _, p := range todo {
			fmt.Fprint(c.stderr, "\n"+publish.Preview(p.view, p.idx, opts))
		}
		fmt.Fprintf(c.stderr, "\n--dry-run: %d %s would go to %s; nothing was uploaded.\n", len(todo), noun(len(todo), "tape", "tapes"), serviceHost(svc.BaseURL))
	default:
		if code := c.shareConfirm(cfg, todo, opts, svc, *yes); code != exitOK {
			return code
		}
		for _, p := range picks {
			r := shareResult{File: p.path, pick: p}
			switch {
			case p.link != "":
				r.URL, r.Skipped = p.link, true
			default:
				receipt, err := client.Upload(ctx, p.view, p.idx, opts)
				if err != nil {
					r.Error = err.Error()
					fmt.Fprintf(c.stderr, "toktape share: %s was not published: %v\n", p.id, err)
					break
				}
				r.URL = receipt.URL
				var tokenErr error
				if receipt.DeleteToken != "" || svc.Token != "" {
					tokenErr = rememberUpload(receipt, svc, p.sha)
				}
				fmt.Fprintf(c.stderr, "toktape share: published %s at %s\n", p.id, receipt.URL)
				c.sayDeleteToken(receipt, tokenErr)
				if receipt.DeleteToken == "" && tokenErr != nil {
					fmt.Fprintf(c.stderr, "toktape: could not record that %s is up, so the next share may publish it again: %v\n", p.id, tokenErr)
				}
			}
			results = append(results, r)
		}
	}
	if len(results) == 0 {
		// Nothing was uploaded: the remembered links, or the dry run's
		// placeholders, make the block.
		for _, p := range picks {
			r := shareResult{File: p.path, pick: p, URL: p.link, Skipped: p.link != ""}
			results = append(results, r)
		}
	}
	return c.shareOutput(results, *dryRun)
}

// shareOutput prints the block (or the -o json array) and settles the exit
// code: a failed upload is exitPublish, with the block of the rest printed.
func (c *cli) shareOutput(results []shareResult, dryRun bool) int {
	failed := 0
	for _, r := range results {
		if r.Error != "" {
			failed++
		}
	}
	if c.json {
		if results == nil {
			results = []shareResult{}
		}
		b, err := json.Marshal(results)
		if err != nil {
			return c.failf(exitPublish, "toktape: %v", err)
		}
		fmt.Fprintf(c.stdout, "%s\n", b)
	} else if block := shareBlock(results, dryRun); block != "" {
		fmt.Fprint(c.stdout, block)
		if !dryRun && isTTY(c.stdout) {
			copyToClipboard(c.stderr, block)
		}
	}
	if failed > 0 {
		fmt.Fprintf(c.stderr, "toktape share: %d of %d %s not published; share again to retry them\n", failed, len(results), noun(len(results), "tape was", "tapes were"))
		return exitPublish
	}
	return exitOK
}

// sharePicks is the newest n tapes of dir by the rule ls lists them in (the
// run id, newest first), chat tapes left out of the count. chats is how many
// it stepped over; unreadable how many files would not load.
func sharePicks(dir string, n int) (picks []*sharePick, chats, unreadable int, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, 0, nil
		}
		return nil, 0, 0, err
	}
	var all []*sharePick
	for _, e := range entries {
		if e.IsDir() || !tape.IsRunFile(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		tp, rerr := tape.Read(path)
		if rerr != nil {
			unreadable++
			continue
		}
		all = append(all, &sharePick{path: path, id: tp.Summary.ID, tp: tp})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].id > all[j].id })
	for _, p := range all {
		if len(picks) == n {
			break
		}
		if p.tp.Summary.IsEmbedding() {
			continue // not publishable yet
		}
		if publish.HoldsConversation(p.tp) {
			chats++
			continue
		}
		p.sha = tapeHash(p.path)
		picks = append(picks, p)
	}
	return picks, chats, unreadable, nil
}

// shareProfile asks for a name and a link when none is saved (TTP-206): on a
// terminal, once, skippable, and never with --yes or a dry run. Anywhere else
// one line says the run goes without a name and how to set one.
func (c *cli) shareProfile(cfg *config.Config, yes, dryRun, interactive bool) {
	if cfg.ProfileName != "" {
		return
	}
	if yes || dryRun || !interactive {
		fmt.Fprintln(c.stderr, "toktape share: publishing without a name; `toktape profile --name \"<your name>\" --link \"<your page>\"` sets one for next time")
		return
	}
	name := c.shareAsk("name", publish.ValidateAuthorName)
	link := c.shareAsk("link", publish.ValidateAuthorLink)
	if name == "" && link == "" {
		return
	}
	// The same fields and the same save `toktape profile` uses.
	if name != "" {
		cfg.ProfileName = name
	}
	if link != "" {
		cfg.ProfileLink = link
	}
	if err := config.Save(cfg); err != nil {
		fmt.Fprintf(c.stderr, "toktape: could not save the profile: %v\n", err)
	}
}

// shareAsk asks for one profile field. An empty answer skips it; a bad one is
// said and asked again once.
func (c *cli) shareAsk(label string, validate func(string) (string, error)) string {
	for try := 0; try < 2; try++ {
		fmt.Fprintf(c.stderr, "%s (Enter to skip): ", label)
		line, err := readLine(shareStdin)
		line = strings.TrimSpace(line)
		if err != nil || line == "" {
			return ""
		}
		v, verr := validate(line)
		if verr == nil {
			return v
		}
		fmt.Fprintf(c.stderr, "toktape share: %v\n", verr)
	}
	fmt.Fprintf(c.stderr, "toktape share: no %s; `toktape profile` sets it any time\n", label)
	return ""
}

// shareConfirm shows what leaves for each tape to upload, then asks the one
// question. The listing is the one publish --dry-run prints, so what is
// agreed to is what publish would send; the first-publish note is part of it,
// and agreeing records it, as publish does.
func (c *cli) shareConfirm(cfg *config.Config, todo []*sharePick, opts publish.Options, svc serviceChoice, yes bool) int {
	for _, p := range todo {
		fmt.Fprint(c.stderr, "\n"+publish.Preview(p.view, p.idx, opts))
	}
	if !cfg.FirstPublishWarningSeen {
		fmt.Fprint(c.stderr, "\nThis is the first run published from this machine, so here is what leaves it.\nYou will not be asked again; `toktape share --dry-run` prints this listing any time.\n")
	}
	if !yes {
		fmt.Fprintf(c.stderr, "\npublish %d tape(s) to %s? [Y/n] ", len(todo), serviceHost(svc.BaseURL))
		answer, err := readLine(shareStdin)
		if err != nil {
			return c.failf(exitUsage, "toktape: could not read the answer: %v", err)
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y", "yes":
		default:
			return c.failf(exitPublish, "toktape: not published")
		}
	}
	if !cfg.FirstPublishWarningSeen {
		cfg.FirstPublishWarningSeen = true
		if err := config.Save(cfg); err != nil {
			fmt.Fprintf(c.stderr, "toktape: could not record that you have seen this: %v\n", err)
		}
	}
	return exitOK
}

// rememberUpload files what an upload leaves behind for the next share: an
// anonymous run's delete token with the tape's hash (what publish files), or,
// for a token-owned run that has no key to file, the hash and the link alone.
func rememberUpload(r *publish.Receipt, svc serviceChoice, tapeSHA string) error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	if r.DeleteToken != "" {
		return publish.RememberTape(dir, *r, svc.BaseURL, tapeSHA)
	}
	return publish.RememberOwned(dir, *r, svc.BaseURL, tapeSHA)
}

// tapeHash is the sha256 of a tape file, hex, "" when it cannot be read.
func tapeHash(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func serviceHost(base string) string {
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		return u.Host
	}
	return base
}

func noun(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// shareBlock is the paste block: one header line, one Markdown line per tape
// that has a link, in pick order. A dry run's tapes still to go carry a
// placeholder where the link will be.
//
// The header's host is the hostname as uploaded — the public view's, which
// keeps a hostname only when the recorder labelled it — and only when every
// tape in the block agrees on a non-empty one. Anything unknown is left out of
// a line, never printed as "?" inside a link title.
func shareBlock(results []shareResult, dryRun bool) string {
	var lines []string
	host, same := "", true
	for i, r := range results {
		link := r.URL
		if link == "" {
			if !dryRun || r.Error != "" {
				continue
			}
			link = "<link>"
		}
		h := r.pick.view.Summary.Host.Hostname
		if i == 0 || len(lines) == 0 {
			host = h
		} else if h != host {
			same = false
		}
		lines = append(lines, blockLine(r.pick.idx, link))
	}
	if len(lines) == 0 {
		return ""
	}
	header := "toktape runs:"
	if same && host != "" {
		header = "toktape runs from " + host + ":"
	}
	return header + "\n" + strings.Join(lines, "\n") + "\n"
}

// blockLine is `- [figure · model · quant](link) — GPUs · engine`, from the
// index and with the strings the hub's preview title uses (web/src/run.js
// shareTitle): the decode rate or the decision p50, the model's normalised id
// or its file name, the quantisation unless the name already says it.
func blockLine(idx publish.Index, link string) string {
	var parts []string
	if idx.Mode == tape.ModeDecision {
		if idx.DecisionP50Ms > 0 {
			parts = append(parts, hubFigure(idx.DecisionP50Ms)+" ms p50 decision")
		}
	} else if idx.DecodePerSec > 0 {
		parts = append(parts, hubFigure(idx.DecodePerSec)+" tok/s")
	}
	name := idx.ModelID
	if name == "" {
		name = strings.TrimSuffix(idx.ModelRaw, ".gguf")
	}
	if name != "" {
		parts = append(parts, name)
	}
	if idx.QuantRaw != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(idx.QuantRaw)) {
		parts = append(parts, idx.QuantRaw)
	}
	title := strings.NewReplacer("[", "(", "]", ")").Replace(strings.Join(parts, " · "))
	if title == "" {
		title = "toktape run"
	}

	var where []string
	if len(idx.GPUsRaw) > 0 {
		where = append(where, hubGPUNames(idx.GPUsRaw))
	}
	if e := strings.TrimSpace(idx.EngineKind + " " + idx.EngineVersion); e != "" {
		where = append(where, e)
	}
	line := "- [" + title + "](" + link + ")"
	if len(where) > 0 {
		line += " — " + strings.Join(where, " · ")
	}
	return line
}

// hubFigure rounds the way the hub does (web/src/page.js fmt): whole numbers
// from 100, one decimal below.
func hubFigure(v float64) string {
	if v >= 100 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// hubGPUNames is web/src/run.js gpuNames: the names as the cards gave them,
// "2× name" when they are all one.
func hubGPUNames(raw []string) string {
	same := len(raw) > 1
	for _, n := range raw {
		if n != raw[0] {
			same = false
		}
	}
	if same {
		return fmt.Sprintf("%d× %s", len(raw), raw[0])
	}
	return strings.Join(raw, " / ")
}

// shortReason is the last clause of an error, cut to one short line.
func shortReason(err error) string {
	m := err.Error()
	if i := strings.LastIndex(m, ": "); i >= 0 && len(m)-i > 12 {
		m = m[i+2:]
	}
	if len(m) > 80 {
		m = m[:80] + "…"
	}
	return m
}
