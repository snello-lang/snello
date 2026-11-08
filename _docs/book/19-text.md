# 19. Text: strings, runes and Unicode

Snello's text follows Unicode 18.0.0. Every table is tested on every code point and every algorithm on every line of the test file Unicode publishes for it, and `examples/run/runes.snello` runs everything in this chapter.

This chapter is for two readers: a developer handling text, and a maintainer of the compiler, the runtime or the library who needs to know where each piece lives. The last section is the maintainer's.

Its subject is Unicode text: the encodings, the character properties, case, normalization and segmentation. Laying text out, breaking lines and ordering bidirectional text, and the rules of a locale, sorting by a language's order or Turkish dotted i, are subjects of their own.

## A string is UTF-8 bytes

A `string` holds bytes, never changes, and is UTF-8 by convention: every literal is, and every function that builds a string writes it. `len(s)` counts bytes and `s[i]` is the byte at `i`, of type `byte`, because both take constant time. Text is something else: a rune, of type `rune`, an `i32`, is one Unicode code point, and a code point takes one to four bytes. `"héllo, 世界"` is 14 bytes and 9 runes:

```snello
s := "héllo, 世界"
fmt.Println(len(s), len(s.Runes()), s[1], s.Runes()[1])
// 14 9 195 233
```

`s[1]` is the first byte of `é`, 195; its rune is 233, U+00E9. A rune prints as its number. To print it as text, convert it to a string; `fmt`'s `%c` verb is planned with the rest of `fmt`'s verbs.

## From runes back to a string

`s.Runes()` gives an owned `[]rune` of exactly the string's runes, for a program that must index them, change them or keep them. `string(rs)` builds the UTF-8 string of a `[]rune`, and `string(r)` that of one rune; a value that is not a code point becomes U+FFFD:

```snello
rs := s.Runes()
rs.Push('!')
fmt.Println(string(rs), string(rs[1]), string('x')) // back from runes to a string
// héllo, 世界! é x
```

Only a `rune` converts to a string, never an `int`: `string(65)` is refused, since in a language where it means `"A"` it is a common mistake for `"65"`, which is `strconv.Itoa(65)`.

## A range over a string yields runes

`for i, r := range s` gives each rune and the byte offset it starts at, so the offsets step by the rune's width:

```snello
for i, r := range s {
    fmt.Println(i, r, utf8.RuneLen(r))
}
// 0 104 1, 1 233 2, 3 108 1, ... 8 19990 3, 11 30028 3
```

`for i := range s` gives the offsets alone, and `for range s` counts. The string is lent for the loop, as `range s.Fields()` lends it: the loop body may read `s` but not assign it or move it, and `s` is there after the loop. The runes are copies, so nothing is consumed.

A string need not be valid UTF-8: one read from a file or a socket may hold any bytes. A byte that does not start a valid sequence yields U+FFFD, `utf8.RuneError`, and the loop moves on one byte, so a broken string is read through to its end and every stray byte is seen:

```snello
for i, r := range "a\xffb" {
    fmt.Println(i, r, r == utf8.RuneError)
}
// 0 97 false, 1 65533 true, 2 98 false
```

The same rule holds everywhere the language reads a string as text: `s.Runes()`, the case functions, `Fields` and `Trim`, `%q`, every function of `unicode/utf8`, `bufio.Reader.ReadRune`, normalization and segmentation. One rule means a string reads the same whichever of them a program uses. `utf8.ValidString(s)` says whether there is any stray byte to read, and `strings.ToValidUTF8(s, "?")` replaces each run of them.

To walk the bytes, write the counted loop. It allocates nothing:

```snello
ascii := 0
for i := 0; i < len(s); i++ {
    if s[i] < 0x80 {
        ascii += 1
    }
}
```

The range walks the runes without a slice, and `utf8.RuneCountInString(s)` counts them without one.

## The `unicode` package

`unicode` answers questions about one rune from Unicode's own tables, at the version `unicode.Version` names, 18.0.0. Most are its general category, the two letters Unicode gives every code point:

| Function | True for |
|---|---|
| `IsLetter(r)` | a letter, category L: `a`, `É`, `Ж`, `世` |
| `IsTitle(r)` | a title case digraph, Lt, such as `ǅ` |
| `IsMark(r)` | a mark, M: a combining accent, a vowel sign |
| `IsNumber(r)` | a number, N: `7`, `²`, `Ⅻ` |
| `IsDigit(r)` | a decimal digit, Nd: `0` to `9`, and `٣`, ARABIC-INDIC DIGIT THREE; not `²` |
| `IsPunct(r)` | punctuation, P: `!`, `¿`, `。` |
| `IsSymbol(r)` | a symbol, S: `+`, `€`, an emoji |
| `IsControl(r)` | a control character, Cc: the C0 and C1 controls |
| `IsSpace(r)` | Unicode's `White_Space` property, the runes `Fields` splits at: the space, tab, newline, U+00A0, U+3000; not U+200B |
| `IsGraphic(r)` | a letter, mark, number, punctuation, symbol or space separator, Zs |
| `IsPrint(r)` | the same with the ASCII space as its only space: what `%q` leaves unescaped |

Others are the properties Unicode derives beyond a category, the ones identifiers and case are defined by:

| Function | True for |
|---|---|
| `IsUpper(r)`, `IsLower(r)` | the derived `Uppercase` and `Lowercase`: the letters of Lu and Ll, and runes such as `Ⓐ` and `ª`; `Category(r) == "Lu"` asks for the letters alone |
| `IsAlphabetic(r)` | the derived `Alphabetic`: every letter and the marks and numbers that write words, such as a Devanagari vowel sign |
| `IsIDStart(r)`, `IsIDContinue(r)` | `XID_Start` and `XID_Continue`, annex 31's identifiers: a letter of any script starts one, and digits, marks and `_` go on |
| `IsEmoji(r)` | the `Emoji` property: `😀` and `❤`, and also the digits, `#` and `*`, which form keycaps |

`Category(r)` gives the two letters themselves, `Lu` for `A` and `Cn` for a code point Unicode has not assigned. `Script(r)` gives the script by Unicode's name, `Latin`, `Cyrillic`, `Han`, `Common` for a rune many scripts use, such as a digit or `€`, `Inherited` for a mark that takes its letter's, and `Unknown` for an unassigned code point; `ScriptCode(r)` gives its four-letter code, `Latn`, `Hani`, `Zyyy`; `InScript(r, name)` takes either. `DigitValue(r)` gives a decimal digit's value, of any script, and `NumericValue(r)` the value Unicode gives any number, `12` for `Ⅻ`, `0.5` for `½`, and `5` for the ideograph `五`, whose value comes from Unihan; each is `None` for a rune without one.

```snello
for _, r := range "A٣€Ж世" {
    fmt.Println(r, unicode.Category(r), unicode.Script(r), unicode.ScriptCode(r), unicode.IsSymbol(r), unicode.IsNumber(r))
}
// 65 Lu Latin Latn false false, 1635 Nd Arabic Arab false true, 8364 Sc Common Zyyy true false, ...
fmt.Println(unicode.DigitValue(0x0663), unicode.NumericValue(0x216B), unicode.IsAlphabetic(0x093E), unicode.IsIDStart('_'), unicode.IsEmoji(0x1F600))
// Some(3) Some(12) true false true
```

`ToUpper(r)`, `ToLower(r)` and `ToTitle(r)` give the rune's simple case mapping, one rune to one rune, and the rune itself when it has none. `CaseFold(r)` gives its simple case folding: every rune of a set that differs only in case maps to the same one, `K`, `k` and the Kelvin sign `K` to `k`, so two runes are equal ignoring case when they fold to the same rune. A value that is not a code point, a negative one or one past `unicode.MaxRune`, has no property, category `Cn`, script `Unknown`, no value, and maps to itself.

Upper-casing the first letter of every word walks the runes and writes each to a builder:

```snello
func title(s ref string) string {
    var b strings.Builder
    start := true
    for _, r := range s {
        if start && unicode.IsLetter(r) {
            b.WriteRune(unicode.ToUpper(r))
        } else {
            b.WriteRune(r)
        }

        start = unicode.IsSpace(r)
    }

    return b.String()
}
```

`title("élan vital 2 mondo")` is `Élan Vital 2 Mondo`. `strings.ToTitle`, below, does it by Unicode's own rules.

## Case on strings

A string changes case by Unicode's full case mapping, which a single rune cannot: a rune may become several, so a string may grow. `strings.ToUpper("straße")` is `STRASSE`, and `ﬁ` upper-cases to `FI`. `strings.ToLower` lowers a capital sigma at the end of a word to the final form `ς`, as Greek writes it, and inside a word to `σ`: `ΟΔΥΣΣΕΥΣ` lowers to `οδυσσευς`. The string methods `s.ToUpper()` and `s.ToLower()` are the same functions. `strings.ToTitle` title-cases text as Unicode defines it: in each word, by the word boundaries below, the rune that starts it to its title case and the rest to lower case, so `straße ﬁx ǆungla` is `Straße Fix ǅungla`. A word starts at its first letter, number or symbol, where ICU starts it by default: a quote before it is passed over, so `'tis` is `'Tis`, and a digit that starts it stays as it is, so `3RD` is `3rd`. The mappings are the ones that hold in every language; a language's own rules, such as Turkish `i` upper-casing to `İ`, are a locale's.

```snello
fmt.Println(strings.ToUpper("straße"), strings.ToLower("ΟΔΥΣΣΕΥΣ"), strings.ToTitle("straße ﬁx ǆungla"))
// STRASSE οδυσσευς Straße Fix ǅungla
fmt.Println(strings.EqualIgnoreCase("Straße", "STRASSE"), strings.EqualIgnoreCase("ı", "I"))
// true false
```

`strings.EqualIgnoreCase(s, t)` says whether two strings are equal ignoring case, by Unicode's full case folding, its default caseless match; it is to `==` what ignoring case is to comparing. `Straße` and `STRASSE` are equal, and so are `ΣΑΣ` and `σας`. Dotless `ı` and `I` are not, since outside Turkish `I` folds to `i`. A stray byte reads as U+FFFD, so two strings that differ only in stray bytes are equal.

## Searching and splitting by rune

`strings.IndexRune(s, r)` gives the byte offset of the first `r`, or -1, and `ContainsRune` says whether there is one. The functions that take a predicate on runes, `func(rune) bool`, work with any of `unicode`'s: `IndexFunc` and `LastIndexFunc` find the first and last rune for which it holds; `TrimFunc`, `TrimLeftFunc` and `TrimRightFunc` lend the string without such runes at its ends; `FieldsFunc` splits around runs of them, each piece a borrow into the string. `strings.Map(f, s)` gives a new string of each rune passed through `f`, dropping a rune for which `f` returns a negative value.

## The encodings: `unicode/utf8` and `unicode/utf16`

`unicode/utf8` reads and writes runes in UTF-8, for a program working with bytes. It is imported by its path and named by its last element, `utf8`:

- `RuneLen(r)` is the bytes the rune takes, one to four, or -1 for a value that is not a code point, a surrogate U+D800 to U+DFFF included; `ValidRune(r)` says whether it is one. `UTFMax` is 4, and below `RuneSelf`, `0x80`, a byte is the rune it encodes.
- `AppendRune(mut p, r)` appends the rune's bytes to a byte slice, growing it, and those of `RuneError` for a value that is not a code point.
- `DecodeRune(ref p)` and `DecodeRuneInString(s)` give the first rune and its width; `DecodeLastRune` and `DecodeLastRuneInString` the last one. A byte that starts no valid sequence is `(RuneError, 1)`, and empty input `(RuneError, 0)`.
- `Valid`, `ValidString`, `RuneCount` and `RuneCountInString` check and count without allocating. `FullRune` and `FullRuneInString` say whether the bytes begin with a whole rune, false only for a rune cut short, which a reader filling a buffer asks before it decodes; `RuneStart(b)` says whether a byte can start one.

```snello
p := []byte{}
utf8.AppendRune(mut p, 'é')
utf8.AppendRune(mut p, 0x1F600)
r, n := utf8.DecodeRune(ref p)
fmt.Println(p, r, n)
// [195 169 240 159 152 128] 233 2
```

`unicode/utf16` is the encoding of Windows, Java and JavaScript, for a program that exchanges text with one of them: a rune below U+10000 is one 16-bit unit and one above it two, a surrogate pair. `Encode(ref runes)` and `Decode(ref units)` convert between `[]rune` and `[]u16`, an unpaired surrogate reading as U+FFFD; `AppendRune`, `EncodeRune`, `DecodeRune`, `RuneLen` and `IsSurrogate` work a rune at a time. `EncodeBytes(s, order, bom)` and `DecodeBytes(p, order)` go between a string and UTF-16 bytes, `utf16.BigEndian` or `utf16.LittleEndian`; a byte-order mark at the start of the bytes says the order instead, and is dropped:

```snello
units := utf16.Encode(ref "a😀".Runes())
fmt.Println(units, utf16.Decode(ref units))
// [97 55357 56832] [97 128512]
fmt.Println(utf16.EncodeBytes("a😀", utf16.LittleEndian, true), utf16.DecodeBytes(ref []byte{0xFF, 0xFE, 97, 0}, utf16.BigEndian))
// [255 254 97 0 61 216 0 222] a
```

## Writing and reading runes

`strings.Builder.WriteRune(r)` appends a rune's bytes, and U+FFFD for a value that is not a code point. `bufio.Reader.ReadRune()` returns `Result<(rune, int)?, io.Error>`: the next rune and its width in bytes, `None` at the end, a stray byte as U+FFFD of width one. It asks the reader under it for more only while its bytes are the start of a rune cut short, so a rune typed at a terminal, or bytes already no rune's start, come back without waiting. `UnreadRune()` puts back the rune just read, for a scanner that looks one rune ahead; it returns false, changing nothing, after any other call.

## Normalization: `unicode/norm`

The same text can be written with different runes: `é` is one rune, U+00E9, or `e` followed by a combining acute accent, U+0301. The two look the same and compare unequal, byte for byte. Normalization puts a string in one form so that they compare equal. `unicode/norm` has the four forms of the standard:

- `norm.NFC`: decompose, then compose again. Most text is written in it, and it is the form to compare and store by.
- `norm.NFD`: decompose every rune, each mark after its letter in canonical order.
- `norm.NFKC` and `norm.NFKD`: the same, and also replace a compatibility character by its plain form, `ﬁ` by `fi` and `²` by `2`, for search and identifiers.

```snello
one := "caf\u00E9"
two := "cafe\u0301"
fmt.Println(one == two, norm.String(norm.NFC, ref one) == norm.String(norm.NFC, ref two), len(one), len(two))
// false true 5 6
fmt.Println(norm.String(norm.NFKC, "ﬁ x²"))
// fi x2
```

`norm.String(f, s)` gives a new string in the form, and `norm.IsNormalString(f, s)` says whether `s` is in it already, by the standard's quick check, which reads the string once and allocates nothing, normalizing only when the check cannot tell. Korean syllables decompose and compose by the standard's algorithm. A stray byte becomes U+FFFD, so a string with one is in no form.

## Segmentation: `unicode/segment`

What a reader sees as one character can be several runes: a letter and its accent, a flag made of two regional indicators, an emoji with a skin tone, a consonant cluster of an Indian script. `"e\u0301🇫🇷👍🏽"`, an `e` with its accent, a flag and a thumb with a skin tone, is 19 bytes, 6 runes and 3 characters. `unicode/segment` cuts text where the standard's annex 29 puts the boundaries:

- `segment.Graphemes(s)`: the grapheme clusters, the characters a reader sees, for counting them, moving a cursor or truncating text without splitting one.
- `segment.Words(s)`: the pieces between word boundaries, the words and, each a piece of its own, the punctuation and the runs of spaces between them; `3.14` and `can't` are one word. `segment.IsWord(piece)` tells a word, a piece with a letter or a number, from the rest.
- `segment.Sentences(s)`: the sentences, each with the spaces after it.

Each gives the pieces in order, each a borrow into the string, so nothing is copied and the pieces join back to the string; a loop over them lends the string for its duration, as `strings.Fields` does. `segment.Count(kind, s)` counts the segments of `segment.Grapheme`, `segment.Word` or `segment.Sentence` without the slice, and `segment.Next(kind, s, at)` gives the boundary after `at`, for walking a string by offsets: `Some(end)` while a segment starts at `at`, and `None` at the end of the string, as for an offset past it or below 0, so a walk stops on the one answer:

```snello
flags := "e\u0301🇫🇷👍🏽"
fmt.Println(len(flags), utf8.RuneCountInString(flags), segment.Count(segment.Grapheme, ref flags))
// 19 6 3
for w := range segment.Words("Hello, wörld! 3.14") {
    if segment.IsWord(w) {
        fmt.Printf("[%s]", w)
    }
}
// [Hello][wörld][3.14]
```

A walk by offsets reads the same boundaries one at a time:

```snello
text := "Hi. Bye."
at := 0
for {
    match segment.Next(segment.Sentence, ref text, at) {
        None => break,
        Some(end) => {
            fmt.Printf("[%s]", text[at:end])
            at = end
        },
    }
}
// [Hi. ][Bye.]
```

The boundaries are the standard's defaults, the same for every language: a sentence ends after `Mr.` as after any full stop followed by a capital, since telling an abbreviation from the end of a sentence takes a language's dictionary; each Han ideograph is a word of its own; and words of Thai or Japanese, which put no space between them, take a dictionary too.

## For maintainers

- **The range and the conversions.** `rangeTypes` in `compiler/types/stmt.go` types the variables, the offset an `int` and the value a `rune`; `rangeStmt` lends an owned string for the loop with `holdRange`, beside the channel's case, instead of consuming it. Each backend writes the loop in `rangeString` in its `stmt.go`: the C backend's step adds the rune's width, so a `continue` moves on; the LLVM backend's adds it in the block after the body, where `continue` jumps. `conversion` in `compiler/types/call.go` takes `string(r)` and `string(rs)`, typing a character literal as a rune and refusing an `int`; each backend's conversion calls `sn_string_from_rune` and `sn_string_from_runes` in `runtime/c/string.c`.
- **The decoder and encoder.** `sn_utf8_decode` and `sn_utf8_encode` in `runtime/c/snello.c`, declared in `snello.h` so that a program's code calls them, are the one reading and writing of UTF-8; the encoder writes U+FFFD for a value that is not a code point. `sn_runes` in `runtime/c/string.c` counts the runes, then allocates exactly that many.
- **The tables.** `runtime/mkunicode.go` writes `runtime/c/unicode.c` from `runtime/testdata/ucd/18.0.0/`, Unicode's own files checked in: `UnicodeData.txt`, `PropList.txt`, `PropertyValueAliases.txt`, `SpecialCasing.txt`, `CaseFolding.txt`, `Scripts.txt`, `DerivedCoreProperties.txt`, `DerivedNormalizationProps.txt`, `CompositionExclusions.txt`, `DerivedNumericValues.txt`, `emoji-data.txt` and the three break property files, with the test files beside them. It writes data and small lookups only: the case runs, the general category, the scripts and their codes, the derived properties, the digit values and the numeric values, the ideographs' from Unihan among them, which the generator checks against `UnicodeData.txt`'s where both give one, the full mappings and foldings, `Cased` and `Case_Ignorable`, the combining classes, decompositions, compositions and quick-check values, and the break properties with `Extended_Pictographic` and `Indic_Conjunct_Break`. The numbering of each enumerated property and bit set is in `runtime/c/internal.h`, and the generator stops on a value it does not know, so a new version cannot file one silently as the default. Run `go run mkunicode.go` in `runtime/` after changing it; to move to a new version, add its files under `testdata/ucd/VERSION`, run `go run mkunicode.go -version VERSION`, change the version the generator, `runtime_test.go` and `unicode.Version` name, and run the tests, which say what the new version changed.
- **The algorithms**, written by hand over the tables: the package bindings, the classes, properties, values and scripts, and the rest of `utf8` in `runtime/c/rune.c`; full case mapping with the final sigma, title case by words, the full folding of `EqualIgnoreCase` and `ToValidUTF8` in `runtime/c/string.c`; normalization, decomposition, canonical ordering by a counting sort, composition with the Hangul algorithm and the quick check, in `runtime/c/norm.c`; segmentation, the rules of annex 29 over a cursor that decodes as far ahead as a rule looks, the sentence rules carrying their state forward, in `runtime/c/segment.c`. Each rule carries its number from the annex in a comment, and every algorithm takes time in proportion to its input.
- **The packages.** `std/unicode/unicode.snello` declares its functions without bodies, bound to `rune.c`. `unicode/utf8`'s `RuneLen` and `AppendRune` and all of `unicode/utf16` are Snello; `utf8_test.snello` writes every code point with `AppendRune` and reads it back through the runtime's decoder, so the two cannot disagree. `unicode/norm` and `unicode/segment` are thin Snello over `norm.c` and `segment.c`; `segment` and `strings.FieldsFunc` build their slices of borrows themselves, a type the library may write and a program may not.
- **Packages two levels down.** `unicode/utf8` was the library's first. `std/std.go` embeds `*/*.snello` and `*/*/*.snello`; the importer reads a package by its whole path and names its file by the last element, and its external tests as `utf8_test`; `stdPackage` in `compiler/testrun/testrun.go` finds the `std/` above a directory, so that `snello test std/unicode/norm` runs; `build/test-std.sh` finds the packages at any depth. A bodiless function's C name joins the path's elements with two underscores: `sn_unicode__norm_normalize`.
- **The tests.** `runtime/runtime_test.go` holds the conformance: `TestUnicodeTables` fails when `unicode.c` is not what the generator writes, or when it or `unicode.Version` names another version than the files'; `TestUnicodeLookup` asks the compiled tables about all 1,114,112 code points, case, folding, every class and derived property, category, script and code, digit and numeric value, and compares each answer with the files, read by the test on its own; `TestCaseMapping` checks the full mappings of every assigned code point, the final sigma and `EqualIgnoreCase` against `SpecialCasing.txt` and `CaseFolding.txt`; `TestNormalization` runs every line of `NormalizationTest.txt` in all five columns and every code point it does not list; `TestQuickCheck` checks that `IsNormalString` agrees with normalizing on every code point and every string of that file; `TestSegmentation` runs every line of `GraphemeBreakTest.txt`, `WordBreakTest.txt` and `SentenceBreakTest.txt`; `TestLinearTime` holds normalization and sentence boundaries to linear time on inputs of 200,000 runes. Beside the compiler's files: `compiler/types/stmt_test.go`, `library_test.go`, `call_test.go` and `operand_test.go`; in each backend `stmt_test.go`, `methods_test.go`, and `expr_test.go` or `call_test.go`; `compiler/importer/importer_test.go` and `compiler/testrun/testrun_test.go` for the nested packages. `range-runes`, `string-runes`, `rune-conversions` and `conversions-through-mut` in `compiler/emit/internal/backendtest` run through both backends in release and debug builds, and each package's `_test.snello` runs under `make test`.
