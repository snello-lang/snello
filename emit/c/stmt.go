// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
	"github.com/teak-lang/teak/types"
)

func (e *Emitter) stmts(list []ast.Stmt) {
	for _, s := range list {
		e.stmt(s)
	}
}

func (e *Emitter) block(b *ast.BlockStmt) {
	e.line("{")
	e.stmts(b.List)
	e.line("}")
}

func (e *Emitter) stmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.BlockStmt:
		e.block(s)
	case *ast.DeclStmt:
		e.declStmt(s.Decl)
	case *ast.ExprStmt:
		if x := e.expr(s.X); x != "" {
			e.line(x + ";")
		}
	case *ast.IncDecStmt:
		e.line(e.expr(s.X) + s.Tok.String() + ";")
	case *ast.AssignStmt:
		e.assign(s)
	case *ast.ReturnStmt:
		e.returnStmt(s)
	case *ast.BranchStmt:
		e.line(s.Tok.String() + ";")
	case *ast.IfStmt:
		e.ifStmt(s)
	case *ast.ForStmt:
		e.forStmt(s)
	case *ast.EmptyStmt:
	default:
		e.unsupported(s.Pos(), "this kind of statement")
	}
}

// declStmt writes var x T = v. A name that is never used gets a (void)
// cast, as the C compiler is asked to treat an unused variable as an
// error.
func (e *Emitter) declStmt(d *ast.VarDecl) {
	if len(d.Values) == 1 && len(d.Names) > 1 || len(d.Values) > 1 && len(d.Values) != len(d.Names) {
		e.unsupported(d.Pos(), "tuple declarations")
		return
	}

	for i, name := range d.Names {
		v := e.info.Defs[name]
		decl := e.cType(v.Type(), name.Pos()) + " " + ident(name.Name)
		if i < len(d.Values) {
			decl += " = " + e.valueTo(d.Values[i], v.Type())
		}

		e.line(decl + ";")
		e.discardUnused(name)
	}
}

// discardUnused writes (void)x for a declared name nothing refers to.
func (e *Emitter) discardUnused(name *ast.Ident) {
	if name.Name == "_" {
		return
	}

	if obj := e.info.Defs[name]; obj != nil && !e.used[obj] {
		e.line("(void)" + ident(name.Name) + ";")
	}
}

func (e *Emitter) assign(s *ast.AssignStmt) {
	if len(s.Lhs) != 1 || len(s.Rhs) != 1 {
		e.assignMany(s)
		return
	}

	lhs, rhs := s.Lhs[0], s.Rhs[0]
	switch s.Tok {
	case token.DEFINE:
		ident, ok := lhs.(*ast.Ident)
		if !ok {
			return
		}

		if obj, declared := e.info.Defs[ident]; declared {
			e.line(e.cType(obj.Type(), ident.Pos()) + " " + e.expr(ident) + " = " + e.valueTo(rhs, obj.Type()) + ";")
			e.discardUnused(ident)
			return
		}

		e.line(e.expr(ident) + " = " + e.valueTo(rhs, e.info.Types[ident]) + ";") // x already declared: an assignment
	case token.ASSIGN:
		if isBlank(lhs) {
			e.line("(void)(" + e.expr(rhs) + ");")
			return
		}

		e.line(e.expr(lhs) + " = " + e.valueTo(rhs, e.info.Types[lhs]) + ";")
	default:
		t := readType(e.info.Types[lhs])
		if isString(t) { // s += t has no C operator
			e.line(e.expr(lhs) + " = tk_concat(" + e.expr(lhs) + ", " + e.valueTo(rhs, t) + ");")
			return
		}

		e.line(e.expr(lhs) + " " + s.Tok.String() + " " + e.valueTo(rhs, t) + ";")
	}
}

// assignMany writes a, b := x, y or a, b = y, x: every right side goes
// into a temporary before any left side changes, as Teak and Go
// require.
func (e *Emitter) assignMany(s *ast.AssignStmt) {
	if len(s.Lhs) != len(s.Rhs) || s.Tok != token.DEFINE && s.Tok != token.ASSIGN {
		e.unsupported(s.TokPos, "this assignment of several values")
		return
	}

	temps := make([]string, len(s.Rhs))
	for i, rhs := range s.Rhs {
		t := e.info.Types[s.Lhs[i]]
		if s.Tok == token.DEFINE {
			if obj, declared := e.info.Defs[s.Lhs[i].(*ast.Ident)]; declared {
				t = obj.Type()
			}
		}

		temps[i] = e.temp()
		e.line(e.cType(t, rhs.Pos()) + " " + temps[i] + " = " + e.valueTo(rhs, t) + ";")
	}

	for i, lhs := range s.Lhs {
		if isBlank(lhs) {
			e.line("(void)" + temps[i] + ";")
			continue
		}

		ident := lhs.(*ast.Ident)
		if obj, declared := e.info.Defs[ident]; s.Tok == token.DEFINE && declared {
			e.line(e.cType(obj.Type(), ident.Pos()) + " " + e.expr(ident) + " = " + temps[i] + ";")
			e.discardUnused(ident)
			continue
		}

		e.line(e.expr(lhs) + " = " + temps[i] + ";")
	}
}

func (e *Emitter) returnStmt(s *ast.ReturnStmt) {
	switch len(s.Results) {
	case 0:
		e.line("return;")
	case 1:
		e.line("return " + e.valueTo(s.Results[0], e.result) + ";")
	default:
		e.unsupported(s.Return, "returning several values")
	}
}

// ifStmt writes an if, with an init statement in a block of its own so
// its names end with the if, as they do in Teak.
func (e *Emitter) ifStmt(s *ast.IfStmt) {
	if s.Init != nil {
		e.line("{")
		e.stmt(s.Init)
	}

	e.line("if " + e.cond(s.Cond) + " {")
	e.stmts(s.Body.List)
	switch els := s.Else.(type) {
	case nil:
		e.line("}")
	case *ast.BlockStmt:
		e.line("} else {")
		e.stmts(els.List)
		e.line("}")
	default:
		e.line("} else {")
		e.stmt(els)
		e.line("}")
	}

	if s.Init != nil {
		e.line("}")
	}
}

// forStmt writes Teak's three loop forms. A condition that needs
// statements of its own is tested at the top of the body.
func (e *Emitter) forStmt(s *ast.ForStmt) {
	e.line("{")
	if s.Init != nil {
		e.stmt(s.Init)
	}

	cond := ""
	if s.Cond != nil && !needsHoist(s.Cond) {
		cond = e.expr(s.Cond)
	}

	post := ""
	if s.Post != nil {
		var ok bool
		if post, ok = e.simpleStmt(s.Post); !ok {
			e.unsupported(s.Post.Pos(), "this kind of statement after the second ; of a for")
		}
	}

	e.line("for (; " + cond + "; " + post + ") {")
	if s.Cond != nil && cond == "" {
		e.line("if (!" + e.cond(s.Cond) + ") { break; }")
	}

	e.stmts(s.Body.List)
	e.line("}")
	e.line("}")
}

// simpleStmt writes a statement that C accepts as an expression, for
// the post clause of a for: x++, x += 1, x = e.
func (e *Emitter) simpleStmt(s ast.Stmt) (string, bool) {
	switch s := s.(type) {
	case *ast.IncDecStmt:
		return e.expr(s.X) + s.Tok.String(), true
	case *ast.AssignStmt:
		if len(s.Lhs) != 1 || len(s.Rhs) != 1 || s.Tok == token.DEFINE {
			return "", false
		}

		t := readType(e.info.Types[s.Lhs[0]])
		if s.Tok == token.ASSIGN {
			return e.expr(s.Lhs[0]) + " = " + e.valueTo(s.Rhs[0], t), true
		}

		if isString(t) {
			return "", false
		}

		return e.expr(s.Lhs[0]) + " " + s.Tok.String() + " " + e.valueTo(s.Rhs[0], t), true
	}

	return "", false
}

// cond writes a condition in exactly one pair of parentheses, as the C
// compiler warns about a second pair around a comparison.
func (e *Emitter) cond(x ast.Expr) string {
	s := e.expr(x)
	if len(s) > 1 && s[0] == '(' && s[len(s)-1] == ')' && balanced(s[1:len(s)-1]) {
		return s
	}

	return "(" + s + ")"
}

// balanced reports whether s has matching parentheses throughout, so
// that an outer pair is one pair and not two expressions side by side.
func balanced(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}

	return depth == 0
}

func isBlank(x ast.Expr) bool {
	id, ok := x.(*ast.Ident)
	return ok && id.Name == "_"
}

func isString(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Kind() == types.String
}
