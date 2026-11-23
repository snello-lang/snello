# 35. Dependencies

The design is section 9 and decision 8 of `../design/08-packages.md`. The commands are `cmd/snello/deps.go`; the module file, the module cache, the selection of versions and the lock file are `compiler/importer/`: `mod.go`, `modcache.go`, `versions.go` and `requires.go`.

A Snello program uses a library someone else wrote by naming the library's git repository and a release of it. The build fetches the library, checks it, and records exactly what it built against, so that every machine that builds the program builds the same thing. There is nothing to install and no copy of the library to keep in your repository: dependencies are fetched and just work.

## Using a library

In a module (chapter 2 starts one with `snello new` or `snello init`), `snello add` requires a library by the name you will import it under and its repository's address:

```
$ snello add regexp https://github.com/snello-lang/regexp
required regexp https://github.com/snello-lang/regexp 0.2.0
```

`add` asks the repository for its releases, takes the latest, fetches it, and writes one line into `snello.mod`:

```
snello 0.1.0

require regexp https://github.com/snello-lang/regexp 0.2.0
```

The program then imports the library by that name, and a package inside it by a path under the name:

```go
package main

import (
    "fmt"
    "regexp"
)

func main() {
    re := regexp.MustCompile(`a+b`)
    fmt.Println(re.MatchString("aaab"))
}
```

`snello build`, `run` and `test` work as before. The first build on a machine fetches what it lacks; every build after finds it in the module cache. You can also write the `require` line by hand; `add` is the way that checks the name, the address and the version before it asks the repository anything, fetches and locks before it changes the file, and leaves `snello.mod` as it was when anything fails, or when you stop it part way. The name must not be a directory of your module, since `import "regexp"` would then name both.

The address is the repository's URL with its scheme: `https://` or `ssh://` and a host, or `file:///` and a path for a repository on your own disk. It is written one way, so that one repository is one module: the host in lower case, no `/` at the end, no `.` or `..` in the path, and no password, which would end up in the lock file; let git's own credentials hold it.

## Versions are tags

A version is a Semantic Versioning version, `0.2.0`, and it names the repository's tag `v0.2.0`. That is the whole contract: a library's author releases a version by pushing a tag, and a program builds against that tag and nothing else, never a branch, even one named like the tag.

`snello add NAME URL` takes the latest release, the highest tag `vMAJOR.MINOR.PATCH` with no pre-release. A pre-release, `0.3.0-rc.1`, is never chosen for you; name it to use it, `snello add regexp URL 0.3.0-rc.1`. For a library required already, `add` with no version raises it to the latest release whatever its major version, as asking for the latest means, and never lowers it: a requirement at the latest release, or at a pre-release above it, is kept, `regexp https://github.com/snello-lang/regexp 0.2.0 is required already`. Naming a version changes the version of a library required already, down as well as up:

```
$ snello add regexp https://github.com/snello-lang/regexp 0.1.4
regexp https://github.com/snello-lang/regexp 0.2.0 -> 0.1.4
```

## The lock file

The build writes `snello.lock` beside `snello.mod`, one line for every module the program reaches by version, the libraries your libraries require included:

```
https://github.com/snello-lang/regexp 0.2.0 3f2a9c1d0b47e5f1a2c3d4e5f60718293a4b5c6d h1:5dHbI9mL3x2s8k4yVq0wQ2xR0Tn1v8dGJm1a0Lq9f3E=
```

Each line is the repository, the version, the commit the tag named when it was first fetched, and a SHA-256 hash of the library's files. Commit the file with `snello.mod`, for a program and for a library alike. You never edit it: the build adds a line for a new dependency, drops a line no longer needed, and leaves the file alone otherwise.

Every build checks each library against its line. A copy in your cache that changed, or that does not match the lock file, is simply fetched again. A tag that its author moved to other code stops the build, with both commits:

```
snello.lock: https://github.com/snello-lang/regexp 0.2.0 was locked at commit 3f2a9c1d0b47, h1:5dHb…, and the tag v0.2.0 now names commit 8e01d77a52c9, h1:Qm7c…:
the tag was moved to other code, or the repository rewritten; if the change is meant, remove the line and build again
```

That is the lock file doing its job: someone replaced the release you built against, and the build will not quietly build something else. Look at the two commits in the repository; if the change is meant, remove that one line from `snello.lock` and build again.

## Updating

`snello update` raises every dependency the module requires by version to its latest compatible release:

```
$ snello update
regexp https://github.com/snello-lang/regexp 0.2.0 -> 0.2.3
```

Compatible means what Semantic Versioning promises, which the first number that is not zero carries: from `1.0.0` the same major version, so `1.4.0` may become `1.6.2` but not `2.0.0`; before `1.0.0`, where a minor release may break things, the same minor version, so `0.2.0` may become `0.2.3` but not `0.3.0`; and before `0.1.0` the same patch version, so `0.0.3` is never raised. `snello update NAME...` raises only the dependencies named. `snello update -major` takes the latest release whatever its major version: do it on purpose, since the library's API may have changed.

`update` raises what your `snello.mod` requires. A library one of your libraries requires is raised by raising that library, or by requiring it yourself at a higher version, which the build then takes.

## Removing

`snello remove NAME...` takes requirements out of `snello.mod` and the lock file:

```
$ snello remove regexp
snello remove: regexp is still imported, at /home/me/app/main.snello:5: remove the imports first
```

It refuses while any file under the module imports the library, so that removing never leaves a program that does not build. It looks wider than a build reaches rather than narrower: test files, files for every platform, a directory under `_`, a module nested in yours and a file with a syntax error all count, so at worst it asks you to look at an import a build would not have used. Once nothing imports it:

```
$ snello remove regexp
removed regexp
```

## Seeing what you depend on

`snello deps` prints every module the program builds against by version, the version selected, and, indented, who requires which version:

```
$ snello deps
https://github.com/snello-lang/regexp 0.2.3
    0.2.3 by the program
    0.2.1 by https://github.com/x/scan 1.0.0
https://github.com/snello-lang/unicode 0.4.1
    0.4.1 by https://github.com/snello-lang/regexp 0.2.3
https://github.com/x/scan 1.0.0
    1.0.0 by the program
```

A program with no dependency by version prints `no dependencies by version`.

`snello deps -why NAME`, for a name your `snello.mod` requires, or `-why URL` for any module `snello deps` lists, prints the chain that brings it in, from the program:

```
$ snello deps -why https://github.com/snello-lang/unicode
the program -> https://github.com/snello-lang/regexp 0.2.3 -> https://github.com/snello-lang/unicode 0.4.1
```

## When two libraries want one library

A program builds against one version of each repository. When two modules require it at different versions, the build takes the higher: each requirement is the oldest version its module was tested with, so the higher one is the oldest that every module accepts. The choice depends only on what the modules require, never on what was released yesterday, so every machine makes the same one.

Two versions that are not compatible, `1.x` and `2.x`, `0.2.x` and `0.3.x`, or `0.0.3` and `0.0.4`, cannot both be met, and the build says who requires which:

```
https://github.com/snello-lang/regexp is required at 0.2.1 by https://github.com/x/scan 1.0.0 and at 0.3.0 by the program; a program builds against one
```

Require the version the other module accepts, or update the other module to a release that accepts yours. A version that lost the selection, an older release some library still names, is never built, so what it in turn requires never stops your build.

## Working offline, and in CI

The module cache is `snello-modules` in your user cache directory, or the directory `SNELLO_MODCACHE` names. It is apart from Snello's build cache, so clearing one never empties the other, and any module in it, or all of it, can be deleted at any time: the next build fetches what it needs again.

A build that finds every module in the cache runs no `git` and needs no network. `snello fetch` fills the cache without building, which is what to run before going offline, or in a CI job before it caches `SNELLO_MODCACHE`'s directory between runs, keyed on `snello.lock`.

## Private repositories

The build fetches with your own `git`, as you run it: an SSH key, a credential helper or a token that lets you clone a repository lets the build fetch it, with nothing to configure in Snello. A fetch never asks for a password, since a build has no terminal to answer it; a repository you cannot reach fails with git's own words.

## Publishing a library

A library is a module like any other: a directory with `snello.mod` and its packages, in a git repository. To release a version, tag a commit and push the tag:

```
$ git tag v0.2.0
$ git push origin v0.2.0
```

Three rules keep your users' builds working:

- **Never move a tag.** A release is the tag's commit forever; every program that built against it has its commit and hash in its lock file and will refuse the moved tag. Release a new version instead.
- **Require by version.** A library fetched by version cannot require a module by a directory, which exists only on your disk; and a library published on a host cannot require a `file:///` repository.
- **Mind Semantic Versioning.** A change that breaks your API takes a new major version, and before `1.0.0` a new minor version, so that `snello update` never raises a program into a break.

## When something goes wrong

| The message | What it means, and what to do |
|---|---|
| `fetching URL VERSION needs git, which is not found: install it with …`, or `listing the releases of URL needs git, …` | Install git with the command named; `snello doctor` checks it too |
| `fetching URL VERSION: …` with git's words | The repository or the tag cannot be reached: check the URL, the tag `vVERSION`, and your access |
| `listing the releases of URL: …` with git's words | `add` with no version, or `update`, cannot reach the repository: check the URL and your access |
| `malformed version "v1.0.0": a version is MAJOR.MINOR.PATCH, …` | Write the version without the `v`, `1.0.0`; the tag keeps it |
| `URL has no release tag, vMAJOR.MINOR.PATCH: name a version` | The repository has no release: name a version, a pre-release among them |
| `… was locked at commit A, … and the tag vVERSION now names commit B` | The tag was moved after you locked it: see "The lock file" above |
| `… the commit's files now hash …: only two commits sharing one id make that` | The repository's history is not what it was; remove the line only if you trust it |
| `URL is required at V1 by X and at V2 by Y; a program builds against one` | Two incompatible versions: see "When two libraries want one library" |
| `… requires NAME by a directory, …, which a module fetched by version cannot reach` | A published library requires a module on its author's disk: its author must require it by version |
| `… requires NAME from URL, a repository on its author's disk, which a module fetched from a host cannot reach` | A published library requires a `file:///` repository: its author must require one on a host |
| `NAME is still imported, at FILE:LINE: remove the imports first` | `snello remove` refuses while the library is used |
| `NAME is not required by snello.mod` | `snello remove` was given a name `snello.mod` has no line for |
| `NAME is not required by version in snello.mod` | `snello update` was given a name required by a directory, or not at all |
| `NAME is required already, from …: remove it first` | `snello add` will not replace a requirement from another repository or a directory |
| `NAME is a directory of the module, …: require it under another name` | An import of the name would mean both the directory and the library |
| `NAME is not a name snello.mod requires by version: give the module's URL, …` | `snello deps -why` takes a name your file requires, or a URL `snello deps` lists |
| `URL is not required by version` | `snello deps -why` was given a URL no module of the program reaches |
| `a repository is named by its URL, …` | The address breaks one of the rules under "Using a library" |

## For maintainers

`importer.ParseModFile` (`mod.go`) reads `require NAME URL VERSION` beside `require NAME DIR`; `checkRepository` holds the URL to its rules with `net/url`, and a comment begins a word, so that the `//` of a URL is no comment.

`Module.Resolve` (`versions.go`) runs in `LoadProgramFrom`, `CheckDir` and `CheckTests` once the module is found, before any package is read. The language server runs `Module.ResolveCached` in its place, through `importer.Options.Cached`: every required module is taken from the module cache and held to the lock file, a module the cache does not hold is an error that names `snello fetch`, and nothing is fetched, hashed or written, so that a check as the developer types never touches the network or the lock file. `walkLocal` collects the requirements by version of the program's module and of the modules its directory requires lead to, each named by its own file; a program with none asks for no cache and has its lock file removed. `reach` fetches each required module through `fetchModule`, hashes it again, and when a cached copy changed or disagrees with the lock file, discards it and fetches again, so the lock file, not the cache, decides; a fresh copy that still disagrees is refused by `changed`, naming both commits. `checkFetchedRequire` refuses what a published module cannot require. The highest version of each repository is selected, then `checkSelected` walks the requirements of the program and of the selected modules only, breadth first, refusing an incompatible one and recording who requires what, which `Dependencies` and `Why` give the `deps` command. `writeLockIfChanged` writes `snello.lock` only when its records changed, through a file renamed into place. The importer's `enter` gives a reached module's requires by version the selected directories.

`fetchModule` (`modcache.go`) keeps a module at `<host>/<path>@<version>` under `ModCacheDir`, `moduleDir` escaping upper case and other characters and refusing a path outside the cache. A fetch makes a bare repository in a work directory at the cache's root, kept short since git on Windows writes no path past 260 characters, writes `info/attributes` so that `git archive` converts nothing, fetches `refs/tags/vVERSION` alone, reads its commit, unpacks the archive's regular files with `untar`, hashes them with `HashDir`, the `h1:` form, writes the commit and the hash in `h1` beside the tree, and renames the two into place as the module's directory, `<host>/<path>@<version>` holding `tree` and `h1`, so that the cache holds a module whole or not at all. `runGit` runs git with the variables that point it at a repository taken out, as a hook sets them, no prompt, and a non-shared, non-interactive ssh unless the user names their own. `ReleaseVersions` lists a repository's release tags through `git ls-remote`. Two fetches of one module at once keep one copy, and a fetch stopped part way is cleaned up an hour later by `removeLeftovers`.

`requires.go` holds what the commands change: `SetRequire` and `DropRequire` edit the module file's text, keeping its comments, its layout and its `\r\n` line ends, and closing up only the blank lines a removed line leaves; `Rewrite` reads the edited text and resolves it as a build does before it writes the file through `replaceFile`, which renames a whole file into place, keeping the old file's permissions and writing through a link, putting the lock file back should that last write fail; `ImportsOf` finds the imports of a name under the module, wider than a build reaches. `Module.CheckRequire` (`mod.go`) checks what `add` is given before git is asked anything. `cmd/snello/deps.go` is the five commands over these, and `latestFor` picks the release `update` raises to.

The tests build real git repositories in temporary directories: `modcache_test.go` holds the fetch to the tag and not a branch, to the commit's own bytes whatever the attributes or the user's filters, to the repository named under a hook's `GIT_DIR`, and to one copy when eight fetches run at once, and `HashDir` to a hash computed with `shasum` apart from the code; `versions_test.go` the lock file, a moved tag, a cache that heals, selection, a version that lost it, and the refusals; `requires_test.go` the edits; `cmd/snello/deps_test.go` the five commands end to end, and `run_test.go`'s `TestRunByVersion` a program built and run against a tag through both backends.
