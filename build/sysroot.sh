#!/bin/sh
# Writes the system files a build for another Linux needs into DIR, a
# sysroot that SNELLO_SYSROOT_linux_amd64 or SNELLO_SYSROOT_linux_arm64
# then names: the C library's headers, start files and libraries, the
# dynamic loader they name, the kernel's headers, which live under
# usr/lib/linux, and the compiler's support library, libgcc, as
# Debian's stable release ships them for the processor named, in an
# image of that processor's run under Docker's emulation when it is not
# the host's. Debian's own image, at the digest build/Dockerfile pins,
# so that a program built against the sysroot runs in the image the
# tests run in. Usage: build/sysroot.sh linux/amd64|linux/arm64 DIR
set -eu

image=debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2

if [ $# -ne 2 ]; then
    echo "usage: build/sysroot.sh linux/amd64|linux/arm64 DIR" >&2
    exit 2
fi

case "$1" in
linux/amd64) multiarch=x86_64-linux-gnu ;;
linux/arm64) multiarch=aarch64-linux-gnu ;;
*)
    echo "build/sysroot.sh: unknown target $1: linux/amd64 or linux/arm64" >&2
    exit 2
    ;;
esac

mkdir -p "$2"
out=$(cd "$2" && pwd)
docker run --rm --platform "$1" -v "$out":/out -e MULTIARCH="$multiarch" -e OWNER="$(id -u):$(id -g)" "$image" sh -c '
set -eu
apt-get update -qq
apt-get install -y -qq --no-install-recommends libc6-dev libgcc-14-dev >/dev/null
loader=$(cd / && ls -d usr/lib/ld-linux-* usr/lib64 2>/dev/null || true)
tar -C / -cf - usr/include usr/lib/linux "usr/lib/$MULTIARCH" "usr/lib/gcc/$MULTIARCH" $loader | tar -C /out -xf -
ln -sfn usr/lib /out/lib
if [ -d /out/usr/lib64 ]; then ln -sfn usr/lib64 /out/lib64; fi
chown -R "$OWNER" /out
'
echo "build/sysroot.sh: wrote the $1 sysroot to $out"
