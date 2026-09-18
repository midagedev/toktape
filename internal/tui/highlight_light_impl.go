package tui

import "strings"

// lexRunesLight is the browser build's lexRunes: a hand-written lexer for
// what a chat answer's fenced blocks actually contain, driven by one
// per-language table. It is the same contract the chroma half implements —
// one codeClass per rune, all-classPlain for a language it does not know,
// never a guess — and highlight_agreement_test.go measures the two against
// each other, rune for rune, so the browser's highlighting cannot quietly
// drift from the terminal's.
//
// It is not a compiler front end. Comments, strings, numbers and words are
// recognised by shape, with the per-language quirks that matter to a block a
// human reads (where a comment's newline lands, which words are keywords,
// what a call site is called); a construct the table does not describe reads
// as plain or punctuation. The thresholds in the agreement test are where
// the two are allowed to differ.
//
// The word lists in lightLangs were extracted from chroma's own lexer
// definitions (lexers/embedded/*.xml, lexers/go.go in chroma v2.14.0)
// through the same classOf mapping the terminal uses, so a keyword means the
// same thing in both builds.
func lexRunesLight(lang, code string) []codeClass {
	runes := []rune(code)
	classes := make([]codeClass, len(runes))
	lx, ok := lightLangs[strings.ToLower(lang)]
	if !ok {
		return classes // unknown tag: plain, the way lexers.Get==nil does
	}
	s := lightScanner{runes: runes, classes: classes, lx: &lx}
	s.scan()
	return classes
}

// lightLang is the one table every language is lexed from: which words are
// keywords (and what class the pane gives each), how comments open, which
// characters open strings. Anything not expressible here reads as plain or
// punctuation.
type lightLang struct {
	// words maps an identifier to the class the pane gives it: keywords,
	// keyword types, constants, builtins. An identifier not in the map is
	// classPlain — the class a plain chroma Name gets too.
	words map[string]codeClass
	// decls maps a keyword to the class of the identifier it declares
	// (`func`, `def`, `fn`, `class`...), kept alive across the receiver
	// group that follows a Go method's `func`.
	decls map[string]codeClass
	// callWords are painted classFunc when what follows them is in callSep
	// (Go's builtins, read as types until they are called, and bash's
	// builtins, called at the first space). An empty callSep means `(` must
	// follow directly.
	callWords map[string]bool
	callSep   string
	// callIsFunc paints any unknown word before `(` as classFunc — the
	// languages whose chroma lexers read a call site as a function name.
	// callAfterWord narrows that to a call whose previous token was a word
	// followed by space: a declaration, in java and c++.
	callIsFunc    bool
	callAfterWord bool
	// decorFunc paints the word after `@` as classFunc (decorators).
	decorFunc bool
	// nsKeywords arm an import-mode that paints dotted names as classType
	// (a chroma NameNamespace) until the statement or line ends.
	nsKeywords map[string]bool
	// punctWords are keywords chroma sorts into its Operator category —
	// python's `in is and or not` — and so read as punctuation.
	punctWords map[string]bool
	// macroBang paints `name!` as classFunc when an opener follows (rust
	// macros; `!=` is an operator and does not match).
	macroBang bool
	// lineCom lists line-comment prefixes, longest first. docLineCom are
	// the prefixes chroma tokenises as doc strings instead (rust) — they
	// never swallow the newline. comBound requires the marker to sit at a
	// word boundary (yaml, whose scalars may carry a `#`). comEatsNL folds
	// the trailing newline into the comment, the way the go/c/js/rust/sql
	// chroma lexers tokenise `//`; the python/java/bash ones do not.
	lineCom    []string
	docLineCom []string
	comBound   bool
	comEatsNL  bool
	// blockCom is the block-comment open/close pair; empty open disables
	// it. nestedCom counts depth (rust). docBlockCom is its doc-string form
	// (rust /** */, again a string to chroma).
	blockCom    [2]string
	nestedCom   bool
	docBlockCom [2]string
	// quoteChars opens a string literal; escapes honours backslash inside
	// one (never inside a backtick, which is raw in go and a template in
	// js); tripleQuoted adds python's ''' / """ forms; strSpansNL says a
	// one-line string may run past the newline (python's may not). In
	// quotedKeys languages (json, yaml) a quoted string followed by `:` is
	// a key and reads as classVar.
	quoteChars   string
	escapes      bool
	tripleQuoted bool
	strSpansNL   bool
	quotedKeys   bool
	// strAffix holds the letters that directly before a quote are the
	// string's prefix rather than a word of their own (python r"...", rust
	// b"..."). An affix containing f or F walks the literal as an
	// f-string, interpolating `{expr}`.
	strAffix string
	// templateBacktick walks a backtick literal as a js template, where
	// `${expr}` interpolates ordinary code.
	templateBacktick bool
	// typeColon reads `name: Type` the way typescript's annotation rule
	// does: NameOther, a Text colon, KeywordType — so after an unknown
	// word the colon is plain and the run behind it is classType.
	typeColon bool
	// plainNumbers never tokenises a number: the digit run reads as a
	// plain name (java, whose number rules never fire in its lexer).
	plainNumbers bool
	// numNoDot reads a number's fraction as separate tokens — the dot an
	// operator, the digits another number (sql, whose lexer has only the
	// integer rule).
	numNoDot bool
	// dotVar paints the word after a `.` as classVar — chroma's
	// NameAttribute (java).
	dotVar bool
	// caseFold looks a word up uppercased, for the lexer whose keywords
	// are case-insensitive (sql).
	caseFold bool
	// labelColon paints an unknown word before a lone `:` as classNumber,
	// chroma's NameLabel (c, c++: their case arms and ternaries).
	labelColon bool
	// colonState is rust's colon model: `::`, `:` and `->` are Text, and
	// a single `:` or an `->` also types the word that follows them.
	colonState bool
	// cppAccess tracks c++'s access specifiers: after `public:` chroma's
	// class-body state stops reading the next constructor as a function.
	cppAccess bool
	// callBlockers are the words whose following word is not a call site
	// even before a `(` — chroma consumes them as keywords first, so
	// `new Foo(` reads the name plain (java).
	callBlockers map[string]bool
	// preproc reads a c `#` line whole as a comment: chroma tokenises the
	// directive into its Comment category.
	preproc bool
	// ctypes paints a lowercase word ending in _t as classType (c's
	// typedef-name rule).
	ctypes bool
	// lifetime: `'a` is a lifetime rather than an unclosed char literal
	// (rust).
	lifetime bool
	// bash: the shell's own model — its Text tokens are runs of a wider
	// word charset (`-l`, `2>`, `*.gguf` are single words), `$name` is
	// classVar, `$(` and `${` open substitutions, `name=` assigns, `)` is
	// a keyword, and a number only counts when the line ends it.
	bash bool
	// scalar: the yaml model, where a line is keys then a value blob —
	// see scanScalar. scalars are the words a value spells as itself (its
	// booleans and nulls, which read as classNumber).
	scalar  bool
	scalars map[string]codeClass
}

// lightScanner is the lexer's state. Comments, strings, substitutions and
// yaml scalars are consumed in one move because they span runes; everything
// else is decided token by token.
type lightScanner struct {
	runes   []rune
	classes []codeClass
	lx      *lightLang
	i       int
	// pendingDecl is the class owed to the next word after a decls keyword
	// (`func`, `def`...). declParens counts the receiver group of a Go
	// method: `func (w *Worker) Run(` owes Run its class, not w.
	pendingDecl codeClass
	declLive    bool
	declParens  int
	// decorLive is set by an `@` in the decorator languages.
	decorLive bool
	// nsLive is import-mode (see lightLang.nsKeywords).
	nsLive bool
	// typeNext is rust's typename state, armed by a single `:` or an `->`:
	// the next word reads as a type if the table does not name it.
	typeNext bool
	// accessLive is c++'s class-body state (see lightLang.cppAccess).
	accessLive bool
	// prevBlocked says the previous word is one of callBlockers, so the
	// word after it — even before a `(` — is not a call site.
	prevBlocked bool
	// prevWord says the previous non-space token was a word and prevSpace
	// that whitespace followed it (java's declaration rule).
	prevWord, prevSpace bool
	// boundary is whether the rune before i was a word boundary, which is
	// where yaml and bash let `#` open a comment.
	boundary bool
}

func (s *lightScanner) scan() {
	s.boundary = true
	for s.i < len(s.runes) {
		r := s.runes[s.i]
		if r == '\n' {
			s.nsLive = false // an import statement ends at its line
			s.boundary = true
			s.prevWord, s.prevSpace = false, false
			s.typeNext, s.prevBlocked = false, false
			s.i++
			continue
		}
		if isSpace(r) {
			s.boundary = true
			if s.prevWord {
				s.prevSpace = true
			}
			s.i++
			continue
		}
		if r == '@' && s.lx.decorFunc {
			s.decorLive = true
			s.fill(classPunct, s.i, s.i+1)
			s.i++
			s.prevWord, s.prevSpace = false, false
			continue
		}
		if s.atPreproc() || s.atComment() {
			s.prevWord, s.prevSpace = false, false
			continue
		}
		if s.lx.scalar && s.scanScalar() {
			s.prevWord, s.prevSpace = false, false
			continue
		}
		s.boundary = false
		sawWord := s.token()
		if !sawWord {
			s.prevBlocked = false // only spaces may carry a blocker forward
		}
		s.prevWord, s.prevSpace = sawWord, false
	}
}

// token lexes one ordinary token — a substitution, string, word, number or
// punctuation — and reports whether it was a word. The interpolated code
// inside js templates, python f-strings and bash substitutions reuses it.
func (s *lightScanner) token() bool {
	r := s.runes[s.i]
	if s.lx.bash && r == '$' {
		s.atDollar()
		return false
	}
	switch {
	case s.atString():
		return false
	case s.atWord():
		return true
	case s.atNumber():
		return false
	default:
		s.punct()
		return false
	}
}

// atComment consumes a comment in one move. Doc-comment markers (rust) read
// as classString because chroma tokenises them into its LiteralString
// category, and never swallow the newline; line comments run to the newline,
// inclusive where the language's chroma lexer folds it into the token; block
// comments to their close, nested when the language nests. Bash's shebang
// line is chroma's CommentPreproc and does swallow its newline.
func (s *lightScanner) atComment() bool {
	if s.lx.comBound && !s.boundary {
		return false
	}
	if s.lx.bash && s.i == 0 && s.hasPrefixAt("#!", 0) {
		end := s.i
		for end < len(s.runes) && s.runes[end] != '\n' {
			end++
		}
		if end < len(s.runes) {
			end++
		}
		s.fill(classComment, s.i, end)
		s.i = end
		return true
	}
	for _, m := range s.lx.lineCom {
		if !s.hasPrefixAt(m, s.i) {
			continue
		}
		class := classComment
		eat := s.lx.comEatsNL
		if containsStr(s.lx.docLineCom, m) {
			class, eat = classString, false
		}
		end := s.i
		for end < len(s.runes) && s.runes[end] != '\n' {
			end++
		}
		if eat && end < len(s.runes) {
			end++
		}
		s.fill(class, s.i, end)
		s.i = end
		return true
	}
	for _, pair := range [][2]string{s.lx.docBlockCom, s.lx.blockCom} {
		open, close := pair[0], pair[1]
		if open == "" || !s.hasPrefixAt(open, s.i) {
			continue
		}
		class := classComment
		if pair == s.lx.docBlockCom {
			class = classString
		}
		depth, end := 1, s.i+len(open)
		for end < len(s.runes) && depth > 0 {
			if s.lx.nestedCom && s.hasPrefixAt(open, end) {
				depth++
				end += len(open)
				continue
			}
			if s.hasPrefixAt(close, end) {
				depth--
				end += len(close)
				continue
			}
			end++
		}
		s.fill(class, s.i, end)
		s.i = end
		return true
	}
	return false
}

// atPreproc consumes a c preprocessor directive the way chroma tokenises
// it: the directive is CommentPreproc, an `#include`'s spaces are Text and
// its argument CommentPreprocFile, any other directive's whole line is the
// comment, and the trailing newline closes the macro state as comment too.
// A backslash continues the directive onto the next line.
func (s *lightScanner) atPreproc() bool {
	if !s.lx.preproc || s.runes[s.i] != '#' || !s.lineStart() {
		return false
	}
	directive := s.i + 1
	for directive < len(s.runes) && isSpace(s.runes[directive]) {
		directive++ // `#  define` is one directive
	}
	wordEnd := directive
	for wordEnd < len(s.runes) && isWordRune(s.runes[wordEnd]) {
		wordEnd++
	}
	body := wordEnd
	if string(s.runes[directive:wordEnd]) == "include" {
		for body < len(s.runes) && isSpace(s.runes[body]) {
			body++
		}
		s.fill(classComment, s.i, wordEnd)
		if body > wordEnd {
			s.fill(classPlain, wordEnd, body) // the include's spaces are Text
		}
	} else {
		s.fill(classComment, s.i, wordEnd)
	}
	for body < len(s.runes) && s.runes[body] != '\n' {
		if s.runes[body] == '\\' && body+1 < len(s.runes) && s.runes[body+1] == '\n' {
			body += 2
			continue
		}
		body++
	}
	if body < len(s.runes) {
		body++ // the newline is CommentPreproc: the macro state closes on it
	}
	s.fill(classComment, wordEnd, body)
	s.i = body
	return true
}

// atString consumes a string, char literal or template whole. Bash strings
// carry variables and substitutions, js templates and python f-strings carry
// interpolated code, and each gets its own walk.
func (s *lightScanner) atString() bool {
	r := s.runes[s.i]
	if !strings.ContainsRune(s.lx.quoteChars, r) {
		return false
	}
	if s.lx.lifetime && r == '\'' && !s.charLiteralAhead() {
		return false // 'a is a lifetime; punct() takes the apostrophe
	}
	switch {
	case s.lx.bash:
		s.dollarString(r)
	case r == '`' && s.lx.templateBacktick:
		s.templateString()
	default:
		end := s.stringEnd()
		class := classString
		if s.lx.quotedKeys && s.tokenAhead(':', end) {
			class = classVar // "key": is a name here, not a value
		}
		s.fill(class, s.i, end)
		s.i = end
	}
	return true
}

// stringEnd finds the close of the literal starting at i and returns the
// rune index just past it (or the end of the text when it never closes).
func (s *lightScanner) stringEnd() int {
	r := s.runes[s.i]
	if s.lx.tripleQuoted && s.hasPrefixAt(strings.Repeat(string(r), 3), s.i) {
		end := s.i + 3
		for end < len(s.runes) && !s.hasPrefixAt(strings.Repeat(string(r), 3), end) {
			end++
		}
		if end < len(s.runes) {
			end += 3
		}
		return end
	}
	end := s.i + 1
	for end < len(s.runes) {
		c := s.runes[end]
		if c == '\\' && s.lx.escapes && r != '`' && end+1 < len(s.runes) {
			end += 2
			continue
		}
		end++
		if c == r {
			break
		}
		if c == '\n' && !s.lx.strSpansNL {
			break // python's one-line strings end at the newline
		}
	}
	return end
}

// dollarString walks a bash string: the quotes and the text are string, the
// `$name` variables inside are classVar, `${` is an expansion of variables
// and keyword defaults, and `$( ... )` is a keyword pair around ordinary
// code. Only double quotes interpolate; single quotes are literal to their
// close, the way chroma's `'.*?'` is.
func (s *lightScanner) dollarString(q rune) {
	s.fill(classString, s.i, s.i+1)
	s.i++
	for s.i < len(s.runes) {
		r := s.runes[s.i]
		switch {
		case r == '\\' && q == '"' && s.i+1 < len(s.runes):
			s.fill(classString, s.i, s.i+2)
			s.i += 2
		case r == q:
			s.fill(classString, s.i, s.i+1)
			s.i++
			return
		case r == '$' && q == '"':
			s.atDollar() // a $ nothing matches is chroma's plain Text
		default:
			j := s.i
			for j < len(s.runes) && s.runes[j] != q && !(q == '"' && (s.runes[j] == '$' || s.runes[j] == '\\')) {
				j++
			}
			if j == s.i {
				j++ // a backslash at the very end is content; keep moving
			}
			s.fill(classString, s.i, j)
			s.i = j
		}
	}
}

// templateString walks a js template literal: the backticks and text are
// string and `${expr}` interpolates ordinary code between string runes.
func (s *lightScanner) templateString() {
	s.fill(classString, s.i, s.i+1)
	s.i++
	for s.i < len(s.runes) {
		r := s.runes[s.i]
		switch {
		case r == '\\' && s.i+1 < len(s.runes):
			s.fill(classString, s.i, s.i+2)
			s.i += 2
		case r == '`':
			s.fill(classString, s.i, s.i+1)
			s.i++
			return
		case r == '$' && s.i+1 < len(s.runes) && s.runes[s.i+1] == '{':
			s.fill(classString, s.i, s.i+2)
			s.i += 2
			s.interp()
		default:
			j := s.i
			for j < len(s.runes) && s.runes[j] != '`' && s.runes[j] != '\\' && s.runes[j] != '$' {
				j++
			}
			if j == s.i {
				j++ // a $ or trailing backslash that opens nothing is text
			}
			s.fill(classString, s.i, j)
			s.i = j
		}
	}
}

// fstringBody walks a python f-string the same way: text is string, `{expr}`
// interpolates ordinary code between string runes. A format-spec `:` after
// an expression stays chroma's string, so interp's plain punctuation is the
// only divergence there.
func (s *lightScanner) fstringBody(q rune) {
	s.fill(classString, s.i, s.i+1)
	s.i++
	for s.i < len(s.runes) {
		r := s.runes[s.i]
		switch {
		case r == '\\' && s.lx.escapes && s.i+1 < len(s.runes):
			s.fill(classString, s.i, s.i+2)
			s.i += 2
		case r == q:
			s.fill(classString, s.i, s.i+1)
			s.i++
			return
		case r == '{':
			s.fill(classString, s.i, s.i+1)
			s.i++
			s.interp()
		case r == '\n' && !s.lx.strSpansNL:
			return // the literal ends at the newline like any other
		default:
			j := s.i
			for j < len(s.runes) && s.runes[j] != q && s.runes[j] != '{' && s.runes[j] != '\\' && s.runes[j] != '\n' {
				j++
			}
			if j == s.i {
				j++ // a trailing backslash is content; keep moving
			}
			s.fill(classString, s.i, j)
			s.i = j
		}
	}
}

// interp lexes ordinary code until the brace nesting closes, painting the
// closing brace as string — chroma reads it back into its StringInterpol.
func (s *lightScanner) interp() {
	depth := 1
	for s.i < len(s.runes) {
		r := s.runes[s.i]
		if r == '}' {
			depth--
			if depth == 0 {
				s.fill(classString, s.i, s.i+1)
				s.i++
				return
			}
		}
		if r == '{' {
			depth++
		}
		s.token()
	}
}

// atDollar consumes bash's `$`-prefixed tokens: `$name` and `$1`-style
// parameters are classVar, `$(` and `$((` are keywords around ordinary code
// (subShell), and `${` is an expansion of variables and `:-` defaults.
// A `$` none of these match is plain.
func (s *lightScanner) atDollar() {
	if s.i+1 >= len(s.runes) {
		s.fill(classPlain, s.i, s.i+1)
		s.i++
		return
	}
	switch c := s.runes[s.i+1]; {
	case c == '(':
		n, depth := 2, 1
		arith := false
		if s.i+2 < len(s.runes) && s.runes[s.i+2] == '(' {
			n, depth, arith = 3, 2, true
		}
		s.fill(classKeyword, s.i, s.i+n)
		s.i += n
		s.subShell(depth, arith)
	case c == '{':
		s.braceExpansion()
	case isWordStart(c) && c != '$':
		end := s.i + 1
		for end < len(s.runes) && isWordRune(s.runes[end]) && s.runes[end] != '$' {
			end++ // a `$` ends the name: `$name$(` is a name then a substitution
		}
		s.fill(classVar, s.i, end)
		s.i = end
	case isDigit(c) || strings.ContainsRune("#$?!_*@-", c):
		s.fill(classVar, s.i, s.i+2)
		s.i += 2
	default:
		s.fill(classPlain, s.i, s.i+1)
		s.i++
	}
}

// subShell lexes the body of a `$( ... )` or `$(( ... ))` — ordinary code —
// through its closing paren(s), which chroma tokenises back into Keyword.
// The whitespace inside is Text, and an arithmetic `$(( ))` reads its digit
// runs as numbers even directly before the closing paren.
func (s *lightScanner) subShell(depth int, arith bool) {
	for s.i < len(s.runes) {
		r := s.runes[s.i]
		if isSpace(r) || r == '\n' {
			s.i++ // the whitespace inside is Text — a subshell spans lines
			continue
		}
		if r == ')' {
			s.fill(classKeyword, s.i, s.i+1)
			s.i++
			depth--
			if depth == 0 {
				return
			}
			continue
		}
		if r == '(' {
			depth++
		}
		if arith && isDigit(r) {
			end := s.i
			for end < len(s.runes) && isDigit(s.runes[end]) {
				end++
			}
			s.fill(classNumber, s.i, end)
			s.i = end
			continue
		}
		s.token()
	}
}

// braceExpansion walks bash's `${...}`: the brackets are string (chroma
// tokenises them as interpolation), the words inside are classVar, and `:-`
// is a keyword default.
func (s *lightScanner) braceExpansion() {
	s.fill(classString, s.i, s.i+2)
	j := s.i + 2
	for j < len(s.runes) && s.runes[j] != '}' && s.runes[j] != '\n' {
		c := s.runes[j]
		switch {
		case c == ':' && j+1 < len(s.runes) && s.runes[j+1] == '-':
			s.fill(classKeyword, j, j+2)
			j += 2
		case isWordRune(c):
			k := j
			for k < len(s.runes) && isWordRune(s.runes[k]) {
				k++
			}
			s.fill(classVar, j, k)
			j = k
		default:
			s.fill(classPunct, j, j+1)
			j++
		}
	}
	if j < len(s.runes) && s.runes[j] == '}' {
		s.fill(classString, j, j+1)
		j++
	}
	s.i = j
}

// charLiteralAhead says whether the quote at i opens a char literal rather
// than a lifetime: 'x', '\n', '\” — but not 'a (rust).
func (s *lightScanner) charLiteralAhead() bool {
	if s.i+1 >= len(s.runes) {
		return false
	}
	if s.runes[s.i+1] == '\\' {
		return true
	}
	return s.i+2 < len(s.runes) && s.runes[s.i+2] == '\''
}

// atWord consumes an identifier or keyword. Its class is the table's, or the
// class a declaration, decorator or import owes it, or classFunc where a
// call site is a function name; an unknown word is plain. In bash a word is
// a run of the shell's wider charset and gets its own move.
func (s *lightScanner) atWord() bool {
	if s.lx.bash && isBashWordRune(s.runes[s.i]) {
		s.bashToken()
		return true
	}
	if !isWordStart(s.runes[s.i]) {
		return false
	}
	end := s.i + 1
	for end < len(s.runes) && isWordRune(s.runes[end]) {
		end++
	}
	word := string(s.runes[s.i:end])

	// A rust macro's name travels with its bang when an opener follows.
	if s.lx.macroBang && end < len(s.runes) && s.runes[end] == '!' {
		k := end + 1
		for k < len(s.runes) && isSpace(s.runes[k]) {
			k++
		}
		if k < len(s.runes) && (s.runes[k] == '(' || s.runes[k] == '[' || s.runes[k] == '{') {
			s.fill(classFunc, s.i, end+1)
			s.i = end + 1
			return true
		}
	}

	// A string-affix letter directly before a quote is the string's prefix;
	// an f-affix walks the literal as an f-string.
	if s.lx.strAffix != "" && len(word) <= 2 && onlyAffix(word, s.lx.strAffix) &&
		end < len(s.runes) && strings.ContainsRune(s.lx.quoteChars, s.runes[end]) {
		s.fill(classString, s.i, end)
		s.i = end
		if strings.ContainsAny(word, "fF") {
			s.fstringBody(s.runes[s.i])
		} else {
			s.atString()
		}
		return true
	}

	key := word
	if s.lx.caseFold {
		key = strings.ToUpper(word) // the sql lexer's keywords are case-insensitive
	}
	class, known := s.lx.words[key]
	if known && s.lx.callWords[word] && s.followsCallSep(end) {
		class = classFunc // a Go builtin is a call, not a type, when called
	}
	// TypeScript's annotation rule reads `name: Type` as one group —
	// NameOther, a Text colon, a KeywordType — so an unknown word before a
	// colon keeps the colon plain and types the run behind it, digits too.
	if s.lx.typeColon && !known && s.annotationAhead(end) {
		s.fill(classPlain, s.i, end)
		s.i = end
		for s.i < len(s.runes) && isSpace(s.runes[s.i]) {
			s.i++
		}
		s.fill(classPlain, s.i, s.i+1) // the colon is Text here, not punctuation
		s.i++
		for s.i < len(s.runes) && isSpace(s.runes[s.i]) {
			s.i++
		}
		j := s.i
		for j < len(s.runes) && isTypeRune(s.runes[j]) {
			j++
		}
		s.fill(classType, s.i, j)
		s.i = j
		return true
	}
	switch {
	case s.declLive && s.declParens == 0:
		class = s.pendingDecl
		s.declLive = false
	case s.decorLive:
		class = classFunc
		s.decorLive = end < len(s.runes) && s.runes[end] == '.' // @a.b is one decorator
	case s.typeNext:
		class = classType // rust's typename state types the word after `:` or `->`
		s.typeNext = false
	case known:
		// the table already spoke — `import` stays a keyword inside an import
	case s.nsLive:
		class = classType // an imported name is a namespace
	case s.lx.punctWords[word]:
		class = classPunct
	case s.lx.labelColon && s.labelColonAhead(end):
		class = classNumber // chroma reads `name:` as a label, digits included
	case s.lx.callIsFunc && s.tokenAhead('(', end):
		class = classFunc
	case s.lx.callAfterWord && s.prevWord && s.prevSpace && !s.prevBlocked && !s.accessLive && s.tokenAhead('(', end):
		class = classFunc
	case s.lx.ctypes && strings.HasSuffix(word, "_t") && word[0] >= 'a' && word[0] <= 'z':
		class = classType // c's typedef names
	}
	if _, ok := s.lx.decls[word]; ok {
		s.pendingDecl, s.declLive, s.declParens = s.lx.decls[word], true, 0
	}
	if s.lx.nsKeywords[word] {
		if s.nsLive {
			// `from x import y`: the y an import already named is plain
			s.nsLive = false
		} else {
			s.nsLive = true
		}
	}
	if s.lx.cppAccess && end < len(s.runes) && s.runes[end] == ':' &&
		(word == "public" || word == "private" || word == "protected") {
		s.accessLive = true // the class body's constructor stops reading as a call
	}
	if s.lx.callBlockers[word] {
		s.prevBlocked = true
	}
	s.fill(class, s.i, end)
	s.i = end
	return true
}

// labelColonAhead says whether the word ending at end is followed by a lone
// colon — c's label shape, `name:` with any spaces and never `::`.
func (s *lightScanner) labelColonAhead(end int) bool {
	for end < len(s.runes) && isSpace(s.runes[end]) {
		end++
	}
	if end >= len(s.runes) || s.runes[end] != ':' {
		return false
	}
	return end+1 >= len(s.runes) || s.runes[end+1] != ':'
}

// followsCallSep says whether the word ending at end is directly followed
// by a rune in the language's call separator set (`(` for Go, the first
// whitespace for bash's builtins).
func (s *lightScanner) followsCallSep(end int) bool {
	if end >= len(s.runes) {
		return false
	}
	if s.lx.callSep == "" {
		return s.runes[end] == '('
	}
	return strings.ContainsRune(s.lx.callSep, s.runes[end])
}

// tokenAhead says whether c follows the word ending at end, past spaces.
func (s *lightScanner) tokenAhead(c rune, end int) bool {
	for end < len(s.runes) && isSpace(s.runes[end]) {
		end++
	}
	return end < len(s.runes) && s.runes[end] == c
}

// annotationAhead says whether the word ending at end is followed by a colon
// and then a type rune — the shape of typescript's annotation rule.
func (s *lightScanner) annotationAhead(end int) bool {
	for end < len(s.runes) && isSpace(s.runes[end]) {
		end++
	}
	if end >= len(s.runes) || s.runes[end] != ':' {
		return false
	}
	end++
	for end < len(s.runes) && isSpace(s.runes[end]) {
		end++
	}
	return end < len(s.runes) && isTypeRune(s.runes[end])
}

// isTypeRune matches the annotation rule's character class: a word rune,
// `?`, `.` or `$`.
func isTypeRune(r rune) bool {
	return isWordRune(r) || r == '?' || r == '.' || r == '$'
}

// bashToken consumes one of bash's Text tokens — a run of the shell's word
// charset, which carries `-`, `.`, `>` and `*` — painting it as a number
// when the line ends it, a builtin when a call separator follows, a
// variable when an assignment follows, and its table class otherwise. This
// is chroma's own rule order.
func (s *lightScanner) bashToken() {
	end := s.i
	for end < len(s.runes) && isBashWordRune(s.runes[end]) {
		end++
	}
	word := string(s.runes[s.i:end])
	digits := word != "" && allRunes(word, isDigit)
	next := rune(0)
	if end < len(s.runes) {
		next = s.runes[end]
	}
	k := end
	for k < len(s.runes) && isSpace(s.runes[k]) {
		k++
	}
	assign := allRunes(word, isWordRune) && k < len(s.runes) &&
		(s.runes[k] == '=' || (s.runes[k] == '+' && k+1 < len(s.runes) && s.runes[k+1] == '='))
	switch {
	case digits && (next == 0 || isSpace(next) || next == '\n'):
		s.fill(classNumber, s.i, end) // \d+(?= |$)
	case s.lx.callWords[word] && strings.ContainsRune(" \t\r\n)`", next):
		s.fill(classFunc, s.i, end)
	case assign:
		s.fill(classVar, s.i, end) // (\b\w+)(\s*)(\+?=)
	default:
		class, known := s.lx.words[word]
		if !known {
			class = classPlain
		}
		s.fill(class, s.i, end)
	}
	s.i = end
}

// atNumber consumes a numeric literal: 0x/0b/0o prefixes, decimals with a
// fraction and exponent, and the type-suffix letters that follow (u32, L, n,
// j), which travel with the number in every chroma lexer here.
func (s *lightScanner) atNumber() bool {
	if !isDigit(s.runes[s.i]) {
		return false
	}
	if s.lx.plainNumbers {
		// java's number rules never fire: the digit run reads as a name
		end := s.i
		for end < len(s.runes) && isWordRune(s.runes[end]) {
			end++
		}
		s.fill(classPlain, s.i, end)
		s.i = end
		return true
	}
	end := s.i
	if s.runes[end] == '0' && end+1 < len(s.runes) {
		switch s.runes[end+1] {
		case 'x', 'X', 'b', 'B', 'o', 'O':
			end += 2
			for end < len(s.runes) && (isDigit(s.runes[end]) || isHexDigit(s.runes[end]) || s.runes[end] == '_') {
				end++
			}
			s.fill(classNumber, s.i, end)
			s.i = end
			return true
		}
	}
	for end < len(s.runes) && (isDigit(s.runes[end]) || s.runes[end] == '_') {
		end++
	}
	if end+1 < len(s.runes) && !s.lx.numNoDot && s.runes[end] == '.' && isDigit(s.runes[end+1]) {
		end++
		for end < len(s.runes) && (isDigit(s.runes[end]) || s.runes[end] == '_') {
			end++
		}
	}
	if end < len(s.runes) && (s.runes[end] == 'e' || s.runes[end] == 'E') {
		j := end + 1
		if j < len(s.runes) && (s.runes[j] == '+' || s.runes[j] == '-') {
			j++
		}
		if j < len(s.runes) && isDigit(s.runes[j]) {
			end = j
			for end < len(s.runes) && isDigit(s.runes[end]) {
				end++
			}
		}
	}
	for end < len(s.runes) && isWordRune(s.runes[end]) {
		end++ // suffix: 100u32, 10L, 3n, 2j
	}
	s.fill(classNumber, s.i, end)
	s.i = end
	return true
}

// scanScalar handles one yaml line: anchors, aliases and tags read as
// chroma's CommentPreproc; a key (bare or quoted, ending in a colon) is
// classVar; a `>` or `|` value opens a folded block; and the value units
// are strings unless they are numbers or the language's booleans.
func (s *lightScanner) scanScalar() bool {
	if s.runes[s.i] == ':' {
		return false // the colon after a key is punctuation, not a value
	}
	if s.lineStart() && s.runes[s.i] == '-' && s.i+1 < len(s.runes) && s.runes[s.i+1] == ' ' {
		s.fill(classPlain, s.i, s.i+1) // a list dash is chroma's Text
		s.i++
		return true
	}
	if s.lineStart() && (s.runes[s.i] == '&' || s.runes[s.i] == '*' || s.runes[s.i] == '!') {
		end := s.i + 1
		for end < len(s.runes) && !isSpace(s.runes[end]) && s.runes[end] != '\n' {
			end++
		}
		s.fill(classComment, s.i, end)
		s.i = end
		return true
	}
	if s.keyAhead() {
		if q := s.runes[s.i]; q == '"' || q == '\'' {
			s.atString() // quotedKeys: "key": is a name
		} else {
			end := s.i + 1
			for end < len(s.runes) && (isWordRune(s.runes[end]) || s.runes[end] == '.' || s.runes[end] == '/') {
				end++
			}
			s.fill(classVar, s.i, end)
			s.i = end
		}
		return true
	}
	if s.runes[s.i] == '>' || s.runes[s.i] == '|' {
		s.foldedBlock()
		return true
	}
	// The value. Chroma tokenises a boolean or a whole-unit number alone
	// (`4 threads` is a number, a space, then a scalar; `72Gi` is not a
	// number at all), a leading `-` as Text — its list-dash rule — and
	// every other plain scalar as one string to the line's end, whose last
	// whitespace before a `#` stays root's.
	if q := s.runes[s.i]; q == '"' || q == '\'' {
		return false // a quoted value is a string; atString reads it whole
	}
	if s.runes[s.i] == '-' {
		s.fill(classPlain, s.i, s.i+1)
		s.i++
		return true
	}
	end := s.i
	for end < len(s.runes) && !isSpace(s.runes[end]) && s.runes[end] != '\n' && s.runes[end] != '#' {
		end++
	}
	if end == s.i {
		return false // nothing here to classify; token() or punct() takes it
	}
	unit := string(s.runes[s.i:end])
	if c, ok := s.lx.scalars[unit]; ok {
		s.fill(c, s.i, end)
		s.i = end
		return true
	}
	if len(unit) <= 3 && allRunes(unit, func(r rune) bool { return strings.ContainsRune("oOnNfF", r) }) {
		s.fill(classNumber, s.i, end) // the `[oNnFf]{1,3}` rule: on, off, no
		s.i = end
		return true
	}
	if isYamlNumber(unit) {
		s.fill(classNumber, s.i, end)
		s.i = end
		return true
	}
	vend := end
	for vend < len(s.runes) && s.runes[vend] != '\n' && s.runes[vend] != '#' {
		vend++
	}
	if vend < len(s.runes) && s.runes[vend] == '#' && isSpace(s.runes[vend-1]) {
		vend-- // the final whitespace before the comment is root's
	}
	s.fill(classString, s.i, vend)
	s.i = vend
	return true
}

// keyAhead says whether the token at i ends in a colon with only a space or
// the line's end behind it — yaml's definition of a key.
func (s *lightScanner) keyAhead() bool {
	end := s.i
	if q := s.runes[end]; q == '"' || q == '\'' {
		end++
		for end < len(s.runes) && s.runes[end] != q {
			end++
		}
		if end < len(s.runes) {
			end++
		}
	} else if isWordStart(s.runes[end]) {
		for end < len(s.runes) && (isWordRune(s.runes[end]) || s.runes[end] == '.' || s.runes[end] == '/') {
			end++
		}
	} else {
		return false
	}
	for end < len(s.runes) && s.runes[end] == ' ' {
		end++
	}
	if end >= len(s.runes) || s.runes[end] != ':' {
		return false
	}
	return end+1 >= len(s.runes) || s.runes[end+1] == ' ' || s.runes[end+1] == '\n'
}

// foldedBlock consumes a yaml `>` / `|` block: the marker is punctuation and
// every following line indented deeper than the marker's line is string.
func (s *lightScanner) foldedBlock() {
	lineStart := s.i
	for lineStart > 0 && s.runes[lineStart-1] != '\n' {
		lineStart--
	}
	indent := 0
	for lineStart+indent < s.i && s.runes[lineStart+indent] == ' ' {
		indent++
	}
	s.fill(classPunct, s.i, s.i+1)
	s.i++
	for s.i < len(s.runes) && (s.runes[s.i] == '+' || s.runes[s.i] == '-') {
		s.fill(classPunct, s.i, s.i+1)
		s.i++
	}
	for s.i < len(s.runes) {
		if s.runes[s.i] == '\n' {
			s.i++
			continue
		}
		lineStart := s.i
		for s.i < len(s.runes) && s.runes[s.i] == ' ' {
			s.i++
		}
		if s.i >= len(s.runes) || s.runes[s.i] == '\n' {
			continue
		}
		if s.i-lineStart <= indent { // back at key level: the block is over
			s.i = lineStart
			return
		}
		for s.i < len(s.runes) && s.runes[s.i] != '\n' {
			s.i++
		}
		s.fill(classString, lineStart, s.i)
	}
}

// punct paints one rune of punctuation or an operator, keeping a pending
// declaration alive only across the receiver group it may span, ending an
// import at its semicolon, and painting bash's `)` as the keyword chroma's
// root rule makes it. Rust's colons are Text (a single one typing the word
// behind it), bash's redirect is Text, and java's post-dot word is a name.
func (s *lightScanner) punct() {
	if s.lx.colonState {
		if r := s.runes[s.i]; r == ':' {
			n := 1 // `::` is a path separator; a lone `:` types what follows
			if s.i+1 < len(s.runes) && s.runes[s.i+1] == ':' {
				n = 2
			}
			s.fill(classPlain, s.i, s.i+n)
			s.i += n
			s.typeNext = n == 1
			return
		}
		if r := s.runes[s.i]; r == '-' && s.i+1 < len(s.runes) && s.runes[s.i+1] == '>' {
			s.fill(classPlain, s.i, s.i+2)
			s.i += 2
			s.typeNext = true
			return
		}
	}
	if s.lx.bash && s.runes[s.i] == '<' {
		s.fill(classPlain, s.i, s.i+1) // the redirect is chroma's Text
		s.i++
		return
	}
	if s.lx.dotVar && !s.nsLive && s.runes[s.i] == '.' &&
		s.i+1 < len(s.runes) && isWordRune(s.runes[s.i+1]) {
		end := s.i + 1
		for end < len(s.runes) && isWordRune(s.runes[end]) {
			end++
		}
		s.fill(classPunct, s.i, s.i+1)
		s.fill(classVar, s.i+1, end)
		s.i = end
		return
	}
	s.typeNext = false // any other punctuation closes a pending typename
	if s.declLive {
		switch r := s.runes[s.i]; {
		case r == '(':
			s.declParens++
		case r == ')' && s.declParens > 0:
			s.declParens--
		case s.declParens == 0:
			s.declLive = false
		}
	}
	if s.accessLive && (s.runes[s.i] == '{' || s.runes[s.i] == '}' || s.runes[s.i] == ';') {
		s.accessLive = false // the class body's braces close the state
	}
	class := classPunct
	switch {
	case s.lx.bash && s.runes[s.i] == ')':
		class = classKeyword
	case s.nsLive && (s.runes[s.i] == '.' || s.runes[s.i] == '*'):
		class = classType // the dots of an imported name are namespace too
	}
	if s.nsLive && s.runes[s.i] == ';' {
		s.nsLive = false
	}
	s.fill(class, s.i, s.i+1)
	s.i++
}

func (s *lightScanner) fill(c codeClass, from, to int) {
	for k := from; k < to && k < len(s.classes); k++ {
		s.classes[k] = c
	}
}

func (s *lightScanner) hasPrefixAt(p string, at int) bool {
	for _, r := range p {
		if at >= len(s.runes) || s.runes[at] != r {
			return false
		}
		at++
	}
	return true
}

// lineStart says whether only whitespace precedes i on its line.
func (s *lightScanner) lineStart() bool {
	for k := s.i - 1; k >= 0; k-- {
		switch s.runes[k] {
		case ' ', '\t':
			continue
		case '\n':
			return true
		default:
			return false
		}
	}
	return true
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\r' }

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func isHexDigit(r rune) bool {
	return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func isWordStart(r rune) bool {
	return r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isWordRune(r rune) bool { return isWordStart(r) || isDigit(r) }

// isBashWordRune matches chroma's bash Text charset: everything a shell
// word may carry — flags, redirects, globs — except the metacharacters that
// end one.
func isBashWordRune(r rune) bool {
	return !strings.ContainsRune("=\t\r\n[]{}()$\"'`\\<&|; ", r)
}

// onlyAffix says whether every rune of word is a string-affix letter.
func onlyAffix(word, affix string) bool {
	for _, r := range word {
		if !strings.ContainsRune(affix, r) {
			return false
		}
	}
	return true
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func allRunes(s string, ok func(rune) bool) bool {
	for _, r := range s {
		if !ok(r) {
			return false
		}
	}
	return true
}

// isYamlNumber mirrors chroma's yaml number rule: a hex, octal or decimal
// form, as one whole unit. A sign is not part of it (a leading `-` reads as
// the list dash) and neither is .inf/.nan — those are scalars, strings.
func isYamlNumber(unit string) bool {
	u := strings.TrimPrefix(strings.TrimPrefix(unit, "+"), "-")
	if strings.HasPrefix(u, "0x") {
		return u[2:] != "" && allRunes(u[2:], isHexDigit)
	}
	if strings.HasPrefix(u, "0o") {
		return u[2:] != "" && allRunes(u[2:], func(r rune) bool { return r >= '0' && r <= '7' })
	}
	dot, digits, exp := false, false, false
	for _, r := range u {
		switch {
		case isDigit(r):
			digits = true
		case r == '.' && !dot && !exp:
			dot = true
		case (r == 'e' || r == 'E') && digits && !exp:
			exp = true
		case (r == '+' || r == '-') && exp:
			continue
		default:
			return false
		}
	}
	return digits
}

// classWords splits a space-joined blob into a word→class map: the form the
// extracted chroma word lists compress into.
func classWords(c codeClass, words string) map[string]codeClass {
	m := make(map[string]codeClass, 32)
	for _, w := range strings.Fields(words) {
		m[w] = c
	}
	return m
}

// withWords merges another blob into the map, for a language whose words
// fall into several classes.
func withWords(m map[string]codeClass, c codeClass, words string) map[string]codeClass {
	for _, w := range strings.Fields(words) {
		m[w] = c
	}
	return m
}

func wordSet(words string) map[string]bool {
	m := make(map[string]bool, 32)
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

// lightLangs is the browser lexer's vocabulary. The blobs are the word lists
// of chroma's own lexers, mapped through the same classOf the terminal uses,
// and each table's comment and string conventions follow its lexer's rules.
var lightLangs = map[string]lightLang{
	"go": {
		words: withWords(withWords(classWords(classKeyword,
			"break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var"),
			classNumber, "true false iota nil"),
			classType, "uint uint8 uint16 uint32 uint64 int int8 int16 int32 int64 float float32 float64 complex64 complex128 byte rune string bool error uintptr"),
		decls:      map[string]codeClass{"func": classFunc},
		callWords:  wordSet("uint uint8 uint16 uint32 uint64 int int8 int16 int32 int64 float float32 float64 complex64 complex128 byte rune string bool error uintptr print println panic recover close complex real imag len cap append copy delete new make clear min max"),
		callIsFunc: true,
		lineCom:    []string{"//"},
		blockCom:   [2]string{"/*", "*/"},
		quoteChars: "\"'`",
		escapes:    true,
		strSpansNL: true,
	},
	"python": {
		words: withWords(withWords(withWords(classWords(classKeyword,
			"nonlocal continue finally except lambda assert global return raise yield while break await async pass else elif with try for del as if match case def class import from"),
			classNumber, "False True None"),
			classType, "self Ellipsis NotImplemented cls PendingDeprecationWarning ConnectionAbortedError ConnectionRefusedError UnicodeTranslateError ConnectionResetError ModuleNotFoundError NotImplementedError FloatingPointError StopAsyncIteration UnicodeDecodeError DeprecationWarning UnicodeEncodeError NotADirectoryError ProcessLookupError ZeroDivisionError IsADirectoryError FileNotFoundError UnboundLocalError KeyboardInterrupt ChildProcessError EnvironmentError IndentationError InterruptedError BlockingIOError ArithmeticError ConnectionError BrokenPipeError FileExistsError ResourceWarning PermissionError RuntimeWarning ReferenceError AttributeError AssertionError UnicodeWarning RecursionError StopIteration BaseException OverflowError SyntaxWarning FutureWarning GeneratorExit ImportWarning UnicodeError TimeoutError WindowsError RuntimeError BytesWarning SystemError UserWarning MemoryError ImportError LookupError BufferError SyntaxError SystemExit ValueError IndexError NameError Exception TypeError TabError EOFError KeyError VMSError Warning OSError IOError"),
			classFunc, "staticmethod classmethod memoryview __import__ issubclass isinstance frozenset bytearray enumerate reversed property compile complex delattr hasattr setattr globals getattr divmod filter locals format object sorted slice print bytes range input tuple round super float eval list dict repr type vars hash next bool open iter oct pow min zip max map bin len set any dir all abs str sum chr int hex ord id"),
		decls:        map[string]codeClass{"def": classFunc, "class": classType},
		punctWords:   wordSet("in is and or not"),
		nsKeywords:   wordSet("import from"),
		decorFunc:    true,
		lineCom:      []string{"#"},
		quoteChars:   "\"'",
		escapes:      true,
		tripleQuoted: true,
		strAffix:     "rbfuRBFU",
	},
	"javascript": {
		words: withWords(withWords(classWords(classKeyword,
			"for in while do break return continue switch case default if else throw try catch finally new delete typeof instanceof void yield this of var let with function abstract async await boolean byte char class const debugger double enum export extends final float goto implements import int interface long native package private protected public short static super synchronized throws transient volatile"),
			classNumber, "true false null NaN Infinity undefined"),
			classFunc, "Array Boolean Date Error Function Math netscape Number Object Packages RegExp String Promise Proxy sun decodeURI decodeURIComponent encodeURI encodeURIComponent eval isFinite isNaN isSafeInteger parseFloat parseInt document window"),
		lineCom:          []string{"//"},
		comEatsNL:        true,
		blockCom:         [2]string{"/*", "*/"},
		quoteChars:       "\"'`",
		escapes:          true,
		strSpansNL:       true,
		templateBacktick: true,
	},
	"typescript": {
		words: withWords(withWords(withWords(classWords(classKeyword,
			"for in of while do break return yield continue switch case default if else throw try catch finally new delete typeof instanceof keyof asserts is infer await void this var let with function abstract async boolean class const debugger enum export extends from get global goto implements import interface namespace package private protected public readonly require set static super type as declare constructor"),
			classNumber, "true false null NaN Infinity undefined"),
			classType, "string bool number any never object symbol unique unknown bigint"),
			classFunc, "Array Boolean Date Error Function Math Number Object Packages RegExp String decodeURI decodeURIComponent encodeURI encodeURIComponent eval isFinite isNaN parseFloat parseInt document window"),
		lineCom:          []string{"//"},
		comEatsNL:        true,
		blockCom:         [2]string{"/*", "*/"},
		quoteChars:       "\"'`",
		escapes:          true,
		strSpansNL:       true,
		templateBacktick: true,
		typeColon:        true,
	},
	"rust": {
		words: withWords(withWords(withWords(classWords(classKeyword,
			"unsafe fn static extern return const crate mod where while await trait super async match impl else move loop pub ref mut for dyn use box in if as abstract override unsized virtual become typeof final macro yield priv try do struct enum type union"),
			classNumber, "true false"),
			classType, "isize usize bool char u128 i128 i64 i32 i16 str u64 u32 f32 f64 u16 i8 u8 self Self"),
			classFunc, "DoubleEndedIterator ExactSizeIterator IntoIterator PartialOrd PartialEq ToString Iterator ToOwned Default Result String FnOnce Extend Option FnMut Unpin Sized AsRef AsMut Clone None From Into Sync drop Send Drop Copy Some Ord Err Box Vec Eq Ok Fn"),
		decls:       map[string]codeClass{"fn": classFunc, "struct": classType, "enum": classType, "type": classType, "union": classType},
		macroBang:   true,
		colonState:  true,
		lineCom:     []string{"///", "//!", "//"},
		docLineCom:  []string{"///", "//!"},
		comEatsNL:   true,
		blockCom:    [2]string{"/*", "*/"},
		nestedCom:   true,
		docBlockCom: [2]string{"/**", "*/"},
		quoteChars:  "'\"",
		escapes:     true,
		strSpansNL:  true,
		strAffix:    "br",
		lifetime:    true,
	},
	"c": {
		words: withWords(withWords(classWords(classKeyword,
			"restricted volatile continue register default typedef struct extern switch sizeof static return union while const break goto enum else case auto for asm if do typename __inline restrict _inline thread inline naked"),
			classType, "bool int long float short double char char8_t char16_t char32_t unsigned signed void int8_t int16_t int32_t int64_t uint8_t uint16_t uint32_t uint64_t int_fast8_t int_fast16_t int_fast32_t int_fast64_t uint_fast8_t uint_fast16_t uint_fast32_t uint_fast64_t int_least8_t int_least16_t int_least32_t int_least64_t uint_least8_t uint_least16_t uint_least32_t uint_least64_t"),
			classFunc, "true false NULL"),
		callIsFunc: true,
		preproc:    true,
		ctypes:     true,
		labelColon: true,
		lineCom:    []string{"//"},
		comEatsNL:  true,
		blockCom:   [2]string{"/*", "*/"},
		quoteChars: "'\"",
		escapes:    true,
	},
	"cpp": {
		words: withWords(withWords(classWords(classKeyword,
			"class reinterpret_cast static_assert thread_local dynamic_cast static_cast const_cast co_return protected namespace consteval constexpr typename co_await co_yield operator restrict explicit template override noexcept requires decltype alignof private alignas virtual mutable nullptr concept export friend typeid throws public delete final throw catch using this new try restricted volatile continue register default typedef struct extern switch sizeof static return union while const break goto enum else case auto for asm if do __inline _inline thread inline naked"),
			classType, "bool int long float short double char char8_t char16_t char32_t wchar_t unsigned signed void int8_t int16_t int32_t int64_t uint8_t uint16_t uint32_t uint64_t int_fast8_t int_fast16_t int_fast32_t int_fast64_t uint_fast8_t uint_fast16_t uint_fast32_t uint_fast64_t int_least8_t int_least16_t int_least32_t int_least64_t uint_least8_t uint_least16_t uint_least32_t uint_least64_t"),
			classFunc, "true false NULL"),
		decls:         map[string]codeClass{"class": classType, "struct": classType, "enum": classType},
		callAfterWord: true,
		preproc:       true,
		labelColon:    true,
		cppAccess:     true,
		lineCom:       []string{"//"},
		comEatsNL:     true,
		blockCom:      [2]string{"/*", "*/"},
		quoteChars:    "'\"",
		escapes:       true,
	},
	"java": {
		words: withWords(withWords(classWords(classKeyword,
			"assert break case catch continue default do else finally for if goto instanceof new return switch this throw try while abstract const enum extends final implements native private protected public sealed static strictfp super synchronized throws transient volatile yield class interface import package"),
			classType, "boolean byte char double float int long short void"),
			classNumber, "true false null"),
		decls:         map[string]codeClass{"class": classType, "interface": classType, "record": classType},
		nsKeywords:    wordSet("import package"),
		decorFunc:     true,
		callAfterWord: true,
		plainNumbers:  true,
		dotVar:        true,
		callBlockers:  wordSet("assert break case catch continue default do else finally for if goto instanceof new return switch this throw try while"),
		lineCom:       []string{"//"},
		blockCom:      [2]string{"/*", "*/"},
		quoteChars:    "\"'",
		escapes:       true,
	},
	"json": {
		words:      classWords(classNumber, "true false null"),
		quotedKeys: true,
		quoteChars: "\"",
		escapes:    true,
		strSpansNL: true,
	},
	"yaml": {
		quotedKeys: true,
		scalar:     true,
		scalars:    classWords(classNumber, "true True TRUE false False FALSE null Null NULL ~"),
		lineCom:    []string{"#"},
		comBound:   true,
		quoteChars: "\"'",
		escapes:    true,
	},
	"sql": {
		words: withWords(classWords(classKeyword,
			"DATETIME_INTERVAL_PRECISION PARAMETER_SPECIFIC_CATALOG PARAMATER_ORDINAL_POSITION USER_DEFINED_TYPE_CATALOG PARAMATER_SPECIFIC_SCHEMA TRANSACTIONS_ROLLED_BACK USER_DEFINED_TYPE_SCHEMA PARAMETER_SPECIFIC_NAME DATETIME_INTERVAL_CODE TRANSACTIONS_COMMITTED USER_DEFINED_TYPE_NAME CHARACTER_SET_CATALOG DYNAMIC_FUNCTION_CODE COMMAND_FUNCTION_CODE RETURNED_OCTET_LENGTH MESSAGE_OCTET_LENGTH CHARACTER_SET_SCHEMA CONSTRAINT_CATALOG TRANSACTION_ACTIVE CHARACTER_SET_NAME CURRENT_TIMESTAMP CONSTRAINT_SCHEMA COLLATION_CATALOG RETURNED_SQLSTATE DYNAMIC_FUNCTION CONDITION_NUMBER CHARACTER_LENGTH COMMAND_FUNCTION COLLATION_SCHEMA CHARACTERISTICS TRIGGER_CATALOG CONNECTION_NAME SUBCLASS_ORIGIN RETURNED_LENGTH TIMEZONE_MINUTE CONSTRAINT_NAME ROUTINE_CATALOG TRIGGER_SCHEMA ROUTINE_SCHEMA LOCALTIMESTAMP IMPLEMENTATION PARAMATER_NAME MESSAGE_LENGTH PARAMETER_MODE COLLATION_NAME TIMEZONE_HOUR SPECIFIC_NAME DETERMINISTIC CORRESPONTING AUTHORIZATION INSTANTIABLE CURRENT_TIME CURRENT_USER ROUTINE_NAME NOCREATEUSER MESSAGE_TEXT SQLEXCEPTION CATALOG_NAME SESSION_USER CLASS_ORIGIN CURRENT_ROLE SPECIFICTYPE SERIALIZABLE CURRENT_DATE OCTET_LENGTH CURRENT_PATH TRIGGER_NAME CHAR_LENGTH SYSTEM_USER REFERENCING UNENCRYPTED COLUMN_NAME SQLWARNINIG DIAGNOSTICS CURSOR_NAME SERVER_NAME INSENSITIVE SCHEMA_NAME UNCOMMITTED TRANSACTION CONSTRUCTOR LANCOMPILER CARDINALITY CONSTRAINTS TRANSLATION CHECKPOINT CONSTRAINT CONNECTION PRIVILEGES COMPLETION CONVERSION DELIMITERS TABLE_NAME INDITCATOR INITIALIZE DESCRIPTOR REPEATABLE CREATEUSER DEFERRABLE DESTRUCTOR PROCEDURAL DICTIONARY DISCONNECT TRANSFORMS KEY_MEMBER BIT_LENGTH ASYMMETRIC ASSIGNMENT ASENSITIVE OVERRIDING PARAMETERS REFERENCES ORDINALITY NOCREATEDB STATISTICS DEALLOCATE SAVE_POINT RECURSIVE STRUCTURE SUBSTRING IMMEDIATE GENERATED SYMMETRIC STATEMENT INCREMENT IMMUTABLE INCLUDING COMMITTED TEMPORARY INITIALLY TERMINATE PRECISION DELIMITER TIMESTAMP INTERSECT ISOLATION TRANSFORM TRANSLATE ROW_COUNT ASSERTION PARAMETER EXCLUSIVE LOCALTIME VALIDATOR AGGREGATE EXCLUDING SENSITIVE EXCEPTION ENCRYPTED OPERATION HIERARCHY COLLATION PROCEDURE CONTINUE ENCODING MINVALUE SPECIFIC ABSOLUTE SECURITY WHENEVER EXISTING VOLATILE MAXVALUE EXTERNAL NULLABLE VARIABLE SQLERROR DISTINCT DISPATCH END-EXEC LOCATION ALLOCATE OVERLAPS UNLISTEN ROLLBACK TRUNCATE DESCRIBE SQLSTATE BACKWARD FUNCTION LANGUAGE KEY_TYPE CASCADED POSITION TRAILING DEFERRED RELATIVE DEFAULTS COALSECE PREORDER GROUPING MODIFIES INHERITS PRESERVE DATABASE RESTRICT IDENTITY TEMPLATE NATIONAL CONTAINS CREATEDB IMPLICIT OPERATOR CONVERT CURRENT CONNECT RECHECK PRIMARY STORAGE DECLARE DEFAULT HANDLER COLLATE PREPARE REINDEX GRANTED CHECKED POSTFIX REPLACE INSTEAD CATALOG RESTART INVOKER PLACING PENDANT DEFINED ITERATE PARTIAL CASCADE BREADTH GENERAL TRIGGER SESSION BETWEEN DEFINER LATERAL LEADING RETURNS TRUSTED UNKNOWN FORWARD UNNAMED OVERLAY FORTRAN ANALYZE OPTIONS ANALYSE FOREIGN ROUTINE LOCATOR DESTROY SUBLIST VERBOSE EXTRACT NOTNULL EXPLAIN VERSION SQLCODE EXECUTE NOTHING DYNAMIC WITHOUT SIMILAR NATURAL COMMENT CLUSTER PASCAL SOURCE EQUALS CALLED ESCAPE EXCEPT SELECT ISNULL DOMAIN SEARCH SCROLL SIMPLE BITVAR MINUTE EXISTS SCHEMA ATOMIC METHOD NOTIFY ACCESS UNIQUE ROLLUP NULLIF OBJECT STABLE COLUMN REVOKE OFFSET COMMIT MODIFY FREEZE DELETE RETURN RESULT UNNEST OPTION GLOBAL VALUES RENAME SYSTEM STATIC UPDATE OUTPUT LISTEN STDOUT STRICT PUBLIC IGNORE PREFIX SECOND CREATE LENGTH BEFORE HAVING INSERT VACUUM CURSOR ELSIF USING ALTER STYPE CYCLE LARGE INPUT CROSS INOUT INNER INFIX INDEX LEVEL USAGE ILIKE VALID OWNER GRANT READS UPPER LIMIT OUTER STDIN SYSID GROUP ALIAS ORDER UNTIL LOCAL RESET START COUNT LOWER TABLE PRIOR AFTER STATE ADMIN RIGHT COBOL FOUND MATCH FORCE ABORT FIRST FINAL CLOSE DEREF FETCH WHERE FALSE SCALE BEGIN CLASS TOAST WRITE NCLOB NCHAR CHECK CHAIN SPACE NAMES EVERY MUMPS CACHE UNION UNDER SETOF MONTH SHARE SCOPE TREAT SHOW SIZE SOME SETS SELF ELSE EACH DROP TYPE FROM RULE DESC ROWS ZONE ROLE TRUE FREE FULL GOTO TRIM HOLD HOST DATA READ INTO USER JOIN CUBE LAST LEFT LESS LIKE LOAD LOCK OPEN COPY ONLY OIDS VIEW WHEN THAN THEN NULL NONE WITH WORK NEXT YEAR MODE CAST CASE MOVE CALL MORE BOTH EXEC CLOB OUT MOD ARE SUM DAY GET AVG NEW SQL ABS MIN ASC END ROW NOT FOR ANY PLI MAX REF MAP ADA KEY AND ADD ALL OLD OFF PAD SET OR ON TO IS OF IN IF GO AS DO AT NO BY C G"),
			classFunc, "CHARACTER SMALLINT INTERVAL DECIMAL SERIAL8 VARYING BOOLEAN VARCHAR INTEGER NUMERIC SERIAL BINARY BIGINT NUMBER FLOAT ARRAY TEXT REAL INT8 DATE CHAR BLOB DEC BIT INT"),
		caseFold:   true,
		numNoDot:   true,
		lineCom:    []string{"--"},
		comEatsNL:  true,
		blockCom:   [2]string{"/*", "*/"},
		quoteChars: "'\"",
		strSpansNL: true,
	},
	"bash": {
		words: classWords(classKeyword,
			"if fi else while do done for then return function case select continue until esac elif"),
		callWords:  wordSet("alias bg bind break builtin caller cd command compgen complete declare dirs disown echo enable eval exec exit export false fc fg getopts hash help history jobs kill let local logout popd printf pushd pwd read readonly set shift shopt source suspend test time times trap true type typeset ulimit umask unalias unset wait"),
		callSep:    " \t\r\n)`",
		lineCom:    []string{"#"},
		comBound:   true,
		quoteChars: "'\"`",
		escapes:    true,
		strSpansNL: true,
		bash:       true,
	},
}

func init() {
	// A fence's alias is its language: the table is shared, not copied.
	for alias, canonical := range map[string]string{
		"py": "python", "js": "javascript", "ts": "typescript", "rs": "rust",
		"yml": "yaml", "sh": "bash", "shell": "bash",
	} {
		lightLangs[alias] = lightLangs[canonical]
	}
}
