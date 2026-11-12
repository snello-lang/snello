// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package backendtest holds the programs every backend's tests build
// and run, with what each must print, so that the C backend and the
// LLVM backend are held to one list and cannot drift apart: the exit
// codes, the panics and the drops they run, the misuses of channels and
// locks, the timers, the scheduler, and the constructs a backend must
// refuse by name rather than lower wrong. Each backend's test file
// supplies only how it builds and runs a program.
package backendtest

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// A Program is a Snello program and what running it must give: its
// standard output exactly, unless AnyOut says the output varies; text
// its standard error contains; its exit code. It runs as a release
// build and as a debug build, unless ReleaseOnly says the debug build
// would only slow it, and only on a machine with at least MinCPUs
// cores.
type Program struct {
	Name        string
	Src         string
	Out         string
	AnyOut      bool
	Stderr      string
	Code        int
	ReleaseOnly bool
	MinCPUs     int
}

// Builds returns the builds the program runs in: release, and debug
// unless ReleaseOnly.
func (p Program) Builds() []bool {
	if p.ReleaseOnly {
		return []bool{false}
	}

	return []bool{false, true}
}

// Check returns what is wrong with a run that printed out and stderr
// and exited with code, or "" when the run is what the program must
// give.
func (p Program) Check(out, stderr string, code int) string {
	switch {
	case code != p.Code:
		return "exit code " + strconv.Itoa(code) + ", want " + strconv.Itoa(p.Code) + "; stderr: " + strconv.Quote(stderr)
	case !p.AnyOut && out != p.Out:
		return "output " + strconv.Quote(out) + ", want " + strconv.Quote(p.Out) + "; stderr: " + strconv.Quote(stderr)
	case !strings.Contains(stderr, p.Stderr):
		return "stderr " + strconv.Quote(stderr) + ", want it to contain " + strconv.Quote(p.Stderr)
	}

	return ""
}

// Skip returns why the program cannot run on this machine, or "".
func (p Program) Skip() string {
	if runtime.NumCPU() < p.MinCPUs {
		return "needs " + strconv.Itoa(p.MinCPUs) + " cores"
	}

	return ""
}

const guard = `package main
import "fmt"
struct Guard impl Drop { name string }
func (g mut Guard) Drop() { fmt.Println("drop", g.name) }
func take(g Guard) { fmt.Println("took", g.name) }
func name(g ref Guard) string { return g.name.Clone() }
`

const tasks = "package main\nimport (\"fmt\"; \"rc\"; \"sync\"; \"time\")\n" +
	"func closer(ch chan int) { time.Sleep(10 * time.Millisecond); close(ch) }\n" +
	"func boom() { panic(\"in a task\") }\n"

// Programs returns the programs both backends run. short asks for the
// smaller version of the one that costs the most, the parked tasks.
func Programs(short bool) []Program {
	programs := []Program{
		{
			Name: "dead",
			Src:  "package main\nimport \"fmt\"\nfunc main() {\n\tfmt.Println(\"before\")\n\treturn\n\tfmt.Println(\"after\")\n}\n",
			Out:  "before\n",
		},
		{
			Name: "exit", // os.Exit ends the program with its code after flushing what was printed
			Src:  "package main\nimport (\"fmt\"; \"os\")\nfunc main() { fmt.Println(\"bye\"); os.Exit(3); fmt.Println(\"not reached\") }\n",
			Out:  "bye\n", Code: 3, ReleaseOnly: true,
		},
		{
			Name: "index", // a checked index; a match through a mut borrow of an element writes the element
			Src: `package main
import "fmt"
enum Slot { Full(n int); Vacant }
func bump(xs mut []Slot) { match mut xs[0] { Full(n) => *n += 1, Vacant => {} } }
func main() {
	xs := []Slot{Full(1)}
	bump(mut xs)
	fmt.Println(xs)
	i := len(xs) // one past the end: the boundary the check must catch
	fmt.Println(xs[i])
}
`,
			Out: "[Full(2)]\n", Stderr: "panic: index out of range", Code: 2, ReleaseOnly: true,
		},
		{
			Name: "subslice-push", // a sub-slice lends its slice's buffer: growing it would free the middle of a block
			Src: `package main
import "fmt"
func grow(xs mut []int) { xs.Push(100) }
func main() {
	xs := []int{1, 2, 3, 4}
	fmt.Println(cap(xs[1:3]))
	grow(mut xs[2:4])
	fmt.Println(xs)
}
`,
			Out: "2\n", Stderr: "panic: Push on a sub-slice: it lends the elements of its slice and cannot change its length", Code: 2,
		},
		{
			Name: "subslice-clear", // refused before an element is dropped, so the slice's own drop frees each once
			Src: `package main
import "fmt"
func empty(xs mut []string) { xs.Clear() }
func main() {
	xs := []string{"a".Clone(), "b".Clone(), "c".Clone()}
	empty(mut xs[0:2])
	fmt.Println(xs)
}
`,
			Stderr: "panic: Clear on a sub-slice", Code: 2,
		},
		{
			Name: "subslice-replace",
			Src: `package main
import "fmt"
func replace(xs mut []string) { *xs = []string{"z".Clone()} }
func main() {
	xs := []string{"a".Clone(), "b".Clone(), "c".Clone()}
	replace(mut xs[1:3])
	fmt.Println(xs)
}
`,
			Stderr: "panic: replacing on a sub-slice", Code: 2,
		},
		{
			Name: "subslice-in-place", // what a sub-slice may do: its elements, in place
			Src: `package main
import "fmt"
func bump(xs mut []int) { for i := 0; i < len(xs); i++ { xs[i] += 10 } }
func order(xs mut []int) { xs.Sort() }
func flip(xs mut []int) { xs.Reverse() }
func names(ws mut []string) { ws.Sort() }
func main() {
	xs := []int{5, 4, 3, 2, 1}
	bump(mut xs[0:2])
	order(mut xs[1:4])
	flip(mut xs[3:5])
	fmt.Println(xs, len(xs[1:4]), cap(xs[1:4]))
	ws := []string{"c".Clone(), "a".Clone(), "b".Clone()}
	names(mut ws[0:2])
	fmt.Println(ws)
}
`,
			Out: "[15 2 3 1 14] 3 3\n[a c b]\n",
		},
		{
			Name: "map-strided-keys", // keys apart only in their high bits: without the hash's finish they share a few slots, and this takes minutes
			Src: `package main
import "fmt"
func main() {
	m := map[int]int{}
	for i := 0; i < 300000; i++ {
		m[i * 65536] = i
	}

	found := 0
	for i := 0; i < 300000; i++ {
		if v := m.Get(i * 65536) {
			if *v == i {
				found++
			}
		}
	}

	fmt.Println(len(m), found)
}
`,
			Out: "300000 300000\n", ReleaseOnly: true,
		},
		{
			Name: "timers", // After and Tick are entries in the runtime's timer heap, not tasks
			Src: `package main
import ("fmt"; "time")
func main() {
	select {
	case <-time.After(time.Duration(9223372036854775807)):
		fmt.Println("the longest After fired") // its deadline wrapped round
	case <-time.After(10 * time.Millisecond):
		fmt.Println("deadline")
	}

	select {
	case <-time.Tick(0):
		fmt.Println("Tick(0) ticked")
	case <-time.Tick(-time.Second):
		fmt.Println("a negative Tick ticked")
	case <-time.After(20 * time.Millisecond):
		fmt.Println("no tick")
	}

	pending := []<-chan time.Time{}
	for i := 0; i < 50000; i++ {
		pending.Push(time.After(time.Hour)) // as tasks, a hundred thousand mappings: past Linux's limit
	}

	fmt.Println(len(pending))
}
`,
			Out: "deadline\nno tick\n50000\n",
		},
		{
			Name: "net-stale-wakes", // a wake left over from a select must not end a socket's wait early, nor reach a task that has gone
			Src: `package main
import ("fmt"; "net")
func serve(l net.Listener, n int) {
	for i := 0; i < n; i++ {
		c := l.Accept().Expect("accept")
		buf := make([]byte, 4)
		got := c.Read(mut buf).Expect("read")
		c.Write(ref buf[0:got]).Expect("write")
	}
}
func main() {
	l := net.Listen("127.0.0.1", 0).Expect("listen")
	port := l.Port()
	n := 200
	spawn serve(l, n)
	ok := 0
	for i := 0; i < n; i++ {
		a := make(chan int)
		b := make(chan int)
		a2, b2 := a.Clone(), b.Clone()
		spawn func() { a2 <- 1 }()
		spawn func() { b2 <- 2 }()
		select { // both may wake it before it leaves: the second wake is left over for the dial
		case <-a:
			<-b
		case <-b:
			<-a
		}

		c := net.Dial("127.0.0.1", port).Expect("dial")
		c.WriteString("ping").Expect("write")
		buf := make([]byte, 4)
		if c.Read(mut buf).Expect("read") == 4 {
			ok++
		}
	}

	fmt.Println(ok)
}
`,
			Out: "200\n",
		},
		{
			Name: "net-stale-promise", // a socket wait renewed after a stale wake must not promise the deadlock detector a second wake that never comes
			Src: `package main
import ("fmt"; "net")
func serve(l net.Listener, fire chan ()) {
	c := l.Accept().Expect("accept")
	for i := 0; i < 20000; i++ {
		<-fire
		c.WriteString("x").Expect("write")
	}
}
func main() {
	l := net.Listen("127.0.0.1", 0).Expect("listen")
	port := l.Port()
	fire := make(chan (), 1)
	spawn serve(l, fire.Clone())
	c := net.Dial("127.0.0.1", port).Expect("dial")
	buf := make([]byte, 1)
	for i := 0; i < 20000; i++ {
		a := make(chan int, 1)
		b := make(chan int, 1)
		a2, b2 := a.Clone(), b.Clone()
		spawn func() { a2 <- 1 }()
		spawn func() { b2 <- 2 }()
		select { // the second sender's wake may be left over: the read's first wait then ends at once and the next renews it
		case <-a:
			<-b
		case <-b:
			<-a
		}

		fire <- () // the peer writes only once asked, so the read has to wait for it
		c.Read(mut buf).Expect("read")
	}

	fmt.Println("done")
	never := make(chan int)
	<-never
}
`,
			Out: "done\n", Stderr: "fatal error: all tasks are asleep - deadlock!", Code: 2,
		},
		{
			Name: "lookup-promises", // every lookup takes back the wake it promised the deadlock detector, whether it finished, timed out or raced its timer, so a program that deadlocks after lookups is reported, not left hanging
			Src: `package main
import ("fmt"; "net"; "time")
func look(done chan int) {
	n := 0
	for i := 0; i < 50; i++ {
		_ = net.LookupHost("localhost")
		_ = net.DialTimeout("localhost", 1, time.Duration(i % 10) * time.Duration(100) * time.Microsecond + time.Nanosecond)
		n += 1
	}

	done <- n
}
func main() {
	done := make(chan int)
	for i := 0; i < 4; i++ {
		spawn look(done.Clone())
	}

	total := 0
	for i := 0; i < 4; i++ {
		total += <-done ?? 0
	}

	fmt.Println(total)
	never := make(chan int)
	<-never
}
`,
			Out: "200\n", Stderr: "fatal error: all tasks are asleep - deadlock!", Code: 2,
		},
		{
			Name: "sleep-under-load", // a sleep ends on time while every worker has two tasks trading messages through its run-next slot: the slot's task yields to the queue
			Src: `package main
import ("fmt"; "os"; "time")
func echo(in chan int, out chan int) {
	for v := range in {
		out <- v
	}
}
func pinger(n int, done chan ()) {
	in := make(chan int)
	out := make(chan int)
	spawn echo(in.Clone(), out.Clone())
	for i := 0; i < n; i++ {
		in <- i
		<-out
	}
	close(in)
	done <- ()
}
func main() {
	cores := os.Cores()
	done := make(chan (), cores)
	for w := 0; w < cores; w++ {
		spawn pinger(1500000, done.Clone())
	}
	start := time.Now()
	for i := 0; i < 20; i++ {
		time.Sleep(time.Millisecond)
	}
	took := time.Since(start)
	fmt.Println(took < 200*time.Millisecond) // twenty sleeps of a millisecond: about 25 ms; starved by the pairs, over 400
	for w := 0; w < cores; w++ {
		<-done
	}
}
`,
			Out: "true\n", ReleaseOnly: true, MinCPUs: 2,
		},
		{
			Name: "pad-lent", // a width pads a lent string, a variable, a field or a ref parameter, into a string of its own: the caller keeps its bytes
			Src: `package main
import "fmt"
struct Row { name string }
func show(s ref string) { fmt.Printf("[%3s][%-3s]\n", s, s) }
func padded() string {
	s := "hello".ToUpper()
	return fmt.Sprintf("%2s", s)
}
func main() {
	fmt.Printf("[%3s][%-3s]\n", "temp".ToUpper(), "temp".ToUpper())
	s := "hello world".ToUpper()
	fmt.Printf("[%3s][%-3s]\n", s, s)
	fmt.Println(s)
	r := Row{name: "field".ToUpper()}
	fmt.Printf("[%3s][%-3s]\n", r.name, r.name)
	fmt.Println(r.name)
	show(ref s)
	fmt.Println(s)
	fmt.Println(padded())
	fmt.Printf("[%5s][%.3s][%5.3s]\n", s, s, s)
}
`,
			Out: "[TEMP][TEMP]\n[HELLO WORLD][HELLO WORLD]\nHELLO WORLD\n[FIELD][FIELD]\nFIELD\n[HELLO WORLD][HELLO WORLD]\nHELLO WORLD\nHELLO\n[HELLO WORLD][HEL][  HEL]\n",
		},
		{
			Name: "repeat-negative", // None, before anything is built: the library does not stop the program over a caller's count
			Src: `package main
import ("fmt"; "strings")
func main() {
	n := -1
	fmt.Println(strings.Repeat("ab", n).IsNone(), strings.Repeat("ab", n) ?? "none")
}
`,
			Out: "true none\n",
		},
		{
			Name: "repeat-overflow", // a result longer than an int can count is None, and nothing is allocated for it
			Src: `package main
import ("fmt"; "strings")
func main() {
	n := 4611686018427387904
	fmt.Println(strings.Repeat("ab", n).IsNone())
}
`,
			Out: "true\n",
		},
		{
			Name: "slice-low-past-high", // the slice bounds in Go's runtime words
			Src: `package main
import ("fmt")
func main() {
	xs := []int{1, 2, 3}
	lo, hi := 3, 1
	fmt.Println(xs[lo:hi])
}
`,
			Stderr: "panic: slice bounds out of range [3:1]", Code: 2,
		},
		{
			Name: "slice-past-length", // the high bound is reported first
			Src: `package main
import ("fmt")
func main() {
	s := "abc"
	hi := 10
	fmt.Println(s[1:hi])
}
`,
			Stderr: "panic: slice bounds out of range [:10] with length 3", Code: 2,
		},
		{
			Name: "insert-past-length", // Go's slices.Insert
			Src: `package main
import ("fmt")
func main() {
	xs := []int{1, 2, 3}
	xs.Insert(7, 0)
	fmt.Println(xs)
}
`,
			Stderr: "panic: slice bounds out of range [7:3]", Code: 2,
		},
		{
			Name: "make-length", // makeslice, as Go's runtime names it
			Src: `package main
import ("fmt")
func main() {
	n := -1
	fmt.Println(make([]int, n))
}
`,
			Stderr: "panic: makeslice: len out of range", Code: 2,
		},
		{
			Name: "make-capacity", // a capacity below the length
			Src: `package main
import ("fmt")
func main() {
	n, c := 3, 1
	fmt.Println(make([]int, n, c))
}
`,
			Stderr: "panic: makeslice: cap out of range", Code: 2,
		},
		{
			Name: "make-capacity-too-large", // a capacity whose size in bytes passes what an address reaches is refused, as Go's makeslice refuses it: the product, wrapped, would be a block of eight bytes that a push writes past
			Src: `package main
import ("fmt")
func main() {
	c := 2305843009213693953
	xs := make([]int, 0, c)
	for i := 0; i < 1000; i++ { xs.Push(i) }
	fmt.Println(len(xs))
}
`,
			Stderr: "panic: makeslice: cap out of range", Code: 2,
		},
		{
			Name: "search-long-needle", // a needle that almost matches everywhere: comparing it at every offset takes a minute
			Src: `package main
import ("fmt"; "strings")
func main() {
	hay := strings.Repeat("a", 20000000) ?? ""
	needle := (strings.Repeat("a", 50000) ?? "") + "b"
	fmt.Println(hay.Contains(needle), (hay + "b").Contains(needle), hay.SplitOnce(needle).IsNone())
	back := "b" + (strings.Repeat("a", 50000) ?? "")
	tail, head := hay + "b", "b" + hay
	fmt.Println(hay.Index(needle), tail.Index(needle), hay.LastIndex(needle), tail.LastIndex(needle), head.LastIndex(back), hay.LastIndex(back))
	fmt.Println(hay.Count(needle), tail.Count(needle), hay.ReplaceAll(needle, "x").Len(), tail.ReplaceAll(needle, "x").Len())
	fmt.Println([]byte(tail).Find(ref []byte(needle)))
}
`,
			Out: "false true true\nNone Some(19950000) None Some(19950000) Some(0) None\n0 1 20000000 19950001\nSome(19950000)\n", ReleaseOnly: true,
		},
		{
			Name: "string-methods", // searches, counts, replacements, trims and SplitN on a string, at the edges: an empty needle, n of zero or less, an empty old, a split of nothing
			Src: `package main
import "fmt"
func main() {
	s := "go, snello, go"
	fmt.Println(s.Index("go"), s.LastIndex("go"), s.Index("rust"), s.LastIndex("rust"), s.Index(""), s.LastIndex(""))
	fmt.Println(s.Count("go"), s.Count("o"), s.Count(""), "héllo".Count(""), "aaaa".Count("aa"))
	fmt.Println(s.Replace("go", "Go", 1), "|", s.ReplaceAll("go", "Go"), "|", s.Replace("go", "x", 0), "|", s.Replace("go", "x", -1))
	fmt.Println("hé".ReplaceAll("", "-"), "abc".Replace("", "-", 2), "aaa".ReplaceAll("a", ""), "".ReplaceAll("", "x"))
	fmt.Println(s.TrimPrefix("go, "), "|", s.TrimSuffix(", go"), "|", s.TrimPrefix("zz"), "|", " \t pad \n".TrimSpace(), "|")
	fmt.Println(len(s.SplitN(", ", 0)), len(s.SplitN(", ", 1)), len(s.SplitN(", ", 2)), len(s.SplitN(", ", 9)), len("".SplitN(",", 3)), len("".SplitN("", 3)))
	for p := range s.SplitN(", ", 2) {
		fmt.Println("[" + p + "]")
	}

	for p := range "héllo".SplitN("", 3) {
		fmt.Println("[" + p + "]")
	}

	parts := s.Split(", ")
	fmt.Println(parts.Contains(ref "snello"), parts.Index(ref "go"), parts.Clone(), parts.Last())
}
`,
			Out: "Some(0) Some(12) None None Some(0) Some(14)\n2 3 15 6 2\nGo, snello, go | Go, snello, Go | go, snello, go | go, snello, go\n-h-é- -a-bc  x\nsnello, go | go, snello | go, snello, go | pad |\n0 1 2 3 1 0\n[go]\n[snello, go]\n[h]\n[é]\n[llo]\ntrue Some(0) [go snello go] Some(\"go\")\n",
		},
		{
			Name: "slice-methods", // Extend copying Copy elements and cloning the rest, Index, First and Last, Truncate dropping the tail first to last and doing nothing past the end, and []byte's Equal, Find and Index
			Src: `package main
import "fmt"
struct Guard impl Drop, Clone { name string }
func (g mut Guard) Drop() { fmt.Println("drop", g.name) }
func (g ref Guard) Clone() Guard { return Guard{name: g.name + "2"} }
struct Bump impl Clone { n int }
struct Wrap impl Copy, Clone { b Bump }
func (b ref Bump) Clone() Bump { return Bump{n: b.n + 100} }
type Fold u8 impl Eq
func (a ref Fold) Eq(b ref Fold) bool { return a.Lower() == b.Lower() }
func (a ref Fold) Lower() u8 {
	if *a >= 'A' && *a <= 'Z' {
		return u8(*a) + 32
	}

	return u8(*a)
}
func main() {
	xs := []int{3, 1, 4}
	xs.Extend(ref []int{1, 5})
	xs.Extend(ref []int{})
	var empty []int
	fmt.Println(xs, xs.Index(ref 4), xs.Index(ref 9), xs.First(), xs.Last(), empty.First(), empty.Last())
	xs.Truncate(9)
	fmt.Println(xs)
	xs.Truncate(2)
	fmt.Println(xs)
	xs.Truncate(-1)
	fmt.Println(xs, len(xs))
	gs := []Guard{Guard{name: "a"}}
	more := []Guard{Guard{name: "b"}, Guard{name: "c"}}
	gs.Extend(ref more)
	fmt.Println(len(gs), gs[1].name, gs[2].name, more[0].name)
	gs.Truncate(1)
	fmt.Println("kept", gs[0].name)
	bs := []byte("hello world")
	fmt.Println(bs.Equal(ref []byte("hello world")), bs.Equal(ref []byte("hello")), bs.Find(ref []byte("world")), bs.Find(ref []byte("xyz")), bs.Find(ref []byte("")), bs.Index(ref 'o'), bs.Index(ref 'z'))
	names := []string{"ann", "bob"}
	fmt.Println(names.Index(ref "bob"), names.Index(ref "cy"))
	folds := []Fold{Fold('a'), Fold('B')}
	b := Fold('b')
	fmt.Println(folds.Contains(ref b), folds.Index(ref b)) // the type's own Eq, not the byte's
	bumps := []Bump{Bump{n: 1}}
	bumps.Extend(ref []Bump{Bump{n: 2}})
	fmt.Println(bumps[1].n, bumps.Clone()[0].n) // a Clone the type writes runs, Copy as it is
	wraps := []Wrap{Wrap{b: Bump{n: 3}}}
	wraps.Extend(ref []Wrap{Wrap{b: Bump{n: 4}}})
	fmt.Println(wraps[1].b.n, wraps.Clone()[0].b.n) // and a derived Clone calls it, inside a slice too
	var r Result<string, string> = Ok("ok".Clone())
	rs := []Result<string, string>{r}
	fmt.Println(rs.Clone())
	i := []int{1, 2, 3} // a variable named as a loop counter of the C backend might be
	i.Extend(ref []int{4})
	i.Truncate(2)
	fmt.Println(i)
	i.Clear()
	fmt.Println(len(i))
}
`,
			Out: "[3 1 4 1 5] Some(2) None Some(3) Some(5) None None\n[3 1 4 1 5]\n[3 1]\n[] 0\n3 b2 c2 b\ndrop b2\ndrop c2\nkept a\ntrue false Some(6) None Some(0) Some(4) None\nSome(1) None\ntrue Some(1)\n102 101\n104 103\n[Ok(\"ok\")]\n[1 2]\n0\ndrop b\ndrop c\ndrop a\n",
		},
		{
			Name: "map-option-result-methods", // GetOrInsert inserting once and dropping the key and value it does not need; AndThen, OrElse, Ok, Err and UnwrapOrElse, each dropping the half it does not give back
			Src: `package main
import "fmt"
struct Guard impl Drop, Clone { name string }
func (g mut Guard) Drop() { fmt.Println("drop", g.name) }
func (g ref Guard) Clone() Guard { return Guard{name: g.name + "2"} }
func main() {
	m := map[string]Guard{}
	m.GetOrInsert("k", Guard{name: "first"}).name = "first!"
	m.GetOrInsert("k", Guard{name: "second"}).name += "?"
	if g := m.Get("k") {
		fmt.Println(g.name, len(m))
	}

	counts := map[string]int{}
	for w := range "a b a c a".Fields() {
		*counts.GetOrInsert(w.Clone(), 0) += 1
	}

	fmt.Println(counts)
	o := Some(4)
	var none int? = None
	fmt.Println(o.AndThen(func(v int) int? { return Some(v * 2) }), none.AndThen(func(v int) int? { return Some(v) }), Some(1).OrElse(func() int? { return Some(9) }), none.OrElse(func() int? { return Some(9) }))
	var ok Result<Guard, string> = Ok(Guard{name: "val"})
	var bad Result<Guard, string> = Err("bad".Clone())
	fmt.Println(ok.Err(), bad.Err())
	var ok2 Result<int, Guard> = Ok(3)
	var bad2 Result<int, Guard> = Err(Guard{name: "err"})
	fmt.Println(ok2.Ok(), bad2.Ok())
	var r Result<int, string> = Ok(3)
	var e Result<int, string> = Err("bad".Clone())
	fmt.Println(r.AndThen(func(v int) Result<int, string> { return Ok(v + 1) }), e.AndThen(func(v int) Result<int, string> { return Ok(v + 1) }))
	var r2 Result<int, string> = Ok(3)
	var e2 Result<int, string> = Err("worse".Clone())
	fmt.Println(r2.UnwrapOrElse(func(s string) int { return len(s) }), e2.UnwrapOrElse(func(s string) int { return len(s) }))
}
`,
			Out: "drop second\nfirst!? 1\nmap[a:3 b:1 c:1]\nSome(8) None Some(1) Some(9)\ndrop val\nNone Some(\"bad\")\ndrop err\nSome(3) None\nOk(4) Err(\"bad\")\n3 5\ndrop first!?\n",
		},
		{
			Name: "integer-methods", // checked arithmetic at the bounds of int, i8 and u8, Abs leaving the minimum as it is, Clamp with lo above hi; and Eq on a tuple and an option, called through a bound
			Src: `package main
import "fmt"
func same<T: Eq>(a ref T, b ref T) bool { return a.Eq(b) }
func main() {
	big, small := 9223372036854775807, -9223372036854775807 - 1
	fmt.Println(big.CheckedAdd(1), big.CheckedSub(1), small.CheckedSub(1), small.CheckedAdd(1), big.CheckedMul(2))
	var a, b i8 = 100, -128
	fmt.Println(a.CheckedAdd(27), a.CheckedAdd(28), a.CheckedMul(2), b.CheckedSub(1), b.Abs(), a.Abs())
	var u, z u8 = 250, 0
	fmt.Println(u.CheckedAdd(5), u.CheckedAdd(6), z.CheckedSub(1), u.CheckedMul(1), u.Clamp(1, 100))
	n, neg := 15, -5
	fmt.Println(neg.Abs(), small.Abs(), n.Clamp(0, 10), neg.Clamp(0, 10), n.Clamp(10, 20), n.Clamp(20, 10))
	t1, t2 := (1, "a"), (1, "b")
	var o1, o2 int? = Some(1), None
	fmt.Println(same(ref t1, ref t1), same(ref t1, ref t2), same(ref o1, ref o1), same(ref o1, ref o2))
}
`,
			Out: "None Some(9223372036854775806) None Some(-9223372036854775807) None\nSome(127) None None None -128 100\nSome(255) None None Some(250) 100\n5 -9223372036854775808 10 0 15 10\ntrue false true false\n",
		},
		{
			Name: "math-functions", // every function of math on exact values and its special cases, an infinity, a NaN and a negative zero, the transcendental ones to six places since their last digit is the system's, the constants and the limits each of its own type, and a function kept as a value; the expected output is Go's
			Src: `package main
import ("fmt"; "math")
func main() {
	fmt.Println(math.Floor(-2.5), math.Ceil(-2.5), math.Trunc(-2.5), math.Round(-2.5), math.Round(2.5), math.Mod(7, 3), math.Mod(-7, 3))
	fmt.Printf("%.6f %.6f %.6f %.6f %.6f %.6f %.6f\n", math.Pow(2, 10), math.Exp(0), math.Log(1), math.Log2(8), math.Log10(1000), math.Cbrt(27), math.Hypot(3, 4))
	fmt.Println(math.Sqrt(16))
	fmt.Printf("%.6f %.6f %.6f %.6f %.6f %.6f %.6f\n", math.Sin(1), math.Cos(1), math.Tan(1), math.Asin(0.5), math.Acos(0.5), math.Atan(1), math.Atan2(1, -1))
	fmt.Printf("%.6f %.6f %.6f %.6f %.6f %.6f %.6f\n", math.Sinh(1), math.Cosh(1), math.Tanh(1), math.E, math.Ln2, math.Exp(1), math.Log(2))
	nan, inf := math.NaN(), math.Inf(1)
	fmt.Println(inf, math.Inf(-1), nan, math.IsNaN(nan), math.IsNaN(inf), math.IsInf(inf, 1), math.IsInf(inf, -1), math.IsInf(math.Inf(-1), 0), math.IsInf(math.MaxFloat64, 0))
	fmt.Println(math.Signbit(math.Copysign(0, -1)), math.Signbit(0), math.Copysign(3, -1), math.Sqrt(-1), math.Log(0), math.Log(-1), math.Pow(-8, 0.5), math.Mod(1, 0), math.Pow(nan, 0))
	fmt.Println(math.MaxInt, math.MinInt, math.MaxInt8, math.MinInt8, math.MaxInt16, math.MinInt16, math.MaxInt32, math.MinInt32, math.MaxInt64, math.MinInt64)
	fmt.Println(math.MaxUint, math.MaxUint8, math.MaxUint16, math.MaxUint32, math.MaxUint64, math.MaxFloat64, math.SmallestNonzeroFloat64, math.MaxFloat32, math.SmallestNonzeroFloat32)
	f := math.Floor
	fmt.Println(f(2.5), math.Floor(f64(math.MaxInt32) / 2))
}
`,
			Out: "-3 -2 -2 -3 3 1 -1\n1024.000000 1.000000 0.000000 3.000000 3.000000 3.000000 5.000000\n4\n0.841471 0.540302 1.557408 0.523599 1.047198 0.785398 2.356194\n1.175201 1.543081 0.761594 2.718282 0.693147 2.718282 0.693147\n+Inf -Inf NaN true false true false true false\ntrue false -3 NaN -Inf NaN NaN NaN 1\n9223372036854775807 -9223372036854775808 127 -128 32767 -32768 2147483647 -2147483648 9223372036854775807 -9223372036854775808\n18446744073709551615 255 65535 4294967295 18446744073709551615 1.7976931348623157e+308 5e-324 3.4028235e+38 1e-45\n2 1.073741823e+09\n",
		},
		{
			Name: "bit-functions", // every function of math/bits at each width, on zero, which gives the width, on a rotation by a negative count and by more than the width, through a value of the function, and as what a task runs and what a defer calls; the expected output is Go's
			Src: `package main
import ("fmt"; "math/bits")
func apply(f func(u64) int, x u64) int { return f(x) }
func main() {
	var a u8 = 0x81
	var b u16 = 0x1234
	var c u32 = 0xf000000f
	var d u64 = 0x123456789abcdef0
	var e uint = 0
	k := -3
	fmt.Println(bits.LeadingZeros8(a), bits.LeadingZeros16(b), bits.LeadingZeros32(c), bits.LeadingZeros64(d), bits.LeadingZeros(e), bits.LeadingZeros8(0), bits.LeadingZeros16(0), bits.LeadingZeros32(0), bits.LeadingZeros64(0))
	fmt.Println(bits.TrailingZeros8(a), bits.TrailingZeros16(b), bits.TrailingZeros32(c), bits.TrailingZeros64(d), bits.TrailingZeros(e), bits.TrailingZeros8(0), bits.TrailingZeros16(0), bits.TrailingZeros32(0), bits.TrailingZeros64(0))
	fmt.Println(bits.OnesCount8(a), bits.OnesCount16(b), bits.OnesCount32(c), bits.OnesCount64(d), bits.OnesCount(e), bits.Len8(a), bits.Len16(b), bits.Len32(c), bits.Len64(d), bits.Len(e))
	fmt.Println(bits.RotateLeft8(a, 1), bits.RotateLeft8(a, k), bits.RotateLeft16(b, 20), bits.RotateLeft32(c, k), bits.RotateLeft64(d, 64), bits.RotateLeft64(d, -65), bits.RotateLeft(e + 1, 63 - k))
	fmt.Println(bits.Reverse8(a), bits.Reverse16(b), bits.Reverse32(c), bits.Reverse64(d), bits.Reverse(e + 1))
	f := bits.OnesCount64
	fmt.Println(apply(f, d), apply(bits.Len64, d), apply(bits.TrailingZeros64, 0))
	spawn bits.OnesCount8(a)
	defer bits.Len16(b)
}
`,
			Out: "0 3 0 3 64 8 16 32 64\n0 2 0 4 64 8 16 32 64\n2 5 8 32 0 8 13 32 61 0\n3 48 9025 4261412865 1311768467463790320 655884233731895160 4\n129 11336 4026531855 1115552785675988040 9223372036854775808\n32 61 64\n",
		},
		{
			Name: "random-numbers", // a generator seeded with 1 and 2 gives the numbers Go's PCG gives for that seed: Int, Int64, Float64, IntN below and above 2^32 and at a power of two, a shuffle of strings and a permutation; a bound with nothing below it gives None and a count below zero none; the package functions give numbers in range
			Src: `package main
import ("fmt"; "math/rand")
func main() {
	r := rand.New(1, 2)
	fmt.Println(r.Int(), r.Int64(), r.Float64(), r.IntN(10), r.IntN(1000000007), r.IntN(1099511627776), r.IntN(4294967297))
	xs := []string{"a".Clone(), "b".Clone(), "c".Clone(), "d".Clone(), "e".Clone()}
	r.Shuffle(mut xs)
	fmt.Println(xs, r.Perm(6), r.Perm(0), r.Perm(-1), r.IntN(0), r.IntN(-1))
	sum := 0
	for i := 0; i < 1000; i++ { sum += (r.IntN(6) ?? 0) + 1 }
	g := rand.IntN(6) ?? -1
	p := rand.Perm(5)
	rand.Shuffle(mut p)
	fmt.Println(sum, g >= 0 && g < 6, len(p), rand.Float64() < 1.0, rand.Int() >= 0, rand.Int64() >= 0, rand.IntN(0))
}
`,
			Out: "4969059760275911952 2147869220224756844 0.5085473976760264 Some(7) Some(234276272) Some(547831175868) Some(2147104640)\n[e d a b c] [0 5 3 1 4 2] [] [] None None\n3558 true 5 true true true None\n",
		},
		{
			Name: "multiply-then-add", // a product and a sum are two operations, each rounded: a compiler that joined them into one instruction would round once and print other last digits, and the two backends would differ; the values come from the arguments' count, so nothing is worked out when the program is compiled; and a literal beside an f32 is an f32, so the sum is rounded as an f32 at each step and not worked out wider
			Src: `package main
import ("fmt"; "math"; "os")
func main() {
	n := f64(len(os.Args))
	a, b, c := 1.0 + n * 1e-8, 1.0 - n * 1e-8, -1.0
	var x, y, z f32 = 1.0 + f32(n) * 1e-4, 1.0 - f32(n) * 1e-4, -1.0
	fmt.Println(a * b + c, c + a * b, a * b - 1.0, math.Sqrt(a) * math.Sqrt(a) - a, x * y + z)
	var f f32 = f32(n) * 0.1
	var h f32 = 16777216.0 * f32(n)
	fmt.Println(h + 1.0 + 1.0, h + 1.0 == h, f + 0.1 == 0.2, f64(f * 1.1), f / 3.0, f - 0.3)
}
`,
			Out: "-2.220446049250313e-16 -2.220446049250313e-16 -2.220446049250313e-16 0 0\n1.6777216e+07 true true 0.11000000685453415 0.033333335 -0.20000002\n",
		},
		{
			Name: "narrow-integers", // arithmetic on an integer type narrower than 64 bits wraps at the type's width wherever its result is read, in a comparison, a conversion, an argument, an index and a division, and not only where it is stored; negation and the bitwise not wrap too; a shift by the width or more gives zero, or minus one for a negative number shifted right, at every width; the expected output is Go's
			Src: `package main

import ("fmt"; "os")

const Max i8 = 127

func show(a int, b int, c int) { fmt.Println(a, b, c) }

func main() {
    n := len(os.Args)
    var a i8 = 127
    var b i8 = -128
    var u u8 = 255
    var w u16 = 65535
    var s i16 = 32767
    var x u32 = 4294967295
    var y i32 = 2147483647
    var d u64 = 1
    var e i64 = -8
    one := i8(n)
    fmt.Println(a + one > a, u + u8(n) == 0, w + u16(n) < w, s + i16(n) < s, x + u32(n) == 0, y + i32(n) < y)
    fmt.Println(a + one, u + u8(n), w * w, s * s, x * x, y * y, u * u, a * a)
    fmt.Println(-b, -b == b, ^u, ^u == 0, ^w, ^x, a - b, u - w8(n))
    show(int(a + one), int(u + u8(n)), int(x + u32(n)))
    fmt.Println(int(a + one), int(u + u8(n)), int(w + u16(n)), int(x + u32(n)), i64(y + i32(n)), u64(u + u8(n)))
    fmt.Println((a + one) / 2, (u + u8(n)) / 2, (x + u32(n)) / 2, (u + u8(n)) % 7)
    fmt.Println(u << u8(n), u << u8(n + 7), u << u8(n + 8), u << u8(n + 200), a << i8(n), a << i8(n + 6), a << i8(n + 7), x << u32(n + 30), x << u32(n + 31), x << u32(n + 40))
    fmt.Println(d << u64(n + 62), d << u64(n + 63), d << u64(n + 99), w << u16(n + 14), w << u16(n + 15), s << i16(n + 20), y << i32(n + 31), e << i64(n + 63))
    fmt.Println(u >> u8(n + 6), u >> u8(n + 7), b >> i8(n), b >> i8(n + 8), e >> i64(n + 62), e >> i64(n + 63), e >> i64(n + 99), x >> u32(n + 31), d >> u64(n + 63), y >> i32(n + 40), s >> i16(n + 15))
    fmt.Println(u << u8(n) > u, (u << u8(n + 3)) >> u8(n + 3), int(u << u8(n + 3)), (a << i8(n)) / 2)
    xs := []int{10, 20, 30}
    fmt.Println(xs[int(u + u8(n))], min(a + one, a), max(u + u8(n), 1))
    fmt.Println(xs[u + u8(n) + 1], xs[one], xs[i16(n) + 1], xs[u + u8(n):u8(n) + 1], "abc"[u8(n):u16(n) + 1])
}

func w8(n int) u8 { return u8(n) + 255 }
`,
			Out: "false true true true true true\n-128 0 1 1 1 1 1 1\n-128 true 0 true 0 0 -1 255\n-128 0 0\n-128 0 0 0 -2147483648 0\n-64 0 0 0\n254 0 0 0 -2 -128 0 2147483648 0 0\n9223372036854775808 0 0 32768 0 0 0 0\n1 0 -64 -1 -1 -1 -1 0 0 0 0\nfalse 15 240 -1\n10 -128 1\n20 20 30 [10 20] b\n",
		},
		{
			Name: "narrow-integers-assigned", // the same on a constant of each width, whose sum the C compiler would otherwise refuse to work out, through an operator assignment, with a literal count, on a rune and an element of a slice; the expected output is Go's
			Src: `package main

import ("fmt"; "os")

const Max i8 = 127
const Top u16 = 65535
const Most = 9223372036854775807
const Last u64 = 18446744073709551615

func main() {
    n := len(os.Args)
    fmt.Println(Max + 1, Max + 1 < Max, Top + 1, Top * 2, -Max - 2, Max << 1, Top << 15, Top >> 15)
    fmt.Println(Most + 1, Most * 2, Most + 1 < Most, Last + 1, Last * Last, -Most - 2)
    var a i8 = 100
    a += 100
    var u u8 = 200
    u *= 2
    var w u16 = 1
    w <<= 15
    w <<= 1
    var x u32 = 7
    x <<= 31
    x >>= 31
    var y i32 = -9
    y >>= 40
    var d u64 = 1
    d <<= 64
    var e i64 = -1
    e <<= 63
    e >>= 70
    fmt.Println(a, u, w, x, y, d, e)
    var m u8 = 255
    m &^= 15
    var k2 i16 = -1
    k2 &^= 255
    d = 255
    d &^= 15 + u64(n)
    fmt.Println(m, k2, d, m &^ 48)
    var r rune = 'z'
    fmt.Println(r - 'a' + 'A', r + rune(n) - 1 == 'z', r * 16777216 * 2, u8('a') + u8(n) * 100 * 3)
    var b u8 = 250
    for i := 0; i < 10; i++ { b++ }
    var c i8 = -128
    c--
    fmt.Println(b, c, b + 250, (b + 250) / 3, (b - 5) * 2, -(c), ^b, ^b + 1, ^c)
    xs := []u8{250, 10}
    fmt.Println(xs[0] + xs[1], xs[0] + xs[1] < xs[1], int(xs[0] * xs[1]), xs[1] << 7, xs[0] >> 9, u16(xs[0]) + u16(xs[1]))
    var k u8 = 1
    var total u64 = 0
    for i := 0; i < 20; i++ { total += u64(k << u8(i)) + u64(k + u8(i * 30)) }
    fmt.Println(total, u32(n) - 2, u32(n) - 2 > 5, i16(n) * 30000 * 3)
}
`,
			Out: "-128 true 0 65534 127 -2 32768 1\n-9223372036854775808 -2 true 0 1 9223372036854775807\n-56 144 0 1 -1 0 -1\n240 -256 239 192\n90 true -201326592 141\n4 127 254 84 254 -127 251 252 -128\n4 true 196 0 0 260\n2647 4294967295 true 24464\n",
		},
		{
			Name: "float-to-integer", // a float converted to an integer type drops its fraction, gives the nearest end of the type when it is past the range, and zero when it is not a number, from an f64 and from an f32, to every integer type, at each end of each range, one past it, the infinities and NaN; the expected output is that of Rust's conversion, which has this rule
			Src: `package main

import ("fmt"; "math"; "os")

func main() {
    n := f64(len(os.Args))
    vs := []f64{0.0 * n, 0.9 * n, -0.9 * n, 1.5 * n, -1.5 * n, 127.0 * n, 127.9 * n, 128.0 * n, -128.0 * n, -128.9 * n, -129.0 * n, 255.9 * n, 256.0 * n, 32767.9 * n, 32768.0 * n, -32769.0 * n, 65535.9 * n, 65536.0 * n, 2147483647.0 * n, 2147483648.0 * n, -2147483649.0 * n, 4294967295.0 * n, 4294967296.0 * n, 9223372036854775807.0 * n, 9223372036854774784.0 * n, -9223372036854775808.0 * n, -9223372036854777856.0 * n, 18446744073709551615.0 * n, 18446744073709549568.0 * n, 1e30 * n, -1e30 * n, math.NaN(), math.Inf(1), math.Inf(-1)}
    for _, v := range vs {
        w := f32(v)
        fmt.Println(i8(v), u8(v), i16(v), u16(v), i32(v), u32(v), i64(v), u64(v), int(v), uint(v), i8(w), u8(w), i16(w), u16(w), i32(w), u32(w))
    }
}
`,
			Out: "0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n1 1 1 1 1 1 1 1 1 1 1 1 1 1 1 1\n-1 0 -1 0 -1 0 -1 0 -1 0 -1 0 -1 0 -1 0\n127 127 127 127 127 127 127 127 127 127 127 127 127 127 127 127\n127 127 127 127 127 127 127 127 127 127 127 127 127 127 127 127\n127 128 128 128 128 128 128 128 128 128 127 128 128 128 128 128\n-128 0 -128 0 -128 0 -128 0 -128 0 -128 0 -128 0 -128 0\n-128 0 -128 0 -128 0 -128 0 -128 0 -128 0 -128 0 -128 0\n-128 0 -129 0 -129 0 -129 0 -129 0 -128 0 -129 0 -129 0\n127 255 255 255 255 255 255 255 255 255 127 255 255 255 255 255\n127 255 256 256 256 256 256 256 256 256 127 255 256 256 256 256\n127 255 32767 32767 32767 32767 32767 32767 32767 32767 127 255 32767 32767 32767 32767\n127 255 32767 32768 32768 32768 32768 32768 32768 32768 127 255 32767 32768 32768 32768\n-128 0 -32768 0 -32769 0 -32769 0 -32769 0 -128 0 -32768 0 -32769 0\n127 255 32767 65535 65535 65535 65535 65535 65535 65535 127 255 32767 65535 65535 65535\n127 255 32767 65535 65536 65536 65536 65536 65536 65536 127 255 32767 65535 65536 65536\n127 255 32767 65535 2147483647 2147483647 2147483647 2147483647 2147483647 2147483647 127 255 32767 65535 2147483647 2147483648\n127 255 32767 65535 2147483647 2147483648 2147483648 2147483648 2147483648 2147483648 127 255 32767 65535 2147483647 2147483648\n-128 0 -32768 0 -2147483648 0 -2147483649 0 -2147483649 0 -128 0 -32768 0 -2147483648 0\n127 255 32767 65535 2147483647 4294967295 4294967295 4294967295 4294967295 4294967295 127 255 32767 65535 2147483647 4294967295\n127 255 32767 65535 2147483647 4294967295 4294967296 4294967296 4294967296 4294967296 127 255 32767 65535 2147483647 4294967295\n127 255 32767 65535 2147483647 4294967295 9223372036854775807 9223372036854775808 9223372036854775807 9223372036854775808 127 255 32767 65535 2147483647 4294967295\n127 255 32767 65535 2147483647 4294967295 9223372036854774784 9223372036854774784 9223372036854774784 9223372036854774784 127 255 32767 65535 2147483647 4294967295\n-128 0 -32768 0 -2147483648 0 -9223372036854775808 0 -9223372036854775808 0 -128 0 -32768 0 -2147483648 0\n-128 0 -32768 0 -2147483648 0 -9223372036854775808 0 -9223372036854775808 0 -128 0 -32768 0 -2147483648 0\n127 255 32767 65535 2147483647 4294967295 9223372036854775807 18446744073709551615 9223372036854775807 18446744073709551615 127 255 32767 65535 2147483647 4294967295\n127 255 32767 65535 2147483647 4294967295 9223372036854775807 18446744073709549568 9223372036854775807 18446744073709549568 127 255 32767 65535 2147483647 4294967295\n127 255 32767 65535 2147483647 4294967295 9223372036854775807 18446744073709551615 9223372036854775807 18446744073709551615 127 255 32767 65535 2147483647 4294967295\n-128 0 -32768 0 -2147483648 0 -9223372036854775808 0 -9223372036854775808 0 -128 0 -32768 0 -2147483648 0\n0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n127 255 32767 65535 2147483647 4294967295 9223372036854775807 18446744073709551615 9223372036854775807 18446744073709551615 127 255 32767 65535 2147483647 4294967295\n-128 0 -32768 0 -2147483648 0 -9223372036854775808 0 -9223372036854775808 0 -128 0 -32768 0 -2147483648 0\n",
		},
		{
			Name: "divide", // the division the language defines and C and the IR do not
			Src: `package main
import "fmt"
func main() {
	a := -9223372036854775807 - 1
	m := -1
	fmt.Println(a / m, a % m, 7 / 2, -7 % 3)
	b := i8(-128)
	c := i8(-1)
	fmt.Println(b / c, b % c)
	u := u8(200)
	u /= 3
	x := 10
	x %= 4
	fmt.Println(u, x)
	d := len("")
	fmt.Println(7 / d)
}
`,
			Out: "-9223372036854775808 0 3 -1\n-128 0\n66 2\n", Stderr: "panic: integer divide by zero", Code: 2,
		},
		{
			Name: "panic-drops", // innermost first, none moved away or dropped before; a temporary dropped after a variable its statement declared
			Src: guard + `func main() {
	a := Guard{name: "a"}
	b := Guard{name: "b"}
	take(b)
	{
		c := Guard{name: "c"}
		fmt.Println("inner", c.name)
	}
	g := Guard{name: "g" + name(Guard{name: "t"})}
	d := Guard{name: "d"}
	xs := []int{}
	i := len(xs)
	fmt.Println(xs[i], d.name, g.name)
}
`,
			Out: "took b\ndrop b\ninner c\ndrop c\ndrop t\ndrop d\ndrop gt\ndrop a\n", Stderr: "panic: index out of range", Code: 2,
		},
		{
			Name: "panicking-drop", // a Drop that panics during the unwinding ends the program with its own message
			Src: guard + `struct Bad impl Drop { name string }
func (b mut Bad) Drop() { panic("in drop") }
func main() {
	a := Guard{name: "a"}
	b := Bad{name: "b"}
	c := Guard{name: "c"}
	if b.name == "b" {
		panic("first")
	}
	fmt.Println(a.name, c.name)
}
`,
			Out: "drop c\n", Stderr: "panic: first\npanic: in drop\n", Code: 2,
		},
		{
			Name: "panic-defers", // the deferred calls that ran run as a panic unwinds, last registered first among them and the values: after the drop of a value declared after the defer, before the drop of one declared before it; a receiver lent mutably is read when the call runs; a defer not reached does not run
			Src: guard + `struct Counter { n int }
func (c mut Counter) Report() { fmt.Println("count", c.n) }
func work(skip bool) {
	a := Guard{name: "a"}
	c := Counter{n: 0}
	defer c.Report()
	defer fmt.Println("deferred")
	if skip {
		defer fmt.Println("not reached")
	}
	b := Guard{name: "b"}
	c.n = 2
	xs := []int{}
	fmt.Println(xs[len(xs)], b.name)
}
func main() { work(false) }
`,
			Out: "drop b\ndeferred\ncount 2\ndrop a\n", Stderr: "panic: index out of range", Code: 2,
		},
		{
			Name: "defer-order", // a return runs the deferred calls and the drops in the order a panic does, last registered first, the arguments evaluated at the defer; a body that falls off its end too; a defer not reached does not run
			Src: guard + `func work(early bool) int {
	a := Guard{name: "a"}
	n := 1
	defer fmt.Println("deferred 1", n)
	b := Guard{name: "b"}
	n = 2
	if early {
		c := Guard{name: "c"}
		defer fmt.Println("deferred 2", n)
		return len(c.name) + len(b.name)
	}

	defer fmt.Println("deferred 3", n)
	return len(a.name)
}
func tail() {
	t := Guard{name: "t"}
	defer fmt.Println("deferred t")
	h := Guard{name: "h"}
	fmt.Println(t.name, h.name)
}
func main() {
	fmt.Println(work(true))
	fmt.Println(work(false))
	tail()
}
`,
			Out: "deferred 2 2\ndrop c\ndrop b\ndeferred 1 1\ndrop a\n2\n" +
				"deferred 3 2\ndrop b\ndeferred 1 1\ndrop a\n1\n" +
				"t h\ndrop h\ndeferred t\ndrop t\n",
		},
		{
			Name: "defer-instances", // each instance of a generic function saves its deferred call's values into a record of its own; a mut receiver is read when the call runs; a defer in a closure runs when the closure returns
			Src: `package main
import "fmt"
struct Server { hits int }
func (s mut Server) cleanup() { fmt.Println("cleanup", s.hits) }
func (s mut Server) Run() {
	defer s.cleanup()
	s.hits = s.hits + 1
}
func show<T>(x T, label string) {
	defer fmt.Println("deferred", label)
	fmt.Println("body", label)
}
func main() {
	srv := Server{hits: 1}
	srv.Run()
	show(1, "int")
	show("s", "string")
	f := func(n int) {
		defer fmt.Println("inner deferred", n)
		fmt.Println("inner body", n)
	}
	f(7)
}`,
			Out: "cleanup 2\nbody int\ndeferred int\nbody string\ndeferred string\ninner body 7\ninner deferred 7\n",
		},
		{
			Name: "defer-ref-receiver", // a receiver lent by ref, a type's own or a predeclared method's, is read when the deferred call runs, on a return and on a panic, so a change the function made before counts; a value the method takes is saved at the defer
			Src: `package main
import "fmt"
struct Tally { name string }
func (t ref Tally) Show() { fmt.Println("show", t.name) }
func work() {
	t := Tally{name: "old".Clone()}
	xs := []int{1}
	o := Some(3)
	defer t.Show()
	defer fmt.Println("at defer", len(xs))
	defer xs.Clone()
	defer fmt.Println(o.Unwrap())
	t.name = "newer".Clone()
	xs.Push(2)
	fmt.Println("body", t.name, len(xs))
}
func boom() {
	t := Tally{name: "old".Clone()}
	defer t.Show()
	t.name = "later".Clone()
	panic("stop")
}
func main() {
	work()
	boom()
}`,
			Out: "body newer 2\n3\nat defer 1\nshow newer\nshow later\n", Stderr: "panic: stop", Code: 2,
		},
		{
			Name: "defer-panics", // a deferred call that panics on a return runs once, and its panic runs the deferred calls and drops still registered
			Src: guard + `func boom() { fmt.Println("boom"); panic("in defer") }
func work() {
	a := Guard{name: "a"}
	defer fmt.Println("deferred 1")
	defer boom()
	fmt.Println("returning", a.name)
}
func main() { work() }
`,
			Out: "returning a\nboom\ndeferred 1\ndrop a\n", Stderr: "panic: in defer\n", Code: 2,
		},
		{
			Name: "defer-panics-unwinding", // a deferred call that panics while a panic unwinds ends the program with both messages, as a Drop that panics does
			Src: guard + `func boom() { panic("in defer") }
func work() {
	a := Guard{name: "a"}
	defer boom()
	if a.name == "a" {
		panic("first")
	}
}
func main() { work() }
`,
			Stderr: "panic: first\npanic: in defer\n", Code: 2,
		},
		{
			Name: "generic-box", // a closure in a generic function copying more than the default box holds
			Src: `package main
import "fmt"
struct Big impl Copy { a int; b int; c int; d int; e int; f int }
func keep<T: Copy>(v T) func() T { return func() T { return v } }
func main() {
	f := keep(Big{a: 1, b: 2, c: 3, d: 4, e: 5, f: 6})
	g := keep(7)
	fmt.Println(f(), g())
}
`,
			Out: "{1 2 3 4 5 6} 7\n",
		},
		{
			Name: "library-conversion", // a conversion to a type a library declares, named through its package
			Src:  "package main\nimport (\"fmt\"; \"time\")\nfunc main() { d := time.Duration(1500); fmt.Println(d.Nanoseconds(), d > time.Duration(0)) }\n",
			Out:  "1500 true\n",
		},
		{
			Name: "tick", // time.Tick, whose timer never ends: three ticks and a clean exit while it still runs
			Src:  "package main\nimport (\"fmt\"; \"time\")\nfunc main() { ticks := time.Tick(time.Millisecond); for i := 0; i < 3; i++ { <-ticks; fmt.Println(\"tick\") } }\n",
			Out:  "tick\ntick\ntick\n",
		},
		{
			Name: "parallel", // a spawned task runs on another core when one is free: neither task waits, and the scheduler does not preempt, so on one worker the first would compute alone until its deadline
			Src: `package main
import ("fmt"; "sync"; "time")
func arrive(count sync.Arc<sync.Mutex<int>>, done chan bool) {
	{
		g := count.Get().Lock()
		*g.Get() += 1
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		g := count.Get().Lock()
		if *g.Get() == 2 {
			done <- true
			return
		}
	}

	done <- false
}
func main() {
	count := sync.NewArc(sync.NewMutex(0))
	done := make(chan bool, 2)
	spawn arrive(count.Clone(), done.Clone())
	spawn arrive(count.Clone(), done.Clone())
	a := <-done ?? false
	b := <-done ?? false
	fmt.Println(a && b)
}
`,
			Out: "true\n", ReleaseOnly: true, MinCPUs: 2,
		},
		{
			Name: "handoff", // a task woken by a task that then computes without a wait runs on another worker meanwhile, moved out of the busy worker's runnext slot by the monitor; main sleeps first, so that every worker is idle and the monitor asleep, and the timer's wake must wake it; compute's 5 ms settle lets every idle worker end its spin and sleep before the slot is filled, so that nothing but the monitor can move the task
			Src: `package main
import ("fmt"; "time")
func compute(ready chan int, ch chan int) {
	<-ready
	settle := time.Now().Add(5 * time.Millisecond)
	for time.Now().Before(settle) {
	}
	ch <- 1
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
	}
}
func reply(ready chan int, ch chan int, out chan bool, start time.Time) {
	ready <- 1
	<-ch
	out <- time.Since(start) < time.Second
}
func main() {
	time.Sleep(10 * time.Millisecond)
	ready := make(chan int, 1)
	ch := make(chan int)
	out := make(chan bool)
	start := time.Now()
	spawn reply(ready.Clone(), ch.Clone(), out.Clone(), start)
	spawn compute(ready.Clone(), ch.Clone())
	fmt.Println(<-out ?? false)
}
`,
			Out: "true\n", ReleaseOnly: true, MinCPUs: 2,
		},
		{
			Name: "asleep-not-deadlocked", // every task parked, but a sleeping one has a timer to wake it: no deadlock
			Src:  "package main\nimport (\"fmt\"; \"time\")\nfunc later(ch chan int) { time.Sleep(20 * time.Millisecond); ch <- 7 }\nfunc main() { ch := make(chan int); spawn later(ch.Clone()); fmt.Println(<-ch ?? 0) }\n",
			Out:  "7\n",
		},
		{
			Name: "writer-first", // a reader arriving while a writer waits goes after the writer, so readers cannot keep a writer out
			Src: `package main
import ("fmt"; "sync"; "time")
func reader(l sync.Arc<sync.RwLock<int>>, held chan (), release chan ()) {
	g := l.Get().Read()
	held <- ()
	<-release
	fmt.Println("first reader done", *g.Get())
}
func writer(l sync.Arc<sync.RwLock<int>>, done chan ()) {
	g := l.Get().Write()
	*g.Get() = 1
	fmt.Println("writer")
	done <- ()
}
func late(l sync.Arc<sync.RwLock<int>>, done chan ()) {
	g := l.Get().Read()
	fmt.Println("late reader", *g.Get())
	done <- ()
}
func main() {
	l := sync.NewArc(sync.NewRwLock(0))
	held := make(chan ())
	release := make(chan ())
	done := make(chan (), 2)
	spawn reader(l.Clone(), held.Clone(), release.Clone())
	<-held
	spawn writer(l.Clone(), done.Clone())
	time.Sleep(30 * time.Millisecond) // the writer is waiting by now
	spawn late(l.Clone(), done.Clone())
	time.Sleep(30 * time.Millisecond) // the late reader would have read by now, were it let in
	release <- ()
	<-done
	<-done
}
`,
			Out: "first reader done 0\nwriter\nlate reader 1\n",
		},
		manyTasks(short),
		{
			Name: "select-returns", // a function ending in a select whose every case returns, on owned and on borrowed channels; a select with no case never returns
			Src: `package main
import "fmt"
func pick(a <-chan int, b <-chan int) int {
	select {
	case v := <-a:
		return v ?? 0
	case v := <-b:
		return (v ?? 0) + 100
	}
}
func pickRef(a ref <-chan int, b ref <-chan int) int {
	select {
	case v := <-a:
		return v ?? 0
	case v := <-b:
		return (v ?? 0) + 100
	}
}
func pickDefault(a ref <-chan int) int {
	select {
	case v := <-a:
		return v ?? 0
	default:
		return 7
	}
}
func first(a ref <-chan int) int {
	for {
		select {
		case v := <-a:
			if n := v {
				if n > 2 {
					return n
				}
			} else {
				return -1
			}
		}
	}
}
func forever() int { select {} }
func main() {
	a := make(chan int, 2)
	b := make(chan int, 1)
	b <- 5
	fmt.Println(pick(a.Clone(), b.Clone()))
	b <- 5
	fmt.Println(pickRef(ref a, ref b))
	fmt.Println(pickDefault(ref a))
	a <- 1
	a <- 3
	fmt.Println(first(ref a))
}
`,
			Out: "105\n105\n7\n3\n",
		},
		{
			Name: "task-drops-channel", // a Drop-bearing struct holding channels moves into a task, which drops it once; a channel inside an option and a tuple
			Src: `package main
import "fmt"
struct Worker impl Drop {
	in   chan int
	done chan int
	name string
}
func (w mut Worker) Drop() {
	fmt.Println("drop", w.name)
	w.done <- 1
}
func run(w Worker) {
	total := 0
	for v := range w.in {
		total += v
	}
	fmt.Println("total", total)
}
func main() {
	w := Worker{in: make(chan int), done: make(chan int, 2), name: "w".Clone()}
	feed := w.in.Clone()
	done := w.done.Clone()
	spawn run(w)
	feed <- 1
	feed <- 2
	close(feed)
	<-done
	fmt.Println("joined", len(done))
	oc := Some(make(chan int, 1))
	if ch := oc {
		ch <- 1
		fmt.Println("option", <-ch ?? 0)
	}
	pair := (make(chan int, 1), 5)
	ch, n := pair
	ch <- n
	fmt.Println("tuple", <-ch ?? 0)
}
`,
			Out: "total 3\ndrop w\njoined 0\noption 1\ntuple 5\n",
		},
		{
			Name: "select-borrowed-ends", // a send case on a ref chan<- T and a receive case on a ref <-chan T; the value of a send not chosen is dropped
			Src: `package main
import "fmt"
func step(out ref chan<- string, in ref <-chan string, v string) string {
	select {
	case out <- v:
		return "sent".Clone()
	case got := <-in:
		return got ?? "closed".Clone()
	}
}
func main() {
	a := make(chan string, 1)
	b := make(chan string, 1)
	fmt.Println(step(ref a, ref b, "one".Clone()))
	b <- "seven".Clone()
	fmt.Println(step(ref a, ref b, "two".Clone()))
	close(b)
	fmt.Println(step(ref a, ref b, "three".Clone()))
	fmt.Println(<-a ?? "".Clone(), len(a))
}
`,
			Out: "sent\nseven\nclosed\none 0\n",
		},
		{
			Name: "integer-literals", // a literal has its type's width: a product of two is not a C int, the largest u64 is not refused, and one formatted with %d is not leaked
			Src: `package main
import "fmt"
func main() {
	secs := 570 * 31556952
	var big u64 = 18446744073709551615
	var small u8 = 200
	n := -9223372036854775808
	var f f64 = 3
	match small {
		200 => fmt.Println("two hundred"),
		_ => fmt.Println("other"),
	}

	fmt.Println(secs, big, n, f, 1 << 40, big == 18446744073709551615)
	fmt.Println(fmt.Sprintf("%d %5d %d", big, small, u32(7)))
}
`,
			Out: "two hundred\n17987462640 18446744073709551615 -9223372036854775808 3 1099511627776 true\n18446744073709551615   200 7\n",
		},
		{
			Name: "const-formats", // a format may be a constant, at the top level or in a function; a const in a function is a constant of its own, read in a closure too
			Src: `package main
import "fmt"
const top = "%d-%s\n"
func a() { const f = "a %d\n"; const n = 7; fmt.Printf(f, n) }
func b() { const f = "b %s\n"; g := func() { fmt.Printf(f, "inner") }; g() }
func main() {
	const f = "%d %d\n"
	fmt.Printf((f), 1, 2)
	fmt.Printf(top, 3, "x")
	a()
	b()
}
`,
			Out: "1 2\n3-x\na 7\nb inner\n",
		},
		{
			Name: "channel-equality", // two handles are equal when they are on one channel, as Go's are, and a channel hashes as that channel: a map key, a field of an Eq and Hash struct
			Src: `package main
import "fmt"
struct H impl Eq, Hash { ch chan int }
func main() {
	a := make(chan int, 1)
	b := a.Clone()
	c := make(chan int, 1)
	fmt.Println(a == b, a == c, H{ch: a.Clone()} == H{ch: b.Clone()}, H{ch: a.Clone()} == H{ch: c.Clone()})
	seen := map[chan int]string{}
	seen.Insert(a.Clone(), "a".Clone())
	seen.Insert(c.Clone(), "c".Clone())
	seen.Insert(b.Clone(), "b".Clone())
	fmt.Println(len(seen), seen.Get(ref a) ?? "none", seen.Contains(ref c))
	keyed := map[H]int{}
	keyed.Insert(H{ch: a.Clone()}, 1)
	keyed.Insert(H{ch: b.Clone()}, 2)
	fmt.Println(len(keyed))
}
`,
			Out: "true false true false\n2 b true\n1\n",
		},
		{
			Name: "bound-over-named", // a standard trait's method through a bound, on a type declared over a predeclared one: Clone on a named slice and a named map, Eq and Cmp on a named number, which the declared type has as its own
			Src: `package main
import "fmt"
type Names []string
type Ages map[string]int
type Weight f64
func dup<T: Clone>(x ref T) T { return x.Clone() }
func same<T: Eq>(a ref T, b ref T) bool { return a.Eq(b) }
func less<T: Ord>(a ref T, b ref T) bool { return a.Cmp(b) == Less }
func main() {
	ns := Names{"a".Clone(), "b".Clone()}
	copy := dup(ref ns)
	copy.Push("c".Clone())
	fmt.Println(len(ns), len(copy))
	ages := Ages{"x": 1}
	fmt.Println(len(dup(ref ages)))
	w := Weight(1.5)
	fmt.Println(same(ref w, ref Weight(1.5)), less(ref w, ref Weight(2.0)))
}`,
			Out: "2 3\n1\ntrue true\n",
		},
		{
			Name: "generic-unit-return", // a generic function returning its T, given () as T: the instance is a void function whose return evaluates the value and returns nothing
			Src: `package main
import "fmt"
func pick<T>(r Result<T, string>) T {
	match r {
		Ok(v) => return v,
		Err(e) => panic(ref e),
	}
}
func say() Result<(), string> { fmt.Println("said"); return Ok(()) }
func main() {
	pick(say())
	var n Result<int, string> = Ok(3)
	fmt.Println(pick(n))
	var r Result<(), string> = Ok(())
	pick(r)
	fmt.Println("done")
}`,
			Out: "said\n3\ndone\n",
		},
		{
			Name: "error-derived", // a type listing Error and writing no Error() prints as %v prints it, an enum, a struct and a type over int; Source() is None; %+v is %v with no source
			Src: `package main
import ("fmt"; "strconv")
enum ConfigError impl Error {
	Unreadable(cause strconv.Error)
	Missing(key string)
	Empty
}
struct Pair impl Error { key string; n int }
type Code int impl Error
func parse(s ref string) Result<int, ConfigError> {
	n := strconv.Atoi(s)?
	return Ok(n)
}
func main() {
	text := "x"
	match parse(ref text) {
		Ok(n) => fmt.Println(n),
		Err(e) => { fmt.Println(e); fmt.Printf("%v|%s|%q|%+v\n", e, e, e, e) },
	}
	e := Missing("port")
	fmt.Println(e, Empty, Pair{key: "k", n: 1}, Code(42))
	if s := e.Source() { fmt.Println("source", s) } else { fmt.Println("no source") }
	fmt.Printf("%+v %+v %+v\n", Pair{key: "k", n: 1}, Code(7), ref e)
}`,
			Out: "Unreadable(\"strconv.Atoi: parsing \\\"x\\\": invalid syntax\")\n" +
				"Unreadable(\"strconv.Atoi: parsing \\\"x\\\": invalid syntax\")|Unreadable(\"strconv.Atoi: parsing \\\"x\\\": invalid syntax\")|\"Unreadable(\\\"strconv.Atoi: parsing \\\\\\\"x\\\\\\\": invalid syntax\\\")\"|Unreadable(\"strconv.Atoi: parsing \\\"x\\\": invalid syntax\")\n" +
				"Missing(\"port\") Empty {k 1} 42\nno source\n{k 1} 7 Missing(\"port\")\n",
		},
		{
			Name: "error-chain", // a written Source gives the cause as an owned trait value, a clone; %+v prints the chain through the tables, on the type and on a trait value of Error; a trait value of Error or fmt.Stringer prints as its message
			Src: `package main
import ("fmt"; "strconv")
struct LoadError impl Error, Clone { path string; cause strconv.Error }
func (e ref LoadError) Error() string { return fmt.Sprintf("load %s", e.path) }
func (e ref LoadError) Source() Error? { return Some(e.cause.Clone()) }
struct Outer impl Error { inner LoadError }
func (e ref Outer) Error() string { return "outer" }
func (e ref Outer) Source() Error? { return Some(e.inner.Clone()) }
struct P impl fmt.Stringer { n int }
func (p ref P) String() string { return fmt.Sprintf("P%d", p.n) }
func main() {
	text := "x"
	cause := match strconv.Atoi(ref text) { Ok(_) => panic("parsed"), Err(c) => c }
	e := LoadError{path: "app.conf", cause: cause}
	fmt.Printf("%v\n%+v\n", e, e)
	o := Outer{inner: e}
	fmt.Printf("%+v\n", o)
	var boxed Error = o
	fmt.Printf("%+v|%v|%s|%q\n", boxed, boxed, boxed, boxed)
	fmt.Println(boxed)
	if s := boxed.Source() { fmt.Println("source:", s) }
	var sv fmt.Stringer = P{n: 3}
	fmt.Println(sv, []fmt.Stringer{P{n: 4}}, Some(sv))
}`,
			Out: "load app.conf\nload app.conf: strconv.Atoi: parsing \"x\": invalid syntax\n" +
				"outer: load app.conf: strconv.Atoi: parsing \"x\": invalid syntax\n" +
				"outer: load app.conf: strconv.Atoi: parsing \"x\": invalid syntax|outer|outer|\"outer\"\nouter\nsource: load app.conf\nP3 [P4] Some(\"P3\")\n",
		},
		{
			Name: "error-pass", // ? passes an error into the one variant holding its type, into a trait value when the function returns Result<T, Error>, and as it is when the types match
			Src: `package main
import ("fmt"; "io"; "os"; "strconv")
enum AppError impl Error {
	Read(cause io.Error)
	Parse(cause strconv.Error)
	Other(msg string)
}
func load(path string) Result<int, AppError> {
	text := os.ReadToString(ref path)?
	n := strconv.Atoi(ref text)?
	return Ok(n)
}
func loadAny(path string) Result<int, Error> {
	text := os.ReadToString(ref path)?
	n := strconv.Atoi(ref text)?
	return Ok(n)
}
func parse(text ref string) Result<int, AppError> { n := strconv.Atoi(text)?; return Ok(n) }
func parseAny(text ref string) Result<int, Error> { n := strconv.Atoi(text)?; return Ok(n) }
func twice(text ref string) Result<int, AppError> { n := parse(text)?; return Ok(n * 2) }
func main() {
	match load("/nonexistent/x") { Ok(n) => fmt.Println(n), Err(e) => fmt.Println(e) }
	match loadAny("/nonexistent/x") { Ok(n) => fmt.Println(n), Err(e) => fmt.Println("error:", e) }
	q := "q"
	match parse(ref q) { Ok(n) => fmt.Println(n), Err(e) => fmt.Println(e) }
	match parseAny(ref q) { Ok(n) => fmt.Println(n), Err(e) => fmt.Println("error:", e) }
	match twice(ref q) { Ok(n) => fmt.Println(n), Err(e) => fmt.Println(e) }
	seven := "7"
	fmt.Println(twice(ref seven), parseAny(ref seven))
}`,
			Out: "Read(\"open /nonexistent/x: no such file or directory\")\nerror: open /nonexistent/x: no such file or directory\n" +
				"Parse(\"strconv.Atoi: parsing \\\"q\\\": invalid syntax\")\nerror: strconv.Atoi: parsing \"q\": invalid syntax\n" +
				"Parse(\"strconv.Atoi: parsing \\\"q\\\": invalid syntax\")\nOk(14) Ok(7)\n",
		},
		{
			Name: "rune-conversions", // string(r) and string(rs) build the UTF-8 of a rune and of a []rune, a borrowed slice too, a value that is not a code point written as U+FFFD; the slices and strings are freed
			Src: `package main
import "fmt"
func join(rs ref []rune) string { return string(rs) }
func main() {
	var r rune = 'é'
	var bad rune = -1
	var sur rune = 0xD800
	rs := "añ世".Runes()
	rs.Push(0x1F600)
	fmt.Println(string(r), string('x'), string(rs), join(ref rs), string(bad), string(sur), len(string([]rune{})), string(rs[1]))
}`,
			Out: "é x añ世😀 añ世😀 \uFFFD \uFFFD 0 ñ\n",
		},
		{
			Name: "waitgroup-below-zero", // a Done with no Add, or an Add that would take the count below zero, answers false and leaves the count, where it stopped the program
			Src: `package main
import ("fmt"; "sync")
func main() {
	wg := sync.NewWaitGroup()
	fmt.Println(wg.Done(), wg.Add(1), wg.Add(-2), wg.Done())
	wg.Wait()
	fmt.Println("done")
}`,
			Out: "false true false true\ndone\n",
		},
		{
			Name: "replace", // replace moves a value out of a place and leaves another, a field, an element and a variable, of a type with Drop and so with no zero value; each value is dropped once, the old where its new owner drops it
			Src: `package main
import "fmt"
struct Loud impl Drop { name string }
func (l mut Loud) Drop() { fmt.Println("drop", l.name) }
func loud(s string) Loud { return Loud{name: s.Clone()} }
struct Slot { l Loud; next *Slot? }
func main() {
	s := Slot{l: loud("a"), next: Some(&Slot{l: loud("tail"), next: None})}
	old := replace(mut s.l, loud("b"))
	fmt.Println("took", old.name, "left", s.l.name)
	xs := []Loud{loud("x"), loud("y")}
	first := replace(mut xs[0], loud("z"))
	fmt.Println("took", first.name, "left", xs[0].name)
	rest := replace(mut s.next, None)
	fmt.Println("detached", rest.IsSome())
	v := loud("v")
	w := replace(mut v, loud("v2"))
	fmt.Println(w.name, v.name)
}`,
			Out: "took a left b\ntook x left z\ndetached true\nv v2\ndrop v\ndrop v2\ndrop tail\ndrop x\ndrop z\ndrop y\ndrop a\ndrop b\n", // in reverse order of declaration: w holds v, v holds v2, first x, old a
		},
		{
			Name: "match-conditions", // a match with no value takes the first condition that holds, as an expression and as a statement whose arms return, continue, break or assign; an owned value an arm moves is dropped once, on the arm that moved it
			Src: `package main
import "fmt"
func grade(score int) string {
	return match {
		score >= 90 => "A",
		score >= 80 => "B",
		_ => "F",
	}
}
func keep(s string) int { return len(s) }
func sign(n int) int {
	match {
		n < 0 => return -1,
		n == 0 => return 0,
		_ => {},
	}

	return 1
}
func main() {
	total := 0
	for _, n := range []int{3, -1, 7, 0, 5} {
		match {
			n < 0 => continue,
			n == 0 => break,
			_ => total += n,
		}
	}

	s := "owned".Clone()
	used := match {
		total > 100 => keep(s),
		len(s) > 3 => keep(s) * 2,
		_ => 0,
	}

	fmt.Println(grade(95), grade(85), grade(10), total, used, sign(-4), sign(0), sign(9))
}`,
			Out: "A B F 10 10 -1 0 1\n",
		},
		{
			Name: "conversions-through-mut", // string(p) and []byte(s) through a mut borrow read the value it lends, a []rune, a []byte and a string, and leave it to its owner
			Src: `package main
import "fmt"
func runes(p mut []rune) string { p.Push('!'); return string(p) }
func bytes(b mut []byte) string { b.Push(0x21); return string(b) }
func raw(s mut string) []byte { *s = *s + "?"; return []byte(s) }
func main() {
	rs := "añ".Runes()
	bs := []byte("hi")
	s := "ok"
	fmt.Println(runes(mut rs), bytes(mut bs), raw(mut s), len(rs), len(bs), s)
}`,
			Out: "añ! hi! [111 107 63] 3 3 ok?\n",
		},
		{
			Name: "string-runes", // Runes gives an owned slice of exactly the string's runes, a byte that starts no valid sequence read as U+FFFD; the slice is the program's to grow and is freed
			Src: `package main
import "fmt"
func main() {
	rs := "añ世\xff".Runes()
	fmt.Println(len(rs), cap(rs), rs)
	rs.Push('!')
	fmt.Println(rs, len("".Runes()))
}`,
			Out: "4 4 [97 241 19990 65533]\n[97 241 19990 65533 33] 0\n",
		},
		{
			Name: "range-runes", // a range over a string yields each rune and the byte offset it starts at; a byte that starts no valid sequence is U+FFFD and the loop moves on one byte; continue and break; the one-variable form; a borrowed and a sliced string; the string usable after the loop
			Src: `package main
import "fmt"
func count(s ref string) int { n := 0; for range s { n += 1 }; return n }
func main() {
	s := "héllo, 世界"
	for i, r := range s { fmt.Println(i, r) }
	fmt.Println(len(s), count(ref s), s)
	bad := "a\xffb\xe4\xb8c"
	for i, r := range bad { fmt.Println(i, r) }
	for i := range "añb" { fmt.Printf("%v ", i) }
	fmt.Println()
	for _, r := range s { if r == 'l' { continue }; if r == ',' { break }; fmt.Printf("%v ", r) }
	fmt.Println()
	for _, r := range s[7:] { fmt.Printf("%v ", r) }
	fmt.Println()
	for _, r := range "" { fmt.Println("never", r) }
	fmt.Println(s)
}`,
			Out: "0 104\n1 233\n3 108\n4 108\n5 111\n6 44\n7 32\n8 19990\n11 30028\n14 9 héllo, 世界\n" +
				"0 97\n1 65533\n2 98\n3 65533\n4 65533\n5 99\n0 1 3 \n104 233 111 \n32 19990 30028 \nhéllo, 世界\n",
		},
		{
			Name: "globals", // what a package variable allows from any task: reading, a ref lent to a function, a Copy value copied out, an element indexed, a method taking ref; the table is read by two tasks at once
			Src: `package main
import ("fmt"; "strings")
const limit = 3
var names = []string{"round", "square"}
var title = "shapes"
var zero = 0
func count(xs ref []string) int { return len(xs) }
func work(done chan<- int) {
	n := zero
	for _, name := range ref names { n += len(name) }
	done <- n + count(ref names) + limit
}
func main() {
	done := make(chan int, 2)
	spawn work(done.Clone())
	spawn work(done.Clone())
	fmt.Println(<-done ?? 0, <-done ?? 0, names[1], title.ToUpper(), strings.Repeat(ref title, 2) ?? "", zero + 1)
}`,
			Out: "16 16 square SHAPES shapesshapes 1\n",
		},
		{
			Name: "atomics", // a Counter in a package variable that two tasks add to as one step each, exact with no lock; an Atomic of a string, a flag and a struct, loaded, stored, swapped and compared from two tasks
			Src: `package main
import ("fmt"; "sync")
struct Point impl Eq, Clone { x int; y int }
var hits = sync.NewCounter(0)
var state = sync.NewAtomic("idle")
func work(done chan<- ()) {
	for i := 0; i < 100000; i++ { hits.Add(1) }
	state.CompareAndSwap("idle", "busy".Clone())
	done <- ()
}
func main() {
	done := make(chan ())
	spawn work(done.Clone())
	spawn work(done.Clone())
	<-done
	<-done
	fmt.Println(hits.Load(), state.Load())
	fmt.Println(hits.CompareAndSwap(200000, 7), hits.CompareAndSwap(1, 2), hits.Load())
	hits.Store(3)
	fmt.Println(hits.Add(-3))
	flag := sync.NewAtomic(false)
	fmt.Println(flag.Load(), flag.CompareAndSwap(ref false, true), flag.Swap(false), flag.Load())
	p := sync.NewAtomic(Point{x: 1, y: 2})
	fmt.Println(p.Swap(Point{x: 3, y: 4}), p.Load(), p.CompareAndSwap(ref Point{x: 3, y: 4}, Point{x: 5, y: 6}), p.Load())
	fmt.Println(state.Swap("done".Clone()), state.Load())
}`,
			Out: "200000 busy\ntrue false 7\n0\nfalse true true false\n{1 2} {3 4} true {5 6}\nbusy done\n",
		},
		{
			Name: "lazy-globals", // a package variable whose initializer is a call is made once, on first use, by whichever task reaches it first, and never when no one does; a lock held in one is written from two tasks
			Src: `package main
import ("fmt"; "sync")
func build(name ref string) string { fmt.Println("making", name); return name.Clone() + "!" }
var greeting = build("hello")
var unused = build("never")
var total = sync.NewMutex(0)
func add(n int, done chan<- ()) {
	for i := 0; i < n; i++ {
		g := total.Lock()
		*g.Get() += 1
	}
	done <- ()
}
func main() {
	fmt.Println(greeting)
	fmt.Println(greeting)
	done := make(chan ())
	spawn add(1000, done.Clone())
	spawn add(1000, done.Clone())
	<-done
	<-done
	fmt.Println(*total.Lock().Get())
}`,
			Out: "making hello\nhello!\nhello!\n2000\n",
		},
		{
			Name: "lazy-cycle", // an initializer that reads its own variable through another is a panic naming the variable, not a wait
			Src: `package main
import "fmt"
func f() int { return b + 1 }
func g() int { return a + 1 }
var a = f()
var b = g()
func main() { fmt.Println(a) }`,
			Code:   2,
			Stderr: "panic: package variable a is read by its own initializer\n",
		},
		{
			Name: "lazy-panic", // an initializer that panics under a mark leaves the variable unmade, so the next use runs it again
			Src: `package main
import ("expect"; "fmt")
func boom() int { panic("boom") }
var bad = boom()
func main() {
	expect.PanicsWith(func() { fmt.Println(bad) }, "boom")
	expect.PanicsWith(func() { fmt.Println(bad) }, "boom")
	fmt.Println("unmade twice")
}`,
			Out: "unmade twice\n",
		},
		{
			Name: "catch", // a panic caught at the runtime's mark: the values with Drop made under it are dropped, what the caller holds stays, marks nest, a runtime panic is a message like any other, and a failed expectation is a panic like any other
			Src: `package main
import ("expect"; "fmt"; "testing")
struct Res impl Drop { name string }
func (r mut Res) Drop() { fmt.Println("drop", r.name) }
func main() {
	kept := Res{name: "kept"}
	expect.Panics(func() {
		inner := Res{name: "inner"}
		fmt.Println("holding", inner.name)
		panic("boom")
	})
	fmt.Println("after", kept.name)
	expect.PanicsWith(func() { a, b := 1, 0; fmt.Println(a / b) }, "integer divide by zero")
	expect.PanicsWith(func() {
		expect.Panics(func() { panic("inner") })
		fmt.Println("between")
		panic("outer")
	}, "outer")
	fmt.Println(testing.Catch(func() {}), testing.Catch(func() { panic("told") }))
	expect.True(len(kept.name) == 4) // an assertion on the main task: the parts it holds are freed at the end, as a task's are with the task
	fmt.Println("done")
}`,
			Out: "holding inner\ndrop inner\nafter kept\nbetween\nNone Some(\"told\")\ndone\ndrop kept\n",
		},
		{
			Name: "catch-abandoned", // the plain values of the frames a caught panic abandons, the []int, the messages PanicsWith and Panics build here and the place Panics holds, are not freed: a panic drops what has a Drop method and the process frees the rest at its end, so a test build is a release build (09-testing.md, section 3)
			Src: `package main
import ("expect"; "fmt")
func main() {
	expect.PanicsWith(func() { xs := []int{1}; fmt.Println(xs[5]) }, "index out of range [5] with length 1")
	expect.PanicsWith(func() { expect.PanicsWith(func() { panic("a") }, "b") }, "catch-abandoned.snello:5: expect.PanicsWith(func() { ... }, \"b\")\n        panicked with: \"a\"\n        want:          \"b\"")
	expect.PanicsWith(func() { expect.Panics(func() {}) }, "catch-abandoned.snello:6: expect.Panics(func() { ... }): the function did not panic") // the place Panics holds and the message it builds are abandoned too
	fmt.Println("done")
}`,
			Out:         "done\n",
			ReleaseOnly: true,
		},
		{
			Name: "catch-drop-panics", // a Drop that panics while a caught panic unwinds ends the process, with both messages, since the first can no longer reach the mark
			Src: `package main
import ("expect"; "fmt")
struct Bad impl Drop { name string }
func (b mut Bad) Drop() { panic(ref ("drop of " + b.name + " failed")) }
func main() {
	expect.Panics(func() {
		b := Bad{name: "b"}
		fmt.Println("holding", b.name)
		panic("original")
	})
	fmt.Println("not reached")
}`,
			Out:         "holding b\n",
			Stderr:      "panic: drop of b failed\n\twhile unwinding from panic: original\n",
			Code:        2,
			ReleaseOnly: true, // the abandoned frames are not freed, as catch-abandoned pins
		},
		{
			Name: "catch-defers", // a panic caught at the runtime's mark runs the deferred calls made under it; the caller's run when the caller returns
			Src: `package main
import ("expect"; "fmt")
func main() {
	defer fmt.Println("main deferred")
	expect.Panics(func() {
		defer fmt.Println("inner deferred")
		panic("boom")
	})
	fmt.Println("after")
}`,
			Out: "inner deferred\nafter\nmain deferred\n",
		},
		{
			Name: "catch-across-threads", // a mark taken on one thread and jumped to after the task parked and resumed on another: the jump restores the task's own stack and registers, nothing of the thread's
			Src: `package main
import ("expect"; "fmt"; "testing"; "time")
struct Res impl Drop { name string }
func (r mut Res) Drop() { fmt.Println("drop", r.name) }
func main() {
	ready := make(chan int)
	done := make(chan string)
	r, d := ready.Clone(), done.Clone()
	spawn func() {
		for i := 0; i < 5; i++ {
			kept := Res{name: "kept"}
			expect.PanicsWith(func() {
				inner := Res{name: "inner"}
				n := <-r ?? -1
				time.Sleep(time.Millisecond)
				panic(fmt.Sprintf("boom %d %s", n, inner.name))
			}, fmt.Sprintf("boom %d inner", i))
			msg := testing.Catch(func() { <-r; panic("second") })
			fmt.Println("round", i, kept.name, msg)
		}
		d <- "ok"
	}()
	for i := 0; i < 5; i++ {
		time.Sleep(time.Millisecond)
		ready <- i
		time.Sleep(time.Millisecond)
		ready <- i
	}
	fmt.Println(<-done ?? "none")
}`,
			Out:         "drop inner\nround 0 kept Some(\"second\")\ndrop kept\ndrop inner\nround 1 kept Some(\"second\")\ndrop kept\ndrop inner\nround 2 kept Some(\"second\")\ndrop kept\ndrop inner\nround 3 kept Some(\"second\")\ndrop kept\ndrop inner\nround 4 kept Some(\"second\")\ndrop kept\nok\n",
			ReleaseOnly: true, // the message built by Sprintf in the abandoned frame is not freed, as catch-abandoned pins
			MinCPUs:     2,
		},
		{
			Name: "guard-temporaries", // a guard built in a statement and lent mut, as Get's receiver is, is dropped with the statement, so the lock is given back: an assignment, an op= and a call through it, in a closure too
			Src: `package main
import ("fmt"; "sync")
func main() {
	m := sync.NewMutex(0)
	*m.Lock().Get() = 5
	*m.Lock().Get() += 1
	f := func() { *m.Lock().Get() += 1 }
	f()
	xs := sync.NewMutex([]int{})
	xs.Lock().Get().Push(1)
	xs.Lock().Get().Push(2)
	fmt.Println(*m.Lock().Get(), len(*xs.Lock().Get()))
}
`,
			Out: "7 2\n",
		},
		{
			Name: "waitgroup-clones", // each task counts itself out on a handle of its own, cloned before the spawn, and the last handle on a group frees its counter: a thousand groups leave nothing behind
			Src: `package main
import ("fmt"; "sync")
func main() {
	total := sync.NewArc(sync.NewMutex(0))
	for round := 0; round < 1000; round++ {
		wg := sync.NewWaitGroup()
		for i := 0; i < 8; i++ {
			w := wg.Clone()
			t := total.Clone()
			wg.Add(1)
			spawn func() {
				*t.Get().Lock().Get() += 1
				w.Done()
			}()
		}

		wg.Wait()
	}

	fmt.Println(*total.Get().Lock().Get())
}
`,
			Out: "8000\n",
		},
		{
			Name: "heap", // the library's binary heap, least first by Cmp: pushes and pops interleaved, a heap built from a slice, Peek, an order reversed by its Cmp, owned elements left in a heap when it goes, and elements larger than the runtime's swap buffer
			Src: `package main
import ("fmt"; "heap"; "sort")
struct Job impl Ord { pri int; name string }
func (a ref Job) Cmp(b ref Job) Ordering { return a.pri.Cmp(b.pri) }
struct Max impl Copy, Ord { n int }
func (a ref Max) Cmp(b ref Max) Ordering { return b.n.Cmp(a.n) }
struct Wide impl Ord { f0 int; f1 int; f2 int; f3 int; f4 int; f5 int; f6 int; f7 int; f8 int; f9 int; f10 int; f11 int; f12 int; f13 int; f14 int; f15 int; f16 int; f17 int; f18 int; f19 int; f20 int; f21 int; f22 int; f23 int; f24 int; f25 int; f26 int; f27 int; f28 int; f29 int; f30 int; f31 int; f32 int }
func (a ref Wide) Cmp(b ref Wide) Ordering { return a.f0.Cmp(b.f32) }
func drain(h mut heap.Heap<int>) []int {
	out := []int{}
	for v := h.Pop(); v != None; v = h.Pop() { out.Push(v.Expect("Some")) }
	return out
}
func main() {
	var empty heap.Heap<int>
	fmt.Println(empty.Len(), empty.Pop(), empty.Peek())
	empty.Push(4)
	fmt.Println(empty.Peek(), empty.Len())
	h := heap.New<int>()
	seed := 7
	for i := 0; i < 500; i++ {
		seed = (seed*1103515245 + 12345) % 2147483648
		h.Push(seed % 1000)
		if i % 3 == 0 { h.Pop() }
	}
	fmt.Println(h.Len())
	xs := drain(mut h)
	fmt.Println(len(xs), sort.IsSorted(ref xs), h.Len(), h.Pop())
	f := heap.From([]int{5, 3, 8, 1, 9, 2, 7, 1, 0, 6})
	fmt.Println(f.Peek(), f.Len())
	fmt.Println(drain(mut f))
	fmt.Println(drain(mut heap.From([]int{})), drain(mut heap.From([]int{4})))
	jobs := heap.New<Job>()
	for _, p := range []int{5, 1, 4, 2, 3} { jobs.Push(Job{pri: p, name: fmt.Sprint("job", p)}) }
	for i := 0; i < 3; i++ {
		if j := jobs.Pop() { fmt.Println(j.pri, j.name) }
	}
	if j := jobs.Peek() { fmt.Println("next", j.name) }
	m := heap.From([]Max{Max{n: 2}, Max{n: 9}, Max{n: 4}})
	m.Push(Max{n: 7})
	order := []int{}
	for v := m.Pop(); v != None; v = m.Pop() { order.Push(v.Expect("Some").n) }
	fmt.Println(order)
	w := heap.New<Wide>()
	for i := 0; i < 20; i++ { k := (i * 7) % 20; w.Push(Wide{f0: k, f32: k}) }
	total := 0
	last := -1
	for v := w.Pop(); v != None; v = w.Pop() {
		x := v.Expect("Some")
		if x.f0 < last || x.f0 != x.f32 { fmt.Println("out of order") }
		last = x.f0
		total += x.f0
	}
	fmt.Println(total)
}
`,
			Out: "0 None None\nSome(4) 1\n333\n333 true 0 None\nSome(0) 10\n[0 1 1 2 3 5 6 7 8 9]\n[] [4]\n1 job1\n2 job2\n3 job3\nnext job4\n[9 7 4 2]\n190\n",
		},
		{
			Name: "heap-edges", // the bottom-up descent of Pop on heaps of two and three, Clear and reuse, IsEmpty, and three thousand pushes with pops between them giving back the multiset pushed
			Src: `package main
import ("fmt"; "heap")
func main() {
	e := heap.From([]int{2, 1})
	fmt.Println(e.IsEmpty(), e.Pop(), e.Pop(), e.Pop(), e.IsEmpty())
	t := heap.From([]int{3, 1, 2})
	fmt.Println(t.Pop(), t.Pop(), t.Pop(), t.Pop())
	c := heap.From([]string{"b".Clone(), "a".Clone()})
	c.Clear()
	fmt.Println(c.Len(), c.IsEmpty())
	c.Push("z".Clone())
	fmt.Println(c.Len(), c.Pop())
	h := heap.New<int>()
	all := []int{}
	got := []int{}
	seed := 9
	for i := 0; i < 3000; i++ {
		seed = (seed*1103515245 + 12345) % 2147483648
		h.Push(seed % 97)
		all.Push(seed % 97)
		if i % 4 == 3 { got.Push(h.Pop().Expect("Some")) }
	}
	for v := h.Pop(); v != None; v = h.Pop() { got.Push(v.Expect("Some")) }
	got.Sort()
	all.Sort()
	same := len(got) == len(all)
	for i := 0; i < len(all) && same; i++ { same = got[i] == all[i] }
	fmt.Println("same multiset", same, len(got))
}
`,
			Out: "false Some(1) Some(2) None true\nSome(1) Some(2) Some(3) None\n0 true\n1 Some(\"z\")\nsame multiset true 3000\n",
		},
		{
			Name: "shortest-floats", // floats print as Go's strconv prints them, the fewest digits that read back: powers of two, where the interval below is half as wide, subnormals, the extremes, float32s, and the layouts of %v, %.3v and %.2f; the expected output is Go's
			Src: `package main
import "fmt"
func main() {
	x := 1.0
	small := []f64{}
	for i := 1; i <= 1074; i++ {
		x = x / 2.0
		if i == 24 || i == 44 || i == 1022 || i == 1074 { small.Push(x) }
	}
	y := 1.0
	for i := 0; i < 89; i++ { y = y * 2.0 }
	fmt.Println(small, y)
	fmt.Println(0.1, 1e23, 5e-324, 1.7976931348623157e308, 123456789.123, 1e21, 1e20, 0.000001, 0.0000001, -2.5, 100.0)
	fmt.Println(f32(1.2621775e-29), f32(1.5474251e+26), f32(1.2379401e+27), f32(0.1), f32(16777216.0), f32(3.4028235e38))
	fmt.Println(fmt.Sprint(1.0 / 3.0), fmt.Sprintf("%v %.3v %.2f", 2.0 / 3.0, 2.0 / 3.0, 2.0 / 3.0))
}
`,
			Out: "[5.960464477539063e-08 5.684341886080802e-14 2.2250738585072014e-308 5e-324] 6.189700196426902e+26\n0.1 1e+23 5e-324 1.7976931348623157e+308 1.23456789123e+08 1e+21 1e+20 1e-06 1e-07 -2.5 100\n1.2621775e-29 1.5474251e+26 1.2379401e+27 0.1 1.6777216e+07 3.4028235e+38\n0.3333333333333333 0.6666666666666666 0.667 0.67\n",
		},
		{
			Name: "map-table", // the map's index through growth across many rebuilds, keys apart only in their high bits, a sliding window of removals, a map emptied and filled again, the size hint, insertion order kept through removal and compaction, an independent clone, struct and tuple keys
			Src: `package main
import "fmt"
struct P impl Eq, Hash { x int; y int }
func main() {
	// Growth across many rebuilds, every key found again, misses missed.
	m := make(map[int]int)
	for i := 0; i < 50000; i++ { m[i * 4096] = i }
	hits := 0
	for i := 0; i < 50000; i++ { if v := m.Get(i * 4096) { if *v == i { hits += 1 } } }
	misses := 0
	for i := 0; i < 50000; i++ { if !m.Contains(i * 4096 + 1) { misses += 1 } }
	fmt.Println(len(m), hits, misses)

	// Churn: a sliding window, the oldest removed as each key comes in.
	w := map[string]int{}
	for i := 0; i < 20000; i++ {
		w.Insert(fmt.Sprint("k", i), i)
		if i >= 100 { w.Remove(fmt.Sprint("k", i - 100)) }
	}
	first := -1
	n := 0
	for k, v := range ref w {
		if first < 0 { first = v }
		n += 1
		_ = k
	}
	fmt.Println(len(w), n, first, w.Contains("k19899"), w.Contains("k19900"))

	// Emptied, then used again; the size hint; insertion order kept through removals.
	e := make(map[string]string, 1000)
	for i := 0; i < 10; i++ { e.Insert(fmt.Sprint(i), fmt.Sprint("v", i)) }
	for i := 0; i < 10; i++ { e.Remove(fmt.Sprint(i)) }
	fmt.Println(len(e), e.Get("3"))
	for _, k := range []string{"z", "a", "m", "b"} { e.Insert(k.Clone(), k.ToUpper()) }
	e.Remove("a")
	e.Insert("a".Clone(), "again".Clone())
	order := []string{}
	for k, v := range ref e { order.Push(k + "=" + v) }
	fmt.Println(order)

	// Clone is independent, in order; struct and tuple keys.
	c := e.Clone()
	c.Insert("q".Clone(), "Q".Clone())
	fmt.Println(len(e), len(c), e.Contains("q"), c.Get("q"))
	ps := map[P]int{}
	ts := map[(int, string)]int{}
	for i := 0; i < 1000; i++ {
		ps[P{x: i, y: i * 7}] += i
		ts[(i % 10, fmt.Sprint(i % 3))] += 1
	}
	fmt.Println(len(ps), ps[P{x: 999, y: 6993}], len(ts), ts[(4, "1")])
}
`,
			Out: "50000 50000 50000\n100 100 19900 false true\n0 None\n[z=Z m=M b=B a=again]\n4 5 false Some(\"Q\")\n1000 999 30 34\n",
		},
		{
			Name: "libc-names", // functions named as the C library's, which the runtime calls, constants, a global, a type and a local named as C's macros: link-time optimization makes the program and the runtime one unit, and a symbol write of the program's must not be libc's
			Src: `package main
import ("fmt"; "os")
const SIZE_MAX = 7
var EOF = 2
struct INT64_MAX { NAN int }
func write(s string) int { return len(s) }
func read(n int) int { return n + 1 }
func open() string { return "open" }
func memset(n int) int { return n * 3 }
func pthread_create(n int) int { return n - 1 }
func main() {
	INT8_MIN := 5
	r := INT64_MAX{NAN: 9}
	fmt.Println(write("abc"), read(1), open(), memset(2), pthread_create(10), SIZE_MAX, EOF, INT8_MIN, r.NAN, r)
	os.Stdout().WriteString("written through the runtime\n")
	done := make(chan int)
	d := done.Clone()
	spawn func() { d <- read(41) }()
	fmt.Println(<-done)
}
`,
			Out: "3 2 open 6 9 7 2 5 9 {9}\nwritten through the runtime\nSome(42)\n",
		},
		{
			Name: "library-corners", // RemoveValue drops what it removes, Contains holds the value lent to it, slices of slices compare, a variable only assigned builds, make's size is evaluated, and a width counts runes as Go does on invalid UTF-8
			Src: `package main
import "fmt"
func size() int {
	fmt.Println("size")
	return 8
}
func main() {
	ws := []string{"a".Clone(), "b".Clone(), "c".Clone()}
	fmt.Println(ws.RemoveValue("b".Clone()), ws.Contains(fmt.Sprint("c")), ws.Contains(fmt.Sprint("z")), ws)
	grid := [][]int{[]int{1, 2}, []int{3}}
	fmt.Println(grid.Contains([]int{3}), grid.Contains([]int{2, 1}))
	m := make(map[string]int, size())
	m["k"] = 1
	last := 0
	for i := 0; i < 3; i++ {
		last = i
	}
	fmt.Printf("%8s|%-6s|%4s|\n", "\x80abc", "日本", "\xff\xfe")
	fmt.Println(len(m))
}
`,
			Out: "true true false [a c]\ntrue false\nsize\n    \x80abc|日本    |  \xff\xfe|\n1\n",
		},
		{
			Name: "sort-stable", // the sort over random, sorted, reversed, equal, organ-pipe, sawtooth and few-valued input, every length to 120 and 20000, elements of 1, 8, 12, 16, 24 and 312 bytes: in order, equal keys in their first order, nothing lost
			Src: `package main
import "fmt"
struct R8 impl Copy { key i32; at i32 }
struct R12 impl Copy { key i32; at i32; pad i32 }
struct R16 impl Copy { key int; at int }
struct R24 impl Copy { key int; at int; pad int }
struct RW { key int; at int; w0 int; w1 int; w2 int; w3 int; w4 int; w5 int; w6 int; w7 int; w8 int; w9 int; w10 int; w11 int; w12 int; w13 int; w14 int; w15 int; w16 int; w17 int; w18 int; w19 int; w20 int; w21 int; w22 int; w23 int; w24 int; w25 int; w26 int; w27 int; w28 int; w29 int; w30 int; w31 int; w32 int; w33 int; w34 int; w35 int; w36 int }
func gen(pat int, n int, i int, seed mut int) int {
	*seed = (*seed * 1103515245 + 12345) % 2147483648
	return match pat {
		0 => *seed % 1000,
		1 => i,
		2 => n - i,
		3 => 7,
		4 => if i < n / 2 { i } else { n - i },
		5 => i % 17,
		_ => *seed % 3,
	}
}
func main() {
	bad := 0
	checks := 0
	seed := 1
	for pat := 0; pat < 7; pat++ {
		for n := 0; n <= 120; n++ {
			bad += check(pat, n, mut seed)
			checks += 1
		}
		bad += check(pat, 20000, mut seed)
		checks += 1
	}
	fmt.Println("checks", checks, "bad", bad)
}
func check(pat int, n int, seed mut int) int {
	bad := 0
	a8 := []R8{}
	a12 := []R12{}
	a16 := []R16{}
	a24 := []R24{}
	aw := []RW{}
	bytes := []byte{}
	sum := 0
	for i := 0; i < n; i++ {
		k := gen(pat, n, i, seed)
		sum += k
		a8.Push(R8{key: i32(k), at: i32(i)})
		a12.Push(R12{key: i32(k), at: i32(i), pad: 0})
		a16.Push(R16{key: k, at: i})
		a24.Push(R24{key: k, at: i, pad: 0})
		if n <= 120 { aw.Push(RW{key: k, at: i}) }
		bytes.Push(byte(k % 256))
	}
	a8.SortBy(func(x ref R8, y ref R8) bool { return x.key < y.key })
	a12.SortBy(func(x ref R12, y ref R12) bool { return x.key < y.key })
	a16.SortBy(func(x ref R16, y ref R16) bool { return x.key < y.key })
	a24.SortBy(func(x ref R24, y ref R24) bool { return x.key < y.key })
	aw.SortBy(func(x ref RW, y ref RW) bool { return x.key < y.key })
	bytes.Sort()
	s8 := 0
	s16 := 0
	for i := 0; i < n; i++ {
		s8 += int(a8[i].key)
		s16 += a16[i].key
		if i > 0 {
			if a8[i-1].key > a8[i].key || (a8[i-1].key == a8[i].key && a8[i-1].at > a8[i].at) { bad += 1 }
			if a12[i-1].key > a12[i].key || (a12[i-1].key == a12[i].key && a12[i-1].at > a12[i].at) { bad += 1 }
			if a16[i-1].key > a16[i].key || (a16[i-1].key == a16[i].key && a16[i-1].at > a16[i].at) { bad += 1 }
			if a24[i-1].key > a24[i].key || (a24[i-1].key == a24[i].key && a24[i-1].at > a24[i].at) { bad += 1 }
			if bytes[i-1] > bytes[i] { bad += 1 }
			if n <= 120 && (aw[i-1].key > aw[i].key || (aw[i-1].key == aw[i].key && aw[i-1].at > aw[i].at)) { bad += 1 }
		}
	}
	if s8 != sum || s16 != sum { bad += 1000 }
	return bad
}
`,
			Out: "checks 854 bad 0\n",
		},
		{
			Name: "sort-quicksort", // the sort's quicksort, small sort and runs of about √n, on either side of each length the sort changes course at, to 100000: unique, four-valued, sawtooth, nearly descending and nearly sorted records in order, stable and each once; numbers; strings few and many, and under a comparison that contradicts itself each still once; floats with NaN each still once
			Src: `package main
import ("fmt"; "math")
struct R impl Copy { key int; at int }
func next(seed mut int) int {
	*seed = (*seed * 1103515245 + 12345) % 2147483648
	return *seed
}
func key(pat int, n int, i int, seed mut int) int {
	return match pat {
		0 => next(seed),
		1 => next(seed) % 4,
		2 => i / 64 * 2 - i % 64,
		3 => n - i + next(seed) % 8,
		_ => if i % 100 == 0 { next(seed) } else { i },
	}
}
func records(n int, seed mut int) int {
	bad := 0
	for pat := 0; pat < 5; pat++ {
		rs := []R{}
		for i := 0; i < n; i++ { rs.Push(R{key: key(pat, n, i, seed), at: i}) }
		rs.SortBy(func(x ref R, y ref R) bool { return x.key < y.key })
		seen := make([]bool, n)
		for i := 0; i < n; i++ {
			if seen[rs[i].at] { bad += 1 }
			seen[rs[i].at] = true
			if i > 0 && (rs[i-1].key > rs[i].key || (rs[i-1].key == rs[i].key && rs[i-1].at > rs[i].at)) { bad += 1 }
		}
	}
	return bad
}
func numbers(n int, seed mut int) int {
	xs := []int{}
	sum := 0
	for i := 0; i < n; i++ {
		x := next(seed)
		xs.Push(x)
		sum += x
	}
	xs.Sort()
	bad := 0
	for i := 0; i < n; i++ {
		sum -= xs[i]
		if i > 0 && xs[i-1] > xs[i] { bad += 1 }
	}
	if sum != 0 { bad += 1000 }
	return bad
}
func texts(n int, m int, seed mut int) int {
	xs := []string{}
	total := 0
	for i := 0; i < n; i++ {
		s := fmt.Sprint(next(seed) % m)
		total += len(s)
		xs.Push(s)
	}
	xs.Sort()
	bad := 0
	for i := 0; i < n; i++ {
		total -= len(xs[i])
		if i > 0 && xs[i-1] > xs[i] { bad += 1 }
	}
	ys := xs.Clone()
	ys.SortBy(func(a ref string, b ref string) bool { return (len(a) + len(b)) % 3 == 0 })
	for i := 0; i < n; i++ { total += len(ys[i]) - len(xs[i]) }
	if total != 0 { bad += 1000 }
	return bad
}
func floats(n int, seed mut int) int {
	nan := math.Sqrt(-1.0)
	fs := []f64{}
	nans := 0
	sum := 0.0
	for i := 0; i < n; i++ {
		if next(seed) % 10 == 0 {
			fs.Push(nan)
			nans += 1
		} else {
			f := f64(next(seed) % 1000)
			fs.Push(f)
			sum += f
		}
	}
	fs.Sort()
	for i := 0; i < n; i++ {
		if fs[i] != fs[i] { nans -= 1 } else { sum -= fs[i] }
	}
	if nans != 0 || sum != 0.0 { return 1 }
	return 0
}
func main() {
	seed := 7
	sizes := []int{21, 33, 64, 65, 100, 129, 1000, 4096, 4097, 100000}
	bad := 0
	for i := 0; i < len(sizes); i++ {
		n := sizes[i]
		bad += records(n, mut seed) + numbers(n, mut seed) + floats(n, mut seed)
		if n <= 4097 { bad += texts(n, 50, mut seed) + texts(n, 1000000, mut seed) }
	}
	fmt.Println("sizes", len(sizes), "bad", bad)
}
`,
			Out: "sizes 10 bad 0\n",
		},
		{
			Name: "read-whole", // os.ReadToString into one block of the file's size: an empty file, one byte, either side of the 512 bytes a file without a size starts from, a page, and a megabyte and three, each read back as written; /dev/null, which has no size
			Src: `package main
import ("fmt"; "os"; "strings")
func main() {
	dir := os.MkdirTemp("", "whole").Expect("tempdir")
	sizes := []int{0, 1, 511, 512, 513, 4096, 70000, 1048579}
	same := 0
	for i := 0; i < len(sizes); i++ {
		n := sizes[i]
		want := (strings.Repeat("data", n / 4) ?? "") + (strings.Repeat("!", n % 4) ?? "")
		p := dir + "/f" + fmt.Sprint(n)
		os.WriteString(ref p, ref want).Expect("write")
		got := os.ReadToString(ref p).Expect("read")
		if got == want && len(got) == n { same += 1 }
	}
	fmt.Println("same", same, "of", len(sizes))
	fmt.Println(len(os.ReadToString("/dev/null").Expect("read /dev/null")))
	os.RemoveAll(ref dir).Expect("remove")
}
`,
			Out: "same 8 of 8\n0\n",
		},
		{
			Name: "net-deadlines", // Go's deadlines: a read that times out near its deadline with Kind TimedOut and Go's message, a passed deadline failing at once until moved, a deadline cleared and one cleared before it fired, one in the past, an Accept that times out, a write held by a peer that never reads timing out partway with the bytes it wrote, and DialTimeout; main waits on a socket with a deadline while the server waits on a channel, so the deadlock detector must count the deadline
			Src: `package main
import ("fmt"; "io"; "net"; "time")
func serve(l net.Listener, gate chan int, done chan int) {
	c := l.Accept().Expect("accept")
	<-gate
	c.WriteString("late\n").Expect("write late")
	<-gate
	time.Sleep(60 * time.Millisecond)
	c.WriteString("again\n").Expect("write again")
	stuck := l.Accept().Expect("accept stuck")
	<-gate
	one := make([]byte, 1)
	done <- stuck.Read(mut one).Expect("read stuck")
}
func timedOut(r Result<int, io.Error>) bool {
	return match r {
		Ok(_) => false,
		Err(e) => e.Kind() == io.TimedOut && e.Error().HasSuffix(": i/o timeout"),
	}
}
func main() {
	l := net.Listen("127.0.0.1", 0).Expect("listen")
	port := l.Port()
	gate := make(chan int)
	done := make(chan int)
	spawn serve(l, gate.Clone(), done.Clone())
	c := net.Dial("127.0.0.1", port).Expect("dial")
	buf := make([]byte, 64)
	var none time.Time

	c.SetReadDeadline(time.Now().Add(50 * time.Millisecond)).Expect("deadline")
	start := time.Now()
	r := c.Read(mut buf)
	waited := time.Since(start)
	fmt.Println("read timed out", timedOut(r), waited >= 40 * time.Millisecond, waited < 2 * time.Second)

	start = time.Now()
	fmt.Println("stays passed", timedOut(c.Read(mut buf)), time.Since(start) < 20 * time.Millisecond)

	c.SetReadDeadline(none).Expect("clear")
	gate <- 1
	fmt.Println("cleared, read", c.Read(mut buf).Expect("read late"))

	c.SetReadDeadline(time.Now().Add(20 * time.Millisecond)).Expect("deadline")
	c.SetReadDeadline(none).Expect("clear before it fires")
	gate <- 1
	fmt.Println("never fired, read", c.Read(mut buf).Expect("read again"))

	c.SetReadDeadline(time.Now().Add(-time.Second)).Expect("past")
	start = time.Now()
	fmt.Println("past", timedOut(c.Read(mut buf)), time.Since(start) < 20 * time.Millisecond)

	l2 := net.Listen("127.0.0.1", 0).Expect("listen 2")
	l2.SetDeadline(time.Now().Add(30 * time.Millisecond)).Expect("listener deadline")
	match l2.Accept() {
		Ok(_) => fmt.Println("accepted"),
		Err(e) => fmt.Println("accept timed out", e.Kind() == io.TimedOut, e.Error().HasPrefix("accept tcp 127.0.0.1:"), e.Error().HasSuffix(": i/o timeout")),
	}

	big := make([]byte, 64 << 20) // made before the deadline is set, which then bounds the write alone: filling 64 MB can outlast 100 ms on a loaded machine
	s := net.Dial("127.0.0.1", port).Expect("dial stuck")
	s.SetWriteDeadline(time.Now().Add(100 * time.Millisecond)).Expect("write deadline")
	match s.Write(ref big) {
		Ok(n) => fmt.Println("wrote", n),
		Err(e) => fmt.Println("write timed out", e.Kind() == io.TimedOut, e.Written() > 0, e.Written() < len(big)),
	}

	d := net.DialTimeout("127.0.0.1", l2.Port(), time.Second)
	fmt.Println("dial with a timeout", d.IsOk())
	gate <- 1
	fmt.Println("served", <-done ?? -1)
}
`,
			Out: "read timed out true true true\nstays passed true true\ncleared, read 5\nnever fired, read 6\npast true true\naccept timed out true true true\nwrite timed out true true true\ndial with a timeout true\nserved 1\n",
		},
		{
			Name: "string-building", // a chain of + in one allocation, strings.Repeat by doubling and Join into a buffer the string takes, and += appending in place with room to spare: from a literal, across doublings, a part of itself, itself, a map entry and an element
			Src: `package main
import ("fmt"; "strings")
func part(s ref string) string { return s[0:2].Clone() }
func mk(s string) string { return s.Clone() }
func main() {
	a := "a".Clone()
	b := "bb"
	c := a + "-" + b + "-" + mk("c") + (b + "!" + a) + ""
	fmt.Println(c, len(c))
	d := "" + "" + ""
	fmt.Println(len(d), d == "")
	x := mk("x") + mk("y") + mk("z")
	fmt.Println(x, strings.Repeat("ab", 5) ?? "", strings.Repeat("q", 0) ?? "", strings.Repeat("", 9) ?? "", strings.Join([]string{"p".Clone(), "q".Clone(), "r".Clone()}, ", "), strings.Join([]string{}, "-"))
	parts := []string{}
	for i := 0; i < 5; i++ { parts.Push(fmt.Sprint(i) + ":" + mk("v") + ";") }
	fmt.Println(parts, (strings.Repeat("xyz", 7) ?? "").Len())
	grow()
}
func grow() {
	s := "lit"
	s += "+"
	s += fmt.Sprint(42)
	fmt.Println(s, len(s))
	t := "".Clone()
	for i := 0; i < 1000; i++ { t += "ab" }
	fmt.Println(len(t), t[0:6], t[1994:2000])
	u := "xy".Clone()
	u += part(ref u)
	u += u.Clone()
	fmt.Println(u)
	m := map[string]string{"k": "v".Clone()}
	m["k"] += "w"
	m["k"] += "z"
	fmt.Println(m["k"])
	ws := []string{"a".Clone()}
	ws[0] += "b"
	fmt.Println(ws, s == "lit+42")
}
`,
			Out: "a-bb-cbb!a 10\n0 true\nxyz ababababab   p, q, r \n[0:v; 1:v; 2:v; 3:v; 4:v;] 21\nlit+42 6\n2000 ababab ababab\nxyxyxyxy\nvwz\n[ab] true\n",
		},
		{
			Name: "integer-text", // integers written as Go's formatBits writes them, two digits a step, 0 to 99 views of a table: the edges of every width, the extremes of int and u64, widths and precisions, and a small number's text grown by +=; the expected output is Go's
			Src: `package main
import ("fmt"; "strconv")
func main() {
	xs := []int{0, 1, 9, 10, 11, 42, 99, 100, 101, 999, 1000, -1, -9, -10, -99, -100, 1234567890, -1234567890, 9223372036854775807, -9223372036854775807 - 1}
	for _, x := range xs { s := strconv.Itoa(x); t := fmt.Sprint(x); fmt.Printf("%d|%6d|%-5d|%s|%s|%v|%.3d\n", x, x, x, s, t, x, x) }
	var u u64 = 18446744073709551615
	var z u8 = 7
	fmt.Println(u, z, u64(0), 3 + 4, -0)
	s := "n=" + strconv.Itoa(5)
	s += strconv.Itoa(77)
	fmt.Println(s)
}
`,
			Out: "0|     0|0    |0|0|0|000\n1|     1|1    |1|1|1|001\n9|     9|9    |9|9|9|009\n10|    10|10   |10|10|10|010\n11|    11|11   |11|11|11|011\n42|    42|42   |42|42|42|042\n99|    99|99   |99|99|99|099\n100|   100|100  |100|100|100|100\n101|   101|101  |101|101|101|101\n999|   999|999  |999|999|999|999\n1000|  1000|1000 |1000|1000|1000|1000\n-1|    -1|-1   |-1|-1|-1|-001\n-9|    -9|-9   |-9|-9|-9|-009\n-10|   -10|-10  |-10|-10|-10|-010\n-99|   -99|-99  |-99|-99|-99|-099\n-100|  -100|-100 |-100|-100|-100|-100\n1234567890|1234567890|1234567890|1234567890|1234567890|1234567890|1234567890\n-1234567890|-1234567890|-1234567890|-1234567890|-1234567890|-1234567890|-1234567890\n9223372036854775807|9223372036854775807|9223372036854775807|9223372036854775807|9223372036854775807|9223372036854775807|9223372036854775807\n-9223372036854775808|-9223372036854775808|-9223372036854775808|-9223372036854775808|-9223372036854775808|-9223372036854775808|-9223372036854775808\n18446744073709551615 7 0 7 0\nn=577\n",
		},
		{
			Name: "text-ascii", // Fields, Trim, ToUpper and ToLower through their ASCII fast paths and their Unicode ones: every ASCII space, U+0085, U+00A0 and U+3000, invalid bytes, empty and all-space text; case by Unicode's full mapping, so ß upper-cases to SS
			Src: `package main
import ("fmt"; "strings")
func main() {
	ins := []string{"", "   ", "a", " a b  c ", "\ta\nb\vc\fd\re ", "x\u0085y z　w", "\xff a \xfe", "Hello, World! 123", "ÀéÎõÜ straße ǅ", "  lead", "trail\t\n", "a\x80b c"}
	for _, s := range ins {
		fs := s.Fields()
		n := len(fs)
		parts := []string{}
		for f := range fs { parts.Push("[" + f + "]") }
		fmt.Printf("%q %d %s %q %q %q\n", s, n, strings.Join(ref parts, ""), s.Trim(), s.ToUpper(), s.ToLower())
	}
}
`,
			Out: "\"\" 0  \"\" \"\" \"\"\n\"   \" 0  \"\" \"   \" \"   \"\n\"a\" 1 [a] \"a\" \"A\" \"a\"\n\" a b  c \" 3 [a][b][c] \"a b  c\" \" A B  C \" \" a b  c \"\n\"\\ta\\nb\\vc\\fd\\re \" 5 [a][b][c][d][e] \"a\\nb\\vc\\fd\\re\" \"\\tA\\nB\\vC\\fD\\rE \" \"\\ta\\nb\\vc\\fd\\re \"\n\"x\\u0085y\\u00a0z\\u3000w\" 4 [x][y][z][w] \"x\\u0085y\\u00a0z\\u3000w\" \"X\\u0085Y\\u00a0Z\\u3000W\" \"x\\u0085y\\u00a0z\\u3000w\"\n\"\\xff a \\xfe\" 3 [\xff][a][\xfe] \"\\xff a \\xfe\" \"\xef\xbf\xbd A \xef\xbf\xbd\" \"\xef\xbf\xbd a \xef\xbf\xbd\"\n\"Hello, World! 123\" 3 [Hello,][World!][123] \"Hello, World! 123\" \"HELLO, WORLD! 123\" \"hello, world! 123\"\n\"\xc3\x80\xc3\xa9\xc3\x8e\xc3\xb5\xc3\x9c stra\xc3\x9fe \xc7\x85\" 3 [\xc3\x80\xc3\xa9\xc3\x8e\xc3\xb5\xc3\x9c][stra\xc3\x9fe][\xc7\x85] \"\xc3\x80\xc3\xa9\xc3\x8e\xc3\xb5\xc3\x9c stra\xc3\x9fe \xc7\x85\" \"\xc3\x80\xc3\x89\xc3\x8e\xc3\x95\xc3\x9c STRASSE \xc7\x84\" \"\xc3\xa0\xc3\xa9\xc3\xae\xc3\xb5\xc3\xbc stra\xc3\x9fe \xc7\x86\"\n\"  lead\" 1 [lead] \"lead\" \"  LEAD\" \"  lead\"\n\"trail\\t\\n\" 1 [trail] \"trail\" \"TRAIL\\t\\n\" \"trail\\t\\n\"\n\"a\\x80b c\" 2 [a\x80b][c] \"a\\x80b c\" \"A\xef\xbf\xbdB C\" \"a\xef\xbf\xbdb c\"\n",
		},
		{
			Name: "fixed-floats", // %f with a precision and %v with one through Ryū's fixed digits, rounded half to even on the exact value: the ties 0.125, 2.5 and 0.375, 1.005 and 9.995 just off them, their neighbours, and doubles of every magnitude, with the C library past seventeen digits; the expected output is Go's
			Src: `package main
import ("fmt"; "strconv")
func main() {
	ins := []string{"0.125", "-0.125", "0.12499999999999999", "0.12500000000000003", "0.375", "-0.375", "0.37499999999999994", "0.37500000000000006", "1.005", "-1.005", "1.0049999999999997", "1.0050000000000001", "2.5", "-2.5", "2.4999999999999996", "2.5000000000000004", "3.5", "-3.5", "3.4999999999999996", "3.5000000000000004", "0.005", "-0.005", "0.004999999999999999", "0.005000000000000001", "0.0049", "-0.0049", "0.004899999999999999", "0.004900000000000001", "9.995", "-9.995", "9.994999999999997", "9.995000000000001", "9.996", "-9.996", "9.995999999999999", "9.996000000000002", "99.995", "-99.995", "99.99499999999999", "99.99500000000002", "0.5", "-0.5", "0.49999999999999994", "0.5000000000000001", "1.5", "-1.5", "1.4999999999999998", "1.5000000000000002", "1e+15", "-1e+15", "9.999999999999999e+14", "1.0000000000000001e+15", "1e+16", "-1e+16", "9.999999999999998e+15", "1.0000000000000002e+16", "1.234567890123456e+14", "-1.234567890123456e+14", "1.2345678901234558e+14", "1.2345678901234561e+14", "1e-07", "-1e-07", "9.999999999999998e-08", "1.0000000000000001e-07", "4.35", "-4.35", "4.349999999999999", "4.3500000000000005", "0.045", "-0.045", "0.04499999999999999", "0.045000000000000005", "1.0000000000000002", "-1.0000000000000002", "1", "1.0000000000000004", "5e-324", "-5e-324", "0", "1e-323", "1.7976931348623157e+308", "-1.7976931348623157e+308", "1.7976931348623155e+308", "1.7976931348623155e+308", "0.1", "-0.1", "0.09999999999999999", "0.10000000000000002", "0.7", "-0.7", "0.6999999999999998", "0.7000000000000001", "1.4090107006278405e-08", "5.044796412934267e+08", "6.149111820836415e+09", "3.438686998980422e-12", "4.0113163621932597e+09", "7.042912116903872e+06", "21.419156992842723", "5.147350980203335e-118", "-1.582465529393064e-307", "-2.1694375702597584e+133", "-1.687153510647768e+234", "10305.74934931837", "-1.6993061890316662e+262", "1.799191905315514e+195", "2.105647572150013e-245", "1.8578164875920722e+06", "2.5738907233711455e+215", "5.885009816741847e+139", "2.055114666540428e-249", "-2.4650643802703088e+98", "2.8480983273663998e+10", "8.319614706356412e+08", "177116.33989191786", "-2.0255557009343277e+73", "1.0452247106370279e-05", "9.579123849138929e-301", "1.6104516791512957e-07", "4.787044437283927e+10"}
	for _, s := range ref ins {
		x := strconv.ParseFloat(s).Expect("parse")
		fmt.Printf("%.0f %.1f %.2f %.3f %.6f %.1v %.3v %.10v %.17v\n", x, x, x, x, x, x, x, x, x)
	}
}
`,
			Out: "0 0.1 0.12 0.125 0.125000 0.1 0.125 0.125 0.125\n-0 -0.1 -0.12 -0.125 -0.125000 -0.1 -0.125 -0.125 -0.125\n0 0.1 0.12 0.125 0.125000 0.1 0.125 0.125 0.12499999999999999\n0 0.1 0.13 0.125 0.125000 0.1 0.125 0.125 0.12500000000000003\n0 0.4 0.38 0.375 0.375000 0.4 0.375 0.375 0.375\n-0 -0.4 -0.38 -0.375 -0.375000 -0.4 -0.375 -0.375 -0.375\n0 0.4 0.37 0.375 0.375000 0.4 0.375 0.375 0.37499999999999994\n0 0.4 0.38 0.375 0.375000 0.4 0.375 0.375 0.37500000000000006\n1 1.0 1.00 1.005 1.005000 1 1 1.005 1.0049999999999999\n-1 -1.0 -1.00 -1.005 -1.005000 -1 -1 -1.005 -1.0049999999999999\n1 1.0 1.00 1.005 1.005000 1 1 1.005 1.0049999999999997\n1 1.0 1.01 1.005 1.005000 1 1.01 1.005 1.0050000000000001\n2 2.5 2.50 2.500 2.500000 2 2.5 2.5 2.5\n-2 -2.5 -2.50 -2.500 -2.500000 -2 -2.5 -2.5 -2.5\n2 2.5 2.50 2.500 2.500000 2 2.5 2.5 2.4999999999999996\n3 2.5 2.50 2.500 2.500000 3 2.5 2.5 2.5000000000000004\n4 3.5 3.50 3.500 3.500000 4 3.5 3.5 3.5\n-4 -3.5 -3.50 -3.500 -3.500000 -4 -3.5 -3.5 -3.5\n3 3.5 3.50 3.500 3.500000 3 3.5 3.5 3.4999999999999996\n4 3.5 3.50 3.500 3.500000 4 3.5 3.5 3.5000000000000004\n0 0.0 0.01 0.005 0.005000 0.005 0.005 0.005 0.0050000000000000001\n-0 -0.0 -0.01 -0.005 -0.005000 -0.005 -0.005 -0.005 -0.0050000000000000001\n0 0.0 0.00 0.005 0.005000 0.005 0.005 0.005 0.0049999999999999992\n0 0.0 0.01 0.005 0.005000 0.005 0.005 0.005 0.005000000000000001\n0 0.0 0.00 0.005 0.004900 0.005 0.0049 0.0049 0.0048999999999999998\n-0 -0.0 -0.00 -0.005 -0.004900 -0.005 -0.0049 -0.0049 -0.0048999999999999998\n0 0.0 0.00 0.005 0.004900 0.005 0.0049 0.0049 0.004899999999999999\n0 0.0 0.00 0.005 0.004900 0.005 0.0049 0.0049 0.0049000000000000007\n10 10.0 9.99 9.995 9.995000 1e+01 9.99 9.995 9.9949999999999992\n-10 -10.0 -9.99 -9.995 -9.995000 -1e+01 -9.99 -9.995 -9.9949999999999992\n10 10.0 9.99 9.995 9.995000 1e+01 9.99 9.995 9.9949999999999974\n10 10.0 10.00 9.995 9.995000 1e+01 10 9.995 9.995000000000001\n10 10.0 10.00 9.996 9.996000 1e+01 10 9.996 9.9960000000000004\n-10 -10.0 -10.00 -9.996 -9.996000 -1e+01 -10 -9.996 -9.9960000000000004\n10 10.0 10.00 9.996 9.996000 1e+01 10 9.996 9.9959999999999987\n10 10.0 10.00 9.996 9.996000 1e+01 10 9.996 9.9960000000000022\n100 100.0 100.00 99.995 99.995000 1e+02 100 99.995 99.995000000000005\n-100 -100.0 -100.00 -99.995 -99.995000 -1e+02 -100 -99.995 -99.995000000000005\n100 100.0 99.99 99.995 99.995000 1e+02 100 99.995 99.99499999999999\n100 100.0 100.00 99.995 99.995000 1e+02 100 99.995 99.995000000000019\n0 0.5 0.50 0.500 0.500000 0.5 0.5 0.5 0.5\n-0 -0.5 -0.50 -0.500 -0.500000 -0.5 -0.5 -0.5 -0.5\n0 0.5 0.50 0.500 0.500000 0.5 0.5 0.5 0.49999999999999994\n1 0.5 0.50 0.500 0.500000 0.5 0.5 0.5 0.50000000000000011\n2 1.5 1.50 1.500 1.500000 2 1.5 1.5 1.5\n-2 -1.5 -1.50 -1.500 -1.500000 -2 -1.5 -1.5 -1.5\n1 1.5 1.50 1.500 1.500000 1 1.5 1.5 1.4999999999999998\n2 1.5 1.50 1.500 1.500000 2 1.5 1.5 1.5000000000000002\n1000000000000000 1000000000000000.0 1000000000000000.00 1000000000000000.000 1000000000000000.000000 1e+15 1e+15 1e+15 1000000000000000\n-1000000000000000 -1000000000000000.0 -1000000000000000.00 -1000000000000000.000 -1000000000000000.000000 -1e+15 -1e+15 -1e+15 -1000000000000000\n1000000000000000 999999999999999.9 999999999999999.88 999999999999999.875 999999999999999.875000 1e+15 1e+15 1e+15 999999999999999.88\n1000000000000000 1000000000000000.1 1000000000000000.12 1000000000000000.125 1000000000000000.125000 1e+15 1e+15 1e+15 1000000000000000.1\n10000000000000000 10000000000000000.0 10000000000000000.00 10000000000000000.000 10000000000000000.000000 1e+16 1e+16 1e+16 10000000000000000\n-10000000000000000 -10000000000000000.0 -10000000000000000.00 -10000000000000000.000 -10000000000000000.000000 -1e+16 -1e+16 -1e+16 -10000000000000000\n9999999999999998 9999999999999998.0 9999999999999998.00 9999999999999998.000 9999999999999998.000000 1e+16 1e+16 1e+16 9999999999999998\n10000000000000002 10000000000000002.0 10000000000000002.00 10000000000000002.000 10000000000000002.000000 1e+16 1e+16 1e+16 10000000000000002\n123456789012346 123456789012345.6 123456789012345.59 123456789012345.594 123456789012345.593750 1e+14 1.23e+14 1.23456789e+14 123456789012345.59\n-123456789012346 -123456789012345.6 -123456789012345.59 -123456789012345.594 -123456789012345.593750 -1e+14 -1.23e+14 -1.23456789e+14 -123456789012345.59\n123456789012346 123456789012345.6 123456789012345.58 123456789012345.578 123456789012345.578125 1e+14 1.23e+14 1.23456789e+14 123456789012345.58\n123456789012346 123456789012345.6 123456789012345.61 123456789012345.609 123456789012345.609375 1e+14 1.23e+14 1.23456789e+14 123456789012345.61\n0 0.0 0.00 0.000 0.000000 1e-07 1e-07 1e-07 9.9999999999999995e-08\n-0 -0.0 -0.00 -0.000 -0.000000 -1e-07 -1e-07 -1e-07 -9.9999999999999995e-08\n0 0.0 0.00 0.000 0.000000 1e-07 1e-07 1e-07 9.9999999999999982e-08\n0 0.0 0.00 0.000 0.000000 1e-07 1e-07 1e-07 1.0000000000000001e-07\n4 4.3 4.35 4.350 4.350000 4 4.35 4.35 4.3499999999999996\n-4 -4.3 -4.35 -4.350 -4.350000 -4 -4.35 -4.35 -4.3499999999999996\n4 4.3 4.35 4.350 4.350000 4 4.35 4.35 4.3499999999999988\n4 4.4 4.35 4.350 4.350000 4 4.35 4.35 4.3500000000000005\n0 0.0 0.04 0.045 0.045000 0.04 0.045 0.045 0.044999999999999998\n-0 -0.0 -0.04 -0.045 -0.045000 -0.04 -0.045 -0.045 -0.044999999999999998\n0 0.0 0.04 0.045 0.045000 0.04 0.045 0.045 0.044999999999999991\n0 0.0 0.05 0.045 0.045000 0.05 0.045 0.045 0.045000000000000005\n1 1.0 1.00 1.000 1.000000 1 1 1 1.0000000000000002\n-1 -1.0 -1.00 -1.000 -1.000000 -1 -1 -1 -1.0000000000000002\n1 1.0 1.00 1.000 1.000000 1 1 1 1\n1 1.0 1.00 1.000 1.000000 1 1 1 1.0000000000000004\n0 0.0 0.00 0.000 0.000000 5e-324 4.94e-324 4.940656458e-324 4.9406564584124654e-324\n-0 -0.0 -0.00 -0.000 -0.000000 -5e-324 -4.94e-324 -4.940656458e-324 -4.9406564584124654e-324\n0 0.0 0.00 0.000 0.000000 0 0 0 0\n0 0.0 0.00 0.000 0.000000 1e-323 9.88e-324 9.881312917e-324 9.8813129168249309e-324\n179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368 179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368.0 179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368.00 179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368.000 179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368.000000 2e+308 1.8e+308 1.797693135e+308 1.7976931348623157e+308\n-179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368 -179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368.0 -179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368.00 -179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368.000 -179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368.000000 -2e+308 -1.8e+308 -1.797693135e+308 -1.7976931348623157e+308\n179769313486231550856124328384506240234343437157459335924404872448581845754556114388470639943126220321960804027157371570809852884964511743044087662767600909594331927728237078876188760579532563768698654064825262115771015791463983014857704008123419459386245141723703148097529108423358883457665451722744025579520 179769313486231550856124328384506240234343437157459335924404872448581845754556114388470639943126220321960804027157371570809852884964511743044087662767600909594331927728237078876188760579532563768698654064825262115771015791463983014857704008123419459386245141723703148097529108423358883457665451722744025579520.0 179769313486231550856124328384506240234343437157459335924404872448581845754556114388470639943126220321960804027157371570809852884964511743044087662767600909594331927728237078876188760579532563768698654064825262115771015791463983014857704008123419459386245141723703148097529108423358883457665451722744025579520.00 179769313486231550856124328384506240234343437157459335924404872448581845754556114388470639943126220321960804027157371570809852884964511743044087662767600909594331927728237078876188760579532563768698654064825262115771015791463983014857704008123419459386245141723703148097529108423358883457665451722744025579520.000 179769313486231550856124328384506240234343437157459335924404872448581845754556114388470639943126220321960804027157371570809852884964511743044087662767600909594331927728237078876188760579532563768698654064825262115771015791463983014857704008123419459386245141723703148097529108423358883457665451722744025579520.000000 2e+308 1.8e+308 1.797693135e+308 1.7976931348623155e+308\n179769313486231550856124328384506240234343437157459335924404872448581845754556114388470639943126220321960804027157371570809852884964511743044087662767600909594331927728237078876188760579532563768698654064825262115771015791463983014857704008123419459386245141723703148097529108423358883457665451722744025579520 179769313486231550856124328384506240234343437157459335924404872448581845754556114388470639943126220321960804027157371570809852884964511743044087662767600909594331927728237078876188760579532563768698654064825262115771015791463983014857704008123419459386245141723703148097529108423358883457665451722744025579520.0 179769313486231550856124328384506240234343437157459335924404872448581845754556114388470639943126220321960804027157371570809852884964511743044087662767600909594331927728237078876188760579532563768698654064825262115771015791463983014857704008123419459386245141723703148097529108423358883457665451722744025579520.00 179769313486231550856124328384506240234343437157459335924404872448581845754556114388470639943126220321960804027157371570809852884964511743044087662767600909594331927728237078876188760579532563768698654064825262115771015791463983014857704008123419459386245141723703148097529108423358883457665451722744025579520.000 179769313486231550856124328384506240234343437157459335924404872448581845754556114388470639943126220321960804027157371570809852884964511743044087662767600909594331927728237078876188760579532563768698654064825262115771015791463983014857704008123419459386245141723703148097529108423358883457665451722744025579520.000000 2e+308 1.8e+308 1.797693135e+308 1.7976931348623155e+308\n0 0.1 0.10 0.100 0.100000 0.1 0.1 0.1 0.10000000000000001\n-0 -0.1 -0.10 -0.100 -0.100000 -0.1 -0.1 -0.1 -0.10000000000000001\n0 0.1 0.10 0.100 0.100000 0.1 0.1 0.1 0.099999999999999992\n0 0.1 0.10 0.100 0.100000 0.1 0.1 0.1 0.10000000000000002\n1 0.7 0.70 0.700 0.700000 0.7 0.7 0.7 0.69999999999999996\n-1 -0.7 -0.70 -0.700 -0.700000 -0.7 -0.7 -0.7 -0.69999999999999996\n1 0.7 0.70 0.700 0.700000 0.7 0.7 0.7 0.69999999999999984\n1 0.7 0.70 0.700 0.700000 0.7 0.7 0.7 0.70000000000000007\n0 0.0 0.00 0.000 0.000000 1e-08 1.41e-08 1.409010701e-08 1.4090107006278405e-08\n504479641 504479641.3 504479641.29 504479641.293 504479641.293427 5e+08 5.04e+08 504479641.3 504479641.29342669\n6149111821 6149111820.8 6149111820.84 6149111820.836 6149111820.836415 6e+09 6.15e+09 6149111821 6149111820.8364153\n0 0.0 0.00 0.000 0.000000 3e-12 3.44e-12 3.438686999e-12 3.438686998980422e-12\n4011316362 4011316362.2 4011316362.19 4011316362.193 4011316362.193260 4e+09 4.01e+09 4011316362 4011316362.1932597\n7042912 7042912.1 7042912.12 7042912.117 7042912.116904 7e+06 7.04e+06 7042912.117 7042912.1169038722\n21 21.4 21.42 21.419 21.419157 2e+01 21.4 21.41915699 21.419156992842723\n0 0.0 0.00 0.000 0.000000 5e-118 5.15e-118 5.14735098e-118 5.1473509802033347e-118\n-0 -0.0 -0.00 -0.000 -0.000000 -2e-307 -1.58e-307 -1.582465529e-307 -1.5824655293930639e-307\n-21694375702597583508002276577820655631110356710487949646856787742518770624458443529133884920935656840804267152116957807698079840731136 -21694375702597583508002276577820655631110356710487949646856787742518770624458443529133884920935656840804267152116957807698079840731136.0 -21694375702597583508002276577820655631110356710487949646856787742518770624458443529133884920935656840804267152116957807698079840731136.00 -21694375702597583508002276577820655631110356710487949646856787742518770624458443529133884920935656840804267152116957807698079840731136.000 -21694375702597583508002276577820655631110356710487949646856787742518770624458443529133884920935656840804267152116957807698079840731136.000000 -2e+133 -2.17e+133 -2.16943757e+133 -2.1694375702597584e+133\n-1687153510647768053302725529160349147406376366380260116673215061391039938778732840566451018155938032586103425794642371006236307049676098011914383650955838862121068395844778279449556399188383725195516904932690344163249133536695568826368 -1687153510647768053302725529160349147406376366380260116673215061391039938778732840566451018155938032586103425794642371006236307049676098011914383650955838862121068395844778279449556399188383725195516904932690344163249133536695568826368.0 -1687153510647768053302725529160349147406376366380260116673215061391039938778732840566451018155938032586103425794642371006236307049676098011914383650955838862121068395844778279449556399188383725195516904932690344163249133536695568826368.00 -1687153510647768053302725529160349147406376366380260116673215061391039938778732840566451018155938032586103425794642371006236307049676098011914383650955838862121068395844778279449556399188383725195516904932690344163249133536695568826368.000 -1687153510647768053302725529160349147406376366380260116673215061391039938778732840566451018155938032586103425794642371006236307049676098011914383650955838862121068395844778279449556399188383725195516904932690344163249133536695568826368.000000 -2e+234 -1.69e+234 -1.687153511e+234 -1.6871535106477681e+234\n10306 10305.7 10305.75 10305.749 10305.749349 1e+04 1.03e+04 10305.74935 10305.74934931837\n-16993061890316661822698933932290685487928201847501247764943123093486146246053881784422495483295808763018159909117097465109184972481955760726881653793965156065564258910163382266215803402178446001464345566055415139139315957399436034085469065719996821945991812874240 -16993061890316661822698933932290685487928201847501247764943123093486146246053881784422495483295808763018159909117097465109184972481955760726881653793965156065564258910163382266215803402178446001464345566055415139139315957399436034085469065719996821945991812874240.0 -16993061890316661822698933932290685487928201847501247764943123093486146246053881784422495483295808763018159909117097465109184972481955760726881653793965156065564258910163382266215803402178446001464345566055415139139315957399436034085469065719996821945991812874240.00 -16993061890316661822698933932290685487928201847501247764943123093486146246053881784422495483295808763018159909117097465109184972481955760726881653793965156065564258910163382266215803402178446001464345566055415139139315957399436034085469065719996821945991812874240.000 -16993061890316661822698933932290685487928201847501247764943123093486146246053881784422495483295808763018159909117097465109184972481955760726881653793965156065564258910163382266215803402178446001464345566055415139139315957399436034085469065719996821945991812874240.000000 -2e+262 -1.7e+262 -1.699306189e+262 -1.6993061890316662e+262\n1799191905315513926092855488784396268983586140502106802356485767906725434150298852271829373716358939158243186783788142481871753483115181626722251089892239492041326689642400621841296147987004653568 1799191905315513926092855488784396268983586140502106802356485767906725434150298852271829373716358939158243186783788142481871753483115181626722251089892239492041326689642400621841296147987004653568.0 1799191905315513926092855488784396268983586140502106802356485767906725434150298852271829373716358939158243186783788142481871753483115181626722251089892239492041326689642400621841296147987004653568.00 1799191905315513926092855488784396268983586140502106802356485767906725434150298852271829373716358939158243186783788142481871753483115181626722251089892239492041326689642400621841296147987004653568.000 1799191905315513926092855488784396268983586140502106802356485767906725434150298852271829373716358939158243186783788142481871753483115181626722251089892239492041326689642400621841296147987004653568.000000 2e+195 1.8e+195 1.799191905e+195 1.7991919053155139e+195\n0 0.0 0.00 0.000 0.000000 2e-245 2.11e-245 2.105647572e-245 2.1056475721500131e-245\n1857816 1857816.5 1857816.49 1857816.488 1857816.487592 2e+06 1.86e+06 1857816.488 1857816.4875920722\n257389072337114546764588339528282749626404090344063938648892639439442306775755785021625077726479497162588576568448731545166534110976879519549895829766124641825466231485340442932010093687341562169263157362236617392128 257389072337114546764588339528282749626404090344063938648892639439442306775755785021625077726479497162588576568448731545166534110976879519549895829766124641825466231485340442932010093687341562169263157362236617392128.0 257389072337114546764588339528282749626404090344063938648892639439442306775755785021625077726479497162588576568448731545166534110976879519549895829766124641825466231485340442932010093687341562169263157362236617392128.00 257389072337114546764588339528282749626404090344063938648892639439442306775755785021625077726479497162588576568448731545166534110976879519549895829766124641825466231485340442932010093687341562169263157362236617392128.000 257389072337114546764588339528282749626404090344063938648892639439442306775755785021625077726479497162588576568448731545166534110976879519549895829766124641825466231485340442932010093687341562169263157362236617392128.000000 3e+215 2.57e+215 2.573890723e+215 2.5738907233711455e+215\n58850098167418466047356571184254955477709143225795819241288567340066642333073593915939010179212244781983525237933658536195879738824573583360 58850098167418466047356571184254955477709143225795819241288567340066642333073593915939010179212244781983525237933658536195879738824573583360.0 58850098167418466047356571184254955477709143225795819241288567340066642333073593915939010179212244781983525237933658536195879738824573583360.00 58850098167418466047356571184254955477709143225795819241288567340066642333073593915939010179212244781983525237933658536195879738824573583360.000 58850098167418466047356571184254955477709143225795819241288567340066642333073593915939010179212244781983525237933658536195879738824573583360.000000 6e+139 5.89e+139 5.885009817e+139 5.8850098167418466e+139\n0 0.0 0.00 0.000 0.000000 2e-249 2.06e-249 2.055114667e-249 2.055114666540428e-249\n-246506438027030878060162306238608680120180024647130285982271784987274050833905806122311847910572032 -246506438027030878060162306238608680120180024647130285982271784987274050833905806122311847910572032.0 -246506438027030878060162306238608680120180024647130285982271784987274050833905806122311847910572032.00 -246506438027030878060162306238608680120180024647130285982271784987274050833905806122311847910572032.000 -246506438027030878060162306238608680120180024647130285982271784987274050833905806122311847910572032.000000 -2e+98 -2.47e+98 -2.46506438e+98 -2.4650643802703088e+98\n28480983274 28480983273.7 28480983273.66 28480983273.664 28480983273.663998 3e+10 2.85e+10 2.848098327e+10 28480983273.663998\n831961471 831961470.6 831961470.64 831961470.636 831961470.635641 8e+08 8.32e+08 831961470.6 831961470.63564122\n177116 177116.3 177116.34 177116.340 177116.339892 2e+05 1.77e+05 177116.3399 177116.33989191786\n-20255557009343276586145558677742754113896629919058470585381547118524628992 -20255557009343276586145558677742754113896629919058470585381547118524628992.0 -20255557009343276586145558677742754113896629919058470585381547118524628992.00 -20255557009343276586145558677742754113896629919058470585381547118524628992.000 -20255557009343276586145558677742754113896629919058470585381547118524628992.000000 -2e+73 -2.03e+73 -2.025555701e+73 -2.0255557009343277e+73\n0 0.0 0.00 0.000 0.000010 1e-05 1.05e-05 1.045224711e-05 1.0452247106370279e-05\n0 0.0 0.00 0.000 0.000000 1e-300 9.58e-301 9.579123849e-301 9.579123849138929e-301\n0 0.0 0.00 0.000 0.000000 2e-07 1.61e-07 1.610451679e-07 1.6104516791512957e-07\n47870444373 47870444372.8 47870444372.84 47870444372.839 47870444372.839272 5e+10 4.79e+10 4.787044437e+10 47870444372.839272\n",
		},
		{
			Name: "slice-swap", // Swap moves two elements into each other's places: numbers, owned strings, a slice of a slice lent mut, slices of slices
			Src: `package main
import "fmt"
func tail(xs mut []string) { xs.Swap(0, 1) }
func main() {
	xs := []int{1, 2, 3, 4}
	xs.Swap(0, 3)
	xs.Swap(1, 1)
	fmt.Println(xs)
	ss := []string{"a".Clone(), "b".Clone(), "c".Clone()}
	ss.Swap(0, 2)
	tail(mut ss[1:3])
	fmt.Println(ss)
	twice := [][]string{[]string{"x".Clone()}, []string{}}
	twice.Swap(0, 1)
	fmt.Println(twice)
}
`,
			Out: "[4 2 3 1]\n[c a b]\n[[] [x]]\n",
		},
		{
			Name: "shadowed-locals", // a local that reuses a name already declared in its function, or the name of a top-level declaration, gets a C name of its own: x := x in a block, a loop variable shadowed in its body, a struct's name, a capture, a spawn, a generic instance
			Src: `package main
import "fmt"
struct item { n int }
var count = 10
func mk(n int) item { return item{n: n} }
func pick<T: Clone>(x T) T {
	{
		x := x.Clone()
		return x
	}
}
func next(n int) int? {
	if n < 3 { return Some(n + 1) }
	return None
}
func main() {
	count := count + 1
	fmt.Println(count)
	item := mk(2)
	got := mk(4)
	fmt.Println(item.n, got.n)
	seen := []int{}
	for i := 0; i < 3; i++ {
		i := i * 10
		seen.Push(i)
	}
	fmt.Println(seen)
	s := "outer".Clone()
	{
		s := s + "!"
		fmt.Println(s)
	}
	fmt.Println(s)
	steps := []int{}
	for cur := next(0); cur != None; cur = next(cur.Expect("Some")) {
		cur := cur.Expect("Some")
		steps.Push(cur)
	}
	fmt.Println(steps)
	n := 1
	f := func() int {
		n := n + 1
		return n
	}
	fmt.Println(f(), n)
	done := make(chan int)
	d := done.Clone()
	v := 5
	{
		v := v * 2
		spawn func() { d <- v }()
	}
	fmt.Println(<-done, v)
	fmt.Println(pick("p".Clone()), pick(3))
	o := Some("in".Clone())
	match o {
		Some(o) => fmt.Println(o),
		None => fmt.Println("none"),
	}
	match next(0) {
		Some(u) => fmt.Println("unused", 1),
		None => fmt.Println("none"),
	}
	match next(1) {
		Some(u) => fmt.Println("unused", 2),
		None => fmt.Println("none"),
	}
}
`,
			Out: "11\n2 4\n[0 10 20]\nouter!\nouter\n[1 2 3]\n2 1\nSome(10) 5\np 3\nin\nunused 1\nunused 2\n",
		},
		{
			Name: "toplevel-names", // a parameter or local named as a top-level declaration of the program gets a C name of its own, in a function, a closure, a function value's adapter and a spawn; a top-level variable keeps its name
			Src: `package main
import "fmt"
struct item { n int }
func mk(n int) item { return item{n: n} }
func f(item int) int { x := mk(item); return x.n }
func g(mk int) int { return mk + 1 }
func main() {
	h := f
	fmt.Println(f(3), g(4), h(5))
	k := func(item int) int { y := mk(item); return y.n }
	fmt.Println(k(6)); later(); done := make(chan int); d := done.Clone(); item := 7; spawn func() { d <- item }(); fmt.Println(<-done)
}
var count = 10
func bump(count int) int { return count + 1 }
func useGlobal() int { count := count + 5; return count }
func later() { fmt.Println(bump(count), useGlobal(), count) }
`,
			Out: "3 5 5\n6\n11 15 10\nSome(7)\n",
		},
		{
			Name: "temporary-parts", // a field or element of a value built in the statement is a place inside the temporary that holds it: read, lent, matched and changed there, and dropped once with the whole
			Src: `package main
import "fmt"
struct Pt { x int; y int }
struct Row { name string; tags []string; at Pt; note string? }
struct Doc { row Row; id int }
func row(n string) Row { return Row{name: n.Clone(), tags: []string{"t1".Clone(), "t2".Clone()}, at: Pt{x: 1, y: 2}, note: Some("n".Clone())} }
func doc() Doc { return Doc{row: row("d"), id: 7} }
func boxed() *Row { return &row("b") }
func names() []string { return []string{"a".Clone(), "bb".Clone()} }
func count(xs ref []string) int { return len(xs) }
func add(xs mut []string) { xs.Push("t3".Clone()) }
func main() {
	fmt.Println(row("r").name, len(row("r").tags), row("r").at, row("r").at.y)
	fmt.Println(doc().row.name, doc().row.tags[1], len(doc().row.tags[0]), doc().id)
	fmt.Println(names()[1], len(names()[0]), count(ref row("r").tags))
	add(mut row("r").tags)
	row("r").tags.Push("t4".Clone())
	match row("m").name {
		"m" => fmt.Println("matched"),
		_ => fmt.Println("missed"),
	}
	match ref row("m").note {
		Some(n) => fmt.Println("note", n),
		None => fmt.Println("none"),
	}
	if n := ref doc().row.note { fmt.Println("if", n) }
	for _, t := range ref row("r").tags { fmt.Println("tag", t) }
	fmt.Println((*boxed()).name, len((*boxed()).tags), *boxed())
	fmt.Println(row("r").name == "r", row("r").name.Len(), row("r").name + "!", row("r").note.IsSome())
	c := doc().row.name.Clone()
	fmt.Println(c)
}
`,
			Out: "r 2 {1 2} 2\nd t2 2 7\nbb 1 2\nmatched\nnote n\nif n\ntag t1\ntag t2\nb 2 {b [t1 t2] {1 2} Some(\"n\")}\ntrue 1 r! true\nd\n",
		},
		{
			Name: "mut-temporaries", // a value built here and lent mut is held once and dropped once, whole or a field of it
			Src: `package main
import "fmt"
struct S { xs []string; n int }
func mk() []string { return []string{"a".Clone()} }
func mks() S { return S{xs: []string{"a".Clone()}, n: 1} }
func grow(xs mut []string) int { xs.Push("b".Clone()); return len(xs) }
func bump(s mut S) int { s.xs.Push("c".Clone()); s.n += 1; return s.n + len(s.xs) }
func main() {
	fmt.Println(grow(mut []string{"x".Clone()}), grow(mut mk()), bump(mut mks()), bump(mut S{xs: mk(), n: 5}))
	grow(mut mk())
	bump(mut mks())
	fmt.Println(grow(mut mks().xs))
}
`,
			Out: "2 2 4 8\n2\n",
		},
		{
			Name: "loop-owned-step", // a loop whose variable owns what it holds, with a step that needs statements: the condition's temporaries are made and dropped on every pass, and the step's label is written only when a continue jumps to it
			Src: `package main
import "fmt"
func next(n int) string? {
	if n < 4 { return Some(fmt.Sprint("s", n)) }
	return None
}
func main() {
	i := 0
	for v := next(i); v != None; v = next(i) {
		i++
		fmt.Println(v)
	}
	i = 0
	for v := next(i); v != None; v = next(i) {
		i++
		if i == 2 { continue }
		fmt.Println("kept", v)
	}
	for j := 0; j < 5 && next(j) != Some("s2".Clone()); j++ { fmt.Println("j", j) }
}
`,
			Out: "Some(\"s0\")\nSome(\"s1\")\nSome(\"s2\")\nSome(\"s3\")\nkept Some(\"s0\")\nkept Some(\"s2\")\nkept Some(\"s3\")\nj 0\nj 1\n",
		},
	}

	for _, misuse := range []struct{ name, body, err string }{
		{"close-twice", "ch := make(chan int); close(ch); close(ch)", "panic: close of a closed channel"},
		{"send-closed", "ch := make(chan int, 1); close(ch); ch <- 1", "panic: send on a closed channel"},
		{"close-while-sending", "ch := make(chan int); spawn closer(ch.Clone()); ch <- 1", "panic: channel closed while a send waited on it"},
		{"task-panics", "ch := make(chan int); spawn boom(); <-ch", "panic: in a task"},
		{"deadlock-receive", "ch := make(chan int); fmt.Println(\"waiting\"); <-ch", "fatal error: all tasks are asleep - deadlock!"},
		{"deadlock-waitgroup", "wg := sync.NewWaitGroup(); wg.Add(1); wg.Wait()", "fatal error: all tasks are asleep - deadlock!"},
		{"deadlock-tasks", "a := make(chan int); b := make(chan int); b2 := b.Clone(); spawn func() { <-a; b2 <- 1 }(); <-b", "fatal error: all tasks are asleep - deadlock!"},
		{"swap-out-of-range", "xs := []int{1}; xs.Swap(0, 1)", "panic: index out of range [1] with length 1"},
	} {
		programs = append(programs, Program{
			Name: misuse.name, Src: tasks + "func main() { " + misuse.body + " }\n",
			AnyOut: true, Stderr: misuse.err, Code: 2,
		})
	}

	return programs
}

// manyTasks parks a hundred thousand tasks on one channel and then
// releases them: a task must cost a reserved stack and the pages it
// touched, not a thread, for them to fit. A stack is two mappings, the
// stack and its guard page, and Linux counts them against
// vm.max_map_count, 65530 by default, so a system that allows fewer
// runs as many as it allows.
func manyTasks(short bool) Program {
	n := 100000
	if short {
		n = 10000
	}

	if data, err := os.ReadFile("/proc/sys/vm/max_map_count"); err == nil {
		if limit, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && limit < 2*n+1000 {
			n = (limit - 1000) / 2
		}
	}

	count := strconv.Itoa(n)
	return Program{
		Name: "many-tasks",
		Src: "package main\nimport \"fmt\"\n" +
			"func wait(gate chan (), done chan ()) { <-gate; done <- () }\n" +
			"func main() {\n\tgate := make(chan ())\n\tdone := make(chan (), " + count + ")\n" +
			"\tfor i := 0; i < " + count + "; i++ { spawn wait(gate.Clone(), done.Clone()) }\n\tclose(gate)\n" +
			"\tfor i := 0; i < " + count + "; i++ { <-done }\n\tfmt.Println(\"released\")\n}\n",
		Out: "released\n", ReleaseOnly: true,
	}
}

// A Refusal is a program a backend must refuse, with text its error
// names the construct by.
type Refusal struct {
	Src, Want string
}

// Refusals are the constructs no backend lowers yet. Each program is
// the body after its package clause.
var Refusals = []Refusal{
	{"trait S { func (s ref Self) A() int }\nstruct X impl S { n int }\nfunc (x ref X) A() int { return x.n }\nfunc main() { var s S = X{n: 1}; print(s) }", "printing values of type S"},
	{"struct P { f func() }\nfunc main() { print(P{f: func() {}}) }", "printing values of type func()"},
	{"struct W<T> impl Error { t T }\nfunc main() { print(W{t: func() {}}) }", "printing values of type func()"}, // a derived message on an instance over a function: the declaration passed the checker
	{"const n = len(\"abc\")\nfunc main() { print(n) }", "a top-level const whose value is not a literal"},
	{"import \"os\"\nfunc main() { quit := os.Exit; quit(0) }", "a function the compiler provides, os.Exit, as a value"},
	{"import \"expect\"\nfunc main() { ok := expect.True; ok(true) }", "expect.True as a value: it is called, so that a failure names its line"},
	{"import \"testing\"\nfunc main() { log := testing.Log; log(\"x\") }", "testing.Log as a value"},
}
