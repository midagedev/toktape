package main

import (
	"fmt"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/compare"
	"github.com/midagedev/toktape/internal/tape"
)

// runCompare diffs two recorded runs.
func runCompare(c *cli, args []string) int {
	fs := newFlagSet("compare")
	files, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("compare", usageFor("compare"), fs, args, err)
	}
	if len(files) != 2 {
		return c.usageTextf(usageText, "toktape compare: expected two tape files")
	}
	a, err := tape.Read(files[0])
	if err != nil {
		return c.usagef("toktape: %v", err)
	}
	b, err := tape.Read(files[1])
	if err != nil {
		return c.usagef("toktape: %v", err)
	}
	// Two embedding tapes compare vector by vector (2026-10-15).
	if ea, eb := a.Summary.IsEmbedding(), b.Summary.IsEmbedding(); ea || eb {
		if !ea || !eb {
			return c.usagef("toktape compare: an embedding tape compares only with another embedding tape")
		}
		fmt.Fprint(c.stdout, card.EmbeddingCompare(a, b))
		return exitOK
	}
	r, err := compare.Tapes(a, b)
	if err != nil {
		return c.usagef("toktape compare: %v", err)
	}
	fmt.Fprint(c.stdout, compare.Text(r))
	return exitOK
}
