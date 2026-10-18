// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"strings"
	"testing"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
)

// TestExprs parses each expression and prints it back with every
// binary, unary, coalesce and try operation parenthesized, so the
// output shows exactly how the parser grouped the input. Skipped bodies
// print as {...}.
func TestExprs(t *testing.T) {
	tests := []struct{ src, want string }{
		// Precedence and associativity.
		{"a + b * c", "(a + (b * c))"},
		{"a * b + c", "((a * b) + c)"},
		{"a - b - c", "((a - b) - c)"},
		{"a == b && c || d", "(((a == b) && c) || d)"},
		{"1 << 10", "(1 << 10)"},
		{"a &^ b", "(a &^ b)"},
		{"i < n && j > m", "((i < n) && (j > m))"},
		{"a ?? b == c", "(a ?? (b == c))"},
		{"a ?? b ?? c", "(a ?? (b ?? c))"},
		{"x ?? y + 1", "(x ?? (y + 1))"},

		// Unary and postfix.
		{"-x?", "(-(x?))"},
		{"!!b", "(!(!b))"},
		{"&*p", "(&(*p))"},
		{"!a.b()", "(!a.b())"},
		{"ref x?", "(ref (x?))"},
		{"mut s.head", "(mut s.head)"},
		{"*v * k", "((*v) * k)"},
		{"<-ch", "(<-ch)"},
		{"(<-reply).Unwrap()", "((<-reply)).Unwrap()"},
		{"&Node{key: k, left: None}", "(&Node{key: k, left: None})"},
		{"dist[e.to] == None || nd < dist[e.to]?", "((dist[e.to] == None) || (nd < (dist[e.to]?)))"},
		{"x.y.z", "x.y.z"},
		{"super.Describe()", "super.Describe()"},

		// Calls, indexing, slicing, tuples.
		{"f(a, b,)", "f(a, b)"},
		{"Ok(())", "Ok(())"},
		{"(k, v)", "(k, v)"},
		{"a[i]", "a[i]"},
		{"a[1:]", "a[1:]"},
		{"src[start:l.pos]", "src[start:l.pos]"},
		{"list[:min(10, len(list))]", "list[:min(10, len(list))]"},
		{"kv[ref key]", "kv[(ref key)]"},

		// The ?? else forms.
		{"take(mut s.head) ?? return None", "(take((mut s.head)) ?? return None)"},
		{"kv[ref key] ?? return Err(Missing(key))", "(kv[(ref key)] ?? return Err(Missing(key)))"},
		{"t.nodes.Get(cur?) ?? break", "(t.nodes.Get((cur?)) ?? break)"},
		{"results.Push(d ?? break)", "results.Push((d ?? break))"},
		{"f(t ?? return)", "f((t ?? return))"},
		{"cur ?? { *cur = Some(x); return }", "(cur ?? {...})"},

		// Where < is a type argument list, and where it is not.
		{"a < b", "(a < b)"},
		{"a < f(b)", "(a < f(b))"},
		{"arena.New<Node>()", "arena.New<Node>()"},
		{"Stack<string>{}", "Stack<string>{}"},
		{"a < b > (c)", "a<b>(c)"}, // the documented trap
		{"a < b > c", "((a < b) > c)"},
		{"New<List<T>>()", "New<List<T>>()"}, // the >> is two closers
		{"map[K]Index<entry<K, V>>{}", "map[K]Index<entry<K, V>>{}"},

		// Composite literals, type operands, conversions.
		{"Count{word: k, n: v}", "Count{word: k, n: v}"},
		{"Count{}", "Count{}"},
		{"[]int{5, 3, 8}", "[]int{5, 3, 8}"},
		{"[]Point{{1, 2}, {3, 4}}", "[]Point{{1, 2}, {3, 4}}"},
		{"map[string]int{\"a\": 1}", "map[string]int{\"a\": 1}"},
		{"struct{}{}", "struct{}{}"},
		{"make(chan struct{})", "make(chan struct{})"},
		{"make([]byte, i)", "make([]byte, i)"},
		{"[]byte(s)", "[]byte(s)"},
		{"\"s\" + 'c'", "(\"s\" + 'c')"},

		// Function literals, if and match expressions.
		{"func(a ref Count, b ref Count) bool { return a.n > b.n }", "func(a ref Count, b ref Count) bool {...}"},
		{"func() {}", "func() {...}"},
		{"if ci { a } else { b }", "if ci {...} else {...}"},
		{"if a { x } else if b { y } else { z }", "if a {...} else if b {...} else {...}"},
		{"if x == y { a } else { b }", "if (x == y) {...} else {...}"},               // y { is not a literal here
		{"if x == []int{1} { a } else { b }", "if (x == []int{1}) {...} else {...}"}, // but a written type is
		{"if f(T{}) { a } else { b }", "if f(T{}) {...} else {...}"},
		{"match e { Lit(v) => v }", "match e {...}"},
		{"f(if c { a } else { b })", "f(if c {...} else {...})"},
	}
	for _, tt := range tests {
		x, errs := parseExprString(tt.src)
		if len(errs) > 0 {
			t.Errorf("%q: unexpected errors: %v", tt.src, errs)
			continue
		}

		if got := exprString(x); got != tt.want {
			t.Errorf("%q: parsed as %q, want %q", tt.src, got, tt.want)
		}
	}
}

func TestExprErrors(t *testing.T) {
	tests := []struct{ src, want string }{
		{"", "expected expression, found 'EOF'"},
		{"1 +", "expected expression, found 'EOF'"}, // no semicolon after an operator, as in Go
		{")", "expected expression, found ')'"},
		{"f(a", "expected ')', found 'newline'"},
		{"a[", "expected expression, found 'EOF'"},
		{"T{a: }", "expected expression, found '}'"},
		{"if c { a }", "expected 'else', found 'newline'"},
		{"x.", "expected identifier, found 'EOF'"},
		{"a.b.c<T>{}", "expected '(', found '{'"},
	}
	for _, tt := range tests {
		_, errs := parseExprString(tt.src)
		if len(errs) == 0 || !strings.Contains(errs[0], tt.want) {
			t.Errorf("%q: got %v, want an error containing %q", tt.src, errs, tt.want)
		}
	}
}

// TestParenIsNotTuple checks the one distinction the canonical form
// cannot show: (x) is a parenthesized expression, not a one-tuple.
func TestParenIsNotTuple(t *testing.T) {
	x, _ := parseExprString("(x)")
	if _, ok := x.(*ast.ParenExpr); !ok {
		t.Errorf("(x) parsed as %T, want *ast.ParenExpr", x)
	}

	x, _ = parseExprString("(x, y)")
	if _, ok := x.(*ast.TupleExpr); !ok {
		t.Errorf("(x, y) parsed as %T, want *ast.TupleExpr", x)
	}
}

// parseExprString parses src as one expression and returns it with the
// error messages.
func parseExprString(src string) (ast.Expr, []string) {
	var p Parser
	p.init("test.teak", []byte(src))
	x := p.parseExpr()
	if p.tok != token.EOF && p.tok != token.SEMICOLON {
		p.errorExpected(p.pos, "end of expression")
	}

	var errs []string
	for _, e := range p.errors {
		errs = append(errs, e.Error())
	}

	return x, errs
}

// exprString renders an expression with parentheses around every
// operation, so grouping is visible.
func exprString(x ast.Expr) string {
	switch x := x.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.BasicLit:
		return x.Value
	case *ast.ParenExpr:
		return "(" + exprString(x.X) + ")"
	case *ast.TupleExpr:
		return "(" + exprList(x.Elts) + ")"
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	case *ast.IndexExpr:
		return exprString(x.X) + "[" + exprString(x.Index) + "]"
	case *ast.SliceExpr:
		s := exprString(x.X) + "["
		if x.Low != nil {
			s += exprString(x.Low)
		}

		s += ":"
		if x.High != nil {
			s += exprString(x.High)
		}

		return s + "]"
	case *ast.CallExpr:
		s := exprString(x.Fun)
		if len(x.TypeArgs) > 0 {
			s += "<" + typeList(x.TypeArgs, ", ") + ">"
		}

		return s + "(" + exprList(x.Args) + ")"
	case *ast.UnaryExpr:
		op := x.Op.String()
		if x.Op == token.REF || x.Op == token.MUT {
			op += " "
		}

		return "(" + op + exprString(x.X) + ")"
	case *ast.BinaryExpr:
		return "(" + exprString(x.X) + " " + x.Op.String() + " " + exprString(x.Y) + ")"
	case *ast.CoalesceExpr:
		return "(" + exprString(x.X) + " ?? " + nodeString(x.Else) + ")"
	case *ast.TryExpr:
		return "(" + exprString(x.X) + "?)"
	case *ast.CompositeLit:
		s := ""
		if x.Type != nil {
			s = typeString(x.Type)
		}

		return s + "{" + exprList(x.Elts) + "}"
	case *ast.KeyValueExpr:
		return exprString(x.Key) + ": " + exprString(x.Value)
	case *ast.FuncLit:
		return typeString(x.Type) + " {...}"
	case *ast.IfExpr:
		s := "if " + exprString(x.Cond) + " {...}"
		if x.Else != nil {
			s += " else " + nodeString(x.Else)
		}

		return s
	case *ast.MatchExpr:
		return "match " + exprString(x.X) + " {...}"
	case *ast.TypeExpr:
		return typeString(x.Type)
	}

	return "?"
}

// nodeString renders the else of a ?? or an if expression: an
// expression, a block, or a diverging statement.
func nodeString(n ast.Node) string {
	switch n := n.(type) {
	case ast.Expr:
		return exprString(n)
	case *ast.BlockStmt:
		return "{...}"
	case *ast.ReturnStmt:
		if len(n.Results) == 0 {
			return "return"
		}

		return "return " + exprList(n.Results)
	case *ast.BranchStmt:
		return n.Tok.String()
	}

	return "?"
}

func exprList(list []ast.Expr) string {
	var parts []string
	for _, x := range list {
		parts = append(parts, exprString(x))
	}

	return strings.Join(parts, ", ")
}
