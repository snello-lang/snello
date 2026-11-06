// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package token

import "testing"

// TestFileSet gives each file of a set its own positions, and resolves
// a position to the file it is in.
func TestFileSet(t *testing.T) {
	fset := NewFileSet()
	a := fset.AddFile("a.snello", 10)
	b := fset.AddFile("b.snello", 5)
	a.AddLine(4) // a second line starts at offset 4
	b.AddLine(2)
	tests := []struct {
		pos  Pos
		want string
	}{
		{a.Pos(0), "a.snello:1:1"},
		{a.Pos(4), "a.snello:2:1"},
		{a.Pos(10), "a.snello:2:7"}, // one past the end is still the file's
		{b.Pos(0), "b.snello:1:1"},
		{b.Pos(3), "b.snello:2:2"},
		{NoPos, "-"},
	}
	for _, tt := range tests {
		if got := fset.Position(tt.pos).String(); got != tt.want {
			t.Errorf("Position(%d) = %s, want %s", tt.pos, got, tt.want)
		}
	}

	if a.Pos(0) == b.Pos(0) || fset.File(b.Pos(0)) != b || fset.File(a.Pos(10)) != a || fset.File(Pos(100)) != nil {
		t.Errorf("the files share positions or a position names the wrong file: a at %d, b at %d", a.Pos(0), b.Pos(0))
	}

	lone := NewFile("c.snello", 3)
	if lone.Pos(0) != 1 || lone.Offset(lone.Pos(2)) != 2 || !lone.Holds(lone.Pos(3)) || lone.Holds(lone.Pos(4)) {
		t.Errorf("a file on its own starts at 1 and holds its size: %d", lone.Pos(0))
	}
}
