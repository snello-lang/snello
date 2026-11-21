#!/bin/sh
# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Builds a release from this checkout into dist/, natively and apart
# from the development image and the Makefile's tests, which are for
# working on the compiler: the compiler for each of the six platforms,
# each in an archive named by the version, the system and the
# processor, snello-0.1.0-linux-amd64.tar.gz, a zip for Windows,
# holding the binary, the LICENSE and the README in a directory of the
# archive's name; then SHA256SUMS over the archives. The version is
# read from compiler/version/version.go, where the pull request
# preparing the release raises it, and TAG, when set, must be that
# version after a v, which the workflow checks a tag by; the binary is
# stamped as the stable channel's.
#
# The archives are the same bit for bit wherever they are built, which
# the release workflow checks by building twice: the compiler is pure
# Go, built with no cgo, paths trimmed and the build ID cleared; every
# file in an archive carries the commit's time, no owner and one of
# two modes, in one order, and the gzip header holds no name or time.
# release/archive, a Go program of the repository's, writes the
# archives, so that no tar, gzip or zip of the system, and no zlib,
# has a say. Needs Go and git. With TAG set, the changelog must have
# the version's section, so that a tag not prepared fails in the
# first minute.
#
#	sh release/build.sh
set -eu

channel=stable
out=${OUT:-dist}
targets="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"
module=github.com/snello-lang/snello

fail() {
    echo "release/build.sh: $*" >&2
    exit 1
}

version=$(sed -n 's/^\tCurrent = "\([^"]*\)".*/\1/p' compiler/version/version.go)
[ -n "$version" ] || fail "no Current in compiler/version/version.go"
if [ -n "${TAG:-}" ]; then
    [ "$TAG" = "v$version" ] || fail "the tag is $TAG and compiler/version/version.go says $version: the pull request preparing the release raises them together"
    grep -q "^## \[$version\]" CHANGELOG.md || fail "CHANGELOG.md has no section for $version: release/prepare.sh writes it"
fi

sum="sha256sum"
command -v sha256sum >/dev/null 2>&1 || sum="shasum -a 256"
epoch=$(git log -1 --format=%ct)

[ "$out" != / ] || fail "OUT is /"
rm -rf "$out"
mkdir -p "$out"
for target in $targets; do
    os=${target%/*}
    arch=${target#*/}
    dir="snello-$version-$os-$arch"
    stage="$out/$dir"
    bin=snello
    [ "$os" = windows ] && bin=snello.exe
    mkdir -p "$stage"
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -buildid= -X $module/compiler/version.channel=$channel" -o "$stage/$bin" ./cmd/snello
    cp LICENSE README.md "$stage/"
    form=tar
    [ "$os" = windows ] && form=zip
    go run ./release/archive "$out" "$dir" "$epoch" "$form"
    rm -r "$stage"
done

(cd "$out" && $sum ./*.tar.gz ./*.zip | sed 's#^\([^ ]*\)  \./#\1  #' > SHA256SUMS)
echo "release/build.sh: $version for $(echo "$targets" | wc -w | tr -d ' ') platforms in $out/, from $(git log -1 --format=%h)"
