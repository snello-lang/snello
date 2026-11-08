// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strings"
	"testing"
)

// builtinCall writes replace(mut place, v) through the place's address,
// taken first: the new value is built, then the old one loaded out and
// the new one stored in, so the place is never empty.
func TestReplace(t *testing.T) {
	body := irFunction(t, emitSource(t, `package main
struct Box { name string }
func main() { b := Box{name: "a".Clone()}; old := replace(mut b.name, "b".Clone()); print(old, b.name) }`), "sn_main")
	gep := strings.Index(body, " = getelementptr inbounds %Box")
	if gep < 0 {
		t.Fatalf("main takes no address of the field:\n%s", body)
	}

	p := body[strings.LastIndex(body[:gep], "%"):gep] // the address's name
	load := strings.Index(body, "load %sn_string, ptr "+p+",")
	if load < gep {
		t.Fatalf("main does not load the old value through %s:\n%s", p, body)
	}

	line := body[load:]
	line = line[strings.Index(line, "\n")+1:]
	if !strings.HasPrefix(strings.TrimSpace(line), "store %sn_string") || !strings.Contains(line[:strings.Index(line, "\n")], "ptr "+p+",") {
		t.Errorf("main does not store the new value through %s right after loading the old one:\n%s", p, body)
	}
}
