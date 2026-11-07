#!/bin/sh
# Runs a command inside the image build/Dockerfile describes, with the
# repository mounted at /src, as the user running it so that what the
# command writes under the repository, bin/snello, is the user's own
# on a Linux host as on a Mac, with the Go caches in directories under
# the user's cache so that a second run builds only what changed and
# CI can keep them between runs, and the compilers named as every
# build tool reads them; with NATIVE=1 set, runs the command on the
# host instead; Go is told not to stamp binaries with the repository's
# version control state, which git refuses to read from a mount whose
# owner it does not trust and which no build here needs. The
# Makefile's targets go through it, and so do CI's Linux jobs for the
# Go suite and the benchmarks, which are not the Makefile's business:
# it runs Snello.
set -eu
if [ -n "${NATIVE:-}" ]; then
    exec "$@"
fi

cache="${XDG_CACHE_HOME:-$HOME/.cache}/snello"
mkdir -p "$cache/go-build" "$cache/go-mod"
exec docker run --rm --user "$(id -u):$(id -g)" -v "$(pwd)":/src -v "$cache/go-build":/go-build -v "$cache/go-mod":/go-mod -w /src -e HOME=/tmp -e GOCACHE=/go-build -e GOMODCACHE=/go-mod -e GOFLAGS=-buildvcs=false -e CC=clang -e CLANG=clang snello-dev "$@"
