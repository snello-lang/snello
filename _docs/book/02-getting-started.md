# 2. Getting started

Status: `snello build` produces native binaries for all of core Snello (stage 1, task 1.5): functions, methods and closures, numbers, strings, slices and maps with their methods, `if`, `for` and `range`, structs, enums and `match`, options and results with `?` and `??`, tuples, owned pointers, `take`, `defer`, files through `os` and `io`, and `fmt` printing. Every value is dropped where its owner dies, and a `Drop` method runs there; `snello build -debug` checks the drops at run time (stage 1, task 1.9).

## The compiler

The compiler is a single program, `snello`. Until there are released binaries it is built from its source, which needs the Go toolchain the compiler is written in; a Snello program needs a C compiler on the path, `cc`, which every machine with developer tools has.

```
git clone https://github.com/snello-lang/snello
cd snello
go build -o snello ./cmd/snello
```

## Hello

A Snello program is a file in a package `main` with a `func main()`:

```snello
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

`snello build` writes a native executable named after the file, in the current directory. The file name must end in `.snello`. More programs that build today are under `examples/run/` in the repository, each with the output it prints beside it. The binary contains Snello's runtime and nothing else; it does not need the compiler, or anything the compiler was built with, to run.

`snello build` writes LLVM IR and has clang compile it, with the optimizer that implies; `snello build -backend c FILE` builds the same program through the C backend, the reference implementation, which hands C to the system's C compiler. The two produce binaries that print the same thing, and the compiler's own tests hold them to it. `snello build -debug FILE` builds the same program with a checking runtime, which stops it on a value used after its release and reports, at the end, the values never released. Chapter 5 says what it catches and why; a program that passes the checker should never trip it, so it is a tool for finding the compiler's mistakes, not the program's.

## The other commands

`snello check FILE` runs the parser and the checker and prints every error with its position, or nothing when the program is correct. It is what `snello build` runs first, and it is useful on its own while writing. `snello emit FILE` prints the IR that `snello build` compiles, for reading what the compiler made of a program; `snello emit -backend c FILE` prints the C source.

`snello parse FILE` prints the syntax tree and `snello lex FILE` the tokens. They exist for people working on the compiler.

## A second program

```snello
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
