// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/scanner"
)

func TestFile(t *testing.T) {
	tests := []struct {
		src     string
		pkg     string
		imports []string // "name path" per import, name "-" when absent
	}{
		{"package main", "main", nil},
		{"package tree\n\nimport \"fmt\"\n", "tree", []string{`- "fmt"`}},
		{"package web\n\nimport (\n\t\"db\"\n\tj \"json\"\n\t\"net/http\"\n)\n", "web", []string{`- "db"`, `j "json"`, `- "net/http"`}},
		{"package a\nimport \"x\"\nimport \"y\"\n", "a", []string{`- "x"`, `- "y"`}},
		{"package a; import \"x\"; import (\"y\"; \"z\")", "a", []string{`- "x"`, `- "y"`, `- "z"`}},
	}
	for _, tt := range tests {
		f, _, err := ParseFile("test.teak", []byte(tt.src))
		if err != nil {
			t.Errorf("%q: unexpected error: %v", tt.src, err)
			continue
		}

		if f.Name.Name != tt.pkg {
			t.Errorf("%q: package %q, want %q", tt.src, f.Name.Name, tt.pkg)
		}

		var got []string
		for _, imp := range f.Imports {
			name := "-"
			if imp.Name != nil {
				name = imp.Name.Name
			}

			got = append(got, name+" "+imp.Path.Value)
		}

		if strings.Join(got, ";") != strings.Join(tt.imports, ";") {
			t.Errorf("%q: imports %v, want %v", tt.src, got, tt.imports)
		}
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		src  string
		want string // substring of the first error
		n    int    // total errors expected
	}{
		{"func main() {}", "1:1: expected 'package', found 'func'", 2},
		{"package", "1:8: expected identifier, found 'EOF'", 1},
		{"package 3", "1:9: expected identifier, found '3'", 2}, // the 3 is then not a declaration
		{"package a\nimport 7\n", "2:8: expected import path, found '7'", 1},
		{"package a\nimport (\"x\" \"y\")\n", "2:13: expected ';' or newline, found '\"y\"'", 1},
		{"package a\nfunc f() {}\nimport \"x\"\n", "3:1: imports must appear before other declarations", 1},
		{"package a\nimport (\"x\"\n\"y\" 7)\n", "3:5: expected ';' or newline, found '7'", 1},
		{"package a\n\"", "2:1: expected declaration, found '\"'", 2},                                 // plus the scanner's unterminated string
		{"package a\nimport (\n\t\"x\"\nfunc f() {}\n", "4:1: expected import path, found 'func'", 2}, // then the missing ')'; f parses
		{"package a\nimport (\n\t\"x\"\n", "4:1: expected ')', found 'EOF'", 1},
	}
	for _, tt := range tests {
		_, _, err := ParseFile("test.teak", []byte(tt.src))
		if err == nil {
			t.Errorf("%q: expected an error", tt.src)
			continue
		}

		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: got %q, want it to contain %q", tt.src, err, tt.want)
		}

		if n := countErrors(err); n != tt.n {
			t.Errorf("%q: %d errors, want %d: %v", tt.src, n, tt.n, err)
		}
	}
}

// TestPartialFile checks that a file with an error still comes back with
// everything parsed before it, so tools can work on broken input.
func TestPartialFile(t *testing.T) {
	f, file, err := ParseFile("test.teak", []byte("package a\nimport \"x\"\n???\n"))
	if err == nil {
		t.Fatal("expected an error")
	}

	if f == nil || f.Name.Name != "a" || len(f.Imports) != 1 {
		t.Errorf("partial file lost its package or imports: %s", ast.Sprint(f, file))
	}
}

// TestExamples parses every example program and expects no errors. The
// examples are the acceptance suite; a syntax change that breaks one is
// wrong until the example or the conventions are updated.
func TestExamples(t *testing.T) {
	for _, f := range exampleFiles(t) {
		if _, _, err := ParseFile(f.name, f.src); err != nil {
			t.Errorf("%s: %v", f.name, err)
		}
	}
}

// BenchmarkExamples parses every example program once per iteration
// and reports allocations, the number the coding standards track.
func BenchmarkExamples(b *testing.B) {
	files := exampleFiles(b)
	var decls int

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for _, f := range files {
			file, _, err := ParseFile(f.name, f.src)
			if err != nil {
				b.Fatal(err)
			}

			decls += len(file.Decls)
		}
	}

	b.ReportMetric(float64(decls)/float64(b.N), "decls/op")
}

func countErrors(err error) int {
	if list, ok := err.(scanner.ErrorList); ok {
		return len(list)
	}

	return 1
}

type exampleFile struct {
	name string
	src  []byte
}

// exampleFiles reads every .teak file under examples/, as the scanner's
// benchmark does.
func exampleFiles(tb testing.TB) []exampleFile {
	tb.Helper()
	var files []exampleFile
	err := filepath.WalkDir(filepath.Join("..", "examples"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && strings.HasSuffix(path, ".teak") {
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			files = append(files, exampleFile{path, src})
		}

		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}

	if len(files) < 20 {
		tb.Fatalf("found %d example files, want at least 20", len(files))
	}

	return files
}
