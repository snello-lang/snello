// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"slices"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// What a value of the form lends, read off the instructions that make
// it rather than off its expression's shape. A borrow lends the place
// it is made of; a value made from parts, a literal, a tuple or a
// variant, lends what each part lends; a call's result lends what the
// argument it borrows from lends, by the rule for returned borrows, and
// a generic call's what its arguments of a type parameter's type lend;
// the value of an if, a match, a ?? or a ? lends what each value stored
// into it lends. A borrow of a value the statement made, and holds
// until its end, is a borrow of a temporary, unless the value is data
// that lives for the whole program: a literal, a constant, or a string
// chosen among such. Whatever shape an expression has, its value is
// made by these instructions, so the escape check and the rule on a
// borrow of a temporary, which read this, see every shape the same way.

// noLend ends a chain of the instructions that make a temporary: no
// instruction, or no more.
const noLend = -1

// A lentPlace is a place a value lends from: lent, the place itself
// borrowed, ref s; or read, a borrow the place holds passed on, r in
// (r, 1) or a borrow variable lent again. A temporary lent, ref f(),
// is a place too, a temporary operand.
type lentPlace struct {
	place  Operand
	lent   bool
	itself bool      // the place is a borrow lent itself, where a borrow of it is wanted: its own slot, not what it points at
	expr   ast.Expr  // for a place a borrow is made of, the expression the borrow is made of, and where
	at     token.Pos // NoPos for a place read
}

// defsOf indexes where each temporary of the form is made, in the
// blocks control reaches: the instruction that makes it, and for the
// value of an if, a match, a ?? or a ?, each store of an arm's value
// into it. It is built once per form, when a value is first followed.
func (m *formBorrows) defsOf() {
	f := m.form
	if m.defsFor == f {
		return
	}

	m.defsFor = f
	m.firstDef = growFilled(m.firstDef, len(f.Temps), noLend)
	m.nextDef = growFilled(m.nextDef, len(f.Instrs), noLend)
	m.following = growFilled(m.following, len(f.Temps), 0)
	for _, b := range f.order {
		blk := f.Blocks[b]
		for i := blk.To - 1; i >= blk.From; i-- { // each block's own in order once the chains are read from their heads
			t := madeInto(&f.Instrs[i])
			if t == NoTemp {
				continue
			}

			m.nextDef[i] = m.firstDef[t]
			m.firstDef[t] = i
		}
	}
}

// growFilled returns s with n elements, each v.
func growFilled(s []int, n, v int) []int {
	if cap(s) < n {
		s = make([]int, n)
	}

	s = s[:n]
	for i := range s {
		s[i] = v
	}

	return s
}

// madeInto is the temporary the instruction makes or stores a value
// into, or NoTemp.
func madeInto(in *Instr) int {
	switch in.Op {
	case OpBorrow, OpMake, OpCall:
		if in.To.IsValue() && in.To.Path == "" {
			return in.To.Temp
		}
	case OpStore:
		if in.Place.IsValue() && in.Place.Path == "" {
			return in.Place.Temp
		}
	}

	return NoTemp
}

// lends returns the temporary a borrow in the value o is of, the first
// met, or NoTemp, and leaves the places o lends from in m.lent, the
// temporaries among them, until the next call of lends, held, partLends,
// bringsBorrow, keptTemporary or partOfTemporary: o a place, read for
// the borrow it holds, or a value made here.
func (m *formBorrows) lends(o Operand) int {
	m.lent = m.lent[:0]
	return m.follow(o)
}

// follow is lends without emptying m.lent: what o lends is added to it.
func (m *formBorrows) follow(o Operand) int {
	if !o.IsTemp() {
		if m.holds(o) {
			m.lent = append(m.lent, lentPlace{place: o})
		}

		return NoTemp
	}

	if o.Temp == NoTemp {
		return NoTemp
	}

	m.defsOf()
	t := o.Temp
	if m.following[t] != 0 { // a value made of itself is no value a program makes; followed once
		return NoTemp
	}

	m.following[t] = 1
	defer func() { m.following[t] = 0 }()
	tmp := NoTemp
	for i := m.firstDef[t]; i != noLend; i = m.nextDef[i] {
		if found := m.madeBy(&m.form.Instrs[i]); tmp == NoTemp {
			tmp = found
		}
	}

	return tmp
}

// holds reports whether the place o holds a borrow, which it passes on
// when it is read: its value is or carries one.
func (m *formBorrows) holds(o Operand) bool {
	_, kind, t := m.placeOf(o)
	if kind == notPlace {
		return false
	}

	if t == nil { // a part a pattern binds: what the whole holds
		t = m.form.Vars[o.Var].typ
	}

	return holdsBorrow(t)
}

// madeBy follows what the instruction in, which makes or fills a
// temporary, puts in it.
func (m *formBorrows) madeBy(in *Instr) int {
	switch in.Op {
	case OpBorrow:
		tmp := m.borrowOf(in.Place, m.written(in.To), m.form.Temps[in.To.Temp].Itself)
		if n := len(m.lent); n > 0 && m.lent[n-1].place == in.Place && !in.Place.IsTemp() {
			m.lent[n-1].expr, m.lent[n-1].at = in.Expr, in.Pos // where the place is lent, which a call's result lending it accesses
		}

		return tmp
	case OpStore:
		return m.follow(in.To) // an arm's value, into the value an if, a match, a ?? or a ? gives
	case OpMake:
		return m.madeOf(in)
	case OpCall:
		return m.called(in)
	}

	return NoTemp
}

// written reports whether the borrow made into t is of the place
// itself: written in the program, ref x or mut x, or made of a borrow
// given where a borrow of it is wanted; rather than one the lowering
// makes of an owned value given where a borrow is wanted, a borrow
// variable passed on, or a place sliced.
func (m *formBorrows) written(t Operand) bool {
	if m.form.Temps[t.Temp].Itself {
		return true // the lowering's borrow of a borrow's own place, as ref r is
	}

	u, ok := unparen(m.form.Temps[t.Temp].Expr).(*ast.UnaryExpr)
	return ok && (u.Op == token.REF || u.Op == token.MUT)
}

// borrowOf follows a borrow of the place o: a place of a variable lends
// itself, but one whose value is a borrow, lent on where the program
// wrote no ref, passes that borrow on; a part of a value made here
// reached through a borrow it is or holds lends what that borrow lends;
// any other part of it is the temporary, unless it is data that lives
// for the whole program. itself says a borrow is lent itself, where a
// borrow of it is wanted: its own slot.
func (m *formBorrows) borrowOf(o Operand, written, itself bool) int {
	if !o.IsTemp() {
		_, _, t := m.placeOf(o)
		m.lent = append(m.lent, lentPlace{place: o, lent: itself || !isBorrow(t), itself: itself}) // ref x of a borrow variable is x's own borrow, as the typing reads it, passed on; a borrow of its slot is the lowering's, lent itself
		return NoTemp
	}

	if o.Temp == NoTemp {
		return NoTemp
	}

	if t := m.form.Temps[o.Temp].Type; t == nil || t == Typ[Invalid] {
		return NoTemp // a value the typing refused, reported there
	}

	if m.reachedThroughBorrow(o) && !(itself && o.Path == "") { // a borrow value lent itself is the temporary's own slot
		return m.follow(Temp(o.Temp))
	}

	if m.static(o.Temp) {
		return NoTemp
	}

	m.lent = append(m.lent, lentPlace{place: Temp(o.Temp), lent: true}) // a temporary lent, which what holds the value holds
	return o.Temp
}

// reachedThroughBorrow reports whether the part o of a value made here
// is reached through a borrow: the value is one, or a * on the path is
// of one, so that the part is what the borrow points at and not the
// value's own.
func (m *formBorrows) reachedThroughBorrow(o Operand) bool {
	t := m.form.Temps[o.Temp].Type
	return isBorrow(t) || derefsBorrow(t, o.Path)
}

// derefsBorrow reports whether the path, from a value of type t, goes
// through a * of a borrow, the value or one it holds in a field: what
// the path reaches is then what the borrow points at.
func derefsBorrow(t Type, path string) bool {
	for path != "" && t != nil {
		part, rest, _ := strings.Cut(path, ".")
		path = rest
		if part == "*" && isBorrow(t) {
			return true
		}

		if part[0] == '?' {
			continue // the part a pattern binds, inside the same value
		}

		t = stepType(t, part)
	}

	return false
}

// madeOf follows a value made from parts: a borrow of a value made here,
// ref f(), is a borrow of it; a closure lends what it captures, which
// the closure's own checks decide; any other value holding a borrow
// lends what each part lends.
func (m *formBorrows) madeOf(in *Instr) int {
	switch x := unparen(in.Expr).(type) {
	case *ast.FuncLit:
		return NoTemp
	case *ast.UnaryExpr:
		if (x.Op == token.REF || x.Op == token.MUT) && len(in.Args) == 1 {
			return m.borrowOf(in.Args[0], true, false)
		}
	}

	if t := m.form.Temps[in.To.Temp].Type; t == nil || !holdsBorrow(t) {
		return NoTemp // a value holding no borrow lends nothing, whatever it is made of
	}

	return m.followEach(in.Args)
}

// followEach follows each of args and returns the first temporary one
// borrows.
func (m *formBorrows) followEach(args []Operand) int {
	tmp := NoTemp
	for _, a := range args {
		if found := m.follow(a); tmp == NoTemp {
			tmp = found
		}
	}

	return tmp
}

// called follows a call's result, when it holds a borrow: by the rule
// for returned borrows it lends what the receiver that is a borrow, or
// the argument of the one borrowed parameter, lends; a generic call's
// result lends what each argument of a type parameter's type lends
// (passing.go), and, when its declared result holds no borrow, nothing
// else.
func (m *formBorrows) called(in *Instr) int {
	call, ok := in.Expr.(*ast.CallExpr)
	if t := m.form.Temps[in.To.Temp].Type; !ok || t == nil || !holdsBorrow(t) {
		return NoTemp
	}

	c := m.checker
	k := c.calledBy(call)
	recv := len(in.Args) > len(call.Args) // the receiver comes first, when the callee is a method
	tmp := NoTemp
	passed, generic := c.passed[call]
	if !generic || !passed.only {
		if recv && k.obj == nil { // a method the language declares: its result borrows its receiver
			tmp = m.follow(in.Args[0])
		} else if src := callSourceIndex(k.sig, recv); src >= 0 && src < len(in.Args) {
			tmp = m.followSource(in.Args[src], m.form.Temps[in.To.Temp].Type)
		}
	}

	if !generic {
		return tmp
	}

	for i, a := range passed.all { // the receiver first when there is one, then the arguments, as the operands are
		if i >= len(in.Args) || !slices.Contains(passed.args, a.x) {
			continue
		}

		found := NoTemp
		if isBorrow(a.decl) {
			found = m.followHeld(in.Args[i]) // a parameter declared ref T: the result holds what the value lent holds, not the borrow
		} else {
			found = m.follow(in.Args[i])
		}

		if tmp == NoTemp {
			tmp = found
		}
	}

	return tmp
}

// followSource follows the operand o a call's result of type result
// borrows from: a borrow's own slot given where a borrow of it is
// wanted gives a result of another type only what the slot holds, as
// *x is all a ref string can be of an x of type ref ref string.
func (m *formBorrows) followSource(o Operand, result Type) int {
	if o.IsValue() && o.Path == "" && m.form.Temps[o.Temp].Itself && !Identical(m.form.Temps[o.Temp].Type, result) {
		return m.followHeld(o)
	}

	return m.follow(o)
}

// held is lends for what the value a borrow o is made of holds, rather
// than for the borrow: a place read for the borrow it holds, or a value
// made here followed; o made otherwise is followed as it is.
func (m *formBorrows) held(o Operand) int {
	m.lent = m.lent[:0]
	return m.followHeld(o)
}

// followHeld is held without emptying m.lent.
func (m *formBorrows) followHeld(o Operand) int {
	if !o.IsValue() || o.Path != "" {
		return m.follow(o)
	}

	m.defsOf()
	i := m.firstDef[o.Temp]
	if i == noLend || m.nextDef[i] != noLend {
		return m.follow(o)
	}

	in := &m.form.Instrs[i]
	switch {
	case in.Op == OpBorrow:
		return m.follow(in.Place)
	case in.Op == OpMake && m.written(in.To) && len(in.Args) == 1:
		return m.follow(in.Args[0])
	}

	return m.follow(o)
}

// static reports whether the value made into t is data that lives for
// the whole program: a literal or a constant, or a string each value
// stored into which is so. A number chosen by an if is copied into the
// if's value, which a borrow would point at after the statement drops
// it; a string's bytes are where the literal put them.
func (m *formBorrows) static(t int) bool {
	m.defsOf()
	c, f := m.checker, m.form
	made := false
	for i := m.firstDef[t]; i != noLend; i = m.nextDef[i] {
		in := &f.Instrs[i]
		switch {
		case in.Op == OpMake && len(in.Args) == 0 && c.static(in.Expr):
		case in.Op == OpStore && in.To.IsValue() && in.To.Path == "" && stringValue(f.Temps[in.To.Temp].Type) && m.static(in.To.Temp):
		default:
			return false
		}

		made = true
	}

	return made
}

// stringValue reports whether a value of type t, stored into the value
// of an if or a match, is a string: t is string, or t is nil, the value
// of an if that follows an else, which the typing gives no type of its
// own and whose arms static checks one by one.
func stringValue(t Type) bool {
	return t == nil || isString(t)
}

// throughBorrow reports whether the place o is reached through a
// borrow on its path: a * of a borrow the variable is, or holds in a
// field, so that the place is what the borrow points at.
func (m *formBorrows) throughBorrow(o Operand) bool {
	return derefsBorrow(m.form.Vars[o.Var].typ, o.Path)
}

// partOfTemporary returns the expression of a value the statement made
// that the operand o, given to a deferred call, borrows from, or nil:
// the deferred call keeps the value it is given, the whole of a value
// made for it, but not what that value lends, nor a part, a view or
// what a call's result borrows of a value made here, which the defer
// statement drops before the call runs.
func (m *formBorrows) partOfTemporary(o Operand) ast.Expr {
	if !o.IsValue() {
		return nil
	}

	if o.Path == "" {
		m.defsOf()
		if i := m.firstDef[o.Temp]; i != noLend && m.nextDef[i] == noLend {
			in := &m.form.Instrs[i]
			if in.Op == OpBorrow && in.Place.IsValue() && in.Place.Path == "" && !m.form.Temps[o.Temp].View {
				o = Temp(in.Place.Temp) // the value made for the call, kept by it: what it lends is what matters
			}
		}
	}

	if tmp := m.lends(o); tmp != NoTemp && tmp != o.Temp {
		return m.form.Temps[tmp].Expr
	}

	if o.Path != "" && !m.reachedThroughBorrow(o) {
		return m.form.Temps[o.Temp].Expr // a part of the value, which goes with it
	}

	return nil
}

// bringsBorrow reports whether the value o lends a place or a
// temporary: what the value a borrow o is made of holds, when lent says
// the parameter takes it lent.
func (m *formBorrows) bringsBorrow(o Operand, lent bool) bool {
	tmp := NoTemp
	if lent {
		tmp = m.held(o)
	} else {
		tmp = m.lends(o)
	}

	return tmp != NoTemp || len(m.lent) > 0
}

// partLends is lends for the part of a value a pattern binds, o a
// value made here and the path the pattern took: an element of a tuple
// or a field of a variant made here lends what the operand it was made
// from lends, and any other part what the whole lends.
func (m *formBorrows) partLends(o Operand) int {
	for o.IsValue() && o.Path != "" {
		part, rest, _ := strings.Cut(o.Path, ".")
		args, ok := m.partsOf(o.Temp, part)
		if !ok {
			break
		}

		if part[1] < '0' || part[1] > '9' { // a variant's name: the field's index follows
			part, rest, _ = strings.Cut(rest, ".")
			if part == "" || part[0] != '?' {
				break
			}
		}

		i, err := strconv.Atoi(part[1:])
		if err != nil || i >= len(args) {
			break
		}

		o = args[i]
		if !o.IsTemp() {
			if rest != "" {
				o.Path = joinPath(o.Path, rest)
			}

			return m.lends(o)
		}

		o.Path = rest
	}

	if !o.IsTemp() {
		return m.lends(o)
	}

	return m.lends(Temp(o.Temp))
}

// borrowedOf returns what the temporary t is a borrow of, when one
// borrow, written or made by the lowering, is all that makes it.
func (m *formBorrows) borrowedOf(t int) (Operand, bool) {
	m.defsOf()
	i := m.firstDef[t]
	if i == noLend || m.nextDef[i] != noLend {
		return Operand{}, false
	}

	in := &m.form.Instrs[i]
	switch {
	case in.Op == OpBorrow:
		return in.Place, true
	case in.Op == OpMake && m.written(in.To) && len(in.Args) == 1:
		return in.Args[0], in.Args[0].Temp != NoTemp || !in.Args[0].IsTemp()
	}

	return Operand{}, false
}

// partType is the type of the part path of a value of type t: through
// a borrow or a pointer, an element, a field, a tuple's element or a
// variant's field, as a pattern names them, a part of a borrow being
// the part of what it points at; nil when it is not known.
func partType(t Type, path string) Type {
	for path != "" && t != nil {
		part, rest, _ := strings.Cut(path, ".")
		path = rest
		if r, ok := t.(*Ref); ok && part != "*" {
			t = r.elem // a part of what a borrow points at, as a pattern or an element through it reaches
		}

		if part[0] != '?' {
			next := stepType(t, part)
			if next == t && part != "*" {
				return nil // a part stepType does not know, which it answers with t
			}

			t = next
			continue
		}

		if len(part) < 2 {
			return nil
		}

		if part[1] >= '0' && part[1] <= '9' { // a tuple's element
			i, err := strconv.Atoi(part[1:])
			if err != nil {
				return nil
			}

			tu, ok := t.Underlying().(*Tuple)
			if !ok || i >= len(tu.elems) {
				return nil
			}

			t = tu.elems[i]
			continue
		}

		index, after, _ := strings.Cut(path, ".") // a variant's name, then its field's index
		path = after
		i, err := strconv.Atoi(strings.TrimPrefix(index, "?"))
		if err != nil {
			return nil
		}

		t = variantField(t, part[1:], i)
	}

	return t
}

// variantField is the type of the i-th field of the variant named name
// of a value of type t, an option's Some, a result's Ok or Err, or an
// enum's, nil when it has none.
func variantField(t Type, name string, i int) Type {
	switch u := t.Underlying().(type) {
	case *Option:
		if name == "Some" && i == 0 {
			return u.elem
		}
	case *Result:
		switch {
		case name == "Ok" && i == 0:
			return u.val
		case name == "Err" && i == 0:
			return u.err
		}
	case *Enum:
		for _, v := range u.variants {
			if v.name == name && i < len(v.fields) {
				return v.fields[i].typ
			}
		}
	}

	return nil
}

// partsOf returns what the value made into t was made from, when it is
// one tuple made here, or one variant named by part, ?Name: the
// operands its elements or fields were given.
func (m *formBorrows) partsOf(t int, part string) ([]Operand, bool) {
	if len(part) < 2 || part[0] != '?' {
		return nil, false
	}

	m.defsOf()
	i := m.firstDef[t]
	if i == noLend || m.nextDef[i] != noLend || m.form.Instrs[i].Op != OpMake {
		return nil, false
	}

	in := &m.form.Instrs[i]
	switch x := unparen(in.Expr).(type) {
	case *ast.TupleExpr:
		return in.Args, part[1] >= '0' && part[1] <= '9'
	case *ast.CallExpr:
		v, ok := m.checker.calleeOf(x).(*Variant)
		return in.Args, ok && v.name == part[1:]
	}

	return nil, false
}
