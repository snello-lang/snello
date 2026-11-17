// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/types"
)

// described is a program with a value of every kind a debug build
// describes, a closure that copies what it captures, and one that lends
// it.
const described = `package main

import "fmt"

struct Point {
    x int
    y int
}

enum Shape {
    Circle(int)
    Rect(int, int)
    Empty
}

func sum(n int) int {
    total := 0
    for i := 0; i < n; i++ {
        total += i
    }

    return total
}

func main() {
    p := Point{x: 3, y: 4}
    name := "snello"
    var o int? = Some(5)
    xs := []int{1, 2, 3}
    head := xs.First()
    sh := Rect(2, 3)
    var r Result<int, string> = Ok(4)
    a := [2]int{1, 2}
    m := map[int]string{}
    t := (1, "one")
    base := 10
    copied := func() int { return base }
    count := 0
    lent := func(k int) { count += k }
    lent(copied())
    one, word := t
    fmt.Println(p.x, name, o, xs, sh, r, a[0], len(m), one, word, sum(p.y), count, head.IsSome())
}
`

// TestDebugInformation holds a debug build's module to what a debugger
// reads: an unoptimized unit with every variable described, the
// program's own functions left alone while the rest is optimized, each
// variable and parameter declared at its slot, the blocks, the
// backend's helpers marked as its own, each type under the C backend's
// names with its members, and a closure's captures through its box.
func TestDebugInformation(t *testing.T) {
	out, err := emit(t, "described", described, Config{Debug: true})
	if err != nil {
		t.Fatal(err)
	}

	module := string(out)
	for _, want := range []string{
		"emissionKind: FullDebug", "isOptimized: false",
		"@sn__sum(i64 %a0) optnone noinline #0", "@sn_main() optnone noinline #0",
		`!DILocalVariable(name: "n", arg: 1,`, `!DILocalVariable(name: "total",`,
		"call void @llvm.dbg.declare(metadata ptr ", "declare void @llvm.dbg.declare(metadata, metadata, metadata)",
		"!DILexicalBlock(", "flags: DIFlagArtificial",
		`!DISubroutineType(types: !{!`, // sum's result and parameter
		`name: "Point", size: 128`, `name: "x",`, `name: "y",`,
		`name: "sn_tag_Shape"`, `!DIEnumerator(name: "sn_case_5_Shape_Rect", value: 1`,
		`name: "sn_opt_int"`, `name: "has",`, `name: "sn_res_int_string"`, `name: "ok",`, `name: "err",`,
		`name: "sn_string"`, `name: "sn_slice_int"`, `name: "ptr",`, `name: "cap",`,
		`name: "at",`, `!DISubrange(count: 2)`, `name: "sn_map"`, `name: "slots",`, `name: "e1",`,
		`name: "fn",`, `name: "box",`,
		`name: "sn_int"`, `name: "int64_t"`,
		`name: "sn_opt_ref_int"`, "tag: DW_TAG_reference_type", // an option of a borrow, the address alone, shown as what it lends
		"!DIExpression(DW_OP_deref, DW_OP_plus_uconst, 0)",                           // a copy in the box
		"!DIExpression(DW_OP_deref, DW_OP_deref, DW_OP_plus_uconst, 0, DW_OP_deref)", // a variable lent, through the environment the box points to
	} {
		if !strings.Contains(module, want) {
			t.Errorf("the debug module lacks %q", want)
		}
	}
}

// TestReleaseNotDescribed holds a release build's module to line tables
// alone, as before: no variable, no type, no function left unoptimized.
func TestReleaseNotDescribed(t *testing.T) {
	out, err := emit(t, "described", described, Config{})
	if err != nil {
		t.Fatal(err)
	}

	module := string(out)
	if !strings.Contains(module, "emissionKind: LineTablesOnly") {
		t.Error("the release module's unit is not line tables only")
	}

	for _, unwanted := range []string{"llvm.dbg.declare", "DILocalVariable", "optnone", "DIFlagArtificial", "DICompositeType", "DILexicalBlock("} {
		if strings.Contains(module, unwanted) {
			t.Errorf("the release module has %q", unwanted)
		}
	}
}

// TestLayout holds the offsets of a record's members to the IR's
// layout: each at its alignment after the last.
func TestLayout(t *testing.T) {
	got := layout([]int64{1, 8, 4, 1, 8}, []int64{1, 8, 4, 1, 8})
	if want := []int64{0, 8, 16, 20, 24}; !slices.Equal(got, want) {
		t.Errorf("layout = %v, want %v", got, want)
	}
}

// TestDebugTypesMatchC builds one program through both backends in a
// debug build and reads each record's members from both objects'
// debugging information with Go's reader: each is alike in name and
// offset, so that a variable prints the same through either backend.
func TestDebugTypesMatchC(t *testing.T) {
	if _, err := exec.LookPath(Compiler()); err != nil {
		t.Skip("no clang")
	}

	f, file, err := parser.ParseFile("described.snello", []byte(described))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	imp := importer.NewImporter(info)
	if _, err := (&types.Config{Importer: imp}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	module, err := Emit(Config{Debug: true}, file, f, info, libraries(imp)...)
	if err != nil {
		t.Fatal(err)
	}

	source, err := c.Emit(file, f, info, libraries(imp)...)
	if err != nil {
		t.Fatal(err)
	}

	irDir, cDir := t.TempDir(), t.TempDir()
	if _, out, err := Build(irDir, "described", module, true); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if _, out, err := c.Build(cDir, "described", source, true); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	records := []string{"Point", "Shape", "sn_string", "sn_opt_int", "sn_res_int_string", "sn_slice_int", "sn_map"}
	ir := objectMembers(t, filepath.Join(irDir, "described.ll.o"), records)
	cs := objectMembers(t, filepath.Join(cDir, "described.snello.o"), records)
	for _, name := range records {
		if len(ir[name]) == 0 || !slices.Equal(ir[name], cs[name]) {
			t.Errorf("%s: the LLVM backend's members %v, the C backend's %v", name, ir[name], cs[name])
		}
	}
}

// objectMembers reads the members of each record named from the
// debugging information of the object at path, as "name@offset", the
// record reached through a name of it as a C typedef names one.
func objectMembers(t *testing.T, path string, names []string) map[string][]string {
	t.Helper()
	var d *dwarf.Data
	var err error
	if goruntime.GOOS == "darwin" {
		var f *macho.File
		if f, err = macho.Open(path); err == nil {
			defer f.Close()
			d, err = f.DWARF()
		}
	} else {
		var f *elf.File
		if f, err = elf.Open(path); err == nil {
			defer f.Close()
			d, err = f.DWARF()
		}
	}

	if err != nil {
		t.Fatal(err)
	}

	wanted := make(map[string]bool, len(names))
	for _, n := range names {
		wanted[n] = true
	}

	out := make(map[string][]string)
	r := d.Reader()
	for {
		e, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}

		if e == nil {
			break
		}

		name, _ := e.Val(dwarf.AttrName).(string)
		if !wanted[name] || out[name] != nil {
			continue
		}

		if e.Tag == dwarf.TagTypedef || e.Tag == dwarf.TagStructType {
			if members := recordMembers(t, d, e); len(members) > 0 {
				out[name] = members
			}
		}
	}

	return out
}

// recordMembers reads the members of the record e is, or names.
func recordMembers(t *testing.T, d *dwarf.Data, e *dwarf.Entry) []string {
	t.Helper()
	typ, err := d.Type(e.Offset)
	if err != nil {
		t.Fatal(err)
	}

	for {
		td, ok := typ.(*dwarf.TypedefType)
		if !ok {
			break
		}

		typ = td.Type
	}

	st, ok := typ.(*dwarf.StructType)
	if !ok || st.Incomplete {
		return nil
	}

	var out []string
	for _, f := range st.Field {
		out = append(out, f.Name+"@"+strconv.FormatInt(f.ByteOffset, 10))
	}

	return out
}
