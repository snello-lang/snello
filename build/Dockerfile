# The one image every make target runs in, and CI's Linux jobs with
# them: Debian, since the runtime uses glibc's thread and scheduler
# calls and the tests measure speed, which musl would change, pinned
# by its digest so that two builds are the same image; Go at the
# version go.mod names, passed in by the Makefile, from go.dev over
# HTTPS; clang and lld as Debian's stable release ships them; and the
# tools the targets need, make, git, curl and its certificates.
FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2

ARG GO_VERSION
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl clang lld make git \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-$(dpkg --print-architecture).tar.gz" | tar -C /usr/local -xz

ENV PATH=/usr/local/go/bin:$PATH \
    CC=clang \
    CLANG=clang

WORKDIR /src
