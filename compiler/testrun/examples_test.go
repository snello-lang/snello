// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testrun

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestExamplesOf reads what each example must print from its last
// comment, and leaves out one that says nothing.
func TestExamplesOf(t *testing.T) {
	src := `package geo

func ExampleArea() {
	print(1)
	// Output: 4
}

func ExampleArea_lines() {
	// a note, not the last comment
	print(1)
	// Output:
	// one
	// two
}

func ExampleArea_any() {
	// Unordered output:
	// b
	// a
}

func ExampleArea_silent() {
	print(1)
}

func Helper() {}
`
	f, err := parseWithComments("geo_test.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	got, err := examplesOf(f)
	if err != nil {
		t.Fatal(err)
	}

	want := []Example{
		{"ExampleArea", " 4\n", false, true},
		{"ExampleArea_lines", "\none\ntwo\n", false, true},
		{"ExampleArea_any", "\nb\na\n", true, true},
		{"ExampleArea_silent", "", false, false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("examples %#v, want %#v", got, want)
	}

	bad, err := parseWithComments("bad_test.snello", []byte("package geo\n\nfunc ExampleArea(n int) {}\n"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := examplesOf(bad); err == nil || err.Error() != "ExampleArea has the wrong signature: want func()" {
		t.Errorf("an example with a parameter: %v", err)
	}
}

// TestCheckExampleNames refuses an example that names nothing the
// package declares, and takes every form that names something.
func TestCheckExampleNames(t *testing.T) {
	files := map[string][]byte{"geo.snello": []byte(`package geo

const O_RDONLY = 0

struct Point { X int }

func (p ref Point) Moved(d int) Point { return Point{X: p.X + d} }

func Point.Parse(s ref string) Point? { return None }

func Area(s int) int { return s * s }
`)}
	for name, ok := range map[string]bool{
		"Example": true, "Example_second": true, "ExampleArea": true, "ExampleArea_big": true, "ExampleO_RDONLY": true,
		"ExamplePoint": true, "ExamplePoint_Moved": true, "ExamplePoint_Parse": true, "ExamplePoint_Moved_far": true,
		"ExampleAreaa": false, "ExamplePoint_Nope": false, "ExamplePoint_Moved_Far": false,
	} {
		err := checkExampleNames([]Example{{Name: name}}, "geo", files)
		if (err == nil) != ok {
			t.Errorf("%s: %v, want accepted %v", name, err, ok)
		}

		if err != nil && err.Error() != name+" names nothing in package geo" {
			t.Errorf("%s: message %q", name, err)
		}
	}
}

// TestSourceExamples lists the examples in the generated main, after
// the tests.
func TestSourceExamples(t *testing.T) {
	p := &Plan{Path: "geo", Names: []string{"TestArea"}, Examples: []Example{{"ExampleArea", "4\n", false, true}, {"ExampleAny", "b\na", true, true}, {"ExampleQuiet", "", false, false}}}
	src := string(p.Source())
	for _, want := range []string{
		`testing.Test{Name: "TestArea", F: pkg.TestArea}`,
		`[]testing.Example{testing.Example{Name: "ExampleArea", F: pkg.ExampleArea, Output: "4\n", Unordered: false}, testing.Example{Name: "ExampleAny", F: pkg.ExampleAny, Output: "b\na", Unordered: true}}`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the generated main lacks %s:\n%s", want, src)
		}
	}

	if strings.Contains(src, "ExampleQuiet") {
		t.Errorf("an example that says nothing of what it prints is listed:\n%s", src)
	}
}

// TestRunExamples runs a package's examples through both backends: one
// that prints what it must, one whose lines come in any order, one that
// prints otherwise and one that panics fail with what they printed, one
// with no output comment is not run, and -run picks among them.
func TestRunExamples(t *testing.T) {
	root := writeTree(t, map[string]string{
		"snello.mod":     "snello 0.1.0\n",
		"geo/geo.snello": "package geo\n\n// Area returns the area of a square of side s.\nfunc Area(s int) int { return s * s }\n",
		"geo/geo_test.snello": `package geo

import "fmt"

func ExampleArea() {
	fmt.Println(Area(2))
	// Output: 4
}

func ExampleArea_both() {
	fmt.Println("b")
	fmt.Println("a")
	// Unordered output:
	// a
	// b
}

func ExampleArea_wrong() {
	fmt.Println(Area(2))
	// Output: 5
}

func ExampleArea_panics() {
	panic("boom")
	// Output: 4
}

func ExampleArea_silent() {
	fmt.Println("never run")
}
`,
		"named/n.snello":      "package named\n\nfunc Area() int { return 1 }\n",
		"named/n_test.snello": "package named\n\nfunc ExampleAreaa() {}\n",
	})
	for _, backend := range []string{"llvm", "c"} {
		var out bytes.Buffer
		code, err := Run(filepath.Join(root, "geo"), backend, []string{"-v"}, &out, &out, nil)
		if err != nil {
			t.Fatalf("%s: %v", backend, err)
		}

		got := timeless(out.String())
		for _, want := range []string{
			"=== RUN   ExampleArea\n--- PASS: ExampleArea (0.00s)\n", "--- PASS: ExampleArea_both (0.00s)\n",
			"--- FAIL: ExampleArea_wrong (0.00s)\ngot:\n4\nwant:\n5\n", "--- FAIL: ExampleArea_panics (0.00s)\n    boom\n",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: output lacks %q:\n%s", backend, want, got)
			}
		}

		if code != 1 || strings.Contains(got, "never run") || strings.Contains(got, "ExampleArea_silent") {
			t.Errorf("%s: exit %d, output:\n%s", backend, code, got)
		}

		out.Reset()
		if _, err := Run(filepath.Join(root, "named"), backend, nil, &out, &out, nil); err == nil || !strings.Contains(err.Error(), "ExampleAreaa names nothing in package named") {
			t.Errorf("%s: an example with no output comment naming nothing: %v", backend, err)
		}

		out.Reset()
		code, err = Run(filepath.Join(root, "geo"), backend, []string{"-run", "ExampleArea_both"}, &out, &out, nil)
		if err != nil || code != 0 || !strings.HasPrefix(out.String(), "ok  \tgeo\t") {
			t.Errorf("%s: -run ExampleArea_both exited %d, %v, printed %q", backend, code, err, out.String())
		}
	}
}
