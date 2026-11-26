// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/platform"
	snelloruntime "github.com/snello-lang/snello/runtime"
)

// debuggerTimeout bounds a debugger's batch session, which a debugger
// asking for a permission it was not given would otherwise hold open.
// windowsProgram is the debuggee's binary on Windows and
// windowsModule the name cdb gives its module, which a breakpoint on a
// function names; cdbScriptLoaded is what cdb prints once the script
// is in, where it has the provider of scripts, which the SDK's cdb for
// the processor noProviderArch ships without; cdbLocals and
// cdbParameters are the commands that show a frame's locals and its
// parameters through the visualizers, and cdbTaskList shows what
// !tasks read, each unwind and what it printed, for a reader of a
// failure.
const (
	debuggerTimeout = 2 * time.Minute
	windowsProgram  = "prog.exe"
	windowsModule   = "prog!"
	cdbScriptLoaded = "JavaScript script successfully loaded"
	noProviderArch  = "arm64"
	cdbLocals       = "dx -r1 @$curframe.LocalVariables"
	cdbTaskList     = "dx -r4 Debugger.State.Scripts.snello.Contents.taskList()"
	cdbParameters   = "dx -r1 @$curframe.Parameters"
)

// scriptError is how cdb shows an error a script's function raised: the
// error, then the function and the place in the script.
var scriptError = regexp.MustCompile(`\[at [^\]]* \(line \d+ col \d+\)\]`)

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
	bin, found := debuggerPath(debugger)
	if !found {
		where := " is not on the path"
		if debugger == cdb {
			where += " or in Windows' SDK"
		}

		t.Skip(debugger + where)
	}

	t.Setenv(c.CacheEnv, t.TempDir())
	support, done, err := debugSupport()
	if err != nil {
		t.Fatal(err)
	}

	defer done()
	type variant struct{ backend, cc string }
	variants := []variant{{"llvm", ""}, {"c", ""}}
	if goruntime.GOOS != darwin && goruntime.GOOS != windows && c.IsGCC("gcc") { // a gcc on Windows builds for MinGW alone, which the build refuses
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
			if code := build(src, b.backend, platform.Host(), true, ""); code != 0 {
				t.Fatalf("snello build -debug exited %d", code)
			}

			name := "sum" // as the program names it, through the LLVM backend
			if b.backend == "c" {
				name = "sn__sum" // by its symbol, the one name C gives a function
			}

			out := runDebugger(t, debugger, bin, support, b.backend)
			if debugger == cdb {
				checkCDB(t, out)
				if out := runToFunction(t, debugger, bin, name); !strings.Contains(out, "n = 0n7") { // in base ten, which cdb marks
					t.Errorf("a breakpoint on %s does not stop there with its parameter in its slot:\n%s", name, out)
				}

				return
			}

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

			if out := runToFunction(t, debugger, bin, name); !strings.Contains(out, name+"(n=7)") && !strings.Contains(out, name+" (n=7)") {
				t.Errorf("a breakpoint on %s does not stop there with its parameter in its slot:\n%s", name, out)
			}
		})
	}
}

// runToFunction runs the debugger, bin, over the program built in the
// current directory to a breakpoint on the function name, and returns
// what it printed of the stop.
func runToFunction(t *testing.T, debugger, bin, name string) string {
	t.Helper()
	args := []string{"--batch", "-o", "breakpoint set -n " + name, "-o", "run", "-o", "kill", "./prog"}
	switch debugger {
	case gdb:
		args = []string{"-batch", "-nx", "-ex", "set disable-randomization off", "-ex", "break " + name, "-ex", "run", "-ex", "kill", "--args", "./prog"}
	case cdb:
		args = []string{cdbCommand, strings.Join([]string{"n 10", "l+t", "bp " + windowsModule + name, "g", "p", "dv", "q"}, cdbSeparator), windowsProgram} // a step to the function's first line, past the prologue a breakpoint on cdb stops before, as lldb's and gdb's stop after it
	}

	ctx, cancel := context.WithTimeout(context.Background(), debuggerTimeout)
	defer cancel()
	out, _ := exec.CommandContext(ctx, bin, args...).CombinedOutput() // kill ends the session with a status of its own
	if ctx.Err() != nil {
		t.Fatalf("%s did not finish within %v:\n%s", debugger, debuggerTimeout, out)
	}

	return string(out)
}

// runDebugger runs the debugger, bin, over the program built in the
// current directory, in batch mode, through the stops TestDebugger
// names, each a breakpoint that stops once, and returns what it
// printed.
func runDebugger(t *testing.T, debugger, bin, support, backend string) string {
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
	switch debugger {
	case cdb:
		commands := []string{"l+t", "n 10"}
		for _, stop := range stops {
			commands = append(commands, "bp /1 `"+stop+"`")
		}

		commands = append(commands, "g", cdbLocals, "g", "dx j", "g", cdbLocals, cdbParameters, "p", "k", ".f+", cdbLocals, "!tasks", cdbTaskList,
			"g", cdbParameters, "g", "!tasks", "q")
		args = append(cdbArgs(support, commands...), windowsProgram)
	case lldb:
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
	default:
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
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s on the %s build did not finish within %v; it may be waiting for a permission to debug:\n%s", debugger, backend, debuggerTimeout, out)
	}

	if err != nil {
		t.Logf("%s exited with %v", debugger, err) // kill ends the session with a status of its own
	}

	return string(out)
}

// checkCDB holds a cdb session over the debuggee's stops to what
// TestDebugger holds lldb's and gdb's to, as cdb prints it: the
// variables of each stop, the values shown by the visualizers the PDB
// carries, the tasks' states listed by the script, and no error of the
// script's. The whole session is logged, for a reader of a failure.
func checkCDB(t *testing.T, out string) {
	t.Helper()
	t.Logf("the cdb session:\n%s", out)
	for _, v := range [][2]string{
		{"n", "7"}, {"total", "0"}, {"i", "0"}, // a parameter and the locals of the stop in sum
		{"j", "0"}, // the second loop's variable, under its own name
		{"w", "2"}, // a function of the program's own package
	} {
		if !regexp.MustCompile(`(?m)^\s*` + v[0] + `\s+: ` + v[1] + `\b`).MatchString(out) { // dx pads a name to a column
			t.Errorf("the session does not show %s as %s", v[0], v[1])
		}
	}

	for _, w := range []string{`"snello"`, "Some(5)", "Rect(2, 3)", "Ok(4)", "len=3"} { // the caller's values as the program writes them
		if !strings.Contains(out, w) {
			t.Errorf("the session does not show %q", w)
		}
	}

	if scriptError.MatchString(out) {
		t.Errorf("the script failed")
	}

	if !strings.Contains(out, cdbScriptLoaded) {
		if goruntime.GOARCH == noProviderArch {
			t.Log("this cdb has no provider of scripts: the tasks are not checked")
			return
		}

		t.Error("the script did not load")
	}

	// The task waiting on the channel, and main while the task stops,
	// are listed by their state alone: their frames, which the script
	// has the debugger unwind from the saved context, are not shown yet,
	// the debugger's k raising an exception on it, which the session's
	// dump of taskList shows for whoever takes that up.
	if !strings.Contains(out, "parked") {
		t.Errorf("the session does not show a parked task")
	}
}

// TestDebuggerCommand holds each debugger's arguments to the script
// loaded, the standard library's sources found, and the program with
// its arguments last: for cdb, the extension loaded by its path, and
// the support's directory first among the places sources are looked
// for, under which the library's are found by their path's end, with
// the user's own places after it.
func TestDebuggerCommand(t *testing.T) {
	support := filepath.Join(string(filepath.Separator), "support")
	t.Setenv(sourcePathVar, "own")
	script, quotedSources := filepath.Join(support, scriptName), strconv.Quote(filepath.Join(support, stdDir))
	for name, want := range map[string]string{lldb: strconv.Quote(script), gdb: script, cdb: cdbScriptLoad + cdbQuote + filepath.Join(support, jsName) + cdbQuote} {
		args := debuggerCommand(name, support, "./prog", []string{"-v", "x"})
		joined := strings.Join(args, " ")
		sources := strings.Contains(joined, strings.TrimSuffix(lower.StdSources, stdPathEnd)+argSeparator+quotedSources)
		if name == cdb {
			sources = slices.Equal(args[:2], []string{cdbSourcePath, support + cdbPathSeparator + "own"})
		}

		if !strings.Contains(joined, want) {
			t.Errorf("%s %v does not load the script", name, args)
		}

		if !sources {
			t.Errorf("%s %v does not map the standard library's sources", name, args)
		}

		if !slices.Equal(args[len(args)-3:], []string{"./prog", "-v", "x"}) {
			t.Errorf("%s %v does not end with the program and its arguments", name, args)
		}
	}
}

// TestTableFields holds both scripts to the runtime's table: each reads
// sn_debugger_table's numbers by their order, so each lists the fields
// internal.h declares, in its order.
func TestTableFields(t *testing.T) {
	header := string(snelloruntime.Files()["internal.h"])
	end := strings.Index(header, "} sn_debugger_table;")
	start := strings.LastIndex(header[:max(end, 0)], "typedef struct {")
	if start < 0 || end < 0 {
		t.Fatal("internal.h declares no sn_debugger_table")
	}

	var want []string
	for _, line := range strings.Split(header[start:end], "\n")[1:] { // the scripts read the head, then 32-bit numbers
		line = strings.TrimSpace(line)
		fields, ok := strings.CutPrefix(line, "int32_t ")
		switch {
		case line == "" || line == "const void *tasks;":
			continue
		case !ok:
			t.Fatalf("sn_debugger_table has a field the scripts cannot read: %s", line)
		}

		for _, f := range strings.Split(strings.TrimSuffix(fields, ";"), ",") {
			want = append(want, strings.TrimSpace(f))
		}
	}

	quoted := regexp.MustCompile(`"([a-z_]+)"`)
	for name, script := range map[string][]byte{scriptName: debuggerScript, jsName: windowsScript} {
		text := string(script)
		at := strings.Index(text, "TABLE_FIELDS = ")
		if at < 0 {
			t.Errorf("%s lists no TABLE_FIELDS", name)
			continue
		}

		list := text[at:]
		last := strings.IndexAny(list, ")]")
		if last < 0 {
			t.Errorf("%s's TABLE_FIELDS does not end", name)
			continue
		}

		list = list[:last]
		var got []string
		for _, m := range quoted.FindAllStringSubmatch(list, -1) {
			got = append(got, m[1])
		}

		if !slices.Equal(got, want) {
			t.Errorf("%s reads the table as %v, internal.h declares %v", name, got, want)
		}
	}
}

// TestPlatformDebugger names the debugger of each system the compiler
// runs on.
func TestPlatformDebugger(t *testing.T) {
	want := map[string]string{darwin: lldb, windows: cdb}[goruntime.GOOS]
	if want == "" {
		want = gdb
	}

	if got := platformDebugger(); got != want {
		t.Errorf("platformDebugger() = %s, want %s", got, want)
	}
}

// TestDebugSupport holds the support directory to the script and the
// standard library's sources: in the cache written once, a second call
// finding the first's with no directory being made left beside it; with
// the cache off written for one session into a directory of its own,
// which the session's end removes.
func TestDebugSupport(t *testing.T) {
	t.Setenv(c.CacheEnv, t.TempDir())
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

// TestDebuggerSetup prints, for lldb and for gdb, the commands an
// editor's debugger runs first: the script loaded, which is written
// where they say, and the standard library's sources mapped; another
// debugger's name is refused, and so is debug with neither a program
// nor -setup.
func TestDebuggerSetup(t *testing.T) {
	t.Setenv(c.CacheEnv, t.TempDir())
	for name, load := range map[string]string{lldb: lldbImport, gdb: gdbSource} {
		var out, errs strings.Builder
		if code := run([]string{debugCommand, "-setup", name}, &out, &errs); code != exitSuccess {
			t.Fatalf("%s: exited %d and said %q", name, code, errs.String())
		}

		lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], load) || !strings.Contains(lines[1], strings.TrimSuffix(lower.StdSources, stdPathEnd)) {
			t.Errorf("%s: %q", name, lines)
			continue
		}

		script, err := strconv.Unquote(strings.TrimPrefix(lines[0], load))
		if err != nil {
			script = strings.TrimPrefix(lines[0], load) // gdb's, unquoted
		}

		if _, err := os.Stat(script); err != nil {
			t.Errorf("%s: the script is not where the command says: %v", name, err)
		}
	}

	for _, args := range [][]string{{debugCommand, "-setup", cdb}, {debugCommand}} {
		var errs strings.Builder
		if code := run(args, io.Discard, &errs); code != exitUsage || errs.Len() == 0 {
			t.Errorf("%v exited %d and said %q", args, code, errs.String())
		}
	}
}
