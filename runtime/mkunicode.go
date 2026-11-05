// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// Mkunicode writes c/unicode.c, the runtime's tables of Unicode, from
// the Unicode Character Database: the simple case mappings of
// UnicodeData.txt, the White_Space runes of PropList.txt, and the
// printable runes, those whose general category is a letter, a mark, a
// number, a punctuation or a symbol, with the ASCII space. Snello's
// strings change case, find white space and quote text by these, so
// they follow the Unicode standard itself, at the version named here,
// whatever toolchain builds the compiler.
//
// It reads the two files from the directory -ucd names, or fetches them
// from unicode.org for -version when no directory is given.
//
//	go run mkunicode.go
//	go run mkunicode.go -ucd /path/to/ucd -o /tmp/unicode.c
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// maxRune is the largest code point.
const maxRune = 0x10FFFF

// A run is the runes lo, lo+stride, ... up to hi, each mapped to itself
// plus delta.
type run struct {
	lo, hi, stride, delta int32
}

// runs collects the runes that f changes into runs of one stride and
// one delta: stride 1 for a block mapped whole, 2 for the blocks where
// upper and lower case alternate.
func runs(f func(rune) rune) []run {
	var out []run
	for r := rune(0); r <= maxRune; r++ {
		d := f(r) - r
		if d == 0 {
			continue
		}

		if n := len(out); n > 0 {
			last := &out[n-1]
			gap := r - last.hi
			skipsNothing := gap == 1 || gap == 2 && f(r-1) == r-1 // a stride of 2 steps over a rune f leaves alone, so no two runs overlap
			if last.delta == d && skipsNothing && (last.hi == last.lo || gap == last.stride) {
				if last.hi == last.lo {
					last.stride = gap
				}

				last.hi = r
				continue
			}
		}

		out = append(out, run{r, r, 1, d})
	}

	return out
}

// database is what the tables are made of, read from the two files.
type database struct {
	upper, lower map[rune]rune
	category     map[rune]string
	space        map[rune]bool
}

// open returns the named file of the database, from dir or fetched.
func open(dir, version, name string) io.ReadCloser {
	if dir != "" {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			log.Fatal(err)
		}

		return f
	}

	url := "https://www.unicode.org/Public/" + version + "/ucd/" + name
	resp, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("%s: %s", url, resp.Status)
	}

	return resp.Body
}

func hex(s string) rune {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 16, 32)
	if err != nil {
		log.Fatalf("bad code point %q", s)
	}

	return rune(n)
}

// read loads UnicodeData.txt and PropList.txt. A range the data gives as
// a First and a Last line has the category of its ends for every rune
// between, and no case mappings.
func read(dir, version string) database {
	db := database{map[rune]rune{}, map[rune]rune{}, map[rune]string{}, map[rune]bool{}}
	data := open(dir, version, "UnicodeData.txt")
	defer data.Close()
	lines := bufio.NewScanner(data)
	first := rune(-1)
	for lines.Scan() {
		f := strings.Split(lines.Text(), ";")
		if len(f) < 15 {
			continue
		}

		r, name, gc := hex(f[0]), f[1], f[2]
		switch {
		case strings.HasSuffix(name, ", First>"):
			first = r
			continue
		case strings.HasSuffix(name, ", Last>"):
			for c := first; c <= r; c++ {
				db.category[c] = gc
			}

			continue
		}

		db.category[r] = gc
		if f[12] != "" {
			db.upper[r] = hex(f[12])
		}

		if f[13] != "" {
			db.lower[r] = hex(f[13])
		}
	}

	if err := lines.Err(); err != nil {
		log.Fatal(err)
	}

	props := open(dir, version, "PropList.txt")
	defer props.Close()
	lines = bufio.NewScanner(props)
	for lines.Scan() {
		line, _, _ := strings.Cut(lines.Text(), "#")
		codes, prop, ok := strings.Cut(line, ";")
		if !ok || strings.TrimSpace(prop) != "White_Space" {
			continue
		}

		lo, hi, isRange := strings.Cut(strings.TrimSpace(codes), "..")
		if !isRange {
			hi = lo
		}

		for c := hex(lo); c <= hex(hi); c++ {
			db.space[c] = true
		}
	}

	if err := lines.Err(); err != nil {
		log.Fatal(err)
	}

	return db
}

// printable reports whether r is a letter, a mark, a number, a
// punctuation or a symbol, or the ASCII space.
func (db database) printable(r rune) bool {
	if r == ' ' {
		return true
	}

	switch db.category[r][:min(1, len(db.category[r]))] {
	case "L", "M", "N", "P", "S":
		return true
	}

	return false
}

func table(b *bytes.Buffer, name string, rs []run) {
	fmt.Fprintf(b, "static const sn_case_run %s[%d] = {\n", name, len(rs))
	for _, r := range rs {
		fmt.Fprintf(b, "    {0x%04X, 0x%04X, %d, %d},\n", r.lo, r.hi, r.stride, r.delta)
	}

	b.WriteString("};\n\n")
}

func main() {
	out := flag.String("o", "c/unicode.c", "the file to write")
	dir := flag.String("ucd", "", "a directory holding UnicodeData.txt and PropList.txt; fetched from unicode.org when empty")
	version := flag.String("version", "17.0.0", "the version of the Unicode Character Database")
	flag.Parse()
	db := read(*dir, *version)
	mapping := func(m map[rune]rune) func(rune) rune {
		return func(r rune) rune {
			if to, ok := m[r]; ok {
				return to
			}

			return r
		}
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, `// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Code generated by mkunicode.go from the Unicode Character Database
// %s; DO NOT EDIT.

#include "snello.h"

// A case run is the runes lo, lo+stride, ... up to hi, each mapped to
// itself plus delta.
typedef struct {
    int32_t lo, hi, stride, delta;
} sn_case_run;

`, *version)
	table(&b, "upper", runs(mapping(db.upper)))
	table(&b, "lower", runs(mapping(db.lower)))
	var spaces []rune
	for r := rune(0x80); r <= maxRune; r++ {
		if db.space[r] {
			spaces = append(spaces, r)
		}
	}

	fmt.Fprintf(&b, "// spaces are the runes above ASCII with the White_Space property.\nstatic const int32_t spaces[%d] = {", len(spaces))
	for i, r := range spaces {
		if i > 0 {
			b.WriteString(", ")
		}

		fmt.Fprintf(&b, "0x%04X", r)
	}

	b.WriteString("};\n\n// printable holds the runes above ASCII that are printable, in ranges.\nstatic const int32_t printable[][2] = {\n")
	for r := rune(0x80); r <= maxRune; r++ {
		if !db.printable(r) {
			continue
		}

		lo := r
		for r+1 <= maxRune && db.printable(r+1) {
			r++
		}

		fmt.Fprintf(&b, "    {0x%04X, 0x%04X},\n", lo, r)
	}

	b.WriteString(`};

// mapped finds r in the runs and returns what it maps to, or r.
static int32_t mapped(const sn_case_run *runs, int64_t n, int32_t r) {
    int64_t lo = 0, hi = n;
    while (lo < hi) {
        int64_t m = lo + (hi - lo) / 2;
        if (r > runs[m].hi) {
            lo = m + 1;
        } else if (r < runs[m].lo) {
            hi = m;
        } else {
            return (r - runs[m].lo) % runs[m].stride == 0 ? r + runs[m].delta : r;
        }
    }

    return r;
}

int32_t sn_rune_upper(int32_t r) {
    if (r < 0x80) {
        return r >= 'a' && r <= 'z' ? r - 32 : r;
    }

    return mapped(upper, sizeof upper / sizeof upper[0], r);
}

int32_t sn_rune_lower(int32_t r) {
    if (r < 0x80) {
        return r >= 'A' && r <= 'Z' ? r + 32 : r;
    }

    return mapped(lower, sizeof lower / sizeof lower[0], r);
}

bool sn_rune_space(int32_t r) {
    if (r < 0x80) {
        return r == ' ' || (r >= '\t' && r <= '\r');
    }

    for (size_t i = 0; i < sizeof spaces / sizeof spaces[0]; i++) {
        if (spaces[i] == r) {
            return true;
        }
    }

    return false;
}

bool sn_isprint(int32_t r) {
    if (r < 0x80) {
        return r >= 0x20 && r < 0x7F;
    }

    int64_t lo = 0, hi = (int64_t)(sizeof printable / sizeof printable[0]) - 1;
    while (lo <= hi) {
        int64_t mid = (lo + hi) / 2;
        if (r < printable[mid][0]) {
            hi = mid - 1;
        } else if (r > printable[mid][1]) {
            lo = mid + 1;
        } else {
            return true;
        }
    }

    return false;
}
`)
	if err := os.WriteFile(*out, b.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}
