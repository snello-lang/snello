// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// A pat is a pattern reduced to what exhaustiveness needs: a wildcard,
// a literal, or a constructor with sub-patterns. A constructor is a
// variant of an enum, an option or a Result, true or false, or the one
// constructor of a tuple.
type pat struct {
	ctor int // wildcard, literal, or a constructor index
	args []pat
}

const (
	wildcard = -1
	literal  = -2 // matches some values of a type with too many to list
)

var wild = pat{ctor: wildcard}

// matchExpr checks match x { arms }: each arm's pattern against the
// type of x, its guard, and its body, then that the unguarded arms
// cover every value. The arms yield one type; an arm that leaves fits
// any. Matching on a borrow binds borrows.
func (c *Checker) matchExpr(e *ast.MatchExpr, expected Type) operand {
	x := c.expr(e.X, nil)
	var scrutinee Type = Typ[Invalid]
	m := value
	if x.mode != invalid {
		scrutinee, m = deref(x)
	}

	b := borrowOf(m)
	result := operand{value, Typ[Never], nil, nil}
	rows := make([][]pat, 0, len(e.Arms))
	f := c.forkAt()
	if x.mode == variable {
		c.watch(e.X) // an arm may assign it before another binds a part by value and moves it under the fork
	}

	consumed := false
	outer := c.scope
	for _, arm := range e.Arms {
		c.openScope()
		c.bindsOwned = false
		c.copies = !isBorrow(x.typ) // a borrow variable binds borrows; a place reached through one copies what is Copy
		p := c.pattern(arm.Pat, scrutinee, b)
		c.resolvingIn(outer, func() { // match o { Some(o) => ... } binds a new o out of the old
			c.bindThrough(arm.Pat, e.X, b)
			if c.bindsOwned && !consumed && held(e.X, x.mode) {
				c.moveBefore(e.X, variable, &f) // a part bound by value leaves the place, on every arm; a temporary's part may not leave it
				consumed = true
			}
		})

		if arm.Guard != nil {
			c.condition(arm.Guard)
		} else {
			rows = appendRows(rows, p)
		}

		v := c.node(arm.Body, expected)
		c.maybeMove(v, v.typ) // the arm's value leaves with the match
		result = c.unify(result, v, arm.Arrow, "match")
		c.closeScope()
		c.branchEnd(&f)
	}

	c.joinAt(&f, true)

	if x.mode != invalid {
		if w := missing([]Type{scrutinee}, rows); w != nil {
			c.errorf(e.Match, "match is not exhaustive: missing "+w[0])
		}
	}

	return result
}

// pattern checks a pattern against the type it must match, declares
// the names it binds, and returns its shape. A borrowed scrutinee binds
// borrows: on a ref State, Open(sess) makes sess a ref Session.
func (c *Checker) pattern(p ast.Pattern, t Type, b *Ref) pat {
	switch p := p.(type) {
	case *ast.WildcardPat:
		return wild
	case *ast.IdentPat:
		return c.identPat(p, t, b)
	case *ast.LitPat:
		c.assign(operand{value, c.literal(p.Lit, t), nil, nil}, t, p.Lit.Pos(), "pattern")
		return pat{ctor: literal}
	case *ast.RangePat:
		c.assign(operand{value, c.literal(p.Lo, t), nil, nil}, t, p.Lo.Pos(), "pattern")
		c.assign(operand{value, c.literal(p.Hi, t), nil, nil}, t, p.Hi.Pos(), "pattern")
		return pat{ctor: literal}
	case *ast.TuplePat:
		tt, ok := t.Underlying().(*Tuple)
		if t != Typ[Invalid] && (!ok || len(tt.elems) != len(p.Elts)) {
			c.errorf(p.Lparen, "a tuple pattern with "+strconv.Itoa(len(p.Elts))+" elements cannot match "+t.String())
			ok = false
		}

		args := make([]pat, len(p.Elts))
		for i, elt := range p.Elts {
			var et Type = Typ[Invalid]
			if ok {
				et = tt.elems[i]
			}

			args[i] = c.pattern(elt, et, b)
		}

		return pat{0, args}
	case *ast.VariantPat:
		return c.variantPat(p, t, b)
	case *ast.OrPat:
		// Only a top-level | is expanded into rows (appendRows); a nested
		// one counts as a literal, which never covers anything, so a
		// match is never called exhaustive by mistake.
		args := make([]pat, len(p.Alts))
		for i, alt := range p.Alts {
			args[i] = c.pattern(alt, t, b)
		}

		return pat{literal, args}
	}

	return wild
}

// identPat is a variant, a constant, or a binding: whichever the name
// resolves to. A name that resolves to nothing is a variant of the
// scrutinee's enum when that has one of the name, as it would be in the
// enum's own package, and a new binding otherwise.
func (c *Checker) identPat(p *ast.IdentPat, t Type, b *Ref) pat {
	_, obj := c.scope.LookupParent(p.Name.Name)
	if obj == nil {
		if v := variantNamed(t, p.Name.Name); v != nil {
			obj = v
		}
	}

	switch obj := obj.(type) {
	case *Variant:
		c.recordUse(p.Name, obj)
		i, fields := c.variantOf(t, obj, p.Name.Pos())
		if i >= 0 && len(fields) > 0 {
			c.errorf(p.Name.Pos(), obj.name+" has fields; write "+obj.name+"(...)")
			return wild
		}

		if i < 0 {
			return wild
		}

		return pat{ctor: i}
	case *Const:
		if obj.typ == Typ[Bool] && isBool(t) {
			c.recordUse(p.Name, obj)
			if obj.value == "true" {
				return pat{ctor: 0}
			}

			return pat{ctor: 1}
		}
	}

	if b == nil && !IsCopy(t) {
		c.bindsOwned = true
	}

	if c.copies && IsCopy(t) {
		b = nil // a Copy value reached through a borrow is read, not lent
	}

	c.declare(c.scope, p.Name, NewVar(p.Name.Pos(), p.Name.Name, wrap(t, b)))
	return wild
}

func (c *Checker) variantPat(p *ast.VariantPat, t Type, b *Ref) pat {
	var v *Variant
	switch path := p.Path.(type) {
	case *ast.Ident:
		if _, obj := c.scope.LookupParent(path.Name); obj != nil {
			c.recordUse(path, obj)
			v, _ = obj.(*Variant)
		} else if v = variantNamed(t, path.Name); v != nil {
			c.recordUse(path, v) // the scrutinee's enum has it, as the enum's own package would
		} else {
			c.errorf(path.Pos(), "undefined: "+path.Name)
		}
	case *ast.SelectorExpr:
		if id, ok := path.X.(*ast.Ident); ok { // io.NotFound(...): a variant through its package
			v, _ = c.qualified(id, path.Sel).(*Variant)
		}
	}

	if v == nil {
		c.errorf(p.Path.Pos(), "a pattern with arguments names a variant")
		for _, arg := range p.Args {
			c.pattern(arg, Typ[Invalid], b)
		}

		return wild
	}

	i, fields := c.variantOf(t, v, p.Path.Pos())
	if i >= 0 && len(fields) != len(p.Args) {
		c.errorf(p.Path.Pos(), v.name+" has "+strconv.Itoa(len(fields))+" fields, not "+strconv.Itoa(len(p.Args)))
		i = wildcard
	}

	args := make([]pat, len(p.Args))
	for j, arg := range p.Args {
		var ft Type = Typ[Invalid]
		if i >= 0 {
			ft = fields[j]
		}

		args[j] = c.pattern(arg, ft, b)
	}

	if i < 0 {
		return wild
	}

	return pat{i, args}
}

// variantOf returns the index of v among the constructors of t and the
// types of its fields, or -1 after an error when v is not a variant of
// t. Invalid t matches anything silently.
// variantNamed returns the variant of the enum type t called name, or
// nil: for a pattern naming a variant of another package's enum, which
// is not in scope here.
func variantNamed(t Type, name string) *Variant {
	if en, ok := t.Underlying().(*Enum); ok {
		for _, v := range en.variants {
			if v.name == name {
				return v
			}
		}
	}

	return nil
}

func (c *Checker) variantOf(t Type, v *Variant, pos token.Pos) (int, []Type) {
	switch u := t.Underlying().(type) {
	case *Option:
		if v == SomeVariant {
			return 0, []Type{u.elem}
		}

		if v == NoneVariant {
			return 1, nil
		}
	case *Result:
		if v == OkVariant {
			return 0, []Type{u.val}
		}

		if v == ErrVariant {
			return 1, []Type{u.err}
		}
	case *Enum:
		if n, ok := t.(*Named); ok && v.parent == n.Origin() {
			return v.index, varTypes(u.variants[v.index].fields) // an instance's fields, with its arguments in place
		}
	case *Basic:
		if u.kind == Invalid {
			return wildcard, nil
		}
	}

	c.errorf(pos, v.name+" is not a variant of "+t.String())
	return wildcard, nil
}

// appendRows adds an arm's pattern to the matrix: one row, or one per
// alternative of a top-level |.
func appendRows(rows [][]pat, p pat) [][]pat {
	if p.ctor == literal && len(p.args) > 0 { // a top-level or-pattern
		for _, alt := range p.args {
			rows = appendRows(rows, alt)
		}

		return rows
	}

	return append(rows, []pat{p})
}

// missing reports a value the rows do not match, as a witness pattern
// per column, or nil when the rows are exhaustive. It is the usefulness
// check of Maranget's "Warnings for pattern matching": a wildcard row is
// useful exactly when the match is not exhaustive. Each step splits the
// first column by its constructors; a column whose type has too many
// values to list keeps only the rows that accept anything there.
func missing(types []Type, rows [][]pat) []string {
	if len(types) == 0 {
		if len(rows) == 0 {
			return []string{} // no row matches the empty vector: a witness
		}

		return nil
	}

	t := types[0]
	n := numCtors(t)
	if n < 0 {
		var rest [][]pat
		for _, row := range rows {
			if row[0].ctor == wildcard {
				rest = append(rest, row[1:])
			}
		}

		if w := missing(types[1:], rest); w != nil {
			return append([]string{"_"}, w...)
		}

		return nil
	}

	for ctor := 0; ctor < n; ctor++ {
		argTypes := ctorArgs(t, ctor)
		k := len(argTypes)
		if w := missing(append(append([]Type{}, argTypes...), types[1:]...), specialize(rows, ctor, k)); w != nil {
			return append([]string{ctorString(t, ctor, w[:k])}, w[k:]...)
		}
	}

	return nil
}

// specialize keeps the rows that can match the constructor in the first
// column and replaces that column with the constructor's k arguments: a
// row with the constructor contributes its sub-patterns, a wildcard row
// k wildcards, and a literal or other-constructor row drops out.
func specialize(rows [][]pat, ctor, k int) [][]pat {
	var spec [][]pat
	for _, row := range rows {
		switch {
		case row[0].ctor == ctor && len(row[0].args) == k:
			spec = append(spec, append(append([]pat{}, row[0].args...), row[1:]...))
		case row[0].ctor == wildcard:
			spec = append(spec, append(wilds(k), row[1:]...))
		}
	}

	return spec
}

// numCtors returns how many constructors a type has, or -1 when its
// values cannot be listed.
func numCtors(t Type) int {
	switch u := t.Underlying().(type) {
	case *Option, *Result:
		return 2
	case *Enum:
		return len(u.variants)
	case *Tuple:
		return 1
	case *Basic:
		if u.kind == Bool {
			return 2
		}
	}

	return -1
}

// ctorArgs returns the field types of a constructor.
func ctorArgs(t Type, ctor int) []Type {
	switch u := t.Underlying().(type) {
	case *Option:
		if ctor == 0 {
			return []Type{u.elem}
		}
	case *Result:
		if ctor == 0 {
			return []Type{u.val}
		}

		return []Type{u.err}
	case *Enum:
		return varTypes(u.variants[ctor].fields)
	case *Tuple:
		return u.elems
	}

	return nil
}

// ctorString writes a constructor with its argument witnesses the way
// a program would write the pattern.
func ctorString(t Type, ctor int, args []string) string {
	name := ""
	switch u := t.Underlying().(type) {
	case *Option:
		name = [...]string{"Some", "None"}[ctor]
	case *Result:
		name = [...]string{"Ok", "Err"}[ctor]
	case *Enum:
		name = u.variants[ctor].name
	case *Tuple:
		return "(" + strings.Join(args, ", ") + ")"
	case *Basic:
		return [...]string{"true", "false"}[ctor]
	}

	if len(args) == 0 {
		return name
	}

	return name + "(" + strings.Join(args, ", ") + ")"
}

// varTypes returns the types of a list of variables: a variant's
// fields, or a signature's parameters.
func varTypes(vars []*Var) []Type {
	if len(vars) == 0 {
		return nil
	}

	ts := make([]Type, len(vars))
	for i, v := range vars {
		ts[i] = v.typ
	}

	return ts
}

func wilds(n int) []pat {
	if n == 0 {
		return nil
	}

	ps := make([]pat, n)
	for i := range ps {
		ps[i] = wild
	}

	return ps
}

func isBool(t Type) bool {
	b, ok := t.Underlying().(*Basic)
	return ok && b.kind == Bool
}
