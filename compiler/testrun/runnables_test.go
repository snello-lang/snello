// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testrun

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/internal/testproc"
)

// onlySrc and onlyTest are a package whose tests' names hold each
// other's, which a pattern that names a test whole tells apart.
const onlySrc = `package p

func Two() int { return 2 }
`

const onlyTest = `package p

import ("expect"; "testing")

func TestArea() { expect.Equal(ref Two(), ref 2) }

func TestAreaBig() { expect.Equal(ref Two(), ref 2) }

func TestOther() { expect.Equal(ref Two(), ref 2) }

func BenchmarkTwo() {
	for testing.Loop() {
		_ = Two()
	}
}
`

// TestFileRunnables finds what a file declares that snello runs, in the
// order written: a test file's tests, benchmarks and the examples with
// an output comment, and a program's main, whatever its package's name;
// a method, a function of a type, a helper, a test outside a test file
// and an example with no output comment are not.
func TestFileRunnables(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"p_test.snello", "package p\n\nfunc TestA() {}\n\nfunc BenchmarkB() {}\n\nfunc helper() {}\n\nfunc Testlower() {}\n\nfunc (r ref R) TestM() {}\n\n// ExampleTwo prints.\nfunc ExampleTwo() {\n\t// Output:\n\t// 2\n}\n\nfunc ExampleQuiet() {}\n", "test TestA, benchmark BenchmarkB, example ExampleTwo"},
		{"main.snello", "package main\n\nfunc helper() {}\n\nfunc main() {}\n", "main main"},
		{"tree.snello", "package tree\n\nfunc main() {}\n\nfunc TestA() {}\n", "main main"},
		{"lib.snello", "package lib\n\nfunc Main() {}\n\nfunc TestA() {}\n", ""},
		{"main_test.snello", "package main\n\nfunc main() {}\n\nfunc TestA() {}\n", "test TestA"},
	}

	for _, tt := range tests {
		file := token.NewFile(tt.name, len(tt.src))
		f, err := parser.ParseComments(file, []byte(tt.src))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}

		var got []string
		for _, r := range FileRunnables(tt.name, f) {
			got = append(got, r.Kind+" "+r.Decl.Name.Name)
		}

		if strings.Join(got, ", ") != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, strings.Join(got, ", "), tt.want)
		}
	}
}

// TestOnly writes the pattern that picks one test, or several, whole.
func TestOnly(t *testing.T) {
	if got := Only("TestArea"); got != "^TestArea$" {
		t.Errorf("one: %q", got)
	}

	if got := Only("TestA", "TestB"); got != "^(TestA|TestB)$" {
		t.Errorf("several: %q", got)
	}
}

// TestOnlyRuns runs the tests a pattern picks, through the test program:
// a pattern that names a test whole runs it and not the tests whose
// names hold its own, a group runs each test it names, ^$ runs none and
// leaves the benchmark -bench names, and a pattern with no anchors
// still runs every test whose name holds it; a pattern that is none of
// these, anchored at one end or holding another character a regular
// expression reads, ends the run before any test, with status 2.
func TestOnlyRuns(t *testing.T) {
	root := writeTree(t, map[string]string{
		"snello.mod":      "snello 0.1.0\n",
		"p/p.snello":      onlySrc,
		"p/p_test.snello": onlyTest,
	})

	dir := filepath.Join(root, "p")
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"-run", Only("TestArea")}, "TestArea"},
		{[]string{"-run", Only("TestArea", "TestOther")}, "TestArea TestOther"},
		{[]string{"-run", "Area"}, "TestArea TestAreaBig"},
		{[]string{"-run", "^$", "-bench", Only("BenchmarkTwo")}, "BenchmarkTwo"},
	}

	for _, pattern := range []string{"^TestArea", "TestArea$", "^TestA|TestB$", "^(TestA|)$", "Test.*"} {
		var out bytes.Buffer
		if code, err := Run(dir, "llvm", []string{"-run", pattern}, &out, &out, nil, testproc.Command); err != nil || code != 2 || !strings.Contains(out.String(), "invalid pattern") {
			t.Errorf("-run %s: exited %d, %v\n%s", pattern, code, err, out.String())
		}
	}

	for _, tt := range tests {
		var out bytes.Buffer
		if code, err := Run(dir, "llvm", append([]string{"-v"}, tt.args...), &out, &out, nil, testproc.Command); err != nil || code != 0 {
			t.Fatalf("%v: exited %d, %v\n%s", tt.args, code, err, out.String())
		}

		var ran []string
		for _, line := range strings.Split(out.String(), "\n") {
			if name, ok := strings.CutPrefix(line, "--- PASS: "); ok {
				ran = append(ran, strings.Fields(name)[0])
			} else if strings.HasPrefix(line, "Benchmark") {
				ran = append(ran, strings.Fields(line)[0])
			}
		}

		if got := strings.Join(ran, " "); got != tt.want {
			t.Errorf("%v ran %q, want %q\n%s", tt.args, got, tt.want, out.String())
		}
	}
}
