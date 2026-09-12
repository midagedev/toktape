package main

import (
	"fmt"
	"io"

	"github.com/midagedev/toktape/internal/compare"
	"github.com/midagedev/toktape/internal/tape"
)

// runCompare diffs two recorded runs.
func runCompare(stdout, stderr io.Writer, args []string) int {
	fs := newFlagSet("compare", stderr)
	files, err := parseArgs(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(files) != 2 {
		fmt.Fprintf(stderr, "toktape compare: expected two tape files\n\n%s", usageText)
		return exitUsage
	}
	a, err := tape.Read(files[0])
	if err != nil {
		fmt.Fprintf(stderr, "toktape: %v\n", err)
		return exitUsage
	}
	b, err := tape.Read(files[1])
	if err != nil {
		fmt.Fprintf(stderr, "toktape: %v\n", err)
		return exitUsage
	}
	fmt.Fprint(stdout, compare.Text(compare.Diff(&a.Summary, &b.Summary)))
	return exitOK
}
