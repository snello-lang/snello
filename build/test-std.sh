#!/bin/sh
# Runs the standard library's own tests, snello test over every
# package under std/ with a _test.snello file, through both backends,
# stopping at the first package that fails. The compiler is bin/snello,
# which make build wrote.
set -eu
for dir in std/*/; do
    if ls "$dir"*_test.snello >/dev/null 2>&1; then
        for backend in llvm c; do
            echo "== $dir ($backend)"
            bin/snello test -v -backend "$backend" "$dir"
        done
    fi
done
