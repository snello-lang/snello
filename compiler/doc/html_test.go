// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/token"
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

// TestScriptKeywords holds the keywords the script colors to the
// language's, every one and no other.
func TestScriptKeywords(t *testing.T) {
	_, list, ok := strings.Cut(pageScript, "var keywords = [")
	list, _, _ = strings.Cut(list, "];")
	if !ok {
		t.Fatal("assets/script.js has no list of keywords")
	}

	colored := map[string]bool{}
	for _, word := range strings.Split(list, ",") {
		colored[strings.Trim(strings.TrimSpace(word), `"`)] = true
	}

	var want int
	for k := token.Kind(0); int(k) < token.NumKinds; k++ {
		if !k.IsKeyword() {
			continue
		}

		want++
		if !colored[k.String()] {
			t.Errorf("assets/script.js does not color the keyword %s", k)
		}
	}

	if len(colored) != want {
		t.Errorf("assets/script.js colors %d words, the language has %d keywords", len(colored), want)
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
		`<h1>geo</h1>`, `<pre>import "geo"</pre>`, `<div class="eyebrow">Standard library</div>`, `<div class="tree-group">Standard library</div>`,
		`<div class="tree-group">Language</div>`, `<li class="open">`, `<a class="package current" href="/pkg/geo">`,
		`<a class="package" href="/pkg/builtin">`, `<h2 id="section-overview">Overview</h2>`, `<h2 id="section-topics">Topics</h2>`, `v0.1.0`,
		`<a class="topic" href="/pkg/geo#Point"><span class="kind kind-struct" title="struct"></span><code>Point</code><span class="topic-abstract">A Point is a place.</span></a>`,
		`<a href="/pkg/geo#Point.Moved"><span class="kind kind-method" title="method"></span><span class="name">Point.Moved</span></a>`,
		`<span class="kind kind-func" title="func"></span><code>Area</code>`, `Moved returns p moved by d.`,
		`<li><a href="#section-topics">Topics</a></li>`, `<li class="sub"><a href="#Point">Point</a></li>`, `data-text="/text/geo"`,
		`<p class="abstract">Package geo measures, see <a href="/pkg/geo#Point">Point</a> and <a href="/pkg/strings#Builder">strings.Builder</a>.</p>`,
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

	if n := strings.Count(page, "Package geo measures"); n != 1 {
		t.Errorf("the first sentence is on the page %d times, want once, under the title", n)
	}
}

// TestWriteIndex lists the packages of each group with their first
// sentences.
func TestWriteIndex(t *testing.T) {
	var b strings.Builder
	if err := WriteIndex(&b, testNav, []IndexEntry{load(t, "strings", false).Index()}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{`<h1>Snello Documentation</h1>`, `<h2>Standard library</h2>`,
		`<a class="topic" href="/pkg/strings"><span class="kind kind-package" title="package"></span><code>strings</code><span class="topic-abstract">Package strings builds and transforms text.</span></a>`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the index lacks %s", want)
		}
	}
}

func TestSynopsis(t *testing.T) {
	for doc, want := range map[string]string{
		"Package geo measures. It is small.\n":    "Package geo measures.",
		"Package geo measures\nshapes.\n\nMore.":  "Package geo measures shapes.",
		"Package geo has no period":               "Package geo has no period",
		"See [Point] and [strings.Builder]. More": "See Point and strings.Builder.",
		"": "",
	} {
		if got := Synopsis(doc); got != want {
			t.Errorf("Synopsis(%q) = %q, want %q", doc, got, want)
		}
	}
}

// fromSource returns the package of one file's source, with its
// unexported names when unexported is set.
func fromSource(t *testing.T, path, src string, unexported bool) *Package {
	t.Helper()
	load := importer.Load{Names: []string{path + ".snello"}, Srcs: [][]byte{[]byte(src)}, Standalone: true, Comments: true}
	_, prog, err := importer.LoadProgramFrom(load, &types.Info{})
	if err != nil {
		t.Fatal(err)
	}

	return New(Source{Path: path, Files: []File{{prog.Syntax[0], prog.Files[0]}}, Pkg: prog.Pkg, Unexported: unexported})
}

// TestWriteHTMLUndocumented writes the page of a package with no
// comment anywhere, every name of a group anchored, and every kind of
// name with its badge.
func TestWriteHTMLUndocumented(t *testing.T) {
	src := `package bare

const A = 1
const B = 2

var V = 3

struct T { X int }

func (t ref T) M() int { return t.X }

enum E {
	One
	Two
}

trait Sized {
	func (s ref Self) Size() int
}

type Celsius f64

func F() {}
`
	var b strings.Builder
	if err := fromSource(t, "bare", src, false).WriteHTML(&b, testNav); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		`id="A"`, `<span id="B"></span>`, `id="T"`, `id="T.M"`, `id="F"`,
		`kind-const" title="const"></span><code>A</code>`, `kind-const" title="const"></span><code>B</code>`,
		`kind-var" title="var"></span><code>V</code>`, `kind-struct" title="struct"></span><code>T</code>`,
		`kind-enum" title="enum"></span><code>E</code>`, `kind-trait" title="trait"></span><code>Sized</code>`,
		`kind-type" title="type"></span><code>Celsius</code>`, `kind-func" title="func"></span><code>F</code>`,
		`kind-method" title="method"></span><span class="name">T.M</span>`,
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the page lacks %s:\n%s", want, b.String())
		}
	}
}

// TestAnchorsUnique holds every page of the standard library, and one
// whose unexported names are the page's own ids and its sections', to
// ids that are each one element's, and every link of the index and of
// the page to an id of its page.
func TestAnchorsUnique(t *testing.T) {
	builtin, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}

	// Names a page's own ids and its sections' might have been, shown
	// with the unexported names.
	clashing := "package clash\n\nfunc filter() {}\nfunc tree() {}\nfunc theme() {}\nfunc overview() {}\nfunc topics() {}\n\nvar values = 1\nvar types = 2\nvar functions = 3\n"
	pages := map[string]*Package{BuiltinPath: builtin, "clash": fromSource(t, "clash", clashing, true)}
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

		for _, part := range strings.Split(page.String(), ` href="#`)[1:] {
			anchor, err := url.PathUnescape(part[:strings.IndexByte(part, '"')]) // the template escapes a link as a browser reads it
			if err != nil || anchor != "" && ids[anchor] == 0 {
				t.Errorf("%s: the page links #%s, which it has no id for", path, anchor)
			}
		}
	}
}

// TestWriteSearch writes every package and every shown name with its
// page, its first sentence and its kind, for the filter.
func TestWriteSearch(t *testing.T) {
	var b strings.Builder
	if err := WriteSearch(&b, []IndexEntry{load(t, "strings", false).Index()}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{`{"t":"strings","h":"/pkg/strings","s":"Package strings builds and transforms text.","k":"package"}`,
		`{"t":"strings.Builder","h":"/pkg/strings#Builder","s":"A Builder accumulates text`, `"k":"struct"`, `"t":"strings.Builder.WriteString"`, `"k":"method"`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the search lacks %s", want)
		}
	}
}

// TestWithoutAbstract drops the first paragraph when it is the first
// sentence the title shows, links and all, and keeps one that says
// more.
func TestWithoutAbstract(t *testing.T) {
	abstract := []Link{{Text: "Package geo measures "}, {Text: "Point", Href: "/pkg/geo#Point"}, {Text: "."}}
	alone := []blockView{
		{Kind: paragraphKind, Spans: []Link{{Text: "Package geo measures"}, {Text: wordSep}, {Text: "Point", Href: "/pkg/geo#Point"}, {Text: "."}}},
		{Kind: paragraphKind, Spans: []Link{{Text: "More."}}},
	}
	if got := withoutAbstract(alone, abstract); len(got) != 1 || got[0].Spans[0].Text != "More." {
		t.Errorf("a first paragraph that is the abstract kept: %v", got)
	}

	more := []blockView{{Kind: paragraphKind, Spans: []Link{{Text: "Package geo measures Point. It is small."}}}}
	if got := withoutAbstract(more, abstract); len(got) != 1 {
		t.Errorf("a first paragraph that says more dropped: %v", got)
	}
}
