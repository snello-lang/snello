// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/types"
)

// testNav is the sidebar of the tests' pages: a language group and a
// standard group, and a group with no package, which is not listed.
var testNav = Nav{Version: "0.1.0", Groups: []NavGroup{
	{Title: LanguageGroup, Paths: []string{BuiltinPath}},
	{Title: StandardGroup, Paths: []string{"strings", "geo", "bare"}},
	{Title: LibrariesGroup},
}}

// iconLicense is the license header the pages' copy of the icon begins
// with, before the brand's icon.
const iconLicense = "<!--\nCopyright 2026 The Snello Programming Language. All rights reserved.\nUse of this source code is governed by a BSD-style\nlicense that can be found in the LICENSE file.\n-->\n"

// TestIcon holds the icon the pages carry to the brand's, under the
// license header.
func TestIcon(t *testing.T) {
	brand, err := os.ReadFile(filepath.Join("..", "..", "_docs", "brand", "icon.svg"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(icon, iconLicense) || strings.TrimPrefix(icon, iconLicense) != string(brand) {
		t.Error("assets/icon.svg is not the license header and then _docs/brand/icon.svg: copy the brand's icon under the header")
	}
}

// TestWriteHTML writes a package's page: each declaration with its
// anchor, its comment with links to what it names, and nothing of a
// comment as markup.
func TestWriteHTML(t *testing.T) {
	src := `// Package geo measures, see [Point] and [strings.Builder].
//
// A <script>alert(1)</script> is text here.
package geo

import "strings"

// A Point is a place.
struct Point { X int }

// Moved returns p moved by d.
func (p ref Point) Moved(d int) Point { return Point{X: p.X + d} }

// Area returns the area of a square of side s.
func Area(s int) int { return s * s }

func use(b ref strings.Builder) int { return b.Len() }
`
	_, prog, err := importer.LoadProgramFrom(importer.Load{Names: []string{"geo.snello"}, Srcs: [][]byte{[]byte(src)}, Standalone: true, Comments: true}, &types.Info{})
	if err != nil {
		t.Fatal(err)
	}

	imp := importer.NewImporter(&types.Info{})
	p := New(Source{Path: "geo", Files: []File{{prog.Syntax[0], prog.Files[0]}}, Pkg: prog.Pkg, Import: func(path string) *types.Package {
		pkg, _ := imp.Import(path)
		return pkg
	}})
	var b strings.Builder
	if err := p.WriteHTML(&b, testNav); err != nil {
		t.Fatal(err)
	}

	page := b.String()
	for _, want := range []string{
		`<h1>package geo<small class="mono">import "geo"</small></h1>`, `<div class="side-title">Standard library</div>`,
		`<li data-name="geo" class="current">`, `<a href="/pkg/geo">geo</a>`, `<h2>Summary</h2>`, `Moved returns p moved by d.`, `v0.1.0`,
		`<a href="/pkg/geo#Point">Point</a>`, `<a href="/pkg/strings#Builder">strings.Builder</a>`,
		`A &lt;script&gt;alert(1)&lt;/script&gt; is text here.`,
		`id="Point"`, `id="Point.Moved"`, `id="Area"`, `func Area(s int) int`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %s:\n%s", want, page)
		}
	}

	if strings.Contains(page, "<script>alert") || strings.Contains(page, "func use") || strings.Contains(page, LibrariesGroup) {
		t.Errorf("a comment written as markup, an unexported name shown, or an empty group listed:\n%s", page)
	}
}

// TestWriteIndex lists the packages with their first sentences and
// every shown name for the filter.
func TestWriteIndex(t *testing.T) {
	var b strings.Builder
	if err := WriteIndex(&b, testNav, []IndexEntry{load(t, "strings", false).Index()}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{`<h2>Standard library</h2>`, `<a class="mono" href="/pkg/strings">strings</a><span class="synopsis">Package strings builds and transforms text.</span>`, `<a class="mono" href="/pkg/strings#Builder">strings.Builder</a>`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the index lacks %s", want)
		}
	}
}

func TestSynopsis(t *testing.T) {
	for doc, want := range map[string]string{
		"Package geo measures. It is small.\n":   "Package geo measures.",
		"Package geo measures\nshapes.\n\nMore.": "Package geo measures shapes.",
		"Package geo has no period":              "Package geo has no period",
		"":                                       "",
	} {
		if got := Synopsis(doc); got != want {
			t.Errorf("Synopsis(%q) = %q, want %q", doc, got, want)
		}
	}
}

// TestWriteHTMLUndocumented writes the page of a package with no
// comment anywhere, every name of a group anchored.
func TestWriteHTMLUndocumented(t *testing.T) {
	src := "package bare\n\nconst A = 1\nconst B = 2\n\nstruct T { X int }\n\nfunc (t ref T) M() int { return t.X }\n\nfunc F() {}\n"
	_, prog, err := importer.LoadProgramFrom(importer.Load{Names: []string{"bare.snello"}, Srcs: [][]byte{[]byte(src)}, Standalone: true, Comments: true}, &types.Info{})
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	if err := New(Source{Path: "bare", Files: []File{{prog.Syntax[0], prog.Files[0]}}, Pkg: prog.Pkg}).WriteHTML(&b, testNav); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{`id="A"`, `<span id="B"></span>`, `id="T"`, `id="T.M"`, `id="F"`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the page lacks %s", want)
		}
	}
}

// TestAnchorsUnique holds every page of the standard library to ids
// that are each one declaration's, and every link of the index to an
// id of its page.
func TestAnchorsUnique(t *testing.T) {
	builtin, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}

	pages := map[string]*Package{BuiltinPath: builtin}
	for _, path := range importer.Paths() {
		if path != BuiltinPath {
			pages[path] = load(t, path, false)
		}
	}

	for path, p := range pages {
		var page strings.Builder
		if err := p.WriteHTML(&page, testNav); err != nil {
			t.Fatal(err)
		}

		ids := map[string]int{}
		for _, part := range strings.Split(page.String(), ` id="`)[1:] {
			ids[part[:strings.IndexByte(part, '"')]]++
		}

		for id, n := range ids {
			if n > 1 {
				t.Errorf("%s: id %q %d times", path, id, n)
			}
		}

		for _, link := range p.Index().Names {
			if _, anchor, _ := strings.Cut(link.Href, anchorMark); ids[anchor] == 0 {
				t.Errorf("%s: the index links %s, which the page has no id for", path, link.Href)
			}
		}
	}
}
