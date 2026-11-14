// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/scanner"
)

// reachLib is a package of reflecting functions the program's types are
// given to.
const reachLib = `package refl
func Show<T>(v ref T) int {
	return match T {
		int => 1,
		string => 2,
		[]E => { s := 0; for _, e := range v { s += Show(e) }; s },
		struct => { s := 0; for f := range fields(T) { s += Show(ref v.[f]) }; s },
		enum => match v { for c := range variants(T) => { s := 0; for f := range fields(c) { s += Show(ref v.[f]) }; s } },
	}
}
func Grow<T>(n int) {
	match T {
		[]E => Grow<[][]E>(n),
		_ => {},
	}
}
struct Own { hidden int }
func Mine() int { return Show(ref Own{hidden: 1}) }
`

// TestReach asks the questions a type answers where a reflecting
// function of another package is used with it: the fields and variants
// it reaches, those its package keeps unless the type lists Reflect, the
// arm each type takes, a field holding a borrow, and types that grow;
// a recursive type ends, and the function reaches its own package's
// hidden fields.
func TestReach(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"struct H { Port int; Name string }\nfunc f() int { return refl.Show(ref H{Port: 1, Name: \"a\"}) }", ""},
		{"struct H { port int }\nfunc f() int { return refl.Show(ref H{port: 1}) }", "refl.Show<H>: field port of H is not exported; export it, or list Reflect on H"},
		{"struct H impl Reflect { port int }\nfunc f() int { return refl.Show(ref H{port: 1}) }", ""},
		{"enum K { On; off }\nfunc f() int { return refl.Show(ref On) }", "refl.Show<K>: variant off of K is not exported; export it, or list Reflect on K"},
		{"enum K impl Reflect { On; off }\nfunc f() int { return refl.Show(ref On) }", ""},
		{"enum V { Leaf(int); Node([]V) }\nfunc f() int { return refl.Show(ref Node([]V{Leaf(1)})) }", ""},
		{"struct C { Ch chan int }\nfunc f(c ref C) int { return refl.Show(c) }", "refl.Show<C>, refl.Show<chan int>: no arm of the match on T takes chan int"},
		{"struct L { S ref string }\nfunc f(l ref L) int { return refl.Show(l) }", "field S of L holds a borrow, ref string"},
		{"func f() { refl.Grow<[]int>(1) }", "refl.Grow<[]int>, refl.Grow<[][]int>: Grow<[][][]int> holds Grow<[][]int>, which holds Grow<[]int>; the types grow without end"}, // a shape's name, no edge the call graph sees
		{"func f() int { return refl.Mine() }", ""},
		// The paths to a reflecting function: a generic type's method, a trait's default, and a method calling one.
		{"struct Box<T> { item T }\nfunc (b ref Box<T>) Kind() int { return match T { int => 1, string => 2 } }\nfunc f() int { return Box<bool>{item: true}.Kind() }", "Kind<bool>: no arm of the match on T takes bool"},
		{"trait Tg { func (s ref Self) Tag<U>(u ref U) int { return match U { int => 1 } } }\nstruct W impl Tg {}\nfunc f() int { return W{}.Tag(ref 2.5) }", "no arm of the match on U takes f64"},
		{"struct Box<T> { item T }\nfunc (b ref Box<T>) Show() int { return refl.Show(ref b.item) }\nfunc f() int { return Box<bool>{item: true}.Show() }", "Show<bool>, refl.Show<bool>: no arm of the match on T takes bool"},
		// A type declared over another package's struct opens none of its fields, Reflect listed or not.
		{"type Mine refl.Own\nfunc count<T>() int { return match T { struct => { n := 0; for f := range fields(T) { n += 1 }; n }, _ => 0 } }\nfunc f() int { return count<Mine>() }", "count<Mine>: field hidden of Mine is not exported by refl, which declares it"},
		{"type Open refl.Own impl Reflect\nfunc count<T>() int { return match T { struct => { n := 0; for f := range fields(T) { n += 1 }; n }, _ => 0 } }\nfunc f() int { return count<Open>() }", "count<Open>: field hidden of Open is not exported by refl, which declares it"},
		// A trait value takes an arm naming its trait only when a value reaches all the trait has.
		{"trait Same { func (s ref Self) Same(o ref Self) bool }\nstruct A impl Same { n int }\nfunc (a ref A) Same(o ref A) bool { return a.n == o.n }\nfunc same<T>(v ref T, w ref T) bool { return match T { Same => v.Same(w), _ => false } }\nfunc f() bool { var x Same = A{n: 1}; var y Same = A{n: 1}; return same(ref x, ref y) }", "same<Same>: a value of Same takes the arm Same, whose method Same mentions Self"},
		{"trait Mk { func (s ref Self) Show() string; func Make(n int) Self }\nstruct M impl Mk { c int }\nfunc (m ref M) Show() string { return \"m\" }\nfunc M.Make(n int) M { return M{c: n} }\nfunc mk<T>(v ref T) string { return match T { Mk => T.Make(3).Show(), _ => \"?\" } }\nfunc f() string { var y Mk = M{c: 2}; return mk(ref y) }", "mk<Mk>: a value of Mk takes the arm Mk, whose function Make takes no value"},
		{"trait Sh { func (s ref Self) Show() string }\nstruct T1 impl Sh { c int }\nfunc (t ref T1) Show() string { return \"t\" }\nfunc show<T>(v ref T) string { return match T { Sh => v.Show(), _ => \"?\" } }\nfunc f() string { var x Sh = T1{c: 1}; return show(ref x) }", ""},
		// A function of a generic type's instance is walked with the instance's arguments.
		{"struct G<A> { a A }\nfunc G<A>.Kind(v ref A) int { return refl.Show(v) }\nfunc f() int { return G<bool>.Kind(ref true) }", "Kind<bool>, refl.Show<bool>: no arm of the match on T takes bool"},
		// An arm may name the function's other type parameters; they are put in before it is chosen.
		{"func same<T, U>() int { return match U { []T => 1, T => 2, _ => 3 } }\nfunc f() int { return same<int, chan int>() }", ""},
	}
	for _, tt := range tests {
		info := &Info{}
		imp := &sourceImporter{srcs: map[string]string{"refl": reachLib}, loaded: map[string]*Package{}, info: info}
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

// TestReachDepth checks the bound on a chain of generic calls: a chain
// of distinct calls within it is followed to its end, where a mistake
// is found, and one past it is refused at the call it began from,
// naming its ends, rather than passed over.
func TestReachDepth(t *testing.T) {
	chain := func(n int) string {
		var b strings.Builder
		b.WriteString("package main\n")
		for i := 0; i < n-1; i++ {
			b.WriteString("func f" + strconv.Itoa(i) + "<T>(v ref T) { f" + strconv.Itoa(i+1) + "(v) }\n")
		}

		b.WriteString("func f" + strconv.Itoa(n-1) + "<T>(v ref T) { match T { int => print(1) } }\n")
		b.WriteString("func main() { f0(ref \"s\") }\n")
		return b.String()
	}

	for _, tt := range []struct {
		n    int
		want string
	}{
		{maxChain, "f" + strconv.Itoa(maxChain-1) + "<string>: no arm of the match on T takes string"},
		{maxChain + 1, "f0<string>, ..., f" + strconv.Itoa(maxChain-1) + "<string>, f" + strconv.Itoa(maxChain) + "<string>: the calls of generic functions go deeper than " + strconv.Itoa(maxChain) + ", more than the compiler follows"},
	} {
		f, file, err := parser.ParseFile("test.snello", []byte(chain(tt.n)))
		if err != nil {
			t.Fatal(err)
		}

		_, err = libraryConfig().Check("", file, f, &Info{}) // the tables a build keeps, which the walk answers for
		errs, _ := err.(scanner.ErrorList)
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), tt.want) {
			got := ""
			if err != nil {
				got = err.Error()
			}

			t.Errorf("a chain of %d calls:\n  got  %q\n  want one error containing %q", tt.n, got, tt.want)
		}
	}
}
