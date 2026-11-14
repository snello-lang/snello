// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/types"
)

// TestFolded writes each computed constant's value as the literal a
// backend writes: a negative number and a negative zero under a minus
// sign, true and false as the predeclared constants, with the type of
// the value they stand for on every node; and leaves as it is a
// literal, which the checker read as it is written, and an expression
// the checker folded nothing for.
func TestFolded(t *testing.T) {
	f, file, err := parser.ParseFile("t.snello", []byte(`package main
const KB = 1 << 10
const Neg i8 = -i8(KB / 16) * 2
const Zero = -0.0
const Product = 0.0 * -1.0
const Small i8 = -5
const Half f32 = 1.0 / 2.0
const Name = "a" + "\n"
const Big = KB > 1000
var plain = 3
func main() {}`))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	if _, err := (&types.Config{Importer: importer.NewImporter(info)}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"KB":      "1024 int",
		"Neg":     "-128 i8",
		"Zero":    "-0.0 (as written)",
		"Product": "-0 f64",
		"Small":   "-5 (as written)",
		"Half":    "0.5 f32",
		"Name":    `"a\n" string`,
		"Big":     "true bool",
		"plain":   "3 (as written)",
	}

	for _, d := range f.Decls {
		d, ok := d.(*ast.VarDecl)
		if !ok {
			continue
		}

		x := d.Values[0]
		out := Folded(info, x)
		got := ast.Source(out) + " " + info.Types[out].String()
		if out == x {
			got = ast.Source(out) + " (as written)"
		}

		if id, ok := out.(*ast.Ident); ok && info.Uses[id] != types.Universe.Lookup(id.Name) {
			t.Errorf("%s: %s does not name the predeclared constant, which a backend writes as itself", d.Names[0].Name, id.Name)
		}

		if u, ok := out.(*ast.UnaryExpr); ok && info.Types[u.X] != info.Types[out] {
			t.Errorf("%s: the negated literal has type %v, want %v", d.Names[0].Name, info.Types[u.X], info.Types[out])
		}

		if got != want[d.Names[0].Name] {
			t.Errorf("%s: written as %s, want %s", d.Names[0].Name, got, want[d.Names[0].Name])
		}
	}
}
