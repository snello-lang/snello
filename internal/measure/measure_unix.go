// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !windows

package measure

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/snello-lang/snello/internal/testproc"
)

const (
	// darwin is the system that counts maxrss in bytes; the others
	// count it in kilobytes.
	darwin   = "darwin"
	kilobyte = 1024

	// measurerSource is the measurer. It runs the program its arguments
	// name as its child and writes, on its report, how long the program
	// ran in nanoseconds, its maxrss as the system counts it, and its
	// exit code, or the signal that ended it as a negative number. The
	// report is descriptor 3, the first after the standard three, as
	// os/exec numbers ExtraFiles, and the child closes it before its
	// exec, so the program does not hold it.
	measurerSource = `#define _DEFAULT_SOURCE

#include <errno.h>
#include <stdio.h>
#include <string.h>
#include <sys/resource.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

enum { report_fd = 3, measurer_failed = 2, exec_failed = 127 };

int main(int argc, char **argv) {
    FILE *report = fdopen(report_fd, "w");
    if (report == NULL || argc < 2) {
        return measurer_failed;
    }

    struct timespec start, end;
    clock_gettime(CLOCK_MONOTONIC, &start);
    pid_t pid = fork();
    if (pid < 0) {
        perror("fork");
        return measurer_failed;
    }

    if (pid == 0) {
        close(report_fd);
        execv(argv[1], argv + 1);
        fprintf(stderr, "cannot run %s: %s\n", argv[1], strerror(errno));
        _exit(exec_failed);
    }

    int status;
    struct rusage usage;
    while (wait4(pid, &status, 0, &usage) < 0) {
        if (errno != EINTR) {
            perror("wait4");
            return measurer_failed;
        }
    }

    clock_gettime(CLOCK_MONOTONIC, &end);
    long long took = (long long)(end.tv_sec - start.tv_sec) * 1000000000LL + (end.tv_nsec - start.tv_nsec);
    int code = WIFEXITED(status) ? WEXITSTATUS(status) : -WTERMSIG(status);
    fprintf(report, "%lld %ld %d\n", took, (long)usage.ru_maxrss, code);
    return fclose(report) == 0 ? 0 : measurer_failed;
}
`
)

// A Measurer runs programs and measures them.
type Measurer struct {
	path string // the measurer's binary
}

// New compiles the measurer into the test's temporary directory with
// the C compiler CC names, cc by default, failing the test if it
// cannot.
func New(t testing.TB) *Measurer {
	t.Helper()
	dir := t.TempDir()
	src, bin := filepath.Join(dir, "measure.c"), filepath.Join(dir, "measure")
	if err := os.WriteFile(src, []byte(measurerSource), 0o644); err != nil {
		t.Fatal(err)
	}

	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}

	if out, err := exec.Command(cc, "-O1", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compiling the measurer: %v\n%s", err, out)
	}

	return &Measurer{path: bin}
}

// Run runs the program at path with args, writing its standard output
// to stdout and its standard error to stderr, and measures it. A
// program that fails, by its exit code or a signal, gives an error.
func (m *Measurer) Run(stdout, stderr io.Writer, path string, args ...string) (Result, error) {
	program, err := filepath.Abs(path) // the measurer runs a path, not a name looked up on the PATH
	if err != nil {
		return Result{}, err
	}

	r, w, err := os.Pipe()
	if err != nil {
		return Result{}, err
	}
	defer r.Close()

	cmd := testproc.Command(m.path, append([]string{program}, args...)...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.ExtraFiles = []*os.File{w}
	err = cmd.Start()
	w.Close() // the measurer's copy is the only writer left, so the read ends when it does
	if err != nil {
		return Result{}, err
	}

	report, readErr := io.ReadAll(r)
	if err := cmd.Wait(); err != nil {
		return Result{}, fmt.Errorf("the measurer of %s: %v", path, err)
	}

	if readErr != nil {
		return Result{}, readErr
	}

	var took, rss int64
	var code int
	if _, err := fmt.Sscan(string(report), &took, &rss, &code); err != nil {
		return Result{}, fmt.Errorf("the measurer of %s reported %q: %v", path, report, err)
	}

	if runtime.GOOS != darwin {
		rss *= kilobyte
	}

	res := Result{Took: time.Duration(took), Peak: rss}
	switch {
	case code > 0:
		return res, fmt.Errorf("%s: exit status %d", path, code)
	case code < 0:
		return res, fmt.Errorf("%s: signal %d", path, -code)
	}

	return res, nil
}
