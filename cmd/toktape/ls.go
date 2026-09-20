package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// runLs lists the recorded runs. A file that does not load is listed with the
// reason rather than skipped: a run directory that silently shrinks is worse
// than one row saying a tape is unreadable.
func runLs(c *cli, args []string) int {
	fs := newFlagSet("ls")
	outDir := fs.String("out", defaultRunsDir(), "directory to list")
	extra, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("ls", usageFor("ls"), fs, args, err)
	}
	if len(extra) > 0 {
		return c.usagef("toktape ls: unexpected argument %q", extra[0])
	}

	entries, err := os.ReadDir(*outDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprint(c.stderr, noRunsMessage(*outDir))
			return exitOK
		}
		return c.usagef("toktape: %v", err)
	}

	var rows [][]string
	for _, e := range entries {
		if e.IsDir() || !tape.IsRunFile(e.Name()) {
			continue
		}
		path := filepath.Join(*outDir, e.Name())
		tp, err := tape.Read(path)
		if err != nil {
			rows = append(rows, []string{tape.TrimExt(e.Name()), "unreadable", "", "", "", ""})
			continue
		}
		s := tp.Summary
		rows = append(rows, []string{
			s.ID,
			modelLabel(s),
			orUnknown(s.Model.Quant),
			formatRate(summaryRate(s)),
			fmt.Sprintf("%d", s.Concurrency),
			tape.Stamp(s.StartedAt),
		})
	}
	if len(rows) == 0 {
		fmt.Fprint(c.stderr, noRunsMessage(*outDir))
		return exitOK
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][0] > rows[j][0] })

	header := []string{"ID", "MODEL", "QUANT", "TOK/S", "N", "DATE"}
	// Right-align the two numeric columns so rates and stream counts line up
	// as a column of digits rather than as ragged text.
	right := map[int]bool{3: true, 4: true}
	fmt.Fprint(c.stdout, table(header, rows, right))
	return exitOK
}

// modelLabel is card.ModelName for a list row (2026-09-15, user: "모델이 다
// 실제값으로 찍혀야해"): the model that ran, never the GGUF header's
// general.name, which a re-quantised variant keeps from the base model. "?" is
// the table's word for an unobserved model.
func modelLabel(s tape.RunSummary) string {
	return orUnknown(card.ModelName(s.Model))
}

// table lays out rows in aligned columns, measuring with card.Width so a
// model name carrying a wide rune does not shift the columns after it
// (handover lesson 5).
func table(header []string, rows [][]string, right map[int]bool) string {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = card.Width(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(widths) && card.Width(c) > widths[i] {
				widths[i] = card.Width(c)
			}
		}
	}
	var b strings.Builder
	write := func(cells []string) {
		for i, c := range cells {
			if i > 0 {
				b.WriteString("  ")
			}
			if i == len(cells)-1 && !right[i] {
				b.WriteString(c)
				continue
			}
			if right[i] {
				b.WriteString(padLeft(c, widths[i]))
			} else {
				b.WriteString(padRight(c, widths[i]))
			}
		}
		b.WriteString("\n")
	}
	write(header)
	for _, r := range rows {
		write(r)
	}
	return b.String()
}

func padRight(s string, w int) string {
	if n := w - card.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func padLeft(s string, w int) string {
	if n := w - card.Width(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "?"
	}
	return s
}
