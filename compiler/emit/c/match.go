// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// A match is what every arm of one match shares: the place and type
// of the value looked into, the temporary the arms assign and its
// type, and the label after the last arm.
type match struct {
	place      string
	typ        types.Type
	result     string // empty when the match yields no value
	resultType types.Type
	end        string
	consume    bool // the scrutinee is taken apart: a binding by value empties its part
}

// matchExpr writes a match as a chain of tests on the scrutinee's
// place. Each arm tests its pattern, binds its names, tests its guard,
// runs its body and jumps to the end; an arm whose guard fails falls
// to the next. The checker proved an arm matches every value, so the
// panic after the last arm is never reached.
func (e *Emitter) matchExpr(x *ast.MatchExpr) string {
	m := match{result: "", resultType: e.typeOf(x)}
	if yields(m.resultType) {
		m.result = e.temp()
		e.line(e.cType(m.resultType, x.Pos()) + " " + m.result + ";")
	}

	var held string
	m.place, m.typ, held = e.consumed(x.X)
	m.consume = held != ""
	e.line("{")
	m.end = e.temp() + "_end"
	for _, arm := range x.Arms {
		e.arm(&m, arm)
	}

	if held != "" {
		e.hold(held, e.typeOf(x.X)) // what no arm took is dropped when the statement ends
	}

	e.line("sn_panic(" + literal("no match arm matched") + ");")
	if len(x.Arms) > 0 {
		e.line(m.end + ":;")
	}

	e.line("}")
	return m.result
}

func (e *Emitter) arm(m *match, arm *ast.MatchArm) {
	if tests := e.patternTests(arm.Pat, m.place, m.typ); len(tests) > 0 {
		e.line("if (" + strings.Join(tests, " && ") + ") {")
	} else {
		e.line("{")
	}

	e.push(false)
	taken := e.patternBinds(arm.Pat, m.place, m.typ)
	if arm.Guard != nil {
		e.line("if " + parenthesized(e.boolean(arm.Guard)) + " {")
	}

	if m.consume {
		e.empty(taken, arm.Pos()) // the parts bound by value left the scrutinee, once the arm is known to run
	}

	e.node(arm.Body, m.result, m.resultType)
	e.pop(arm.Pos())
	e.line("goto " + m.end + ";")
	if arm.Guard != nil {
		e.line("}")
	}

	e.line("}")
}

// empty writes the emptying of places whose values were bound by
// value.
func (e *Emitter) empty(taken []owned, pos token.Pos) {
	for _, t := range taken {
		e.line(t.name + " = " + e.zero(t.typ, pos) + ";")
	}
}

// ifBind writes if pat := value {: the body runs when the option holds
// a value the pattern matches, with the pattern's names bound.
func (e *Emitter) ifBind(s *ast.IfBindStmt) {
	place, t, held := e.consumed(s.Value)
	if held != "" {
		e.hold(held, e.typeOf(s.Value))
	}

	e.line("{")
	elem := t.Underlying().(*types.Option).Elem()
	tests := append([]string{place + ".has"}, e.patternTests(s.Pat, place+".val", elem)...)
	e.line("if (" + strings.Join(tests, " && ") + ") {")
	e.push(false)
	taken := e.patternBinds(s.Pat, place+".val", elem)
	if held != "" {
		e.empty(taken, s.Pos())
	}

	e.stmts(s.Body.List)
	e.pop(s.Body.Rbrace)
	e.elseBranch(s.Else)
	e.line("}")
}

// patternTests writes the conditions under which a pattern matches
// the value at place, of type t.
func (e *Emitter) patternTests(p ast.Pattern, place string, t types.Type) []string {
	switch p := p.(type) {
	case *ast.IdentPat:
		switch obj := e.info.Uses[p.Name].(type) {
		case *types.Variant:
			return []string{e.tagTest(place, t, obj)}
		case *types.Const:
			if obj.Name() == "true" {
				return []string{place}
			}

			return []string{"!" + place}
		}
	case *ast.LitPat:
		return []string{e.literalTest(place, p.Lit)}
	case *ast.RangePat:
		return []string{place + " >= " + e.literal(p.Lo), place + " <= " + e.literal(p.Hi)}
	case *ast.TuplePat:
		var tests []string
		for i, elt := range p.Elts {
			sub, st := tupleElem(place, t, i)
			tests = append(tests, e.patternTests(elt, sub, st)...)
		}

		return tests
	case *ast.VariantPat:
		v := variantOf(t, e.info.Uses[pathName(p.Path)].(*types.Variant))
		tests := []string{e.tagTest(place, t, v)}
		for i, arg := range p.Args {
			sub, ft := e.payload(place, t, v, i)
			tests = append(tests, e.patternTests(arg, sub, ft)...)
		}

		return tests
	case *ast.OrPat:
		alts := make([]string, len(p.Alts))
		for i, alt := range p.Alts {
			tests := e.patternTests(alt, place, t)
			if len(tests) == 0 {
				return nil // an alternative that always matches
			}

			alts[i] = strings.Join(tests, " && ")
			if len(tests) > 1 {
				alts[i] = "(" + alts[i] + ")"
			}
		}

		return []string{"(" + strings.Join(alts, " || ") + ")"}
	}

	return nil
}

// patternBinds declares the names a pattern binds, each to the part
// of the value at place it stands for: a copy, or through a mut borrow
// its address. It returns the parts bound by value that own
// something, which a consuming match empties once the arm runs.
func (e *Emitter) patternBinds(p ast.Pattern, place string, t types.Type) []owned {
	switch p := p.(type) {
	case *ast.IdentPat:
		v, ok := e.info.Defs[p.Name].(*types.Var)
		if !ok {
			return nil
		}

		vt := e.sub(v.Type())

		value := place
		if depth(vt) > depth(t) { // a mut binding of a value: its address
			value = "&" + place
		}

		local := e.declareLocal(p.Name)
		e.line(e.cType(vt, p.Pos()) + " " + local + " = " + value + ";")
		e.discardUnused(p.Name)
		e.own(local, vt)
		if _, borrow := vt.(*types.Ref); !borrow && needsDrop(vt) {
			return []owned{{name: place, typ: vt}}
		}
	case *ast.TuplePat:
		var taken []owned
		for i, elt := range p.Elts {
			sub, st := tupleElem(place, t, i)
			taken = append(taken, e.patternBinds(elt, sub, st)...)
		}

		return taken
	case *ast.VariantPat:
		var taken []owned
		v := variantOf(t, e.info.Uses[pathName(p.Path)].(*types.Variant))
		for i, arg := range p.Args {
			sub, ft := e.payload(place, t, v, i)
			taken = append(taken, e.patternBinds(arg, sub, ft)...)
		}

		return taken
	}

	return nil
}

// tagTest writes the test that the value at place was built by v.
func (e *Emitter) tagTest(place string, t types.Type, v *types.Variant) string {
	switch t.Underlying().(type) {
	case *types.Option:
		if v == types.SomeVariant {
			return place + ".has"
		}

		return "!" + place + ".has"
	case *types.Result:
		if v == types.OkVariant {
			return place + ".ok"
		}

		return "!" + place + ".ok"
	}

	return place + ".tag == " + strconv.Itoa(v.Index())
}

// payload writes the place of the i'th field of variant v inside the
// value at place, and its type.
func (e *Emitter) payload(place string, t types.Type, v *types.Variant, i int) (string, types.Type) {
	switch u := t.Underlying().(type) {
	case *types.Option:
		return place + ".val", u.Elem()
	case *types.Result:
		if v == types.OkVariant {
			return place + ".val", u.Val()
		}

		return place + ".err", u.Err()
	}

	f := v.Fields()[i]
	return place + "." + variantMember(v) + "." + fieldName(f, i), f.Type()
}

func tupleElem(place string, t types.Type, i int) (string, types.Type) {
	return place + ".e" + strconv.Itoa(i), t.Underlying().(*types.Tuple).At(i)
}

func (e *Emitter) literalTest(place string, lit *ast.BasicLit) string {
	if lit.Kind == token.STRING {
		return "sn_string_eq(" + place + ", " + e.literal(lit) + ")"
	}

	return place + " == " + e.literal(lit)
}

// pathName is the identifier a variant pattern's path resolves through:
// the name itself, or the member of a qualified name, io.NotFound.
func pathName(path ast.Expr) *ast.Ident {
	if sel, ok := path.(*ast.SelectorExpr); ok {
		return sel.Sel
	}

	return path.(*ast.Ident)
}
