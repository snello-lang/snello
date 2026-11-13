// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/snello-lang/snello/compiler/emit/c"
)

// A release build optimizes the module and the runtime as one unit,
// so that a runtime function a program calls in a loop is written into
// it in place of the call. LLVM writes one function into another only
// when the processor the caller is compiled for has every feature the
// callee is compiled for. Clang names the processor and its features
// on each function of the runtime it compiles; a function of the
// module that named none would be compiled for the plainest processor
// of its kind, which lacks some, and then no function of the runtime
// would ever be written into the program. So every function of the
// module names the same processor and features as the runtime's, which
// are clang's own for the target, asked of clang itself, as clang's
// and Rust's compilers put them on every function they write.
//
// The emitter knows nothing of the machine: every function it writes
// refers to one attribute group, which it writes at the end of the
// module holding a mark alone, and Targeted puts the target's
// attributes in the mark's place when the module is built, so that no
// way of building a module can leave them out.

// funcAttrs is the reference every function's definition makes to the
// module's attribute group, after its parameters, and emptyGroup the
// group as the emitter writes it, the last line of a module: a group
// may not be empty, and the mark is an attribute LLVM reads past.
// noJumpTables is the attribute the group is filled with beside the
// target's: a switch is written as comparisons, as the C backend's
// -fno-jump-tables writes the runtime's, since the analysis of a task's
// stack cannot follow a jump through a table of addresses.
const (
	funcAttrs    = " #0"
	emptyGroup   = "attributes #0 = { \"snello-target\"=\"unset\" }\n"
	noJumpTables = "\"no-jump-tables\"=\"true\""
)

// targetKeys are the attributes of a function that say what processor
// it is compiled for.
var targetKeys = []string{"target-cpu", "target-features", "tune-cpu"}

// targets holds the answers of this process, by the key of the file
// that keeps each.
var targets sync.Map

// Targeted returns the module with its attribute group filled with
// the processor and features of the target its triple names, so that
// every function of the module is compiled for them. A module whose
// group is filled already, or that has none, is returned as it is; a
// clang that cannot be asked is an error, since the module built
// without the answer would run with no function of the runtime written
// into it, and say nothing.
func Targeted(module []byte) ([]byte, error) {
	if !bytes.HasSuffix(module, []byte(emptyGroup)) {
		return module, nil
	}

	triple := moduleTriple(module)
	if triple == "" {
		return module, nil
	}

	attrs, err := target(triple)
	if err != nil {
		return module, fmt.Errorf("asking %s what processor %s is: %w", Compiler(), triple, err)
	}

	out := make([]byte, 0, len(module)+len(attrs)+1)
	out = append(out, module[:len(module)-len(emptyGroup)]...)
	return append(out, "attributes #0 = { "+attrs+" "+noJumpTables+" }\n"...), nil
}

// target returns the attributes clang gives every function it compiles
// for a target triple, the processor and its features, written as a
// module's attribute group holds them. Clang is asked once for each
// clang and target, and the answer kept in the cache directory, since
// the question costs a tenth of a small build. The clang is known as
// the runtime's cache knows a compiler, by its path, size, time and
// what it says its version is: the clang on a Mac's path is a launcher
// that an update of the compiler behind it leaves as it was. An answer
// read from the cache is held to the form an answer has, and one that
// is not, a file cut short or written over, is asked for again.
func target(triple string) (string, error) {
	id, err := c.CompilerID(Compiler())
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(fmt.Appendf(nil, "snello target 2\n%s\n%s\n", id, triple))
	key := hex.EncodeToString(sum[:])[:32]
	if v, ok := targets.Load(key); ok {
		return v.(string), nil
	}

	var kept string
	if dir, ok := c.CacheDir(); ok {
		kept = filepath.Join(dir, "target", key)
		if b, err := os.ReadFile(kept); err == nil && validTarget(string(b)) {
			targets.Store(key, string(b))
			return string(b), nil
		}
	}

	cmd := exec.Command(Compiler(), "-target", triple, "-O2", "-S", "-emit-llvm", "-x", "c", "-o", "-", "-")
	cmd.Stdin = strings.NewReader("void f(void) {}\n")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	attrs := targetAttributes(out)
	if !validTarget(attrs) {
		return "", fmt.Errorf("it names no processor on a function it compiles")
	}

	targets.Store(key, attrs)
	if kept != "" { // written beside and renamed, so that a build reads the answer whole or not at all
		if os.MkdirAll(filepath.Dir(kept), 0o755) == nil {
			if f, err := os.CreateTemp(filepath.Dir(kept), "new-*"); err == nil {
				_, werr := f.WriteString(attrs)
				if cerr := f.Close(); werr != nil || cerr != nil || os.Rename(f.Name(), kept) != nil {
					os.Remove(f.Name())
				}
			}
		}
	}

	return attrs, nil
}

// validTarget reports whether attrs is an answer as target gives one:
// what targetAttributes reads back out of a group holding it, and
// naming a processor or its features.
func validTarget(attrs string) bool {
	return attrs != "" && targetAttributes([]byte("attributes #0 = { "+attrs+" }\n")) == attrs
}

// targetAttributes reads, from a module clang wrote for a function,
// the attributes of that function that say what processor it is
// compiled for, each as "key"="value", in the order of targetKeys.
func targetAttributes(module []byte) string {
	var attrs []string
	for line := range bytes.Lines(module) {
		if !bytes.HasPrefix(line, []byte("attributes #")) {
			continue
		}

		for _, key := range targetKeys {
			start := []byte(`"` + key + `"="`)
			_, rest, found := bytes.Cut(line, start)
			if !found {
				continue
			}

			if value, _, closed := bytes.Cut(rest, []byte(`"`)); closed {
				attrs = append(attrs, string(start)+string(value)+`"`)
			}
		}

		break
	}

	return strings.Join(attrs, " ")
}
