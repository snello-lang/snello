// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

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
		{types.Instantiate(pair.Type().(*types.Named), []types.Type{intT}), "Pair_int"},
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
	if inst.Symbol() != "Pair_int" {
		t.Errorf("the instance's symbol is %q after NamedName, want it kept", inst.Symbol())
	}

	if got := NamedName(inst); got != "Pair_int" {
		t.Errorf("NamedName again = %q, want Pair_int", got)
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
