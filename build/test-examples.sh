#!/bin/sh
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
