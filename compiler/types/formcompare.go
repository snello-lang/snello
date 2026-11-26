// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The comparison of the passes over the form with the walk's, while both
// run: what each refuses, what each says the closures capture, what each
// moves, and what each gives to a parameter its callee may keep, every
// difference reported as an error of its own. It goes with the walk's
// passes.

// compareForms makes the checker run the move pass over the form
// beside the tree's and report every disagreement as an error, for the
// tests that run the corpus with both.
var compareForms bool

// A formMove records a place moved, where, and in which block: a move
// made in a loop's blocks is the loop's own. round is the head of the
// loop the record came round, when it did: inside that loop it is the
// loop rule's, reported once, and no use again.
type formMove struct {
	place Operand
	pos   token.Pos
	block int
	round int
}

// A formError is a message at a position the pass over the form
// reports.
type formError struct {
	pos token.Pos
	msg string
}

// A formMoves is the pass over one form.
type formMoves struct {
	c    *Checker
	f    *Form
	in   [][]formMove // the moved set where each block begins
	errs []formError
}

// CompareForms turns the comparison of the move pass over the form with
// the tree's on or off: while both run, every disagreement is reported
// as an error. The tests of the packages that load the library and the
// examples run them with it on.
func CompareForms(on bool) { compareForms = on }

// isMoveMessage reports whether a message is the move pass's: one of
// its refusals, but for a move the borrow pass refuses, which names what
// the place is borrowed by.
func isMoveMessage(msg string) bool {
	if strings.Contains(msg, " is borrowed by ") || strings.Contains(msg, " is borrowed at ") {
		return false
	}

	for _, prefix := range []string{"use of moved value ", "use of partially moved value ", "cannot move "} {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}

	return strings.HasSuffix(msg, loopMovedMessage(""))
}

// comparePasses reports, when both passes run, each message the passes
// over the form give that the tree's did not, and each of the tree's
// the form's did not, among the errors reported since first: the move
// pass's messages against moves, the borrow pass's against borrows. A
// body that does not type is left alone, since what its moves and
// borrows would be is moot.
// It reports whether the body typed, so that what the walk recorded of
// its moves and captures can be compared too.
func (c *Checker) comparePasses(moves, borrows []formError, first int) bool {
	defer func() { c.captureErrs, c.formKeeps = c.captureErrs[:0], c.formKeeps[:0] }() // the body's, compared or dropped with it
	treeMoves, treeBorrows, treeEscapes := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range c.errors[first:] {
		text := e.Pos.String() + ": " + e.Msg
		switch {
		case isMoveMessage(e.Msg):
			treeMoves[text] = true
		case isEscapeMessage(e.Msg):
			treeEscapes[text] = true
		case isBorrowMessage(e.Msg):
			treeBorrows[text] = true
		case !strings.HasPrefix(e.Msg, "form"):
			return false // a typing error: what the walk did after it says nothing
		}
	}

	var escapes []formError // the borrow pass's graph follows the returned borrows too: its errors are told apart by their text, in place
	kept := borrows[:0]
	for _, e := range borrows {
		if isEscapeMessage(e.msg) {
			escapes = append(escapes, e)
		} else {
			kept = append(kept, e)
		}
	}

	c.comparePass("moves", moves, treeMoves)
	c.comparePass("borrows", kept, treeBorrows)
	c.comparePass("escape", escapes, treeEscapes)
	c.comparePass("captures", c.captureErrs, nil) // what the forms say the closures capture, against the walk's record: every difference is one
	c.compareKeeps()
	return true
}

// compareCaptures reports, while both run, where the captures a closure's
// form says it makes differ from the walk's record: a variable one has
// and the other not, or captured another way.
func (c *Checker) compareCaptures(lit *ast.FuncLit, form, walk []capture) {
	modes := [...]string{byCopy: "copied", byRef: "borrowed", byMut: "borrowed mut", byMove: "moved"}
	for _, f := range form {
		j := slices.IndexFunc(walk, func(w capture) bool { return w.v == f.v })
		switch {
		case j < 0:
			c.captureErrs = append(c.captureErrs, formError{lit.Pos(), f.v.name + " captured by the form, not by the walk"})
		case walk[j].mode != f.mode:
			c.captureErrs = append(c.captureErrs, formError{lit.Pos(), f.v.name + " " + modes[f.mode] + " by the form, " + modes[walk[j].mode] + " by the walk"})
		}
	}

	for _, w := range walk {
		if !slices.ContainsFunc(form, func(f capture) bool { return f.v == w.v }) {
			c.captureErrs = append(c.captureErrs, formError{lit.Pos(), w.v.name + " captured by the walk, not by the form"})
		}
	}
}

// compareMoved reports, while both run, where the places the form moves
// differ from the moves the walk recorded for the emitters: every move
// instruction of the body's form and its closures' of an owned place no
// Drop reads, the captures' and the moves both refuse aside, is a
// recorded move, and every recorded move in the body is one.
func (c *Checker) compareMoved(f *Form, body *ast.BlockStmt) {
	moved := map[ast.Expr]bool{}
	f.eachForm(func(f *Form) {
		for i := range f.Instrs {
			in := &f.Instrs[i]
			if in.Op != OpMove || in.Capture || in.Place.IsTemp() {
				continue
			}

			if _, part := unparen(in.Expr).(*ast.PartExpr); part {
				continue // refused by both, recorded by neither
			}

			kind, fields := placeKindOf(f.Vars[in.Place.Var].typ, in.Place.Path)
			if holder, _ := dropHolder(place{f.Vars[in.Place.Var], fields}); kind == placeOwned && holder == "" { // a move out of a borrow, a container or a value a Drop reads is refused, and recorded by neither
				moved[in.Expr] = true
				if !c.info.Moves[in.Expr] {
					c.errorf(in.Pos, "form moved: "+exprText(in.Expr)+" moved by the form, not by the walk")
				}
			}
		}
	})

	ast.Inspect(body, func(n ast.Node) bool {
		if e, ok := n.(ast.Expr); ok && c.info.Moves[e] && !moved[e] {
			c.errorf(e.Pos(), "form moved: "+exprText(e)+" moved by the walk, not by the form")
		}

		return true
	})
}

// isEscapeMessage reports whether a message is the escape pass's: a
// borrow returned that the function's source does not hold, or a
// closure that borrows returned, stored, sent or spawned.
func isEscapeMessage(msg string) bool {
	if strings.HasPrefix(msg, "cannot return a borrow") {
		return true
	}

	for _, how := range [...]string{"returned", "stored", "sent", "spawned"} {
		if strings.HasSuffix(msg, " and cannot be "+how) {
			return true
		}
	}

	return false
}

// comparePass reports the differences between what a pass over the
// form gave and what the tree's pass of the same kind did.
func (c *Checker) comparePass(what string, form []formError, tree map[string]bool) {
	given := make(map[string]bool, len(form))
	for _, e := range form {
		text := c.position(e.pos).String() + ": " + e.msg
		given[text] = true
		if !tree[text] {
			c.errorf(e.pos, "form "+what+": "+text+" (the tree's pass did not)")
		}
	}

	for text := range tree {
		if !given[text] {
			c.errorf(token.NoPos, "form "+what+" misses: "+text)
		}
	}
}

// isBorrowMessage reports whether a message is the borrow pass's: a
// collision with a loan, a borrow kept past its owner or stored where
// the function cannot follow it, or a package variable changed.
func isBorrowMessage(msg string) bool {
	return strings.Contains(msg, " is borrowed") || strings.Contains(msg, " is already borrowed") ||
		strings.HasPrefix(msg, "cannot keep a borrow of ") || strings.HasPrefix(msg, "cannot store a borrow") ||
		strings.Contains(msg, " is a package variable, shared by every task")
}

// compareKeeps reports, while both run, each closure that borrows the
// form's pass found given to a parameter its callee may keep that the
// walk did not record.
func (c *Checker) compareKeeps() {
	for _, k := range c.formKeeps {
		if !slices.Contains(c.keepCalls, k) {
			c.errorf(k.pos, "form keep: "+k.what+" passed to "+k.callee+", which the walk did not record")
		}
	}
}
