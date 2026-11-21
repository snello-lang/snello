#!/bin/sh
# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Formats the sources in place: the Go files with gofmt, the C runtime,
# its headers and the C the examples bind with clang-format under the
# repository's .clang-format, and the Python, the debuggers' script,
# with black, under its own defaults. Run before every commit; CI's
# gofmt check holds the Go files to it. The files are found by name,
# not asked of git, since the repository is mounted into the image
# under another owner. Snello files have no formatter yet and follow
# gofmt's layout by hand: tabs, one statement a line, the braces where
# Go puts them. unicode.c and pow10.c are written by mkunicode.go and
# mkpow10.go and keep their generators' layout.
set -eu
gofmt -w $(find . -name '*.go' -not -path './bin/*' -not -path './.git/*')
clang-format -i $(find runtime examples \( -name '*.c' -o -name '*.h' \) -not -name unicode.c -not -name pow10.c)
black --quiet $(find . -name '*.py' -not -path './bin/*' -not -path './.git/*')
