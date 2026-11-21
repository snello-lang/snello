# How a release happens

This directory holds everything a release is made with, apart from the development image and the Makefile, which are for working on the compiler. This page is the procedure, step by step, with the reason for each step; the decisions behind it, and their reasons, are in [the distribution design note](../_docs/design/14-distribution.md).

## What a release is

A release is a tag `vX.Y.Z` on `main` and a GitHub release under it holding one archive per platform, `snello-X.Y.Z-linux-amd64.tar.gz` and five more, each with the `snello` binary, the `LICENSE` and the `README.md`; `SHA256SUMS` over them, signed; build provenance and an SBOM for each. A developer downloads one archive and has the whole toolchain but for the system's clang, since the binary carries the runtime and the standard library.

The maintainers decide what is released and when. **Nothing is released by a push, by a pull request or on a schedule.** One act releases: a maintainer pushing a tag, described below.

## The pieces

| Piece | What it is |
|---|---|
| `compiler/version/version.go` | `version.Current`, the version the compiler says it is and checks modules against |
| `CHANGELOG.md`, `changelogs/` | the index of every release, newest first, and one file per release in the Keep a Changelog form, which is the release's notes |
| `prepare.sh` | `make release-prepare VERSION=X.Y.Z`: writes the version into `version.go`, its changelog into `changelogs/X.Y.Z.md` through `changelog.sh`, and its line into the index |
| `changelog.sh` | writes a release's changelog from the Conventional Commits since the last release |
| `dry-run.sh` | `make release-dry-run`: builds the six archives and smoke-tests this machine's, as the workflow does, publishing nothing |
| `install.sh`, `install.ps1` | the one-line install, served from `main`: the latest release's archive for the machine, checked against `SHA256SUMS`, into `~/.local/bin` or `%LOCALAPPDATA%\Programs\snello`, then `snello doctor`, offering to install clang, and on Windows LLVM and the Build Tools, when a build still needs them; the workflow runs each against the release it just published, `install.sh` on a bare Debian too, where it installs clang itself |
| `build.sh` | builds the six archives and `SHA256SUMS` into `dist/`, the same bytes wherever it runs, through `archive/`, the Go program that writes an archive |
| `smoke.sh` | a developer's first five minutes from an archive on the path: `snello doctor`, `snello new`, run and test through both backends |
| `.github/workflows/release.yml` | on a tag, builds, checks and publishes the release; started by hand, a dry run that publishes nothing |

## Once, before the first release

This is the owner's, since it needs the repository's settings; the release workflow itself needs no secret, since it signs and attests under its own identity, and `main` already takes changes through a pull request alone.

1. **Install Mend's Renovate app** on the repository. *Why:* `.github/renovate.json` is read by the app; without it nothing keeps the pinned actions and the image's base current.

## Every release, step by step

1. **Decide.** A maintainer decides that `main` is a release: a minor when it has something to ship, a patch when a fix cannot wait, or a pre-release, `0.1.0-alpha.1`, to put a build in people's hands before the version it leads to. A pre-release is published marked as one and is never the latest release, so a script asking for the latest skips it. *Why no calendar:* a cadence promised and missed costs more trust than none; a release that is a decision is one someone stands behind.
2. **Prepare it in a pull request.** On a branch from `main`: `make release-prepare VERSION=0.2.0`, which writes the version into `compiler/version/version.go`, the release's changelog, from the commits since the last release, into `changelogs/0.2.0.md`, and its line at the top of `CHANGELOG.md`; read the file and keep what reads well; open the pull request, `chore(release): prepare 0.2.0`, and merge it like any other. *Why one file per release:* the release's notes are that file, whole, and the index stays a screen long however many releases there are. *Why a pull request:* the version and the changelog are reviewed under the branch rule, and the changelog is read before it is published. *Why a script writes the changelog:* the version is one of SemVer's numbers, which the commits since the last tag decide, `feat` a minor and `fix` a patch, and the changelog is their subjects grouped; what is read from the commits is not mistyped.
3. **Push the tag.** On the merge commit: `git tag -a v0.2.0 -m 'snello 0.2.0' && git push origin v0.2.0`. This is the release. *Why the merge commit is proven:* the branch rule requires the pull request's branch to be up to date with `main` before it merges, so the pull request's checks tested the tree the merge put on `main`, and nothing runs on `main` itself. *Why a tag by hand:* it is the common practice, Go itself and most Go projects release this way, and it needs no token of a maintainer's in a workflow.
4. **The tag starts `release.yml`, which builds twice.** `build.sh` runs on the x86-64 and the arm64 Linux runners, each building all six platforms, since the compiler is pure Go; it refuses a tag whose version is not `version.Current`, and a version with no changelog file, so a tag pushed on the wrong commit builds nothing. The two sets of archives are compared byte for byte, and a difference stops everything. *Why twice:* a release whose bytes depend on the machine that built it cannot be checked by anyone; building twice proves the reproducibility rather than claiming it.
5. **Each archive is smoke-tested on its own platform.** On six runners, one per platform, the archive is unpacked, put on the path, and `smoke.sh` runs with nothing else installed but the runner's clang. A failure on a tier-1 platform stops the release; a failure on `windows/arm64`, tier 2, is written into the release notes and does not stop it. *Why:* a release that fails its own first five minutes on a platform it promises is not a release; tier 2 promises a fix before the next release, not the release itself.
6. **The release is signed, attested and published.** `SHA256SUMS` is signed by Sigstore under the workflow's own identity, with no key kept anywhere; GitHub records the provenance of each archive, which workflow on which commit built it; `syft` writes an SBOM for each; then the GitHub release `vX.Y.Z` is created as a draft, the archives uploaded, and the draft published, with the release's changelog file as its notes, each archive's tier and checksum, and the commands that verify them. The workflow refuses to publish a version that has no changelog file. *Why signed and attested:* so that anyone can check that a download is what this workflow built, which is the baseline a toolchain is held to now.
7. **The install scripts are tried against it.** On a runner of each system, `install.sh` and `install.ps1` install the release just published, pinned to its tag, and `snello version` must say so. *Why:* the scripts are what a developer runs; a release they cannot install is reported in the run, though the release stands.
8. **Done.** The release is on the Releases page, where the install scripts read it.

A maintainer's part is steps 1 to 3; the rest is the workflow's, in about half an hour.

## A dry run

`make release-dry-run` builds the six archives into `dist/` and runs the smoke test on this machine's, from the archive alone, as the workflow does on each platform; it publishes nothing and changes nothing. Started by hand from the Actions page, `release.yml` does the same on every platform, on `main` or any branch, writes the notes too, and the archives and the notes stay as the run's artifact for ninety days. *Why:* it proves the release before release day, and tries a change to it, without a release nobody decided on.

## When something fails

- **A smoke test fails on a tier-1 platform.** Nothing is published and the tag stands. Fix the cause on `main` through a pull request as usual, prepare a patch release, and tag it. The failed tag stays as a record and is never reused: versions are never reused, as SemVer says.
- **The two builds differ.** The same: nothing is published; find what varies by machine, fix it on `main`, release again. `build.sh` can be run twice on two machines by hand to look for the cause.
- **The tag's version is not `version.Current`**, or it has no changelog file: the workflow stops at the first build and says so. Prepare the release properly and tag the right commit under the next version.
- **The workflow fails for a reason outside the code**, a runner down or a network error: re-run the failed jobs from the Actions page. The tag is unchanged, so the re-run builds the same release, and a draft a failed publish left behind is replaced.
- **The changelog's date** is the day the release was prepared, not the day of the tag; when days pass between the two, change the date in the release's file and the index, or accept it.

## A patch for an older release

The latest two minor releases get fixes. When a fix must reach the previous minor, make `release-0.N` from its tag, `git switch -c release-0.N v0.N.0`, merge the fix into it through a pull request, prepare the patch on that branch as in step 2, and push the tag `v0.N.1` on it; `release.yml` builds a tag wherever it is. No release branch exists until this happens, and none is made ahead of need.

## Trying it on a fork

Fork the repository and start `release.yml` by hand from the Actions page for the dry run; to try the tag's path to the end, push a tag on the fork, which publishes a release there.

## Building the archives by hand

`sh release/build.sh` builds the six archives into `dist/`, from this checkout, with Go and git alone: `release/archive`, a Go program, writes the archives, so that no tar, gzip or zip of the system, and no zlib, has a say in their bytes. Run on two machines, it writes the same bytes, which `diff -r` shows. It is not needed to release, only to look at what a release holds or to chase a difference.
