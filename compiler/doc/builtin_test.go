// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import "testing"

// TestBuiltin reads the predeclared names, parsed and never checked:
// Option with its variants, a slice's method among the functions, and
// links that name something.
func TestBuiltin(t *testing.T) {
	p, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}

	if p.Type("Option") == nil || !p.Resolve("Option.Some") {
		t.Error("Option or its variant Some is missing")
	}

	if p.Func("Push") == nil {
		t.Error("the slice method Push is missing")
	}

	for _, b := range p.Broken {
		t.Errorf("%s: [%s] names nothing", b.Pos, b.Name)
	}
}
