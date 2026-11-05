// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/internal/backendtest"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// TestHello builds the smallest program through the LLVM backend, in
// both builds, and checks what it prints and that it exits cleanly:
// the module, the calling convention of a call into the runtime, the
// link with the runtime and the runtime's own main all work or none
// of them does.
func TestHello(t *testing.T) {
	src := "package main\nimport \"fmt\"\nfunc main() {\n\tfmt.Println(\"hello, Snello\")\n\tfmt.Println(\"two\", \"words\")\n}\n"
	for _, debug := range []bool{false, true} {
		out, stderr, code := run(t, "hello", src, debug)
		if out != "hello, Snello\ntwo words\n" || code != 0 {
			t.Errorf("debug=%v: got output %q, stderr %q and exit code %d, want the two lines and 0", debug, out, stderr, code)
		}
	}
}

// TestRunExamples builds every program under examples/run and the
// core programs that have an expected output, through the LLVM
// backend, in both builds, and compares what each prints with the
// .out file beside it: the same suite the C backend runs, so that the
// two backends are held to the same output.
func TestRunExamples(t *testing.T) {
	programs, err := filepath.Glob(filepath.Join("..", "..", "..", "examples", "run", "*.snello"))
	if err != nil || len(programs) == 0 {
		t.Fatalf("no programs under examples/run: %v", err)
	}

	core, _ := filepath.Glob(filepath.Join("..", "..", "..", "examples", "core", "*.snello"))
	for _, path := range append(programs, core...) {
		name := strings.TrimSuffix(filepath.Base(path), ".snello")
		want, err := os.ReadFile(strings.TrimSuffix(path, ".snello") + ".out")
		if err != nil {
			continue // a core program without an output yet; the C suite names the ones that need it
		}

		for _, debug := range []bool{false, true} {
			suffix := ""
			if debug {
				suffix = "-debug"
			}

			t.Run(name+suffix, func(t *testing.T) {
				t.Parallel()
				src, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}

				out, stderr, code := run(t, name, string(src), debug, strings.TrimSuffix(path, ".snello")+".files")
				if out != string(want) || code != 0 {
					t.Errorf("%s printed:\n%s--- stderr ---\n%s--- exit %d, want ---\n%s", path, out, stderr, code, want)
				}
			})
		}
	}
}

// TestNoReservation runs the allocator's program, in both builds,
// under a limit on the address space of 2 GB, below which the heap
// makes no reservation: every block is then the system's, through the
// fallback a used-up reservation takes too, and a free must still find
// its way back. Only Linux enforces the limit, so only Linux runs it.
func TestNoReservation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only Linux enforces a limit on the address space")
	}

	path := filepath.Join("..", "..", "..", "examples", "run", "alloc.snello")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	want, err := os.ReadFile(strings.TrimSuffix(path, ".snello") + ".out")
	if err != nil {
		t.Fatal(err)
	}

	for _, debug := range []bool{false, true} {
		module, err := emit(t, "alloc", string(src), Config{Debug: debug})
		if err != nil {
			t.Fatal(err)
		}

		bin, out, err := Build(t.TempDir(), "alloc", module, debug)
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}

		got, err := exec.Command("sh", "-c", `ulimit -v 2097152 && exec "$0"`, bin).CombinedOutput()
		if err != nil || string(got) != string(want) {
			t.Errorf("debug=%v: %v, printed:\n%s--- want ---\n%s", debug, err, got, want)
		}
	}
}

// TestAcceptAfterReset sends a server, before it calls Accept, one
// connection its client resets and then one that stays. Accept never
// fails for the client's reset: Darwin hands the dead connection over
// with socket options it refuses, and Accept skips it, as Go's runtime
// does; Linux hands it over usable, and the server's write to it fails
// instead, so the server writes to each connection until one takes it.
// A Snello program cannot reset a connection, so Go plays the clients.
func TestAcceptAfterReset(t *testing.T) {
	src := `package main
import ("fmt"; "net"; "os"; "time")
func main() {
	l := net.Listen("127.0.0.1", 0).Expect("listen")
	fmt.Fprintln(mut os.Stderr, l.Port())
	time.Sleep(300 * time.Millisecond)
	for {
		c := l.Accept().Expect("accept")
		if c.WriteString("hello").IsOk() {
			return
		}
	}
}
`
	module, err := emit(t, "server", src, Config{})
	if err != nil {
		t.Fatal(err)
	}

	bin, out, err := Build(t.TempDir(), "server", module, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	lines := bufio.NewScanner(stderr)
	if !lines.Scan() {
		t.Fatal("the server printed no port")
	}

	addr := "127.0.0.1:" + lines.Text()
	reset, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}

	reset.(*net.TCPConn).SetLinger(0) // close sends a reset
	reset.Close()
	kept, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}

	defer kept.Close()
	got := make([]byte, 5)
	if _, err := kept.Read(got); err != nil || string(got) != "hello" {
		for lines.Scan() {
			t.Log(lines.Text())
		}

		t.Fatalf("read %q, %v; want hello", got, err)
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("server: %v", err)
	}
}

// TestResetMessage has a client reset a connection a server is about
// to read: the server's Error is worded as Go words it, naming both
// ends, and its Kind is Reset, so a program tells it from the rest
// without reading the message. Go plays the client, which a Snello
// program cannot, since resetting takes a socket option net does not
// offer.
func TestResetMessage(t *testing.T) {
	src := `package main
import ("fmt"; "io"; "net"; "os"; "time")
func main() {
	l := net.Listen("127.0.0.1", 0).Expect("listen")
	fmt.Fprintln(mut os.Stderr, l.Port())
	c := l.Accept().Expect("accept")
	c.WriteString("hi").Expect("write")
	time.Sleep(300 * time.Millisecond)
	buf := make([]byte, 8)
	match c.Read(mut buf) {
		Ok(n) => fmt.Fprintln(mut os.Stderr, "read", n),
		Err(e) => fmt.Fprintln(mut os.Stderr, e, e.Kind() == io.Reset),
	}
}
`
	module, err := emit(t, "server", src, Config{})
	if err != nil {
		t.Fatal(err)
	}

	bin, out, err := Build(t.TempDir(), "server", module, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	lines := bufio.NewScanner(stderr)
	if !lines.Scan() {
		t.Fatal("the server printed no port")
	}

	server := "127.0.0.1:" + lines.Text()
	c, err := net.Dial("tcp", server)
	if err != nil {
		t.Fatal(err)
	}

	hi := make([]byte, 2)
	if _, err := c.Read(hi); err != nil { // accepted, so the reset reaches a connection the server holds
		t.Fatal(err)
	}

	client := c.LocalAddr().String()
	c.(*net.TCPConn).SetLinger(0)
	c.Close()
	want := "read tcp " + server + "->" + client + ": read: connection reset by peer true"
	if !lines.Scan() || lines.Text() != want {
		t.Errorf("the server printed %q, want %q", lines.Text(), want)
	}

	cmd.Wait()
}

// TestPrograms builds and runs the programs every backend runs, from
// emit/internal/backendtest, and checks each against what it must give.
func TestPrograms(t *testing.T) {
	for _, p := range backendtest.Programs(testing.Short()) {
		t.Run(p.Name, func(t *testing.T) {
			if why := p.Skip(); why != "" {
				t.Skip(why)
			}

			for _, debug := range p.Builds() {
				out, stderr, code := run(t, p.Name, p.Src, debug)
				if bad := p.Check(out, stderr, code); bad != "" {
					t.Errorf("debug=%v: %s", debug, bad)
				}
			}
		})
	}
}

// TestUnsupported checks that a construct the backend does not lower
// is an error that names it, never silent wrong code.
func TestUnsupported(t *testing.T) {
	for _, r := range backendtest.Refusals {
		_, err := emit(t, "unsupported", "package main\n"+r.Src+"\n", Config{})
		if err == nil || !strings.Contains(err.Error(), "the LLVM backend does not support") || !strings.Contains(err.Error(), r.Want) {
			t.Errorf("%s: got %v, want an error naming %q", r.Src, err, r.Want)
		}
	}
}

// TestABI pins the calling convention of a three-word struct per
// processor, which clang decides for C and the backend must match,
// and the target triple written for each processor and system.
func TestABI(t *testing.T) {
	amd64, err := targetABI("amd64")
	if err != nil || amd64.words("%sn_string") != "ptr byval(%sn_string) align 8" {
		t.Errorf("amd64: %q, %v", amd64.words("%sn_string"), err)
	}

	arm64, err := targetABI("arm64")
	if err != nil || arm64.words("%sn_string") != "ptr" {
		t.Errorf("arm64: %q, %v", arm64.words("%sn_string"), err)
	}

	if _, err := targetABI("riscv64"); err == nil {
		t.Error("riscv64: want an error, the convention is not known")
	}

	for _, tt := range []struct{ arch, os, want string }{
		{"arm64", "darwin", "arm64-apple-macosx"},
		{"amd64", "darwin", "x86_64-apple-macosx"},
		{"arm64", "linux", "aarch64-unknown-linux-gnu"},
		{"amd64", "linux", "x86_64-unknown-linux-gnu"},
	} {
		if got := triple(tt.arch, tt.os); got != tt.want {
			t.Errorf("triple(%s, %s) = %q, want %q", tt.arch, tt.os, got, tt.want)
		}
	}
}

// TestCrossCompile compiles the module emitted for each processor on
// Linux to an object, so that both calling conventions are checked by
// clang on every machine the suite runs on; running it is the other
// runner's job.
func TestCrossCompile(t *testing.T) {
	src := "package main\nimport \"fmt\"\nfunc main() {\n\tfmt.Println(\"hello, Snello\")\n}\n"
	for _, arch := range []string{"amd64", "arm64"} {
		module, err := emit(t, "hello", src, Config{Arch: arch, OS: "linux"})
		if err != nil {
			t.Fatal(err)
		}

		dir := t.TempDir()
		path := filepath.Join(dir, "hello.ll")
		if err := os.WriteFile(path, module, 0o644); err != nil {
			t.Fatal(err)
		}

		out, err := exec.Command(Compiler(), "-target", moduleTriple(module), "-O2", "-c", "-o", filepath.Join(dir, "hello.o"), path).CombinedOutput()
		if err != nil {
			t.Errorf("%s: %v\n%s", arch, err, out)
		}
	}
}

// BenchmarkEmit emits every program under examples/run once per
// iteration, parsed and checked ahead of time, and reports
// allocations, the number the coding standards track.
func BenchmarkEmit(b *testing.B) {
	type checked struct {
		f    *ast.File
		file *token.File
		info *types.Info
		libs []lower.Library
	}

	paths, _ := filepath.Glob(filepath.Join("..", "..", "..", "examples", "run", "*.snello"))
	var programs []checked
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}

		f, file, err := parser.ParseFile(path, src)
		if err != nil {
			b.Fatal(err)
		}

		info := &types.Info{}
		imp := importer.NewImporter(info)
		if _, err := (&types.Config{Importer: imp}).Check("", file, f, info); err != nil {
			b.Fatal(err)
		}

		programs = append(programs, checked{f, file, info, libraries(imp)})
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, p := range programs {
			if _, err := Emit(Config{}, p.file, p.f, p.info, p.libs...); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// emit parses, checks and emits src as the program name.
func emit(t *testing.T, name, src string, cfg Config) ([]byte, error) {
	t.Helper()
	f, file, err := parser.ParseFile(name+".snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	imp := importer.NewImporter(info)
	if _, err := (&types.Config{Importer: imp}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	return Emit(cfg, file, f, info, libraries(imp)...)
}

// libraries returns the packages an importer loaded, as Emit takes
// them.
func libraries(imp *importer.Importer) []lower.Library {
	var libs []lower.Library
	for _, p := range imp.Packages() {
		libs = append(libs, lower.Library{Path: p.Path, File: p.File, Syntax: p.Syntax})
	}

	return libs
}

// run builds src, a Snello program, through the backend and runs it,
// returning its standard output, its standard error and its exit code.
// The files under fixtures, if the directory exists, are copied beside
// the binary first, for a program that reads them.
func run(t *testing.T, name, src string, debug bool, fixtures ...string) (string, string, int) {
	t.Helper()
	module, err := emit(t, name, src, Config{Debug: debug})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	for _, fixture := range fixtures {
		entries, err := os.ReadDir(fixture)
		if err != nil {
			continue // no files to place
		}

		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(fixture, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	bin, out, err := Build(dir, name, module, debug)
	if err != nil {
		t.Fatalf("%v\n%s\n--- emitted IR ---\n%s", err, out, module)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second) // a program that hangs fails rather than stalling the suite
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = dir
	stdout, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok {
		return string(stdout), string(exit.Stderr), exit.ExitCode()
	}

	if err != nil {
		t.Fatalf("running %s: %v", name, err)
	}

	return string(stdout), "", 0
}

func TestMain(m *testing.M) {
	if _, err := exec.LookPath(Compiler()); err != nil {
		os.Stderr.WriteString("skipping emit/llvm tests: no clang on the PATH\n")
		os.Exit(0)
	}

	os.Exit(m.Run())
}
