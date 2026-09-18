//go:build !js

package tui

import (
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// lexRunes returns the class of each rune of code, lexed as lang.
// Implemented twice: with chroma for the terminal, and with a small
// hand-written lexer for the browser build, where chroma's embedded lexer
// set is most of the download. A language the implementation does not know
// yields all-classPlain, never a guess.
func lexRunes(lang, code string) []codeClass {
	classes := make([]codeClass, utf8.RuneCountInString(code))
	if lx := lexers.Get(lang); lx != nil {
		if it, err := chroma.Coalesce(lx).Tokenise(nil, code); err == nil {
			off := 0
			for tok := it(); tok != chroma.EOF; tok = it() {
				c := classOf(tok.Type)
				n := utf8.RuneCountInString(tok.Value)
				for k := off; k < off+n && k < len(classes); k++ {
					classes[k] = c
				}
				off += n
			}
		}
	}
	return classes
}

// classOf maps chroma's token categories onto the pane's treatment.
func classOf(t chroma.TokenType) codeClass {
	switch t {
	case chroma.NameFunction, chroma.NameFunctionMagic, chroma.NameDecorator,
		chroma.NameBuiltin:
		// A builtin is something being called, so it reads with the calls and
		// not with the types: len() and a user's own helper are the same move
		// to someone scanning a line.
		return classFunc
	case chroma.NameClass, chroma.NameBuiltinPseudo, chroma.NameException,
		chroma.NameNamespace, chroma.KeywordType:
		return classType
	case chroma.NameConstant, chroma.KeywordConstant, chroma.NameLabel:
		return classNumber
	case chroma.NameAttribute, chroma.NameVariable, chroma.NameVariableClass,
		chroma.NameVariableGlobal, chroma.NameVariableInstance, chroma.NameProperty,
		chroma.NameTag:
		return classVar
	}
	switch {
	case t.InCategory(chroma.Comment):
		return classComment
	case t.InCategory(chroma.Keyword):
		return classKeyword
	case t.InSubCategory(chroma.LiteralString):
		return classString
	case t.InSubCategory(chroma.LiteralNumber):
		return classNumber
	case t.InCategory(chroma.Literal):
		return classString
	case t.InCategory(chroma.Punctuation), t.InCategory(chroma.Operator):
		return classPunct
	}
	return classPlain
}
