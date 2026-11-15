// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// snCaller is the runtime's SN_CALLER, INT64_MIN + 1, the capacity of
// the slice a call lays out in its frame for a variadic ...T parameter:
// the function owns its elements but not its buffer.
const snCaller = "-9223372036854775807"

// Calls: of the program's functions and methods, of generic instances,
// through a bound or a trait value, of the operators a declared type
// defines, conversions, and calls into the runtime under the
// processor's C convention.

// operatorCall writes an operator on a value of a declared type that
// lists the operator's trait as a call of the trait's method, the left
// operand the receiver: a + b is Add(a, b). ok is false when t has no
// such method, so the operator is written as the IR's.
func (e *Emitter) operatorCall(t types.Type, op token.Kind, unary bool, recv ast.Expr, args []ast.Expr, pos token.Pos) (value, bool) {
	fn := types.OperatorMethod(t, op, unary)
	if fn == nil {
		return unit, false
	}

	name, sig := e.methodNameOf(nil, t.(*types.Named), fn.Name(), pos)
	if sig == nil {
		return unit, true
	}

	return e.callNamed(name, sig, recv, args, false, pos), true
}

// callNamed writes a call of the function name, of signature sig, on
// the receiver and arguments given, the last a slice for the variadic
// parameter when spread.
func (e *Emitter) callNamed(name string, sig *types.Signature, recv ast.Expr, args []ast.Expr, spread bool, pos token.Pos) value {
	wants, values := e.callOperands(recv, args, spread, sig)
	return e.callValues(name, sig, wants, values, pos)
}

// callValues writes a call of the function name, of signature sig,
// with the values already computed for the parameter types wants. A
// function of the program takes its values as the IR aggregates they
// are; one the runtime defines is called under the C convention
// (abi.go), with a ref parameter given the value it lends, since C has
// no borrows: the runtime reads a ref string as a sn_string.
func (e *Emitter) callValues(name string, sig *types.Signature, wants []types.Type, values []value, pos token.Pos) value {
	if e.runtime[name] {
		args := make([]value, len(values))
		for i, v := range values {
			args[i] = v
			if r, ok := wants[i].(*types.Ref); ok && !r.Mut() {
				args[i] = e.derefTo(v, r, r.Elem())
			}
		}

		return e.rt(name, e.resultType(sig.Results(), pos), args...)
	}

	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = v.typed()
	}

	call := "call " + e.resultType(sig.Results(), pos) + " @" + name + "(" + strings.Join(parts, ", ") + ")"
	if sig.Results() == types.Unit {
		e.inst(call)
		return unit
	}

	return e.op(call, e.irType(sig.Results(), pos))
}

// unframe gives the slice at slot p, laid out in a caller's frame for a
// variadic ...T parameter, a buffer of its own, of elements of type
// elem: for a function that may keep it past its call, and for a call
// that outlives the frame, a spawn's.
func (e *Emitter) unframe(p string, elem types.Type) {
	e.declare("sn_slice_unframe", "declare void @sn_slice_unframe(ptr, i64)")
	e.inst("call void @sn_slice_unframe(ptr " + p + ", i64 " + e.sizeOf(elem).s + ")")
}

// unframeLast gives the slice a call packs for a variadic ...T
// parameter, the last of values, a buffer of its own, for a call that
// outlives the caller's frame, a spawn's.
func (e *Emitter) unframeLast(sig *types.Signature, spread bool, values []value) {
	kind, elem := sig.VariadicParam()
	if kind != types.VariadicOwned || spread || len(values) == 0 {
		return
	}

	slot := e.spill(values[len(values)-1])
	e.unframe(slot, e.sub(elem))
	values[len(values)-1] = e.load("%sn_slice", slot)
}

// callOperands evaluates a call's receiver, when there is one, and its
// arguments, each converted to the type the signature wants, and
// returns those types with the values. The arguments a variadic
// parameter of the program's takes one by one, when the call does not
// spread a slice, are evaluated in their turn with the others, each to
// its element's type, a lent one as the borrow it is, so that a
// temporary it lends is dropped after the call, and then made the one
// value the parameter takes (variadicValue).
func (e *Emitter) callOperands(recv ast.Expr, args []ast.Expr, spread bool, sig *types.Signature) ([]types.Type, []value) {
	kind, elem := sig.VariadicParam()
	packed := !spread && (kind == types.VariadicOwned || kind == types.VariadicLent)
	fixed := len(sig.Params())
	var each types.Type
	if packed {
		fixed--
		elem = e.sub(elem)
		each = elem
		if kind == types.VariadicLent {
			each = types.NewRef(elem, false)
		}
	}

	wants := make([]types.Type, 0, len(args)+1)
	exprs := make([]ast.Expr, 0, len(args)+1)
	if recv != nil {
		wants = append(wants, sig.Recv().Type())
		exprs = append(exprs, recv)
	}

	for i, a := range args {
		var want types.Type
		switch {
		case packed && i >= fixed:
			want = each
		case i < len(sig.Params()):
			want = sig.Params()[i].Type()
		}

		wants = append(wants, want)
		exprs = append(exprs, a)
	}

	values := e.operands(exprs, wants)
	if !packed {
		return wants, values
	}

	at := len(exprs) - (len(args) - fixed) // the first of the packed arguments
	param := e.sub(sig.Params()[fixed].Type())
	pos := sig.Params()[fixed].Pos()
	if len(args) > fixed {
		pos = args[fixed].Pos()
	}

	values = append(values[:at], e.variadicValue(kind, elem, values[at:], pos))
	return append(wants[:at], param), values
}

// variadicValue makes the value a variadic parameter of the program's
// takes from the arguments' values, laid out in an array in a slot of
// the caller's frame, for the call: for ...T a slice of capacity
// SN_CALLER, whose elements, moved in, the function owns and drops, and
// whose buffer nothing frees; for ...ref T a slice over the arguments'
// values, each the argument's own, lent, which nothing drops: read
// through the borrow, or the borrow itself for a string or a slice,
// which a ref borrow views. Neither allocates.
func (e *Emitter) variadicValue(kind types.VariadicKind, elem types.Type, values []value, pos token.Pos) value {
	if len(values) == 0 {
		return value{"%sn_slice", "zeroinitializer"}
	}

	n := value{"i64", strconv.Itoa(len(values))}
	et := e.irType(elem, pos)
	out := e.slot("%sn_slice")
	room := n
	if kind == types.VariadicOwned {
		room = value{"i64", snCaller}
	}

	base := value{"ptr", e.slot("[" + n.s + " x " + et + "]")}
	e.store(base, e.gep("%sn_slice", out, 0))
	e.store(n, e.gep("%sn_slice", out, 1))
	e.store(room, e.gep("%sn_slice", out, 2))

	for i, v := range values {
		if kind == types.VariadicLent && !viewed(elem) {
			v = e.load(et, v.s) // the value the borrow reaches
		}

		e.store(v, e.elem(et, base.s, strconv.Itoa(i)))
	}

	return e.load("%sn_slice", out)
}

// call writes a conversion, a built-in, a variant constructor, a
// library function, a method, or a call of a function of the program.
func (e *Emitter) call(x *ast.CallExpr) value {
	if _, ok := x.Fun.(*ast.TypeExpr); ok { // a conversion to a type written out, []byte(s)
		return e.conversion(x.Args[0], e.typeOf(x))
	}

	if p := lower.PartUsed(e.info, x.Fun); p != nil { // c(for f := range fields(c) { value }), a variant built from its fields
		return e.variantBuild(x, p)
	}

	if id, ok := x.Fun.(*ast.Ident); ok {
		switch obj := e.info.Uses[id].(type) {
		case *types.Builtin:
			return e.builtinCall(x, obj)
		case *types.TypeName:
			return e.conversion(x.Args[0], e.typeOf(x)) // the call's type: a type parameter's name stands for the type it was given
		case *types.Func:
			if key := lower.Key(obj); key != "" {
				return e.libraryCall(x, key)
			}

			e.locate(x, obj)
			return e.funcCall(x, id, obj)
		case *types.Variant:
			return e.variantValue(e.typeOf(x), obj, e.operands(x.Args, e.variantFields(e.typeOf(x), obj)), x.Pos())
		}
	}

	sel, ok := x.Fun.(*ast.SelectorExpr)
	if !ok {
		if sig, ok := lower.ValueType(e.typeOf(x.Fun)).(*types.Signature); ok {
			return e.callValue(x, sig)
		}

		e.unsupported(x.Pos(), "calling a value of type "+e.typeOf(x.Fun).String())
		return unit
	}

	if pkg := e.packageOf(sel.X); pkg != nil {
		if fn, ok := e.info.Uses[sel.Sel].(*types.Func); ok {
			if key := lower.Key(fn); key != "" {
				return e.libraryCall(x, key)
			}

			e.locate(x, fn)
		}

		if v, ok := e.info.Uses[sel.Sel].(*types.Variant); ok {
			return e.variantValue(e.typeOf(x), v, e.operands(x.Args, e.variantFields(e.typeOf(x), v)), x.Pos())
		}

		if _, ok := e.info.Uses[sel.Sel].(*types.TypeName); ok { // a conversion to a library's type, time.Duration(n)
			return e.conversion(x.Args[0], e.typeOf(x))
		}

		return e.funcCall(x, sel.Sel, e.info.Uses[sel.Sel].(*types.Func)) // a library function, instantiated when generic
	}

	fn, ok := e.info.Uses[sel.Sel].(*types.Func)
	if !ok {
		if sig, ok := lower.ValueType(e.typeOf(x.Fun)).(*types.Signature); ok && e.info.Uses[sel.Sel] != nil {
			return e.callValue(x, sig) // a field holding a function value
		}

		return e.methodCall(x, sel) // a predeclared method, which the checker records no object for
	}

	if fn.Static() {
		return e.staticCall(x, sel)
	}

	sig := fn.Type().(*types.Signature)
	if id, ok := sel.X.(*ast.Ident); ok && id.Name == "super" {
		return e.superCall(x, sel, fn)
	}

	switch lower.ValueType(sig.Recv().Type()).(type) {
	case *types.TypeParam:
		return e.boundCall(x, sel, sig.Recv().Type())
	case *types.Trait:
		sig = e.sub(sig).(*types.Signature) // Iterator<T> in generic code is this instance's Iterator<int>
		return e.traitCall(x, sel, sig.Recv().Type(), sig)
	}

	named, ok := lower.ValueType(sig.Recv().Type()).(*types.Named)
	if !ok {
		return e.methodCall(x, sel)
	}

	name, sig := e.methodNameOf(sel.Sel, named, sel.Sel.Name, x.Pos())
	if sig == nil {
		return unit
	}

	return e.callNamed(name, sig, sel.X, x.Args, x.Ellipsis.IsValid(), x.Pos())
}

// staticCall writes T.F(args), a function of a type: the function of
// the type T stands for in the instance being written, Point.Parse, or
// a generic type's instance's, called directly with no receiver.
func (e *Emitter) staticCall(x *ast.CallExpr, sel *ast.SelectorExpr) value {
	name, sig := e.staticCallee(sel, x.Pos())
	if sig == nil {
		return unit
	}

	return e.callNamed(name, sig, nil, x.Args, x.Ellipsis.IsValid(), x.Pos())
}

// staticCallee names the function T.F a call or a spawn of sel runs,
// with its signature in the instance being written, or nil after an
// unsupported case.
func (e *Emitter) staticCallee(sel *ast.SelectorExpr, pos token.Pos) (string, *types.Signature) {
	owner, ok := lower.ValueType(e.sub(e.typeOf(sel.X))).(*types.Named)
	var fn *types.Func
	if ok {
		fn = owner.Method(sel.Sel.Name)
	}

	if fn == nil {
		e.unsupported(pos, "the function "+sel.Sel.Name+" of "+e.typeOf(sel.X).String())
		return "", nil
	}

	sig := e.sub(fn.Type()).(*types.Signature)
	name := lower.MethodName(owner, fn.Name())
	if e.generics[fn.Origin()] != nil {
		targs := append([]types.Type(nil), owner.TypeArgs()...)
		if inst, ok := e.info.Instances[sel.Sel]; ok {
			for _, t := range inst.TypeArgs {
				targs = append(targs, e.sub(t))
			}

			sig = e.sub(inst.Type).(*types.Signature)
		}

		name = e.request(fn, targs)
	}

	return name, sig
}

// superCall writes super.M(args): the next definition of M in the
// implementing type's resolution order, after the trait whose default
// is being written, resolved for that type and called directly with
// the method's own receiver.
func (e *Emitter) superCall(x *ast.CallExpr, sel *ast.SelectorExpr, fn *types.Func) value {
	recv := e.sub(fn.Type().(*types.Signature).Recv().Type())
	named, ok := lower.ValueType(recv).(*types.Named)
	if !ok {
		e.unsupported(x.Pos(), "super on "+recv.String())
		return unit
	}

	target := named.SuperMethod(fn)
	if target == nil {
		e.unsupported(x.Pos(), "super."+sel.Sel.Name+" on "+named.String())
		return unit
	}

	name, sig := e.callInstance(nil, target, named)
	return e.callNamed(name, sig, sel.X, x.Args, x.Ellipsis.IsValid(), x.Pos())
}

// boundCall writes a call of a method a bound declares, x.Area() with
// x of a type parameter bounded by Shape, whose receiver type is
// paramRecv: the instance being written has a type argument in the
// parameter's place, and the call goes to that type's own method.
func (e *Emitter) boundCall(x *ast.CallExpr, sel *ast.SelectorExpr, paramRecv types.Type) value {
	recv := e.sub(paramRecv)
	if _, ok := lower.ValueType(recv).(*types.Trait); ok { // the type argument is a trait: its values dispatch
		return e.traitCall(x, sel, recv, e.sub(e.info.Uses[sel.Sel].(*types.Func).Type()).(*types.Signature))
	}

	named, ok := lower.ValueType(recv).(*types.Named)
	if !ok || named.Method(sel.Sel.Name) == nil && isPredeclaredKind(named.Underlying()) {
		return e.methodCall(x, sel) // a predeclared type's own method, Cmp on an int or Clone on a string, which a type declared over one has as its own
	}

	if named.Method(sel.Sel.Name) == nil { // a standard trait the type has from its parts without listing it, Eq on an enum
		if call, ok := e.standardMethod(x, sel, named); ok {
			return call
		}
	}

	name, sig := e.methodNameOf(sel.Sel, named, sel.Sel.Name, x.Pos())
	if sig == nil {
		return unit
	}

	return e.callNamed(name, sig, sel.X, x.Args, x.Ellipsis.IsValid(), x.Pos())
}

// methodNameOf returns the name of the method the declared type t has
// under name, and its signature: an own method, an inherited default
// or a generic instance's method, asked for as a call on a value of t
// would ask; id is the call's selector when there is one, for the type
// arguments of a generic method. A method the type does not have is
// reported, with a nil signature.
func (e *Emitter) methodNameOf(id *ast.Ident, t *types.Named, name string, pos token.Pos) (string, *types.Signature) {
	fn := t.Method(name)
	if fn == nil {
		e.unsupported(pos, "the method "+name+" of a trait on "+t.String())
		return "", nil
	}

	sig := fn.Type().(*types.Signature)
	if fn.Derived() { // written from the type's parts, as the functions the backend has for them
		switch name {
		case "Eq":
			return e.comparer(t, pos), sig
		case "Cmp":
			return e.orderer(t, pos), sig
		case "Hash":
			return e.hasher(t, pos), sig
		case "Clone":
			return e.cloner(t, pos), sig
		case "Error":
			return e.messenger(t, pos), sig
		case "Source":
			return e.sourcer(t, pos), sig
		}
	}

	if e.generics[fn.Origin()] != nil {
		return e.callInstance(id, fn, sig.Recv().Type())
	}

	return lower.MethodName(sig.Recv().Type(), name), sig
}

// funcCall writes the call x of the declared function fn, named by
// id: its instance for a generic function, else the function itself.
func (e *Emitter) funcCall(x *ast.CallExpr, id *ast.Ident, fn *types.Func) value {
	if e.generics[fn] != nil {
		name, sig := e.callInstance(id, fn, nil)
		return e.callNamed(name, sig, nil, x.Args, x.Ellipsis.IsValid(), x.Pos())
	}

	return e.callNamed(lower.FuncName(fn), fn.Type().(*types.Signature), nil, x.Args, x.Ellipsis.IsValid(), x.Pos())
}

// packageOf returns the package an identifier names, or nil.
func (e *Emitter) packageOf(x ast.Expr) *types.PkgName {
	if id, ok := x.(*ast.Ident); ok {
		if pkg, ok := e.info.Uses[id].(*types.PkgName); ok {
			return pkg
		}
	}

	return nil
}

// conversion writes T(x): between number types a change of width
// extends by the source's sign or truncates and a change of kind
// rounds toward zero or converts exactly as the IR defines; between a
// string and its bytes a copy through the runtime.
func (e *Emitter) conversion(x ast.Expr, to types.Type) value {
	from := lower.ValueType(e.typeOf(x))
	switch {
	case isString(from) && isByteSlice(to):
		return e.stringToBytes(e.looked(x, from))
	case isByteSlice(from) && isString(to):
		return e.bytesToString(e.looked(x, from))
	case isRuneSlice(from) && isString(to):
		return e.rt("sn_string_from_runes", "%sn_string", e.looked(x, from))
	case isRune(from) && isString(to):
		return e.rt("sn_string_from_rune", "%sn_string", e.looked(x, from))
	}

	v := e.derefTo(e.operands([]ast.Expr{x}, lower.Taken(from))[0], e.typeOf(x), from)
	ir := e.irType(to, x.Pos())
	switch {
	case !(isInteger(from) || isFloat(from)) || !(isInteger(to) || isFloat(to)):
		if e.irType(from, x.Pos()) == ir {
			return value{ir, v.s} // the same shape under another name
		}

		if _, ok := to.Underlying().(*types.Struct); ok { // a struct and a type declared over it: the same fields, under two names the IR keeps apart
			p := e.slot(v.typ)
			e.store(v, p)
			return e.load(ir, p)
		}

		e.unsupported(x.Pos(), "converting "+from.String()+" to "+to.String())
		return unit
	case isFloat(from) && isFloat(to):
		switch {
		case bits(from) < bits(to):
			return e.op("fpext "+v.typed()+" to "+ir, ir)
		case bits(from) > bits(to):
			return e.op("fptrunc "+v.typed()+" to "+ir, ir)
		}
	case isFloat(from): // the fraction dropped, a float past the type's range its nearest end, a NaN zero: the IR's plain conversion leaves a float out of range undefined
		fn := "llvm.fptosi.sat." + ir + "." + v.typ
		if isUnsigned(to) {
			fn = "llvm.fptoui.sat." + ir + "." + v.typ
		}

		e.declare(fn, "declare "+ir+" @"+fn+"("+v.typ+")")
		return e.op("call "+ir+" @"+fn+"("+v.typed()+")", ir)
	case isFloat(to):
		if isUnsigned(from) {
			return e.op("uitofp "+v.typed()+" to "+ir, ir)
		}

		return e.op("sitofp "+v.typed()+" to "+ir, ir)
	case bits(from) < bits(to):
		if isUnsigned(from) {
			return e.op("zext "+v.typed()+" to "+ir, ir)
		}

		return e.op("sext "+v.typed()+" to "+ir, ir)
	case bits(from) > bits(to):
		return e.op("trunc "+v.typed()+" to "+ir, ir)
	}

	return value{ir, v.s} // the same bits under another name
}

// isRune reports whether t is rune, i32, or a type declared over it.
func isRune(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Kind() == types.Int32
}

// isRuneSlice reports whether t is []rune.
func isRuneSlice(t types.Type) bool {
	s, ok := t.Underlying().(*types.Slice)
	return ok && isRune(s.Elem())
}

// isByteSlice reports whether t is []byte.
func isByteSlice(t types.Type) bool {
	s, ok := t.Underlying().(*types.Slice)
	if !ok {
		return false
	}

	b, ok := s.Elem().Underlying().(*types.Basic)
	return ok && b.Kind() == types.Uint8
}

// rt calls the runtime function name with the arguments, under the
// processor's calling convention (abi.go): a three-word struct is
// passed and returned through a slot, a small scalar is extended by
// the caller. ret is the IR type of the result, or void.
func (e *Emitter) rt(name, ret string, args ...value) value {
	sret := ""
	if ret == "%sn_string" || ret == "%sn_slice" {
		sret = e.slot(ret)
	}

	result := ret
	if sret != "" {
		result = "void"
	} else if ext := e.abi.small(ret); ext != "" {
		result = ext[1:] + " " + ret
	}

	_, declared := e.decls[name]
	var decl, call bytes.Buffer // the call, and the declaration the first call writes
	if !declared {
		put(&decl, "declare ", result, " @", name, "(")
	}

	put(&call, "call ", result, " @", name, "(")
	if sret != "" {
		put(&decl, e.abi.result(ret))
		put(&call, e.abi.result(ret), " ", sret)
	}

	for i, a := range args {
		if i > 0 || sret != "" {
			decl.WriteString(", ")
			call.WriteString(", ")
		}

		switch a.typ {
		case "%sn_string", "%sn_slice":
			put(&decl, e.abi.words(a.typ))
			put(&call, e.abi.words(a.typ), " ", e.spill(a))
		default:
			put(&decl, a.typ, e.abi.small(a.typ))
			put(&call, a.typ, e.abi.small(a.typ), " ", a.s)
		}
	}

	call.WriteByte(')')
	if !declared {
		decl.WriteByte(')')
		e.declare(name, decl.String())
	}

	switch {
	case sret != "":
		e.inst(call.String())
		return e.load(ret, sret)
	case ret == "void":
		e.inst(call.String())
		return unit
	}

	return e.op(call.String(), ret)
}

// callWords calls a runtime function of no result taking one
// three-word value.
func (e *Emitter) callWords(name string, v value) {
	e.rt(name, "void", v)
}

// isPredeclaredKind reports whether t is a predeclared kind, a number,
// bool, string, slice, map, array, tuple or option, whose standard
// methods a type declared over it has as its own, time.Duration's Eq
// and Cmp or a named slice's Clone; a struct or an enum has what it
// lists.
func isPredeclaredKind(t types.Type) bool {
	switch t.(type) {
	case *types.Struct, *types.Enum:
		return false
	}

	return true
}
