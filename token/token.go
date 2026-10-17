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

import "strconv"

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
	GO
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
	GO:       "go",
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

// Pos is a position in a source file. Line and Column are 1-based;
// Offset is the 0-based byte offset.
type Pos struct {
	Filename string
	Offset   int
	Line     int
	Column   int
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

// IsValid reports whether the position carries a line number.
func (p Pos) IsValid() bool { return p.Line > 0 }

func (p Pos) String() string {
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
