# 2. Getting started

Status: `teak build` produces native binaries for the part of the language the C backend lowers so far (stage 1, task 1.5, first slice): functions, numbers, strings, booleans, `if` and `for`, and `fmt` printing. The rest of core Teak follows in the next two slices.

## The compiler

The compiler is a single program, `teak`. Until there are released binaries it is built from its source, which needs the Go toolchain the compiler is written in; a Teak program needs a C compiler on the path, `cc`, which every machine with developer tools has.

```
git clone https://github.com/teak-lang/teak
cd teak
go build -o teak ./cmd/teak
```

## Hello

A Teak program is a file in a package `main` with a `func main()`:

```teak
package main

import "fmt"

func main() {
    fmt.Println("hello, teak")
}
```

Save it as `hello.teak` and build it:

```
teak build hello.teak
./hello
```

`teak build` writes a native executable named after the file, in the current directory. The file name must end in `.teak`. More programs that build today are under `examples/run/` in the repository, each with the output it prints beside it. The binary contains Teak's runtime and nothing else; it does not need the compiler, or anything the compiler was built with, to run.

## The other commands

`teak check FILE` runs the parser and the checker and prints every error with its position, or nothing when the program is correct. It is what `teak build` runs first, and it is useful on its own while writing.

`teak parse FILE` prints the syntax tree and `teak lex FILE` the tokens. They exist for people working on the compiler.

## A second program

```teak
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
