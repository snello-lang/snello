// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"math"
	"strconv"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/parser"
)

// constAt checks src, a package declaring the constant v, and returns
// v's type and value, failing on any error.
func constAt(t *testing.T, src string) (Type, Constant) {
	t.Helper()
	f, file, err := parser.ParseFile("test.snello", []byte("package p\nimport \"math\"\nconst _ = math.Pi\n"+src))
	if err != nil {
		t.Fatalf("%q: parse error: %v", src, err)
	}

	pkg, err := libraryConfig().Check("", file, f, &Info{})
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}

	k, ok := pkg.Scope().Lookup("v").(*Const)
	if !ok {
		t.Fatalf("%q: no constant v", src)
	}

	return k.Type(), k.Val()
}

// The folding agrees with run-time arithmetic: the compiler's own on
// the same values, which is what both backends compute.
func TestFoldIntegers(t *testing.T) {
	neg, two := -7, 2
	for _, tt := range []struct {
		src  string
		typ  string
		want string
	}{
		{"const v = 1 << 10", "int", "1024"},
		{"const KB = 1 << 10\nconst v = KB * KB", "int", "1048576"},
		{"const v = KB * KB\nconst KB = 1 << 10", "int", "1048576"}, // declared after its use
		{"const v = -7 / 2", "int", strconv.Itoa(neg / two)},
		{"const v = -7 % 2", "int", strconv.Itoa(neg % two)},
		{"const v = -7 >> 1", "int", strconv.Itoa(neg >> 1)},
		{"const v = 0xff &^ 0x0f", "int", "240"},
		{"const v u8 = ^u8(0x0f)", "u8", "240"},
		{"const v i8 = ^i8(0)", "i8", "-1"},
		{"const v i8 = -128", "i8", "-128"},
		{"const v = 1 << 62 >> 60", "int", "4"},
		{"const v u64 = 1 << 63", "u64", "9223372036854775808"},
		{"const v = 'a' + 1", "rune", "98"},
		{"const v = u8(240)", "u8", "240"},
		{"const v = int(2.0e3)", "int", "2000"},
		{"const v = i16(math.MaxInt8) + 1", "i16", "128"},
		{"type Size int\nconst v Size = 3 * 4", "Size", "12"},
	} {
		typ, v := constAt(t, tt.src)
		if typ.String() != tt.typ || v.Kind() != ConstInt || v.Int().String() != tt.want {
			t.Errorf("%q: got %v %s, want %s %s", tt.src, typ, v.text(), tt.typ, tt.want)
		}
	}
}

func TestFoldFloats(t *testing.T) {
	one, three := 1.0, 3.0
	for _, tt := range []struct {
		src  string
		want float64
	}{
		{"const v = 1.0 / 3.0", one / three},
		{"const v f32 = 1.0 / 3.0", float64(float32(one) / float32(three))},
		{"const v f32 = 0.1", float64(float32(0.1))},
		{"const v = 2.5 * 2", 5},
		{"const B = 1 << 53 + 1\nconst v = f64(B)", float64(1 << 53)}, // rounded to the nearest, ties to even
		{"const v = -0.0", math.Copysign(0, -1)},
		{"const v = math.Pi * 2.0", math.Pi * 2},
	} {
		_, v := constAt(t, tt.src)
		if v.Kind() != ConstFloat || math.Float64bits(v.Float()) != math.Float64bits(tt.want) {
			t.Errorf("%q: got %v, want %v", tt.src, v.Float(), tt.want)
		}
	}
}

func TestFoldBoolsAndStrings(t *testing.T) {
	for _, tt := range []struct {
		src  string
		want string
	}{
		{"const v = \"a\" + \"b\"", `"ab"`},
		{"const N = \"x\"\nconst v = N + N + \"\\n\"", `"xx\n"`},
		{"const v = 1 < 2 && \"a\" != \"b\"", "true"},
		{"const v = !(2.5 > 3.0) || false", "true"},
		{"const v = \"ab\" < \"a\"", "false"},
	} {
		_, v := constAt(t, tt.src)
		if v.text() != tt.want {
			t.Errorf("%q: got %s, want %s", tt.src, v.text(), tt.want)
		}
	}
}

// The value of each constant expression is recorded for the backends:
// the value of a constant, and each field of a struct constant.
func TestFoldRecorded(t *testing.T) {
	src := "package p\nstruct P { x int; s string }\nconst N = 2\nconst v = P{x: N * 3, s: \"a\" + \"b\"}\nconst w = N + 1"
	f, file, err := parser.ParseFile("test.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &Info{}
	if _, err := libraryConfig().Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	var got []string
	record := func(x ast.Expr) {
		if v, ok := info.Values[x]; ok {
			got = append(got, ast.Source(x)+" = "+v.text())
		}
	}

	for _, d := range f.Decls {
		d, ok := d.(*ast.VarDecl)
		if !ok {
			continue
		}

		for _, x := range d.Values {
			record(x)
			if lit, ok := x.(*ast.CompositeLit); ok {
				for _, elt := range lit.Elts {
					record(elt.(*ast.KeyValueExpr).Value)
				}
			}
		}
	}

	want := []string{"2 = 2", "N * 3 = 6", "\"a\" + \"b\" = \"ab\"", "N + 1 = 3"}
	if len(got) != len(want) {
		t.Fatalf("recorded %q, want %q", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("recorded %q, want %q", got[i], want[i])
		}
	}
}

// A field's default is folded in its field's type and recorded for the
// backends, a constant declared after the struct included.
func TestFoldDefaults(t *testing.T) {
	src := "package p\nstruct S { a int = N * 2; b f64 = 1; c string = \"x\" + \"y\"; d u8 = 255 }\nconst N = 21"
	f, file, err := parser.ParseFile("test.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &Info{}
	pkg, err := libraryConfig().Check("", file, f, info)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"N * 2 = 42 int", "1 = 1 f64", "\"x\" + \"y\" = \"xy\" string", "255 = 255 u8"}
	st := pkg.Scope().Lookup("S").Type().Underlying().(*Struct)
	for i, v := range st.fields {
		x := st.Default(i)
		got := ast.Source(x) + " = " + info.Values[x].text() + " " + info.Types[x].String()
		if got != want[i] {
			t.Errorf("field %s: %s, want %s", v.Name(), got, want[i])
		}
	}
}

// A refused default stays the field's default, so a literal that
// leaves the field out, keyed or positional, is not refused for it a
// second time, whatever the field's type: the one mistake is the one
// error.
func TestRefusedDefault(t *testing.T) {
	for _, src := range []string{
		"func size() int { return 4 }\nstruct A { n int = size() }\nfunc f() { print(A{}) }",
		"struct A { m int; n int = \"four\" }\nfunc f() { print(A{}, A{1}) }",
		"struct In { a int = 2 }\nconst cIn = In{a: 7}\nstruct S { x int; in In = cIn }\nfunc f() { print(S{x: 1}, S{}, S{2}) }", // In has no zero value
	} {
		if got := checkErrors(t, "package p\n"+src); len(got) != 1 {
			t.Errorf("%s: got %q, want the default's error alone", src, got)
		}
	}
}

func TestIntRange(t *testing.T) {
	for _, tt := range []struct {
		kind        BasicKind
		least, most string
	}{
		{Int8, "-128", "127"},
		{Uint8, "0", "255"},
		{Int32, "-2147483648", "2147483647"},
		{Int, "-9223372036854775808", "9223372036854775807"},
		{Uint64, "0", "18446744073709551615"},
	} {
		least, most := intRange(Typ[tt.kind])
		if least.String() != tt.least || most.String() != tt.most {
			t.Errorf("%s: got %s..%s, want %s..%s", Typ[tt.kind], least, most, tt.least, tt.most)
		}
	}
}
