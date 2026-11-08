#!/bin/sh
# Runs the standard library's own tests, snello test over every
# package under std/ with a _test.snello file, those two levels down
# such as std/unicode/utf8 included, through both backends, stopping at
# the first package that fails. The compiler is bin/snello, which make
# build wrote.
set -eu
for dir in $(find std -name '*_test.snello' -exec dirname {} \; | sort -u); do
    for backend in llvm c; do
        echo "== $dir ($backend)"
        bin/snello test -v -backend "$backend" "$dir"
    done
done
