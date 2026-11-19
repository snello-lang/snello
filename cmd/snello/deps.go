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
	"path/filepath"
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/version"
)

// The commands that change and show the dependencies of the module of
// the current directory, and their arguments and flags.
const (
	addCommand    = "add"
	removeCommand = "remove"
	updateCommand = "update"
	depsCommand   = "deps"
	fetchCommand  = "fetch"

	addArgs    = "NAME URL [VERSION]"
	removeArgs = "NAME..."
	updateArgs = "[NAME...]"

	majorFlag  = "major"
	majorUsage = "raise each dependency to its latest release, across a major version too"
	whyFlag    = "why"
	whyUsage   = "print how the program comes to require the dependency `NAME` or URL"

	// requiredIndent begins a line of deps that says who requires a
	// dependency, and chainArrow parts the steps of a chain -why prints.
	requiredIndent = "    "
	chainArrow     = " -> "

	// urlSchemeEnd ends the scheme of a URL, which tells -why's URL from
	// a name, which never holds it.
	urlSchemeEnd = "://"
)

// declareAdd declares add, which has no flags.
func declareAdd(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	return func(args []string) int {
		v := ""
		if len(args) == 3 {
			v = args[2]
		}

		return addDependency(args[0], args[1], v, stdout, stderr)
	}
}

// declareRemove declares remove, which has no flags.
func declareRemove(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	return func(args []string) int { return removeDependencies(args, stdout, stderr) }
}

// declareUpdate declares update's flag, -major.
func declareUpdate(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	major := fs.Bool(majorFlag, false, majorUsage)
	return func(args []string) int { return updateDependencies(args, *major, stdout, stderr) }
}

// declareDeps declares deps's flag, -why.
func declareDeps(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	why := fs.String(whyFlag, "", whyUsage)
	return func(args []string) int { return listDependencies(*why, stdout, stderr) }
}

// declareFetch declares fetch, which has no flags.
func declareFetch(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	return func(args []string) int { return fetchDependencies(stderr) }
}

// addDependency requires name from the repository at url at v, or at
// the repository's latest release when v is empty, in the module of the
// current directory: the require line is added, or its version changed
// when name is required from url already, and the module fetched,
// selected and locked as a build would, the module file left as it was
// when that fails. The name, the URL and the version are checked before
// the repository is asked anything. With no version, a requirement
// already at the latest release or above it, a pre-release, is kept.
// It says what it did on stdout.
func addDependency(name, url, v string, stdout, stderr io.Writer) int {
	fail := failer(addCommand, stderr)
	m, src, err := moduleHere()
	if err != nil {
		return fail(err)
	}

	if err := m.CheckRequire(name, url, v); err != nil {
		return fail(err)
	}

	before := requiredVersion(m, name, url)
	if v == "" {
		releases, err := importer.ReleaseVersions(url)
		if err != nil {
			return fail(err)
		}

		if len(releases) == 0 {
			return fail(errors.New(url + " has no release tag, vMAJOR.MINOR.PATCH: name a version"))
		}

		v = releases[0]
		if before != "" && compareVersions(v, before) <= 0 {
			v = before
		}
	}

	edited, err := importer.SetRequire(src, name, url, v)
	if err != nil {
		return fail(err)
	}

	if _, err := importer.Rewrite(m.Dir, edited); err != nil {
		return fail(err)
	}

	switch before {
	case "":
		fmt.Fprintln(stdout, "required "+name+" "+url+" "+v)
	case v:
		fmt.Fprintln(stdout, name+" "+url+" "+v+" is required already")
	default:
		fmt.Fprintln(stdout, name+" "+url+" "+before+chainArrow+v)
	}

	return exitSuccess
}

// removeDependencies takes the require lines of names out of the module
// of the current directory, refusing a name its file does not require
// and one a file under the module still imports, named with each
// import, and locks what is left as a build would, the module file left
// as it was when that fails.
func removeDependencies(names []string, stdout, stderr io.Writer) int {
	fail := failer(removeCommand, stderr)
	m, src, err := moduleHere()
	if err != nil {
		return fail(err)
	}

	names = distinct(names)
	edited, err := importer.DropRequire(src, names)
	if err != nil {
		return fail(err)
	}

	for _, name := range names {
		where, err := importer.ImportsOf(m.Dir, name)
		if err != nil {
			return fail(err)
		}

		if len(where) > 0 {
			return fail(errors.New(name + " is still imported, at " + strings.Join(where, ", ") + ": remove the imports first"))
		}
	}

	if _, err := importer.Rewrite(m.Dir, edited); err != nil {
		return fail(err)
	}

	for _, name := range names {
		fmt.Fprintln(stdout, "removed "+name)
	}

	return exitSuccess
}

// updateDependencies raises each dependency by version of the module of
// the current directory, or those names names, to its repository's
// latest release that is compatible with the version required, sharing
// its first number other than zero, or its latest release whatever its
// major when major is set; and locks the module as a build would, the
// module file left as it was when that fails. It says on stdout what it
// raised, and that nothing was when nothing was.
func updateDependencies(names []string, major bool, stdout, stderr io.Writer) int {
	fail := failer(updateCommand, stderr)
	m, src, err := moduleHere()
	if err != nil {
		return fail(err)
	}

	byName := map[string]importer.Require{}
	for _, r := range m.Requires {
		byName[r.Name] = r
	}

	targets := distinct(names)
	if len(targets) == 0 {
		for _, r := range m.Requires {
			if r.URL != "" {
				targets = append(targets, r.Name)
			}
		}
	}

	for _, name := range targets {
		if r, ok := byName[name]; !ok || r.URL == "" {
			return fail(errors.New(name + " is not required by version in " + importer.ModFile))
		}
	}

	var raised []string
	for _, name := range targets {
		r := byName[name]
		releases, err := importer.ReleaseVersions(r.URL)
		if err != nil {
			return fail(err)
		}

		latest := latestFor(r.Version, releases, major)
		if latest == "" {
			continue
		}

		if src, err = importer.SetRequire(src, name, r.URL, latest); err != nil {
			return fail(err)
		}

		raised = append(raised, name+" "+r.URL+" "+r.Version+chainArrow+latest)
	}

	if len(raised) == 0 {
		latest := "its latest compatible release"
		if major {
			latest = "its latest release"
		}

		fmt.Fprintln(stdout, "every dependency is at "+latest)
		return exitSuccess
	}

	if _, err := importer.Rewrite(m.Dir, src); err != nil {
		return fail(err)
	}

	for _, line := range raised {
		fmt.Fprintln(stdout, line)
	}

	return exitSuccess
}

// latestFor returns the release of releases, highest first, a
// dependency required at current is raised to: the highest above it
// that is compatible with it, or the highest above it when major is
// set; empty when none is above it.
func latestFor(current string, releases []string, major bool) string {
	cur, _ := version.Parse(current)
	for _, r := range releases {
		v, _ := version.Parse(r)
		if v.Compare(cur) <= 0 {
			return ""
		}

		if major || v.Compatible(cur) {
			return r
		}
	}

	return ""
}

// listDependencies prints the modules the module of the current
// directory builds against by version, each with its version and,
// indented, who requires which version of it; or, with why set to a
// name the module requires or a repository's URL, the chain of
// requirements from the program to it.
func listDependencies(why string, stdout, stderr io.Writer) int {
	fail := failer(depsCommand, stderr)
	m, _, err := moduleHere()
	if err != nil {
		return fail(err)
	}

	if err := m.Resolve(); err != nil {
		return fail(err)
	}

	if why != "" {
		url := why
		for _, r := range m.Requires {
			if r.Name == why && r.URL != "" {
				url = r.URL
			}
		}

		chain := m.Why(url)
		switch {
		case chain != nil:
			fmt.Fprintln(stdout, strings.Join(chain, chainArrow))
			return exitSuccess
		case strings.Contains(url, urlSchemeEnd):
			return fail(errors.New(url + " is not required by version"))
		default:
			return fail(errors.New(why + " is not a name " + importer.ModFile + " requires by version: give the module's URL, as " + program + " " + depsCommand + " lists it"))
		}
	}

	deps := m.Dependencies()
	if len(deps) == 0 {
		fmt.Fprintln(stdout, "no dependencies by version")
	}

	for _, d := range deps {
		fmt.Fprintln(stdout, d.URL+" "+d.Version)
		for _, r := range d.Required {
			fmt.Fprintln(stdout, requiredIndent+r.Version+" by "+r.By)
		}
	}

	return exitSuccess
}

// fetchDependencies fetches every module the module of the current
// directory reaches by version into the module cache, and locks it as a
// build would, so that a build after needs no network.
func fetchDependencies(stderr io.Writer) int {
	m, _, err := moduleHere()
	if err == nil {
		err = m.Resolve()
	}

	if err != nil {
		return failer(fetchCommand, stderr)(err)
	}

	return exitSuccess
}

// moduleHere returns the module of the current directory, the nearest
// snello.mod in it or above it, and its file's text.
func moduleHere() (*importer.Module, string, error) {
	m, err := importer.FindModule(currentDir)
	if err != nil {
		return nil, "", err
	}

	if m == nil {
		return nil, "", errors.New("no " + importer.ModFile + " in the current directory or above it: " + program + " " + initCommand + " starts a module")
	}

	src, err := os.ReadFile(filepath.Join(m.Dir, importer.ModFile))
	if err != nil {
		return nil, "", err
	}

	return m, string(src), nil
}

// requiredVersion returns the version m requires name at from the
// repository at url, or "" when it does not require it so.
func requiredVersion(m *importer.Module, name, url string) string {
	for _, r := range m.Requires {
		if r.Name == name && r.URL == url {
			return r.Version
		}
	}

	return ""
}

// compareVersions compares the versions a and b, which a module file
// held or a repository's tags named, and so parse: -1, 0 or +1.
func compareVersions(a, b string) int {
	va, _ := version.Parse(a)
	vb, _ := version.Parse(b)
	return va.Compare(vb)
}

// distinct returns names with each name once, in the order first given.
func distinct(names []string) []string {
	var out []string
	for _, n := range names {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}

	return out
}

// failer returns what a command does with an error: it writes it on
// stderr after the command's name and exits 1.
func failer(command string, stderr io.Writer) func(error) int {
	return func(err error) int {
		fmt.Fprintln(stderr, program+" "+command+": "+err.Error())
		return exitFailure
	}
}
