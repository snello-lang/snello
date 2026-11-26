// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The lowering of statements: each in the order the program runs it,
// its temporaries dropped at its end, its scopes' variables where they
// end, and a path that leaves with its drops.

// stmts lowers the statements of a block in order, stopping at one
// after which nothing runs.
func (l *lowering) stmts(list []ast.Stmt) {
	for _, s := range list {
		if l.cur == noBlock {
			return
		}

		l.stmt(s)
	}
}

func (l *lowering) stmt(s ast.Stmt) {
	defer l.nest()() // a statement inside another, in an arm, drops its own temporaries and leaves the outer statement's
	bases := len(l.bases)
	defer func() { l.bases = l.bases[:bases] }()
	switch s := s.(type) {
	case *ast.BlockStmt:
		l.block(s)
	case *ast.DeclStmt:
		if !s.Decl.Const {
			l.varDecl(s.Decl)
		}
	case *ast.ExprStmt:
		l.value(s.X, nil)
		l.dropOwned(s.X.Pos())
	case *ast.AssignStmt:
		l.assign(s)
	case *ast.IncDecStmt:
		p := l.place(s.X, false)
		l.emit(Instr{Op: OpRead, Place: p, Expr: s.X, Pos: s.X.Pos()})
		t := l.temp(l.typeOf(s.X), false, s.X)
		l.emit(Instr{Op: OpMake, To: t, Args: l.args(p), Expr: s.X, Pos: s.TokPos})
		l.store(l.place(s.X, true), t, s.X)
		l.dropOwned(s.Pos())
	case *ast.SendStmt:
		ch := l.channel(s.Chan)
		v := l.keptValue(s.Value, "sent", l.sendType(s.Chan))
		l.take(v)
		l.emit(Instr{Op: OpCall, To: l.temp(Unit, false, s.Value), Args: l.args(ch, v), Expr: s.Value, Pos: s.Arrow})
		l.dropOwned(s.Pos())
	case *ast.SpawnStmt:
		l.spawned(s.Call)
		l.dropOwned(s.Spawn)
	case *ast.DeferStmt:
		l.deferStmt(s)
	case *ast.ReturnStmt:
		l.returnStmt(s)
	case *ast.BranchStmt:
		l.branchStmt(s)
	case *ast.IfStmt:
		l.ifStmt(s)
	case *ast.IfBindStmt:
		l.ifBind(s)
	case *ast.ForStmt:
		l.forStmt(s)
	case *ast.RangeStmt:
		l.rangeStmt(s)
	case *ast.SelectStmt:
		l.selectStmt(s)
	}
}

func (l *lowering) block(b *ast.BlockStmt) {
	if b == nil { // a block the parser could not finish
		return
	}

	l.openScope()
	l.stmts(b.List)
	l.closeScope(b.Rbrace)
}

// varDecl lowers var x, y T = a, b: the values left to right, then the
// declarations and stores; a name declared without a value holds its
// type's zero value, made here.
func (l *lowering) varDecl(d *ast.VarDecl) {
	start := len(l.argStack)
	values := l.values(len(d.Names), d.Values, func(i int) Type { return l.namedType(d.Names[i]) }, nil) // a declaration's names are new: the values read none of them
	for i, name := range d.Names {
		v, ok := l.c.info.Defs[name].(*Var)
		if !ok || isBlank(name) {
			continue
		}

		if i < len(values) {
			l.declareStore(v, values[i], name)
		} else {
			l.declareStore(v, l.temp(v.typ, false, name), name)
		}
	}

	l.argStack = l.argStack[:start]
	l.dropOwned(d.Pos())
}

// namedType is the type of the variable a name declares or refers to,
// nil for a blank name or one the checker could not type.
func (l *lowering) namedType(id *ast.Ident) Type {
	if v := l.named(id); v != nil {
		return v.typ
	}

	return nil
}

// values lowers the values given to n names or places, left to right,
// one operand each: one value per name, or the parts of one tuple. A
// value read from a place is taken into a temporary when the statement
// has several values, so that a, b = b, a gives each place what was
// read, and a value that carries a borrow out of a variable one of the
// places lhs names is carried, so that a = id(a) and r := r keep what
// the value read. The operands are gathered on the argument stack from
// where it stood, for the caller to take off once they are stored.
func (l *lowering) values(n int, exprs []ast.Expr, want func(i int) Type, lhs []ast.Expr) []Operand {
	start := len(l.argStack)
	if len(exprs) == 1 && n > 1 {
		t := l.value(exprs[0], nil)
		l.take(t)
		for i := 0; i < n; i++ {
			l.argStack = append(l.argStack, t.sub("?"+strconv.Itoa(i)))
		}

		return l.argStack[start:]
	}

	for i, e := range exprs {
		var w Type
		if i < n {
			w = want(i)
		}

		o := l.value(e, w)
		switch {
		case i < len(lhs) && holdsBorrow(l.typeOf(e)) && l.c.readsAny(e, lhs):
			o = l.carry(e, o, lhs[i].Pos())
		case !o.IsTemp() && n > 1:
			o = l.made(e, l.args(o), true)
		}

		if n > 1 {
			l.given(o) // a value of a statement with several keeps what it borrows lent until it is stored, as an argument does for its call
		}

		l.argStack = append(l.argStack, o)
	}

	return l.argStack[start:]
}

// carry takes the value o of e, which reads a variable its statement
// stores, into a temporary made where the place at pos is stored: the
// temporary holds what the value lends before any store lets go of it.
func (l *lowering) carry(e ast.Expr, o Operand, pos token.Pos) Operand {
	t := l.madeAt(e, l.args(o), true, pos)
	l.f.Temps[t.Temp].Carried = true
	return t
}

// assign lowers =, := and the operator assignments in the order the
// program runs them: the indexes and keys of the places, the values,
// then each store, where the rest of the place is reached; a compound
// assignment reads its place, makes the value, and reaches the place
// again to store.
func (l *lowering) assign(s *ast.AssignStmt) {
	start := len(l.argStack)
	switch s.Tok {
	case token.DEFINE:
		values := l.values(len(s.Lhs), s.Rhs, func(i int) Type {
			id, _ := s.Lhs[i].(*ast.Ident)
			return l.namedType(id) // a name := reuses is assigned, with its type
		}, s.Lhs)
		for i, lhs := range s.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && !isBlank(id) && i < len(values) {
				l.storeName(id, true, values[i])
			}
		}
	case token.ASSIGN:
		for _, lhs := range s.Lhs {
			if !isBlank(lhs) {
				l.indexes(lhs)
			}
		}

		values := l.values(len(s.Lhs), s.Rhs, func(i int) Type {
			if isBlank(s.Lhs[i]) {
				return nil
			}

			return l.typeOf(s.Lhs[i])
		}, s.Lhs)
		for i, lhs := range s.Lhs {
			if !isBlank(lhs) && i < len(values) {
				if _, name := lhs.(*ast.Ident); !name && len(s.Rhs) == len(s.Lhs) {
					l.keep(s.Rhs[i], "stored", lhs.Pos()) // a field or an element keeps the value
				}

				l.store(l.place(lhs, true), values[i], lhs)
			}
		}
	default:
		lhs := s.Lhs[0]
		p := l.place(lhs, false)
		l.emit(Instr{Op: OpRead, Place: p, Expr: lhs, Pos: lhs.Pos()})
		var v Operand
		if fn := OperatorMethod(stripRef(l.typeOf(lhs)), s.Tok.Binary(), false); fn != nil { // a type's own operator: the right side goes to its parameter
			v = l.value(s.Rhs[0], fn.typ.(*Signature).params[0].typ)
			l.take(v)
		} else {
			v = l.read(s.Rhs[0]) // an operator reads its right side; nothing moves
		}

		t := l.temp(l.typeOf(lhs), false, lhs)
		l.emit(Instr{Op: OpMake, To: t, Args: l.args(p, v), Expr: lhs, Pos: s.TokPos})
		l.reachAgain(lhs)
		l.store(l.place(lhs, true), t, lhs)
	}

	l.argStack = l.argStack[:start]
	l.dropOwned(s.Pos())
}

// reachAgain reads again what a call the place is reached through reads
// of its arguments, where the store reaches the place a second time: the
// call ran once, but the store finds what the value moved or lent since.
func (l *lowering) reachAgain(e ast.Expr) {
	ast.Inspect(e, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		for _, arg := range call.Args {
			a := arg
			if u, ok := unparen(arg).(*ast.UnaryExpr); ok && (u.Op == token.REF || u.Op == token.MUT) {
				a = u.X
			}

			if p, ok := l.placeOf(a); ok {
				l.emit(Instr{Op: OpRead, Place: p, Expr: a, Pos: a.Pos()})
			}
		}

		return false
	})
}

// store writes the value o into the place p: a temporary stored is
// taken, so that the statement's end drops it no more.
func (l *lowering) store(p, o Operand, e ast.Expr) { l.storeAs(p, o, e, false) }

// storeAs is store, declaring the place's variable when decl says so.
func (l *lowering) storeAs(p, o Operand, e ast.Expr, decl bool) {
	l.take(o)
	l.bound(o)
	l.emit(Instr{Op: OpStore, Place: p, To: o, Decl: decl, Expr: e, Pos: e.Pos()})
}

// armValue lowers the expression an arm or a block ends with, stores
// its value into result, and drops the arm's temporaries.
func (l *lowering) armValue(e ast.Expr, result Operand) {
	v := l.value(e, l.f.Temps[result.Temp].Type)
	l.store(result, v, e)
	l.dropOwned(e.Pos())
}

func (l *lowering) deferStmt(s *ast.DeferStmt) {
	call, ok := s.Call.(*ast.CallExpr)
	if !ok {
		return
	}

	args, _ := l.callArgs(call, l.c.calledBy(call))
	for _, a := range args {
		l.take(a) // the deferred call holds them until the function returns
	}

	l.emit(Instr{Op: OpDefer, Args: args, Expr: s.Call, Pos: s.Defer})
	l.dropOwned(s.Defer)
}

func (l *lowering) returnStmt(s *ast.ReturnStmt) {
	start := len(l.argStack)
	switch {
	case len(s.Results) == 1:
		l.argStack = append(l.argStack, l.value(s.Results[0], l.results))
	case len(s.Results) > 1:
		tuple, _ := l.results.(*Tuple)
		for i, r := range s.Results {
			var w Type
			if tuple != nil && i < len(tuple.elems) {
				w = tuple.elems[i]
			}

			l.argStack = append(l.argStack, l.value(r, w))
		}
	}

	for i, v := range l.argStack[start:] { // one return per value, each with its expression: what each lets out is checked by it
		l.take(v)
		l.emit(Instr{Op: OpReturn, Args: l.args(v), Expr: s.Results[i], Pos: s.Return})
	}

	l.argStack = l.argStack[:start]

	l.leave(0, s.Return, nil)
}

func (l *lowering) branchStmt(s *ast.BranchStmt) {
	if len(l.loops) == 0 {
		return
	}

	loop := l.loops[len(l.loops)-1]
	to := loop.exit
	if s.Tok == token.CONTINUE {
		to = loop.cont
	}

	l.leave(loop.scopes, s.TokPos, nil, to)
}

// ifStmt lowers if, with its init in a scope of its own, its condition,
// then an arm for each branch, joined after.
func (l *lowering) ifStmt(s *ast.IfStmt) {
	if s.Body == nil {
		return
	}

	l.openScope()
	if s.Init != nil {
		l.stmt(s.Init)
	}

	l.condition(s.Cond)
	then, join := l.newBlock(), l.newBlock()
	els := join
	if s.Else != nil {
		els = l.newBlock()
	}

	l.branch(then, els)
	l.block(s.Body)
	l.joinTo(join)
	if s.Else != nil {
		l.start(els)
		l.stmt(s.Else)
		l.joinTo(join)
	}

	l.start(join)
	l.closeScope(s.Body.Rbrace)
}

// condition evaluates a condition, whose temporaries are dropped once
// it is decided, before any arm runs.
func (l *lowering) condition(e ast.Expr) {
	if e == nil {
		return
	}

	l.value(e, Typ[Bool])
	l.dropOwnedAs(e.Pos(), dropCondition)
}

// ifBind lowers if p := o { }: the value, then a branch whose arm binds
// the pattern's names from it.
func (l *lowering) ifBind(s *ast.IfBindStmt) {
	if s.Value == nil || s.Body == nil || s.Pat == nil {
		return
	}

	v := l.scrutinee(s.Value, l.patBindsByValue(s.Pat))
	then, join := l.newBlock(), l.newBlock()
	els := join
	if s.Else != nil {
		els = l.newBlock()
	}

	l.branch(then, els)
	l.openScope()
	l.reachMut(s.Value, v)
	l.bind(s.Pat, v.sub("?Some.?0")) // the pattern binds the payload
	l.stmts(s.Body.List)
	l.closeScope(s.Body.Rbrace)
	l.joinTo(join)
	if s.Else != nil {
		l.start(els)
		l.stmt(s.Else)
		l.joinTo(join)
	}

	l.start(join)
	l.dropOwned(s.Pos())
}

// bind declares the names a pattern binds and gives each the part of
// the value v it binds: a tuple's element by its index, a variant's
// field by the variant's name and the field's index, the whole for a
// name alone.
func (l *lowering) bind(p ast.Pattern, v Operand) {
	switch p := p.(type) {
	case *ast.IdentPat:
		if bound, ok := l.c.info.Defs[p.Name].(*Var); ok {
			l.declareStore(bound, v, p.Name)
		}
	case *ast.TuplePat:
		for i, elt := range p.Elts {
			l.bind(elt, v.sub("?"+strconv.Itoa(i)))
		}
	case *ast.VariantPat:
		for i, arg := range p.Args {
			l.bind(arg, v.sub("?"+calleeName(p.Path).Name+".?"+strconv.Itoa(i)))
		}
	case *ast.OrPat:
		for _, alt := range p.Alts {
			l.bind(alt, v)
		}
	}
}

// forStmt lowers a for loop: its init in a scope of its own; a head
// where each pass begins; the test, out of which the loop exits; the
// body; the post statement, which a continue goes to; and back to the
// head.
func (l *lowering) forStmt(s *ast.ForStmt) {
	if s.Body == nil {
		return
	}

	l.openScope()
	if s.Init != nil {
		l.stmt(s.Init)
	}

	head, exit := l.openLoop(loopBegins(s))
	body, post := l.newBlock(), l.newBlock()
	l.loops[len(l.loops)-1].cont = post // a continue runs the post statement
	l.goTo(head)
	if s.Cond != nil {
		l.condition(s.Cond)
		l.edge(l.cur, exit)
	}

	l.goTo(body)
	l.block(s.Body)
	l.joinTo(post)
	l.start(post)
	if s.Post != nil {
		l.stmt(s.Post)
	}

	l.joinTo(head)
	l.loops = l.loops[:len(l.loops)-1]
	l.start(exit)
	l.closeScope(s.Body.Rbrace)
}

// loopBegins is where a for loop's passes begin, after its init, which
// runs once: a variable the init declares is declared before the loop,
// and one moved in a pass is moved in the next.
func loopBegins(s *ast.ForStmt) token.Pos {
	if s.Cond != nil {
		return s.Cond.Pos()
	}

	return s.Body.Lbrace
}

// rangeStmt lowers a range loop: the collection once, moved when an
// owned slice is ranged by value and lent otherwise; then each pass
// binds the item and runs the body.
func (l *lowering) rangeStmt(s *ast.RangeStmt) {
	if s.X == nil || s.Body == nil {
		return
	}

	x := l.rangeSource(s.X, s.For) // before the loop's scope: a break or a continue leaves it be, the loop's end drops it
	l.openScope()
	head, exit := l.openLoop(s.For)
	body := l.newBlock()
	l.goTo(head)
	l.edge(l.cur, exit)
	l.goTo(body)
	l.openScope() // the pass's: its key and value, dropped where the pass ends
	item := x.sub("[]")
	for _, e := range []ast.Expr{s.Key, s.Value} {
		if e == nil || isBlank(e) {
			continue
		}

		if id, ok := e.(*ast.Ident); ok {
			l.storeName(id, s.Tok == token.DEFINE, item)
		} else {
			l.store(l.place(e, false), item, e)
		}
	}

	l.block(s.Body)
	l.closePass(s.Body.Rbrace)
	l.closeLoop(exit)
	l.closeScope(s.Body.Rbrace)
	l.dropOwned(s.For)
}

// rangeSource evaluates what a range loop walks, once: an owned place
// of a non-Copy type ranged by value is moved, an iterator or a channel
// is lent mut, any other place is lent, and a value made here is held
// for the loop.
func (l *lowering) rangeSource(x ast.Expr, pos token.Pos) Operand {
	t := l.typeOf(x)
	if p, ok := l.placeOf(x); ok && t != nil {
		switch {
		case isBorrow(t): // a borrow variable is lent again, for the loop
			l.emit(Instr{Op: OpRead, Place: p, Expr: x, Pos: x.Pos()})
			return l.lendLoop(p, x, isMut(t), pos)
		case l.kindOf(p) != placeOwned: // reached through a borrow or an element: lent for the loop, since it cannot be moved out
			return l.lendLoop(p, x, false, pos)
		case isChan(t), IteratorNext(t) != nil:
			return l.lendLoop(p, x, true, pos)
		case isString(t), holdsBorrow(t): // a string's runes are copies, and a collection of borrows lends what they borrow: neither is consumed
			return l.lendLoop(p, x, false, pos)
		case IsCopy(t):
			l.emit(Instr{Op: OpRead, Place: p, Expr: x, Pos: x.Pos()})
			return p
		}

		l.emit(Instr{Op: OpMove, Place: p, Expr: x, Pos: x.Pos()})
		return p
	}

	v := l.value(x, nil)                             // made here: the range statement's, dropped when the loop ends
	if t != nil && (isBorrow(t) || holdsBorrow(t)) { // what it lends is lent to the loop, for every pass, and the loop holds the value
		l.take(v)
		mut, _ := borrowIn(t)
		return l.lendLoop(v, x, mut, pos)
	}

	return v
}

// lendLoop lends the place p, or what the value p lends, to the range
// loop, at the for, for every pass: the loop holds it until it ends.
func (l *lowering) lendLoop(p Operand, e ast.Expr, mut bool, pos token.Pos) Operand {
	tmp := l.lendAt(p, e, mut, pos)
	l.f.Temps[tmp.Temp].Loop = true
	return tmp
}

// selectStmt lowers select: each case's channel, and a send's value,
// in order; then one arm per case, joined after.
func (l *lowering) selectStmt(s *ast.SelectStmt) {
	start := len(l.argStack) // each case's value, sent or received, which the case taken alone takes
	for _, cc := range s.Body {
		var v Operand
		switch comm := cc.Comm.(type) {
		case *ast.SendStmt:
			l.channel(comm.Chan)
			if p, ok := l.placeOf(comm.Value); ok { // read now, taken by the case alone when it is the one chosen
				l.emit(Instr{Op: OpRead, Place: p, Expr: comm.Value, Pos: comm.Value.Pos()})
				v = p
			} else {
				v = l.value(comm.Value, l.sendType(comm.Chan))
				l.take(v)
			}
		case *ast.ExprStmt:
			v = l.value(comm.X, nil)
			l.take(v)
		case *ast.AssignStmt:
			if len(comm.Rhs) == 1 {
				v = l.value(comm.Rhs[0], nil)
				l.take(v)
			}
		}

		l.argStack = append(l.argStack, v)
	}

	values := l.argStack[start:]
	join := l.newBlock()
	from := l.cur
	l.endBlock()
	for i, cc := range s.Body {
		arm := l.newBlock()
		l.edge(from, arm)
		l.start(arm)
		l.openScope()
		switch comm := cc.Comm.(type) {
		case *ast.SendStmt:
			if v := values[i]; !v.IsTemp() {
				if t := l.sendType(comm.Chan); t != nil && !IsCopy(t) {
					l.emit(Instr{Op: OpMove, Place: v, Expr: comm.Value, Pos: comm.Value.Pos()})
				}
			}
		case *ast.ExprStmt:
			if v := values[i]; v.IsValue() {
				l.emit(Instr{Op: OpDrop, Place: v, Pos: cc.Case}) // received and not bound: the case drops it
			}
		case *ast.AssignStmt:
			for _, lhs := range comm.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && !isBlank(id) {
					l.storeName(id, comm.Tok == token.DEFINE, values[i])
				}
			}
		}

		l.stmts(cc.Body)
		l.closeScope(cc.Case)
		l.joinTo(join)
	}

	l.argStack = l.argStack[:start]
	l.start(join)
	l.dropOwned(s.Select)
}
