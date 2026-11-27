// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/testrun"
	"github.com/snello-lang/snello/compiler/version"
)

const (
	// The commands, the first argument of every command line.
	buildCommand   = "build"
	checkCommand   = "check"
	debugCommand   = "debug"
	docCommand     = "doc"
	doctorCommand  = "doctor"
	emitCommand    = "emit"
	envCommand     = "env"
	helpCommand    = "help"
	initCommand    = "init"
	lexCommand     = "lex"
	lspCommand     = "lsp"
	newCommand     = "new"
	parseCommand   = "parse"
	runCommand     = "run"
	testCommand    = "test"
	versionCommand = "version"

	// The first arguments that ask for the help, as the flag package
	// takes them: -h, -help and --help.
	shortHelpArg = "-h"
	helpArg      = "-help"
	longHelpArg  = "--help"

	// The exit codes: a command that did its work, one that failed,
	// and one given wrongly, as the flag package exits.
	exitSuccess = 0
	exitFailure = 1
	exitUsage   = 2

	// The flags: the backend, a build for a debugger, those test passes
	// on to the program it builds, each written after flagStart, and
	// the JSON env and test print; a test program runs each test once
	// unless -count says otherwise.
	backendFlag  = "backend"
	debugFlag    = "debug"
	targetFlag   = "target"
	verboseFlag  = "v"
	failfastFlag = "failfast"
	shuffleFlag  = "shuffle"
	shortFlag    = "short"
	updateFlag   = "update"
	runFlag      = "run"
	benchFlag    = "bench"
	countFlag    = "count"
	timeoutFlag  = "timeout"
	jsonFlag     = "json"
	outFlag      = "o"
	compileFlag  = "c"
	setupFlag    = "setup"
	stdioFlag    = "stdio"
	clientFlag   = "clientProcessId"
	flagStart    = "-"
	defaultCount = 1

	// What a command takes after its flags, as the help writes it, and
	// flagsArg, written before it when the command has flags; anyArgs
	// is a command's most arguments when it takes any number.
	pathArg     = "FILE|DIR"
	fileArg     = "FILE"
	dirArg      = "DIR"
	optionalDir = "[DIR]"
	debugArgs   = "FILE|DIR [ARGS...]" // what debug and run take: the program, then its arguments
	commandArg  = "[COMMAND]"
	flagsArg    = "[flags]"
	anyArgs     = -1

	// The columns of the list of commands: the least width, a tab's
	// width, the spaces between columns, what pads them, and the
	// tabwriter's flags, none.
	columnMinWidth = 0
	columnTabWidth = 8
	columnPadding  = 4
	columnPadChar  = ' '
	columnFlags    = 0

	// program is the command's own name, which every message begins
	// with, and wordSeparator parts the words of a usage line.
	program       = "snello"
	wordSeparator = " "

	// backendUsage and debugUsage describe the flags of a build; the
	// word in backquotes is the value's name in the help.
	backendUsage = "the backend, `llvm|c`; c is the reference backend"
	debugUsage   = "build for a debugger, unoptimized, with the runtime's checks"

	// testProgramSuffix ends the name of the program test -c builds.
	testProgramSuffix = ".test"
	targetUsage       = "the platform to build for, `SYSTEM/PROCESSOR`, linux/arm64; the host's when not given"
)

// A testFlag is a flag test passes on to the program it builds, as it
// was given, with what the help says of it.
type testFlag struct {
	name, usage string
}

// testOptions are the flags of test that go to the program it builds,
// where they are parsed to.
type testOptions struct {
	bools   map[string]*bool
	strs    map[string]*string
	count   *int
	timeout *time.Duration
}

// testBoolFlags and testStringFlags are the flags test passes on to the
// program, in the order it passes them; -count and -timeout, which test
// writes out itself, are declared on their own.
var (
	testBoolFlags = []testFlag{
		{verboseFlag, "print every test's lines, not only a failed test's"},
		{failfastFlag, "stop at the first test that fails"},
		{shuffleFlag, "run the tests in a random order, printing its seed"},
		{shortFlag, "tell the tests to skip their long parts, as testing.Short reports"},
		{updateFlag, "tell expect.Golden to write its files, not compare with them"},
	}
	testStringFlags = []testFlag{
		{runFlag, "run only the tests whose name holds `NAME`"},
		{benchFlag, "run the benchmarks whose name holds `NAME`, . for all"},
	}
)

// A command is one of snello's commands: its name, what it takes after
// its flags and how many arguments that is, a line saying what it does,
// and declare, which declares its flags on the set the command's
// arguments are parsed with and its help lists, so that the two never
// differ, and returns the work over the arguments the flags leave.
type command struct {
	name             string
	args             string
	minArgs, maxArgs int
	short            string
	declare          func(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int
}

// run runs the command line args, the program's name left out, and
// returns the exit code: the command's, or exitUsage for no command, an
// unknown one, or one given wrongly, with what to run for help. The
// help asked for with -h, -help or --help, as the command or after it,
// is printed on stdout. The driver's own messages, the help, and what
// help, version, init and test print go to stdout and stderr; the
// commands that compile, lex, parse, check, build, emit and debug,
// write to the process's own streams, as the compiler's errors always
// have.
func run(args []string, stdout, stderr io.Writer) int {
	return runWith(args, stdout, stderr, exec.Command)
}

// runWith is run, the programs that run and test build, and the
// debugger debug starts, started through start: exec.Command for the
// command, and for a test the command that ends them with the test
// binary (internal/testproc).
func runWith(args []string, stdout, stderr io.Writer, start testrun.Command) int {
	if len(args) == 0 {
		printCommands(stderr)
		return exitUsage
	}

	if isHelpArg(args[0]) {
		printCommands(stdout)
		return exitSuccess
	}

	c := lookupIn(commandsFor(start), args[0])
	if c == nil {
		fmt.Fprintf(stderr, "%s: unknown command %q; run %s %s\n", program, args[0], program, helpCommand)
		return exitUsage
	}

	fs := newFlagSet(c, stderr)
	work := c.declare(fs, stdout, stderr)
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommand(stdout, c)
			return exitSuccess
		}

		printUsage(stderr, c)
		return exitUsage
	}

	if fs.NArg() < c.minArgs || c.maxArgs != anyArgs && fs.NArg() > c.maxArgs {
		printUsage(stderr, c)
		return exitUsage
	}

	return work(fs.Args())
}

// commands returns snello's commands in the order the help lists them,
// by name, as the command runs them. It is a function and not a
// variable since help's work looks commands up, which a variable's
// initialization could not refer to. It allocates the list.
func commands() []*command {
	return commandsFor(exec.Command)
}

// commandsFor is commands, run and test starting the program they
// build, and debug its debugger, through start.
func commandsFor(start testrun.Command) []*command {
	return []*command{
		{name: addCommand, args: addArgs, minArgs: 2, maxArgs: 3, short: "require a dependency by version, its latest release unless one is named", declare: declareAdd},
		{name: buildCommand, args: pathArg, minArgs: 1, maxArgs: 1, short: "compile a program, a file or a directory, to a binary", declare: declareCompile(buildCommand, true, build)},
		{name: checkCommand, args: pathArg, minArgs: 1, maxArgs: 1, short: "check a program, or a package, and print its errors", declare: declareCheck},
		{name: debugCommand, args: debugArgs, minArgs: 0, maxArgs: anyArgs, short: "build a program for a debugger and run it under one", declare: declareDebug(start)},
		{name: depsCommand, minArgs: 0, maxArgs: 0, short: "print the dependencies by version, and who requires each", declare: declareDeps},
		{name: docCommand, args: docArgs, minArgs: 0, maxArgs: 1, short: "print a package's documentation, or a name's", declare: declareDoc},
		{name: doctorCommand, minArgs: 0, maxArgs: 0, short: "check what a build needs, and say how to fix what is missing", declare: declareDoctor},
		{name: emitCommand, args: pathArg, minArgs: 1, maxArgs: 1, short: "print the IR, or the C, that build compiles", declare: declareCompile(emitCommand, false, emit)},
		{name: envCommand, args: envArgs, minArgs: 0, maxArgs: 1, short: "print what a build reads here, each with where it comes from", declare: declareEnv},
		{name: fetchCommand, minArgs: 0, maxArgs: 0, short: "fetch every dependency into the module cache, for a build with no network", declare: declareFetch},
		{name: helpCommand, args: commandArg, minArgs: 0, maxArgs: 1, short: "print the commands, or a command's help", declare: declareHelp},
		{name: initCommand, args: optionalDir, minArgs: 0, maxArgs: 1, short: "start a module, writing its snello.mod", declare: declareInit},
		{name: lexCommand, args: fileArg, minArgs: 1, maxArgs: 1, short: "print the tokens of a source file", declare: declareLex},
		{name: lspCommand, minArgs: 0, maxArgs: 0, short: "serve an editor the Language Server Protocol on standard input and output", declare: declareLSP},
		{name: newCommand, args: dirArg, minArgs: 1, maxArgs: 1, short: "start a module in a new directory, with a program and its test", declare: declareNew},
		{name: parseCommand, args: fileArg, minArgs: 1, maxArgs: 1, short: "print the syntax tree of a source file, with its comments", declare: declareParse},
		{name: removeCommand, args: removeArgs, minArgs: 1, maxArgs: anyArgs, short: "take dependencies out of snello.mod, once nothing imports them", declare: declareRemove},
		{name: runCommand, args: debugArgs, minArgs: 1, maxArgs: anyArgs, short: "build a program, run it with ARGS, and leave no file", declare: declareRun(start)},
		{name: testCommand, args: dirArg, minArgs: 1, maxArgs: 1, short: "build and run the tests of a package", declare: declareTest(start)},
		{name: updateCommand, args: updateArgs, minArgs: 0, maxArgs: anyArgs, short: "raise dependencies to their latest compatible releases", declare: declareUpdate},
		{name: versionCommand, minArgs: 0, maxArgs: 0, short: "print the version, commit, channel and platform", declare: declareVersion},
	}
}

// lookup returns the command named name, or nil. It builds the table
// of commands.
func lookup(name string) *command {
	return lookupIn(commands(), name)
}

// lookupIn returns the command of cs named name, or nil.
func lookupIn(cs []*command, name string) *command {
	for _, c := range cs {
		if c.name == name {
			return c
		}
	}

	return nil
}

// isHelpArg reports whether arg asks for the help: -h, -help or --help.
func isHelpArg(arg string) bool {
	return arg == shortHelpArg || arg == helpArg || arg == longHelpArg
}

// newFlagSet returns the set the flags of c are declared on, whose
// errors are written to stderr; the help and the usage line are run's
// to print.
func newFlagSet(c *command, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {}
	return fs
}

// printCommands writes the help: what snello is, how a command line is
// written, and every command with its line.
func printCommands(w io.Writer) {
	fmt.Fprintf(w, "%s builds, checks and tests Snello programs.\n\nUsage:\n\n\t%s COMMAND [ARGUMENTS]\n\nThe commands are:\n\n", program, program)
	tw := tabwriter.NewWriter(w, columnMinWidth, columnTabWidth, columnPadding, columnPadChar, columnFlags)
	for _, c := range commands() {
		fmt.Fprintf(tw, "\t%s\t%s\n", c.name, c.short)
	}

	tw.Flush()
	fmt.Fprintf(w, "\nRun %s %s COMMAND for a command's flags and arguments.\n", program, helpCommand)
}

// printCommand writes the help of c: its usage line, what it does, and
// its flags, from the set its arguments are parsed with.
func printCommand(w io.Writer, c *command) {
	fs := declared(c, w)
	fmt.Fprintf(w, "usage: %s\n\n%s: %s.\n", usageLine(c, fs), c.name, c.short)
	if hasFlags(fs) {
		fmt.Fprintf(w, "\nFlags:\n")
		fs.PrintDefaults()
	}
}

// printUsage writes the usage line of c and what to run for its help,
// for a command given wrongly.
func printUsage(w io.Writer, c *command) {
	fmt.Fprintf(w, "usage: %s\nrun %s %s %s for its help\n", usageLine(c, declared(c, w)), program, helpCommand, c.name)
}

// declared returns a set with the flags of c declared on it, for its
// help, whose messages go to w.
func declared(c *command, w io.Writer) *flag.FlagSet {
	fs := newFlagSet(c, w)
	c.declare(fs, w, w)
	return fs
}

// usageLine returns how a command line of c is written: the command,
// flagsArg when it has flags, and what it takes after them.
func usageLine(c *command, fs *flag.FlagSet) string {
	line := program + wordSeparator + c.name
	if hasFlags(fs) {
		line += wordSeparator + flagsArg
	}

	if c.args != "" {
		line += wordSeparator + c.args
	}

	return line
}

// hasFlags reports whether any flag is declared on fs.
func hasFlags(fs *flag.FlagSet) bool {
	has := false
	fs.VisitAll(func(*flag.Flag) { has = true })
	return has
}

// checkBackend reports whether backend names a backend, and says on
// stderr what the command named does when it does not.
func checkBackend(name, backend string, stderr io.Writer) bool {
	if isBackend(backend) {
		return true
	}

	fmt.Fprintf(stderr, "%s %s: unknown backend %q: %s or %s\n", program, name, backend, lower.LLVMBackend, lower.CBackend)
	return false
}

// declareCompile declares the flags of build, or of emit, both named
// by name, the backend, a build for a debugger, the target, and where
// the binary goes when output says the command writes one, and the work
// that hands them to work, build's or emit's.
func declareCompile(name string, output bool, work func(path, backend string, target platform.Target, debug bool, out string) int) func(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	return func(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
		backend := declareBackend(fs)
		debug := fs.Bool(debugFlag, false, debugUsage)
		target := declareTarget(fs)
		out := new(string)
		if output {
			out = fs.String(outFlag, "", "write the binary to `FILE`; the program's name in this directory when not given")
		}

		return func(args []string) int {
			if !checkBackend(name, *backend, stderr) {
				return exitUsage
			}

			t, ok := checkTarget(name, *target, stderr)
			if !ok {
				return exitUsage
			}

			return work(args[0], *backend, t, *debug, *out)
		}
	}
}

// declareTarget declares the target flag on fs, which build, emit and
// check take, and returns where it is parsed to: empty for the host.
func declareTarget(fs *flag.FlagSet) *string {
	return fs.String(targetFlag, "", targetUsage)
}

// checkTarget reads the target named, the host's when it is empty,
// and says on stderr what the command named does when it names none
// Snello builds for.
func checkTarget(name, target string, stderr io.Writer) (platform.Target, bool) {
	if target == "" {
		return platform.Host(), true
	}

	t, err := platform.Parse(target)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %v\n", program, name, err)
		return platform.Target{}, false
	}

	return t, true
}

// declareBackend declares the backend flag on fs, which build, emit,
// debug and test take, and returns where it is parsed to.
func declareBackend(fs *flag.FlagSet) *string {
	return fs.String(backendFlag, lower.LLVMBackend, backendUsage)
}

// declareCheck declares the flag of check, the target, whose files it
// checks.
func declareCheck(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	target := declareTarget(fs)
	return func(args []string) int {
		t, ok := checkTarget(checkCommand, *target, stderr)
		if !ok {
			return exitUsage
		}

		return check(args[0], t)
	}
}

// declareDebug declares the flags of debug, the backend and the setup
// an editor's debugger asks for; the arguments after the program's path
// are the program's.
func declareDebug(start testrun.Command) func(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	return func(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
		return declaredDebug(fs, stdout, stderr, start)
	}
}

// declaredDebug declares debug's flags on fs, the debugger started
// through start.
func declaredDebug(fs *flag.FlagSet, stdout, stderr io.Writer, start testrun.Command) func(args []string) int {
	backend := declareBackend(fs)
	setup := fs.String(setupFlag, "", "print the commands the debugger `lldb|gdb` runs first, for an editor that starts it, and write what they read")
	return func(args []string) int {
		if !checkBackend(debugCommand, *backend, stderr) {
			return exitUsage
		}

		if *setup != "" {
			return printSetup(*setup, stdout, stderr)
		}

		if len(args) == 0 {
			fmt.Fprintf(stderr, "%s %s: a program to debug, %s, or -%s\n", program, debugCommand, debugArgs, setupFlag)
			return exitUsage
		}

		return debug(args[0], *backend, args[1:], start)
	}
}

// declareDoctor declares doctor, which has no flags.
func declareDoctor(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	return func(args []string) int { return doctor(self(), stdout) }
}

// declareHelp declares help, which prints the commands, or the help of
// the one named.
func declareHelp(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	return func(args []string) int {
		if len(args) == 0 {
			printCommands(stdout)
			return exitSuccess
		}

		c := lookup(args[0])
		if c == nil {
			fmt.Fprintf(stderr, "%s %s: unknown command %q; run %s %s\n", program, helpCommand, args[0], program, helpCommand)
			return exitUsage
		}

		printCommand(stdout, c)
		return exitSuccess
	}
}

// declareInit declares init, which has no flags.
func declareInit(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	return func(args []string) int {
		dir := currentDir
		if len(args) == 1 {
			dir = args[0]
		}

		return initModule(dir, stderr)
	}
}

// declareLex declares lex, which has no flags.
func declareLex(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	return func(args []string) int { return lex(args[0]) }
}

// declareNew declares new, which has no flags.
func declareNew(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	return func(args []string) int { return newModule(args[0], stderr) }
}

// declareRun declares the flags of run, build's; the arguments after
// the program's path are the program's.
func declareRun(start testrun.Command) func(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	return func(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
		return declaredRun(fs, stdout, stderr, start)
	}
}

// declaredRun declares run's flags on fs, the program started through
// start.
func declaredRun(fs *flag.FlagSet, stdout, stderr io.Writer, start testrun.Command) func(args []string) int {
	backend := declareBackend(fs)
	debug := fs.Bool(debugFlag, false, debugUsage)
	return func(args []string) int {
		if !checkBackend(runCommand, *backend, stderr) {
			return exitUsage
		}

		code, sig := runProgram(args[0], *backend, *debug, args[1:], os.Stdin, stdout, stderr, start)
		if sig != nil {
			raise(sig) // the command ends as the program did, which a shell running a script needs to stop it
		}

		return code
	}
}

// declareParse declares parse, which has no flags.
func declareParse(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	return func(args []string) int { return parse(args[0], stdout) }
}

// declareTest declares the flags of test: the backend, and those it
// passes on to the program it builds, which runs the tests.
func declareTest(start testrun.Command) func(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	return func(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
		return declaredTest(fs, stdout, stderr, start)
	}
}

// declaredTest declares test's flags on fs, the program started through
// start.
func declaredTest(fs *flag.FlagSet, stdout, stderr io.Writer, start testrun.Command) func(args []string) int {
	backend := declareBackend(fs)
	o := declareTestOptions(fs)
	asJSON := fs.Bool(jsonFlag, false, "print the run as events, one JSON object a line, for an editor or a tool")
	compileOnly := fs.Bool(compileFlag, false, "build the tests into a program and do not run it; the program takes the flags above when it runs")
	out := fs.String(outFlag, "", "with -c, write the program to `FILE`; the package's name and .test in this directory when not given")
	debug := fs.Bool(debugFlag, false, "with -c, "+debugUsage)
	return func(args []string) int {
		if !checkBackend(testCommand, *backend, stderr) {
			return exitUsage
		}

		if (*debug || *out != "") && !*compileOnly {
			fmt.Fprintln(stderr, "snello test: -debug and -o are for the program -c builds; a test run is a release build")
			return exitUsage
		}

		if *compileOnly && (*asJSON || len(o.programFlags()) > 0) {
			fmt.Fprintln(stderr, "snello test: -c builds the program and runs nothing; give it -run and the other flags when it runs")
			return exitUsage
		}

		if *compileOnly {
			return buildTests(args[0], *backend, *out, *debug, stderr)
		}

		return runTests(args[0], *backend, o.programFlags(), *asJSON, stdout, stderr, start)
	}
}

// declareTestOptions declares on fs the flags test passes on to the
// program it builds, and returns where they are parsed to.
func declareTestOptions(fs *flag.FlagSet) testOptions {
	o := testOptions{bools: map[string]*bool{}, strs: map[string]*string{}}
	for _, f := range testBoolFlags {
		o.bools[f.name] = fs.Bool(f.name, false, f.usage)
	}

	for _, f := range testStringFlags {
		o.strs[f.name] = fs.String(f.name, "", f.usage)
	}

	o.count = fs.Int(countFlag, defaultCount, "run the tests `N` times")
	o.timeout = fs.Duration(timeoutFlag, 0, "end the tests after `D`, such as 30s; 0 for no limit")
	return o
}

// programFlags returns the flags the program test builds is run with:
// each flag of testBoolFlags given and of testStringFlags given a
// value, in their order; -count when it is not the default; and
// -timeout in whole seconds, rounded up, which is how the program
// counts it. It allocates the list.
func (o testOptions) programFlags() []string {
	var pass []string
	for _, f := range testBoolFlags {
		if *o.bools[f.name] {
			pass = append(pass, flagStart+f.name)
		}
	}

	for _, f := range testStringFlags {
		if *o.strs[f.name] != "" {
			pass = append(pass, flagStart+f.name, *o.strs[f.name])
		}
	}

	if *o.count != defaultCount {
		pass = append(pass, flagStart+countFlag, strconv.Itoa(*o.count))
	}

	if *o.timeout > 0 {
		pass = append(pass, flagStart+timeoutFlag, strconv.Itoa(int((*o.timeout+time.Second-1)/time.Second)))
	}

	return pass
}

// declareVersion declares version, which prints the compiler's line.
func declareVersion(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	return func(args []string) int {
		fmt.Fprintln(stdout, version.Describe())
		return exitSuccess
	}
}
