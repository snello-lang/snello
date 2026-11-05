# Coding standards

These apply to the Go code of the compiler and to the Snello code under `examples/`. They exist so that every file reads the same way, whoever or whatever wrote it.

## Principles

- **No dependencies.** The compiler imports only the Go standard library. `go.mod` has no `require` line. Development tools (linters, formatters) may be used but are never imported.
- **Model on the Go toolchain.** Each package mirrors its counterpart in Go's own source: `token` on `go/token`, `scanner` on `go/scanner`, `ast` on `go/ast`, `parser` on `go/parser`, the checker on `go/types`. When unsure how to structure something, read how Go did it.
- **Simple over clever.** A Go developer should read every file on the first pass. If a construct needs a comment to explain what it is doing, rewrite the construct before writing the comment.
- **The examples are the acceptance suite.** Every stage must process all twenty programs under `examples/`. A change that breaks one is wrong until either the code or the design document is updated.

## Files

- Every `.go` and `.snello` file starts with the license header:

  ```go
  // Copyright 2026 The Snello Programming Language. All rights reserved.
  // Use of this source code is governed by a BSD-style
  // license that can be found in the LICENSE file.
  ```

- Every package has a package comment in exactly one file, the one named after the package, describing what the package is for and what it is modeled on.
- Go files under `examples/` carry `//go:build ignore` after the header; they are reference programs, not part of the build.
- `gofmt` and `go vet` are clean at every commit.
- Markdown is not hard-wrapped: one line per paragraph, so it fills the page width in any viewer. Tables and code blocks are the only exceptions.

## File layout

- Put every package-level declaration at the top of the file, after the imports: constants first, then variables, then types. Do not declare a `const`, `var` or `type` between functions or methods.
- The exception is a set of constants of a named type (an enum): put its `const` block directly after the type declaration.

  ```go
  // Kind is the kind of a lexical token.
  type Kind int

  const (
      ILLEGAL Kind = iota
      EOF
      // ...
  )
  ```

- Put exported functions and methods before unexported ones. Do not place an unexported helper between exported ones; put it in the file's unexported section, next to the unexported code it works with.
- One file per concern. When a file passes about five hundred lines, split it by what it does, as `go/parser` splits expressions from statements.

## Code style

### Blank lines around multi-line blocks

Put a blank line before and after any multi-line statement block (a `{ }`-delimited body spanning more than one line: `for`, `if`, `switch`, `select`, function literals, composite literals used as statements).

Exceptions:

- No blank line when the block is the first or last statement in its enclosing block. A blank line directly after an opening brace or directly before a closing brace is wrong.
- A simple statement immediately before a block may be grouped with it when it directly sets up the block's condition or subject.

Single-line statements forming a related unit may stay grouped without blank lines. The blank line is only required at the boundary between a multi-line block and surrounding code.

```go
offs := s.offset

for isLetter(s.ch) || isDigit(s.ch) {
    s.next()
}

lit := string(s.src[offs:s.offset])
tok := token.Lookup(lit)
if tok == token.IDENT {
    s.insertSemi = true
}

return tok, lit
```

### Naming

- Names say what a thing is, not where it came from. `nextOffset`, not `rdOffset`. If a name needs its comment to be understood, rename it.
- Unexported helpers are verbs: `scanNumber`, `skipWhitespace`.
- Single-letter receivers, consistent within a type: `s *Scanner`, `p *Parser`.
- Abbreviations only when universal in Go: `pos`, `lit`, `tok`, `src`, `err`.

### Comments

- A comment says why, or what a reader cannot see from the code. It never restates the code.
- Every exported identifier has a doc comment that starts with its name.
- A comment that explains a subtle choice points at the precedent: "mirrors go/scanner", "see _docs/design/04-borrows-ref-and-mut.md".
- No banner or separator comments (`// ---- Types ----`, `// =====`). Sections of a file are separated by the order of declarations and by blank lines, not by decorated comments.

### Errors

- The compiler reports errors with a position and a message that says what was found and what was expected, in the user's terms: `"hexadecimal literal has no digits"`, never `"bad token"`.
- One problem, one error. Do not report the same fault twice from two checks. The parser enforces a coarser version of this mechanically, as `go/parser` does: at most one error per line, since after the first the parser is guessing at what the line meant.
- Error handlers are callbacks, as in `go/scanner`; packages do not print or exit.

## Compiler performance

Compile time is a feature, and in Go the way to lose it is allocation: every object the compiler allocates is work for Go's collector while the user waits. The rules:

- **Measure, do not guess.** Every package that processes source has a benchmark over the programs in `examples/`, run with `-benchmem`. Allocations per operation is the number to watch; a change that raises it needs a reason in its commit message. Current baseline: the scanner performs about nine allocations per file, none per token; the parser performs about 17,200 allocations over the 56 example files parsed completely (the twenty programs, the eight core programs and the 27 negative programs), about 61 per declaration, every one an AST node or a node-owned slice; the checker performs about 1,960 allocations over the eight core programs, mostly scopes and their maps, operand slices, the signatures the predeclared methods build on demand, the lists of moved places the move pass forks at branches, the borrows each function's variables hold, and the captures of each closure; the C emitter performs about 61,000 allocations over the twenty-six programs under `examples/run/`, the library packages a program imports compiled with it, almost all of them the strings of C text it joins; the LLVM emitter performs about 133,000 over the same programs, the strings of IR text, more of them since the IR names every intermediate value, which is the cost of writing text and acceptable while the text is what clang reads.
- **Positions are integers.** `token.Pos` is a byte offset; line and column are resolved only when an error is printed. No node carries a string for its position.
- **Substrings, not copies.** The scanner holds the source as a string and returns identifiers and literals as substrings of it. Never `string(bytes)` per token.
- **Tables, not maps, for small fixed sets.** A set of token kinds is an array indexed by kind, one load per test. Maps are for data whose keys are not known at compile time.
- **Nothing per token or per node that is not the token or the node.** No closures created inside the scan or parse loop, no `fmt` on the hot path, no interface conversions of small values in loops, no slices appended one element at a time when the length is known.
- **Error paths may allocate freely.** `fmt.Sprintf` in an error message is fine; the program being compiled is broken and the user is reading, not waiting.
- **Reflection is for debugging output only.** The AST printer uses it; nothing on the compile path does.

## Tests

- Standard `testing` package only. No assertion libraries, no mocks.
- Table-driven: a slice of input and expected output, one loop. Each package has three kinds of test and rarely needs more:
  1. exact output for representative inputs,
  2. the error message for each kind of bad input,
  3. a sweep over every program in `examples/`.
- A test failure message shows the input, what was produced, and what was expected.
- Helpers used only by tests live at the bottom of the test file.

## Snello code in `examples/` and `std/`

- Each file under `examples/` begins with a comment stating what the program tests; each package under `std/` begins with a package comment, as a Go package does, and every exported name has a doc comment that starts with the name.
- A library function the runtime provides is declared without a body, and its doc comment says so.
- Syntax follows `_docs/design/00-syntax-conventions.md`. When an example needs something the conventions do not cover, the conventions change first, then the example.
- Lines the compiler must reject are shown as comments with the reason, so they can become negative tests later.

## Commits

- One concern per commit.
- Message: a type prefix, then a short imperative summary. Types: `docs`, `feat`, `fix`, `test`, `refactor`, `chore`. Example: `feat: scanner with Go-style semicolon insertion`.
- Design documents are updated in the same commit as the code that changes what they describe.
