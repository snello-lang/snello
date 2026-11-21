#!/bin/sh
# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# snello check over every example program that has its expected
# output beside it, which is the examples suite's own rule for a
# program that is finished; one without is pending on a library it
# waits for. Then the two examples of several packages, and the
# programs the backends' tests share. The compiler is bin/snello,
# which make build wrote.
set -eu
for file in examples/*/*.snello; do
    if [ -e "${file%.snello}.out" ]; then
        bin/snello check "$file"
    fi
done

for dir in examples/packaged examples/bound; do
    bin/snello check "$dir"
done

for file in compiler/emit/internal/backendtest/testdata/*.snello; do
    bin/snello check "$file"
done
