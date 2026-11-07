// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "github.com/snello-lang/snello/compiler/ast"

// checkBinding checks a function a package of the program's own
// declares without a body: a binding to C under the function's derived
// symbol, as _docs/design/08-packages.md section 6 has it. It has no
// receiver and no type parameter, since C has neither, and each
// parameter and result is of a type the runtime's ABI passes already:
// a number or a bool by value, a ref string or a ref []byte as a view,
// a mut []byte as a buffer to fill, and a mut number or bool as an
// out-parameter. A C library's handle crosses as an int the package
// wraps in a struct of its own with a Drop method, since a struct with
// Drop carries a flag the compiler sets and C could not. Anything else
// is refused with its type named.
func (c *Checker) checkBinding(d *ast.FuncDecl, sig *Signature) {
	if d.Recv != nil {
		c.errorf(d.Name.Pos(), "cannot bind a method to C: bind a function and write the method over it")
		return
	}

	if len(sig.tparams) > 0 {
		c.errorf(d.Name.Pos(), "cannot bind a generic function to C")
		return
	}

	for i, p := range sig.params {
		if !c.crosses(p.typ, true) {
			c.errorf(d.Params[i].Pos(), "cannot cross to C: "+p.typ.String()+", in parameter "+p.Name())
		}
	}

	if sig.results != Unit && !c.crosses(sig.results, false) {
		c.errorf(d.Results[0].Pos(), "cannot cross to C: "+sig.results.String()+", in the result")
	}
}

// crosses reports whether a value of type t crosses to C as a
// parameter, or as a result when param is false, under the rules
// checkBinding states.
func (c *Checker) crosses(t Type, param bool) bool {
	switch t := t.(type) {
	case *Basic:
		return plain(t)
	case *Ref:
		if !param {
			return false
		}

		switch e := t.elem.(type) {
		case *Basic:
			if t.mut {
				return plain(e) // an out-parameter
			}

			return e.kind == String // a view
		case *Slice:
			b, ok := e.elem.(*Basic)
			return ok && b.kind == Byte // a view, or a buffer to fill
		}
	}

	return false
}

// plain reports whether a basic type crosses to C by value: a number
// or a bool, never a string, which crosses as a view of its bytes. The
// numbers are the kinds declared from Int8 to Float64, in that order.
func plain(b *Basic) bool {
	return b.kind == Bool || b.kind >= Int8 && b.kind <= Float64
}
