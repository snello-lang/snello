# 40. The language server

`snellols` is Snello's language server: an editor starts it beside itself and talks to it over the Language Server Protocol, the one protocol every editor of note speaks, and it reports the errors of the files the editor has open, as the developer types, word for word what `snello check` would print. It checks through the compiler's own loader and checker, so the editor and the build never disagree on a program. `_docs/design/15-language-server.md` has the design.

## What it does

- **Errors as you type.** Every syntax error and every error of the checker, the borrow checker's included, is shown on the file and the word it names, as an editor underlines an error. A change is checked before the next keystroke: a whole package, its tests included, takes about 2 ms on an eight-core arm64 Mac.
- **Your files are only read.** The server never fetches a module or writes `snello.lock`: required modules come from the module cache, and a module the cache does not hold is reported with the command that fetches it, `snello fetch`.
- **The editor's text, not the disk's.** A file open in the editor is checked as the editor holds it, saved or not; a file of the package that is not open is read from the disk. A package edited in one window and imported in another is checked as it stands in the editor. A new file joins its package once it is saved.
- **Test files too.** A package's test files are checked as `snello test` loads them, so an error in a test is shown on the test file.
- **One-file programs.** A directory in which more than one file declares `main`, as `examples/tree/` does, holds programs of one file each, and each is checked alone, as `snello build FILE` builds it.

Hover, go to definition, symbols, completion and formatting are not here yet; each comes as its own task of the Language server milestone, and the server tells the editor what it does, so an editor never asks for what it cannot give.

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

One goroutine reads the stream; another owns the documents and the results and does everything else, so nothing the server keeps is shared. Messages that arrive while a check runs are handled together after it, and every package with an open document is checked once after them, so a burst of typing costs one check at its end. `BenchmarkCheck` measures a check of every package under `examples/`.

The server reads sources through the loader's read function, `importer.Load.Read` and `Importer.Read`, which it answers from the open documents; every command of the compiler leaves it empty and reads the disk. A directory is checked by `importer.CheckDir`, the function `snello check DIR` uses, and a library's tests by `importer.CheckTests`. A position is converted from the byte offset of the compiler's position to the protocol's line and character, in UTF-8 when the editor offers it and in UTF-16 otherwise. The module is resolved from the module cache alone, `importer.Options.Cached`, so a check never fetches or writes.

A new capability is a method the server answers, announced in its answer to `initialize`, with its types added to `lsp/protocol` and a scripted session in `lsp/server/server_test.go` that asks for it. `go test ./lsp/...` runs the framing's tests, the positions' in each encoding, the documents' changes, a sweep that opens every package under `examples/` and finds no diagnostic but the pending example's, which match `snello check`'s, and sessions over a pipe that edit a module into errors and back.
