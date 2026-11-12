// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"errors"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
)

// The directives of a layout of package time, the letters after a
// percent sign, as the strftime of C and POSIX has them: those that
// write a number, which may be written without its zeros, %-d, or with
// spaces for them, %_d; the rest that Format writes; and those that
// say no day or time, which Parse does not read.
const (
	layoutNumbers = "YCymdejHIMSuwUWVGgfs"
	layoutOthers  = "BbhAapPzZFTXRDxrcnt%"
	layoutUnread  = "CGgVUWuws"
)

// CheckTimeLayout checks a layout that time's Format takes, or its
// Parse when parsed is set: every percent sign starts a directive the
// package has, so a mistake in a layout is never written as text; and
// a layout Parse takes has no directive it does not read, and says
// which half of the day a twelve-hour clock is in.
func CheckTimeLayout(layout string, parsed bool) error {
	twelve, half := false, false
	for i := 0; i < len(layout); i++ {
		if layout[i] != '%' {
			continue
		}

		start := i
		i++
		flag := i < len(layout) && (layout[i] == '-' || layout[i] == '_')
		if flag {
			i++
		}

		if i < len(layout) && layout[i] == '.' { // %.f, %.3f, %.6f, %.9f
			i++
			if i < len(layout) && strings.IndexByte("369", layout[i]) >= 0 {
				i++
			}

			if flag || i >= len(layout) || layout[i] != 'f' {
				return errors.New("%" + rest(layout, start+1, i) + " is not a directive: the fraction of a second is %.f, %.3f, %.6f or %.9f")
			}

			continue
		}

		colon := i < len(layout) && layout[i] == ':'
		if colon {
			i++
		}

		if i >= len(layout) {
			return errors.New("a % at the end names no directive: %% writes a percent sign")
		}

		d := layout[i]
		name := "%" + layout[start+1:i+1]
		switch {
		case colon && (d != 'z' || flag):
			return errors.New(name + " is not a directive: a colon goes before z alone, %:z")
		case strings.IndexByte(layoutNumbers, d) < 0 && strings.IndexByte(layoutOthers, d) < 0:
			return errors.New(name + " is not a directive")
		case flag && strings.IndexByte(layoutNumbers, d) < 0:
			return errors.New(name + " is not a directive: " + layout[start+1:start+2] + " goes before a number, and %" + string(d) + " writes none")
		case parsed && strings.IndexByte(layoutUnread, d) >= 0:
			return errors.New(name + " does not say which day or time it is, and Parse does not read it")
		}

		twelve = twelve || d == 'I' || d == 'r'
		half = half || d == 'p' || d == 'P' || d == 'r'
	}

	if parsed && twelve && !half {
		return errors.New("%I is an hour of a half day, and the layout has no %p to say which half")
	}

	if parsed && half && !twelve {
		return errors.New("%p says which half of the day an hour is in, and the layout has no %I for it")
	}

	return nil
}

// rest returns the text of layout from lo up to and including hi, or
// to its end.
func rest(layout string, lo, hi int) string {
	if hi >= len(layout) {
		return layout[lo:]
	}

	return layout[lo : hi+1]
}

// timeLayout reports whether a call is one of package time's that take
// a layout, the function Parse or the method Format of a Time, and
// whether it is the one that reads a text; the layout is their first
// argument.
func timeLayout(e *ast.CallExpr, f Object, sig *Signature) (parsed, ok bool) {
	if sig.recv == nil {
		fn, isFunc := f.(*Func)
		return true, isFunc && fn.pkg != nil && fn.pkg.path == "time" && fn.name == "Parse"
	}

	sel, isSel := unparen(e.Fun).(*ast.SelectorExpr)
	if !isSel || sel.Sel.Name != "Format" {
		return false, false
	}

	n, isNamed := receiverBase(sig.recv.typ).(*Named)
	return false, isNamed && n.obj != nil && n.obj.name == "Time" && n.obj.pkg != nil && n.obj.pkg.path == "time"
}

// checkTimeLayout checks a call of time's Format or Parse against its
// layout, as printFormat checks a format of fmt's: the layout is a
// string literal or a constant declared as one, read when the program
// is built, and every directive in it is one the package has.
func (c *Checker) checkTimeLayout(e *ast.CallExpr, name string, parsed bool) {
	if len(e.Args) == 0 {
		return
	}

	arg := e.Args[0]
	obj := c.named(arg)
	layout, ok := formatText(arg, obj)
	if k, isConst := obj.(*Const); !ok && isConst && k.typ != nil && !isString(k.typ) {
		return // not a string at all, which the argument's own check reported
	} else if !ok {
		c.errorf(arg.Pos(), name+" takes its layout as a string literal or a constant")
		return
	}

	if err := CheckTimeLayout(layout, parsed); err != nil {
		c.errorf(arg.Pos(), name+": "+err.Error())
	}
}
