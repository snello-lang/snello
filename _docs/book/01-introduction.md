# 1. Introduction

Snello is a language for programs that must be fast, must not leak, and must be read by the next person without a glossary. It is a compiled language: `snello build` turns a program into a native executable with a small runtime inside it and nothing else. There is no garbage collector. The compiler decides where every value dies, frees it there, and refuses the programs in which that decision would be unsafe. It does this without asking you to write down how long anything lives.

## A taste

```go
package main

import "fmt"

enum Shape {
    Circle(radius f64)
    Rect(w f64, h f64)
}

struct Canvas {
    name   string
    shapes []Shape
}

func (c mut Canvas) Add(s Shape) {
    c.shapes.Push(s)
}

func area(s Shape) f64 {
    return match s {
        Circle(radius) => 3.14 * radius * radius,
        Rect(w, h)     => w * h,
    }
}

func main() {
    canvas := Canvas{name: "sketch", shapes: []Shape{Circle(1.0)}}
    canvas.Add(Rect(2.0, 3.0))
    total := 0.0
    for _, s := range canvas.shapes {    // the loop takes the shapes: they move out of the canvas
        total += area(s)
    }

    fmt.Println(canvas.name, total)
    fmt.Println(len(canvas.shapes))      // COMPILE ERROR "use of moved value canvas.shapes"
}
```

An enum holds one of several variants, each with its own data, and `match` takes it apart and must cover every one. A struct holds fields, and `Add` changes its canvas in place, which its `mut` receiver says. The loop takes the shapes by value, so they move out of the canvas: its name is still there, but the last line, which reads the shapes afterwards, is refused at compile time, before anything runs:

```
shapes.snello:35:21: use of moved value canvas.shapes; moved at 30:23
```

## What a Snello program is made of

A program is a package of functions, structs and enums. A struct holds fields; an enum holds one of several variants, each with its own data; `match` takes an enum apart and must handle every variant. A value has one owner at a time. Passing it to a function or storing it in a field moves it, and the old name is dead; copying is explicit, by `Clone`, except for the small values marked `Copy`, which copy silently. A function that only needs to look at a value takes it as `ref`; one that needs to change it in place takes it as `mut`; one that keeps it takes it by value. These three words, on the parameter and at the call site for `mut`, are the whole of what you write about ownership.

Absence is a value of type `T?`, never a null. A failure is a `Result`, and `?` passes it up. A `Drop` method runs when a value's owner is done with it, so a file closes itself and a temporary directory removes itself, on every path out of a function, including a panic.

## What you get for it

Three things, all checked before the program runs. A value is never used after it was moved or freed. Two parts of a program never change one value at the same time, since a `mut` borrow excludes every other use for as long as it lives. And nothing is freed twice or forgotten: the drops are placed by the compiler, and the compiler's own test suite runs every example program under a debug build that counts them.

The cost is a set of rules to learn, six of them for borrows, which chapters 5 and 6 state in full. Each rule comes with the message you will see when you break it and the idiom that fixes it. The programs that break each rule on purpose live in the repository beside the compiler, and the compiler's tests keep every message in this book true.

## How to read this book

Chapter 2 builds the compiler and runs a first program. Chapters 3 and 4 are the everyday language: numbers, strings, functions, structs, enums and `match`. Chapters 5 to 8 are what makes Snello Snello: ownership, borrows, owned pointers, and options and results. Chapter 9 covers the collections. Every sample is cut from a program under `examples/` in the repository, and each chapter begins with a status line saying how much of it the compiler enforces today. Readers who know a similar language will find what carries over, and what to unlearn, in Appendix B.
