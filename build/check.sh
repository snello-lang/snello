#!/bin/sh
# snello check over every example program that has its expected
# output, which is the examples suite's own rule for a program that is
# finished; one without is pending on a library it waits for. The
# compiler is bin/snello, which make build wrote.
set -eu
for file in examples/*.snello; do
    name=$(basename "$file" .snello)
    if [ -e "examples/expected/$name.out" ]; then
        bin/snello check "$file"
    fi
done

for file in examples/run/*.snello; do
    bin/snello check "$file"
done
