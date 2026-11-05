// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// A match is what every arm of one match shares: the address and type
// of the value looked into, the slot the arms store into and its type,
// the block after the last arm, and whether the scrutinee is taken
// apart, so that a binding by value empties its part.
type match struct {
	place      string
	typ        types.Type
	result     string // empty when the match yields no value
	resultType types.Type
	end        string
	consume    bool
}

// matchExpr writes a match as a chain of tests on the scrutinee's
// place. Each arm tests its pattern, branching to the next arm where a
// test fails, binds its names, tests its guard, runs its body and jumps
// to the end. The checker proved an arm matches every value, so the
// panic after the last arm is never reached.
func (e *Emitter) matchExpr(x *ast.MatchExpr) value {
	m := match{resultType: e.typeOf(x)}
	if yields(m.resultType) {
		m.result = e.slot(e.irType(m.resultType, x.Pos()))
	}

	var held string
	m.place, m.typ, held = e.consumed(x.X)
	m.consume = held != ""
	m.end = e.block("match.end")
	for _, arm := range x.Arms {
		e.arm(&m, arm)
	}

	if held != "" {
		e.hold(held, e.typeOf(x.X)) // what no arm took is dropped when the statement ends
	}

	e.panic(e.constString("no match arm matched"))
	e.label(m.end)
	if m.result == "" {
		return unit
	}

	return e.load(e.irType(m.resultType, x.Pos()), m.result)
}

func (e *Emitter) arm(m *match, arm *ast.MatchArm) {
	next := e.block("arm.next")
	e.patternTest(arm.Pat, m.place, m.typ, next)
	e.push(false)
	taken := e.patternBinds(arm.Pat, m.place, m.typ)
	if arm.Guard != nil {
		body := e.block("arm.body")
		e.branch(e.scopedBool(arm.Guard).s, body, next) // a failed guard leaves the bindings as copies the scrutinee still owns
		e.label(body)
	}

	if m.consume {
		e.empty(taken, arm.Pos()) // the parts bound by value left the scrutinee, once the arm is known to run
	}

	e.node(arm.Body, m.result, m.resultType)
	if !e.fn.terminated {
		e.pop(arm.Pos())
		e.jump(m.end)
	} else {
		e.fn.frames = e.fn.frames[:len(e.fn.frames)-1]
	}

	e.label(next)
}

// empty writes the emptying of places whose values were bound by
// value.
func (e *Emitter) empty(taken []owned, pos token.Pos) {
	for _, t := range taken {
		e.zero(t.slot, t.typ, pos)
	}
}

// ifBind writes if pat := value {: the body runs when the option holds
// a value the pattern matches, with the pattern's names bound.
func (e *Emitter) ifBind(s *ast.IfBindStmt) {
	p, t, held := e.consumed(s.Value)
	if held != "" {
		e.hold(held, e.typeOf(s.Value))
	}

	u := t.Underlying().(*types.Option)
	has, val := e.optionParts(e.irType(t, s.Pos()), p, u)
	then, els, end := e.block("bind"), e.block("else"), e.block("end")
	e.branch(has.s, then, els)
	e.label(then)
	e.patternTest(s.Pat, val, u.Elem(), els)
	e.push(false)
	taken := e.patternBinds(s.Pat, val, u.Elem())
	if held != "" {
		e.empty(taken, s.Pos())
	}

	e.stmts(s.Body.List)
	if !e.fn.terminated {
		e.pop(s.Body.Rbrace)
		e.jump(end)
	} else {
		e.fn.frames = e.fn.frames[:len(e.fn.frames)-1]
	}

	e.label(els)
	if s.Else != nil {
		e.stmt(s.Else)
	}

	if !e.fn.terminated {
		e.jump(end)
	}

	e.label(end)
}

// patternTest writes the tests under which a pattern matches the value
// at the address p, of type t, branching to fail where one does not and
// continuing in a new block where all pass.
func (e *Emitter) patternTest(pat ast.Pattern, p string, t types.Type, fail string) {
	test := func(c value) {
		ok := e.block("pat")
		e.branch(c.s, ok, fail)
		e.label(ok)
	}

	switch pat := pat.(type) {
	case *ast.IdentPat:
		switch obj := e.info.Uses[pat.Name].(type) {
		case *types.Variant:
			test(e.tagTest(p, t, obj))
		case *types.Const:
			v := e.load("i1", p)
			if obj.Name() == "false" {
				v = e.op("xor i1 "+v.s+", true", "i1")
			}

			test(v)
		}
	case *ast.LitPat:
		test(e.literalTest(p, t, pat.Lit))
	case *ast.RangePat:
		ir := e.irType(t, pat.Lo.Pos())
		v := e.load(ir, p)
		ge, _ := compare(token.GEQ, isFloat(t), isUnsigned(t))
		le, _ := compare(token.LEQ, isFloat(t), isUnsigned(t))
		cmp := "icmp"
		if isFloat(t) {
			cmp = "fcmp"
		}

		test(e.op(cmp+" "+ge+" "+v.typed()+", "+e.literalOf(pat.Lo, t).s, "i1"))
		test(e.op(cmp+" "+le+" "+v.typed()+", "+e.literalOf(pat.Hi, t).s, "i1"))
	case *ast.TuplePat:
		for i, elt := range pat.Elts {
			sub, st := e.tupleElem(p, t, i)
			e.patternTest(elt, sub, st, fail)
		}
	case *ast.VariantPat:
		v := variantOf(t, e.info.Uses[pathName(pat.Path)].(*types.Variant))
		test(e.tagTest(p, t, v))
		for i, arg := range pat.Args {
			sub, ft := e.payload(p, t, v, i)
			e.patternTest(arg, sub, ft, fail)
		}
	case *ast.OrPat:
		matched := e.block("alt.matched")
		for i, alt := range pat.Alts {
			next := fail
			if i < len(pat.Alts)-1 {
				next = e.block("alt")
			}

			e.patternTest(alt, p, t, next)
			e.jump(matched)
			if next != fail {
				e.label(next)
			}
		}

		e.label(matched)
	}
}

// patternBinds declares the names a pattern binds, each to the part
// of the value at the address p it stands for: a copy, or through a
// mut borrow its address. It returns the parts bound by value that own
// something, which a consuming match empties once the arm runs.
func (e *Emitter) patternBinds(pat ast.Pattern, p string, t types.Type) []owned {
	switch pat := pat.(type) {
	case *ast.IdentPat:
		v, ok := e.info.Defs[pat.Name].(*types.Var)
		if !ok {
			return nil
		}

		vt := e.sub(v.Type())
		slot := e.local(v)
		if depth(vt) > depth(t) { // a mut binding of a value: its address
			e.store(value{"ptr", p}, slot)
		} else {
			e.store(e.load(e.irType(vt, pat.Pos()), p), slot)
		}

		e.own(slot, vt)
		if _, borrow := vt.(*types.Ref); !borrow && needsDrop(vt) {
			return []owned{{p, vt}}
		}
	case *ast.TuplePat:
		var taken []owned
		for i, elt := range pat.Elts {
			sub, st := e.tupleElem(p, t, i)
			taken = append(taken, e.patternBinds(elt, sub, st)...)
		}

		return taken
	case *ast.VariantPat:
		var taken []owned
		v := variantOf(t, e.info.Uses[pathName(pat.Path)].(*types.Variant))
		for i, arg := range pat.Args {
			sub, ft := e.payload(p, t, v, i)
			taken = append(taken, e.patternBinds(arg, sub, ft)...)
		}

		return taken
	}

	return nil
}

// tagTest writes the test that the value at the address p was built
// by v.
func (e *Emitter) tagTest(p string, t types.Type, v *types.Variant) value {
	ir := e.irType(t, token.NoPos)
	switch u := t.Underlying().(type) {
	case *types.Option:
		has, _ := e.optionParts(ir, p, u)
		if v == types.SomeVariant {
			return has
		}

		return e.op("xor i1 "+has.s+", true", "i1")
	case *types.Result:
		ok := e.load("i1", e.gep(ir, p, 0))
		if v == types.OkVariant {
			return ok
		}

		return e.op("xor i1 "+ok.s+", true", "i1")
	}

	tag := e.load("i32", e.gep(ir, p, 0))
	return e.op("icmp eq "+tag.typed()+", "+strconv.Itoa(v.Index()), "i1")
}

// payload writes the address of the i'th field of variant v inside the
// value at the address p, and its type.
func (e *Emitter) payload(p string, t types.Type, v *types.Variant, i int) (string, types.Type) {
	ir := e.irType(t, token.NoPos)
	switch u := t.Underlying().(type) {
	case *types.Option:
		_, val := e.optionParts(ir, p, u)
		return val, u.Elem()
	case *types.Result:
		if v == types.OkVariant {
			return e.gep(ir, p, 1), u.Val()
		}

		return e.gep(ir, p, 1), u.Err()
	}

	f := v.Fields()[i]
	vt := e.variantType(v.Fields(), token.NoPos)
	return e.gep(vt, e.gep(ir, p, 1), i), f.Type()
}

// tupleElem writes the address of element i of the tuple at the
// address p, and its type.
func (e *Emitter) tupleElem(p string, t types.Type, i int) (string, types.Type) {
	u := t.Underlying().(*types.Tuple)
	return e.gep(e.irType(t, token.NoPos), p, i), u.At(i)
}

// literalTest writes the test that the value at the address p equals
// the literal.
func (e *Emitter) literalTest(p string, t types.Type, lit *ast.BasicLit) value {
	v := e.load(e.irType(t, lit.Pos()), p)
	if lit.Kind == token.STRING {
		return e.rt("sn_string_eq", "i1", v, e.literalOf(lit, t))
	}

	if isFloat(t) {
		return e.op("fcmp oeq "+v.typed()+", "+e.literalOf(lit, t).s, "i1")
	}

	return e.op("icmp eq "+v.typed()+", "+e.literalOf(lit, t).s, "i1")
}

// pathName is the identifier a variant pattern's path resolves through:
// the name itself, or the member of a qualified name, io.NotFound.
func pathName(path ast.Expr) *ast.Ident {
	if sel, ok := path.(*ast.SelectorExpr); ok {
		return sel.Sel
	}

	return path.(*ast.Ident)
}
