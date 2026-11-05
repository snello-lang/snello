// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scanner

import (
	"fmt"
	"sort"

	"github.com/snello-lang/snello/compiler/token"
)

// An Error is one lexical or syntax error with its resolved position.
// It is shared by the scanner and the parser, as in go/scanner.
type Error struct {
	Pos token.Position
	Msg string
}

// An ErrorList collects errors in the order they were reported. Its
// zero value is ready to use.
type ErrorList []*Error

// Error returns the position and message, formatted for a terminal.
func (e *Error) Error() string {
	if e.Pos.IsValid() {
		return e.Pos.String() + ": " + e.Msg
	}

	return e.Msg
}

// Add appends an error.
func (l *ErrorList) Add(pos token.Position, msg string) {
	*l = append(*l, &Error{pos, msg})
}

// Len returns the number of errors.
func (l ErrorList) Len() int { return len(l) }

// Sort orders the list by position.
func (l ErrorList) Sort() {
	sort.SliceStable(l, func(i, j int) bool {
		a, b := l[i].Pos, l[j].Pos
		if a.Filename != b.Filename {
			return a.Filename < b.Filename
		}

		return a.Offset < b.Offset
	})
}

// Error returns the first error and a count of the rest, so a list can
// stand in for a single error.
func (l ErrorList) Error() string {
	switch len(l) {
	case 0:
		return "no errors"
	case 1:
		return l[0].Error()
	}

	return fmt.Sprintf("%s (and %d more errors)", l[0], len(l)-1)
}

// Err returns the list as an error, or nil when it is empty, so a caller
// can write `if err := list.Err(); err != nil`.
func (l ErrorList) Err() error {
	if len(l) == 0 {
		return nil
	}

	return l
}
