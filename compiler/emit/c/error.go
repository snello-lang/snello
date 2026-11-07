// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// The Error trait's derived methods, its chain, and the printing of
// a trait value. A type that lists Error and writes no Error() has
// the message derived: the value as %v prints it, which is the
// stringer of its structure. A type that writes no Source() has it
// derived as None. %+v prints the chain: the message, then each
// source's message after a colon and a space, asked of each source
// through its table, since a source is a trait value. A trait value
// of Error or of fmt.Stringer prints as its message through its table
// as well. ? passes an error into the function's error type where the
// types differ: wrapped in the variant the checker chose, or boxed as
// a trait value.

// messenger returns the function behind a derived Error(): the
// stringer of the type's structure, taking the value as a ref method
// does.
func (e *Emitter) messenger(t *types.Named, pos token.Pos) string {
	return e.stringer(t, pos)
}

// sourcer writes the function behind a derived Source(): None,
// whatever the value.
func (e *Emitter) sourcer(t *types.Named, pos token.Pos) string {
	name := "sn_src_" + lower.Mangle(t)
	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	opt := e.cType(types.NewOption(types.ErrorTrait), pos)
	put(&e.protos, "static ", opt, " ", name, "(", e.cType(t, pos), " v);\n")
	put(&e.helpers, "static ", opt, " ", name, "(", e.cType(t, pos), " v) {\n\t(void)v;\n\treturn (", opt, "){.has = false};\n}\n\n")
	return name
}

// traitText writes the function printing a trait value of Error or of
// fmt.Stringer as its message, through its table. The value is read
// twice, for its table and its data, so a function takes it once.
func (e *Emitter) traitText(tr *types.Trait, pos token.Pos) string {
	name := "sn_msg_" + lower.Mangle(tr)
	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	ct := e.cType(tr, pos)
	put(&e.protos, "static sn_string ", name, "(", ct, " v);\n")
	put(&e.helpers, "static sn_string ", name, "(", ct, " v) {\n\treturn v.tab->", lower.Ident(messageMethod(tr)), "(v.data);\n}\n\n")
	return name
}

// messageMethod names the method a trait value prints through: Error
// for a trait refining Error, String for one refining fmt.Stringer.
func messageMethod(tr *types.Trait) string {
	if tr.Refines(types.ErrorTrait) {
		return "Error"
	}

	return "String"
}

// printsAsMessage reports whether a trait value of tr prints: it
// refines Error or fmt.Stringer, whose method gives the text.
func printsAsMessage(tr *types.Trait) bool {
	return tr.Refines(types.ErrorTrait) || types.IsStringer(tr)
}

// chainer writes the function behind %+v on a value of type t, a
// declared type implementing Error or a trait value of it: the
// message, then each source's message after a colon and a space, each
// source dropped once asked for its own.
func (e *Emitter) chainer(t types.Type, pos token.Pos) string {
	name := "sn_chain_" + lower.Mangle(t)
	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	opt := e.cType(types.NewOption(types.ErrorTrait), pos)
	ct := e.cType(t, pos)
	var msg, src string
	switch u := t.(type) {
	case *types.Named:
		m, _ := e.methodNameOf(nil, u, "Error", pos)
		s, _ := e.methodNameOf(nil, u, "Source", pos)
		msg, src = m+"(v)", s+"(v)"
	case *types.Trait:
		msg, src = "v.tab->"+lower.Ident("Error")+"(v.data)", "v.tab->"+lower.Ident("Source")+"(v.data)"
	}

	put(&e.protos, "static sn_string ", name, "(", ct, " v);\n")
	var b strings.Builder
	put(&b, "static sn_string ", name, "(", ct, " v) {\n")
	put(&b, "\tsn_string out = ", msg, ";\n")
	put(&b, "\t", opt, " s = ", src, ";\n")
	put(&b, "\twhile (s.has) {\n")
	put(&b, "\t\tout = sn_join(sn_join(out, ", literal(": "), ", 1), s.val.tab->", lower.Ident("Error"), "(s.val.data), 3);\n")
	put(&b, "\t\t", opt, " next = s.val.tab->", lower.Ident("Source"), "(s.val.data);\n")
	put(&b, "\t\t", e.dropOf("s.val", types.ErrorTrait, pos), "\n")
	put(&b, "\t\ts = next;\n\t}\n\n\treturn out;\n}\n\n")
	e.helpers.WriteString(b.String())
	return name
}

// passed writes the conversion ? makes of the error err, of type
// from, into the error type of the function being written: wrapped in
// the variant the checker chose, boxed as a trait value the type
// implements, or as it is.
func (e *Emitter) passed(x *ast.TryExpr, err string, from types.Type) string {
	want := e.result.Underlying().(*types.Result).Err()
	if v := e.info.Wraps[x]; v != nil {
		return e.variantValue(want, v, []string{err}, x.Pos())
	}

	if tr, ok := want.(*types.Trait); ok {
		if n, ok := from.(*types.Named); ok {
			return e.box(err, n, tr, x.Pos())
		}
	}

	return err
}
