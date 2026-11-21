#!/bin/sh
# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# A release's dry run on this machine, publishing nothing: the six
# archives built into dist/ by build.sh, as the workflow builds them,
# then this machine's archive unpacked into a directory of its own and
# smoke.sh run with it alone on the path, as the workflow runs it on
# each platform. What it prints is what the workflow would see.
#
#	sh release/dry-run.sh
set -eu

out=${OUT:-dist}

sh release/build.sh
version=$(sed -n 's/^\tCurrent = "\([^"]*\)".*/\1/p' compiler/version/version.go)
os=$(go env GOOS)
arch=$(go env GOARCH)
name="snello-$version-$os-$arch"
unpacked=$(mktemp -d)
trap 'rm -rf "$unpacked"' EXIT
if [ "$os" = windows ]; then
    if command -v unzip >/dev/null 2>&1; then unzip -q "$out/$name.zip" -d "$unpacked"; else 7z x -y -o"$unpacked" "$out/$name.zip" >/dev/null; fi
else
    tar -xzf "$out/$name.tar.gz" -C "$unpacked"
fi
echo "release/dry-run.sh: $name unpacked into $unpacked, on the path alone with the system's compilers"
PATH="$unpacked/$name:$PATH" sh release/smoke.sh
echo "release/dry-run.sh: the release of $version would pass on $os/$arch"
