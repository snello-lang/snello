// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The standard streams as a program's caller sees them: in order, with
// nothing held back, open even when the caller closed one, and failing
// at the write that fails. Each test builds a program and runs it the
// way a shell would, which a run program under examples/run cannot.

// release builds src as a release program and returns its binary.
func release(t *testing.T, name, src string) string {
	t.Helper()
	module, err := emit(t, name, src, Config{})
	if err != nil {
		t.Fatal(err)
	}

	bin, out, err := Build(t.TempDir(), name, module, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	return bin
}

// TestStreamOrder sends both streams down one pipe: what fmt prints,
// what is written to os.Stdout and os.Stderr, and print's output come
// out in the order the program made them.
func TestStreamOrder(t *testing.T) {
	bin := release(t, "order", `package main
import ("fmt"; "os")
func main() {
	fmt.Println("1-out")
	os.Stderr.WriteString("2-err\n").Expect("stderr")
	fmt.Printf("%d-out\n", 3)
	print("4-err\n")
	os.Stdout.WriteString("5-out\n").Expect("stdout")
}
`)
	var both bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Stdout, cmd.Stderr = &both, &both
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	if got, want := both.String(), "1-out\n2-err\n3-out\n4-err\n5-out\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestPrompt reads a prompt that ends without a newline from a pipe
// before answering it: the output reaches the pipe when it is printed,
// not when a buffer fills or the program ends.
func TestPrompt(t *testing.T) {
	bin := release(t, "prompt", `package main
import ("bufio"; "fmt"; "os")
func main() {
	fmt.Printf("Name: ")
	r := bufio.NewReader(mut os.Stdin)
	line := r.ReadString('\n').Expect("read") ?? "".Clone()
	fmt.Printf("got %d\n", line.Len())
}
`)
	cmd := exec.Command(bin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	defer cmd.Process.Kill()
	prompt := make(chan string, 1)
	go func() {
		buf := make([]byte, 6)
		n, _ := stdout.Read(buf)
		prompt <- string(buf[:n])
	}()

	select {
	case got := <-prompt:
		if got != "Name: " {
			t.Fatalf("prompt %q, want %q", got, "Name: ")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the prompt did not reach the pipe before the answer")
	}

	stdin.Write([]byte("bob\n"))
	stdin.Close()
	rest := make([]byte, 16)
	n, _ := stdout.Read(rest)
	if got := string(rest[:n]); got != "got 4\n" {
		t.Errorf("after the answer %q, want %q", got, "got 4\n")
	}

	cmd.Wait()
}

// TestClosedStdin starts a program with its standard input closed. The
// runtime opens /dev/null on it first, so the file the program opens
// does not take descriptor 0 and stdin reads nothing, where it would
// otherwise read the file, and the file's drop closes it.
func TestClosedStdin(t *testing.T) {
	bin := release(t, "closed", `package main
import ("fmt"; "os")
func main() {
	{
		f := os.Open(ref os.Args[1]).Expect("open")
		_ = f
	}

	buf := make([]byte, 64)
	fmt.Println("stdin", os.Stdin.Read(mut buf).Expect("read"))
}
`)
	file := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(file, []byte("secret contents\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("sh", "-c", `exec "$0" "$1" <&-`, bin, file).CombinedOutput()
	if err != nil || string(out) != "stdin 0\n" {
		t.Errorf("%v: %q, want %q", err, out, "stdin 0\n")
	}
}

// TestWriteLimit writes blocks of 1000 bytes to standard output under a
// limit on the size of the file it goes to, until a write fails: the
// file holds every block reported written, and of the one that failed
// at most a part, where a buffer would have reported blocks written
// that never reached the file. The shells count the limit in different
// units, so the test asks only that a write fail and that the reports
// agree with the file.
func TestWriteLimit(t *testing.T) {
	bin := release(t, "limit", `package main
import ("fmt"; "os")
func main() {
	block := make([]byte, 1000)
	written := 0
	for i := 0; i < 256; i++ {
		if os.Stdout.Write(ref block).IsErr() {
			fmt.Fprintln(mut os.Stderr, written, "failed")
			return
		}

		written++
	}

	fmt.Fprintln(mut os.Stderr, written, "never failed")
}
`)
	file := filepath.Join(t.TempDir(), "out")
	cmd := exec.Command("sh", "-c", `trap '' XFSZ; ulimit -f 32; exec "$0" > "$1"`, bin, file)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}

	var blocks int
	var how string
	fmt.Sscan(stderr.String(), &blocks, &how)
	if size := info.Size(); how != "failed" || size < int64(blocks)*1000 || size >= int64(blocks+1)*1000 {
		t.Errorf("reported %q with %d bytes in the file, want a failure after the blocks the file holds", stderr.String(), info.Size())
	}
}

// TestRemoveAllNamesEntry removes a tree holding a file in a directory
// that may not be changed: the error names that file and the call that
// failed, as Go's does, not the path asked for.
func TestRemoveAllNamesEntry(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the system lets root remove what the test locks")
	}

	bin := release(t, "removeall", `package main
import ("fmt"; "os")
func main() {
	fmt.Println(os.RemoveAll(ref os.Args[1]))
}
`)
	dir := filepath.Join(t.TempDir(), "tree")
	locked := filepath.Join(dir, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(locked, "f"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}

	defer os.Chmod(locked, 0o755) // so that the test's own cleanup can remove it

	out, err := exec.Command(bin, dir).CombinedOutput()
	want := `Err("unlinkat ` + filepath.Join(locked, "f") + `: permission denied")`
	if err != nil || strings.TrimSpace(string(out)) != want {
		t.Errorf("%v: %s, want %s", err, out, want)
	}
}
