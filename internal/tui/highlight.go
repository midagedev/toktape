package tui

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// Syntax in a fenced code block (TTP-52, user 2026-09-14: "코드 하일라이팅은?").
//
// The palette allows no colour for it — "one accent hue, one warm hue, one
// bad hue, and neutrals; nothing else gets a colour" (internal/palette), the
// accent is the rates', the warm and bad hues are alarms, and a settled body
// may not wear the header's tone (TestBodyTextSitsBelowTheHeaderTone). So a
// block is not coloured, it is shaped: keywords carry weight, comments and
// punctuation step down the same ladder the body already moves on, and the
// names, strings and numbers stay where the band put them. A class can only
// deepen a rune's shade, never lift it, so the glow contract and the ladder
// gates hold on a code answer exactly as on prose.
//
// The lexer is chroma (what glamour, glow and crush lex with), chosen by the
// fence's language tag and nothing else: a block with no tag, or a tag chroma
// does not know, is drawn plain rather than lexed as a guess — in the browser
// build, which this file knows nothing about, the small hand-written lexer in
// highlight_light_impl.go keeps the same rule (docs/toktape-spec.ko.md §9.8).

// codeClass is what a rune of a fenced block is, as far as the pane cares.
type codeClass uint8

const (
	classPlain   codeClass = iota // prose, and code the treatment leaves alone
	classKeyword                  // bold at the band's stop
	classPunct                    // punctuation and operators: one stop down
	classComment                  // two stops down
	classFence                    // the ``` line itself: chrome, dim
	classString                   // a string or character literal (palette.CodeString)
	classNumber                   // a number, a boolean, nil (palette.CodeNumber)
	classFunc                     // what is called or defined (palette.CodeFunc)
	classType                     // a class, type or builtin name (palette.CodeType)
	classVar                      // a plain identifier (palette.CodeVar)
)

// codeClasses is, rune for rune over the text the stream's tokens spell out,
// the class of each rune. Nil when the stream has no fenced block, which is
// the common case and costs nothing.
func codeClasses(s Stream) []codeClass {
	var (
		classes []codeClass
		off     int
	)
	for _, r := range textRuns(s) {
		n := utf8.RuneCountInString(r.text)
		if !r.reasoning && strings.Contains(r.text, "`") {
			if classes == nil {
				classes = make([]codeClass, utf8.RuneCountInString(s.Text))
			}
			if strings.Contains(r.text, "```") {
				classifyFences(r.text, classes[off:off+n])
			}
			classifySpans(r.text, classes[off:off+n])
		}
		off += n
	}
	return classes
}

// classifySpans marks the inline `code` spans of one answer run.
//
// Most of the code an answer shows is not fenced: it is a name, a call or a
// flag inside a sentence, and until 2026-09-16 those read as prose, which is
// what made a tile of an answer about code look like a tile of an answer
// about anything (user: "코드 하일라이팅이 너무 안 예쁘다"). A span wears the
// name hue, the same one a call inside a fenced block wears, so the two say
// the same thing about the same text.
//
// Only a CLOSED span counts. While a stream is writing one, the opening
// backtick is just a backtick, and colouring the rest of the answer until the
// span closes would repaint the tile on every token.
func classifySpans(text string, out []codeClass) {
	runes := []rune(text)
	open := -1
	for i, r := range runes {
		if r != '`' {
			continue
		}
		// A fence is three of them; classifyFences owns those lines.
		if i+2 < len(runes) && runes[i+1] == '`' && runes[i+2] == '`' {
			open = -1
			continue
		}
		if open < 0 {
			open = i
			continue
		}
		for k := open; k <= i && k < len(out); k++ {
			if out[k] != classPlain {
				continue // inside a fenced block: the lexer already spoke
			}
			out[k] = classVar
		}
		open = -1
	}
}

// classifyFences finds the fenced blocks in one answer run and writes their
// classes into out, which is one slot per rune of text.
func classifyFences(text string, out []codeClass) {
	lines := strings.SplitAfter(text, "\n")
	var (
		off     int
		inBlock bool
		lang    string
		block   strings.Builder
		start   int
	)
	for _, line := range lines {
		n := utf8.RuneCountInString(line)
		if tag, ok := fenceLine(line); ok {
			for k := off; k < off+n; k++ {
				out[k] = classFence
			}
			if inBlock {
				classifyBlock(lang, block.String(), out[start:off])
				block.Reset()
			} else {
				lang, start = tag, off+n
			}
			inBlock = !inBlock
		} else if inBlock {
			block.WriteString(line)
		}
		off += n
	}
	// A block still open is the one being written; it is lexed as far as it
	// goes, so the code lights up as it arrives and not when it is closed.
	if inBlock {
		classifyBlock(lang, block.String(), out[start:off])
	}
}

// fenceLine says whether line is a code fence and, if so, its language tag.
// Markdown allows the fence up to three spaces of indent.
func fenceLine(line string) (lang string, ok bool) {
	t := strings.TrimRight(line, "\r\n")
	if len(t)-len(strings.TrimLeft(t, " ")) > 3 {
		return "", false
	}
	t = strings.TrimLeft(t, " ")
	if !strings.HasPrefix(t, "```") {
		return "", false
	}
	tag := strings.TrimSpace(strings.TrimPrefix(t, "```"))
	if i := strings.IndexAny(tag, " \t{"); i >= 0 {
		tag = tag[:i]
	}
	return strings.ToLower(tag), true
}

// classifyBlock lexes one block and writes its classes into out, one per
// rune of code. An unknown or missing language leaves it plain.
func classifyBlock(lang, code string, out []codeClass) {
	if lang == "" || code == "" {
		return
	}
	classes := lexBlock(lang, code)
	copy(out, classes)
}

// lexCache holds the classes of the blocks lexed most recently. While a block
// is streaming its text changes every token, so its entry is replaced each
// frame; the blocks behind it never change and hit. Bounded so a long session
// cannot grow it.
var lexCache = struct {
	sync.Mutex
	m map[string][]codeClass
}{m: map[string][]codeClass{}}

const lexCacheMax = 64

func lexBlock(lang, code string) []codeClass {
	key := lang + "\n" + code
	lexCache.Lock()
	if c, ok := lexCache.m[key]; ok {
		lexCache.Unlock()
		return c
	}
	lexCache.Unlock()

	classes := lexRunes(lang, code)

	lexCache.Lock()
	if len(lexCache.m) >= lexCacheMax {
		for k := range lexCache.m {
			delete(lexCache.m, k)
			if len(lexCache.m) < lexCacheMax/2 {
				break
			}
		}
	}
	lexCache.m[key] = classes
	lexCache.Unlock()
	return classes
}
