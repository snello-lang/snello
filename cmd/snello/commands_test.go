// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"cmp"
	"flag"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/version"
)

// TestHelp holds the help to every command and its line, printed on
// stdout for help, -h, -help and --help, and on stderr, as a usage
// error, for a command line with no command.
func TestHelp(t *testing.T) {
	for _, args := range [][]string{{helpCommand}, {shortHelpArg}, {helpArg}, {longHelpArg}} {
		var out, errs strings.Builder
		if code := run(args, &out, &errs); code != exitSuccess || errs.Len() != 0 {
			t.Errorf("%v exited %d and said %q on stderr", args, code, errs.String())
		}

		for _, c := range commands() {
			if !strings.Contains(out.String(), c.name) || !strings.Contains(out.String(), c.short) {
				t.Errorf("%v does not list %s: %s", args, c.name, c.short)
			}
		}
	}

	var out, errs strings.Builder
	if code := run(nil, &out, &errs); code != exitUsage || out.Len() != 0 || !strings.Contains(errs.String(), "The commands are:") {
		t.Errorf("no command exited %d, printed %q and said %q", code, out.String(), errs.String())
	}
}

// TestHelpCommand holds each command's help to its usage line and to
// every flag its arguments are parsed with, read from the same
// declaration, and help, -h after the command, to the same text.
func TestHelpCommand(t *testing.T) {
	for _, c := range commands() {
		var out, errs strings.Builder
		if code := run([]string{helpCommand, c.name}, &out, &errs); code != exitSuccess || errs.Len() != 0 {
			t.Errorf("help %s exited %d and said %q", c.name, code, errs.String())
		}

		help := out.String()
		if !strings.HasPrefix(help, "usage: snello "+c.name) || !strings.Contains(help, c.short) {
			t.Errorf("help %s: %q", c.name, help)
		}

		fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
		c.declare(fs, io.Discard, io.Discard)
		fs.VisitAll(func(f *flag.Flag) {
			if !strings.Contains(help, "-"+f.Name) || !strings.Contains(help, firstLine(f.Usage)) {
				t.Errorf("help %s does not list -%s", c.name, f.Name)
			}
		})

		var dashH strings.Builder
		if code := run([]string{c.name, shortHelpArg}, &dashH, io.Discard); code != exitSuccess || dashH.String() != help {
			t.Errorf("%s -h exited %d and printed %q, want the help", c.name, code, dashH.String())
		}
	}
}

// TestCommandErrors holds the messages of a command line given wrongly:
// an unknown command, -version among them, with what to run for help;
// an unknown command after help; a flag the command does not have, a
// wrong count of arguments and an unknown backend, each with the
// command's usage line.
func TestCommandErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"nosuch"}, "snello: unknown command \"nosuch\"; run snello help\n"},
		{[]string{"-version"}, "snello: unknown command \"-version\"; run snello help\n"},
		{[]string{helpCommand, "nosuch"}, "snello help: unknown command \"nosuch\"; run snello help\n"},
		{[]string{helpCommand, "a", "b"}, "usage: snello help [COMMAND]\nrun snello help help for its help\n"},
		{[]string{buildCommand}, "usage: snello build [flags] FILE|DIR\nrun snello help build for its help\n"},
		{[]string{buildCommand, "-x", "a.snello"}, "flag provided but not defined: -x\nusage: snello build [flags] FILE|DIR\n"},
		{[]string{buildCommand, "-backend", "z", "a.snello"}, "snello build: unknown backend \"z\": llvm or c\n"},
		{[]string{testCommand, "-backend", "z", "dir"}, "snello test: unknown backend \"z\": llvm or c\n"},
		{[]string{versionCommand, "x"}, "usage: snello version\n"},
		{[]string{lexCommand}, "usage: snello lex FILE\n"},
	}
	for _, tt := range tests {
		var out, errs strings.Builder
		if code := run(tt.args, &out, &errs); code != exitUsage || out.Len() != 0 || !strings.HasPrefix(errs.String(), tt.want) {
			t.Errorf("%v exited %d, printed %q and said %q, want %q", tt.args, code, out.String(), errs.String(), tt.want)
		}
	}
}

// TestVersionCommand holds version to the compiler's line.
func TestVersionCommand(t *testing.T) {
	var out strings.Builder
	if code := run([]string{versionCommand}, &out, io.Discard); code != exitSuccess || out.String() != version.Describe()+"\n" {
		t.Errorf("version exited %d and printed %q", code, out.String())
	}
}

// TestVersionStamp builds the command as a release does, with its
// channel stamped in by the linker, and as a build from source does,
// with none, and reads the version line of each back: the release's
// channel, or devel.
func TestVersionStamp(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the command twice")
	}

	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go on the path to build the command with")
	}

	dir := t.TempDir()
	for channel, want := range map[string]string{"stable": " stable ", "": " devel "} {
		bin := filepath.Join(dir, "snello-"+cmp.Or(channel, "devel"))
		args := []string{"build", "-o", bin}
		if channel != "" {
			args = append(args, "-ldflags", "-X github.com/snello-lang/snello/compiler/version.channel="+channel)
		}

		if out, err := exec.Command(goTool, append(args, ".")...).CombinedOutput(); err != nil {
			t.Fatalf("building with channel %q: %v\n%s", channel, err, out)
		}

		out, err := exec.Command(bin, versionCommand).Output()
		if line := string(out); err != nil || !strings.HasPrefix(line, "snello "+version.Current+" ") || !strings.Contains(line, want) {
			t.Errorf("channel %q: version printed %q, %v, want %q in it", channel, line, err, want)
		}
	}
}

// firstLine returns the first line of s, the part of a flag's usage the
// help prints on one line.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.ReplaceAll(line, "`", "")
}

// TestProgramFlags holds the flags test passes on to the program it
// builds: each flag given, in the order of the lists, -count only when
// not the default, and -timeout in whole seconds, rounded up.
func TestProgramFlags(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"-v", "-short"}, "-v -short"},
		{[]string{"-short", "-v", "-failfast"}, "-v -failfast -short"},
		{[]string{"-run", "Area", "-bench", "."}, "-run Area -bench ."},
		{[]string{"-count", "1"}, ""},
		{[]string{"-count", "3"}, "-count 3"},
		{[]string{"-timeout", "1500ms"}, "-timeout 2"},
		{[]string{"-timeout", "30s", "-update"}, "-update -timeout 30"},
	}
	for _, tt := range tests {
		fs := flag.NewFlagSet(testCommand, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		o := declareTestOptions(fs)
		if err := fs.Parse(tt.args); err != nil {
			t.Fatalf("%v: %v", tt.args, err)
		}

		if got := strings.Join(o.programFlags(), " "); got != tt.want {
			t.Errorf("%v passes %q, want %q", tt.args, got, tt.want)
		}
	}
}
