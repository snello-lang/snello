// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// targetAttributes keeps, of the attributes clang wrote for a
// function, the ones that name its processor, whichever are there and
// in one order, and nothing of a module that has none.
func TestTargetAttributes(t *testing.T) {
	tests := []struct{ module, want string }{
		{
			"define void @f() #0 {\n  ret void\n}\n\nattributes #0 = { nounwind uwtable \"frame-pointer\"=\"non-leaf\" \"target-cpu\"=\"apple-m1\" \"target-features\"=\"+aes,+neon,+v8a\" }\n",
			`"target-cpu"="apple-m1" "target-features"="+aes,+neon,+v8a"`,
		},
		{
			"attributes #0 = { \"tune-cpu\"=\"generic\" \"target-features\"=\"+cx8,+sse2\" \"target-cpu\"=\"x86-64\" }\nattributes #1 = { \"target-cpu\"=\"other\" }\n",
			`"target-cpu"="x86-64" "target-features"="+cx8,+sse2" "tune-cpu"="generic"`,
		},
		{"attributes #0 = { nounwind }\n", ""},
		{"define void @f() {\n  ret void\n}\n", ""},
		{"attributes #0 = { \"target-cpu\"=\"cut", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := targetAttributes([]byte(tt.module)); got != tt.want {
			t.Errorf("targetAttributes(%q) = %q, want %q", tt.module, got, tt.want)
		}
	}
}

// target asks clang for the host's processor and keeps the answer in
// the cache directory: asked again, by a process that has not asked
// before, it reads the file and runs no clang; a file that does not
// hold an answer is passed over and written anew; and it gives the
// answer for another system than the host's.
func TestTarget(t *testing.T) {
	if _, err := exec.LookPath(Compiler()); err != nil {
		t.Skip("no clang")
	}

	host := triple("", "")
	dir := t.TempDir()
	t.Setenv("SNELLO_CACHE", dir)
	targets.Clear()
	got, err := target(host)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, `"target-cpu"="`) {
		t.Errorf("the host's target names no processor: %q", got)
	}

	kept, _ := filepath.Glob(filepath.Join(dir, "target", "*"))
	if len(kept) != 1 {
		t.Fatalf("the cache holds %d answers, want 1: %v", len(kept), kept)
	}

	const marked = `"target-cpu"="read from the file"`
	if err := os.WriteFile(kept[0], []byte(marked), 0o644); err != nil {
		t.Fatal(err)
	}

	targets.Clear() // another process's, as far as this one knows
	if again, err := target(host); err != nil || again != marked {
		t.Errorf("asked again: %q, %v, want the file's %q", again, err, marked)
	}

	for _, spoiled := range []string{"garbage \"", "", `"target-cpu"="cut`, "\"target-cpu\"=\"a\"\n\"x\""} {
		if err := os.WriteFile(kept[0], []byte(spoiled), 0o644); err != nil {
			t.Fatal(err)
		}

		targets.Clear()
		if again, err := target(host); err != nil || again != got {
			t.Errorf("with %q in the file: %q, %v, want clang's %q", spoiled, again, err, got)
		}

		if b, _ := os.ReadFile(kept[0]); string(b) != got {
			t.Errorf("with %q in the file: the file is left holding %q, want %q", spoiled, b, got)
		}
	}

	t.Setenv("SNELLO_CACHE", "off")
	targets.Clear()
	if off, err := target(host); err != nil || off != got {
		t.Errorf("with the cache off: %q, %v, want %q", off, err, got)
	}

	targets.Clear()
	other, err := target(triple("amd64", "linux"))
	if err != nil {
		t.Skipf("this clang does not compile for linux on amd64: %v", err)
	}

	if !strings.Contains(other, `"target-cpu"="x86-64"`) {
		t.Errorf("for linux on amd64: %q, want the x86-64 processor", other)
	}
}

// validTarget takes an answer as targetAttributes writes one and
// nothing else.
func TestValidTarget(t *testing.T) {
	tests := []struct {
		attrs string
		want  bool
	}{
		{`"target-cpu"="apple-m1" "target-features"="+aes,+neon"`, true},
		{`"target-cpu"="x86-64" "target-features"="+cx8" "tune-cpu"="generic"`, true},
		{`"target-features"="+neon"`, true},
		{``, false},
		{`garbage "`, false},
		{`"target-cpu"="cut`, false},
		{`"target-cpu"="a" nounwind`, false},
		{`"target-features"="+neon" "target-cpu"="a"`, false},
		{"\"target-cpu\"=\"a\"\n", false},
	}
	for _, tt := range tests {
		if got := validTarget(tt.attrs); got != tt.want {
			t.Errorf("validTarget(%q) = %t, want %t", tt.attrs, got, tt.want)
		}
	}
}

// Every function the emitter writes refers to the module's attribute
// group, which is the module's last line and holds a mark; Targeted fills it
// with the target's processor, once, and leaves alone a module whose
// group is filled or that has none.
func TestTargeted(t *testing.T) {
	const src = "package main\nimport \"fmt\"\nvar table = []int{1, 2}\nfunc twice(n int) int { return n * 2 }\nfunc main() {\n\tfmt.Println(twice(len(table)), \"a\".Clone())\n}\n"
	module, err := emit(t, "target", src, Config{})
	if err != nil {
		t.Fatal(err)
	}

	defines := 0
	for line := range bytes.Lines(module) {
		if bytes.HasPrefix(line, []byte("define ")) {
			defines++
			if !bytes.HasSuffix(line, []byte(") #0 {\n")) {
				t.Errorf("a function names no attributes: %s", line)
			}
		}
	}

	if defines < 4 {
		t.Errorf("the module defines %d functions, want main, twice, the variable's and a helper at least", defines)
	}

	if !bytes.HasSuffix(module, []byte("\n"+emptyGroup)) {
		t.Fatalf("the module does not end with its unfilled attributes:\n%s", module[max(0, len(module)-200):])
	}

	plain := []byte("define void @f() {\n  ret void\n}\n")
	if same, err := Targeted(plain); err != nil || !bytes.Equal(same, plain) {
		t.Errorf("a module with no group was changed, or refused: %v", err)
	}

	if _, err := exec.LookPath(Compiler()); err != nil {
		t.Skip("no clang")
	}

	filled, err := Targeted(module)
	if err != nil {
		t.Fatal(err)
	}

	body, group, _ := bytes.Cut(filled, []byte("\nattributes #0 = { "))
	if !bytes.Contains(group, []byte(noJumpTables)) {
		t.Errorf("the group does not keep switches out of jump tables: %s", group)
	}

	if !bytes.Equal(body, module[:len(module)-len("\n"+emptyGroup)]) || !bytes.HasPrefix(group, []byte(`"target-cpu"="`)) || !bytes.HasSuffix(group, []byte("\" }\n")) {
		t.Errorf("the filled module is not the module with its group naming a processor:\n%s", filled[max(0, len(filled)-300):])
	}

	if again, err := Targeted(filled); err != nil || !bytes.Equal(again, filled) {
		t.Errorf("a filled module was filled again, or refused: %v", err)
	}

	t.Setenv("CLANG", filepath.Join(t.TempDir(), "no-such-clang"))
	if _, err := Targeted(module); err == nil || !strings.Contains(err.Error(), "what processor") {
		t.Errorf("with no clang to ask: %v, want an error that says what was asked", err)
	}
}

// A release build writes a function of the runtime into the program
// in place of a call of it: the cube root's one-line function, called
// from the program alone, is in a debug build's binary, which links
// the runtime as it was compiled, and gone from a release build's,
// where every call of it became the math library's.
func TestRuntimeInlined(t *testing.T) {
	const src = "package main\nimport (\"fmt\"; \"math\"; \"os\")\nfunc main() {\n\tfmt.Println(math.Cbrt(f64(len(os.Args)) * 27))\n}\n"
	for _, debug := range []bool{true, false} {
		module, err := emit(t, "inlined", src, Config{Debug: debug})
		if err != nil {
			t.Fatal(err)
		}

		bin, out, err := Build(t.TempDir(), "inlined", module, debug)
		if err != nil {
			if _, missing := exec.LookPath(Compiler()); missing != nil {
				t.Skip("no clang")
			}

			t.Fatalf("%v\n%s", err, out)
		}

		data, err := os.ReadFile(bin)
		if err != nil {
			t.Fatal(err)
		}

		if has := bytes.Contains(data, []byte("sn_math_Cbrt")); has != debug {
			t.Errorf("debug=%t: the binary holds the runtime's sn_math_Cbrt: %t, want %t; a release build that holds it was linked with a module whose functions do not name the runtime's processor", debug, has, debug)
		}
	}
}
