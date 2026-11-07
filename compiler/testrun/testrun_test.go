// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testrun

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/llvm"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/types"
)

// writeTree writes a module under a temporary directory and returns
// its root.
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

const shapesSrc = `package shapes

func Area(w int, h int) int { return w * h }

func Double(xs ref []int) []int {
	out := make([]int, 0, len(xs))
	for _, x := range xs {
		out.Push(x * 2)
	}
	return out
}
`

const shapesTest = `package shapes

import ("expect"; "fmt"; "testing")

struct Case { name string; in []int; want []int }

func TestArea() {
	expect.Equal(ref Area(2, 3), ref 6)
}

func TestDouble() {
	cases := []Case{
		Case{name: "two", in: []int{1, 2}, want: []int{2, 4}},
		Case{name: "wrong", in: []int{1}, want: []int{3}},
	}
	for _, c := range cases {
		testing.Run(ref c.name, func() {
			testing.Log(ref fmt.Sprint("in ", c.in))
			expect.EqualSlice(ref Double(ref c.in), ref c.want)
		})
	}
}

func TestSkipped() {
	testing.Skip("not on this machine")
}

func TestDir() {
	d := testing.TempDir()
	expect.True(d.Join("x").HasSuffix("/x"))
}

func TestPanics() {
	expect.PanicsWith(func() { var o int? = None; o.Expect("nothing") }, "nothing")
}

func TestParts() {
	n := 2
	limit := 3
	testing.Logf("n is %d", n)
	expect.True(n > limit)
}

func TestFailf() {
	expect.Failf("port %d is taken", 8080)
}
`

// The output of the shapes tests with -v, through either backend: the
// failing subtest fails its test, the skip and the logged line are
// shown, and the verdict is FAIL.
const shapesOut = `=== RUN   TestArea
--- PASS: TestArea (0.00s)
=== RUN   TestDir
--- PASS: TestDir (0.00s)
=== RUN   TestDouble
=== RUN   TestDouble/two
--- PASS: TestDouble/two (0.00s)
    shapes_test.snello:18: in [1 2]
=== RUN   TestDouble/wrong
--- FAIL: TestDouble/wrong (0.00s)
    shapes_test.snello:18: in [1]
    shapes_test.snello:19: expect.EqualSlice(ref Double(ref c.in), ref c.want)
        got:  [2]   ([]int, 1 element)
        want: [3]   (1 element)
        element 0: got 2 where 3 was wanted
--- FAIL: TestDouble (0.00s)
=== RUN   TestFailf
--- FAIL: TestFailf (0.00s)
    shapes_test.snello:45: expect.Failf("port %d is taken", 8080): port 8080 is taken
=== RUN   TestPanics
--- PASS: TestPanics (0.00s)
=== RUN   TestParts
--- FAIL: TestParts (0.00s)
    shapes_test.snello:40: n is 2
    shapes_test.snello:41: expect.True(n > limit) is false
        n is 2, limit is 3
=== RUN   TestSkipped
--- SKIP: TestSkipped (0.00s)
    shapes_test.snello:25: not on this machine
FAIL
`

const moreSrc = `package more

import "io"

func Parse(s ref string) Result<int, io.Error> {
	if s == "1" { return Ok(1) }
	return Err(io.NewError("parse", s.Clone(), "not a number", io.Other))
}

func Report() string { return "line one\nline two\nline three\n" }
`

const moreTest = `package more

import ("expect"; "strings"; "testing"; "time")

func TestOptions() {
	var o int? = Some(3)
	v := expect.IsSome(o)
	expect.Equal(ref v, ref 3)
	var n int? = None
	expect.IsNone(n)
	one := expect.IsOk(Parse("1"))
	expect.Equal(ref one, ref 1)
	e := expect.IsErr(Parse("x"))
	expect.Contains(ref e.Error(), "not a number")
}

func TestStrings() {
	xs := []int{1, 2, 3}
	expect.Len(ref xs, 3)
	expect.ContainsElem(ref xs, ref 2)
	expect.HasPrefix("snello", "sn")
	expect.HasSuffix("snello", "llo")
	empty := []int{}
	expect.Empty(ref empty)
	expect.EqualText(ref Report(), "line one\nline two\nline three\n")
}

func TestTextDiff() {
	expect.EqualText(ref Report(), "line one\nline 2\nline three\n")
}

func TestGolden() {
	expect.Golden("report", ref Report())
}

func TestOk() {
	v := expect.IsOk(Parse("x"))
	expect.Equal(ref v, ref 1)
}

func TestParallelA() {
	testing.Parallel()
	for i := 0; i < 20000; i++ { // asserting while B fails: the place of a call is the task's own, never A's seen by B
		expect.True(i >= 0)
	}
	time.Sleep(20 * time.Millisecond)
	testing.Log("a done")
}

func TestParallelB() {
	testing.Parallel()
	testing.Log("b done")
	for i := 0; i < 20000; i++ {
		expect.True(i >= 0)
	}
	expect.True(1 > 2)
}

func TestShort() {
	if testing.Short() {
		testing.Skip("short")
	}
}

func BenchmarkRepeat() {
	testing.ReportAllocs()
	for testing.Loop() {
		_ = strings.Repeat("ab", 16)
	}
}

func TestParallelTwice() {
	testing.Parallel()
	testing.Parallel()
}

func BenchmarkFails() {
	for testing.Loop() {
		expect.True(2 > 3)
	}
}
`

// TestMore runs the second fixture: the option, result, containment,
// text and golden assertions, parallel tests whose lines are held,
// a benchmark, and the flags, through the default backend, with the
// forms of the failures pinned.
func TestMore(t *testing.T) {
	root := writeTree(t, map[string]string{
		"snello.mod":            "",
		"more/more.snello":      moreSrc,
		"more/more_test.snello": moreTest,
		"more/testdata/.keep":   "",
	})
	dir := filepath.Join(root, "more")
	run := func(args ...string) (string, int) {
		var out bytes.Buffer
		code, err := Run(dir, "llvm", args, &out, &out)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}

		return timeless(out.String()), code
	}

	// The golden file is missing, then written by -update, then read.
	if out, code := run("-run", "Golden"); code != 1 || !strings.Contains(out, "more_test.snello:"+lineOf(moreTest, "expect.Golden(")+": expect.Golden(\"report\", ref Report()): open testdata/report.golden: no such file or directory; snello test -update writes the file") {
		t.Errorf("golden missing: exited %d\n%s", code, out)
	}

	if out, code := run("-update", "-run", "Golden"); code != 0 {
		t.Errorf("golden -update: exited %d\n%s", code, out)
	}

	if golden, err := os.ReadFile(filepath.Join(dir, "testdata", "report.golden")); err != nil || string(golden) != "line one\nline two\nline three\n" {
		t.Errorf("golden file: %q, %v", golden, err)
	}

	out, code := run("-v")
	if code != 1 {
		t.Errorf("-v exited %d", code)
	}

	for _, want := range []string{
		"--- PASS: TestGolden (0.00s)\n",
		"--- PASS: TestOptions (0.00s)\n",
		"--- PASS: TestStrings (0.00s)\n",
		"--- FAIL: TestOk (0.00s)\n    more_test.snello:" + lineOf(moreTest, "v := expect.IsOk(Parse(\"x\"))") + ": expect.IsOk(Parse(\"x\")): is Err, Ok was wanted\n        error: parse x: not a number\n",
		"--- FAIL: TestTextDiff (0.00s)\n    more_test.snello:" + lineOf(moreTest, "line 2\\nline three") + ": expect.EqualText(ref Report(), \"line one\\nline 2\\nline three\\n\")\n        line 2: got \"line two\" where \"line 2\" was wanted\n",
		"--- FAIL: TestParallelB (0.00s)\n    more_test.snello:" + lineOf(moreTest, "b done") + ": b done\n    more_test.snello:" + lineOf(moreTest, "expect.True(1 > 2)") + ": expect.True(1 > 2) is false\n",
		"--- PASS: TestParallelA (0.00s)\n    more_test.snello:" + lineOf(moreTest, "a done") + ": a done\n",
		"--- FAIL: TestParallelTwice (0.00s)\n    testing.Parallel called twice\n",
		"FAIL\nFAIL\tmore\t",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("-v output lacks %q:\n%s", want, out)
		}
	}

	// The parallel tests start after every sequential one has ended,
	// and their lines are held until each ends.
	if a, b, last := strings.Index(out, "=== RUN   TestParallelA"), strings.Index(out, "=== RUN   TestParallelB"), strings.Index(out, "--- PASS: TestTextDiff"); !(a < b) || last > a && last < 0 {
		t.Errorf("parallel tests out of order:\n%s", out)
	}

	if out, code := run("-short", "-run", "Short"); code != 0 || !strings.Contains(out, "") {
		t.Errorf("-short: exited %d\n%s", code, out)
	}

	if out, _ := run("-v", "-short", "-run", "Short"); !strings.Contains(out, "--- SKIP: TestShort (0.00s)\n    more_test.snello:"+lineOf(moreTest, "testing.Skip(\"short\")")+": short\n") {
		t.Errorf("-short -v:\n%s", out)
	}

	if out, code := run("-run", "Nothing", "-bench", "Repeat"); code != 0 || !strings.Contains(out, "BenchmarkRepeat") || !strings.Contains(out, "ns/op") || !strings.Contains(out, "32 B/op        1 allocs/op") {
		t.Errorf("-bench: exited %d\n%s", code, out)
	}

	if out, code := run("-count", "2", "-run", "Options", "-v"); code != 0 || strings.Count(out, "--- PASS: TestOptions") != 2 {
		t.Errorf("-count 2: exited %d\n%s", code, out)
	}

	if out, code := run("-failfast", "-run", "T"); code != 1 || strings.Count(out, "--- FAIL") != 1 {
		t.Errorf("-failfast: exited %d, want one failure\n%s", code, out)
	}

	if out, code := run("-shuffle", "-run", "Options"); code != 0 || !strings.HasPrefix(out, "-shuffle ") {
		t.Errorf("-shuffle: exited %d\n%s", code, out)
	}

	if out, code := run("-timeout", "30", "-run", "Options"); code != 0 {
		t.Errorf("-timeout: exited %d\n%s", code, out)
	}

	// A count that is not a number ends the run before any test, as a
	// flag parser does; -run . runs every test, as -bench . does; and
	// a benchmark failing an expectation is reported as a test is.
	if out, code := run("-count", "abc", "-run", "Options"); code != 2 || !strings.Contains(out, "invalid value \"abc\" for -count") || strings.Contains(out, "=== RUN") {
		t.Errorf("-count abc: exited %d\n%s", code, out)
	}

	if out, code := run("-run", ".", "-v"); code != 1 || !strings.Contains(out, "--- PASS: TestOptions") || !strings.Contains(out, "--- PASS: TestParallelA") {
		t.Errorf("-run .: exited %d\n%s", code, out)
	}

	if out, code := run("-run", "Nothing", "-bench", "Fails"); code != 1 || !strings.Contains(out, "--- FAIL: BenchmarkFails\n    more_test.snello:"+lineOf(moreTest, "expect.True(2 > 3)")+": expect.True(2 > 3) is false\n") {
		t.Errorf("-bench Fails: exited %d\n%s", code, out)
	}
}

// TestRun runs the tests of a library package and of a main package
// through both backends, as snello test does, and checks what they
// print and how they exit; then the refusals: a test with the wrong
// signature, a library under no module, and a directory with no test
// file.
func TestRun(t *testing.T) {
	root := writeTree(t, map[string]string{
		"snello.mod":                "",
		"shapes/shapes.snello":      shapesSrc,
		"shapes/shapes_test.snello": shapesTest,
		"app/main.snello":           "package main\n\nimport \"fmt\"\n\nfunc greet(name ref string) string { return \"hello \" + name }\n\nfunc main() { fmt.Println(greet(\"world\")) }\n",
		"app/main_test.snello":      "package main\n\nimport \"expect\"\n\nfunc TestGreet() { expect.Equal(ref greet(\"x\"), \"hello x\") }\n",
		"badsig/b.snello":           "package badsig\n",
		"badsig/b_test.snello":      "package badsig\n\nfunc TestWrong(n int) {}\n",
		"notests/n.snello":          "package notests\n",
		"../loose/l.snello":         "package loose\n",
		"../loose/l_test.snello":    "package loose\n\nfunc TestL() {}\n",
	})
	for _, backend := range []string{"llvm", "c"} {
		var out bytes.Buffer
		code, err := Run(filepath.Join(root, "shapes"), backend, []string{"-v"}, &out, &out)
		if err != nil {
			t.Fatalf("%s: shapes: %v", backend, err)
		}

		got := timeless(out.String())
		if code != 1 || !strings.HasPrefix(got, shapesOut) || !strings.HasPrefix(got[len(shapesOut):], "FAIL\tshapes\t") {
			t.Errorf("%s: shapes exited %d and printed\n%s\nwant exit 1 and\n%s", backend, code, got, shapesOut)
		}

		out.Reset()
		code, err = Run(filepath.Join(root, "app"), backend, nil, &out, &out)
		if err != nil || code != 0 || !strings.HasPrefix(out.String(), "ok  \tapp\t") {
			t.Errorf("%s: app exited %d, %v, printed %q", backend, code, err, out.String())
		}

		out.Reset()
		code, err = Run(filepath.Join(root, "app"), backend, []string{"-v", "-run", "Nothing"}, &out, &out)
		if err != nil || code != 0 || strings.Contains(out.String(), "=== RUN") {
			t.Errorf("%s: app -run Nothing exited %d, %v, printed %q", backend, code, err, out.String())
		}
	}

	// The program built beside its tests carries nothing of them: no
	// test file, and no symbol of testing or expect.
	names, err := importer.SourceFiles(filepath.Join(root, "app"))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	imp, prog, err := importer.LoadProgram(names, info)
	if err != nil {
		t.Fatal(err)
	}

	var libs []lower.Library
	for _, lib := range imp.Packages() {
		libs = append(libs, lower.Library{Path: lib.Path, File: lib.File, Syntax: lib.Syntax})
	}

	for _, backend := range []string{"llvm", "c"} {
		var src []byte
		if backend == "llvm" {
			src, err = llvm.EmitFiles(llvm.Config{}, prog.Files, prog.Syntax, info, libs...)
		} else {
			src, err = c.EmitFiles(prog.Files, prog.Syntax, info, libs...)
		}

		if err != nil {
			t.Fatal(err)
		}

		if bytes.Contains(src, []byte("sn_testing_")) || bytes.Contains(src, []byte("sn_expect_")) || bytes.Contains(src, []byte("TestGreet")) {
			t.Errorf("%s: the program built beside its tests carries a symbol of them", backend)
		}
	}

	// The example the book cuts its tests from.
	for _, backend := range []string{"llvm", "c"} {
		var out bytes.Buffer
		code, err := Run(filepath.Join("..", "..", "examples", "run", "packaged", "shapes"), backend, nil, &out, &out)
		if err != nil || code != 0 || !strings.HasPrefix(out.String(), "ok  \tshapes\t") {
			t.Errorf("%s: the packaged example's tests exited %d, %v, printed %q", backend, code, err, out.String())
		}
	}

	for _, c := range []struct{ dir, want string }{
		{"badsig", "TestWrong has the wrong signature: want func()"},
		{"notests", "no _test.snello files"},
		{"../loose", "no snello.mod above the package; snello test needs the module to name it"},
	} {
		var out bytes.Buffer
		_, err := Run(filepath.Join(root, c.dir), "llvm", nil, &out, &out)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.dir, err, c.want)
		}
	}
}

// lineOf is the line of the first line holding needle in src, for the
// expected messages, which name the fixture's lines.
func lineOf(src, needle string) string {
	for i, line := range strings.Split(src, "\n") {
		if strings.Contains(line, needle) {
			return strconv.Itoa(i + 1)
		}
	}

	return "?"
}

// timeless replaces every (0.01s) by (0.00s), since a test's time is
// not what is compared.
func timeless(s string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		if i := strings.LastIndex(line, " (0."); i >= 0 && strings.HasSuffix(line, "s)\n") {
			line = line[:i] + " (0.00s)\n"
		}

		b.WriteString(line)
	}

	return b.String()
}

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("cc"); err != nil {
		os.Stderr.WriteString("skipping testrun tests: no C compiler on the PATH\n")
		os.Exit(0)
	}

	os.Exit(m.Run())
}
