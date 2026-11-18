// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package natvis

import (
	"debug/elf"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// typesSource declares the types a Snello program's C is made of, as the
// backends name them and lay them out: a string, a map, a slice, an
// option of a value, an option of a pointer as the LLVM backend lays it
// out, the pointer alone, where the C backend keeps has beside it, a
// result, and an enum with a case of two values, one of one and one of
// none; and a struct of no kind the debugger shows otherwise.
const typesSource = `typedef struct { const unsigned char *ptr; long long len; } sn_string;
typedef struct { void *slots; long long count; } sn_map;
typedef sn_map sn_map_string_int;
struct sn_slice_int { long long *ptr; long long len; long long cap; };
struct sn_opt_int { _Bool has; long long val; };
struct sn_opt_ptr { long long *val; };
struct sn_res_int_err { _Bool ok; union { long long val; sn_string err; }; };
struct Shape {
	enum sn_tag_Shape { sn_case_5_Shape_Circle, sn_case_5_Shape_Rect, sn_case_5_Shape_Empty } tag;
	union {
		struct { long long f0; } Circle;
		struct { long long f0; long long f1; } Rect;
	};
};
struct Point { long long x; long long y; };
sn_string s; sn_map_string_int m; struct sn_slice_int xs; struct sn_opt_int o; struct sn_opt_ptr p;
struct sn_res_int_err r; struct Shape shape; struct Point point;
`

// referenceSource declares an option of a borrow's value as the LLVM
// backend describes it, a struct whose only member is a reference.
const referenceSource = `struct sn_opt_ref { long long &val; };
long long x;
sn_opt_ref r{x};
`

// TestFromDWARF writes the visualizers of a program's types from its
// debugging information: the runtime's string and map, each slice,
// option, result and enum the program describes, and none for a struct
// of no such kind; the file is well-formed XML.
func TestFromDWARF(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("no clang")
	}

	obj := compile(t, "types.c", typesSource)

	file := visualizers(t, obj)

	if err := xml.Unmarshal(file, new(struct{})); err != nil {
		t.Fatalf("not well-formed: %v\n%s", err, file)
	}

	text := string(file)
	for _, want := range []string{
		`<Type Name="sn_string">`, `{ptr,[len]s8}`,
		`<Type Name="sn_map">`, `len={count}`,
		`<Type Name="sn_slice_int">`, `<ValuePointer>ptr</ValuePointer>`,
		`<Type Name="sn_opt_int">`, `Condition="has == 0">None<`, `Some({val})`,
		`<Type Name="sn_opt_ptr">`, `Condition="val == 0">None<`,
		`<Type Name="sn_res_int_err">`, `Condition="ok != 0">Ok({val})<`, `Err({err})`,
		`<Type Name="Shape">`, `Condition="tag == 1">Rect({Rect.f0}, {Rect.f1})<`,
		`Condition="tag == 0">Circle({Circle.f0})<`, `Condition="tag == 2">Empty<`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the file has no %s:\n%s", want, text)
		}
	}

	if strings.Contains(text, `"Point"`) || strings.Count(text, `<Type Name="sn_slice_int">`) != 1 {
		t.Errorf("a struct of no kind shown, or a type twice:\n%s", text)
	}
}

// TestFromDWARFReference shows an option of a borrow's value as None by
// the reference's address, which the reference's value would not be.
func TestFromDWARFReference(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("no clang")
	}

	text := string(visualizers(t, compile(t, "ref.cpp", referenceSource)))
	if !strings.Contains(text, `<Type Name="sn_opt_ref">`) || !strings.Contains(text, `Condition="&amp;val == 0">None<`) {
		t.Errorf("an option of a reference is not None by its address:\n%s", text)
	}
}

// compile compiles the source, named name, into an object with DWARF.
func compile(t *testing.T, name, source string) string {
	t.Helper()
	dir := t.TempDir()
	src, obj := filepath.Join(dir, name), filepath.Join(dir, name+".o")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command("clang", "--target=x86_64-linux-gnu", "-g", "-gdwarf-4", "-c", "-o", obj, src).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	return obj
}

// visualizers returns the natvis file of the object's DWARF.
func visualizers(t *testing.T, obj string) []byte {
	t.Helper()
	f, err := elf.Open(obj)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	d, err := f.DWARF()
	if err != nil {
		t.Fatal(err)
	}

	file, err := FromDWARF(d)
	if err != nil {
		t.Fatal(err)
	}

	return file
}

// TestCaseOf reads the case an enumerator names, by a length in
// letters, and an enumerator of another form, or of a length past its
// end, as itself.
func TestCaseOf(t *testing.T) {
	for in, want := range map[string]string{
		"sn_case_5_Shape_Rect":            "Rect",
		"sn_case_10_sn_io_Kind_NotExist":  "NotExist",
		"sn_case_5_Shape_Rect_With_Parts": "Rect_With_Parts",
		"sn_case_5_Größe_Klein":           "Klein", // the length counts letters, not bytes
		"sn_case_2_ÖÖ_Klein":              "Klein",
		"sn_case_9_Shape_Rect":            "sn_case_9_Shape_Rect",
		"sn_case_x_Shape":                 "sn_case_x_Shape",
		"other":                           "other",
	} {
		if got := caseOf(in); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

// TestEscape writes XML's own characters as their entities.
func TestEscape(t *testing.T) {
	if got := escape(`a < b && "c" > d`); got != "a &lt; b &amp;&amp; &quot;c&quot; &gt; d" {
		t.Errorf("got %q", got)
	}
}
