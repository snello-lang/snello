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
