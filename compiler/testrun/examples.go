// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testrun

import (
	"errors"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/doc"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
)

// What an example's signature and name are refused with.
const (
	wrongSignature = " has the wrong signature: want func()"
	namesNothing   = " names nothing in package "
)

// An Example is a function of a test file named Example, ExampleNAME or
// ExampleTYPE_MEMBER, with an optional suffix in lower case after an
// underscore, whose last comment says what it must print: its name,
// that text, and whether its lines may come in any order. One with no
// such comment is compiled and not run, and is not listed. Its name
// and its comment are read by doc's rules, which snello doc shows it
// by.
type Example struct {
	Name      string
	Output    string
	Unordered bool
	Runs      bool // it says what it prints, so it is run; one that does not is name-checked and compiled alone
}

// examplesOf returns the examples of a test file parsed with its
// comments, refusing one with parameters, results or type parameters,
// so that an example is never silently not run.
func examplesOf(f *ast.File) ([]Example, error) {
	var out []Example
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Of != nil || !doc.IsExample(fn.Name.Name) {
			continue
		}

		if len(fn.Params) != 0 || len(fn.Results) != 0 || len(fn.TypeParams) != 0 {
			return nil, errors.New(fn.Name.Name + wrongSignature)
		}

		output, unordered, _, runs := doc.ExpectedOutput(f, fn)
		out = append(out, Example{fn.Name.Name, output, unordered, runs})
	}

	return out, nil
}

// checkExampleNames refuses an example whose name names nothing the
// package declares, by doc.ExampleTarget's rule.
func checkExampleNames(examples []Example, pkg string, files map[string][]byte) error {
	names, members, err := declared(files)
	if err != nil {
		return err
	}

	isDeclared := func(name string) bool { return names[name] }
	isMember := func(owner, name string) bool { return members[owner][name] }
	for _, e := range examples {
		if _, _, ok := doc.ExampleTarget(e.Name, isDeclared, isMember); !ok {
			return errors.New(e.Name + namesNothing + pkg)
		}
	}

	return nil
}

// declared returns the names the package's files declare at the top,
// and the methods and functions of each of its types.
func declared(files map[string][]byte) (map[string]bool, map[string]map[string]bool, error) {
	names := map[string]bool{}
	members := map[string]map[string]bool{}
	for name, src := range files {
		f, _, err := parser.ParseFile(name, src)
		if err != nil {
			return nil, nil, err
		}

		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				owner := ""
				switch {
				case d.Recv != nil:
					owner = receiverName(d.Recv.Type)
				case d.Of != nil:
					owner = d.Of.Name.Name
				default:
					names[d.Name.Name] = true
					continue
				}

				if members[owner] == nil {
					members[owner] = map[string]bool{}
				}

				members[owner][d.Name.Name] = true
			case *ast.TypeDecl:
				names[d.Name.Name] = true
			case *ast.TraitDecl:
				names[d.Name.Name] = true
			case *ast.VarDecl:
				for _, n := range d.Names {
					names[n.Name] = true
				}
			}
		}
	}

	return names, members, nil
}

// receiverName returns the name of a receiver's type through its mode.
func receiverName(t ast.Type) string {
	switch t := t.(type) {
	case *ast.RefType:
		return receiverName(t.Elem)
	case *ast.NamedType:
		return t.Name.Name
	}

	return ""
}

// parseWithComments parses a test file with its comments, for its
// examples' expected output.
func parseWithComments(name string, src []byte) (*ast.File, error) {
	return parser.ParseComments(token.NewFile(name, len(src)), src)
}
