// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import "testing"

// TestPathPrefix writes the symbol prefix of import paths, and refuses
// two paths that would share one.
func TestPathPrefix(t *testing.T) {
	tests := []struct{ path, prefix string }{
		{"", "sn__"},
		{"fmt", "sn_fmt_"},
		{"net/http", "sn_net__http_"},
		{"example.com/app/shapes", "sn_example_com__app__shapes_"},
		{"example.com/my-app", "sn_example_com__my_app_"},
	}
	for _, tt := range tests {
		if got := PathPrefix(tt.path); got != tt.prefix {
			t.Errorf("PathPrefix(%q) = %q, want %q", tt.path, got, tt.prefix)
		}
	}

	if err := CheckPrefixes([]Library{{Path: "fmt"}, {Path: "example.com/app/a.b"}, {Path: "example.com/app/a_c"}}); err != nil {
		t.Errorf("distinct prefixes: %v", err)
	}

	err := CheckPrefixes([]Library{{Path: "example.com/app/a.b"}, {Path: "example.com/app/a_b"}})
	if want := "import paths example.com/app/a.b and example.com/app/a_b would share one symbol prefix"; err == nil || err.Error() != want {
		t.Errorf("shared prefix: got %v, want %q", err, want)
	}
}
