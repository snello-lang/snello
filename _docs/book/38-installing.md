# 38. Installing Snello

The compiler is one program, `snello`, which carries the runtime and the standard library inside it; installing Snello is putting that one file on your path and having clang beside it. This chapter is every way to do that, what each needs, and how to upgrade and uninstall by each.

## The one line

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/snello-lang/snello/main/release/install.sh | sh
```

Windows, in PowerShell 5.1 or later:

```powershell
irm https://raw.githubusercontent.com/snello-lang/snello/main/release/install.ps1 | iex
```

The script finds your system and processor, downloads the latest release's archive and its checksums, refuses an archive the checksums do not vouch for, verifies the archive's provenance when the GitHub CLI is installed and signed in and says so when it is not, puts `snello` into `~/.local/bin`, or `%LOCALAPPDATA%\Programs\snello` on Windows, and says when that directory is not on your path, which on Windows it adds. It ends with `snello doctor`; when a build still needs clang, the script says so and asks whether to install it now, yes or no, and on yes runs the system's own package manager, Apple's Command Line Tools, or on Windows LLVM through `winget` with its directory put on your `Path`, and then the Build Tools. A `sudo` password, Apple's dialog and an installer's own prompt stay yours. Nothing passes through a browser, so macOS puts no quarantine mark on what it installs.

Three variables shape it, set for the shell the script runs in, not for `curl`:

| Variable | What it does | Example |
|---|---|---|
| `SNELLO_VERSION` | installs that version instead of the latest release | `curl -fsSL … \| SNELLO_VERSION=0.1.0 sh`; on Windows `$env:SNELLO_VERSION = '0.1.0'` before the line |
| `SNELLO_INSTALL` | installs into that directory | `curl -fsSL … \| SNELLO_INSTALL=/opt/snello/bin sh` |
| `SNELLO_YES` | answers yes to every question, for a machine with nobody at the keyboard | `curl -fsSL … \| SNELLO_YES=1 sh` |

While no release is out yet, the script installs the newest pre-release and says so; a pre-release is marked as one on the Releases page and is never what "latest" means.

**Upgrade:** run the line again. **Uninstall:** remove `~/.local/bin/snello`, or the directory `%LOCALAPPDATA%\Programs\snello` on Windows.

## By hand, from a release

Every release on the [Releases page](https://github.com/snello-lang/snello/releases) holds one archive per platform, each with the binary, the license and the README:

| Platform | Archive |
|---|---|
| Linux, x86-64 | `snello-VERSION-linux-amd64.tar.gz` |
| Linux, arm64 | `snello-VERSION-linux-arm64.tar.gz` |
| macOS, Apple silicon | `snello-VERSION-darwin-arm64.tar.gz` |
| macOS, Intel | `snello-VERSION-darwin-amd64.tar.gz` |
| Windows, x86-64 | `snello-VERSION-windows-amd64.zip` |
| Windows, arm64 | `snello-VERSION-windows-arm64.zip` |

1. **Download** the archive, and from the same release `SHA256SUMS` and `SHA256SUMS.sigstore.json`, into one directory. Download with `curl` or `gh` rather than a browser: macOS marks what a browser downloads as quarantined and refuses to run it until the binaries are signed, which `xattr -d com.apple.quarantine snello` undoes.
2. **Check it.** `sha256sum -c --ignore-missing SHA256SUMS`, `shasum -a 256` on macOS, says the archive is the one the release lists. With [cosign](https://github.com/sigstore/cosign) 3 or later, `cosign verify-blob --bundle SHA256SUMS.sigstore.json --certificate-oidc-issuer https://token.actions.githubusercontent.com --certificate-identity-regexp '^https://github.com/snello-lang/snello/\.github/workflows/release\.yml@refs/tags/v' SHA256SUMS` says the list was signed by this repository's release workflow and nothing else; with the [GitHub CLI](https://cli.github.com/), `gh attestation verify snello-VERSION-linux-amd64.tar.gz --repo snello-lang/snello` says the workflow on that commit built the archive. The release's notes carry the same lines and every archive's checksum.
3. **Unpack it and put `snello` on your path.** Linux and macOS: `tar -xzf snello-VERSION-linux-amd64.tar.gz`, then `mv snello-VERSION-linux-amd64/snello ~/.local/bin/`, or any directory on your path. Windows: unzip the archive and add its folder to your `Path`, through Settings, or `setx Path "%Path%;C:\path\to\snello-VERSION-windows-amd64"` and a new terminal.
4. **Install clang** as the next section says, and run `snello doctor`.

**Upgrade:** the same four steps with the next release's archive. **Uninstall:** remove the binary.

## From source, with Go

The compiler is written in Go, with nothing but Go's standard library, so with Go 1.27.2 or later on your machine:

```sh
go install github.com/snello-lang/snello/cmd/snello@latest
```

puts the latest tagged release's compiler into Go's `bin` directory, `~/go/bin` unless `GOBIN` says otherwise, built from source. It says `devel` where a release says `stable`, since it carries no channel, and is otherwise the same compiler. To work on the compiler, clone it instead:

```sh
git clone https://github.com/snello-lang/snello
cd snello
go build -o bin/snello ./cmd/snello
```

`make build` does the same inside the Linux image CI uses, for a build that matches CI's; it needs Docker, and `NATIVE=1 make build` uses the Go on your machine instead. The [contribution guide](../../CONTRIBUTING.md) has the rest.

**Upgrade:** `go install …@latest` again, or `git pull` and `go build`. **Uninstall:** remove the binary.

## What a build needs beside the binary

`snello` compiles a program to LLVM IR, or to C, and the system's clang builds the executable from it with Snello's runtime, so a build needs clang, version 15 or later, and a linker that takes clang's link-time optimization, which a release build links with; the C backend and a debug build compile with a C compiler, `cc`, which is clang on Windows; and `git`, only to fetch a module a `require` line names by version.

| System | What installs it |
|---|---|
| macOS | `xcode-select --install`, Apple's Command Line Tools, which bring clang, `cc` and the linker |
| Debian, Ubuntu | `sudo apt install clang` |
| Fedora | `sudo dnf install clang` |
| Arch | `sudo pacman -S clang` |
| Windows | `winget install LLVM.LLVM`, then `C:\Program Files\LLVM\bin` added to your `Path` and a new window, since the installer adds it to no Path; and Visual Studio's Build Tools with the C++ workload, which hold the Windows SDK and the C runtime clang links against |

`snello doctor` checks each of these by trying it as a build does and prints, for anything missing, the one line that installs it; every line should say `ok` before you build. `CLANG` and `CC` name other compilers to use instead of the ones on the path, and [chapter 32](32-the-snello-command.md) has every variable `snello` reads.

## Versions, releases and pre-releases

`snello version` says which compiler this is: the version, the commit it was built from, the channel, `stable` for a release and `devel` for a build from source, and the platform:

```
$ snello version
snello 0.1.0 (8477985) stable darwin/arm64
```

A release is a tag `vX.Y.Z` with its archives on the Releases page; a pre-release, `0.1.0-alpha.1`, is one on the way to the version it names, marked as such on the page and never the latest release, so the script and `go install …@latest` take it only while no release exists yet, or when named. A module's `snello` line says which release it was written for, and a pre-release of that version compiles it; [chapter 2](02-getting-started.md) has the module file.

## Not there yet

Homebrew and winget packages, and a container image, come after the first release; until then the script is the way to keep current. The binaries are not yet signed with an Apple Developer ID or an Authenticode certificate, which is why a browser download is quarantined on macOS and warned about on Windows while the script's and `curl`'s are not.
