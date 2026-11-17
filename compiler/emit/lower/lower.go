// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package lower holds the decisions every backend makes the same way:
// what a backend takes in, which library functions the compiler writes
// in place of a body, the names of symbols and the predicates over
// types that say what a value needs when it is dropped. A backend walks
// the checked syntax tree on its own and writes its own text; what it
// must agree on with the others lives here, so that the differential
// test between backends compares two walks of one set of rules.
package lower

import (
	"errors"
	"path"
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// The backends, by the names a command and a test choose one by: the
// LLVM backend, and the C backend, the reference.
const (
	LLVMBackend = "llvm"
	CBackend    = "c"
)

// The directions of a channel handle as the runtime numbers them: the
// bits of the ends the handle holds.
const (
	HandleSend = 1
	HandleRecv = 2
	HandleBoth = HandleSend | HandleRecv
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
	Std    bool     // a package of the standard library, whose files are the compiler's own (SourceName)
}

// StdFiles are the files of the standard library's packages among
// libraries, which the line tables name under SourceName's directory:
// files and not names, since a program's own file may have the name of
// one of the library's.
func StdFiles(libraries []Library) map[*token.File]bool {
	out := make(map[*token.File]bool)
	for _, l := range libraries {
		if l.Std {
			out[l.File] = true
		}
	}

	return out
}

// A Binding is a package's C side, which the backends compile with the
// program: its import path, the symbol prefix that names its directory
// in a build, the header the compiler writes there for its C to
// include, the .c and .h files of its directory, and the functions it
// declares without a body, bound to C under their derived symbols.
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
// names could then collide in the output; no real program meets it.
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
// say it never returns yet; the cells of rc and sync, held by their
// handles as an address, since several handles own one cell and a mut
// is reached through a ref, which the language refuses and the count
// and the lock make safe there; the slots of a deque's buffer, which
// hold an element or nothing and so cannot be values of the language,
// which the deque's count and head make safe there; and testing.Catch
// runs a function under the runtime's mark, which takes the function
// as C takes one, through an adapter the backend writes.
// The functions of math/bits are written too, each as an instruction,
// and BitOp reads their names.
var lowered = map[string]bool{
	"fmt.Println": true, "fmt.Printf": true, "fmt.Sprint": true, "fmt.Sprintf": true,
	"fmt.Fprint": true, "fmt.Fprintln": true, "fmt.Fprintf": true, "os.Exit": true,
	"rc.rawNew": true, "rc.rawMut": true, "rc.rawFree": true, "rc.moveOut": true, "rc.leak": true,
	"sync.rawNew": true, "sync.rawMut": true, "sync.rawTake": true,
	"deque.rawAlloc": true, "deque.rawFree": true, "deque.rawPut": true, "deque.rawTake": true,
	"deque.rawRef": true, "deque.rawMut": true, "deque.rawMove": true,
	"testing.Catch": true, "testing.Logf": true, "expect.Failf": true,
}

// located names the functions of testing the compiler hands the place
// of their call before the call, the file and line alone; every
// exported function of expect is handed the whole call's text as
// well, so that a test's failure names its file and line, the
// expression as the test wrote it and the values of its parts: the
// backend hands the runtime the place, and for expect the parts, which
// it keeps for the running task and the package's where and parts read
// back, and then writes the call as any other.
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

// DeferredLent returns, for each argument of a deferred call, the local
// variable the call lends it from, or nil: a whole variable passed to a
// ref parameter, with ref or without, a variadic ...ref one's included,
// and after ... a slice lent to one. The deferred call reaches such a
// variable when it runs, through its address, which the record of the
// defer keeps: what the variable holds then is what the call reads,
// since the checker lets the variable be read and given a new value in
// between and only refuses moving it away. A ref lends its value in
// both backends, so a copy taken when the defer ran would read a value
// the variable had since dropped.
func DeferredLent(info *types.Info, call *ast.CallExpr) []*types.Var {
	sig, ok := ValueType(info.Types[call.Fun]).(*types.Signature)
	if !ok {
		return nil
	}

	kind, _ := sig.VariadicParam()
	fixed := len(sig.Params())
	if kind == types.VariadicOwned || kind == types.VariadicLent {
		fixed--
	}

	var lent []*types.Var
	for i, a := range call.Args {
		lends := kind == types.VariadicLent && i >= fixed // an element of a ...ref parameter, or the slice after ...
		if i < fixed {
			r, ok := sig.Params()[i].Type().(*types.Ref)
			lends = ok && !r.Mut()
		}

		v := Variable(info, unborrowed(a))
		if !lends || v == nil || v.Pkg() != nil || v.IsField() {
			continue
		}

		if lent == nil {
			lent = make([]*types.Var, len(call.Args))
		}

		lent[i] = v
	}

	return lent
}

// MayLeave reports whether evaluating x may leave the statement before
// it ends, by a return or a jump out of a loop: x holds a ?, a ??
// whose else may leave, or an if or match whose arms may, outside the
// body of a closure, which leaves only itself. A value made before such
// an operand, and given to what the statement builds after it, is held
// until then, so that leaving drops it.
func MayLeave(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.TryExpr, *ast.CoalesceExpr, *ast.IfExpr, *ast.MatchExpr:
		return true
	case *ast.ParenExpr:
		return MayLeave(x.X)
	case *ast.TupleExpr:
		return anyLeaves(x.Elts)
	case *ast.SelectorExpr:
		return MayLeave(x.X)
	case *ast.IndexExpr:
		return MayLeave(x.X) || MayLeave(x.Index)
	case *ast.SliceExpr:
		return MayLeave(x.X) || x.Low != nil && MayLeave(x.Low) || x.High != nil && MayLeave(x.High)
	case *ast.CallExpr:
		return MayLeave(x.Fun) || anyLeaves(x.Args)
	case *ast.SpawnExpr:
		return MayLeave(x.Call)
	case *ast.UnaryExpr:
		return MayLeave(x.X)
	case *ast.BinaryExpr:
		return MayLeave(x.X) || MayLeave(x.Y)
	case *ast.CompositeLit:
		for _, elt := range x.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				elt = kv.Value
			}

			if MayLeave(elt) {
				return true
			}
		}
	}

	return false
}

func anyLeaves(xs []ast.Expr) bool {
	for _, x := range xs {
		if MayLeave(x) {
			return true
		}
	}

	return false
}

// HandleDir returns the direction of a channel handle of type t as the
// runtime numbers them: a chan<- T sends, a <-chan T receives, a chan T
// does both.
func HandleDir(t types.Type) int {
	switch t.Underlying().(*types.Chan).Dir() {
	case types.SendOnly:
		return HandleSend
	case types.RecvOnly:
		return HandleRecv
	}

	return HandleBoth
}

// Narrowed reports whether a value of type have, a chan T, is wanted as
// a handle of one end, and which end that is as the runtime numbers
// them: the handle keeps that end and gives the other up where it
// meets the type wanted.
func Narrowed(have, want types.Type) (int, bool) {
	hc, ok := have.Underlying().(*types.Chan)
	if !ok || hc.Dir() != types.SendRecv {
		return 0, false
	}

	wc, ok := want.Underlying().(*types.Chan)
	if !ok || wc.Dir() == types.SendRecv {
		return 0, false
	}

	return HandleDir(want), true
}

// MayEnd reports whether evaluating x may end the statement before it
// ends: leave it, as MayLeave says, or panic in it, by a call, a spawn,
// an index, a slice, a division or a remainder. A value made before
// such an operand, and given to what the statement builds after it, is
// held until then, so that a panic whose task has a handle drops it.
func MayEnd(x ast.Expr) bool {
	if MayLeave(x) {
		return true
	}

	switch x := x.(type) {
	case *ast.CallExpr, *ast.SpawnExpr, *ast.IndexExpr, *ast.SliceExpr:
		return true
	case *ast.ParenExpr:
		return MayEnd(x.X)
	case *ast.TupleExpr:
		return anyEnds(x.Elts)
	case *ast.SelectorExpr:
		return MayEnd(x.X)
	case *ast.UnaryExpr:
		return MayEnd(x.X)
	case *ast.BinaryExpr:
		return x.Op == token.QUO || x.Op == token.REM || MayEnd(x.X) || MayEnd(x.Y)
	case *ast.CompositeLit:
		for _, elt := range x.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				elt = kv.Value
			}

			if MayEnd(elt) {
				return true
			}
		}
	}

	return false
}

func anyEnds(xs []ast.Expr) bool {
	for _, x := range xs {
		if MayEnd(x) {
			return true
		}
	}

	return false
}

// FramedVariadic returns the variadic ...T parameter of the function of
// signature sig and body, and whether the slice it is given may stay in
// its caller's frame for the whole call: true unless the body moves the
// slice away whole, other than consuming it in a range loop, or a
// closure captures it, either of which may keep it past the call. A
// function that may keep it gives it a buffer of its own when it starts
// (sn_slice_unframe); one that only reads it, ranges over it, changes
// its elements or lends it uses the caller's, with no allocation, and a
// change of its length moves it then. A range loop the walk does not
// reach, inside a match's arm, counts as a move that keeps: what is
// missed costs an allocation, never the frame.
func FramedVariadic(info *types.Info, sig *types.Signature, body *ast.BlockStmt) (*types.Var, bool) {
	if kind, _ := sig.VariadicParam(); kind != types.VariadicOwned || body == nil {
		return nil, false
	}

	xs := sig.Params()[len(sig.Params())-1]
	var ranged []ast.Expr // the range loops over xs itself, which consume it in the function
	rangesOver(info, body.List, xs, &ranged)
	for x, moved := range info.Moves {
		if id, ok := x.(*ast.Ident); ok && moved && sameParam(info.Uses[id], xs) && !slices.Contains(ranged, x) {
			return xs, false
		}
	}

	for _, caps := range info.Captures {
		for _, c := range caps {
			if sameParam(c.Var, xs) {
				return xs, false
			}
		}
	}

	return xs, true
}

// sameParam reports whether obj is the parameter p, or the parameter of
// the generic declaration an instance's p was made from, which keeps its
// name and its place, and which the body's names refer to.
func sameParam(obj types.Object, p *types.Var) bool {
	v, ok := obj.(*types.Var)
	return ok && (v == p || v.Pos() == p.Pos() && v.Name() == p.Name())
}

// rangesOver collects the range loops over the variable xs in a list of
// statements and the blocks inside them.
func rangesOver(info *types.Info, list []ast.Stmt, xs *types.Var, out *[]ast.Expr) {
	for _, st := range list {
		switch st := st.(type) {
		case *ast.RangeStmt:
			if id, ok := st.X.(*ast.Ident); ok && sameParam(info.Uses[id], xs) {
				*out = append(*out, st.X)
			}

			rangesOver(info, st.Body.List, xs, out)
		case *ast.BlockStmt:
			rangesOver(info, st.List, xs, out)
		case *ast.ForStmt:
			rangesOver(info, st.Body.List, xs, out)
		case *ast.IfStmt:
			rangesOver(info, st.Body.List, xs, out)
			if st.Else != nil {
				rangesOver(info, []ast.Stmt{st.Else}, xs, out)
			}
		case *ast.IfBindStmt:
			rangesOver(info, st.Body.List, xs, out)
			if st.Else != nil {
				rangesOver(info, []ast.Stmt{st.Else}, xs, out)
			}
		}
	}
}

// Taken returns what a conversion's operand of type t is evaluated as,
// the wanted types of operands: t when its value is not Copy, which the
// conversion takes, as an argument by value is taken, so a temporary is
// not dropped after it and a variable is moved; none for a Copy value,
// which is only read.
func Taken(t types.Type) []types.Type {
	if types.IsCopy(t) {
		return nil
	}

	return []types.Type{t}
}

// LeftOut returns the indices of the fields of st, a struct literal's
// type, that the literal x leaves out and that declare a default, in
// the order of the struct, each of which the literal gives its
// default's folded value.
func LeftOut(st *types.Struct, x *ast.CompositeLit) []int {
	var out []int
	for i := 0; i < st.NumFields(); i++ {
		if st.Default(i) == nil || named(x, st.Field(i).Name()) {
			continue
		}

		out = append(out, i)
	}

	return out
}

// named reports whether the keyed literal x gives the field name.
func named(x *ast.CompositeLit, name string) bool {
	for _, elt := range x.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == name {
				return true
			}
		}
	}

	return false
}

// unborrowed strips a ref written before an argument, ref s, to the
// place it lends.
func unborrowed(e ast.Expr) ast.Expr {
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.REF {
		return u.X
	}

	return e
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
