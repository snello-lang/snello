// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"slices"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The move pass runs inside the checker's walk of
// a body. A value goes to a new owner when it is assigned, passed by
// value, returned, sent, put into a literal or captured by a closure
// that consumes it; after that the old name is dead. The pass keeps the
// list of places moved on the path being checked, forks the list at a
// branch and joins the branches' lists after, and keeps a loop's list to
// report a value moved on one pass and reached again on the next. A
// Copy value is never moved. See _docs/book/05-ownership.md for the
// rules as the reader sees them.

// A place is a variable, or a field path inside one: n, n.value.
type place struct {
	v    *Var
	path string // the field names after the variable, joined by dots
}

// A placeKind says how a place is reached: owned, through a borrow, or
// through an index, which lends the element.
type placeKind int

const (
	notPlace placeKind = iota
	placeOwned
	placeBorrowed
	placeIndexed
)

// A move records a place that was moved and where.
type move struct {
	place
	pos     token.Pos
	revived bool // not a move: the place was given a new value here, after a move recorded before the enclosing branch or loop began
}

// A region is a loop or select open at the current point, with
// the records of the paths that leave it: ends are the paths reaching
// its end or a continue, which for a loop reach its next iteration, and
// exits the paths leaving by break. A path with no records is counted
// but not kept.
type region struct {
	loop     bool
	infinite bool // a loop with no condition: only a break reaches the point after it
	pos      token.Pos
	mark     int // len(moved) at entry
	ends     [][]move
	exits    [][]move
	ended    int
	exited   int
}

// A fork remembers the state at a branch point, so that every branch
// starts from it and their results are joined after.
type fork struct {
	mark     int
	dead     bool
	paths    [][]move // the records of the branches that do not leave, those with any
	live     int      // the branches that do not leave
	all      bool     // whether every branch so far leaves
	branches int
}

// A floor is where the records of an open branch set, region or closure
// body begin in moved. What lies under it belongs to the enclosing
// paths and is not undone from inside (giveBack). A match watches its
// scrutinee: an arm that assigns it is recorded even before an arm
// binding a part by value puts the scrutinee's move under the fork.
type floor struct {
	mark     int
	watch    place
	watching bool
}

func (p place) text() string {
	if p.path == "" {
		return p.v.name
	}

	return p.v.name + "." + p.path
}

// within reports whether p is q or lies inside q.
func (p place) within(q place) bool {
	return p.v == q.v && (q.path == "" || p.path == q.path || len(p.path) > len(q.path) && p.path[:len(q.path)] == q.path && p.path[len(q.path)] == '.')
}

// resolve finds the object a name in an expression already checked
// denotes: in the current scope, or, while the bindings of a match arm
// or an if-binding are checked against the value they take apart, in
// the scope the value was written in, since a binding may reuse a name
// the value reads, if o := o, and the move or borrow of the value is
// the old o's.
func (c *Checker) resolve(name string) Object {
	s := c.scope
	if c.names != nil {
		s = c.names
	}

	_, obj := s.LookupParent(name)
	return obj
}

// resolvingIn runs f with the names of expressions resolved in outer.
func (c *Checker) resolvingIn(outer *Scope, f func()) {
	saved := c.names
	c.names = outer
	f()
	c.names = saved
}

// placeOf resolves an expression to the place it denotes and how it is
// reached. Names are looked up again rather than carried on operands,
// so that nothing is allocated per expression on the way.
func (c *Checker) placeOf(e ast.Expr) (place, placeKind) {
	switch e := e.(type) {
	case *ast.Ident:
		if e.Name == "super" { // the receiver of the method being checked (superCall)
			if c.recv == nil {
				return place{}, notPlace
			}

			return place{c.recv, ""}, placeOwned
		}

		v, ok := c.resolve(e.Name).(*Var)
		if !ok || v.field || v.pos == token.NoPos {
			return place{}, notPlace
		}

		return place{v, ""}, placeOwned // the variable itself, even when its value is a borrow
	case *ast.ParenExpr:
		return c.placeOf(e.X)
	case *ast.UnaryExpr:
		if e.Op == token.MUL {
			return through(c.placeOf(e.X))
		}
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok { // a package variable of another package, os.Args: a place, so that the rules of a package variable reach it
			if pkg, ok := c.resolve(id.Name).(*PkgName); ok && pkg.pkg != nil {
				if v, ok := pkg.pkg.scope.Lookup(e.Sel.Name).(*Var); ok && v.global {
					return place{v, ""}, placeOwned
				}

				return place{}, notPlace
			}
		}

		p, kind := through(c.placeOf(e.X))
		if kind == placeOwned || kind == placeBorrowed {
			if p.path == "" {
				p.path = e.Sel.Name
			} else {
				p.path += "." + e.Sel.Name
			}
		}

		return p, kind
	case *ast.IndexExpr:
		p, kind := c.placeOf(e.X)
		if kind == notPlace {
			return p, notPlace
		}

		return p, placeIndexed
	case *ast.SliceExpr:
		p, kind := c.placeOf(e.X) // a view of the place sliced: ref xs[0:2] borrows xs
		if kind == notPlace {
			return p, notPlace
		}

		return p, placeIndexed
	}

	return place{}, notPlace
}

// through says how what a place holds is reached: through a borrow
// when the place is a variable holding one.
func through(p place, kind placeKind) (place, placeKind) {
	if kind == placeOwned && p.path == "" {
		if _, ok := p.v.typ.(*Ref); ok {
			return p, placeBorrowed
		}
	}

	return p, kind
}

// checkUse reports a use of a place that was moved, wholly or in part,
// on the current path. An assignment target is not a use of what it
// holds: a whole variable may be given a new value, and so may a moved
// field, but a field of a moved value may not.
func (c *Checker) checkUse(e ast.Expr) {
	if c.inner || len(c.moved) == 0 && len(c.borrows) == 0 && len(c.temps) == 0 {
		return
	}

	p, kind := c.placeOf(e)
	if kind == notPlace {
		return
	}

	if !c.noUse {
		c.access(e, p, readAccess, e.Pos(), e.Pos()) // a read collides with a mut borrow
	}

	if len(c.moved) == 0 || c.noUse && kind == placeOwned && p.path == "" {
		return
	}

	for i := len(c.moved) - 1; i >= 0; i-- { // the latest record of the place decides
		m := c.moved[i]
		if m.revived {
			if p.within(m.place) {
				return // given a new value since it moved
			}

			continue
		}

		switch {
		case c.noUse && kind == placeOwned && m.place == p:
			return // the moved field is given a new value
		case p.within(m.place) || kind == placeIndexed && m.v == p.v:
			c.errorf(e.Pos(), "use of moved value "+m.text()+"; moved at "+c.lineCol(m.pos))
			return
		case m.within(p) && !c.noUse && !revivedAfter(c.moved[i+1:], m.place):
			c.errorf(e.Pos(), "use of partially moved value "+p.text()+"; "+m.text()+" moved at "+c.lineCol(m.pos))
			return
		}
	}
}

// revivedAfter reports whether the records give the place q, or a place
// holding it, a new value.
func revivedAfter(records []move, q place) bool {
	for _, r := range records {
		if r.revived && q.within(r.place) {
			return true
		}
	}

	return false
}

// isPlaceChain reports whether e reaches a place through selectors,
// indexes, parentheses and dereferences only, so that a selector on it
// checks the whole chain as one use.
func isPlaceChain(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.Ident:
		return true
	case *ast.ParenExpr:
		return isPlaceChain(e.X)
	case *ast.SelectorExpr:
		return isPlaceChain(e.X)
	case *ast.IndexExpr:
		return isPlaceChain(e.X)
	case *ast.UnaryExpr:
		return e.Op == token.MUL && isPlaceChain(e.X)
	}

	return false
}

// operandOf evaluates an expression that is read for its value inside a
// place, such as an index, with the flags of the place around it off.
func (c *Checker) operandOf(e ast.Expr, hint Type) operand {
	inner, noUse := c.inner, c.noUse
	c.inner, c.noUse = false, false
	op := c.expr(e, hint)
	c.inner, c.noUse = inner, noUse
	return op
}

// maybeMove records the move of an operand consumed as a value of type
// t: a place of a non-Copy type, when t is not a borrow and not any,
// which lend instead. A mut borrow held in a variable is itself not
// Copy, whatever it borrows, so binding it to another name moves it,
// as does dropping it into the blank; passing it to a call
// lends it again, which the caller handles.
func (c *Checker) maybeMove(op operand, t Type) {
	if op.expr == nil || !held(op.expr, op.mode) && op.mode != borrowed && op.mode != mutBorrowed {
		return
	}

	if _, ok := t.(*Ref); ok && !isMut(op.typ) || t == nil || isAny(t) || IsCopy(op.typ) {
		return
	}

	m := op.mode
	if m == value {
		m = variable // a part of a value built here, which stays in it
	}

	c.move(op.expr, m)
}

// held reports whether an operation that takes the value of e, reached
// as m says, takes it from a place: a variable, or a part of a value
// built here, which the move refuses, since the whole is the
// statement's and is dropped with it.
func held(e ast.Expr, m mode) bool {
	return m == variable || m == value && partOf(e)
}

// partOf reports whether e names a part of a value, a field, an element
// or what a pointer points at, rather than a whole.
func partOf(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.ParenExpr:
		return partOf(e.X)
	case *ast.SelectorExpr, *ast.IndexExpr:
		return true
	case *ast.UnaryExpr:
		return e.Op == token.MUL
	}

	return false
}

func isMut(t Type) bool {
	r, ok := t.(*Ref)
	return ok && r.mut
}

func isBorrow(t Type) bool {
	_, ok := t.(*Ref)
	return ok
}

// move records that the place e denotes, reached as m says, is moved,
// or reports why it cannot be: nothing moves out through a borrow, an
// element is lent by its index, never moved, and a part of a value no
// name holds, the field of what a call returned or of the cell its
// pointer points at, has nowhere to go: the whole is dropped with the
// statement, and the part would be dropped twice.
func (c *Checker) move(e ast.Expr, m mode) {
	p, kind := c.placeOf(e)
	switch kind {
	case notPlace:
		if m == borrowed || m == mutBorrowed {
			c.errorf(e.Pos(), "cannot move "+exprText(e)+" out of a borrow")
		} else if m == variable && partOf(e) {
			c.errorf(e.Pos(), "cannot move "+exprText(e)+" out of a temporary: bind the temporary to a name first")
		}
	case placeOwned:
		c.capture(p.v, byMove)
		c.access(e, p, moveAccess, e.Pos(), e.Pos())
		c.moved = append(c.moved, move{p, e.Pos(), false})
		if c.info != nil {
			c.info.Moves[e] = true
		}
	case placeBorrowed:
		c.errorf(e.Pos(), "cannot move "+p.text()+" out of a borrow")
	case placeIndexed:
		c.errorf(e.Pos(), "cannot move "+exprText(e)+" out of the "+c.containerName(e))
	}
}

// moveBefore records a move that belongs before a set of branches, when
// it is only known inside one of them: the move goes under the fork,
// so that every branch and the join see it.
func (c *Checker) moveBefore(e ast.Expr, how mode, f *fork) {
	n := len(c.moved)
	c.move(e, how)
	if len(c.moved) == n {
		return // not a place, or reported
	}

	m := c.moved[n]
	copy(c.moved[f.mark+1:], c.moved[f.mark:n])
	c.moved[f.mark] = m
	f.mark++
	c.floors[len(c.floors)-1].mark++
}

// revive makes the place e denotes owned again: an assignment to it
// gives it back.
func (c *Checker) revive(e ast.Expr) {
	p, kind := c.placeOf(e)
	if kind != placeOwned || len(c.moved) == 0 && len(c.floors) == 0 {
		return
	}

	c.giveBack(p, e.Pos())
}

// giveBack records that the place p has a value again. The moves of p,
// or of a part of it, made since the innermost open branch set, region
// or closure body began are undone; a move made before it stays, since
// the enclosing construct holds its index and its other paths still
// see it, and a revival record says instead that p is owned from here
// on this path, for the join to weigh. At the top level nothing
// encloses, and the moves are simply gone.
func (c *Checker) giveBack(p place, pos token.Pos) {
	floor := c.floor()
	kept := c.moved[:floor]
	for _, m := range c.moved[floor:] {
		if !m.within(p) {
			kept = append(kept, m)
		}
	}

	c.moved = kept
	if floor > 0 || c.watched(p) {
		c.moved = append(c.moved, move{p, pos, true})
	}
}

// floor returns the index in moved where the innermost open branch set,
// region or closure body began its records; 0 when none is open.
func (c *Checker) floor() int {
	if len(c.floors) == 0 {
		return 0
	}

	return c.floors[len(c.floors)-1].mark
}

// watched reports whether an open match watches p, or a place within
// or holding it, as its scrutinee.
func (c *Checker) watched(p place) bool {
	for _, f := range c.floors {
		if f.watching && (p.within(f.watch) || f.watch.within(p)) {
			return true
		}
	}

	return false
}

// containerName names what an index reaches into, for a message.
func (c *Checker) containerName(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.IndexExpr:
			if _, isMap := c.placeType(x.X).Underlying().(*Map); isMap {
				return "map"
			}

			return "slice"
		case *ast.SelectorExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return "slice"
		}
	}
}

// placeType is the type of the value a place expression reaches, found
// through the variable's type and the fields' types.
func (c *Checker) placeType(e ast.Expr) Type {
	switch e := e.(type) {
	case *ast.Ident:
		if obj := c.resolve(e.Name); obj != nil && obj.Type() != nil {
			return obj.Type()
		}
	case *ast.ParenExpr:
		return c.placeType(e.X)
	case *ast.UnaryExpr:
		return stripRef(c.placeType(e.X))
	case *ast.SelectorExpr:
		if st, ok := stripRef(c.placeType(e.X)).Underlying().(*Struct); ok {
			if f := fieldNamed(st.fields, e.Sel.Name); f != nil {
				return f.typ
			}
		}
	case *ast.IndexExpr:
		switch t := stripRef(c.placeType(e.X)).Underlying().(type) {
		case *Slice:
			return t.elem
		case *Map:
			return t.value
		}
	}

	return Typ[Invalid]
}

// stripRef is the type behind the borrows and owned pointers in front
// of t.
func stripRef(t Type) Type {
	for {
		switch u := t.(type) {
		case *Ref:
			t = u.elem
		case *Pointer:
			t = u.elem
		default:
			return t
		}
	}
}

// exprText writes a place expression as the source would, for a
// message: xs[0], n.value.
func exprText(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.BasicLit:
		return e.Value
	case *ast.ParenExpr:
		return "(" + exprText(e.X) + ")"
	case *ast.SelectorExpr:
		return exprText(e.X) + "." + e.Sel.Name
	case *ast.IndexExpr:
		return exprText(e.X) + "[" + exprText(e.Index) + "]"
	case *ast.SliceExpr:
		return exprText(e.X) + "[" + optText(e.Low) + ":" + optText(e.High) + "]"
	case *ast.UnaryExpr:
		if e.Op == token.REF || e.Op == token.MUT {
			return e.Op.String() + " " + exprText(e.X) // the keyword, then the operand: ref fs
		}

		return e.Op.String() + exprText(e.X)
	case *ast.CallExpr:
		if len(e.Args) == 0 {
			return exprText(e.Fun) + "()"
		}

		return exprText(e.Fun) + "(...)"
	}

	return "..."
}

// optText writes an optional bound of a slice expression.
func optText(e ast.Expr) string {
	if e == nil {
		return ""
	}

	return exprText(e)
}

// forkAt starts a set of branches from the current state. The branches'
// records begin at its mark, which the floor stack holds for giveBack.
func (c *Checker) forkAt() fork {
	c.floors = append(c.floors, floor{mark: len(c.moved)})
	return fork{mark: len(c.moved), dead: c.dead, all: true}
}

// watch makes the innermost open branch set watch the place e denotes,
// when it is an owned place: a match's scrutinee, whose move may be put
// under the fork by a later arm.
func (c *Checker) watch(e ast.Expr) {
	if p, kind := c.placeOf(e); kind == placeOwned {
		top := &c.floors[len(c.floors)-1]
		top.watch, top.watching = p, true
	}
}

// branchEnd ends one branch: its records count after the join unless it
// left, and the state goes back to the fork for the next branch.
func (c *Checker) branchEnd(f *fork) {
	if !c.dead {
		f.paths = addPath(f.paths, c.moved[f.mark:])
		f.live++
		f.all = false
	}

	f.branches++
	c.moved = c.moved[:f.mark]
	c.dead = f.dead
}

// addPath keeps the records of one path reaching a join, when it has
// any: a path without adds no move and gives nothing back, and is only
// counted.
func addPath(paths [][]move, records []move) [][]move {
	if len(records) == 0 {
		return paths
	}

	return append(paths, slices.Clone(records))
}

// joinAt joins the branches. complete says whether the branches cover
// every path; otherwise a path with no records continues as well.
func (c *Checker) joinAt(f *fork, complete bool) {
	c.floors = c.floors[:len(c.floors)-1]
	c.join(f.paths, f.live, complete)
	c.dead = f.dead || complete && f.all && f.branches > 0
}

// join applies the records of the paths meeting here, live of them,
// those with records given: every move counts after the join, and a
// place every path gave back is given back here, decided anew against
// the records under the enclosing floor. complete says whether the
// paths are all the ways here; a path without records gives nothing
// back, so nothing is given back unless every live path has records.
func (c *Checker) join(paths [][]move, live int, complete bool) {
	for _, records := range paths {
		c.moved = appendMoves(c.moved, records)
	}

	if !complete || len(paths) < live {
		return
	}

	for _, p := range revivedByAll(paths) {
		c.giveBack(p, token.NoPos)
	}
}

// revivedByAll returns the places every path gives back, each path by
// its own records (netRevived); a whole given back on one path counts
// for a part given back on another.
func revivedByAll(paths [][]move) []place {
	if len(paths) == 0 {
		return nil
	}

	sets := make([][]place, len(paths))
	for i, records := range paths {
		sets[i] = netRevived(records)
	}

	var all []place
	for _, set := range sets {
		for _, p := range set {
			if !slices.Contains(all, p) && inEvery(sets, p) {
				all = append(all, p)
			}
		}
	}

	return all
}

// netRevived returns the places a path's records give back: the places
// of its revivals, less any that a later move touches, moving it, a
// part of it or a place holding it.
func netRevived(records []move) []place {
	var set []place
	for _, m := range records {
		if m.revived {
			set = append(set, m.place)
			continue
		}

		kept := set[:0]
		for _, r := range set {
			if !m.within(r) && !r.within(m.place) {
				kept = append(kept, r)
			}
		}

		set = kept
	}

	return set
}

// inEvery reports whether every set holds p or a place holding it.
func inEvery(sets [][]place, p place) bool {
	for _, set := range sets {
		if !slices.ContainsFunc(set, p.within) {
			return false
		}
	}

	return true
}

// enterRegion opens a loop or select, and leaveRegion closes it,
// joining the paths that reach the point after it: a select's end and
// its breaks; a loop's breaks, and when the loop has a condition, its
// ends and the path on which the body never ran, which gives nothing
// back. For a loop, the moves on the paths reaching the
// next iteration are checked: a value declared before the loop and
// moved on such a path is gone when the loop comes round. The point
// after a region is reachable; the caller's statement result says when
// it is not.
func (c *Checker) enterRegion(loop, infinite bool, pos token.Pos) {
	c.regions = append(c.regions, region{loop: loop, infinite: infinite, pos: pos, mark: len(c.moved)})
	c.floors = append(c.floors, floor{mark: len(c.moved)})
}

func (c *Checker) leaveRegion() {
	r := c.regions[len(c.regions)-1]
	c.regions = c.regions[:len(c.regions)-1]
	c.floors = c.floors[:len(c.floors)-1]
	if !c.dead {
		r.ends, r.ended = addPath(r.ends, c.moved[r.mark:]), r.ended+1
	}

	c.moved = c.moved[:r.mark]
	if r.loop {
		for _, records := range r.ends {
			for _, m := range records {
				if !m.revived && m.v.pos < r.pos {
					c.errorf(m.pos, m.text()+" moved in a previous iteration of the loop")
				}
			}
		}
	}

	paths, live := r.exits, r.exited
	if !r.infinite {
		paths = slices.Concat(paths, r.ends)
		live += r.ended
	}

	if r.loop && !r.infinite {
		live++ // the body never ran
	}

	c.join(paths, live, true)
	c.dead = false
}

// appendMoves appends the moves among records to moved, leaving out the
// revivals.
func appendMoves(moved, records []move) []move {
	for _, m := range records {
		if !m.revived {
			moved = append(moved, m)
		}
	}

	return moved
}

// branch records a break or continue: the records so far go to the
// loop it leaves or continues, the innermost one, and the path ends
// here. Outside any loop the statement is an error.
func (c *Checker) branch(tok token.Kind, pos token.Pos) {
	if c.dead {
		return
	}

	for i := len(c.regions) - 1; i >= 0; i-- {
		r := &c.regions[i]
		if !r.loop {
			continue // a match or select is not a loop: a break inside one leaves the loop around it
		}

		if tok == token.CONTINUE {
			r.ends, r.ended = addPath(r.ends, c.moved[r.mark:]), r.ended+1
		} else {
			r.exits, r.exited = addPath(r.exits, c.moved[r.mark:]), r.exited+1
		}

		return
	}

	c.errorf(pos, tok.String()+" is not inside a loop")
}
