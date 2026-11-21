#!/bin/sh
# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Installs Snello on Linux or macOS from a release, in one line:
#
#	curl -fsSL https://raw.githubusercontent.com/snello-lang/snello/main/release/install.sh | sh
#
# It finds the system and the processor, downloads the archive of the
# latest release, or of the version SNELLO_VERSION names, or of the
# newest pre-release while no release exists yet, and SHA256SUMS
# beside it, refuses an archive the list does not vouch for, checks
# the archive's provenance when the GitHub CLI is installed and signed
# in and says so when it is not, puts the snello binary into
# ~/.local/bin, or into SNELLO_INSTALL, says when that directory is
# not on the path and how to add it, and ends by running snello
# doctor, which says what a build still needs and how to install it.
# Run again, it upgrades; removing the binary uninstalls. Nothing is
# downloaded through a browser, so macOS puts no quarantine mark on
# the binary. Needs curl and tar.
set -eu

repo=snello-lang/snello
api=https://api.github.com/repos/$repo/releases
download=https://github.com/$repo/releases/download
dir=${SNELLO_INSTALL:-$HOME/.local/bin}
version=${SNELLO_VERSION:-}
name=snello

say() { printf 'install.sh: %s\n' "$*"; }
fail() {
    printf 'install.sh: %s\n' "$*" >&2
    exit 1
}
need() { command -v "$1" >/dev/null 2>&1 || fail "needs $1"; }
tag_of() { sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1; }

need curl
need tar
os=$(uname -s)
case $os in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) fail "$os is not a system a release is built for: Linux and macOS here, Windows through install.ps1" ;;
esac
arch=$(uname -m)
case $arch in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) fail "$arch is not a processor a release is built for: x86-64 and arm64" ;;
esac
if [ "$os" = darwin ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null)" = 1 ]; then
    arch=arm64 # a shell under Rosetta says x86_64 of an Apple processor
fi

# The version: the one named, else the latest release, else the newest
# pre-release while there is no release yet, since GitHub's latest
# release is never a pre-release.
if [ -n "$version" ]; then
    tag=v${version#v}
else
    tag=$(curl -fsL "$api/latest" 2>/dev/null | tag_of || true)
    if [ -z "$tag" ]; then
        tag=$(curl -fsSL --proto '=https' "$api?per_page=1" | tag_of)
        [ -n "$tag" ] || fail "no release found at https://github.com/$repo/releases"
        say "no release yet: installing the newest pre-release, ${tag#v}"
    fi
fi
version=${tag#v}
archive=snello-$version-$os-$arch.tar.gz

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
say "downloading $archive from $download/$tag"
curl -fsL --proto '=https' --proto-redir =https --retry 3 -o "$work/$archive" "$download/$tag/$archive" || fail "release $tag has no $archive: https://github.com/$repo/releases/tag/$tag"
curl -fsL --proto '=https' --proto-redir =https --retry 3 -o "$work/SHA256SUMS" "$download/$tag/SHA256SUMS" || fail "release $tag has no SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
    (cd "$work" && sha256sum -c --ignore-missing --quiet SHA256SUMS) || fail "$archive does not match the release's SHA256SUMS: it is not the file the release published"
else
    (cd "$work" && shasum -a 256 -c --ignore-missing --quiet SHA256SUMS) || fail "$archive does not match the release's SHA256SUMS: it is not the file the release published"
fi
say "checksum verified against SHA256SUMS"

if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    gh attestation verify "$work/$archive" --repo "$repo" >/dev/null || fail "$archive's provenance does not verify: it was not built by $repo's release workflow"
    say "provenance verified: built by $repo's release workflow"
else
    say "provenance not checked: the GitHub CLI, gh, signed in, checks it with gh attestation verify"
fi

tar -xzf "$work/$archive" -C "$work"
mkdir -p "$dir"
cp "$work/snello-$version-$os-$arch/$name" "$dir/$name.new"
chmod 755 "$dir/$name.new"
mv -f "$dir/$name.new" "$dir/$name"
say "installed snello $version into $dir"
case ":$PATH:" in
    *":$dir:"*) ;;
    *) say "$dir is not on your path: add export PATH=\"$dir:\$PATH\" to your shell's profile and open a new terminal" ;;
esac

"$dir/$name" version

# What a build still needs, offered: snello doctor says what is
# missing, and when clang is, the script asks and runs the system's
# own install, its package manager or Apple's Command Line Tools, so
# that one line ends with a machine that builds. The question goes through the terminal, since
# the standard input of a script piped into sh is the script; with
# SNELLO_YES set the answer is yes, for a build machine; with no
# terminal and no SNELLO_YES it is no. A sudo password, Apple's dialog
# and a package manager's own questions stay the developer's.
ask() {
    [ -z "${SNELLO_YES:-}" ] || return 0
    if ! (: </dev/tty) 2>/dev/null; then # the device exists with no terminal behind it where nobody sits
        say "no terminal to ask on: set SNELLO_YES=1 to answer yes, or run it yourself"
        return 1
    fi
    printf 'install.sh: %s [Y/n] ' "$1" >/dev/tty
    read -r answer </dev/tty || return 1 # the end of input is a no
    case $answer in
        [nN]*) return 1 ;;
        *) return 0 ;;
    esac
}

# have_clang reports whether a build has its clang: the one CLANG
# names or clang on the path, which on macOS is a stub before the
# Command Line Tools are installed, so there the tools are what count.
have_clang() {
    if [ "$os" = darwin ] && [ -z "${CLANG:-}" ]; then
        xcode-select -p >/dev/null 2>&1
    else
        command -v "${CLANG:-clang}" >/dev/null 2>&1
    fi
}

# clang_install prints the line that installs clang here, the system's
# package manager asked not to ask, without sudo for root, and nothing
# on a system it does not know.
clang_install() {
    case $os in
        darwin) echo "xcode-select --install" ;;
        linux)
            sudo=sudo
            [ "$(id -u)" != 0 ] || sudo=""
            case " $(. /etc/os-release 2>/dev/null && echo "${ID:-} ${ID_LIKE:-}") " in # Debian itself has no ID_LIKE
                *debian* | *ubuntu*) echo "${sudo:+$sudo }apt-get update && ${sudo:+$sudo }apt-get install -y clang" ;;
                *fedora* | *rhel*) echo "${sudo:+$sudo }dnf install -y clang" ;;
                *arch*) echo "${sudo:+$sudo }pacman -S --noconfirm clang" ;;
            esac
            ;;
    esac
}

if "$dir/$name" doctor; then
    exit 0
fi

line=$(clang_install)
if have_clang || [ -z "$line" ]; then
    exit 1 # doctor said what is missing and how to install it
fi
ask "clang is not installed, and a build needs it: install it now with $line?" || exit 1
say "running $line"
sh -c "$line" || fail "$line failed: run it yourself, then snello doctor"
if [ "$os" = darwin ]; then
    say "waiting for the Command Line Tools to finish installing; Ctrl-C stops waiting, and snello doctor says when they are in"
    waited=0
    until xcode-select -p >/dev/null 2>&1; do
        sleep 5
        waited=$((waited + 5))
        [ "$waited" -lt 1800 ] || fail "the Command Line Tools did not finish in thirty minutes: run snello doctor once they are in"
    done
fi
"$dir/$name" doctor # the last command: the script ends as doctor does
