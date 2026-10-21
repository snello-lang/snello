// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"strings"
	"testing"

	"github.com/teak-lang/teak/compiler/parser"
	"github.com/teak-lang/teak/compiler/scanner"
	"github.com/teak-lang/teak/compiler/types"
)

// TestPackages loads every package of the library, so that a package
// that stops compiling fails here and not in the first program that
// imports it.
func TestPackages(t *testing.T) {
	paths := Paths()
	if len(paths) == 0 {
		t.Fatal("no packages embedded")
	}

	for _, path := range paths {
		if path == "builtin" {
			continue // documentation of the predeclared names, never imported: TestBuiltin reads it
		}

		imp := NewImporter(&types.Info{})
		pkg, err := imp.Import(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}

		if pkg.Path() != path || pkg.Name() != path[strings.LastIndex(path, "/")+1:] {
			t.Errorf("%s: loaded as %s %q", path, pkg.Name(), pkg.Path())
		}

		if got := imp.Packages(); len(got) == 0 || got[len(got)-1].Pkg != pkg {
			t.Errorf("%s: not the last package loaded", path)
		}
	}
}

// TestImports checks a program against the library: what an import
// gives it, and the messages for a package or a name it cannot have.
func TestImports(t *testing.T) {
	tests := []struct {
		src  string
		want []string // one substring per expected error, in position order
	}{
		{"import \"math\"\nfunc f(x f64) f64 { return math.Sqrt(x) * math.Pi + math.Abs(x) + math.Min(x, 1) + math.Max(x, 1) }", nil},
		{"import m \"math\"\nfunc f(x f64) f64 { return m.Sqrt(x) }", nil},
		{"import \"math\"\nfunc f() f64 { return math.Tau }", []string{"3:28: undefined: math.Tau"}},
		{"import \"math\"\nfunc f() f64 { return math.Sqrt(2, 3) }", []string{"3:32: wrong number of arguments: have 2, want 1"}},
		{"import \"heap\"\nfunc f() { heap.New() }", []string{"2:8: package heap is not in the standard library"}}, // the use is not reported again
		{"import \"builtin\"\nfunc f() int { return len(\"x\") }", []string{"2:8: package builtin is not imported: its names are predeclared"}},
		{"import \"fmt\"\nfunc f() { fmt.Println(1) }", nil}, // the compiler provides fmt
		{"import (\"io\"; \"os\")\nfunc f(p ref string) Result<i64, io.Error> { in := os.Open(p)?; out := os.Create(p)?; n := io.Copy(mut out, mut in)?; out.Close()?; return Ok(n) }", nil},
		{"import \"os\"\nfunc f() { os.Stdout.WriteString(\"x\").Expect(\"write\"); print(len(os.Args)); os.Exit(1) }", nil}, // Exit is the compiler's, the rest the package's
		{"import \"os\"\nfunc f(p ref string) int { fd := 0; return os.open(p, mut fd) }", []string{"3:47: name open not exported by package os"}},
		{"import \"io\"\nfunc f(e io.Error) io.Kind { return e.Kind() }", nil},
		{"import \"io\"\nfunc f() io.Error { return io.Error{Op: \"open\", Path: \"p\", Msg: \"m\", kind: Other} }", []string{"3:70: kind of io.Error is not exported", "3:76: undefined: Other"}},
		{"import (\"io\"; \"os\")\nfunc f(p ref string) Result<string, error> { return Ok(os.ReadToString(p)?) }", []string{"? cannot pass on an error of type io.Error from a function returning Result<string, error>"}},
		{"import (\"strconv\"; \"strings\")\nfunc f(xs ref []string) Result<int, strconv.Error> { var b strings.Builder; b.WriteString(strings.Join(xs, \",\")); b.WriteByte('!'); return strconv.Atoi(b.String()) }", nil},
		{"import \"strings\"\nfunc f(s ref string) int { n := 0; for w := range strings.Fields(s) { n += w.Len() }; return n }", nil}, // Fields yields borrows into s
		{"import \"strconv\"\nfunc f(s ref string) int { v := 0.0; return strconv.parseFloat(s, mut v) }", []string{"name parseFloat not exported by package strconv"}},
		{"import \"strconv\"\nfunc f(e strconv.Error) string { return e.Func.Clone() + e.Error() }", nil},
		{"import (\"fmt\"; \"os\")\nfunc f(n int) { fmt.Fprintln(mut os.Stderr, \"n\", n); fmt.Fprintf(mut os.Stdout, \"%d\\n\", n) }", nil},
		{"import (\"fmt\"; \"os\")\nfunc f() { fmt.Fprintln(os.Stderr, 1) }", []string{"3:25: cannot use Stderr (os.File) as mut os.File in argument"}},
		{"import \"fmt\"\nfunc f(n int) { fmt.Fprintln(mut n, 1) }", []string{"3:17: cannot use int as the type parameter W of fmt.Fprintln: int does not implement Writer"}},
		{"import (\"fmt\"; \"io\")\nfunc f(e mut io.Error) { fmt.Fprintln(mut e, 1) }", []string{"3:26: cannot use io.Error as the type parameter W of fmt.Fprintln: io.Error does not implement Writer"}},
		{"func f(args ...ref any) {}", []string{"2:13: a variadic parameter is not supported yet"}}, // the library's alone
		{"import \"arena\"\nfunc f() { a := arena.New<string>(); i := a.Insert(\"x\"); if s := a.Get(i) { print(s.Len()) }; if s := a.GetMut(i) { *s = \"y\" }; print(a.Remove(i).IsSome(), a.Len()) }", nil},
		{"import \"arena\"\nfunc f(i arena.Index<int>) bool { j := i; m := map[arena.Index<int>]int{}; m[i] = 1; return i == j && len(m) == 1 }", nil}, // Index is Copy, Eq and Hash
		{"import \"arena\"\nfunc f(a ref arena.Arena<string>, i arena.Index<int>) bool { return a.Get(i).IsSome() }", []string{"3:75: cannot use i (arena.Index<int>) as arena.Index<string> in argument"}},
		{"import \"arena\"\nfunc f(a ref arena.Arena<string>, i arena.Index<string>) bool { return a.live(i) }", []string{"3:74: live of arena.Arena<string> is not exported"}},
		{"import \"arena\"\nfunc f() int { a := arena.New(); return a.Len() }", []string{"3:21: cannot infer the type parameter T of arena.New: give it, arena.New<...>"}},
		{"import \"rc\"\nfunc f() int { a := rc.New(\"x\"); b := a.Clone(); w := a.Downgrade(); if s := a.GetMut() { *s = \"y\" }; n := a.Get().Len() + b.StrongCount() + a.WeakCount(); if up := w.Upgrade() { n += up.Get().Len() }; return n }", nil},
		{"import \"rc\"\nfunc dup<T: Clone>(x ref T) T { return x.Clone() }\nfunc f(r ref rc.Rc<int>) rc.Rc<int> { return dup(r) }", nil},             // Rc<T> is Clone for any T
		{"import \"rc\"\nfunc f() int { a := rc.New(1); b := a; return *a.Get() + *b.Get() }", []string{"3:48: use of moved value a; moved at 3:37"}}, // a handle is moved, not copied
		{"import \"rc\"\nfunc f(r ref rc.Rc<int>) ref int { return r.Get() }", nil},                                                                   // the borrow follows the receiver
		{"import \"rc\"\nfunc f(r ref rc.Rc<int>) rc.Rc<int> { if p := ref r.p { return rc.Rc<int>{p: Some(rc.alias(p))} }; panic(\"gone\") }", []string{"3:53: p of rc.Rc<int> is not exported", "3:75: p of rc.Rc<int> is not exported", "3:86: name alias not exported by package rc"}},
		{"import (\"bufio\"; \"fmt\"; \"io\"; \"os\")\nfunc f(src mut os.File) Result<i64, io.Error> { w := bufio.NewWriter(mut os.Stdout); fmt.Fprintln(mut w, 1); r := bufio.NewReader(mut src); n := io.Copy(mut w, mut r)?; w.Flush()?; return Ok(n) }", nil}, // a borrow-holding writer passed to the generic functions that only borrow it
		{"import (\"bufio\"; \"os\")\nfunc f(src mut os.File) int { sc := bufio.NewScanner(mut src); n := 0; for sc.Scan() { n += len(sc.Bytes()) + sc.Text().Len() }; return n }", nil},
		{"import (\"bufio\"; \"os\")\nfunc f() { w := bufio.NewWriter(os.Stdout); w.Flush() }", []string{"3:33: cannot use Stdout (os.File) as mut os.File in argument"}},
		{"import (\"bufio\"; \"os\")\nfunc f(ws mut []bufio.Writer<os.File>) {}", []string{"3:15: a slice cannot hold Writer: it holds a borrow"}},
		{"import (\"bufio\"; \"os\")\nfunc f(src mut os.File) int { sc := bufio.NewScanner(mut src); sc.Scan(); line := sc.Bytes(); sc.Scan(); return len(line) }", []string{"cannot borrow sc as mut: sc is borrowed as ref"}},
		{"func f(x any) {}", []string{"2:10: any is only the type of a variadic parameter, ...ref any; a value of any type is a trait value"}},
		{"import \"os\"\nfunc f(r Result<int, string>) int { return match r { Ok(n) => n, Err(_) => os.Exit(2) } }", nil}, // Exit never returns, so the arm needs no value
		{"import \"io\"\nfunc f(k io.Kind) int { return match k { NotExist => 1, io.Exists => 3, _ => 0 } }", nil},        // variants of another package's enum, bare and qualified
		{"import \"io\"\nfunc f(k io.Kind) bool { return k == io.NotExist }", nil},
		{"import \"io\"\nfunc f(k io.Kind) int { return match k { Missing => 1, _ => 0 } }", nil}, // a name that is no variant of Kind binds, as any name would
		{"import \"io\"\nfunc f() io.Kind { return io.Kind }", []string{"3:27: io.Kind (type) is not an expression"}},
		{"struct T {}\nfunc f() int { x := T; return T }", []string{"3:21: T (type) is not an expression", "3:31: T (type) is not an expression"}},
		{"import \"sort\"\nfunc f(xs mut []int, ws mut []string) int { sort.Sort(mut xs); sort.Sort(mut ws); return sort.Search(ref xs, 3) }", nil},
		{"import \"sort\"\nstruct P { x int }\nfunc f(ps mut []P) { sort.Sort(mut ps) }", []string{"cannot use P as the type parameter T of sort.Sort: P does not implement Ord"}},
		{"import \"sort\"\nstruct P { x int }\nfunc f(ps mut []P) { sort.SortBy(mut ps, func(a ref P, b ref P) bool { return a.x < b.x }) }", nil},
		{"import \"time\"\nfunc f() time.Duration { time.Sleep(5 * time.Millisecond); return time.Since(time.Now()) + 2*time.Second }", nil},
		{"import \"time\"\nfunc f(t time.Time) bool { return t.Add(time.Minute).After(t) && t.Sub(t) == 0 }", nil},
		{"import \"time\"\nfunc f(t time.Time) i64 { return t.sec }", []string{"sec of time.Time is not exported"}},
		{"func Sqrt(x f64) f64\nfunc f() f64 { return Sqrt(2) }", []string{"2:6: missing function body"}},
		// A borrow a function of another package returns is tied to the
		// argument it borrows, as one of the program's own is.
		{"import (\"bufio\"; \"os\")\nfunc f(file mut os.File) { r := bufio.NewReader(mut file); file.WriteString(\"x\").Expect(\"w\"); print(r.ReadByte()) }", []string{"cannot borrow file as mut: file is already borrowed as mut by r"}},
		{"import (\"bufio\"; \"os\")\nfunc f(file mut os.File) { r := bufio.NewReader(mut file); r.Get().WriteString(\"x\").Expect(\"w\"); print(r.ReadByte()) }", nil}, // through the Reader that holds it
		{"import (\"bufio\"; \"os\")\nfunc f(file mut os.File) { w := bufio.NewWriter(mut file); w.Get().WriteString(\"x\").Expect(\"w\"); w.Flush().Expect(\"f\") }", nil},
		{"import (\"bufio\"; \"os\")\nfunc f(file mut os.File) { r := bufio.NewReader(mut file); print(r.ReadByte()); file.WriteString(\"x\").Expect(\"w\") }", nil},                                              // the Reader's last use ends the borrow
		{"import (\"rc\"; \"sync\")\nfunc f() { m := sync.NewMutex(rc.New(1)); print(m) }", []string{"3:17: cannot use rc.Rc<int> as the type parameter T of sync.NewMutex: rc.Rc<int> does not implement Send"}}, // a lock holds only what may cross to another task
		{"import (\"rc\"; \"sync\")\nfunc f() { l := sync.NewRwLock(rc.New(1)); print(l) }", []string{"3:17: cannot use rc.Rc<int> as the type parameter T of sync.NewRwLock: rc.Rc<int> does not implement Send"}},
		{"import \"sync\"\nfunc f() { a := sync.NewArc(sync.NewMutex(make(chan int))); print(a) }", nil}, // a channel handle is Send, and a Mutex of a Send value is
	}
	for _, tt := range tests {
		f, file, err := parser.ParseFile("t.teak", []byte("package p\n"+tt.src+"\n"))
		if err != nil {
			t.Fatalf("%q: parse error: %v", tt.src, err)
		}

		info := &types.Info{}
		conf := types.Config{Importer: NewImporter(info)}
		_, err = conf.Check("", file, f, info)
		var got []string
		if list, ok := err.(scanner.ErrorList); ok {
			for _, e := range list {
				got = append(got, e.Error())
			}
		}

		if len(got) != len(tt.want) {
			t.Errorf("%q: got %d errors %q, want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i, w := range tt.want {
			if !strings.Contains(got[i], w) {
				t.Errorf("%q: error %d = %q, want it to contain %q", tt.src, i, got[i], w)
			}
		}
	}
}
