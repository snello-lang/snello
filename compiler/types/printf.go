// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
)

// formatted reports whether f is one of fmt's functions that take a
// format, and returns the index of the format among its parameters.
func formatted(f Object) (int, bool) {
	fn, ok := f.(*Func)
	if !ok || fn.pkg == nil || fn.pkg.path != "fmt" {
		return 0, false
	}

	switch fn.name {
	case "Printf", "Sprintf":
		return 0, true
	case "Fprintf":
		return 1, true
	}

	return 0, false
}

// printFormat checks a call of fmt's Printf, Sprintf or Fprintf against
// its format, as go vet's printf check does, as errors rather than
// warnings: the format is a string literal or a constant declared as
// one, each verb is one fmt
// prints, there are as many arguments as verbs, and each argument has
// a type its verb prints. args are the arguments after the format.
func (c *Checker) printFormat(e *ast.CallExpr, name string, at int, args []operand) {
	if at >= len(e.Args) {
		return
	}

	f := e.Args[at]
	obj := c.named(f)
	format, ok := formatText(f, obj)
	if k, isConst := obj.(*Const); !ok && isConst && k.typ != nil && !isString(k.typ) {
		return // not a string at all, which the argument's own check reported
	} else if !ok {
		c.errorf(f.Pos(), name+" takes its format as a string literal or a constant")
		return
	}

	pieces, err := ParseFormat(format)
	if err != nil {
		c.errorf(f.Pos(), name+": "+err.Error())
		return
	}

	n := 0
	for _, p := range pieces {
		if p.Verb == 0 {
			continue
		}

		if n >= len(args) {
			c.errorf(f.Pos(), name+" format %"+string(p.Verb)+" reads argument "+strconv.Itoa(n+1)+", but the call has "+strconv.Itoa(len(args)))
			return
		}

		if msg := verbFits(p, args[n].typ); msg != "" {
			c.errorf(e.Args[at+1+n].Pos(), name+" format "+msg)
		}

		n++
	}

	if n < len(args) {
		c.errorf(e.Args[at+1+n].Pos(), name+" call has "+strconv.Itoa(len(args))+" arguments, but its format reads "+strconv.Itoa(n))
	}
}

// named returns the object a name or a package's member names, without
// recording a use or reporting an undefined name: the format was
// checked as an argument already, and this only looks again.
func (c *Checker) named(e ast.Expr) Object {
	switch e := unparen(e).(type) {
	case *ast.Ident:
		_, obj := c.scope.LookupParent(e.Name)
		return obj
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			if _, obj := c.scope.LookupParent(x.Name); obj != nil {
				if pkg, ok := obj.(*PkgName); ok && ast.IsExported(e.Sel.Name) && pkg.Imported() != nil { // nil for a package whose check failed, reported already
					return pkg.Imported().Scope().Lookup(e.Sel.Name)
				}
			}
		}
	}

	return nil
}

// verbFits returns what is wrong with printing a value of type t with
// the verb p, or "". Each verb prints the types Go's fmt prints with it
// and Snello has: %v any value; %d, %o and %b an integer, in base ten,
// eight and two; %x and %X an integer in base sixteen, or the bytes of
// a string or a []u8, two digits each, a type that prints as its Error
// or String method counting as the string it returns, whatever it is
// declared over, as in Go; %c an integer as the character
// it is; %s a string, an error, or a type printing as its Error or
// String method, which a type parameter bounded by Error or
// fmt.Stringer and a trait value of a trait refining one do too; %q a
// string, such a type, or an integer as the rune it is; %f, %e and %g
// a float; %t a bool. A precision is a float's digits with %f, %e, %g
// and %v, a string's characters with %s, %q and %v, and an integer's
// least digits with %d, %x, %X, %o and %b. The zero flag goes with a
// number, an integer or a float, printed as one: not with %v, %x or %X
// of a number whose type prints as its String or Error method. Those
// three verbs, %s and %q print through the methods, the verbs a string
// takes; every other verb prints the value itself.
func verbFits(p FormatPiece, t Type) string {
	for {
		switch u := t.(type) {
		case *Ref:
			t = u.elem
			continue
		case *Pointer:
			t = u.elem
			continue
		}

		break
	}

	b, _ := t.Underlying().(*Basic)
	is := func(info BasicInfo) bool { return b != nil && b.info&info != 0 }
	text := is(IsString) || PrintsAsText(t)
	verb := "%" + string(p.Verb)
	what := ""
	switch p.Verb {
	case 'v':
		if p.Plus && !isErrorType(t) {
			return "%+v takes an error, not " + t.String()
		}

		if p.Plus && p.Prec >= 0 {
			return "%+v takes no precision"
		}

		if p.Prec >= 0 && !is(IsString) && !is(IsFloat) {
			return "%." + strconv.Itoa(p.Prec) + "v takes a precision on a string or a float, not on " + t.String()
		}
	case 'd', 'o', 'b':
		if !is(IsInteger) {
			what = "an integer"
		}
	case 'x', 'X':
		if !is(IsInteger) && !text && !isByteSlice(t) {
			what = "an integer, a string or a []u8"
		} else if p.Prec >= 0 && (!is(IsInteger) || PrintsAsText(t)) {
			return verb + " takes no precision on " + t.String()
		}
	case 'c':
		if !is(IsInteger) {
			what = "an integer"
		} else if p.Prec >= 0 {
			return "%c takes no precision"
		}
	case 's':
		if !text {
			what = "a string"
		}
	case 'q':
		if !text && !is(IsInteger) {
			what = "a string or a rune"
		} else if p.Prec >= 0 && !text {
			return "%q takes no precision on " + t.String()
		}
	case 'f', 'e', 'g':
		if !is(IsFloat) {
			what = "a float"
		}
	case 't':
		if !is(IsBoolean) {
			what = "a bool"
		} else if p.Prec >= 0 {
			return "%t takes no precision"
		}
	}

	if what != "" {
		return verb + " takes " + what + ", not " + t.String()
	}

	if p.Zero && !is(IsInteger) && !is(IsFloat) {
		return verb + " takes the 0 flag on a number, not on " + t.String()
	}

	if p.Zero && formatHasByte("vxX", p.Verb) && PrintsAsText(t) { // these verbs print its text, which is no number
		return verb + " takes the 0 flag on a number, not on " + t.String() + ", which prints as its text"
	}

	return ""
}
