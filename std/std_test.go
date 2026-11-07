// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package std_test

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/testrun"
	"github.com/snello-lang/snello/std"
)

// TestPackages runs the standard library's own tests, written in
// Snello in each package's _test.snello files, through the runner
// snello test uses, under the library's privileges, through both
// backends; every package with a test file must pass.
func TestPackages(t *testing.T) {
	var paths []string
	fs.WalkDir(std.Sources, ".", func(name string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(name, "_test.snello") {
			paths = append(paths, path.Dir(name))
		}

		return nil
	})

	sort.Strings(paths)
	if len(paths) == 0 {
		t.Fatal("no standard package has a test file")
	}

	for _, p := range paths {
		for _, backend := range []string{"llvm", "c"} {
			t.Run(p+"/"+backend, func(t *testing.T) {
				plan, err := testrun.PlanStd(p)
				if err != nil {
					t.Fatal(err)
				}

				bin, err := plan.Build(backend, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}

				var out bytes.Buffer
				cmd := exec.Command(bin, "-v")
				cmd.Stdout, cmd.Stderr = &out, &out
				if err := cmd.Run(); err != nil || !strings.Contains(out.String(), "\nok  \t"+p+"\t") {
					t.Errorf("%s: %v\n%s", p, err, out.String())
				}
			})
		}
	}
}

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("cc"); err != nil {
		os.Stderr.WriteString("skipping std tests: no C compiler on the PATH\n")
		os.Exit(0)
	}

	os.Exit(m.Run())
}
