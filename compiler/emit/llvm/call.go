// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

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

	return e.callNamed(name, sig, recv, args, pos), true
}

// callNamed writes a call of the function name, of signature sig, on
// the receiver and arguments given.
func (e *Emitter) callNamed(name string, sig *types.Signature, recv ast.Expr, args []ast.Expr, pos token.Pos) value {
	wants, values := e.callOperands(recv, args, sig)
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

// callOperands evaluates a call's receiver, when there is one, and its
// arguments, each converted to the type the signature wants, and
// returns those types with the values.
func (e *Emitter) callOperands(recv ast.Expr, args []ast.Expr, sig *types.Signature) ([]types.Type, []value) {
	wants := make([]types.Type, 0, len(args)+1)
	exprs := make([]ast.Expr, 0, len(args)+1)
	if recv != nil {
		wants = append(wants, sig.Recv().Type())
		exprs = append(exprs, recv)
	}

	for i, a := range args {
		var want types.Type
		if i < len(sig.Params()) {
			want = sig.Params()[i].Type()
		}

		wants = append(wants, want)
		exprs = append(exprs, a)
	}

	return wants, e.operands(exprs, wants)
}

// call writes a conversion, a built-in, a variant constructor, a
// library function, a method, or a call of a function of the program.
func (e *Emitter) call(x *ast.CallExpr) value {
	if _, ok := x.Fun.(*ast.TypeExpr); ok { // a conversion to a type written out, []byte(s)
		return e.conversion(x.Args[0], e.typeOf(x))
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

	sig := fn.Type().(*types.Signature)
	if id, ok := sel.X.(*ast.Ident); ok && id.Name == "super" {
		return e.superCall(x, sel, fn)
	}

	switch lower.ValueType(sig.Recv().Type()).(type) {
	case *types.TypeParam:
		return e.boundCall(x, sel, sig.Recv().Type())
	case *types.Trait:
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

	return e.callNamed(name, sig, sel.X, x.Args, x.Pos())
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
	return e.callNamed(name, sig, sel.X, x.Args, x.Pos())
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

	name, sig := e.methodNameOf(sel.Sel, named, sel.Sel.Name, x.Pos())
	if sig == nil {
		return unit
	}

	return e.callNamed(name, sig, sel.X, x.Args, x.Pos())
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
		return e.callNamed(name, sig, nil, x.Args, x.Pos())
	}

	return e.callNamed(lower.FuncName(fn), fn.Type().(*types.Signature), nil, x.Args, x.Pos())
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
	}

	v := e.derefTo(e.operands([]ast.Expr{x}, nil)[0], e.typeOf(x), from)
	ir := e.irType(to, x.Pos())
	switch {
	case !(isInteger(from) || isFloat(from)) || !(isInteger(to) || isFloat(to)):
		if e.irType(from, x.Pos()) == ir {
			return value{ir, v.s} // the same shape under another name
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
	case isFloat(from):
		if isUnsigned(to) {
			return e.op("fptoui "+v.typed()+" to "+ir, ir)
		}

		return e.op("fptosi "+v.typed()+" to "+ir, ir)
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
	var decl, call []string
	sret := ""
	if ret == "%sn_string" || ret == "%sn_slice" {
		sret = e.slot(ret)
		decl = append(decl, e.abi.result(ret))
		call = append(call, e.abi.result(ret)+" "+sret)
	}

	for _, a := range args {
		switch a.typ {
		case "%sn_string", "%sn_slice":
			slot := e.spill(a)
			decl = append(decl, e.abi.words(a.typ))
			call = append(call, e.abi.words(a.typ)+" "+slot)
		default:
			decl = append(decl, a.typ+e.abi.small(a.typ))
			call = append(call, a.typ+e.abi.small(a.typ)+" "+a.s)
		}
	}

	result := ret
	if sret != "" {
		result = "void"
	} else if ext := e.abi.small(ret); ext != "" {
		result = ext[1:] + " " + ret
	}

	e.declare(name, "declare "+result+" @"+name+"("+strings.Join(decl, ", ")+")")
	text := "call " + result + " @" + name + "(" + strings.Join(call, ", ") + ")"
	switch {
	case sret != "":
		e.inst(text)
		return e.load(ret, sret)
	case ret == "void":
		e.inst(text)
		return unit
	}

	return e.op(text, ret)
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
