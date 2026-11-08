# Stage 10: releases a developer can install

Goal: a developer installs Snello and runs a first program in five minutes, with no Go and no clone of the repository, from a release that meets today's standards for a toolchain: Semantic Versioning 2.0, releases on a schedule with a stable and a nightly channel, reproducible builds, SLSA build provenance, Sigstore signatures, an SBOM, the OpenSSF Scorecard's checks, and binaries macOS trusts. Today the only way in is to build the compiler from source with Go, and the README's first step, `make build`, builds inside the Linux image, which gives a Mac a binary it cannot run. Nothing of the language changes in this stage but the module file's version line, task 10.2: the compiler already embeds its runtime and its standard library, so one binary is the whole toolchain but for the system compiler that turns IR into an executable. The stage ships `0.x` previews; what a major release needs beyond them is the section "Before 1.0" at the end, met by the stages that remain. Proposed 2026-11-08, for review before any task starts; it can run beside stage 9, since it touches the command, the module file's first line and the workflows, and not the checker or the backends.

## Task 10.1: How Snello is distributed

**Description:** The decisions every later task builds on, recorded in a new design note, `../design/10-distribution.md`, before any code.

- **Versions.** Semantic Versioning 2.0 for the toolchain, `v0.1.0` first, every release cut from a tag on main.
- **Cadence and channels.** A minor release on a fixed schedule, every six to eight weeks during `0.x`, whatever is ready, as Go's and Rust's are released by the calendar and not by features; a patch release for a fix that cannot wait. Two channels: `stable`, the tagged releases, and `nightly`, built from main every day and kept for a week, as Rust, Zig and Deno offer.
- **Support.** The latest two minor releases get fixes, as Go fixes its latest two; older ones get none, and the note says so.
- **What `0.x` promises.** Nothing between minor versions: a program may need changing, the release notes say what, and the module's version line of task 10.2 lets a release tell which programs were written for which version.
- **What a release holds.** One archive per platform with the `snello` binary, the license and the README; `SHA256SUMS` over them, signed; build provenance and an SBOM for each.
- **Platforms.** Linux and macOS, each on amd64 and arm64, the four the runtime supports today; Windows is not one, and the note says so.
- **What a developer needs beside the binary.** clang 15 or later, the first to read the opaque `ptr` of the IR the backend writes, and on Linux a linker that takes clang's link-time optimization, since a release build links with `-flto`: the image installs `lld` but the build never names a linker, so the note settles which one works by trying a clean Debian and a clean Fedora. The C backend needs a C compiler. Go is a contributor's tool and never an installer's. Bundling clang, as Zig does, is weighed and left for 1.0: it makes the download self-contained at about ten times the size, and the container image of task 10.9 covers a machine with nothing on it.
- **Where the binary goes.** `~/.local/bin` by the install script unless told otherwise, Homebrew's prefix by the formula.
- **No telemetry.** The command reports nothing anywhere, and the note says so, since a new language cannot spend the trust an opt-out collection costs.

**Acceptance criteria:**
- [ ] `../design/10-distribution.md` written and reviewed before the code, with each decision above and its reason
- [ ] The README's getting started no longer tells a Mac to build inside the Linux image: until the first release it says `NATIVE=1 make build`, or `go install`, for a contributor

**Verification:**
- [ ] The note reviewed; `make check` and `make test` unaffected

**Dependencies:** none
**Files:** `../design/10-distribution.md`, `README.md`
**Scope:** S

## Task 10.2: The language version in `snello.mod`

**Description:** A module says which version of the language it is written for, so that a later release can change a rule for new modules and keep old ones compiling, as Go's `go 1.22` line and Rust's `edition` do. Cheap before 1.0 and a breaking change after it. `snello.mod` gains a first line, `snello 0.1`, which `snello new` writes; a module without one is read as the version of the compiler that reads it, with a note on `snello build`'s first run that the line is missing; a module asking for a newer version than the compiler is refused, `this module needs snello 0.3; this is snello 0.1.0: upgrade it`. The design is a section of `../design/08-packages.md`, where the module file is: what the line means today, nothing but the check, and what it is for later, a rule changed for the modules that declare the version after it.

**Acceptance criteria:**
- [ ] The section written in `../design/08-packages.md` and reviewed before the code
- [ ] The line read by `compiler/importer/mod.go`, a newer version refused with the message designed, a missing line noted once
- [ ] Every `snello.mod` in the repository given the line; chapter 2's packages section shows it

**Verification:**
- [ ] `go test ./compiler/importer ./cmd/snello ./examples`

**Dependencies:** 10.1
**Files:** `compiler/importer/mod.go`, `../design/08-packages.md`, `../book/02-getting-started.md`, every `snello.mod`
**Scope:** S

## Task 10.3: `snello version` and `snello help`

**Description:** A released binary says what it is. `snello version` prints the version, the commit, the channel and the platform, `snello 0.1.0 (8477985) stable darwin/arm64`, stamped in when the release is built; a binary built from source without the stamp prints `snello devel (COMMIT)`, the commit read from the build information Go records. `snello help` prints every command with a line each, and `snello help COMMAND` its flags, replacing the usage block that lists them in one line today; an unknown command says `snello: unknown command "x"; run snello help`.

**Acceptance criteria:**
- [ ] `snello version` and `snello help` as described, with `-version` refused in favour of the command
- [ ] The version stamped by the release build's linker flags and read back by a test of `cmd/snello`
- [ ] Every command's flags listed by `snello help COMMAND`, from the same table the parser of the flags reads, so the two cannot drift

**Verification:**
- [ ] `go test ./cmd/snello`

**Dependencies:** 10.1
**Files:** `cmd/snello/`
**Scope:** S

## Task 10.4: `snello doctor` and a clear message when the toolchain is missing

**Description:** The one dependency is the system compiler, and a developer who lacks it today meets an error from the operating system's exec. `snello doctor` checks what a build needs and says what to do for each thing missing, with the command for the platform: clang found and its version 15 or later, on Linux the linker task 10.1 found that link-time optimization needs, the C compiler for the C backend, the directory the binary is in on the path. `snello build`, `run` and `test` run the same check when clang cannot be started and print its line instead of the exec error, `clang not found: install it with xcode-select --install, or set CLANG`. The check is the driver's, in one place, and the backends' `Compiler()` functions use it.

**Acceptance criteria:**
- [ ] `snello doctor` prints one line per check, passed or failed with the fix, and exits non-zero when a build would fail
- [ ] A clang older than 15 refused with its version and the minimum
- [ ] `snello build` with no clang on the path prints the doctor's line, tested with an empty `PATH`
- [ ] The fix named for macOS, Debian and Ubuntu, Fedora and Arch

**Verification:**
- [ ] `go test ./cmd/snello ./compiler/emit/...`

**Dependencies:** 10.1
**Files:** `cmd/snello/`, `compiler/emit/llvm/build.go`, `compiler/emit/c/build.go`
**Scope:** S

## Task 10.5: `snello run` and `snello new`

**Description:** The first five minutes. `snello run FILE|DIR [ARGS]` builds into a temporary directory, runs the program with the arguments after the path, removes the binary and exits with the program's code; it takes `build`'s flags. `snello new NAME` writes a directory with a `snello.mod` holding the version line of task 10.2, a `main.snello` printing a greeting and a `main_test.snello` with one test, and prints the three commands that build, run and test it. `new` refuses a directory that exists.

**Acceptance criteria:**
- [ ] `snello run` builds and runs, passes the arguments and the exit code through, and leaves no file behind, on success, on a failed build and on a panic
- [ ] `snello new hello && cd hello && snello run . && snello test .` passes through both backends
- [ ] Chapter 2 of the book starts from `snello new`

**Verification:**
- [ ] `go test ./cmd/snello`

**Dependencies:** 10.2, 10.3
**Files:** `cmd/snello/`, `../book/02-getting-started.md`
**Scope:** S

## Task 10.6: The release workflow and the supply chain

**Description:** How a release is made, and why anyone can trust it.

- **Through a pull request.** release-please reads the Conventional Commits on main and keeps a release pull request open with the next version and the changelog in the Keep a Changelog form; merging it tags the release. A version is never typed by hand, and main's rule that changes come through a pull request holds for releases too.
- **The build.** The tag starts `.github/workflows/release.yml`, which builds the four platforms from one runner, since the compiler is pure Go, with cgo off, `-trimpath`, the build ID cleared and the timestamp fixed to the tagged commit's, so that the binary is the same bit for bit wherever it is built; the workflow builds each platform twice and refuses a difference. It packs each into `snello-X.Y.Z-OS-ARCH.tar.gz` with the license and the README, writes `SHA256SUMS`, signs it with Sigstore's keyless signing, attests the build provenance of each archive, SLSA Build Level 2 through GitHub's artifact attestations, and writes an SBOM per archive in SPDX.
- **The smoke test.** Before publishing, each archive is unpacked on a runner of its platform, Linux amd64 and arm64 and macOS amd64 and arm64, and builds, runs and tests `snello new`'s program with nothing but the archive and the runner's clang; a failure publishes nothing.
- **Nightly.** The same workflow on a schedule builds main as `nightly`, published to one release that is replaced each day, with the same checks.
- **The repository's hygiene, by the OpenSSF Scorecard.** Every action pinned by its commit hash in every workflow, `GITHUB_TOKEN` given the least each job needs, Dependabot keeping the actions current, the Scorecard's own workflow run weekly with its badge in the README, and the branch rule that changes come through a pull request applied to administrators as well.

**Acceptance criteria:**
- [ ] A release pull request opened by release-please on a fork, and its merge producing a release with four archives, signed `SHA256SUMS`, provenance and an SBOM for each, and the changelog
- [ ] Each platform built twice with identical bytes, and a difference failing the workflow
- [ ] Each archive smoke-tested on its own platform before the release is published; a failing smoke test publishes nothing
- [ ] `gh attestation verify` and `cosign verify-blob` pass on the release's files
- [ ] A nightly release built and replaced on schedule
- [ ] Every workflow's actions pinned by hash and its tokens least-privileged; the Scorecard's workflow runs and its score is recorded in this task's progress

**Verification:**
- [ ] A dry run on a fork, recorded in this task's progress with the release's link

**Dependencies:** 10.3, 10.5
**Files:** `.github/workflows/release.yml`, `.github/workflows/scorecard.yml`, `.github/dependabot.yml`, `release-please-config.json`, every workflow
**Scope:** M

## Task 10.7: macOS signing and notarization

**Description:** A binary downloaded through a browser is quarantined by macOS, and Gatekeeper refuses one that is not signed by a Developer ID and notarized by Apple. The install script and Homebrew download without the quarantine, so they work without it, but a download link on a website would not. The release workflow signs the two macOS binaries with the project's Developer ID, with the hardened runtime, submits them to Apple's notary service, and staples nothing, since a bare binary cannot hold a ticket; the certificate and the notary's credentials are repository secrets, available to the release job alone. The Apple Developer Program membership, 99 dollars a year, is the project's to hold.

**Acceptance criteria:**
- [ ] Both macOS binaries signed and notarized by the workflow; `spctl --assess` accepts a binary downloaded through a browser on a clean Mac
- [ ] The secrets scoped to the release job's environment, with its approval rule

**Verification:**
- [ ] The assessment run on a clean Mac, recorded in this task's progress

**Dependencies:** 10.6
**Files:** `.github/workflows/release.yml`
**Scope:** S

## Task 10.8: Installing: the script and Homebrew

**Description:** Two ways in, both from the release. `install.sh`, served from the repository's raw main, is POSIX `sh`: it finds the platform, downloads the archive of the latest stable release, or of `SNELLO_VERSION`, or the nightly with `SNELLO_CHANNEL=nightly`, checks it against `SHA256SUMS` and refuses on a mismatch, verifies the provenance when `gh` is installed, installs into `~/.local/bin` or `SNELLO_INSTALL`, and ends by running `snello doctor`. A Homebrew tap, `snello-lang/homebrew-tap`, holds a formula the release workflow updates with the new version and checksums, depending on `llvm` so that clang comes with it on a machine without the Command Line Tools. Upgrading is the same command again, and uninstalling one line for each, in the book.

**Acceptance criteria:**
- [ ] `curl -fsSL .../install.sh | sh` installs a release on a clean macOS and a clean Debian, and `snello new hello && cd hello && snello run .` then works with no Go on the machine
- [ ] A tampered archive refused by the script with the checksums' line
- [ ] `brew install snello-lang/tap/snello` installs the same release; the release workflow bumps the formula
- [ ] `shellcheck` clean

**Verification:**
- [ ] Both installs run in fresh containers or runners, recorded in this task's progress

**Dependencies:** 10.6
**Files:** `install.sh`, `.github/workflows/release.yml`, the tap repository
**Scope:** M

## Task 10.9: The container image and `setup-snello`

**Description:** Two ways in for a machine with nothing on it and for continuous integration. An image, `ghcr.io/snello-lang/snello`, on the same Debian base as the build image, with the release's binary, clang and the linker, tagged by version, by minor version, `stable` and `nightly`, for amd64 and arm64, signed and attested as the archives are, so that `docker run --rm -v "$PWD:/src" -w /src ghcr.io/snello-lang/snello run main.snello` needs nothing installed but Docker. And a GitHub Action, `snello-lang/setup-snello`, which installs a given version or channel on a runner, checks it as the script does and caches it, so that a project tests its Snello code in one step.

**Acceptance criteria:**
- [ ] The image published by the release workflow for both architectures with its tags, signature and provenance, and `snello new`'s program built, run and tested in it
- [ ] `setup-snello` installs a version and the nightly on Linux and macOS runners, used by a workflow of the repository's own
- [ ] Both documented in chapter 18

**Verification:**
- [ ] A dry run of each on a fork, recorded in this task's progress

**Dependencies:** 10.6
**Files:** `build/Dockerfile.release`, `.github/workflows/release.yml`, the action's repository
**Scope:** M

## Task 10.10: The documents a release needs

**Description:** The README leads with installing: the script, Homebrew and the image, then building from source for a contributor, and states clang's minimum up front. A chapter of the book of its own, `18. Installing and the snello command`, for developers and maintainers: installing, upgrading and uninstalling by each way, the channels, every command with its flags, what `snello doctor` checks, and for maintainers how a release is cut through release-please, what the workflow does at each step, how to verify a release's signature, provenance and SBOM, and how a failed release is withdrawn. `CHANGELOG.md`, which release-please keeps. `SECURITY.md`, with GitHub's private vulnerability reporting turned on, the versions that get fixes, and advisories published with a CVE. A support matrix in the distribution note: the platforms, the minimum clang, the versions supported, and that `0.x` releases may break programs.

**Acceptance criteria:**
- [ ] The README, chapter 18, `CHANGELOG.md`, `SECURITY.md` and the support matrix as described, and the book's index and Appendix B updated
- [ ] Private vulnerability reporting enabled on the repository
- [ ] Chapter 2 points at chapter 18 for installing

**Verification:**
- [ ] Read through by the reviewer; every command in chapter 18 run as written

**Dependencies:** 10.1 to 10.9
**Files:** `README.md`, `../book/18-installing.md`, `../book/02-getting-started.md`, `../book/README.md`, `CHANGELOG.md`, `SECURITY.md`
**Scope:** S

## Checkpoint

`v0.1.0` is released through release-please's pull request and the workflow alone. On a clean macOS machine and a clean Linux machine, neither with Go, a developer installs it by the script, by Homebrew and through the image, runs `snello doctor`, and has `snello new`'s program built, run and tested in under five minutes. `snello version` names the release; its archives verify against their signature, provenance and SBOM and rebuild to the same bytes; a macOS binary downloaded through a browser runs. The nightly channel publishes every day. Chapter 18 describes all of it, and the distribution note records each decision.

## Before 1.0

A major release is a promise, and these are its conditions, met by the stages that remain and checked at the checkpoint of the release that would be 1.0:

- **Stage 9 done**: every name the gaps document decided exists, tested and documented.
- **Stage 8 done**: a program depends on a package outside the compiler through `snello.mod`, with versions, and `regexp` ships that way.
- **Stage 7 done**: the language server, the formatter and the editor extension, released with the compiler on the same schedule.
- **A compatibility promise** written in the distribution note: what 1.0 freezes, the grammar, the ownership rules, the standard library's names and signatures, the command's commands and flags, and the module file's form; and how a later change to any of them is made, through the version line of task 10.2.
- **SLSA Build Level 3**: the release built by a reusable workflow isolated from the repository's other jobs.
- **Windows decided**: supported, with the runtime's port, a fifth archive and a package for winget, or declared outside 1.0 in the support matrix.
- **Bundling clang decided**, from the distribution note's weighing.
- **A playground**: Snello run in a browser, compiled and run in a sandboxed service, as Go's, Rust's and Zig's are, since it is how most developers first try a language.
- **The book published** as a site, versioned with each release.
- **Packages for distributions**, Debian's and Fedora's, Nix and a mise plugin, once versions are stable enough for packagers to follow.
- **A release checklist** in chapter 18, so that each release is made the same way.
