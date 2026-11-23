# The language server

Status: being built. The server, its documents and its diagnostics, `snellols`; hover and definition, symbols, completion and formatting are later tasks of the same milestone, and the editor extension is its own.

An editor learns what a program means from a language server: a process it starts beside itself and talks to over the Language Server Protocol, JSON-RPC 2.0 messages on the server's standard input and output. This note gives Snello one, `snellols`, written in Go beside the compiler and built from the same packages, so that what the editor shows is what `snello check` says, word for word, with no second front end to drift from the first.

## The precedents

Every language with a server of note runs its own checker in it rather than a second one written for the editor: Go's `gopls` runs `go/types`, Rust's `rust-analyzer` its own front end, which the compiler is slowly converging on, clangd the compiler's front end itself. Where a server's checker differs from the compiler's, the editor shows errors the build does not, or misses ones it does, and developers learn to distrust it; the servers that kept one checker kept that trust. `gopls` began, as this one does, by checking a whole package on every change and caching nothing below the package; it grew an incremental cache once packages and workspaces grew large enough for a whole check to be noticed. The protocol is one for every editor, VS Code's, Neovim's, Helix's, Emacs's and Zed's among them, so one server serves them all.

## 1. The process and the protocol

`snellols` reads messages from standard input and writes them to standard output, each a header, `Content-Length: N`, a blank line, and N bytes of JSON; a `Content-Type` header, which the protocol allows, is read and ignored. Logs go to standard error, which editors show in their output panes, never to standard output, which is the protocol's.

Three packages under `lsp/` hold it, each with its tests, as `compiler/` holds the compiler: `lsp/jsonrpc` the framing and the messages, a request with an id, a notification without, a response with a result or an error; `lsp/protocol` the protocol's types, only the ones the server uses, named as the specification names them; and `lsp/server` the server itself. `cmd/snellols` is the command: it runs the server over its standard input and output and exits with the code the protocol asks for, 0 after a `shutdown` and an `exit`, 1 after an `exit` alone or a closed input.

A request is answered once, in the order its work finishes, which for this server is the order it came in. A request the server does not know is answered with the protocol's `MethodNotFound`; one before `initialize` with `ServerNotInitialized`; a notification the server does not know, and every `$/` notification, `$/cancelRequest` included, is passed over, as the protocol allows, since nothing here runs long enough to be worth cancelling.

## 2. What the server says it does

`initialize` is answered with the capabilities of what is built and no more, so that an editor never asks for what the server cannot give:

- `textDocumentSync`: open and close, incremental changes, and saves without their text;
- `positionEncoding`: UTF-8 when the editor offers it, UTF-16 otherwise (section 5);
- `serverInfo`: `snellols` and the compiler's version.

The diagnostics are pushed, `textDocument/publishDiagnostics`, which every editor takes; the pulled form of protocol 3.17 asks the editor to poll, and adds nothing a push does not give a server whose checks are this quick.

## 3. Documents

The server keeps the text of every document the editor has open, by its URI, with the version the editor gave it. `didOpen` stores it; `didChange` applies each change in turn, a range replaced by its text or, with no range, the whole text replaced; `didSave` changes nothing, since the editor's text is already the file's; `didClose` forgets the document, and the next check reads the file from disk. A change is applied to the bytes the server holds, its range converted from the editor's positions to byte offsets in the text as it stands before that change, as the protocol orders the changes of one notification.

A package is checked from these buffers where a file of it is open and from the disk elsewhere: the loader reads every source through a function the server gives it, `importer.Options.Read`, `Load.Read` and `Importer.Read`, which answers from the open documents first. Each reads through `os.ReadFile` when no function is given, as every command of the compiler does. The read reaches the packages the open file's package imports too, so a library package edited in one window and imported in another is checked as it stands in the editor. A file that exists only in the editor, created and not yet saved, joins its package when it is saved, since the package's files are listed from the directory: a save is checked as a change is, which also picks up a file of an imported package saved from another editor.

The module's requirements are resolved from the module cache and the lock file alone, `Module.ResolveCached` through `importer.Options.Cached`, never as a build resolves them: a build fetches a module the cache lacks, fetches again a copy whose files changed, hashes every module, writes `snello.lock`, and removes it when nothing is required by version. A check as the developer types does none of it: it would reach the network, and write the program's files, on every keystroke. A required module the cache does not hold is an error naming `snello fetch`, which puts it there; a cached copy is held to the lock file's record, and its files are not hashed, which the build does.

## 4. Checking

A document's package is its directory's, checked as `snello check DIR` checks it, through the one function both use, `importer.CheckDir`: as a program when its package is `main` or the directory is a module's root, as a library under the module's rules otherwise. The package's test files are then checked as `snello test` loads them: the test runner's plan, `testrun.PlanDirFrom`, says whether they are a main package's, the package's own or an external test package's, `NAME_test`; a main package is checked with its test files, and a library by the import path its tests are reached at, `importer.CheckTests`, so that every error keeps the test file and the position it names. The runner `snello test` generates is not checked: an error there would name a file the developer never sees.

A directory in which more than one file declares `main` holds programs of one file each, as `examples/tree/` holds two, each built by `snello build FILE`; the server checks each such file alone, as `snello check FILE` does, where `snello check DIR` would report the second `main` as a redeclaration. The rule follows how the programs are built, and no file of a package of several files declares `main` but one.

Every package with an open document is checked again after every change, whole, from its files to its imports, the standard library's included: about 2 ms a package for the packages under `examples/`, tests included, on an eight-core arm64 Mac (section 8), so a keystroke's check is done long before the next. Changes that arrive while a check runs are applied together, and every package with an open document is checked once after them, so a burst of typing costs one check at its end, never one per key behind the keys. The server reads messages on one goroutine and does everything else on another, which owns the documents and the results, so that nothing it keeps is shared between them.

## 5. Positions

The compiler's position is a line and a column in bytes, both from 1. The protocol's is a line from 0 and a character in the units the editor and the server agreed: UTF-16 code units by default, the units of JavaScript's strings, or UTF-8 bytes, or code points, when the editor offers them in `general.positionEncodings` (protocol 3.17). The server takes UTF-8 when offered, since it is the compiler's own unit and needs no conversion, and UTF-16 otherwise, which every editor understands. A column in UTF-16 is counted over the line's text up to the byte: a character of the Basic Multilingual Plane is one unit, one beyond it, an emoji, two. A tab is one unit, as the protocol counts it.

## 6. Diagnostics

Every error the parser or the checker reports becomes a diagnostic of severity Error, with source `snello` and the compiler's message, on the file the error names. Its range starts at the error's position, its byte offset converted to the protocol's line and character, and covers the word there, an identifier, a keyword or a number, or the one character there when it starts none, since an editor underlines a range; at a line's end or the text's end it is a point, which an editor marks at the end of the line. The parser stops a check: a file that does not parse reports its syntax errors alone, and the checker's appear once it parses, as `snello check` reports them.

An error without a position, a module file that does not parse or a package of no files, is reported on the first line of every open document of the package, so that it is seen where the developer is; one of the tests' alone, on the open test files. A package that imports one that does not compile reports it as the checker does, on the import, with the imported package's errors in its message; the imported package's own file shows its errors once it is open, since its own check then names them. The loader keeps such a package's errors whole behind the message, so that a check of the package itself finds each one's file and position. A file's diagnostics are the union of what every check reports on it. A check that reports nothing on a file it reported on before publishes an empty list for it, which clears the editor's marks; closing a document publishes its file's diagnostics again from the remaining checks, and nothing for a file no check names.

## 7. What is not incremental

Nothing below the package is kept between checks: every check parses and checks every file of the package and of every package it imports. It is the simplest form that is right, and fast enough for the packages Snello has. What would change for a workspace large enough to notice: a cache of the checked packages by their import path and the hash of their sources, the standard library's first since it never changes under the editor; a package checked again only when one of its files, or of the packages it imports, changed; and, last, a parse of the edited file alone, reusing its siblings' syntax. Each waits for a measurement that asks for it.

## 8. Cost and memory

The server holds the text of the open documents and, for each, the diagnostics of the last check; a check's packages are dropped when it ends. A keystroke costs the check of the packages with open documents. `BenchmarkCheck` measures it over every package under `examples/`, each with its tests, open at once: 74 ms and 483,000 allocations for the 35 packages on an eight-core arm64 Mac, 2.1 ms and about 13,800 allocations a package. The server is a Go program like the compiler, run beside the editor; nothing of it reaches a Snello program, and the language's rules of ownership and memory are untouched: the server reports them, as the compiler does.

## 9. How it is tested

`go test ./lsp/...`. The framing is tested on a message split across reads, two messages in one read, a header with a content type, and a header that is not the protocol's. The positions are tested on ASCII, a two-byte character, a four-byte one, a tab and a line past the end, in both encodings. The documents are tested on a change at the start, the middle and the end of a line, across lines, after a non-ASCII character, and a whole replacement. The server is tested over a pipe in a scripted session: it is initialized, opens a file of a module, has it edited into a syntax error, a type error and back, and reads the diagnostics each time; an error in another file of the package is reported on that file; an unsaved buffer is checked as it stands; a file not open is read from disk; and every package under `examples/`, its tests included, opens with no diagnostic, the two programs of `examples/tree/` each checked alone, while the pending `cli-grep` shows the errors `snello check` prints, message for message. The loader's read function, `importer.CheckDir` and `importer.CheckTests` are tested in the importer's package, and the plan read through a function in the test runner's.

## 10. What is not here

Hover and definition (#114), symbols, references and rename (#115), completion (#116) and formatting (#118) are later tasks, each a capability the server will then announce. The editor extension, which starts `snellols` and gives the editor Snello's grammar, is #117. The pulled diagnostics of protocol 3.17, files changed on disk outside the editor, `workspace/didChangeWatchedFiles`, and a file that exists only in the editor joining its package before it is saved are not here either. Three kinds of file are never checked: one named for another platform, `poll_windows.snello` on macOS, and one whose name begins with a dot or an underscore, which no package of this machine holds; and the module file as the editor holds it, since the module is read from the disk.

## Decisions

- **One checker.** The server checks through the compiler's own loader and checker, so the editor and `snello check` agree on every program, message for message.
- **A whole package on every change**, every package with an open document, changes that arrive during a check applied together; nothing is cached below the package until a measurement asks for it.
- **The editor's buffers through the loader's read function**, for the open file's package and every package it imports; the disk elsewhere.
- **The module from the cache alone**: nothing fetched, hashed or written as the developer types; a module the cache lacks names `snello fetch`.
- **UTF-8 positions when the editor offers them**, UTF-16 otherwise.
- **Pushed diagnostics**, on the file each error names, the union of every check's, cleared when a check no longer reports them.
- **Test files checked as `snello test` loads them**, by the test runner's plan and the importer, never through the generated runner.
- **A directory of several files that declare `main` is programs of one file each**, checked alone, as they are built.
