// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

// A Scope maps names to objects and has a parent scope, as in go/types:
// the universe, then the package, then each file, function and block.
type Scope struct {
	parent *Scope
	elems  map[string]Object
}

// NewScope returns an empty scope inside parent.
func NewScope(parent *Scope) *Scope {
	return &Scope{parent: parent}
}

// Parent returns the enclosing scope, or nil for the universe.
func (s *Scope) Parent() *Scope { return s.parent }

// Lookup returns the object declared in this scope under name, or nil.
func (s *Scope) Lookup(name string) Object {
	return s.elems[name]
}

// LookupParent finds name in this scope or the nearest enclosing one and
// returns that scope and the object, or nil and nil.
func (s *Scope) LookupParent(name string) (*Scope, Object) {
	for ; s != nil; s = s.parent {
		if obj := s.elems[name]; obj != nil {
			return s, obj
		}
	}

	return nil, nil
}

// Insert adds obj to the scope under its name and returns nil, or
// returns the object already declared under that name and changes
// nothing, so the caller can report both positions.
func (s *Scope) Insert(obj Object) Object {
	name := obj.Name()
	if old := s.elems[name]; old != nil {
		return old
	}

	if s.elems == nil {
		s.elems = make(map[string]Object)
	}

	s.elems[name] = obj
	return nil
}

// Len returns the number of objects declared in this scope.
func (s *Scope) Len() int { return len(s.elems) }
