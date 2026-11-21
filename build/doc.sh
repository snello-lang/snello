#!/bin/sh
# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Shows what snello doc prints, each form and flag once, with the
# command line above what it printed: a standard package's overview, a
# type and a method, a package by the last element of its path, a
# trait with its implementors, a predeclared name, a name looked for
# without its package, a name several packages have, which is an error
# naming them, the unexported names with -u, every comment with -all,
# and a package of a program's own with an example on its page. The
# compiler is bin/snello, which make build wrote.
set -eu
snello="$(pwd)/bin/snello"

# show prints a command line and runs it, saying how it exited when it
# failed, as the name several packages have does.
show() {
    echo
    echo "\$ snello doc $*"
    "$snello" doc "$@" || echo "(exit $?)"
}

show strings
show strings.Builder
show strings.Builder.WriteString
show json
show io.Reader
show builtin.Option
show len
show Reader
show -u strings
show -all strings

cd examples/packaged
show shapes
show shapes.Describe
