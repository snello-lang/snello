// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The standard package a spawn written as an expression gives its
// value from, and the type of that value in it.
const (
	taskPackage = "task"
	handleType  = "Handle"
)

// fmtPath is the package the compiler's printing is written against,
// loaded into every program checked, since the code the backends write
// for a value printed calls fmt's own functions.
const fmtPath = "fmt"

// The task boundary is crossed in three places: a spawn, for what the
// task takes in, a send, for the value put on the channel, and a
// receive, for the value taken off it. What crosses is moved, and its
// type is Send (send.go); a borrow never crosses, since it points into
// a frame of the task that made it. A send moves the value as an
// assignment does, and a receive yields an owned T?, so the two need
// nothing beyond the type rule, which is checked once where the
// channel type is written: a channel of a type that is not Send cannot
// be made. The spawn is where the work is: its arguments and the
// variables its closure mentions are moved into the task, and each
// must be Send.

// spawned checks the call a spawn statement starts. A closure written
// in the statement takes what it captures with it: every capture
// becomes a move, as a task has frames of its own and cannot borrow
// from the one that spawned it. Any other callee cannot be a closure
// holding a borrow, nor take one in a parameter or in its receiver,
// and its receiver must be Send; its arguments are checked as they are
// evaluated (spawnArg).
func (c *Checker) spawned(e *ast.CallExpr, f operand, sig *Signature) {
	if !isFuncLit(e.Fun) {
		c.kept(f, "spawned", e.Fun.Pos()) // a closure written here moves what it captures into the task instead, where it is lowered
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

// spawnExpr checks spawn f(x) written as an expression, for a handle
// on the task: the call is spawned as a statement's is, and the value
// is a task.Handle of the call's result, which the handle carries to
// the task that joins, so the result is Send as the arguments are.
func (c *Checker) spawnExpr(e *ast.SpawnExpr) operand {
	call, ok := e.Call.(*ast.CallExpr)
	if !ok {
		c.expr(e.Call, nil)
		return invalidOperand // reported by the parser
	}

	reported := len(c.errors)
	c.spawning = call
	r := c.expr(call, nil)
	c.spawning = nil
	handle := c.taskHandle(e.Pos())
	if r.mode == invalid || handle == nil || len(c.errors) > reported {
		return invalidOperand // a call refused as spawned is reported once
	}

	if why := NotSend(r.typ); why != "" {
		c.errorf(e.Pos(), "cannot spawn for a handle: "+why)
		return invalidOperand
	}

	return operand{value, Instantiate(handle, []Type{r.typ}), nil, nil}
}

// taskHandle returns the declaration of task.Handle, loaded through the
// importer whether or not the program imports the package: a spawn
// written as an expression needs no import to be written, and only a
// program that names the handle's type imports task.
func (c *Checker) taskHandle(pos token.Pos) *Named {
	if c.conf.Importer == nil {
		c.errorf(pos, "spawn as an expression needs the task package, which nothing loads here")
		return nil
	}

	pkg, err := c.conf.Importer.Import(taskPackage)
	if err != nil {
		c.errorf(pos, err.Error())
		return nil
	}

	obj, ok := pkg.scope.Lookup(handleType).(*TypeName)
	if !ok {
		c.errorf(pos, "package "+taskPackage+" declares no type "+handleType)
		return nil
	}

	named, _ := obj.typ.(*Named)
	return named
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

// closeCall refuses close(ch): a channel ends when its last sending
// handle is dropped, and the name stays predeclared so that a program
// written with the call is told what to write instead.
func (c *Checker) closeCall(e *ast.CallExpr) operand {
	c.errorf(e.Pos(), "close is gone: a channel ends when its last sending handle is dropped")
	c.exprs(e.Args)
	return invalidOperand
}
