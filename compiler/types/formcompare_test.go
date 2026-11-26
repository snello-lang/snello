// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "testing"

// TestIsMoveMessage sorts the checker's messages into the move pass's,
// compared against the form's, and the rest, the borrow pass's refusal
// of a move among them.
func TestIsMoveMessage(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"use of moved value s; moved at 1:2", true},
		{"use of partially moved value n: n.value moved at 1:2", true},
		{"cannot move s out of a borrow", true},
		{"s moved in a previous iteration of the loop", true},
		{"cannot move v: v is borrowed by r", false},
		{"cannot move v: v is borrowed at 1:2", false},
		{"undefined: s", false},
	}
	for _, tt := range tests {
		if got := isMoveMessage(tt.msg); got != tt.want {
			t.Errorf("isMoveMessage(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

// TestIsEscapeMessage sorts the checker's messages into the escape
// pass's: a returned borrow refused, and a closure that borrows kept
// somewhere it may not go.
func TestIsEscapeMessage(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"cannot return a borrow of local s", true},
		{"closure captures s by borrow and cannot be returned", true},
		{"closure g captures s by borrow and cannot be stored", true},
		{"closure captures s by borrow and cannot be sent", true},
		{"closure captures s by borrow and cannot be spawned", true},
		{"closure captures s by borrow and cannot be passed to f, which keeps g", false},
		{"cannot move v: v is borrowed by r", false},
	}
	for _, tt := range tests {
		if got := isEscapeMessage(tt.msg); got != tt.want {
			t.Errorf("isEscapeMessage(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}
