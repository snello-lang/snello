// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/scanner"
)

// attrDecls are the attribute types the tests write: one over a string,
// a struct of constants, and a struct with no fields.
const attrDecls = `
type Name string impl Attribute
struct Range impl Attribute { min int; max int }
struct Omit impl Attribute {}
type Plain string
const Wide = Range{min: 0, max: 99}
`

// TestAttrs checks the attributes a program may write and read: on a
// field, a variant and a type, of a type over a string, a struct and a
// struct written by its name, a constant's name among them, and attr
// reading each from a part and a type parameter.
func TestAttrs(t *testing.T) {
	tests := []string{
		"struct S { a int @Name(\"x\") @Omit; b, c string = \"d\", \"e\" @Range{min: 1, max: 2}; d int @Wide }",
		"enum E @Name(\"e\") { A @Name(\"a\"); B(int) @Omit; C }",
		"struct S @Omit { a int }",
		"type F string impl FieldAttribute\ntype V string impl VariantAttribute, FieldAttribute\nstruct S { a int @F(\"x\") @V(\"y\") }\nenum E { A(n int @F(\"z\")) @V(\"a\") }", // a variant's field is a field
		"func f<T>() int { return match T { struct => { n := 0; for f := range fields(T) { if attr(f, Name) != None { n += 1 } }; n }, _ => 0 } }",
		"func f<T>() string { return match T { enum => match attr(T, Name) { Some(n) => string(n), None => \"\" }, _ => \"\" } }",
		"func f<T>(v ref T) int { return match T { enum => match v { for c := range variants(T) => { match attr(c, Omit) { Some(_) => 2, None => 1 } } }, _ => 0 } }",
	}
	for _, src := range tests {
		if got := checkErrors(t, "package p\n"+attrDecls+src); len(got) != 0 {
			t.Errorf("%s:\n  got %q, want no errors", src, got)
		}
	}
}

// TestAttrErrors checks the attributes refused where they are written
// and the uses of attr refused, each with its message.
func TestAttrErrors(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"struct S { a int @Plain(\"x\") }", "a value of type Plain is not an attribute: an attribute's type lists Attribute"},
		{"struct S { a int @5 }", "a value of type int is not an attribute"},
		{"struct S { a int @\"x\" }", "a value of type string is not an attribute"},
		{"var v = \"x\"\nstruct S { a int @Name(v) }", "is not constant"},
		{"struct S { a int @Range }", "Range is a type: write a value of it, the attribute, as a constant"},
		{"struct S { a int @Plain }", "Plain is not an attribute: it does not list Attribute"},
		{"struct S { a int @Name(\"x\") @Name(\"y\") }", "Name is given twice"},
		{"struct S { a int @Undeclared }", "undefined"},
		{"struct S { a int @Undef(1) }", "undefined: Undef"},
		// An attribute limited to some places is refused on the others.
		{"type F string impl FieldAttribute\nstruct S @F(\"x\") { a int }", "F is not written on a type: it lists FieldAttribute"},
		{"type F string impl FieldAttribute\nenum E { A @F(\"x\") }", "F is not written on a variant: it lists FieldAttribute"},
		{"type T string impl TypeAttribute\nstruct S { a int @T(\"x\") }", "T is not written on a field: it lists TypeAttribute"},
		{"type FV string impl FieldAttribute, VariantAttribute\nstruct S @FV(\"x\") { a int }", "it lists FieldAttribute and VariantAttribute"},
		{"struct G<T> impl FieldAttribute { v T }", "G cannot be an attribute"},
		{"struct G<T> impl Attribute { v T }", "G cannot be an attribute: an attribute is a constant, a number, a string, a bool or a struct of them, and not generic"},
		{"enum EA impl Attribute { X; Y }", "EA cannot be an attribute"},
		{"struct H impl Attribute { s []int }", "H cannot be an attribute"},
		{"struct In { n int }\nstruct S { In @Name(\"in\")\n a int }", "an embedded field takes no attribute"},
		{"func f<T>() { match T { struct => { for f := range fields(T) { x := attr(f, Plain) } }, _ => {} } }", "Plain is not an attribute's type"},
		{"func f<T>() { match T { struct => { for f := range fields(T) { x := attr(f, Name(\"x\")) } }, _ => {} } }", "is not an attribute's type"},
		{"func f<T>() { x := attr(T, Name) }", "attr reads the attributes of a part a loop over fields or variants gives"},
		{"func f(n int) { x := attr(n, Name) }", "attr reads the attributes of a part"},
		{"func f<T>() { match T { struct => { x := attr(T) }, _ => {} } }", "attr takes what an attribute is written on and the attribute's type"},
	}
	for _, tt := range tests {
		got := checkErrors(t, "package p\n"+attrDecls+tt.src)
		if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), tt.want) {
			t.Errorf("%s:\n  got %q\n  want an error containing %q", tt.src, got, tt.want)
		}
	}

	// A library's attribute limited to fields, written on a type.
	if got := checkErrors(t, "package p\nimport \"encoding/json\"\nstruct S @json.Skip { a int }\n"); len(got) != 1 || !strings.Contains(got[0], "json.Skip is not written on a type: it lists FieldAttribute") {
		t.Errorf("@json.Skip on a type: got %q, want it refused for listing FieldAttribute", got)
	}
}

// TestAttrQuiet checks that a mistake already reported is not reported
// again by an attribute or by attr: one error each.
func TestAttrQuiet(t *testing.T) {
	for _, src := range []string{
		"struct S { a int @Undef(1) }",
		"func f<T>() { match T { struct => { for f := range fields(T) { x := attr(g, Name) } }, _ => {} } }",
		"func f<T>() { match T { struct => { for f := range fields(T) { g := f; x := attr(g, Name) } }, _ => {} } }",
	} {
		if got := checkErrors(t, "package p\n"+attrDecls+src); len(got) != 1 {
			t.Errorf("%s:\n  got %d errors %q, want 1", src, len(got), got)
		}
	}
}

// TestAttrRecorded checks what the checker records for the backends: a
// field's, a variant's and a type's attributes by the position of the
// name, each with its type and value expression, and nil for a struct
// written by its name; a declaration without any records nothing.
func TestAttrRecorded(t *testing.T) {
	src := "package p\n" + attrDecls + "struct S @Omit { a int @Name(\"x\"); b int }\nenum E { A @Wide; B(n int @Name(\"k\")) }\nstruct In { n int }\nstruct Out { In\n m int @Name(\"m\") }\n"
	f, file, err := parser.ParseFile("attrs.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &Info{}
	pkg, err := libraryConfig().Check("p", file, f, info)
	if err != nil {
		t.Fatal(err)
	}

	lookup := func(name string) *Named { return pkg.Scope().Lookup(name).Type().(*Named) }
	s, e, out := lookup("S"), lookup("E"), lookup("Out")
	en := e.Underlying().(*Enum)
	name, omit, rng := lookup("Name"), lookup("Omit"), lookup("Range")
	for _, tt := range []struct {
		what  string
		found func() (Attribute, bool)
		value bool
	}{
		{"S", func() (Attribute, bool) { return info.TypeAttr(s, omit) }, false},
		{"S.a", func() (Attribute, bool) { return info.FieldAttr(s, 0, name) }, true},
		{"E.A", func() (Attribute, bool) { return info.VariantAttr(en.Variant(0), rng) }, true},
		{"E.B.n", func() (Attribute, bool) { return info.VariantFieldAttr(en.Variant(1), 0, name) }, true},
		{"Out.m", func() (Attribute, bool) { return info.FieldAttr(out, 0, name) }, true}, // an embedded field is no field of its own, so m is the first
	} {
		a, ok := tt.found()
		if !ok || (a.Value != nil) != tt.value {
			t.Errorf("%s: attribute found %v with a value %v, want found with a value %v", tt.what, ok, a.Value != nil, tt.value)
		}
	}

	if _, ok := info.FieldAttr(s, 1, name); ok || len(info.Attrs) != 5 {
		t.Errorf("S.b has an attribute, or %d declarations have some, want none and 5", len(info.Attrs))
	}
}

// TestAttrPackages checks that a declaration's attributes are its own
// when two packages declare a field at the same offset of their files,
// as each package's positions count from its own first file: the field
// of the program reads none, and the library's reads its own, also
// through an instance of the library's generic type. Another package's
// constant is a value, converted or given to an attribute.
func TestAttrPackages(t *testing.T) {
	libHead, progHead := "package lib\n", "package main\nimport \"lib\"\n"
	pad := "//" + strings.Repeat(" ", len(progHead)-len(libHead)-3) + "\n" // so that T's X and U's Y begin at one offset
	lib := libHead + pad + "struct T impl Reflect { X int @Key(\"x\"); W int }\ntype Key string impl Attribute\nstruct G<A> impl Reflect { V A @Key(\"v\") }\nconst Base = \"base\"\n"
	prog := progHead + "struct U impl Reflect { Y int; Z int }\nstruct K { k int @lib.Key(lib.Base) }\nfunc f() string { return string(lib.Base) }\n" // another package's constant is a value, converted or given to an attribute
	info := &Info{}
	imp := &sourceImporter{srcs: map[string]string{"lib": lib}, loaded: map[string]*Package{}, info: info}
	imp.conf.Importer = imp
	f, file, err := parser.ParseFile("main.snello", []byte(prog))
	if err != nil {
		t.Fatal(err)
	}

	pkg, err := imp.conf.Check("", file, f, info)
	if err != nil {
		t.Fatal(err)
	}

	l := imp.loaded["lib"]
	key := l.Scope().Lookup("Key").Type()
	u := pkg.Scope().Lookup("U").Type().(*Named)
	tt := l.Scope().Lookup("T").Type().(*Named)
	g := Instantiate(l.Scope().Lookup("G").Type().(*Named), []Type{Typ[Int]})
	if u.Underlying().(*Struct).Field(0).Pos() != tt.Underlying().(*Struct).Field(0).Pos() {
		t.Fatalf("U.Y and lib.T.X are at different offsets: the test needs them at one")
	}

	if _, ok := info.FieldAttr(u, 0, key); ok {
		t.Errorf("main.U.Y reads lib.T.X's attribute: a declaration's attributes are its own")
	}

	if _, ok := info.FieldAttr(tt, 0, key); !ok {
		t.Errorf("lib.T.X: no attribute Key, want its own")
	}

	if _, ok := info.FieldAttr(g, 0, key); !ok {
		t.Errorf("lib.G<int>.V: no attribute Key, want its declaration's")
	}

	k := pkg.Scope().Lookup("K").Type().(*Named)
	if _, ok := info.FieldAttr(k, 0, key); !ok {
		t.Errorf("K.k: no attribute lib.Key(lib.Base), want it")
	}
}

// TestAttrUnexported checks that an attribute of another package's type
// is held to what the package exports: an unexported type and an
// undefined one are each refused once, by name.
func TestAttrUnexported(t *testing.T) {
	info := &Info{}
	imp := &sourceImporter{srcs: map[string]string{"lib": "package lib\ntype key string impl Attribute\nstruct hidden impl Attribute {}\n"}, loaded: map[string]*Package{}, info: info}
	imp.conf.Importer = imp
	f, file, err := parser.ParseFile("main.snello", []byte("package main\nimport \"lib\"\nstruct S { a int @lib.key(\"x\"); b int @lib.hidden; c int @lib.Nope }\n"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = imp.conf.Check("", file, f, info)
	errs, _ := err.(scanner.ErrorList)
	want := []string{"name key not exported by package lib", "name hidden not exported by package lib", "undefined: lib.Nope"}
	if len(errs) != len(want) {
		t.Fatalf("got %d errors %v, want %d", len(errs), err, len(want))
	}

	for i, w := range want {
		if !strings.Contains(errs[i].Error(), w) {
			t.Errorf("error %d: got %q, want %q", i, errs[i].Error(), w)
		}
	}
}
