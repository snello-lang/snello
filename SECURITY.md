# Security policy

Snello promises memory safety: a program the compiler accepts does not read or write memory it does not own, free a value twice, or race on shared data, outside the C a package binds, which the compiler cannot see into. A way around that promise is a vulnerability, and so is anything that lets someone install something other than what Snello's release workflow built.

## Reporting a vulnerability

Report it privately, never in a public issue, a pull request or a discussion: on the repository's **Security** tab, choose **Report a vulnerability**, or go straight to [the form](https://github.com/snello-lang/snello/security/advisories/new). Only the maintainers see the report.

Say what lets someone see it happen:

- what `snello version` prints, and the platform;
- the smallest program, input or steps that show it;
- what it lets a program or a person do that they should not be able to.

The maintainers acknowledge a report within seven days, say whether they take it as a vulnerability, and keep the reporter told as the fix is made.

## What counts

- **A hole in the checker:** a program the compiler accepts that uses a value after it is freed or moved, frees one twice, keeps a borrow past its value, reads past the end of a slice or a string, or shares a value between tasks unsafely.
- **The runtime and the standard library:** memory corruption, or a crash or a hang a program's input can cause where the library promises an error, in `encoding/json`, `net`, `bufio` or any package that reads what a program is given.
- **The release:** the install scripts, the archives, `SHA256SUMS` and its signature, the provenance: anything that lets a download be other than what `release.yml` built from a tag.
- **The `snello` command**, when building, testing or checking a module runs or writes something the module did not ask for.

Not a vulnerability, and welcome as an ordinary issue: a bug that breaks no promise above; a crash in C a package binds, which the compiler cannot see into; and a program that panics on a mistake the language turns into a panic, such as an index out of range, which ends the program rather than reading memory.

## Versions that get fixes

| Release | Security fixes |
|---|---|
| The latest minor release, `0.N` | yes, in a patch release `0.N.x` |
| The minor release before it, `0.(N-1)` | yes, on the same day as the latest |
| Any older release | no |
| A pre-release, such as `0.1.0-alpha.2` | no: the fix goes into the next pre-release or release |

The latest two minor releases get fixes; [the distribution design note](_docs/design/14-distribution.md) says why, and [chapter 38](_docs/book/38-installing.md) has the whole support matrix.

## How a fix is disclosed

The fix is made in a private GitHub security advisory, out of public view, and released as patch releases of every supported minor on the same day. The advisory is then published with a CVE, which the maintainers request from GitHub on the advisory, and names the versions affected, the versions fixed and what to do; the release notes name the advisory. The reporter is credited in it unless they ask not to be.
