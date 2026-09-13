package tui

import (
	"strings"
	"testing"
	"unicode"

	"github.com/midagedev/toktape/internal/card"
)

// realTemplate is the head of what a real llama-server reports as its chat
// template: the Jinja source itself, newlines and all (DeepSeek V4.1 Flash on
// llama.cpp b96, recorded on ws 2026-09-14, 6916 characters and 136 newlines).
const realTemplate = "{%- if not add_generation_prompt is defined -%}\n" +
	"  {%- set add_generation_prompt = false -%}\n" +
	"{%- endif -%}\n"

// TestPromptModalHoldsARealTemplate is TTP-51. The modal's template row added
// the template to a line as it came, so on every real recording the newline
// escaped the row: the modal was 39 rows on a 38-row screen and its colour
// frame no longer stripped to its plain one. The row now names what the
// template is, and the frame keeps its size.
func TestPromptModalHoldsARealTemplate(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{120, 36}, {156, 38}} {
		m := goldenModel(t, doneAt)
		m.Summary.Template.ChatTemplate = realTemplate
		m.Mode = ModePrompt
		plain := View(m, doneAt, sz.w, sz.h)
		rows := strings.Split(plain, "\n")
		if len(rows) != sz.h {
			t.Errorf("%dx%d: the prompt modal is %d rows", sz.w, sz.h, len(rows))
		}
		for i, r := range rows {
			if got := width(r); got != sz.w {
				t.Errorf("%dx%d: row %d is %d columns: %q", sz.w, sz.h, i, got, r)
				break
			}
		}
		m.Theme = ColourTheme()
		if card.StripANSI(View(m, doneAt, sz.w, sz.h)) != plain {
			t.Errorf("%dx%d: stripping the palette does not reproduce the plain modal", sz.w, sz.h)
		}
		if !strings.Contains(plain, "jinja, 3 lines") {
			t.Errorf("%dx%d: the template row does not say what the template is\n%s", sz.w, sz.h, plain)
		}
	}
}

// TestALineNeverCarriesAControlRune closes the class TTP-51 belongs to: a line
// is one row of exactly its width whatever a caller hands it, so no string
// from a server — a template, a model name, a note — can break a frame.
func TestALineNeverCarriesAControlRune(t *testing.T) {
	const w = 12
	in := "a\nb\tc\rd\x1be"
	for _, c := range []struct {
		name string
		add  func(l *lineBuf)
	}{
		{"add", func(l *lineBuf) { l.add(l.th.text, in) }},
		{"addTrunc", func(l *lineBuf) { l.addTrunc(l.th.text, in) }},
		{"addRaw", func(l *lineBuf) { l.addRaw(l.th.text, in) }},
	} {
		for _, th := range []Theme{PlainTheme(), ColourTheme()} {
			l := newLine(th, w)
			c.add(l)
			got := card.StripANSI(l.String())
			for _, r := range got {
				if unicode.IsControl(r) {
					t.Errorf("%s (colour=%v): the line carries control rune %U: %q", c.name, th.colour, r, got)
					break
				}
			}
			if width(got) != w {
				t.Errorf("%s (colour=%v): the line is %d columns, want %d: %q", c.name, th.colour, width(got), w, got)
			}
		}
	}
}
