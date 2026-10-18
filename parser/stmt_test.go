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

// TestStmts parses each statement and prints it back in canonical
// form, with every expression parenthesized as in TestExprs and the
// statements of a block separated by semicolons.
func TestStmts(t *testing.T) {
	tests := []struct{ src, want string }{
		// Simple statements.
		{"f(x)", "f(x)"},
		{"x := f()", "x := f()"},
		{"a, b := x, y", "a, b := x, y"},
		{"x = 1", "x = 1"},
		{"n += 1", "n += 1"},
		{"i++", "i++"},
		{"out <- r", "out <- r"},
		{"*cur = Some(n)", "(*cur) = Some(n)"},
		{"var x int", "var x int"},
		{"var x, y = 1, 2", "var x, y = 1, 2"},
		{"spawn worker(id, tasks)", "spawn worker(id, tasks)"},
		{"spawn func() { x }()", "spawn func() {...}()"},
		{"defer f.Close()", "defer f.Close()"},
		{"return", "return"},
		{"return a, b", "return a, b"},
		{"return match e { }", "return match e {...}"},
		{"break", "break"},
		{"continue", "continue"},
		{"{ a; b }", "{ a; b }"},
		{"match e { }", "match e {...}"},
		{"n := take(mut s.head) ?? return None", "n := (take((mut s.head)) ?? return None)"},

		// if: Go's forms and Teak's binding.
		{"if x > 0 { a }", "if (x > 0) { a }"},
		{"if v := f(); v > 0 { a }", "if v := f(); (v > 0) { a }"},
		{"if x := opt { a }", "if x := opt { a }"},
		{"if (k, v) := line.SplitOnce(\"=\") { a }", "if (k, v) := line.SplitOnce(\"=\") { a }"},
		{"if Some(n) := x { a }", "if Some(n) := x { a }"},
		{"if _ := x { a }", "if _ := x { a }"},
		{"if a { x } else { y }", "if a { x } else { y }"},
		{"if a { x } else if b { y } else { z }", "if a { x } else if b { y } else { z }"},
		{"if x := opt { a } else { b }", "if x := opt { a } else { b }"},
		{"if x == y { a }", "if (x == y) { a }"},
		{"if f(T{}) { a }", "if f(T{}) { a }"},

		// for: every form.
		{"for { a }", "for { a }"},
		{"for cur != None { a }", "for (cur != None) { a }"},
		{"for i := 0; i < n; i++ { a }", "for i := 0; (i < n); i++ { a }"},
		{"for ; i < n; { a }", "for (i < n) { a }"}, // the same loop as without the semicolons
		{"for v := it.Next(); v != None; v = it.Next() { a }", "for v := it.Next(); (v != None); v = it.Next() { a }"},
		{"for i := range xs { a }", "for i := range xs { a }"},
		{"for _, x := range xs { a }", "for _, x := range xs { a }"},
		{"for k, v := range m { a }", "for k, v := range m { a }"},
		{"for x := range ch { a }", "for x := range ch { a }"},
		{"for _, f := range ref o.files { a }", "for _, f := range (ref o.files) { a }"},
		{"for _, k := range []int{5, 3} { a }", "for _, k := range []int{5, 3} { a }"},
		{"for i = range xs { a }", "for i = range xs { a }"},
		{"for range n { a }", "for range n { a }"},

		// switch and select.
		{"switch x { case 1, 2: a\ncase 3: b\ndefault: c }", "switch x { case 1, 2: a; case 3: b; default: c }"},
		{"switch { case x > 0: a }", "switch { case (x > 0): a }"},
		{"switch v := f(); v { case 1: a }", "switch v := f(); v { case 1: a }"},
		{"select { case out <- r: a\ncase <-cancel.Done(): b\ncase t := <-tasks: c\ndefault: d }", "select { case out <- r: a; case (<-cancel.Done()): b; case t := (<-tasks): c; default: d }"},
		{"select { case <-timer: }", "select { case (<-timer): }"},
	}
	for _, tt := range tests {
		s, errs := parseStmtString(tt.src)
		if len(errs) > 0 {
			t.Errorf("%q: unexpected errors: %v", tt.src, errs)
			continue
		}

		if got := stmtString(s); got != tt.want {
			t.Errorf("%q: parsed as %q, want %q", tt.src, got, tt.want)
		}
	}
}

func TestStmtErrors(t *testing.T) {
	tests := []struct {
		src  string
		want string // substring of the first error
		n    int    // total errors expected
	}{
		{"spawn x", "expression in spawn must be a function call", 1},
		{"defer 1 + 2", "expression in defer must be a function call", 1},
		{"a, b", "expected ':=' or '=', found 'newline'", 1},
		{"if { a }", "expected condition, found '{'", 1},
		{"if x := y; { a }", "expected expression, found '{'", 1},
		{"if a { x } else y", "expected 'if' or '{', found 'y'", 2}, // cascade: y is then not a statement end
		{"if a + b := x { y }", "expected a name, a tuple or a variant pattern before ':='", 1},
		{"if x, y := f() { a }", "an if binding has one pattern and one value", 1},
		{"for a, b, c := range xs { }", "range allows at most two variables", 1},
		{"for i += range xs { }", "'range' needs ':=' or '='", 1},
		{"for i := 0; i < n { }", "expected ';', found '{'", 1},
		{"x := 1 y := 2", "expected ';' or newline, found 'y'", 1},
		{"{ a; ) ; b }", "expected statement, found ')'", 1}, // the line is skipped; b still parses
		{"switch x { case 1 a }", "expected ':', found 'a'", 1},
	}
	for _, tt := range tests {
		_, errs := parseStmtString(tt.src)
		if len(errs) == 0 || !strings.Contains(errs[0], tt.want) {
			t.Errorf("%q: got %v, want an error containing %q", tt.src, errs, tt.want)
			continue
		}

		if len(errs) != tt.n {
			t.Errorf("%q: %d errors, want %d: %v", tt.src, len(errs), tt.n, errs)
		}
	}
}

// TestRecovery checks that a broken statement does not take the rest
// of the block with it: neither one that cannot start, nor one that
// does not end where it should.
func TestRecovery(t *testing.T) {
	tests := []struct{ src, want string }{
		{"{ a; ) ; b }", "{ a; ?; b }"},
		{"{ x := 1 y := 2\nb }", "{ x := 1; b }"},
	}
	for _, tt := range tests {
		s, errs := parseStmtString(tt.src)
		if len(errs) != 1 {
			t.Errorf("%q: got %v, want one error", tt.src, errs)
			continue
		}

		if got := stmtString(s); got != tt.want {
			t.Errorf("%q: parsed as %q, want %q", tt.src, got, tt.want)
		}
	}
}

// TestBindPatterns checks the node kinds an if binding produces, since
// _ prints the same whether it is a name or a wildcard.
func TestBindPatterns(t *testing.T) {
	s, _ := parseStmtString("if _ := x { a }")
	if _, ok := s.(*ast.IfBindStmt).Pat.(*ast.WildcardPat); !ok {
		t.Errorf("_ parsed as %T, want *ast.WildcardPat", s.(*ast.IfBindStmt).Pat)
	}

	s, _ = parseStmtString("if v := x { a }")
	if _, ok := s.(*ast.IfBindStmt).Pat.(*ast.IdentPat); !ok {
		t.Errorf("v parsed as %T, want *ast.IdentPat", s.(*ast.IfBindStmt).Pat)
	}
}

// parseStmtString parses src as one statement and returns it with the
// error messages.
func parseStmtString(src string) (ast.Stmt, []string) {
	var p Parser
	p.init("test.teak", []byte(src))
	s := p.parseStmt()
	if p.tok != token.EOF && p.tok != token.SEMICOLON {
		p.errorExpected(p.pos, "';' or newline")
	}

	var errs []string
	for _, e := range p.errors {
		errs = append(errs, e.Error())
	}

	return s, errs
}

// stmtString renders a statement in canonical form.
func stmtString(s ast.Stmt) string {
	switch s := s.(type) {
	case *ast.BlockStmt:
		return "{ " + stmtList(s.List) + " }"
	case *ast.DeclStmt:
		return declString(s.Decl)
	case *ast.EmptyStmt:
		return ";"
	case *ast.ExprStmt:
		return exprString(s.X)
	case *ast.SendStmt:
		return exprString(s.Chan) + " <- " + exprString(s.Value)
	case *ast.IncDecStmt:
		return exprString(s.X) + s.Tok.String()
	case *ast.AssignStmt:
		return exprList(s.Lhs) + " " + s.Tok.String() + " " + exprList(s.Rhs)
	case *ast.SpawnStmt:
		return "spawn " + exprString(s.Call)
	case *ast.DeferStmt:
		return "defer " + exprString(s.Call)
	case *ast.ReturnStmt:
		if len(s.Results) == 0 {
			return "return"
		}

		return "return " + exprList(s.Results)
	case *ast.BranchStmt:
		return s.Tok.String()
	case *ast.IfStmt:
		out := "if "
		if s.Init != nil {
			out += stmtString(s.Init) + "; "
		}

		return out + exprString(s.Cond) + " " + stmtString(s.Body) + elseString(s.Else)
	case *ast.IfBindStmt:
		return "if " + patString(s.Pat) + " := " + exprString(s.Value) + " " + stmtString(s.Body) + elseString(s.Else)
	case *ast.ForStmt:
		out := "for "
		if s.Init != nil || s.Post != nil {
			out += optStmt(s.Init) + "; " + optExpr(s.Cond) + "; " + optStmt(s.Post) + " "
		} else if s.Cond != nil {
			out += exprString(s.Cond) + " "
		}

		return out + stmtString(s.Body)
	case *ast.RangeStmt:
		out := "for "
		if s.Key != nil {
			out += exprString(s.Key)
			if s.Value != nil {
				out += ", " + exprString(s.Value)
			}

			out += " " + s.Tok.String() + " "
		}

		return out + "range " + exprString(s.X) + " " + stmtString(s.Body)
	case *ast.SwitchStmt:
		out := "switch "
		if s.Init != nil {
			out += stmtString(s.Init) + "; "
		}

		if s.Tag != nil {
			out += exprString(s.Tag) + " "
		}

		var clauses []string
		for _, c := range s.Body {
			head := "default"
			if c.List != nil {
				head = "case " + exprList(c.List)
			}

			clauses = append(clauses, clauseString(head, c.Body))
		}

		return out + "{ " + strings.Join(clauses, "; ") + " }"
	case *ast.SelectStmt:
		var clauses []string
		for _, c := range s.Body {
			head := "default"
			if c.Comm != nil {
				head = "case " + stmtString(c.Comm)
			}

			clauses = append(clauses, clauseString(head, c.Body))
		}

		return "select { " + strings.Join(clauses, "; ") + " }"
	}

	return "?"
}

func clauseString(head string, body []ast.Stmt) string {
	if len(body) == 0 {
		return head + ":"
	}

	return head + ": " + stmtList(body)
}

func stmtList(list []ast.Stmt) string {
	var parts []string
	for _, s := range list {
		parts = append(parts, stmtString(s))
	}

	return strings.Join(parts, "; ")
}

func elseString(s ast.Stmt) string {
	if s == nil {
		return ""
	}

	return " else " + stmtString(s)
}

func optStmt(s ast.Stmt) string {
	if s == nil {
		return ""
	}

	return stmtString(s)
}

func optExpr(x ast.Expr) string {
	if x == nil {
		return ""
	}

	return exprString(x)
}

// patString renders the patterns an if binding can use.
func patString(pat ast.Pattern) string {
	switch pat := pat.(type) {
	case *ast.IdentPat:
		return pat.Name.Name
	case *ast.WildcardPat:
		return "_"
	case *ast.TuplePat:
		var parts []string
		for _, elt := range pat.Elts {
			parts = append(parts, patString(elt))
		}

		return "(" + strings.Join(parts, ", ") + ")"
	case *ast.VariantPat:
		var parts []string
		for _, arg := range pat.Args {
			parts = append(parts, patString(arg))
		}

		return exprString(pat.Path) + "(" + strings.Join(parts, ", ") + ")"
	}

	return "?"
}
