// Copyright 2026 The Teak Programming Language. All rights reserved.
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

// A Program is a Teak program and what running it must give: its
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
		return "exit code " + strconv.Itoa(code) + ", want " + strconv.Itoa(p.Code)
	case !p.AnyOut && out != p.Out:
		return "output " + strconv.Quote(out) + ", want " + strconv.Quote(p.Out)
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
			Name: "repeat-negative", // Go's message, before anything is built
			Src: `package main
import ("fmt"; "strings")
func main() {
	n := -1
	fmt.Println(strings.Repeat("ab", n))
}
`,
			Stderr: "panic: strings: negative Repeat count", Code: 2,
		},
		{
			Name: "repeat-overflow", // a result longer than an int can count
			Src: `package main
import ("fmt"; "strings")
func main() {
	n := 4611686018427387904
	fmt.Println(len(strings.Repeat("ab", n)))
}
`,
			Stderr: "panic: strings: Repeat output length overflow", Code: 2,
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
			Name: "search-long-needle", // a needle that almost matches everywhere: comparing it at every offset takes a minute
			Src: `package main
import ("fmt"; "strings")
func main() {
	hay := strings.Repeat("a", 20000000)
	needle := strings.Repeat("a", 50000) + "b"
	fmt.Println(hay.Contains(needle), (hay + "b").Contains(needle), hay.SplitOnce(needle).IsNone())
}
`,
			Out: "false true true\n", ReleaseOnly: true,
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
}
`,
			Out: "11\n2 4\n[0 10 20]\nouter!\nouter\n[1 2 3]\n2 1\nSome(10) 5\np 3\nin\n",
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
		{"empty-waitgroup", "var wg sync.WaitGroup; wg.Add(1)", "panic: sync: use of an empty WaitGroup"},
		{"negative-waitgroup", "wg := sync.NewWaitGroup(); wg.Done()", "panic: sync: negative WaitGroup count"},
		{"empty-mutex", "var m sync.Mutex<int>; g := m.Lock(); *g.Get() += 1", "panic: sync: Lock on an empty Mutex"},
		{"empty-once", "var o sync.Once; o.Do(func() {})", "panic: sync: Do on an empty Once"},
		{"deadlock-receive", "ch := make(chan int); fmt.Println(\"waiting\"); <-ch", "fatal error: all tasks are asleep - deadlock!"},
		{"deadlock-waitgroup", "wg := sync.NewWaitGroup(); wg.Add(1); wg.Wait()", "fatal error: all tasks are asleep - deadlock!"},
		{"deadlock-tasks", "a := make(chan int); b := make(chan int); b2 := b.Clone(); spawn func() { <-a; b2 <- 1 }(); <-b", "fatal error: all tasks are asleep - deadlock!"},
		{"empty-rc", "var r rc.Rc<int>; fmt.Println(r.Get())", "panic: rc: Get on an empty handle"},
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
	{"const n = len(\"abc\")\nfunc main() { print(n) }", "a top-level var or const whose value is not a literal"},
	{"var n = len(\"abc\")\nfunc main() { print(n) }", "a top-level var or const whose value is not a literal"},
	{"import \"os\"\nfunc main() { quit := os.Exit; quit(0) }", "a function the compiler provides, os.Exit, as a value"},
}
