#!/bin/sh
# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Runs the tests of the example packages that have them, snello test
# over every directory under examples/ with a _test.snello file,
# through both backends.
set -eu
for test in $(find examples -name '*_test.snello' | sort); do
    dir=$(dirname "$test")
    for backend in llvm c; do
        echo "== $dir ($backend)"
        bin/snello test -v -backend "$backend" "$dir"
    done
done
