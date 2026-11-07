// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/types"
)

// The program the Error trait's helpers are written for: a derived
// Error() and Source() on an enum, a struct and a type over int, a
// written Source, ? into a variant and into a trait value, %+v, and
// a trait value printed. What it prints is checked by the error
// programs of backendtest, which both backends run; this test reads
// the IR written for it, so that each helper of error.go is seen in
// the output under its own name.
const errorSrc = `package main
import ("fmt"; "strconv")
enum AppError impl Error, Clone { Parse(cause strconv.Error); Missing(key string) }
struct Pair impl Error { key string; n int }
type Code int impl Error
struct Wrapped impl Error { cause AppError }
func (e ref Wrapped) Error() string { return "wrapped" }
func (e ref Wrapped) Source() Error? { return Some(e.cause.Clone()) }
func parse(text ref string) Result<int, AppError> { n := strconv.Atoi(text)?; return Ok(n) }
func parseAny(text ref string) Result<int, Error> { n := strconv.Atoi(text)?; return Ok(n) }
func main() {
	q := "q"
	fmt.Println(parse(ref q), parseAny(ref q), Pair{key: "k", n: 1}, Code(7))
	w := Wrapped{cause: Missing("port")}
	fmt.Printf("%+v %+v\n", w, Code(7))
	var boxed Error = w
	fmt.Println(boxed)
	fmt.Printf("%+v\n", boxed)
}
`

func emitError(t *testing.T) string {
	t.Helper()
	f, file, err := parser.ParseFile("t.snello", []byte(errorSrc))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	imp := importer.NewImporter(info)
	if _, err := (&types.Config{Importer: imp}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	out, err := Emit(Config{}, file, f, info, libraries(imp)...)
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}

// irFunction returns the body of the IR function named name, up to its
// closing brace, or fails the test.
func irFunction(t *testing.T, out, name string) string {
	t.Helper()
	i := strings.Index(out, "define internal ")
	for i >= 0 {
		line := out[i:]
		line = line[:strings.Index(line, "\n")]
		if strings.Contains(line, " @"+name+"(") {
			body := out[i:]
			return body[:strings.Index(body, "\n}\n")]
		}

		next := strings.Index(out[i+1:], "define internal ")
		if next < 0 {
			break
		}

		i += 1 + next
	}

	t.Fatalf("no function written as @%s", name)
	return ""
}

// The helpers error.go writes: a derived Source, the chain behind %+v
// on a declared type and on a trait value, and a trait value's message
// through its table. The rest of what the program needs is tested
// beside the file that writes it.
func TestSourcer(t *testing.T) {
	out := emitError(t)
	for _, name := range []string{"sn_src_AppError", "sn_src_Code", "sn_src_sn_strconv_Error"} {
		if body := irFunction(t, out, name); !strings.Contains(body, "ret { i1, %sn_trait } zeroinitializer") {
			t.Errorf("@%s does not give None:\n%s", name, body)
		}
	}
}

func TestChainer(t *testing.T) {
	out := emitError(t)
	tests := []struct{ name, want, what string }{
		{"sn_chain_Wrapped", "call %sn_string @sn_Wrapped_Error(ptr %v)", "the chain starts with the type's own message"},
		{"sn_chain_Wrapped", "call { i1, %sn_trait } @sn_Wrapped_Source(ptr %v)", "and asks the type for its source"},
		{"sn_chain_Wrapped", "chain.cond", "walking down the sources"},
		{"sn_chain_Wrapped", "call void @sn_drop_Error(ptr", "each source dropped once asked"},
		{"sn_chain_Code", "call %sn_string @sn_str_Code(ptr %v)", "%+v on a derived message starts with the printed form"},
		{"sn_chain_Code", "call { i1, %sn_trait } @sn_src_Code(ptr %v)", "and with the derived source"},
		{"sn_chain_Error", "getelementptr inbounds ptr, ptr %t", "%+v on a trait value starts through its table"},
	}
	for _, tt := range tests {
		if body := irFunction(t, out, tt.name); !strings.Contains(body, tt.want) {
			t.Errorf("%s: @%s lacks %q:\n%s", tt.what, tt.name, tt.want, body)
		}
	}
}
