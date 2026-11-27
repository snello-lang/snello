// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testrun

import (
	"bytes"
	"encoding/json"
	"github.com/snello-lang/snello/internal/testproc"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// jsonSrc and jsonTest are a package with a test of every outcome: one
// that passes with subtests, a failed check after a log, a panic, a
// skip, a failed slice check, and an example that prints other than it
// must.
const jsonSrc = `package p

func Two() int { return 2 }
`

const jsonTest = `package p

import ("expect"; "fmt"; "testing")

func TestPass() {
	testing.Run("one", func() { expect.Equal(ref Two(), ref 2) })
}

func TestLogged() {
	testing.Log("a note")
	fmt.Println("printed by the test")
	expect.Equal(ref Two(), ref 3)
}

func TestBoom() {
	xs := []int{}
	if len(xs) == 0 {
		panic("boom")
	}
}

func TestSkip() { testing.Skip("not here") }

func TestSlice() { expect.EqualSlice(ref []int{1, 2}, ref []int{1, 3}) }

// ExampleTwo prints two.
func ExampleTwo() {
	fmt.Println(Two())
	fmt.Println("x")
	// Output:
	// 2
	// y
}
`

// framed writes lines as the test program writes them under jsonFlag:
// each line given with a leading ! framed, any other written as it is.
func framed(lines ...string) string {
	var b strings.Builder
	for _, line := range lines {
		if rest, own := strings.CutPrefix(line, "!"); own {
			b.WriteByte(framing)
			line = rest
		}

		b.WriteString(line)
		b.WriteByte('\n')
	}

	return b.String()
}

// convert runs a converter over out, the program's output written in
// one piece, and returns the events it wrote, without their times, and
// the output events left out unless keep says otherwise.
func convert(t *testing.T, dir, out string, code int, keep bool) []Event {
	t.Helper()
	var b bytes.Buffer
	j := newConverter(&b, "p", dir)
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	j.now, j.start = func() time.Time { return at }, at
	if _, err := j.Write([]byte(out)); err != nil {
		t.Fatal(err)
	}

	j.finish(code)
	return decode(t, b.Bytes(), keep)
}

// decode reads the events of a stream, each with its time and package
// checked and then cleared.
func decode(t *testing.T, stream []byte, keep bool) []Event {
	t.Helper()
	var events []Event
	for _, line := range bytes.Split(bytes.TrimSuffix(stream, []byte("\n")), []byte("\n")) {
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("%s: %v", line, err)
		}

		if ev.Time == nil || ev.Package == "" {
			t.Errorf("%s: no time or no package", line)
		}

		ev.Time, ev.Package = nil, ""
		if keep || ev.Action != ActionOutput {
			events = append(events, ev)
		}
	}

	return events
}

func text(s string) *string { return &s }

// TestConvert converts the output of each kind of run into its events:
// the actions, the tests they are of, and a failure's place, message and
// values.
func TestConvert(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p_test.snello"), []byte(jsonTest), 0o644); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(dir, "p_test.snello")
	if abs, err := filepath.Abs(testFile); err == nil {
		testFile = abs
	}

	tests := []struct {
		name string
		out  string
		code int
		want []Event
	}{
		{"a pass, a subtest and the package", framed("!=== RUN   TestPass", "!=== RUN   TestPass/one", "!--- PASS: TestPass/one (0.00s)", "!--- PASS: TestPass (0.01s)", "!PASS", "!ok  \tp\t0.012s"), 0, []Event{
			{Action: ActionStart},
			{Action: ActionRun, Test: "TestPass"},
			{Action: ActionRun, Test: "TestPass/one"},
			{Action: ActionPass, Test: "TestPass/one"},
			{Action: ActionPass, Test: "TestPass", Elapsed: 0.01},
			{Action: ActionPass, Elapsed: 0.012},
		}},
		{"a failed check after a log, its place, message and values", framed("!=== RUN   TestLogged", "printed by the test", "!--- FAIL: TestLogged (0.00s)", "    p_test.snello:11: a note", "!    p_test.snello:13: expect.Equal(ref Two(), ref 3)", "!        got:  2", "!        want: 3   (int)", "!FAIL", "!FAIL\tp\t0.001s"), 1, []Event{
			{Action: ActionStart},
			{Action: ActionRun, Test: "TestLogged"},
			{Action: ActionFailure, Test: "TestLogged", File: testFile, Line: 13, Message: "expect.Equal(ref Two(), ref 3)", Got: text("2"), Want: text("3")},
			{Action: ActionFail, Test: "TestLogged"},
			{Action: ActionFail, Elapsed: 0.001},
		}},
		{"a panic, placed at the innermost frame in the package's files", framed("!=== RUN   TestBoom", "!--- FAIL: TestBoom (0.00s)", "!    boom", "!        p.TestBoom at p_test.snello:19", "!        testing.runAndReport at testing.snello:548"), 1, []Event{
			{Action: ActionStart},
			{Action: ActionRun, Test: "TestBoom"},
			{Action: ActionFailure, Test: "TestBoom", File: testFile, Line: 19, Message: "boom\np.TestBoom at p_test.snello:19\ntesting.runAndReport at testing.snello:548"},
			{Action: ActionFail, Test: "TestBoom"},
			{Action: ActionFail},
		}},
		{"a skip", framed("!=== RUN   TestSkip", "!--- SKIP: TestSkip (0.00s)", "!    not here"), 0, []Event{
			{Action: ActionStart},
			{Action: ActionRun, Test: "TestSkip"},
			{Action: ActionSkip, Test: "TestSkip"},
			{Action: ActionPass},
		}},
		{"a slice check, its type notes left out", framed("!--- FAIL: TestSlice (0.00s)", "!    p_test.snello:24: expect.EqualSlice(ref []int{1, 2}, ref []int{1, 3})", "!        got:  [1 2]   (elements, 2 elements)", "!        want: [1 3]   (2 elements)", "!        element 1: got 2 where 3 was wanted"), 1, []Event{
			{Action: ActionStart},
			{Action: ActionFailure, Test: "TestSlice", File: testFile, Line: 24, Message: "expect.EqualSlice(ref []int{1, 2}, ref []int{1, 3})\nelement 1: got 2 where 3 was wanted", Got: text("[1 2]"), Want: text("[1 3]")},
			{Action: ActionFail, Test: "TestSlice"},
			{Action: ActionFail},
		}},
		{"an example's texts, each on the lines below its label", framed("!=== RUN   ExampleTwo", "!--- FAIL: ExampleTwo (0.00s)", "!    got:", "!        2", "!        x", "!    want:", "!        2", "!        y"), 1, []Event{
			{Action: ActionStart},
			{Action: ActionRun, Test: "ExampleTwo"},
			{Action: ActionFailure, Test: "ExampleTwo", Got: text("2\nx"), Want: text("2\ny")},
			{Action: ActionFail, Test: "ExampleTwo"},
			{Action: ActionFail},
		}},
		{"a benchmark's line", framed("!BenchmarkTwo             1200000000         0.86 ns/op", "!ok  \tp\t1.070s"), 0, []Event{
			{Action: ActionStart},
			{Action: ActionBench, Test: "BenchmarkTwo", Output: "BenchmarkTwo             1200000000         0.86 ns/op\n"},
			{Action: ActionPass, Elapsed: 1.07},
		}},
		{"an example that printed nothing, its empty text kept", framed("!--- FAIL: ExampleTwo (0.00s)", "!    got:", "!        ", "!    want:", "!        2"), 1, []Event{
			{Action: ActionStart},
			{Action: ActionFailure, Test: "ExampleTwo", Got: text(""), Want: text("2")},
			{Action: ActionFail, Test: "ExampleTwo"},
			{Action: ActionFail},
		}},
		{"a result after output left unended", framed("!=== RUN   TestPass") + "x\x16--- PASS: TestPass (0.00s)\n", 0, []Event{
			{Action: ActionStart},
			{Action: ActionRun, Test: "TestPass"},
			{Action: ActionPass, Test: "TestPass"},
			{Action: ActionPass},
		}},
		{"a test the program's end left running", framed("!=== RUN   TestSlow", "panic: tests timed out after 1s"), 2, []Event{
			{Action: ActionStart},
			{Action: ActionRun, Test: "TestSlow"},
			{Action: ActionFail, Test: "TestSlow"},
			{Action: ActionFail},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := append([]Event{{Action: ActionStart}}, convert(t, dir, tt.out, tt.code, false)...)
			if !sameEvents(got, tt.want) {
				t.Errorf("events:\n%s\nwant:\n%s", show(got), show(tt.want))
			}
		})
	}
}

// TestConvertOutput holds every line of the run to an output event of
// its own: a framed line as the runner wrote it, a test's own line as
// the running test's, and a line the program left unended.
func TestConvertOutput(t *testing.T) {
	out := framed("!=== RUN   TestLogged", "printed by the test") + "no newline"
	events := convert(t, "", out, 1, true)
	var outputs []Event
	for _, ev := range events {
		if ev.Action == ActionOutput {
			outputs = append(outputs, ev)
		}
	}

	want := []Event{
		{Action: ActionOutput, Test: "TestLogged", Output: "=== RUN   TestLogged\n"},
		{Action: ActionOutput, Test: "TestLogged", Output: "printed by the test\n"},
		{Action: ActionOutput, Test: "TestLogged", Output: "no newline\n"},
	}

	if !sameEvents(outputs, want) {
		t.Errorf("outputs:\n%s\nwant:\n%s", show(outputs), show(want))
	}
}

// TestConvertAfterResult gives what a passed test logged, and a skipped
// test's reason, both written after the result, to that test, and
// what the next test prints to the next.
func TestConvertAfterResult(t *testing.T) {
	out := framed("!=== RUN   TestA", "!--- PASS: TestA (0.00s)", "    p_test.snello:3: logged", "!=== RUN   TestSkip", "!--- SKIP: TestSkip (0.00s)", "!    not here", "!=== RUN   TestB", "printed by B")
	want := map[string]string{
		"    p_test.snello:3: logged\n": "TestA",
		"    not here\n":                "TestSkip",
		"printed by B\n":                "TestB",
	}

	for _, ev := range convert(t, "", out, 0, true) {
		if test, ok := want[ev.Output]; ok && ev.Test != test {
			t.Errorf("%q is %q's, want %q's", ev.Output, ev.Test, test)
		}
	}
}

// TestConvertSplit converts a stream written a byte at a time as the
// same stream written whole.
func TestConvertSplit(t *testing.T) {
	out := framed("!=== RUN   TestPass", "!--- PASS: TestPass (0.00s)", "!ok  \tp\t0.001s")
	whole := convert(t, "", out, 0, true)
	var b bytes.Buffer
	j := newConverter(&b, "p", "")
	for i := 0; i < len(out); i++ {
		j.Write([]byte{out[i]})
	}

	j.finish(0)
	if split := decode(t, b.Bytes(), true); !sameEvents(split, whole) {
		t.Errorf("a byte at a time:\n%s\nwhole:\n%s", show(split), show(whole))
	}
}

// TestRunJSON runs a package's tests as events through both backends:
// the program frames its report, and the converter turns each outcome
// into its events; a package that does not compile is its error and
// its failure.
func TestRunJSON(t *testing.T) {
	root := writeTree(t, map[string]string{
		"snello.mod":        "snello 0.1.0\n",
		"p/p.snello":        jsonSrc,
		"p/p_test.snello":   jsonTest,
		"bad/bad.snello":    "package bad\n\nfunc Two() int { return \"two\" }\n",
		"bad/b_test.snello": "package bad\n\nfunc TestTwo() {}\n",
	})

	for _, backend := range []string{"llvm", "c"} {
		var out bytes.Buffer
		if code := RunJSON(filepath.Join(root, "p"), backend, nil, &out, nil, testproc.Command); code != 1 {
			t.Errorf("%s: exited %d, want 1\n%s", backend, code, out.String())
		}

		if packages := packagesOf(t, out.Bytes()); len(packages) != 1 || !packages["example.com/m/p"] && !packages["p"] {
			t.Errorf("%s: the events name the packages %v, want one", backend, packages)
		}

		results := map[string]string{}
		failures := map[string]Event{}
		for _, ev := range decode(t, out.Bytes(), false) {
			switch ev.Action {
			case ActionPass, ActionFail, ActionSkip:
				results[ev.Test] = ev.Action
			case ActionFailure:
				failures[ev.Test] = ev
			}
		}

		wantResults := map[string]string{"TestPass": ActionPass, "TestPass/one": ActionPass, "TestLogged": ActionFail, "TestBoom": ActionFail, "TestSkip": ActionSkip, "TestSlice": ActionFail, "ExampleTwo": ActionFail, "": ActionFail}
		for name, action := range wantResults {
			if results[name] != action {
				t.Errorf("%s: %q ended %q, want %q", backend, name, results[name], action)
			}
		}

		testFile, _ := filepath.Abs(filepath.Join(root, "p", "p_test.snello"))
		if f := failures["TestLogged"]; f.File != testFile || f.Line != lineIn(jsonTest, "ref Two(), ref 3") || f.Got == nil || *f.Got != "2" || f.Want == nil || *f.Want != "3" {
			t.Errorf("%s: TestLogged's failure %+v", backend, f)
		}

		if f := failures["TestBoom"]; f.File != testFile || f.Line != lineIn(jsonTest, `panic("boom")`) || !strings.HasPrefix(f.Message, "boom") {
			t.Errorf("%s: TestBoom's failure %+v", backend, f)
		}

		if f := failures["ExampleTwo"]; f.Got == nil || *f.Got != "2\nx" || f.Want == nil || *f.Want != "2\ny" {
			t.Errorf("%s: ExampleTwo's failure %+v", backend, f)
		}
	}

	var out bytes.Buffer
	if code := RunJSON(filepath.Join(root, "bad"), "llvm", nil, &out, nil, testproc.Command); code != 1 {
		t.Errorf("a package that does not compile: exited %d", code)
	}

	events := decode(t, out.Bytes(), true)
	last := events[len(events)-1]
	if events[0].Action != ActionStart || last.Action != ActionFail || last.Test != "" || !strings.Contains(out.String(), "cannot use a value of type string as int") {
		t.Errorf("a package that does not compile:\n%s", out.String())
	}
}

// packagesOf is the set of packages the events of a stream name.
func packagesOf(t *testing.T, stream []byte) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, line := range bytes.Split(bytes.TrimSuffix(stream, []byte("\n")), []byte("\n")) {
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("%s: %v", line, err)
		}

		names[ev.Package] = true
	}

	return names
}

// lineIn is the line of src that holds needle, counted from 1.
func lineIn(src, needle string) int {
	for i, line := range strings.Split(src, "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}

	return 0
}

// sameEvents reports whether two lists of events are the same, value
// for value.
func sameEvents(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if show([]Event{a[i]}) != show([]Event{b[i]}) {
			return false
		}
	}

	return true
}

// show writes events as JSON, one a line, for a comparison and a message.
func show(events []Event) string {
	var b strings.Builder
	for _, ev := range events {
		line, _ := json.Marshal(ev)
		b.Write(line)
		b.WriteByte('\n')
	}

	return b.String()
}
