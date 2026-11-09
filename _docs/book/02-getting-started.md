# 2. Getting started

## The compiler

The compiler is a single program, `snello`. Until there are released binaries it is built from its source, which needs the Go toolchain the compiler is written in; a Snello program needs a C compiler on the path, `cc`, which every machine with developer tools has.

```
git clone https://github.com/snello-lang/snello
cd snello
go build -o snello ./cmd/snello
```

## Hello

A Snello program is a file in a package `main` with a `func main()`:

```go
package main

import "fmt"

func main() {
    fmt.Println("hello, snello")
}
```

Save it as `hello.snello` and build it:

```
snello build hello.snello
./hello
```

`snello build` writes a native executable named after the file, in the current directory, or after the directory when given one: `snello build DIR` builds every `.snello` file in it as one program, which the section on packages below comes to. The file name must end in `.snello`. More programs that build today are under `examples/run/` in the repository, each with the output it prints beside it. The binary contains Snello's runtime and nothing else; it does not need the compiler, or anything the compiler was built with, to run.

`snello build` writes LLVM IR and has clang compile it, with the optimizer that implies; `snello build -backend c FILE` builds the same program through the C backend, the reference implementation, which hands C to the system's C compiler. The two produce binaries that print the same thing, and the compiler's own tests hold them to it. `snello build -debug FILE` builds the same program with a checking runtime, which stops it on a value used after its release and reports, at the end, the values never released. Chapter 5 says what it catches and why; a program that passes the checker should never trip it, so it is a tool for finding the compiler's mistakes, not the program's.

## The other commands

`snello check FILE` runs the parser and the checker and prints every error with its position, or nothing when the program is correct. It is what `snello build` runs first, and it is useful on its own while writing. `snello emit FILE` prints the IR that `snello build` compiles, for reading what the compiler made of a program; `snello emit -backend c FILE` prints the C source.

`snello parse FILE` prints the syntax tree and `snello lex FILE` the tokens. They exist for people working on the compiler.

## Tests

A package's tests live beside it in files named `NAME_test.snello`, which are part of the package only when it is tested: `snello build` never reads one, and nothing of a test reaches a program's binary. A test is a plain function named `Test` and a capital, taking nothing and returning nothing; it passes when it returns, and it fails by a failed expectation, which is a panic the runner catches at the test's boundary, dropping what the test made. The `expect` package asserts and the `testing` package runs; both may be imported from a test file alone. From `examples/run/packaged/shapes/shapes_test.snello`:

```go
package shapes

import ("expect"; "testing")

struct Case { name string; kind Kind; want string }

func TestDescribe() {
    cases := []Case{
        Case{name: "round", kind: Round, want: "round"},
        Case{name: "square", kind: Square, want: "square"},
    }
    for _, c := range cases {
        testing.Run(ref c.name, func() { expect.Equal(ref Describe(c.kind), ref c.want) })
    }
}

func TestArea() {
    expect.Equal(ref Area(Rect{W: 2, H: 3}), ref 6.0)
    expect.True(Area(Circle{Radius: 1}) > 3.14)
}
```

`snello test examples/run/packaged/shapes` builds the package with its tests and a generated `main`, runs it, and prints `ok  	shapes	0.001s`, or each failure with the expression as the test wrote it, then `got` and `want`, and the first difference in words:

```
--- FAIL: TestDescribe/square (0.00s)
    shapes_test.snello:14: expect.Equal(ref Describe(c.kind), ref c.want)
        got:  square
        want: squared   (string)
FAIL
FAIL	shapes	0.001s
```

`snello test -v DIR` prints every test's lines, and `-run NAME` runs the tests whose name holds NAME. The compiler hands every assertion its file and line and the call as the test wrote it, and for `expect.True(n > limit)` the values of the operands, so a failure reads `expect.True(n > limit) is false` and then `n is 2, limit is 3`. Chapter 16 has the rest: every assertion and the message it prints, subtests and tables, logging and skipping, temporary directories, parallel tests, benchmarks, golden files, the flags, testing a `main` package, and how it is all built.

## Packages

A program grows past one file the way a Go program does: a directory is a package, and a module file names the tree the packages live in. Each `.snello` file in a directory begins with the same package clause, and the files see each other's declarations. The directory the program is in is the main package, and `snello build DIR` builds every file in it into a binary named after the directory; `snello build FILE` still builds the one file alone, as `go run hello.go` does.

A file `snello.mod` at the root of the tree marks the module, and an import names a directory under the root:

```
app/
    snello.mod          may be empty: it marks the root
    main.snello         package main; import "shapes"
    shapes/
        shape.snello    package shapes
        area.snello     package shapes
```

```go
package main

import (
    "fmt"
    "shapes"
)

func main() {
    c := shapes.Circle{Radius: 2}
    fmt.Println(shapes.Area(c))
}
```

An import is answered by the standard library first, then by the module's directories, then by a `require` line in `snello.mod`, which names a dependency and the directory on disk that holds it, `require regexp ../regexp`, after which the program imports `"regexp"` and `"regexp/syntax"`. There are no versions and nothing is fetched: a dependency is a directory, as Go's `replace` makes one, and the module has no name, since nothing needs one: a dependency is the directory it is, so two dependencies that each require a third share one copy of it, and a value of the third's types passes between them. A directory or a dependency named as a standard package is refused, so `strings` is always the standard `strings`. Appendix B maps the file onto `go.mod`, and `../design/08-packages.md` has the rules in full.

## A second program

```go
package main

import "fmt"

func fib(n int) int {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}

func main() {
    for i := 0; i < 10; i += 1 {
        fmt.Println(i, fib(i))
    }
}
```

Chapter 3 explains everything this program uses.
