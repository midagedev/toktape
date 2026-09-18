package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/midagedev/toktape/internal/config"
	"github.com/midagedev/toktape/internal/publish"
)

// profileUsage is the profile verb's own text, wired into usageFor the way
// renderUsage is: `help profile`, `profile --help` and a rejected
// `profile` invocation print the same bytes (TTP-92).
const profileUsage = `toktape profile — the author one publish says published it

Usage:
  toktape profile                         print the current profile
  toktape profile --name TEXT --link URL --avatar FILE
                                          set the nickname, the one link and the avatar
  toktape profile --clear                 remove all three

The profile is opt-in per machine and unverified — anyone may type any
name — and it stays so until tokens become accounts. What is set here
travels with every publish until --clear, and one run travels without it
with publish --no-profile. There is no email field: one URL only.

  --name TEXT     1–40 runes, no control characters
  --link URL      1–200 bytes, http or https with a host
  --avatar FILE   a PNG, at most 65536 bytes and 256×256
  --clear         remove the name, the link and the avatar

Examples:
  toktape profile --name "Lab Rat" --link https://github.com/example --avatar ~/pic.png
  toktape profile
`

// runProfile prints or sets the resident author profile (TTP-125).
//
// With no flags it prints the current profile, one field per line, `?` for
// unset — and for the avatar its resolved path plus its byte size, or `?`
// with "missing" beside it when the file is gone. With setters it validates
// each field with the contract's limits (refusing with the limit named),
// saves through config.Save — which keeps the other keys — and prints what
// it saved.
func runProfile(c *cli, args []string) int {
	fs := newFlagSet("profile")
	name := fs.String("name", "", "the nickname one publish carries")
	link := fs.String("link", "", "the one link one publish carries")
	avatar := fs.String("avatar", "", "the avatar image file")
	clear := fs.Bool("clear", false, "remove the name, the link and the avatar")
	files, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("profile", usageFor("profile"), args, err)
	}
	if len(files) != 0 {
		return c.usageTextf(usageFor("profile"), "toktape profile: takes no file, only flags")
	}

	cfg, err := config.Load()
	if err != nil {
		return c.usagef("toktape: %v", err)
	}

	if !*clear && *name == "" && *link == "" && *avatar == "" {
		printProfile(c, cfg)
		return exitOK
	}

	if *clear && (*name != "" || *link != "" || *avatar != "") {
		return c.usagef("toktape profile: --clear removes all three and takes no setters beside it")
	}

	if *clear {
		cfg.ProfileName, cfg.ProfileLink, cfg.ProfileAvatar = "", "", ""
		if err := config.Save(cfg); err != nil {
			return c.failf(exitUnavailable, "toktape: %v", err)
		}
		fmt.Fprintln(c.stdout, "cleared the profile — nothing about you travels now")
		return exitOK
	}

	if *name != "" {
		v, err := publish.ValidateAuthorName(*name)
		if err != nil {
			return c.usagef("toktape profile: %v", err)
		}
		cfg.ProfileName = v
	}
	if *link != "" {
		v, err := publish.ValidateAuthorLink(*link)
		if err != nil {
			return c.usagef("toktape profile: %v", err)
		}
		cfg.ProfileLink = v
	}
	if *avatar != "" {
		// Stored as typed; resolved at use, so a profile written on one
		// machine still reads on another layout. The bytes are checked
		// now, at set time, with the size measured and the limit named.
		if _, err := publish.LoadAvatar(expandHome(*avatar)); err != nil {
			return c.usagef("toktape profile: %v", err)
		}
		cfg.ProfileAvatar = *avatar
	}
	if err := config.Save(cfg); err != nil {
		return c.failf(exitUnavailable, "toktape: %v", err)
	}
	fmt.Fprintln(c.stdout, "saved profile")
	printProfile(c, cfg)
	return exitOK
}

// printProfile prints the current profile, one field per line, `?` for
// unset. The avatar line carries the resolved path and the byte size, or
// `?` with "missing" beside it when the file is gone.
func printProfile(c *cli, cfg *config.Config) {
	fmt.Fprintf(c.stdout, "name    %s\n", nonEmptyProfile(cfg.ProfileName))
	fmt.Fprintf(c.stdout, "link    %s\n", nonEmptyProfile(cfg.ProfileLink))
	fmt.Fprintf(c.stdout, "avatar  %s\n", avatarProfileLine(cfg.ProfileAvatar))
}

func nonEmptyProfile(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

// avatarProfileLine resolves the stored path and reports its size. The path
// is resolved the same way the publish verb resolves it, so the two cannot
// disagree about which file travels.
func avatarProfileLine(stored string) string {
	if stored == "" {
		return "?"
	}
	path := expandHome(stored)
	abs := absPath(path)
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("? (missing: %s)", abs)
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(b)); err == nil {
		return fmt.Sprintf("%s (%d bytes, %d×%d PNG)", abs, len(b), cfg.Width, cfg.Height)
	}
	return fmt.Sprintf("%s (%d bytes)", abs, len(b))
}

// expandHome expands a leading `~`, which the config parser does not: the
// file holds the path as typed and resolution happens at use.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
