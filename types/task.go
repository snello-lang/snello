// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
)

// The task boundary is crossed in three places: a spawn, for what the
// task takes in, a send, for the value put on the channel, and a
// receive, for the value taken off it. What crosses is moved, and its
// type is Send (send.go); a borrow never crosses, since it points into
// a frame of the task that made it (_docs/design/03-ownership-across-
// tasks.md). A send moves the value as an assignment does, and a
// receive yields an owned T?, so the two need nothing beyond the type
// rule, which is checked once where the channel type is written: a
// channel of a type that is not Send cannot be made. The spawn is where
// the work is: its arguments and the variables its closure mentions
// are moved into the task, and each must be Send.

// spawned checks the call a spawn statement starts. A closure written
// in the statement takes what it captures with it: every capture
// becomes a move, as a task has frames of its own and cannot borrow
// from the one that spawned it. Any other callee cannot be a closure
// holding a borrow, nor take one in a parameter or in its receiver,
// and its receiver must be Send; its arguments are checked as they are
// evaluated (spawnArg).
func (c *Checker) spawned(e *ast.CallExpr, f operand, sig *Signature) {
	if lit, ok := unparen(e.Fun).(*ast.FuncLit); ok {
		c.spawnCaptures(lit, e.Pos())
	} else {
		c.kept(f, "spawned", e.Fun.Pos())
	}

	if sig.recv != nil {
		if isBorrow(sig.recv.typ) {
			c.errorf(e.Pos(), "a spawned task cannot hold a borrow")
			return
		}

		if !c.taken(exprText(e.Fun), sig.recv.typ, e.Pos()) {
			return
		}
	}

	for _, p := range sig.params {
		if holdsBorrow(p.typ) {
			c.errorf(e.Pos(), "a spawned task cannot hold a borrow")
			return
		}
	}
}

// spawnCaptures moves the variables a spawned closure captures into
// the task, at the spawn: a capture the closure only reads or changes,
// a borrow anywhere else, is a move here, and the variable is dead in
// the function afterwards; a Copy value is copied in, as it is for any
// closure, and a value the closure already moved stays moved. Each
// must be Send, which a borrow variable never is, so what is moved
// here is always an owned variable. The body was checked before the
// spawn, and the borrows it takes of a capture are of the task's own
// value, in the task's frame; the move is therefore checked against
// the borrows live after the closure, where the body's are over and
// the function's own still count.
func (c *Checker) spawnCaptures(lit *ast.FuncLit, pos token.Pos) {
	caps := c.captured[lit]
	for i := range caps {
		cap := &caps[i]
		if !c.taken(cap.v.name, cap.v.typ, pos) || cap.mode == byCopy || cap.mode == byMove {
			continue
		}

		cap.mode = byMove
		c.capture(cap.v, byMove) // a closure around the spawn moves it too, as move does
		p := place{cap.v, ""}
		c.access(&ast.Ident{NamePos: pos, Name: cap.v.name}, p, moveAccess, pos, lit.Body.Rbrace) // named for the message
		c.moved = append(c.moved, move{p, pos, false})
	}
}

// spawnArg checks an argument of a spawned call: the task owns it, so
// it must be Send. A borrow is reported against the parameter, by
// spawned, since a parameter of a borrow type cannot be spawned at all.
func (c *Checker) spawnArg(arg ast.Expr, op operand) {
	if op.mode != invalid && !holdsBorrow(op.typ) {
		c.taken(exprText(arg), op.typ, arg.Pos())
	}
}

// taken reports whether a value of type t, named what in the message,
// may be taken into a spawned task, reporting why not.
func (c *Checker) taken(what string, t Type, pos token.Pos) bool {
	why := NotSend(t)
	if why != "" {
		c.errorf(pos, "cannot spawn with "+what+": "+why)
	}

	return why == ""
}

// sendable reports whether elem may be carried by a channel: a value
// taken off the channel is owned by whichever task receives it, so the
// type must be Send. The borrow case is reported by storable, before.
func (c *Checker) sendable(elem Type, pos token.Pos) bool {
	why := NotSend(elem)
	if why == "" {
		return true
	}

	c.errorf(pos, "a channel cannot carry "+elem.String()+": "+why)
	return false
}

// closeCall checks close(ch): the channel is one this side may send on,
// and nothing is sent on it afterwards.
func (c *Checker) closeCall(e *ast.CallExpr) operand {
	if len(e.Args) != 1 {
		c.errorf(e.Lparen, "close takes one channel")
		c.exprs(e.Args)
		return invalidOperand
	}

	x := c.expr(e.Args[0], nil)
	if x.mode == invalid {
		return invalidOperand
	}

	ch, ok := valueType(x).Underlying().(*Chan)
	if !ok {
		c.errorf(e.Args[0].Pos(), "cannot close a value of type "+x.typ.String())
		return invalidOperand
	}

	if ch.dir == RecvOnly {
		c.errorf(e.Args[0].Pos(), "cannot close a receive-only channel")
		return invalidOperand
	}

	return operand{value, Unit, nil, nil}
}
