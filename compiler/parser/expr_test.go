// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
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
		{"f(xs...)", "f(xs...)"}, // a slice passed as the variadic parameter
		{"f(a, ref xs...,)", "f(a, (ref xs)...)"},
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
		{"()", "()"},
		{"make(chan ())", "make(chan ())"},
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
		{"match e { Lit(v) => v }", "match e { Lit(v) => v }"},
		{"match { x > 0 => a\nx < 0 => b\n_ => c }", "match { (x > 0) => a; (x < 0) => b; _ => c }"}, // a match with no value: each arm a condition
		{"match { ok(x) => return, _ => {} }", "match { ok(x) => return; _ => {  } }"},
		{"match { x == []int{1} => a, _ => b }", "match { (x == []int{1}) => a; _ => b }"},                                           // a literal in a condition, inside the braces
		{"if match { T{a: 1}.a == x => c, _ => d } { a } else { b }", "if match { (T{a: 1}.a == x) => c; _ => d } {...} else {...}"}, // the arms are a body of their own, even in a control clause
		{"match match { T{a: 1}.a == x => 1, _ => 2 } { 1 => a, _ => b }", "match match { (T{a: 1}.a == x) => 1; _ => 2 } { 1 => a; _ => b }"},
		{"f(if c { a } else { b })", "f(if c {...} else {...})"},
		{"Self.Count() * 2", "(Self.Count() * 2)"}, // a function of the implementing type, in a trait's default body
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
		{"f(xs..., b)", "can only use ... with the final argument"},
		{"a[", "expected expression, found 'EOF'"},
		{"T{a: }", "expected expression, found '}'"},
		{"if c { a }", "expected 'else', found 'newline'"},
		{"x.", "expected identifier, found 'EOF'"},
		{"a.b.c<T>{}", "expected '(', found '{'"},
		{"match { x > 0 if y => a, _ => b }", "an arm of a match with no value is its condition alone: join two with &&"},
		{"match { x > 0 a, _ => b }", "expected '=>', found 'a'"},
		{"switch x { case 1: a }", "Snello has no switch: use match"}, // where an expression is wanted too
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
	p.init(token.NewFile("test.snello", len(src)), []byte(src))
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

		if x.Ellipsis.IsValid() {
			return s + "(" + exprList(x.Args) + "...)"
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
	case *ast.PartExpr:
		return exprString(x.X) + ".[" + x.Part.Name + "]"
	case *ast.PartsElt:
		key := ""
		if x.Key != nil {
			key = "[" + x.Key.Name + "]: "
		}

		return "for " + x.Var.Name + " := range " + exprString(x.X) + " { " + key + exprString(x.Value) + " }"
	case *ast.FuncLit:
		return typeString(x.Type) + " {...}"
	case *ast.IfExpr:
		s := "if " + exprString(x.Cond) + " {...}"
		if x.Else != nil {
			s += " else " + nodeString(x.Else)
		}

		return s
	case *ast.MatchExpr:
		if x.Conds {
			var arms []string
			for _, arm := range x.Arms {
				head := "_"
				if arm.Guard != nil {
					head = exprString(arm.Guard)
				}

				arms = append(arms, head+" => "+armBodyString(arm.Body))
			}

			return "match { " + strings.Join(arms, "; ") + " }"
		}

		if len(x.Arms) == 0 {
			return "match " + exprString(x.X) + " { }"
		}

		var arms []string
		for _, arm := range x.Arms {
			s := patString(arm.Pat)
			if arm.Guard != nil {
				s += " if " + exprString(arm.Guard)
			}

			arms = append(arms, s+" => "+armBodyString(arm.Body))
		}

		return "match " + exprString(x.X) + " { " + strings.Join(arms, "; ") + " }"
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

// TestReflection parses the forms a function reflecting on its type
// parameter writes: a match on a type parameter, of the function or of
// its receiver's type, whose arms are types, shapes and kinds; a part
// reached through a loop, x.[f]; and a literal element written once for
// each part. A match on a value is unchanged, outside a generic
// function and on a name that is no type parameter.
func TestReflection(t *testing.T) {
	tests := []struct{ src, want string }{
		{"func F<T>(v ref T) { x := match T { bool => 1, i8 | int => 2, []E => 3, map[K]V => 4, E? => 5, struct => 6, enum => 7, _ => 9 } }",
			"match T { bool => 1; i8 | int => 2; []E => 3; map[K]V => 4; (E)? => 5; struct => 6; enum => 7; _ => 9 }"},
		{"func (b ref Box<T>) F() { x := match T { int => 1, _ => 2 } }", "match T { int => 1; _ => 2 }"}, // the receiver's type parameter
		{"func Box<T>.K() { x := match T { []E => 1, _ => 2 } }", "match T { []E => 1; _ => 2 }"},         // the type parameter of the type a function belongs to
		{"func F<T>(v ref T) { x := v.[f] }", "v.[f]"},
		{"func F<T>(v ref T) { x := v.[f].[g] }", "v.[f].[g]"},
		{"func F<T>() T { x := T{ for f := range fields(T) { [f]: F() } } }", "T{for f := range fields(T) { [f]: F() }}"},
		{"func F<T>() T { x := [N]E{ for i := range N { F() } } }", "[N]E{for i := range N { F() }}"},
		{"func F(T int) { x := match T { 1 => 2, _ => 3 } }", "match T { 1 => 2; _ => 3 }"},                                                         // a value named T, no type parameter
		{"func F<T>() { x := match T { []E => match E { int => 1, _ => 2 }, _ => 3 } }", "match T { []E => match E { int => 1; _ => 2 }; _ => 3 }"}, // a shape's name is matched on as a type
		{"func F<T>() { x := match f.type { int => 1, _ => 2 } }", "match f.type { int => 1; _ => 2 }"},                                             // a field's type
		{"func F<T>() { x := read<f.type>(r) }", "read<f.type>(r)"},
		{"func F<T>() { x := f.default ?? 1 }", "(f.default ?? 1)"},                                                                          // a field's default
		{"func F<T>(v ref T) { x := match v { for c := range variants(T) => c.name } }", "match v { for c := range variants(T) => c.name }"}, // an arm written once per variant
		{"func F<T>() T { x := c(for f := range fields(c) { F() }) }", "c(for f := range fields(c) { F() })"},                                // a variant built from its fields
	}
	for _, tt := range tests {
		f, _, err := ParseFile("test.snello", []byte("package p\n"+tt.src))
		if err != nil {
			t.Errorf("%s: %v", tt.src, err)
			continue
		}

		body := f.Decls[0].(*ast.FuncDecl).Body
		got := exprString(body.List[0].(*ast.AssignStmt).Rhs[0])
		if got != tt.want {
			t.Errorf("%s:\n  got  %s\n  want %s", tt.src, got, tt.want)
		}
	}

	errs := []struct{ src, want string }{
		{"func F<T>() { x := match T { int if true => 1, _ => 2 } }", "an arm of a match on a type takes no guard"},
		{"func F<T>(v T) { x := v.[1] }", "expected identifier, found '1'"},
		{"func Box<T>.K() { x := match U { []E => 1, _ => 2 } }", "expected"}, // U is no type parameter, so its arms are values
		{"trait D { func (s ref Self) D() int { return match Self { struct => 1, _ => 2 } } }", "a match on a type is on a type parameter by its name, and Self is none"},
	}
	for _, tt := range errs {
		_, _, err := ParseFile("test.snello", []byte("package p\n"+tt.src))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: got %v, want an error containing %q", tt.src, err, tt.want)
		}
	}
}
