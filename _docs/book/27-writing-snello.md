# 27. Writing Snello

Snello code is written one way, so that a reader who knows one package can read any other without learning its author's habits first. This chapter sets that way down. The standard library is held to it, and it is the way the rest of this book writes its examples. It builds on Go's own conventions, gofmt's layout and Effective Go's naming, and adds what Snello needs that Go does not have: constants that cannot be patterns, values that may not be empty, and borrows.

## A file, top to bottom

A file reads from what a package gives to how it does it:

1. The package comment, on one file of the package, saying what the package is for and how it is used.
2. The imports.
3. Every constant, in one place, each group with a comment: the characters a parser reads, the limits, the literal names. No constant is declared between functions.
4. The package's variables, after the constants.
5. The types: the exported ones first, then the unexported ones its code uses. An enum stands with the other types; a set of constants that only make sense with one type may follow it directly, as Go places an enumeration's constants after their type.
6. The exported functions and, for each type, its exported methods and its functions of the type (chapter 26).
7. The unexported functions and methods: the helpers, after everything a user calls.

A type's exported methods come before its unexported ones, never between them. A reader looking for what a type offers finds it in one run, and the helpers those methods use come after:

```go
func (q ref Queue<T>) Len() int { ... }
func (q mut Queue<T>) Push(v T) { ... }
func (q mut Queue<T>) Pop() T? { ... }

// grow is a helper: it comes after the last exported method of Queue
func (q mut Queue<T>) grow() { ... }
```

## Names say what a thing does

A name is read far more often than it is written, and most often where it is used, away from its comment. It says what the function does or what the value is, in words the reader already has.

- A function that acts is a verb and its object: `readString`, `skipSpace`, `writeQuoted`, `findKeyValue`. `str`, `next` and `more` say which part of the code they come from, not what they do.
- A function that answers yes or no starts with `is`, `has` or a verb of the question: `isSpace`, `isOneValue`, `keyEquals`.
- A function that makes a value names the value: `missingFieldError`, `fieldKey`.
- A function of a type that makes one is `Of`, `New` or the source it reads, `Point.Parse`; a function of a type is not a constructor the compiler writes for every type (chapter 26).
- An exported name reads with its package: `json.Marshal`, never `json.JSONMarshal`.
- Short names belong to short scopes: `i` in a three-line loop, `c` for the byte being looked at, `r` for the receiver used on every line. A name that lives longer than a few lines is a word.
- An abbreviation is used only when it is the word people say: `ID`, `URL`, `UTF8`.

If a name needs its comment to be understood, the name changes.

## No magic values

A character, a string or a number that means something in the code is a named constant at the top of the file, with the name its specification gives it where it has one:

```go
// The structural characters of JSON text, named as RFC 8259 names them.
const beginObject byte = '{'
const valueSeparator byte = ','

// The literal names of JSON text.
const literalNull = "null"
```

A constant is no pattern, since `match c { open => ... }` would read as either the constant or a new name; the compiler says so, `open is a constant, not a pattern: write its value, or a guard, _ if x == open`. Where several arms would compare with constants, the match is written on conditions, as Go writes `switch { case c == beginObject: }`, which reads as the list it is; a guard suits a single arm among patterns:

```go
c := r.peek()
match {
	c == quotationMark => ...,
	c == beginObject => ...,
	c == literalNull[0] => ...,
	_ => ...,
}
```

What stays as a literal is what is its own definition: the escape table of a string, `e == 'n' => '\n'`, and the digits of a number.

## Comments state the rule

Every exported name has a doc comment that starts with its name and says what it does, what it gives back and when it fails. An unexported function has one when the name alone does not say all of that. A comment says what the code means, a rule it keeps or a reason a reader cannot see; it does not repeat the code, and it does not point at a document, a date, a task or another language: the comment is read with the code it sits on, long after any of those has moved.

```go
// skipClose moves past the closing byte close when it comes next, after
// space, and reports whether it did.
```

## Errors are values

A library never panics on what its caller gives it. A value that may be missing is an option, `Node?`; an operation that may fail returns a `Result` whose error says what was found and where, `json: expected a value at offset 12`. `?` passes an error up, and `??` gives the value to use instead (chapter 8). A panic is for a mistake in the program itself, never for a caller's input, and the standard library does not panic on one; `expect` and `testing` are the exception by design, since failing the test that called them is what they are for.

## Borrows and ownership in a signature

A signature says what a function does with what it is given:

- A parameter the function only reads is `ref T`: `func Parse(text ref string)`.
- A parameter it changes is `mut T`.
- A parameter it keeps, stores or gives away is `T`, owned.
- A function that gives back a view of what it was lent returns `ref T`, and the caller's value outlives the view, which the checker holds it to (chapter 6).

An owned value is moved, not copied: `.Clone()` is written only where two owners are wanted.

## Layout

The layout is Go's: tabs, braces on the line of the statement they open, one statement per line. A struct, an enum or a trait of two or more members is written one member per line, a struct's field types in one column, so that each member can be read, and commented, on its own; one of a single member may stay on one line:

```go
enum Value {
	Null
	Bool(bool)
	Number(string)
	String(string)
	Array([]Value)
	Object([]Field)
}

struct Field {
	Key   string
	Value Value
}

struct Text { text string }
```

A blank line comes before and after a block that spans lines, unless the block opens or closes the block around it:

```go
start := r.at
r.skipValue()?

for r.hasNextElement(endArray)? {
	r.skipValue()?
}

return Ok(r.text[start:r.at])
```

## For maintainers

- **The standard library.** Every package under `std/` follows this chapter; a change to one is held to it in review, and `CODING_STANDARDS.md` points here for Snello code.
- **What the compiler holds a program to.** A constant as a pattern is refused, `open is a constant, not a pattern: write its value, or a guard, _ if x == open`, in `compiler/types/match.go`; the rest of this chapter is for people, and is held in review.
