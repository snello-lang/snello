// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/runtime"
)

// TestCodeSection holds the section a backend writes a program's code
// into to the one the runtime looks for: the header's mark names it on
// every system, the scheduler reads it by that name on Linux and from
// the loader on macOS, and on Windows its two marks sort before and
// after it. Were the names to part, a program's tasks would no longer
// be preempted, and nothing else would say so.
func TestCodeSection(t *testing.T) {
	files := runtime.Files()
	header, sched := string(files["snello.h"]), string(files["sched.c"])
	for _, system := range []string{platform.Darwin, platform.Linux, platform.Windows} {
		if name := CodeSection(system); !strings.Contains(header, `text=\"`+name+`\"`) {
			t.Errorf("%s: the runtime's header opens no section named %s", system, name)
		}
	}

	if name := CodeSection("freebsd"); name != CodeSection(platform.Linux) {
		t.Errorf("another system's section is %s, want an ELF binary's, %s", name, CodeSection(platform.Linux))
	}

	if elf := CodeSection(platform.Linux); !strings.HasPrefix(elf, ".") || !strings.Contains(sched, `code_section[] = "`+elf+`"`) {
		t.Errorf("the scheduler does not read %s by its name, or the name is one a C program could write, which a link keeps every function of", elf)
	}

	parts := strings.Split(CodeSection(platform.Darwin), ",")
	if len(parts) < 2 || !strings.Contains(sched, `"`+parts[0]+`", "`+parts[1]+`"`) {
		t.Errorf("the scheduler does not ask the loader for %v", parts)
	}

	pe := CodeSection(platform.Windows)
	first, last := pe[:len(pe)-1]+"a", pe[:len(pe)-1]+"z"
	if !(first < pe && pe < last) || !strings.Contains(sched, `"`+first+`"`) || !strings.Contains(sched, `"`+last+`"`) || !strings.Contains(sched, `"`+pe+`"`) {
		t.Errorf("the scheduler's marks do not sort around %s as %s and %s", pe, first, last)
	}
}

// TestMangle writes types as fragments of symbols: a signature with no
// parameter and with several, a tuple, a type made of others, and a
// prefixed symbol, which is the prefix and the fragment; a declared
// type's and a function's symbols are kept on them once made, an
// instance's own, and the predeclared Ordering, shared by every check
// in the process, keeps none.
func TestMangle(t *testing.T) {
	f, file, err := parser.ParseFile("t.snello", []byte(`package main
struct Pair<T> { a T; b T }
func f() {}
func main() {}`))
	if err != nil {
		t.Fatal(err)
	}

	pkg, err := (&types.Config{}).Check("", file, f, nil)
	if err != nil {
		t.Fatal(err)
	}

	pair := pkg.Scope().Lookup("Pair").(*types.TypeName)
	fn := pkg.Scope().Lookup("f").(*types.Func)
	intT := types.Typ[types.Int]
	named := func(name string) types.Type {
		return types.NewNamed(types.NewTypeName(token.NoPos, name, nil), types.NewStruct(nil), nil)
	}
	param := func(typ types.Type) *types.Var { return types.NewVar(token.NoPos, "p", typ) }
	tests := []struct {
		t    types.Type
		want string
	}{
		{types.NewSignature(nil, nil, types.Unit), "fn0_unit"},
		{types.NewSignature(nil, nil, intT), "fn0_int"},
		{types.NewSignature(nil, []*types.Var{param(intT), param(types.Typ[types.String])}, types.Unit), "fn2_int_string_unit"},
		{types.NewTuple(intT, types.Typ[types.String]), "tup2_int_string"},
		{types.NewOption(types.NewSlice(types.NewRef(intT, false))), "opt_slice_ref_int"},
		{types.NewMap(types.Typ[types.String], types.NewRef(intT, true)), "map_string_mut_int"},
		{types.NewArray(intT, 3), "array_3_int"},
		{types.NewChan(types.SendRecv, intT), "chan_int"},
		{types.NewChan(types.SendOnly, intT), "sendchan_int"},
		{types.NewChan(types.RecvOnly, intT), "recvchan_int"},
		{types.NewResult(intT, types.Typ[types.Error]), "res_int_error"},
		{types.Ordering, "Ordering"},
		{types.Instantiate(pair.Type().(*types.Named), []types.Type{intT}), "sn_g4Pair_int"},
		{types.NewSlice(types.Ordering), "slice_8Ordering"},        // a name inside after its length
		{types.NewTuple(named("A_x"), named("B")), "tup2_3A_x_1B"}, // one with an underscore after its length, so that
		{types.NewTuple(named("A"), named("x_B")), "tup2_1A_3x_B"}, // these two are two
	}
	for _, tt := range tests {
		if got := Mangle(tt.t); got != tt.want {
			t.Errorf("Mangle(%s) = %q, want %q", tt.t, got, tt.want)
		}

		if got, want := Symbol("sn_drop_", tt.t), "sn_drop_"+tt.want; got != want {
			t.Errorf("Symbol(sn_drop_, %s) = %q, want %q", tt.t, got, want)
		}
	}

	inst := types.Instantiate(pair.Type().(*types.Named), []types.Type{intT})
	if inst.Symbol() != "sn_g4Pair_int" {
		t.Errorf("the instance's symbol is %q after NamedName, want it kept", inst.Symbol())
	}

	if got := NamedName(inst); got != "sn_g4Pair_int" {
		t.Errorf("NamedName again = %q, want sn_g4Pair_int", got)
	}

	if got := FuncName(fn); got != "sn__f" || fn.Symbol() != "sn__f" {
		t.Errorf("FuncName(f) = %q with %q kept, want sn__f both", got, fn.Symbol())
	}

	if types.Ordering.Symbol() != "" || types.Ordering.Obj().Symbol() != "" {
		t.Errorf("Ordering keeps a symbol, %q and %q, and is shared by every check", types.Ordering.Symbol(), types.Ordering.Obj().Symbol())
	}
}

// TestPathPrefix writes the symbol prefix of import paths, a package of
// the program's own and a standard one, and refuses two paths that
// would share one.
func TestPathPrefix(t *testing.T) {
	tests := []struct{ path, prefix, std string }{
		{"shapes", "snm_shapes_", "sn_shapes_"},
		{"fmt", "snm_fmt_", "sn_fmt_"},
		{"net/http", "snm_net__http_", "sn_net__http_"},
		{"app/shapes", "snm_app__shapes_", "sn_app__shapes_"},
		{"my-app", "snm_my_app_", "sn_my_app_"},
	}
	for _, tt := range tests {
		if got := PathPrefix(tt.path); got != tt.prefix {
			t.Errorf("PathPrefix(%q) = %q, want %q", tt.path, got, tt.prefix)
		}

		if got := StdPrefix(tt.path); got != tt.std {
			t.Errorf("StdPrefix(%q) = %q, want %q", tt.path, got, tt.std)
		}
	}

	if err := CheckPrefixes([]Library{{Path: "fmt"}, {Path: "app/a.b"}, {Path: "app/a_c"}}); err != nil {
		t.Errorf("distinct prefixes: %v", err)
	}

	err := CheckPrefixes([]Library{{Path: "app/a.b"}, {Path: "app/a_b"}})
	if want := "import paths app/a.b and app/a_b would share one symbol prefix"; err == nil || err.Error() != want {
		t.Errorf("shared prefix: got %v, want %q", err, want)
	}
}

// TestDisplayName holds the name a trace gives a function: a method
// with its type, a function of a package with the package, main's
// functions bare, and a generic instance with its arguments.
func TestDisplayName(t *testing.T) {
	f, file, err := parser.ParseFile("t.snello", []byte(`package main
struct Grid { n int }
func (g ref Grid) At(i int) int { return g.n + i }
func render() {}
func keep<T>(v T) T { return v }
func main() {}`))
	if err != nil {
		t.Fatal(err)
	}

	pkg, err := (&types.Config{}).Check("", file, f, nil)
	if err != nil {
		t.Fatal(err)
	}

	grid := pkg.Scope().Lookup("Grid").(*types.TypeName)
	at := grid.Type().(*types.Named).Method("At")
	if got := DisplayName(at, RecvType(at.Type().(*types.Signature)), nil); got != "Grid.At" {
		t.Errorf("a method is named %q, want Grid.At", got)
	}

	if got := DisplayName(pkg.Scope().Lookup("render").(*types.Func), nil, nil); got != "render" {
		t.Errorf("a function of main is named %q, want render", got)
	}

	if got := DisplayName(pkg.Scope().Lookup("keep").(*types.Func), nil, []types.Type{types.Typ[types.Int]}); got != "keep[int]" {
		t.Errorf("an instance is named %q, want keep[int]", got)
	}

	if got := RecvType(pkg.Scope().Lookup("render").(*types.Func).Type().(*types.Signature)); got != nil {
		t.Errorf("a function has a receiver type %v", got)
	}
}

// TestSourceName holds a file's name in the line tables to the path the
// compiler read it from, and a file of the standard library's to its
// name under StdSources, which no file of the user's has.
func TestSourceName(t *testing.T) {
	if got := SourceName("src/prog/main.snello", false); got != "src/prog/main.snello" {
		t.Errorf("a program's file is named %q", got)
	}

	if got := SourceName("json/json.snello", true); got != StdSources+"json/json.snello" {
		t.Errorf("a file of the standard library's is named %q", got)
	}

	fmtFile, ownFmt := token.NewFileSet().AddFile("fmt/fmt.snello", 1), token.NewFileSet().AddFile("fmt/fmt.snello", 1)
	if std := StdFiles([]Library{{File: fmtFile, Std: true}, {File: ownFmt}}); !std[fmtFile] || std[ownFmt] {
		t.Errorf("the standard library's files are %v; a program's own file of the same name is not one", std)
	}
}

// TestBasicAndCaseNames holds the C names both backends describe a value
// by: a basic type's, and an enum's tag and cases'.
func TestBasicAndCaseNames(t *testing.T) {
	for kind, want := range map[types.BasicKind]string{types.Int: "sn_int", types.Bool: "bool", types.String: "sn_string", types.Uint8: "uint8_t", types.Float64: "double"} {
		if got := BasicName(types.Typ[kind]); got != want {
			t.Errorf("BasicName(%v) = %q, want %q", kind, got, want)
		}
	}

	shape := types.NewNamed(types.NewTypeName(token.NoPos, "Shape", nil), nil, nil)
	if got := TagName(shape); got != "sn_tag_Shape" {
		t.Errorf("TagName = %q", got)
	}

	if got := CaseName(shape, types.NewVariant(token.NoPos, "Rect", shape, 1, nil)); got != "sn_case_5_Shape_Rect" {
		t.Errorf("CaseName = %q", got)
	}
}
