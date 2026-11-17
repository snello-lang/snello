// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/lower"
)

// debuggerTimeout bounds a debugger's batch session, which a debugger
// asking for a permission it was not given would otherwise hold open.
const debuggerTimeout = 2 * time.Minute

// debuggee is the program the debugger tests stop in, in a module with
// a package of its own: a function with a loop, a closure that
// captures, two loops with a variable of one name, the program's values
// of every kind the formatters show, options of a pointer among them, a
// function of the program's own package, and a task that waits on a
// channel and then, past its receive, stops while main waits on its
// reply. Each place a test stops at is marked.
const debuggee = `package main

import ("fmt"; "geo"; "time")

struct Point {
    x int
    y int
}

struct Node {
    v int
}

enum Shape {
    Circle(int)
    Rect(int, int)
    Empty
}

func sum(n int) int {
    total := 0
    for i := 0; i < n; i++ {
        total += i // stop: sum
    }

    return total
}

func wait(ch chan int, back chan int) {
    v := <-ch
    time.Sleep(time.Millisecond * 50)
    fmt.Println("got", v) // stop: got
    back <- 1
}

func main() {
    p := Point{x: 3, y: 4}
    name := "snello"
    var o int? = Some(5)
    xs := []int{1, 2, 3}
    sh := Rect(2, 3)
    var r Result<int, string> = Ok(4)
    var none *Node? = None
    var some *Node? = Some(&Node{v: 1})
    first := xs.First()
    ys := []int{}
    empty := ys.First()
    shapes := []Shape{Rect(2, 3)}
    lent := shapes.First()
    ch := make(chan int)
    back := make(chan int)
    spawn wait(ch.Clone(), back.Clone())
    time.Sleep(time.Millisecond * 20)
    count := 0
    add := func(k int) {
        count += k // stop: closure
    }
    add(2)
    for j := 0; j < 1; j++ {
        fmt.Println("first", j)
    }
    for j := 0; j < 1; j++ {
        fmt.Println("second", j) // stop: second
    }
    s := sum(p.x + p.y)
    match none { Some(n) => fmt.Println(n.v), None => fmt.Println("none") }
    match some { Some(n) => fmt.Println(n.v), None => fmt.Println("none") }
    fmt.Println(name, s, o, xs, sh, r, count, geo.Area(2, 3), first.IsSome(), empty.IsNone(), lent.IsSome())
    ch <- 7
    fmt.Println(<-back)
}
`

// geo is the program's own package, which a debug build leaves
// unoptimized as it does the main package.
const geo = `package geo

func Area(w int, h int) int {
    a := w * h // stop: own
    return a
}
`

// stopAt is the line of src marked "stop: mark".
func stopAt(t *testing.T, src, mark string) int {
	t.Helper()
	for i, line := range strings.Split(src, "\n") {
		if strings.HasSuffix(line, "// stop: "+mark) {
			return i + 1
		}
	}

	t.Fatalf("no line marked %q", mark)
	return 0
}

// TestDebugger builds the debuggee as snello build -debug does, from two
// directories above its own, so that its file is named by a path, and
// with the build's directory removed, so that macOS reads the bundle,
// through both backends, and drives the platform's debugger over it in
// batch mode with Snello's script loaded: a breakpoint at a line of a
// closure, of the second of two loops, of a function, of the program's
// own package and of a task; the captured variable, the loop's variable
// under its name, the parameters and the locals printed; a step to the
// next line; the backtrace; the caller's values as the program writes
// them; the source shown at the stop; and the tasks listed twice, once
// with the waiting task's frame and once with main's, waiting on the
// task.
func TestDebugger(t *testing.T) {
	debugger := platformDebugger()
	if !onPath(debugger) {
		t.Skip(debugger + " is not on the path")
	}

	t.Setenv("SNELLO_CACHE", t.TempDir())
	support, done, err := debugSupport()
	if err != nil {
		t.Fatal(err)
	}

	defer done()
	type variant struct{ backend, cc string }
	variants := []variant{{"llvm", ""}, {"c", ""}}
	if goruntime.GOOS != darwin && c.IsGCC("gcc") {
		variants = append(variants, variant{"c", "gcc"}) // the compiler most Linux systems build C with
	}

	for _, b := range variants {
		t.Run(b.backend+b.cc, func(t *testing.T) {
			if b.cc != "" {
				t.Setenv("CC", b.cc)
			}

			dir := t.TempDir()
			src := filepath.Join("src", "prog") // the binary, prog, is written beside src
			for path, text := range map[string]string{filepath.Join(src, "main.snello"): debuggee, filepath.Join("geo", "geo.snello"): geo, "snello.mod": "// the debuggee's module\nsnello 0.1.0\n"} {
				if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(path)), 0o755); err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(filepath.Join(dir, path), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			t.Chdir(dir)
			if code := build(src, b.backend, true); code != 0 {
				t.Fatalf("snello build -debug exited %d", code)
			}

			out := runDebugger(t, debugger, support, b.backend)
			want := []string{
				"n = 7", "total = 0", "i = 0", // a parameter and the locals of the stop in sum
				"total += i", // the source, found by its path from the directory built in
				"main.snello:" + strconv.Itoa(stopAt(t, debuggee, "sum")-1), // the loop's line, which the step goes to
				"j = 0", // the second loop's variable, under its own name
				"main.snello:" + strconv.Itoa(stopAt(t, debuggee, "second")),
				`"snello"`, "Some(5)", "Rect(2, 3)", "Ok(4)", "len=3", // the caller's values as the program writes them
				"none = None", "some = Some(", // options of a pointer
				"first = Some(1)", "empty = None", "lent = Some(Rect(2, 3))", // options of a borrow, the address and null in the LLVM backend's build
				"parked", "wait at main.snello:", // the task waiting on the channel, at its line
				"w = 2", "geo.snello:" + strconv.Itoa(stopAt(t, geo, "own")), // a function of the program's own package
				"main at main.snello:", // main, waiting on the task's reply, listed while the task stops
			}
			if b.backend == "llvm" {
				want = append(want, "count = 0") // a captured variable, under its own name
			} else {
				want = append(want, "sn_main") // the caller in the backtrace, by its symbol, the one name C gives it
			}

			for _, w := range want {
				if !strings.Contains(out, w) {
					t.Errorf("the session does not show %q:\n%s", w, out)
				}
			}

			if strings.Contains(out, "Traceback") || strings.Contains(out, "Error:") {
				t.Errorf("the script failed:\n%s", out)
			}

			name := "sum" // as the program names it, through the LLVM backend
			if b.backend == "c" {
				name = "sn__sum" // by its symbol, the one name C gives a function
			}

			if out := runToFunction(t, debugger, name); !strings.Contains(out, name+"(n=7)") && !strings.Contains(out, name+" (n=7)") {
				t.Errorf("a breakpoint on %s does not stop there with its parameter in its slot:\n%s", name, out)
			}
		})
	}
}

// runToFunction runs the debugger over the program built in the current
// directory to a breakpoint on the function name, and returns what it
// printed of the stop.
func runToFunction(t *testing.T, debugger, name string) string {
	t.Helper()
	args := []string{"--batch", "-o", "breakpoint set -n " + name, "-o", "run", "-o", "kill", "./prog"}
	if debugger == gdb {
		args = []string{"-batch", "-nx", "-ex", "set disable-randomization off", "-ex", "break " + name, "-ex", "run", "-ex", "kill", "--args", "./prog"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), debuggerTimeout)
	defer cancel()
	out, _ := exec.CommandContext(ctx, debugger, args...).CombinedOutput() // kill ends the session with a status of its own
	if ctx.Err() != nil {
		t.Fatalf("%s did not finish within %v:\n%s", debugger, debuggerTimeout, out)
	}

	return string(out)
}

// runDebugger runs the debugger over the program built in the current
// directory, in batch mode, through the stops TestDebugger names, each
// a breakpoint that stops once, and returns what it printed.
func runDebugger(t *testing.T, debugger, support, backend string) string {
	t.Helper()
	script := filepath.Join(support, scriptName)
	stops := []string{
		"main.snello:" + strconv.Itoa(stopAt(t, debuggee, "closure")),
		"main.snello:" + strconv.Itoa(stopAt(t, debuggee, "second")),
		"main.snello:" + strconv.Itoa(stopAt(t, debuggee, "sum")),
		"geo.snello:" + strconv.Itoa(stopAt(t, geo, "own")),
		"main.snello:" + strconv.Itoa(stopAt(t, debuggee, "got")),
	}

	var args []string
	if debugger == lldb {
		args = []string{"--batch", "-o", "command script import " + strconv.Quote(script)}
		for _, stop := range stops {
			file, line, _ := strings.Cut(stop, ":")
			args = append(args, "-o", "breakpoint set --one-shot true -f "+file+" -l "+line)
		}

		for _, command := range []string{"run", "frame variable", "continue", "frame variable j", "continue", "frame variable",
			"next", "bt", "up", "frame variable", "tasks", "continue", "frame variable", "continue", "tasks", "kill"} {
			args = append(args, "-o", command)
		}

		args = append(args, "./prog")
	} else {
		args = []string{"-batch", "-nx", "-ex", "set disable-randomization off", "-ex", "set pagination off", "-x", script}
		for _, stop := range stops {
			args = append(args, "-ex", "tbreak "+stop)
		}

		sum := stops[2][strings.Index(stops[2], ":")+1:]
		for _, command := range []string{"run", "info locals", "continue", "info locals", "continue", "info args", "info locals",
			"list " + sum + "," + sum, "next", "bt", "up", "info locals", "tasks", "continue", "info args", "continue", "tasks", "kill"} {
			args = append(args, "-ex", command)
		}

		args = append(args, "--args", "./prog")
	}

	ctx, cancel := context.WithTimeout(context.Background(), debuggerTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, debugger, args...).CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s on the %s build did not finish within %v; it may be waiting for a permission to debug:\n%s", debugger, backend, debuggerTimeout, out)
	}

	if err != nil {
		t.Logf("%s exited with %v", debugger, err) // kill ends the session with a status of its own
	}

	return string(out)
}

// TestDebuggerCommand holds the platform's debugger's arguments to the
// script loaded, the standard library's directory mapped, and the
// program with its arguments last.
func TestDebuggerCommand(t *testing.T) {
	name, args := debuggerCommand("/support", "./prog", []string{"-v", "x"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, filepath.Join("/support", scriptName)) {
		t.Errorf("%s %v does not load the script", name, args)
	}

	if !strings.Contains(joined, strings.TrimSuffix(lower.StdSources, "/")) || !strings.Contains(joined, filepath.Join("/support", stdDir)) {
		t.Errorf("%s %v does not map the standard library's sources", name, args)
	}

	if !slices.Equal(args[len(args)-3:], []string{"./prog", "-v", "x"}) {
		t.Errorf("%s %v does not end with the program and its arguments", name, args)
	}

	if want := platformDebugger(); name != want {
		t.Errorf("the debugger is %s, want %s", name, want)
	}
}

// TestDebugSupport holds the support directory to the script and the
// standard library's sources: in the cache written once, a second call
// finding the first's with no directory being made left beside it; with
// the cache off written for one session into a directory of its own,
// which the session's end removes.
func TestDebugSupport(t *testing.T) {
	t.Setenv("SNELLO_CACHE", t.TempDir())
	dir, done, err := debugSupport()
	if err != nil {
		t.Fatal(err)
	}

	done()
	if got, err := os.ReadFile(filepath.Join(dir, scriptName)); err != nil || string(got) != string(debuggerScript) {
		t.Errorf("the script: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, stdDir, "fmt", "fmt.snello")); err != nil {
		t.Errorf("the standard library's sources: %v", err)
	}

	again, _, err := debugSupport()
	if err != nil || again != dir {
		t.Errorf("a second call gave %q, %v; want %q", again, err, dir)
	}

	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil || len(entries) != 1 {
		t.Errorf("the support's directory holds %d entries, want 1: %v", len(entries), err)
	}

	t.Setenv("SNELLO_CACHE", "off")
	first, doneFirst, err := debugSupport()
	if err != nil {
		t.Fatal(err)
	}

	second, doneSecond, err := debugSupport()
	if err != nil {
		t.Fatal(err)
	}

	if first == second {
		t.Errorf("two sessions with the cache off share %q", first)
	}

	if _, err := os.Stat(filepath.Join(first, scriptName)); err != nil {
		t.Errorf("a session's script: %v", err)
	}

	doneFirst()
	doneSecond()
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Errorf("a session's support outlived it: %v", err)
	}
}
