#!/bin/sh
# Checks the first five minutes of a developer on each distribution
# whose install command snello doctor names: builds snello for Linux,
# once for each processor, and for each distribution under
# build/distros an image with nothing to build with and one with
# clang, and runs build/distros/check.sh in each, which also holds the
# messages of build, run and test to the distribution's install
# command. Arch publishes no arm64 image, so it runs as amd64, under
# emulation on an arm64 machine. DISTROS names fewer, as
# DISTROS="debian fedora". The images, snello-distro-*, are kept, so
# that the next run builds only what changed; docker image rm removes
# them. It needs Docker and Go on the host.
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP
host=$(go env GOARCH)
cp "$root/build/distros/check.sh" "$work/check.sh"
for distro in ${DISTROS:-debian ubuntu fedora arch}; do
    goarch=$host
    case $distro in
    debian | ubuntu) fix="sudo apt install clang" ;;
    fedora) fix="sudo dnf install clang" ;;
    arch)
        fix="sudo pacman -S clang"
        goarch=amd64
        ;;
    *)
        echo "build/distros.sh: no distribution $distro" >&2
        exit 2
        ;;
    esac
    if [ ! -f "$work/snello-$goarch" ]; then
        (cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH=$goarch go build -o "$work/snello-$goarch" ./cmd/snello)
    fi
    cp "$work/snello-$goarch" "$work/snello"
    for target in bare clang; do
        image=snello-distro-$distro-$target
        echo "== $distro, $target"
        docker build -q --platform "linux/$goarch" --target "$target" -f "$root/build/distros/$distro.Dockerfile" -t "$image" "$work" >/dev/null
        docker run --rm --platform "linux/$goarch" -e FIX="$fix" "$image" snello-check "$target"
    done
done
