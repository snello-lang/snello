// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/scanner"
	"github.com/teak-lang/teak/token"
)

// A Package is a checked Teak package: its name and the scope holding
// its top-level declarations.
type Package struct {
	name  string
	scope *Scope
}

// Info records what the checker learned about the syntax tree, as
// go/types.Info does. Defs maps each identifier that declares something
// to the object it declares; Uses maps each identifier that refers to
// something to the object it refers to. An identifier appears in at
// most one of the two.
type Info struct {
	Defs  map[*ast.Ident]Object
	Uses  map[*ast.Ident]Object
	Types map[ast.Expr]Type // the type of every expression that has one
}

// A Checker holds the state of one check: the file being checked, the
// package being built, the current scope, and the errors so far.
type Checker struct {
	file   *token.File
	pkg    *Package
	info   *Info
	errors scanner.ErrorList
	scope  *Scope     // the innermost scope open at the current point
	objs   []Object   // the object of each top-level declaration, by index; nil for var and const
	sig    *Signature // the function whose body is being checked; nil at the top level

	// The move pass (move.go): the places moved on the current path,
	// whether the path can reach this point, the loops, switches and
	// selects open here, and two flags for how a place is being read.
	moved      []move
	dead       bool
	regions    []region
	noUse      bool // evaluating an assignment target, which is not a use
	inner      bool // evaluating the base of a selector, which the selector checks as a whole
	bindsOwned bool // a pattern bound a non-Copy value by value
}

// Check resolves every name in f, a file of one package, and returns
// the package and the errors found, sorted by position. The file's
// positions are resolved through file. info, when not nil, receives
// the objects of every identifier.
func Check(file *token.File, f *ast.File, info *Info) (*Package, error) {
	c := &Checker{file: file, info: info}
	c.pkg = &Package{name: f.Name.Name, scope: NewScope(Universe)}
	if info != nil {
		if info.Defs == nil {
			info.Defs = make(map[*ast.Ident]Object)
		}

		if info.Uses == nil {
			info.Uses = make(map[*ast.Ident]Object)
		}

		if info.Types == nil {
			info.Types = make(map[ast.Expr]Type)
		}
	}

	c.checkFile(f)
	c.errors.Sort()
	return c.pkg, c.errors.Err()
}

// Name returns the package name.
func (p *Package) Name() string { return p.name }

// Scope returns the package scope.
func (p *Package) Scope() *Scope { return p.scope }

// checkFile runs the passes in order: collect the file's declarations
// into the package scope, give them their types, then walk the bodies.
func (c *Checker) checkFile(f *ast.File) {
	fileScope := NewScope(c.pkg.scope)
	c.scope = fileScope
	c.collectImports(f, fileScope)
	c.collectDecls(f)
	c.resolveDecls(f)
	c.resolveBodies(f)
}

// errorf reports an error at pos. Error paths may allocate freely.
func (c *Checker) errorf(pos token.Pos, msg string) {
	c.errors.Add(c.file.Position(pos), msg)
}

// lineCol formats a position of the file being checked as line:col,
// for a message that points at a second place in the same file.
func (c *Checker) lineCol(pos token.Pos) string {
	p := c.file.Position(pos)
	return strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Column)
}

// declare puts obj into scope under ident, recording the definition,
// and reports a redeclaration with the position of the first one.
func (c *Checker) declare(scope *Scope, ident *ast.Ident, obj Object) {
	if ident.Name == "_" {
		c.recordDef(ident, obj)
		return
	}

	if old := scope.Insert(obj); old != nil {
		msg := ident.Name + " redeclared in this block"
		if old.Pos().IsValid() {
			msg += "; first declared at " + c.lineCol(old.Pos())
		}

		c.errorf(ident.Pos(), msg)
	}

	c.recordDef(ident, obj)
}

// lookup resolves ident through the open scopes and records the use.
// An undefined name is reported once, here.
func (c *Checker) lookup(ident *ast.Ident) Object {
	_, obj := c.scope.LookupParent(ident.Name)
	if obj == nil {
		if ident.Name == "nil" {
			c.errorf(ident.Pos(), "nil is not defined; an absent value is None")
		} else {
			c.errorf(ident.Pos(), "undefined: "+ident.Name)
		}

		return nil
	}

	c.recordUse(ident, obj)
	return obj
}

// openScope makes a new scope the current one; closeScope restores the
// enclosing one. Every block, clause, arm and function body is one.
func (c *Checker) openScope() *Scope {
	c.scope = NewScope(c.scope)
	return c.scope
}

func (c *Checker) closeScope() {
	c.scope = c.scope.parent
}

func (c *Checker) recordDef(ident *ast.Ident, obj Object) {
	if c.info != nil {
		c.info.Defs[ident] = obj
	}
}

func (c *Checker) recordUse(ident *ast.Ident, obj Object) {
	if c.info != nil {
		c.info.Uses[ident] = obj
	}
}

func (c *Checker) recordType(e ast.Expr, t Type) {
	if c.info != nil {
		c.info.Types[e] = t
	}
}
