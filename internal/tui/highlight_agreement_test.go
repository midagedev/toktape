//go:build !js

package tui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The browser lexer's contract is agreement with chroma, the lexer the
// terminal ships (TTP-116, docs/toktape-spec.ko.md §9.8). chroma's per-rune
// classes over these fixtures are the golden; the light lexer is measured
// against it and must stay above the thresholds below, pooled over every
// fixture. The goldens are regenerated with `go test ./internal/tui/ -run
// TestLightLexerAgreesWithChroma -update-lex` — the terminal build is where
// chroma compiles, so the test and its goldens live behind !js too. (-update
// belongs to the view goldens and is already taken.)
var updateGoldens = flag.Bool("update-lex", false, "rewrite the chroma goldens under testdata/highlight")

// lightFixtures lists every language tag the browser lexer promises to
// handle, mapped to the fixture that exercises it. The aliases share their
// canonical language's file: the fence tags `py` and `python` must lex the
// same code identically, and the alias assertion below pins that.
var lightFixtures = []struct{ tag, fixture string }{
	{"go", "go"},
	{"python", "python"}, {"py", "python"},
	{"javascript", "javascript"}, {"js", "javascript"},
	{"typescript", "typescript"}, {"ts", "typescript"},
	{"rust", "rust"}, {"rs", "rust"},
	{"c", "c"}, {"cpp", "cpp"},
	{"java", "java"},
	{"json", "json"},
	{"yaml", "yaml"}, {"yml", "yaml"},
	{"bash", "bash"}, {"sh", "bash"}, {"shell", "bash"},
	{"sql", "sql"},
}

// tally is one class's slice of the agreement score.
type tally struct{ have, agree int }

func (t tally) pct() float64 {
	if t.have == 0 {
		return 100 // nothing to lose: no rune of this class exists
	}
	return float64(t.agree) / float64(t.have) * 100
}

// agreement is the light-vs-chroma scoreboard. The thresholds are the
// lead's; a passing number nobody can see is one nobody can watch drift, so
// the percentages are printed on success as well as on failure.
type agreement struct {
	runes, agree int
	comment      tally
	str          tally
	keyword      tally
}

func (a *agreement) add(golden, light []codeClass) {
	for i, g := range golden {
		l := classPlain
		if i < len(light) {
			l = light[i]
		}
		if g == l {
			a.agree++
		}
		a.runes++
		switch g {
		case classComment:
			a.comment.have++
			if g == l {
				a.comment.agree++
			}
		case classString:
			a.str.have++
			if g == l {
				a.str.agree++
			}
		case classKeyword:
			a.keyword.have++
			if g == l {
				a.keyword.agree++
			}
		}
	}
}

func (a agreement) overall() float64 {
	if a.runes == 0 {
		return 100
	}
	return float64(a.agree) / float64(a.runes) * 100
}

func (a agreement) report() string {
	return fmt.Sprintf("overall %.1f%% (%d/%d), comments %.1f%% (%d/%d), strings %.1f%% (%d/%d), keywords %.1f%% (%d/%d)",
		a.overall(), a.agree, a.runes,
		a.comment.pct(), a.comment.agree, a.comment.have,
		a.str.pct(), a.str.agree, a.str.have,
		a.keyword.pct(), a.keyword.agree, a.keyword.have)
}

// TestLightLexerAgreesWithChroma is the numeric contract of the browser
// build's highlighter (TTP-116): the hand-written lexer's classes against
// the committed chroma goldens, rune for rune, over every fixture pooled.
func TestLightLexerAgreesWithChroma(t *testing.T) {
	dir := filepath.Join("testdata", "highlight")
	var pool agreement
	for _, f := range lightFixtures {
		if f.tag != f.fixture {
			continue // aliases are asserted below, against their canonical run
		}
		code, err := os.ReadFile(filepath.Join(dir, f.fixture+".txt"))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		if n := len(strings.Split(strings.TrimRight(string(code), "\n"), "\n")); n < 25 {
			t.Errorf("%s.txt: %d lines, want at least 25 — a thin fixture agrees trivially", f.fixture, n)
		}

		golden := lexRunes(f.tag, string(code))
		goldenPath := filepath.Join(dir, f.fixture+".golden")
		if *updateGoldens {
			if err := os.WriteFile(goldenPath, []byte(encodeGolden(golden, string(code))), 0o644); err != nil {
				t.Fatalf("write golden: %v", err)
			}
		}
		want, err := os.ReadFile(goldenPath)
		if err != nil {
			t.Fatalf("read golden (run with -update to create it): %v", err)
		}
		wantClasses, err := parseGolden(string(want))
		if err != nil {
			t.Fatalf("%s: %v", goldenPath, err)
		}
		if len(wantClasses) != len(golden) {
			t.Fatalf("%s: golden covers %d runes, fixture has %d — regenerate with -update",
				goldenPath, len(wantClasses), len(golden))
		}

		light := lexRunesLight(f.tag, string(code))
		if len(light) != len(golden) {
			t.Fatalf("%s: light lexer returned %d classes for %d runes", f.fixture, len(light), len(golden))
		}

		var lang agreement
		lang.add(wantClasses, light)
		pool.add(wantClasses, light)
		t.Logf("%-11s overall %.1f%% (%d/%d)", f.fixture+":", lang.overall(), lang.agree, lang.runes)
		if os.Getenv("TOKTAPE_DUMP_LEX") != "" {
			dumpDisagreements(t, f.fixture, string(code), wantClasses, light)
		}
	}
	if *updateGoldens {
		t.Log("goldens rewritten from chroma")
	}

	if p := pool.comment.pct(); p < 98 {
		t.Errorf("light lexer comment agreement %.1f%% (%d/%d), want at least 98%%", p, pool.comment.agree, pool.comment.have)
	}
	if p := pool.str.pct(); p < 95 {
		t.Errorf("light lexer string agreement %.1f%% (%d/%d), want at least 95%%", p, pool.str.agree, pool.str.have)
	}
	if p := pool.keyword.pct(); p < 90 {
		t.Errorf("light lexer keyword agreement %.1f%% (%d/%d), want at least 90%%", p, pool.keyword.agree, pool.keyword.have)
	}
	if p := pool.overall(); p < 90 {
		t.Errorf("light lexer overall agreement %.1f%% (%d/%d), want at least 90%%", p, pool.agree, pool.runes)
	}
	t.Logf("light vs chroma, pooled: %s", pool.report())

	// Aliases: `py` and `python` are the same language to whoever wrote the
	// fence, so the two tags must lex the same text into the same classes.
	for _, f := range lightFixtures {
		if f.tag == f.fixture {
			continue
		}
		code, err := os.ReadFile(filepath.Join(dir, f.fixture+".txt"))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		canonical := lexRunesLight(f.fixture, string(code))
		alias := lexRunesLight(f.tag, string(code))
		if fmt.Sprint(alias) != fmt.Sprint(canonical) {
			t.Errorf("tag %q does not lex like its canonical %q", f.tag, f.fixture)
		}
	}
}

// dumpDisagreements prints every line the light lexer read differently from
// the golden, with one mark per rune (. plain, K keyword, : punct, c comment,
// s string, # number, f func, T type, v var). It is the maintenance window
// into a failing percentage: run the test with TOKTAPE_DUMP_LEX=1 to see
// which construct moved, instead of only that something did.
func dumpDisagreements(t *testing.T, name, code string, golden, light []codeClass) {
	t.Helper()
	marks := map[codeClass]rune{
		classPlain: '.', classKeyword: 'K', classPunct: ':', classComment: 'c',
		classString: 's', classNumber: '#', classFunc: 'f', classType: 'T', classVar: 'v',
	}
	from := 0
	for _, line := range strings.Split(strings.TrimSuffix(code, "\n"), "\n") {
		to := from + len([]rune(line))
		diff := false
		for k := from; k < to; k++ {
			if golden[k] != light[k] {
				diff = true
			}
		}
		if diff {
			t.Logf("%s: %s", name, line)
			t.Logf("  want %s", markLine(marks, golden, from, to))
			t.Logf("  got  %s", markLine(marks, light, from, to))
		}
		from = to + 1
	}
}

func markLine(marks map[codeClass]rune, classes []codeClass, from, to int) string {
	var b strings.Builder
	for k := from; k < to && k < len(classes); k++ {
		b.WriteRune(marks[classes[k]])
	}
	return b.String()
}

// encodeGolden writes one digit per rune, one golden line per fixture line:
// the digit of a line's trailing newline is that golden line's last digit.
func encodeGolden(classes []codeClass, code string) string {
	var b strings.Builder
	di := 0
	for _, r := range code {
		b.WriteByte('0' + byte(classes[di]))
		di++
		if r == '\n' {
			b.WriteByte('\n')
		}
	}
	for ; di < len(classes); di++ { // a fixture with no trailing newline
		b.WriteByte('0' + byte(classes[di]))
	}
	return b.String()
}

// parseGolden turns the digit-per-rune text back into classes.
func parseGolden(text string) ([]codeClass, error) {
	var out []codeClass
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		for _, d := range line {
			if d < '0' || d > '9' {
				return nil, fmt.Errorf("golden has non-digit %q", d)
			}
			out = append(out, codeClass(d-'0'))
		}
	}
	return out, nil
}
