// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/teak-lang/teak/compiler/parser"
	"github.com/teak-lang/teak/compiler/scanner"
	"github.com/teak-lang/teak/compiler/types"
	"github.com/teak-lang/teak/std"
)

// method is a predeclared method as std/builtin/builtin.teak declares
// it: the receiver's name, mode and type, the method's name, and its
// parameters and result as written.
type method struct {
	recvName, mode, recv, name, params, result string
}

// methodLine matches a method declaration of the documentation:
// func (xs mut []T) Push(v T), with an optional type parameter list
// on the name, Map<U>.
var methodLine = regexp.MustCompile(`^func \((\w+) (ref |mut |)([^)]+)\) (\w+)(?:<\w+>)?\((.*?)\) ?(.*)$`)

// documented returns the methods std/builtin/builtin.teak declares.
func documented(t *testing.T) []method {
	src, err := std.Sources.ReadFile("builtin/builtin.teak")
	if err != nil {
		t.Fatal(err)
	}

	var methods []method
	for _, line := range strings.Split(string(src), "\n") {
		if m := methodLine.FindStringSubmatch(line); m != nil {
			methods = append(methods, method{m[1], strings.TrimSpace(m[2]), m[3], m[4], m[5], m[6]})
		}
	}

	return methods
}

// TestBuiltin keeps the documentation of the predeclared methods in
// step with the checker's table, both ways: every method the file
// declares exists with that receiver mode, those parameters and that
// result, and every method the table has is in the file. The file's
// declarations are checked as functions of a library package whose
// receiver is the first parameter, with T, K, V, E and U declared as
// types, so that the signatures compare as types, not as text.
func TestBuiltin(t *testing.T) {
	methods := documented(t)
	if len(methods) < 30 {
		t.Fatalf("only %d methods documented", len(methods))
	}

	// Each method becomes a method of a struct holding the receiver's
	// type, with the documented receiver mode, so that the borrow rules
	// read it as the method they document.
	var b strings.Builder
	b.WriteString("package doc\nstruct T impl Eq, Hash {}\nstruct K impl Eq, Hash {}\nstruct V {}\nstruct E {}\nstruct U {}\n")
	for i, m := range methods {
		n := strconv.Itoa(i)
		b.WriteString("struct R" + n + " { r " + m.recv + " }\n")
		b.WriteString("func (" + m.recvName + " " + m.mode + " R" + n + ") m" + n + "(" + m.params + ") " + m.result + " { panic(\"doc\") }\n")
	}

	f, file, err := parser.ParseFile("doc.teak", []byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	pkg, err := (&types.Config{}).Check("doc", file, f, info)
	if err != nil {
		if list, ok := err.(scanner.ErrorList); ok {
			for _, e := range list {
				t.Error(e)
			}
		}

		t.Fatalf("the documented signatures do not check: %v", err)
	}

	for i, m := range methods {
		n := strconv.Itoa(i)
		holder := pkg.Scope().Lookup("R" + n).(*types.TypeName).Type().(*types.Named)
		recv := holder.Underlying().(*types.Struct).Field(0).Type()
		sig := holder.Method("m" + n).Type().(*types.Signature)
		want := types.BuiltinMethod(recv, m.name)
		if want == nil {
			t.Errorf("%s on %s: documented but the checker has no such method", m.name, m.recv)
			continue
		}

		if got, want := receiverMode(sig), receiverMode(want); got != want {
			t.Errorf("%s on %s: documented on a %s receiver, the checker's is %s", m.name, m.recv, got, want)
		}

		// The file's T, K, V, E and U are types of package doc; the
		// checker's U is a type parameter and prints bare.
		if got, want := strings.ReplaceAll(signature(sig.Params(), sig.Results()), "doc.", ""), strings.ReplaceAll(signature(want.Params(), want.Results()), "doc.", ""); got != want {
			t.Errorf("%s on %s: documented as %s, the checker has %s", m.name, m.recv, got, want)
		}
	}

	// The other way: every name in the checker's table is documented
	// on the receiver kind of the table it is in.
	table, err := os.ReadFile("../types/library.go")
	if err != nil {
		t.Fatal(err)
	}

	kinds := map[string]string{"sliceMethod": "[]T", "mapMethod": "map[K]V", "chanMethod": "chan T", "stringMethod": "string", "standardMethod": "string", "byteMethod": "byte", "optionMethod": "T?", "resultMethod": "Result<T, E>"}
	have := map[string]bool{}
	for _, m := range methods {
		have[m.recv+" "+m.name] = true
	}

	kind := ""
	for _, line := range strings.Split(string(table), "\n") {
		if strings.HasPrefix(line, "func ") {
			kind = ""
			for fn, recv := range kinds {
				if strings.HasPrefix(line, "func "+fn+"(") {
					kind = recv
				}
			}
		}

		if kind == "" || !strings.HasPrefix(strings.TrimSpace(line), "case \"") {
			continue
		}

		for _, name := range regexp.MustCompile(`"(\w+)"`).FindAllStringSubmatch(line, -1) {
			if !have[kind+" "+name[1]] {
				t.Errorf("%s on %s: in the checker's table but not documented in std/builtin", name[1], kind)
			}
		}
	}
}

// receiverMode says how a method takes its receiver: by value, ref or
// mut.
func receiverMode(sig *types.Signature) string {
	r, ok := sig.Recv().Type().(*types.Ref)
	switch {
	case !ok:
		return "value"
	case r.Mut():
		return "mut"
	}

	return "ref"
}

// signature writes parameters and a result as the checker prints them.
func signature(params []*types.Var, result types.Type) string {
	var b strings.Builder
	b.WriteString("(")
	for i, p := range params {
		if i > 0 {
			b.WriteString(", ")
		}

		b.WriteString(p.Type().String())
	}

	b.WriteString(") ")
	b.WriteString(result.String())
	return b.String()
}
