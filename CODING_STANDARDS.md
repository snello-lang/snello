# Coding standards

These apply to the Go code of the compiler and to the Teak code under `examples/`. They exist so that every file reads the same way, whoever or whatever wrote it.

## Principles

- **No dependencies.** The compiler imports only the Go standard library. `go.mod` has no `require` line. Development tools (linters, formatters) may be used but are never imported.
- **Model on the Go toolchain.** Each package mirrors its counterpart in Go's own source: `token` on `go/token`, `scanner` on `go/scanner`, `ast` on `go/ast`, `parser` on `go/parser`, the checker on `go/types`. When unsure how to structure something, read how Go did it.
- **Simple over clever.** A Go developer should read every file on the first pass. If a construct needs a comment to explain what it is doing, rewrite the construct before writing the comment.
- **The examples are the acceptance suite.** Every stage must process all twenty programs under `examples/`. A change that breaks one is wrong until either the code or the design document is updated.

## Files

- Every `.go` and `.teak` file starts with the license header:

  ```go
  // Copyright 2026 The Teak Programming Language. All rights reserved.
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
- A comment that explains a subtle choice points at the precedent: "mirrors go/scanner", "see design/04-borrows-ref-and-mut.md".
- No banner or separator comments (`// ---- Types ----`, `// =====`). Sections of a file are separated by the order of declarations and by blank lines, not by decorated comments.

### Errors

- The compiler reports errors with a position and a message that says what was found and what was expected, in the user's terms: `"hexadecimal literal has no digits"`, never `"bad token"`.
- One problem, one error. Do not report the same fault twice from two checks.
- Error handlers are callbacks, as in `go/scanner`; packages do not print or exit.

## Tests

- Standard `testing` package only. No assertion libraries, no mocks.
- Table-driven: a slice of input and expected output, one loop. Each package has three kinds of test and rarely needs more:
  1. exact output for representative inputs,
  2. the error message for each kind of bad input,
  3. a sweep over every program in `examples/`.
- A test failure message shows the input, what was produced, and what was expected.
- Helpers used only by tests live at the bottom of the test file.

## Teak code in `examples/`

- Each file begins with a comment stating what the program tests.
- Syntax follows `design/00-syntax-conventions.md`. When an example needs something the conventions do not cover, the conventions change first, then the example.
- Lines the compiler must reject are shown as comments with the reason, so they can become negative tests later.

## Commits

- One concern per commit.
- Message: a type prefix, then a short imperative summary. Types: `docs`, `feat`, `fix`, `test`, `refactor`, `chore`. Example: `feat: scanner with Go-style semicolon insertion`.
- Design documents are updated in the same commit as the code that changes what they describe.
