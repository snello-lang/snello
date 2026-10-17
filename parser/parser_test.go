// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
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
		{"package", "1:8: expected 'IDENT', found 'EOF'", 1},
		{"package 3", "1:9: expected 'IDENT', found '3'", 1},
		{"package a\nimport 7\n", "2:8: expected import path, found '7'", 1},
		{"package a\nimport (\"x\" \"y\")\n", "2:13: expected ';' or newline, found '\"y\"'", 1},
		{"package a\nfunc f() {}\nimport \"x\"\n", "2:1: expected declaration", 2},
		{"package a\nimport (\"x\"\n\"y\" 7)\n", "3:5: expected ';' or newline, found '7'", 1},
		{"package a\n\"", "2:1: expected declaration, found '\"'", 2}, // plus the scanner's unterminated string
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

func countErrors(err error) int {
	if list, ok := err.(scanner.ErrorList); ok {
		return len(list)
	}

	return 1
}
