# 40. The language server

`snello lsp` is Snello's language server, a command of `snello` itself: an editor starts it beside itself and talks to it over the Language Server Protocol, the one protocol every editor of note speaks. It reports the errors of the files the editor has open, as the developer types, word for word what `snello check` would print, and it answers hover and go to definition. It checks through the compiler's own loader and checker, so the editor and the build never disagree on a program. `_docs/design/15-language-server.md` has the design.

## What it does

- **Errors as you type.** Every syntax error and every error of the checker, the borrow checker's included, is shown on the file and the word it names, as an editor underlines an error. A change is checked before the next keystroke: a whole package, its tests included, takes about 2 ms on an eight-core arm64 Mac.
- **Every platform's files.** A file for another system, `poll_windows.snello` open on a Mac, is checked for its own system, with nothing to set, and an error only that system's build has says so: `on windows/amd64: undefined: x`.
- **Your files are only read.** The server never fetches a module or writes `snello.lock`: required modules come from the module cache, and a module the cache does not hold is reported with the command that fetches it, `snello fetch`.
- **The editor's text, not the disk's.** A file open in the editor is checked as the editor holds it, saved or not; a file of the package that is not open is read from the disk. A package edited in one window and imported in another is checked as it stands in the editor. A new file joins its package once it is saved.
- **Test files too.** A package's test files are checked as `snello test` loads them, so an error in a test is shown on the test file.
- **The standard library's own sources.** In a checkout of the compiler, a file under `std/` is checked as the standard package it is, with the library's privileges, its functions the runtime provides and its fields only the library writes, and the whole library read from the checkout as the editor holds it, as `snello test std/NAME` builds it. `std/builtin`, which documents the predeclared names and is never compiled, shows nothing but its tests' errors. References to a name the library exports reach every package of the checkout; a rename of one is refused, since programs outside the checkout use it through the compiler's own copy. The checkout and the `snello` running the server are to be of one version: a `std/` without a package the running compiler has is not taken for the library.
- **One-file programs.** A directory in which more than one file declares `main`, as `examples/tree/` does, holds programs of one file each, and each is checked alone, as `snello build FILE` builds it.
- **Hover.** On a name, the declaration as the checker types it, with its doc comment below: `func Largest<A: Shape, B: Shape>(a ref A, b ref B) f64`, a struct with its fields, a trait with its methods, `var count int`, `const Pi f64 = 3.141592653589793`. On an operator, a literal or a parenthesis, the type of its expression. On a package's name, its import's path or the package clause, the package's own comment, as `snello doc` prints it. On a name the language declares, `Eq`, `int`, `len`, `true`, `Some`, or a method of one of its types, `xs.Push`, `s.Contains`, `o.IsSome`, `n.CheckedAdd(1)` on any integer, the comment `std/builtin/builtin.snello` writes for it. On a form the language writes with its own syntax, `map`, `chan`, `[]`, `*` or `?` in a type, or `ref` and `mut` in a type or at a call, the form as written there, `map[string]int`, with its comment; on every keyword, `spawn`, `match`, `defer`, `select`, `impl`, `super` among them, and on the language's own operators, `x?`, `x ?? y`, `<-`, `&`, `=>`, `...`, `@` and a pattern's `|` and `..=`, and on the blank name `_`, their comments too. On an arithmetic operator, a comparison or a compound assignment, `+`, `<`, `+=`, the type of what it gives with the comment of the standard trait behind it, `Add` or `Ord`. On `Result` in a type and on a name in an attribute, the comment of what it names; on a parameter of a function without a body, its declaration; and on `f.default`, `f.name` or `f.type` of a field `fields(T)` gives, in an expression or in a type, the comment of `fields`. The blank name `_` has its comment wherever it stands. A guard's `if`, and in a match on a type an arm's `|` and its kind, `struct` or `enum`, have the pattern's. In `builtin.snello` itself, a name it declares or uses has its comment too.
- **Go to definition.** From a name to where it is declared: in the file, in another file of the package, in another package of the module or a module it requires, or in the standard library. A method called through a trait bound goes to the trait's method. The standard library is inside the compiler, so the server writes the file it is asked about to `std/` in Snello's cache directory, the one `SNELLO_CACHE` names or `snello/` in your user cache, read-only, and opens that copy; hover and definition work in it, and it shows no errors, since it is the library's own code. With `SNELLO_CACHE=off` the server says it cannot. From a file of the library's own checkout, the definition is the checkout's file. A name the language declares, and a form, goes to where `std/builtin/builtin.snello` documents it, in the same read-only copy, and a name in `builtin.snello` itself to its declaration there. On an import's path or a package clause, definition opens the package's file that carries its comment.
- **Through a syntax error.** While a file does not parse, its package cannot be checked: hover and definition answer nothing in that file, and the files beside it keep the answers of the last check until the error is fixed.

- **Outline.** A file's declarations as a tree: its structs with their fields, its enums with their variants, its traits with their fields and methods, and each type's methods under it, then its functions, variables and constants. It comes from the file's syntax, so a file with type errors still has its outline.
- **Workspace symbols.** Every declaration of the module, found by a few of its letters in order: `lgst` finds `Largest`. The module is the one of each open file, and of the folder the editor has open, found by its `snello.mod`.
- **References.** Every use of a name across the packages that can name it: its own package, tests included, and every package of the module that imports it, directly or through another. The two `Shape` traits of `examples/packaged/`, the program's and the package's, are told apart.
- **Rename.** Renames a name at its declaration and every use, across the module and on every platform's files, in one edit the editor applies at once. It refuses a name of the language, of the standard library, of a module you require, or a package's name, and a new name that is a keyword. A method is renamed with every method it shares a dispatch with, a trait's method with each type's that implements it and each trait's that overrides it, from whichever of them you start. Before it answers, the packages are checked with the new name: a rename the checker would refuse is refused with the checker's own message, renaming `Largest` to `Area` in `examples/packaged/shapes` says `Area redeclared in this block`, and so is one that would compile and do something else: a name that would then refer to another declaration, or a method that would then override, or stop overriding, one it does not now. A method that shares its dispatch with one of a trait the language declares, `Clone`, keeps its name. While a package that uses the name does not compile, rename waits for it to.

- **Completion.** After a dot, the members of what is before it: a value's fields and methods, its traits' and its bounds' among them, or the exported names of a package. On a name, every name visible there, the innermost first: the locals in scope, the parameters, the package's names, the imports and the language's, then the keywords; never a local declared further down. In an import's quotes, the standard library's packages and your module's. The list comes in the same order every time, and nothing is offered in a string or a comment.

Formatting is not here yet; it comes as its own task, and the server tells the editor what it does, so an editor never asks for what it cannot give.

## Running it

The server is part of the `snello` command, so wherever `snello` is installed the server is too, at the same version, and there is nothing else to install or set: `snello lsp` speaks on its standard input and output and writes its own logs to standard error. It takes `--stdio` and `--clientProcessId`, which some editors pass, and needs neither, and it reads no settings: the module is found by its `snello.mod` and a file's platform by its name.

In VS Code, the [Snello extension](https://github.com/snello-lang/vscode-extension) starts it for you, the first time a Snello file opens in a window, and stops it when the window closes, and gives VS Code Snello's grammar. It runs the first `snello` on the `PATH`, or the command whose absolute path the setting `snello.serverPath` names, and starts it again when the setting changes; without one, it says so once and highlights as before. In Restricted Mode a folder's own settings cannot choose the command. Its README says how to build and install it.

Editors that take a server by its command need only the file type and the command:

In Neovim 0.11 and later, in `init.lua`:

```lua
vim.filetype.add({ extension = { snello = "snello" } })
vim.lsp.config("snello", { cmd = { "snello", "lsp" }, filetypes = { "snello" }, root_markers = { "snello.mod" } })
vim.lsp.enable("snello")
```

In Helix, in `languages.toml`:

```toml
[language-server.snello]
command = "snello"
args = ["lsp"]

[[language]]
name = "snello"
scope = "source.snello"
file-types = ["snello"]
roots = ["snello.mod"]
language-servers = ["snello"]
```

## For maintainers

The server is three packages and a command, each with its tests:

| Package | What it does |
|---|---|
| `lsp/jsonrpc` | the framing, `Content-Length` and the body, and the three kinds of message |
| `lsp/protocol` | the protocol's types the server uses, named as the specification names them |
| `lsp/server` | the documents, the checks and the diagnostics, and the session's loop |
| `cmd/snello` | the command, `snello lsp` among its commands, which runs one session over standard input and output |

One goroutine reads the stream; another owns the documents and the results and does everything else, so nothing the server keeps is shared. Messages that arrive while a check runs are handled together after it, and every package with an open document is checked once after them, so a burst of typing costs one check at its end; a hover or a definition asked meanwhile is answered after that check, on the latest text. `BenchmarkCheck` measures a check of every package under `examples/`.

Each check leaves a view, `lsp/server/view.go`: the checker's `types.Info` on the package's own files and their syntax trees, which the loader hands over through `importer.Options.Parsed`, every file the check parsed, which a position is resolved against, and the sources it read; the rest of the check is let go when it is kept. Hover and definition find the identifier under the cursor with `ast.Inspect`, or else the token there and the innermost node that owns it, and read its object or its type from the `Info`. A load puts every file it parses into one `token.FileSet`, so that an object's position names its file in whichever package declares it. The hover's declaration is `types.ObjectString`; its doc comment is read by parsing the declaring file again with `parser.ParseComments`, on the hover alone.

The outline and the workspace's symbols are read from syntax alone, `lsp/server/symbols.go`; a declaration's stretch ends at its last token, which the server finds by scanning the text, since a syntax node does not always record where it ends. Completion, `lsp/server/completion.go`, writes a name of its own over the one being typed, so that a half-typed line parses, and checks the package with the place given to the checker, `importer.Options.At` and `types.Config.At`, which records the names visible there as it resolves that name, `types.Info.Visible`.

References and rename, `lsp/server/references.go`, check the packages that can name the object when the request comes: those that import its package, each import resolved through `Module.Lookup` as the importer resolves it, that spell the name, for every platform their files differ for. They know the object across those checks by where it is declared, its file and its offset, since each check makes its own objects. A rename of a method widens to its whole dispatch, found from the `impl` lists and the traits refined, and checks the packages again with the edits in place: a new error from the checker refuses it, and so does a name that would refer to another declaration than before, or a dispatch the methods would join. `BenchmarkReferences` measures both.

The server reads sources through the loader's read function, `importer.Load.Read` and `Importer.Read`, which it answers from the open documents; every command of the compiler leaves it empty and reads the disk. A directory is checked by `importer.CheckDir`, the function `snello check DIR` uses, and a library's tests by `importer.CheckTests`. A position is converted from the byte offset of the compiler's position to the protocol's line and character, in UTF-8 when the editor offers it and in UTF-16 otherwise. The module is resolved from the module cache alone, `importer.Options.Cached`, so a check never fetches or writes.

A new capability is a method the server answers, announced in its answer to `initialize`, with its types added to `lsp/protocol` and a scripted session in `lsp/server/server_test.go` that asks for it. `go test ./lsp/...` runs the framing's tests, the positions' in each encoding, the documents' changes, a sweep that opens every package under `examples/` and finds no diagnostic but the pending example's, which match `snello check`'s, sessions over a pipe that edit a module into errors and back, and sessions over `examples/packaged/` that ask for definitions, hovers, outlines, symbols, references and renames and check them against the declarations; the rename's copy is built and run.
