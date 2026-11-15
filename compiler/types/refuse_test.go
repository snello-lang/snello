// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
)

// refuseLib is a package whose reflecting function refuses what it
// cannot take: a type that is no enum, and, on an enum that lists Mark,
// a variant with an unnamed field.
const refuseLib = `package refl
struct Mark impl TypeAttribute {}
const why = "an unnamed field"
func Need<T>() int {
	return match T {
		enum => match attr(T, Mark) {
			Some(_) => {
				n := 0
				for c := range variants(T) {
					for f := range fields(c) {
						match f.name {
							"" => refuse("Need wants named fields: " + c.name + " has " + why),
							_ => n += 1,
						}
					}
				}

				n
			}
			None => 0,
		},
		_ => refuse("Need takes an enum"),
	}
}
`

// TestRefuse checks refuse where the compiler reaches it: for each type
// whose instance takes the arm that holds it, a match on an attribute
// and on a part's name decided for each copy, and the message written
// with the copy's names; a type that takes no such arm passes.
func TestRefuse(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"enum E @refl.Mark { A; B(int); C(n int) }\nfunc f() int { return refl.Need<E>() }", "refl.Need<E>: Need wants named fields: B has an unnamed field"},
		{"enum E @refl.Mark { A; C(n int) }\nfunc f() int { return refl.Need<E>() }", ""},
		{"enum E { A; B(int) }\nfunc f() int { return refl.Need<E>() }", ""}, // no Mark: the arm that refuses is not taken
		{"struct S { n int }\nfunc f() int { return refl.Need<S>() }", "refl.Need<S>: Need takes an enum"},
		{"func f() int { return 1 }", ""}, // a refuse no instance reaches refuses nothing
	}
	for _, tt := range tests {
		info := &Info{}
		imp := &sourceImporter{srcs: map[string]string{"refl": refuseLib}, loaded: map[string]*Package{}, info: info}
		imp.conf.Importer = imp
		f, file, err := parser.ParseFile("test.snello", []byte("package main\nimport \"refl\"\n"+tt.src))
		if err != nil {
			t.Fatal(err)
		}

		_, err = imp.conf.Check("", file, f, info)
		got := ""
		if err != nil {
			got = err.Error()
		}

		if tt.want == "" && got != "" || tt.want != "" && !strings.Contains(got, tt.want) {
			t.Errorf("%s:\n  got  %q\n  want %q", tt.src, got, tt.want)
		}
	}
}

// TestRefuseMessage checks what refuse is given: one message of text
// the compiler knows, and outside a generic function a refusal where it
// is written.
func TestRefuseMessage(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"package p\nconst why = \"no\"\nfunc f() int { refuse(\"f: \" + why) }", []string{"3:16: f: no"}},
		{"package p\nfunc f(s string) int { refuse(\"bad \" + s) }", []string{"refuse's message is text the compiler knows: string constants and parts' names, c.name, joined with +; s is not"}},
		{"package p\nfunc f(n int) int { refuse(n) }", []string{"cannot use n (int) as string in argument"}},
		{"package p\nfunc f() int { refuse() }", []string{"refuse takes one message"}},
		{"package p\nfunc f<T>() int { return match T { int => 1, _ => refuse(\"f takes an int\") } }", nil}, // no instance reaches it here
	}
	for _, tt := range tests {
		got := checkErrors(t, tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%s:\n  got %d errors %q\n  want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s:\n  got  %q\n  want %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}
