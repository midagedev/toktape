package main

import (
	"regexp"
	"strings"
	"testing"
)

// recipeHeading is the section a maintainer pastes into an issue (TTP-200).
const recipeHeading = "## Asking someone else for a tape"

// recipeBlock returns the first fenced block under the recipe heading.
func recipeBlock(t *testing.T) string {
	t.Helper()
	doc := repoFile(t, "docs/publish.md")
	_, rest, ok := strings.Cut(doc, recipeHeading+"\n")
	if !ok {
		t.Fatalf("docs/publish.md has no %q section", recipeHeading)
	}
	if i := strings.Index(rest, "\n## "); i >= 0 {
		rest = rest[:i]
	}
	m := fence.FindStringSubmatch(rest)
	if m == nil {
		t.Fatalf("the %q section has no fenced block", recipeHeading)
	}
	return m[1]
}

var flagToken = regexp.MustCompile(`^--?[A-Za-z][A-Za-z0-9-]*`)

// TestRecipeBlockParses: every `toktape <verb> ...` line of the pasted recipe
// names a verb that exists and flags that verb accepts. Verbs that declare
// their flags in a function (exampleFlagSet) are checked against the real
// flag set; decide and profile declare theirs inline, so they are checked
// against the flags their own usage text lists.
func TestRecipeBlockParses(t *testing.T) {
	lines := 0
	for _, line := range strings.Split(recipeBlock(t), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "toktape" {
			continue
		}
		lines++
		verb := fields[1]
		if !verbs[verb] {
			t.Errorf("%q: no verb %q", line, verb)
			continue
		}
		fs, ferr := exampleFlagSet(verb)
		for _, f := range fields[2:] {
			name := flagToken.FindString(f)
			if name == "" {
				continue
			}
			name = strings.TrimLeft(name, "-")
			if ferr == nil {
				if fs.Lookup(name) == nil {
					t.Errorf("%q: toktape %s has no flag --%s", line, verb, name)
				}
				continue
			}
			usage := usageFor(verb)
			if !strings.Contains(usage, "--"+name) && !strings.Contains(usage, " -"+name+" ") {
				t.Errorf("%q: the usage of toktape %s does not list --%s", line, verb, name)
			}
		}
	}
	if lines < 5 {
		t.Errorf("the recipe has %d toktape lines, want at least 5", lines)
	}
}
