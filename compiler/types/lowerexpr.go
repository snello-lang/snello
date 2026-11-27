// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The lowering of expressions: each for its value, read, lent or moved
// as the use wants, a value made here into a temporary of its
// statement's.

// value lowers e for its value, given to a place or a parameter of type
// want, nil when the value's own type is wanted, and returns the
// operand holding it: the place itself for a place read or moved, a
// temporary for a borrow or a value made here. A Copy value is read;
// an owned place given where a borrow is wanted is lent; any other
// place is moved.
func (l *lowering) value(e ast.Expr, want Type) Operand {
	o := l.valueOf(e, want)
	if r, ok := want.(*Ref); ok && o.IsValue() && o.Path == "" {
		if t := l.f.Temps[o.Temp].Type; t != nil && (!isBorrow(t) || Identical(r.elem, t)) && !isNever(t) && t != Typ[Invalid] {
			if p, ok := l.borrowedPlace(o); ok && isBorrow(t) { // ref x of a borrow variable, which the typing reads as x's own borrow: where a borrow of it is wanted, x's slot is lent
				lent := l.lendAs(p, e, r, e.Pos())
				l.f.Temps[lent.Temp].Itself = true
				return lent
			}

			lent := l.lendAs(o, e, l.refOf(t, r), e.Pos()) // a value made here given where a borrow is wanted is lent, as an owned place is: the statement holds it, and what takes the borrow holds a borrow of it
			l.f.Temps[lent.Temp].Itself = isBorrow(t)      // a borrow made here lent where a borrow of it is wanted: its own slot, the temporary
			return lent
		}
	}

	return o
}

// borrowedPlace returns the variable's place the temporary o was just
// made a borrow of, when the last instruction written is that borrow.
func (l *lowering) borrowedPlace(o Operand) (Operand, bool) {
	if n := len(l.f.Instrs); n > 0 {
		if in := &l.f.Instrs[n-1]; in.Op == OpBorrow && in.To == o && !in.Place.IsTemp() {
			return in.Place, true
		}
	}

	return Operand{}, false
}

// valueOf is value but for the borrow of a value made here that a
// borrow is wanted of, which value makes.
func (l *lowering) valueOf(e ast.Expr, want Type) Operand {
	if e == nil { // an expression the parser could not finish
		return Temp(NoTemp)
	}

	switch x := e.(type) {
	case *ast.ParenExpr:
		return l.value(x.X, want)
	case *ast.BasicLit:
		return l.made(e, nil, false)
	case *ast.TypeExpr:
		return Temp(NoTemp)
	case *ast.FuncLit:
		return l.funcLit(x)
	case *ast.CallExpr:
		return l.call(x)
	case *ast.SpawnExpr:
		return l.made(e, l.args(l.spawned(x.Call)), true) // the closure's captures move into the task where the closure stands
	case *ast.UnaryExpr:
		return l.unary(x, want)
	case *ast.BinaryExpr:
		return l.binary(x)
	case *ast.CompositeLit:
		return l.composite(x)
	case *ast.TupleExpr:
		start := len(l.argStack)
		for i, elt := range x.Elts {
			l.argStack = append(l.argStack, l.keptValue(elt, "stored", l.c.slotType(x, i, nil))) // a tuple holds its elements as a struct holds its fields: a closure that borrows cannot be put in one
		}

		return l.madeFrom(e, start, true)
	case *ast.IfExpr:
		return l.ifExpr(x)
	case *ast.MatchExpr:
		return l.matchExpr(x)
	case *ast.CoalesceExpr:
		return l.coalesce(x)
	case *ast.TryExpr:
		return l.try(x)
	case *ast.SliceExpr:
		if x.Low != nil {
			l.value(x.Low, Typ[Int])
		}

		if x.High != nil {
			l.value(x.High, Typ[Int])
		}

		p, ok := l.placeOrPart(x.X) // xs[i:j] is a borrow of xs, lent for the statement unless a call takes it
		if !ok {
			p = l.value(x.X, nil) // a slice of a value made here borrows the temporary that holds it
		}

		view := l.lend(p, x.X, isMut(l.typeOf(e)))
		l.f.Temps[view.Temp].View = true
		return view
	}

	if p, ok := l.placeOrPart(e); ok {
		return l.use(p, e, want)
	}

	return l.made(e, nil, false)
}

// placeOrPart returns the operand of a place: a variable with its path,
// or a part of a value made here, a field or an element of a call's
// result, as a place of the temporary that holds the value. The indexes
// and keys inside a place that is read are evaluated here, once, as the
// program evaluates them before it reaches the place; an assignment's
// place evaluates its own (indexes).
func (l *lowering) placeOrPart(e ast.Expr) (Operand, bool) {
	if p, ok := l.placeOf(e); ok {
		l.keys(e)
		return p, true
	}

	return l.partOfValue(e)
}

// partOfValue evaluates a part of a value made here, a field or an
// element of a call's result, as a place of the temporary that holds
// the value: a move out of it is a move out of a temporary.
func (l *lowering) partOfValue(e ast.Expr) (Operand, bool) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return l.partOfValue(x.X)
	case *ast.SelectorExpr:
		if l.isPkgSelector(x) {
			return Operand{}, false
		}

		if _, isField := l.c.info.Uses[x.Sel].(*Var); !isField {
			return Operand{}, false
		}

		return l.through(l.root(x.X), x.X, x.Sel.Name), true
	case *ast.IndexExpr:
		base := l.root(x.X) // what is indexed before the index, as the program evaluates them
		l.read(x.Index)
		return l.through(base, x.X, "[]"), true
	case *ast.UnaryExpr:
		if x.Op == token.MUL {
			return l.sub(l.root(x.X), []byte("*")), true
		}
	}

	return Operand{}, false
}

// root evaluates what a part of a value is reached from: a part itself,
// reached on without a use of its own, or the value, made once.
func (l *lowering) root(e ast.Expr) Operand {
	if p, ok := l.partOfValue(e); ok {
		return p
	}

	return l.baseValue(e)
}

// use reads, lends or moves the place p for a use of its value where a
// value of type want is wanted; a borrow stored behind a borrow, wanted
// as it is, is read through the outer one, and a borrow given where a
// borrow of it is wanted is lent itself.
func (l *lowering) use(p Operand, e ast.Expr, want Type) Operand {
	t := l.typeOf(e)
	if r, ok := want.(*Ref); ok && isBorrow(t) && Identical(r.elem, t) { // a borrow given where a borrow of it is wanted: its own place is lent, as ref r lends it
		tmp := l.lendAs(p, e, r, e.Pos())
		l.f.Temps[tmp.Temp].Itself = true
		return tmp
	}

	if r, ok := t.(*Ref); ok && isBorrow(r.elem) && want != nil && !AssignableTo(t, want) && AssignableTo(r.elem, want) { // a borrow read out through a borrow of it, x of type ref ref string where a ref string is wanted: what x points at is the value
		through := l.sub(p, []byte("*"))
		l.emit(Instr{Op: OpRead, Place: through, Expr: e, Pos: e.Pos()})
		return through
	}

	if r, ok := want.(*Ref); ok && !isBorrow(t) {
		return l.lendAs(p, e, l.refOf(t, r), e.Pos())
	}

	if r, ok := t.(*Ref); ok && want != nil && !isBorrow(want) && !isAny(want) && !IsCopy(r.elem) { // a borrow given where the value is wanted: a move out of what it points to, which is refused
		through := l.sub(p, []byte("*"))
		l.emit(Instr{Op: OpMove, Place: through, Expr: e, Pos: e.Pos()})
		return through
	}

	if t == nil || IsCopy(t) || isAny(want) {
		l.emit(Instr{Op: OpRead, Place: p, Expr: e, Pos: e.Pos()})
		return p
	}

	l.emit(Instr{Op: OpMove, Place: p, Expr: e, Pos: e.Pos()})
	return p
}

// lend lends the place p, written as e, into a borrow temporary, mut
// when mut says so, which the statement holds until its end unless a
// call takes it; a borrow of a part of a temporary is of that temporary.
func (l *lowering) lend(p Operand, e ast.Expr, mut bool) Operand { return l.lendAt(p, e, mut, e.Pos()) }

// lendAt is lend, the borrow made at pos.
func (l *lowering) lendAt(p Operand, e ast.Expr, mut bool, pos token.Pos) Operand {
	return l.lendAs(p, e, l.ref(l.typeOf(e), mut), pos)
}

// refOf is the type of a borrow of a value of type t made where want,
// a borrow, is wanted: want itself when it is of t, as it is in most
// places, else a borrow of t with want's mutability.
func (l *lowering) refOf(t Type, want *Ref) *Ref {
	if Identical(want.elem, t) {
		return want
	}

	return l.ref(t, want.mut)
}

// lendAs is lendAt, the borrow made of the type ref.
func (l *lowering) lendAs(p Operand, e ast.Expr, ref *Ref, pos token.Pos) Operand {
	tmp := l.temp(ref, true, e)
	l.derives(tmp, p)
	l.emit(Instr{Op: OpBorrow, Place: p, To: tmp, Mut: ref.mut, Expr: e, Pos: pos})
	return tmp
}

// made writes the making of a value from its parts into a temporary,
// which the statement owns when its type is not Copy or holds a borrow;
// a borrow among the parts stays the statement's, lent until its end.
func (l *lowering) made(e ast.Expr, args []Operand, owned bool) Operand {
	return l.madeAt(e, args, owned, e.Pos())
}

// madeAt is made, the value made at pos.
func (l *lowering) madeAt(e ast.Expr, args []Operand, owned bool, pos token.Pos) Operand {
	t := l.temp(l.typeOf(e), owned, e)
	for _, a := range args {
		if l.isBorrowTemp(a) {
			l.parts = append(l.parts, lentPart{made: t.Temp, part: a.Temp}) // lent for a call that takes the value, else until the statement ends
		} else {
			l.take(a)
		}
	}

	l.emit(Instr{Op: OpMake, To: t, Args: l.args(args...), Expr: e, Pos: pos})
	return t
}

// spawned lowers the call a spawn statement makes: what a closure
// written there captures moves where the closure stands, into the task.
func (l *lowering) spawned(call ast.Expr) Operand {
	x, ok := call.(*ast.CallExpr)
	if !ok {
		return l.value(call, nil)
	}

	was := l.spawning
	l.spawning = x
	defer func() { l.spawning = was }()
	return l.call(x)
}

func (l *lowering) unary(x *ast.UnaryExpr, want Type) Operand {
	switch x.Op {
	case token.REF, token.MUT:
		if p, ok := l.placeOrPart(x.X); ok { // a borrow of a part of a value made here is of what the statement holds
			tmp := l.temp(l.typeOf(x), true, x)
			l.derives(tmp, p)
			l.emit(Instr{Op: OpBorrow, Place: p, To: tmp, Mut: x.Op == token.MUT, Expr: x.X, Pos: x.X.Pos()})
			return tmp
		}

		v := l.value(x.X, nil) // a borrow of a value made here, which the statement holds until its end
		t := l.temp(l.typeOf(x), false, x)
		l.derives(t, v)
		l.emit(Instr{Op: OpMake, To: t, Args: l.args(v), Expr: x, Pos: x.Pos()})
		return t
	case token.MUL:
		p, _ := l.placeOrPart(x)
		return l.use(p, x, want)
	case token.AND:
		v := l.value(x.X, nil) // &x moves x to the heap
		return l.made(x, l.args(v), true)
	case token.ARROW:
		ch := l.channel(x.X) // a receive reads its channel, as every operation on one does
		return l.made(x, l.args(ch), true)
	}

	if fn := OperatorMethod(stripRef(l.typeOf(x.X)), x.Op, true); fn != nil { // a type's own operator: a call, whose operand goes to its receiver
		a := l.value(x.X, fn.typ.(*Signature).recv.typ)
		l.take(a)
		t := l.temp(l.typeOf(x), true, x)
		l.emit(Instr{Op: OpCall, To: t, Args: l.args(a), Expr: x, Pos: x.Pos()})
		return t
	}

	v := l.read(x.X)
	return l.made(x, l.args(v), true)
}

// binary lowers an operator: && and || decide their right side by their
// left, and any other evaluates both, left first.
func (l *lowering) binary(x *ast.BinaryExpr) Operand {
	if x.Op == token.LAND || x.Op == token.LOR {
		result := l.temp(Typ[Bool], false, x)
		l.store(result, l.value(x.X, Typ[Bool]), x.X)
		right, join := l.newBlock(), l.newBlock()
		l.branch(right, join)
		l.store(result, l.value(x.Y, Typ[Bool]), x.Y)
		l.joinTo(join)
		l.start(join)
		return result
	}

	if fn := OperatorMethod(stripRef(l.typeOf(x.X)), x.Op, false); fn != nil { // a type's own operator: a call, whose operands go to its receiver and parameter
		sig := fn.typ.(*Signature)
		a := l.value(x.X, sig.recv.typ)
		b := l.value(x.Y, sig.params[0].typ)
		l.take(a)
		l.take(b)
		t := l.temp(l.typeOf(x), true, x)
		l.emit(Instr{Op: OpCall, To: t, Args: l.args(a, b), Expr: x, Pos: x.Pos()})
		return t
	}

	a := l.read(x.X)
	b := l.read(x.Y)
	return l.made(x, l.args(a, b), true)
}

// read evaluates an operand an operator or a builtin reads: a place is
// read, not moved, whatever its type; a value made here is made.
func (l *lowering) read(e ast.Expr) Operand {
	if p, ok := l.placeOrPart(e); ok {
		l.emit(Instr{Op: OpRead, Place: p, Expr: e, Pos: e.Pos()})
		return p
	}

	return l.value(e, nil)
}

// composite lowers a struct, slice, array or map literal: its parts
// left to right, each given to its field or element.
func (l *lowering) composite(x *ast.CompositeLit) Operand {
	start := len(l.argStack)
	for i, elt := range x.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if _, isName := kv.Key.(*ast.Ident); !isName || l.c.info.Types[kv.Key] != nil {
				l.argStack = append(l.argStack, l.value(kv.Key, nil)) // a map's key is a value; a struct's field name is not
			}

			l.argStack = append(l.argStack, l.keptValue(kv.Value, "stored", l.slotType(x, i, kv.Key)))
			continue
		}

		if pe, ok := elt.(*ast.PartsElt); ok { // written once for every field of the type: its value is evaluated on each pass of a loop over them
			l.partsElt(pe)
			continue
		}

		l.argStack = append(l.argStack, l.keptValue(elt, "stored", l.slotType(x, i, nil)))
	}

	return l.madeFrom(x, start, true)
}

// keptValue lowers a value put where a closure that borrows may not go,
// how saying where, and records it as such.
func (l *lowering) keptValue(e ast.Expr, how string, want Type) Operand {
	l.keep(e, how, e.Pos())
	return l.value(e, want)
}

// partsElt lowers an element written for every field as a loop whose
// body evaluates the value and gives it to the field: a value moved into
// a field is moved on every pass, which the move pass refuses as moved
// in a previous iteration, and a closure that borrows cannot be stored.
func (l *lowering) partsElt(pe *ast.PartsElt) {
	head, exit := l.openLoop(pe.For)
	body := l.newBlock()
	l.goTo(head)
	l.edge(l.cur, exit)
	l.goTo(body)
	l.argStack = append(l.argStack, l.keptValue(pe.Value, "stored", nil))
	l.closeLoop(exit)
}

// slotType is the type of the slot the i-th element of x fills, as the
// checker gives it (Checker.slotType).
func (l *lowering) slotType(x *ast.CompositeLit, i int, key ast.Expr) Type {
	return l.c.slotType(x, i, key)
}

// madeFrom is made for the operands gathered on the stack from start,
// which it takes off.
func (l *lowering) madeFrom(e ast.Expr, start int, owned bool) Operand {
	t := l.made(e, l.argStack[start:], owned)
	l.argStack = l.argStack[:start]
	return t
}

// funcLit lowers a closure: its body as a form of its own, and here the
// making of the closure from its captures, each read, lent or moved by
// what the body does with it.
func (l *lowering) funcLit(x *ast.FuncLit) Operand {
	var form *Form
	if sig, ok := l.c.info.Types[x].(*Signature); ok && x.Body != nil {
		outer := l.c.lowering
		l.c.lowering = l.inner // the closure's body is lowered apart, by the scratch of the next depth, while this body's lowering is under way
		form = l.c.lowerBody(sig, x.Body)
		l.inner = l.c.lowering
		l.c.lowering = outer
		l.f.closures = append(l.f.closures, closureForm{form: form, sig: sig, pos: x.Pos()}) // checked once its body's form is built, under its body's graph
		l.inner.f = nil                                                                      // kept until then: the next closure gets a form of its own
	}

	spawned := l.spawning != nil && unparen(l.spawning.Fun) == x // the closure the spawn runs, whose captures move where it stands; a closure inside its arguments is not it
	captures := l.captures(x, form, spawned)
	uses := l.firstUses(x, captures)
	start := len(l.argStack)
	for i, cap := range captures {
		p := Operand{Var: l.index(cap.v)}
		use := uses[i] // a capture is read where the body first names it
		switch cap.mode {
		case byCopy:
			l.emit(Instr{Op: OpRead, Place: p, Capture: true, Expr: use, Pos: use.Pos()})
			l.argStack = append(l.argStack, p)
		case byRef, byMut:
			if cap.mode == byMut && cap.reads {
				l.emit(Instr{Op: OpRead, Place: p, Capture: true, Expr: use, Pos: use.Pos()}) // the closure reads the value when it runs: one moved before it is gone
			}

			tmp := l.temp(l.ref(cap.v.typ, cap.mode == byMut), false, use)
			l.emit(Instr{Op: OpBorrow, Place: p, To: tmp, Mut: cap.mode == byMut, Capture: true, Expr: use, Pos: use.Pos()})
			l.argStack = append(l.argStack, tmp)
		case byMove:
			expr, pos := use, use.Pos()
			if moved := movedIn(form, cap.v); moved != nil {
				expr, pos = moved.Expr, moved.Pos // the body's own move: the value is gone where the body moves it
			}

			if spawned {
				pos = x.Pos() // moved into the task where the closure stands
			}

			l.emit(Instr{Op: OpMove, Place: p, Capture: true, Expr: expr, Pos: pos})
			l.argStack = append(l.argStack, p)
		}
	}

	return l.madeFrom(x, start, true)
}

// captures records what the closure x captures, read off its form, for the
// typing's record and the emitters: a spawned closure takes what it
// captures with it, as a task has frames of its own, so each capture that
// is not copied in moves, when its type may cross to another task; and a
// closure that holds a value, copied or moved, hides the type it holds.
func (l *lowering) captures(x *ast.FuncLit, form *Form, spawned bool) []capture {
	if form == nil {
		return nil
	}

	c := l.c
	caps := formCaptures(form, x.Pos())
	for i := range caps {
		cap := &caps[i]
		if spawned && cap.mode != byCopy && c.taken(cap.v.name, cap.v.typ, l.spawning.Pos()) {
			cap.mode = byMove
		}

		if cap.mode == byCopy || cap.mode == byMove {
			c.hide(cap.v.typ, x.Pos(), "a closure") // the closure holds the value, and its type says nothing of it
		}
	}

	if c.captured == nil {
		c.captured = make(map[*ast.FuncLit][]capture)
	}

	c.captured[x] = caps
	vars := make([]Capture, len(caps))
	for i, cap := range caps {
		vars[i] = Capture{cap.v, cap.mode == byCopy}
	}

	c.info.Captures[x] = vars
	return caps
}

// formCaptures reads off a closure's form what the closure captures: each
// variable of the form declared before the closure begins, with the
// strongest use the body makes of it, moved, changed or lent mut, or
// read, where a Copy value read is copied in. A variable a closure inside
// the body captures counts as the body's own use of it.
func formCaptures(f *Form, closure token.Pos) []capture {
	modes, reads := make([]captureMode, len(f.Vars)), make([]bool, len(f.Vars))
	for i := range modes {
		modes[i] = byRef
	}

	for i := range f.Instrs {
		in := &f.Instrs[i]
		switch in.Op {
		case OpRead, OpMove, OpBorrow, OpStore:
		default:
			continue // no place of its own
		}

		if in.Place.IsTemp() {
			continue
		}

		if in.Op != OpStore {
			reads[in.Place.Var] = true
		}

		switch {
		case in.Op == OpMove:
			modes[in.Place.Var] = byMove
		case in.Op == OpStore || in.Op == OpBorrow && in.Mut || in.Op == OpRead && in.Mut:
			modes[in.Place.Var] = max(modes[in.Place.Var], byMut)
		}
	}

	var caps []capture
	for i, v := range f.Vars {
		if v.pos != token.NoPos && !v.field && !v.global && v.pos < closure { // declared before the closure: captured
			caps = append(caps, capture{v, captureModeOf(v, modes[i]), reads[i]})
		}
	}

	return caps
}

// movedIn is the first instruction of a closure's form that moves the
// captured variable v, or nil: where the body moves what it captures.
func movedIn(form *Form, v *Var) *Instr {
	if form == nil {
		return nil
	}

	for _, b := range form.order { // a move control cannot reach moves nothing
		blk := form.Blocks[b]
		for i := blk.From; i < blk.To; i++ {
			if in := &form.Instrs[i]; in.Op == OpMove && !in.Place.IsTemp() && form.Vars[in.Place.Var] == v {
				return in
			}
		}
	}

	return nil
}

// firstUses returns, for each capture of a closure, the first name in
// its body that refers to the captured variable, where the capture is
// made, in one walk of the body; a name inside a closure the body
// spawns gives that closure, where the variable moves into the task; a
// variable the body never names gives the closure itself.
func (l *lowering) firstUses(x *ast.FuncLit, captures []capture) []ast.Expr {
	uses := make([]ast.Expr, len(captures))
	if x.Body == nil { // a body the parser left out: the closure itself
		for i := range uses {
			uses[i] = x
		}

		return uses
	}

	left := len(captures)
	var spawned []*ast.FuncLit // the spawned closures met so far
	ast.Inspect(x.Body, func(n ast.Node) bool {
		if left == 0 {
			return false
		}

		switch n := n.(type) {
		case *ast.SpawnStmt:
			if lit := spawnedLit(n.Call); lit != nil {
				spawned = append(spawned, lit)
			}
		case *ast.SpawnExpr:
			if lit := spawnedLit(n.Call); lit != nil {
				spawned = append(spawned, lit)
			}
		case *ast.Ident:
			v, ok := l.c.info.Uses[n].(*Var)
			if !ok {
				break
			}

			for i, cap := range captures {
				if cap.v != v || uses[i] != nil {
					continue
				}

				uses[i] = n
				for _, lit := range spawned {
					if lit.Pos() <= n.Pos() && n.Pos() < lit.Body.Rbrace { // moved into the task where the spawned closure stands
						uses[i] = lit
						break
					}
				}

				left--
			}
		}

		return true
	})
	for i := range uses {
		if uses[i] == nil {
			uses[i] = x
		}
	}

	return uses
}

// spawnedLit is the closure a spawn's call runs, or nil when the spawn
// calls something else.
func spawnedLit(call ast.Expr) *ast.FuncLit {
	if x, ok := call.(*ast.CallExpr); ok {
		if lit, ok := unparen(x.Fun).(*ast.FuncLit); ok && lit.Body != nil {
			return lit
		}
	}

	return nil
}

// ifExpr lowers if as an expression: each arm's value stored into one
// temporary before the arm's names are dropped.
func (l *lowering) ifExpr(x *ast.IfExpr) Operand {
	if x.Then == nil {
		return Temp(NoTemp)
	}

	result := l.temp(l.typeOf(x), false, x)
	defer l.nest()() // the condition and the arms drop their own temporaries, not the statement's around the if
	l.condition(x.Cond)
	then, els, join := l.newBlock(), l.newBlock(), l.newBlock()
	l.branch(then, els)
	l.blockValue(x.Then, result)
	l.joinTo(join)
	l.start(els)
	switch e := x.Else.(type) {
	case *ast.BlockStmt:
		l.blockValue(e, result)
	case *ast.IfExpr:
		l.store(result, l.ifExpr(e), e)
	}

	l.joinTo(join)
	l.start(join)
	l.chosen(result, x)
	return result
}

// chosen marks the value of an expression with arms, chosen by the arm
// taken, as read where the arms join, which is where the statement
// takes it, and owned by the statement from there.
func (l *lowering) chosen(result Operand, x ast.Expr) {
	l.emit(Instr{Op: OpRead, Place: result, Expr: x, Pos: x.Pos()})
	l.own(result)
}

// blockValue lowers a block whose last statement is its value, stored
// into result before the block's names are dropped.
func (l *lowering) blockValue(b *ast.BlockStmt, result Operand) {
	l.openScope()
	if len(b.List) > 0 {
		last := len(b.List) - 1
		l.stmts(b.List[:last])
		if es, ok := b.List[last].(*ast.ExprStmt); ok && l.cur != noBlock {
			l.armValue(es.X, result)
		} else if l.cur != noBlock {
			l.stmt(b.List[last])
		}
	}

	l.closeScope(b.Rbrace)
}

// matchExpr lowers a match: the value, moved before the arms when an
// arm binds a part of it by value; then an arm per pattern, whose names
// are bound from the value, whose guard, when it fails, goes on to the
// next arm dropping what the pattern bound, and whose value is stored
// into one temporary.
func (l *lowering) matchExpr(x *ast.MatchExpr) Operand {
	if x.X == nil {
		return Temp(NoTemp)
	}

	result := l.temp(l.typeOf(x), false, x)
	scrutinee := l.scrutinee(x.X, l.bindsByValue(x))
	defer l.nest()() // the arms drop their own temporaries, not the value or the statement's around the match
	join := l.newBlock()
	next := l.cur
	for i, arm := range x.Arms {
		if arm.Pat == nil {
			continue
		}

		body := l.newBlock()
		if l.cur != next {
			l.start(next)
		}

		l.edge(l.cur, body)
		if i+1 < len(x.Arms) { // some arm is taken: the last has no path on to a next
			next = l.newBlock()
			l.edge(l.cur, next)
		}

		l.start(body)
		l.openScope()
		l.reachMut(x.X, scrutinee)
		if _, parts := arm.Pat.(*ast.PartsPat); parts && !scrutinee.IsTemp() { // the arm written for each variant reads the variant it is written for: the value is lent to it for the arm
			held := l.lend(scrutinee, x.X, false)
			l.f.Temps[held.Temp].Named = "the arm written for each variant"
		}

		l.bind(arm.Pat, scrutinee)
		if arm.Guard != nil {
			l.guardOf(x, arm, i+1 < len(x.Arms), next)
		}

		l.armBody(arm, result)
		l.joinTo(join)
	}

	l.start(join)
	l.chosen(result, x)
	return result
}

// guardOf lowers an arm's guard in blocks of its own, marked as the
// guard's: when it holds the arm goes on, and when it fails what the
// pattern bound is dropped on the way to the next arm, when there is
// one.
func (l *lowering) guardOf(x *ast.MatchExpr, arm *ast.MatchArm, hasNext bool, next int) {
	g := &Guard{What: guardWhat(x.Conds), From: arm.Guard.Pos(), To: arm.Arrow}
	outer := l.guard
	l.guard = g // every block the guard's own expressions make is the guard's; a match inside it marks its own guards' blocks
	l.goTo(l.newBlock())
	l.condition(arm.Guard)
	l.guard = outer
	taken, failed := l.newBlock(), l.newBlock()
	g.Taken = taken
	l.edge(l.cur, taken)
	l.goTo(failed)
	l.dropScope(arm.Arrow, dropBlock) // the guard failed: what the pattern bound is dropped on the way to the next arm
	if hasNext {
		l.edge(l.cur, next)
	}

	l.start(taken)
}

// armBody lowers an arm's body, an expression, a block or a statement,
// storing the value of an expression, or of the expression a block ends
// with, into result, then drops the arm's names.
func (l *lowering) armBody(arm *ast.MatchArm, result Operand) {
	switch body := arm.Body.(type) {
	case *ast.BlockStmt:
		l.blockValue(body, result) // a block ending in an expression gives the arm's value, as an if's arm does
	case ast.Expr:
		if l.cur != noBlock {
			l.armValue(body, result)
		}
	case ast.Stmt:
		l.stmt(body)
	}

	l.closeScope(arm.Arrow)
}

// scrutinee evaluates the value a match or an if-binding takes apart:
// an owned place of a non-Copy type whose part a name binds by value is
// moved before the arms; any other place is read, its parts bound
// through a borrow or copied; a value made here is held for the arms.
func (l *lowering) scrutinee(x ast.Expr, bindsByValue bool) Operand {
	if p, ok := l.placeOrPart(x); ok { // a place, or a part of a value made here, which an arm's binding by value may not move out of
		if t := l.typeOf(x); t != nil && !IsCopy(t) && !isBorrow(t) && l.kindOf(p) == placeOwned && bindsByValue {
			l.emit(Instr{Op: OpMove, Place: p, Expr: x, Pos: x.Pos()})
		} else {
			l.emit(Instr{Op: OpRead, Place: p, Expr: x, Pos: x.Pos()})
		}

		return p
	}

	return l.value(x, nil) // made here: the statement's, dropped at its end
}

// reachMut records that an arm matched through a mut borrow of the
// place the scrutinee x names reaches the place mut, as the arm's
// bindings borrow it mut; through a ref borrow or a value, nothing.
func (l *lowering) reachMut(x ast.Expr, v Operand) {
	if b, ok := l.typeOf(x).(*Ref); ok && b.mut && !v.IsTemp() {
		l.emit(Instr{Op: OpRead, Place: v, Mut: true, Expr: x, Pos: x.Pos()})
	}
}

// bindsByValue reports whether an arm of the match binds a part of the
// value by value, which moves the value before the arms.
func (l *lowering) bindsByValue(x *ast.MatchExpr) bool {
	for _, arm := range x.Arms {
		if l.patBindsByValue(arm.Pat) {
			return true
		}
	}

	return false
}

// patBindsByValue reports whether a pattern binds a name of a non-Copy
// type by value.
func (l *lowering) patBindsByValue(p ast.Pattern) bool {
	found := false
	ast.Inspect(p, func(n ast.Node) bool {
		if id, ok := n.(*ast.IdentPat); ok {
			if v, ok := l.c.info.Defs[id.Name].(*Var); ok && !IsCopy(v.typ) { // a mut borrow bound by value moves, as a name given one does
				found = true
			}
		}

		return !found
	})
	return found
}

// coalesce lowers x ?? y: the value, then a branch where None takes the
// else, an expression stored into the result or a statement that
// leaves.
func (l *lowering) coalesce(x *ast.CoalesceExpr) Operand {
	v := l.scrutinee(x.X, !IsCopy(l.typeOf(x))) // the value inside leaves an owned place when it is not Copy
	result := l.temp(l.typeOf(x), false, x)
	defer l.nest()() // the else drops its own temporaries, not the statement's around the ??
	some, none, join := l.newBlock(), l.newBlock(), l.newBlock()
	l.branch(some, none)
	l.unwrap(result, v, x, x.OpPos)
	l.joinTo(join)
	l.start(none)
	switch e := x.Else.(type) {
	case ast.Expr:
		l.armValue(e, result)
	case ast.Stmt:
		l.stmt(e)
	}

	l.joinTo(join)
	l.start(join)
	l.chosen(result, x)
	return result
}

// try lowers x?: the value, then a branch where the error returns,
// dropping what every open scope holds.
func (l *lowering) try(x *ast.TryExpr) Operand {
	v := l.scrutinee(x.X, true) // the Result or the option leaves an owned place when it is not Copy: its error or its None goes to the caller
	result := l.temp(l.typeOf(x), false, x)
	defer l.nest()() // the leave drops what the ? made, and what the leave itself abandons around it
	ok, fail, join := l.newBlock(), l.newBlock(), l.newBlock()
	l.edge(l.cur, ok)
	l.goTo(fail)
	l.take(v)
	l.emit(Instr{Op: OpReturn, Args: l.args(v), Expr: x, Pos: x.Question}) // the None or the Err goes to the caller
	l.leave(0, x.Question, x)
	l.start(ok)
	l.unwrap(result, v, x, x.Question)
	l.goTo(join)
	l.chosen(result, x)
	return result
}

// unwrap makes result from the value inside v, an option or a result
// that holds one: the value inside is the result's, and v, which held
// it, has nothing left to drop.
func (l *lowering) unwrap(result, v Operand, e ast.Expr, pos token.Pos) {
	l.emit(Instr{Op: OpMake, To: result, Args: l.args(v), Expr: e, Pos: pos})
	l.take(v)
}
