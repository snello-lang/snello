// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"

	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/scanner"
	"github.com/teak-lang/teak/compiler/token"
)

// A Package is a checked Teak package: its name, its import path,
// empty for the program being compiled, and the scope holding its
// top-level declarations.
type Package struct {
	name  string
	path  string
	scope *Scope
}

// An Importer loads the package an import path names, as
// go/types.Importer does: it parses and checks the package's source,
// once per path, and returns it, or an error saying why it could not.
type Importer interface {
	Import(path string) (*Package, error)
}

// A Config says how a program is checked: the Importer that loads the
// packages it imports. Without one, only the core library table
// answers an import. Modeled on go/types.Config.
type Config struct {
	Importer Importer
}

// Info records what the checker learned about the syntax tree, as
// go/types.Info does. Defs maps each identifier that declares something
// to the object it declares; Uses maps each identifier that refers to
// something to the object it refers to. An identifier appears in at
// most one of the two.
type Info struct {
	Defs      map[*ast.Ident]Object
	Uses      map[*ast.Ident]Object
	Types     map[ast.Expr]Type          // the type of every expression that has one
	Moves     map[ast.Expr]bool          // the place expressions whose value moves away: the emitter empties the place
	Instances map[*ast.Ident]Instance    // the name of each generic function at a call, with the arguments inferred for it
	Captures  map[*ast.FuncLit][]Capture // the variables of the enclosing functions each closure refers to, in the order first seen
}

// An Instance is a generic function as one call uses it: its type
// arguments, and its signature with them in place.
// A Capture is a variable of an enclosing function that a closure
// refers to. Copied says the closure holds a copy of its value, made
// when the closure was: a Copy value the closure only reads. Any other
// capture is reached through a borrow of the variable, or owned after a
// move.
type Capture struct {
	Var    *Var
	Copied bool
}

type Instance struct {
	TypeArgs []Type
	Type     *Signature
}

// A Checker holds the state of one check: the file being checked, the
// package being built, the current scope, and the errors so far.
type Checker struct {
	conf   *Config
	file   *token.File
	pkg    *Package
	info   *Info
	errors scanner.ErrorList
	scope  *Scope     // the innermost scope open at the current point
	objs   []Object   // the object of each top-level declaration, by index; nil for var and const
	sig    *Signature // the function whose body is being checked, a closure's inside one; nil at the top level
	recv   *Var       // the receiver of the method whose body is being checked, through any closures; nil elsewhere

	// typeScopes holds, for a generic function and for a method of a
	// generic type, the scope that declares its type parameters, under
	// which the signature was resolved and the body is checked.
	typeScopes map[*ast.FuncDecl]*Scope
	badMethods map[*ast.FuncDecl]bool // methods left out after an error in their receiver
	callee     ast.Expr               // the function of the call being checked, which alone may name a generic function
	wantType   ast.Expr               // the expression a type may stand at besides the callee: the first argument of make
	callEdges  []callEdge             // the type parameters calls pass to generic callees, for the cycle check (cycle.go)

	// The traits (trait.go): the type Self stands for while a trait's
	// methods are resolved or a default's body is checked, the bound
	// checks that waited for the declarations, and the default methods
	// whose bodies are checked with the function bodies.
	self          *TypeParam
	pendingBounds []pendingBound
	pendingKeys   []pendingKey // the map key questions asked before the declarations were resolved (stdtraits.go)
	instances     []*Trait     // the operator traits written with an argument, Mul<f64>, one per argument type (stdtraits.go)
	defaults      []traitDefault

	// The move pass (move.go): the places moved on the current path,
	// whether the path can reach this point, the loops, switches and
	// selects open here, and two flags for how a place is being read.
	moved      []move
	floors     []floor // the open branch sets, regions and closure bodies, innermost last (move.go)
	dead       bool
	regions    []region
	noUse      bool                    // evaluating an assignment target, which is not a use
	inner      bool                    // evaluating the base of a selector, which the selector checks as a whole
	bindsOwned bool                    // a pattern bound a non-Copy value by value
	names      *Scope                  // while set, the scope the names of a pattern's value resolve in (resolve)
	readOnly   map[*ast.SliceExpr]bool // the slices of a value reached through a ref borrow, which mut may not lend
	copies     bool                    // the scrutinee is a place reached through a borrow, whose Copy parts a pattern copies

	// The borrow pass (borrow.go): the borrows held by variables of the
	// function being checked, the ones lent for the call or statement
	// being checked, and the accesses that collided with one, decided
	// at the end of the function.
	borrows   []borrow
	temps     []borrow
	conflicts []conflict
	depth     int // of expression nesting; a lent borrow ends with the full expression

	// The escape pass (escape.go): whether the declarations are resolved
	// and the questions asked before they were, the closures being
	// checked and what each closure literal captured, and the call a
	// spawn statement is starting.
	declared bool
	pending  []pendingStore
	closures []closure
	captured map[*ast.FuncLit][]capture
	spawning *ast.CallExpr
	variadic []operand // the variadic arguments of the call just checked, which a format is checked against

	// temporary maps an expression lent to a call or a ref to the
	// temporary its value is or borrows: an owned value no variable
	// holds, which the statement drops at its end, so that a borrow of
	// it cannot be kept past the statement or returned.
	temporary map[ast.Expr]ast.Expr
}

// Check resolves every name in f, a file of one package, and returns
// the package and the errors found, sorted by position. The file's
// positions are resolved through file. info, when not nil, receives
// the objects of every identifier. The imports are answered by the
// core library table alone; a program that imports a package written
// in Teak is checked through a Config.
func Check(file *token.File, f *ast.File, info *Info) (*Package, error) {
	return (&Config{}).Check("", file, f, info)
}

// Check resolves every name in f, a file of the package at the import
// path, as the function Check does, with the packages it imports
// loaded through the Config's Importer. The program being compiled has
// the empty path; a library package is checked under its own, into the
// same Info, so the backend compiles them together.
func (conf *Config) Check(path string, file *token.File, f *ast.File, info *Info) (*Package, error) {
	c := &Checker{conf: conf, file: file, info: info, typeScopes: make(map[*ast.FuncDecl]*Scope), badMethods: make(map[*ast.FuncDecl]bool)}
	c.pkg = &Package{name: f.Name.Name, path: path, scope: NewScope(Universe)}
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

		if info.Moves == nil {
			info.Moves = make(map[ast.Expr]bool)
		}

		if info.Instances == nil {
			info.Instances = make(map[*ast.Ident]Instance)
		}

		if info.Captures == nil {
			info.Captures = make(map[*ast.FuncLit][]Capture)
		}
	}

	c.checkFile(f)
	c.errors.Sort()
	return c.pkg, c.errors.Err()
}

// Name returns the package name.
func (p *Package) Name() string { return p.name }

// Path returns the import path of the package, or the empty string for
// the program being compiled.
func (p *Package) Path() string { return p.path }

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
	c.checkCallCycles()
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

	if scope == c.pkg.scope {
		obj.setPkg(c.pkg) // a top-level name is reached from other packages, and the backend names it after its package
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
