// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ast

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/token"
)

// TestClone copies a body: the copy prints the same and shares no node
// with the original, so a change to one leaves the other as it was.
func TestClone(t *testing.T) {
	name := &Ident{NamePos: 5, Name: "x"}
	body := &BlockStmt{Lbrace: 1, List: []Stmt{
		&ExprStmt{X: &CallExpr{Fun: &Ident{NamePos: 2, Name: "print"}, Args: []Expr{name, &BasicLit{ValuePos: 7, Kind: token.INT, Value: "1"}}}},
	}}

	c := Clone(body).(*BlockStmt)
	call := c.List[0].(*ExprStmt).X.(*CallExpr)
	if Source(call) != "print(x, 1)" || call.Args[0].Pos() != 5 {
		t.Fatalf("the copy is %q at %d, want print(x, 1) at 5", Source(call), call.Args[0].Pos())
	}

	if call.Args[0] == Expr(name) || c == body {
		t.Fatal("the copy shares a node with the original")
	}

	call.Args[0].(*Ident).Name = "y"
	if name.Name != "x" {
		t.Errorf("changing the copy changed the original: %s", name.Name)
	}

	if Clone(nil) != nil {
		t.Error("the copy of nothing is not nothing")
	}
}

// TestRewrite replaces expressions wherever they stand under a node,
// inside a statement, an argument list and a nested call, and leaves
// the rest as it was.
func TestRewrite(t *testing.T) {
	body := &BlockStmt{List: []Stmt{
		&ExprStmt{X: &CallExpr{Fun: &Ident{Name: "f"}, Args: []Expr{&Ident{Name: "a"}, &CallExpr{Fun: &Ident{Name: "g"}, Args: []Expr{&Ident{Name: "a"}}}}}},
		&ReturnStmt{Results: []Expr{&Ident{Name: "a"}}},
	}}

	Rewrite(body, func(x Expr) Expr {
		if id, ok := x.(*Ident); ok && id.Name == "a" {
			return &Ident{Name: "b"}
		}

		return x
	})

	if got := Source(body.List[0].(*ExprStmt).X); got != "f(b, g(b))" {
		t.Errorf("the call is %s, want f(b, g(b))", got)
	}

	if got := Source(body.List[1].(*ReturnStmt).Results[0]); got != "b" {
		t.Errorf("the result is %s, want b", got)
	}
}

// TestInspect visits every node in written order, and leaves the insides
// of a node unvisited when told to.
func TestInspect(t *testing.T) {
	call := &CallExpr{Fun: &Ident{Name: "f"}, Args: []Expr{&Ident{Name: "a"}, &CallExpr{Fun: &Ident{Name: "g"}, Args: []Expr{&Ident{Name: "b"}}}}}
	var seen []string
	Inspect(call, func(n Node) bool {
		if id, ok := n.(*Ident); ok {
			seen = append(seen, id.Name)
		}

		return true
	})

	if got := strings.Join(seen, " "); got != "f a g b" {
		t.Errorf("visited %q, want f a g b", got)
	}

	seen = nil
	Inspect(call, func(n Node) bool {
		if id, ok := n.(*Ident); ok {
			seen = append(seen, id.Name)
		}

		inner, ok := n.(*CallExpr)
		return !ok || inner == call // the inner call's insides are left
	})

	if got := strings.Join(seen, " "); got != "f a" {
		t.Errorf("visited %q, want f a", got)
	}
}
