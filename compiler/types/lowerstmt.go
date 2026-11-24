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
		t := l.temp(l.typeOf(s.X), false)
		l.emit(Instr{Op: OpMake, To: t, Args: l.args(p), Expr: s.X, Pos: s.TokPos})
		l.store(l.place(s.X, true), t, s.X)
		l.dropOwned(s.TokPos)
	case *ast.SendStmt:
		ch := l.channel(s.Chan)
		v := l.value(s.Value, l.sendType(s.Chan))
		l.take(v)
		l.emit(Instr{Op: OpCall, To: l.temp(Unit, false), Args: l.args(ch, v), Expr: s.Value, Pos: s.Arrow})
		l.dropOwned(s.Arrow)
	case *ast.SpawnStmt:
		l.spawned(s.Call, s.Spawn)
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
	values := l.values(len(d.Names), d.Values, func(i int) Type { return l.namedType(d.Names[i]) })
	for i, name := range d.Names {
		v, ok := l.c.info.Defs[name].(*Var)
		if !ok || isBlank(name) {
			continue
		}

		idx := l.declare(v)
		if i < len(values) {
			l.store(Operand{Var: idx}, values[i], name)
		} else {
			l.emit(Instr{Op: OpStore, Place: Operand{Var: idx}, To: l.temp(v.typ, false), Expr: name, Pos: name.Pos()})
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
// one operand each: one value per name, or the parts of one tuple. The
// operands are gathered on the argument stack from where it stood, for
// the caller to take off once they are stored.
func (l *lowering) values(n int, exprs []ast.Expr, want func(i int) Type) []Operand {
	start := len(l.argStack)
	if len(exprs) == 1 && n > 1 {
		t := l.value(exprs[0], nil)
		l.take(t)
		for i := 0; i < n; i++ {
			l.argStack = append(l.argStack, t.sub(strconv.Itoa(i)))
		}

		return l.argStack[start:]
	}

	for i, e := range exprs {
		var w Type
		if i < n {
			w = want(i)
		}

		l.argStack = append(l.argStack, l.value(e, w))
	}

	return l.argStack[start:]
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
		})
		for i, lhs := range s.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || isBlank(id) || i >= len(values) {
				continue
			}

			if p, ok := l.target(id, true); ok {
				l.store(p, values[i], id)
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
		})
		for i, lhs := range s.Lhs {
			if !isBlank(lhs) && i < len(values) {
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

		t := l.temp(l.typeOf(lhs), false)
		l.emit(Instr{Op: OpMake, To: t, Args: l.args(p, v), Expr: lhs, Pos: s.TokPos})
		l.reachAgain(lhs)
		l.store(l.place(lhs, true), t, lhs)
	}

	l.argStack = l.argStack[:start]
	l.dropOwned(s.TokPos)
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
func (l *lowering) store(p, o Operand, e ast.Expr) {
	l.take(o)
	l.emit(Instr{Op: OpStore, Place: p, To: o, Expr: e, Pos: e.Pos()})
}

// armValue lowers the expression an arm or a block ends with, stores
// its value into result, and drops the arm's temporaries.
func (l *lowering) armValue(e ast.Expr, result Operand) {
	v := l.value(e, l.tempTypes[result.Temp])
	l.store(result, v, e)
	l.dropOwned(e.Pos())
}

func (l *lowering) deferStmt(s *ast.DeferStmt) {
	call, ok := s.Call.(*ast.CallExpr)
	if !ok {
		return
	}

	args := l.callArgs(call, l.calleeOf(call))
	for _, a := range args {
		l.take(a) // the deferred call holds them until the function returns
	}

	l.emit(Instr{Op: OpDefer, Args: args, Expr: s.Call, Pos: s.Defer})
	l.dropOwned(s.Defer)
}

func (l *lowering) returnStmt(s *ast.ReturnStmt) {
	switch {
	case len(s.Results) == 1:
		l.take(l.value(s.Results[0], l.results))
	case len(s.Results) > 1:
		tuple, _ := l.results.(*Tuple)
		for i, r := range s.Results {
			var w Type
			if tuple != nil && i < len(tuple.elems) {
				w = tuple.elems[i]
			}

			l.take(l.value(r, w))
		}
	}

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
	l.dropOwned(e.Pos())
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
	l.bind(s.Pat, v)
	l.stmts(s.Body.List)
	l.closeScope(s.Body.Rbrace)
	l.joinTo(join)
	if s.Else != nil {
		l.start(els)
		l.stmt(s.Else)
		l.joinTo(join)
	}

	l.start(join)
	l.dropOwned(s.Body.Rbrace)
}

// bind declares the names a pattern binds and gives each what it binds
// out of the value v.
func (l *lowering) bind(p ast.Pattern, v Operand) {
	ast.Inspect(p, func(n ast.Node) bool {
		if id, ok := n.(*ast.IdentPat); ok {
			if bound, ok := l.c.info.Defs[id.Name].(*Var); ok {
				l.store(Operand{Var: l.declare(bound)}, v, id.Name)
			}
		}

		return true
	})
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

	head, exit := l.newBlock(), l.newBlock() // the exit is outside the loop
	l.f.Blocks[head].Head, l.f.Blocks[head].Pos = true, s.For
	l.loops = append(l.loops, lowLoop{head: head, exit: exit, scopes: len(l.scopes)})
	body, post := l.newBlock(), l.newBlock()
	l.loops[len(l.loops)-1].cont = post
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

// rangeStmt lowers a range loop: the collection once, moved when an
// owned slice is ranged by value and lent otherwise; then each pass
// binds the item and runs the body.
func (l *lowering) rangeStmt(s *ast.RangeStmt) {
	if s.X == nil || s.Body == nil {
		return
	}

	x := l.rangeSource(s.X) // before the loop's scope: a break or a continue leaves it be, the loop's end drops it
	l.openScope()
	head, exit := l.newBlock(), l.newBlock() // the exit is outside the loop
	l.f.Blocks[head].Head, l.f.Blocks[head].Pos = true, s.For
	l.loops = append(l.loops, lowLoop{head: head, cont: head, exit: exit, scopes: len(l.scopes)})
	body := l.newBlock()
	l.goTo(head)
	l.edge(l.cur, exit)
	l.goTo(body)
	l.openScope()
	item := x.sub("[]")
	for _, e := range []ast.Expr{s.Key, s.Value} {
		if e == nil || isBlank(e) {
			continue
		}

		id, ok := e.(*ast.Ident)
		if !ok {
			l.store(l.place(e, false), item, e)
		} else if p, ok := l.target(id, s.Tok == token.DEFINE); ok {
			l.store(p, item, id)
		}
	}

	l.stmts(s.Body.List)
	l.closeScope(s.Body.Rbrace)
	l.joinTo(head)
	l.loops = l.loops[:len(l.loops)-1]
	l.start(exit)
	l.closeScope(s.Body.Rbrace)
	l.dropOwned(s.Body.Rbrace)
}

// rangeSource evaluates what a range loop walks, once: an owned place
// of a non-Copy type ranged by value is moved, an iterator or a channel
// is lent mut, any other place is lent, and a value made here is held
// for the loop.
func (l *lowering) rangeSource(x ast.Expr) Operand {
	t := l.typeOf(x)
	if p, ok := l.placeOf(x); ok && t != nil {
		switch {
		case isBorrow(t):
			l.emit(Instr{Op: OpRead, Place: p, Expr: x, Pos: x.Pos()})
			return p
		case l.kindOf(p) != placeOwned: // reached through a borrow or an element: lent for the loop, since it cannot be moved out
			return l.lend(p, x, false)
		case isChan(t), IteratorNext(t) != nil:
			return l.lend(p, x, true)
		case isString(t):
			return l.lend(p, x, false)
		case IsCopy(t):
			l.emit(Instr{Op: OpRead, Place: p, Expr: x, Pos: x.Pos()})
			return p
		}

		l.emit(Instr{Op: OpMove, Place: p, Expr: x, Pos: x.Pos()})
		return p
	}

	return l.value(x, nil) // made here: the range statement's, dropped when the loop ends
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
			if v := values[i]; v.IsTemp() && v.Temp != NoTemp {
				l.emit(Instr{Op: OpDrop, Place: v, Pos: cc.Case}) // received and not bound: the case drops it
			}
		case *ast.AssignStmt:
			for _, lhs := range comm.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && !isBlank(id) {
					if p, ok := l.target(id, comm.Tok == token.DEFINE); ok {
						l.store(p, values[i], id)
					}
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
