// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package scanner implements the lexer for Teak source text.
//
// It follows the structure of Go's go/scanner: a Scanner is initialized
// with a source buffer and returns one token per call to Scan, inserting
// semicolons at line ends by Go's rule, extended to lines ending in a
// postfix ? or in the > that closes a type argument list. Comments are
// skipped.
package scanner

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/teak-lang/teak/token"
)

const (
	eof = -1     // the character past the end of the source
	bom = 0xFEFF // byte order mark, permitted once at the start of a file
)

// An ErrorHandler is called for each lexical error, with its position
// already resolved to a line and column.
type ErrorHandler func(pos token.Position, msg string)

// A Scanner holds the state of one lexing pass over a file.
//
// The source is held as a string so that every identifier and literal
// the scanner returns is a substring of it, sharing its memory: the one
// copy made in Init is the only allocation the scanner performs per
// file. (Converting a []byte slice to a string, as go/scanner does,
// allocates once per token.)
type Scanner struct {
	src  string
	file *token.File
	err  ErrorHandler

	// The scanner keeps one character of lookahead: ch is the character
	// being looked at, offset is where it starts in src, and nextOffset
	// is where the character after it starts (one byte later for ASCII,
	// up to four for other UTF-8 characters). next() moves both forward.
	ch         rune // the current character; eof (-1) past the end of src
	offset     int  // byte offset in src where ch starts
	nextOffset int  // byte offset in src where the character after ch starts
	insertSemi bool // set when a newline here should produce a semicolon

	// ErrorCount is the number of errors reported so far.
	ErrorCount int
}

// Init prepares the scanner to lex src. The scanner records each line
// start in file as it goes, so positions can be resolved later. err may
// be nil.
func (s *Scanner) Init(file *token.File, src []byte, err ErrorHandler) {
	s.src = string(src)
	s.file = file
	s.err = err
	s.ch = ' '
	s.offset = 0
	s.nextOffset = 0
	s.insertSemi = false
	s.ErrorCount = 0
	s.next()

	if s.ch == bom {
		s.next()
	}
}

// Scan returns the next token. For identifiers, keywords and literals,
// lit is the source text; for an inserted semicolon it is "\n"; for
// operators it is empty. At end of input it returns EOF repeatedly.
func (s *Scanner) Scan() (pos token.Pos, tok token.Kind, lit string) {
scanAgain:
	s.skipWhitespace()
	pos = s.pos()
	insertSemi := false

	switch ch := s.ch; {
	case isLetter(ch):
		lit = s.scanIdentifier()
		if tok = token.Lookup(lit); tok != token.IDENT {
			lit = tok.String() // the table's string, so lit never pins src
		}

		switch tok {
		case token.IDENT, token.BREAK, token.CONTINUE, token.RETURN, token.SELF, token.SUPER:
			insertSemi = true
		}

	case isDecimal(ch) || ch == '.' && isDecimal(rune(s.peek())):
		insertSemi = true
		tok, lit = s.scanNumber()

	default:
		s.next()

		switch ch {
		case eof:
			if s.insertSemi {
				s.insertSemi = false
				return pos, token.SEMICOLON, "\n"
			}

			tok = token.EOF
		case '\n':
			// Only reached when insertSemi was set; skipWhitespace eats
			// every other newline.
			s.insertSemi = false
			return pos, token.SEMICOLON, "\n"
		case '"':
			insertSemi = true
			tok = token.STRING
			lit = s.scanString()
		case '\'':
			insertSemi = true
			tok = token.CHAR
			lit = s.scanRune()
		case '`':
			insertSemi = true
			tok = token.STRING
			lit = s.scanRawString()
		case ':':
			tok = s.switch2(token.COLON, token.DEFINE)
		case '.':
			tok = token.PERIOD
			if s.ch == '.' {
				tok, lit = s.scanDots(pos)
			}
		case ',':
			tok = token.COMMA
		case ';':
			tok = token.SEMICOLON
			lit = ";"
		case '(':
			tok = token.LPAREN
		case ')':
			insertSemi = true
			tok = token.RPAREN
		case '[':
			tok = token.LBRACK
		case ']':
			insertSemi = true
			tok = token.RBRACK
		case '{':
			tok = token.LBRACE
		case '}':
			insertSemi = true
			tok = token.RBRACE
		case '+':
			tok = s.switch3(token.ADD, token.ADD_ASSIGN, '+', token.INC)
			insertSemi = tok == token.INC
		case '-':
			tok = s.switch3(token.SUB, token.SUB_ASSIGN, '-', token.DEC)
			insertSemi = tok == token.DEC
		case '*':
			tok = s.switch2(token.MUL, token.MUL_ASSIGN)
		case '/':
			switch s.ch {
			case '/':
				s.skipLineComment()
				goto scanAgain
			case '*':
				if s.skipBlockComment() && s.insertSemi {
					s.insertSemi = false
					return pos, token.SEMICOLON, "\n"
				}

				goto scanAgain
			default:
				tok = s.switch2(token.QUO, token.QUO_ASSIGN)
			}
		case '%':
			tok = s.switch2(token.REM, token.REM_ASSIGN)
		case '^':
			tok = s.switch2(token.XOR, token.XOR_ASSIGN)
		case '<':
			if s.ch == '-' {
				s.next()
				tok = token.ARROW
			} else {
				tok = s.switch4(token.LSS, token.LEQ, '<', token.SHL, token.SHL_ASSIGN)
			}
		case '>':
			tok = s.switch4(token.GTR, token.GEQ, '>', token.SHR, token.SHR_ASSIGN)
			// A line ending in > or >> is one that closes a type
			// argument list: cache Arc<RwLock<Entry>>. A comparison or
			// shift keeps its right operand on the same line.
			insertSemi = tok == token.GTR || tok == token.SHR
		case '=':
			if s.ch == '>' {
				s.next()
				tok = token.FATARROW
			} else {
				tok = s.switch2(token.ASSIGN, token.EQL)
			}
		case '!':
			tok = s.switch2(token.NOT, token.NEQ)
		case '&':
			if s.ch == '^' {
				s.next()
				tok = s.switch2(token.AND_NOT, token.AND_NOT_ASSIGN)
			} else {
				tok = s.switch3(token.AND, token.AND_ASSIGN, '&', token.LAND)
			}
		case '|':
			tok = s.switch3(token.OR, token.OR_ASSIGN, '|', token.LOR)
		case '?':
			if s.ch == '?' {
				s.next()
				tok = token.COALESCE
			} else {
				// Postfix ? ends an expression, so a newline after it
				// ends the statement.
				tok = token.QUESTION
				insertSemi = true
			}
		default:
			s.errorf(pos, "illegal character %#U", ch)
			insertSemi = s.insertSemi // keep the pending state
			tok = token.ILLEGAL
			lit = string(ch)
		}
	}

	s.insertSemi = insertSemi
	return pos, tok, lit
}

func (s *Scanner) pos() token.Pos { return s.file.Pos(s.offset) }

func (s *Scanner) error(pos token.Pos, msg string) {
	s.ErrorCount++
	if s.err != nil {
		s.err(s.file.Position(pos), msg)
	}
}

func (s *Scanner) errorf(pos token.Pos, format string, args ...any) {
	s.error(pos, fmt.Sprintf(format, args...))
}

// next advances to the next character.
func (s *Scanner) next() {
	if s.nextOffset >= len(s.src) {
		s.offset = len(s.src)
		if s.ch == '\n' {
			s.file.AddLine(s.offset)
		}

		s.ch = eof
		return
	}

	s.offset = s.nextOffset
	if s.ch == '\n' {
		s.file.AddLine(s.offset)
	}

	r, w := rune(s.src[s.nextOffset]), 1
	if r >= utf8.RuneSelf {
		r, w = utf8.DecodeRuneInString(s.src[s.nextOffset:])
		if r == utf8.RuneError && w == 1 {
			s.error(s.pos(), "illegal UTF-8 encoding")
		}
	}

	s.nextOffset += w
	s.ch = r
}

// peek returns the byte after the current character without advancing.
func (s *Scanner) peek() byte {
	if s.nextOffset < len(s.src) {
		return s.src[s.nextOffset]
	}

	return 0
}

func (s *Scanner) skipWhitespace() {
	for s.ch == ' ' || s.ch == '\t' || s.ch == '\r' || s.ch == '\n' && !s.insertSemi {
		s.next()
	}
}

// skipLineComment consumes a // comment up to, not including, the newline.
func (s *Scanner) skipLineComment() {
	for s.ch != '\n' && s.ch != eof {
		s.next()
	}
}

// skipBlockComment consumes a /* */ comment and reports whether it
// contained a newline.
func (s *Scanner) skipBlockComment() bool {
	pos := s.pos()
	s.next() // the '*'
	hasNewline := false

	for s.ch != eof {
		ch := s.ch
		if ch == '\n' {
			hasNewline = true
		}

		s.next()
		if ch == '*' && s.ch == '/' {
			s.next()
			return hasNewline
		}
	}

	s.error(pos, "comment not terminated")
	return hasNewline
}

func (s *Scanner) scanIdentifier() string {
	offs := s.offset

	for isLetter(s.ch) || isDigit(s.ch) {
		s.next()
	}

	return s.src[offs:s.offset]
}

// scanDots scans the rest of a token that began with two dots; the first
// dot is consumed and the second is the current character.
func (s *Scanner) scanDots(pos token.Pos) (token.Kind, string) {
	s.next()

	switch s.ch {
	case '.':
		s.next()
		return token.ELLIPSIS, ""
	case '=':
		s.next()
		return token.RANGEINC, ""
	}

	s.error(pos, "unexpected '..'; did you mean '..='?")
	return token.ILLEGAL, ".."
}

// scanDigits consumes digits of the given base and underscores, and
// reports how many digits it saw. An underscore must sit between two
// digits, as in Go: 1_000 is fine, 1__0 and 1_ are not.
func (s *Scanner) scanDigits(base int) int {
	n := 0
	prevUnderscore := false

	for {
		switch {
		case s.ch == '_':
			if n == 0 || prevUnderscore {
				s.error(s.pos(), "'_' must separate successive digits")
			}

			prevUnderscore = true
		case base == 16 && isHex(s.ch),
			base == 10 && isDecimal(s.ch),
			base == 8 && '0' <= s.ch && s.ch <= '7',
			base == 2 && (s.ch == '0' || s.ch == '1'):
			n++
			prevUnderscore = false
		default:
			if prevUnderscore {
				s.error(s.pos(), "'_' must separate successive digits")
			}

			return n
		}

		s.next()
	}
}

func (s *Scanner) scanNumber() (token.Kind, string) {
	offs := s.offset
	tok := token.INT
	base := 10
	prefix := ""

	if s.ch == '0' {
		s.next()

		switch s.ch {
		case 'x', 'X':
			base, prefix = 16, "hexadecimal"
			s.next()
		case 'b', 'B':
			base, prefix = 2, "binary"
			s.next()
		case 'o', 'O':
			base, prefix = 8, "octal"
			s.next()
		}
	}

	if s.scanDigits(base) == 0 && prefix != "" {
		s.error(s.pos(), prefix+" literal has no digits")
	}

	if base == 10 {
		// A fraction, unless the dot starts a `..=` operator.
		if s.ch == '.' && s.peek() != '.' {
			tok = token.FLOAT
			s.next()
			s.scanDigits(10)
		}

		if s.ch == 'e' || s.ch == 'E' {
			tok = token.FLOAT
			s.next()
			if s.ch == '+' || s.ch == '-' {
				s.next()
			}

			if !isDecimal(s.ch) {
				s.error(s.pos(), "exponent has no digits")
			}

			s.scanDigits(10)
		}
	}

	return tok, s.src[offs:s.offset]
}

// scanEscape consumes an escape sequence after the backslash. quote is
// the delimiter of the enclosing literal.
func (s *Scanner) scanEscape(quote rune) {
	pos := s.pos()
	var n int
	var base, max uint32

	switch s.ch {
	case 'a', 'b', 'f', 'n', 'r', 't', 'v', '\\', quote:
		s.next()
		return
	case '0', '1', '2', '3', '4', '5', '6', '7':
		// The first octal digit is the current character; the loop
		// below consumes it along with the other two.
		n, base, max = 3, 8, 255
	case 'x':
		s.next()
		n, base, max = 2, 16, 255
	case 'u':
		s.next()
		n, base, max = 4, 16, unicode.MaxRune
	case 'U':
		s.next()
		n, base, max = 8, 16, unicode.MaxRune
	default:
		if s.ch == eof {
			s.error(pos, "escape sequence not terminated")
		} else {
			s.error(pos, "unknown escape sequence")
		}

		return
	}

	var x uint32
	for n > 0 {
		d := uint32(digitVal(s.ch))
		if d >= base {
			s.errorf(s.pos(), "illegal character %#U in escape sequence", s.ch)
			return
		}

		x = x*base + d
		s.next()
		n--
	}

	if x > max || 0xD800 <= x && x < 0xE000 {
		s.error(pos, "escape sequence is invalid Unicode code point")
	}
}

// scanString scans a "..." literal; the opening quote is already consumed.
func (s *Scanner) scanString() string {
	offs := s.offset - 1

	for {
		ch := s.ch
		if ch == '\n' || ch == eof {
			s.error(s.pos(), "string literal not terminated")
			break
		}

		s.next()
		if ch == '"' {
			break
		}

		if ch == '\\' {
			s.scanEscape('"')
		}
	}

	return s.src[offs:s.offset]
}

// scanRawString scans a `...` literal; the opening backquote is consumed.
func (s *Scanner) scanRawString() string {
	offs := s.offset - 1

	for {
		ch := s.ch
		if ch == eof {
			s.error(s.pos(), "raw string literal not terminated")
			break
		}

		s.next()
		if ch == '`' {
			break
		}
	}

	return s.src[offs:s.offset]
}

// scanRune scans a '.' literal; the opening quote is already consumed.
func (s *Scanner) scanRune() string {
	offs := s.offset - 1
	n := 0
	terminated := false

	for {
		ch := s.ch
		if ch == '\n' || ch == eof {
			s.error(s.pos(), "rune literal not terminated")
			break
		}

		s.next()
		if ch == '\'' {
			terminated = true
			break
		}

		n++
		if ch == '\\' {
			s.scanEscape('\'')
		}
	}

	if terminated && n != 1 {
		s.error(s.pos(), "illegal rune literal")
	}

	return s.src[offs:s.offset]
}

// switch2 returns tok1 if the current character is '=', else tok0.
func (s *Scanner) switch2(tok0, tok1 token.Kind) token.Kind {
	if s.ch == '=' {
		s.next()
		return tok1
	}

	return tok0
}

// switch3 is switch2 with a third option: tok2 if the current character
// is ch2.
func (s *Scanner) switch3(tok0, tok1 token.Kind, ch2 rune, tok2 token.Kind) token.Kind {
	if s.ch == '=' {
		s.next()
		return tok1
	}

	if s.ch == ch2 {
		s.next()
		return tok2
	}

	return tok0
}

// switch4 is switch3 with a fourth option: tok3 if ch2 is followed by '='.
func (s *Scanner) switch4(tok0, tok1 token.Kind, ch2 rune, tok2, tok3 token.Kind) token.Kind {
	if s.ch == '=' {
		s.next()
		return tok1
	}

	if s.ch == ch2 {
		s.next()
		if s.ch == '=' {
			s.next()
			return tok3
		}

		return tok2
	}

	return tok0
}

func isLetter(ch rune) bool {
	return 'a' <= ch && ch <= 'z' || 'A' <= ch && ch <= 'Z' || ch == '_' || ch >= utf8.RuneSelf && unicode.IsLetter(ch)
}

func isDigit(ch rune) bool {
	return '0' <= ch && ch <= '9' || ch >= utf8.RuneSelf && unicode.IsDigit(ch)
}

func isDecimal(ch rune) bool { return '0' <= ch && ch <= '9' }

func isHex(ch rune) bool {
	return '0' <= ch && ch <= '9' || 'a' <= ch && ch <= 'f' || 'A' <= ch && ch <= 'F'
}

func digitVal(ch rune) int {
	switch {
	case '0' <= ch && ch <= '9':
		return int(ch - '0')
	case 'a' <= ch && ch <= 'f':
		return int(ch - 'a' + 10)
	case 'A' <= ch && ch <= 'F':
		return int(ch - 'A' + 10)
	}

	return 16
}
