// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
)

func TestIsExample(t *testing.T) {
	for name, want := range map[string]bool{
		"Example": true, "ExampleArea": true, "Example_second": true, "ExampleArea_grow": true,
		"Examples": false, "Exampleish": false, "TestArea": false, "example": false,
	} {
		if got := IsExample(name); got != want {
			t.Errorf("IsExample(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestExampleTarget(t *testing.T) {
	names := map[string]bool{"O_RDONLY": true, "Point": true, "Area": true}
	members := map[string]map[string]bool{"Point": {"Moved": true, "Parse": true}}
	declared := func(n string) bool { return names[n] }
	member := func(owner, n string) bool { return members[owner][n] }
	for name, want := range map[string][3]string{ // the target, the suffix, and "ok" or ""
		"Example":                {"", "", "ok"},
		"Example_second":         {"", "second", "ok"},
		"ExampleArea":            {"Area", "", "ok"},
		"ExampleArea_big":        {"Area", "big", "ok"},
		"ExampleO_RDONLY":        {"O_RDONLY", "", "ok"},
		"ExamplePoint_Moved":     {"Point.Moved", "", "ok"},
		"ExamplePoint_Moved_far": {"Point.Moved", "far", "ok"},
		"ExampleAreaa":           {"", "", ""},
		"ExamplePoint_Nope":      {"", "", ""},
		"ExamplePoint_Moved_Far": {"", "", ""},
	} {
		target, suffix, ok := ExampleTarget(name, declared, member)
		if target != want[0] || suffix != want[1] || ok != (want[2] == "ok") {
			t.Errorf("ExampleTarget(%q) = %q, %q, %v, want %v", name, target, suffix, ok, want)
		}
	}
}

// TestExamplesOnPage shows a name's example on its page: its code as
// written, without the comment that says what it prints, and its
// output; a package's with -all.
func TestExamplesOnPage(t *testing.T) {
	src := `package shapes_test

import ("fmt"; "shapes")

func Example() {
	fmt.Println("shapes")
	// Output: shapes
}

func ExampleArea_square() {
	a := shapes.Area(2)
	fmt.Println(a)
	// Output:
	// 4
}
`
	file := token.NewFile("shapes_test.snello", len(src))
	f, err := parser.ParseComments(file, []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	p := loadShapes(t)
	p.Examples = map[string][]*Example{}
	p.addExamples([]TestFile{{f, file, []byte(src)}})
	var b strings.Builder
	p.WriteName(&b, "Area")
	want := `func Area(s ref Shape) int
    Area is the area of s, see Shape.

Example (square):
    a := shapes.Area(2)
    fmt.Println(a)

    Output:
    4
`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}

	b.Reset()
	p.WritePackage(&b, true)
	if !strings.Contains(b.String(), "Example:\n    fmt.Println(\"shapes\")\n\n    Output:\n    shapes\n") {
		t.Errorf("the package's example is missing:\n%s", b.String())
	}
}
