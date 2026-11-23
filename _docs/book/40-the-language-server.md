# 40. The language server

`snellols` is Snello's language server: an editor starts it beside itself and talks to it over the Language Server Protocol, the one protocol every editor of note speaks. It reports the errors of the files the editor has open, as the developer types, word for word what `snello check` would print, and it answers hover and go to definition. It checks through the compiler's own loader and checker, so the editor and the build never disagree on a program. `_docs/design/15-language-server.md` has the design.

## What it does

- **Errors as you type.** Every syntax error and every error of the checker, the borrow checker's included, is shown on the file and the word it names, as an editor underlines an error. A change is checked before the next keystroke: a whole package, its tests included, takes about 2 ms on an eight-core arm64 Mac.
- **Your files are only read.** The server never fetches a module or writes `snello.lock`: required modules come from the module cache, and a module the cache does not hold is reported with the command that fetches it, `snello fetch`.
- **The editor's text, not the disk's.** A file open in the editor is checked as the editor holds it, saved or not; a file of the package that is not open is read from the disk. A package edited in one window and imported in another is checked as it stands in the editor. A new file joins its package once it is saved.
- **Test files too.** A package's test files are checked as `snello test` loads them, so an error in a test is shown on the test file.
- **One-file programs.** A directory in which more than one file declares `main`, as `examples/tree/` does, holds programs of one file each, and each is checked alone, as `snello build FILE` builds it.
- **Hover.** On a name, the declaration as the checker types it, with its doc comment below: `func Largest<A: Shape, B: Shape>(a ref A, b ref B) f64`, a struct with its fields, a trait with its methods, `var count int`, `const Pi f64 = 3.141592653589793`. On an operator, a literal or a parenthesis, the type of its expression.
- **Go to definition.** From a name to where it is declared: in the file, in another file of the package, in another package of the module or a module it requires, or in the standard library. A method called through a trait bound goes to the trait's method. The standard library is inside the compiler, so the server writes the file it is asked about to `std/` in Snello's cache directory, the one `SNELLO_CACHE` names or `snello/` in your user cache, read-only, and opens that copy; hover and definition work in it, and it shows no errors, since it is the library's own code. With `SNELLO_CACHE=off` the server says it cannot.
- **Through a syntax error.** While a file does not parse, its package cannot be checked: hover and definition answer nothing in that file, and the files beside it keep the answers of the last check until the error is fixed.

Symbols, references, rename, completion and formatting are not here yet; each comes as its own task of the Language server milestone, and the server tells the editor what it does, so an editor never asks for what it cannot give.

## Running it

`snellols` is not yet in the release archives. From a checkout of the compiler, `go install ./cmd/snellols` puts it on the path beside `go`'s other installs. It speaks on its standard input and output and writes its own logs to standard error; it takes `--stdio`, which some editors pass, and `-version`.

The VS Code extension, which starts it for you and gives VS Code Snello's grammar, is its own task. Editors that take a server by its command need only the file type and the command:

In Neovim 0.11 and later, in `init.lua`:

```lua
vim.filetype.add({ extension = { snello = "snello" } })
vim.lsp.config("snellols", { cmd = { "snellols" }, filetypes = { "snello" }, root_markers = { "snello.mod" } })
vim.lsp.enable("snellols")
```

In Helix, in `languages.toml`:

```toml
[language-server.snellols]
command = "snellols"

[[language]]
name = "snello"
scope = "source.snello"
file-types = ["snello"]
roots = ["snello.mod"]
language-servers = ["snellols"]
```

## For maintainers

The server is three packages and a command, each with its tests:

| Package | What it does |
|---|---|
| `lsp/jsonrpc` | the framing, `Content-Length` and the body, and the three kinds of message |
| `lsp/protocol` | the protocol's types the server uses, named as the specification names them |
| `lsp/server` | the documents, the checks and the diagnostics, and the session's loop |
| `cmd/snellols` | the command, which runs one session over standard input and output |

One goroutine reads the stream; another owns the documents and the results and does everything else, so nothing the server keeps is shared. Messages that arrive while a check runs are handled together after it, and every package with an open document is checked once after them, so a burst of typing costs one check at its end; a hover or a definition asked meanwhile is answered after that check, on the latest text. `BenchmarkCheck` measures a check of every package under `examples/`.

Each check leaves a view, `lsp/server/view.go`: the checker's `types.Info` on the package's own files and their syntax trees, which the loader hands over through `importer.Options.Parsed`, every file the check parsed, which a position is resolved against, and the sources it read; the rest of the check is let go when it is kept. Hover and definition find the identifier under the cursor with `ast.Inspect`, or else the token there and the innermost node that owns it, and read its object or its type from the `Info`. A load puts every file it parses into one `token.FileSet`, so that an object's position names its file in whichever package declares it. The hover's declaration is `types.ObjectString`; its doc comment is read by parsing the declaring file again with `parser.ParseComments`, on the hover alone.

The server reads sources through the loader's read function, `importer.Load.Read` and `Importer.Read`, which it answers from the open documents; every command of the compiler leaves it empty and reads the disk. A directory is checked by `importer.CheckDir`, the function `snello check DIR` uses, and a library's tests by `importer.CheckTests`. A position is converted from the byte offset of the compiler's position to the protocol's line and character, in UTF-8 when the editor offers it and in UTF-16 otherwise. The module is resolved from the module cache alone, `importer.Options.Cached`, so a check never fetches or writes.

A new capability is a method the server answers, announced in its answer to `initialize`, with its types added to `lsp/protocol` and a scripted session in `lsp/server/server_test.go` that asks for it. `go test ./lsp/...` runs the framing's tests, the positions' in each encoding, the documents' changes, a sweep that opens every package under `examples/` and finds no diagnostic but the pending example's, which match `snello check`'s, sessions over a pipe that edit a module into errors and back, and sessions over `examples/packaged/` that ask for definitions and hovers and check them against the declarations.
