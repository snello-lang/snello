// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package lower holds the decisions every backend makes the same way,
// as _docs/design/07-lowering.md records them: what a backend takes
// in, which library functions the compiler writes in place of a body,
// and in time the names of symbols and the predicates over types that
// say what a value needs when it is dropped. A backend walks the
// checked syntax tree on its own and writes its own text; what it
// must agree on with the others lives here, so that the differential
// test between backends compares two walks of one set of rules.
package lower

import (
	"errors"
	"path"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// A Library is a package written in Snello that the program imports, as
// the importer loaded it: its import path, its source file and its
// syntax, checked into the program's Info, and the .c and .h files of
// its directory when it binds C, none for a standard package. It is
// compiled with the program, into the same output, its names qualified
// by its import path.
type Library struct {
	Path   string
	File   *token.File
	Syntax *ast.File
	CFiles []string
	Links  []string // the system libraries the package's module links
}

// A Binding is a package's C side, which the backends compile with the
// program (_docs/design/08-packages.md, section 6): its import path,
// the symbol prefix that names its directory in a build, the header the
// compiler writes there for its C to include, the .c and .h files of
// its directory, and the functions it declares without a body, bound
// to C under their derived symbols.
type Binding struct {
	Path   string
	Prefix string
	Header string
	Files  []string
	Funcs  []*types.Func
	Links  []string // the system libraries to link for the package, its module's link lines
}

// Bindings collects the C side of every library that has one, in the
// order of the libraries: a standard package has none, since the
// runtime is its C side, and a package of the program's own has one
// when its directory holds a .c or .h file or its module links a
// system library.
func Bindings(info *types.Info, libraries []Library) []Binding {
	var bindings []Binding
	index := make(map[string]int)
	for _, lib := range libraries {
		if len(lib.CFiles) == 0 && len(lib.Links) == 0 {
			continue
		}

		i, ok := index[lib.Path]
		if !ok {
			i = len(bindings)
			index[lib.Path] = i
			bindings = append(bindings, Binding{Path: lib.Path, Prefix: strings.TrimSuffix(PathPrefix(lib.Path), "_"), Header: path.Base(lib.Path) + ".snello.h", Files: lib.CFiles, Links: lib.Links})
		}

		for _, d := range lib.Syntax.Decls {
			if d, ok := d.(*ast.FuncDecl); ok && d.Body == nil {
				bindings[i].Funcs = append(bindings[i].Funcs, info.Defs[d.Name].(*types.Func))
			}
		}
	}

	return bindings
}

// CheckPrefixes refuses two libraries whose import paths would share
// one symbol prefix, example.com/a.b and example.com/a_b, since their
// names could then collide in the output; no real program meets it
// (_docs/design/08-packages.md).
func CheckPrefixes(libraries []Library) error {
	paths := make(map[string]string, len(libraries))
	for _, lib := range libraries {
		prefix := PathPrefix(lib.Path)
		if other, ok := paths[prefix]; ok && other != lib.Path {
			return errors.New("import paths " + other + " and " + lib.Path + " would share one symbol prefix")
		}

		paths[prefix] = lib.Path
	}

	return nil
}

// lowered names the library functions the compiler writes, by import
// path and name: fmt's printing is written by the type of each
// argument, with the format read at compile time, so the runtime has
// no formatter; os.Exit is the runtime's exit, since a function cannot
// say it never returns yet; and the primitives of rc and sync are the
// pointer itself, since a second owner of one pointer, and a mut
// reached through a ref, are what the language refuses and the
// reference count and the lock are what make them safe there; and
// testing.Catch runs a function under the runtime's mark, which takes
// the function as C takes one, through an adapter the backend writes.
// The functions of math/bits are written too, each as an instruction,
// and BitOp reads their names.
var lowered = map[string]bool{
	"fmt.Println": true, "fmt.Printf": true, "fmt.Sprint": true, "fmt.Sprintf": true,
	"fmt.Fprint": true, "fmt.Fprintln": true, "fmt.Fprintf": true, "os.Exit": true,
	"rc.alias": true, "rc.leak": true, "sync.alias": true, "sync.leak": true, "sync.inside": true,
	"testing.Catch": true, "testing.Logf": true, "expect.Failf": true,
}

// located names the functions of testing the compiler hands the place
// of their call before the call, the file and line alone; every
// exported function of expect is handed the whole call's text as
// well, so that a test's failure names its file and line, the
// expression as the test wrote it and the values of its parts
// (_docs/design/09-testing.md, section 4): the backend hands the
// runtime the place, and for expect the parts, which it keeps for the
// running task and the package's where and parts read back, and then
// writes the call as any other.
var located = map[string]string{"testing.Log": "line", "testing.Logf": "line", "testing.Skip": "line"}

// Located returns how the place of a call of fn is handed to it: call,
// line, or "" for a function that is not told.
func Located(fn *types.Func) string {
	if fn.Pkg() == nil {
		return ""
	}

	if fn.Pkg().Path() == "expect" && ast.IsExported(fn.Name()) {
		return "call"
	}

	return located[fn.Pkg().Path()+"."+fn.Name()]
}

// ClonesAsBytes reports whether a clone of a value of type t is a copy
// of its bytes: it is Copy, and no type in it writes a Clone of its
// own, which every clone the compiler writes calls, a derived Clone
// calling a field's. A slice's Clone and Extend copy such elements with
// memcpy and clone the rest one by one.
func ClonesAsBytes(t types.Type) bool {
	return types.IsCopy(t) && !WritesClone(t)
}

// WritesClone reports whether t, or a type inside it, declares a Clone
// method of its own, which every clone of it the compiler writes must
// call.
func WritesClone(t types.Type) bool {
	return writesClone(t, map[*types.Named]bool{})
}

func writesClone(t types.Type, seen map[*types.Named]bool) bool {
	switch t := t.(type) {
	case *types.Named:
		if seen[t] {
			return false
		}

		seen[t] = true
		if m := t.Method("Clone"); m != nil && !m.Derived() {
			return true
		}

		return writesClone(t.Underlying(), seen)
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if writesClone(t.Field(i).Type(), seen) {
				return true
			}
		}
	case *types.Tuple:
		for i := 0; i < t.Len(); i++ {
			if writesClone(t.At(i), seen) {
				return true
			}
		}
	case *types.Option:
		return writesClone(t.Elem(), seen)
	case *types.Result:
		return writesClone(t.Val(), seen) || writesClone(t.Err(), seen)
	case *types.Array:
		return writesClone(t.Elem(), seen)
	case *types.Enum:
		for i := 0; i < t.NumVariants(); i++ {
			for _, f := range t.Variant(i).Fields() {
				if writesClone(f.Type(), seen) {
					return true
				}
			}
		}
	}

	return false
}

// Variable returns the variable an expression names, through
// parentheses, or nil for any other expression.
func Variable(info *types.Info, e ast.Expr) *types.Var {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			break
		}

		e = p.X
	}

	if id, ok := e.(*ast.Ident); ok {
		v, _ := info.Uses[id].(*types.Var)
		return v
	}

	return nil
}

// Simple reports whether an expression may be evaluated twice without
// anyone noticing: a name, a literal, a field or an element of one, or
// the predeclared len of one. The parts of a condition are shown for
// such operands alone, since showing them evaluates them again.
func Simple(info *types.Info, e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Ident, *ast.BasicLit:
		return true
	case *ast.ParenExpr:
		return Simple(info, x.X)
	case *ast.SelectorExpr:
		return Simple(info, x.X)
	case *ast.IndexExpr:
		return Simple(info, x.X) && Simple(info, x.Index)
	case *ast.CallExpr:
		id, ok := x.Fun.(*ast.Ident)
		if !ok || id.Name != "len" || len(x.Args) != 1 {
			return false
		}

		_, builtin := info.Uses[id].(*types.Builtin) // not a function of the program's named len
		return builtin && Simple(info, x.Args[0])
	}

	return false
}

// Comparison reports whether a condition is a comparison of two
// operands worth showing: both simple, and not both literals.
func Comparison(info *types.Info, e ast.Expr) (x, y ast.Expr, ok bool) {
	b, isBinary := e.(*ast.BinaryExpr)
	if !isBinary {
		return nil, nil, false
	}

	switch b.Op {
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
	default:
		return nil, nil, false
	}

	if !Simple(info, b.X) || !Simple(info, b.Y) {
		return nil, nil, false
	}

	_, xlit := b.X.(*ast.BasicLit)
	_, ylit := b.Y.(*ast.BasicLit)
	if xlit && ylit {
		return nil, nil, false
	}

	return b.X, b.Y, true
}

// Parts says what the parts of an assertion are, which each backend
// then writes in its own text: typed for Equal, NotEqual and
// EqualSlice, whose parts are the name of the type compared, or the
// operands of a condition that is a comparison of simple operands, the
// literals among them left out, for True and False; neither for any
// other assertion.
func Parts(info *types.Info, x *ast.CallExpr, name string) (typed bool, operands []ast.Expr) {
	switch name {
	case "Equal", "NotEqual", "EqualSlice":
		return len(x.Args) > 0, nil
	case "True", "False":
		if len(x.Args) != 1 {
			return false, nil
		}

		l, r, ok := Comparison(info, x.Args[0])
		if !ok {
			return false, nil
		}

		for _, operand := range []ast.Expr{l, r} {
			if _, lit := operand.(*ast.BasicLit); !lit {
				operands = append(operands, operand)
			}
		}
	}

	return false, operands
}

// Key returns the key of fn among the lowered functions, path.Name, or
// the empty string for a function the compiler does not write.
func Key(fn *types.Func) string {
	if fn.Pkg() == nil {
		return ""
	}

	key := fn.Pkg().Path() + "." + fn.Name()
	if _, _, ok := BitOp(key); ok || lowered[key] {
		return key
	}

	return ""
}

// BitOp reads the key of a function of math/bits, which both backends
// write in place of the call as the processor's instruction: the
// operation, LeadingZeros, TrailingZeros, OnesCount, RotateLeft,
// Reverse or Len, and the width in bits of the type it is on, which the
// name ends with, 64 for the one on uint that names none.
func BitOp(key string) (op string, width int, ok bool) {
	name, found := strings.CutPrefix(key, "math/bits.")
	if !found {
		return "", 0, false
	}

	op = strings.TrimRight(name, "0123456789")
	switch op {
	case "LeadingZeros", "TrailingZeros", "OnesCount", "RotateLeft", "Reverse", "Len":
	default:
		return "", 0, false
	}

	switch name[len(op):] {
	case "", "64":
		return op, 64, true
	case "8":
		return op, 8, true
	case "16":
		return op, 16, true
	case "32":
		return op, 32, true
	}

	return "", 0, false
}

// HasSymbol reports whether fn is a function the program may name
// where it does not call it, as a value or as what a task runs: every
// function but the ones the compiler writes, which have no symbol to
// name, and of those the functions of math/bits, which the runtime
// defines as well.
func HasSymbol(fn *types.Func) bool {
	key := Key(fn)
	_, _, bit := BitOp(key)
	return key == "" || bit
}

// Links returns the system libraries the bindings link, each once, in
// the order met: -lNAME for each at the link of the program.
func Links(bindings []Binding) []string {
	var links []string
	seen := make(map[string]bool)
	for _, b := range bindings {
		for _, l := range b.Links {
			if !seen[l] {
				seen[l] = true
				links = append(links, l)
			}
		}
	}

	return links
}
