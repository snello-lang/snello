// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teak-lang/teak/token"
)

type elt struct {
	tok token.Kind
	lit string
}

func TestTokens(t *testing.T) {
	tests := []struct {
		src  string
		want []elt
	}{
		{"x := 1", []elt{{token.IDENT, "x"}, {token.DEFINE, ""}, {token.INT, "1"}, {token.SEMICOLON, "\n"}}},
		{"func (t ref Tree) Len() int", []elt{
			{token.FUNC, "func"}, {token.LPAREN, ""}, {token.IDENT, "t"}, {token.REF, "ref"}, {token.IDENT, "Tree"}, {token.RPAREN, ""},
			{token.IDENT, "Len"}, {token.LPAREN, ""}, {token.RPAREN, ""}, {token.IDENT, "int"}, {token.SEMICOLON, "\n"},
		}},
		{"struct Circle impl Shape, Named {", []elt{
			{token.STRUCT, "struct"}, {token.IDENT, "Circle"}, {token.IMPL, "impl"},
			{token.IDENT, "Shape"}, {token.COMMA, ""}, {token.IDENT, "Named"}, {token.LBRACE, ""},
		}},
		{"n := take(mut s.head) ?? return None", []elt{
			{token.IDENT, "n"}, {token.DEFINE, ""}, {token.IDENT, "take"}, {token.LPAREN, ""}, {token.MUT, "mut"},
			{token.IDENT, "s"}, {token.PERIOD, ""}, {token.IDENT, "head"}, {token.RPAREN, ""}, {token.COALESCE, ""},
			{token.RETURN, "return"}, {token.IDENT, "None"}, {token.SEMICOLON, "\n"},
		}},
		{"p.cur = p.lex.Next()?\nx", []elt{
			{token.IDENT, "p"}, {token.PERIOD, ""}, {token.IDENT, "cur"}, {token.ASSIGN, ""}, {token.IDENT, "p"},
			{token.PERIOD, ""}, {token.IDENT, "lex"}, {token.PERIOD, ""}, {token.IDENT, "Next"}, {token.LPAREN, ""},
			{token.RPAREN, ""}, {token.QUESTION, ""}, {token.SEMICOLON, "\n"}, {token.IDENT, "x"}, {token.SEMICOLON, "\n"},
		}},
		{"'0'..='9' | '.' => {", []elt{
			{token.CHAR, "'0'"}, {token.RANGEINC, ""}, {token.CHAR, "'9'"}, {token.OR, ""}, {token.CHAR, "'.'"},
			{token.FATARROW, ""}, {token.LBRACE, ""},
		}},
		{"next *Node<T>?", []elt{
			{token.IDENT, "next"}, {token.MUL, ""}, {token.IDENT, "Node"}, {token.LSS, ""}, {token.IDENT, "T"},
			{token.GTR, ""}, {token.QUESTION, ""}, {token.SEMICOLON, "\n"},
		}},
		{"cache Arc<RwLock<T>>\nn := a >\nb", []elt{
			{token.IDENT, "cache"}, {token.IDENT, "Arc"}, {token.LSS, ""}, {token.IDENT, "RwLock"}, {token.LSS, ""},
			{token.IDENT, "T"}, {token.SHR, ""}, {token.SEMICOLON, "\n"}, {token.IDENT, "n"}, {token.DEFINE, ""},
			{token.IDENT, "a"}, {token.GTR, ""}, {token.SEMICOLON, "\n"}, {token.IDENT, "b"}, {token.SEMICOLON, "\n"},
		}},
		{"out <- job // moved\n", []elt{
			{token.IDENT, "out"}, {token.ARROW, ""}, {token.IDENT, "job"}, {token.SEMICOLON, "\n"},
		}},
		{"1.5e3 0x1F 0b101 1_000 .5 1..=3", []elt{
			{token.FLOAT, "1.5e3"}, {token.INT, "0x1F"}, {token.INT, "0b101"}, {token.INT, "1_000"},
			{token.FLOAT, ".5"}, {token.INT, "1"}, {token.RANGEINC, ""}, {token.INT, "3"}, {token.SEMICOLON, "\n"},
		}},
		{"s := `{\"a\": 1}`", []elt{
			{token.IDENT, "s"}, {token.DEFINE, ""}, {token.STRING, "`{\"a\": 1}`"}, {token.SEMICOLON, "\n"},
		}},
		{"func (s ref Self) Describe() string { return super.Describe() }", []elt{
			{token.FUNC, "func"}, {token.LPAREN, ""}, {token.IDENT, "s"}, {token.REF, "ref"}, {token.SELF, "Self"},
			{token.RPAREN, ""}, {token.IDENT, "Describe"}, {token.LPAREN, ""}, {token.RPAREN, ""}, {token.IDENT, "string"},
			{token.LBRACE, ""}, {token.RETURN, "return"}, {token.SUPER, "super"}, {token.PERIOD, ""}, {token.IDENT, "Describe"},
			{token.LPAREN, ""}, {token.RPAREN, ""}, {token.RBRACE, ""}, {token.SEMICOLON, "\n"},
		}},
		{"a /* multi\nline */ b", []elt{
			{token.IDENT, "a"}, {token.SEMICOLON, "\n"}, {token.IDENT, "b"}, {token.SEMICOLON, "\n"},
		}},
	}
	for _, tt := range tests {
		got, errs := scanAll(t, "test", tt.src)
		if len(errs) > 0 {
			t.Errorf("%q: unexpected errors: %v", tt.src, errs)
		}

		if len(got) != len(tt.want) {
			t.Errorf("%q:\n got  %v\n want %v", tt.src, got, tt.want)
			continue
		}

		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("%q: token %d: got %v %q, want %v %q", tt.src, i, got[i].tok, got[i].lit, tt.want[i].tok, tt.want[i].lit)
			}
		}
	}
}

func TestSingleError(t *testing.T) {
	// An unterminated rune literal reports once, not once per check.
	_, errs := scanAll(t, "test", "'a")
	if len(errs) != 1 {
		t.Errorf("got %d errors %v, want 1", len(errs), errs)
	}
}

func TestBOM(t *testing.T) {
	got, errs := scanAll(t, "test", "\uFEFFpackage main")
	if len(errs) > 0 || len(got) != 3 || got[0].tok != token.PACKAGE {
		t.Errorf("BOM not skipped: %v %v", got, errs)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct{ src, want string }{
		{"\"abc", "string literal not terminated"},
		{"'ab'", "illegal rune literal"},
		{"a .. b", "unexpected '..'"},
		{"1e", "exponent has no digits"},
		{"$", "illegal character"},
		{"/* open", "comment not terminated"},
		{"0x", "hexadecimal literal has no digits"},
		{"0b2", "binary literal has no digits"},
		{"1__0", "'_' must separate successive digits"},
		{"1_", "'_' must separate successive digits"},
	}
	for _, tt := range tests {
		_, errs := scanAll(t, "test", tt.src)
		if len(errs) == 0 || !strings.Contains(errs[0], tt.want) {
			t.Errorf("%q: got errors %v, want one containing %q", tt.src, errs, tt.want)
		}
	}
}

// TestExamples lexes every program under examples/. The programs are the
// acceptance suite; the scanner only requires that they contain no
// illegal characters.
func TestExamples(t *testing.T) {
	for _, f := range exampleFiles(t) {
		toks, errs := scanAll(t, f.name, string(f.src))
		for _, e := range errs {
			t.Error(e)
		}

		for _, tk := range toks {
			if tk.tok == token.ILLEGAL {
				t.Errorf("%s: illegal token %q", f.name, tk.lit)
			}
		}

		if len(toks) == 0 {
			t.Errorf("%s: no tokens", f.name)
		}
	}
}

// BenchmarkExamples lexes every example program. Run with -benchmem:
// allocations per operation are the number to watch, since each one is
// work for Go's collector during a compile.
func BenchmarkExamples(b *testing.B) {
	files := exampleFiles(b)
	var total int

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for _, f := range files {
			var s Scanner
			s.Init(token.NewFile(f.name, len(f.src)), f.src, nil)

			for {
				_, tok, _ := s.Scan()
				if tok == token.EOF {
					break
				}

				total++
			}
		}
	}

	b.ReportMetric(float64(total)/float64(b.N), "tokens/op")
}

type exampleFile struct {
	name string
	src  []byte
}

// exampleFiles returns every .teak program under examples/, and fails
// the test if there are fewer than the twenty the design promises.
func exampleFiles(tb testing.TB) []exampleFile {
	tb.Helper()
	var files []exampleFile
	err := filepath.WalkDir(filepath.Join("..", "examples"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && strings.HasSuffix(path, ".teak") {
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			files = append(files, exampleFile{path, src})
		}

		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}

	if len(files) < 20 {
		tb.Fatalf("found %d example files, want at least 20", len(files))
	}

	return files
}

// scanAll lexes src to the end and returns the tokens and the error
// messages in order.
func scanAll(t *testing.T, name, src string) ([]elt, []string) {
	t.Helper()
	var s Scanner
	var errs []string
	s.Init(token.NewFile(name, len(src)), []byte(src), func(pos token.Position, msg string) {
		errs = append(errs, pos.String()+": "+msg)
	})

	var out []elt
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}

		out = append(out, elt{tok, lit})
		if len(out) > 1_000_000 {
			t.Fatal("scanner did not terminate")
		}
	}

	return out, errs
}
