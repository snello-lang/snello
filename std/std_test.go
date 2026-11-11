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
	"time"

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
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// A Chunks hands out its bytes in pieces of the sizes it is given, in
// turn, a size of zero counting as one, and one a call when it is
// given none.
struct Chunks impl io.Reader {
	data  []byte
	at    int
	sizes []byte
	turn  int
}

func (c mut Chunks) Read(buf mut []byte) Result<int, io.Error> {
	size := 1
	if len(c.sizes) > 0 {
		size = max(int(c.sizes[c.turn%len(c.sizes)]), 1)
		c.turn++
	}

	n := min(size, len(buf), len(c.data)-c.at)
	for i := 0; i < n; i++ {
		buf[i] = c.data[c.at+i]
	}

	c.at += n
	return Ok(n)
}

// scanned scans the input of a question with a line Scanner, or a word
// Scanner when words says so: two bytes of the limit, zero for the
// default, a byte of the count of read sizes, the sizes, then the data.
// It answers with every line or word in hexadecimal between brackets,
// then end, or the error that ended the scan.
func scanned(raw ref []byte, words bool) string {
	limit := int(raw[0])<<8 | int(raw[1])
	k := int(raw[2])
	var src = Chunks{data: raw[3+k:].Clone(), at: 0, sizes: raw[3:3+k].Clone(), turn: 0}
	sc := if words { bufio.Words(mut src) } else { bufio.NewScanner(mut src) }
	if limit > 0 {
		sc.Buffer(limit)
	}

	var out strings.Builder
	for sc.Scan() {
		out.WriteString(fmt.Sprintf("[%x]", sc.Bytes()))
	}

	match sc.Err() {
		Some(e) => out.WriteString(" " + e.Error()),
		None => out.WriteString(" end"),
	}

	return out.String()
}

// buffered runs the operations of a question on a bytes.Buffer, each a
// byte of the operation and a byte of its argument, a write taking the
// bytes after them, and answers with what each returned and the length
// after it, then the bytes left.
func buffered(raw ref []byte) string {
	var b bytes.Buffer
	var out strings.Builder
	i := 0
	for i+1 < len(raw) {
		op := int(raw[i]) % 14
		arg := raw[i+1]
		i += 2
		if op <= 1 {
			k := min(int(arg)%8, len(raw)-i)
			if op == 0 {
				s := string(raw[i:i+k])
				b.WriteString(ref s)
			} else {
				_ = b.Write(raw[i:i+k])
			}

			i += k
		} else if op == 2 {
			b.WriteByte(arg)
		} else if op == 3 {
			v := int(arg) << 8
			if i < len(raw) {
				v = v | int(raw[i])
				i++
			}

			b.WriteRune(rune(v*18 - 300))
		} else if op == 4 {
			p := make([]byte, int(arg)%9)
			n := b.Read(mut p) ?? 0
			out.WriteString(fmt.Sprintf("r%x", p[0:n]))
		} else if op == 5 {
			out.WriteString(fmt.Sprintf("n%x", b.Next(int(arg)%9)))
		} else if op == 6 {
			match b.ReadByte() {
				Some(c) => out.WriteString(fmt.Sprintf("b%d", c)),
				None => out.WriteString("b-"),
			}
		} else if op == 7 {
			match b.ReadRune() {
				Some((r, n)) => out.WriteString(fmt.Sprintf("u%d/%d", r, n)),
				None => out.WriteString("u-"),
			}
		} else if op == 8 {
			out.WriteString(fmt.Sprintf("B%t", b.UnreadByte()))
		} else if op == 9 {
			out.WriteString(fmt.Sprintf("U%t", b.UnreadRune()))
		} else if op == 10 {
			match b.ReadBytes(arg) {
				Some(l) => out.WriteString(fmt.Sprintf("l%x", l)),
				None => out.WriteString("l-"),
			}
		} else if op == 11 {
			match b.ReadString(arg) {
				Some(l) => out.WriteString(fmt.Sprintf("s%x", l)),
				None => out.WriteString("s-"),
			}
		} else if op == 12 {
			b.Truncate(int(arg) - 3)
		} else if arg%4 == 0 {
			b.Reset()
		} else {
			b.Grow(int(arg))
		}

		out.WriteString(fmt.Sprintf(":%d ", b.Len()))
	}

	out.WriteString(fmt.Sprintf("=%x/%x", b.Bytes(), b.String()))
	return out.String()
}

// reads runs the operations of a question on a bufio.Reader over a
// Chunks: a byte of the count of read sizes, the sizes, two bytes of
// the length of the operations, the operations, each a byte of the
// operation and a byte of its argument, then the data. It answers with
// what each operation returned.
func reads(raw ref []byte) Result<string, io.Error> {
	k := int(raw[0])
	at := 3 + k
	end := at + (int(raw[1+k])<<8 | int(raw[2+k]))
	var src = Chunks{data: raw[end:].Clone(), at: 0, sizes: raw[1:1+k].Clone(), turn: 0}
	r := bufio.NewReader(mut src)
	var out strings.Builder
	for at+1 < end {
		op := int(raw[at]) % 9
		arg := int(raw[at+1])
		at += 2
		if op == 0 || op == 8 {
			n := arg%7 + 1
			if op == 0 {
				n = if arg < 200 { arg % 41 } else { 4096 + (arg-200)*40 }
			}

			p := make([]byte, n)
			got := r.Read(mut p)?
			out.WriteString(fmt.Sprintf("r%x ", p[0:got]))
		} else if op == 1 {
			match r.ReadByte()? {
				Some(c) => out.WriteString(fmt.Sprintf("b%d ", c)),
				None => out.WriteString("b- "),
			}
		} else if op == 2 {
			match r.ReadRune()? {
				Some((c, n)) => out.WriteString(fmt.Sprintf("u%d/%d ", c, n)),
				None => out.WriteString("u- "),
			}
		} else if op == 3 {
			out.WriteString(fmt.Sprintf("B%t ", r.UnreadByte()))
		} else if op == 4 {
			out.WriteString(fmt.Sprintf("U%t ", r.UnreadRune()))
		} else if op == 5 {
			match r.ReadBytes(byte(arg))? {
				Some(l) => out.WriteString(fmt.Sprintf("l%x ", l)),
				None => out.WriteString("l- "),
			}
		} else if op == 6 {
			match r.ReadString(byte(arg))? {
				Some(l) => out.WriteString(fmt.Sprintf("s%x ", l)),
				None => out.WriteString("s- "),
			}
		} else {
			n := if arg < 200 { arg % 50 } else if arg < 240 { 4096 - (arg-200)*5 } else { 239 - arg }
			match r.Peek(n) {
				Ok(p) => out.WriteString(fmt.Sprintf("p%x ", p)),
				Err(_) => out.WriteString("p! "),
			}
		}
	}

	return Ok(out.String())
}

// queued runs a Buffer as a queue behind a backlog of 8 MiB that fills
// its slice, a record read and one written a hundred thousand times,
// and answers with what it holds at the end.
func queued() int {
	b := bytes.NewBuffer(make([]byte, 1<<23))
	for i := 0; i < 100000; i++ {
		_ = b.Next(16)
		b.WriteString("0123456789abcdef")
	}

	return b.Len()
}

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
		} else if line[0] == 'l' || line[0] == 'w' {
			fmt.Println(scanned(ref raw, line[0] == 'w'))
		} else if line[0] == 'b' {
			fmt.Println(buffered(ref raw))
		} else if line[0] == 'r' {
			match reads(ref raw) {
				Ok(answer) => fmt.Println(answer),
				Err(e) => fmt.Println("error:", e),
			}
		} else if line[0] == 'k' {
			fmt.Println(queued())
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

	type reply struct {
		answer string
		err    error
	}

	done := make(chan reply, 1)
	go func() {
		_, err := fmt.Fprintf(asked.in, "%c%x\n", letter, input)
		var answer string
		if err == nil {
			answer, err = asked.out.ReadString('\n')
		}

		done <- reply{answer, err}
	}()

	var r reply
	select {
	case r = <-done:
	case <-time.After(askLimit):
		asked.cmd.Process.Kill()
		r = <-done // the pipes close with the process
		r.err = fmt.Errorf("no answer in %v", askLimit)
	}

	if r.err != nil {
		asked.in.Close()
		state := asked.cmd.Wait()
		asked.cmd = nil
		t.Fatalf("the answerer stopped on %c of %.300q: %v, %v", letter, input, r.err, state)
	}

	return strings.TrimSuffix(r.answer, "\n")
}

// askLimit is how long the answerer has to answer one question: a
// question no input of the fuzz tests takes a second over, so that a
// function of the library that never returns is a failure with the
// input that made it hang, not a fuzzer that stops.
const askLimit = 20 * time.Second

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

// chunks hands out its bytes in pieces of the sizes it is given, in
// turn, as the answerer's Chunks does.
type chunks struct {
	data, sizes []byte
	turn        int
}

func (c *chunks) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}

	size := 1
	if len(c.sizes) > 0 {
		size = max(int(c.sizes[c.turn%len(c.sizes)]), 1)
		c.turn++
	}

	n := copy(p[:min(size, len(p))], c.data)
	c.data = c.data[n:]
	return n, nil
}

// goScanned is what Go's bufio.Scanner makes of the data, read in the
// pieces the sizes give, with lines or with ScanWords, the limit set by
// Buffer when it is not zero: every token in hexadecimal between
// brackets, then end or the error that ended the scan.
func goScanned(data, sizes []byte, limit int, words bool) string {
	sc := bufio.NewScanner(&chunks{data: data, sizes: sizes})
	if words {
		sc.Split(bufio.ScanWords)
	}

	if limit > 0 {
		sc.Buffer(nil, limit)
	}

	var b strings.Builder
	for sc.Scan() {
		fmt.Fprintf(&b, "[%x]", sc.Bytes())
	}

	if err := sc.Err(); err != nil {
		b.WriteString(" " + err.Error())
	} else {
		b.WriteString(" end")
	}

	return b.String()
}

// FuzzScanner holds bufio's Scanner, by lines and by words, to Go's
// bufio.Scanner on any input read in any pieces, down to a byte a
// read, under any limit: the same lines and words come out in the same
// order, so no byte is lost or read twice, a line or word past the
// limit ends the scan with "token too long" where Go's does, and none
// is handed out past it.
func FuzzScanner(f *testing.F) {
	type seed struct {
		data, sizes string
		limit       uint16
		words       bool
	}

	for _, s := range []seed{
		{"one\ntwo\n\nthree", "", 0, false},
		{"a\r\nb\rc\r", "\x01", 0, false},
		{"exactly8", "\x03\x05", 8, false},
		{"exactly8\n", "", 8, false},
		{"exactly8\n", "", 9, false},
		{strings.Repeat("x", 5000) + "\nshort", "\xff", 4096, false},
		{strings.Repeat("y", 70000), "\x10", 0, false},
		{"\n\n\n", "", 1, false},
		{"  one two\tthree\u3000four\n\n five\xffsix  ", "\x01", 0, true},
		{"\u0085\u00a0\u1680\u2000\u200a\u2028\u2029\u202f\u205f\u3000x", "\x02", 0, true},
		{strings.Repeat(" ", 10000) + "end", "\x07", 16, true},
		{"word" + strings.Repeat("z", 20), "", 10, true},
		{"\xe3\x80", "\x01", 0, true},
	} {
		f.Add([]byte(s.data), []byte(s.sizes), s.limit, s.words)
	}

	f.Fuzz(func(t *testing.T, data, sizes []byte, limit uint16, words bool) {
		small := len(sizes) == 0 // the reads that hand out a few bytes each
		for _, s := range sizes {
			small = small || s < 64
		}

		// Go's ScanWords decodes a word again from its start after every
		// read, so a long input read in small pieces takes the reference
		// seconds; such an input is kept short.
		if len(data) > 1<<17 || len(sizes) > 255 || (small && len(data) > 1<<13) {
			return
		}

		payload := append([]byte{byte(limit >> 8), byte(limit), byte(len(sizes))}, sizes...)
		payload = append(payload, data...)
		letter := byte('l')
		if words {
			letter = 'w'
		}

		if got, want := ask(t, letter, string(payload)), goScanned(data, sizes, int(limit), words); got != want {
			t.Errorf("the Scanner of %q, words %t, read in pieces of %v, limit %d:\ngot  %.300s\nwant %.300s", data, words, sizes, limit, got, want)
		}
	})
}

// goBuffered is what Go's bytes.Buffer makes of the operations the
// answerer's buffered runs, in the same words. Go's Buffer puts a byte
// back in two places where Snello's refuses, which stale marks: after a
// Grow, where Go steps back into bytes the Grow may have dropped, and
// after a line read that found nothing, where Go steps back over a byte
// an earlier call read.
func goBuffered(raw []byte) string {
	var b bytes.Buffer
	var out strings.Builder
	stale := false
	for i := 0; i+1 < len(raw); {
		op, arg := int(raw[i])%14, raw[i+1]
		i += 2
		was := stale
		stale = false
		switch op {
		case 0, 1:
			k := min(int(arg)%8, len(raw)-i)
			if op == 0 {
				b.WriteString(string(raw[i : i+k]))
			} else {
				b.Write(raw[i : i+k])
			}

			i += k
		case 2:
			b.WriteByte(arg)
		case 3:
			v := int(arg) << 8
			if i < len(raw) {
				v |= int(raw[i])
				i++
			}

			b.WriteRune(rune(v*18 - 300))
		case 4:
			p := make([]byte, int(arg)%9)
			n, _ := b.Read(p)
			fmt.Fprintf(&out, "r%x", p[:n])
		case 5:
			fmt.Fprintf(&out, "n%x", b.Next(int(arg)%9))
		case 6:
			if c, err := b.ReadByte(); err == nil {
				fmt.Fprintf(&out, "b%d", c)
			} else {
				out.WriteString("b-")
			}
		case 7:
			if r, n, err := b.ReadRune(); err == nil {
				fmt.Fprintf(&out, "u%d/%d", r, n)
			} else {
				out.WriteString("u-")
			}
		case 8, 9:
			put := false
			if was {
				stale = true
			} else if op == 8 {
				put = b.UnreadByte() == nil
			} else {
				put = b.UnreadRune() == nil
			}

			fmt.Fprintf(&out, "%c%t", "BU"[op-8], put)
		case 10, 11:
			if line, _ := b.ReadBytes(arg); len(line) > 0 {
				fmt.Fprintf(&out, "%c%x", "ls"[op-10], line)
			} else {
				fmt.Fprintf(&out, "%c-", "ls"[op-10])
				stale = true
			}
		case 12:
			if n := int(arg) - 3; n <= 0 {
				b.Reset()
			} else {
				b.Truncate(min(n, b.Len()))
			}
		default:
			if arg%4 == 0 {
				b.Reset()
			} else {
				b.Grow(int(arg))
				stale = true
			}
		}

		fmt.Fprintf(&out, ":%d ", b.Len())
	}

	fmt.Fprintf(&out, "=%x/%x", b.Bytes(), b.String())
	return out.String()
}

// FuzzBuffer holds bytes.Buffer to Go's on any sequence of its
// operations: every write, read, unread, Truncate, Reset and Grow gives
// what Go's gives and leaves the bytes Go's leaves, so no sequence
// loses a byte, reads one twice or puts back one that was not read.
func FuzzBuffer(f *testing.F) {
	for _, s := range []string{
		"\x00\x05hello\x04\x03\x08\x00\x08\x00\x06\x00",
		"\x00\x02ab\x06\x00\x06\x00\x0d\x0a\x08\x00\x00\x01x",                  // an unread after a Grow of an emptied Buffer
		"\x00\x06h\xc3\xa9llo\x06\x00\x07\x00\x0d\xff\x09\x00\x08\x00",         // and after one that moves the bytes
		"\x01\x07a\nb\nc\nd\x0a\x0a\x0b\x0a\x0b\x7a\x0a\x0a\x08\x00",           // lines, and an unread after one that found nothing
		"\x03\x0c\x00\x03\xf0\x00\x03\x00\x00\x07\x00\x09\x00\x07\x00\x07\x00", // a surrogate, a value past the last code point, one below zero
		"\x00\x07abcdefg\x05\x03\x0c\x05\x0c\x00\x0c\xff\x0d\x00",
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, ops []byte) {
		if len(ops) > 1<<12 {
			return
		}

		if got, want := ask(t, 'b', string(ops)), goBuffered(ops); got != want {
			t.Errorf("the Buffer of %q:\ngot  %.600s\nwant %.600s", ops, got, want)
		}
	})
}

// goReads is what Go's bufio.Reader makes of the operations the
// answerer's reads runs, over the same data in the same pieces, in the
// same words. An UnreadRune that does not come straight after a
// ReadRune is refused without asking Go: its documentation says it
// fails there, and its implementation lets a few through, after a Peek
// refused for its count and after some reads that gave nothing.
func goReads(data, sizes, ops []byte) string {
	r := bufio.NewReaderSize(&chunks{data: data, sizes: sizes}, 4096)
	var out strings.Builder
	last := -1
	for i := 0; i+1 < len(ops); i += 2 {
		op, arg := int(ops[i])%9, int(ops[i+1])
		before := last
		last = op
		switch op {
		case 0, 8:
			n := arg%7 + 1
			if op == 0 {
				n = arg % 41
				if arg >= 200 {
					n = 4096 + (arg-200)*40
				}
			}

			p := make([]byte, n)
			got, _ := r.Read(p)
			fmt.Fprintf(&out, "r%x ", p[:got])
		case 1:
			if c, err := r.ReadByte(); err == nil {
				fmt.Fprintf(&out, "b%d ", c)
			} else {
				out.WriteString("b- ")
			}
		case 2:
			if c, n, err := r.ReadRune(); err == nil {
				fmt.Fprintf(&out, "u%d/%d ", c, n)
			} else {
				out.WriteString("u- ")
			}
		case 3:
			fmt.Fprintf(&out, "B%t ", r.UnreadByte() == nil)
		case 4:
			fmt.Fprintf(&out, "U%t ", before == 2 && r.UnreadRune() == nil)
		case 5, 6:
			if line, _ := r.ReadBytes(byte(arg)); len(line) > 0 {
				fmt.Fprintf(&out, "%c%x ", "ls"[op-5], line)
			} else {
				fmt.Fprintf(&out, "%c- ", "ls"[op-5])
			}
		default:
			n := 239 - arg
			if arg < 200 {
				n = arg % 50
			} else if arg < 240 {
				n = 4096 - (arg-200)*5
			}

			if p, err := r.Peek(n); err == nil || err == io.EOF {
				fmt.Fprintf(&out, "p%x ", p)
			} else {
				out.WriteString("p! ")
			}
		}
	}

	return out.String()
}

// FuzzReader holds bufio's Reader to Go's on any sequence of its reads,
// peeks and unreads over any input read in any pieces: each gives the
// bytes Go's gives, a Peek reads none, UnreadByte puts a byte back
// where Go's does and nowhere else, and UnreadRune a rune only straight
// after the ReadRune that read it.
func FuzzReader(f *testing.F) {
	type seed struct{ data, sizes, ops string }
	long := strings.Repeat("0123456789", 500) // a line past the buffer's 4096 bytes
	for _, s := range []seed{
		{"abc\ndef", "", "\x01\x00\x03\x00\x03\x00\x06\x0a\x03\x00\x00\x02\x02\x00\x04\x00\x03\x00"},
		{"x", "\x01", "\x01\x00\x01\x00\x03\x00\x01\x00\x00\x05\x06\x0a\x03\x00\x01\x00"},         // an unread after reads that gave nothing
		{"\xc3\xa9!", "\x01", "\x02\x00\x07\xf0\x04\x00\x02\x00\x01\x00\x01\x00\x04\x00\x03\x00"}, // a Peek refused for its count, then the unreads
		{long + "\n" + long, "\xff\x07", "\x07\xc8\x05\x0a\x03\x00\x00\xd2\x03\x00\x06\x39\x02\x00\x04\x00\x07\x0a"},
		{"\xe4\xb8\x96\xe4\xb8", "\x02\x01", "\x02\x00\x02\x00\x04\x00\x02\x00\x02\x00\x03\x00\x01\x00"},
		{"\xc3\xa9ab", "", "\x02\x00\x08\x00\x04\x00\x01\x00\x02\x00\x02\x00\x03\x00\x01\x00"}, // a rune, a Read, the unreads; then a ReadRune at the end
		{"\xe4", "0", "802010"}, // a rune cut short at the end, a line read that finds nothing, then UnreadRune, which Go lets through
	} {
		f.Add([]byte(s.data), []byte(s.sizes), []byte(s.ops))
	}

	f.Fuzz(func(t *testing.T, data, sizes, ops []byte) {
		if len(data) > 1<<15 || len(sizes) > 255 || len(ops) > 1<<10 {
			return
		}

		payload := append([]byte{byte(len(sizes))}, sizes...)
		payload = append(payload, byte(len(ops)>>8), byte(len(ops)))
		payload = append(append(payload, ops...), data...)
		if got, want := ask(t, 'r', string(payload)), goReads(data, sizes, ops); got != want {
			t.Errorf("the Reader of %q, read in pieces of %v, asked %q:\ngot  %.600s\nwant %.600s", data, sizes, ops, got, want)
		}
	})
}

// TestBufferQueue holds a bytes.Buffer used as a queue to a cost that
// does not grow with what it holds: a record read and a record written,
// a hundred thousand times, behind a backlog of 8 MiB that fills the
// slice. A Buffer that moved the backlog to make room for each record
// would move 800 GB.
func TestBufferQueue(t *testing.T) {
	ask(t, 'i', "0") // the answerer, built and started
	start := time.Now()
	if got := ask(t, 'k', ""); got != "8388608" {
		t.Errorf("the queue holds %s bytes at the end, want 8388608", got)
	}

	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("100,000 records through a queue of 8 MiB took %v; a Buffer that moves only what it must takes milliseconds", took)
	}
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
