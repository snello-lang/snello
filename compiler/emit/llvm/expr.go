// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// A value the function computed: the text of its operand, a register
// or a constant, and its IR type. A borrow is a ptr to the place it
// lends, an owned pointer a ptr to its block, a composite an
// aggregate.
type value struct {
	typ string
	s   string
}

// unit is the value of (), which carries nothing.
var unit = value{"{}", "zeroinitializer"}

// typed writes the value as an instruction operand, type first.
func (v value) typed() string {
	return v.typ + " " + v.s
}

// depth is the number of pointers in front of the value of a Snello
// type: one per borrow and per owned pointer, except a borrow that is
// a view (types.go), which is the value itself.
func depth(t types.Type) int {
	switch t := t.(type) {
	case *types.Ref:
		if view(t) {
			return depth(t.Elem())
		}

		return 1 + depth(t.Elem())
	case *types.Pointer:
		return 1 + depth(t.Elem())
	}

	return 0
}

// expr computes x into a register or a constant. A place the checker
// said is moved from is emptied as it is read.
func (e *Emitter) expr(x ast.Expr) value {
	if slot, ok := e.fn.pre[x]; ok {
		return e.load(e.irType(e.typeOf(x), x.Pos()), slot) // evaluated at a defer statement
	}

	if e.info.Moves[x] && needsDrop(e.typeOf(x)) {
		return e.moved(x)
	}

	switch x := x.(type) {
	case *ast.Ident:
		return e.ident(x)
	case *ast.BasicLit:
		return e.literal(x)
	case *ast.ParenExpr:
		return e.expr(x.X)
	case *ast.TupleExpr:
		return e.tuple(x)
	case *ast.SelectorExpr:
		return e.selector(x)
	case *ast.IndexExpr:
		return e.load(e.irType(e.typeOf(x), x.Pos()), e.index(x, false))
	case *ast.SliceExpr:
		return e.sliceExpr(x)
	case *ast.CallExpr:
		return e.call(x)
	case *ast.UnaryExpr:
		return e.unary(x)
	case *ast.BinaryExpr:
		return e.binary(x)
	case *ast.CompositeLit:
		return e.compositeLit(x)
	case *ast.FuncLit:
		return e.funcLit(x)
	case *ast.CoalesceExpr:
		return e.coalesce(x)
	case *ast.TryExpr:
		return e.try(x)
	case *ast.IfExpr:
		return e.ifExpr(x)
	case *ast.MatchExpr:
		return e.matchExpr(x)
	}

	e.unsupported(x.Pos(), strings.TrimPrefix(fmt.Sprintf("%T", x), "*ast.")+" expressions")
	return unit
}

// ident reads a variable from its slot, names a predeclared constant,
// or builds a variant or a function value.
func (e *Emitter) ident(x *ast.Ident) value {
	switch obj := e.info.Uses[x].(type) {
	case *types.Const:
		if obj.Pos() == token.NoPos && (obj.Name() == "true" || obj.Name() == "false") {
			return value{"i1", obj.Name()}
		}

		sym, local := e.localConst[obj]
		if !local {
			sym = lower.ObjName(obj)
		}

		return e.load(e.irType(e.typeOf(x), x.Pos()), "@"+sym)
	case *types.Func:
		return e.funcValue(obj, x.Pos())
	case *types.Var:
		t := e.irType(e.sub(obj.Type()), x.Pos())
		return e.load(t, e.varSlot(obj, x.Pos()))
	case *types.Variant:
		return e.variantValue(e.typeOf(x), obj, nil, x.Pos())
	}

	e.unsupported(x.Pos(), "the name "+x.Name+" in this position")
	return unit
}

// varSlot returns the address of the variable v: its slot in this
// function, the place a closure reaches it through, or its global.
func (e *Emitter) varSlot(v *types.Var, pos token.Pos) string {
	if slot, ok := e.fn.vars[v]; ok {
		return slot
	}

	if s, ok := e.fn.captured[v]; ok {
		return s
	}

	if e.external[v] || e.globals[v] {
		return "@" + lower.ObjName(v)
	}

	e.unsupported(pos, "the variable "+v.Name()+" in this position")
	return "null"
}

// literal writes a number, character or string as a constant of the
// type the checker gave it: an integer by its bits, so that the one
// magnitude the IR cannot read as a signed number is written as the
// negative it is, and a float as the hex of its bits, which the IR
// reads exactly.
func (e *Emitter) literal(x *ast.BasicLit) value {
	return e.literalOf(x, e.typeOf(x))
}

// literalOf writes the literal x as a constant of the type t, which a
// pattern gives it, since the checker records no type for a literal
// inside a pattern.
func (e *Emitter) literalOf(x *ast.BasicLit, t types.Type) value {
	ir := e.irType(t, x.Pos())
	switch x.Kind {
	case token.STRING:
		s, err := strconv.Unquote(x.Value)
		if err != nil {
			e.unsupported(x.Pos(), "this string literal")
		}

		return e.constString(s)
	case token.CHAR:
		r, _, _, err := strconv.UnquoteChar(x.Value[1:len(x.Value)-1], '\'')
		if err != nil {
			e.unsupported(x.Pos(), "this character literal")
		}

		return value{ir, strconv.FormatInt(int64(r), 10)}
	case token.INT:
		if isFloat(t) { // 2 in 2.5 * 2: the literal is a float by its context
			f, _ := strconv.ParseFloat(x.Value, 64)
			return value{ir, hexFloat(f, bits(t))}
		}

		n, err := strconv.ParseUint(x.Value, 0, 64) // base 0 reads 0x, 0b, 0o and _
		if err != nil {
			e.unsupported(x.Pos(), "this integer literal")
		}

		return value{ir, strconv.FormatInt(int64(n), 10)}
	case token.FLOAT:
		f, err := strconv.ParseFloat(x.Value, 64)
		if err != nil {
			e.unsupported(x.Pos(), "this float literal")
		}

		return value{ir, hexFloat(f, bits(t))}
	}

	e.unsupported(x.Pos(), "this literal")
	return unit
}

// hexFloat writes f as the IR's exact form of a float constant, the
// hex of the double's bits; a float constant is written as the double
// of its rounded value.
func hexFloat(f float64, width int) string {
	if width == 32 {
		f = float64(float32(f))
	}

	return fmt.Sprintf("0x%016X", math.Float64bits(f))
}

// constString writes s as a private constant of the module and
// returns the sn_string value pointing at it: a literal owns nothing,
// which a capacity of zero says.
func (e *Emitter) constString(s string) value {
	return value{"%sn_string", "{ ptr " + e.constBytes(s) + ", i64 " + strconv.Itoa(len(s)) + ", i64 0 }"}
}

// constBytes writes the bytes of s, and a zero after them for the
// runtime functions that take a C string, as a private constant of
// the module and returns its name.
func (e *Emitter) constBytes(s string) string {
	name := "@.str." + strconv.Itoa(e.strs)
	e.strs++
	put(&e.consts, name, " = private unnamed_addr constant [", strconv.Itoa(len(s)+1), " x i8] c\"", escape(s), "\\00\"\n")
	return name
}

// escape writes the bytes of s as the IR writes a constant string:
// printable ASCII as itself, everything else as two hex digits after a
// backslash.
func escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c < 0x7f && c != '"' && c != '\\' {
			b.WriteByte(c)
			continue
		}

		fmt.Fprintf(&b, "\\%02X", c)
	}

	return b.String()
}

// unary writes a prefix operator. A borrow is the address of the
// place, as valueTo decides; * reads through one borrow or pointer;
// &x moves x to a new block on the heap; <-ch receives.
func (e *Emitter) unary(x *ast.UnaryExpr) value {
	switch x.Op {
	case token.REF, token.MUT:
		v := e.valueTo(x.X, e.typeOf(x))
		if !e.isPlace(x.X) && needsDrop(e.typeOf(x.X)) { // a borrow written out of a value built here holds what it lends itself
			if depth(e.typeOf(x.X)) < depth(e.typeOf(x)) {
				if x.Op == token.REF {
					e.hold(v.s, e.typeOf(x.X)) // the slot the value was lent from; valueTo holds one it lends mut
				}
			} else {
				e.hold(e.spill(v), e.typeOf(x.X))
			}
		}

		return v
	case token.MUL:
		v := e.expr(x.X)
		if _, owned := e.typeOf(x.X).(*types.Pointer); owned && !e.isPlace(x.X) { // a pointer built here owns its block: held, and freed when the statement ends
			e.hold(e.spill(v), e.typeOf(x.X))
		}

		return e.derefTo(v, e.typeOf(x.X), e.typeOf(x))
	case token.AND:
		elem := e.typeOf(x).(*types.Pointer).Elem()
		cell := e.alloc(elem, x.Pos())
		e.store(e.valueTo(x.X, elem), cell)
		return value{"ptr", cell}
	case token.ARROW:
		return e.receive(x)
	case token.XOR:
		v := e.valueTo(x.X, e.typeOf(x))
		return e.op("xor "+v.typed()+", -1", v.typ)
	case token.SUB:
		t := e.typeOf(x.X)
		if call, ok := e.operatorCall(t, x.Op, true, x.X, nil, x.Pos()); ok {
			return call
		}

		v := e.valueTo(x.X, e.typeOf(x))
		if isFloat(t) {
			return e.op("fneg "+v.typed(), v.typ)
		}

		return e.op("sub "+v.typ+" 0, "+v.s, v.typ)
	case token.NOT:
		v := e.valueTo(x.X, types.Typ[types.Bool])
		return e.op("xor "+v.typed()+", true", v.typ)
	}

	e.unsupported(x.OpPos, "the operator "+x.Op.String())
	return unit
}

// alloc returns the address of a new block on the heap for a value of
// type t.
func (e *Emitter) alloc(t types.Type, pos token.Pos) string {
	e.declare("sn_alloc", "declare ptr @sn_alloc(i64)")
	return e.op("call ptr @sn_alloc(i64 "+strconv.FormatInt(max(e.size(t), 1), 10)+")", "ptr").s
}

// deref returns the address an owned pointer holds, checked first in
// a debug build, which panics on a pointer that moved away.
func (e *Emitter) deref(p value) value {
	if !e.cfg.Debug {
		return p
	}

	e.declare("sn_deref", "declare ptr @sn_deref(ptr)")
	return e.op("call ptr @sn_deref(ptr "+p.s+")", "ptr")
}

// derefTo reads v, a value of the Snello type have, down to the pointer
// depth of want: through a borrow, which is the address of the place,
// and through an owned pointer, which a debug build checks first.
func (e *Emitter) derefTo(v value, have, want types.Type) value {
	for depth(have) > depth(want) {
		switch u := have.(type) {
		case *types.Ref:
			have = u.Elem()
		case *types.Pointer:
			v = e.deref(v)
			have = u.Elem()
		}

		v = e.load(e.irType(have, token.NoPos), v.s)
	}

	return v
}

// binary writes an operator on two values. && and || run their right
// side only when the left leaves the answer open; == and != on a
// composite or a string compare by parts; + on strings concatenates.
func (e *Emitter) binary(x *ast.BinaryExpr) value {
	if x.Op == token.LAND || x.Op == token.LOR {
		return e.logical(x)
	}

	t := lower.ValueType(e.typeOf(x.X))
	if call, ok := e.operatorCall(t, x.Op, false, x.X, []ast.Expr{x.Y}, x.Pos()); ok {
		return call
	}

	if parts := lower.Addends(x, e.typeOf); len(parts) > 2 { // a + b + c: one allocation for the whole
		wants := make([]types.Type, len(parts))
		for i := range wants {
			wants[i] = t
		}

		vals := e.operands(parts, wants)
		pieces := make([]piece, len(parts))
		for i, v := range vals {
			pieces[i] = piece{text: text{v, !e.isRead(parts[i])}}
		}

		return e.concat(pieces, x.Pos())
	}

	var want types.Type = t
	if x.Op != token.ADD {
		want = types.NewRef(t, false) // a comparison only looks at its sides: one built here is held for the statement
	}

	sides := e.operands([]ast.Expr{x.X, x.Y}, []types.Type{want, want})
	l, r := sides[0], sides[1]
	addr := func(v value) string { // a side lent as a view is a value: its address is a slot's
		if depth(want) > 0 {
			return v.s
		}

		return e.spill(v)
	}

	if x.Op == token.EQL || x.Op == token.NEQ {
		if _, basic := t.Underlying().(*types.Basic); !basic || isString(t) {
			eq := e.equal(addr(l), addr(r), t, x.Pos())
			if x.Op == token.NEQ {
				return e.op("xor i1 "+eq.s+", true", "i1")
			}

			return eq
		}
	}

	if isString(t) {
		if x.Op == token.ADD {
			return e.join(text{l, !e.isRead(x.X)}, text{r, !e.isRead(x.Y)}).v // a side built here is freed after the join
		}

		c := e.rt("sn_string_cmp", "i32", l, r)
		name, _ := compare(x.Op, false, false)
		return e.op("icmp "+name+" "+c.typed()+", 0", "i1")
	}

	if x.Op != token.ADD { // the sides were lent: read them
		l, r = e.load(e.irType(t, x.Pos()), l.s), e.load(e.irType(t, x.Pos()), r.s)
	}

	return e.arith(x.Op, l, r, t, x.OpPos)
}

// logical writes a && b or a || b.
func (e *Emitter) logical(x *ast.BinaryExpr) value {
	l := e.valueTo(x.X, types.Typ[types.Bool])
	right, end := e.block("right"), e.block("end")
	left := e.block("left")
	e.jump(left)
	e.label(left) // the block the phi names must be the one that branched
	if x.Op == token.LAND {
		e.branch(l.s, right, end)
	} else {
		e.branch(l.s, end, right)
	}

	e.label(right)
	mark := len(e.fn.temps)
	r := e.valueTo(x.Y, types.Typ[types.Bool])
	e.dropTemps(mark, x.Pos()) // what the right side built is dropped on its own path: on the other, nothing was built
	fromRight := e.block("from.right")
	e.jump(fromRight)
	e.label(fromRight)
	e.jump(end)
	e.label(end)
	short := "false"
	if x.Op == token.LOR {
		short = "true"
	}

	return e.op("phi i1 [ "+short+", %"+left+" ], [ "+r.s+", %"+fromRight+" ]", "i1")
}

// arith writes the operator op on l and r, values of the Snello type t.
func (e *Emitter) arith(op token.Kind, l, r value, t types.Type, pos token.Pos) value {
	if !isInteger(t) && !isFloat(t) && !isBool(t) {
		e.unsupported(pos, "the operator "+op.String()+" on values of type "+t.String())
		return unit
	}

	float := isFloat(t)
	if name, ok := compare(op, float, isUnsigned(t)); ok {
		if float {
			return e.op("fcmp "+name+" "+l.typed()+", "+r.s, "i1")
		}

		return e.op("icmp "+name+" "+l.typed()+", "+r.s, "i1")
	}

	switch op {
	case token.ADD, token.SUB, token.MUL:
		name := map[token.Kind]string{token.ADD: "add", token.SUB: "sub", token.MUL: "mul"}[op]
		if float {
			name = "f" + name
		}

		return e.op(name+" "+l.typed()+", "+r.s, l.typ)
	case token.QUO, token.REM:
		if float {
			name := "fdiv"
			if op == token.REM {
				name = "frem"
			}

			return e.op(name+" "+l.typed()+", "+r.s, l.typ)
		}

		return e.divide(op, l, r, isUnsigned(t))
	case token.AND, token.OR, token.XOR:
		name := map[token.Kind]string{token.AND: "and", token.OR: "or", token.XOR: "xor"}[op]
		return e.op(name+" "+l.typed()+", "+r.s, l.typ)
	case token.AND_NOT:
		mask := e.op("xor "+r.typed()+", -1", r.typ)
		return e.op("and "+l.typed()+", "+mask.s, l.typ)
	case token.SHL, token.SHR:
		return e.shift(op, l, r, isUnsigned(t))
	}

	e.unsupported(pos, "the operator "+op.String())
	return unit
}

// shift writes a shift. A count of the width or more is zero, or
// minus one for a negative number shifted right, as the language's
// wrapping arithmetic implies and as the IR, which leaves such a
// count undefined, does not; a select settles it without a branch.
func (e *Emitter) shift(op token.Kind, l, r value, unsigned bool) value {
	width := strings.TrimPrefix(l.typ, "i")
	if r.typ != l.typ {
		r = e.op("zext "+r.typed()+" to "+l.typ, l.typ)
	}

	big := e.op("icmp uge "+r.typed()+", "+width, "i1")
	safe := e.op("select i1 "+big.s+", "+l.typ+" 0, "+r.typed(), l.typ)
	switch {
	case op == token.SHL:
		v := e.op("shl "+l.typed()+", "+safe.s, l.typ)
		return e.op("select i1 "+big.s+", "+l.typ+" 0, "+v.typed(), l.typ)
	case unsigned:
		v := e.op("lshr "+l.typed()+", "+safe.s, l.typ)
		return e.op("select i1 "+big.s+", "+l.typ+" 0, "+v.typed(), l.typ)
	}

	v := e.op("ashr "+l.typed()+", "+safe.s, l.typ)
	sign := e.op("ashr "+l.typed()+", "+strconv.Itoa(bitsOf(l.typ)-1), l.typ)
	return e.op("select i1 "+big.s+", "+sign.typed()+", "+v.typed(), l.typ)
}

// bitsOf is the width of an integer IR type.
func bitsOf(t string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(t, "i"))
	return n
}

// compare returns the IR predicate of a comparison operator, for a
// float or for an integer of the given signedness, and whether op is
// one. Two floats with a NaN among them are unordered: equal is false
// and not-equal true, as the language says.
func compare(op token.Kind, float, unsigned bool) (string, bool) {
	var names map[token.Kind]string
	switch {
	case float:
		names = map[token.Kind]string{token.EQL: "oeq", token.NEQ: "une", token.LSS: "olt", token.LEQ: "ole", token.GTR: "ogt", token.GEQ: "oge"}
	case unsigned:
		names = map[token.Kind]string{token.EQL: "eq", token.NEQ: "ne", token.LSS: "ult", token.LEQ: "ule", token.GTR: "ugt", token.GEQ: "uge"}
	default:
		names = map[token.Kind]string{token.EQL: "eq", token.NEQ: "ne", token.LSS: "slt", token.LEQ: "sle", token.GTR: "sgt", token.GEQ: "sge"}
	}

	name, ok := names[op]
	return name, ok
}

// divide writes an integer division or remainder. A divisor of zero
// panics, as the language says and as the IR, which leaves it
// undefined, does not; the one quotient that overflows, the smallest
// number divided by minus one, wraps to the smallest number and its
// remainder is zero, which the IR also leaves undefined and a select
// settles without a branch.
func (e *Emitter) divide(op token.Kind, l, r value, unsigned bool) value {
	zero, ok := e.block("div.zero"), e.block("div")
	isZero := e.op("icmp eq "+r.typed()+", 0", "i1")
	e.branch(isZero.s, zero, ok)
	e.label(zero)
	e.panic(e.constString("integer divide by zero"))
	e.label(ok)
	name := map[token.Kind]string{token.QUO: "sdiv", token.REM: "srem"}[op]
	if unsigned {
		name = "u" + name[1:]
		return e.op(name+" "+l.typed()+", "+r.s, l.typ)
	}

	minusOne := e.op("icmp eq "+r.typed()+", -1", "i1")
	safe := e.op("select i1 "+minusOne.s+", "+r.typ+" 1, "+r.typed(), r.typ)
	q := e.op(name+" "+l.typed()+", "+safe.s, l.typ)
	wrapped := value{l.typ, "0"}
	if op == token.QUO {
		wrapped = e.op("sub "+l.typ+" 0, "+l.s, l.typ)
	}

	return e.op("select i1 "+minusOne.s+", "+wrapped.typed()+", "+q.typed(), l.typ)
}

// panic ends the program with the message, a sn_string value.
func (e *Emitter) panic(msg value) {
	e.callWords("sn_panic", msg)
	e.inst("unreachable")
	e.fn.terminated = true
}

// valueTo writes x where a value of type want is expected: with the
// reads through borrows and pointers the checker allowed silently, as
// when a mut borrow or an owned pointer is read as its value, or as
// the address of the place when a borrow is wanted of a value.
func (e *Emitter) valueTo(x ast.Expr, want types.Type) value {
	have := e.typeOf(x)
	if want == nil || have == nil {
		return e.expr(x)
	}

	if tr, ok := want.(*types.Trait); ok {
		if n, ok := have.(*types.Named); ok {
			return e.box(e.expr(x), n, tr, x.Pos()) // a value becoming a trait value
		}
	}

	if depth(have) < depth(want) {
		if !e.isPlace(x) { // a value built here and lent lives in a temporary for the statement
			slot := e.spill(e.expr(x))
			if r, ok := want.(*types.Ref); ok && r.Mut() {
				e.hold(slot, have) // lent mut, as a mut receiver is: no operand writer holds it, as operands does one lent ref
			}

			return value{"ptr", slot}
		}

		return value{"ptr", e.lvalue(x)}
	}

	return e.derefTo(e.expr(x), have, want)
}

// lvalue returns the address of the storage x denotes, about to be
// written or lent mutably: a variable's slot, a field or element of
// one, with a map element made when its key is new, or for a value
// built here a temporary.
func (e *Emitter) lvalue(x ast.Expr) string {
	return e.addr(x, true)
}

// addr is lvalue, with write saying whether a map element indexed on
// the way is made when its key is new or read as the zero value.
func (e *Emitter) addr(x ast.Expr, write bool) string {
	switch x := x.(type) {
	case *ast.Ident:
		if v, ok := e.info.Uses[x].(*types.Var); ok {
			return e.varSlot(v, x.Pos())
		}
	case *ast.ParenExpr:
		return e.addr(x.X, write)
	case *ast.SelectorExpr:
		if f, ok := e.info.Uses[x.Sel].(*types.Var); ok && e.packageOf(x.X) == nil {
			return e.fieldAddr(e.placeOf(x.X, write), lower.ValueType(e.typeOf(x.X)), f, x.Pos())
		}

		if v, ok := e.info.Uses[x.Sel].(*types.Var); ok {
			return "@" + lower.ObjName(v) // a library's variable
		}
	case *ast.IndexExpr:
		return e.index(x, write)
	case *ast.UnaryExpr:
		if x.Op == token.MUL {
			return e.placeOf(x.X, write)
		}
	}

	return e.spill(e.expr(x))
}

// place returns the address of the value x denotes, read through the
// borrows and pointers in front of it, for a field access or a match
// on it; a value built here is held in a temporary and dropped when
// the statement ends.
func (e *Emitter) place(x ast.Expr) string {
	return e.placeOf(x, false)
}

// placeOf is place, making a map element on the way when write says
// so.
func (e *Emitter) placeOf(x ast.Expr, write bool) string {
	if e.isPlace(x) {
		return e.placeAt(e.addr(x, write), e.typeOf(x))
	}

	p, _ := e.scrutinee(x)
	return p
}

// placeAt reads from the address p, holding a value of type t, down
// to the address of the value under its borrows and pointers.
func (e *Emitter) placeAt(p string, t types.Type) string {
	for depth(t) > 0 {
		v := e.load("ptr", p)
		if _, ok := t.(*types.Pointer); ok {
			v = e.deref(v)
		}

		p = v.s
		switch u := t.(type) {
		case *types.Ref:
			t = u.Elem()
		case *types.Pointer:
			t = u.Elem()
		}
	}

	return p
}

// fieldAddr returns the address of the field f of the value of type
// base at the address p: a member of a struct, or, when base is a
// trait, a place in the box reached through the table's accessor.
func (e *Emitter) fieldAddr(p string, base types.Type, f *types.Var, pos token.Pos) string {
	if tr, ok := base.(*types.Trait); ok {
		return e.traitField(p, tr, f)
	}

	st := base.Underlying().(*types.Struct)
	return e.gep(e.irType(base, pos), p, fieldIndex(st, f.Name()))
}

// fieldIndex is the position of the named field in st.
func fieldIndex(st *types.Struct, name string) int {
	for i := 0; i < st.NumFields(); i++ {
		if st.Field(i).Name() == name {
			return i
		}
	}

	return -1
}

// isPlace reports whether x denotes a variable or a part of a value,
// which can be read in place and bound to without a copy. A field, an
// element or what a pointer points at is a place wherever the value
// is: in a variable, behind a borrow, or in the temporary scrutinee
// holds for a value built here, which the statement drops whole, so
// that its part is never copied out and dropped twice.
func (e *Emitter) isPlace(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Ident:
		_, ok := e.info.Uses[x].(*types.Var)
		return ok
	case *ast.ParenExpr:
		return e.isPlace(x.X)
	case *ast.SelectorExpr:
		v, ok := e.info.Uses[x.Sel].(*types.Var)
		if ok && e.packageOf(x.X) != nil {
			return true // a variable of the library, such as os.Args
		}

		return ok && v.IsField()
	case *ast.IndexExpr:
		return true
	case *ast.UnaryExpr:
		return x.Op == token.MUL
	}

	return false
}

// operands writes a list of operands evaluated left to right, each
// converted to the wanted type when wants gives one. A value built
// here that the operation only looks at is held for the statement.
func (e *Emitter) operands(xs []ast.Expr, wants []types.Type) []value {
	parts := make([]value, len(xs))
	for i, x := range xs {
		var want types.Type
		if wants != nil {
			want = wants[i]
		}

		parts[i] = e.valueTo(x, want)
		if e.lent(x, want) { // built here and only lent: dropped when the statement ends
			if want != nil && depth(e.typeOf(x)) < depth(want) {
				e.hold(parts[i].s, e.typeOf(x)) // the slot valueTo lent it from
			} else {
				e.hold(e.spill(parts[i]), e.typeOf(x))
			}
		}
	}

	return parts
}

// lent reports whether x is a value built here that the operation
// only looks at, as a ref parameter or a print does, so that nothing
// else will drop it.
func (e *Emitter) lent(x ast.Expr, want types.Type) bool {
	if u, ok := x.(*ast.UnaryExpr); ok && (u.Op == token.REF || u.Op == token.MUT) {
		return false // a borrow written out holds what it lends itself
	}

	if want != nil {
		r, ok := want.(*types.Ref)
		if !ok || r.Mut() {
			return false
		}
	}

	x = borrowed(x)
	if _, literal := x.(*ast.BasicLit); literal {
		return false // a literal owns nothing
	}

	return !e.isRead(x) && needsDrop(e.typeOf(x))
}

// borrowed returns the value an operand lends: the operand of a ref,
// which may be built there, else the operand itself.
func borrowed(x ast.Expr) ast.Expr {
	if u, ok := x.(*ast.UnaryExpr); ok && u.Op == token.REF {
		return u.X
	}

	return x
}

// scrutinee writes the value a match, ?, ?? or if-binding looks into,
// as a place its parts can be read from and bound to: the expression
// itself when it denotes a place, otherwise a temporary holding it,
// dropped when the statement ends. It returns the address and the type
// of the value there.
func (e *Emitter) scrutinee(x ast.Expr) (string, types.Type) {
	p, t, held := e.consumed(x)
	if held != "" {
		e.hold(held, e.typeOf(x))
	}

	return p, t
}

// consumed is scrutinee for an operation that takes the value apart:
// a value built here, or a place whose value moves into the operation,
// goes into a temporary the caller owns, returned as the third result
// and empty for a place read where it is. The caller moves parts out of
// the temporary, emptying each, and drops the rest.
func (e *Emitter) consumed(x ast.Expr) (string, types.Type, string) {
	t := e.typeOf(x)
	if e.isPlace(x) && !e.info.Moves[x] {
		return e.place(x), lower.ValueType(t), ""
	}

	var tmp string
	if e.isPlace(x) {
		tmp = e.spill(e.moved(x))
	} else {
		tmp = e.spill(e.expr(x))
	}

	return e.placeAt(tmp, t), lower.ValueType(t), tmp
}

// changed writes a place a method is about to change, as scrutinee
// does, except that a map element is made when its key is new.
func (e *Emitter) changed(x ast.Expr) (string, types.Type) {
	if t := e.typeOf(x); e.isPlace(x) {
		return e.placeAt(e.lvalue(x), t), lower.ValueType(t)
	}

	return e.scrutinee(x)
}

// taken writes the move of the value at the address p, of type t, out
// of the consumed scrutinee held, of type heldType: the value is read
// and the scrutinee emptied so that its drop releases nothing twice.
// When nothing was consumed, held is empty and the value is read in
// place.
func (e *Emitter) taken(p string, t types.Type, held string, heldType types.Type, pos token.Pos) value {
	v := e.load(e.irType(t, pos), p)
	if held != "" {
		e.zero(held, heldType, pos)
	}

	return v
}

// inside writes the part of a scrutinee at the address p, of type
// elem, as the type want the operation yields it: its address, when
// the scrutinee was looked into through a borrow and the part is lent
// on, or the value, taken out as taken does.
func (e *Emitter) inside(p string, elem, want types.Type, held string, heldType types.Type, pos token.Pos) value {
	if depth(want) > depth(elem) {
		return value{"ptr", p}
	}

	return e.taken(p, want, held, heldType, pos)
}

// try writes x?: the value inside, after a return of the absent or
// failed case as the function's own result.
func (e *Emitter) try(x *ast.TryExpr) value {
	p, t, held := e.consumed(x.X)
	fail, ok := e.block("try.fail"), e.block("try")
	var val string
	var elem types.Type
	switch u := t.Underlying().(type) {
	case *types.Option:
		var has value
		elem = u.Elem()
		has, val = e.optionParts(e.irType(t, x.Pos()), p, u)
		e.branch(has.s, ok, fail)
		e.label(fail)
		e.leaveFunction(x.Pos())
		e.inst("ret " + e.variantValue(e.fn.sig.Results(), types.NoneVariant, nil, x.Pos()).typed())
	case *types.Result:
		ir := e.irType(t, x.Pos())
		elem = u.Val()
		okFlag := e.load("i1", e.gep(ir, p, 0))
		val = e.gep(ir, p, 1)
		e.branch(okFlag.s, ok, fail)
		e.label(fail)
		err := e.taken(val, u.Err(), held, e.typeOf(x.X), x.Pos())
		e.leaveFunction(x.Pos())
		e.inst("ret " + e.variantValue(e.fn.sig.Results(), types.ErrVariant, []value{err}, x.Pos()).typed())
	}

	e.fn.terminated = true
	e.label(ok)
	return e.inside(val, elem, e.typeOf(x), held, e.typeOf(x.X), x.Pos())
}

// leaveFunction writes the drops a return from inside an expression
// runs: the deferred calls, then every open frame.
func (e *Emitter) leaveFunction(pos token.Pos) {
	e.runDefers()
	e.leave(0, pos)
}

// coalesce writes x ?? else: the value inside when there is one, or
// the else value; an else that leaves, by return, break or continue,
// is written as the statement it is. Through a borrow the result is
// the address of the value inside, which a mut borrow writes through.
func (e *Emitter) coalesce(x *ast.CoalesceExpr) value {
	rt := e.typeOf(x)
	p, t, held := e.consumed(x.X)
	ir := e.irType(t, x.Pos())
	var test value
	var val string
	var elem types.Type
	r, isResult := t.Underlying().(*types.Result)
	if isResult {
		test = e.load("i1", e.gep(ir, p, 0))
		val = e.gep(ir, p, 1)
		elem = r.Val()
	} else {
		u := t.Underlying().(*types.Option)
		test, val = e.optionParts(ir, p, u)
		elem = u.Elem()
	}

	result := e.slot(e.irType(rt, x.Pos()))
	some, els, end := e.block("some"), e.block("else"), e.block("end")
	e.branch(test.s, some, els)
	e.label(some)
	e.store(e.inside(val, elem, rt, held, e.typeOf(x.X), x.Pos()), result)

	e.jump(end)
	e.label(els)
	if held != "" && isResult && needsDrop(r.Err()) {
		e.dropOf(val, r.Err(), x.Pos()) // the error is not looked at
		e.zero(held, e.typeOf(x.X), x.Pos())
	}

	e.node(x.Else, result, rt)
	if !e.fn.terminated {
		e.jump(end)
	}

	e.label(end)
	return e.load(e.irType(rt, x.Pos()), result)
}

// node writes what follows => in a match arm or ?? in a coalesce: an
// expression, whose value goes to the result slot; a block, whose last
// expression does; or a statement that leaves.
func (e *Emitter) node(n ast.Node, result string, want types.Type) {
	switch n := n.(type) {
	case ast.Expr:
		e.assignOrRun(result, n, want)
	case *ast.BlockStmt:
		e.blockValue(n, result, want)
	case ast.Stmt:
		e.stmt(n)
	}
}

// ifExpr writes if c { a } else { b } with a value: both branches
// store into one slot.
func (e *Emitter) ifExpr(x *ast.IfExpr) value {
	rt := e.typeOf(x)
	result := ""
	if yields(rt) {
		result = e.slot(e.irType(rt, x.Pos()))
	}

	end := e.block("end")
	e.ifExprInto(x, result, rt, end)
	e.label(end)
	if result == "" {
		return unit
	}

	return e.load(e.irType(rt, x.Pos()), result)
}

func (e *Emitter) ifExprInto(x *ast.IfExpr, result string, rt types.Type, end string) {
	c := e.scopedBool(x.Cond)
	then, els := e.block("then"), e.block("else")
	e.branch(c.s, then, els)
	e.label(then)
	e.blockValue(x.Then, result, rt)
	if !e.fn.terminated {
		e.jump(end)
	}

	e.label(els)
	switch els := x.Else.(type) {
	case *ast.BlockStmt:
		e.blockValue(els, result, rt)
	case *ast.IfExpr:
		e.ifExprInto(els, result, rt, end)
		return
	}

	if !e.fn.terminated {
		e.jump(end)
	}
}

// scopedBool writes a condition and drops the temporaries evaluating
// it made before the condition is used, so that a test inside a block
// owns nothing.
func (e *Emitter) scopedBool(x ast.Expr) value {
	mark := len(e.fn.temps)
	v := e.valueTo(x, types.Typ[types.Bool]) // read through a borrow of a bool, which the checker allows silently
	e.dropTemps(mark, x.Pos())
	return v
}

// blockValue writes a block whose last expression is its value,
// stored into result when the block yields one.
func (e *Emitter) blockValue(b *ast.BlockStmt, result string, want types.Type) {
	if len(b.List) == 0 {
		return
	}

	e.push(false)
	last := len(b.List) - 1
	e.stmts(b.List[:last])
	if es, ok := b.List[last].(*ast.ExprStmt); ok {
		e.assignOrRun(result, es.X, want)
	} else {
		e.stmt(b.List[last])
	}

	if !e.fn.terminated {
		e.pop(b.Rbrace)
	} else {
		e.fn.frames = e.fn.frames[:len(e.fn.frames)-1]
	}
}

// assignOrRun writes x as the value of result, or as a statement when
// there is no result to store, as for a branch that leaves or an
// expression whose value is ().
func (e *Emitter) assignOrRun(result string, x ast.Expr, want types.Type) {
	e.scoped(x.Pos(), func() {
		v := e.valueTo(x, want)
		switch {
		case e.fn.terminated:
		case result == "" || isNever(e.typeOf(x)):
			e.discard(v, e.typeOf(x), x.Pos())
		default:
			e.store(v, result)
		}
	})
}

// yields reports whether an expression of type t leaves a value to
// hold: not (), and not never, the type of a branch that leaves.
func yields(t types.Type) bool {
	return t != nil && t != types.Unit && !isNever(t)
}

// selector writes a field access through any borrows and pointers in
// front of the struct, or a member of a package.
func (e *Emitter) selector(x *ast.SelectorExpr) value {
	if pkg := e.packageOf(x.X); pkg != nil {
		switch obj := e.info.Uses[x.Sel].(type) {
		case *types.Func:
			if lower.Key(obj) != "" {
				e.unsupported(x.Pos(), "a function the compiler provides, "+pkg.Name()+"."+x.Sel.Name+", as a value")
				return unit
			}

			return e.funcValue(obj, x.Pos())
		case *types.Variant:
			return e.variantValue(e.typeOf(x), obj, nil, x.Pos())
		case *types.Var:
			return e.load(e.irType(e.typeOf(x), x.Pos()), "@"+lower.ObjName(obj))
		}

		return e.load(e.irType(e.typeOf(x), x.Pos()), "@"+lower.ObjName(e.info.Uses[x.Sel]))
	}

	f, ok := e.info.Uses[x.Sel].(*types.Var)
	if !ok {
		e.unsupported(x.Pos(), "methods as values")
		return unit
	}

	base := lower.ValueType(e.typeOf(x.X))
	return e.load(e.irType(e.typeOf(x), x.Pos()), e.fieldAddr(e.place(x.X), base, f, x.Pos()))
}

// tuple writes (a, b) as an aggregate and () as the unit value.
func (e *Emitter) tuple(x *ast.TupleExpr) value {
	if len(x.Elts) == 0 {
		return unit
	}

	t := e.typeOf(x).(*types.Tuple)
	wants := make([]types.Type, t.Len())
	for i := range wants {
		wants[i] = t.At(i)
	}

	ir := e.irType(t, x.Pos())
	out := value{ir, "zeroinitializer"}
	for i, p := range e.operands(x.Elts, wants) {
		out = e.insert(out, p, i)
	}

	return out
}

// compositeLit writes a struct, slice, map or array literal; fields
// left out are zero.
func (e *Emitter) compositeLit(x *ast.CompositeLit) value {
	t := e.typeOf(x)
	ir := e.irType(t, x.Pos())
	switch u := t.Underlying().(type) {
	case *types.Slice:
		return e.sliceLit(x, t, u)
	case *types.Map:
		return e.mapLit(x, t, u)
	case *types.Array:
		wants := make([]types.Type, len(x.Elts))
		for i := range wants {
			wants[i] = u.Elem()
		}

		out := value{ir, "zeroinitializer"}
		for i, p := range e.operands(x.Elts, wants) {
			out = e.insert(out, p, i)
		}

		return out
	case *types.Struct:
		out := value{ir, "zeroinitializer"}
		if hasDrop(t) {
			out = e.insert(out, value{"i1", "true"}, liveIndex(t.(*types.Named)))
		}

		values := make([]ast.Expr, len(x.Elts))
		wants := make([]types.Type, len(x.Elts))
		indices := make([]int, len(x.Elts))
		for i, elt := range x.Elts {
			kv := elt.(*ast.KeyValueExpr)
			key := kv.Key.(*ast.Ident)
			values[i] = kv.Value
			indices[i] = fieldIndex(u, key.Name)
			wants[i] = u.Field(indices[i]).Type()
		}

		for i, p := range e.operands(values, wants) {
			out = e.insert(out, p, indices[i])
		}

		return out
	}

	e.unsupported(x.Pos(), "literals of type "+t.String())
	return unit
}

// variantValue writes a value of an enum, option or Result type built
// by a variant from its arguments.
func (e *Emitter) variantValue(t types.Type, v *types.Variant, args []value, pos token.Pos) value {
	ir := e.irType(t, pos)
	switch u := lower.ValueType(t).Underlying().(type) {
	case *types.Option:
		if v == types.SomeVariant {
			return e.someValue(u, args[0], pos)
		}

		return value{ir, zero(ir)}
	case *types.Result:
		slot := e.slot(ir)
		e.store(value{ir, "zeroinitializer"}, slot)
		if v == types.OkVariant {
			e.store(value{"i1", "true"}, e.gep(ir, slot, 0))
		}

		e.store(args[0], e.gep(ir, slot, 1))
		return e.load(ir, slot)
	case *types.Enum:
		slot := e.slot(ir)
		e.store(value{ir, "zeroinitializer"}, slot)
		e.store(value{"i32", strconv.Itoa(v.Index())}, e.gep(ir, slot, 0))
		if hasDrop(lower.ValueType(t)) {
			e.store(value{"i1", "true"}, e.gep(ir, slot, 2))
		}

		if len(args) > 0 {
			vt := e.variantType(u.Variant(v.Index()).Fields(), pos)
			payload := e.gep(ir, slot, 1)
			for i, a := range args {
				e.store(a, e.gep(vt, payload, i))
			}
		}

		return e.load(ir, slot)
	}

	e.unsupported(pos, "values of type "+t.String())
	return unit
}

// someValue builds Some(v) for the option type u.
func (e *Emitter) someValue(u *types.Option, v value, pos token.Pos) value {
	if nullable(u) {
		return v
	}

	ir := e.irType(u, pos)
	out := e.insert(value{ir, "zeroinitializer"}, value{"i1", "true"}, 0)
	return e.insert(out, v, 1)
}

// optionParts reads the option of type u at the address p: whether it
// holds a value, and the address of the value.
func (e *Emitter) optionParts(ir, p string, u *types.Option) (value, string) {
	if nullable(u) {
		v := e.load("ptr", p)
		return e.op("icmp ne ptr "+v.s+", null", "i1"), p
	}

	return e.load("i1", e.gep(ir, p, 0)), e.gep(ir, p, 1)
}

// storeOption writes Some(v) at the address p, of the option type u.
func (e *Emitter) storeOption(p string, u *types.Option, v value, pos token.Pos) {
	e.store(e.someValue(u, v, pos), p)
}

// variantFields lists the types a variant's arguments must have.
func (e *Emitter) variantFields(t types.Type, v *types.Variant) []types.Type {
	switch u := lower.ValueType(t).Underlying().(type) {
	case *types.Option:
		return []types.Type{u.Elem()}
	case *types.Result:
		if v == types.OkVariant {
			return []types.Type{u.Val()}
		}

		return []types.Type{u.Err()}
	}

	fields := variantOf(t, v).Fields()
	ts := make([]types.Type, len(fields))
	for i, f := range fields {
		ts[i] = f.Type()
	}

	return ts
}
