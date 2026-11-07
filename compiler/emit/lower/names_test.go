// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import "testing"

// TestPathPrefix writes the symbol prefix of import paths, a package of
// the program's own and a standard one, and refuses two paths that
// would share one.
func TestPathPrefix(t *testing.T) {
	tests := []struct{ path, prefix, std string }{
		{"shapes", "snm_shapes_", "sn_shapes_"},
		{"fmt", "snm_fmt_", "sn_fmt_"},
		{"net/http", "snm_net__http_", "sn_net__http_"},
		{"app/shapes", "snm_app__shapes_", "sn_app__shapes_"},
		{"my-app", "snm_my_app_", "sn_my_app_"},
	}
	for _, tt := range tests {
		if got := PathPrefix(tt.path); got != tt.prefix {
			t.Errorf("PathPrefix(%q) = %q, want %q", tt.path, got, tt.prefix)
		}

		if got := StdPrefix(tt.path); got != tt.std {
			t.Errorf("StdPrefix(%q) = %q, want %q", tt.path, got, tt.std)
		}
	}

	if err := CheckPrefixes([]Library{{Path: "fmt"}, {Path: "app/a.b"}, {Path: "app/a_c"}}); err != nil {
		t.Errorf("distinct prefixes: %v", err)
	}

	err := CheckPrefixes([]Library{{Path: "app/a.b"}, {Path: "app/a_b"}})
	if want := "import paths app/a.b and app/a_b would share one symbol prefix"; err == nil || err.Error() != want {
		t.Errorf("shared prefix: got %v, want %q", err, want)
	}
}
