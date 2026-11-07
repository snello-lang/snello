// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "testing"

// The Error trait as the universe declares it: a message and a cause,
// both on ref Self, the cause an owned trait value of Error itself.
func TestErrorTraitMethods(t *testing.T) {
	if n := len(ErrorTrait.required); n != 2 {
		t.Fatalf("Error requires %d methods, want 2", n)
	}

	for _, tt := range []struct{ name, want string }{
		{"Error", "ref Self Error() string"},
		{"Source", "ref Self Source() Error?"},
	} {
		m := ErrorTrait.Method(tt.name)
		if m == nil {
			t.Errorf("Error has no method %s", tt.name)
			continue
		}

		sig := m.Type().(*Signature)
		if got := sig.Recv().Type().String() + " " + methodText(tt.name, sig); got != tt.want {
			t.Errorf("Error.%s is %s, want %s", tt.name, got, tt.want)
		}

		if !m.Derived() {
			continue
		}

		t.Errorf("Error.%s is marked derived on the trait itself", tt.name)
	}
}
