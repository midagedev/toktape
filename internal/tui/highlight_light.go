//go:build js

package tui

// lexRunes returns the class of each rune of code, lexed as lang. In this
// build it is the hand-written lexer in highlight_light_impl.go; chroma,
// the terminal's lexer, is most of the wasm download and stays out
// (docs/toktape-spec.ko.md §9.8). The implementation is compiled without a
// build constraint so the terminal's tests can measure its agreement with
// chroma (highlight_agreement_test.go).
func lexRunes(lang, code string) []codeClass {
	return lexRunesLight(lang, code)
}
