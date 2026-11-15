// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ast

import "reflect"

// The nodes Inspect visits, an interface type that is never written.
var nodeType = reflect.TypeFor[Node]()

// Clone returns a deep copy of n: every node under it is a node of its
// own, at the same positions, so that the copy can be checked apart
// from the original and the checker's tables, keyed by node, hold each
// apart. An arm of a match on a type that lists several types is
// checked once for each, from a copy of its body.
func Clone(n Node) Node {
	if n == nil {
		return nil
	}

	return cloneValue(reflect.ValueOf(n)).Interface().(Node)
}

// cloneValue copies v: a pointer to a struct, the struct and what it
// holds; a slice, its elements; an interface, what it holds. Anything
// else, a position, a string, a token, a bool, is a value already.
func cloneValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}

		c := reflect.New(v.Elem().Type())
		c.Elem().Set(cloneValue(v.Elem()))
		return c
	case reflect.Struct:
		c := reflect.New(v.Type()).Elem()
		for i := range v.NumField() {
			c.Field(i).Set(cloneValue(v.Field(i)))
		}

		return c
	case reflect.Slice:
		if v.IsNil() {
			return v
		}

		c := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			c.Index(i).Set(cloneValue(v.Index(i)))
		}

		return c
	case reflect.Interface:
		if v.IsNil() {
			return v
		}

		c := reflect.New(v.Type()).Elem()
		c.Set(cloneValue(v.Elem()))
		return c
	}

	return v
}

// Rewrite replaces each expression under n, n itself aside, with what f
// returns for it, after rewriting the expressions under it: f returns
// its argument for an expression it keeps. The checker rewrites a
// function's x.[f] into the selector the backends write for each field.
func Rewrite(n Node, f func(Expr) Expr) {
	if n != nil {
		rewriteValue(reflect.ValueOf(n), f)
	}
}

// rewriteValue rewrites the expressions v holds: a field or element
// whose static type is Expr is replaced by f's result, and every node
// is walked into.
func rewriteValue(v reflect.Value, f func(Expr) Expr) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			rewriteValue(v.Elem(), f)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			rewriteSlot(v.Field(i), f)
		}
	case reflect.Slice:
		for i := range v.Len() {
			rewriteSlot(v.Index(i), f)
		}
	}
}

// rewriteSlot rewrites a field or element: its expressions, then the
// slot itself when it holds an Expr, whether the slot is typed Expr or
// is wider, as a match arm's body is a Node that is a block or a value.
// The result is set when the slot can hold it.
func rewriteSlot(slot reflect.Value, f func(Expr) Expr) {
	rewriteValue(slot, f)
	if slot.Kind() != reflect.Interface || slot.IsNil() || !slot.CanSet() {
		return
	}

	x, ok := slot.Interface().(Expr)
	if !ok {
		return
	}

	if y := f(x); y != nil && reflect.TypeOf(y).AssignableTo(slot.Type()) {
		slot.Set(reflect.ValueOf(y))
	}
}

// Inspect calls f for n and, while f returns true for a node, for every
// node under it, in the order they are written; f returns false to leave
// a node's insides unvisited.
func Inspect(n Node, f func(Node) bool) {
	if n != nil {
		inspectValue(reflect.ValueOf(n), f)
	}
}

// inspectValue visits what v holds: a node, met as the pointer it is,
// is given to f, and walked into when f says so; an interface, a slice
// or a struct of other values is walked through.
func inspectValue(v reflect.Value, f func(Node) bool) {
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			inspectValue(v.Elem(), f)
		}
	case reflect.Pointer:
		if v.IsNil() || v.Type().Implements(nodeType) && !f(v.Interface().(Node)) {
			return
		}

		inspectValue(v.Elem(), f)
	case reflect.Slice:
		for i := range v.Len() {
			inspectValue(v.Index(i), f)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			inspectValue(v.Field(i), f)
		}
	}
}
