// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
)

// TestMatch parses match expressions and prints them back, one arm per
// semicolon, with patterns in source form and bodies as TestExprs and
// TestStmts print them.
func TestMatch(t *testing.T) {
	tests := []struct{ src, want string }{
		// Patterns, one form each.
		{"match x { _ => a }", "match x { _ => a }"},
		{"match x { n => a }", "match x { n => a }"},
		{"match x { None => a }", "match x { None => a }"},
		{"match x { 1 => a }", "match x { 1 => a }"},
		{"match x { -1 => a }", "match x { -1 => a }"},
		{"match x { -1..=1 => a }", "match x { -1..=1 => a }"},
		{"match x { \"-i\" => a }", "match x { \"-i\" => a }"},
		{"match c { '0'..='9' => a }", "match c { '0'..='9' => a }"},
		{"match c { '0'..='9' | '.' => a }", "match c { '0'..='9' | '.' => a }"},
		{"match c { 'a' | 'b' | 'c' => a }", "match c { 'a' | 'b' | 'c' => a }"},
		{"match x { Some(n) => a }", "match x { Some(n) => a }"},
		{"match x { Bin(Plus, l, r) => a }", "match x { Bin(Plus, l, r) => a }"},
		{"match x { Bin(_, _, _) => a }", "match x { Bin(_, _, _) => a }"},
		{"match x { token.Plus => a }", "match x { token.Plus => a }"},
		{"match x { pkg.Some(n) => a }", "match x { pkg.Some(n) => a }"},
		{"match p { (a, b) => a }", "match p { (a, b) => a }"},
		{"match p { (Some(l), Some(r)) => a }", "match p { (Some(l), Some(r)) => a }"},
		{"match p { (n) => a }", "match p { n => a }"}, // parentheses group, as with types

		// Guards and tuples, from example 17.
		{"match (s, ev) { (Connecting(n, _), Timeout) if n < 3 => Connecting(n + 1, now) }",
			"match (s, ev) { (Connecting(n, _), Timeout) if (n < 3) => Connecting((n + 1), now) }"},
		{"match a { s if s.HasPrefix(\"-\") => return Err(s) }", "match a { s if s.HasPrefix(\"-\") => return Err(s) }"},

		// Arm bodies: expression, block, return, send, assignment.
		{"match x { Stop => return, Deposit(n) => acct.balance += n }", "match x { Stop => return; Deposit(n) => acct.balance += n }"},
		{"match x { Balance(reply) => reply <- acct.balance }", "match x { Balance(reply) => reply <- acct.balance }"},
		{"match x { \"-i\" => o.ignoreCase = true, \"-n\" => o.lineNumbers = true }", "match x { \"-i\" => o.ignoreCase = true; \"-n\" => o.lineNumbers = true }"},
		{"match x { Equal => { n.val = v; return } }", "match x { Equal => { n.val = v; return } }"},
		{"match x { (l, None) => return l, (None, r) => return r }", "match x { (l, None) => return l; (None, r) => return r }"},
		{"match x { _ => return (n, right) }", "match x { _ => return (n, right) }"},
		{"match x { d => results.Push(d ?? break) }", "match x { d => results.Push((d ?? break)) }"},

		// Separators: comma, newline, both, trailing.
		{"match x {\n\t1 => a\n\t2 => b\n}", "match x { 1 => a; 2 => b }"},
		{"match x {\n\t1 => a,\n\t2 => b,\n}", "match x { 1 => a; 2 => b }"},
		{"match x {\n\t1 => { a }\n\t2 => { b },\n}", "match x { 1 => { a }; 2 => { b } }"},
		{"match x { }", "match x { }"},
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

func TestMatchErrors(t *testing.T) {
	tests := []struct {
		src  string
		want string // substring of the first error
		n    int    // total errors expected
	}{
		{"match x { Closed \"c\", _ => a }", "expected '=>', found '\"c\"'", 1},
		{"match x { Some(n => v }", "expected ')' to close the pattern, found '=>'", 1},
		{"match x { 1..= => a }", "expected literal after '..=', found '=>'", 1},
		{"match x { => a }", "expected pattern, found '=>'", 1},
		{"match x { 1 => a b => c }", "expected ',' or newline after the arm, found 'b'", 1},
		{"match x { 1 => a", "expected '}', found 'EOF'", 1},
		{"match x { - => a }", "expected number after '-', found '=>'", 1},
		{"match x {\n\tClosed { x; y },\n\t_ => b,\n}", "expected '=>', found '{'", 1}, // the block is skipped whole
	}
	for _, tt := range tests {
		_, errs := parseExprString(tt.src)
		if len(errs) == 0 || !strings.Contains(errs[0], tt.want) {
			t.Errorf("%q: got %v, want an error containing %q", tt.src, errs, tt.want)
			continue
		}

		if len(errs) != tt.n {
			t.Errorf("%q: %d errors, want %d: %v", tt.src, len(errs), tt.n, errs)
		}
	}
}

// TestPatternKinds checks node kinds where the printed form cannot:
// a bare name, a qualified constant variant, and a wildcard.
func TestPatternKinds(t *testing.T) {
	arms := func(src string) []*ast.MatchArm {
		x, _ := parseExprString(src)
		return x.(*ast.MatchExpr).Arms
	}

	if _, ok := arms("match x { None => a }")[0].Pat.(*ast.IdentPat); !ok {
		t.Error("None should parse as an IdentPat; the checker decides whether it is a variant")
	}

	if _, ok := arms("match x { token.Plus => a }")[0].Pat.(*ast.VariantPat); !ok {
		t.Error("token.Plus should parse as a VariantPat with no arguments")
	}

	if _, ok := arms("match x { _ => a }")[0].Pat.(*ast.WildcardPat); !ok {
		t.Error("_ should parse as a WildcardPat")
	}

	// An expression body is the expression itself, as MatchArm promises,
	// not an ExprStmt around it; a statement body stays a statement.
	if _, ok := arms("match x { _ => a }")[0].Body.(ast.Expr); !ok {
		t.Errorf("expression body parsed as %T, want ast.Expr", arms("match x { _ => a }")[0].Body)
	}

	if _, ok := arms("match x { _ => y = a }")[0].Body.(*ast.AssignStmt); !ok {
		t.Errorf("assignment body parsed as %T, want *ast.AssignStmt", arms("match x { _ => y = a }")[0].Body)
	}
}
