package tui

import (
	"fmt"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// promptModal renders the "p" overlay: the prompt as the server actually
// rendered it, plus the template settings that decided what was measured.
//
// This panel exists because of handover lesson 4 — reasoning_effort none
// against thinking turned a 60-token answer into a 4,000-token one, and no
// rate comparison means anything until both sides agree on what was sent. The
// hash is printed so two runs can be shown to have sent the same bytes.
func promptModal(m Model, th Theme, boxW int) []string {
	inner := boxW - 4
	var rows []string
	rows = append(rows, th.paint(th.dim, "┌"+repeat('─', boxW-2)+"┐"))

	line := func(s string) {
		rows = append(rows, th.paint(th.dim, "│")+" "+pad(s, inner)+" "+th.paint(th.dim, "│"))
	}
	rule := func() {
		rows = append(rows, th.paint(th.dim, "├"+repeat('─', boxW-2)+"┤"))
	}

	tpl := m.Summary.Template
	l := newLine(th, inner)
	l.add(th.accent, "RENDERED PROMPT")
	if n := tpl.RenderedPromptTokens; n > 0 {
		v := fmt.Sprintf("%d tokens", n)
		l.gapTo(width(v))
		l.add(th.dim, v)
	}
	line(l.String())
	rule()

	for _, row := range templateRows(th, tpl, inner) {
		line(row)
	}
	rule()

	body := renderedPrompt(m)
	lines := wrap(body, inner)
	const maxBody = 16
	if len(lines) > maxBody {
		lines = append(lines[:maxBody-1], fmt.Sprintf("… %d more lines", len(lines)-maxBody+1))
	}
	for _, s := range lines {
		l := newLine(th, inner)
		l.add(th.text, s)
		line(l.String())
	}
	rows = append(rows, th.paint(th.dim, "└"+repeat('─', boxW-2)+"┘"))
	return rows
}

// templateRows are the settings that decide what a run is measuring.
func templateRows(th Theme, tpl tape.TemplateInfo, inner int) []string {
	var out []string
	l := newLine(th, inner)
	l.add(th.dim, "template ")
	l.add(th.text, orUnknown(tpl.ChatTemplate))
	l.add(th.dim, "   effort ")
	l.add(th.text, orUnknown(tpl.ReasoningEffort))
	out = append(out, l.String())

	l = newLine(th, inner)
	l.add(th.dim, "</think> ")
	l.add(styleFor(th, !tpl.RenderedHasThinkClose), yesNo(tpl.RenderedHasThinkClose))
	if sha := tpl.RenderedPromptSHA256; sha != "" {
		v := "sha " + sha[:min(12, len(sha))]
		l.gapTo(width(v))
		l.add(th.dim, v)
	}
	out = append(out, l.String())

	if len(tpl.TemplateKwargs) > 0 {
		keys := make([]string, 0, len(tpl.TemplateKwargs))
		for k := range tpl.TemplateKwargs {
			keys = append(keys, k)
		}
		sortStrings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, k+"="+tpl.TemplateKwargs[k])
		}
		l = newLine(th, inner)
		l.add(th.dim, "kwargs ")
		l.addTrunc(th.text, strings.Join(parts, " "))
		out = append(out, l.String())
	}
	return out
}

// renderedPrompt is what /apply-template returned for the stream whose cursor
// is live, falling back to the first stream. It is never reconstructed from a
// template we guessed at: when the endpoint was unavailable the panel says so.
func renderedPrompt(m Model) string {
	i := m.activeStream()
	if i < 0 || i >= len(m.Streams) || m.Streams[i].RenderedPrompt == "" {
		i = 0
	}
	if i < len(m.Streams) && m.Streams[i].RenderedPrompt != "" {
		return m.Streams[i].RenderedPrompt
	}
	return "no rendered prompt recorded (/apply-template unavailable)"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// sortStrings is an insertion sort; kwargs maps hold a handful of keys and
// this keeps the render path free of a sort import.
func sortStrings(v []string) {
	for i := 1; i < len(v); i++ {
		x := v[i]
		j := i - 1
		for j >= 0 && v[j] > x {
			v[j+1] = v[j]
			j--
		}
		v[j+1] = x
	}
}
