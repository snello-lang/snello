// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"bytes"
	"fmt"
	"math/bits"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ucd is the copy of the Unicode Character Database the tables are
// written from, checked in so that the tests read it offline.
var ucd = filepath.Join("testdata", "ucd", "18.0.0")

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
	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}

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

	bin := filepath.Join(dir, "probe")
	if b, err := exec.Command(cc, "-std=c11", "-o", bin, filepath.Join(dir, "probe.c"), filepath.Join(dir, "unicode.c")).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}

	got, err := exec.Command(bin).Output()
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
    done = sn_chan_make(sizeof(int64_t), 1, NULL); // buffered: the sender is done with it once its value is in
    sn_spawn(run, NULL);
    int64_t code;
    sn_chan_recv(done, &code);
    sn_chan_drop(&done); // the task's work is over: the last handle frees the channel
    return sn_finish();
}
`
	bin := buildProbe(t, filepath.Join(dir, "build"), probe)
	out, err := exec.Command(bin, root).CombinedOutput()
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
	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}

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

	if err := os.WriteFile(filepath.Join(dir, "probe.c"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "probe")
	args := []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-Wno-tautological-compare", "-fwrapv", "-fno-strict-aliasing", "-pthread", "-O1", "-DSN_DEBUG", "-o", bin, filepath.Join(dir, "probe.c")}
	args = append(args, Paths(dir)...)
	args = append(args, "-lm")
	if b, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}

	return bin
}

// probeLines runs a probe built against the whole runtime with input
// on its standard input and returns its output lines.
func probeLines(t *testing.T, name, src, input string, args ...string) []string {
	t.Helper()
	bin := buildProbe(t, filepath.Join(t.TempDir(), name), src)
	cmd := exec.Command(bin, args...)
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
		out, err := exec.Command(bin, mode).CombinedOutput()
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
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if want := "0 4 4 -h-é-!-\n"; string(out) != want {
		t.Errorf("an empty needle: got %q, want %q", out, want)
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
	out, err := exec.Command(bin).CombinedOutput()
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
#include <pthread.h>
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
    pthread_t th[4];
    for (intptr_t k = 0; k < 4; k++) pthread_create(&th[k], NULL, draw, (void *)k);
    for (int k = 0; k < 4; k++) pthread_join(th[k], NULL);
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
		cmd := exec.Command(bin)
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
