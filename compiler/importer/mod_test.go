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

// TestModFile reads module files: what each directive gives, and the
// message for each kind of bad line.
func TestModFile(t *testing.T) {
	tests := []struct {
		src      string
		path     string
		requires []Require
		err      string
	}{
		{"module example.com/app\n", "example.com/app", nil, ""},
		{"// the app\nmodule example.com/app // its path\n\nrequire example.com/dep ../dep\nrequire example.com/other /abs/other\n", "example.com/app", []Require{{"example.com/dep", filepath.Join("/m", "..", "dep")}, {"example.com/other", "/abs/other"}}, ""},
		{"module example.com/app\nmodule example.com/b\n", "", nil, "snello.mod:2: a second module directive"},
		{"module\n", "", nil, "snello.mod:1: module needs a path"},
		{"module app\n", "", nil, `snello.mod:1: malformed module path "app": missing dot in first path element`},
		{"module example.com/app/\n", "", nil, `snello.mod:1: malformed import path "example.com/app/": empty path element`},
		{"module example.com/app\nrequire example.com/dep\n", "", nil, "snello.mod:2: require needs an import path and a directory"},
		{"module example.com/app\nrequire dep ../dep\n", "", nil, `snello.mod:2: malformed module path "dep": missing dot in first path element`},
		{"module example.com/app\nexclude example.com/dep\n", "", nil, `snello.mod:2: unknown directive "exclude"`},
		{"require example.com/dep ../dep\n", "", nil, "snello.mod: no module directive"},
		{"", "", nil, "snello.mod: no module directive"},
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

		if m.Path != tt.path || m.Dir != "/m" || len(m.Requires) != len(tt.requires) {
			t.Errorf("%q: got %+v, want path %s, dir /m and %d requires", tt.src, m, tt.path, len(tt.requires))
			continue
		}

		for i, r := range tt.requires {
			if m.Requires[i] != r {
				t.Errorf("%q: require %d = %+v, want %+v", tt.src, i, m.Requires[i], r)
			}
		}
	}
}

// TestFindModule finds the nearest module file above a directory, and
// none where there is none.
func TestFindModule(t *testing.T) {
	root := writeTree(t, map[string]string{
		"snello.mod":           "module example.com/app\n",
		"deep/snello.mod":      "module example.com/deep\n",
		"deep/pkg/x.snello":    "package pkg\n",
		"shallow/pkg/x.snello": "package pkg\n",
	})
	tests := []struct{ dir, path string }{
		{".", "example.com/app"},
		{"shallow/pkg", "example.com/app"},
		{"deep", "example.com/deep"},
		{"deep/pkg", "example.com/deep"},
	}
	for _, tt := range tests {
		m, err := FindModule(filepath.Join(root, tt.dir))
		if err != nil || m == nil || m.Path != tt.path {
			t.Errorf("%s: got %+v, %v, want module %s", tt.dir, m, err, tt.path)
		}
	}

	if m, err := FindModule(t.TempDir()); m != nil || err != nil {
		t.Errorf("a directory with no module file: got %+v, %v, want none", m, err)
	}

	bad := writeTree(t, map[string]string{"snello.mod": "module app\n", "pkg/x.snello": "package pkg\n"})
	if m, err := FindModule(filepath.Join(bad, "pkg")); m != nil || err == nil || !strings.Contains(err.Error(), `malformed module path "app"`) {
		t.Errorf("a module file that does not read: got %+v, %v, want its error", m, err)
	}
}

// TestPackageDir resolves an import path to a directory under the
// module or a require line.
func TestPackageDir(t *testing.T) {
	m := &Module{Path: "example.com/app", Dir: "/m", Requires: []Require{{"example.com/dep", "/d"}}}
	tests := []struct {
		path     string
		dir      string
		required bool // through the require line
		ok       bool
	}{
		{"example.com/app", "/m", false, true},
		{"example.com/app/shapes", "/m/shapes", false, true},
		{"example.com/app/a/b", "/m/a/b", false, true},
		{"example.com/dep", "/d", true, true},
		{"example.com/dep/greet", "/d/greet", true, true},
		{"example.com/application", "", false, false},
		{"example.com/other", "", false, false},
		{"fmt", "", false, false},
	}
	for _, tt := range tests {
		if dir, req, ok := m.PackageDir(tt.path); dir != tt.dir || ok != tt.ok || (req != nil) != tt.required {
			t.Errorf("%s: got %q, required %v, %v, want %q, required %v, %v", tt.path, dir, req != nil, ok, tt.dir, tt.required, tt.ok)
		}
	}
}

// TestOwnPackages checks programs against packages of their own under
// a module: what an import gives, where a path is found, and the
// message for each kind of import that cannot be.
func TestOwnPackages(t *testing.T) {
	dep := writeTree(t, map[string]string{
		"snello.mod":              "module example.com/dep\nrequire example.com/leaf ../leaf\nrequire example.com/third ../third\n",
		"chain/chain.snello":      "package chain\n\nimport \"example.com/third/pkg\"\n\nfunc N() int { return pkg.N() }\n",
		"../third/snello.mod":     "module example.com/third\n",
		"../third/pkg/pkg.snello": "package pkg\n\nfunc N() int { return 3 }\n",
		"greet/greet.snello":      "package greet\n\nimport \"example.com/leaf\"\n\nfunc Hello() string { return leaf.Word().Clone() + \", hello\" }\n",
		"../leaf/leaf.snello":     "package leaf\n\nfunc Word() string { return \"leaf\" }\n",
		"../bare/bare.snello":     "package bare\n\nfunc N() int { return 1 }\n",
		"../renamed/snello.mod":   "module example.com/other\n",
		"../renamed/r.snello":     "package renamed\n\nfunc N() int { return 1 }\n",
	})
	root := writeTree(t, map[string]string{
		"snello.mod":               "module example.com/app\nrequire example.com/dep " + dep + "\nrequire example.com/bare " + filepath.Join(dep, "..", "bare") + "\nrequire example.com/renamed " + filepath.Join(dep, "..", "renamed") + "\n",
		"shapes/shapes.snello":     shapesSrc,
		"shapes/area.snello":       areaSrc,
		"util/util.snello":         "package util\n\nimport (\n\t\"example.com/app/shapes\"\n\t\"strings\"\n)\n\nfunc Describe<S: shapes.Shape>(s ref S) string { return strings.ToUpper(s.Name()) }\n",
		"cycle/a/a.snello":         "package a\n\nimport \"example.com/app/cycle/b\"\n\nfunc A() int { return b.B() }\n",
		"cycle/b/b.snello":         "package b\n\nimport \"example.com/app/cycle/a\"\n\nfunc B() int { return a.A() }\n",
		"cmd/tool/main.snello":     "package main\n\nfunc main() {}\n",
		"empty/.keep":              "",
		"two/a.snello":             "package two\n\nfunc A() int { return b() }\n",
		"two/b.snello":             "package two\n\nfunc b() int { return 2 }\n",
		"mixed/a.snello":           "package mixed\n",
		"mixed/b.snello":           "package other\n",
		"broken/a.snello":          "package broken\n",
		"broken/b.snello":          "package broken\n\nfunc B() int { return c }\n",
		"bodiless/bodiless.snello": "package bodiless\n\nfunc Sqrt(x f64) f64\n",
		"variadic/variadic.snello": "package variadic\n\nfunc Print(args ...ref any) {}\n",
		"local/local.snello":       "package local\n\nstruct T { l local }\n",
		"foreign/foreign.snello":   "package foreign\n\nimport \"io\"\n\nfunc (e ref io.Error) Twice() string { return e.Error() + e.Error() }\n",
		"a.b/x.snello":             "package x\n\nfunc X() int { return 1 }\n",
		"a_b/x.snello":             "package x\n\nfunc X() int { return 2 }\n",
	})
	tests := []struct {
		src  string
		want []string // one substring per expected error, in position order
	}{
		{"import \"example.com/app/shapes\"\nfunc f() f64 { c := shapes.Circle{Radius: 2}; r := shapes.Rect{W: 1, H: 2}; return shapes.Area(c) + shapes.Area(r) + shapes.Largest(ref c, ref r) }", nil},
		{"import \"example.com/app/shapes\"\nfunc f(k shapes.Kind) int { return match k { Round => 1, shapes.Square => 2 } }", nil},
		{"import \"example.com/app/shapes\"\nstruct Dot impl shapes.Shape {}\nfunc (d ref Dot) Name() string { return \"dot\" }\nfunc (d ref Dot) Area() f64 { return 0 }\nfunc f(d Dot) f64 { return shapes.Area(d) }", nil}, // a trait of another package implemented by the program
		{"import s \"example.com/app/shapes\"\nfunc f() f64 { return s.Area(s.Circle{Radius: 1}) }", nil},
		{"import \"example.com/app/util\"\nimport \"example.com/app/shapes\"\nfunc f() string { c := shapes.Circle{Radius: 1}; return util.Describe(ref c) }", nil},
		{"import \"example.com/app/shapes\"\nfunc f() f64 { return shapes.radius(1) }", []string{"name radius not exported by package shapes"}},
		{"import \"example.com/app/shapes\"\nfunc f() { shapes.Area(1) }", []string{"cannot use int as the type parameter S of shapes.Area: int does not implement Shape"}},
		{"import \"example.com/app/cycle/a\"\nfunc f() int { return a.A() }", []string{"import cycle: example.com/app/cycle/a imports example.com/app/cycle/b imports example.com/app/cycle/a"}},
		{"import \"example.com/dep/greet\"\nfunc f() string { return greet.Hello() }", nil}, // through a require line, and the required module's own require line
		{"import \"example.com/bare\"\nfunc f() int { return bare.N() }", nil},              // a required directory with no module file of its own
		{"import \"example.com/dep/chain\"\nfunc f() int { return chain.N() }", nil},        // a required module requiring a third, which has a module file of its own
		{"import \"example.com/renamed\"\nfunc f() int { return renamed.N() }", []string{"snello.mod: module path is example.com/other, but the directory is required as example.com/renamed"}},
		{"import \"example.com/app/cmd/tool\"\nfunc f() {}", []string{`import "example.com/app/cmd/tool" is a program, not an importable package`}},
		{"import \"example.com/app/empty\"\nfunc f() {}", []string{"for import example.com/app/empty"}},
		{"import \"example.com/app/nosuch\"\nfunc f() {}", []string{"for import example.com/app/nosuch"}},
		{"import \"example.com/elsewhere\"\nfunc f() {}", []string{"package example.com/elsewhere is not in the standard library, in module example.com/app or in its require lines"}},
		{"import \"nosuch\"\nfunc f() {}", []string{"package nosuch is not in the standard library, in module example.com/app or in its require lines"}},
		{"import \"./shapes\"\nfunc f() {}", []string{`malformed import path "./shapes": a relative path; name the package by its path under the module`}},
		{"import \"example.com/app/two\"\nfunc f() int { return two.A() }", nil}, // a package of two files, one calling into the other
		{"import \"example.com/app/mixed\"\nfunc f() {}", []string{"found packages mixed (a.snello) and other (b.snello) in mixed"}},
		{"import \"example.com/app/broken\"\nfunc f() {}", []string{"b.snello:3:23: undefined: c"}}, // an error in the second file names it
		{"import \"example.com/app/bodiless\"\nfunc f() {}", []string{"package example.com/app/bodiless does not compile:", "missing function body"}},
		{"import \"example.com/app/variadic\"\nfunc f() {}", []string{"a variadic parameter is not supported yet"}},
		{"import \"example.com/app/local\"\nfunc f() {}", []string{"local is a field of the standard library's types that stay in their task; a program does not write it"}},
		{"import \"example.com/app/foreign\"\nfunc f() {}", []string{"cannot define new methods on non-local type io.Error"}},
		{"import \"example.com/app/shapes\"\nfunc (c ref shapes.Circle) Twice() f64 { return c.Radius * 2 }", []string{"cannot define new methods on non-local type shapes.Circle"}},
		{"import \"example.com/app/a.b\"\nimport y \"example.com/app/a_b\"\nfunc f() int { return x.X() + y.X() }", nil}, // two paths sharing a symbol prefix load; the backends refuse them, lower.CheckPrefixes
		{"import \"fmt\"\nfunc f() { fmt.Println(1) }", nil},                                                             // the standard library still answers first
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
