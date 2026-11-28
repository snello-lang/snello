// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/token"
)

// An implementation keeps its trait's word. A generic body that calls a
// trait's method, Next() T?, holds what it returns as built from what
// it gave, the borrows stored in the receiver and its arguments, and a
// generic call's result is followed from outside on that word. So an
// implementation whose result holds a borrow where the trait declares a
// value of its type parameter returns only borrows stored inside its
// receiver and the parameters the trait declares with Self or a type
// parameter, reached through ref borrows: never a borrow of what the
// receiver owns, nor a reborrow through a mut, which would give two
// live items or one the next call frees.

// A word is what an implementation keeps of its trait's declaration
// when the trait declares a result with a value of a type parameter in
// it, not behind a borrow the declaration writes, Next() T? or Get()
// (T, ref int), and the implementation's result holds a borrow. A value
// behind a declared borrow, Peek() ref T?, is reached through that
// borrow, which the rule for returned borrows ties to the receiver.
type word struct {
	sig      *Signature
	what     string // the trait's method, Iterator<ref int>.Next, for a message
	param    []bool // whether the trait declares each parameter with Self or a trait parameter
	reported int    // the return, by its number in the graph, last reported for breaking the word
}

// A returnedPart is the part of a variable a returned borrow is of:
// the path in it, whether the borrow lends the place there or reads
// the borrow stored there, whether it lends an element of it, and
// whether the path is behind the borrow the variable is, *r or r.f
// with r a borrow, rather than in the variable itself.
type returnedPart struct {
	path    string
	lent    bool
	indexed bool
	behind  bool
	read    bool // a borrow read out of a place, or copied from what a borrow variable holds, is on the way: what follows is what holds it, not a place lent itself
}

// once wraps report so that the return seq is reported once.
func (w *word) once(seq int, report func(token.Pos, string)) func(token.Pos, string) {
	return func(pos token.Pos, msg string) {
		if w.reported != seq {
			w.reported = seq
			report(pos, msg)
		}
	}
}

// through returns the part of what the loan b borrows that the
// returned part of b's holder is: x := ref v.io and a return of ref
// x.r[0] lend v.io.r[0], x := ref v.d and one of ref (*x)[0] lend an
// element behind v.d, lent or read as the return does, and x := ref v
// and one of *x read v. A loan held in a part of its holder is not
// followed into, and the part is the loan's own place, lent or read as
// the loan was made, a loan copied from what a borrow variable holds
// being read; so is the whole holder's, returned itself.
func (r returnedPart) through(b *borrow) returnedPart {
	read := b.read || b.lent // a borrow read out of the place, or copied from what a borrow variable holds: what it points at is behind it
	hpath := b.hpath
	if r.behind && (hpath == "*" || strings.HasPrefix(hpath, "*.")) {
		hpath = strings.TrimPrefix(strings.TrimPrefix(hpath, "*"), ".") // held behind the holder's borrow, where the returned part is
	}

	if r.path == "" && r.indexed && b.hpath == "" {
		return returnedPart{path: b.place.path, lent: r.lent, indexed: true, read: r.read || read}
	}

	if hpath != "" && r.path != hpath && pathWithin(r.path, hpath) { // a part behind the borrow held in a field, w.inner.text with inner the borrow: the rest of the path is in what it points at
		rest := strings.TrimPrefix(r.path[len(hpath):], ".")
		return returnedPart{path: joinPath(b.place.path, rest), lent: r.lent && !read, indexed: r.indexed, read: r.read || read}
	}

	if r.path == "" && !r.behind || b.hpath != "" {
		return returnedPart{path: b.place.path, lent: !read, behind: read, read: r.read || read}
	}

	return returnedPart{path: joinPath(b.place.path, r.path), lent: r.lent && !read, indexed: r.indexed, read: r.read || read}
}

// noteWord records that have, the implementation of the trait owner's
// method want, keeps its word, when the trait's declaration of the
// result holds a value of a type parameter outside any borrow it writes
// and have's result holds a borrow.
func (c *Checker) noteWord(have *Func, owner *Trait, want *Func) {
	decl, ok := want.Origin().typ.(*Signature)
	hsig, ok2 := have.typ.(*Signature)
	if !ok || !ok2 || !paramByValue(decl.results) || !holdsBorrow(hsig.results) || have.origin != nil {
		return // the implementation's own body is checked only when it is a declaration
	}

	w := &word{sig: hsig, what: owner.String() + "." + want.name, param: make([]bool, len(decl.params))}
	for i, p := range decl.params {
		w.param[i] = holdsTypeParam(p.typ)
	}

	if c.words == nil {
		c.words = make(map[*Func]*word)
	}

	c.words[have] = w
	have.word = true
}

// wordKept reports whether f, or the declaration f is an instance of,
// keeps its trait's word: a call of it gives a result that holds what
// the receiver and the arguments hold, read out of them, and lends the
// receiver nothing of its own, so the next item may be pulled while one
// is held.
func (f *Func) wordKept() bool {
	if f.origin != nil {
		return f.origin.word
	}

	return f.word
}

// keepsWord reports whether the body being checked is that of an
// implementation keeping its trait's word, and not a closure's in it.
func (c *Checker) keepsWord() bool {
	return c.word != nil && c.sig == c.word.sig
}

// flowHeld marks, in a body that keeps a trait's word, what a parameter
// taken by value holds as the parameter's to give: the graph's
// variables for the borrows stored in it, which a result may hold. What
// a borrowed parameter points at is reached only through a loan of one
// of its places, which wordSource decides by the place's path.
func (c *Checker) flowHeld(p *Var) {
	f := c.flow
	if isBorrow(p.typ) {
		return
	}

	if f.held == nil {
		f.held = make(map[*Var]bool)
	}

	for o, q := range f.outside {
		if q == p {
			f.held[o] = true
		}
	}
}

// wordSource checks a borrow returned out of the parameter v, a place
// at path in it that the return lends, or reads the borrows of, against
// the word the body keeps, and reports whether it decided: the borrow
// must be stored inside v and reached through a ref borrow. indexed
// says the place lent is an element of the one at path, ref w.xs[i],
// so that a borrow at path is crossed. One out of a parameter taken by
// value is left to the rule for returned borrows, which refuses a
// borrow of what the parameter owns.
func (c *Checker) wordSource(v *Var, path string, lent, indexed bool, pos token.Pos, report func(token.Pos, string)) bool {
	if c.wordParam(v, pos, report) {
		return true
	}

	if !isBorrow(v.typ) {
		return false
	}

	if path == "" && !lent {
		report(pos, "cannot return "+v.name+": it is the borrow "+c.word.what+" is given, and it returns only what its receiver and parameters borrow")
		return true
	}

	t := stripRef(v.typ)
	names := strings.Split(path, ".")
	for i, name := range names {
		s, ok := t.Underlying().(*Struct)
		if !ok {
			break
		}

		fld := fieldNamed(s.fields, name)
		if fld == nil {
			break
		}

		t = fld.typ
		at := v.name + "." + strings.Join(names[:i+1], ".")
		last := i == len(names)-1
		if r, ok := t.(*Ref); ok && (!last || !lent || indexed) { // ref w.r lends the place r is stored in, which v owns
			if r.mut {
				report(pos, "cannot return a borrow through "+at+": it is a mut borrow, and "+c.word.what+" returns only what is reached through ref borrows, since the next call could change or free it")
			}

			return true
		}

		if last && !lent {
			if HoldsMutBorrow(t) {
				report(pos, "cannot return a borrow through "+at+": it holds a mut borrow, and "+c.word.what+" returns only what is reached through ref borrows")
			}

			return true
		}
	}

	place := joinPath(v.name, path)
	if r, ok := t.(*Ref); ok && r.mut && path != "" {
		report(pos, "cannot return a borrow through "+place+": it is a mut borrow, and "+c.word.what+" returns only what is reached through ref borrows, since the next call could change or free it")
		return true
	}

	if path == "" {
		place = "what " + v.name + " points at"
	}

	report(pos, "cannot return a borrow of "+place+": "+v.name+" owns it, and "+c.word.what+" returns only what "+v.name+" borrows")
	return true
}

// wordLoan checks a loan the returned value holds, in a body that keeps
// its trait's word, and reports whether it decided: a loan of a place
// of a parameter is decided by its path (wordSource); one of what the
// parameter, or a borrow stored behind it, points at by which it is
// (wordHeld); and one copied because a place behind the parameter was
// lent is decided by the loan of that place, which the lend made beside
// it and which goes wherever the copy goes.
func (c *Checker) wordLoan(b *borrow, part returnedPart, pos token.Pos, report func(token.Pos, string)) bool {
	if c.isParam(b.v) {
		at := part.through(b)
		return c.wordSource(b.v, at.path, at.lent, at.indexed, pos, report)
	}

	p := c.flow.outside[b.v]
	if p == nil {
		return false
	}

	if b.lent {
		return true
	}

	c.wordHeld(b.v, p, pos, report)
	return true
}

// wordHeld checks a borrow returned out of o, the variable the graph
// made for what the parameter p, or a borrow stored behind it, points
// at.
func (c *Checker) wordHeld(o, p *Var, pos token.Pos, report func(token.Pos, string)) {
	if c.wordParam(p, pos, report) || c.flow.held[o] {
		return
	}

	report(pos, "cannot return a borrow of what "+p.name+" points at: "+c.word.what+" returns only what is stored behind "+p.name+", reached through ref borrows")
}

// wordParam reports a returned borrow of the parameter v that the
// trait's declaration does not give, one of a concrete type, and
// whether it reported.
func (c *Checker) wordParam(v *Var, pos token.Pos, report func(token.Pos, string)) bool {
	if v == c.sig.recv {
		return false
	}

	for i, p := range c.sig.params {
		if p == v && i < len(c.word.param) && !c.word.param[i] {
			report(pos, "cannot return a borrow of "+v.name+": "+c.word.what+" declares "+v.name+" with neither Self nor a type parameter of the trait, and returns only what such parameters borrow")
			return true
		}
	}

	return false
}

// paramByValue reports whether a value of type t holds a value of a
// type parameter that no borrow written in t leads to: T, T?, (T, ref
// int), []T, W<T>; not ref T?. A function type counts, since what it
// returns is such a value.
func paramByValue(t Type) bool {
	switch t := t.(type) {
	case *TypeParam:
		return true
	case *Ref:
		return false
	case *Pointer:
		return paramByValue(t.elem)
	case *Option:
		return paramByValue(t.elem)
	case *Slice:
		return paramByValue(t.elem)
	case *Array:
		return paramByValue(t.elem)
	case *Chan:
		return paramByValue(t.elem)
	case *Map:
		return paramByValue(t.key) || paramByValue(t.value)
	case *Result:
		return paramByValue(t.val) || paramByValue(t.err)
	case *Tuple:
		return slices.ContainsFunc(t.elems, paramByValue)
	case *Signature:
		return holdsTypeParam(t)
	case *Named:
		return slices.ContainsFunc(typeArgs(t), holdsTypeParam)
	}

	return false
}
