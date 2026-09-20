package publish

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// Preview is what is about to be uploaded, field by field.
//
// It is the whole argument for a structured record. asciinema and VHS ship a
// byte stream, so the only honest thing either could say before an upload is
// "the recording"; a tape knows which field is the hostname and which is the
// model's path, so this can name each one and say what became of it.
//
// One function, two callers, on purpose (§9.3): `publish --dry-run` prints it
// as the product, and the one-time first-publish warning prints it as the
// thing being warned about. A warning that showed less than the dry run would
// be a warning that understated what it was asking about.
//
// view is the public view, so this reports what survived rather than what
// would: everything below is read out of the tape that is going to be sent.
func Preview(view *tape.Tape, idx Index, opts Options) string {
	var b strings.Builder
	s := &view.Summary

	fmt.Fprintf(&b, "Publishing %s\n", nonEmpty(s.ID, "a run with no id"))
	fmt.Fprintf(&b, "  visibility     %s\n", visibility(opts))
	fmt.Fprintf(&b, "  recorded       %s\n", tape.Stamp(s.StartedAt))
	b.WriteString("\n")

	b.WriteString("What is in it\n")
	fmt.Fprintf(&b, "  model          %s\n", nonEmpty(idx.ModelRaw, "?"))
	if idx.Repo != "" {
		fmt.Fprintf(&b, "  repo           %s\n", idx.Repo)
	}
	fmt.Fprintf(&b, "  engine         %s\n", strings.TrimSpace(nonEmpty(idx.EngineKind, "?")+" "+idx.EngineVersion))
	fmt.Fprintf(&b, "  host           %s · %s\n", nonEmpty(idx.OS, "?"), hostLine(idx))
	fmt.Fprintf(&b, "  prompt set     %s\n", promptSetLine(s.PromptSet))
	b.WriteString("\n")

	b.WriteString("What was taken out\n")
	fmt.Fprintf(&b, "  hostname       %s\n", hostnameLine(s))
	fmt.Fprintf(&b, "  server url     %s\n", nonEmpty(s.Server.URL, "removed"))
	fmt.Fprintf(&b, "  model path     removed · the file name %s travels\n", nonEmpty(s.Model.FileName, "is not recorded either"))
	fmt.Fprintf(&b, "  server argv    %s\n", argvLine(s))
	b.WriteString("\n")

	b.WriteString("What travels besides the record\n")
	fmt.Fprintf(&b, "  text           %s\n", textLine(view, opts))
	// The card is uploaded, not drawn by the service — drawing one needs the
	// whole tape and the service never opens it — so it is something that
	// leaves this machine and belongs in a listing of what does.
	b.WriteString("  card           a 1200×675 PNG of the card, drawn from the fields above\n")
	b.WriteString("\n")

	// Who the run says published it. The profile is opt-in per machine and
	// unverified — anyone may type any name — so the listing says what it
	// is, the same way --dry-run lists everything else that leaves. The
	// bio is listed only when it will travel: the verb sets it only on a
	// token-owned publish, so a set Bio here is a travelling one and an
	// unset one prints nothing at all (never "bio none").
	b.WriteString("Who it says published it\n")
	if opts.Author == nil {
		b.WriteString("  profile        none — nothing about you travels\n")
	} else {
		fmt.Fprintf(&b, "  name           %s\n", nonEmpty(opts.Author.Name, "none"))
		fmt.Fprintf(&b, "  link           %s\n", nonEmpty(opts.Author.Link, "none"))
		fmt.Fprintf(&b, "  avatar         %s\n", avatarLine(opts.Author.Avatar))
	}
	if opts.Bio != "" {
		fmt.Fprintf(&b, "  bio            %s\n", noteLine(opts.Bio))
	}
	b.WriteString("\n")

	// The lab-note: the title and the body's first line with its length, so
	// the warning answers "how much" the way the text line does.
	b.WriteString("The note\n")
	fmt.Fprintf(&b, "  title          %s\n", nonEmpty(opts.Title, "none"))
	fmt.Fprintf(&b, "  note           %s\n", noteLine(opts.Note))
	b.WriteString("\n")

	b.WriteString("The row the search will hold\n")
	row, err := json.MarshalIndent(idx, "  ", "  ")
	if err != nil {
		// Unreachable for a value this package built, and a preview that
		// silently dropped its most concrete section would be worse than one
		// that says why.
		fmt.Fprintf(&b, "  (could not be rendered: %v)\n", err)
		return b.String()
	}
	fmt.Fprintf(&b, "  %s\n", row)
	return b.String()
}

func visibility(opts Options) string {
	if opts.Private {
		return "private · out of the search, readable by anyone with the link"
	}
	return "public · listed in the search"
}

func hostLine(idx Index) string {
	if idx.HostClass != "" {
		return idx.HostClass
	}
	if idx.GPUCount > 0 {
		return fmt.Sprintf("%d GPUs", idx.GPUCount)
	}
	return "no GPU recorded"
}

func promptSetLine(id string) string {
	if id == "" {
		return "none · this run is not in a comparison set"
	}
	return id + " · comparable with other runs of the same set"
}

func hostnameLine(s *tape.RunSummary) string {
	switch {
	case s.Host.HostnameSource == tape.HostnameLabelled:
		return s.Host.Hostname + " · your own label, so it travels"
	case s.Host.Hostname != "":
		// Unreachable through PublicView; if it is ever reached, saying so is
		// better than the preview quietly agreeing with a bug.
		return s.Host.Hostname + " · NOT removed, which is a defect"
	default:
		return "removed"
	}
}

func argvLine(s *tape.RunSummary) string {
	if len(s.Server.Args) == 0 {
		return "none recorded"
	}
	joined := strings.Join(s.Server.Args, " ")
	if len(joined) > 120 {
		joined = joined[:119] + "…"
	}
	return "paths shortened to file names · " + joined
}

// textLine counts what a reader of the published page will actually see, in
// characters, because "the prompts and the answers" is not a quantity and the
// question the warning exists to answer is how much.
func textLine(view *tape.Tape, opts Options) string {
	if opts.Text == WithoutText {
		return "nothing · the prompts and the generated text are removed, the timings stay"
	}
	prompt, completion := 0, 0
	for _, r := range view.Requests {
		for _, m := range r.Prompt.Messages {
			prompt += len([]rune(m.Content))
		}
		completion += len([]rune(r.Prompt.Completion)) + len([]rune(r.Prompt.Reasoning))
	}
	if prompt+completion == 0 {
		return "nothing · this run recorded no text"
	}
	return fmt.Sprintf("%d characters of prompt and %d of generated text, across %s",
		prompt, completion, plural(len(view.Requests), "stream"))
}

// avatarLine names the avatar's bytes and its header dimensions, the same
// two facts the set-time check refused on. No avatar travels as "none".
func avatarLine(b []byte) string {
	if len(b) == 0 {
		return "none"
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(b)); err == nil {
		return fmt.Sprintf("%d bytes, %d×%d PNG", len(b), cfg.Width, cfg.Height)
	}
	return fmt.Sprintf("%d bytes", len(b))
}

// noteLine shows the body's first line and its length in characters, so a
// long note still answers "how much" on one line.
func noteLine(note string) string {
	if strings.TrimSpace(note) == "" {
		return "none"
	}
	first, _, _ := strings.Cut(note, "\n")
	n := len([]rune(note))
	if n == 1 {
		return fmt.Sprintf("%s … (1 character)", first)
	}
	return fmt.Sprintf("%s … (%d characters)", first, n)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
