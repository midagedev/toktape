package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/midagedev/toktape/internal/config"
	"github.com/midagedev/toktape/internal/publish"
	"github.com/midagedev/toktape/internal/tape"
)

// publishFlags is every flag the publish verb declares.
type publishFlags struct {
	output    *string
	dryRun    *bool
	private   *bool
	noText    *bool
	withText  *bool
	yes       *bool
	url       *string
	noProfile *bool
	title     *string
	note      *string
	noteFile  *string
}

// declarePublishFlags registers the publish verb's flags on fs.
func declarePublishFlags(fs *flag.FlagSet) *publishFlags {
	return &publishFlags{
		output:  declareOutputFlag(fs),
		dryRun:  fs.Bool("dry-run", false, "print what would be uploaded and upload nothing"),
		private: fs.Bool("private", false, "keep the run out of the search (the link still works)"),
		// Two flags rather than one --text=BOOL because both are used to
		// override, in opposite directions: --no-text on a machine that
		// publishes text, --with-text on a machine whose config turned it
		// off. A boolean flag cannot spell the second one without the reader
		// having to know the config's value to predict what it does.
		noText:    fs.Bool("no-text", false, "upload without the prompts and the generated text"),
		withText:  fs.Bool("with-text", false, "upload with them, overriding publish_text in the config"),
		yes:       fs.Bool("yes", false, "acknowledge the first-publish warning without being asked"),
		url:       fs.String("url", "", "the service to publish to (default "+publish.DefaultBaseURL+")"),
		noProfile: fs.Bool("no-profile", false, "this run travels without the author profile"),
		title:     fs.String("title", "", "the lab-note's title for this run"),
		note:      fs.String("note", "", "the lab-note's body for this run"),
		noteFile:  fs.String("note-file", "", "read the lab-note's body from this file"),
	}
}

// runPublish uploads one recorded run and prints its link.
//
// The tape on disk is never modified and never re-measured: publishing is a
// rendering of a run that already happened, the same as `card`. What it adds
// over the other verbs is that the rendering leaves this machine, which is
// why every decision about what leaves is made before anything is sent — the
// public view, the index and the preview are all built first, and --dry-run
// stops exactly there.
func runPublish(ctx context.Context, c *cli, args []string) int {
	fs := newFlagSet("publish")
	f := declarePublishFlags(fs)
	files, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("publish", usageFor("publish"), args, err)
	}
	format, refused := outputFor("publish", *f.output)
	c.json = format.isJSON()
	if refused != nil {
		return c.fail(*refused)
	}
	if len(files) != 1 {
		return c.usageTextf(usageText, "toktape publish: expected one tape file")
	}
	if *f.noText && *f.withText {
		return c.usagef("toktape publish: --no-text and --with-text say opposite things")
	}
	if *f.note != "" && *f.noteFile != "" {
		return c.usagef("toktape publish: --note and --note-file say the same thing twice")
	}

	cfg, err := config.Load()
	if err != nil {
		// The config holds the opt-out. Publishing past a file we could not
		// read would be publishing past a decision somebody made.
		return c.usagef("toktape: %v", err)
	}

	tp, err := tape.Read(files[0])
	if err != nil {
		return c.usagef("toktape: %v", err)
	}

	note, err := noteText(f)
	if err != nil {
		return c.usagef("toktape publish: %v", err)
	}
	// The profile is read from the config — the resident form `toktape
	// profile` wrote — and validated again here, because the file is
	// hand-editable and the set-time check is not a guarantee.
	author, err := authorFor(cfg, *f.noProfile)
	if err != nil {
		return c.usagef("toktape publish: %v", err)
	}
	title := *f.title
	if title != "" {
		if title, err = publish.ValidateTitle(title); err != nil {
			return c.usagef("toktape publish: %v", err)
		}
	}
	if note != "" {
		if note, err = publish.ValidateNote(note); err != nil {
			return c.usagef("toktape publish: %v", err)
		}
	}

	opts := publish.Options{Text: textPolicy(f, cfg), Private: *f.private, Author: author, Title: title, Note: note}
	view := publish.PublicView(tp, opts.Text)
	idx := publish.IndexOf(view)
	preview := publish.Preview(view, idx, opts)

	if *f.dryRun {
		// The product of a dry run is the listing, so it goes to stdout.
		fmt.Fprint(c.stdout, preview)
		return exitOK
	}

	if !cfg.FirstPublishWarningSeen {
		if code := c.firstPublishWarning(preview, *f.yes); code != exitOK {
			return code
		}
		cfg.FirstPublishWarningSeen = true
		if err := config.Save(cfg); err != nil {
			// Not fatal: the upload the user just agreed to is still the
			// thing they asked for, and the only cost is being asked again.
			fmt.Fprintf(c.stderr, "toktape: could not record that you have seen this: %v\n", err)
		}
	}

	client := &publish.Client{
		BaseURL:   *f.url,
		Token:     cfg.Token,
		UserAgent: "toktape/" + version,
	}
	receipt, err := client.Upload(ctx, view, idx, opts)
	if err != nil {
		return c.fail(failure{
			code: exitPublish,
			msg:  fmt.Sprintf("toktape: %v", err),
			hint: "toktape publish " + files[0] + " --dry-run prints what would be sent, and touches no network",
		})
	}
	return c.publishReceipt(receipt)
}

// noteText resolves the lab-note's body to one string. --note and
// --note-file together is a usage error, settled before this is called.
func noteText(f *publishFlags) (string, error) {
	if *f.noteFile == "" {
		return *f.note, nil
	}
	b, err := os.ReadFile(*f.noteFile)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", *f.noteFile, err)
	}
	return string(b), nil
}

// authorFor builds the profile one publish carries from the resident
// config, or nil when nothing about the author travels: --no-profile, or a
// machine that never set one. Every field is validated with the contract's
// limits — the config file is hand-editable, so the set-time check is not a
// guarantee — and the refusal names the limit.
func authorFor(cfg *config.Config, noProfile bool) (*publish.Author, error) {
	if noProfile {
		return nil, nil
	}
	if cfg.ProfileName == "" && cfg.ProfileLink == "" && cfg.ProfileAvatar == "" {
		return nil, nil
	}
	a := &publish.Author{}
	if cfg.ProfileName != "" {
		name, err := publish.ValidateAuthorName(cfg.ProfileName)
		if err != nil {
			return nil, err
		}
		a.Name = name
	}
	if cfg.ProfileLink != "" {
		link, err := publish.ValidateAuthorLink(cfg.ProfileLink)
		if err != nil {
			return nil, err
		}
		a.Link = link
	}
	if cfg.ProfileAvatar != "" {
		avatar, err := publish.LoadAvatar(expandHome(cfg.ProfileAvatar))
		if err != nil {
			return nil, err
		}
		a.Avatar = avatar
	}
	return a, nil
}

// textPolicy resolves the one setting with three sources. The order is the
// one §9.3 states: a flag beats the config, and the config beats the default,
// which is that the body travels.
func textPolicy(f *publishFlags, cfg *config.Config) publish.TextPolicy {
	switch {
	case *f.noText:
		return publish.WithoutText
	case *f.withText:
		return publish.WithText
	case !cfg.WantsText():
		return publish.WithoutText
	default:
		return publish.WithText
	}
}

// publishReceipt prints the link. It is the product, so it goes to stdout on
// one line and `url=$(toktape publish run.tape)` works.
func (c *cli) publishReceipt(r *publish.Receipt) int {
	if c.json {
		b, err := json.Marshal(r)
		if err != nil {
			return c.failf(exitPublish, "toktape: %v", err)
		}
		fmt.Fprintf(c.stdout, "%s\n", b)
		return exitOK
	}
	fmt.Fprintln(c.stdout, r.URL)
	if r.DeleteToken != "" {
		// Printed once and stored nowhere. Keeping it in the config would
		// turn that file into a list of everything this machine ever posted,
		// which is a record nobody asked for.
		fmt.Fprintf(c.stderr, "\nDelete token: %s\nThis is the only key to that upload and it is not saved anywhere. Keep it or lose the ability to take the run down.\n", r.DeleteToken)
	}
	return exitOK
}

// firstPublishWarning shows what is about to leave the machine, once per
// machine (§9.3).
//
// Once, not every time: a warning that asks on every upload is one people
// learn to dismiss without reading, and an opt-out you have to remember to
// pass is not an opt-out. It shows the same listing --dry-run prints, because
// a warning that showed less than the dry run would understate what it is
// asking about.
//
// The answer is read from the terminal and never from stdin. A publish in a
// pipeline must not eat the data flowing through it, and stdin in a script is
// not a person. When there is no terminal this refuses and names --yes rather
// than assuming consent: the one question this program asks is the one it
// must not answer on the user's behalf.
func (c *cli) firstPublishWarning(preview string, yes bool) int {
	fmt.Fprint(c.stderr, preview)
	fmt.Fprint(c.stderr, "\nThis is the first run published from this machine, so here is what leaves it.\nYou will not be asked again; `--dry-run` prints this listing any time.\n")

	if yes {
		fmt.Fprintln(c.stderr, "\n--yes was given, so publishing.")
		return exitOK
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return c.fail(failure{
			code: exitUsage,
			msg:  "toktape: this is the first publish from this machine and there is no terminal to ask on",
			hint: "pass --yes to acknowledge the listing above, or run it once from a terminal",
		})
	}
	defer tty.Close()

	fmt.Fprint(tty, "\nPublish it? [y/N] ")
	answer, err := readLine(tty)
	if err != nil {
		return c.failf(exitUsage, "toktape: could not read the answer: %v", err)
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return exitOK
	default:
		// Nothing went wrong, but nothing was published either, and stdout is
		// empty — so `url=$(toktape publish run.tape)` must not exit 0 with an
		// empty url and look like it worked. exitPublish says what is true of
		// both this and a failed upload: the run is not up.
		return c.failf(exitPublish, "toktape: not published")
	}
}

// readLine reads one line, byte at a time, because the terminal is shared:
// buffering would swallow whatever the user types next.
func readLine(r io.Reader) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return b.String(), nil
			}
			b.WriteByte(buf[0])
		}
		if err != nil {
			if err == io.EOF {
				return b.String(), nil
			}
			return "", err
		}
	}
}
