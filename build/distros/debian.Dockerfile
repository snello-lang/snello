# Debian's stable release as a developer has it: bare, with nothing to
# build with, and with clang, the one package a build needs, which
# brings cc with it and links with link-time optimization as it comes.
FROM debian:trixie AS bare
COPY snello /usr/local/bin/snello
COPY check.sh /usr/local/bin/snello-check

FROM bare AS clang
RUN apt-get update \
    && apt-get install -y --no-install-recommends clang \
    && rm -rf /var/lib/apt/lists/*
