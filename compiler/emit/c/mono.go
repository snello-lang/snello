// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/types"
)

// A generic declaration is written once per set of type arguments the
// program uses it with, as a C type or function of its own, named
// after the arguments: List<int> is List_int and Map<int, string> is
// Map_int_string. The checker records the type arguments of every
// call of a generic function or method; a generic type's instances
// are the types the program's values have. The emitter walks a
// generic body with a substitution in effect, so every type it reads
// from the checker's tables has the arguments in place of the
// parameters, and a call inside the body to another generic asks for
// that instance in turn. Instances are written after the program's
// own functions, from a worklist, until no new one is asked for; the
// prototypes written first let any order of definition compile.
//
// A trait's default method is a generic method over Self, and goes
// the same way: one copy per implementing type that takes it, named
// after the type and the trait, Circle_Shape_Describe, since a type
// may need the default behind the one it took, through super.

// An instance is one copy of a generic function or method to write:
// its declaration, the type arguments of its receiver's type and
// then its own, and its C name.
type instance struct {
	decl  *ast.FuncDecl
	fn    *types.Func
	targs []types.Type
	name  string
}

// sub returns t with the type arguments of the instance being written
// in place of its parameters; outside an instance, t itself.
func (e *Emitter) sub(t types.Type) types.Type {
	if e.subst == nil || t == nil {
		return t
	}

	return types.Subst(t, e.subst)
}

// typeOf returns the type the checker gave x, with the type arguments
// of the instance being written in place.
func (e *Emitter) typeOf(x ast.Expr) types.Type {
	return e.sub(e.info.Types[x])
}

// generic reports whether a function declaration is a generic function
// or a method of a generic type, which is written per instance rather
// than once, and keeps its declaration for the instances to come.
func (e *Emitter) generic(d *ast.FuncDecl, fn *types.Func) bool {
	sig := fn.Type().(*types.Signature)
	if sig.TypeParams() == nil && len(recvParams(sig)) == 0 {
		return false
	}

	e.generics[fn], e.files[d] = d, e.file
	return true
}

// recvParams returns the type parameters a method's receiver stands
// for: those of the declared generic type it names, or Self for a
// trait's default method; nil for a function or a plain type.
func recvParams(sig *types.Signature) []*types.TypeParam {
	if recv := sig.Recv(); recv != nil {
		switch t := lower.ValueType(recv.Type()).(type) {
		case *types.Named:
			return t.TypeParams()
		case *types.TypeParam:
			return append([]*types.TypeParam{t}, t.Bounds()[0].TypeParams()...) // Self, then the trait's own
		}
	}

	return nil
}

// selfMethod reports whether fn is a trait's default method, whose
// receiver is Self.
func selfMethod(fn *types.Func) bool {
	recv := fn.Type().(*types.Signature).Recv()
	if recv == nil {
		return false
	}

	_, ok := lower.ValueType(recv.Type()).(*types.TypeParam)
	return ok
}

// request asks for the instance of the generic function or method fn
// with the given type arguments, the receiver's type's first, and
// returns its C name. The instance is written once, later.
func (e *Emitter) request(fn *types.Func, targs []types.Type) string {
	origin := fn.Origin()
	sig := origin.Type().(*types.Signature)
	name := lower.FuncName(origin)
	own := targs
	if recv := sig.Recv(); recv != nil {
		k := len(recvParams(sig))
		own = targs[k:]
		if selfMethod(origin) {
			trait := lower.ValueType(recv.Type()).(*types.TypeParam).Bounds()[0]
			name = lower.MethodName(targs[0], trait.Obj().Name()+"_"+origin.Name()) // the implementing type's copy of the default
		} else {
			name = lower.MethodName(types.Instantiate(lower.ValueType(recv.Type()).(*types.Named), targs[:k]), origin.Name())
		}
	}

	for _, t := range own {
		name += "_" + lower.Mangle(t)
	}

	if !e.done[name] {
		e.done[name] = true
		e.work = append(e.work, instance{e.generics[origin], origin, targs, name})
	}

	return name
}

// instances writes every instance asked for, including the ones the
// instances themselves ask for.
func (e *Emitter) instances() {
	for len(e.work) > 0 {
		inst := e.work[0]
		e.work = e.work[1:]
		e.writeInstance(inst)
	}
}

// writeInstance writes one copy of a generic function or method, with
// its substitution in effect for the body.
func (e *Emitter) writeInstance(inst instance) {
	sig := inst.fn.Type().(*types.Signature)
	tparams := append(recvParams(sig), sig.TypeParams()...)
	outer := e.subst
	e.subst = make(map[*types.TypeParam]types.Type, len(tparams))
	for i, p := range tparams {
		e.subst[p] = inst.targs[i]
	}

	outerFile := e.file
	e.file = e.files[inst.decl]
	e.writeFunction(inst.name, e.sub(sig).(*types.Signature), inst.decl.Body, inst.decl.Pos())
	e.subst, e.file = outer, outerFile
}

// callInstance writes the C name of the generic function or method a
// call uses, from the type arguments the checker inferred for it, with
// the instance being written's own arguments in place. recv is the
// receiver's type for a method: for a trait's default it is the one
// leading type argument, for a generic type's method its own type
// arguments are the leading ones. id is the name at the call, or nil
// for a call the emitter makes up.
func (e *Emitter) callInstance(id *ast.Ident, fn *types.Func, recv types.Type) (string, *types.Signature) {
	var targs []types.Type
	if recv != nil {
		t := lower.ValueType(e.sub(recv))
		if selfMethod(fn.Origin()) {
			targs = append(targs, t)
			if from := fn.FromTrait(); from != nil { // the arguments of the trait instance the type inherited the default from
				for _, a := range from.TypeArgs() {
					if n, ok := t.(*types.Named); ok {
						a = n.Subst(a)
					}

					targs = append(targs, e.sub(a))
				}
			}
		} else if n, ok := t.(*types.Named); ok {
			targs = append(targs, n.TypeArgs()...)
		}
	}

	sig := e.sub(fn.Type()).(*types.Signature)
	if inst, ok := e.info.Instances[id]; ok {
		for _, t := range inst.TypeArgs {
			targs = append(targs, e.sub(t))
		}

		sig = e.sub(inst.Type).(*types.Signature)
	}

	return e.request(fn, targs), sig
}

// variantOf returns the variant of the enum type t that corresponds to
// v: v itself, or for an instance of a generic enum the instance's
// variant, whose fields have the arguments in place.
func variantOf(t types.Type, v *types.Variant) *types.Variant {
	if en, ok := lower.ValueType(t).Underlying().(*types.Enum); ok && v.Parent() != nil && v.Parent().TypeParams() != nil {
		return en.Variant(v.Index())
	}

	return v
}
