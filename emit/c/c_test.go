// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/teak-lang/teak/parser"
	"github.com/teak-lang/teak/types"
)

func TestPrograms(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"hello", `package main
import "fmt"
func main() { fmt.Println("hello") }
`, "hello\n"},
		{"numbers", `package main
import "fmt"
func fib(n int) int { if n < 2 { return n }; return fib(n-1) + fib(n-2) }
func main() {
    sum := 0
    for i := 0; i < 10; i += 1 { sum += fib(i) }
    fmt.Println(fib(10), sum, 7 / 2, 7 % 2, -3, 2.5 * 2, 1.0 / 3.0)
    var big i64 = 1
    for i := 0; i < 40; i += 1 { big *= 2 }
    fmt.Println(big, u8(255), 'a', f64(3) / 2 == 1.5)
}
`, "55 88 3 1 -3 5 0.3333333333333333\n1099511627776 255 97 true\n"},
		{"strings", `package main
import "fmt"
func greet(name ref string) string { return "hello, " + name }
func main() {
    s := greet("teak")
    fmt.Println(s, len(s), s == "hello, teak", s < "zz", s[0], s[7])
    t := s
    t += "!"
    fmt.Println(t)
    fmt.Println(fmt.Sprintf("%d + %d = %d; %s %q %v", 1, 2, 3, "a", "b", true))
    fmt.Printf("%s-%d\n", "x", 42)
    fmt.Println(fmt.Sprint(1, 2, "a", 3.5, "b", "c"))
}
`, "hello, teak 11 true true 104 116\nhello, teak!\n1 + 2 = 3; a \"b\" true\nx-42\n1 2a3.5bc\n"},
		{"corners", `package main
import "fmt"
func unused() int { return 1 }
func main() {
    x := 1_000 + 0b101 + 0o17 + 0x1F
    var big i64 = 9223372036854775807
    big += 1
    fmt.Println("??= \"q\" \\ \u00e9", x, big, 1e3, 0.1 + 0.2)
}
`, "??= \"q\" \\ \u00e9 1051 -9223372036854775808 1000 0.30000000000000004\n"},
		{"control", `package main
import "fmt"
type Score int
func grade(s Score) string {
    if s >= 90 { return "A" } else if s >= 80 { return "B" }
    return "C"
}
func main() {
    for i := 0; i < 3; i += 1 {
        if i == 1 { continue }
        fmt.Println(i, grade(Score(70 + i*12)))
    }
    n := 0
    for n < 5 { n += 2 }
    for { n -= 1; if n == 0 { break } }
    x, y := 1, 2
    x, y = y, x
    fmt.Println(n, x, y, !(x > y) && y != 0)
}
`, "0 C\n2 A\n0 2 1 false\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildAndRun(t, tt.name, tt.src)
			if got != tt.want {
				t.Errorf("output:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

// TestExit checks that os.Exit ends the program with its code after
// flushing what was printed.
func TestExit(t *testing.T) {
	src := "package main\nimport (\"fmt\"; \"os\")\nfunc main() { fmt.Println(\"bye\"); os.Exit(3); fmt.Println(\"not reached\") }\n"
	out, code := run(t, "exit", src)
	if out != "bye\n" || code != 3 {
		t.Errorf("got output %q and exit code %d, want \"bye\\n\" and 3", out, code)
	}
}

// TestUnsupported checks that a construct the backend does not lower
// is an error that names it, never silent wrong code.
func TestUnsupported(t *testing.T) {
	src := "package main\nstruct P { x int }\nfunc main() { p := P{x: 1}; print(p.x) }\n"
	f, file, err := parser.ParseFile("t.teak", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	if _, err := types.Check(file, f, info); err != nil {
		t.Fatal(err)
	}

	_, err = Emit(file, f, info)
	if err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Errorf("got %v, want an unsupported-construct error", err)
	}
}

// buildAndRun emits src, compiles it with the system C compiler under
// the warnings the standards require, runs the binary, and returns its
// standard output.
func buildAndRun(t *testing.T, name, src string) string {
	t.Helper()
	out, code := run(t, name, src)
	if code != 0 {
		t.Fatalf("%s exited with %d\n%s", name, code, out)
	}

	return out
}

// run builds src and runs it, returning its standard output and exit code.
func run(t *testing.T, name, src string) (string, int) {
	t.Helper()
	f, file, err := parser.ParseFile(name+".teak", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	if _, err := types.Check(file, f, info); err != nil {
		t.Fatal(err)
	}

	csrc, err := Emit(file, f, info)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	bin, out, err := Build(dir, name, csrc)
	if err != nil {
		t.Fatalf("%v\n%s\n--- emitted C ---\n%s", err, out, csrc)
	}

	cmd := exec.Command(bin)
	cmd.Dir = dir
	stdout, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok {
		return string(stdout), exit.ExitCode()
	}

	if err != nil {
		t.Fatalf("running %s: %v", name, err)
	}

	return string(stdout), 0
}

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("cc"); err != nil {
		os.Stderr.WriteString("skipping emit/c tests: no C compiler on the PATH\n")
		os.Exit(0)
	}

	os.Exit(m.Run())
}
