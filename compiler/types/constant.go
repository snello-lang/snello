// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"errors"
	"math"
	"math/big"
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The kinds of a constant's value, and the largest shift count the
// folding computes: a shift by more moves every bit of a 64-bit value
// out, so a larger count is read as this one.
const (
	ConstUnknown ConstKind = iota // no value: the expression had an error, reported where it was found
	ConstInt
	ConstFloat
	ConstBool
	ConstString

	maxShift = 128
)

// ConstKind says which part of a Constant holds its value.
type ConstKind int

// A pendingDefault is a struct field's default, x, of the type typ,
// checked in the file that declares the field.
type pendingDefault struct {
	x    ast.Expr
	typ  Type
	file int
}

// A pendingConst is what checks a top-level constant: the declaration,
// the index of its name, the file it is in, and how far it has gone:
// being checked, so that a use met now is a cycle, or checked.
type pendingConst struct {
	decl     *ast.VarDecl
	index    int
	file     int
	checking bool
	done     bool
}

// A Constant is the value of a constant expression as the checker
// folds it: an integer, exact; a float, held as the type holds it, an
// f32 rounded to one; a boolean; or a string.
type Constant struct {
	kind ConstKind
	i    *big.Int
	f    float64
	b    bool
	s    string
}

// Kind returns which part of c holds its value.
func (c Constant) Kind() ConstKind { return c.kind }

// Int returns the value of an integer constant.
func (c Constant) Int() *big.Int { return c.i }

// Float returns the value of a float constant.
func (c Constant) Float() float64 { return c.f }

// Bool returns the value of a boolean constant.
func (c Constant) Bool() bool { return c.b }

// Str returns the value of a string constant.
func (c Constant) Str() string { return c.s }

// text writes the value for a message.
func (c Constant) text() string {
	switch c.kind {
	case ConstInt:
		return c.i.String()
	case ConstFloat:
		return strconv.FormatFloat(c.f, 'g', -1, 64)
	case ConstBool:
		return strconv.FormatBool(c.b)
	case ConstString:
		return strconv.Quote(c.s)
	}

	return "?"
}

func intConst(i *big.Int) Constant       { return Constant{kind: ConstInt, i: i} }
func floatConst(f float64) Constant      { return Constant{kind: ConstFloat, f: f} }
func boolConst(b bool) Constant          { return Constant{kind: ConstBool, b: b} }
func stringConst(s string) Constant      { return Constant{kind: ConstString, s: s} }
func failed() (Constant, bool)           { return Constant{}, false }
func folded(v Constant) (Constant, bool) { return v, true }

// constValue checks x, the value of a constant, against the type t the
// declaration gives it, or none, as what, folds it, and records the value of x
// and of each field a struct literal gives. It returns the operand the
// check gave and the value; a struct constant's value is unknown, as
// only its fields have one. Nothing is folded after the check reported
// an error, which would be reported again; and a value that cannot be
// folded is always reported, here if nowhere else, so that no constant
// reaches the backends without one.
func (c *Checker) constValue(x ast.Expr, t Type, what string) (operand, Constant) {
	if bad := c.notConstantValue(x); bad != nil {
		c.errorf(bad.Pos(), exprText(bad)+" is not constant")
		return invalidOperand, Constant{}
	}

	defer c.foldInto(c.folding)()
	before := len(c.errors)
	op := c.expr(x, t)
	if op.mode != invalid && t != nil {
		c.assign(op, t, x.Pos(), what)
	}

	if op.mode == invalid || len(c.errors) > before {
		c.unreported(x, before)
		return invalidOperand, Constant{}
	}

	if _, ok := unparen(x).(*ast.CompositeLit); ok {
		if !constType(op.typ) {
			c.errorf(x.Pos(), "a constant is a number, a bool, a string or a struct of them, not a "+op.typ.String())
			return invalidOperand, Constant{}
		}

		if !c.foldFields(x) {
			c.unreported(x, before)
			return invalidOperand, Constant{}
		}

		return op, Constant{}
	}

	v, ok := c.fold(x)
	if !ok {
		c.unreported(x, before)
		return invalidOperand, Constant{}
	}

	c.recordValue(x, v)
	return op, v
}

// foldInto makes folding a map of no entries for the expression about
// to be checked, and returns what puts back outer, the map of the
// expression whose check needed this one, or none. The one map the
// checker keeps is emptied and reused, and a map is made only for a
// constant checked while another is.
func (c *Checker) foldInto(outer map[ast.Expr]Type) func() {
	switch {
	case outer != nil:
		c.folding = make(map[ast.Expr]Type)
	case c.foldTypes != nil:
		clear(c.foldTypes)
		c.folding = c.foldTypes
	default:
		c.foldTypes = make(map[ast.Expr]Type)
		c.folding = c.foldTypes
	}

	return func() { c.folding = outer }
}

// unreported refuses x as not constant when its check or its folding
// failed and nothing has been reported since before.
func (c *Checker) unreported(x ast.Expr, before int) {
	if len(c.errors) == before {
		c.errorf(x.Pos(), exprText(x)+" is not constant")
	}
}

// constType reports whether a struct constant may have the type t: a
// struct whose fields are numbers, bools, strings or such structs.
func constType(t Type) bool {
	switch u := t.Underlying().(type) {
	case *Basic:
		return u.info&(IsNumeric|IsBoolean|IsString) != 0
	case *Struct:
		for _, f := range u.fields {
			if !constType(f.typ) {
				return false
			}
		}

		return true
	}

	return false
}

// arrayLen reads an array length: a constant expression of an integer
// type, at least zero.
func (c *Checker) arrayLen(e ast.Expr) int64 {
	if bad := c.notConstant(e); bad != nil {
		c.errorf(bad.Pos(), "array length must be a non-negative integer constant: "+exprText(bad)+" is not constant")
		return 0
	}

	defer c.foldInto(c.folding)()
	before := len(c.errors)
	if op := c.expr(e, nil); op.mode == invalid || len(c.errors) > before {
		c.unreported(e, before)
		return 0
	}

	v, ok := c.fold(e)
	if !ok {
		c.unreported(e, before)
		return 0
	}

	if v.kind != ConstInt || v.i.Sign() < 0 || !v.i.IsInt64() {
		c.errorf(e.Pos(), "array length must be a non-negative integer constant, not "+v.text())
		return 0
	}

	return v.i.Int64()
}

// foldFields folds the field values of the struct literal x, a struct
// constant's value, and of any struct literal inside it, and reports
// whether every one has its value.
func (c *Checker) foldFields(x ast.Expr) bool {
	all := true
	for _, elt := range unparen(x).(*ast.CompositeLit).Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			elt = kv.Value
		}

		if _, ok := unparen(elt).(*ast.CompositeLit); ok {
			all = c.foldFields(elt) && all
		} else if v, ok := c.fold(elt); ok {
			c.recordValue(elt, v)
		} else {
			all = false
		}
	}

	return all
}

// notConstant returns the first part of x that is not constant, or nil
// when x is a constant expression: a literal, the name of a constant,
// an operator over constant expressions, or a conversion of one. A name
// that is not declared is left to the check, which reports it.
func (c *Checker) notConstant(x ast.Expr) ast.Expr {
	switch x := unparen(x).(type) {
	case *ast.BasicLit:
		return nil
	case *ast.Ident:
		switch c.resolve(x.Name).(type) {
		case *Const, nil:
			return nil
		}
	case *ast.SelectorExpr:
		if _, ok := c.packageObject(x).(*Const); ok {
			return nil
		}
	case *ast.UnaryExpr:
		if x.Op == token.SUB || x.Op == token.NOT || x.Op == token.XOR {
			return c.notConstant(x.X)
		}
	case *ast.BinaryExpr:
		if bad := c.notConstant(x.X); bad != nil {
			return bad
		}

		return c.notConstant(x.Y)
	case *ast.CallExpr:
		if len(x.Args) == 1 && c.isConstantType(x.Fun) {
			return c.notConstant(x.Args[0])
		}
	}

	return x
}

// isConstantType reports whether x names a number, string or bool type,
// T or pkg.T, a type declared over one among them, so that a call of it
// is a conversion a constant expression may make: Celsius(21.5),
// json.Name("host").
func (c *Checker) isConstantType(x ast.Expr) bool {
	var obj Object
	switch x := unparen(x).(type) {
	case *ast.Ident:
		obj = c.resolve(x.Name)
	case *ast.SelectorExpr:
		obj = c.packageObject(x)
	}

	tn, ok := obj.(*TypeName)
	if !ok || tn.typ == nil {
		return false
	}

	c.resolveEarly(tn.typ)
	return hasInfo(tn.typ, IsNumeric|IsString|IsBoolean)
}

// notConstantValue is notConstant for a constant's value, which may
// also be a struct literal whose fields are constant.
func (c *Checker) notConstantValue(x ast.Expr) ast.Expr {
	lit, ok := unparen(x).(*ast.CompositeLit)
	if !ok {
		return c.notConstant(x)
	}

	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			elt = kv.Value
		}

		if bad := c.notConstantValue(elt); bad != nil {
			return bad
		}
	}

	return nil
}

// fold computes the value of x, a constant expression checked already
// without an error, in the arithmetic of its type, which the check
// recorded in folding. It reports what makes the value no value of the
// type, and returns false after an error.
func (c *Checker) fold(x ast.Expr) (Constant, bool) {
	t := c.folding[x]
	switch x := x.(type) {
	case *ast.ParenExpr:
		return c.fold(x.X)
	case *ast.BasicLit:
		if t == nil {
			return failed() // the check refused it
		}

		v, ok := literalValue(x)
		if !ok {
			return failed()
		}

		return c.represent(v, t, x)
	case *ast.Ident:
		return c.constOf(x, c.resolve(x.Name))
	case *ast.SelectorExpr:
		return c.constOf(x, c.packageObject(x))
	case *ast.UnaryExpr:
		return c.foldUnary(x, t)
	case *ast.BinaryExpr:
		return c.foldBinary(x, t)
	case *ast.CallExpr:
		return c.foldConversion(x, t)
	}

	return failed()
}

// constOf returns the value of the constant obj, which x names, true
// and false among them, or false for one whose value had an error. A
// struct constant has no value of its own, only its fields have one,
// and is refused as a part of an expression.
func (c *Checker) constOf(x ast.Expr, obj Object) (Constant, bool) {
	k, ok := obj.(*Const)
	if !ok {
		return failed()
	}

	if k.pos == token.NoPos && (k.name == "true" || k.name == "false") {
		return folded(boolConst(k.name == "true"))
	}

	if k.val.kind == ConstUnknown && k.typ != nil && k.typ != Typ[Invalid] {
		c.errorf(x.Pos(), exprText(x)+" is not constant: a struct constant is no part of a constant expression, which is made of numbers, bools and strings")
	}

	return k.val, k.val.kind != ConstUnknown
}

// literalValue reads a literal: an integer or a character exactly, a
// float as the double its text rounds to, which is what the backends
// write, infinite when it is too large for one, which represent
// refuses, and a string.
func literalValue(lit *ast.BasicLit) (Constant, bool) {
	switch lit.Kind {
	case token.INT:
		if n, err := strconv.ParseUint(lit.Value, 0, 64); err == nil { // base 0 reads 0x, 0b, 0o, a leading 0 and _
			return intConst(new(big.Int).SetUint64(n)), true
		}

		i, ok := new(big.Int).SetString(lit.Value, 0) // past 64 bits
		return intConst(i), ok
	case token.FLOAT:
		f, err := strconv.ParseFloat(lit.Value, 64)
		return floatConst(f), err == nil || errors.Is(err, strconv.ErrRange)
	case token.CHAR:
		r, _, _, err := strconv.UnquoteChar(lit.Value[1:len(lit.Value)-1], '\'')
		return intConst(big.NewInt(int64(r))), err == nil
	case token.STRING:
		s, err := strconv.Unquote(lit.Value)
		return stringConst(s), err == nil
	}

	return failed()
}

// foldUnary computes -x, ^x and !x. A literal after a minus sign is
// read as it is written and negated before its type is asked to hold
// it, so -128 is an i8.
func (c *Checker) foldUnary(x *ast.UnaryExpr, t Type) (Constant, bool) {
	var v Constant
	var ok bool
	if lit, isLit := unparen(x.X).(*ast.BasicLit); isLit && x.Op == token.SUB {
		v, ok = literalValue(lit)
	} else {
		v, ok = c.fold(x.X)
	}

	if !ok || t == nil {
		return failed()
	}

	switch {
	case x.Op == token.NOT && v.kind == ConstBool:
		return folded(boolConst(!v.b))
	case x.Op == token.SUB && v.kind == ConstInt:
		return c.represent(intConst(new(big.Int).Neg(v.i)), t, x)
	case x.Op == token.SUB && v.kind == ConstFloat:
		return c.represent(floatConst(-v.f), t, x)
	case x.Op == token.XOR && v.kind == ConstInt:
		if hasInfo(t, IsUnsigned) { // the bits of the type's width flipped: its largest value less x
			_, most := intRange(t)
			return c.represent(intConst(new(big.Int).Sub(most, v.i)), t, x)
		}

		return c.represent(intConst(new(big.Int).Not(v.i)), t, x)
	}

	return failed()
}

// foldBinary computes x op y, whose operands have one type, the check
// made sure: a comparison gives a bool, and every other operator a
// value of the operands' type.
func (c *Checker) foldBinary(x *ast.BinaryExpr, t Type) (Constant, bool) {
	a, ok := c.fold(x.X)
	if !ok {
		return failed()
	}

	b, ok := c.fold(x.Y)
	if !ok || t == nil || a.kind != b.kind {
		return failed()
	}

	switch x.Op {
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
		return folded(boolConst(compare(a, x.Op, b)))
	case token.LAND:
		return folded(boolConst(a.b && b.b))
	case token.LOR:
		return folded(boolConst(a.b || b.b))
	}

	switch a.kind {
	case ConstString:
		if x.Op == token.ADD {
			return folded(stringConst(a.s + b.s))
		}
	case ConstFloat:
		return c.foldFloat(x, t, a.f, b.f)
	case ConstInt:
		return c.foldInt(x, t, a.i, b.i)
	}

	return failed()
}

// foldFloat computes a float operation as the processor does: the
// exact result rounded to the type, f64 or f32. An explicit conversion
// rounds each result, so no two operations are fused into one.
func (c *Checker) foldFloat(x *ast.BinaryExpr, t Type, a, b float64) (Constant, bool) {
	if (x.Op == token.QUO) && b == 0 {
		c.errorf(x.OpPos, "division by zero")
		return failed()
	}

	var r float64
	if isFloat32(t) {
		fa, fb := float32(a), float32(b)
		switch x.Op {
		case token.ADD:
			r = float64(float32(fa + fb))
		case token.SUB:
			r = float64(float32(fa - fb))
		case token.MUL:
			r = float64(float32(fa * fb))
		case token.QUO:
			r = float64(float32(fa / fb))
		default:
			return failed()
		}
	} else {
		switch x.Op {
		case token.ADD:
			r = float64(a + b)
		case token.SUB:
			r = float64(a - b)
		case token.MUL:
			r = float64(a * b)
		case token.QUO:
			r = float64(a / b)
		default:
			return failed()
		}
	}

	return c.represent(floatConst(r), t, x)
}

// foldInt computes an integer operation exactly, then holds the result
// to the type: a division truncates toward zero and a remainder takes
// the sign of the dividend, as at run time.
func (c *Checker) foldInt(x *ast.BinaryExpr, t Type, a, b *big.Int) (Constant, bool) {
	r := new(big.Int)
	switch x.Op {
	case token.ADD:
		r.Add(a, b)
	case token.SUB:
		r.Sub(a, b)
	case token.MUL:
		r.Mul(a, b)
	case token.QUO, token.REM:
		if b.Sign() == 0 {
			c.errorf(x.OpPos, "division by zero")
			return failed()
		}

		if x.Op == token.QUO {
			r.Quo(a, b)
		} else {
			r.Rem(a, b)
		}
	case token.AND:
		r.And(a, b)
	case token.OR:
		r.Or(a, b)
	case token.XOR:
		r.Xor(a, b)
	case token.AND_NOT:
		r.AndNot(a, b)
	case token.SHL, token.SHR:
		if b.Sign() < 0 {
			c.errorf(x.Y.Pos(), "negative shift count "+b.String())
			return failed()
		}

		n := uint(maxShift)
		if b.IsUint64() && b.Uint64() < maxShift {
			n = uint(b.Uint64())
		}

		if x.Op == token.SHL {
			r.Lsh(a, n) // a count past maxShift leaves a value no type holds, unless it is zero
		} else {
			r.Rsh(a, n) // rounds toward minus infinity, as an arithmetic shift does
		}
	default:
		return failed()
	}

	return c.represent(intConst(r), t, x)
}

// foldConversion computes T(x) between number types, which keeps the
// value: an integer the target cannot hold, a float with a fraction
// converted to an integer type, and a float too large for its target
// are refused, and an integer converted to a float rounds to the
// nearest. A conversion to anything but a number type is no constant
// expression, which notConstant says.
func (c *Checker) foldConversion(x *ast.CallExpr, t Type) (Constant, bool) {
	v, ok := c.fold(x.Args[0])
	if !ok || t == nil {
		return failed()
	}

	switch {
	case hasInfo(t, IsString) || hasInfo(t, IsBoolean): // a string stays a string, a bool a bool, under the type's name
		if hasInfo(t, IsString) != (v.kind == ConstString) || hasInfo(t, IsBoolean) != (v.kind == ConstBool) {
			c.errorf(x.Pos(), "constant "+v.text()+" does not convert to "+t.String()+": a constant conversion keeps a string a string and a bool a bool")
			return failed()
		}

		return c.represent(v, t, x)
	case v.kind == ConstString || v.kind == ConstBool:
		return failed() // a conversion of a string or a bool to a number type, which the check refused
	case hasInfo(t, IsInteger) && v.kind == ConstFloat:
		if v.f != math.Trunc(v.f) {
			c.errorf(x.Pos(), "constant "+v.text()+" truncated to "+t.String())
			return failed()
		}

		i, _ := new(big.Float).SetFloat64(v.f).Int(nil)
		return c.represent(intConst(i), t, x)
	case hasInfo(t, IsFloat) && v.kind == ConstInt:
		f := new(big.Float).SetInt(v.i)
		if isFloat32(t) {
			f32, _ := f.Float32() // one rounding, to the nearest f32, as the processor converts
			return c.represent(floatConst(float64(f32)), t, x)
		}

		f64, _ := f.Float64()
		return c.represent(floatConst(f64), t, x)
	}

	return c.represent(v, t, x)
}

// represent holds v to the type t, and reports a value outside it: an
// integer to the type's range, a float rounded to an f32 for an f32 and
// refused when it is infinite, an integer literal in a float's place
// made the float.
func (c *Checker) represent(v Constant, t Type, x ast.Expr) (Constant, bool) {
	switch {
	case hasInfo(t, IsInteger) && v.kind == ConstInt:
		least, most := intRange(t)
		if v.i.Cmp(least) < 0 || v.i.Cmp(most) > 0 {
			c.errorf(x.Pos(), "constant "+v.text()+" overflows "+t.String())
			return failed()
		}

		return folded(v)
	case hasInfo(t, IsFloat) && (v.kind == ConstFloat || v.kind == ConstInt):
		if v.kind == ConstInt { // 2 in 2.5 * 2, read as the float it stands for
			f, _ := new(big.Float).SetInt(v.i).Float64()
			v = floatConst(f)
		}

		if isFloat32(t) {
			v.f = float64(float32(v.f))
		}

		if math.IsInf(v.f, 0) {
			c.errorf(x.Pos(), "constant "+exprText(x)+" overflows "+t.String())
			return failed()
		}

		return folded(v)
	case hasInfo(t, IsBoolean) && v.kind == ConstBool, hasInfo(t, IsString) && v.kind == ConstString:
		return folded(v)
	}

	c.errorf(x.Pos(), exprText(x)+" is not constant: a constant is a number, a bool or a string")
	return failed()
}

// compare reports whether a op b holds, for two values of one kind.
func compare(a Constant, op token.Kind, b Constant) bool {
	var n int
	switch a.kind {
	case ConstInt:
		n = a.i.Cmp(b.i)
	case ConstFloat:
		switch {
		case a.f < b.f:
			n = -1
		case a.f > b.f:
			n = 1
		}
	case ConstString:
		switch {
		case a.s < b.s:
			n = -1
		case a.s > b.s:
			n = 1
		}
	case ConstBool:
		if a.b != b.b {
			n = 1 // only == and != are defined on bools
		}
	}

	switch op {
	case token.EQL:
		return n == 0
	case token.NEQ:
		return n != 0
	case token.LSS:
		return n < 0
	case token.LEQ:
		return n <= 0
	case token.GTR:
		return n > 0
	}

	return n >= 0
}

// intRange returns the least and the largest value of the integer type
// t, which no caller changes.
func intRange(t Type) (least, most *big.Int) {
	r := intRanges[t.Underlying().(*Basic).kind]
	return r[0], r[1]
}

// intRanges holds the least and the largest value of each integer
// kind, computed once.
var intRanges = func() (ranges [Uint + 1][2]*big.Int) {
	one := big.NewInt(1)
	for _, b := range Typ[Int8 : Uint+1] {
		width := uint(64)
		switch b.kind {
		case Int8, Uint8:
			width = 8
		case Int16, Uint16:
			width = 16
		case Int32, Uint32:
			width = 32
		}

		if b.info&IsUnsigned != 0 {
			ranges[b.kind] = [2]*big.Int{new(big.Int), new(big.Int).Sub(new(big.Int).Lsh(one, width), one)}
			continue
		}

		most := new(big.Int).Sub(new(big.Int).Lsh(one, width-1), one)
		ranges[b.kind] = [2]*big.Int{new(big.Int).Neg(new(big.Int).Add(most, one)), most}
	}

	return ranges
}()

// isFloat32 reports whether t is f32 or a type declared over it.
func isFloat32(t Type) bool {
	b, ok := t.Underlying().(*Basic)
	return ok && b.kind == Float32
}

// recordValue records the value of a constant expression, which the
// backends write in its place.
func (c *Checker) recordValue(x ast.Expr, v Constant) {
	if c.info != nil {
		c.info.Values[x] = v
	}
}

// collectConsts notes every top-level constant of the package's files,
// in the order of the files and of their declarations, each checked
// the first time a type or another constant needs it, as an array
// length does, or by checkConsts.
func (c *Checker) collectConsts(fs []*ast.File) {
	n := 0
	for _, f := range fs {
		for _, d := range f.Decls {
			if d, ok := d.(*ast.VarDecl); ok && d.Const {
				n += len(d.Names)
			}
		}
	}

	if n == 0 {
		return
	}

	pending := make([]pendingConst, 0, n)
	c.constOrder = make([]*Const, 0, n)
	for i, f := range fs {
		for _, d := range f.Decls {
			d, ok := d.(*ast.VarDecl)
			if !ok || !d.Const {
				continue
			}

			for j, name := range d.Names {
				k, ok := c.pkg.scope.Lookup(name.Name).(*Const)
				if name.Name == "_" { // declared nowhere, and checked all the same
					k, ok = NewConst(name.Pos(), name.Name, nil, Constant{}), true
					c.recordDef(name, k)
				}

				if ok && k.pos == name.Pos() {
					pending = append(pending, pendingConst{decl: d, index: j, file: i})
					c.constOrder = append(c.constOrder, k)
				}
			}
		}
	}

	if len(pending) == 0 {
		return
	}

	c.consts = make(map[*Const]*pendingConst, len(pending)) // made once every record is in place, so that none moves
	for i, k := range c.constOrder {
		c.consts[k] = &pending[i]
	}
}

// checkDefaults checks each struct field's default as a constant's
// value of the field's type, in the file that declares the field, once
// every constant has its value: a default is folded, and its value
// recorded, as a constant's is. A refused one stays the field's
// default, so a literal that leaves the field out is not refused for
// it a second time; the program builds no further.
func (c *Checker) checkDefaults() {
	for _, d := range c.fieldDefaults {
		c.enter(d.file)
		c.constValue(d.x, d.typ, "field default")
	}

	c.fieldDefaults = nil
}

// checkConsts checks the top-level constants nothing has needed yet,
// in the order they are declared.
func (c *Checker) checkConsts() {
	for _, k := range c.constOrder {
		c.checkConst(k)
	}
}

// checkConst checks the top-level constant k, once, in the file that
// declares it, wherever the check that needs it is: what is being
// checked there is put aside and taken back after. That is the file,
// its scope and its table, and what an expression's check sets while
// it walks one; a constant is needed only while the types, the
// constants and the signatures are checked, before any body, so the
// state of a body's check is empty whenever one is. A constant met
// again while it is being checked needs itself, and is refused with
// the chain of names that leads back to it.
func (c *Checker) checkConst(k *Const) {
	p := c.consts[k]
	switch {
	case p == nil, p.done:
		return
	case p.checking:
		c.constCycle(k)
		return
	}

	p.checking = true
	at, folding := c.here(), c.folding
	callee, wantType, inner, negated, names := c.callee, c.wantType, c.inner, c.negated, c.names
	c.callee, c.wantType, c.inner, c.negated, c.names = nil, nil, false, nil, nil
	c.enter(p.file)
	c.constPath = append(c.constPath, k)
	d := p.decl
	var declared Type
	if d.Type != nil {
		declared = c.typ(d.Type)
		c.resolveEarly(declared)
	}

	switch {
	case len(d.Values) == 0:
		c.errorf(k.pos, "const "+k.name+" needs a value")
		k.typ = Typ[Invalid]
	case len(d.Values) != len(d.Names):
		if p.index == 0 {
			c.errorf(d.Pos(), "assignment mismatch: "+strconv.Itoa(len(d.Names))+" names but "+strconv.Itoa(len(d.Values))+" values")
		}

		k.typ = Typ[Invalid]
	default:
		c.constInit(k, d.Values[p.index], declared)
	}

	c.constPath = c.constPath[:len(c.constPath)-1]
	c.restore(at)
	c.folding = folding
	c.callee, c.wantType, c.inner, c.negated, c.names = callee, wantType, inner, negated, names
	p.checking, p.done = false, true
}

// constCycle refuses the constants from k, which needs itself, to the
// one being checked, and leaves each with no type, so that no use of
// them is reported again.
func (c *Checker) constCycle(k *Const) {
	at := len(c.constPath) - 1
	for at > 0 && c.constPath[at] != k {
		at--
	}

	chain := c.constPath[at:]
	msg := "initialization cycle: " + k.name + " refers to itself"
	if len(chain) > 1 {
		msg = "initialization cycle: "
		for i, from := range chain {
			to := k
			if i+1 < len(chain) {
				to = chain[i+1]
			}

			if i > 0 {
				msg += ", "
			}

			msg += from.name + " refers to " + to.name
		}
	}

	c.errorf(k.pos, msg)
	for _, in := range chain {
		in.typ = Typ[Invalid]
	}
}

// constInit gives the constant k the type and the value of x, its value
// in its declaration, under the type declared for it, or none.
func (c *Checker) constInit(k *Const, x ast.Expr, declared Type) {
	op, v := c.constValue(x, declared, "declaration")
	if k.typ == Typ[Invalid] { // refused as part of a cycle while its value was checked
		return
	}

	if op.mode == invalid {
		k.typ = Typ[Invalid]
		return
	}

	k.typ = declared
	if declared == nil {
		k.typ = inferred(op)
	}

	k.val = v
}

// localConsts checks a const declaration inside a body: each value
// before any of the names exists, as the right side cannot see them,
// and then the names, each with its type and its value.
func (c *Checker) localConsts(d *ast.VarDecl, declared Type) {
	if len(d.Values) == 0 {
		c.declareVars(c.scope, d, Typ[Invalid])
		c.errorf(d.Names[0].Pos(), "const "+d.Names[0].Name+" needs a value")
		return
	}

	if len(d.Values) != len(d.Names) {
		c.declareVars(c.scope, d, Typ[Invalid])
		c.errorf(d.Pos(), "assignment mismatch: "+strconv.Itoa(len(d.Names))+" names but "+strconv.Itoa(len(d.Values))+" values")
		return
	}

	ks := make([]*Const, len(d.Names))
	for i, x := range d.Values {
		ks[i] = &Const{}
		c.constInit(ks[i], x, declared)
	}

	c.declareVars(c.scope, d, nil)
	for i, name := range d.Names {
		if k, ok := c.scope.Lookup(name.Name).(*Const); ok && k.pos == name.Pos() {
			k.typ, k.val = ks[i].typ, ks[i].val
		}
	}
}

// collectUninferred notes the package variables whose type their value
// gives, which have none until their declaration is checked: a use of
// one before then is refused rather than read with no type.
func (c *Checker) collectUninferred(fs []*ast.File) {
	for _, f := range fs {
		for _, d := range f.Decls {
			if d, ok := d.(*ast.VarDecl); ok && !d.Const && d.Type == nil {
				for _, name := range d.Names {
					if v, ok := c.pkg.scope.Lookup(name.Name).(*Var); ok && v.pos == name.Pos() {
						if c.uninferred == nil {
							c.uninferred = make(map[*Var]bool)
						}

						c.uninferred[v] = true
					}
				}
			}
		}
	}
}
