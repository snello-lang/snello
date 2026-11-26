// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/snello-lang/snello/compiler/importer"
)

// framing begins each line std/testing writes itself when the program
// runs with jsonFlag: its report of the tests and of the run. A line
// without it is what a test printed or logged.
const framing = '\x16'

// jsonFlag is the flag the test program is run with for its output to
// be converted to events.
const jsonFlag = "-json"

// failureIndent sets a failure's lines under the result line they
// belong to, as std/testing writes them.
const failureIndent = "    "

// failPackage begins the package's last line when a test failed, and
// benchmarkTag a benchmark's name, which its result line begins with.
const (
	failPackage  = "FAIL\t"
	benchmarkTag = "Benchmark"
)

// The actions of an event.
const (
	ActionStart   = "start"   // the package's run begins
	ActionRun     = "run"     // a test, a subtest or an example begins
	ActionPass    = "pass"    // a test, or the package, passed
	ActionFail    = "fail"    // a test, or the package, failed
	ActionSkip    = "skip"    // a test was skipped
	ActionOutput  = "output"  // a line the run printed, a test's or the package's
	ActionBench   = "bench"   // a benchmark's result line
	ActionFailure = "failure" // one failed check of a test, with where it is and what it compared
)

var (
	// resultLine is a test's result: its verdict, its name and how long
	// it took, which a failed benchmark's line leaves out.
	resultLine = regexp.MustCompile(`^--- (PASS|FAIL|SKIP): (\S+)(?: \(([0-9.]+)s\))?$`)

	// runLine is a test's start.
	runLine = regexp.MustCompile(`^=== RUN +(\S+)$`)

	// packageLine is the package's last line: its verdict, its name and
	// how long the run took.
	packageLine = regexp.MustCompile(`^(?:ok  |FAIL)\t(\S+)\t([0-9.]+)s$`)

	// located is a failure's first line when the check names where it
	// is: the file, the line, and the check as written.
	located = regexp.MustCompile(`^(\S+\.snello):([0-9]+)(?:: ?(.*))?$`)

	// frameLine is a line of a panic's trace: the function, and the
	// file and line it was at.
	frameLine = regexp.MustCompile(`^\S+ at (\S+\.snello):([0-9]+)$`)

	// valueLine is a compared value's label and, on the label's line,
	// the value; typeNote is what a value may end with, its type or its
	// length, which is no part of the value.
	valueLine = regexp.MustCompile(`^\s*(got|want):(?: +(.*))?$`)
	typeNote  = regexp.MustCompile(` {3}\(.*\)$`)
)

// An Event is one line of snello test -json: a test's start or result,
// a line the run printed, a benchmark's result, or a failed check. The
// fields up to Output are those every tool that reads a test run as
// JSON reads; a failure adds where the failed check is, its message,
// and the two values it compared when it printed them.
type Event struct {
	Time    *time.Time `json:",omitempty"`
	Action  string
	Package string  `json:",omitempty"`
	Test    string  `json:",omitempty"`
	Elapsed float64 `json:",omitempty"`
	Output  string  `json:",omitempty"`
	File    string  `json:",omitempty"`
	Line    int     `json:",omitempty"`
	Message string  `json:",omitempty"`
	Got     *string `json:",omitempty"`
	Want    *string `json:",omitempty"`
}

// RunJSON builds the tests of the package in dir through the backend,
// in work, and runs the program with args, writing what happens to w as events,
// one JSON object a line, and returns the program's exit code. A
// package that cannot be built is an event of its own: its error as the
// package's output, and the package's failure.
func RunJSON(dir, backend string, args []string, w io.Writer, work Work) int {
	j := newConverter(w, mainLabel(dir), dir)
	p, err := PlanDir(dir)
	if err == nil {
		j.pkg, j.dir = p.label(), p.Dir // every event names the package as its results do
	}

	j.event(Event{Action: ActionStart})
	if warn := importer.ModuleWarning(dir); warn != "" {
		j.printed(warn)
	}

	if err == nil {
		var code int
		code, err = p.run(dir, backend, append([]string{jsonFlag}, args...), j, j, work)
		if err == nil {
			j.finish(code)
			return code
		}
	}

	j.printed(err.Error())
	j.finish(1)
	return 1
}

// A converter turns the output of a test program run with jsonFlag into
// events as it is written, a line at a time.
type converter struct {
	enc     *json.Encoder
	pkg     string // the package's name in the events
	dir     string // the package's directory, which a failure's file is in
	now     func() time.Time
	start   time.Time // when the run began, for the package's time when the program wrote none
	partial []byte    // the start of a line not yet ended
	running []string  // the tests begun and not ended, the innermost last
	last    string    // the test whose result was read last, which what follows it is of until another line of the runner's
	done    bool      // the package's own line was read

	failed  string   // the test whose failure's lines are being read, when its result was a failure
	elapsed float64  // that test's time, given with its result once its lines are read
	lines   []string // the failure's lines, set under its result
}

func newConverter(w io.Writer, pkg, dir string) *converter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // a test's output as it was printed, < and & included
	return &converter{enc: enc, pkg: pkg, dir: dir, now: time.Now, start: time.Now()}
}

// Write takes the program's output, its standard output and its
// standard error together, and converts each line it ends.
func (j *converter) Write(p []byte) (int, error) {
	j.partial = append(j.partial, p...)
	for {
		i := bytes.IndexByte(j.partial, '\n')
		if i < 0 {
			break
		}

		j.line(string(j.partial[:i]))
		j.partial = j.partial[i+1:]
	}

	return len(p), nil
}

// finish ends the events once the program has exited with code: a line
// it left unended, a failure being read, each test begun and never
// ended, which the program's end failed, and the package's own result
// when the program wrote none.
func (j *converter) finish(code int) {
	if len(j.partial) > 0 {
		j.line(string(j.partial))
		j.partial = nil
	}

	j.endFailure()
	for i := len(j.running) - 1; i >= 0; i-- {
		j.event(Event{Action: ActionFail, Test: j.running[i]})
	}

	j.running = nil
	if !j.done {
		action := ActionPass
		if code != 0 {
			action = ActionFail
		}

		j.event(Event{Action: action, Elapsed: j.now().Sub(j.start).Seconds()})
	}
}

// line converts one line of the program's output.
func (j *converter) line(text string) {
	text = strings.TrimSuffix(text, "\r")
	if i := strings.IndexByte(text, framing); i > 0 { // a test's output left unended, then a line of the runner's
		j.line(text[:i])
		j.line(text[i:])
		return
	}

	own := len(text) > 0 && text[0] == framing
	if !own && j.failed != "" { // what the failed test logged, between its result and its failure
		j.event(Event{Action: ActionOutput, Test: j.failed, Output: text + "\n"})
		return
	}

	if !own && j.last != "" { // what a passed or skipped test logged, after its result
		j.event(Event{Action: ActionOutput, Test: j.last, Output: text + "\n"})
		return
	}

	if !own {
		j.printed(text)
		return
	}

	text = text[1:]
	if j.failed != "" && strings.HasPrefix(text, failureIndent) {
		j.event(Event{Action: ActionOutput, Test: j.failed, Output: text + "\n"})
		j.lines = append(j.lines, strings.TrimPrefix(text, failureIndent))
		return
	}

	if j.last != "" && strings.HasPrefix(text, failureIndent) { // a skip's reason, under its result
		j.event(Event{Action: ActionOutput, Test: j.last, Output: text + "\n"})
		return
	}

	j.endFailure()
	j.last = ""
	if m := runLine.FindStringSubmatch(text); m != nil {
		j.running = append(j.running, m[1])
		j.event(Event{Action: ActionRun, Test: m[1]})
		j.event(Event{Action: ActionOutput, Test: m[1], Output: text + "\n"})
		return
	}

	if m := resultLine.FindStringSubmatch(text); m != nil {
		name := m[2]
		elapsed, _ := strconv.ParseFloat(m[3], 64)
		j.event(Event{Action: ActionOutput, Test: name, Output: text + "\n"})
		j.ended(name)
		switch m[1] {
		case "PASS":
			j.event(Event{Action: ActionPass, Test: name, Elapsed: elapsed})
			j.last = name
		case "SKIP":
			j.event(Event{Action: ActionSkip, Test: name, Elapsed: elapsed})
			j.last = name
		default:
			j.failed, j.elapsed = name, elapsed // reported once its lines are read
		}

		return
	}

	if m := packageLine.FindStringSubmatch(text); m != nil {
		elapsed, _ := strconv.ParseFloat(m[2], 64)
		j.event(Event{Action: ActionOutput, Output: text + "\n"})
		action := ActionPass
		if strings.HasPrefix(text, failPackage) {
			action = ActionFail
		}

		j.event(Event{Action: action, Elapsed: elapsed})
		j.done = true
		return
	}

	if name, _, _ := strings.Cut(text, " "); strings.HasPrefix(name, benchmarkTag) {
		j.event(Event{Action: ActionBench, Test: name, Output: text + "\n"})
		return
	}

	j.event(Event{Action: ActionOutput, Output: text + "\n"}) // a verdict, or a line of the run's own
}

// printed reports text, each of its lines, as output of the innermost
// test running, or of the package when none is.
func (j *converter) printed(text string) {
	test := ""
	if len(j.running) > 0 {
		test = j.running[len(j.running)-1]
	}

	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		j.event(Event{Action: ActionOutput, Test: test, Output: line + "\n"})
	}
}

// ended takes the test named name off the tests running.
func (j *converter) ended(name string) {
	for i := len(j.running) - 1; i >= 0; i-- {
		if j.running[i] == name {
			j.running = append(j.running[:i], j.running[i+1:]...)
			return
		}
	}
}

// endFailure reports the failed test whose lines were being read: its
// failure, when it wrote one, then its result.
func (j *converter) endFailure() {
	if j.failed == "" {
		return
	}

	if len(j.lines) > 0 {
		ev := j.failure(j.lines)
		ev.Test = j.failed
		j.event(ev)
	}

	j.event(Event{Action: ActionFail, Test: j.failed, Elapsed: j.elapsed})
	j.failed, j.elapsed, j.lines = "", 0, j.lines[:0]
}

// failure reads a failure's lines, set under its result: where the
// failed check is, the values it compared, each on its label's line or
// on the lines below it, and the message, the rest of the lines. The
// check is where its first line says; a panic's, the innermost frame of
// its trace in the package's own files.
func (j *converter) failure(lines []string) Event {
	ev := Event{Action: ActionFailure}
	var message []string
	for i := 0; i < len(lines); i++ {
		if i == 0 {
			if m := located.FindStringSubmatch(lines[0]); m != nil {
				ev.File, ev.Line = j.fileOf(m[1]), atoi(m[2])
				message = append(message, m[3])
				continue
			}
		}

		if m := frameLine.FindStringSubmatch(strings.TrimSpace(lines[i])); m != nil && ev.File == "" && j.inPackage(m[1]) {
			ev.File, ev.Line = j.fileOf(m[1]), atoi(m[2])
		}

		m := valueLine.FindStringSubmatch(lines[i])
		if m == nil {
			message = append(message, strings.TrimSpace(lines[i]))
			continue
		}

		value := typeNote.ReplaceAllString(m[2], "")
		if m[2] == "" { // the value on the lines below its label, set deeper than it
			depth := indentOf(lines[i]) + len(failureIndent)
			var below []string
			for i+1 < len(lines) && valueLine.FindStringSubmatch(lines[i+1]) == nil && indentOf(lines[i+1]) >= depth {
				i++
				below = append(below, lines[i][depth:])
			}

			value = strings.Join(below, "\n")
		}

		if m[1] == "got" {
			ev.Got = &value
		} else {
			ev.Want = &value
		}
	}

	ev.Message = strings.TrimSpace(strings.Join(message, "\n"))
	return ev
}

// fileOf is the path of the package's file name, absolute when the
// package is in a directory.
func (j *converter) fileOf(name string) string {
	if j.dir == "" || filepath.IsAbs(name) {
		return name
	}

	if abs, err := filepath.Abs(filepath.Join(j.dir, name)); err == nil {
		return abs
	}

	return filepath.Join(j.dir, name)
}

// inPackage reports whether name is one of the package's own files, in
// its directory.
func (j *converter) inPackage(name string) bool {
	if j.dir == "" {
		return false
	}

	info, err := os.Stat(j.fileOf(name))
	return err == nil && !info.IsDir()
}

// atoi reads a line number the patterns matched, digits alone.
func atoi(digits string) int {
	n, _ := strconv.Atoi(digits)
	return n
}

// indentOf is how many spaces line begins with.
func indentOf(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

// event writes ev as the package's, at the time it happens.
func (j *converter) event(ev Event) {
	t := j.now()
	ev.Time, ev.Package = &t, j.pkg
	if err := j.enc.Encode(ev); err != nil {
		fmt.Fprintln(os.Stderr, "snello test:", err)
	}
}
