// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testrun

import (
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/doc"
	"github.com/snello-lang/snello/compiler/importer"
)

// The kinds of runnable: a program's main, a test, a benchmark and an
// example.
const (
	KindMain      = "main"
	KindTest      = "test"
	KindBenchmark = "benchmark"
	KindExample   = "example"
)

// The parts of a pattern that names whole tests, which -run and -bench
// read as one test's name, or one of a group's.
const (
	anchorStart = "^"
	anchorEnd   = "$"
	alternation = "|"
)

// A Runnable is what snello runs that a file declares: its kind, and
// the function, whose name is the test's or the benchmark's.
type Runnable struct {
	Kind string
	Decl *ast.FuncDecl
}

// FileRunnables returns what snello runs that the file f, named name,
// declares, in the order written, by the rules snello test and snello
// run find them by: in a test file, the tests and benchmarks it lists
// and the examples it runs, those with an output comment; in any other
// file, main, which makes its package a program whatever its name, as
// snello build takes one. A test whose signature is wrong is listed,
// and running it reports what is wrong.
func FileRunnables(name string, f *ast.File) []Runnable {
	test := strings.HasSuffix(name, importer.TestFileSuffix)
	var found []Runnable
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Of != nil {
			continue
		}

		switch {
		case !test:
			if fn.Name.Name == KindMain {
				found = append(found, Runnable{KindMain, fn})
			}
		case isTest(fn.Name.Name) && strings.HasPrefix(fn.Name.Name, "Benchmark"):
			found = append(found, Runnable{KindBenchmark, fn})
		case isTest(fn.Name.Name):
			found = append(found, Runnable{KindTest, fn})
		case doc.IsExample(fn.Name.Name):
			if _, _, _, runs := doc.ExpectedOutput(f, fn); runs {
				found = append(found, Runnable{KindExample, fn})
			}
		}
	}

	return found
}

// Only is the pattern of -run, or of -bench, that picks the tests named
// and no other: ^Name$ for one, ^(A|B)$ for several. A test's name is an
// identifier, which holds no character a pattern reads.
func Only(names ...string) string {
	if len(names) == 1 {
		return anchorStart + names[0] + anchorEnd
	}

	return anchorStart + "(" + strings.Join(names, alternation) + ")" + anchorEnd
}
