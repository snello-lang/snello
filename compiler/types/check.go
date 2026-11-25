// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"path/filepath"
	"slices"
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/token"
)

// A Package is a checked Snello package: its name, its import path,
// empty for the program being compiled, whether it is the standard
// library's, and the scope holding its top-level declarations.
type Package struct {
	name     string
	path     string
	std      bool
	scope    *Scope
	defaults []ast.Expr // the defaults its structs' fields declare, which each field names by index (Var.def)
}

// An Importer loads the package an import path names: it parses and
// checks the package's source, once per path, and returns it, or an
// error saying why it could not.
type Importer interface {
	Import(path string) (*Package, error)
}

// An ImporterFrom is an Importer that resolves a path from the
// directory of the file importing it, so that a package of the
// program's own is found through the module
// the file is in. The checker calls ImportFrom when the Config's
// Importer has it.
type ImporterFrom interface {
	Importer
	ImportFrom(path, dir string) (*Package, error)
}

// A Config says how a program is checked: the Importer that loads the
// packages it imports, and whether the package is the standard
// library's, which may declare what a program may not: a function
// without a body, a variadic ...ref any, a local field and a slice of
// borrows. Without an Importer, only the core library table answers an
// import. At, when valid, is the position of a name a completion asks
// about: the checker records in Info.Visible the names visible there,
// as it resolves the name; a check with no position records nothing.
type Config struct {
	Importer Importer
	Standard bool
	At       token.Pos
}

// Info records what the checker learned about the syntax tree. Defs
// maps each identifier that declares something to the object it
// declares; Uses maps each identifier that refers to something to the
// object it refers to. An identifier appears in at most one of the two.
type Info struct {
	Defs      map[*ast.Ident]Object
	Uses      map[*ast.Ident]Object
	Types     map[ast.Expr]Type             // the type of every expression that has one
	Moves     map[ast.Expr]bool             // the place expressions whose value moves away: the emitter empties the place
	Instances map[*ast.Ident]Instance       // the name of each generic function at a call, with the arguments inferred for it
	Captures  map[*ast.FuncLit][]Capture    // the variables of the enclosing functions each closure refers to, in the order first seen
	Wraps     map[*ast.TryExpr]*Variant     // the variant each x? wraps the error in, when the function's error enum holds the error's type in one
	Values    map[ast.Expr]Constant         // the value of each constant's value expression, and of each field a struct constant's literal gives
	Arms      map[*ast.MatchArm]TypeArm     // what each arm of a match on a type parameter matches
	Parts     map[*Part][]*ast.SelectorExpr // the selectors each v.[f] of a part f became, whose field a backend names in each copy of the loop
	Attrs     map[Object][]Attribute        // the attributes written on each field, variant and type name, by the declaration
	Visible   []Object                      // the names visible at Config.At, the innermost scope's first, each scope's by name, a name an inner one hides left out
	Forms     map[*ast.BlockStmt]*Form      // each function's and closure's body as the program evaluates it, by its body, kept only when the caller makes the map (form.go)
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

// A TypeArm is what an arm of a match on a type parameter matches: a
// type, which may be a shape whose type parameters the arm binds, []E;
// a kind, struct, enum or tuple; or, with neither, any type.
type TypeArm struct {
	Type  Type
	Binds []*TypeParam
	Kind  string
}

type Instance struct {
	TypeArgs []Type
	Type     *Signature
}

// A Checker holds the state of one check: the files of the package,
// the one being walked, the package being built, the current scope,
// and the errors so far.
type Checker struct {
	conf   *Config
	files  []*token.File
	file   *token.File
	pkg    *Package
	info   *Info
	errors scanner.ErrorList
	scope  *Scope     // the innermost scope open at the current point
	objs   []Object   // the object of each top-level declaration, by index; nil for var and const
	sig    *Signature // the function whose body is being checked, a closure's inside one; nil at the top level

	// refined holds what an arm of a match on a type knows of a type
	// parameter: the type or shape the arm matched, int or []E, which
	// every type written with the parameter is read as inside the arm.
	refined map[*TypeParam]Type
	// kinds holds the kind an arm matched a type parameter as, struct,
	// enum or tuple, which lets the arm reach its parts.
	kinds map[*TypeParam]string
	// refuses says a refuse was checked, which the walk of reach.go
	// reports where it is reached.
	refuses bool
	// sawParts says the body being checked reaches a field with v.[f],
	// which selectParts makes a selector once the body is checked.
	sawParts bool
	// held is what a match whose arm is written once per variant holds,
	// for each variant part: the value it matched, whose fields v.[f]
	// reaches.
	held map[*Part]*Var
	recv *Var // the receiver of the method whose body is being checked, through any closures; nil elsewhere

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
	// whether the path can reach this point, the loops and selects open
	// here, and two flags for how a place is being read.
	moved      []move
	floors     []floor // the open branch sets, regions and closure bodies, innermost last (move.go)
	dead       bool
	regions    []region
	loops      int                     // the loops open here, inside which a borrow made later in the body reaches an access made earlier
	noUse      bool                    // evaluating an assignment target, which is not a use
	lentMode   mode                    // what the last ref or mut checked lent: a place, or a value made for it
	stringByte ast.Expr                // the last byte of a string indexed, which mut may not lend
	placeFor   bool                    // checking the place take or replace is given, which they refuse themselves when it is not one
	inner      bool                    // evaluating the base of a selector, which the selector checks as a whole
	bindsOwned bool                    // a pattern bound a non-Copy value by value
	dropOwner  Type                    // the type with a Drop whose variant a pattern is inside, by value; nil for none
	names      *Scope                  // while set, the scope the names of a pattern's value resolve in (resolve)
	readOnly   map[*ast.SliceExpr]bool // the slices of a value reached through a ref borrow, which mut may not lend
	copies     bool                    // the scrutinee is a place reached through a borrow, whose Copy parts a pattern copies

	// The borrow pass (borrow.go, flow.go): the graph of the body being
	// checked, which holds the borrows its variables hold and is solved
	// at the body's end, with the graphs kept for reuse; and the borrows
	// lent for the call or statement being checked.
	flow         *flow
	pinned       map[*ast.Ident]*Var // the variables a block's or an arm's value names, resolved while their scope is open (pin)
	deferring    *ast.CallExpr       // the call a defer statement makes, whose signature deferSig records
	deferSig     *Signature
	temps        []borrow
	depth        int          // of expression nesting; a lent borrow ends with the full expression
	movedValues  []ast.Expr   // the values of a statement of several whose moves are recorded where each was checked (values)
	lowering     *lowering    // the scratch of the lowering of a body, reused across bodies (lowering.go)
	closureMoves []formError  // what the move pass over the form refuses in the closures of the body being checked, while both passes run (checkBody)
	closureLoans []formError  // the same for the borrow pass over the form
	borrowPass   *formBorrows // the scratch of the borrow pass over the form, reused across bodies
	formPass     bool         // the passes over the form are running: what the recorders refuse goes to formErrs, and a name resolves through the uses the typing recorded
	formErrs     []formError
	typingPlace  bool         // typing the place an assignment writes before its value: an access to the place is held, and made at the store (target)
	heldAccess   []heldAccess // the accesses held, in their order (replay)

	// The keeping pass (keep.go): the function a call's arguments are
	// checked for, and what is decided at the package's end.
	calling    Object
	enclosing  []*Signature // the signatures of the functions around the closure being checked, outermost first: a parameter of any of them may be kept from inside the closure
	keepEdges  []keepEdge
	keepCalls  []keepCall
	keepValues []keepValue
	keepImpls  []keepImpl

	// The implementations that keep a trait's word, and the one whose
	// body is being checked (word.go).
	words map[*Func]*word
	word  *word

	// The generic calls a generic body makes, which pass on what the
	// callee hides, and the type arguments holding a borrow to check
	// against it once every body is checked (hide.go).
	hideEdges    []hideEdge
	pendingHides []pendingHide

	// The escape pass (escape.go): whether the declarations are resolved
	// and the questions asked before they were, the closures being
	// checked and what each closure literal captured, and the call a
	// spawn statement is starting.
	declared     bool
	pending      []pendingStore
	pendingOwned []pendingStore // the instances ownedParts was asked about then, elem the type
	closures     []closure
	captured     map[*ast.FuncLit][]capture
	spawning     *ast.CallExpr
	variadic     []operand     // the variadic arguments of the call just checked, which a format is checked against
	negated      *ast.BasicLit // the literal a minus sign is written before, while it is checked: its range is the negative numbers

	// The constants (constant.go): the top-level constants of the
	// package by what checks each, the constants being checked, outermost
	// first, the types of the constant expression being folded, and the
	// package variables whose type their value gives, until it does.
	early         map[*Named]*pendingType // the types declared over another, which a constant may need resolved before their turn (resolve.go)
	fieldDefaults []pendingDefault        // the struct fields' defaults, checked once the constants are (checkDefaults)
	fileIndex     int                     // the index of the file being walked, which entering a file sets
	consts        map[*Const]*pendingConst
	constOrder    []*Const
	constPath     []*Const
	folding       map[ast.Expr]Type
	foldTypes     map[ast.Expr]Type // the map folding reuses
	uninferred    map[*Var]bool

	// Each file's scope of its imports and table of declared objects,
	// by the index of the file, which entering a file makes current.
	fileState []fileState

	// temporary maps an expression lent to a call or a ref to the
	// temporary its value is or borrows: an owned value no variable
	// holds, which the statement drops at its end, so that a borrow of
	// it cannot be kept past the statement or returned.
	temporary map[ast.Expr]ast.Expr

	// passed maps a call of a generic function whose type arguments
	// hold a borrow to the arguments whose borrows its result holds
	// (passing.go).
	passed map[*ast.CallExpr]passedTo
}

// Check resolves every name in f, a file of one package, and returns
// the package and the errors found, sorted by position. The file's
// positions are resolved through file. info, when not nil, receives
// the objects of every identifier. The imports are answered by the
// core library table alone; a program that imports a package written
// in Snello is checked through a Config.
func Check(file *token.File, f *ast.File, info *Info) (*Package, error) {
	return (&Config{}).Check("", file, f, info)
}

// Check resolves every name in f, a file of the package at the import
// path, as the function Check does, with the packages it imports
// loaded through the Config's Importer. The program being compiled has
// the empty path; a library package is checked under its own, into the
// same Info, so the backend compiles them together.
func (conf *Config) Check(path string, file *token.File, f *ast.File, info *Info) (*Package, error) {
	return conf.CheckFiles(path, []*token.File{file}, []*ast.File{f}, info)
}

// CheckFiles checks a package of several files, as Check checks one:
// the files of one directory, parsed into one FileSet so that their
// positions are distinct, each with its syntax. The files see each
// other's declarations and each its own imports; files that disagree
// on the package clause are refused, naming both packages and a file of
// each.
func (conf *Config) CheckFiles(path string, files []*token.File, fs []*ast.File, info *Info) (*Package, error) {
	if len(files) != len(fs) || len(fs) == 0 {
		panic("types: CheckFiles needs one token.File per ast.File")
	}

	c := &Checker{conf: conf, files: files, file: files[0], info: info, typeScopes: make(map[*ast.FuncDecl]*Scope), badMethods: make(map[*ast.FuncDecl]bool)}
	c.pkg = &Package{name: fs[0].Name.Name, path: path, std: conf.Standard, scope: NewScope(Universe)}
	for i, f := range fs[1:] {
		if f.Name.Name != c.pkg.name {
			first, other := filepath.Base(files[0].Name()), filepath.Base(files[i+1].Name())
			c.errorf(f.Name.Pos(), "found packages "+c.pkg.name+" ("+first+") and "+f.Name.Name+" ("+other+") in "+filepath.Base(filepath.Dir(files[0].Name())))
		}
	}

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

		if info.Wraps == nil {
			info.Wraps = make(map[*ast.TryExpr]*Variant)
		}

		if info.Values == nil {
			info.Values = make(map[ast.Expr]Constant)
		}
	}

	c.checkFiles(fs)
	c.errors.Sort()
	return c.pkg, c.errors.Err()
}

// Name returns the package name.
func (p *Package) Name() string { return p.name }

// Path returns the import path of the package, or the empty string for
// the program being compiled.
func (p *Package) Path() string { return p.path }

// Standard reports whether the package is the standard library's,
// checked with the library's privileges and named in the runtime's
// symbol space.
func (p *Package) Standard() bool { return p.std }

// Scope returns the package scope.
func (p *Package) Scope() *Scope { return p.scope }

// checkFile runs the passes in order: collect the file's declarations
// into the package scope, give them their types, then walk the bodies.
// checkFiles walks the package's files together, a phase at a time
// over all of them, so that a declaration in one file is known to
// every other before any body is entered: the imports and the
// declared names of each file, then the types, then the constants,
// any of which a type's array length checked earlier, then the
// signatures, then the initializers of the variables, then the
// bodies. Each file has its own scope for its imports, under
// the package scope, and its own table of declared objects, by the
// index of the declaration in the file.
func (c *Checker) checkFiles(fs []*ast.File) {
	c.fileState = make([]fileState, len(fs))
	enter := c.enter
	for i, f := range fs {
		c.fileState[i].scope = NewScope(c.pkg.scope)
		enter(i)
		c.collectImports(f, c.fileState[i].scope)
		c.collectDecls(f)
		c.fileState[i].objs = c.objs
	}

	c.collectEarly(fs)
	c.collectConsts(fs)

	for i, f := range fs {
		enter(i)
		c.resolveTraitLists(f)
	}

	for i, f := range fs { // every file's refinements known, then checked for cycles, then given to the instances, then ordered
		enter(i)
		c.checkRefinementCycles(f)
	}

	for i, f := range fs {
		enter(i)
		c.fillTraitRefines(f)
	}

	for i, f := range fs {
		enter(i)
		c.orderTraitLists(f)
	}

	types := 0
	for _, f := range fs {
		for _, d := range f.Decls {
			if _, ok := d.(*ast.TypeDecl); ok {
				types++
			}
		}
	}

	named := make([]*Named, 0, types)
	for i, f := range fs {
		enter(i)
		named = c.resolveTypes(f, named)
	}

	c.checkTypeCycles(named)
	c.resolvePending()
	c.resolvePendingBounds()
	c.checkConsts()
	c.checkDefaults()
	for i, f := range fs {
		enter(i)
		c.resolveSignatures(f)
	}

	for i, f := range fs {
		enter(i)
		c.checkRefinements(f)
		c.checkImpls(f)
		c.checkAttrs(f) // an attribute's type lists Attribute, known once the impl lists are
	}

	c.collectUninferred(fs)
	for i, f := range fs {
		enter(i)
		c.initializers(f)
	}

	for i, f := range fs {
		enter(i)
		c.funcBodies(f)
	}

	c.checkDefaultBodies()
	c.resolveKeeps() // what each function keeps is known once every body is checked
	c.resolveHides() // and which type parameters a body hides
	c.checkCallCycles()
	c.reach(fs)
	c.freeLowerings()
}

// A fileState is a file's scope of its imports, under the package
// scope, and its table of declared objects, by the index of the
// declaration in the file.
type fileState struct {
	scope *Scope
	objs  []Object
}

// enter makes the file of index i the one being walked: its scope of
// imports, under the package scope, and its table of declared objects.
func (c *Checker) enter(i int) {
	c.file, c.scope, c.objs, c.fileIndex = c.files[i], c.fileState[i].scope, c.fileState[i].objs, i
}

// A walkPlace is where the checker walks: the file, its index, the
// innermost scope open and the file's declared objects, all that enter
// changes.
type walkPlace struct {
	file  *token.File
	index int
	scope *Scope
	objs  []Object
}

// here returns where the checker walks, for restore after it checks a
// declaration of another file on demand.
func (c *Checker) here() walkPlace {
	return walkPlace{c.file, c.fileIndex, c.scope, c.objs}
}

// restore makes p, which here returned, the place walked again, every
// part of it, so that nothing checked after it reads the other file's.
func (c *Checker) restore(p walkPlace) {
	c.file, c.fileIndex, c.scope, c.objs = p.file, p.index, p.scope, p.objs
}

// errorf reports an error at pos, once: an arm of a match on a type
// that lists several types is checked once for each, from copies of
// one body, and its mistakes are one each. Error paths may allocate
// freely.
func (c *Checker) errorf(pos token.Pos, msg string) {
	if c.formPass {
		if !slices.Contains(c.formErrs, formError{pos, msg}) {
			c.formErrs = append(c.formErrs, formError{pos, msg})
		}

		return
	}

	at := c.position(pos)
	for _, e := range c.errors {
		if e.Pos == at && e.Msg == msg {
			return
		}
	}

	c.errors.Add(at, msg)
}

// position resolves pos through the file of the package it is in,
// which is the file being walked but for a position of a declaration
// in another file.
func (c *Checker) position(pos token.Pos) token.Position {
	for _, f := range c.files {
		if f.Holds(pos) {
			return f.Position(pos)
		}
	}

	return c.file.Position(pos)
}

// lineCol formats a position as line:col, for a message that points
// at a second place; a place in another file of the package is named
// with that file.
func (c *Checker) lineCol(pos token.Pos) string {
	return c.positionText(c.position(pos))
}

// positionText writes p as line:column, with its file's name in front
// when that is not the file being checked.
func (c *Checker) positionText(p token.Position) string {
	at := strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Column)
	if p.Filename != c.file.Name() {
		return filepath.Base(p.Filename) + ":" + at
	}

	return at
}

// declare puts obj into scope under ident, recording the definition,
// and reports a redeclaration with the position of the first one.
func (c *Checker) declare(scope *Scope, ident *ast.Ident, obj Object) {
	if ident.Name == "_" {
		c.recordDef(ident, obj)
		return
	}

	if v, ok := obj.(*Var); ok {
		c.flowAssign(v, "") // a declaration gives the variable its value: what it held on a loop's last pass is let go
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
// An undefined name is reported once, here. A name at Config.At has the
// names visible there recorded first, whatever it resolves to: they are
// the scopes open now, and a local declared further on is not yet.
func (c *Checker) lookup(ident *ast.Ident) Object {
	if c.conf.At.IsValid() && ident.NamePos == c.conf.At && c.info != nil {
		c.info.Visible = c.scope.Visible()
	}

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

	if c.folding != nil {
		c.folding[e] = t
	}
}
