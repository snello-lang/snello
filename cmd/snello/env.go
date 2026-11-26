// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/toolchain"
	"github.com/snello-lang/snello/compiler/version"
)

// The settings env prints, by name: the ones that are not a variable
// a build reads, the temporary directory's on each system, and how
// the settings are laid out and described.
const (
	envArgs         = "[NAME]"
	versionSetting  = "VERSION"
	targetSetting   = "TARGET"
	moduleSetting   = "MODULE"
	tmpSetting      = "TMPDIR"
	tmpWindows      = "TMP"
	tempWindows     = "TEMP"
	notSet          = "not set"
	noModule        = "none"
	thisBinary      = "this binary"
	thisMachine     = "this machine; -target names another"
	onPath          = "on the path"
	namesIt         = " names it"
	notFoundNote    = "; not found: run snello doctor"
	systemCache     = "the system's cache directory"
	noCacheDir      = "no cache directory the system names: nothing is kept"
	besideCache     = "beside the runtime's cache"
	systemTemp      = "the system's"
	noModuleNote    = "no snello.mod here or above: the standard library alone"
	sysrootUnset    = "a build for %s reads it when set"
	columnPad       = 2
	jsonIndent      = "  "
	unknownSetting  = "snello env: no setting %s; the settings are %s\n"
	settingSep      = ", "
	moduleLineStart = "snello "
)

// A setting is one thing a build reads: its name, the value a build
// here would use, and where that value came from, a variable, the
// path, or the system's choice.
type setting struct {
	Name, Value, Source string
}

// settings returns every setting a build run in dir reads, in the order
// env prints them: the version, the target, the compilers, the caches,
// the sysroots of the targets that take one, the temporary directory,
// and the module.
func settings(dir string) []setting {
	out := []setting{
		{versionSetting, version.Describe(), thisBinary},
		{targetSetting, platform.Host().String(), thisMachine},
		compilerSetting(toolchain.ClangEnv, toolchain.Clang()),
		compilerSetting(toolchain.CCEnv, toolchain.CC()),
		cacheSettingOf(),
		modCacheSetting(),
	}
	for _, t := range platform.Targets() {
		if t.OS == platform.Darwin { // builds its programs against the SDK the system's clang finds
			continue
		}

		s := setting{t.SysrootVariable(), t.Sysroot(), t.SysrootVariable()}
		if s.Value == "" {
			s.Value, s.Source = notSet, fmt.Sprintf(sysrootUnset, t)
		}

		out = append(out, s)
	}

	return append(out, tempSetting(), moduleSettingOf(dir))
}

// compilerSetting returns a compiler as a build finds it: the variable
// that names it, or its name on the path, resolved to the file that
// would run, or noted as not found.
func compilerSetting(env, name string) setting {
	source := onPath
	if os.Getenv(env) != "" {
		source = env + namesIt
	}

	path, err := exec.LookPath(name)
	if err != nil {
		return setting{env, name, source + notFoundNote}
	}

	return setting{env, path, source}
}

// cacheSettingOf returns the runtime's cache: the directory, or off,
// by the variable or because the system names no cache directory.
func cacheSettingOf() setting {
	dir, ok := c.CacheDir()
	switch {
	case os.Getenv(c.CacheEnv) != "" && !ok:
		return setting{c.CacheEnv, c.CacheOff, c.CacheEnv}
	case os.Getenv(c.CacheEnv) != "":
		return setting{c.CacheEnv, dir, c.CacheEnv}
	case !ok:
		return setting{c.CacheEnv, c.CacheOff, noCacheDir}
	}

	return setting{c.CacheEnv, dir, systemCache}
}

// modCacheSetting returns the module cache's directory.
func modCacheSetting() setting {
	source := besideCache
	if os.Getenv(importer.ModCacheEnv) != "" {
		source = importer.ModCacheEnv
	}

	dir, err := importer.ModCacheDir()
	if err != nil {
		return setting{importer.ModCacheEnv, notSet, err.Error()}
	}

	return setting{importer.ModCacheEnv, dir, source}
}

// tempSetting returns the directory builds work in, under the name of
// the variable the system reads for it.
func tempSetting() setting {
	name, source := tmpSetting, systemTemp
	if runtime.GOOS == windows {
		name = tmpWindows
		if os.Getenv(tmpWindows) == "" && os.Getenv(tempWindows) != "" {
			name = tempWindows
		}
	}

	if os.Getenv(name) != "" {
		source = name
	}

	return setting{name, filepath.Clean(os.TempDir()), source}
}

// moduleSettingOf returns the module a program in dir belongs to: its
// file, with the version line the file has, or why the file does not
// read, or none.
func moduleSettingOf(dir string) setting {
	m, err := importer.FindModule(dir)
	switch {
	case err != nil:
		return setting{moduleSetting, nearestModFile(dir), err.Error()}
	case m == nil:
		return setting{moduleSetting, noModule, noModuleNote}
	case m.Version == "":
		return setting{moduleSetting, filepath.Join(m.Dir, importer.ModFile), "no snello line: read as " + moduleLineStart + version.First}
	}

	return setting{moduleSetting, filepath.Join(m.Dir, importer.ModFile), moduleLineStart + m.Version}
}

// nearestModFile returns the path of the nearest module file in dir or
// above it, which FindModule read and refused.
func nearestModFile(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return importer.ModFile
	}

	for {
		name := filepath.Join(dir, importer.ModFile)
		if _, err := os.Stat(name); err == nil {
			return name
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return importer.ModFile
		}

		dir = parent
	}
}

// bare returns a setting's value as a script reads it: empty for one
// that is not set, where the table says so in words.
func bare(s setting) string {
	if s.Value == notSet {
		return ""
	}

	return s.Value
}

// env prints the settings on w: every one in columns, name, value and
// source; one's value alone when name is given, empty for one that is
// not set; or every one, or the one named, as JSON, name to value,
// when asJSON is set. An unknown name is a usage error, with the names
// listed on stderr.
func env(name string, asJSON bool, stdout, stderr io.Writer) int {
	all := settings(".")
	if name != "" {
		found := false
		for _, s := range all {
			if s.Name == name {
				all, found = []setting{s}, true
			}
		}

		if !found {
			names := make([]string, 0, len(all))
			for _, s := range all {
				names = append(names, s.Name)
			}

			fmt.Fprintf(stderr, unknownSetting, name, strings.Join(names, settingSep))
			return exitUsage
		}

		if !asJSON {
			fmt.Fprintln(stdout, bare(all[0]))
			return exitSuccess
		}
	}

	if asJSON {
		values := make(map[string]string, len(all))
		for _, s := range all {
			values[s.Name] = bare(s)
		}

		out, err := json.MarshalIndent(values, "", jsonIndent)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitFailure
		}

		fmt.Fprintln(stdout, string(out))
		return exitSuccess
	}

	w := tabwriter.NewWriter(stdout, 0, 0, columnPad, ' ', 0)
	for _, s := range all {
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, s.Value, s.Source)
	}

	w.Flush()
	return exitSuccess
}

// declareEnv declares env: -json, and a name for one setting's value.
func declareEnv(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	asJSON := fs.Bool(jsonFlag, false, "print the settings, or the one named, as JSON, name to value")
	return func(args []string) int {
		name := ""
		if len(args) == 1 {
			name = args[0]
		}

		return env(name, *asJSON, stdout, stderr)
	}
}
