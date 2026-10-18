// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package token defines the lexical tokens of Teak.
//
// The set is Go's, minus what Teak leaves out (interface, goto,
// fallthrough), plus the handful Teak adds: the keywords trait, impl,
// enum, match, ref, mut, Self and super, and the operators ?, ??, => and
// ..=.
package token

import (
	"sort"
	"strconv"
)

// Kind is the kind of a lexical token.
type Kind int

// The list of tokens.
const (
	ILLEGAL Kind = iota
	EOF
	COMMENT

	literalBeg
	IDENT  // main
	INT    // 12345
	FLOAT  // 123.45
	CHAR   // 'a'
	STRING // "abc"
	literalEnd

	operatorBeg
	ADD // +
	SUB // -
	MUL // * (also the owned pointer type: *T)
	QUO // /
	REM // %

	AND     // & (also heap allocation: &expr)
	OR      // |
	XOR     // ^
	SHL     // <<
	SHR     // >>
	AND_NOT // &^

	ADD_ASSIGN // +=
	SUB_ASSIGN // -=
	MUL_ASSIGN // *=
	QUO_ASSIGN // /=
	REM_ASSIGN // %=

	AND_ASSIGN     // &=
	OR_ASSIGN      // |=
	XOR_ASSIGN     // ^=
	SHL_ASSIGN     // <<=
	SHR_ASSIGN     // >>=
	AND_NOT_ASSIGN // &^=

	LAND  // &&
	LOR   // ||
	ARROW // <-
	INC   // ++
	DEC   // --

	EQL    // ==
	LSS    // < (also opens a type parameter list: List<T>)
	GTR    // > (also closes one)
	ASSIGN // =
	NOT    // !

	NEQ      // !=
	LEQ      // <=
	GEQ      // >=
	DEFINE   // :=
	ELLIPSIS // ...

	LPAREN // (
	LBRACK // [
	LBRACE // {
	COMMA  // ,
	PERIOD // .

	RPAREN    // )
	RBRACK    // ]
	RBRACE    // }
	SEMICOLON // ;
	COLON     // :

	// Teak additions.
	QUESTION // ? (the option type T? and the propagating postfix x?)
	COALESCE // ?? (unwrap or else)
	FATARROW // => (match arm)
	RANGEINC // ..= (inclusive range pattern)
	operatorEnd

	keywordBeg
	BREAK
	CASE
	CHAN
	CONST
	CONTINUE
	DEFAULT
	DEFER
	ELSE
	FOR
	FUNC
	SPAWN
	IF
	IMPORT
	MAP
	PACKAGE
	RANGE
	RETURN
	SELECT
	STRUCT
	SWITCH
	TYPE
	VAR

	// Teak additions.
	TRAIT
	IMPL
	ENUM
	MATCH
	REF
	MUT
	SELF  // Self, the implementing type inside a trait
	SUPER // super, the next definition in the impl order
	keywordEnd

	// NumKinds is one more than the largest Kind, for tables indexed by
	// Kind.
	NumKinds = int(keywordEnd) + 1
)

// Operator precedences, as in Go, with one addition: `??` binds loosest
// of all binary operators, so `a ?? b` takes the whole expression on its
// right. Unary operators bind tighter than any binary operator.
const (
	LowestPrec  = 0
	UnaryPrec   = 7
	HighestPrec = 8
)

var tokens = [...]string{
	ILLEGAL: "ILLEGAL",
	EOF:     "EOF",
	COMMENT: "COMMENT",

	IDENT:  "IDENT",
	INT:    "INT",
	FLOAT:  "FLOAT",
	CHAR:   "CHAR",
	STRING: "STRING",

	ADD: "+",
	SUB: "-",
	MUL: "*",
	QUO: "/",
	REM: "%",

	AND:     "&",
	OR:      "|",
	XOR:     "^",
	SHL:     "<<",
	SHR:     ">>",
	AND_NOT: "&^",

	ADD_ASSIGN: "+=",
	SUB_ASSIGN: "-=",
	MUL_ASSIGN: "*=",
	QUO_ASSIGN: "/=",
	REM_ASSIGN: "%=",

	AND_ASSIGN:     "&=",
	OR_ASSIGN:      "|=",
	XOR_ASSIGN:     "^=",
	SHL_ASSIGN:     "<<=",
	SHR_ASSIGN:     ">>=",
	AND_NOT_ASSIGN: "&^=",

	LAND:  "&&",
	LOR:   "||",
	ARROW: "<-",
	INC:   "++",
	DEC:   "--",

	EQL:    "==",
	LSS:    "<",
	GTR:    ">",
	ASSIGN: "=",
	NOT:    "!",

	NEQ:      "!=",
	LEQ:      "<=",
	GEQ:      ">=",
	DEFINE:   ":=",
	ELLIPSIS: "...",

	LPAREN: "(",
	LBRACK: "[",
	LBRACE: "{",
	COMMA:  ",",
	PERIOD: ".",

	RPAREN:    ")",
	RBRACK:    "]",
	RBRACE:    "}",
	SEMICOLON: ";",
	COLON:     ":",

	QUESTION: "?",
	COALESCE: "??",
	FATARROW: "=>",
	RANGEINC: "..=",

	BREAK:    "break",
	CASE:     "case",
	CHAN:     "chan",
	CONST:    "const",
	CONTINUE: "continue",
	DEFAULT:  "default",
	DEFER:    "defer",
	ELSE:     "else",
	FOR:      "for",
	FUNC:     "func",
	SPAWN:    "spawn",
	IF:       "if",
	IMPORT:   "import",
	MAP:      "map",
	PACKAGE:  "package",
	RANGE:    "range",
	RETURN:   "return",
	SELECT:   "select",
	STRUCT:   "struct",
	SWITCH:   "switch",
	TYPE:     "type",
	VAR:      "var",

	TRAIT: "trait",
	IMPL:  "impl",
	ENUM:  "enum",
	MATCH: "match",
	REF:   "ref",
	MUT:   "mut",
	SELF:  "Self",
	SUPER: "super",
}

var keywords map[string]Kind

// Pos is a compact position: a byte offset into the file being
// compiled, plus one so that the zero value means "no position". Nodes
// store a Pos, not a line and column, so that a node is eight bytes
// smaller and carries no string; File.Position resolves the line and
// column when an error is printed. This mirrors go/token.
type Pos int

// NoPos is the zero Pos, meaning no position.
const NoPos Pos = 0

// A Position is a resolved position, for printing.
type Position struct {
	Filename string
	Offset   int // 0-based byte offset
	Line     int // 1-based
	Column   int // 1-based, in bytes
}

// A File records the line starts of one source file so that a Pos can
// be resolved to a line and column. The scanner fills it in as it goes.
type File struct {
	name  string
	lines []int // byte offset of the start of each line; lines[0] is 0
}

// String returns the string corresponding to the token kind: the
// operator or keyword text, or the kind name for literals and specials.
func (k Kind) String() string {
	if 0 <= k && k < Kind(len(tokens)) && tokens[k] != "" {
		return tokens[k]
	}

	return "token(" + strconv.Itoa(int(k)) + ")"
}

// Lookup maps an identifier to its keyword kind, or IDENT.
func Lookup(ident string) Kind {
	if k, ok := keywords[ident]; ok {
		return k
	}

	return IDENT
}

// IsLiteral reports whether the kind is an identifier or a basic literal.
func (k Kind) IsLiteral() bool { return literalBeg < k && k < literalEnd }

// IsOperator reports whether the kind is an operator or delimiter.
func (k Kind) IsOperator() bool { return operatorBeg < k && k < operatorEnd }

// IsKeyword reports whether the kind is a keyword.
func (k Kind) IsKeyword() bool { return keywordBeg < k && k < keywordEnd }

// Precedence returns the binary operator precedence of k, or LowestPrec
// if k is not a binary operator.
func (k Kind) Precedence() int {
	switch k {
	case COALESCE:
		return 1
	case LOR:
		return 2
	case LAND:
		return 3
	case EQL, NEQ, LSS, LEQ, GTR, GEQ:
		return 4
	case ADD, SUB, OR, XOR:
		return 5
	case MUL, QUO, REM, SHL, SHR, AND, AND_NOT:
		return 6
	}

	return LowestPrec
}

// IsValid reports whether p is a real position.
func (p Pos) IsValid() bool { return p != NoPos }

// NewFile returns a File for a source of the given size. The line table
// is sized for the typical line length up front so it rarely grows.
func NewFile(name string, size int) *File {
	lines := make([]int, 1, size/32+1)
	return &File{name: name, lines: lines}
}

// Name returns the file name.
func (f *File) Name() string { return f.name }

// Pos returns the Pos for a byte offset in the file.
func (f *File) Pos(offset int) Pos { return Pos(offset + 1) }

// Offset returns the byte offset of a Pos.
func (f *File) Offset(p Pos) int { return int(p) - 1 }

// AddLine records that a new line starts at the given byte offset. The
// scanner calls it once per newline, in order.
func (f *File) AddLine(offset int) {
	if offset > f.lines[len(f.lines)-1] {
		f.lines = append(f.lines, offset)
	}
}

// Position resolves p to a line and column by binary search over the
// recorded line starts.
func (f *File) Position(p Pos) Position {
	if !p.IsValid() {
		return Position{Filename: f.name}
	}

	offset := f.Offset(p)
	line := sort.Search(len(f.lines), func(i int) bool { return f.lines[i] > offset })
	return Position{
		Filename: f.name,
		Offset:   offset,
		Line:     line,
		Column:   offset - f.lines[line-1] + 1,
	}
}

// IsValid reports whether the position carries a line number.
func (p Position) IsValid() bool { return p.Line > 0 }

func (p Position) String() string {
	s := p.Filename
	if p.IsValid() {
		if s != "" {
			s += ":"
		}

		s += strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Column)
	}

	if s == "" {
		s = "-"
	}

	return s
}

func init() {
	keywords = make(map[string]Kind, keywordEnd-keywordBeg)
	for k := keywordBeg + 1; k < keywordEnd; k++ {
		keywords[tokens[k]] = k
	}
}
