// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// Mkunicode writes c/unicode.c, the runtime's tables of Unicode, from
// the Unicode Character Database, and the small lookups that read
// them: the general category of every code point and the classes made
// of it; the simple case mappings, title case and the simple case
// folding; the mappings of SpecialCasing.txt that hold in every
// language and the full case folding; the Cased and Case_Ignorable
// properties the final sigma needs; the derived properties Alphabetic,
// Uppercase, Lowercase, XID_Start and XID_Continue, and Emoji; the
// decimal digit values and the numeric values, the ideographs' from
// Unihan among them; the White_Space runes and the
// printable ones; the script of every code point and its four-letter
// code; the normalization quick-check values; the combining
// classes, decompositions and canonical compositions of normalization;
// and the grapheme, word and sentence break properties of text
// segmentation, with Extended_Pictographic and Indic_Conjunct_Break.
// The algorithms over them are written by hand in rune.c, norm.c and
// segment.c, so this file holds data and lookups and nothing else.
// Snello's text follows the Unicode standard itself, at the version
// named here, whatever toolchain builds the compiler.
//
// It reads the files from the directory -ucd names, by default the copy
// checked in under testdata/ucd/VERSION, which the runtime's tests read
// as well; -ucd "" fetches them from unicode.org instead.
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
	"slices"
	"strconv"
	"strings"
)

// maxRune is the largest code point.
const maxRune = 0x10FFFF

// The values of the enumerated properties, in the order the runtime's
// internal.h numbers them; the first of each is the default, zero.
var (
	categories     = []string{"Cn", "Lu", "Ll", "Lt", "Lm", "Lo", "Mn", "Mc", "Me", "Nd", "Nl", "No", "Pc", "Pd", "Ps", "Pe", "Pi", "Pf", "Po", "Sm", "Sc", "Sk", "So", "Zs", "Zl", "Zp", "Cc", "Cf", "Cs", "Co"}
	graphemeBreaks = []string{"Other", "CR", "LF", "Control", "Extend", "ZWJ", "Regional_Indicator", "Prepend", "SpacingMark", "L", "V", "T", "LV", "LVT"}
	wordBreaks     = []string{"Other", "CR", "LF", "Newline", "Extend", "ZWJ", "Regional_Indicator", "Format", "Katakana", "Hebrew_Letter", "ALetter", "Single_Quote", "Double_Quote", "MidNumLet", "MidLetter", "MidNum", "Numeric", "ExtendNumLet", "WSegSpace"}
	sentenceBreaks = []string{"Other", "CR", "LF", "Extend", "Sep", "Format", "Sp", "Lower", "Upper", "OLetter", "Numeric", "ATerm", "SContinue", "STerm", "Close"}
	conjunctBreaks = []string{"None", "Linker", "Consonant", "Extend"}
)

// The files read, and the directory of each under the database's root
// on unicode.org; the checked-in copy holds them side by side.
var remote = map[string]string{
	"GraphemeBreakProperty.txt": "auxiliary/",
	"WordBreakProperty.txt":     "auxiliary/",
	"SentenceBreakProperty.txt": "auxiliary/",
	"emoji-data.txt":            "emoji/",
	"DerivedNumericValues.txt":  "extracted/",
}

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

// A decomposition is the runes a code point decomposes to, and whether
// the mapping is a compatibility one.
type decomposition struct {
	to     []rune
	compat bool
}

// A composition is the pair a, b and the rune c it composes to.
type composition struct{ a, b, c rune }

// database is what the tables are made of, read from the files.
type database struct {
	upper, lower, title map[rune]rune
	category            map[rune]string
	ccc                 map[rune]int
	decomp              map[rune]decomposition
	space               map[rune]bool
	special             [3]map[rune][]rune // upper, lower, title: the unconditional mappings of SpecialCasing.txt
	foldSimple          map[rune]rune      // CaseFolding.txt, statuses C and S
	foldFull            map[rune][]rune    // status F
	script              map[rune]string
	scriptCode          map[string]string          // a script's four-letter code by its long name, from PropertyValueAliases.txt
	decimal             map[rune]int               // UnicodeData.txt's decimal digit value
	numeric             map[rune][2]int64          // its numeric value, a numerator and a denominator
	props               map[string]map[rune]string // by property: Cased, Case_Ignorable, InCB, the derived properties, the quick checks, Full_Composition_Exclusion, Extended_Pictographic, Emoji, GCB, WB, SB
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

	url := "https://www.unicode.org/Public/" + version + "/ucd/" + remote[name] + name
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

func hexes(s string) []rune {
	var out []rune
	for f := range strings.FieldsSeq(s) {
		out = append(out, hex(f))
	}

	return out
}

// lines calls f with the fields, split at ';' and trimmed, of every
// line of the named file that is not a comment.
func lines(dir, version, name string, f func(fields []string)) {
	r := open(dir, version, name)
	defer r.Close()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		if strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.Split(line, ";")
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}

		f(fields)
	}

	if err := sc.Err(); err != nil {
		log.Fatal(err)
	}
}

// rational reads the numeric value s of r, an integer or a fraction
// n/d, as its numerator and denominator.
func rational(r rune, s string) [2]int64 {
	num, den, frac := strings.Cut(s, "/")
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil {
		log.Fatalf("U+%04X: numeric value %q", r, s)
	}

	d := int64(1)
	if frac {
		if d, err = strconv.ParseInt(den, 10, 64); err != nil || d <= 0 {
			log.Fatalf("U+%04X: numeric value %q", r, s)
		}
	}

	return [2]int64{n, d}
}

// codeRange reads lo or lo..hi.
func codeRange(s string) (rune, rune) {
	lo, hi, isRange := strings.Cut(s, "..")
	if !isRange {
		hi = lo
	}

	return hex(lo), hex(hi)
}

// property reads a file of code points and a property, with an
// optional value after it, into db.props, the value being the property
// name itself when the line gives none; only the properties in want
// are kept.
func (db database) property(dir, version, name string, want map[string]bool) {
	lines(dir, version, name, func(f []string) {
		if len(f) < 2 || !want[f[1]] {
			return
		}

		key, val := f[1], f[1]
		if len(f) > 2 {
			val = f[2]
		}

		if db.props[key] == nil {
			db.props[key] = map[rune]string{}
		}

		lo, hi := codeRange(f[0])
		for c := lo; c <= hi; c++ {
			db.props[key][c] = val
		}
	})
}

// valued reads a file whose second field is the value of one property,
// a break property, into db.props under name.
func (db database) valued(dir, version, file, name string) {
	db.props[name] = map[rune]string{}
	lines(dir, version, file, func(f []string) {
		lo, hi := codeRange(f[0])
		for c := lo; c <= hi; c++ {
			db.props[name][c] = f[1]
		}
	})
}

// read loads the database. A range UnicodeData.txt gives as a First and
// a Last line has the category of its ends for every rune between, and
// no case mappings or decompositions.
func read(dir, version string) database {
	db := database{
		upper: map[rune]rune{}, lower: map[rune]rune{}, title: map[rune]rune{},
		category: map[rune]string{}, ccc: map[rune]int{}, decomp: map[rune]decomposition{}, space: map[rune]bool{},
		special:    [3]map[rune][]rune{{}, {}, {}},
		foldSimple: map[rune]rune{}, foldFull: map[rune][]rune{},
		script: map[rune]string{}, scriptCode: map[string]string{}, decimal: map[rune]int{}, numeric: map[rune][2]int64{}, props: map[string]map[rune]string{},
	}

	first := rune(-1)
	lines(dir, version, "UnicodeData.txt", func(f []string) {
		if len(f) < 15 {
			return
		}

		r, name, gc := hex(f[0]), f[1], f[2]
		switch {
		case strings.HasSuffix(name, ", First>"):
			first = r
			return
		case strings.HasSuffix(name, ", Last>"):
			for c := first; c <= r; c++ {
				db.category[c] = gc
			}

			return
		}

		db.category[r] = gc
		if f[6] != "" {
			db.decimal[r], _ = strconv.Atoi(f[6])
		}

		if f[8] != "" {
			db.numeric[r] = rational(r, f[8])
		}

		if n, _ := strconv.Atoi(f[3]); n != 0 {
			db.ccc[r] = n
		}

		if d := f[5]; d != "" {
			compat := strings.HasPrefix(d, "<")
			if compat {
				_, d, _ = strings.Cut(d, ">")
			}

			db.decomp[r] = decomposition{hexes(d), compat}
		}

		if f[12] != "" {
			db.upper[r] = hex(f[12])
		}

		if f[13] != "" {
			db.lower[r] = hex(f[13])
		}

		switch {
		case f[14] != "":
			db.title[r] = hex(f[14])
		case f[12] != "":
			db.title[r] = hex(f[12]) // an empty title mapping is the upper case one
		}
	})

	lines(dir, version, "PropList.txt", func(f []string) {
		if len(f) >= 2 && f[1] == "White_Space" {
			lo, hi := codeRange(f[0])
			for c := lo; c <= hi; c++ {
				db.space[c] = true
			}
		}
	})

	lines(dir, version, "SpecialCasing.txt", func(f []string) {
		if len(f) < 5 || f[4] != "" { // a condition, a language's or a context's, is not a mapping of every language
			return
		}

		r := hex(f[0])
		for i, col := range []int{3, 1, 2} { // upper, lower, title
			db.special[i][r] = hexes(f[col])
		}
	})

	lines(dir, version, "CaseFolding.txt", func(f []string) {
		r := hex(f[0])
		switch f[1] {
		case "C", "S":
			db.foldSimple[r] = hex(f[2])
		case "F":
			db.foldFull[r] = hexes(f[2])
		}
	})

	// The numeric values of UnicodeData.txt, and those Unihan gives the
	// ideographs, 五 its 5, which only the derived file holds; where both
	// give one they must be the same number, UnicodeData.txt's 2/12 the
	// derived file's 1/6, and UnicodeData.txt's fraction is kept.
	lines(dir, version, "DerivedNumericValues.txt", func(f []string) {
		lo, hi := codeRange(f[0])
		for c := lo; c <= hi; c++ {
			v := rational(c, f[3])
			old, ok := db.numeric[c]
			if !ok {
				db.numeric[c] = v
			} else if old[0]*v[1] != v[0]*old[1] {
				log.Fatalf("U+%04X: numeric value %d/%d in UnicodeData.txt, %s in DerivedNumericValues.txt", c, old[0], old[1], f[3])
			}
		}
	})

	lines(dir, version, "Scripts.txt", func(f []string) {
		lo, hi := codeRange(f[0])
		for c := lo; c <= hi; c++ {
			db.script[c] = f[1]
		}
	})

	lines(dir, version, "PropertyValueAliases.txt", func(f []string) {
		if len(f) >= 3 && f[0] == "sc" {
			db.scriptCode[f[2]] = f[1]
		}
	})

	db.property(dir, version, "DerivedCoreProperties.txt", map[string]bool{"Cased": true, "Case_Ignorable": true, "InCB": true, "Alphabetic": true, "Uppercase": true, "Lowercase": true, "XID_Start": true, "XID_Continue": true})
	db.property(dir, version, "DerivedNormalizationProps.txt", map[string]bool{"Full_Composition_Exclusion": true, "NFD_QC": true, "NFC_QC": true, "NFKD_QC": true, "NFKC_QC": true})
	db.property(dir, version, "emoji-data.txt", map[string]bool{"Extended_Pictographic": true, "Emoji": true})
	db.valued(dir, version, "GraphemeBreakProperty.txt", "GCB")
	db.valued(dir, version, "WordBreakProperty.txt", "WB")
	db.valued(dir, version, "SentenceBreakProperty.txt", "SB")
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

// index returns the position of v in values; an empty v, a rune the
// file does not list, is the default, 0. A value the list does not
// have stops the generator, so that a value a new version of the
// database adds is never filed silently as the default.
func index(values []string, v string) int {
	if v == "" {
		return 0
	}

	if i := slices.Index(values, v); i >= 0 {
		return i
	}

	log.Fatalf("unknown property value %q: add it to the list holding %q and to internal.h", v, values[0])
	return 0
}

func table(b *bytes.Buffer, name string, rs []run) {
	fmt.Fprintf(b, "static const sn_case_run %s[%d] = {\n", name, len(rs))
	for _, r := range rs {
		fmt.Fprintf(b, "    {0x%04X, 0x%04X, %d, %d},\n", r.lo, r.hi, r.stride, r.delta)
	}

	b.WriteString("};\n\n")
}

// ranges writes the runes from 0 up for which value is not zero, as a
// table of lo, hi and the value under name, each run of one value one
// entry.
func ranges(b *bytes.Buffer, name, doc string, value func(rune) int) {
	fmt.Fprintf(b, "// %s %s\nstatic const ucd_range %s[] = {\n", name, doc, name)
	for r := rune(0); r <= maxRune; r++ {
		v := value(r)
		if v == 0 {
			continue
		}

		lo := r
		for r+1 <= maxRune && value(r+1) == v {
			r++
		}

		fmt.Fprintf(b, "    {0x%04X, 0x%04X, %d},\n", lo, r, v)
	}

	b.WriteString("};\n\n")
}

// mappings writes the runes of m, sorted, each with up to three runes
// it maps to, under name.
func mappings(b *bytes.Buffer, name, doc string, m map[rune][]rune) {
	keys := make([]rune, 0, len(m))
	for r := range m {
		keys = append(keys, r)
	}

	slices.Sort(keys)
	fmt.Fprintf(b, "// %s %s\nstatic const ucd_full %s[%d] = {\n", name, doc, name, len(keys))
	for _, r := range keys {
		to := m[r]
		if len(to) > 3 {
			log.Fatalf("%s: U+%04X maps to %d runes", name, r, len(to))
		}

		cells := []string{"0", "0", "0"}
		for i, c := range to {
			cells[i] = fmt.Sprintf("0x%04X", c)
		}

		fmt.Fprintf(b, "    {0x%04X, %d, {%s}},\n", r, len(to), strings.Join(cells, ", "))
	}

	b.WriteString("};\n\n")
}

// names writes a table of strings under name.
func names(b *bytes.Buffer, name string, values []string) {
	fmt.Fprintf(b, "static const char *const %s[%d] = {", name, len(values))
	for i, v := range values {
		if i%8 == 0 {
			b.WriteString("\n   ")
		}

		fmt.Fprintf(b, " %q,", v)
	}

	b.WriteString("\n};\n\n")
}

func main() {
	out := flag.String("o", "c/unicode.c", "the file to write")
	version := flag.String("version", "18.0.0", "the version of the Unicode Character Database")
	dir := flag.String("ucd", "", "a directory holding the database's files; testdata/ucd/VERSION when not given, fetched from unicode.org when given empty")
	flag.Parse()
	given := false
	flag.Visit(func(f *flag.Flag) { given = given || f.Name == "ucd" })
	if !given {
		*dir = filepath.Join("testdata", "ucd", *version)
	}

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

#include "internal.h"

// A case run is the runes lo, lo+stride, ... up to hi, each mapped to
// itself plus delta.
typedef struct {
    int32_t lo, hi, stride, delta;
} sn_case_run;

// A range is the runes lo to hi, which all have the value v.
typedef struct {
    int32_t lo, hi, v;
} ucd_range;

// A full mapping is a rune and the n runes it maps to.
typedef struct {
    int32_t r, n;
    int32_t to[3];
} ucd_full;

// A decomposition is a rune, whether its mapping is a compatibility
// one, and the n runes of decomposed from at.
typedef struct {
    int32_t r, compat, at, n;
} ucd_decomp;

// A composition is the pair a, b and the rune c they compose to.
typedef struct {
    int32_t a, b, c;
} ucd_comp;

// A numeric value is a rune and the numerator and denominator of its
// value.
typedef struct {
    int32_t r;
    int64_t num;
    int32_t den;
} ucd_numeric;

`, *version)
	table(&b, "upper", runs(mapping(db.upper)))
	table(&b, "lower", runs(mapping(db.lower)))
	table(&b, "title", runs(mapping(db.title)))
	table(&b, "fold", runs(mapping(db.foldSimple)))
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

	b.WriteString("};\n\n")
	ranges(&b, "printable", "holds the runes above ASCII that are printable.", func(r rune) int {
		if r >= 0x80 && db.printable(r) {
			return 1
		}

		return 0
	})
	ranges(&b, "category", "is the general category of every assigned rune, numbered as categories; an unassigned one is Cn.", func(r rune) int { return index(categories, db.category[r]) })
	names(&b, "categories", categories)
	scripts := []string{"Unknown"}
	seen := map[string]bool{"Unknown": true}
	for _, s := range db.script {
		if !seen[s] {
			seen[s] = true
			scripts = append(scripts, s)
		}
	}

	slices.Sort(scripts[1:])
	ranges(&b, "script", "is the script of every rune Scripts.txt lists, numbered as scripts; any other is Unknown.", func(r rune) int { return index(scripts, db.script[r]) })
	names(&b, "scripts", scripts)
	codes := make([]string, len(scripts))
	for i, s := range scripts {
		if codes[i] = db.scriptCode[s]; codes[i] == "" {
			log.Fatalf("script %s has no code in PropertyValueAliases.txt", s)
		}
	}

	names(&b, "script_codes", codes)
	bits := func(r rune, names ...string) int {
		v := 0
		for i, n := range names {
			if db.props[n][r] != "" {
				v |= 1 << i
			}
		}

		return v
	}

	ranges(&b, "derived", "holds the derived properties of every rune that has one, as bits in the order of internal.h's SN_UC_ALPHABETIC.", func(r rune) int {
		return bits(r, "Alphabetic", "Uppercase", "Lowercase", "XID_Start", "XID_Continue", "Emoji")
	})
	ranges(&b, "quick", "holds the normalization quick-check values that are not Yes, as bits in the order of internal.h's SN_QC_NFD_NO.", func(r rune) int {
		v := 0
		for i, q := range [][2]string{{"NFD_QC", "N"}, {"NFC_QC", "N"}, {"NFC_QC", "M"}, {"NFKD_QC", "N"}, {"NFKC_QC", "N"}, {"NFKC_QC", "M"}} {
			if db.props[q[0]][r] == q[1] {
				v |= 1 << i
			}
		}

		return v
	})
	ranges(&b, "decimal", "is the decimal digit value of every decimal digit, plus one.", func(r rune) int {
		if d, ok := db.decimal[r]; ok {
			return d + 1
		}

		return 0
	})
	numerics := make([]rune, 0, len(db.numeric))
	for r := range db.numeric {
		numerics = append(numerics, r)
	}

	slices.Sort(numerics)
	fmt.Fprintf(&b, "// numerics are the runes with a numeric value, each with its numerator and denominator.\nstatic const ucd_numeric numerics[%d] = {\n", len(numerics))
	for _, r := range numerics {
		fmt.Fprintf(&b, "    {0x%04X, %d, %d},\n", r, db.numeric[r][0], db.numeric[r][1])
	}

	b.WriteString("};\n\n")
	mappings(&b, "special_upper", "are the upper case mappings of SpecialCasing.txt that hold in every language.", db.special[0])
	mappings(&b, "special_lower", "are its lower case mappings.", db.special[1])
	mappings(&b, "special_title", "are its title case mappings.", db.special[2])
	mappings(&b, "fold_full", "are the full case foldings of CaseFolding.txt, status F.", db.foldFull)
	prop := func(name string) func(rune) int {
		return func(r rune) int {
			if db.props[name][r] != "" {
				return 1
			}

			return 0
		}
	}

	ranges(&b, "cased", "are the runes with the Cased property.", prop("Cased"))
	ranges(&b, "case_ignorable", "are the runes with the Case_Ignorable property.", prop("Case_Ignorable"))
	ranges(&b, "ccc", "is the canonical combining class of every rune that has one.", func(r rune) int { return db.ccc[r] })

	keys := make([]rune, 0, len(db.decomp))
	for r := range db.decomp {
		keys = append(keys, r)
	}

	slices.Sort(keys)
	var flat []rune
	fmt.Fprintf(&b, "// decomps are the decompositions of UnicodeData.txt; decomposed holds their runes.\nstatic const ucd_decomp decomps[%d] = {\n", len(keys))
	for _, r := range keys {
		d := db.decomp[r]
		compat := 0
		if d.compat {
			compat = 1
		}

		fmt.Fprintf(&b, "    {0x%04X, %d, %d, %d},\n", r, compat, len(flat), len(d.to))
		flat = append(flat, d.to...)
	}

	fmt.Fprintf(&b, "};\n\nstatic const int32_t decomposed[%d] = {", len(flat))
	for i, r := range flat {
		if i%12 == 0 {
			b.WriteString("\n   ")
		}

		fmt.Fprintf(&b, " 0x%04X,", r)
	}

	b.WriteString("\n};\n\n")
	var comps []composition
	for _, r := range keys {
		d := db.decomp[r]
		if d.compat || len(d.to) != 2 || db.props["Full_Composition_Exclusion"][r] != "" {
			continue
		}

		comps = append(comps, composition{d.to[0], d.to[1], r})
	}

	slices.SortFunc(comps, func(x, y composition) int {
		if x.a != y.a {
			return int(x.a - y.a)
		}

		return int(x.b - y.b)
	})
	fmt.Fprintf(&b, "// comps are the canonical compositions, by their pair, the exclusions left out.\nstatic const ucd_comp comps[%d] = {\n", len(comps))
	for _, c := range comps {
		fmt.Fprintf(&b, "    {0x%04X, 0x%04X, 0x%04X},\n", c.a, c.b, c.c)
	}

	b.WriteString("};\n\n")
	valued := func(name string, values []string) func(rune) int {
		return func(r rune) int { return index(values, db.props[name][r]) }
	}

	ranges(&b, "gcb", "is the Grapheme_Cluster_Break of every rune that has one.", valued("GCB", graphemeBreaks))
	ranges(&b, "wb", "is the Word_Break of every rune that has one.", valued("WB", wordBreaks))
	ranges(&b, "sb", "is the Sentence_Break of every rune that has one.", valued("SB", sentenceBreaks))
	ranges(&b, "extpict", "are the runes with the Extended_Pictographic property.", prop("Extended_Pictographic"))
	ranges(&b, "incb", "is the Indic_Conjunct_Break of every rune that has one.", valued("InCB", conjunctBreaks))
	b.WriteString(lookups)
	if err := os.WriteFile(*out, b.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}

// lookups are the functions over the tables, the same for every
// version of the database.
const lookups = `#define LEN(t) ((int64_t)(sizeof t / sizeof t[0]))

// value finds r in the n ranges of t and returns its value, or 0.
static int value(const ucd_range *t, int64_t n, int32_t r) {
    int64_t lo = 0, hi = n - 1;
    while (lo <= hi) {
        int64_t mid = lo + (hi - lo) / 2;
        if (r < t[mid].lo) {
            hi = mid - 1;
        } else if (r > t[mid].hi) {
            lo = mid + 1;
        } else {
            return t[mid].v;
        }
    }

    return 0;
}

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

// full finds r in the n mappings of t and copies what it maps to into
// out, returning how many runes that is, or 0 when t has no mapping.
static int full(const ucd_full *t, int64_t n, int32_t r, int32_t out[3]) {
    int64_t lo = 0, hi = n - 1;
    while (lo <= hi) {
        int64_t mid = lo + (hi - lo) / 2;
        if (r < t[mid].r) {
            hi = mid - 1;
        } else if (r > t[mid].r) {
            lo = mid + 1;
        } else {
            for (int i = 0; i < t[mid].n; i++) {
                out[i] = t[mid].to[i];
            }

            return t[mid].n;
        }
    }

    return 0;
}

int32_t sn_rune_upper(int32_t r) {
    if (r < 0x80) {
        return r >= 'a' && r <= 'z' ? r - 32 : r;
    }

    return mapped(upper, LEN(upper), r);
}

int32_t sn_rune_lower(int32_t r) {
    if (r < 0x80) {
        return r >= 'A' && r <= 'Z' ? r + 32 : r;
    }

    return mapped(lower, LEN(lower), r);
}

int32_t sn_rune_title(int32_t r) {
    if (r < 0x80) {
        return r >= 'a' && r <= 'z' ? r - 32 : r;
    }

    return mapped(title, LEN(title), r);
}

int32_t sn_rune_fold(int32_t r) {
    if (r < 0x80) {
        return r >= 'A' && r <= 'Z' ? r + 32 : r;
    }

    return mapped(fold, LEN(fold), r);
}

bool sn_rune_space(int32_t r) {
    if (r < 0x80) {
        return r == ' ' || (r >= '\t' && r <= '\r');
    }

    for (int64_t i = 0; i < LEN(spaces); i++) {
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

    return value(printable, LEN(printable), r) != 0;
}

int sn_rune_category(int32_t r) {
    if (r < 0 || r > 0x10FFFF) {
        return SN_GC_Cn;
    }

    return value(category, LEN(category), r);
}

const char *sn_category_name(int c) {
    return c >= 0 && c < LEN(categories) ? categories[c] : "Cn";
}

bool sn_rune_letter(int32_t r) {
    if (r < 0x80) {
        return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z');
    }

    return sn_category_name(sn_rune_category(r))[0] == 'L';
}

bool sn_rune_digit(int32_t r) {
    if (r < 0x80) {
        return r >= '0' && r <= '9';
    }

    return sn_rune_category(r) == SN_GC_Nd;
}

bool sn_rune_is_upper(int32_t r) {
    if (r < 0x80) {
        return r >= 'A' && r <= 'Z';
    }

    return sn_rune_category(r) == SN_GC_Lu;
}

bool sn_rune_is_lower(int32_t r) {
    if (r < 0x80) {
        return r >= 'a' && r <= 'z';
    }

    return sn_rune_category(r) == SN_GC_Ll;
}

int sn_rune_script(int32_t r) {
    if (r < 0 || r > 0x10FFFF) {
        return 0;
    }

    return value(script, LEN(script), r);
}

const char *sn_script_name(int s) {
    return s >= 0 && s < LEN(scripts) ? scripts[s] : "Unknown";
}

const char *sn_script_code(int s) {
    return s >= 0 && s < LEN(script_codes) ? script_codes[s] : "Zzzz";
}

int sn_uc_derived(int32_t r) { return value(derived, LEN(derived), r); }
int sn_uc_quick(int32_t r) { return value(quick, LEN(quick), r); } // no shortcut by code point: the Latin-1 letters with accents have a quick check of No for the decomposed forms
int sn_uc_decimal(int32_t r) { return value(decimal, LEN(decimal), r) - 1; }

bool sn_uc_numeric(int32_t r, int64_t *num, int32_t *den) {
    int64_t lo = 0, hi = LEN(numerics) - 1;
    while (lo <= hi) {
        int64_t mid = lo + (hi - lo) / 2;
        if (r < numerics[mid].r) {
            hi = mid - 1;
        } else if (r > numerics[mid].r) {
            lo = mid + 1;
        } else {
            *num = numerics[mid].num;
            *den = numerics[mid].den;
            return true;
        }
    }

    return false;
}

int sn_script_count(void) {
    return (int)LEN(scripts);
}

int sn_uc_special(int kind, int32_t r, int32_t out[3]) {
    switch (kind) {
    case SN_UC_UPPER:
        return full(special_upper, LEN(special_upper), r, out);
    case SN_UC_LOWER:
        return full(special_lower, LEN(special_lower), r, out);
    case SN_UC_TITLE:
        return full(special_title, LEN(special_title), r, out);
    default:
        return full(fold_full, LEN(fold_full), r, out);
    }
}

bool sn_uc_cased(int32_t r) { return value(cased, LEN(cased), r) != 0; }
bool sn_uc_case_ignorable(int32_t r) { return value(case_ignorable, LEN(case_ignorable), r) != 0; }
int sn_uc_ccc(int32_t r) { return r < 0x300 ? 0 : value(ccc, LEN(ccc), r); }

int sn_uc_decomp(int32_t r, bool compat, const int32_t **out) {
    int64_t lo = 0, hi = LEN(decomps) - 1;
    while (lo <= hi) {
        int64_t mid = lo + (hi - lo) / 2;
        if (r < decomps[mid].r) {
            hi = mid - 1;
        } else if (r > decomps[mid].r) {
            lo = mid + 1;
        } else {
            if (decomps[mid].compat && !compat) {
                return 0;
            }

            *out = decomposed + decomps[mid].at;
            return decomps[mid].n;
        }
    }

    return 0;
}

int32_t sn_uc_compose(int32_t a, int32_t b) {
    int64_t lo = 0, hi = LEN(comps) - 1;
    while (lo <= hi) {
        int64_t mid = lo + (hi - lo) / 2;
        if (a < comps[mid].a || (a == comps[mid].a && b < comps[mid].b)) {
            hi = mid - 1;
        } else if (a > comps[mid].a || b > comps[mid].b) {
            lo = mid + 1;
        } else {
            return comps[mid].c;
        }
    }

    return -1;
}

int sn_uc_gcb(int32_t r) { return value(gcb, LEN(gcb), r); }
int sn_uc_wb(int32_t r) { return value(wb, LEN(wb), r); }
int sn_uc_sb(int32_t r) { return value(sb, LEN(sb), r); }
bool sn_uc_extpict(int32_t r) { return value(extpict, LEN(extpict), r) != 0; }
int sn_uc_incb(int32_t r) { return value(incb, LEN(incb), r); }
`
