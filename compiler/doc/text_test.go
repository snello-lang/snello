// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/types"
)

// shapes has every kind of declaration a page shows, and names it must
// not show.
const shapes = `// Package shapes measures shapes.
package shapes

// The units, one group.
const Meter = 1 // a meter
const Foot = 3  // a foot

// Origin is where shapes start.
var Origin = Point{}

// A Point is a place.
struct Point impl Eq {
	// X is across.
	X int = 0 // the column
	y int
}

// Shape is what is measured.
enum Shape {
	// Circle is round.
	Circle(r int)
	Square
}

// Measured is what has an area.
trait Measured {
	// Area returns the area.
	func (s ref Self) Area() int
}

// NewPoint makes a point at x.
func NewPoint(x int) Point { return Point{X: x} }

// Point.Parse reads a point.
func Point.Parse(s ref string) Point? { return None }

// Moved returns p moved by d.
func (p ref Point) Moved(d int) Point { return Point{X: p.X + d} }

// Area is the area of s, see [Shape].
func Area(s ref Shape) int { return 0 }

func helper() {}
`

func loadShapes(t *testing.T) *Package {
	t.Helper()
	_, prog, err := importer.LoadProgramFrom(importer.Load{Names: []string{"shapes.snello"}, Srcs: [][]byte{[]byte(shapes)}, Standalone: true, Comments: true}, &types.Info{})
	if err != nil {
		t.Fatal(err)
	}

	return New(Source{Path: "shapes", Files: []File{{prog.Syntax[0], prog.Files[0]}}, Pkg: prog.Pkg})
}

func TestWritePackage(t *testing.T) {
	var b strings.Builder
	loadShapes(t).WritePackage(&b, false)
	want := `package shapes // import "shapes"

Package shapes measures shapes.

const Meter = 1
const Foot = 3

var Origin = Point{}

func Area(s ref Shape) int

trait Measured { ... }

struct Point impl Eq { ... }
    func NewPoint(x int) Point
    func Point.Parse(s ref string) Point?
    func (p ref Point) Moved(d int) Point

enum Shape { ... }
`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestWriteName(t *testing.T) {
	p := loadShapes(t)
	for name, want := range map[string]string{
		"Point": `struct Point impl Eq {
    // X is across.
    X int = 0 // the column
    // Has unexported fields.
}
    A Point is a place.

func NewPoint(x int) Point
func Point.Parse(s ref string) Point?
func (p ref Point) Moved(d int) Point
`,
		"Shape": `enum Shape {
    // Circle is round.
    Circle(r int)
    Square
}
    Shape is what is measured.
`,
		"Measured": `trait Measured {
    // Area returns the area.
    func (s ref Self) Area() int
}
    Measured is what has an area.
`,
		"Area": `func Area(s ref Shape) int
    Area is the area of s, see Shape.
`,
		"Foot": `const Meter = 1 // a meter
const Foot = 3 // a foot
    The units, one group.
`,
		"Point.Moved": `func (p ref Point) Moved(d int) Point
    Moved returns p moved by d.
`,
		"Point.X": `X int = 0
    X is across.
`,
	} {
		var b strings.Builder
		if !p.WriteName(&b, name) {
			t.Errorf("%s: not found", name)
			continue
		}

		if b.String() != want {
			t.Errorf("%s: got:\n%s\nwant:\n%s", name, b.String(), want)
		}
	}

	for _, name := range []string{"helper", "Point.y", "Nope", "Point.Nope", "Point.NewPoint"} {
		if p.WriteName(&strings.Builder{}, name) {
			t.Errorf("%s: found, want not shown", name)
		}
	}
}
