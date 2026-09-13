package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// The three READMEs are one document in three languages. Prose differs; the
// commands and the card do not. These tests pin the two things a reader
// copies: the card must be the card the code renders today (the golden), and
// every fenced code block must be byte-identical across languages, so a flag
// renamed in one file cannot go stale in the other two.

var readmes = []string{"README.md", "README.ko.md", "README.ja.md"}

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// TestREADMECardIsTheGolden: the card pasted at the top of every README is
// the one internal/card renders from assets/hero.tape, the recording the hero
// clip above it plays (2026-09-14).
//
// It used to be a fixture's card. A reader had no way to check that one; this
// one they can render themselves — `toktape card assets/hero.tape` is in the
// caption — and it is the same run the image shows, so the page makes one
// claim instead of two. When the card's layout changes, this says the READMEs
// must be re-pasted with it.
func TestREADMECardIsTheGolden(t *testing.T) {
	tp, err := tape.Read(filepath.Join("..", "..", "assets", "hero.tape"))
	if err != nil {
		t.Fatalf("the hero's recording is missing: %v", err)
	}
	golden := strings.TrimRight(card.Text(&tp.Summary), "\n")
	for _, name := range readmes {
		if !strings.Contains(repoFile(t, name), golden) {
			t.Errorf("%s: card block is not `toktape card assets/hero.tape`; re-paste it", name)
		}
	}
}

var fence = regexp.MustCompile("(?s)```[a-z]*\n(.*?)```")

// TestREADMECodeBlocksMatchAcrossLanguages: same fences, same order, same
// bytes in every language.
func TestREADMECodeBlocksMatchAcrossLanguages(t *testing.T) {
	var want []string
	for i, name := range readmes {
		var got []string
		for _, m := range fence.FindAllStringSubmatch(repoFile(t, name), -1) {
			got = append(got, m[1])
		}
		if len(got) == 0 {
			t.Fatalf("%s: no fenced code blocks", name)
		}
		if i == 0 {
			want = got
			continue
		}
		if len(got) != len(want) {
			t.Errorf("%s: %d code blocks, README.md has %d", name, len(got), len(want))
			continue
		}
		for j := range want {
			if got[j] != want[j] {
				t.Errorf("%s: code block %d differs from README.md:\n--- README.md\n%s--- %s\n%s", name, j+1, want[j], name, got[j])
			}
		}
	}
}

var relLink = regexp.MustCompile(`\]\(([^)#:]+)\)`)

// TestREADMERelativeLinksResolve: every relative link in the READMEs and
// CONTRIBUTING.md points at a file that exists.
func TestREADMERelativeLinksResolve(t *testing.T) {
	for _, name := range append(readmes, "CONTRIBUTING.md") {
		for _, m := range relLink.FindAllStringSubmatch(repoFile(t, name), -1) {
			target := m[1]
			if _, err := os.Stat(filepath.Join("..", "..", target)); err != nil {
				t.Errorf("%s: link target %q does not exist", name, target)
			}
		}
	}
}
