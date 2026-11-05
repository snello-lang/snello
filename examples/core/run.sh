#!/bin/sh
# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Builds the compiler from this tree, then builds and runs every
# positive core program and compares what it prints with the .out file
# beside it, with the files of name.files copied beside the binary
# first. This is the one-script check of the core suite; go test ./...
# runs the same programs, as release and as debug builds.
#
#     sh examples/core/run.sh

set -u
root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

go build -o "$work/snello" "$root/cmd/snello" || exit 1
status=0
for src in "$root"/examples/core/*.snello; do
    name=$(basename "$src" .snello)
    dir="$work/$name"
    mkdir -p "$dir"
    if [ -d "$root/examples/core/$name.files" ]; then
        cp "$root/examples/core/$name.files"/* "$dir/"
    fi

    if (cd "$dir" && "$work/snello" build "$src" && "./$name" > out.txt) && diff "$root/examples/core/$name.out" "$dir/out.txt"; then
        echo "ok   $name"
    else
        echo "FAIL $name"
        status=1
    fi
done

exit $status
