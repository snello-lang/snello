# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Arch Linux as a developer has it: bare, with nothing to build with,
# and with clang, the one package a build needs, which brings gcc as cc
# and links with link-time optimization as it comes. Arch publishes no
# arm64 image, so build/distros.sh runs it as amd64. Inside Docker,
# pacman's sandbox fails, error restricting syscalls via seccomp, and
# is turned off. Arch installs only onto a current system, so clang's
# install upgrades the image's packages with it, and the upgrade of
# openssl to openssl-3 meets the files /usr/lib/libcrypto.so.3 and
# libssl.so.3, which both packages hold, and which the overwrite lets
# the newer one take.
FROM archlinux:latest AS bare
COPY snello /usr/local/bin/snello
COPY check.sh /usr/local/bin/snello-check

FROM bare AS clang
RUN sed -i 's/^\[options\]/[options]\nDisableSandbox/' /etc/pacman.conf \
    && pacman -Syu --noconfirm --overwrite '*' clang \
    && pacman -Scc --noconfirm
