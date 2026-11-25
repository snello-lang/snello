// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The form is a function's body as the program evaluates it: blocks of
// instructions over places and temporaries, joined by the edges control
// can take, each instruction in the order the program does it. The
// move and borrow passes read it, so that what they check comes in the
// program's order and not the syntax tree's; a backend may read it for
// the same order. Nothing in a program names it.

// NoVar marks an operand that is a temporary, not a variable; NoTemp a
// temporary that was never made, the value of an expression the parser
// could not finish.
const (
	NoVar  = -1
	NoTemp = -1
)

// An InstrOp is what an instruction does.
type InstrOp uint8

const (
	OpRead   InstrOp = iota // the value at Place is read: a Copy out of it, or a use of what it holds
	OpMove                  // the value at Place leaves it; Place and every place within it are moved until a store revives them
	OpBorrow                // a borrow of Place, ref or mut, is made into To, copying the loans Place holds in the parts of To that reach them
	OpMake                  // a literal, tuple, variant or closure is built from Args into To
	OpCall                  // the callee runs with what Args hold, giving back what it lends, into To
	OpStore                 // To's value is given to Place, dropping what Place held; a borrow variable is pointed elsewhere; a temporary stored to is a block's or an arm's value
	OpDrop                  // the value at Place, a variable or a temporary, is dropped; a Drop reads what the value borrows
	OpDefer                 // Args are evaluated now and lent to a holder live to every return; the call runs in each returning leave
	OpReturn                // Args is one value the function returns, taken before the leave that follows; Expr the expression it is the value of
	OpLeave                 // a return, a ? that returns, a break or a continue: the deferred calls when it returns, the drops in Args, then the edge
)

// A Form is a function's body as the program evaluates it.
type Form struct {
	Vars   []*Var      // the body's variables, the parameters first, each once, by the index an Operand names
	Blocks []FormBlock // in the order control reaches them when no edge goes back
	Instrs []Instr     // every block's instructions in one list, a block a range of it
	Temps  []TempInfo  // the body's temporaries, by the number an Operand names

	operands []Operand     // what every instruction's Args slice into, one list for the body
	succs    []int         // what every block's Succs slice into
	closures []closureForm // the closures written in the body, whose passes run under the body's
}

// A closureForm is the form of a closure written in a body, with the
// closure's signature and where it begins.
type closureForm struct {
	form *Form
	sig  *Signature
	pos  token.Pos
}

// A TempInfo is what the lowering knows of a temporary: the expression
// whose value it holds, its type, the owned temporary it is or a borrow
// of a part of which it is, and what takes it.
type TempInfo struct {
	Expr    ast.Expr
	Type    Type
	Origin  int  // the owned temporary this one is, or is a borrow of a part of, or a call's result borrows from: itself for a value made here
	Loop    bool // a borrow a range loop holds of what it ranges over, for every pass
	Bound   bool // a binding or a loop variable takes a part of it, or of a borrow of it: it is held until its statement drops it
	Given   bool // a call or a defer takes it, or a statement with several values stores it: what it lends is lent until then
	Carried bool // made of a value that reads a variable its statement stores: it holds what the value lends before any store
}

// A FormBlock is a straight run of instructions and the blocks control
// may go to from its end.
type FormBlock struct {
	From, To int       // the block's instructions, Instrs[From:To]
	Succs    []int     // the blocks control may go to next; none for a block that leaves the function
	Head     bool      // a loop's head, where each pass begins, which an edge from a later block comes back to
	Loop     int       // the head of the innermost loop the block is in, or -1
	Pos      token.Pos // for a head, where the loop begins: a variable declared before it is not the loop's own
	Guard    *Guard    // for a match guard's block, what it is and where it stands; nil for any other
	preds    int       // the edges into the block: none, after the first block, is code control cannot reach, which is not written
}

// A Guard says what a block that evaluates a match guard is, a match
// guard or the condition of a match arm, and where it stands, from the
// guard to the arm's =>, between which a name is the guard's own.
type Guard struct {
	Taken    int // the block the arm goes on in when the guard holds; every other way out of the guard's blocks is its failure
	What     string
	From, To token.Pos
}

// An Instr is one step of a body. Expr is the expression it comes from,
// for the position and the text of a message.
type Instr struct {
	Op      InstrOp
	Place   Operand   // what is read, moved, borrowed, stored to or dropped
	To      Operand   // the temporary made or stored
	Args    []Operand // what a make, call or defer takes; what a leave drops
	Mut     bool      // a borrow as mut
	Capture bool      // a read, borrow or move a closure's making records for a variable it captures; for a mut borrow, no use of the value until the closure runs
	Decl    bool      // a store that declares its variable, giving it its first value
	Expr    ast.Expr
	Pos     token.Pos
	why     dropWhy // for a drop or a leave, where the value dies, which a message names
}

// An Operand is a place, a variable and a path into it of field names,
// of * for the value a borrow or pointer reaches, of [] for any element
// of a slice, and of ?N for the part a pattern binds, a variant's name or
// a tuple's index after a ?, or a temporary, a value the statement makes.
type Operand struct {
	Var  int    // the index of the variable in the Form's Vars, or NoVar for a temporary
	Temp int    // the temporary's number, when Var is NoVar
	Path string // the parts after the variable, joined by dots
}

// Temp returns the operand naming temporary n.
func Temp(n int) Operand { return Operand{Var: NoVar, Temp: n} }

// IsTemp reports whether o is a temporary.
func (o Operand) IsTemp() bool { return o.Var == NoVar }

// IsValue reports whether o is a temporary that holds a value: not a
// place, and not the mark of no value.
func (o Operand) IsValue() bool { return o.Var == NoVar && o.Temp != NoTemp }

// sub is the place part reached from o: a field by name, * for what a
// borrow or pointer at o reaches, [] for an element.
func (o Operand) sub(part string) Operand {
	return Operand{Var: o.Var, Temp: o.Temp, Path: joinPath(o.Path, part)}
}

// String writes the form, block by block, each instruction on a line,
// for a test and for reading: t1 = call(t0, xs); store b.n <- t1;
// move s; drop r; leave(r, s); and after each block the blocks it goes
// to.
func (f *Form) String() string {
	var b strings.Builder
	for i, blk := range f.Blocks {
		b.WriteString("b" + strconv.Itoa(i))
		if blk.Head {
			b.WriteString(" (head)")
		}

		b.WriteString(":\n")
		for _, in := range f.Instrs[blk.From:blk.To] {
			b.WriteString("  " + f.instrString(in) + "\n")
		}

		b.WriteString("  ->")
		for _, s := range blk.Succs {
			b.WriteString(" b" + strconv.Itoa(s))
		}

		b.WriteString("\n")
	}

	return b.String()
}

// OperandString writes an operand: the variable's name with its path,
// or tN for a temporary.
func (f *Form) OperandString(o Operand) string {
	name := "t" + strconv.Itoa(o.Temp)
	if !o.IsTemp() {
		name = f.Vars[o.Var].name
	} else if o.Temp == NoTemp {
		name = "none" // an expression the parser could not finish
	}

	if o.Path == "" {
		return name
	}

	return name + "." + o.Path
}

func (f *Form) instrString(in Instr) string {
	switch in.Op {
	case OpRead:
		if in.Mut {
			return "read mut " + f.OperandString(in.Place)
		}

		return "read " + f.OperandString(in.Place)
	case OpMove:
		return "move " + f.OperandString(in.Place)
	case OpBorrow:
		kind := "ref"
		if in.Mut {
			kind = "mut"
		}

		return f.OperandString(in.To) + " = borrow " + kind + " " + f.OperandString(in.Place)
	case OpMake:
		return f.OperandString(in.To) + " = make" + f.argsString(in.Args)
	case OpCall:
		return f.OperandString(in.To) + " = call" + f.argsString(in.Args)
	case OpStore:
		return "store " + f.OperandString(in.Place) + " <- " + f.OperandString(in.To)
	case OpDrop:
		return "drop " + f.OperandString(in.Place)
	case OpDefer:
		return "defer" + f.argsString(in.Args)
	case OpReturn:
		return "return" + f.argsString(in.Args)
	case OpLeave:
		return "leave" + f.argsString(in.Args)
	}

	return "?"
}

func (f *Form) argsString(args []Operand) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = f.OperandString(a)
	}

	return "(" + strings.Join(parts, ", ") + ")"
}
