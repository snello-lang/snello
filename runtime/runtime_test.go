// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/snello-lang/snello/internal/testproc"
)

// probeTimeout bounds a probe whose failure is a hang, a lost wake,
// which the deadline turns into a failure; stacksTimeout bounds the
// debugger that reads a hung probe's threads.
const (
	probeTimeout  = 30 * time.Second
	stacksTimeout = time.Minute
)

// wakeRuns is how many processes TestWakeAfterWaitEnds runs its rounds
// in, each starting its workers anew, so that a race of the wake whose
// window a run seldom meets is met more often.
const wakeRuns = 5

// allocScalesLimit is how many times more four threads allocating may
// grow a processor's time than four threads listing grow theirs. Each
// thread's own allocations grow it as listing does, about once; one
// word every allocation writes, shared by the threads, ten times and
// more. Three lies between the two on a ratio's scale.
const allocScalesLimit = 3.0

// The debuggers a hung probe's threads are read with: cdb on Windows,
// from Windows' SDK, which installs it under its kit's directory for
// each processor, lldb on macOS and gdb elsewhere, each attached to
// the probe, printing every thread's stack and detaching.
const (
	cdbName         = "cdb"
	cdbStacks       = "~*k 40; qd"
	lldbName        = "lldb"
	lldbStacks      = "thread backtrace all"
	gdbName         = "gdb"
	gdbStacks       = "thread apply all bt 40"
	programFilesX86 = "ProgramFiles(x86)"
	darwinOS        = "darwin"
)

// windowsKitDebuggers is where Windows' SDK installs its debuggers,
// under the program files' directory, in one directory a processor.
var (
	windowsKitDebuggers = []string{"Windows Kits", "10", "Debuggers"}
	kitProcessors       = map[string]string{"amd64": "x64", "arm64": "arm64"}
)

// probeOpen opens a probe's code to preemption, as a backend opens a
// program's, and spinners is a probe's program after its includes: a
// task computing without a wait on every core, and the main task
// sleeping 100 ms five times and printing how many tasks were
// preempted, how many workers there are and how late its wakes were in
// all.
const (
	probeOpen = "SN_OPEN_CODE\n"
	spinners  = `static void spin(void *arg) {
    (void)arg;
    volatile int64_t x = 0;
    for (;;) {
        x++;
    }
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    for (int64_t i = 0; i < sn_cores(); i++) {
        sn_spawn(spin, NULL, 0, SN_UNBOUNDED);
    }

    int64_t late = 0;
    for (int i = 0; i < 5; i++) {
        int64_t start = sn_time_mono();
        sn_sleep(100000000);
        late += (sn_time_mono() - start) / 1000000 - 100;
    }

    printf("preempted %lld, workers %lld of %lld cores, %lld ms late\n", (long long)sn_sched_preempted(),
           (long long)sn_sched_workers(), (long long)sn_cores(), (long long)late);
    return 0;
}
`
)

// What a probe is built with where the system differs: the C compiler
// when CC names none, cc, and clang on Windows, which has no cc; and
// the name of its binary, with Windows' suffix there.
const (
	probeCC        = "cc"
	probeWindowsCC = "clang"
	probeBinary    = "probe"
	windowsSuffix  = ".exe"
	windowsOS      = "windows"
)

// windowsBinary is a source a probe is built with on Windows, where
// the C library opens the standard streams in text mode and writes a
// newline as two bytes: it makes them binary before main, as a Snello
// program's are, so that a probe's output reads as it does elsewhere.
const windowsBinary = `#include <fcntl.h>
#include <io.h>
#include <stdlib.h>
__attribute__((constructor)) static void probe_binary(void) {
    _set_fmode(_O_BINARY);
    for (int fd = 0; fd <= 2; fd++) {
        _setmode(fd, _O_BINARY);
    }
}
`

// probeHeader is probe.h, written beside every probe: what a probe
// needs of the system beyond the runtime, in one form for every system,
// POSIX's calls or Windows' under it: a thread it waits for, a pipe and
// its reads and writes, the processor time the process has used and a
// finer count of the processor's work, in units only a ratio of two
// reads, the cycles Windows counts where its processor time moves in
// ticks of 15.6 ms, an
// alarm that ends the probe, and a client's connection to a port on the
// loopback, its sends and its close. It follows internal.h, which
// includes the system's headers first, so a probe that needs more of
// them than POSIX's threads give, getaddrinfo's, defines the system's
// feature macros, _GNU_SOURCE and _DARWIN_C_SOURCE, before both.
const probeHeader = `#ifndef PROBE_H
#define PROBE_H
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#if defined(_WIN32)
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <fcntl.h>
#include <io.h>
#pragma comment(lib, "ws2_32")
typedef HANDLE probe_thread;
typedef struct {
    void *(*main)(void *);
    void *arg;
} probe_start_args;
static DWORD WINAPI probe_entry(void *p) {
    probe_start_args a = *(probe_start_args *)p;
    free(p);
    a.main(a.arg);
    return 0;
}
static inline probe_thread probe_thread_start(void *(*main)(void *), void *arg) {
    probe_start_args *a = malloc(sizeof *a);
    HANDLE t = NULL;
    if (a != NULL) {
        a->main = main;
        a->arg = arg;
        t = CreateThread(NULL, 0, probe_entry, a, 0, NULL);
    }

    if (t == NULL) {
        fputs("probe: cannot start a thread\n", stderr);
        exit(1);
    }

    return t;
}
static inline void probe_thread_join(probe_thread t) {
    WaitForSingleObject(t, INFINITE);
    CloseHandle(t);
}
static inline int probe_pipe(int fds[2]) { return _pipe(fds, 4096, _O_BINARY); }
static inline int probe_read(int fd, void *p, unsigned n) { return _read(fd, p, n); }
static inline int probe_write(int fd, const void *p, unsigned n) { return _write(fd, p, n); }
static inline int64_t probe_cpu_ns(void) {
    FILETIME created, exited, kernel, user;
    GetProcessTimes(GetCurrentProcess(), &created, &exited, &kernel, &user);
    uint64_t k = (uint64_t)kernel.dwHighDateTime << 32 | kernel.dwLowDateTime;
    uint64_t u = (uint64_t)user.dwHighDateTime << 32 | user.dwLowDateTime;
    return (int64_t)(k + u) * 100;
}
static inline int64_t probe_cpu_work(void) {
    ULONG64 cycles = 0;
    QueryProcessCycleTime(GetCurrentProcess(), &cycles);
    return (int64_t)cycles;
}
static DWORD WINAPI probe_alarm_entry(void *p) {
    Sleep((DWORD)(uintptr_t)p * 1000);
    TerminateProcess(GetCurrentProcess(), 14);
    return 0;
}
static inline void probe_alarm(unsigned seconds) { CloseHandle(CreateThread(NULL, 0, probe_alarm_entry, (void *)(uintptr_t)seconds, 0, NULL)); }
typedef SOCKET probe_socket;
#define PROBE_NO_SOCKET INVALID_SOCKET
static inline int probe_close_socket(probe_socket s) { return closesocket(s); }
#else
#include <arpa/inet.h>
#include <netinet/in.h>
#include <pthread.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>
typedef pthread_t probe_thread;
static inline probe_thread probe_thread_start(void *(*main)(void *), void *arg) {
    pthread_t t;
    if (pthread_create(&t, NULL, main, arg) != 0) {
        fputs("probe: cannot start a thread\n", stderr);
        exit(1);
    }

    return t;
}
static inline void probe_thread_join(probe_thread t) { pthread_join(t, NULL); }
static inline int probe_pipe(int fds[2]) { return pipe(fds); }
static inline int probe_read(int fd, void *p, unsigned n) { return (int)read(fd, p, n); }
static inline int probe_write(int fd, const void *p, unsigned n) { return (int)write(fd, p, n); }
static inline int64_t probe_cpu_ns(void) {
    struct timespec t;
    clock_gettime(CLOCK_PROCESS_CPUTIME_ID, &t);
    return (int64_t)t.tv_sec * 1000000000 + t.tv_nsec;
}
static inline int64_t probe_cpu_work(void) { return probe_cpu_ns(); }
static inline void probe_alarm(unsigned seconds) { alarm(seconds); }
typedef int probe_socket;
#define PROBE_NO_SOCKET (-1)
static inline int probe_close_socket(probe_socket s) { return close(s); }
#endif
// probe_connect_loopback connects a client to port on the loopback and
// returns its socket, or PROBE_NO_SOCKET.
static inline probe_socket probe_connect_loopback(int64_t port) {
    probe_socket s = socket(AF_INET, SOCK_STREAM, 0);
    if (s == PROBE_NO_SOCKET) {
        return s;
    }

    struct sockaddr_in to = {.sin_family = AF_INET, .sin_port = htons((uint16_t)port)};
    to.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    if (connect(s, (struct sockaddr *)&to, sizeof to) != 0) {
        probe_close_socket(s);
        return PROBE_NO_SOCKET;
    }

    return s;
}
static inline int probe_send(probe_socket s, const char *p, int n) { return (int)send(s, p, n, 0); }
#endif
`

// The options of a probe's compile and link: POSIX's threads and the
// mathematics library, or on Windows the C runtime's define, LLVM's
// linker and CodeView, the debugging information cdb reads a hung
// probe's threads by.
var (
	posixProbeFlags   = []string{"-pthread"}
	posixProbeLibs    = []string{"-lm"}
	windowsProbeFlags = []string{"-D_CRT_SECURE_NO_WARNINGS", "-fuse-ld=lld", "-g", "-gcodeview"}
)

// ucd is the copy of the Unicode Character Database the tables are
// written from, checked in so that the tests read it offline.
var ucd = filepath.Join("testdata", "ucd", "18.0.0")

// staticFunction matches the line that begins a static function's
// definition or declaration in the runtime's C, attributes and all,
// alloc.c's HELPER among them, and names the function.
var staticFunction = regexp.MustCompile(`(?m)^(?:SN_HELD_STATIC\s+)?(?:static\s+(?:inline\s+)?(?:__attribute__\(\([^)]*\)\)\s+)?(?:inline\s+)?(?:_Noreturn\s+)?|HELPER\s+)[A-Za-z_][\w \*]*?[ \*](\w+)\(`)

// TestUnicodeTables checks that c/unicode.c is what mkunicode.go writes
// from the database under testdata, so the tables cannot be edited by
// hand, and that it and unicode.Version name the database's version, so
// a move to the next one cannot leave Version behind.
func TestUnicodeTables(t *testing.T) {
	out := filepath.Join(t.TempDir(), "unicode.c")
	if b, err := exec.Command("go", "run", "mkunicode.go", "-ucd", ucd, "-o", out).CombinedOutput(); err != nil {
		t.Fatalf("mkunicode: %v\n%s", err, b)
	}

	want, _ := os.ReadFile(out)
	got, _ := os.ReadFile(filepath.Join("c", "unicode.c"))
	if !bytes.Equal(got, want) {
		t.Fatal("c/unicode.c is stale: run go run mkunicode.go in runtime/")
	}

	src, err := os.ReadFile(filepath.Join("..", "std", "unicode", "unicode.snello"))
	if err != nil {
		t.Fatal(err)
	}

	decl := `const Version string = "` + filepath.Base(ucd) + `"`
	if !bytes.Contains(src, []byte(decl)) || !bytes.Contains(got, []byte("Database\n// "+filepath.Base(ucd)+";")) {
		t.Fatalf("unicode.Version and c/unicode.c must name the tables' version, %s: std/unicode/unicode.snello needs %s", filepath.Base(ucd), decl)
	}
}

// A runeFacts is what the database says of one rune: its general
// category, its simple case mappings, and whether it is white space.
type runeFacts struct {
	category            string
	upper, lower, title rune
	fold                rune
	space               bool
	script              string
	decimal, numeric    string // the decimal digit value and the numeric value, as UnicodeData.txt writes them, else DerivedNumericValues.txt
}

// readUCD reads UnicodeData.txt, PropList.txt, CaseFolding.txt,
// Scripts.txt and DerivedNumericValues.txt apart from the generator, so
// that a mistake in its reading is not repeated here. A rune the data
// does not list has no category, and maps to itself.
func readUCD(t *testing.T) map[rune]*runeFacts {
	t.Helper()
	facts := map[rune]*runeFacts{}
	get := func(r rune) *runeFacts {
		if facts[r] == nil {
			facts[r] = &runeFacts{upper: r, lower: r, title: r, fold: r}
		}

		return facts[r]
	}

	code := func(s string) rune {
		n, err := strconv.ParseUint(strings.TrimSpace(s), 16, 32)
		if err != nil {
			t.Fatalf("bad code point %q", s)
		}

		return rune(n)
	}

	data, err := os.ReadFile(filepath.Join(ucd, "UnicodeData.txt"))
	if err != nil {
		t.Fatal(err)
	}

	first := rune(-1)
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ";")
		if len(f) < 15 {
			continue
		}

		r := code(f[0])
		switch {
		case strings.HasSuffix(f[1], ", First>"):
			first = r
		case strings.HasSuffix(f[1], ", Last>"):
			for c := first; c <= r; c++ {
				get(c).category = f[2]
			}
		default:
			get(r).category = f[2]
			get(r).decimal, get(r).numeric = f[6], f[8]
			if f[12] != "" {
				get(r).upper = code(f[12])
				get(r).title = code(f[12]) // a title mapping left empty is the upper case one
			}

			if f[13] != "" {
				get(r).lower = code(f[13])
			}

			if f[14] != "" {
				get(r).title = code(f[14])
			}
		}
	}

	props, err := os.ReadFile(filepath.Join(ucd, "PropList.txt"))
	if err != nil {
		t.Fatal(err)
	}

	for _, line := range strings.Split(string(props), "\n") {
		line, _, _ = strings.Cut(line, "#")
		codes, prop, ok := strings.Cut(line, ";")
		if !ok || strings.TrimSpace(prop) != "White_Space" {
			continue
		}

		lo, hi, isRange := strings.Cut(strings.TrimSpace(codes), "..")
		if !isRange {
			hi = lo
		}

		for c := code(lo); c <= code(hi); c++ {
			get(c).space = true
		}
	}

	scan := func(file string, each func(f []string)) {
		data, err := os.ReadFile(filepath.Join(ucd, file))
		if err != nil {
			t.Fatal(err)
		}

		for _, line := range strings.Split(string(data), "\n") {
			line, _, _ = strings.Cut(line, "#")
			f := strings.Split(line, ";")
			if len(f) < 2 {
				continue
			}

			for i := range f {
				f[i] = strings.TrimSpace(f[i])
			}

			each(f)
		}
	}

	scan("CaseFolding.txt", func(f []string) {
		if f[1] == "C" || f[1] == "S" {
			get(code(f[0])).fold = code(f[2])
		}
	})
	scan("Scripts.txt", func(f []string) {
		lo, hi, isRange := strings.Cut(f[0], "..")
		if !isRange {
			hi = lo
		}

		for c := code(lo); c <= code(hi); c++ {
			get(c).script = f[1]
		}
	})
	scan("DerivedNumericValues.txt", func(f []string) { // the ideographs' values from Unihan, which UnicodeData.txt leaves out
		lo, hi, isRange := strings.Cut(f[0], "..")
		if !isRange {
			hi = lo
		}

		for c := code(lo); c <= code(hi); c++ {
			if get(c).numeric == "" {
				get(c).numeric = f[3]
			}
		}
	})
	return facts
}

// TestUnicodeLookup compiles the tables with a program that asks them
// about every code point and compares each answer with the database
// under testdata, read by readUCD: the case of every rune either way,
// whether it is white space, printable, a letter, a decimal digit, an
// uppercase or a lowercase letter, its title case and simple folding,
// its general category, its script and the script's code, its decimal
// digit and numeric values, and the derived properties Alphabetic,
// Uppercase, Lowercase, XID_Start, XID_Continue and Emoji. The meaning
// of each is Unicode's.
func TestUnicodeLookup(t *testing.T) {
	cc := probeCompiler()

	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("no C compiler")
	}

	dir := t.TempDir()
	for name, data := range Files() {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	probe := `#include "internal.h"
#include <stdio.h>
int main(void) {
    for (int32_t r = 0; r <= 0x10FFFF; r++) {
        int32_t u = sn_rune_upper(r), l = sn_rune_lower(r), ti = sn_rune_title(r), fo = sn_rune_fold(r);
        bool s = sn_rune_space(r), p = sn_isprint(r);
        bool le = sn_rune_letter(r), d = sn_rune_digit(r), up = sn_rune_is_upper(r), lo = sn_rune_is_lower(r);
        const char *cat = sn_category_name(sn_rune_category(r)), *sc = sn_script_name(sn_rune_script(r)), *code = sn_script_code(sn_rune_script(r));
        int dv = sn_uc_decimal(r), der = sn_uc_derived(r);
        int64_t num = 0;
        int32_t den = 0;
        bool hasNum = sn_uc_numeric(r, &num, &den);
        if (u != r || l != r || ti != r || fo != r || s || !p || le || d || up || lo || cat[0] != 'C' || cat[1] != 'n' || sc[0] != 'U' || dv >= 0 || der != 0 || hasNum) {
            printf("%X %X %X %X %X %d %d %d %d %d %d %s %s %s %d %d", r, u, l, ti, fo, s, p, le, d, up, lo, cat, sc, code, dv, der);
            if (hasNum) {
                printf(" %lld/%d", (long long)num, den);
            }
            printf("\n");
        }
    }
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "probe.c"), []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := probePath(dir)
	if b, err := exec.Command(cc, append(append(append([]string{"-std=c11"}, systemProbeFlags()...), "-o", bin, filepath.Join(dir, "probe.c"), filepath.Join(dir, "unicode.c")), systemProbeSources(t, dir)...)...).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}

	got, err := testproc.Command(bin).Output()
	if err != nil {
		t.Fatal(err)
	}

	facts := readUCD(t)
	scriptCodes := map[string]string{}
	derived := map[string]map[rune]bool{}
	for _, file := range []string{"PropertyValueAliases.txt", "DerivedCoreProperties.txt", "emoji-data.txt"} {
		data, err := os.ReadFile(filepath.Join(ucd, file))
		if err != nil {
			t.Fatal(err)
		}

		for _, line := range strings.Split(string(data), "\n") {
			line, _, _ = strings.Cut(line, "#")
			f := strings.Split(line, ";")
			for i := range f {
				f[i] = strings.TrimSpace(f[i])
			}

			switch {
			case file == "PropertyValueAliases.txt" && len(f) >= 3 && f[0] == "sc":
				scriptCodes[f[2]] = f[1]
			case file != "PropertyValueAliases.txt" && len(f) == 2:
				if derived[f[1]] == nil {
					derived[f[1]] = map[rune]bool{}
				}

				lo, hi, isRange := strings.Cut(f[0], "..")
				if !isRange {
					hi = lo
				}

				for c := codes(t, lo)[0]; c <= codes(t, hi)[0]; c++ {
					derived[f[1]][c] = true
				}
			}
		}
	}

	var want bytes.Buffer
	for r := rune(0); r <= 0x10FFFF; r++ {
		f := facts[r]
		if f == nil {
			f = &runeFacts{upper: r, lower: r, title: r, fold: r}
		}

		major := ""
		if f.category != "" {
			major = f.category[:1]
		}

		u, l, ti, fo, s := f.upper, f.lower, f.title, f.fold, f.space
		p := r == ' ' || strings.Contains("LMNPS", major) && major != ""
		le, d, up, lo := major == "L", f.category == "Nd", f.category == "Lu", f.category == "Ll"
		cat, sc := f.category, f.script
		if cat == "" {
			cat = "Cn"
		}

		if sc == "" {
			sc = "Unknown"
		}

		code := scriptCodes[sc]
		dv := -1
		if f.decimal != "" {
			dv, _ = strconv.Atoi(f.decimal)
		}

		der := 0
		for i, prop := range []string{"Alphabetic", "Uppercase", "Lowercase", "XID_Start", "XID_Continue", "Emoji"} {
			if derived[prop][r] {
				der |= 1 << i
			}
		}

		if u != r || l != r || ti != r || fo != r || s || !p || le || d || up || lo || cat != "Cn" || sc != "Unknown" || dv >= 0 || der != 0 || f.numeric != "" {
			fmt.Fprintf(&want, "%X %X %X %X %X %d %d %d %d %d %d %s %s %s %d %d", r, u, l, ti, fo, bit(s), bit(p), bit(le), bit(d), bit(up), bit(lo), cat, sc, code, dv, der)
			if f.numeric != "" {
				num, den, frac := strings.Cut(f.numeric, "/")
				if !frac {
					den = "1"
				}

				fmt.Fprintf(&want, " %s/%s", num, den)
			}

			want.WriteString("\n")
		}
	}

	if !bytes.Equal(got, want.Bytes()) {
		g, w := bytes.Split(got, []byte("\n")), bytes.Split(want.Bytes(), []byte("\n"))
		for i := 0; i < len(g) && i < len(w); i++ {
			if !bytes.Equal(g[i], w[i]) {
				t.Fatalf("line %d: the tables say %q, the database %q", i+1, g[i], w[i])
			}
		}

		t.Fatalf("the tables give %d lines, the database %d", len(g), len(w))
	}
}

// TestRemoveAllDeep removes a tree four hundred directories deep
// through sn_os_removeAll, called from a spawned task, whose stack is
// 256 KB: the walk must keep no frame per level, since a tree can be
// deeper than any stack, and must work relative to a directory already
// open, since a path can be longer than the system opens in one call.
// The depth is what a kernel makes and removes in a second or two,
// Darwin's doing work that grows with the depth on every call under
// the tree: a walk with a stat buffer and a few words per level, some
// 220 bytes, would need about twelve hundred levels to run past the
// stack, and Darwin's path limit of 1024 bytes is passed at about five
// hundred, so this depth guards the shape of the walk, not its limits.
// The tree is made a level at a time through os.Root, which works
// relative to an open directory the same way.
func TestRemoveAllDeep(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "tree")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}

	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 400; i++ {
		if err := r.Mkdir("a", 0o755); err != nil {
			t.Fatal(err)
		}

		sub, err := r.OpenRoot("a")
		if err != nil {
			t.Fatal(err)
		}

		r.Close()
		r = sub
	}

	r.Close()
	probe := `#include "snello.h"
#include <stdio.h>
#include <string.h>
static sn_chan *done;
static sn_string path;
static void run(void *arg) {
    (void)arg;
    sn_string failed = SN_STR("", 0), op = SN_STR("", 0);
    int64_t code = sn_os_removeAll(path, &failed, &op);
    printf("%lld", (long long)code);
    if (code != 0) {
        printf(" %.*s %.*s", (int)op.len, (const char *)op.ptr, (int)failed.len, (const char *)failed.ptr);
    }

    printf("\n");
    sn_string_drop(&failed);
    sn_string_drop(&op);
    sn_chan_send(done, &code);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    path = SN_STR(argv[1], (int64_t)strlen(argv[1]));
    done = sn_chan_make(sizeof(int64_t), 1, NULL, "probe"); // buffered: the sender is done with it once its value is in
    sn_spawn(run, NULL, 0, SN_UNBOUNDED);
    int64_t code;
    sn_chan_recv(done, &code);
    while (sn_tasks_running() > 0) { // a task that has sent its last word may still be ending: the probe's
        sn_sleep(1000000);           // deadline, not the finish's grace, bounds a task that never ends
    }

    sn_chan_drop(&done, SN_CHAN_BOTH); // after the tasks, as a task's own handle would keep it

    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(dir, "build"), probe)
	out, err := testproc.Command(bin, root).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if string(out) != "0\n" {
		t.Fatalf("RemoveAll gave %q, want 0", out)
	}

	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("the tree is still there: %v", err)
	}
}

// TestStaticNamesUnique holds the runtime to one static function of a
// name across its files. The analysis of a task's stack reads the
// linked binary by function name, and reads two local functions of one
// name as one, the deeper of the two, so that a task calling the
// shallow one is bounded by the other: a lock's spin read as the
// scheduler's, which looks at the sockets' poller, gave every task that
// takes a lock a deeper stack than it uses, and a socket's preparation
// read as the scheduler's start left the net programs unbounded.
func TestStaticNamesUnique(t *testing.T) {
	defined := map[string]string{}
	for name, data := range Files() {
		if filepath.Ext(name) != ".c" {
			continue
		}

		seen := map[string]bool{}
		for _, m := range staticFunction.FindAllSubmatch(data, -1) {
			f := string(m[1])
			if seen[f] {
				continue // a declaration before the definition
			}

			seen[f] = true
			if other, ok := defined[f]; ok {
				t.Errorf("%s and %s both define a static function %s", other, name, f)
			}

			defined[f] = name
		}
	}

	if len(defined) == 0 {
		t.Fatal("no static function found: the pattern no longer reads the runtime")
	}
}

// TestHandoff holds a waker that wakes from no lock to the handoff's
// promise: a task it took out while the task was running, as a task out
// of a park a kept wake ended is, goes on only once the waker is done
// with it. The waker here, a thread of the test's, takes the task out of
// its word and waits 50 ms before it wakes it, as a waker the system
// descheduled would; a task that went on meanwhile would end, and the
// runtime free it, before the wake read it.
// TestSmallStacks spawns tasks of every class of stack, a bound given
// as the analysis of a linked program gives one, twice over, so that
// stacks pass between the workers' caches and the pools; panics on a
// small stack under a mark, twice, with a drop deeper than the stack's
// bound, which a debug build stops for unless the panic runs on a stack
// of its own; and, in a debug build, checks a task that writes deeper
// than its bound stops the program.
func TestSmallStacks(t *testing.T) {
	probe := `#include "snello.h"
#include <stdio.h>
enum { rounds = 2, tasks = 20000 };
static const uint32_t bounds[] = {2000, 4000, 8000, 16000, 32000, 64000, SN_UNBOUNDED};
static sn_chan *done;
static int dropped;
// fill writes every byte of a stack's region, each a volatile store
// that no optimizer leaves out.
static void fill(volatile uint8_t *p, size_t n, uint8_t v) {
    for (size_t i = 0; i < n; i++) {
        p[i] = v;
    }
}
// work writes a quarter of its bound into its stack, the rest left to
// what a task runs besides.
static void work(void *arg) {
    uint32_t b = *(uint32_t *)arg;
    size_t n = b == SN_UNBOUNDED ? 16384 : b / 4;
    volatile uint8_t *buf = __builtin_alloca(n);
    fill(buf, n, 1);
    int64_t one = buf[n - 1];
    sn_chan_send(done, &one);
}
// deep_drop is a drop with a frame deeper than the bound of the task
// that panics.
static void deep_drop(void *p) {
    (void)p;
    volatile uint8_t frame[6000];
    fill(frame, sizeof frame, 2);
    dropped += frame[0] - 1;
}
static void boom(void *ctx) {
    sn_guard(ctx, deep_drop);
    sn_panic(SN_STR("boom", 4));
}
static void catcher(void *arg) {
    (void)arg;
    for (int i = 0; i < 2; i++) {
        sn_string msg = SN_STR("", 0);
        int value;
        bool caught = sn_catch(boom, &value, &msg, false);
        printf("caught %d %.*s dropped %d\n", caught, (int)msg.len, (const char *)msg.ptr, dropped);
        sn_string_drop(&msg);
    }
    int64_t one = 1;
    sn_chan_send(done, &one);
}
static void too_deep(void *arg) {
    (void)arg;
    volatile uint8_t frame[1000];
    fill(frame, sizeof frame, 3);
    int64_t one = frame[0] - 2;
    sn_chan_send(done, &one);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    int n = (int)(sizeof bounds / sizeof bounds[0]);
    done = sn_chan_make(sizeof(int64_t), rounds * tasks + 1, NULL, "probe"); // buffered: no task parks to send
    if (argc > 1) {
        sn_spawn(too_deep, NULL, 0, 100);
        int64_t v;
        sn_chan_recv(done, &v);
        while (sn_tasks_running() > 0) { // a task that has sent its last word may still be ending: the probe's
            sn_sleep(1000000);           // deadline, not the finish's grace, bounds a task that never ends
        }

        sn_chan_drop(&done, SN_CHAN_BOTH); // after the tasks, as a task's own handle would keep it

        return sn_finish();
    }
    sn_spawn(catcher, NULL, 0, 4000);
    int64_t v, sum = 0;
    sn_chan_recv(done, &v);
    for (int r = 0; r < rounds; r++) {
        for (int i = 0; i < tasks; i++) {
            sn_spawn(work, &bounds[i % n], sizeof bounds[i % n], bounds[i % n]);
        }
        for (int i = 0; i < tasks; i++) {
            sn_chan_recv(done, &v);
            sum += v;
        }
    }
    printf("%lld\n", (long long)sum);
    while (sn_tasks_running() > 0) { // a task that has sent its last word may still be ending: the probe's
        sn_sleep(1000000);           // deadline, not the finish's grace, bounds a task that never ends
    }

    sn_chan_drop(&done, SN_CHAN_BOTH); // after the tasks, as a task's own handle would keep it

    return sn_finish();
}
`
	want := "caught 1 boom dropped 1\ncaught 1 boom dropped 2\n" + fmt.Sprint(2*20000) + "\n"
	for _, mode := range [][]string{{"-O1", "-DSN_DEBUG"}, {"-O2"}} {
		dir := filepath.Join(t.TempDir(), "build")
		bin := buildProbeAs(t, dir, probe, mode...)
		out, err := testproc.Command(bin).CombinedOutput()
		if err != nil || string(out) != want {
			t.Fatalf("%v: %v\n%s", mode, err, out)
		}
	}

	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := testproc.Command(bin, "deep").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 || !strings.Contains(string(out), "past the bound of 100 bytes") {
		t.Fatalf("a task past its bound in a debug build: %v\n%s", err, out)
	}
}

// TestPanicTrace walks the frames of a panic through the table a
// program carries: a function three calls deep in itself under main,
// each frame a line of the trace after the panic's own, innermost
// first, and no frame of the runtime or of what called main. The
// probe's table is written by hand over its own functions, in order of
// address as the compiler writes it, with the slide of zero a program
// not moved by the loader has, so that the runtime's walk and lookup
// are what the test holds, not the compiler's table.
func TestPanicTrace(t *testing.T) {
	probe := `#include "snello.h"
#include <string.h>
// The parts: two anchors, two rows, two frames, two symbols, one file, and the strings.
enum { anchors_at = 0, rows_at = 48, frames_at = 64, symbols_at = 96, files_at = 104, strings_at = 108, parts_room = 160 };
enum { function_room = 512 };
const sn_trace_table sn_trace = { (uintptr_t)&sn_trace, anchors_at, 2, rows_at, 2, frames_at, 2, symbols_at, 2, files_at, 1, strings_at };
uint8_t sn_trace_parts[parts_room] = { 0 }; // defined, not tentative, so that it replaces the runtime's own
const sn_trace_name sn_trace_names[] = { { "deep", "deep" }, { "main", "main" } };
const int64_t sn_trace_names_count = 2;
__attribute__((noinline)) static int deep(int n) {
    if (n > 0) {
        int r = deep(n - 1);
        __asm__ volatile("" ::: "memory");
        return r + 1;
    }

    sn_panic(SN_STR("boom", 4));
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    sn_trace_anchor of_deep = { (uintptr_t)deep, function_room, 0, 1 }, of_main = { (uintptr_t)main, function_room, 1, 1 };
    bool deep_first = of_deep.address < of_main.address;
    sn_trace_anchor anchors[2] = { deep_first ? of_deep : of_main, deep_first ? of_main : of_deep };
    sn_trace_row rows[2] = { { 0, 0 }, { 0, 1 } };
    sn_trace_frame frames[2] = { { 0, 0, 10, SN_TRACE_NONE }, { 1, 0, 20, SN_TRACE_NONE } };
    int32_t symbols[2] = { 0, 5 }, files[1] = { 10 };
    memcpy(sn_trace_parts + anchors_at, anchors, sizeof anchors);
    memcpy(sn_trace_parts + rows_at, rows, sizeof rows);
    memcpy(sn_trace_parts + frames_at, frames, sizeof frames);
    memcpy(sn_trace_parts + symbols_at, symbols, sizeof symbols);
    memcpy(sn_trace_parts + files_at, files, sizeof files);
    memcpy(sn_trace_parts + strings_at, "deep\0main\0probe.snello", 23);
    deep(3); // not returned, which would make the call a jump and leave main no frame
    return 0;
}
`
	bin := buildProbeAs(t, filepath.Join(t.TempDir(), "build"), probe, "-O1", "-DSN_DEBUG", "-fno-omit-frame-pointer")
	out, err := testproc.Command(bin).CombinedOutput()
	want := "panic: boom\n" + strings.Repeat("\tdeep at probe.snello:10\n", 4) + "\tmain at probe.snello:20\n"
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 || string(out) != want {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestCatchReport catches a panic under a mark that reports it: the
// panic's line is printed once per panic, before what the program
// prints next, and the count of contained panics grows by one each.
func TestCatchReport(t *testing.T) {
	probe := `#include "snello.h"
#include <stdio.h>
static sn_chan *done;
static void boom(void *ctx) {
    (void)ctx;
    sn_panic(SN_STR("boom", 4));
}
static void catcher(void *arg) {
    (void)arg;
    for (int i = 0; i < 2; i++) {
        sn_string msg = SN_STR("", 0);
        bool caught = sn_catch(boom, NULL, &msg, true);
        printf("caught %d %.*s contained %lld\n", caught, (int)msg.len, (const char *)msg.ptr, (long long)sn_panics_contained());
        sn_string_drop(&msg);
    }
    int64_t one = 1;
    sn_chan_send(done, &one);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    done = sn_chan_make(sizeof(int64_t), 1, NULL, "probe");
    sn_spawn(catcher, NULL, 0, SN_UNBOUNDED);
    int64_t v;
    sn_chan_recv(done, &v);
    while (sn_tasks_running() > 0) { // a task that has sent its last word may still be ending: the probe's
        sn_sleep(1000000);           // deadline, not the finish's grace, bounds a task that never ends
    }

    sn_chan_drop(&done, SN_CHAN_BOTH); // after the tasks, as a task's own handle would keep it

    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := testproc.Command(bin).CombinedOutput()
	want := "panic: boom\ncaught 1 boom contained 1\npanic: boom\ncaught 1 boom contained 2\n"
	if err != nil || string(out) != want {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestHandoff(t *testing.T) {
	probe := `#include "internal.h"
#include "probe.h"
#include <stdio.h>
static _Atomic uintptr_t word;
static sn_handoff woken;
static _Atomic int ended;
static int ended_at_wake = -1;
static sn_chan *done;
static void *waker(void *arg) {
    (void)arg;
    uintptr_t task;
    while ((task = atomic_exchange(&word, 0)) == 0) {
        sn_spin_hint();
    }

    sn_sleep_ns(50000000);
    ended_at_wake = atomic_load(&ended);
    sn_handoff_wake(&woken, (sn_task *)task);
    sn_outside(-1);
    return NULL;
}
static void run(void *arg) {
    (void)arg;
    sn_outside(1);
    atomic_store(&word, (uintptr_t)sn_self());
    while (atomic_load(&word) != 0) { // taken out while running, never parked
        sn_spin_hint();
    }

    sn_handoff_wait(&woken);
    atomic_store(&ended, 1);
    int64_t one = 1;
    sn_chan_send(done, &one);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    done = sn_chan_make(sizeof(int64_t), 1, NULL, "probe");
    probe_thread id = probe_thread_start(waker, NULL);
    sn_spawn(run, NULL, 0, SN_UNBOUNDED);
    int64_t got;
    sn_chan_recv(done, &got);
    probe_thread_join(id);
    printf("ended before the wake: %d\n", ended_at_wake);
    while (sn_tasks_running() > 0) { // a task that has sent its last word may still be ending: the probe's
        sn_sleep(1000000);           // deadline, not the finish's grace, bounds a task that never ends
    }

    sn_chan_drop(&done, SN_CHAN_BOTH); // after the tasks, as a task's own handle would keep it

    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := testproc.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if string(out) != "ended before the wake: 0\n" {
		t.Fatalf("got %q: the task went on before its waker was done with it", out)
	}
}

// TestOnce has sixteen threads call one setup at once, a setup slow
// enough that the others sleep on its word, and holds the setup to
// running once and every caller to returning only after it ran; a
// caller after the setup finds it done.
func TestOnce(t *testing.T) {
	probe := `#include "internal.h"
#include "probe.h"
#include <stdio.h>
enum { threads = 16, setup_ns = 20000000 };
static sn_once once = SN_ONCE_INIT;
static _Atomic int runs, early;
static void setup(void) {
    sn_sleep_ns(setup_ns);
    atomic_fetch_add(&runs, 1);
}
static void *call(void *arg) {
    (void)arg;
    sn_once_call(&once, setup);
    if (atomic_load(&runs) != 1) {
        atomic_fetch_add(&early, 1);
    }
    return NULL;
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    probe_thread ids[threads];
    for (int i = 0; i < threads; i++) {
        ids[i] = probe_thread_start(call, NULL);
    }

    for (int i = 0; i < threads; i++) {
        probe_thread_join(ids[i]);
    }

    sn_once_call(&once, setup);
    printf("runs %d, early %d\n", atomic_load(&runs), atomic_load(&early));
    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := runProbe(bin)
	if err != nil || string(out) != "runs 1, early 0\n" {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestQueueLockUnderContention has eight threads take and give one
// queue lock two hundred thousand times each, the holder yielding its
// processor now and then so that the contenders run out of spin and
// sleep on the lock's word, and holds the lock to a count that adds up;
// a sleeper left behind would be a lost wake, a thread that never
// returns, which the deadline on the run turns into a failure. A second
// run has two threads and a holder that sleeps while holding, so that
// every take goes through the sleep.
func TestQueueLockUnderContention(t *testing.T) {
	probe := `#include "internal.h"
#include "probe.h"
#include <stdio.h>
#include <stdlib.h>
static sn_qlock lock;
static int64_t count;
static int threads, rounds, holds;
static void *contend(void *arg) {
    (void)arg;
    for (int i = 0; i < rounds; i++) {
        sn_qlock_take(&lock);
        count++;
        if (i % 64 == 0) {
            if (holds > 0) {
                sn_sleep_ns((int64_t)holds * 1000); // held long enough for every contender to sleep
            } else {
                sn_yield();
            }
        }

        sn_qlock_give(&lock);
    }

    return NULL;
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    sn_sched_start(); // the workers, whose count lets a contender spin
    threads = atoi(argv[1]);
    rounds = atoi(argv[2]);
    holds = atoi(argv[3]);
    sn_qlock_init(&lock);
    probe_thread ids[64];
    for (int i = 0; i < threads; i++) {
        ids[i] = probe_thread_start(contend, NULL);
    }

    for (int i = 0; i < threads; i++) {
        probe_thread_join(ids[i]);
    }

    printf("count %lld\n", (long long)count);
    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	for _, c := range []struct {
		threads, rounds, holds int
	}{{8, 200000, 0}, {2, 20000, 1}} {
		out, err := runProbe(bin, strconv.Itoa(c.threads), strconv.Itoa(c.rounds), strconv.Itoa(c.holds))
		want := fmt.Sprintf("count %d\n", c.threads*c.rounds)
		if err != nil || string(out) != want {
			t.Fatalf("%d threads: %v\n%s", c.threads, err, out)
		}
	}
}

// TestWakeAfterWaitEnds has tasks leave a channel wait and end at once,
// many times over, while their wakers are still finishing the wake, in
// a debug build whose runtime stops the program when a wake reaches a
// task that has ended: each receiver carries a kept wake into its wait,
// so its park returns at once, into the few instructions of the waker's
// claim, and the wait's handoff keeps it there until the claim is done,
// so the stop never comes. The rounds run in wakeRuns processes. A
// receiver not ended ending_ns after the rounds lost its wake: the
// probe writes each task not ended with its state, as the debug
// build's list of its tasks holds them, read with the workers idle. A
// sender that lost its wake never sends its word, and leaves main in
// the rounds, which the probe's deadline ends with the threads' stacks.
func TestWakeAfterWaitEnds(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
#include <string.h>
enum { rounds = 20000 };
static const int64_t ending_ns = 10000000000; // after the rounds, a task not ended within it lost its wake
static sn_chan *ch, *done;
static void receiver(void *arg) {
    (void)arg;
    sn_wake(sn_self()); // a wake kept for its park: the park returns at once, into the waker's claim
    int64_t v;
    sn_chan_recv(ch, &v); // woken by the sender, and gone before the sender has queued it, when it can
}
static void sender(void *arg) {
    (void)arg;
    int64_t v = 1;
    sn_chan_send(ch, &v);
    int64_t one = 1;
    sn_chan_send(done, &one);
}
static uintptr_t word(uintptr_t at) { uintptr_t w; memcpy(&w, (const void *)at, sizeof w); return w; }
static const char *role(uintptr_t run) {
    if (run == (uintptr_t)receiver) {
        return "receiver";
    }

    if (run == (uintptr_t)sender) {
        return "sender";
    }

    return "other";
}
static const char *state_name(const sn_debugger_table *d, int state) {
    if (state == d->running) {
        return "running";
    }

    if (state == d->runnable) {
        return "runnable";
    }

    if (state == d->parking) {
        return "parking";
    }

    if (state == d->parked) {
        return "parked";
    }

    return "unknown";
}
// unended writes each task that has not ended with its state, from the debug build's list of its tasks, read
// with the workers idle: whether the receiver that lost its wake waits for one or was queued
static void unended(void) {
    const sn_debugger_table *d = &sn_debugger;
    for (uintptr_t task = word((uintptr_t)d->tasks); task != 0; task = word(task + (uintptr_t)d->next)) {
        int state;
        memcpy(&state, (const void *)(task + (uintptr_t)d->state), sizeof state);
        int without_wake = state & ~d->woken;
        if (without_wake == d->done || task == (uintptr_t)sn_self()) {
            continue;
        }

        const char *kept = (state & d->woken) != 0 ? ", with a wake kept" : "";
        printf("a task did not end: the %s, %s%s\n", role(word(task + (uintptr_t)d->run)), state_name(d, without_wake), kept);
    }
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    ch = sn_chan_make(sizeof(int64_t), 0, NULL, "probe");
    done = sn_chan_make(sizeof(int64_t), 1, NULL, "probe");
    for (int r = 0; r < rounds; r++) {
        if (r % 2 == 0) { // the receiver first, so that the sender wakes it, and the other way round
            sn_spawn(receiver, NULL, 0, SN_UNBOUNDED);
            sn_spawn(sender, NULL, 0, SN_UNBOUNDED);
        } else {
            sn_spawn(sender, NULL, 0, SN_UNBOUNDED);
            sn_spawn(receiver, NULL, 0, SN_UNBOUNDED);
        }

        int64_t got;
        sn_chan_recv(done, &got);
    }

    printf("rounds %d\n", rounds);
    int64_t ending_by = sn_time_mono() + ending_ns;
    while (sn_tasks_running() > 0) { // a task that has sent its last word may still be ending
        if (sn_time_mono() > ending_by) {
            unended();
            return 1;
        }

        sn_sleep(1000000);
    }

    sn_chan_drop(&ch, SN_CHAN_BOTH);
    sn_chan_drop(&done, SN_CHAN_BOTH); // after the tasks, as a task's own handle would keep it

    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	for run := range wakeRuns {
		out, err := runProbe(bin)
		if err != nil || string(out) != "rounds 20000\n" {
			t.Fatalf("run %d: %v\n%s", run, err, out)
		}
	}
}

// TestCancelRaces holds the scopes of scope.c to their races: a cancel
// of a group against a sender for the one wait of a member receiving,
// with both the member and the sender in the group, so that whichever
// loses the wait is woken by the cancel instead, and a group's Add
// against a member that ends at once, which the handle channel's word
// settles with one compare-and-swap; twenty thousand rounds of each,
// which must neither hang, crash nor leak.
func TestCancelRaces(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
enum { rounds = 20000 };
static sn_chan *quiet;
static int64_t taken, cut;
static void member(void *p) {
    sn_chan *h = *(sn_chan **)p;
    int64_t v;
    int64_t r = sn_chan_recv(quiet, &v) ? 2 : 1; // the value, or the cancel
    sn_chan_offer(h, &r);
    sn_chan_drop(&h, SN_CHAN_BOTH);
}
static void sender(void *p) {
    sn_chan *h = *(sn_chan **)p;
    int64_t v = 7;
    sn_chan_send(quiet, &v); // taken by the member, or dropped once the cancel reaches it
    int64_t r = 0;
    sn_chan_offer(h, &r);
    sn_chan_drop(&h, SN_CHAN_BOTH);
}
static void quick(void *p) {
    sn_chan *h = *(sn_chan **)p;
    int64_t r = 3;
    sn_chan_offer(h, &r);
    sn_chan_drop(&h, SN_CHAN_BOTH);
}
static int64_t join(sn_chan *h) {
    int64_t r;
    sn_chan_recv(h, &r);
    sn_chan_drop(&h, SN_CHAN_BOTH);
    return r;
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    quiet = sn_chan_make(sizeof(int64_t), 0, NULL, "probe");
    for (int r = 0; r < rounds; r++) {
        int64_t g = sn_task_newScope();
        sn_chan *m = sn_chan_handle(sizeof(int64_t), NULL, "probe");
        sn_chan *s = sn_chan_handle(sizeof(int64_t), NULL, "probe");
        sn_spawn_handle(member, &m, sizeof m, SN_UNBOUNDED, m);
        sn_task_adopt(g, m);
        sn_spawn_handle(sender, &s, sizeof s, SN_UNBOUNDED, s);
        sn_task_adopt(g, s);
        sn_task_cancelScope(g);
        if (join(m) == 2) {
            taken++;
        } else {
            cut++;
        }

        join(s);
        sn_task_dropScope(g);
    }

    for (int r = 0; r < rounds; r++) {
        int64_t g = sn_task_newScope();
        sn_chan *q = sn_chan_handle(sizeof(int64_t), NULL, "probe");
        sn_spawn_handle(quick, &q, sizeof q, SN_UNBOUNDED, q);
        sn_task_adopt(g, q);
        sn_task_cancelScope(g);
        if (join(q) != 3) {
            printf("a quick member lost its result\n");
        }

        sn_task_dropScope(g);
    }

    printf("rounds %d, %s\n", rounds, taken > 0 && cut > 0 ? "both ways" : "one way");
    while (sn_tasks_running() > 0) {
        sn_sleep(1000000);
    }

    sn_chan_drop(&quiet, SN_CHAN_BOTH);
    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := runProbe(bin)
	if err != nil || !strings.HasPrefix(string(out), "rounds 20000, ") {
		t.Fatalf("%v\n%s", err, out)
	}

	t.Log(strings.TrimSpace(string(out)))
}

// TestScopeDeadlineRaces holds the deadlines of scope.c to their races
// with the scopes' releases: a Within whose body returns as its deadline
// fires, so that the leave and the firing let the scope's holds go at
// once; an inner Within left while the outer's deadline cancels it; and
// a group cancelled from another task while its member enters and
// leaves Withins; thousands of rounds, which must neither hang nor
// crash, and leave no task running; in a debug build, which counts the
// scopes among the program's blocks, nor leave a scope unfreed.
func TestScopeDeadlineRaces(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
enum { rounds = 2000 };
static const int64_t hour = 3600000000000LL;
static sn_chan *done;
static void withins(void *arg) {
    (void)arg;
    for (int i = 0; i < 4; i++) {
        sn_task_enter(20000 + 7000 * i); // the body sleeps for about the deadline: either may come first
        sn_sleep(20000);
        sn_task_leave();
    }

    sn_task_enter(30000);
    sn_task_enter(hour); // the inner, left as the outer's deadline cancels both
    sn_sleep(30000);
    sn_task_leave();
    sn_task_leave();
    int64_t one = 1;
    sn_chan_send(done, &one);
}
static void member(void *p) {
    sn_chan *h = *(sn_chan **)p;
    for (int i = 0; i < 20; i++) {
        sn_task_enter(hour);
        sn_sleep(2000);
        sn_task_leave();
    }

    int64_t r = 0;
    sn_chan_offer(h, &r);
    sn_chan_drop(&h, SN_CHAN_BOTH);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    done = sn_chan_make(sizeof(int64_t), 1, NULL, "probe");
    for (int r = 0; r < rounds; r++) {
        sn_spawn(withins, NULL, 0, SN_UNBOUNDED);
        int64_t got;
        sn_chan_recv(done, &got);
    }

    for (int r = 0; r < rounds; r++) {
        int64_t g = sn_task_newScope();
        sn_chan *m = sn_chan_handle(sizeof(int64_t), NULL, "probe");
        sn_spawn_handle(member, &m, sizeof m, SN_UNBOUNDED, m);
        sn_task_adopt(g, m);
        sn_sleep(1000 * (r % 40));
        sn_task_cancelScope(g);
        int64_t got;
        sn_chan_recv(m, &got);
        sn_chan_drop(&m, SN_CHAN_BOTH);
        sn_task_dropScope(g);
    }

    printf("rounds %d\n", rounds);
    while (sn_tasks_running() > 0) {
        sn_sleep(1000000);
    }

    sn_chan_drop(&done, SN_CHAN_BOTH);
    return sn_finish();
}
`
	for _, mode := range [][]string{nil, {"-O1", "-DSN_DEBUG", "-fno-omit-frame-pointer"}} {
		bin := buildProbeAs(t, filepath.Join(t.TempDir(), "build"), probe, mode...)
		out, err := runProbe(bin)
		if err != nil || string(out) != "rounds 2000\n" {
			t.Fatalf("%v: %v\n%s", mode, err, out)
		}
	}
}

// TestDebuggerTable holds the table a debug build publishes for a
// debugger's script to the runtime it describes: a task parked on a
// channel is on the list, parked; the switch's caller resumes past the
// saved return address and below its own frame; and the frame pointer
// and return address its saved context holds at the published offsets
// lead, frame by frame, by the walk the script makes, to the task's
// own function.
func TestDebuggerTable(t *testing.T) {
	if runtime.GOOS == windowsOS {
		t.Skip("the walk is the one lldb's and gdb's script makes; on Windows cdb unwinds, since x86-64's frame pointers make no chain there")
	}

	probe := `#include "internal.h"
#include <stdio.h>
#include <string.h>
enum { reach = 256, deepest = 16, frame_align = 16 };
static sn_chan *ch;
static __attribute__((noinline)) void receiver(void *arg) {
    (void)arg;
    int64_t v;
    sn_chan_recv(ch, &v);
}
static uintptr_t word(uintptr_t at) { uintptr_t w; memcpy(&w, (const void *)at, sizeof w); return w; }
int main(int argc, char **argv) {
    sn_init(argc, argv);
    ch = sn_chan_make(sizeof(int64_t), 0, NULL, "probe");
    sn_spawn(receiver, NULL, 0, SN_UNBOUNDED);
    const sn_debugger_table *d = &sn_debugger;
    uintptr_t found = 0;
    for (int tries = 0; found == 0 && tries < 1000; tries++) {
        sn_sleep(1000000);
        for (uintptr_t task = word((uintptr_t)d->tasks); task != 0; task = word(task + (uintptr_t)d->next)) {
            int state;
            memcpy(&state, (const void *)(task + (uintptr_t)d->state), sizeof state);
            if (word(task + (uintptr_t)d->run) == (uintptr_t)receiver && (state & ~d->woken) == d->parked) {
                found = task;
            }
        }
    }

    if (found == 0) {
        printf("the receiver is not on the list, parked\n");
        return 1;
    }

    uintptr_t sp = word(found + (uintptr_t)d->sp), fp = word(sp + (uintptr_t)d->saved_fp), last = sp;
    if (d->resumed_sp <= d->saved_pc || fp < sp + (uintptr_t)d->resumed_sp) {
        printf("the switch's caller resumes at %d, not between the saved return address and its frame\n", (int)d->resumed_sp);
        return 1;
    }

    bool reached = false;
    for (int i = 0; i < deepest && fp != 0 && fp % frame_align == 0 && fp > last && fp - sp < (uintptr_t)d->stack_room; i++) {
        uintptr_t ret = word(fp + sizeof(uintptr_t));
        if (ret > (uintptr_t)receiver && ret - (uintptr_t)receiver < reach) {
            reached = true;
        }

        last = fp;
        fp = word(fp);
    }

    printf(reached ? "reached\n" : "not reached\n");
    int64_t one = 1;
    sn_chan_send(ch, &one);
    while (sn_tasks_running() > 0) {
        sn_sleep(1000000);
    }

    sn_chan_drop(&ch, SN_CHAN_BOTH); // after the tasks, as a task's own handle would keep it

    return sn_finish();
}
`
	bin := buildProbeAs(t, filepath.Join(t.TempDir(), "build"), probe, "-O1", "-DSN_DEBUG", "-fno-omit-frame-pointer") // as every build compiles the runtime, which x86-64's compilers otherwise drop the frame pointer from
	out, err := runProbe(bin)
	if err != nil || string(out) != "reached\n" {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestDebuggerListHeld holds a debug build's list of tasks to the
// records still held: once many more tasks have ended than the workers
// keep unfreed, the list a debugger's script walks holds at most the
// records the workers keep and the main task's, and each one listed is
// a task of the probe's that ended, since a record is unlinked before
// it is freed, and a debugger reads no freed record. The list is read
// with every task ended and every worker idle, so nothing changes it.
func TestDebuggerListHeld(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
#include <string.h>
enum { kept_most = 1024, spawned_per_kept = 4 }; // a worker keeps its last ended tasks unfreed: four times as many end
static void ends(void *arg) { (void)arg; }
static uintptr_t word(uintptr_t at) { uintptr_t w; memcpy(&w, (const void *)at, sizeof w); return w; }
// record_held reports whether the record at task is one a task of the probe's left as it ended
static bool record_held(const sn_debugger_table *d, uintptr_t task) {
    int state;
    memcpy(&state, (const void *)(task + (uintptr_t)d->state), sizeof state);
    return word(task + (uintptr_t)d->run) == (uintptr_t)ends && (state & ~d->woken) == d->done;
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    int64_t spawned = 0;
    do { // the first spawn starts the workers, whose count is read as they may grow
        sn_spawn(ends, NULL, 0, SN_UNBOUNDED);
        spawned++;
    } while (spawned < spawned_per_kept * kept_most * sn_sched_workers());

    while (sn_tasks_running() > 0) {
        sn_sleep(1000000);
    }

    atomic_thread_fence(memory_order_acquire); // the counts read, the list's last changes are seen
    const sn_debugger_table *d = &sn_debugger;
    int64_t listed = 0, most = kept_most * sn_sched_workers() + 1; // and the main task's
    for (uintptr_t task = word((uintptr_t)d->tasks); task != 0 && listed <= most; task = word(task + (uintptr_t)d->next)) {
        listed++;
        if (task != (uintptr_t)sn_self() && !record_held(d, task)) {
            printf("record %lld is no ended task's\n", (long long)listed);
            return 1;
        }
    }

    if (listed <= most) {
        printf("held\n");
    } else {
        printf("more than %lld listed\n", (long long)most);
    }

    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := runProbe(bin)
	if err != nil || string(out) != "held\n" {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestSlotTakenOnce has a task wake its partner into its worker's slot
// many times over, sometimes parking at once, so that the worker takes
// the slot itself, and sometimes computing a few microseconds first, so
// that an idle worker takes it: the partner counts its runs, and the
// count holds the slot to one take per wake, neither lost nor doubled,
// in a debug build whose runtime stops the program when a wake reaches
// a task that has ended.
func TestSlotTakenOnce(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
enum { rounds = 20000 };
static sn_chan *go, *go_in, *back, *back_out, *done; // a handle a task, as a program's tasks hold them
static _Atomic int64_t runs;
static void partner(void *arg) {
    (void)arg;
    int64_t v;
    while (sn_chan_recv(go_in, &v)) {
        atomic_fetch_add(&runs, 1);
        sn_chan_send(back_out, &v);
    }

    sn_chan_drop(&go_in, SN_CHAN_RECV);
    sn_chan_drop(&back_out, SN_CHAN_SEND);
    int64_t one = 1;
    sn_chan_send(done, &one);
}
static void waker(void *arg) {
    (void)arg;
    for (int64_t r = 0; r < rounds; r++) {
        sn_chan_send(go, &r); // wakes the partner into this worker's slot
        if (r % 2 == 0) {
            volatile int64_t spin = 0;
            while (spin < 4000) { // a few microseconds without parking: a thief takes the slot
                spin++;
            }
        }

        int64_t v;
        sn_chan_recv(back, &v); // parks: the slot's task runs here, unless a thief took it
    }

    sn_chan_drop(&go, SN_CHAN_SEND); // the partner's receive ends
    sn_chan_drop(&back, SN_CHAN_RECV);
    int64_t one = 1;
    sn_chan_send(done, &one);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    go = sn_chan_make(sizeof(int64_t), 0, NULL, "probe");
    go_in = sn_chan_clone(go, SN_CHAN_RECV);
    go = sn_chan_narrow(go, SN_CHAN_SEND);
    back = sn_chan_make(sizeof(int64_t), 0, NULL, "probe");
    back_out = sn_chan_clone(back, SN_CHAN_SEND);
    back = sn_chan_narrow(back, SN_CHAN_RECV);
    done = sn_chan_make(sizeof(int64_t), 1, NULL, "probe");
    sn_spawn(partner, NULL, 0, SN_UNBOUNDED);
    sn_spawn(waker, NULL, 0, SN_UNBOUNDED);
    int64_t got;
    sn_chan_recv(done, &got); // both tasks have said their last word, and end
    sn_chan_recv(done, &got);
    while (sn_tasks_running() > 0) { // a task that has sent its last word may still be ending: the probe's
        sn_sleep(1000000);           // deadline, not the finish's grace, bounds a task that never ends
    }

    printf("runs %lld\n", (long long)atomic_load(&runs));
    sn_chan_drop(&done, SN_CHAN_BOTH);
    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := runProbe(bin)
	if err != nil || string(out) != "runs 20000\n" {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestWorkerAddedWhenHeld spawns a task that computes without a wait on
// every core and has the main task sleep a hundred milliseconds: with
// every worker held, the monitor starts one more, which runs the main
// task when its sleep ends, within a few milliseconds of the hundred
// on a quiet machine and well within a second on a loaded one. Without
// it the main task never ran again.
func TestWorkerAddedWhenHeld(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
static void spin(void *arg) {
    (void)arg;
    volatile int64_t x = 0;
    for (;;) {
        x++;
    }
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    for (int64_t i = 0; i < sn_cores(); i++) {
        sn_spawn(spin, NULL, 0, SN_UNBOUNDED);
    }

    int64_t start = sn_time_mono();
    sn_sleep(100000000);
    printf("ran again after %lld ms\n", (long long)((sn_time_mono() - start) / 1000000));
    return 0;
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := runProbe(bin)
	var ms int
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if _, err := fmt.Sscanf(string(out), "ran again after %d ms", &ms); err != nil || ms < 100 || ms > 1000 {
		t.Fatalf("the main task ran again after %d ms, wanted a hundred and a few, a hundred and fifteen on a quiet machine: %s", ms, out)
	}
}

// TestWorkersBlockedInRead has a task on every core block in a read of
// a pipe that nothing writes to, as a bound function that blocks holds
// its worker: the main task's wake from its sleep is what the monitor
// adds a worker for, and the task it then spawns follows it on that
// worker, before the main task writes to the pipe and lets the readers
// go.
func TestWorkersBlockedInRead(t *testing.T) {
	probe := `#include "internal.h"
#include "probe.h"
#include <stdio.h>
static int fds[2];
static _Atomic int64_t ran_at;
static void reader(void *arg) {
    (void)arg;
    char b;
    (void)probe_read(fds[0], &b, 1); // blocks in the system, holding its worker
}
static void flag(void *arg) {
    (void)arg;
    atomic_store(&ran_at, sn_time_mono());
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    if (probe_pipe(fds) != 0) {
        return 1;
    }

    int64_t n = sn_cores();
    for (int64_t i = 0; i < n; i++) {
        sn_spawn(reader, NULL, 0, SN_UNBOUNDED);
    }

    sn_sleep(30000000); // every reader is in its read by now
    int64_t start = sn_time_mono();
    sn_spawn(flag, NULL, 0, SN_UNBOUNDED);
    while (atomic_load(&ran_at) == 0 && sn_time_mono() - start < 5000000000) {
        sn_sleep(1000000);
    }

    int64_t at = atomic_load(&ran_at);
    for (int64_t i = 0; i < n; i++) {
        (void)probe_write(fds[1], "x", 1); // the readers go
    }

    printf("ran after %lld ms\n", at == 0 ? -1LL : (long long)((at - start) / 1000000));
    return 0;
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := runProbe(bin)
	var ms int
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if _, err := fmt.Sscanf(string(out), "ran after %d ms", &ms); err != nil || ms < 0 || ms > 60 {
		t.Fatalf("the task ran after %d ms with every worker blocked in a read, wanted within a few tens: %s", ms, out)
	}
}

// heldFunction matches a function of the runtime marked SN_HELD or
// SN_HELD_STATIC, which the scheduler never preempts a task inside:
// its name, for TestHeldSection to find in the binary.
var heldFunction = regexp.MustCompile(`(?m)^SN_HELD(?:_STATIC)?\s+(?:static\s+)?(?:_Noreturn\s+)?[A-Za-z_][\w \*]*?[ \*](\w+)\(`)

// TestHeldSection builds a probe as a release build is built, with and
// without link-time optimization, and reads its symbols: every
// function the runtime marks held that the compiler kept as a function
// of its own lies in the held section, and so does the switch, which
// the assembly puts there, so that none is in the code section, the
// only code a task is preempted in (sched.c, preempt_safe); and neither
// is a function of the runtime that is not held, kept a function of its
// own, nor the probe's own main, which opens nothing: the runtime as it
// is compiled is closed, every function of it. A compiler that cloned
// or moved one into the code section would silently break the
// preemption.
func TestHeldSection(t *testing.T) {
	var nm []string
	var held, open string
	switch runtime.GOOS {
	case darwinOS:
		nm, held, open = []string{"nm", "-m"}, "(__TEXT,__sn_held)", "(__TEXT,__sn_code)"
	case "linux":
		nm, held, open = []string{"nm", "-S", "--format=sysv"}, ".sn_held", ".sn_code"
	default:
		t.Skip("no symbol listing with sections here")
	}

	if _, err := exec.LookPath(nm[0]); err != nil {
		t.Skip("no nm")
	}

	marked := map[string]bool{"sn_switch": true}
	for name, data := range Files() {
		if filepath.Ext(name) != ".c" {
			continue
		}

		for _, m := range heldFunction.FindAllSubmatch(data, -1) {
			marked[string(m[1])] = true
		}
	}

	if len(marked) < 100 {
		t.Fatalf("only %d functions marked held: the pattern no longer reads the runtime", len(marked))
	}

	probe := `#include "internal.h"
// A function of the runtime's that is not held, called through a
// pointer so that it stays a function of its own under every
// optimization.
static int64_t (*volatile index_of)(sn_string, sn_string) = sn_string_index;
int main(int argc, char **argv) {
    sn_init(argc, argv);
    return (int)index_of(SN_STR("ab", 2), SN_STR("b", 1)) - 1;
}
`
	for _, mode := range [][]string{{"-O2"}, {"-O2", "-flto"}} {
		bin := buildProbeAs(t, filepath.Join(t.TempDir(), "build"), probe, mode...)
		out, err := exec.Command(nm[0], append(nm[1:], bin)...).Output()
		if err != nil {
			t.Fatalf("%v: %v", nm, err)
		}

		found, outside, plain := 0, []string{}, ""
		for _, line := range strings.Split(string(out), "\n") {
			name, section := symbolLine(line)
			base := name
			if runtime.GOOS == darwinOS {
				base = strings.TrimPrefix(name, "_") // what macOS puts before every C symbol; Linux's _start and _init are their own
			}

			if i := strings.IndexByte(base, '.'); i > 0 { // a clone the compiler made, name.constprop.0, is the function's
				base = base[:i]
			}

			switch {
			case base == "sn_string_index":
				plain = section
			case base == "main" && section == open:
				t.Errorf("%v: the probe's main, which opens nothing, is in the code section", mode)
			}

			if !marked[base] || !codeSection(section) {
				continue
			}

			found++
			if section != held {
				outside = append(outside, name+" in "+section)
			}
		}

		if found < 20 { // a release build keeps the functions the probe reaches, and no other
			t.Fatalf("%v: only %d held functions found among the symbols: the listing changed shape\n%s", mode, found, out)
		}

		if len(outside) > 0 {
			t.Errorf("%v: %d held functions outside the held section:\n%s", mode, len(outside), strings.Join(outside, "\n"))
		}

		if plain == "" || plain == open { // the runtime is closed: a package's C may call it with a lock of its own held
			t.Errorf("%v: sn_string_index is in %q, want a section that is not the code section, %s", mode, plain, open)
		}
	}
}

// codeSection reports whether a section of nm's listing holds code:
// macOS's sections of the __TEXT segment but its constants, and any
// section on Linux, where symbolLine names only functions.
func codeSection(section string) bool {
	if strings.HasPrefix(section, "(__TEXT,") {
		return section != "(__TEXT,__const)"
	}

	return section != "" && !strings.HasPrefix(section, "(")
}

// symbolLine reads a function's name and section from a line of nm's
// listing: macOS's "addr (seg,sect) external _name", Linux's sysv
// form "name |addr| T | FUNC|size| |section"; other lines give none.
func symbolLine(line string) (name, section string) {
	if strings.Contains(line, "|") {
		fields := strings.Split(line, "|")
		if len(fields) == 7 && strings.TrimSpace(fields[3]) == "FUNC" {
			return strings.TrimSpace(fields[0]), strings.TrimSpace(fields[6])
		}

		return "", ""
	}

	fields := strings.Fields(line)
	if len(fields) >= 4 && strings.HasPrefix(fields[1], "(") {
		return fields[len(fields)-1], fields[1]
	}

	return "", ""
}

// TestPreempted spawns a task that computes without a wait on every
// core and has the main task sleep 100 ms five times: each wake finds
// every worker held, and the monitor takes a spinner off its worker
// for the main task rather than starting a thread, so the main task
// runs again within a few tens of milliseconds of each sleep's end,
// within a second in all on a loaded machine, with the workers as many
// as the cores; at most one more, where three signals in a row found
// no point to leave at, as on a thread the system left unrun on a
// loaded runner, and the monitor started a thread as before.
func TestPreempted(t *testing.T) {
	if !opensCode() {
		t.Skip("the probes' compiler cannot open code to preemption")
	}

	probe := `#include "internal.h"
#include <stdio.h>
` + probeOpen + spinners
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := runProbe(bin)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	var preempted, workers, cores, late int
	if _, err := fmt.Sscanf(string(out), "preempted %d, workers %d of %d cores, %d ms late", &preempted, &workers, &cores, &late); err != nil {
		t.Fatalf("unexpected output: %s", out)
	}

	if preempted < 5 || workers > cores+1 || late > 1000 {
		t.Fatalf("with a spinner on every core: %s, want five or more preemptions, at most one worker added, and the main task's five wakes within a second of their time in all", out)
	}
}

// TestSpawnBacklogBounded has the main task spawn tasks far faster than
// the other workers can take them, since each is held by a task
// computing without a wait: a spawn that overflows its ring into an
// inject queue already holding a full ring per worker has the spawning
// task step aside, so the tasks queued at any moment stay within two
// full rings per worker, the overflow that passed the mark and the
// tasks displaced, and so does the memory their records and stacks
// hold, whatever the machine's load. Every spawned task still runs. It
// spawns four times the most the bound can reach, the workers grown to
// the sixteen a core the monitor may start, so that a spawner left to
// overflow as it likes passes the bound on any machine.
func TestSpawnBacklogBounded(t *testing.T) {
	if !opensCode() {
		t.Skip("the probes' compiler cannot open code to preemption")
	}

	probe := `#include "internal.h"
#include <stdio.h>
#include <stdatomic.h>
` + probeOpen + `enum { ring = 256, workers_per_core = 16 };
static _Atomic int64_t ran, stop;
static void spin(void *arg) {
    (void)arg;
    while (!atomic_load(&stop)) {
    }
}
static void count(void *arg) {
    (void)arg;
    atomic_fetch_add(&ran, 1);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    for (int64_t i = 1; i < sn_cores(); i++) {
        sn_spawn(spin, NULL, 0, SN_UNBOUNDED);
    }

    int64_t spawns = 4 * (2 * workers_per_core * sn_cores() * ring + ring + 2 * workers_per_core * sn_cores() + 1);
    int64_t most = 0;
    for (int64_t i = 0; i < spawns; i++) {
        sn_spawn(count, NULL, 0, SN_UNBOUNDED);
        int64_t queued = sn_sched_queued();
        most = queued > most ? queued : most;
    }

    while (atomic_load(&ran) < spawns) {
        sn_sleep(1000000);
    }

    atomic_store(&stop, 1);
    int64_t workers = sn_sched_workers();
    printf("queued at most %lld, bound %lld, ran %lld of %lld\n", (long long)most,
           (long long)(2 * workers * ring + ring + 2 * workers + 1), (long long)atomic_load(&ran), (long long)spawns);
    return 0;
}
`
	out, err := runProbe(buildProbeAs(t, filepath.Join(t.TempDir(), "build"), probe, "-O2"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	var most, bound, ran, spawns int
	if _, err := fmt.Sscanf(string(out), "queued at most %d, bound %d, ran %d of %d", &most, &bound, &ran, &spawns); err != nil {
		t.Fatalf("unexpected output: %s", out)
	}

	if most > bound || ran != spawns {
		t.Fatalf("spawning past busy workers: %s, want the queued tasks within two full rings per worker, an overflow, the workers' displaced tasks and the spawner, and every task run", out)
	}
}

// TestPreemptedOnlyInOpenCode has a task on every core compute without
// a wait in code that is not open to preemption, as the C side of a
// package is, and the C library a program links in: none is ever
// interrupted, whatever its thread holds, and the main task still runs
// again, on a thread the monitor starts beside the held workers, as it
// did before a task could be preempted.
func TestPreemptedOnlyInOpenCode(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
` + spinners
	out, err := runProbe(buildProbe(t, filepath.Join(t.TempDir(), "build"), probe))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	var preempted, workers, cores, late int
	if _, err := fmt.Sscanf(string(out), "preempted %d, workers %d of %d cores, %d ms late", &preempted, &workers, &cores, &late); err != nil {
		t.Fatalf("unexpected output: %s", out)
	}

	if preempted != 0 || workers <= cores || late > 1000 {
		t.Fatalf("with a spinner in closed code on every core: %s, want no preemption, a worker added, and the main task's five wakes within a second of their time in all", out)
	}
}

// TestPreemptedKeepsRegisters has a task on every core compute a
// checksum through many live values, integer and floating-point, for
// about a second while the main task wakes every 10 ms, so that each
// is taken off its worker again and again and resumed on another with
// its registers put back: every checksum equals the one the main task
// computes alone afterwards, and the tasks were preempted many times.
func TestPreemptedKeepsRegisters(t *testing.T) {
	if !opensCode() {
		t.Skip("the probes' compiler cannot open code to preemption")
	}

	probe := `#include "internal.h"
#include <stdio.h>
#include <stdatomic.h>
SN_OPEN_CODE
enum { steps = 200000000, most = 64 };
static _Atomic int64_t done;
static uint64_t sums[most];
static double floats[most];
// checksum keeps eight floating-point and four integer accumulators
// live through every step, more than the callee-saved registers of
// either processor, so that a preemption that lost any register, or
// the vector unit's state, changes the result.
__attribute__((noinline)) static void checksum(int64_t seed, uint64_t *sum, double *f) {
    uint64_t a = (uint64_t)seed, b = a * 3 + 1, c = b * 5 + 7, d = c * 7 + 11;
    double f0 = seed, f1 = 1.5, f2 = 2.25, f3 = 3.125, f4 = 0.5, f5 = 0.75, f6 = 1.0625, f7 = 1.03125;
    for (int64_t i = 0; i < steps; i++) {
        a = a * 6364136223846793005ULL + 1442695040888963407ULL;
        b ^= a >> 17;
        c += b | 1;
        d = (d << 3) ^ c;
        f0 = f0 * 1.0000001 + 0.5;
        f1 = f1 + f0 * 1e-9;
        f2 = f2 * 0.9999999 + f1 * 1e-9;
        f3 = f3 + (f2 - f1) * 1e-12;
        f4 = f4 * 1.0000002 - f3 * 1e-9;
        f5 = f5 + f4 * 1e-10;
        f6 = f6 * 0.9999998 + f5 * 1e-11;
        f7 = f7 + f6 * 1e-12;
    }

    *sum = a ^ b ^ c ^ d;
    *f = f0 + f1 + f2 + f3 + f4 + f5 + f6 + f7;
}
static void compute(void *arg) {
    int64_t i = *(int64_t *)arg;
    checksum(i, &sums[i], &floats[i]);
    atomic_fetch_add(&done, 1);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    int64_t n = sn_cores() < most ? sn_cores() : most;
    for (int64_t i = 0; i < n; i++) {
        sn_spawn(compute, &i, sizeof i, SN_UNBOUNDED);
    }

    while (atomic_load(&done) < n) {
        sn_sleep(10000000); // every wake finds the workers held and has one of the tasks moved
    }

    int64_t wrong = 0;
    for (int64_t i = 0; i < n; i++) {
        uint64_t sum;
        double f;
        checksum(i, &sum, &f);
        if (sum != sums[i] || f != floats[i]) {
            wrong++;
        }
    }

    printf("%lld wrong of %lld, preempted %lld\n", (long long)wrong, (long long)n, (long long)sn_sched_preempted());
    return 0;
}
`
	bin := buildProbeAs(t, filepath.Join(t.TempDir(), "build"), probe, "-O2")
	out, err := runProbe(bin)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	var wrong, n, preempted int
	if _, err := fmt.Sscanf(string(out), "%d wrong of %d, preempted %d", &wrong, &n, &preempted); err != nil {
		t.Fatalf("unexpected output: %s", out)
	}

	if wrong != 0 || preempted < 10 {
		t.Fatalf("checksums across preemptions: %s, want none wrong and many preemptions", out)
	}
}

// TestPreemptedKeepsVectorRegisters has a task on every core load
// every vector register with a pattern of its own, at the widest form
// the system keeps, spin without touching one, and store them, while
// the main task wakes every 10 ms so that each is taken off its worker
// in the spin and resumed on another: every register comes back whole.
// Where the widest are the 512-bit registers, every other task holds
// the 256-bit ones, so that on macOS, which gives a thread the 512-bit
// state at its first use of it, a task is saved and resumed on threads
// of both forms. A program built for a processor that has the wide
// registers, or a function written with them, holds values there, and
// a preemption that kept only their low halves would resume the task
// with a stale half in each.
func TestPreemptedKeepsVectorRegisters(t *testing.T) {
	if !opensCode() {
		t.Skip("the probes' compiler cannot open code to preemption")
	}

	probe := `#include "internal.h"
#include <stdio.h>
#include <string.h>
#include <stdatomic.h>
#if defined(__APPLE__)
#include <sys/sysctl.h>
#endif
SN_OPEN_CODE
enum { holds = 20, spins = 100000000, most = 64, room = 32 * 64 + 16 };
static _Atomic int64_t done, wrong;
// hold loads every vector register from want, spins n times, and
// stores them all to got.
static void (*hold)(uint64_t n, const uint8_t *want, uint8_t *got);
static size_t held;       // the bytes the registers hold in all
static const char *width; // of the widest registers the system keeps
// beside, what every other task holds: the registers one size down where
// the widest are the 512-bit ones, the widest elsewhere. On macOS a
// thread that has run only such tasks keeps the AVX form, so tasks move
// between threads of both forms.
static void (*hold_beside)(uint64_t n, const uint8_t *want, uint8_t *got);
static size_t held_beside;
#define ALL16(M) M(0) M(1) M(2) M(3) M(4) M(5) M(6) M(7) M(8) M(9) M(10) M(11) M(12) M(13) M(14) M(15)
#define ALL32(M) ALL16(M) M(16) M(17) M(18) M(19) M(20) M(21) M(22) M(23) M(24) M(25) M(26) M(27) M(28) M(29) M(30) M(31)
#if defined(__x86_64__)
#define SPIN "1:\n subq $1, %[n]\n jnz 1b\n"
#define LOW16 "xmm0", "xmm1", "xmm2", "xmm3", "xmm4", "xmm5", "xmm6", "xmm7", "xmm8", "xmm9", "xmm10", "xmm11", "xmm12", "xmm13", "xmm14", "xmm15"
#define HIGH16 "xmm16", "xmm17", "xmm18", "xmm19", "xmm20", "xmm21", "xmm22", "xmm23", "xmm24", "xmm25", "xmm26", "xmm27", "xmm28", "xmm29", "xmm30", "xmm31"
#define XL(i) "movdqu " #i "*16(%[w]), %%xmm" #i "\n"
#define XS(i) "movdqu %%xmm" #i ", " #i "*16(%[g])\n"
#define YL(i) "vmovdqu " #i "*32(%[w]), %%ymm" #i "\n"
#define YS(i) "vmovdqu %%ymm" #i ", " #i "*32(%[g])\n"
#define ZL(i) "vmovdqu64 " #i "*64(%[w]), %%zmm" #i "\n"
#define ZS(i) "vmovdqu64 %%zmm" #i ", " #i "*64(%[g])\n"
#define KL(i) "kmovw 2046+" #i "*2(%[w]), %%k" #i "\n"
#define KS(i) "kmovw %%k" #i ", 2046+" #i "*2(%[g])\n"
#define MASKS(M) M(1) M(2) M(3) M(4) M(5) M(6) M(7)
__attribute__((noinline)) static void hold_128(uint64_t n, const uint8_t *w, uint8_t *g) {
    __asm__ volatile(ALL16(XL) SPIN ALL16(XS) : [n] "+r"(n) : [w] "r"(w), [g] "r"(g) : "cc", "memory", LOW16);
}
__attribute__((noinline, target("avx"))) static void hold_256(uint64_t n, const uint8_t *w, uint8_t *g) {
    __asm__ volatile(ALL16(YL) SPIN ALL16(YS) "vzeroupper\n"
                     : [n] "+r"(n) : [w] "r"(w), [g] "r"(g) : "cc", "memory", LOW16);
}
// The 512-bit registers, all thirty-two, and the seven mask registers
// an instruction may name, after them in the buffers.
__attribute__((noinline, target("avx512f"))) static void hold_512(uint64_t n, const uint8_t *w, uint8_t *g) {
    __asm__ volatile(ALL32(ZL) MASKS(KL) SPIN ALL32(ZS) MASKS(KS) "vzeroupper\n"
                     : [n] "+r"(n) : [w] "r"(w), [g] "r"(g)
                     : "cc", "memory", LOW16, HIGH16, "k1", "k2", "k3", "k4", "k5", "k6", "k7");
}
static void cpuid(uint32_t leaf, uint32_t sub, uint32_t r[4]) {
    __asm__ volatile("cpuid" : "=a"(r[0]), "=b"(r[1]), "=c"(r[2]), "=d"(r[3]) : "a"(leaf), "c"(sub));
}
// kept is the vector state the system saves for a thread, the
// processor's own word for it.
static uint64_t kept(void) {
    uint32_t low, high;
    __asm__ volatile("xgetbv" : "=a"(low), "=d"(high) : "c"(0));
    return ((uint64_t)high << 32) | low;
}
// wide512 reports whether the processor has the 512-bit registers and
// the system keeps them. macOS gives a thread their state at its first
// use of one, so a thread's own word shows it only after: the system
// says it, hw.optional.avx512f.
static bool wide512(void) {
#if defined(__APPLE__)
    int has = 0;
    size_t size = sizeof has;
    return sysctlbyname("hw.optional.avx512f", &has, &size, NULL, 0) == 0 && has != 0;
#else
    uint32_t r[4];
    cpuid(7, 0, r);
    return (r[1] >> 16 & 1) != 0 && (kept() & 0xe6) == 0xe6;
#endif
}
// choose takes the widest registers the processor has and the system
// keeps.
static void choose(void) {
    uint32_t r[4];
    hold = hold_beside = hold_128, held = held_beside = 16 * 16, width = "128-bit";
    cpuid(1, 0, r);
    if ((r[2] >> 27 & 1) == 0 || (r[2] >> 28 & 1) == 0 || (kept() & 0x6) != 0x6) {
        return;
    }

    hold = hold_beside = hold_256, held = held_beside = 16 * 32, width = "256-bit";
    if (wide512()) {
        hold = hold_512, held = 32 * 64 + 7 * 2, width = "512-bit";
    }
}
#else
#define VL(i) "ldr q" #i ", [%[w], #" #i "*16]\n"
#define VS(i) "str q" #i ", [%[g], #" #i "*16]\n"
__attribute__((noinline)) static void hold_128(uint64_t n, const uint8_t *w, uint8_t *g) {
    __asm__ volatile(ALL32(VL) "1:\n subs %[n], %[n], #1\n b.ne 1b\n" ALL32(VS)
                     : [n] "+r"(n) : [w] "r"(w), [g] "r"(g)
                     : "cc", "memory", "v0", "v1", "v2", "v3", "v4", "v5", "v6", "v7", "v8", "v9", "v10", "v11",
                       "v12", "v13", "v14", "v15", "v16", "v17", "v18", "v19", "v20", "v21", "v22", "v23", "v24",
                       "v25", "v26", "v27", "v28", "v29", "v30", "v31");
}
static void choose(void) { hold = hold_beside = hold_128, held = held_beside = 32 * 16, width = "128-bit"; }
#endif
static void keep(void *arg) {
    int64_t id = *(int64_t *)arg;
    uint8_t want[room], got[room];
    for (size_t i = 0; i < sizeof want; i++) {
        want[i] = (uint8_t)(i * 7 + (size_t)id + 1); // a pattern of its own for each register and each task
    }

    void (*mine)(uint64_t, const uint8_t *, uint8_t *) = id % 2 == 0 ? hold : hold_beside;
    size_t bytes = id % 2 == 0 ? held : held_beside;
    for (int h = 0; h < holds; h++) {
        memset(got, 0, sizeof got);
        mine(spins, want, got);
        if (memcmp(want, got, bytes) != 0) {
            atomic_fetch_add(&wrong, 1);
        }
    }

    atomic_fetch_add(&done, 1);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    choose();
    int64_t n = sn_cores() < most ? sn_cores() : most;
    for (int64_t i = 0; i < n; i++) {
        sn_spawn(keep, &i, sizeof i, SN_UNBOUNDED);
    }

    while (atomic_load(&done) < n) {
        sn_sleep(10000000); // every wake finds the workers held and has one of the tasks moved
    }

    printf("%lld wrong of %lld holds, preempted %lld, %s registers\n", (long long)atomic_load(&wrong),
           (long long)(n * holds), (long long)sn_sched_preempted(), width);
    return 0;
}
`
	bin := buildProbeAs(t, filepath.Join(t.TempDir(), "build"), probe, "-O2")
	out, err := runProbe(bin)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	var wrong, holds, preempted int
	var width string
	if _, err := fmt.Sscanf(string(out), "%d wrong of %d holds, preempted %d, %s registers", &wrong, &holds, &preempted, &width); err != nil {
		t.Fatalf("unexpected output: %s", out)
	}

	if wrong != 0 || preempted < 10 {
		t.Fatalf("vector registers across preemptions: %s, want every hold whole and many preemptions", out)
	}

	t.Logf("held the %s registers through %d preemptions", width, preempted)
}

// TestPreemptedChannels has tasks that compute for a while and then
// send on a channel, beside a task computing without a wait on every
// core, with the main task waking every 10 ms: a sender is taken off
// its worker in its computing and resumed on another, and its sends,
// through the ring and the channel's lock, arrive whole and in order.
func TestPreemptedChannels(t *testing.T) {
	if !opensCode() {
		t.Skip("the probes' compiler cannot open code to preemption")
	}

	probe := `#include "internal.h"
#include <stdio.h>
#include <stdatomic.h>
SN_OPEN_CODE
enum { sends = 20, spin_ns = 15000000 };
static _Atomic int64_t stop;
static void spin(void *arg) {
    (void)arg;
    while (!atomic_load(&stop)) {
    }
}
static void sender(void *arg) {
    sn_chan *c = *(sn_chan **)arg;
    for (int64_t i = 1; i <= sends; i++) {
        int64_t until = sn_time_mono() + spin_ns;
        volatile int64_t x = 0;
        while (sn_time_mono() < until) {
            x++;
        }

        sn_chan_send(c, &i);
    }

    sn_chan_drop(&c, SN_CHAN_SEND);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    int64_t n = sn_cores();
    for (int64_t i = 0; i < n; i++) {
        sn_spawn(spin, NULL, 0, SN_UNBOUNDED);
    }

    sn_chan *c = sn_chan_make(sizeof(int64_t), 4, NULL, "probe");
    for (int64_t i = 0; i < n; i++) {
        sn_chan *mine = sn_chan_clone(c, SN_CHAN_SEND);
        sn_spawn(sender, &mine, sizeof mine, SN_UNBOUNDED);
    }

    c = sn_chan_narrow(c, SN_CHAN_RECV); // the senders' handles end the channel
    int64_t sum = 0, got = 0, v;
    while (sn_chan_recv(c, &v)) {
        sum += v;
        got++;
    }

    atomic_store(&stop, 1);
    sn_chan_drop(&c, SN_CHAN_RECV);
    printf("%lld senders, got %lld values summing %lld, preempted %lld\n", (long long)n, (long long)got,
           (long long)sum, (long long)sn_sched_preempted());
    return 0;
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := runProbe(bin)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	var senders, got, sum, preempted int
	if _, err := fmt.Sscanf(string(out), "%d senders, got %d values summing %d, preempted %d", &senders, &got, &sum, &preempted); err != nil {
		t.Fatalf("unexpected output: %s", out)
	}

	if got != 20*senders || sum != senders*20*21/2 || preempted == 0 {
		t.Fatalf("senders preempted between sends: %s, want %d values summing %d and some preemptions", out, 20*senders, senders*20*21/2)
	}
}

// TestDeadlockWithAddedWorkers has a task on every core compute for a
// while and then wait on a channel no one sends on, with the main task
// sleeping meanwhile, so that the monitor adds a worker for its wake,
// and then waiting on the same channel: every task asleep is still
// reported as the deadlock it is, the added workers idle among the
// others.
func TestDeadlockWithAddedWorkers(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
static sn_chan *never;
static void spin_then_wait(void *arg) {
    sn_chan *c = *(sn_chan **)arg;
    int64_t until = sn_time_mono() + 60000000;
    while (sn_time_mono() < until) {
    }

    int64_t v;
    sn_chan_recv(c, &v);
    sn_chan_drop(&c, SN_CHAN_RECV);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    never = sn_chan_make(sizeof(int64_t), 0, NULL, "probe");
    for (int64_t i = 0; i < sn_cores(); i++) {
        sn_chan *h = sn_chan_clone(never, SN_CHAN_RECV);
        sn_spawn(spin_then_wait, &h, sizeof h, SN_UNBOUNDED);
    }

    sn_sleep(20000000); // wakes on a worker the monitor adds: every other is held
    printf("main ran again\n");
    fflush(stdout);
    int64_t v;
    sn_chan_recv(never, &v); // the sending handle is this task's own: no one sends
    return 0;
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := runProbe(bin)
	var code int
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	}

	if code != 2 || !strings.HasPrefix(string(out), "main ran again\nfatal error: all tasks are asleep - deadlock!") {
		t.Fatalf("exit %d, wanted 2 with the deadlock reported after the main task ran again:\n%s", code, out)
	}
}

// TestMonitorMovesStaleSlot has the main task wake a task into its
// worker's slot and then compute without parking until the task has
// run, while chains of tasks, each spawning the next before it ends,
// keep every other worker's own queue full, so that none runs out of
// tasks and takes the slot as a thief: the task runs only once the
// monitor has moved it to the inject queue, at its first look at which
// the worker has switched to no task since the look before, and a busy
// worker takes it from there at one of its looks. Without the move the
// task would run only once the main task parked, which it never does
// while the task has not run, until two seconds pass, so the move is
// what a run within them shows; how soon is printed, a few
// milliseconds on a quiet machine, and not held to a figure, since a
// shared runner can stall a whole machine for longer than that.
func TestMonitorMovesStaleSlot(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
static sn_chan *go;
static _Atomic int64_t ran_at;
static _Atomic bool stop;
static void chain(void *arg) {
    (void)arg;
    volatile int64_t sum = 0;
    for (int64_t i = 0; i < 20000; i++) {
        sum += i;
    }

    if (!atomic_load(&stop)) {
        sn_spawn(chain, NULL, 0, SN_UNBOUNDED);
    }
}
static void waiter(void *arg) {
    (void)arg;
    int64_t v;
    sn_chan_recv(go, &v);
    atomic_store(&ran_at, sn_time_mono());
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    go = sn_chan_make(sizeof(int64_t), 0, NULL, "probe");
    sn_spawn(waiter, NULL, 0, SN_UNBOUNDED);
    sn_sleep(1000000); // the waiter parks on its receive
    int64_t n = 2 * sn_cores();
    for (int64_t i = 0; i < n; i++) {
        sn_spawn(chain, NULL, 0, SN_UNBOUNDED);
    }

    sn_sleep(1000000); // every worker is in a chain by now
    int64_t one = 1;
    int64_t woke = sn_time_mono();
    sn_chan_send(go, &one); // the waiter wakes into this worker's slot
    int64_t at;
    while ((at = atomic_load(&ran_at)) == 0 && sn_time_mono() - woke < 2000000000) { // computing, never parking
    }

    atomic_store(&stop, true);
    printf("ran after %lld ms\n", at == 0 ? -1LL : (long long)((at - woke) / 1000000));
    sn_sleep(5000000); // the chains end
    while (sn_tasks_running() > 0) { // a task that has sent its last word may still be ending: the probe's
        sn_sleep(1000000);           // deadline, not the finish's grace, bounds a task that never ends
    }

    sn_chan_drop(&go, SN_CHAN_BOTH); // after the tasks, as a task's own handle would keep it

    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := runProbe(bin)
	var ms int
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if _, err := fmt.Sscanf(string(out), "ran after %d ms", &ms); err != nil || ms < 0 {
		t.Fatalf("the slot's task did not run while the sender computed: %s", out)
	}

	t.Logf("the slot's task ran after %d ms", ms)
}

// TestRingUnderContention has eight tasks send fifty thousand values
// each into a ring of seven places, a capacity that is no power of two,
// read by four tasks that sum what they take, and the same with values
// of no size, counted: every value sent arrives once, the ring's length
// never reads above its capacity, and the receivers end once the last
// sender has gone and the ring is drained.
func TestRingUnderContention(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
enum { senders = 8, receivers = 4, each = 50000, places = 7 };
static _Atomic int64_t sum, count, over;
static void send_ints(void *arg) {
    sn_chan *c = *(sn_chan **)arg;
    for (int64_t i = 1; i <= each; i++) {
        sn_chan_send(c, &i);
        if (sn_chan_len(c) > places) {
            atomic_fetch_add(&over, 1);
        }
    }

    sn_chan_drop(&c, SN_CHAN_SEND);
}
static void recv_ints(void *arg) {
    sn_chan *c = *(sn_chan **)arg;
    int64_t v, mine = 0;
    while (sn_chan_recv(c, &v)) {
        mine += v;
    }

    atomic_fetch_add(&sum, mine);
    sn_chan_drop(&c, SN_CHAN_RECV);
}
static void send_units(void *arg) {
    sn_chan *c = *(sn_chan **)arg;
    for (int64_t i = 0; i < each; i++) {
        sn_chan_send(c, &i); // a value of no size: the place is real, nothing is copied
    }

    sn_chan_drop(&c, SN_CHAN_SEND);
}
static void recv_units(void *arg) {
    sn_chan *c = *(sn_chan **)arg;
    int64_t mine = 0, none;
    while (sn_chan_recv(c, &none)) {
        mine++;
    }

    atomic_fetch_add(&count, mine);
    sn_chan_drop(&c, SN_CHAN_RECV);
}
static void run(sn_chan *c, void (*send)(void *), void (*recv)(void *)) {
    for (int i = 0; i < senders; i++) {
        sn_chan *h = sn_chan_clone(c, SN_CHAN_SEND);
        sn_spawn(send, &h, sizeof h, SN_UNBOUNDED);
    }

    for (int i = 0; i < receivers; i++) {
        sn_chan *h = sn_chan_clone(c, SN_CHAN_RECV);
        sn_spawn(recv, &h, sizeof h, SN_UNBOUNDED);
    }

    sn_chan_drop(&c, SN_CHAN_BOTH); // the tasks' handles alone remain: the last sender ends it
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    run(sn_chan_make(sizeof(int64_t), places, NULL, "probe"), send_ints, recv_ints);
    run(sn_chan_make(0, places, NULL, "probe"), send_units, recv_units);
    while (sn_tasks_running() > 0) {
        sn_sleep(1000000);
    }

    printf("sum %lld count %lld over %lld\n", (long long)atomic_load(&sum), (long long)atomic_load(&count),
           (long long)atomic_load(&over));
    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := runProbe(bin)
	want := fmt.Sprintf("sum %d count %d over 0\n", 8*50000*50001/2, 8*50000)
	if err != nil || string(out) != want {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestRingEndsAndFrees has a ring hold three values when its last sender
// goes: a receiver takes the three and then nothing; and senders parked
// on a full ring wake when the last receiver goes, each dropping its
// value with the element's drop, which counts them along with the
// values the ring still held when the channel was freed.
func TestRingEndsAndFrees(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
enum { places = 4, parked = 3 };
static _Atomic int64_t dropped, woke;
static void drop(void *v) {
    (void)v;
    atomic_fetch_add(&dropped, 1);
}
static void send_one(void *arg) {
    sn_chan *c = *(sn_chan **)arg;
    int64_t v = 7;
    sn_chan_send(c, &v); // parks: the ring is full
    atomic_fetch_add(&woke, 1);
    sn_chan_drop(&c, SN_CHAN_SEND);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    sn_chan *c = sn_chan_make(sizeof(int64_t), places, NULL, "probe");
    sn_chan *r = sn_chan_narrow(sn_chan_clone(c, SN_CHAN_BOTH), SN_CHAN_RECV);
    for (int64_t i = 1; i <= 3; i++) {
        sn_chan_send(c, &i);
    }

    sn_chan_drop(&c, SN_CHAN_BOTH); // the last sender: ended, with three values left
    int64_t v, got = 0;
    while (sn_chan_recv(r, &v)) {
        got += v;
    }

    bool again = sn_chan_recv(r, &v);
    sn_chan_drop(&r, SN_CHAN_RECV);
    sn_chan *d = sn_chan_make(sizeof(int64_t), places, drop, "probe");
    for (int64_t i = 0; i < places; i++) {
        sn_chan_send(d, &i); // full
    }

    for (int i = 0; i < parked; i++) {
        sn_chan *h = sn_chan_clone(d, SN_CHAN_SEND);
        sn_spawn(send_one, &h, sizeof h, SN_UNBOUNDED);
    }

    sn_sleep(20000000); // the senders park
    sn_chan_drop(&d, SN_CHAN_BOTH); // the last receiver: the parked senders wake and drop, the ring's values go
    while (sn_tasks_running() > 0) {
        sn_sleep(1000000);
    }

    printf("got %lld again %d woke %lld dropped %lld\n", (long long)got, again, (long long)atomic_load(&woke),
           (long long)atomic_load(&dropped));
    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := runProbe(bin)
	if err != nil || string(out) != "got 6 again 0 woke 3 dropped 7\n" {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestRingSelectRaces has a task select over two rings, each fed by a
// sender that pushes the moment the selector is about to register,
// twenty thousand rounds: every round completes, so the select's
// registration and its second try leave no push unseen, and each round
// takes exactly one value.
func TestRingSelectRaces(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
enum { rounds = 20000 };
static sn_chan *a, *b;
static _Atomic int64_t taken;
static void feed(void *arg) {
    sn_chan *c = *(sn_chan **)arg;
    for (int64_t i = 0; i < rounds / 2; i++) {
        sn_chan_send(c, &i);
    }

    sn_chan_drop(&c, SN_CHAN_SEND);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    a = sn_chan_make(sizeof(int64_t), 1, NULL, "probe");
    b = sn_chan_make(sizeof(int64_t), 2, NULL, "probe");
    sn_chan *fa = sn_chan_clone(a, SN_CHAN_SEND), *fb = sn_chan_clone(b, SN_CHAN_SEND);
    sn_spawn(feed, &fa, sizeof fa, SN_UNBOUNDED);
    sn_spawn(feed, &fb, sizeof fb, SN_UNBOUNDED);
    for (int r = 0; r < rounds; r++) {
        int64_t va, vb;
        sn_case cases[2] = { { a, &va, false, false }, { b, &vb, false, false } };
        int i = sn_select(cases, 2, false);
        if (i >= 0 && cases[i].ok) {
            atomic_fetch_add(&taken, 1);
        }
    }

    sn_chan_drop(&a, SN_CHAN_BOTH);
    sn_chan_drop(&b, SN_CHAN_BOTH);
    printf("taken %lld\n", (long long)atomic_load(&taken));
    while (sn_tasks_running() > 0) { // a task that has sent its last word may still be ending: the probe's
        sn_sleep(1000000);           // deadline, not the finish's grace, bounds a task that never ends
    }

    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := runProbe(bin)
	if err != nil || string(out) != "taken 20000\n" {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestChannelEndsAtOnce drops the last sending handle and the last
// receiving handle of a channel in two tasks at the same moment, many
// times over, in a debug build: each last handle has work to do in the
// channel as it goes, and the channel must be freed once, after both,
// never under the other, which the debug allocator's poisoning and its
// leak report hold it to. A handle given up for an end it does not hold
// stops the program, which the second probe shows.
func TestChannelEndsAtOnce(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
enum { rounds = 20000 };
static _Atomic int round_on;
static sn_chan *done;
typedef struct {
    sn_chan *h;
    int dir;
    int round;
} handle;
static void dropper(void *arg) {
    handle *h = arg;
    while (atomic_load(&round_on) != h->round) { // both drop as the round opens
        sn_spin_hint();
    }

    sn_chan_drop(&h->h, h->dir);
    int64_t one = 1;
    sn_chan_send(done, &one);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    done = sn_chan_make(sizeof(int64_t), 2, NULL, "probe");
    for (int r = 1; r <= rounds; r++) {
        sn_chan *c = sn_chan_make(sizeof(int64_t), 1, NULL, "probe");
        int64_t v = r;
        sn_chan_send(c, &v); // a value left in the ring goes with the channel
        handle sender = { sn_chan_clone(c, SN_CHAN_SEND), SN_CHAN_SEND, r };
        handle receiver = { sn_chan_narrow(c, SN_CHAN_RECV), SN_CHAN_RECV, r };
        sn_spawn(dropper, &sender, sizeof sender, SN_UNBOUNDED);
        sn_spawn(dropper, &receiver, sizeof receiver, SN_UNBOUNDED);
        atomic_store(&round_on, r);
        int64_t got;
        sn_chan_recv(done, &got);
        sn_chan_recv(done, &got);
    }

    while (sn_tasks_running() > 0) { // a task that has sent its last word may still be ending: the exit check of a
        sn_sleep(1000000);           // debug build reports a task still running, which is no leak of the channel's
    }

    printf("rounds %d\n", rounds);
    sn_chan_drop(&done, SN_CHAN_BOTH);
    return sn_finish();
}
`
	dir := t.TempDir()
	bin := buildProbe(t, filepath.Join(dir, "build"), probe)
	out, err := testproc.Command(bin).CombinedOutput()
	if err != nil || string(out) != "rounds 20000\n" {
		t.Fatalf("%v\n%s", err, out)
	}

	twice := `#include "internal.h"
int main(int argc, char **argv) {
    sn_init(argc, argv);
    sn_chan *c = sn_chan_narrow(sn_chan_make(sizeof(int64_t), 1, NULL, "probe"), SN_CHAN_SEND);
    sn_chan *s = sn_chan_clone(c, SN_CHAN_SEND);
    sn_chan_drop(&s, SN_CHAN_RECV); // a sending handle given up as a receiving one, of which none is left
    sn_chan_drop(&c, SN_CHAN_SEND);
    return sn_finish();
}
`
	bin = buildProbe(t, filepath.Join(dir, "twice"), twice)
	out, err = testproc.Command(bin).CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 || !strings.Contains(string(out), "a channel handle was given up twice") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestSocketsWithEveryWorkerBusy holds a socket's task to waking while
// every worker is busy: chains of tasks, each spawning the next before
// it ends, keep every worker's own queue full, so that no worker runs
// out of tasks to look at the sockets' poller and none waits in it,
// and a client outside the scheduler connects once they do. Only the
// monitor's look hears of the connection; without it the accepting task
// would wait for ever, and the test's alarm would end it.
func TestSocketsWithEveryWorkerBusy(t *testing.T) {
	probe := `#include "internal.h"
#include "probe.h"
#include <stdio.h>
static _Atomic bool accepted;
static int64_t listener, port;
static sn_chan *done;
static void chain(void *arg) {
    (void)arg;
    volatile int64_t sum = 0;
    for (int64_t i = 0; i < 20000; i++) {
        sum += i;
    }

    if (!atomic_load(&accepted)) {
        sn_spawn(chain, NULL, 0, SN_UNBOUNDED);
    }
}
static void serve(void *arg) {
    (void)arg;
    int64_t conn = -1;
    int64_t code = sn_net_accept(listener, &conn);
    atomic_store(&accepted, true);
    sn_chan_send(done, &code);
}
static void *client(void *arg) {
    (void)arg;
    sn_sleep_ns(300000000);
    if (probe_connect_loopback(port) == PROBE_NO_SOCKET) {
        puts("connect failed");
    }

    return NULL;
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    probe_alarm(20);
    sn_string call = SN_STR("", 0);
    if (sn_net_listen(SN_STR("127.0.0.1", 9), 0, &listener, &port, &call) != 0) {
        puts("listen failed");
        return 1;
    }

    done = sn_chan_make(sizeof(int64_t), 1, NULL, "probe");
    sn_spawn(serve, NULL, 0, SN_UNBOUNDED);
    int64_t n = 4 * sn_cores();
    for (int64_t i = 0; i < n; i++) {
        sn_spawn(chain, NULL, 0, SN_UNBOUNDED);
    }

    probe_thread id = probe_thread_start(client, NULL);
    int64_t code;
    sn_chan_recv(done, &code);
    probe_thread_join(id);
    printf("accepted: %lld\n", (long long)code);
    while (sn_tasks_running() > 0) { // a task that has sent its last word may still be ending: the probe's
        sn_sleep(1000000);           // deadline, not the finish's grace, bounds a task that never ends
    }

    sn_chan_drop(&done, SN_CHAN_BOTH); // after the tasks, as a task's own handle would keep it

    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := testproc.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if string(out) != "accepted: 0\n" {
		t.Fatalf("got %q, want the connection accepted", out)
	}
}

// TestKeeperRestsOnUnwaitedEvents holds the keeper to resting while
// the sockets' poller holds events no task waits for: a connection
// accepted and left unread, with data in it and room to send, while the
// main task sleeps half a second. The keeper's wait ends on those
// events, and a keeper that left them in the poller would wait again,
// end at once and spin through the sleep, using a second of the
// processor for each second of it.
func TestKeeperRestsOnUnwaitedEvents(t *testing.T) {
	probe := `#include "internal.h"
#include "probe.h"
#include <stdio.h>
static int64_t listener, port;
static sn_chan *done;
static void serve(void *arg) {
    (void)arg;
    int64_t conn = -1;
    int64_t code = sn_net_accept(listener, &conn);
    sn_chan_send(done, &code);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    probe_alarm(20);
    sn_string call = SN_STR("", 0);
    if (sn_net_listen(SN_STR("127.0.0.1", 9), 0, &listener, &port, &call) != 0) {
        puts("listen failed");
        return 1;
    }

    done = sn_chan_make(sizeof(int64_t), 1, NULL, "probe");
    sn_spawn(serve, NULL, 0, SN_UNBOUNDED);
    probe_socket s = probe_connect_loopback(port);
    if (s == PROBE_NO_SOCKET) {
        puts("connect failed");
        return 1;
    }

    int64_t code;
    sn_chan_recv(done, &code);
    if (probe_send(s, "abc", 3) != 3) {
        puts("send failed");
        return 1;
    }

    int64_t before = probe_cpu_ns() / 1000;
    sn_sleep(500000000);
    int64_t used = probe_cpu_ns() / 1000 - before;
    printf("accepted: %lld\n", (long long)code);
    if (used > 150000) {
        printf("the sleep used %lld µs of the processor\n", (long long)used);
    }

    sn_chan_drop(&done, SN_CHAN_BOTH);
    probe_close_socket(s);
    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := testproc.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if string(out) != "accepted: 0\n" {
		t.Fatalf("got %q, want the connection accepted and the keeper at rest", out)
	}
}

// TestLookupsOffTheWorkers holds lookups to running beside the workers:
// a name server that does not answer, here a getaddrinfo of the test's
// own that blocks until the test lets it go, which the linker takes
// over the system's, holds twice as many lookups as there are workers,
// and a task spawned after them still computes, while a lookup with a
// timeout of 50 ms gives up on time. A lookup that held its worker
// would leave the computation none to run on, and the test's alarm
// would end it.
func TestLookupsOffTheWorkers(t *testing.T) {
	probe := `#define _GNU_SOURCE
#define _DARWIN_C_SOURCE
#include "internal.h"
#include "probe.h"
#include <stdio.h>
#if !defined(_WIN32)
#include <netdb.h>
#endif
static sn_mutex gate_mu = SN_MUTEX_INIT;
static sn_cond gate_cv = SN_COND_INIT;
static bool gate_open;
static void wait_for_gate(void) {
    sn_mutex_lock(&gate_mu);
    while (!gate_open) {
        sn_cond_wait(&gate_cv, &gate_mu);
    }

    sn_mutex_unlock(&gate_mu);
}
#if defined(_WIN32)
// The runtime calls the system's resolver through its slot of the table
// of imports, which this object defines, so that the linker takes it
// over the system library's.
static INT WSAAPI held_lookup(PCWSTR node, PCWSTR service, const ADDRINFOW *hints, PADDRINFOW *res) {
    (void)node, (void)service, (void)hints;
    *res = NULL;
    wait_for_gate();
    return EAI_NONAME;
}
static VOID WSAAPI held_free(PADDRINFOW ai) {
    (void)ai;
}
void *__imp_GetAddrInfoW = (void *)held_lookup;
void *__imp_FreeAddrInfoW = (void *)held_free;
#else
int getaddrinfo(const char *node, const char *service, const struct addrinfo *hints, struct addrinfo **res) {
    (void)node, (void)service, (void)hints;
    *res = NULL;
    wait_for_gate();
    return EAI_NONAME;
}
void freeaddrinfo(struct addrinfo *ai) {
    (void)ai;
}
#endif
static sn_chan *results;
static sn_chan *computed;
static void look(void *arg) {
    int64_t timeout = *(int64_t *)arg;
    sn_slice out = {0};
    sn_string reason = SN_STR("", 0);
    int64_t code = sn_net_lookup(SN_STR("hangs.test", 10), timeout, &out, &reason);
    sn_chan_send(results, &code);
}
static void compute(void *arg) {
    (void)arg;
    volatile int64_t sum = 0;
    for (int64_t i = 0; i < 100000000; i++) {
        sum += i % 7;
    }

    int64_t got = sum;
    sn_chan_send(computed, &got);
}
int main(int argc, char **argv) {
    sn_init(argc, argv);
    probe_alarm(30);
    int64_t n = 2 * sn_cores();
    results = sn_chan_make(sizeof(int64_t), n + 1, NULL, "probe");
    computed = sn_chan_make(sizeof(int64_t), 1, NULL, "probe");
    for (int64_t i = 0; i < n; i++) {
        int64_t none = 0;
        sn_spawn(look, &none, sizeof none, SN_UNBOUNDED);
    }

    int64_t late = 50000000;
    sn_spawn(look, &late, sizeof late, SN_UNBOUNDED);
    sn_spawn(compute, NULL, 0, SN_UNBOUNDED);
    int64_t sum;
    sn_chan_recv(computed, &sum);
    printf("computed %lld\n", (long long)sum);
    int64_t code;
    sn_chan_recv(results, &code);
    printf("first back: %s\n", code == SN_NET_TIMED_OUT ? "timed out" : "something else");
    sn_mutex_lock(&gate_mu);
    gate_open = true;
    sn_cond_broadcast(&gate_cv);
    sn_mutex_unlock(&gate_mu);
    int64_t none = 0;
    for (int64_t i = 0; i < n; i++) {
        sn_chan_recv(results, &code);
        none += code == SN_NET_NO_SUCH_HOST;
    }

    printf("no such host: %s\n", none == n ? "all" : "not all");
    while (sn_tasks_running() > 0) { // a task that has sent its last word may still be ending: the probe's
        sn_sleep(1000000);           // deadline, not the finish's grace, bounds a task that never ends
    }

    sn_chan_drop(&results, SN_CHAN_BOTH);
    sn_chan_drop(&computed, SN_CHAN_BOTH); // after the tasks, as a task's own handle would keep it

    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "build"), probe)
	out, err := testproc.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if want := "computed 299999995\nfirst back: timed out\nno such host: all\n"; string(out) != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func bit(b bool) int {
	if b {
		return 1
	}

	return 0
}

// buildProbe compiles a C program against the whole runtime in dir, as
// the C backend compiles a program and with its flags, as a debug build
// so that the runtime's checks and its leak report run; it skips the
// test without a C compiler.
func buildProbe(t *testing.T, dir, src string) string {
	t.Helper()
	return buildProbeAs(t, dir, src, "-O1", "-DSN_DEBUG")
}

// buildProbeAs is buildProbe with the options of the build named: a
// debug build's, or a release build's for a probe that measures.
func buildProbeAs(t *testing.T, dir, src string, mode ...string) string {
	t.Helper()
	cc := probeCompiler()

	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("no C compiler")
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, data := range Files() {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for name, text := range map[string]string{"probe.c": src, "probe.h": probeHeader} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	bin := probePath(dir)
	args := []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-Wno-tautological-compare", "-fwrapv", "-fno-strict-aliasing", "-ffp-contract=off"}
	args = append(append(args, systemProbeFlags()...), mode...)
	args = append(args, "-o", bin, filepath.Join(dir, "probe.c"))
	args = append(append(args, Paths(dir)...), systemProbeSources(t, dir)...)
	if runtime.GOOS != windowsOS {
		args = append(args, posixProbeLibs...)
	}
	if b, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}

	return bin
}

// runProbe runs the probe bin with args and returns what it wrote, its
// output and its errors together. A probe still running at probeTimeout
// is a hang, a lost wake: it is reported with each of its threads'
// stacks, as the system's debugger reads them, and then ended.
func runProbe(bin string, args ...string) ([]byte, error) {
	var out bytes.Buffer
	cmd := testproc.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return out.Bytes(), err
	case <-time.After(probeTimeout):
		stacks := threadStacks(cmd.Process.Pid)
		cmd.Process.Kill()
		<-done
		return out.Bytes(), fmt.Errorf("still running after %v, its threads:\n%s", probeTimeout, stacks)
	}
}

// threadStacks returns the stack of each thread of the process pid, as
// the system's debugger prints them, or why it could not read them.
func threadStacks(pid int) string {
	ctx, cancel := context.WithTimeout(context.Background(), stacksTimeout)
	defer cancel()
	p := strconv.Itoa(pid)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case windowsOS:
		cmd = testproc.CommandContext(ctx, windowsDebugger(), "-p", p, "-c", cdbStacks)
	case darwinOS:
		cmd = testproc.CommandContext(ctx, lldbName, "--batch", "-p", p, "-o", lldbStacks)
	default:
		cmd = testproc.CommandContext(ctx, gdbName, "-batch", "-p", p, "-ex", gdbStacks)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("%s: %v\n%s", cmd.Path, err, out)
	}

	return string(out)
}

// windowsDebugger returns the cdb on the path, else the one Windows'
// SDK installs for the processor the tests run on.
func windowsDebugger() string {
	if path, err := exec.LookPath(cdbName); err == nil {
		return path
	}

	parts := append([]string{os.Getenv(programFilesX86)}, windowsKitDebuggers...)
	return filepath.Join(append(parts, kitProcessors[runtime.GOARCH], cdbName+windowsSuffix)...)
}

// opensCode reports whether the probes' compiler can open a unit's
// code to preemption, which the mark needs clang for (snello.h,
// SN_OPEN_CODE): a probe another compiler built has no open code, and
// a test of its tasks' preemption has nothing to test.
func opensCode() bool {
	cmd := exec.Command(probeCompiler(), "-E", "-x", "c", "-")
	cmd.Stdin = strings.NewReader("__clang__\n")
	out, err := cmd.Output()
	return err != nil || !strings.Contains(string(out), "__clang__") // a compiler that defines the name writes its value
}

// probeCompiler returns the C compiler a probe is built with: the one
// CC names, else the system's.
func probeCompiler() string {
	if cc := os.Getenv("CC"); cc != "" {
		return cc
	}

	if runtime.GOOS == windowsOS {
		return probeWindowsCC
	}

	return probeCC
}

// probePath returns the path of a probe's binary in dir.
func probePath(dir string) string {
	if runtime.GOOS == windowsOS {
		return filepath.Join(dir, probeBinary+windowsSuffix)
	}

	return filepath.Join(dir, probeBinary)
}

// systemProbeFlags returns the options a probe's compile and link take
// on the system the tests run on.
func systemProbeFlags() []string {
	if runtime.GOOS == windowsOS {
		return windowsProbeFlags
	}

	return posixProbeFlags
}

// systemProbeSources writes into dir the sources a probe is built with
// on the system the tests run on, beside its own, and returns their
// paths: windowsBinary's on Windows, none elsewhere.
func systemProbeSources(t *testing.T, dir string) []string {
	t.Helper()
	if runtime.GOOS != windowsOS {
		return nil
	}

	path := filepath.Join(dir, "windows_binary.c")
	if err := os.WriteFile(path, []byte(windowsBinary), 0o644); err != nil {
		t.Fatal(err)
	}

	return []string{path}
}

// probeLines runs a probe built against the whole runtime with input
// on its standard input and returns its output lines.
func probeLines(t *testing.T, name, src, input string) []string {
	t.Helper()
	bin := buildProbe(t, filepath.Join(t.TempDir(), name), src)
	cmd := testproc.Command(bin)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	text := strings.TrimSuffix(string(out), "\n")
	if text == "" {
		return nil
	}

	return strings.Split(text, "\n")
}

// codes reads a sequence of hexadecimal code points.
func codes(t *testing.T, s string) []rune {
	t.Helper()
	var out []rune
	for f := range strings.FieldsSeq(s) {
		n, err := strconv.ParseUint(f, 16, 32)
		if err != nil {
			t.Fatalf("bad code point %q", f)
		}

		out = append(out, rune(n))
	}

	return out
}

func hexText(rs []rune) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = fmt.Sprintf("%04X", r)
	}

	return strings.Join(parts, " ")
}

// The probe the normalization and case tests share: each line of its
// input is a mode and code points, and it answers with code points.
const textProbe = `#include "snello.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static sn_string encode(const int32_t *rs, int n) {
    uint8_t *p = sn_alloc((size_t)n * 4 + 1);
    int64_t at = 0;
    for (int i = 0; i < n; i++) {
        at += sn_utf8_encode(rs[i], p + at);
    }

    return (sn_string){p, at, at > 0 ? at : 0};
}

static void print(sn_string s) {
    for (int64_t i = 0; i < s.len;) {
        int32_t r;
        i += sn_utf8_decode(s.ptr + i, s.len - i, &r);
        printf(i == s.len ? "%04X" : "%04X ", r);
    }
}

int main(void) {
    char line[4096];
    while (fgets(line, sizeof line, stdin)) {
        char mode[16];
        int at = 0, used;
        if (sscanf(line, "%15s%n", mode, &used) != 1) {
            continue;
        }

        at += used;
        int32_t rs[256];
        int n = 0;
        unsigned v;
        while (sscanf(line + at, "%x%n", &v, &used) == 1) {
            rs[n++] = (int32_t)v;
            at += used;
        }

        sn_string s = encode(rs, n);
        if (strcmp(mode, "norm") == 0) {
            for (int f = 0; f < 4; f++) {
                sn_string t = sn_unicode__norm_normalize(s, f);
                print(t);
                printf(f < 3 ? ";" : "\n");
                sn_string_drop(&t);
            }
        } else if (strcmp(mode, "self") == 0) { // every code point given that does not normalize to itself in every form
            for (int i = 0; i < n; i++) {
                sn_string one = encode(&rs[i], 1);
                for (int f = 0; f < 4; f++) {
                    if (!sn_unicode__norm_isNormal(one, f)) {
                        printf("%04X %d\n", rs[i], f);
                    }
                }

                sn_string_drop(&one);
            }
        } else if (strcmp(mode, "agree") == 0) { // the quick check against normalizing and comparing, for the whole string
            for (int f = 0; f < 4; f++) {
                sn_string t = sn_unicode__norm_normalize(s, f);
                bool same = t.len == s.len && memcmp(t.ptr, s.ptr, (size_t)s.len) == 0;
                if (sn_unicode__norm_isNormal(s, f) != same) {
                    printf("%d ", f);
                }

                sn_string_drop(&t);
            }

            printf("\n");
        } else if (strcmp(mode, "agreeEach") == 0) { // the same, each code point given alone, printing the ones that disagree
            for (int i = 0; i < n; i++) {
                sn_string one = encode(&rs[i], 1);
                for (int f = 0; f < 4; f++) {
                    sn_string t = sn_unicode__norm_normalize(one, f);
                    bool same = t.len == one.len && memcmp(t.ptr, one.ptr, (size_t)one.len) == 0;
                    if (sn_unicode__norm_isNormal(one, f) != same) {
                        printf("%04X %d\n", rs[i], f);
                    }

                    sn_string_drop(&t);
                }

                sn_string_drop(&one);
            }
        } else if (strcmp(mode, "case") == 0) {
            sn_string u = sn_to_upper(s), l = sn_to_lower(s), t = sn_strings_toTitle(s);
            print(u);
            printf(";");
            print(l);
            printf(";");
            print(t);
            printf("\n");
            sn_string_drop(&u);
            sn_string_drop(&l);
            sn_string_drop(&t);
        } else if (strcmp(mode, "fold") == 0) { // a count k, then k runes against the rest
            int k = rs[0];
            sn_string a = encode(rs + 1, k), b = encode(rs + 1 + k, n - 1 - k);
            printf("%d\n", sn_strings_equalIgnoreCase(a, b));
            sn_string_drop(&a);
            sn_string_drop(&b);
        } else { // grapheme, word or sentence: the boundaries' byte offsets
            int kind = strcmp(mode, "grapheme") == 0 ? 0 : strcmp(mode, "word") == 0 ? 1 : 2;
            int64_t off = 0;
            printf("0");
            while (off < s.len) {
                off += sn_unicode__segment_next(s, off, kind);
                printf(" %lld", (long long)off);
            }

            printf("\n");
        }

        sn_string_drop(&s);
    }

    return 0;
}
`

// TestNormalization checks the four forms against every line of
// NormalizationTest.txt, with the columns its header prescribes: for
// c1 to c5, NFC gives c2 of the first three and c4 of the last two,
// NFD c3 and c5, NFKC c4 and NFKD c5; and checks that every code point
// the file's Part 1 does not list is in every form already.
func TestNormalization(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(ucd, "NormalizationTest.txt"))
	if err != nil {
		t.Fatal(err)
	}

	var input strings.Builder
	var cases [][5][]rune
	listed := map[rune]bool{}
	part := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "@Part") {
			part = strings.Fields(line)[0]
			continue
		}

		line, _, _ = strings.Cut(line, "#")
		f := strings.Split(line, ";")
		if len(f) < 6 {
			continue
		}

		var c [5][]rune
		for i := range c {
			c[i] = codes(t, f[i])
		}

		if part == "@Part1" {
			listed[c[0][0]] = true
		}

		cases = append(cases, c)
		for i := range c {
			fmt.Fprintf(&input, "norm %s\n", hexText(c[i]))
		}
	}

	var rest []rune
	for r := rune(0); r <= 0x10FFFF; r++ {
		if !listed[r] && (r < 0xD800 || r > 0xDFFF) {
			rest = append(rest, r)
		}
	}

	for i := 0; i < len(rest); i += 200 {
		fmt.Fprintf(&input, "self %s\n", hexText(rest[i:min(i+200, len(rest))]))
	}

	got := probeLines(t, "norm", textProbe, input.String())
	if len(got) < 5*len(cases) {
		t.Fatalf("the probe answered %d lines for %d cases", len(got), 5*len(cases))
	}

	forms := []string{"NFC", "NFD", "NFKC", "NFKD"}
	failures := 0
	for k, c := range cases {
		for col := 0; col < 5; col++ {
			want := [4][]rune{c[1], c[2], c[3], c[4]} // NFC, NFD, NFKC, NFKD of c1, c2, c3
			if col >= 3 {
				want = [4][]rune{c[3], c[4], c[3], c[4]}
			}

			parts := strings.Split(got[5*k+col], ";")
			for f := range forms {
				if parts[f] != hexText(want[f]) {
					failures++
					if failures <= 10 {
						t.Errorf("%s(%s), column %d: got %s, want %s", forms[f], hexText(c[col]), col+1, parts[f], hexText(want[f]))
					}
				}
			}
		}
	}

	forms4 := []string{"NFC", "NFD", "NFKC", "NFKD"}
	for _, line := range got[5*len(cases):] {
		failures++
		if failures <= 20 {
			code, form, _ := strings.Cut(line, " ")
			f, _ := strconv.Atoi(form)
			t.Errorf("U+%s, which Part 1 does not list, is not in %s", code, forms4[min(max(f, 0), 3)])
		}
	}

	if failures > 0 {
		t.Fatalf("%d failures", failures)
	}
}

// TestSegmentation checks the boundaries of grapheme clusters, words and
// sentences against every line of GraphemeBreakTest.txt,
// WordBreakTest.txt and SentenceBreakTest.txt: code points with ÷ where
// a boundary is and × where none is.
func TestSegmentation(t *testing.T) {
	for _, tt := range []struct{ kind, file string }{
		{"grapheme", "GraphemeBreakTest.txt"},
		{"word", "WordBreakTest.txt"},
		{"sentence", "SentenceBreakTest.txt"},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(ucd, tt.file))
			if err != nil {
				t.Fatal(err)
			}

			var input strings.Builder
			var wants, texts []string
			for _, line := range strings.Split(string(data), "\n") {
				line, _, _ = strings.Cut(line, "#")
				if strings.TrimSpace(line) == "" {
					continue
				}

				var rs []rune
				offsets := []string{}
				size := 0
				for f := range strings.FieldsSeq(line) {
					switch f {
					case "÷":
						offsets = append(offsets, strconv.Itoa(size))
					case "×":
					default:
						r := codes(t, f)[0]
						rs = append(rs, r)
						size += len(string(r))
					}
				}

				wants = append(wants, strings.Join(offsets, " "))
				texts = append(texts, line)
				fmt.Fprintf(&input, "%s %s\n", tt.kind, hexText(rs))
			}

			got := probeLines(t, tt.kind, textProbe, input.String())
			if len(got) != len(wants) {
				t.Fatalf("the probe answered %d lines for %d cases", len(got), len(wants))
			}

			failures := 0
			for i := range wants {
				if got[i] != wants[i] {
					failures++
					if failures <= 10 {
						t.Errorf("%s: got boundaries %s, want %s", strings.TrimSpace(texts[i]), got[i], wants[i])
					}
				}
			}

			if failures > 0 {
				t.Fatalf("%d of %d cases fail", failures, len(wants))
			}
		})
	}
}

// TestCaseMapping checks the full case mappings of strings against
// SpecialCasing.txt and UnicodeData.txt for every assigned code point:
// a mapping of SpecialCasing.txt that holds in every language, else the
// simple mapping, upper, lower and title; the final sigma in context;
// and EqualIgnoreCase against CaseFolding.txt's full folding, statuses C and
// F: every rune equal to its folding, and to its upper case exactly
// when the two fold alike.
func TestCaseMapping(t *testing.T) {
	facts := readUCD(t)
	special := [3]map[rune][]rune{{}, {}, {}}
	full := map[rune][]rune{}
	read := func(file string, each func(f []string)) {
		data, err := os.ReadFile(filepath.Join(ucd, file))
		if err != nil {
			t.Fatal(err)
		}

		for _, line := range strings.Split(string(data), "\n") {
			line, _, _ = strings.Cut(line, "#")
			f := strings.Split(line, ";")
			if len(f) < 3 {
				continue
			}

			for i := range f {
				f[i] = strings.TrimSpace(f[i])
			}

			each(f)
		}
	}

	read("SpecialCasing.txt", func(f []string) {
		if len(f) >= 5 && f[4] == "" {
			r := codes(t, f[0])[0]
			special[0][r], special[1][r], special[2][r] = codes(t, f[3]), codes(t, f[1]), codes(t, f[2])
		}
	})
	read("CaseFolding.txt", func(f []string) {
		r := codes(t, f[0])[0]
		if f[1] == "C" || f[1] == "F" {
			full[r] = codes(t, f[2])
		}
	})

	var input strings.Builder
	var runes []rune
	for r := rune(0); r <= 0x10FFFF; r++ {
		if f := facts[r]; f != nil && f.category != "Cs" && f.category != "" {
			runes = append(runes, r)
			fmt.Fprintf(&input, "case %04X\n", r)
		}
	}

	sigma := [][2]string{ // a capital sigma lowered in context, and what it gives
		{"03A3", "03C3"},                     // alone: no cased letter before
		{"0041 03A3", "0061 03C2"},           // at the end of a word
		{"0041 03A3 0042", "0061 03C3 0062"}, // inside a word
		{"0041 0027 03A3", "0061 0027 03C2"}, // a case-ignorable apostrophe between
		{"0041 03A3 0027 0042", "0061 03C3 0027 0062"},
		{"03A3 0041", "03C3 0061"},
	}
	for _, c := range sigma {
		fmt.Fprintf(&input, "case %s\n", c[0])
	}

	foldOf := func(r rune) string { // the full folding, C and F: a rune with no entry folds to itself
		if m, ok := full[r]; ok {
			return hexText(m)
		}

		return hexText([]rune{r})
	}

	var folds [][2][]rune
	var equal []bool
	for r := range full {
		folds = append(folds, [2][]rune{{r}, full[r]})
		equal = append(equal, true)
	}

	for _, r := range runes {
		if f := facts[r]; f.upper != r { // a rune and its upper case are equal when their foldings are, not always: dotless i folds to itself
			folds = append(folds, [2][]rune{{r}, {f.upper}})
			equal = append(equal, foldOf(r) == foldOf(f.upper))
		}
	}

	for _, f := range folds {
		fmt.Fprintf(&input, "fold %X %s %s\n", len(f[0]), hexText(f[0]), hexText(f[1]))
	}

	fmt.Fprintf(&input, "fold 1 0041 0062\nfold 2 0073 0073 00DF\nfold 1 00DF 0073\n") // not equal, equal by full folding, a prefix is not equal
	got := probeLines(t, "case", textProbe, input.String())
	if len(got) != len(runes)+len(sigma)+len(folds)+3 {
		t.Fatalf("the probe answered %d lines, want %d", len(got), len(runes)+len(sigma)+len(folds)+3)
	}

	failures := 0
	fail := func(format string, args ...any) {
		failures++
		if failures <= 10 {
			t.Errorf(format, args...)
		}
	}

	for i, r := range runes {
		f := facts[r]
		simple := [3]rune{f.upper, f.lower, f.title}
		parts := strings.Split(got[i], ";")
		for k, name := range []string{"upper", "lower", "title"} {
			want := []rune{simple[k]}
			if m, ok := special[k][r]; ok {
				want = m
			}

			if r == 0x03A3 && k == 1 {
				want = []rune{0x03C3} // alone, the sigma is not final
			}

			if parts[k] != hexText(want) {
				fail("%s of %04X: got %s, want %s", name, r, parts[k], hexText(want))
			}
		}
	}

	for i, c := range sigma {
		if parts := strings.Split(got[len(runes)+i], ";"); parts[1] != c[1] {
			fail("lower of %s: got %s, want %s", c[0], parts[1], c[1])
		}
	}

	for i, f := range folds {
		if want := map[bool]string{false: "0", true: "1"}[equal[i]]; got[len(runes)+len(sigma)+i] != want {
			fail("EqualIgnoreCase(%s, %s): got %s, want %s", hexText(f[0]), hexText(f[1]), got[len(runes)+len(sigma)+i], want)
		}
	}

	if tail := got[len(got)-3:]; strings.Join(tail, " ") != "0 1 0" {
		fail("EqualIgnoreCase of A and b, ss and ß, ß and s: got %v, want 0 1 0", tail)
	}

	if failures > 0 {
		t.Fatalf("%d failures", failures)
	}
}

// TestQuickCheck checks that IsNormalString, which answers from the
// quick-check values and goes on to normalize only for their maybe,
// agrees with normalizing and comparing: for every code point alone and
// for every string of NormalizationTest.txt, in all four forms.
func TestQuickCheck(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(ucd, "NormalizationTest.txt"))
	if err != nil {
		t.Fatal(err)
	}

	var input strings.Builder
	cases := 0
	for _, line := range strings.Split(string(data), "\n") {
		line, _, _ = strings.Cut(line, "#")
		f := strings.Split(line, ";")
		if len(f) < 6 {
			continue
		}

		for i := 0; i < 5; i++ {
			fmt.Fprintf(&input, "agree %s\n", hexText(codes(t, f[i])))
			cases++
		}
	}

	var all []rune
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r < 0xD800 || r > 0xDFFF {
			all = append(all, r)
		}
	}

	for i := 0; i < len(all); i += 200 {
		fmt.Fprintf(&input, "agreeEach %s\n", hexText(all[i:min(i+200, len(all))]))
	}

	got := probeLines(t, "quick", textProbe, input.String())
	failures := 0
	for i, line := range got {
		if i < cases && strings.TrimSpace(line) == "" {
			continue
		}

		failures++
		if failures <= 10 {
			t.Errorf("the quick check disagrees with normalizing: line %d: %q", i+1, line)
		}
	}

	if failures > 0 {
		t.Fatalf("%d disagreements", failures)
	}
}

// TestLinearTime runs the two algorithms whose naive forms take time in
// the square of the input, sentence boundaries over a long run of
// spaces or closing marks, and canonical ordering over a long run of
// marks, on inputs where the square would take minutes, under a bound
// a linear algorithm meets with room to spare.
func TestLinearTime(t *testing.T) {
	probe := `#include "snello.h"
#include <stdio.h>
#include <string.h>
int main(int argc, char **argv) {
    (void)argc;
    int64_t n = 200000;
    uint8_t *p = sn_alloc((size_t)n * 4 + 4);
    int64_t len = 0;
    if (strcmp(argv[1], "spaces") == 0 || strcmp(argv[1], "closes") == 0) {
        p[len++] = 'x';
        p[len++] = '.';
        for (int64_t i = 0; i < n; i++) {
            p[len++] = argv[1][0] == 's' ? ' ' : ')';
        }
        sn_string s = {p, len, 0};
        int64_t at = 0, k = 0;
        while (at < len) {
            at += sn_unicode__segment_next(s, at, 2);
            k++;
        }
        printf("%lld\n", (long long)k);
    } else {
        p[len++] = 'a';
        for (int64_t i = 0; i < n; i++) { // U+0316, class 220, then U+0301, class 230, alternating
            len += sn_utf8_encode(i % 2 == 0 ? 0x0316 : 0x0301, p + len);
        }
        sn_string s = {p, len, 0};
        sn_string t = sn_unicode__norm_normalize(s, 0);
        printf("%lld\n", (long long)t.len);
        sn_string_drop(&t);
    }
    sn_free(p);
    return 0;
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "linear"), probe)
	for _, mode := range []string{"spaces", "closes", "marks"} {
		start := time.Now()
		out, err := testproc.Command(bin, mode).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", mode, err, out)
		}

		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("%s: 200,000 runes took %v; a linear algorithm takes milliseconds", mode, took)
		}
	}
}

// TestStringSearch holds the string searches to a plain reference over
// texts of a few letters, where matches, near matches and overlaps are
// common: Index, LastIndex, Count, Replace and SplitN, and the byte
// slice's Find, at every needle length the filters treat differently.
func TestStringSearch(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
#include <string.h>
static uint64_t seed = 88172645463325252ull;
static uint64_t next(void) { seed ^= seed << 13; seed ^= seed >> 7; seed ^= seed << 17; return seed; }
static int64_t ref_index(sn_string s, sn_string t) {
    for (int64_t i = 0; i + t.len <= s.len; i++) if (memcmp(s.ptr + i, t.ptr, (size_t)t.len) == 0) return i;
    return -1;
}
static int64_t ref_last(sn_string s, sn_string t) {
    for (int64_t i = s.len - t.len; i >= 0; i--) if (memcmp(s.ptr + i, t.ptr, (size_t)t.len) == 0) return i;
    return -1;
}
static int64_t ref_count(sn_string s, sn_string t) {
    int64_t n = 0;
    for (int64_t i = 0; i + t.len <= s.len;) {
        if (memcmp(s.ptr + i, t.ptr, (size_t)t.len) == 0) { n++; i += t.len; } else i++;
    }
    return n;
}
int main(void) {
    static uint8_t sb[300], tb[40], nb[8], rb[4000];
    int bad = 0;
    for (int iter = 0; iter < 20000 && bad < 10; iter++) {
        int64_t n = (int64_t)(next() % 260), m = 1 + (int64_t)(next() % 24), letters = 1 + (int64_t)(next() % 3);
        for (int64_t i = 0; i < n; i++) sb[i] = (uint8_t)('a' + next() % letters);
        for (int64_t i = 0; i < m; i++) tb[i] = (uint8_t)('a' + next() % letters);
        if (n > m && next() % 2) memcpy(sb + next() % (uint64_t)(n - m), tb, (size_t)m); // a sure match somewhere
        sn_string s = {sb, n, 0}, t = {tb, m, 0};
        int64_t got = sn_string_index(s, t), want = ref_index(s, t);
        if (got != want) { printf("Index %lld want %lld n=%lld m=%lld\n", (long long)got, (long long)want, (long long)n, (long long)m); bad++; }
        got = sn_string_last_index(s, t), want = ref_last(s, t);
        if (got != want) { printf("LastIndex %lld want %lld n=%lld m=%lld\n", (long long)got, (long long)want, (long long)n, (long long)m); bad++; }
        got = sn_string_count(s, t), want = ref_count(s, t);
        if (got != want) { printf("Count %lld want %lld\n", (long long)got, (long long)want); bad++; }
        sn_slice_u8 a = {sb, n, n}, b = {tb, m, m};
        if (sn_bytes_find(a, b) != ref_index(s, t)) { printf("Find\n"); bad++; }
        int64_t nn = (int64_t)(next() % 4);
        for (int64_t i = 0; i < nn; i++) nb[i] = (uint8_t)('x' + next() % 2);
        sn_string nw = {nb, nn, 0};
        int64_t limit = (int64_t)(next() % 6) - 1;
        sn_string r = sn_string_replace(s, t, nw, limit);
        int64_t len = 0, done = 0;
        for (int64_t i = 0; i < n;) {
            if (done < limit && i + m <= n && memcmp(sb + i, tb, (size_t)m) == 0) { memcpy(rb + len, nb, (size_t)nn); len += nn; i += m; done++; }
            else rb[len++] = sb[i++];
        }
        if (r.len != len || (len > 0 && memcmp(r.ptr, rb, (size_t)len) != 0)) { printf("Replace n=%lld\n", (long long)limit); bad++; }
        sn_string_drop(&r);
        sn_slice out;
        int64_t pieces = (int64_t)(next() % 5);
        sn_split_n(s, t, pieces, &out);
        int64_t at = 0, k = 0;
        sn_string *f = out.ptr;
        for (; k < out.len; k++) {
            int64_t cut = k < out.len - 1 ? ref_index((sn_string){sb + at, n - at, 0}, t) : n - at;
            if (cut < 0 || f[k].ptr != sb + at || f[k].len != cut) { printf("SplitN piece %lld\n", (long long)k); bad++; break; }
            at += cut + m;
        }
        int64_t want_pieces = pieces <= 0 ? 0 : 1 + ref_count(s, t);
        if (want_pieces > pieces) want_pieces = pieces;
        if (out.len != want_pieces) { printf("SplitN %lld want %lld\n", (long long)out.len, (long long)want_pieces); bad++; }
        sn_slice_free(&out);
    }
    // Periodic texts and long needles cut from them, altered at either
    // end or not: the comparisons fail late, so the searches hand over
    // to Two-Way, forwards and backwards, which must agree with the
    // reference too.
    static uint8_t lb[6000], lt[700];
    for (int iter = 0; iter < 3000 && bad < 10; iter++) {
        int64_t period = 1 + (int64_t)(next() % 9), n = 600 + (int64_t)(next() % 5000), m = 2 + (int64_t)(next() % 600);
        uint8_t pat[9];
        for (int64_t i = 0; i < period; i++) pat[i] = (uint8_t)('a' + next() % 3);
        for (int64_t i = 0; i < n; i++) lb[i] = pat[i % period];
        for (int64_t k = (int64_t)(next() % 4); k > 0; k--) lb[next() % (uint64_t)n] = (uint8_t)('a' + next() % 4); // a few breaks in the period
        if (m > n) m = n;
        int64_t from = (int64_t)(next() % (uint64_t)(n - m + 1));
        memcpy(lt, lb + from, (size_t)m);
        switch (next() % 3) {
        case 0: lt[m - 1] = 'z'; break; // fails at its far end, forwards
        case 1: lt[0] = 'z'; break;     // and backwards
        }
        sn_string s = {lb, n, 0}, t = {lt, m, 0};
        int64_t got = sn_string_index(s, t), want = ref_index(s, t);
        if (got != want) { printf("long Index %lld want %lld n=%lld m=%lld p=%lld\n", (long long)got, (long long)want, (long long)n, (long long)m, (long long)period); bad++; }
        got = sn_string_last_index(s, t), want = ref_last(s, t);
        if (got != want) { printf("long LastIndex %lld want %lld n=%lld m=%lld p=%lld\n", (long long)got, (long long)want, (long long)n, (long long)m, (long long)period); bad++; }
        got = sn_string_count(s, t), want = ref_count(s, t);
        if (got != want) { printf("long Count %lld want %lld\n", (long long)got, (long long)want); bad++; }
    }
    // Short cases built to end in near misses, a pattern of a block of
    // one to eight bytes repeated with one byte changed, in a text that
    // follows the block with a stray byte now and then: Two-Way on its
    // own, both ways, and the searches that may hand over to it.
    static uint8_t sb2[320], tb2[48];
    for (int iter = 0; iter < 200000 && bad < 10; iter++) {
        int64_t period = 1 + (int64_t)(next() % 8), m = 1 + (int64_t)(next() % 40), n = (int64_t)(next() % 300);
        uint8_t block[8];
        for (int64_t i = 0; i < period; i++) block[i] = (uint8_t)('a' + next() % 3);
        for (int64_t i = 0; i < m; i++) tb2[i] = block[i % period];
        tb2[next() % (uint64_t)m] = (uint8_t)('a' + next() % 3);
        for (int64_t i = 0; i < n; i++) sb2[i] = next() % 20 == 0 ? (uint8_t)('a' + next() % 3) : block[i % period];
        if (n > m && next() % 2) memcpy(sb2 + next() % (uint64_t)(n - m), tb2, (size_t)m);
        sn_string s = {sb2, n, 0}, t = {tb2, m, 0};
        int64_t want = ref_index(s, t), last = ref_last(s, t);
        if (sn_two_way(s, t) != want || sn_string_index(s, t) != want) { printf("short Index want %lld n=%lld m=%lld\n", (long long)want, (long long)n, (long long)m); bad++; }
        if (sn_two_way_last(s, t) != last || sn_string_last_index(s, t) != last) { printf("short LastIndex want %lld n=%lld m=%lld\n", (long long)last, (long long)n, (long long)m); bad++; }
        if (sn_string_count(s, t) != ref_count(s, t)) { printf("short Count\n"); bad++; }
    }
    sn_string e = {(const uint8_t *)"h\xc3\xa9!", 4, 0}, z = {(const uint8_t *)"", 0, 0}, d = {(const uint8_t *)"-", 1, 0};
    sn_string r = sn_string_replace(e, z, d, 1 << 20);
    printf("%lld %lld %lld %.*s\n", (long long)sn_string_index(e, z), (long long)sn_string_last_index(e, z), (long long)sn_string_count(e, z), (int)r.len, (const char *)r.ptr);
    sn_string_drop(&r);
    return bad;
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "search"), probe)
	out, err := testproc.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if want := "0 4 4 -h-é-!-\n"; string(out) != want {
		t.Errorf("an empty needle: got %q, want %q", out, want)
	}
}

// TestPiecesAndScans holds the appends, which copy a short piece
// without memcpy, and the JSON scan, which reads eight bytes at a time,
// to byte-by-byte references: pieces of every length to forty appended
// to a builder and to a string, and scans of random texts of quotes,
// backslashes, control characters, bytes of 0x80 and more and letters,
// from random offsets, with and without stopping at the high bytes.
func TestPiecesAndScans(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
#include <string.h>
static uint64_t seed = 88172645463325252ull;
static uint64_t next(void) { seed ^= seed << 13; seed ^= seed >> 7; seed ^= seed << 17; return seed; }
static int64_t ref_scan(sn_string s, int64_t at, bool high) {
    for (int64_t i = at; i < s.len; i++) {
        uint8_t c = (uint8_t)s.ptr[i];
        if (c == '"' || c == '\\' || c < 0x20 || (high && c >= 0x80)) return i;
    }
    return s.len;
}
int main(void) {
    static uint8_t piece[41], want[4000];
    int bad = 0;
    sn_slice_u8 built = {NULL, 0, 0};
    sn_string joined = {(const uint8_t *)"", 0, 0};
    int64_t total = 0;
    for (int64_t n = 0; n <= 40 && bad < 10; n++) {
        for (int64_t i = 0; i < n; i++) piece[i] = (uint8_t)next();
        sn_string p = {piece, n, 0};
        sn_strings_appendString(&built, p);
        sn_string_append(&joined, &p);
        memcpy(want + total, piece, (size_t)n);
        total += n;
        if (built.len != total || memcmp(built.ptr, want, (size_t)total) != 0) { printf("builder after %lld\n", (long long)n); bad++; }
        if (joined.len != total || memcmp(joined.ptr, want, (size_t)total) != 0) { printf("string after %lld\n", (long long)n); bad++; }
    }
    sn_slice_free((sn_slice *)&built);
    sn_string_drop(&joined);
    static uint8_t text[200];
    static const uint8_t specials[] = {'"', '\\', 0, 0x1f, 0x80, 0xff, 'a', 'b', ' ', '~'};
    for (int iter = 0; iter < 20000 && bad < 10; iter++) {
        int64_t n = (int64_t)(next() % 200);
        for (int64_t i = 0; i < n; i++) text[i] = next() % 8 == 0 ? specials[next() % sizeof specials] : (uint8_t)('a' + next() % 26);
        sn_string s = {text, n, 0};
        int64_t at = (int64_t)(next() % (uint64_t)(n + 2)) - 1;
        bool high = next() % 2;
        int64_t got = sn_encoding__json_scanString(s, at, high), w = ref_scan(s, at < 0 ? 0 : at, high);
        if (got != w) { printf("scan at %lld high %d: %lld want %lld\n", (long long)at, high, (long long)got, (long long)w); bad++; }
    }
    return bad;
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "pieces"), probe)
	if out, err := testproc.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestJSONBlock holds a Decoder's block to its two operations: grow
// lengthens it by zeros, keeping what it holds, in place while its
// capacity allows and else to exactly the new length, so that a block
// stopped at its limit holds no more than it was asked to; and slide
// moves a range of it to its start.
func TestJSONBlock(t *testing.T) {
	probe := `#include "internal.h"
#include <stdio.h>
#include <string.h>
int main(void) {
    int bad = 0;
    sn_slice_u8 b = {sn_alloc(8), 8, 8};
    memcpy((void *)b.ptr, "abcdefgh", 8);
    sn_encoding__json_grow(&b, 5);
    if (b.len != 13 || b.cap != 13) { printf("grown to len %lld cap %lld, want 13 13\n", (long long)b.len, (long long)b.cap); bad++; }
    if (memcmp(b.ptr, "abcdefgh\0\0\0\0\0", 13) != 0) { printf("grown block holds the wrong bytes\n"); bad++; }
    sn_slice_u8 roomy = {sn_alloc(100), 10, 100};
    memset((void *)roomy.ptr, 'x', 100);
    const uint8_t *before = roomy.ptr;
    sn_encoding__json_grow(&roomy, 20);
    if (roomy.ptr != before || roomy.len != 30 || roomy.cap != 100) { printf("grown in place: len %lld cap %lld moved %d\n", (long long)roomy.len, (long long)roomy.cap, roomy.ptr != before); bad++; }
    for (int i = 10; i < 30; i++) if (roomy.ptr[i] != 0) { printf("byte %d not zeroed\n", i); bad++; break; }
    sn_encoding__json_grow(&roomy, 0);
    sn_encoding__json_grow(&roomy, -4);
    if (roomy.len != 30) { printf("a growth by nothing changed the length to %lld\n", (long long)roomy.len); bad++; }
    memcpy((void *)b.ptr, "0123456789abc", 13);
    sn_encoding__json_slide(&b, 4, 9);
    if (memcmp(b.ptr, "45678", 5) != 0) { printf("slide moved the wrong bytes\n"); bad++; }
    sn_slice_free((sn_slice *)&b);
    sn_slice_free((sn_slice *)&roomy);
    return bad;
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "block"), probe)
	if out, err := testproc.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestSearchLinear times the searches on a text and a pattern that
// repeat with a period of sixteen, the pattern failing only at its far
// end: a search that compared the whole pattern at every candidate would
// take n times m over sixteen, seconds here, and Two-Way takes
// milliseconds.
func TestSearchLinear(t *testing.T) {
	probe := `#include "snello.h"
#include <stdio.h>
int main(void) {
    int64_t n = 20000000, m = 200001;
    uint8_t *s = sn_alloc((size_t)n), *t = sn_alloc((size_t)m);
    for (int64_t i = 0; i < n; i++) s[i] = i % 16 == 0 ? 'a' : i % 16 == 1 ? 'b' : 'c';
    for (int64_t i = 0; i < m; i++) t[i] = i % 16 == 0 ? 'a' : i % 16 == 1 ? 'b' : 'c';
    t[m - 1] = 'd';
    sn_string S = {s, n, 0}, T = {t, m, 0};
    printf("%lld %lld %lld\n", (long long)sn_string_index(S, T), (long long)sn_string_last_index(S, T), (long long)sn_string_count(S, T));
    t[m - 1] = 'c';
    t[0] = 'd';
    printf("%lld %lld\n", (long long)sn_string_index(S, T), (long long)sn_string_last_index(S, T));
    return 0;
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "linear"), probe)
	start := time.Now()
	out, err := testproc.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if want := "-1 -1 0\n-1 -1\n"; string(out) != want {
		t.Errorf("got %q, want %q", out, want)
	}

	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("five searches of a 200,001-byte pattern in 20 MB took %v; a linear search takes milliseconds", took)
	}
}

// TestRandom holds the runtime's random source against Go's. The
// thread's generator, keyed with Go's own test seed and drawn from as
// the package functions draw, gives the numbers Go's ChaCha8 gives,
// through its refills and past the points where it keys itself again;
// the 128-bit multiply gives Go's two halves. Four threads that key
// themselves each draw numbers of their own, about half of whose bits
// are set, on a system that gives random bytes and on one that gives
// none, where the key is made of the clock.
func TestRandom(t *testing.T) {
	const seed, count = "ABCDEFGHIJKLMNOPQRSTUVWXYZ123456", 3000
	pairs := [][2]uint64{{0, 0}, {1, 1}, {1<<64 - 1, 1<<64 - 1}, {1<<64 - 1, 2}, {0x123456789abcdef0, 0xfedcba9876543210}, {1 << 32, 1 << 32}, {1<<63 + 1, 3}}
	var muls strings.Builder
	for _, p := range pairs {
		fmt.Fprintf(&muls, "{%du, %du},", p[0], p[1])
	}

	// A thread's 4096 numbers hold 262,144 bits, 131,072 of them set give
	// or take 256: a count more than 1,536 away, six times that, is a
	// generator that is not even.
	probe := `#include "internal.h"
#include "probe.h"
#include <stdio.h>
static uint64_t first[4];
static int ones[4];
static void *draw(void *arg) {
    int k = (int)(intptr_t)arg;
    first[k] = sn_math__rand_random();
    for (int i = 0; i < 4096; i++) ones[k] += __builtin_popcountll(sn_math__rand_random());
    return NULL;
}
int main(int argc, char **argv) {
    (void)argv;
    sn_rand_no_system = argc > 1;
    sn_rand_key((const uint8_t *)"` + seed + `");
    for (int i = 0; i < ` + strconv.Itoa(count) + `; i++) printf("%llx\n", (unsigned long long)sn_math__rand_random());
    uint64_t pairs[][2] = {` + muls.String() + `};
    for (size_t i = 0; i < sizeof pairs / sizeof pairs[0]; i++) {
        uint64_t lo, hi = sn_math__rand_mul64(pairs[i][0], pairs[i][1], &lo);
        printf("%llx %llx\n", (unsigned long long)hi, (unsigned long long)lo);
    }
    probe_thread th[4];
    for (intptr_t k = 0; k < 4; k++) th[k] = probe_thread_start(draw, (void *)k);
    for (int k = 0; k < 4; k++) probe_thread_join(th[k]);
    int same = 0, off = 0;
    for (int a = 0; a < 4; a++) {
        for (int b = a + 1; b < 4; b++) same += first[a] == first[b];
        off += ones[a] < 131072 - 1536 || ones[a] > 131072 + 1536;
    }
    printf("%d %d\n", same, off);
}
`
	bin := buildProbe(t, filepath.Join(t.TempDir(), "random"), probe)
	var key [32]byte
	copy(key[:], seed)
	for _, source := range []string{"the system", "the clock"} {
		cmd := testproc.Command(bin)
		if source == "the clock" {
			cmd.Args = append(cmd.Args, "clock")
		}

		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("keyed from %s: %v", source, err)
		}

		lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
		if len(lines) != count+len(pairs)+1 {
			t.Fatalf("keyed from %s: got %d lines, want %d", source, len(lines), count+len(pairs)+1)
		}

		want := rand.NewChaCha8(key)
		for i := range count {
			if w := strconv.FormatUint(want.Uint64(), 16); lines[i] != w {
				t.Fatalf("number %d of the generator: got %s, want %s", i, lines[i], w)
			}
		}

		for i, p := range pairs {
			hi, lo := bits.Mul64(p[0], p[1])
			if w := fmt.Sprintf("%x %x", hi, lo); lines[count+i] != w {
				t.Errorf("%d times %d: got %s, want %s", p[0], p[1], lines[count+i], w)
			}
		}

		if got := lines[len(lines)-1]; got != "0 0" {
			t.Errorf("keyed from %s, threads that drew the same first number, and threads whose bits are not about half set: got %s, want 0 0", source, got)
		}
	}
}

// TestAllocCounts holds the counts of what sn_alloc gives out, which
// each thread keeps for itself: four threads' blocks and bytes are all
// in the sum, while the threads run and after they have ended, and a
// block of no bytes is a block.
func TestAllocCounts(t *testing.T) {
	probe := `#include "internal.h"
#include "probe.h"
#include <stdio.h>
static void *work(void *arg) {
    (void)arg;
    for (int i = 0; i < 10000; i++) sn_free(sn_alloc(48));
    return NULL;
}
int main(void) {
    int64_t b0, n0, b1, n1, b2, n2;
    sn_alloc_counts(&b0, &n0);
    probe_thread th[4];
    for (int k = 0; k < 4; k++) th[k] = probe_thread_start(work, NULL);
    for (int k = 0; k < 4; k++) probe_thread_join(th[k]);
    sn_alloc_counts(&b1, &n1);
    sn_free(sn_alloc(0));
    sn_free(sn_alloc(1000));
    sn_alloc_counts(&b2, &n2);
    printf("%lld %lld %lld %lld\n", (long long)(b1 - b0), (long long)(n1 - n0), (long long)(b2 - b1), (long long)(n2 - n1));
}
`
	for _, mode := range [][]string{{"-O1", "-DSN_DEBUG"}, {"-O2"}} {
		bin := buildProbeAs(t, filepath.Join(t.TempDir(), "counts"), probe, mode...)
		out, err := testproc.Command(bin).Output()
		if err != nil {
			t.Fatalf("%v: %v", mode, err)
		}

		if got, want := string(out), "40000 1920000 2 1000\n"; got != want {
			t.Errorf("%v: blocks and bytes of four threads, then of two blocks more: got %q, want %q", mode, got, want)
		}
	}
}

// TestAllocScales holds what allocating costs a processor when four
// threads allocate at once to what it costs one alone, against the same
// ratio for listing: the allocator's own work done on lists of the
// thread's own, the same rounds taking and giving blocks of the same
// sizes, one call each, with counts of the thread's own written as an
// allocation's are. Each thread keeps what it allocates and frees to
// itself, so four threads allocating spend four times one thread's
// processor time and little more, as four threads listing do; a word
// every allocation writes, shared by the threads, has each processor
// wait for the line of memory it is on, and four threads allocating
// then spend many times what four listing do. What the machine adds
// when four threads run, two threads sharing one core or a clock
// lowered for all four, it adds to the same work listed, so the
// comparison holds on a machine of shared cores. Processor time is
// measured, not the clock's, so that a machine busy with other tests
// does not look like a shared word, and of three runs the middle
// ratio is kept, each run's four times compared among themselves, so
// that one time a machine's clock misreports, too small or too large,
// decides nothing: a virtualized runner has reported a thread's time
// at a thousandth of its worth.
func TestAllocScales(t *testing.T) {
	if runtime.NumCPU() < 4 {
		t.Skip("fewer than four processors")
	}

	probe := `#include "snello.h"
#include "probe.h"
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
enum { rounds = 4000000, kept = 64, classes = 8, step = 16, spare = 16 };
static void *allocating(void *arg) {
    (void)arg;
    void *keep[kept] = {0};
    for (int i = 0; i < rounds; i++) {
        int k = i & (kept - 1);
        sn_free(keep[k]);
        keep[k] = sn_alloc(step + (size_t)(i & (classes - 1)) * step);
    }
    for (int k = 0; k < kept; k++) sn_free(keep[k]);
    return NULL;
}
typedef struct {
    void *head[classes];
    int64_t count[classes];
    _Atomic int64_t blocks, bytes;
} lists;
static _Thread_local lists mine;
static __attribute__((noinline)) void give(void *p, size_t n) {
    if (p == NULL) {
        return;
    }
    int c = (int)((n - 1) / step);
    *(void **)p = mine.head[c];
    mine.head[c] = p;
    if (++mine.count[c] > rounds) {
        abort();
    }
}
static __attribute__((noinline)) void *take(size_t n) {
    int c = (int)((n - 1) / step);
    void *p = mine.head[c];
    mine.head[c] = *(void **)p;
    mine.count[c]--;
    atomic_store_explicit(&mine.blocks, atomic_load_explicit(&mine.blocks, memory_order_relaxed) + 1, memory_order_relaxed);
    atomic_store_explicit(&mine.bytes, atomic_load_explicit(&mine.bytes, memory_order_relaxed) + (int64_t)n, memory_order_relaxed);
    return p;
}
static void *listing(void *arg) {
    (void)arg;
    size_t largest = (size_t)classes * step;
    unsigned char *own = malloc((size_t)classes * spare * largest);
    for (int c = 0; c < classes; c++) {
        for (int j = 0; j < spare; j++) give(own + ((size_t)c * spare + (size_t)j) * largest, (size_t)(c + 1) * step);
    }
    void *keep[kept] = {0};
    for (int i = 0; i < rounds; i++) {
        int k = i & (kept - 1);
        size_t n = step + (size_t)(i & (classes - 1)) * step;
        give(keep[k], n);
        keep[k] = take(n);
    }
    free(own);
    return NULL;
}
static double spent(void *(*work)(void *), int threads) {
    probe_thread th[8];
    int64_t a = probe_cpu_work();
    for (int k = 0; k < threads; k++) th[k] = probe_thread_start(work, NULL);
    for (int k = 0; k < threads; k++) probe_thread_join(th[k]);
    return (double)(probe_cpu_work() - a);
}
int main(void) {
    spent(allocating, 1);
    spent(listing, 1);
    double alloc1 = spent(allocating, 1), alloc4 = spent(allocating, 4);
    double list1 = spent(listing, 1), list4 = spent(listing, 4);
    printf("%.0f %.0f %.0f %.0f\n", alloc1, alloc4, list1, list4);
}
`
	bin := buildProbeAs(t, filepath.Join(t.TempDir(), "scales"), probe, "-O2")
	var ratios []float64 // each run's growth of allocating's time against listing's
	for range 3 {
		out, err := testproc.Command(bin).Output()
		if err != nil {
			t.Fatal(err)
		}

		var times [4]float64 // allocating on one thread and four, listing on one and four
		if _, err := fmt.Sscanf(string(out), "%f %f %f %f", &times[0], &times[1], &times[2], &times[3]); err != nil {
			t.Fatalf("%q: %v", out, err)
		}

		for _, v := range times {
			if !(v > 0) || math.IsInf(v, 0) {
				t.Fatalf("%q: a processor time that is not a positive number", out)
			}
		}

		allocating, listing := times[1]/(4*times[0]), times[3]/(4*times[2])
		t.Logf("four threads against one: allocating %.2f times, listing %.2f times", allocating, listing)
		ratios = append(ratios, allocating/listing)
	}

	slices.Sort(ratios)
	if middle := ratios[len(ratios)/2]; middle > allocScalesLimit {
		t.Errorf("a processor's time to allocate, four threads against one, grows %.2f times what listing's grows in the middle run, want under %.1f", middle, allocScalesLimit)
	}
}

// floatTexts writes texts for the float readers: the numbers of Go's
// own tests, then for doubles and floats of every magnitude, the
// subnormals among them, the text in the shortest form, with an
// exponent and digits to spare, in hexadecimal and as a fraction; the
// point halfway to the next one written out in full, which a reader
// must round to the even side, with a digit added, taken away and
// changed after it, which it must not, the digit added standing past
// the eight hundredth too, further than the exact reading keeps
// digits; and texts changed a byte at a time, most of them no number.
func floatTexts() []string {
	r := rand.New(rand.NewPCG(1, 2))
	texts := []string{
		"", "0", "-0", "+1", "1.5", "-2e3", "1e400", "-1e400", "1e-400", ".5", "5.", ".", "e1", "1e", "1e+", "1_000.5", "1__0", "_1", "1_", "0x1p-2", "0X1.8p1", "0x1", "0x_1p0", "0x1p", "0x.p1",
		"inf", "+Infinity", "-INF", "infinit", "infinityx", "nan", "NaN", "+nan", "-nan", "nan(1)", " 1", "1 ", "1.5x", "0b1", "1e1_0", "1e_10", "1e99999999999999999999", "1e-99999999999999999999",
		"0.000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001e100", "100000000000000000000000000000000000000000000000000000000000000000000000000000e-70",
		"0x1.fffffffffffff7fffp1023", "0x1.fffffffffffff8p1023", "0x0.0000000000001p-1022", "0x1p-1075", "0x1.00000000000008p0", "0x1.000001p0", "0x1.0000010000000000000001p0", "0x8p-1078",
		"4.9e-324", "2.4703282292062327e-324", "2.4703282292062328e-324", "1.7976931348623158e308", "1.7976931348623159e308", "3.4028235e38", "3.4028236e38", "1.401298464324817e-45", "7e-46",
	}

	for i := 0; i < 12000; i++ {
		var v float64
		switch r.IntN(4) {
		case 0:
			v = math.Float64frombits(r.Uint64())
		case 1:
			v = math.Float64frombits(r.Uint64() & (1<<52 - 1)) // a subnormal
		case 2:
			v = float64(math.Float32frombits(r.Uint32()))
		default:
			v = math.Ldexp(float64(r.Int64N(1<<53)), r.IntN(200)-100)
		}

		if v != v || math.IsInf(v, 0) {
			continue
		}

		texts = append(texts,
			strconv.FormatFloat(v, 'g', -1, 64), strconv.FormatFloat(v, 'e', r.IntN(40), 64), strconv.FormatFloat(v, 'x', -1, 64), strconv.FormatFloat(v, 'x', r.IntN(20), 64),
			strconv.FormatFloat(float64(float32(v)), 'g', -1, 32))
		if a := math.Abs(v); a > 1e-30 && a < 1e30 {
			texts = append(texts, strconv.FormatFloat(v, 'f', r.IntN(40), 64))
		}

		for _, narrow := range []bool{false, true} {
			above := math.Nextafter(v, math.Inf(1))
			if narrow {
				f := float32(v)
				v, above = float64(f), float64(math.Nextafter32(f, float32(math.Inf(1))))
			}

			if math.IsInf(v, 0) || math.IsInf(above, 0) {
				continue
			}

			half := new(big.Float).SetPrec(2200).SetFloat64(v)
			half.Add(half, new(big.Float).SetPrec(2200).SetFloat64(above))
			half.Quo(half, big.NewFloat(2))
			digits, exp, _ := strings.Cut(half.Text('e', 800), "e")
			digits = strings.TrimRight(digits, "0")
			texts = append(texts, digits+"e"+exp, digits+"1e"+exp, digits+strings.Repeat("0", r.IntN(30))+"1e"+exp)
			if i%16 == 0 {
				texts = append(texts, digits+strings.Repeat("0", 830-len(digits))+"1e"+exp)
			}
			if len(digits) > 3 {
				texts = append(texts, digits[:len(digits)-1]+"e"+exp, digits[:2+r.IntN(len(digits)-2)]+"e"+exp)
			}
		}
	}

	const alphabet = "0123456789.eEpPxX_+-infINFnaNtyY bo"
	for i, n := 0, len(texts); i < n; i += 5 {
		b := []byte(texts[i])
		if len(b) == 0 || len(b) > 60 {
			continue
		}

		for range 3 {
			c := append([]byte(nil), b...)
			switch r.IntN(3) {
			case 0:
				c[r.IntN(len(c))] = alphabet[r.IntN(len(alphabet))]
			case 1:
				j := r.IntN(len(c) + 1)
				c = append(c[:j], append([]byte{alphabet[r.IntN(len(alphabet))]}, c[j:]...)...)
			default:
				j := r.IntN(len(c))
				c = append(c[:j], c[j+1:]...)
			}

			texts = append(texts, string(c))
		}
	}

	return texts
}

// TestParseFloat holds the runtime's float readers to Go's strconv on
// the texts of floatTexts: the same texts are numbers, the same are
// past the largest float, and every other reads as the same bits, as a
// double and as a float.
func TestParseFloat(t *testing.T) {
	probe := `#include "snello.h"
#include <stdio.h>
#include <string.h>
int main(void) {
    static char line[1 << 16];
    while (fgets(line, sizeof line, stdin) != NULL) {
        size_t n = strlen(line);
        if (n > 0 && line[n - 1] == '\n') n--;
        sn_string s = {(const uint8_t *)line, (int64_t)n, 0};
        double d = 0;
        float f = 0;
        int64_t cd = sn_strconv_parseFloat(s, &d), cf = sn_strconv_parseFloat32(s, &f);
        uint64_t bd;
        uint32_t bf;
        memcpy(&bd, &d, 8);
        memcpy(&bf, &f, 4);
        printf("%lld %016llx %lld %08x\n", (long long)cd, (unsigned long long)(cd ? 0 : bd), (long long)cf, cf ? 0 : bf);
    }
    return 0;
}
`
	code := func(err error) int {
		switch {
		case err == nil:
			return 0
		case err.(*strconv.NumError).Err == strconv.ErrRange:
			return 2
		}

		return 1
	}

	texts := floatTexts()
	got := probeLines(t, "parsefloat", probe, strings.Join(texts, "\n")+"\n")
	if len(got) != len(texts) {
		t.Fatalf("the probe answered %d of %d texts", len(got), len(texts))
	}

	bad := 0
	for i, s := range texts {
		d, errD := strconv.ParseFloat(s, 64)
		f, errF := strconv.ParseFloat(s, 32)
		bd, bf := math.Float64bits(d), math.Float32bits(float32(f))
		switch { // a NaN is the one the runtime makes, and a text that does not read gives no bits
		case errD != nil:
			bd = 0
		case d != d:
			bd = 0x7ff8000000000001
		}

		switch {
		case errF != nil:
			bf = 0
		case f != f:
			bf = 0x7fc00000
		}

		if want := fmt.Sprintf("%d %016x %d %08x", code(errD), bd, code(errF), bf); got[i] != want {
			if bad++; bad <= 10 {
				t.Errorf("%.120q: got %s, want %s", s, got[i], want)
			}
		}
	}
}

// TestUnquote holds the runtime's quote reader to Go's strconv.Unquote
// on literals of the three kinds, well formed and not, made from
// pieces that meet every rule of the syntax and changed a byte at a
// time; the one text they differ on is the empty character literal,
// which Go takes for the empty string. The probe reads each text from
// a block of its own size, so that a read past it is a read past the
// block.
func TestUnquote(t *testing.T) {
	probe := `#include "snello.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
static int hex(char c) { return c <= '9' ? c - '0' : c - 'a' + 10; }
int main(void) {
    static char line[1 << 16];
    while (fgets(line, sizeof line, stdin) != NULL) {
        size_t n = strlen(line);
        if (n > 0 && line[n - 1] == '\n') n--;
        uint8_t *bytes = malloc(n / 2 + 1);
        for (size_t i = 0; i < n / 2; i++) bytes[i] = (uint8_t)(hex(line[2 * i]) << 4 | hex(line[2 * i + 1]));
        bool ok;
        sn_string out = sn_strconv_unquote((sn_string){bytes, (int64_t)(n / 2), 0}, &ok);
        printf("%d ", ok);
        for (int64_t i = 0; i < out.len; i++) printf("%02x", out.ptr[i]);
        printf("\n");
        sn_string_drop(&out);
        free(bytes);
    }
    return 0;
}
`
	r := rand.New(rand.NewPCG(3, 4))
	pieces := []string{
		"a", "b", "\\", "\"", "'", "`", "\n", "\r", "\t", "x", "u", "U", "0", "1", "7", "8", "f", "F", "g", "\x80", "\xff", "é", "日", "\U0001F600", "\xed\xa0\x80",
		`\n`, `\x41`, `\u00e9`, `\U0001F600`, `\377`, `\400`, `\ud800`, `\U00110000`, `\'`, `\"`, " ", "\x00", "\x7f",
	}

	texts := []string{"", `"`, `""`, "''", "``", "`", "'", `"a`, `a"`, `"a"b"`, "`a`b`", "'ab'", "'\xff'", "\"\xff\"", `'\''`, `'"'`, `"'"`}
	for range 60000 {
		s := ""
		for range r.IntN(8) {
			s += pieces[r.IntN(len(pieces))]
		}

		switch r.IntN(6) {
		case 0:
			s = strconv.Quote(s)
		case 1:
			s = "\"" + s + "\""
		case 2:
			s = "'" + s + "'"
		case 3:
			s = "`" + s + "`"
		case 4:
			s = strconv.QuoteRune(rune(r.IntN(0x11000)))
		}

		texts = append(texts, s)
		if b := []byte(s); len(b) > 0 && r.IntN(3) == 0 {
			b[r.IntN(len(b))] = byte(r.IntN(256))
			texts = append(texts, string(b))
		}
	}

	var in strings.Builder
	for _, s := range texts {
		in.WriteString(hex.EncodeToString([]byte(s)))
		in.WriteByte('\n')
	}

	got := probeLines(t, "unquote", probe, in.String())
	if len(got) != len(texts) {
		t.Fatalf("the probe answered %d of %d texts", len(got), len(texts))
	}

	bad := 0
	for i, s := range texts {
		want := "0 "
		if text, err := strconv.Unquote(s); err == nil && s != "''" {
			want = "1 " + hex.EncodeToString([]byte(text))
		}

		if got[i] != want {
			if bad++; bad <= 10 {
				t.Errorf("%q: got %s, want %s", s, got[i], want)
			}
		}
	}
}

// TestNumberFormats holds what the runtime writes for the verbs of a
// base, a character and an exponent, for the zero flag and for
// strconv's FormatInt and FormatFloat to Go's fmt and strconv: integers
// of every size in every base, floats of every magnitude to a
// precision in each of the three layouts, the infinities and NaN among
// them, the digits of a float to its last one that is not zero and
// past it, however many are asked for, and for the verbs a precision
// below zero or past the largest a format takes, which is held to what
// a format writes.
func TestNumberFormats(t *testing.T) {
	probe := `#include "snello.h"
#include <inttypes.h>
#include <stdio.h>
#include <string.h>
static void show(sn_string s) {
    fwrite(s.ptr, 1, (size_t)s.len, stdout); // every byte, a zero among them
    putchar('\n');
    sn_string_drop(&s);
}
int main(void) {
    char op;
    uint64_t bits;
    long long a, b;
    while (scanf(" %c %" SCNx64 " %lld %lld", &op, &bits, &a, &b) == 4) {
        double v;
        memcpy(&v, &bits, 8);
        switch (op) {
        case 'i': show(sn_int_base((int64_t)bits, a, b != 0)); break;
        case 'u': show(sn_uint_base(bits, a, b != 0)); break;
        case 'b': show(sn_strconv_formatBits(bits, a, b != 0)); break;
        case 'c': show(sn_char((int64_t)bits)); break;
        case 'e': show(sn_f64_e(v, a)); break;
        case 'g': show(sn_f64_g(v, a)); break;
        case 'f': show(sn_strconv_formatFixed(v, a)); break;
        case 'z': show(sn_pad_zero(sn_int_base((int64_t)bits, a, false), b, true)); break;
        case 'Z': show(sn_pad_zero(sn_f64_fixed(v, a), b, true)); break;
        case 'E': show(sn_pad_zero(sn_f64_e(v, a), b, true)); break;
        case 'h': {
            uint8_t raw[8];
            memcpy(raw, &bits, 8);
            show(sn_hex((sn_string){raw, a, 0}, b != 0, false));
            show(sn_hex_bytes((sn_slice){raw, a, a}, b != 0));
            break;
        }
        }
    }
    return 0;
}
`
	r := rand.New(rand.NewPCG(5, 6))
	var in strings.Builder
	var want []string
	ask := func(op byte, bits uint64, a, b int, answers ...string) {
		fmt.Fprintf(&in, "%c %x %d %d\n", op, bits, a, b)
		want = append(want, answers...)
	}

	upper := func(s string, yes int) string {
		if yes != 0 {
			return strings.ToUpper(s)
		}

		return s
	}

	ints := []uint64{0, 1, 9, 10, 35, 36, 255, 1 << 31, 1<<63 - 1, 1 << 63, 1<<64 - 1}
	for range 3000 {
		ints = append(ints, r.Uint64()>>r.IntN(64))
	}

	for _, u := range ints {
		base, up, width := []int{2, 8, 16}[r.IntN(3)], r.IntN(2), r.IntN(70)
		ask('i', u, base, up, upper(strconv.FormatInt(int64(u), base), up))
		ask('u', u, base, up, upper(strconv.FormatUint(u, base), up))
		ask('z', u, base, width, zeroPadded(strconv.FormatInt(int64(u), base), width))
		base = 2 + r.IntN(35)
		ask('b', u, base, 0, strconv.FormatUint(u, base))
		ask('b', u, base, 1, "-"+strconv.FormatUint(u, base))
		n := r.IntN(9)
		raw := make([]byte, 8)
		for i := range raw {
			raw[i] = byte(u >> (8 * i))
		}

		text := upper(hex.EncodeToString(raw[:n]), up)
		ask('h', u, n, up, text, text)
	}

	for _, c := range []int64{0, 'A', 0xE9, 0x1F642, 0x10FFFF, 0x110000, 0xD800, 0xDFFF, 0xD7FF, 0xE000, -1, math.MinInt64, math.MaxInt64, 1<<32 + 'A'} {
		ch := rune(0xFFFD)
		if c >= 0 && c <= 0x10FFFF && (c < 0xD800 || c > 0xDFFF) {
			ch = rune(c)
		}

		ask('c', uint64(c), 0, 0, string(ch))
	}

	floats := []float64{0, math.Copysign(0, -1), 1, -1, 0.1, 2.5, 3.5, 9.9996, 1e21, 1e22, 1e-7, 1e100, 1e-310, 5e-324, math.MaxFloat64, math.Inf(1), math.Inf(-1), math.NaN()}
	for range 3000 {
		floats = append(floats, math.Float64frombits(r.Uint64()), math.Ldexp(r.Float64(), r.IntN(120)-60))
	}

	for _, v := range floats {
		prec, width := r.IntN(25), r.IntN(40)
		ask('e', math.Float64bits(v), prec, 0, strconv.FormatFloat(v, 'e', prec, 64))
		ask('g', math.Float64bits(v), 1+prec, 0, strconv.FormatFloat(v, 'g', 1+prec, 64))
		ask('E', math.Float64bits(v), prec, width, fmt.Sprintf("%0*.*e", width, prec, v))
		if a := math.Abs(v); !(a > 1e40) { // a NaN among them
			ask('f', math.Float64bits(v), prec, 0, strconv.FormatFloat(v, 'f', prec, 64))
			ask('Z', math.Float64bits(v), prec, width, fmt.Sprintf("%0*.*f", width, prec, v))
		}
	}

	for _, v := range []float64{5e-324, 1.5, math.MaxFloat64, 1.0 / 3, 0.5, 2.5, 0.999, 9.5e-10, math.Inf(-1), math.NaN(), 0, math.Copysign(0, -1)} { // past a double's last digit
		for _, prec := range []int{0, 1, 9, 18, 60, 400, 1073, 1074, 1075, 1100, 5000} {
			ask('f', math.Float64bits(v), prec, 0, strconv.FormatFloat(v, 'f', prec, 64))
			ask('f', math.Float64bits(-v), prec, 0, strconv.FormatFloat(-v, 'f', prec, 64))
			if prec <= 1100 {
				ask('e', math.Float64bits(v), prec, 0, strconv.FormatFloat(v, 'e', prec, 64))
				ask('g', math.Float64bits(v), prec, 0, strconv.FormatFloat(v, 'g', prec, 64))
			}
		}
	}

	// The functions the verbs call hold their precision to what a
	// format takes: none below zero, and a million at most, whatever
	// number reaches them.
	for _, prec := range []int{-1, -5, math.MinInt64, 1000001, 1 << 40, math.MaxInt64} {
		held := min(max(prec, 0), 1000000)
		ask('Z', math.Float64bits(1.5), prec, 0, strconv.FormatFloat(1.5, 'f', held, 64))
		ask('e', math.Float64bits(-1.5), prec, 0, strconv.FormatFloat(-1.5, 'e', held, 64))
		ask('g', math.Float64bits(1.0/3), prec, 0, strconv.FormatFloat(1.0/3, 'g', held, 64))
	}

	// strconv's FormatFloat writes as many digits as it is asked for,
	// past a million too.
	for _, prec := range []int{1000001, 2500000} {
		ask('f', math.Float64bits(-1.5), prec, 0, strconv.FormatFloat(-1.5, 'f', prec, 64))
		ask('f', math.Float64bits(5e-324), prec, 0, strconv.FormatFloat(5e-324, 'f', prec, 64))
	}

	got := probeLines(t, "formats", probe, in.String())
	if len(got) != len(want) {
		t.Fatalf("the probe wrote %d lines, want %d", len(got), len(want))
	}

	bad := 0
	for i := range want {
		if got[i] != want[i] {
			if bad++; bad <= 10 {
				t.Errorf("line %d: got %.200s, want %.200s", i, got[i], want[i])
			}
		}
	}
}

// zeroPadded is the text of an integer made up to width with zeros
// after its sign, in base ten.
func zeroPadded(s string, width int) string {
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}

	for len(sign)+len(s) < width {
		s = "0" + s
	}

	return sign + s
}

// TestNumbersWhateverTheLocale reads and writes floats after the
// program's locale is set to one that writes a comma for the point, as
// a C library a program binds may set it: the runtime reads and writes
// a point all the same, since no float goes through the C library. It
// is skipped where the system has no such locale.
func TestNumbersWhateverTheLocale(t *testing.T) {
	probe := `#include "snello.h"
#include <locale.h>
#include <stdio.h>
static void show(sn_string s) {
    fwrite(s.ptr, 1, (size_t)s.len, stdout);
    putchar('\n');
    sn_string_drop(&s);
}
int main(void) {
    const char *names[] = {"de_DE.UTF-8", "de_DE.utf8", "fr_FR.UTF-8", "fr_FR.utf8", "de_DE", "fr_FR"};
    const char *set = NULL;
    for (size_t i = 0; set == NULL && i < sizeof names / sizeof names[0]; i++) set = setlocale(LC_ALL, names[i]);
    if (set == NULL || localeconv()->decimal_point[0] != ',') {
        puts("no locale");
        return 0;
    }
    double d = 0;
    float f = 0;
    printf("%lld %lld %lld\n", (long long)sn_strconv_parseFloat(SN_STR("1.5", 3), &d), (long long)sn_strconv_parseFloat32(SN_STR("2.5", 3), &f), (long long)sn_strconv_parseFloat(SN_STR("1,5", 3), &d));
    show(sn_f64_to_string(d));
    show(sn_f32_to_string(f));
    show(sn_f64_fixed(1.5, 2));
    show(sn_f64_fixed(1.5, 20));
    show(sn_f64_fixed(1e20, 3));
    show(sn_f64_g(1.5, 20));
    show(sn_f64_e(1.5, 20));
    show(sn_strconv_formatFixed(0.1, 30));
    return 0;
}
`
	got := probeLines(t, "locale", probe, "")
	if len(got) == 1 && got[0] == "no locale" {
		t.Skip("the system has no locale that writes a comma for the point")
	}

	want := []string{
		"0 0 1", "1.5", "2.5", "1.50", "1.50000000000000000000", "100000000000000000000.000", "1.5", "1.50000000000000000000e+00",
		"0.100000000000000005551115123126",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
