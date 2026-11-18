# The one image every make target runs in, and CI's Linux jobs with
# them: Debian, since the runtime uses glibc's thread and scheduler
# calls and the tests measure speed, which musl would change, pinned
# by its digest so that two builds are the same image; Go at the
# version go.mod names, passed in by the Makefile, from go.dev over
# HTTPS; clang and lld as Debian's stable release ships them; gcc as
# well, the cc of most Linux systems, which the C backend's tests build
# a set of programs with so that its warnings are met too; clang-format
# and black for make fmt, the C's and the Python's formatters; gdb, with
# its Python, which the debugger's test drives on Linux; the C library
# and libgcc of the other Linux processor, Debian's cross packages,
# which clang finds with no sysroot named, the binutils whose objdump
# reads that processor's code for the stacks' analysis, and qemu's user
# mode, which runs that processor's programs, so that a build for the
# other Linux is tested, bounded and run; and the tools the targets need, make, git, curl
# and its certificates.
FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2

ARG GO_VERSION
RUN other=$([ "$(dpkg --print-architecture)" = amd64 ] && echo arm64 || echo amd64) \
    && cpu=$([ "$other" = amd64 ] && echo x86-64 || echo aarch64) \
    && apt-get update \
    && apt-get install -y --no-install-recommends black ca-certificates curl clang clang-format lld gcc gdb make git \
        "libc6-dev-$other-cross" "libgcc-14-dev-$other-cross" "binutils-$cpu-linux-gnu" qemu-user \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-$(dpkg --print-architecture).tar.gz" | tar -C /usr/local -xz

ENV PATH=/usr/local/go/bin:$PATH \
    CC=clang \
    CLANG=clang

WORKDIR /src
