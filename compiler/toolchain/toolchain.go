// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package toolchain checks what a build needs from the system it runs
// on: clang, which reads the IR the LLVM backend writes, a link with
// link-time optimization, which a release build links with, and a C
// compiler, which the C backend and a debug build compile with; and
// says, for each thing missing, the command that installs it on that
// system. Each check tries the thing, as a build would use it, rather
// than read a version number, since vendors number their clang each
// their own way. The backends find their compilers through it, report
// one that cannot be started or cannot run as its errors say, and the
// driver's doctor runs every check.
package toolchain

import (
	"bufio"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	// The compilers a build runs, by the names it looks for them under
	// on the path, unless the environment variables CLANG and CC name
	// others, as every build tool reads them; pathEnv is the path, and
	// commandName the snello command, whose directory the path should
	// hold.
	clangName   = "clang"
	ccName      = "cc"
	ClangEnv    = "CLANG"
	CCEnv       = "CC"
	pathEnv     = "PATH"
	commandName = "snello"

	// MinClang is the oldest clang that reads the IR the LLVM backend
	// writes, whose pointers are opaque, ptr; irProbe is a module of
	// that IR, which the clang check compiles.
	MinClang = 15
	irProbe  = "define ptr @snello_probe(ptr %p) {\n  ret ptr %p\n}\n"

	// cProbe is a program of C, which the C compiler check compiles and
	// the link check links with link-time optimization, and the probes'
	// files, written into a directory of their own.
	cProbe     = "int main(void) { return 0; }\n"
	irFile     = "probe.ll"
	cFile      = "probe.c"
	irObject   = "probe-ir.o"
	cObject    = "probe-c.o"
	binaryFile = "probe"
	probeDir   = "snello-doctor"
	probeMode  = 0o644

	// The flags the probes run the compilers with, as a build runs them;
	// irLanguage makes a compiler read the probe as IR, which one that
	// reads no IR refuses rather than pass the file to its linker.
	versionFlag  = "--version"
	compileFlag  = "-c"
	outputFlag   = "-o"
	optimizeFlag = "-O2"
	ltoFlag      = "-flto"
	languageFlag = "-x"
	irLanguage   = "ir"

	// The systems whose install command a fix names, by runtime.GOOS
	// and by the ID and ID_LIKE of os-release, which a Linux system
	// describes itself in.
	darwin = "darwin"
	linux  = "linux"
	debian = "debian"
	ubuntu = "ubuntu"
	fedora = "fedora"
	arch   = "arch"

	// The keys of os-release a system is named by, and what may quote a
	// value; lineEnd ends a line a compiler prints.
	idKey     = "ID="
	idLikeKey = "ID_LIKE="
	quotes    = `"'`
	lineEnd   = "\n"

	// Why a compiler cannot be used, as MissingError says it: it is on
	// no directory of the path, it is found only through a relative
	// directory of the path, which a build does not run from, or it is
	// there and fails to run; reasonStart opens what follows a reason.
	notFound    = "not found"
	foundByDot  = "found only through a relative directory of PATH, which a build does not run from"
	failsToRun  = "cannot run"
	reasonStart = ": "
)

// osReleaseFiles are where a Linux system describes itself, in the
// order os-release(5) says to read them: the second only when the first
// does not exist.
var osReleaseFiles = []string{"/etc/os-release", "/usr/lib/os-release"}

// installs are the commands that install clang, which brings a C
// compiler with it, on each system the fixes name.
var installs = map[string]string{
	darwin: "xcode-select --install",
	debian: "sudo apt install clang",
	ubuntu: "sudo apt install clang",
	fedora: "sudo dnf install clang",
	arch:   "sudo pacman -S clang",
}

// A Check is one thing a build needs: what is checked, whether it
// passed, what was found or what failed, how to fix it when it did, and
// whether a build fails without it, or only something beside the build.
type Check struct {
	Name   string
	OK     bool
	Detail string
	Fix    string
	Needed bool
}

// A MissingError is a compiler a build runs that it cannot use: Name,
// which the variable Env may name instead, is not found, or is found and
// fails to run, which Reason then says, in the compiler's words when it
// gave any. It says how to install a compiler here.
type MissingError struct {
	Name, Env, Reason string
}

// Error says which compiler cannot be used, why, and what to do: for
// example, clang not found: install it with xcode-select --install, or
// set CLANG.
func (e *MissingError) Error() string {
	return e.what() + reasonStart + installFix(e.Env)
}

// Clang returns the clang a build runs: the one CLANG names, else clang
// on the path.
func Clang() string {
	return compiler(ClangEnv, clangName)
}

// CC returns the C compiler a build runs: the one CC names, else cc on
// the path.
func CC() string {
	return compiler(CCEnv, ccName)
}

// EnvOf returns the variable that names the compiler a build runs as
// name: CLANG for clang's, else CC.
func EnvOf(name string) string {
	if name == Clang() {
		return ClangEnv
	}

	return CCEnv
}

// Require returns nil when the compiler name, which the variable env may
// name instead, can be started, and else the *MissingError a build
// reports in place of the system's error. It looks the compiler up on
// the path once.
func Require(name, env string) error {
	if _, err := exec.LookPath(name); err != nil {
		e := &MissingError{Name: name, Env: env}
		if errors.Is(err, exec.ErrDot) {
			e.Reason = foundByDot
		}

		return e
	}

	return nil
}

// Unusable returns the *MissingError for the compiler name, which the
// variable env may name instead, that was found and failed to run, err
// the failure and output what it printed: the first line of its words,
// which say why, such as that a system's compiler is not installed
// behind the command that starts it, or the failure when it said none.
func Unusable(name, env string, output []byte, err error) error {
	return &MissingError{Name: name, Env: env, Reason: failsToRun + " (" + said(output, err) + ")"}
}

// Install returns the command that installs clang on the system the
// compiler runs on, or what to do where it knows none. It reads
// os-release on Linux.
func Install() string {
	return installFor(system())
}

// Checks runs every check, in the order the doctor prints them: clang,
// a link with link-time optimization, the C compiler, and whether
// running snello by name runs self, the command running, which only
// that needs. It runs the compilers on small programs it writes into a
// temporary directory, which it removes; a directory it cannot make is
// the one check it reports.
func Checks(self string) []Check {
	dir, err := os.MkdirTemp("", probeDir)
	if err != nil {
		return []Check{{Name: "a temporary directory", Detail: err.Error(), Fix: "set TMPDIR to a directory that can be written", Needed: true}}
	}

	defer os.RemoveAll(dir)
	clang := checkClang(dir)
	return []Check{clang, checkLTO(dir, clang.OK), checkCC(dir), checkPath(self)}
}

// what says which compiler e is about and why it cannot be used: the
// variable that names it when one does, and the reason, not found when
// e has none.
func (e *MissingError) what() string {
	reason := notFound
	if e.Reason != "" {
		reason = e.Reason
	}

	if os.Getenv(e.Env) != "" {
		return e.Env + " names " + e.Name + ", " + reason
	}

	return e.Name + " " + reason
}

// compiler returns the compiler env names, else name.
func compiler(env, name string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}

	return name
}

// checkClang finds clang, asks its version, and compiles a module of
// the IR the backend writes with it, in dir, as IR whatever the file's
// name: a clang older than MinClang, which cannot, is named with its
// version.
func checkClang(dir string) Check {
	c := Check{Name: "clang", Needed: true}
	clang := Clang()
	version, err := usable(clang, ClangEnv)
	if err != nil {
		c.Detail, c.Fix = failure(err), installFix(ClangEnv)
		return c
	}

	src, obj := filepath.Join(dir, irFile), filepath.Join(dir, irObject)
	if err := os.WriteFile(src, []byte(irProbe), probeMode); err != nil {
		c.Detail = err.Error()
		return c
	}

	if out, err := exec.Command(clang, languageFlag, irLanguage, compileFlag, outputFlag, obj, src).CombinedOutput(); err != nil || !exists(obj) {
		c.Detail = version + " cannot read the IR Snello writes, which needs clang " + strconv.Itoa(MinClang) + " or later" + reasonStart + said(out, err)
		c.Fix = "install a newer clang with " + Install() + ", or set " + ClangEnv + " to one"
		return c
	}

	c.OK, c.Detail = true, version
	return c
}

// checkLTO links a program with link-time optimization, as a release
// build links, in dir, when clang is there to link it.
func checkLTO(dir string, haveClang bool) Check {
	c := Check{Name: "link-time optimization", Needed: true}
	if !haveClang {
		c.Detail, c.Fix = "not tried without clang", "install clang first"
		return c
	}

	src := filepath.Join(dir, cFile)
	if err := os.WriteFile(src, []byte(cProbe), probeMode); err != nil {
		c.Detail = err.Error()
		return c
	}

	if out, err := exec.Command(Clang(), optimizeFlag, ltoFlag, outputFlag, filepath.Join(dir, binaryFile), src).CombinedOutput(); err != nil {
		c.Detail = "clang cannot link with " + ltoFlag + reasonStart + said(out, err)
		c.Fix = "install the linker clang links with, which " + Install() + " brings"
		return c
	}

	c.OK, c.Detail = true, "clang links with "+ltoFlag
	return c
}

// checkCC finds the C compiler, asks its version, and compiles a
// program of C with it, in dir, as the C backend and a debug build
// compile.
func checkCC(dir string) Check {
	c := Check{Name: "C compiler", Needed: true}
	cc := CC()
	version, err := usable(cc, CCEnv)
	if err != nil {
		c.Detail, c.Fix = failure(err), installFix(CCEnv)
		return c
	}

	src := filepath.Join(dir, cFile)
	if err := os.WriteFile(src, []byte(cProbe), probeMode); err != nil {
		c.Detail = err.Error()
		return c
	}

	if out, err := exec.Command(cc, compileFlag, outputFlag, filepath.Join(dir, cObject), src).CombinedOutput(); err != nil {
		c.Detail = version + " cannot compile C" + reasonStart + said(out, err)
		c.Fix = installFix(CCEnv)
		return c
	}

	c.OK, c.Detail = true, version
	return c
}

// checkPath reports whether running snello by name runs self, the
// command running: the first snello on the path is the same file,
// however links lead to it. Only running it by name needs that, and no
// build does.
func checkPath(self string) Check {
	c := Check{Name: "snello on the path"}
	found, err := exec.LookPath(commandName)
	if err == nil && sameFile(found, self) {
		c.OK, c.Detail = true, filepath.Dir(found)
		return c
	}

	c.Detail, c.Fix = filepath.Dir(self)+" is not on the path", "add it to "+pathEnv+" to run snello by name"
	return c
}

// usable returns the version a compiler says it is, the first line of
// its --version, when it can be started and runs, and else the
// *MissingError that says which it cannot.
func usable(name, env string) (string, error) {
	if err := Require(name, env); err != nil {
		return "", err
	}

	out, err := exec.Command(name, versionFlag).CombinedOutput()
	if err != nil {
		return "", Unusable(name, env, out, err)
	}

	return firstOf(string(out)), nil
}

// failure returns what a check says of a compiler it cannot use: which
// it is and why, without the fix, which the check says apart.
func failure(err error) string {
	var e *MissingError
	if errors.As(err, &e) {
		return e.what()
	}

	return err.Error()
}

// said returns the first line a failed command printed, or its failure
// when it printed nothing, a crash among them.
func said(out []byte, err error) string {
	if line := firstOf(string(out)); line != "" {
		return line
	}

	if err != nil {
		return err.Error()
	}

	return ""
}

// exists reports whether path names a file.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// sameFile reports whether a and b name one file.
func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}

	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}

// system names the system the compiler runs on as installs keys it:
// darwin, or the Linux distribution os-release names, by its ID or by
// one it is like; "" for any other.
func system() string {
	if runtime.GOOS == darwin {
		return darwin
	}

	if runtime.GOOS != linux {
		return ""
	}

	return release(osReleaseFiles)
}

// release returns the distribution the first of files that exists
// names, as os-release(5) says to read them: a file that exists and
// does not read names none, and the next is not read.
func release(files []string) string {
	for _, name := range files {
		f, err := os.Open(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return ""
		}

		defer f.Close()
		return distribution(f)
	}

	return ""
}

// installFor returns the command that installs clang on the system
// named as installs keys it, or what to do on one it knows none for.
func installFor(system string) string {
	if cmd, ok := installs[system]; ok {
		return cmd
	}

	return "your system's package manager, clang " + strconv.Itoa(MinClang) + " or later"
}

// installFix returns the fix for a compiler a build cannot use: the
// command that installs clang here, which brings a C compiler, or env,
// which names another compiler.
func installFix(env string) string {
	return "install it with " + Install() + ", or set " + env
}

// distribution returns the distribution an os-release file, read from
// r, names that installs knows: its ID, else the first of its ID_LIKE
// that installs knows, the closest first, as the file lists them.
func distribution(r io.Reader) string {
	var id string
	var like []string
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := s.Text()
		switch {
		case strings.HasPrefix(line, idKey):
			id = strings.Trim(strings.TrimPrefix(line, idKey), quotes)
		case strings.HasPrefix(line, idLikeKey):
			like = strings.Fields(strings.Trim(strings.TrimPrefix(line, idLikeKey), quotes))
		}
	}

	if s.Err() != nil {
		return "" // a file that does not read names no system
	}

	for _, d := range append([]string{id}, like...) {
		if _, ok := installs[d]; ok {
			return d
		}
	}

	return ""
}

// firstOf returns the first line of s, trimmed.
func firstOf(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), lineEnd)
	return line
}
