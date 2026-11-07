# Stage 7: the language server

Goal: Snello in an editor the way Go is in one: errors as you type, hover, go to definition, references, symbols, completion, rename and formatting, served by one command, `snellols`, that speaks the Language Server Protocol over standard input and output, as `gopls` does. The server is a thin layer over the compiler's own packages, which already hold what it needs: the scanner and parser with error recovery, the checker filling `types.Info` with every definition and use, positions through `token.FileSet`, and `importer.LoadProgram`, which loads a file or a directory with its module. Nothing is added to the language for it, and the zero-dependency rule holds: the protocol is JSON-RPC 2.0 over `encoding/json`, written in the repository as Go's `jsonrpc2` is written in `x/tools`. The VS Code extension in `../../../vscode-extension/`, which today carries the grammar alone, gains the client that launches the server; that side takes the `vscode-languageclient` package, the one place a dependency is normal. Planned 2026-11-06, as a stage of its own; the number is an index, and the stage may be taken before stage 6.

Every task starts with its design in `../design/09-language-server.md`, one note for the stage, grown a section per task, reviewed before the code, and every task ends with a Go test that drives the server over a pipe with a scripted session, so the suite needs no editor. The twenty examples and the programs under `examples/run/` are the acceptance suite here as everywhere: the server must report no error on a program the compiler accepts, and the same errors the compiler reports on one it refuses.

## Task 7.1: The server and its diagnostics

**Description:** The design note, the command and the protocol, with the one feature that gives most of the daily value: the errors of the open file's package, reported as the program is edited. `cmd/snellols` reads JSON-RPC 2.0 messages framed by `Content-Length` from standard input and writes its answers to standard output, under a package `lsp/` of the repository's own: the framing and the message types in `lsp/jsonrpc`, the server in `lsp/server`, and the protocol's types, only the ones the stage uses, in `lsp/protocol`, with the Go counterpart named on each. It answers `initialize` with its capabilities, `shutdown` and `exit`, and the document notifications, `didOpen`, `didChange`, `didSave` and `didClose`, keeping the text of every open document, so that a package is checked from the editor's buffers where they are open and from disk elsewhere. On every change the server loads the document's package through the importer, every `.snello` file of the directory with the module above it, parses and checks it, and publishes the errors of every file of the package as diagnostics, a parse error and a check error alike, with the message the compiler prints. A position maps from the compiler's line and byte column to the protocol's line and UTF-16 code unit, so that a column after a non-ASCII character lands where the editor puts the cursor. The loader gains what this needs: `importer.LoadProgram` takes the sources of its files from a function the caller gives, the buffers or the disk, in place of reading them itself, which is a small change since parsing takes sources as bytes already. A whole package is checked on every edit, since the checker runs in milliseconds on packages of this size; incremental checking waits for a package large enough to notice, and the note says what would change.

**Acceptance criteria:**
- [ ] `snellols` completes the `initialize` handshake with VS Code and reports the capabilities of this task alone
- [ ] A syntax error and a type error typed into an open file appear as diagnostics within a keystroke, with the compiler's message and the right line and character, and disappear when fixed; an error in another file of the same package is reported on that file
- [ ] An unsaved buffer is checked as the editor holds it, and a file of the package not open in the editor is read from disk
- [ ] A position after a non-ASCII character in a string or a comment is reported at the editor's character, not the byte
- [ ] A program under `examples/` or `examples/run/` that the compiler accepts shows no diagnostic, and the three pending examples show the errors the compiler reports
- [ ] The protocol and the server are in `../design/09-language-server.md`, with how a request is dispatched, how documents are kept, and what is deliberately not incremental

**Verification:**
- [ ] `go test ./lsp/...`: a scripted session over a pipe opens a file, edits it into an error and back, and reads the diagnostics each time; the framing is tested on a message split across reads and on two messages in one read
- [ ] Opened by hand in VS Code on `examples/run/packaged/` with the extension of task 7.5, or its development build, and the three pending examples

**Dependencies:** Stage 5, task 5.3
**Files:** `cmd/snellols/`, `lsp/jsonrpc/`, `lsp/protocol/`, `lsp/server/`, `compiler/importer/program.go`, `../design/09-language-server.md`
**Scope:** M

## Task 7.2: Hover and definition

**Description:** The two requests that make a program readable in the editor: `textDocument/hover` and `textDocument/definition`. Both start from the identifier under the cursor, which needs a walk over the syntax tree by position that the `ast` package does not have; it gains one, `ast.Inspect` after Go's, which the server uses to find the innermost node at an offset and which the compiler's own tools may use after it. From the identifier, `types.Info` gives the object through `Defs` and `Uses`; the definition's position is the object's, in this package or in a package it imports, standard or the program's own, and the hover shows the object's declaration as the checker types it, `func Largest<A: Shape, B: Shape>(a ref A, b ref B) f64`, `struct Rect impl Shape`, `var count int`, with the type of an expression where the cursor is on no identifier. The doc comment belongs in the hover, as `gopls` shows it, and the syntax tree keeps no comments today: the parser gains the doc comment of a declaration, the comment group ending on the line above it, kept on the declaration as `go/ast` keeps `Doc`, and nothing else of comments yet; the formatter of task 7.6 is what needs the rest. The type parameters of a generic declaration, the receiver and the traits a type implements print as the declaration wrote them, through the checker's `String` methods, which the note checks are fit to be read in a tooltip.

**Acceptance criteria:**
- [ ] Go to definition on a name declared in the file, in another file of the package, in a package of the program's own and in a standard package lands on the declaration; on a method through a trait bound it lands on the trait's method
- [ ] Hover on a function, a method, a type, a trait, a variable, a constant, a field and an enum variant shows its declaration as typed, and its doc comment below it; hover on an expression shows its type
- [ ] A declaration's doc comment is kept by the parser and printed by `snello parse`, with the comment groups of the examples as the test
- [ ] `ast.Inspect` is in the `ast` package with its test, and the server's position lookup uses it

**Verification:**
- [ ] `go test ./lsp/... ./compiler/ast ./compiler/parser`: a scripted session asks for definitions and hovers at positions in `examples/run/packaged/` and checks the answers against the known declarations
- [ ] By hand in VS Code on `examples/run/packaged/` and `examples/07-traits.snello`

**Dependencies:** 7.1
**Files:** `lsp/server/`, `compiler/ast/`, `compiler/parser/`, `compiler/types/`, `../design/09-language-server.md`
**Scope:** M

## Task 7.3: Symbols, references and rename

**Description:** `textDocument/documentSymbol`, the outline of a file, from its top-level declarations with their kinds, methods under their types and variants under their enums; `workspace/symbol`, the same over every file of the module, found by `snello.mod` as the importer finds it; `textDocument/references`, every use of the object under the cursor through `Uses`, across the files of the package and the packages of the module that import it, which the server checks on request; `textDocument/rename`, built on references, with `prepareRename` refusing a name of a standard package, since the library is the compiler's, and refusing a rename to a keyword or to a name declared in the same scope, with the checker's own message for the clash. A rename of an exported name changes its uses in the module's other packages, which is what the module gives the server over a file alone. Positions in a rename's edits are the protocol's, as in task 7.1, and every edit of a file is sent as one `TextEdit` list in one `WorkspaceEdit`, so the editor applies them at once.

**Acceptance criteria:**
- [ ] The outline of `examples/07-traits.snello` lists its trait, types, methods under their types, enum variants under their enum, functions and globals, with the kinds the protocol has
- [ ] Workspace symbols over `examples/run/packaged/` find `Largest` and `Kind` from the package directory
- [ ] References to `Shape` from `examples/run/packaged/main.snello` include its uses in `report.snello` and in `shapes/area.snello`
- [ ] Renaming `Largest` across `examples/run/packaged/` changes the declaration and both uses, and renaming to `Area` is refused with the checker's redeclaration message; renaming `fmt.Println` is refused as a standard name

**Verification:**
- [ ] `go test ./lsp/...`: scripted sessions for each request over `examples/run/packaged/` and `examples/07-traits.snello`, the rename checked by building the renamed copy and running it
- [ ] By hand in VS Code

**Dependencies:** 7.2
**Files:** `lsp/server/`, `../design/09-language-server.md`
**Scope:** M

## Task 7.4: Completion

**Description:** `textDocument/completion` in three places. After a dot: the fields and methods of the value's type, methods through its traits included, and for a package name the names it exports, each with its kind and its declaration as the detail; this comes from the checker's types and scopes and needs the server to check the file as it stands with the dot, which parses with an error the parser recovers from. On a bare identifier: the names in scope at the position, locals innermost first, then the package's declarations and the imports, then the predeclared names, through `types.Scope` and `LookupParent`, which needs the scope of a position, kept by the checker per block where today it keeps only type scopes per function; the note decides how. In an import path: the standard packages, from `importer.Paths`, and the packages of the module, from its directories. Keywords complete as themselves. No snippet expansion and no ranking beyond the order above, which is what the owner asked for in every feature of the stage: simple, predictable, and the same every time.

**Acceptance criteria:**
- [ ] After `r.` on a `shapes.Rect` the list has `W`, `H`, `Name` and `Area`; after `shapes.` it has the package's exported names and nothing unexported
- [ ] On a bare identifier inside a function the list has the locals in scope, the function's parameters, the package's declarations and the imported package names, and no local declared after the position or in another block
- [ ] Inside `import "` the list has every standard package path and the packages of the module
- [ ] A keyword completes, and the list is empty inside a string or a comment

**Verification:**
- [ ] `go test ./lsp/... ./compiler/types`: scripted sessions at positions in `examples/run/packaged/` and `examples/run/traits.snello`
- [ ] By hand in VS Code

**Dependencies:** 7.2
**Files:** `lsp/server/`, `compiler/types/`, `compiler/importer/`, `../design/09-language-server.md`
**Scope:** L

## Task 7.5: The editor extension

**Description:** The VS Code extension in `../../../vscode-extension/` becomes a client of the server: it starts `snellols` from the setting `snello.serverPath`, or from the `PATH`, for every `.snello` document, shows the server's diagnostics, and forwards the requests of tasks 7.1 to 7.4 as they land, through `vscode-languageclient`, the one dependency of the extension. The extension's README says how to build the server, `go build -o snellols ./cmd/snellols`, and where to point the setting; the repository's README names the extension and the server beside `snello build`. A packaged `.vsix` is built from the extension's directory as today. The server's own tests stay in Go and need no editor; the extension is tested by hand, since its logic is the client's, and the stage's checkpoint is the review of that.

**Acceptance criteria:**
- [ ] Opening a `.snello` file starts the server once per window, and closing the window stops it; a server that is not found reports the setting to set, once
- [ ] Diagnostics, hover, definition, references, symbols, rename and completion work in VS Code on `examples/run/packaged/` as the tasks above specify
- [ ] The extension's README and the repository's README say how to install and point the client at the server

**Verification:**
- [ ] The `.vsix` installed from a clean build and the walk through `examples/run/packaged/` done by the owner
- [ ] `go test ./lsp/...` green

**Dependencies:** 7.1; 7.2 to 7.4 for their features
**Files:** `../../../vscode-extension/`, `README.md`
**Scope:** S

## Task 7.6: Formatting

**Description:** `textDocument/formatting` and `snello fmt`, one formatter behind both, as `gofmt` is behind `gopls`. The formatter is a printer over the syntax tree, which must keep every comment and every blank line between declarations and statements, since a formatter that loses a comment is not used twice; the parser therefore keeps all comments, as `go/parser` keeps them in a file's comment list with positions, and the printer places each by position as `go/printer` does. The style is the one the examples and the standard library are written in, which `00-syntax-conventions.md` records; what it does not record, the design note decides, with `gofmt` as the precedent where Snello's syntax is Go's: tabs, one statement per line, spaces around binary operators, the alignment of struct fields and of trailing comments, and `import ("fmt"; "net")` kept on one line as the examples write it. `snello fmt FILE|DIR` writes the files in place, `snello fmt -l` lists those that would change, and the examples and the standard library are the acceptance suite: formatting every file of the repository must change nothing, which a test checks, so that the formatter's style is the repository's style by construction.

**Acceptance criteria:**
- [ ] The parser keeps every comment with its position, and `snello parse` prints them
- [ ] `snello fmt -l` over `examples/`, `std/` and `examples/run/` lists no file, after the files are brought to the formatter's style once in a change of their own, reviewed by the owner
- [ ] A file with its indentation and spacing wrong is formatted to the style, with every comment kept where it was
- [ ] Format on save in VS Code formats through the server, and the result is the same as `snello fmt`'s

**Verification:**
- [ ] `go test ./compiler/format ./compiler/parser ./lsp/...`: the idempotence test over every `.snello` file of the repository, and the comment-keeping tests
- [ ] By hand in VS Code

**Dependencies:** 7.2 for the doc comments, 7.5 for the editor
**Files:** `compiler/format/`, `compiler/parser/`, `compiler/ast/`, `cmd/snello/`, `lsp/server/`, `../design/09-language-server.md`, `../design/00-syntax-conventions.md`
**Scope:** L

## Checkpoint

The owner opens `examples/run/packaged/` and the twenty examples in VS Code with the extension installed: no false diagnostic on a program the compiler accepts, the compiler's errors on the pending ones, hover and definition across the package boundary, references and a rename across the files of the module, completion after a dot and in an import path, and format on save changing nothing in a file already in the style. `go test ./...` is green on the three runners with the server's scripted sessions in it, the zero-dependency rule holds on the Go side, and the book has a chapter on the editor, which says what the server does and how to install it.
