// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/types"
)

// TestFormsAgree checks every package of the standard library and
// every example with the checker's move pass over the form running
// beside the tree's, which reports every disagreement as an error: the
// corpus the pass over the form must agree with before it runs alone.
func TestFormsAgree(t *testing.T) {
	types.CompareForms(true)
	defer types.CompareForms(false)
	root := filepath.Join("..", "..")
	std := filepath.Join(root, "std")
	err := filepath.WalkDir(std, func(dir string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || filepath.Base(dir) == "builtin" || filepath.Base(dir) == "testdata" {
			return err
		}

		sources, _ := filepath.Glob(filepath.Join(dir, "*.snello"))
		if len(sources) == 0 {
			return nil
		}

		rel, _ := filepath.Rel(std, dir)
		if err := CheckDir(dir, &types.Info{}, Options{}); err != nil {
			t.Errorf("std/%s: %v", rel, err)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	examples, _ := filepath.Glob(filepath.Join(root, "examples", "*"))
	for _, dir := range examples {
		if filepath.Base(dir) == "cli-grep" { // waits for a library it imports
			continue
		}

		sources, _ := filepath.Glob(filepath.Join(dir, "*.snello"))
		if len(sources) == 0 {
			continue
		}

		var mains []string
		for _, src := range sources {
			if text, err := os.ReadFile(src); err == nil && strings.Contains(string(text), "func main") {
				mains = append(mains, src)
			}
		}

		programs := [][]string{sources}
		if len(mains) > 1 { // one program per file
			programs = nil
			for _, main := range mains {
				programs = append(programs, []string{main})
			}
		}

		for _, names := range programs {
			if _, _, err := LoadProgram(names, platform.Host(), &types.Info{}); err != nil {
				t.Errorf("%s: %v", names[0], err)
			}
		}
	}
}
