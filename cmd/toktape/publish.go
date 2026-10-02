package main

import (
	"context"
	"encoding/json"
	"errors"
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
	public    *bool
	noText    *bool
	withText  *bool
	yes       *bool
	url       *string
	noProfile *bool
	title     *string
	note      *string
	noteFile  *string
	edit      *string
	del       *string
	token     *string
	// conversation is --include-conversation: a chat tape travels only
	// when the upload names this.
	conversation *bool
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
		url:       fs.String("url", "", "the service to publish to: this flag, TOKTAPE_SERVICE, service in config.toml, else "+publish.DefaultBaseURL),
		noProfile: fs.Bool("no-profile", false, "this run travels without the author profile"),
		title:     fs.String("title", "", "the lab-note's title for this run (or its new title with --edit)"),
		note:      fs.String("note", "", "the lab-note's body for this run (or its new body with --edit)"),
		noteFile:  fs.String("note-file", "", "read the lab-note's body from this file"),
		public:    fs.Bool("public", false, "with --edit: list the run in the search again"),
		edit:      fs.String("edit", "", "change a run you own instead of uploading: the run id or its /r/<id> link"),
		del:       fs.String("delete", "", "take a published run down: the run id or its /r/<id> link"),
		token:     fs.String("token", "", "a token to present as typed, to whatever --url names (with --delete: the run's delete token)"),
		// A flag of its own, not --with-text: a chat tape's text is what a
		// person typed, and the machine-wide publish_text says nothing about
		// that. The name says what leaves.
		conversation: fs.Bool("include-conversation", false, "publish a chat tape, the conversation's text included"),
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
		return c.badFlags("publish", usageFor("publish"), fs, args, err)
	}
	format, refused := outputFor("publish", *f.output)
	c.json = format.isJSON()
	if refused != nil {
		return c.fail(*refused)
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

	// --delete is the third operation on the same verb: nothing is uploaded
	// and no tape is read; one published run is taken down.
	if *f.del != "" {
		if *f.edit != "" {
			return c.usagef("toktape publish: --delete and --edit are different operations on a run; name one")
		}
		if len(files) != 0 {
			return c.usagef("toktape publish: --delete takes the run id itself, not a tape file")
		}
		return runPublishDelete(ctx, c, f, cfg)
	}

	// --edit is a different operation on the same verb: no tape is read and
	// nothing is uploaded; one owned run is changed instead.
	if *f.edit != "" {
		if len(files) != 0 {
			return c.usagef("toktape publish: --edit takes the run id itself, not a tape file")
		}
		return runPublishEdit(ctx, c, f, cfg)
	}
	if *f.public {
		return c.usagef("toktape publish: --public lists a run again and only makes sense with --edit")
	}

	// Which service, and with which token — one resolver for every verb that
	// reaches a hub (cmd/toktape/service.go): the journal token in the config
	// belongs to the config's service and travels nowhere else, so a --url
	// pointing elsewhere publishes anonymously rather than leaking it.
	svc, err := resolveService(*f.url, *f.token, cfg, os.Getenv)
	if err != nil {
		return c.usagef("toktape publish: %v", err)
	}
	sayWithheldToken(c, cfg, svc)
	if len(files) != 1 {
		return c.usageTextf(usageText, "toktape publish: expected one tape file")
	}
	if *f.noText && *f.withText {
		return c.usagef("toktape publish: --no-text and --with-text say opposite things")
	}

	tp, err := tape.Read(files[0])
	if err != nil {
		return c.usagef("toktape: %v", err)
	}
	// Before the dry run and the first-publish question: a chat is refused
	// here, whatever else was typed, so the flag is the only way through —
	// --yes and --with-text answer different questions (2026-09-24).
	// publish.Client.Upload asks the same predicate again.
	switch chat := publish.HoldsConversation(tp); {
	case chat && !*f.conversation:
		return c.usagef("toktape publish: %s is a chat tape and holds the conversation's text; --include-conversation publishes it anyway", files[0])
	case !chat && *f.conversation:
		return c.usagef("toktape publish: --include-conversation is for a chat tape, and %s is not a chat", files[0])
	case *f.conversation && *f.noText:
		// The flag's name promises the conversation goes; --no-text would
		// send the tape without it.
		return c.usagef("toktape publish: --include-conversation and --no-text say opposite things")
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

	// The bio travels only on a token-owned publish: an anonymous run has
	// no home to show it on, so without a token it is left off here (and
	// the uploader drops it again, so a hand-built Options cannot send it
	// either). svc.Token, not cfg.Token — a withheld journal token makes
	// this an anonymous upload too.
	var bio string
	if svc.Token != "" && cfg.ProfileBio != "" {
		if bio, err = publish.ValidateBio(cfg.ProfileBio); err != nil {
			return c.usagef("toktape publish: %v", err)
		}
	}

	opts := publish.Options{Text: textPolicy(f, cfg), Private: *f.private, Author: author, Title: title, Note: note, Bio: bio,
		IncludeConversation: *f.conversation}
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
		BaseURL:   svc.BaseURL,
		Token:     svc.Token,
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
	// An anonymous upload's key is the only one there will ever be, and the
	// upload has already succeeded — so remembering it can only warn, never
	// unwind the publish. The service rides along: the key opens the run only
	// where it was issued.
	var tokenErr error
	if receipt.DeleteToken != "" {
		tokenErr = rememberDeleteToken(receipt, svc.BaseURL)
	}
	return c.publishReceipt(receipt, tokenErr)
}

// rememberDeleteToken files an anonymous upload's delete token and service
// under ~/.toktape/published.json (publish.Remember).
func rememberDeleteToken(r *publish.Receipt, service string) error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	return publish.Remember(dir, *r, service)
}

// runPublishDelete takes one published run down. The service is the one that
// knows the run, in order of how much each source knows about it: the --url
// flag, the host inside a pasted /r/<id> link, the service the receipt
// recorded when this machine uploaded the run, then the resident chain
// (TOKTAPE_SERVICE, config, default — cmd/toktape/service.go). The key is
// whichever of the three the service accepts, in a fixed order: a --token
// typed on the command line, the saved delete token — presented only to the
// service its entry names, since it opens the run nowhere else — or the
// journal token in the config, which the resolver sends only to the service
// it belongs to. Already-gone is a success shape, because the server reads it
// that way (web/src/del.js): a delete retried after a dropped connection must
// not report a failure the second time. Either way the saved entry is
// dropped, so the file never holds a key to a run that is no longer up.
func runPublishDelete(ctx context.Context, c *cli, f *publishFlags, cfg *config.Config) int {
	id := editTargetID(*f.del)
	if id == "" {
		return c.usagef("toktape publish: --delete wants a run id or its /r/<id> link")
	}

	receiptsDir, err := config.Dir()
	if err != nil {
		return c.usagef("toktape: %v", err)
	}
	srcFlag := "the --token flag"
	srcEnvToken := "the TOKTAPE_TOKEN variable"
	srcSaved := "the saved delete token in " + tildePath(publish.ReceiptsPath(receiptsDir))
	srcJournal := "the journal token in config.toml"

	// The service the run lives on. An entry from before services were
	// recorded (empty service) went to the hosted one, so that is what it
	// means here, not "wherever the config points".
	savedSvc, savedTok, hasSaved := publish.EntryFor(receiptsDir, id)
	savedHome := firstSet(savedSvc, publish.DefaultBaseURL)
	flagURL := firstSet(*f.url, runLinkBase(*f.del))
	if flagURL == "" && hasSaved {
		flagURL = savedHome
	}
	svc, err := resolveService(flagURL, *f.token, cfg, os.Getenv)
	if err != nil {
		return c.usagef("toktape publish: %v", err)
	}

	var token, source string
	switch {
	case *f.token != "":
		token, source = *f.token, srcFlag
	case os.Getenv("TOKTAPE_TOKEN") != "":
		token, source = os.Getenv("TOKTAPE_TOKEN"), srcEnvToken
	case hasSaved && sameService(svc.BaseURL, savedHome):
		token, source = savedTok, srcSaved
	case svc.Token != "":
		token, source = svc.Token, srcJournal
	default:
		// The journal token was the last candidate and stayed home (or there
		// never was one); the note says which knob turns, the refusal names
		// all three sources.
		sayWithheldToken(c, cfg, svc)
		return c.usagef("toktape publish --delete %s: no key to present; it would come from %s, %s or %s",
			id, srcFlag, srcSaved, srcJournal)
	}

	if *f.dryRun {
		// The source, never the token: a dry run's whole job is to show what
		// would happen without doing it, and the token is the one secret in
		// this verb.
		fmt.Fprintf(c.stdout, "would delete %s with %s\n", id, source)
		return exitOK
	}

	client := &publish.Client{
		BaseURL:   svc.BaseURL,
		UserAgent: "toktape/" + version,
	}
	err = client.Delete(ctx, id, token)
	switch {
	case err == nil:
		fmt.Fprintf(c.stdout, "deleted %s\n", id)
	case errors.Is(err, publish.ErrGone):
		fmt.Fprintf(c.stdout, "%s\n", err)
	default:
		return c.fail(failure{
			code: exitPublish,
			msg:  fmt.Sprintf("toktape: %v", err),
			hint: "the key has to be the run's own delete token or the journal token that owns it",
		})
	}
	// The run is down (or was never there to begin with); a key to it is now
	// a key to nothing, and a warning about the cleanup is not a failure.
	if forgetErr := publish.Forget(receiptsDir, id); forgetErr != nil {
		fmt.Fprintf(c.stderr, "toktape: could not remove the saved entry for %s: %v\n", id, forgetErr)
	}
	return exitOK
}

// runPublishEdit changes one owned run instead of uploading (TTP-127).
//
// The run is named by id or by its /r/<id> link. Only the flags given
// travel — a flag left off leaves its field alone — and the journal token
// in the config is the only key: an anonymous run cannot be edited and a
// delete token cannot edit. On success the run's link goes to stdout, the
// same product an upload prints.
func runPublishEdit(ctx context.Context, c *cli, f *publishFlags, cfg *config.Config) int {
	if *f.dryRun {
		return c.usagef("toktape publish: --dry-run prints what would be uploaded, and --edit uploads nothing")
	}
	if *f.private && *f.public {
		return c.usagef("toktape publish: --private and --public say opposite things")
	}
	if *f.title == "" && *f.note == "" && *f.noteFile == "" && !*f.private && !*f.public {
		return c.usagef("toktape publish: --edit changes nothing without --title, --note or --private/--public")
	}
	// One resolver for every verb that reaches a hub: the journal token
	// travels only to the service it belongs to (cmd/toktape/service.go), and
	// an edit aimed elsewhere is refused here rather than sent tokenless.
	svc, err := resolveService(*f.url, *f.token, cfg, os.Getenv)
	if err != nil {
		return c.usagef("toktape publish: %v", err)
	}
	sayWithheldToken(c, cfg, svc)
	if svc.Token == "" {
		return c.usagef("toktape publish --edit needs a journal token in config.toml; an anonymous run cannot be edited")
	}

	id := editTargetID(*f.edit)
	if id == "" {
		return c.usagef("toktape publish: --edit wants a run id or its /r/<id> link")
	}

	var e publish.Edit
	if *f.title != "" {
		title := *f.title
		e.Title = &title
	}
	if *f.note != "" || *f.noteFile != "" {
		note, err := noteText(f)
		if err != nil {
			return c.usagef("toktape publish: %v", err)
		}
		e.Note = &note
	}
	if *f.private || *f.public {
		private := *f.private
		e.Private = &private
	}

	client := &publish.Client{
		BaseURL:   svc.BaseURL,
		Token:     svc.Token,
		UserAgent: "toktape/" + version,
	}
	receipt, err := client.Edit(ctx, id, e)
	if err != nil {
		return c.fail(failure{
			code: exitPublish,
			msg:  fmt.Sprintf("toktape: %v", err),
			hint: "only the journal token that owns the run can change it",
		})
	}
	// An edit is token-owned, so there is no delete token to remember.
	return c.publishReceipt(receipt, nil)
}

// editTargetID takes the id or the link: a bare id travels as-is, and a
// /r/<id> URL gives up everything before /r/ and one trailing extension
// (.json, .tape, .toktape, .png — the page, the record, the card).
func editTargetID(arg string) string {
	s := arg
	if i := strings.LastIndex(s, "/r/"); i >= 0 {
		s = s[i+len("/r/"):]
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "."); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
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
//
// tokenErr is the outcome of remembering an anonymous upload's delete token:
// nil when it was saved (or there was none to save), an error when the store
// could not be written — which downgrades the message to a warning, because
// the upload has already succeeded and unwinding it is not an option.
func (c *cli) publishReceipt(r *publish.Receipt, tokenErr error) int {
	if c.json {
		b, err := json.Marshal(r)
		if err != nil {
			return c.failf(exitPublish, "toktape: %v", err)
		}
		fmt.Fprintf(c.stdout, "%s\n", b)
		return exitOK
	}
	fmt.Fprintln(c.stdout, r.URL)
	if r.DeleteToken == "" {
		return exitOK
	}
	// Printed once, and now also saved (2026-09-20). The old comment here
	// argued against storing: keeping the token would be "a record nobody
	// asked for". The user asked — take-down had to be one command, and a key
	// that exists only in terminal scrollback is a key people lose. The file
	// holds only the anonymous uploads from this machine, it is 0600 next to
	// config.toml, and `--delete` removes the entry once the run is down.
	if tokenErr == nil {
		fmt.Fprintf(c.stderr, "\nDelete token: %s\nSaved to %s; `toktape publish --delete %s` takes the run down.\n",
			r.DeleteToken, tildePath(publish.ReceiptsPath(mustConfigDir())), r.ID)
		return exitOK
	}
	fmt.Fprintf(c.stderr, "\nDelete token: %s\nWarning: could not save it for `toktape publish --delete`: %v.\nThis is the only key to that upload — keep it or lose the ability to take the run down.\n",
		r.DeleteToken, tokenErr)
	return exitOK
}

// mustConfigDir is the config directory for display. The publish that got
// this far already read a config, so a home directory that answers now would
// be a surprise; the empty string keeps the message truthful rather than
// failing the run over where to print a path.
func mustConfigDir() string {
	dir, err := config.Dir()
	if err != nil {
		return ""
	}
	return dir
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
