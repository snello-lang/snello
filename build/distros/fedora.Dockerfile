# Fedora's latest release as a developer has it: bare, with nothing to
# build with, and with clang, the one package a build needs, which
# brings gcc as cc and links with link-time optimization as it comes.
FROM fedora:latest AS bare
COPY snello /usr/local/bin/snello
COPY check.sh /usr/local/bin/snello-check

FROM bare AS clang
RUN dnf install -y clang && dnf clean all
