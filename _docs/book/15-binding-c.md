# 15. Binding C

`examples/bound/` runs every form below through both backends, as a release and a debug build.

The standard library is small on purpose, and what it leaves out is written as libraries outside the compiler, on the packages of chapter 2. Many such libraries are not written from scratch: a database, a compression format or a cryptographic suite already exists as a C library on every system, and the library a Snello program wants is a thin layer over it. This chapter is how a package of your own reaches C: declares a function C defines, passes values to it and gets them back, holds what C hands it, and tells the build which system library C needs.

## A function C defines

A function declared without a body in a package is defined in C. The compiler gives it a symbol from the package's path and the function's name, and the package's `.c` files, beside its `.snello` files, define it under that symbol. Here is a package `tally` whose counter lives on the C side:

```go
package tally

func open(start int) int
func add(handle int, n int) int
func release(handle int)
func Live() int
```

For each of these the compiler writes a header, `tally.snello.h`, into the build, with the prototype the Snello signature means: `sn_int snm_tally_add(sn_int handle, sn_int n);` for `add`. The package's C includes that header and defines the functions:

```c
#include "tally.snello.h"

static int64_t live;

sn_int snm_tally_add(sn_int handle, sn_int n) {
    struct counter *c = (struct counter *)(intptr_t)handle;
    c->n += n;
    return c->n;
}
```

A definition that disagrees with its declaration, a parameter too few or a type too wide, is an error the C compiler reports, since the header says what the Snello side expects. The symbol is never anything but the derived one: a function of a system library is reached through a definition of one line that calls it, as `mathx` does below.

Only a package binds C. The main package is refused a function without a body, so the C side of a program is always a directory a reader can find, and a program that needs none is never near one.

## What crosses

The values that cross are the ones the runtime passes already, in the form the runtime header `snello.h` gives them, and nothing is converted on the way:

| Snello | C | How |
|---|---|---|
| `int`, `uint`, the sized integers, `byte` | `sn_int`, `sn_uint`, `int32_t`, `uint8_t`, and so on | by value |
| `f64`, `f32`, `bool` | `double`, `float`, `bool` | by value |
| `ref string` | `sn_string` | a view of the bytes, pointer and length, for the call |
| `ref []byte` | `sn_slice_u8` | a view of the bytes |
| `mut []byte` | `sn_slice_u8 *` | a buffer C fills within its capacity, setting its length |
| `mut int`, `mut bool`, any `mut` number | `sn_int *`, `bool *` | an out-parameter: a count, an error code, a yes |

Everything else is refused where the function is declared, with its type named: `cannot cross to C: map[string]int, in parameter counts`. A struct, a pointer, a slice of anything but bytes, a map, an owned `string`, an option, a result, a tuple, a closure, a trait value or a type parameter does not cross. A library that needs more converts on the Snello side, around the binding, where the ownership rules hold.

```go
// Sum adds the bytes of xs, which C reads as a view.
func Sum(xs ref []byte) int

// Fill writes b over the whole room of buf and returns how many bytes it wrote.
func Fill(buf mut []byte, b byte) int

// Parse reads a decimal number from s into n and reports whether s was one.
func Parse(s ref string, n mut int) bool
```

## A handle

A C library hands back something it owns and the program must give back later: a database connection, a file of its own format, a parser. It crosses as an `int`, the pointer's value, which Snello never reads, and the package wraps it in a struct with a `Drop` method, so that dropping the struct gives the thing back:

```go
struct Tally impl Drop { handle int }

func (t mut Tally) Drop() { release(t.handle) }

func (t mut Tally) Add(n int) int { return add(t.handle, n) }

func Open(start int) *Tally { return &Tally{handle: open(start)} }
```

The struct is made and freed by the program as any value is, and chapter 5's rules hold: a `Tally` has one owner, and where that owner dies the counter is released. The program sees it:

```go
{
    t := tally.Open(40)
    fmt.Println(t.Add(2), t.Count(), tally.Live())   // 42 42 1
}
fmt.Println(tally.Live())                            // 0
```

## A system library

C that calls a library installed on the machine needs that library at the link. The module file says which, one line per library, by the name the linker knows:

```
link m
```

That is the math library, and it becomes `-lm` when every program that imports a package of the module is linked; `link sqlite3` or `link z` name the SQLite or zlib libraries the same way. A flag or a path is refused: the line names a library, and the system's linker finds it. Binding one of its functions is then the one-line definition:

```go
package mathx

// Cbrt is the cube root of x, from the system's math library.
func Cbrt(x f64) f64
```

```c
#include "mathx.snello.h"
#include <math.h>

double snm_mathx_Cbrt(double x) {
    return cbrt(x);
}
```

## What the C side keeps to

The compiler checks the types, and the author keeps three promises the compiler cannot check. The C side keeps no view past the call, since the bytes belong to the caller and may move or be freed when it returns. It calls nothing of the program back, since no Snello function has a symbol a library may rely on. And a call holds the worker thread it runs on until it returns, as a file read does, so a C function that waits on a device holds one of the program's workers while it waits. Memory the C side takes for itself is its own to free; the debug build's tracker counts only what the program allocates.

## Checking a package on its own

`snello check tally` checks the package as the library it is, bindings included; `snello build` of the program compiles the package's C beside the runtime and links it. A package that declares a function without a body and has no `.c` file beside it is refused when it is imported, naming the function, and so is a package shipping a file under the name of the header the compiler writes.

The whole of this chapter is `examples/bound/` in the repository: `tally` and `mathx` beside a program that uses both, with the output it prints beside the directory.
