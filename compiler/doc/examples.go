// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/snello-lang/snello/compiler/ast"
)

// What an example's name and its comment are read by: the prefix of its
// name, what parts its pieces, and the words that begin the comment
// saying what it prints.
const (
	examplePrefix  = "Example"
	exampleSep     = "_"
	outputLabel    = "Output:"
	unorderedLabel = "Unordered output:"
)

// An Example is a function of a test file a page shows under the name
// it documents: its suffix, the code of its body as written, without
// the comment saying what it prints, and what it prints.
type Example struct {
	Suffix string
	Code   string
	Output string
}

// ExampleTarget returns what an example's name documents: "" for the
// package, Example; a name of the package, ExampleNAME,
// matched whole first, so that ExampleO_RDONLY documents O_RDONLY; a
// method or a function of a type, ExampleTYPE_MEMBER, as TYPE.MEMBER;
// each with an optional suffix in lower case after an underscore. It
// asks declared whether the package declares a name at its top, and
// member whether a type has a method or a function of the type; ok is
// false when the name documents nothing.
func ExampleTarget(name string, declared func(string) bool, member func(owner, name string) bool) (target, suffix string, ok bool) {
	rest, isExample := strings.CutPrefix(name, examplePrefix)
	switch {
	case !isExample:
		return "", "", false
	case rest == "":
		return "", "", true
	case declared(rest):
		return rest, "", true
	}

	if after, ok := strings.CutPrefix(rest, exampleSep); ok && isLowerStart(after) {
		return "", after, true
	}

	for i := strings.Index(rest, exampleSep); i >= 0; i = nextSep(rest, i) {
		owner, after := rest[:i], rest[i+1:]
		if !declared(owner) {
			continue
		}

		if isLowerStart(after) {
			return owner, after, true
		}

		name, suffix, hasSuffix := strings.Cut(after, exampleSep)
		if member(owner, name) && (!hasSuffix || isLowerStart(suffix)) {
			return owner + string(memberSep) + name, suffix, true
		}
	}

	return "", "", false
}

// IsExample reports whether name is an example's: Example alone, or
// Example and then a capital or an underscore.
func IsExample(name string) bool {
	rest, ok := strings.CutPrefix(name, examplePrefix)
	r, _ := utf8.DecodeRuneInString(rest)
	return ok && (rest == "" || unicode.IsUpper(r) || strings.HasPrefix(rest, exampleSep))
}

// ExpectedOutput returns what an example must print: the text after
// Output: or Unordered output:, in any case, in the last comment group
// of its body, the rest of that line and the lines after it, and that
// group; ok is false for an example that says nothing of it.
func ExpectedOutput(f *ast.File, fn *ast.FuncDecl) (output string, unordered bool, group *ast.CommentGroup, ok bool) {
	if f.Docs == nil || fn.Body == nil {
		return "", false, nil, false
	}

	for _, g := range f.Docs.Groups {
		if g.Pos() > fn.Body.Lbrace && g.Pos() < fn.Body.Rbrace {
			group = g
		}
	}

	if group == nil {
		return "", false, nil, false
	}

	text := strings.TrimLeft(group.Text(), " \t\n")
	for _, label := range []struct {
		word      string
		unordered bool
	}{{unorderedLabel, true}, {outputLabel, false}} {
		if len(text) >= len(label.word) && strings.EqualFold(text[:len(label.word)], label.word) {
			return text[len(label.word):], label.unordered, group, true
		}
	}

	return "", false, nil, false
}

// addExamples finds the examples of the package's test files and keeps
// each under the name it documents, with the code of its body.
func (p *Package) addExamples(tests []TestFile) {
	declared := func(name string) bool { _, ok := p.decls[name]; return ok }
	member := func(owner, name string) bool { return p.members[owner][name] }
	for _, f := range tests {
		for _, d := range f.Syntax.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Of != nil || fn.Body == nil || !IsExample(fn.Name.Name) {
				continue
			}

			target, suffix, ok := ExampleTarget(fn.Name.Name, declared, member)
			if !ok {
				continue
			}

			output, _, group, hasOutput := ExpectedOutput(f.Syntax, fn)
			end := fn.Body.Rbrace
			if hasOutput {
				end = group.Pos() // the code stops where the comment saying what it prints begins
			}

			code := string(f.Src[f.Tok.Offset(fn.Body.Lbrace)+1 : f.Tok.Offset(end)])
			p.Examples[target] = append(p.Examples[target], &Example{suffix, codeOf(code), strings.TrimSpace(output)})
		}
	}
}

// codeOf returns a body's text as a page shows it: its blank lines at
// either end dropped, its common indentation taken off, and the
// carriage returns of a file written with them dropped.
func codeOf(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r", ""), lineSep)
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}

	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}

	return strings.Join(dedent(lines), lineSep)
}

func nextSep(s string, i int) int {
	if j := strings.Index(s[i+1:], exampleSep); j >= 0 {
		return i + 1 + j
	}

	return -1
}

func isLowerStart(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return s != "" && unicode.IsLower(r)
}
