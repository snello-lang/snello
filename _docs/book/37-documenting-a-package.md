# 37. Documenting a package

The design is `../design/13-documentation.md`. The command is `cmd/snello/doc.go`; what it reads and prints is `compiler/doc/`, over the comments the parser keeps when asked, `compiler/parser/comments.go`.

A package documents itself with comments on its declarations, and `snello doc` prints them: a package's overview, a name's page, or a member of a type, for the standard library, the predeclared names and the program's own packages alike. What it prints comes from the package as the checker sees it, so a page is always a package that compiles, and it shows what a signature says that a comment need not repeat: how each argument is passed, `ref`, `mut` or by value, and the traits a type implements.

## Writing a doc comment

A doc comment is the comment immediately above a declaration, with no blank line between: above `package` for the package, above a function, a method, a type, a trait, a constant or a variable, above a trait's method, above a field of a struct and above a variant of an enum. A field, a variant, a constant or a variable may instead carry a comment at the end of its line. A comment anywhere else, inside a body or after a blank line, is not documentation.

```go
// Package geo measures shapes.
package geo

// A Point is a place on the plane.
struct Point {
    // X is the column, counted from the left.
    X int
    Y int // the row, counted from the top
}

// Area returns the area of a square of side s.
func Area(s int) int { return s * s }
```

A comment begins with the name it documents and says what the thing is or does, in full sentences: `// Area returns ...`, `// A Point is ...`. A package has one comment, above `package` in one of its files.

Constants and variables written on consecutive lines are one group, and the comment above the first is the group's: each keeps its own comment at the end of its line, and the page prints the group together.

```go
// The units of length, in millimetres.
const Millimetre = 1   // the unit
const Metre = 1000     // a thousand units
```

The text of a comment is plain, and reads as written in the source:

- **A paragraph** is lines of text; a blank comment line, `//` alone, ends it.
- **A heading** is a line `# Heading` alone, with a blank line before and after.
- **A list** is lines indented past the text that begin with `-`, `*`, `+` or a number and `.` or `)`; an indented line without a marker continues the item above.
- **Code** is any other indented lines, shown as written.
- **A link** is a name in brackets: `[Point]`, `[Point.X]` for a member, `[io.Reader]` for a name of an imported package, `[unicode/utf8.RuneLen]` by a full path, `[strings]` for a package, `[Option]` for a predeclared name. A web address, `https://...`, is a link too.

```go
// Read fills buf, as an [io.Reader] does, and returns how many bytes it
// wrote. Use it in a loop:
//
//	for {
//	    n := r.Read(mut buf)?
//	    if n == 0 { break }
//	}
//
// It fails when:
//   - the connection is closed;
//   - the deadline passes.
```

A link that names nothing is warned of, with the file, line and column of its comment, and the page prints it as written, in its brackets: a link left behind by a rename is found the first time the page is read. Brackets around anything else, `[0, n)` or `v.[f]`, are text.

## Reading the documentation

```
snello doc                      the package in the current directory
snello doc strings              a package: its comment, and a line for each exported name
snello doc json                 a package by the last element of its path, encoding/json, when one package has it
snello doc strings.Builder      a name: its declaration and comment, its methods, the functions that make one
snello doc strings.Builder.Len  a method, a field, a variant, or a function of the type
snello doc Builder              a name looked for in the current package, then in the standard library
snello doc builtin.Option       a predeclared name
snello doc -all strings         every exported name with its comment
snello doc -u strings           the unexported names too
```

A package is named as an import names it, `strings`, `unicode/utf8`, or a package of the program's module or a dependency's, or by its directory, `./geo`. An overview lists the package's constants and variables, its functions, and its types, each type with the functions that make it, its functions of the type and its methods under it:

```
$ snello doc strings
package strings // import "strings"

Package strings builds and transforms text.
...

func Join(parts ref []string, sep ref string) string
func Repeat(s ref string, n int) string?
...

struct Builder { ... }
    func (b ref Builder) Len() int
    func (b mut Builder) WriteString(s ref string)
    ...

struct Reader impl io.Reader { ... }
    func NewReader(s string) Reader
    func (r mut Reader) Read(buf mut []byte) Result<int, io.Error>
```

A name's page prints its declaration, its comment under it, and what belongs to it. A struct shows its exported fields, with their comments, defaults and attributes, and says when it has others; an enum shows its variants; a trait shows its methods and lists the types that implement it, in the standard library and in the program's module, and for a predeclared trait the predeclared types too:

```
$ snello doc strings.Builder
struct Builder {
    // Has unexported fields.
}
    A Builder accumulates text piece by piece and gives it back as one
    string, ...

func (b ref Builder) Len() int
func (b mut Builder) WriteString(s ref string)
...

$ snello doc io.Reader
trait Reader {
    func (r mut Self) Read(buf mut []byte) Result<int, Error>
}
    A Reader is a source of bytes: ...

Implemented by:
    bufio.Reader, bytes.Buffer, net.Conn, os.File, os/exec.Pipe, strings.Reader
```

A function that returns a type, as `T`, `*T`, `T?` or `Result<T, E>`, is listed under that type, as the way to make one. A name with no package is looked for in the current directory's package, then in every standard package; when several have it, the command says which, `snello doc: Reader is in bufio, io and strings`, and a package and a dot settle it. A name or a package that is not found is said so, and the command exits 1.

The files of a package are chosen for the platform the command runs on, as a build for it chooses them.

## Examples

An example is a function of a test file that shows a name in use and says what it prints, which `snello test` checks (chapter 16), so an example on a page always runs:

```go
func ExampleArea() {
    fmt.Println(Area(3))
    // Output: 9
}
```

`snello doc` shows it on the page of the name it documents, its code as written and what it prints; a package's own examples, named `Example`, show with `-all`:

```
$ snello doc geo.Area

func Area(s int) int
    Area returns the area of a square of side s.

Example:
    fmt.Println(Area(3))

    Output:
    9
```

## Pages in a browser

```
$ snello doc -http
Serving the documentation at http://localhost:6060/
```

`snello doc -http` serves the same pages to a browser, for every standard package, `builtin` among them, and every package of the module it runs in. A sidebar lists the packages in groups, the language's `builtin`, the standard library and the module's own, with the current package's names under it and a search box above that filters them as it is typed. A page begins with the package's comment and a summary of its types, functions, constants and variables, each with its first sentence, and then shows each declaration under a header bar, with an anchor of its own, its comment with its links to the pages they name, its examples, a type's methods under it and a trait's implementors. The index lists the packages in their groups, and its search box finds any exported name of any package. A button switches between the light and the dark theme, which follows the system's until it is pressed. A page is made when it is asked for, from the package as it is on disk then, so an edit shows on the next reload; nothing is written to disk.

The server answers the local machine alone: it listens on `localhost:6060`, or on the address `-http=HOST:PORT` names, warning when that address is not the local machine's, and answers only a browser that asks for it by a local name, so that a page of another site cannot read a private module through it. It runs until it is stopped, with Ctrl-C.

## For maintainers

**The comments.** The scanner skips comments, and keeps them only when asked, `Scanner.KeepComments`, in a list behind one pointer in its own state: a scanner that keeps none is one word larger, which leaves the parser holding it in its allocation's size class, so a build allocates what it did, to the byte, as the scanner's, the parser's and the checker's benchmarks over the examples show. `parser.ParseComments` parses with the comments kept and, once the file is parsed, attaches them in one pass by their lines (`comments.go`): a group of comments on consecutive lines, the first beginning its line, ending on the line above a declaration is its doc comment; a comment after a token on a field's, a variant's, a constant's or a variable's line is its line comment; a member written on its type's own line takes no doc comment, since the one above is the type's. The file holds them behind one pointer, `ast.File.Docs`, nil for a build. The importer keeps comments for the packages `Importer.Comments` names, and `importer.Load.Comments` for a program's own files.

**The model.** `doc.New` builds a package's pages from its files and its checked package: the package's comment, its constants and variables in their groups, its functions, and its types with their makers, functions of the type and methods, each sorted by name; `doc.Builtin` parses `std/builtin`, which is never compiled, alone. `doc.Parse` reads a comment's text into blocks and `doc.Spans` a line into text and links, by the rules above. A link is resolved in the package's own declarations and their members, then as a package the file imports, by its path, or by its name, through the checker's scopes, then among the predeclared names, `std/builtin`'s declarations; every comment's links are resolved as the package is built, and those that name nothing are the page's `Broken`. `doc.Implementors` reads the `impl` written on each type, through every trait that refines the one asked for, and asks the checker, `types.Implements`, which predeclared types a predeclared trait has. A page is written into one `strings.Builder`, through the writers of `compiler/ast/source.go`, `ast.WriteSignature`, `ast.WriteType` and the rest, which write a declaration as it is written.

**Examples.** `doc.IsExample`, `doc.ExampleTarget` and `doc.ExpectedOutput` are the one reading of an example's name and of its output comment, which both the runner, `compiler/testrun/examples.go`, and the pages use: the runner refuses an example with a signature other than `func()` or a name that names nothing, and lists each that says what it prints in the generated `main` as a `testing.Example`; `testing.Main` runs them after each round's tests on its own task, under `Catch`, with the standard output pointed at a file by the runtime's `sn_testing_captureStart` and back by `sn_testing_captureEnd` (`runtime/c/testing.c`), `dup2` and, on Windows, `_dup2` with `SetStdHandle` for a child and the console's cache cleared; a page cuts an example's code from its test file's source, between its braces and before its output comment.

**The pages.** `doc.WriteHTML` and `doc.WriteIndex` write a page from the same model the terminal prints, through `html/template`, which escapes every comment, name and value, with a `doc.Nav`, the sidebar's groups, which the server gives; the templates, the style and the script are `compiler/doc/assets/pages.html`, `style.css` and `script.js`, embedded in the compiler, the last two served under `/assets/` with the brand's icon, `doc.Asset`, a copy of `_docs/brand/icon.svg` under the license header, which a test holds it to; `Package.Target` gives where a link points, a package's path and the anchor on its page, which `Resolve` asks too. `cmd/snello/dochttp.go` is the server, over the standard library's `net` and `net/textproto`: one request a connection, read with a limit and a deadline, answered with the page's length and a close; it loads its packages at each request, answers `GET` and `HEAD` alone, and refuses a request whose `Host` is not the address it serves or a loopback name on its port. It does not use `net/http`, which made every command start about 3 ms later from the size it added to `snello`; the templates are parsed the first time a page is written, so a command that writes none pays nothing for them.

**Trying it.** `make doc` runs `build/doc.sh`, which prints each form and flag of the command once, with the command line above what it printed, ending with `examples/packaged/`'s package and its example; `make doc-serve` serves the pages, from the development image with its port published on the host's loopback address alone, or natively with `NATIVE=1`.

**The command.** `snello doc` loads every package with its comments, through an importer of the current directory's module: a directory under the module's root by its path there, the root or a directory under no module as a program, a standard package or a module's by its import path, and the standard library one package at a time when a name is looked for. The tests are `cmd/snello/doc_test.go`, over the standard library and a module written for them, and `compiler/doc`'s, which hold every standard package's comments to links that name something.
