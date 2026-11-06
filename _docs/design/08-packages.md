# Packages and modules

Status: designed 2026-11-05 for task 5.3 of `../specs/stage-5-libraries-performance.md`. Built the same day: the module file, the three places an import is found, a required module's own file, the cycle message, the refusals of section 4 and the standard library's privileges held back from a package of the program's own, with `snello build FILE` finding the module above the file. Built 2026-11-06: a package of several files, through `token.FileSet` and `types.Config.CheckFiles`. Not built yet: `snello build DIR` and `examples/run/packaged/`. What a package may hand to C is task 5.5's and is not decided here.

A Snello program is one file today, and an import resolves only against the standard library the compiler embeds, so no library can exist outside this repository. This note gives Snello Go's shape for code outside one file: a directory of files is a package, a module file names the directory tree a program's own packages live in, and an import path is found in the standard library, in the module, or through a `require` line that names a directory on disk. No versions and no network: what a registry would add is a stage of its own.

## The precedents

- **Go** makes the files of one directory one package, names a module in `go.mod` and resolves an import path under the module path to a directory under the module's; `replace` points a path at a directory. The standard library is found first, and a path whose first element has no dot is reserved for it.
- **Rust** makes a crate the unit, with modules inside it declared in the source and a `Cargo.toml` naming its dependencies, which Cargo fetches and builds. The crate is a bigger unit than a package and the module tree is written in the code, which is more to learn than Go's rule that the directory is the package.
- **Zig** has no package manager in the language at all; a dependency is a path given to the build. Snello follows Go, as everywhere else, and keeps Zig's modesty about what a build needs: a directory.

## 1. A directory is a package

Every `.snello` file in one directory belongs to one package, and the files see each other's top-level declarations, as Go's do. Each file begins with the same package clause; a directory whose files disagree is refused with Go's message, `found packages shapes (area.snello) and shape (shape.snello) in shapes`. A name declared in two files of one package is a redeclaration, reported as one in one file is, with the other file named: `Area redeclared in this block; other declaration at shape.snello:12:6`. Imports are per file, as in Go: a file sees the packages it imports, not the ones its neighbours do.

The package's name is what its package clause says, and the import path's last element is expected to match it, as Go expects; a program may rename the package at the import, `import s "example.com/app/shapes"`, as it may today. A method is declared in the package of its receiver's type, as in Go; a method on a type of another package is refused with Go's message, `cannot define new methods on non-local type shapes.Circle`.

A package of the program's own is checked under the rules a program is checked under today. What the checker allows only to the standard library stays the standard library's: a function without a body, `...ref any` and `any`, `local`, a slice of borrows, and the lowered names of `fmt`, `rc` and `sync`. A package of the program's own gets each with the message a program gets today, `missing function body`, until task 5.5 decides what crosses to C. The checker learns which kind of package it is checking from the Config it is checked under, as Go's compiler learns from its `-std` flag that it is compiling the standard library, where today it reads it from the import path being empty.

## 2. The main package

The main package is `package main` with a `func main()`, as today. `snello build DIR` builds the main package of a directory, every `.snello` file in it, and names the binary after the directory, as `go build` names it after the directory's last element. `snello build FILE` is unchanged: the one file is the whole main package, as `go run hello.go` compiles the one file named, and a second file beside it is not read. `snello check` and `snello emit` take a file or a directory the same way; `snello lex` and `snello parse` read one file.

The main package is never imported. An import path that resolves to a directory whose package is `main` is refused as Go refuses it: `import "example.com/app/cmd/tool" is a program, not an importable package`.

## 3. The module file

A module is a directory tree of packages under one path, named by a file `snello.mod` at its root:

```
module example.com/app

require example.com/regexp ../regexp
```

The file has two directives. `module PATH` names the module, once. `require PATH DIR` says that the import path `PATH`, and every path under it, is found in the directory `DIR`, relative to the module file's directory or absolute; one line per dependency. A `//` comment runs to the end of its line. Nothing else is in the file: no versions, no `go` directive, no `replace`, since a `require` line already names a directory, which is what Go's `replace` makes of a requirement. A directive the file does not have, or a line that is not one, is refused with its line: `snello.mod:3: unknown directive "exclude"`.

The module path's first element must contain a dot, `example.com`, as Go requires of a path a registry would serve. In Snello the rule serves twice: no module path can be taken for a standard package, which has no dot, and no symbol of a module package can collide with the runtime's own `sn_` names, which are built from dot-less words. The message is Go's: `snello.mod:1: malformed module path "app": missing dot in first path element`.

The module a program belongs to is the one named by the nearest `snello.mod` in the program's directory or above it, as Go finds `go.mod`. A program with no module file above it, every program under `examples/` today, imports the standard library alone, as it does now. A module file is only read; `snello` never writes one.

## 4. How an import path is found

A file imports a path, and the importer answers it from three places, in this order, taking the first that has it:

1. **The standard library**, embedded in the compiler. `fmt` is always `fmt`; a module cannot shadow a standard package, and need not try, since its paths have a dot in the first element.
2. **The module.** A path equal to the module path or under it, `example.com/app/shapes`, names the directory at that place under the module's root, `shapes/`. The directory must exist and hold a `.snello` file: `no package at shapes/ for import example.com/app/shapes`.
3. **The `require` lines.** A path equal to a required path or under it names the directory the line gives, or a directory under it for the rest of the path: with `require example.com/regexp ../regexp`, the import `example.com/regexp` is the directory `../regexp` and `example.com/regexp/syntax` is `../regexp/syntax`. A required module may have a `snello.mod` of its own, whose `module` line must name the path the `require` line gave; its own `require` lines are followed for its own imports, so a library can depend on a library. It may have none, when it is a directory of packages and nothing more.

A path none of the three answers is refused at the import, with the places searched: `package example.com/other is not in the standard library, in module example.com/app or in its require lines`, or, with no module file, today's `package nosuch is not in the standard library`. An import path is a path as Go defines one: elements separated by `/`, none empty, none `.` or `..`; a relative path is refused, `import "./shapes" is relative; name the package by its path under the module`.

An import cycle is refused with the cycle written out, at the import that closes it: `import cycle: example.com/app/a imports example.com/app/b imports example.com/app/a`. The standard library's own message takes the same form.

## 5. One program, one compilation

A package of the program's own is loaded as the standard library's are today: parsed, checked once into the program's `types.Info`, and compiled with the program by whichever backend runs, into the one C file or the one IR module, each of its top-level names qualified by its import path in the backend's symbol, so the module is linked as the program is and link-time optimization sees it whole. Nothing of this changes for a package of the program's own but the qualification: a symbol is `sn_<path>_<Name>` with `/` written as `__` (`07-lowering.md`), and the path of a module package carries characters a C identifier cannot, `.` and `-`, which are each written as `_`: `example.com/app/shapes.Area` is `sn_example_com__app__shapes_Area`. Two import paths that differ only in those characters would share a prefix; the backends refuse the pair as `import paths example.com/a.b and example.com/a_b would share one symbol prefix`, a case no real program meets, and the rule stays with the symbols in `compiler/emit/lower/`, where the importer knows nothing of what a backend names.

A package of several files is several compilation units of one package, so every pass that takes a file and its syntax takes a list of them. A position must then say which file it is in: `token.Pos` is a byte offset in one file today, and gains a base per file through a `token.FileSet`, as `go/token`'s does, so a position anywhere in the program resolves to its file, line and column, and the checker and the backends report an error in a second file with that file's name. The parser fills a file the set made; `parser.ParseFile` keeps its signature for one file on its own.

## 6. What is not here

- **Versions, a registry and fetching.** A dependency is a directory; where it came from is not the compiler's business yet. Go's `go.sum`, Cargo's lock file and the resolution of versions are a stage of their own, after `regexp` has shown what a library outside the compiler needs.
- **Vendoring, build tags, test files.** `_test.snello` and a `snello test` are not decided; `lib/regexp`'s tests in task 5.6 will say what they need.
- **A package's C.** What a package of the program's own may declare without a body, and what it may link, is task 5.5's design, added to this note then.
- **Nested modules and workspaces.** A `snello.mod` under another's tree is not looked for; the nearest file up from the program is the module, and the rest are ignored.

## Decisions

**Decided 1 (2026-11-05): the directory is the package, and the module file is Go's `go.mod` without versions.** The alternative, a package declared in the source as Rust's `mod` tree is, was set aside because a Go developer must read a Snello program without a glossary, and Go's rule needs none: the files beside you are your package. The module file keeps Go's two words, `module` and `require`, and no third, so that a reader of `go.mod` reads `snello.mod` at sight; `require` naming a directory rather than a version is the one difference, and it is Go's `replace` under the name a reader meets first.

**Decided 2 (2026-11-05): no privilege for a package outside the compiler.** A package of the program's own cannot declare a function without a body or write `...ref any`, since what either means at the boundary to C is task 5.5's question; refusing them now with the program's messages keeps the standard library the only package with a C side, which is what task 5.6 must prove enough.

**Decided 3 (2026-11-05): a dot in the module path's first element.** Go only asks it of paths fetched from a registry, and `module app` is common in local Go code. Snello asks it of every module, since it is also what keeps a module's symbols out of the runtime's, and a program that minds writes `module app.local`.
