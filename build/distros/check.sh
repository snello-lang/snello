#!/bin/sh
# Runs inside an image build/distros.sh makes of a distribution, as
# check.sh bare or check.sh clang. With clang, the one package a build
# needs, a developer's first five minutes pass: snello doctor, then a
# module snello new starts, run, run for a debugger, and tested,
# through both backends. With nothing to build with, snello doctor
# fails and names the install command FIX names for the distribution,
# and build, run and test, through both backends, say that line and
# nothing else.
set -eu
mode=$1
cd "$(mktemp -d)"
fail() {
    echo "$*" >&2
    exit 1
}
case $mode in
clang)
    snello doctor
    snello new hello
    cd hello
    for backend in llvm c; do
        said=$(snello run -backend "$backend" .)
        [ "$said" = "Hello, Snello!" ] || fail "snello run -backend $backend printed: $said"
        said=$(snello run -backend "$backend" -debug .)
        [ "$said" = "Hello, Snello!" ] || fail "snello run -backend $backend -debug printed: $said"
        snello test -backend "$backend" .
    done
    ;;
bare)
    if said=$(snello doctor); then
        fail "snello doctor passed with nothing to build with"
    fi
    case $said in
    *"install it with $FIX, or set CLANG"*) ;;
    *) fail "snello doctor said: $said" ;;
    esac
    snello new hello
    cd hello
    for backend in llvm c; do
        compiler=clang variable=CLANG
        if [ "$backend" = c ]; then
            compiler=cc variable=CC
        fi
        for command in build run test; do
            if said=$(snello "$command" -backend "$backend" . 2>&1); then
                fail "snello $command -backend $backend passed with nothing to build with"
            fi
            [ "$said" = "$compiler not found: install it with $FIX, or set $variable" ] ||
                fail "snello $command -backend $backend said: $said"
        done
    done
    ;;
*)
    echo "usage: check.sh bare|clang" >&2
    exit 2
    ;;
esac
echo "$mode: ok"
