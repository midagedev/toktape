// Package config reads and writes ~/.toktape/config.toml, the one resident
// file toktape has (docs/toktape-spec.ko.md §9.3).
//
// It exists because publishing needs somewhere to keep a token, and once
// there is a file for the token there is no reason for a second one for the
// settings. Until this package there was no configuration path in toktape at
// all: ~/.toktape was a directory of run files and nothing read anything out
// of it.
//
// The parser is four keys of `name = value` and nothing else — no tables, no
// arrays, no multi-line strings. That is not a TOML implementation and does
// not pretend to be one; the file is named .toml so an editor colours it and
// a human recognises it. A dependency would be the alternative, and this
// repo's binary is deliberately stdlib-only (cmd/toktape/main.go). If the
// file ever needs a table, that is the moment to reconsider, not now.
//
// Absence is not an error. A machine with no config has never published, and
// every field's zero value is the behaviour of a machine that has not been
// configured.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the file's contents. Every field is optional.
type Config struct {
	// Token owns a journal namespace (§9.2). It is a credential, which is
	// why the file is written 0600 and why Path is under the user's home
	// rather than anywhere a run file might be shared from.
	Token string
	// FirstPublishWarningSeen records that the one-time warning has been
	// shown. The warning is shown once per machine, not once per upload: a
	// safeguard you have to remember to ask for is not a safeguard, and one
	// that asks every time is one people learn to dismiss without reading.
	FirstPublishWarningSeen bool
	// PublishText is the machine-wide opt-out. It is a pointer because its
	// absence and its false are different states: unset means "the default,
	// which is to publish the text", and false is a choice somebody made.
	// Writing the file must not turn the first into the second.
	PublishText *bool
	// HostLabel is the resident form of record --host-label (TTP-93): the
	// name this machine goes by in public, for people who never want their
	// real hostname in a tape.
	HostLabel string
}

// FileName is the config file's name inside Dir.
const FileName = "config.toml"

// Dir is ~/.toktape, the directory toktape already writes run files into.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: no home directory: %w", err)
	}
	return filepath.Join(home, ".toktape"), nil
}

// Path is the config file's full path.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Load reads the config. A missing file is an empty Config and no error: a
// machine that has never published has no file, and that is a state, not a
// failure.
//
// A malformed line IS an error. The file holds a credential and an opt-out,
// and a parser that skipped what it could not read would silently publish
// text on a machine whose owner had turned it off. Refusing names the line.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads a config from an explicit path, for tests and for a caller
// that has been given one.
func LoadFrom(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	defer f.Close()

	cfg := &Config{}
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("config: %s line %d: not `name = value`: %q", path, line, text)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(stripComment(value))
		if err := cfg.set(key, value); err != nil {
			return nil, fmt.Errorf("config: %s line %d: %w", path, line, err)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) set(key, value string) error {
	switch key {
	case "token":
		s, err := unquote(value)
		if err != nil {
			return fmt.Errorf("token: %w", err)
		}
		c.Token = s
	case "host_label":
		s, err := unquote(value)
		if err != nil {
			return fmt.Errorf("host_label: %w", err)
		}
		c.HostLabel = s
	case "first_publish_warning_seen":
		b, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("first_publish_warning_seen: %w", err)
		}
		c.FirstPublishWarningSeen = b
	case "publish_text":
		b, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("publish_text: %w", err)
		}
		c.PublishText = &b
	default:
		// A key nobody reads is a setting somebody believes is in effect. An
		// older toktape meeting a newer file is the one case where that is
		// not a mistake, and it is also the case where being told beats
		// guessing, so this refuses either way.
		return fmt.Errorf("unknown key %q", key)
	}
	return nil
}

// WantsText is the machine's answer to "does the body travel", with the
// default (yes, §9.3) applied for a machine that never said.
func (c *Config) WantsText() bool {
	return c == nil || c.PublishText == nil || *c.PublishText
}

// Save writes the config, creating ~/.toktape if it is missing.
func Save(c *Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	return SaveTo(filepath.Join(dir, FileName), c)
}

// SaveTo writes a config to an explicit path.
//
// The file is written whole through a temporary file in the same directory
// and renamed, so a config that is being read while it is written is either
// the old one or the new one. It is 0600 from creation rather than chmod'ed
// afterwards: a token must never exist on disk world-readable, not even for
// the instant between the two calls.
func SaveTo(path string, c *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: create %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	if _, err := tmp.WriteString(c.render()); err != nil {
		tmp.Close()
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// render writes only the keys that are set. A file full of commented-out
// defaults invites editing settings nobody chose, and an unset publish_text
// written as false would turn a default into a decision.
func (c *Config) render() string {
	var b strings.Builder
	b.WriteString("# toktape configuration. Written by `toktape publish`; safe to edit.\n")
	b.WriteString("# https://github.com/midagedev/toktape\n\n")
	if c.Token != "" {
		fmt.Fprintf(&b, "token = %s\n", quote(c.Token))
	}
	if c.FirstPublishWarningSeen {
		b.WriteString("first_publish_warning_seen = true\n")
	}
	if c.PublishText != nil {
		fmt.Fprintf(&b, "publish_text = %t\n", *c.PublishText)
	}
	if c.HostLabel != "" {
		fmt.Fprintf(&b, "host_label = %s\n", quote(c.HostLabel))
	}
	return b.String()
}

// stripComment drops a trailing `# ...` that is outside a quoted string, so a
// token containing a '#' survives and an annotated line still parses.
func stripComment(v string) string {
	inQuote := false
	for i, r := range v {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == '#' && !inQuote:
			return v[:i]
		}
	}
	return v
}

// unquote accepts a double-quoted string, which is what this package writes.
// A bare value is accepted too — somebody editing the file by hand will type
// one — but an unterminated quote is refused rather than guessed at.
func unquote(v string) (string, error) {
	if !strings.HasPrefix(v, `"`) {
		return v, nil
	}
	s, err := strconv.Unquote(v)
	if err != nil {
		return "", fmt.Errorf("not a quoted string: %s", v)
	}
	return s, nil
}

func quote(s string) string { return strconv.Quote(s) }

// parseBool takes only TOML's own spelling. "yes"/"1" are not booleans here,
// and accepting them would mean deciding what "on" means for a setting whose
// whole job is to be unambiguous.
func parseBool(v string) (bool, error) {
	switch v {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("want true or false, got %q", v)
}
