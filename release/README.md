# How a release happens

This directory holds everything a release is made with, apart from the development image and the Makefile, which are for working on the compiler. This page is the procedure, step by step, with the reason for each step; the decisions behind it, and their reasons, are in [the distribution design note](../_docs/design/14-distribution.md).

## What a release is

A release is a tag `vX.Y.Z` on `main` and a GitHub release under it holding one archive per platform, `snello-X.Y.Z-linux-amd64.tar.gz` and five more, each with the `snello` binary, the `LICENSE` and the `README.md`; `SHA256SUMS` over them, signed; build provenance and an SBOM for each. A developer downloads one archive and has the whole toolchain but for the system's clang, since the binary carries the runtime and the standard library.

The maintainers decide what is released and when. **Nothing is released by a push, by a pull request or on a schedule.** One act releases: a maintainer pushing a tag, described below.

## The pieces

| Piece | What it is |
|---|---|
| `compiler/version/version.go` | `version.Current`, the version the compiler says it is and checks modules against |
| `CHANGELOG.md` | every release's section, newest first, in the Keep a Changelog form |
| `prepare.sh` | `make release-prepare VERSION=X.Y.Z`: writes the version into `version.go` and its section into `CHANGELOG.md`, through `changelog.sh` |
| `changelog.sh` | writes a release's section from the Conventional Commits since the last tag |
| `dry-run.sh` | `make release-dry-run`: builds the six archives and smoke-tests this machine's, as the workflow does, publishing nothing |
| `build.sh` | builds the six archives and `SHA256SUMS` into `dist/`, the same bytes wherever it runs, through `archive/`, the Go program that writes an archive |
| `smoke.sh` | a developer's first five minutes from an archive on the path: `snello doctor`, `snello new`, run and test through both backends |
| `.github/workflows/release.yml` | on a tag, builds, checks and publishes the release; started by hand, a dry run that publishes nothing |

## Once, before the first release

These are the owner's, since they need the repository's settings. The release workflow itself needs no secret: it signs and attests under its own identity.

1. **Protect `main`**: require a pull request and the status checks, for administrators too. *Why:* a release is a change like any other.
2. **Install Mend's Renovate app** on the repository. *Why:* `.github/renovate.json` is read by the app; without it nothing keeps the pinned actions and the image's base current.

## Every release, step by step

1. **Decide.** A maintainer decides that `main` is a release: a minor when it has something to ship, a patch when a fix cannot wait. *Why no calendar:* a cadence promised and missed costs more trust than none; a release that is a decision is one someone stands behind.
2. **Prepare it in a pull request.** On a branch from `main`: `make release-prepare VERSION=0.2.0`, which writes the version into `compiler/version/version.go` and the release's section, from the commits since the last tag, at the top of `CHANGELOG.md`; read the section and keep what reads well; open the pull request, `chore(release): prepare 0.2.0`, and merge it like any other. *Why a pull request:* the version and the changelog are reviewed under the branch rule, and the changelog is read before it is published. *Why a script writes the section:* the version is one of SemVer's numbers, which the commits since the last tag decide, `feat` a minor and `fix` a patch, and the changelog is their subjects grouped; what is read from the commits is not mistyped.
3. **Push the tag.** On the merge commit: `git tag -a v0.2.0 -m 'snello 0.2.0' && git push origin v0.2.0`. This is the release. *Why a tag by hand:* it is the common practice, Go itself and most Go projects release this way, and it needs no token of a maintainer's in a workflow.
4. **The tag starts `release.yml`, which builds twice.** `build.sh` runs on the x86-64 and the arm64 Linux runners, each building all six platforms, since the compiler is pure Go; it refuses a tag whose version is not `version.Current`, and a changelog without the version's section, so a tag pushed on the wrong commit builds nothing. The two sets of archives are compared byte for byte, and a difference stops everything. *Why twice:* a release whose bytes depend on the machine that built it cannot be checked by anyone; building twice proves the reproducibility rather than claiming it.
5. **Each archive is smoke-tested on its own platform.** On six runners, one per platform, the archive is unpacked, put on the path, and `smoke.sh` runs with nothing else installed but the runner's clang. A failure on a tier-1 platform stops the release; a failure on `windows/arm64`, tier 2, is written into the release notes and does not stop it. *Why:* a release that fails its own first five minutes on a platform it promises is not a release; tier 2 promises a fix before the next release, not the release itself.
6. **The release is signed, attested and published.** `SHA256SUMS` is signed by Sigstore under the workflow's own identity, with no key kept anywhere; GitHub records the provenance of each archive, which workflow on which commit built it; `syft` writes an SBOM for each; then the GitHub release `vX.Y.Z` is created as a draft, the archives uploaded, and the draft published, with the changelog's section as its notes, each archive's tier and checksum, and the commands that verify them. The workflow refuses to publish when the changelog's newest section is not the version's. *Why signed and attested:* so that anyone can check that a download is what this workflow built, which is the baseline a toolchain is held to now.
7. **Done.** The release is on the Releases page, where the install script and the package managers read it.

A maintainer's part is steps 1 to 3; the rest is the workflow's, in about half an hour.

## A dry run

`make release-dry-run` builds the six archives into `dist/` and runs the smoke test on this machine's, from the archive alone, as the workflow does on each platform; it publishes nothing and changes nothing. Started by hand from the Actions page, `release.yml` does the same on every platform, on `main` or any branch, writes the notes too, and the archives and the notes stay as the run's artifact for ninety days. *Why:* it proves the release before release day, and tries a change to it, without a release nobody decided on.

## When something fails

- **A smoke test fails on a tier-1 platform.** Nothing is published and the tag stands. Fix the cause on `main` through a pull request as usual, prepare a patch release, and tag it. The failed tag stays as a record and is never reused: versions are never reused, as SemVer says.
- **The two builds differ.** The same: nothing is published; find what varies by machine, fix it on `main`, release again. `build.sh` can be run twice on two machines by hand to look for the cause.
- **The tag's version is not `version.Current`**, or the changelog has no section for it: the workflow stops at the first build or at the notes and says so. Prepare the release properly and tag the right commit under the next version.
- **The workflow fails for a reason outside the code**, a runner down or a network error: re-run the failed jobs from the Actions page. The tag is unchanged, so the re-run builds the same release, and a draft a failed publish left behind is replaced.
- **The changelog's date** is the day the release was prepared, not the day of the tag; when days pass between the two, change the date in the pull request, or accept it.

## A patch for an older release

The latest two minor releases get fixes. When a fix must reach the previous minor, make `release-0.N` from its tag, `git switch -c release-0.N v0.N.0`, merge the fix into it through a pull request, prepare the patch on that branch as in step 2, and push the tag `v0.N.1` on it; `release.yml` builds a tag wherever it is. No release branch exists until this happens, and none is made ahead of need.

## Trying it on a fork

Fork the repository and start `release.yml` by hand from the Actions page for the dry run; to try the tag's path to the end, push a tag on the fork, which publishes a release there.

## Building the archives by hand

`sh release/build.sh` builds the six archives into `dist/`, from this checkout, with Go and git alone: `release/archive`, a Go program, writes the archives, so that no tar, gzip or zip of the system, and no zlib, has a say in their bytes. Run on two machines, it writes the same bytes, which `diff -r` shows. It is not needed to release, only to look at what a release holds or to chase a difference.
