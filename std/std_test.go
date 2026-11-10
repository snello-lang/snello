// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package std_test

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/testrun"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/std"
)

// TestPackages runs the standard library's own tests, written in
// Snello in each package's _test.snello files, through the runner
// snello test uses, under the library's privileges, through both
// backends; every package with a test file must pass.
func TestPackages(t *testing.T) {
	var paths []string
	fs.WalkDir(std.Sources, ".", func(name string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(name, "_test.snello") {
			paths = append(paths, path.Dir(name))
		}

		return nil
	})

	sort.Strings(paths)
	if len(paths) == 0 {
		t.Fatal("no standard package has a test file")
	}

	for _, p := range paths {
		for _, backend := range []string{"llvm", "c"} {
			t.Run(p+"/"+backend, func(t *testing.T) {
				plan, err := testrun.PlanStd(p)
				if err != nil {
					t.Fatal(err)
				}

				bin, err := plan.Build(backend, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}

				var out bytes.Buffer
				cmd := exec.Command(bin, "-v")
				cmd.Dir = p // the package's directory, so that testdata/ is beside it, as snello test std/NAME runs it
				cmd.Stdout, cmd.Stderr = &out, &out
				if err := cmd.Run(); err != nil || !strings.Contains(out.String(), "\nok  \t"+p+"\t") {
					t.Errorf("%s: %v\n%s", p, err, out.String())
				}
			})
		}
	}
}

// answerer is a Snello program that reads a question a line, the
// letter of a function of the library and its input's bytes in
// hexadecimal, and answers each with one line: what the function
// returned, or why it returned an error. The fuzz tests below ask it
// what the library makes of an input and hold the answer to Go's own
// for the same input, so that the library is exercised as a program
// reaches it, through the compiler, with the runtime's checks compiled
// in.
const answerer = `package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
)

func nibble(c u8) u8 {
	if c <= '9' {
		return c - '0'
	}

	return c - 'a' + 10
}

func reason(e ref strconv.Error) string {
	if e.Kind() == strconv.Range {
		return "range"
	}

	return "syntax"
}

func main() {
	in := os.Stdin()
	sc := bufio.NewScanner(mut in)
	sc.Buffer(1 << 20)
	for sc.Scan() {
		line := sc.Bytes()
		raw := []byte{}
		for i := 1; i+1 < len(line); i += 2 {
			raw.Push(nibble(line[i])<<4 | nibble(line[i+1]))
		}

		s := string(raw)
		if line[0] == 'f' {
			wide := match strconv.ParseFloat(ref s) {
				Ok(v) => fmt.Sprint(v),
				Err(e) => reason(ref e),
			}
			narrow := match strconv.ParseFloat32(ref s) {
				Ok(v) => fmt.Sprint(v),
				Err(e) => reason(ref e),
			}
			fmt.Println(wide, narrow)
		} else if line[0] == 'i' {
			ten := match strconv.Atoi(ref s) {
				Ok(v) => fmt.Sprint(v),
				Err(e) => reason(ref e),
			}
			any := match strconv.ParseInt(ref s, 0) {
				Ok(v) => fmt.Sprint(v),
				Err(e) => reason(ref e),
			}
			fmt.Println(ten, any)
		} else {
			match strconv.Unquote(ref s) {
				Ok(text) => fmt.Printf("ok %x\n", text),
				Err(e) => fmt.Println(reason(ref e)),
			}
		}
	}
}
`

// The answerer, built and started once for the process and asked one
// question at a time.
var asked struct {
	sync.Mutex
	dir string
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

// ask gives the answerer one input of the function a letter names and
// returns its answer. The answerer dying is a failure of the input that
// killed it, and the next question starts another.
func ask(t *testing.T, letter byte, input string) string {
	t.Helper()
	asked.Lock()
	defer asked.Unlock()
	if asked.cmd == nil {
		if asked.dir == "" {
			dir, err := os.MkdirTemp("", "snello-answerer")
			if err != nil {
				t.Fatal(err)
			}

			asked.dir = dir
			buildAnswerer(t, dir)
		}

		cmd := exec.Command(filepath.Join(asked.dir, "answerer"))
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}

		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}

		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		asked.cmd, asked.in, asked.out = cmd, in, bufio.NewReader(out)
	}

	_, err := fmt.Fprintf(asked.in, "%c%x\n", letter, input)
	var answer string
	if err == nil {
		answer, err = asked.out.ReadString('\n')
	}

	if err != nil {
		asked.in.Close()
		state := asked.cmd.Wait()
		asked.cmd = nil
		t.Fatalf("the answerer stopped on %c of %q: %v, %v", letter, input, err, state)
	}

	return strings.TrimSuffix(answer, "\n")
}

// buildAnswerer compiles the answerer into dir as a debug build through
// the C backend.
func buildAnswerer(t *testing.T, dir string) {
	t.Helper()
	f, file, err := parser.ParseFile("answerer.snello", []byte(answerer))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	imp := importer.NewImporter(info)
	if _, err := (&types.Config{Importer: imp}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	pkgs := imp.Packages()
	libs := make([]lower.Library, len(pkgs))
	for i, p := range pkgs {
		libs[i] = lower.Library{Path: p.Path, File: p.File, Syntax: p.Syntax, CFiles: p.CFiles, Links: p.Links}
	}

	src, err := c.Emit(file, f, info, libs...)
	if err != nil {
		t.Fatal(err)
	}

	if _, out, err := c.Build(dir, "answerer", src, true, lower.Bindings(info, libs)...); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// reasonOf is the answerer's word for an error of Go's strconv.
func reasonOf(err error) string {
	if e, ok := err.(*strconv.NumError); ok && e.Err == strconv.ErrRange {
		return "range"
	}

	return "syntax"
}

// short keeps a fuzz input to what one line of the answerer's scanner
// holds twice over, in hexadecimal.
func short(s string) bool { return len(s) <= 1<<18 }

// FuzzParseFloat holds ParseFloat and ParseFloat32 to Go's strconv on
// any text: the same texts are numbers, and each reads as the same
// float, told by the shortest text that reads back as it.
func FuzzParseFloat(f *testing.F) {
	for _, s := range []string{
		"", "0", "-0", "+1", "1.5", "-2e3", "1e400", "1e-400", ".5", "5.", ".", "e1", "1e", "1e+", "1_000.5", "1__0", "_1", "1_", "0x1p-2", "0X1.8p1", "0x1", "0x_1p0", "0x1p",
		"inf", "+Infinity", "-INF", "infinit", "nan", "NaN", "+nan", "nan(1)", " 1", "1 ", "1.5x", "0b1", "1e1_0", "1e_10",
		"9007199254740993", "9007199254740993.00000000000000000001", "1.00000017881393432617187499", "16777217", "3.4028236e38", "1e39", "1e-46",
		"2.2250738585072011e-308", "2.2250738585072012e-308", "1.7976931348623158e308", "1.7976931348623159e308", "4.9e-324", "2.4703282292062327e-324", "2.4703282292062328e-324",
		"0.1000000000000000055511151231257827021181583404541015625", "179769313486231580793728971405303415079934132710037826936173778980444968292764750946649017977587207096330286416692887910946555547851940402630657488671505820681908902000708383676273854845817711531764475730270069855571366959622842914819860834936475292719074168444365510704342711559699508093042880177904174497791.9999999999999999999999999999999999999999999999999999999999999999999999",
		"0x1.fffffffffffff7fffp1023", "0x1.fffffffffffff8p1023", "0x0.0000000000001p-1022", "0x1p-1075", "0x1.00000000000008p0", "0x1.000001p0",
	} {
		f.Add(s)
	}

	f.Add("9007199254740993." + strings.Repeat("0", 830) + "1") // a tie, and a digit past the eight hundredth

	f.Fuzz(func(t *testing.T, s string) {
		if !short(s) {
			return
		}

		wide, narrow := "", ""
		if v, err := strconv.ParseFloat(s, 64); err != nil {
			wide = reasonOf(err)
		} else {
			wide = strconv.FormatFloat(v, 'g', -1, 64)
		}

		if v, err := strconv.ParseFloat(s, 32); err != nil {
			narrow = reasonOf(err)
		} else {
			narrow = strconv.FormatFloat(v, 'g', -1, 32)
		}

		if got, want := ask(t, 'f', s), wide+" "+narrow; got != want {
			t.Errorf("ParseFloat and ParseFloat32 of %q: got %s, want %s", s, got, want)
		}
	})
}

// wellFormed reports whether s is an integer in the syntax ParseInt
// reads with the base read from the text, whatever its size: Go's big
// integers read the same syntax and hold any.
func wellFormed(s string) bool {
	_, ok := new(big.Int).SetString(s, 0)
	return ok
}

// FuzzAtoi holds Atoi, and ParseInt with the base read from the text,
// to Go's strconv on any text, but for text that is no number and
// starts with digits past the largest integer: Go reports the range
// as soon as the digits pass it, and Snello the syntax, the range
// being a number's.
func FuzzAtoi(f *testing.F) {
	for _, s := range []string{
		"", "0", "-0", "+0", "42", "-7", "+", "-", "4x", " 1", "1 ", "1_0", "9223372036854775807", "9223372036854775808", "-9223372036854775808", "-9223372036854775809",
		"99999999999999999999999999", "9270000000000000000A", "99999999999999999999999999x", "0x1F", "0X_1f", "0x", "0b101", "0b2", "0o17", "0755", "08", "1_000", "1__0", "_1", "1_", "-0x8000000000000000", "0x8000000000000000", "0_7", "00", "0x_",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		if !short(s) {
			return
		}

		ten, any := "", ""
		if v, err := strconv.Atoi(s); err != nil {
			ten = reasonOf(err)
			if strings.Trim(strings.TrimLeft(s, "+-"), "0123456789") != "" || len(s)-len(strings.TrimLeft(s, "+-")) > 1 {
				ten = "syntax"
			}
		} else {
			ten = strconv.Itoa(v)
		}

		if v, err := strconv.ParseInt(s, 0, 64); err != nil {
			any = reasonOf(err)
			if !wellFormed(s) {
				any = "syntax"
			}
		} else {
			any = strconv.FormatInt(v, 10)
		}

		if got, want := ask(t, 'i', s), ten+" "+any; got != want {
			t.Errorf("Atoi and ParseInt of %q: got %s, want %s", s, got, want)
		}
	})
}

// FuzzUnquote holds Unquote to Go's strconv on any text, but for the
// empty character literal, which Go takes for the empty string and
// Snello refuses, as its own scanner does.
func FuzzUnquote(f *testing.F) {
	for _, s := range []string{
		"", `"`, `""`, `"a"`, `"a\tb\n"`, `"\u00e9\U0001F642\x41\101"`, `"\q"`, `"\x4"`, `"\ud800"`, `"\U00110000"`, `"\400"`, `"\'"`, `"\""`, "\"a\nb\"", "\"\xff\"", `"a"b"`, `"a`, `a"`,
		"`raw`", "`a\rb`", "`a`b`", "`", "``", `'x'`, `'\''`, `'\"'`, `'"'`, `''`, `'ab'`, `'\n'`, "'\xff'", "'é'", `'\u00e9'`, `'`, `x`,
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		if !short(s) {
			return
		}

		want := "syntax"
		if text, err := strconv.Unquote(s); err == nil && s != "''" {
			want = "ok " + hex.EncodeToString([]byte(text))
		}

		if got := ask(t, 'q', s); got != want {
			t.Errorf("Unquote of %q: got %s, want %s", s, got, want)
		}
	})
}

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("cc"); err != nil {
		os.Stderr.WriteString("skipping std tests: no C compiler on the PATH\n")
		os.Exit(0)
	}

	code := m.Run()
	if asked.cmd != nil {
		asked.in.Close()
		asked.cmd.Wait()
	}

	if asked.dir != "" {
		os.RemoveAll(asked.dir)
	}

	os.Exit(code)
}
