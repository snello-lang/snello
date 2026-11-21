#!/bin/sh
# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# A developer's first five minutes from a release archive, which the
# release workflow runs on a runner of each platform before it
# publishes, with nothing but the unpacked archive on the path and the
# system's clang: snello version and snello doctor, snello new, then
# the program run and tested through both backends, release and debug,
# in a directory of its own, each run held to what the program prints.
set -eu

greeting="Hello, Snello!"

fail() {
    echo "release/smoke.sh: $*" >&2
    exit 1
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"
snello version
snello doctor
snello new hello
cd hello
for backend in llvm c; do
    for mode in "" -debug; do
        said=$(snello run -backend "$backend" $mode .)
        [ "$said" = "$greeting" ] || fail "snello run -backend $backend $mode printed: $said"
    done
    snello test -backend "$backend" .
done
echo "release/smoke.sh: the first five minutes pass"
