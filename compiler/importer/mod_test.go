// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/types"
)

// TestModFile reads module files: what a require line gives, an empty
// file marking the root, and the message for each kind of bad line.
func TestModFile(t *testing.T) {
	tests := []struct {
		src      string
		requires []Require
		err      string
	}{
		{"", nil, ""},
		{"// the app\n\nrequire dep ../dep // a library beside it\nrequire other /abs/other\n", []Require{{"dep", filepath.Join("/m", "..", "dep")}, {"other", "/abs/other"}}, ""},
		{"module example.com/app\n", nil, "snello.mod:1: a module has no name: the file marks the root, and an import names a directory under it"},
		{"require dep\n", nil, "snello.mod:1: require needs a name and a directory"},
		{"require strings ../s\n", nil, "snello.mod:1: strings is a standard package"},
		{"require net/http ../h\n", nil, `snello.mod:1: a required name is one element, and "net/http" has a /: the rest of a path names a directory under the dependency`},
		{"require dep/ ../dep\n", nil, `snello.mod:1: malformed import path "dep/": empty path element`},
		{"require dep ../a\nrequire dep ../b\n", nil, "snello.mod:2: a second require of dep"},
		{"exclude dep\n", nil, `snello.mod:1: unknown directive "exclude"`},
		{"link m\nlink sqlite3\nlink m\n", nil, ""},
		{"link\n", nil, "snello.mod:1: link needs a library name"},
		{"link -lm\n", nil, "snello.mod:1: link names a library the system's linker finds, as -l does, not a flag or a path"},
		{"link /usr/lib/libm.a\n", nil, "snello.mod:1: link names a library the system's linker finds, as -l does, not a flag or a path"},
	}
	for _, tt := range tests {
		m, err := ParseModFile(filepath.Join("/m", ModFile), []byte(tt.src))
		if tt.err != "" {
			if err == nil || !strings.HasSuffix(err.Error(), tt.err) {
				t.Errorf("%q: got %v, want an error ending in %q", tt.src, err, tt.err)
			}

			continue
		}

		if err != nil {
			t.Errorf("%q: %v", tt.src, err)
			continue
		}

		if m.Dir != "/m" || len(m.Requires) != len(tt.requires) {
			t.Errorf("%q: got %+v, want dir /m and %d requires", tt.src, m, len(tt.requires))
			continue
		}

		if strings.HasPrefix(tt.src, "link m") && (len(m.Links) != 2 || m.Links[0] != "m" || m.Links[1] != "sqlite3") {
			t.Errorf("%q: links %v, want m and sqlite3 once each", tt.src, m.Links)
		}

		for i, r := range tt.requires {
			if m.Requires[i] != r {
				t.Errorf("%q: require %d = %+v, want %+v", tt.src, i, m.Requires[i], r)
			}
		}
	}
}

// TestFindModule finds the nearest module file above a directory, none
// where there is none, and refuses a module whose directory would
// shadow a standard package.
func TestFindModule(t *testing.T) {
	root := writeTree(t, map[string]string{
		"snello.mod":           "",
		"deep/snello.mod":      "// an inner module\n",
		"deep/pkg/x.snello":    "package pkg\n",
		"shallow/pkg/x.snello": "package pkg\n",
	})
	tests := []struct{ dir, root string }{
		{".", root},
		{"shallow/pkg", root},
		{"deep", filepath.Join(root, "deep")},
		{"deep/pkg", filepath.Join(root, "deep")},
	}
	for _, tt := range tests {
		m, err := FindModule(filepath.Join(root, tt.dir))
		if err != nil || m == nil || m.Dir != tt.root {
			t.Errorf("%s: got %+v, %v, want the module at %s", tt.dir, m, err, tt.root)
		}
	}

	if m, err := FindModule(t.TempDir()); m != nil || err != nil {
		t.Errorf("a directory with no module file: got %+v, %v, want none", m, err)
	}

	bad := writeTree(t, map[string]string{"snello.mod": "module app\n", "pkg/x.snello": "package pkg\n"})
	if m, err := FindModule(filepath.Join(bad, "pkg")); m != nil || err == nil || !strings.Contains(err.Error(), "a module has no name") {
		t.Errorf("a module file that does not read: got %+v, %v, want its error", m, err)
	}

	shadow := writeTree(t, map[string]string{"snello.mod": "", "strings/s.snello": "package strings\n", "docs/notes.txt": ""})
	if m, err := FindModule(shadow); m != nil || err == nil || !strings.Contains(err.Error(), "the directory strings shadows the standard package strings; rename it") {
		t.Errorf("a directory shadowing a standard package: got %+v, %v, want its error", m, err)
	}
}

// TestLookup resolves an import path to a directory under the module's
// root or under a require line, and refuses a path both answer.
func TestLookup(t *testing.T) {
	root := writeTree(t, map[string]string{"shapes/s.snello": "package shapes\n", "a/b/x.snello": "package b\n", "both/x.snello": "package both\n", "note.txt": ""})
	m := &Module{Dir: root, Requires: []Require{{"dep", "/d"}, {"both", "/b"}}}
	tests := []struct {
		path     string
		dir      string
		required bool // through a require line
		err      string
	}{
		{"shapes", filepath.Join(root, "shapes"), false, ""},
		{"a/b", filepath.Join(root, "a", "b"), false, ""},
		{"a", filepath.Join(root, "a"), false, ""},
		{"dep", "/d", true, ""},
		{"dep/greet", "/d/greet", true, ""},
		{"deposit", "", false, ""},
		{"nosuch", "", false, ""},
		{"note.txt", "", false, ""},
		{"Shapes", "", false, ""}, // spelled as the directory is, or not at all, on every file system
		{"both", "", false, `import "both" is both a directory of the module and under the required both`},
	}
	for _, tt := range tests {
		dir, req, err := m.Lookup(tt.path)
		if tt.err != "" {
			if err == nil || err.Error() != tt.err {
				t.Errorf("%s: got %v, want %q", tt.path, err, tt.err)
			}

			continue
		}

		if err != nil || dir != tt.dir || (req != nil) != tt.required {
			t.Errorf("%s: got %q, required %v, %v, want %q, required %v", tt.path, dir, req != nil, err, tt.dir, tt.required)
		}
	}
}

// TestOwnPackages checks programs against packages of their own under
// a module: what an import gives, where a path is found, and the
// message for each kind of import that cannot be.
func TestOwnPackages(t *testing.T) {
	dep := writeTree(t, map[string]string{
		"snello.mod":              "require leaf ../leaf\nrequire third ../third\n",
		"chain/chain.snello":      "package chain\n\nimport \"third/pkg\"\n\nfunc N() int { return pkg.N() }\n",
		"util/util.snello":        "package util\n\nfunc U() int { return 7 }\n",
		"own/own.snello":          "package own\n\nimport \"util\"\n\nfunc N() int { return util.U() }\n",
		"../third/snello.mod":     "",
		"../third/pkg/pkg.snello": "package pkg\n\nfunc N() int { return 3 }\n",
		"greet/greet.snello":      "package greet\n\nimport \"leaf\"\n\nfunc Hello() string { return leaf.Word().Clone() + \", hello\" }\n",
		"../leaf/leaf.snello":     "package leaf\n\nfunc Word() string { return \"leaf\" }\n",
		"../bare/bare.snello":     "package bare\n\nfunc N() int { return 1 }\n",
	})
	root := writeTree(t, map[string]string{
		"snello.mod":                 "require dep " + dep + "\nrequire bare " + filepath.Join(dep, "..", "bare") + "\nrequire twin " + filepath.Join(dep, "..", "bare") + "\n",
		"twin/t.snello":              "package twin\n",
		"shapes/shapes.snello":       shapesSrc,
		"shapes/area.snello":         areaSrc,
		"util/util.snello":           "package util\n\nimport (\n\t\"shapes\"\n\t\"strings\"\n)\n\nfunc Describe<S: shapes.Shape>(s ref S) string { return strings.ToUpper(s.Name()) }\n\nfunc Own() string { return \"own\" }\n",
		"cycle/a/a.snello":           "package a\n\nimport \"cycle/b\"\n\nfunc A() int { return b.B() }\n",
		"cycle/b/b.snello":           "package b\n\nimport \"cycle/a\"\n\nfunc B() int { return a.A() }\n",
		"cmd/tool/main.snello":       "package main\n\nfunc main() {}\n",
		"empty/.keep":                "",
		"two/a.snello":               "package two\n\nfunc A() int { return b() }\n",
		"two/b.snello":               "package two\n\nfunc b() int { return 2 }\n",
		"mixed/a.snello":             "package mixed\n",
		"mixed/b.snello":             "package other\n",
		"broken/a.snello":            "package broken\n",
		"broken/b.snello":            "package broken\n\nfunc B() int { return c }\n",
		"bodiless/bodiless.snello":   "package bodiless\n\nfunc Sqrt(x f64) f64\n",
		"bound/bound.snello":         "package bound\n\nfunc Open(name ref string, n mut int) int\nfunc Sum(xs ref []byte) int\nfunc Fill(buf mut []byte, b byte) int\nfunc Done(h int, ok mut bool) bool\nfunc Each(a uint, b byte, c f32, d i8, e f64, f u64) u16\n",
		"bound/bound.c":              "// defined by the C side\n",
		"hdronly/h.snello":           "package hdronly\n\nfunc F() int\n",
		"hdronly/h.h":                "",
		"reserved/r.snello":          "package reserved\n\nfunc F() int\n",
		"reserved/r.c":               "",
		"reserved/reserved.snello.h": "",
		"cross1/c.snello":            "package cross1\n\nfunc Bad(m map[string]int)\n",
		"cross1/c.c":                 "",
		"cross2/c.snello":            "package cross2\n\nfunc Bad(s string) int\n",
		"cross2/c.c":                 "",
		"cross3/c.snello":            "package cross3\n\nfunc Bad() []int\n",
		"cross3/c.c":                 "",
		"cross4/c.snello":            "package cross4\n\nstruct Box { s string }\n\nfunc Bad(b *Box)\n",
		"cross4/c.c":                 "",
		"cross5/c.snello":            "package cross5\n\nstruct Box { n int }\n\nfunc (b ref Box) M() int\n",
		"cross5/c.c":                 "",
		"cross6/c.snello":            "package cross6\n\nfunc G<T>(x T)\n",
		"cross6/c.c":                 "",
		"variadic/variadic.snello":   "package variadic\n\nfunc Print(args ...ref any) {}\n",
		"local/local.snello":         "package local\n\nstruct T { l local }\n",
		"foreign/foreign.snello":     "package foreign\n\nimport \"io\"\n\nfunc (e ref io.Error) Twice() string { return e.Error() + e.Error() }\n",
		"a.b/x.snello":               "package x\n\nfunc X() int { return 1 }\n",
		"a_b/x.snello":               "package x\n\nfunc X() int { return 2 }\n",
	})
	tests := []struct {
		src  string
		want []string // one substring per expected error, in position order
	}{
		{"import \"shapes\"\nfunc f() f64 { c := shapes.Circle{Radius: 2}; r := shapes.Rect{W: 1, H: 2}; return shapes.Area(c) + shapes.Area(r) + shapes.Largest(ref c, ref r) }", nil},
		{"import \"shapes\"\nfunc f(k shapes.Kind) int { return match k { Round => 1, shapes.Square => 2 } }", nil},
		{"import \"shapes\"\nstruct Dot impl shapes.Shape {}\nfunc (d ref Dot) Name() string { return \"dot\" }\nfunc (d ref Dot) Area() f64 { return 0 }\nfunc f(d Dot) f64 { return shapes.Area(d) }", nil}, // a trait of another package implemented by the program
		{"import s \"shapes\"\nfunc f() f64 { return s.Area(s.Circle{Radius: 1}) }", nil},
		{"import \"util\"\nimport \"shapes\"\nfunc f() string { c := shapes.Circle{Radius: 1}; return util.Describe(ref c) }", nil},
		{"import \"shapes\"\nfunc f() f64 { return shapes.radius(1) }", []string{"name radius not exported by package shapes"}},
		{"import \"shapes\"\nfunc f() { shapes.Area(1) }", []string{"cannot use int as the type parameter S of shapes.Area: int does not implement Shape"}},
		{"import \"cycle/a\"\nfunc f() int { return a.A() }", []string{"import cycle: cycle/a imports cycle/b imports cycle/a"}},
		{"import \"dep/greet\"\nfunc f() string { return greet.Hello() }", nil},                                                                    // through a require line, and the required module's own require line
		{"import \"bare\"\nfunc f() int { return bare.N() }", nil},                                                                                 // a required directory with no module file of its own
		{"import \"dep/chain\"\nfunc f() int { return chain.N() }", nil},                                                                           // a required module requiring a third, which has a module file of its own
		{"import \"dep/own\"\nimport \"util\"\nfunc f() string { return util.Own() + own.N() }", []string{"mismatched types string and int in +"}}, // the dependency's util and the program's are two packages: own.N is the dependency's int
		{"import \"dep/util\"\nimport \"util\"\nfunc f() int { return util.U() }", []string{"util redeclared"}},
		{"import \"twin\"\nfunc f() {}", []string{`import "twin" is both a directory of the module and under the required twin`}},
		{"import \"cmd/tool\"\nfunc f() {}", []string{`import "cmd/tool" is a program, not an importable package`}},
		{"import \"empty\"\nfunc f() {}", []string{"for import empty"}},
		{"import \"nosuch\"\nfunc f() {}", []string{"package nosuch is not in the standard library, in the module's directories or in its require lines"}},
		{"import \"./shapes\"\nfunc f() {}", []string{`malformed import path "./shapes": a relative path; name the package by its directory under the module`}},
		{"import \"two\"\nfunc f() int { return two.A() }", nil}, // a package of two files, one calling into the other
		{"import \"mixed\"\nfunc f() {}", []string{"found packages mixed (a.snello) and other (b.snello) in mixed"}},
		{"import \"broken\"\nfunc f() {}", []string{"b.snello:3:23: undefined: c"}}, // an error in the second file names it
		{"import \"bodiless\"\nfunc f() {}", []string{"package bodiless declares Sqrt without a body and has no .c file beside it to define it"}},
		{"import \"bound\"\nfunc f() int { var n int; var ok bool; h := bound.Open(\"x\", mut n); if bound.Done(h, mut ok) { return 0 }; return n + h + bound.Sum(ref []byte{1}) + int(bound.Each(1, 2, 3, 4, 5, 6)) }", nil}, // a package's binding to C, each crossable form
		{"import \"hdronly\"\nfunc f() {}", []string{"package hdronly declares F without a body and has no .c file beside it to define it"}},
		{"import \"reserved\"\nfunc f() {}", []string{"package reserved: reserved.snello.h is the header the compiler writes for the package; rename it"}},
		{"import \"cross1\"\nfunc f() {}", []string{"cannot cross to C: map[string]int, in parameter m"}},
		{"import \"cross2\"\nfunc f() {}", []string{"cannot cross to C: string, in parameter s"}}, // an owned string: only a view crosses
		{"import \"cross3\"\nfunc f() {}", []string{"cannot cross to C: []int, in the result"}},
		{"import \"cross4\"\nfunc f() {}", []string{"cannot cross to C: *cross4.Box, in parameter b"}}, // a handle is an int the package wraps, never a pointer
		{"import \"cross5\"\nfunc f() {}", []string{"cannot bind a method to C"}},
		{"import \"cross6\"\nfunc f() {}", []string{"cannot bind a generic function to C"}},
		{"import \"variadic\"\nfunc f() {}", []string{"a variadic parameter is not supported yet"}},
		{"import \"local\"\nfunc f() {}", []string{"local is a field of the standard library's types that stay in their task; a program does not write it"}},
		{"import \"foreign\"\nfunc f() {}", []string{"cannot define new methods on non-local type io.Error"}},
		{"import \"shapes\"\nfunc (c ref shapes.Circle) Twice() f64 { return c.Radius * 2 }", []string{"cannot define new methods on non-local type shapes.Circle"}},
		{"import \"a.b\"\nimport y \"a_b\"\nfunc f() int { return x.X() + y.X() }", nil}, // two paths sharing a symbol prefix load; the backends refuse them, lower.CheckPrefixes
		{"import \"fmt\"\nfunc f() { fmt.Println(1) }", nil},                             // the standard library still answers first
	}
	for _, tt := range tests {
		f, file, err := parser.ParseFile(filepath.Join(root, "main.snello"), []byte("package main\n"+tt.src+"\n"))
		if err != nil {
			t.Fatalf("%q: parse error: %v", tt.src, err)
		}

		info := &types.Info{}
		imp := NewImporter(info)
		if imp.Module, err = FindModule(root); err != nil {
			t.Fatal(err)
		}

		conf := types.Config{Importer: imp}
		_, err = conf.Check("", file, f, info)
		var got []string
		if list, ok := err.(scanner.ErrorList); ok {
			for _, e := range list {
				got = append(got, e.Error())
			}
		}

		if len(got) == 0 && err != nil {
			got = append(got, err.Error())
		}

		if len(tt.want) > 0 && len(got) == 1 && len(tt.want) > 1 { // one error carrying several lines
			for _, w := range tt.want[1:] {
				if !strings.Contains(got[0], w) {
					t.Errorf("%q: error %q, want it to contain %q", tt.src, got[0], w)
				}
			}

			tt.want = tt.want[:1]
		}

		if len(got) != len(tt.want) {
			t.Errorf("%q: got %d errors %q, want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i, w := range tt.want {
			if !strings.Contains(got[i], w) {
				t.Errorf("%q: error %d = %q, want it to contain %q", tt.src, i, got[i], w)
			}
		}
	}
}

// shapesSrc is the first file of a package with a function, a struct,
// an enum, a trait and a generic function, for using each across the
// boundary.
const shapesSrc = `package shapes

trait Shape {
	func (s ref Self) Name() string
	func (s ref Self) Area() f64
}

struct Circle impl Shape { Radius f64 }

func (c ref Circle) Name() string { return "circle" }

func (c ref Circle) Area() f64 { return 3.0 * c.Radius * c.Radius }

struct Rect impl Shape { W f64; H f64 }

func (r ref Rect) Name() string { return "rect" }

func (r ref Rect) Area() f64 { return r.W * r.H }

`

// areaSrc is the second file of the package: an enum and the generic
// functions, over the trait and the structs the first file declares.
const areaSrc = `package shapes

enum Kind impl Copy, Clone, Eq { Round; Square }

func Area<S: Shape>(s S) f64 { return s.Area() }

func Largest<A: Shape, B: Shape>(a ref A, b ref B) f64 { return max(a.Area(), b.Area()) }

func radius(r f64) f64 { return r }
`

// writeTree writes the files of a tree under a new temporary directory
// and returns it; a path may climb out of it with .., for a directory
// beside it.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "m")
	for name, src := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return root
}
