#!/bin/sh
# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Prepares a release in the working tree, for the pull request a
# maintainer opens: writes the version given into
# compiler/version/version.go, where snello version and the module's
# check read it, and the release's changelog into a file of its own,
# changelogs/VERSION.md, from the commits since the last release, or
# since the last tag of any kind for a pre-release, through
# changelog.sh, under the version given and today's date, the day the
# release was prepared, with a line for it at the top of the index,
# CHANGELOG.md, carrying the file's own date. It refuses a version that is not Semantic
# Versioning's MAJOR.MINOR.PATCH, with a pre-release after a hyphen
# allowed, 0.1.0-alpha.1, one that is tagged already, here or
# at origin, and one that has a changelog file or an index line
# already; it commits nothing and pushes nothing, writes nothing when a
# step fails, and ends by saying what the maintainer does next: read
# the file, open the pull request, and after its merge push the tag.
#
#	sh release/prepare.sh 0.2.0
set -eu

index=CHANGELOG.md
logs=changelogs
versions=compiler/version/version.go

fail() {
    echo "release/prepare.sh: $*" >&2
    exit 1
}

[ $# -eq 1 ] && [ -n "$1" ] || fail "usage: sh release/prepare.sh MAJOR.MINOR.PATCH, or MAJOR.MINOR.PATCH-PRERELEASE"
version=$1
echo "$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$' || fail "$version is not MAJOR.MINOR.PATCH, each number in decimal with no leading zero, with a pre-release after a hyphen at most, 0.1.0-alpha.1"
git fetch --tags --quiet origin 2>/dev/null || echo "release/prepare.sh: could not fetch the tags of origin; the tags known here are checked" >&2
if git rev-parse -q --verify "refs/tags/v$version" >/dev/null; then
    fail "v$version is tagged already: a version is never reused"
fi
log=$logs/$version.md
[ ! -e "$log" ] || fail "$log exists already"
if grep -q "^- \[$version\]" "$index"; then
    fail "$index lists $version already"
fi

# The changelog is written first, so that a failure leaves the tree as
# it was: the release's file, then its line at the top of the index's
# list, newest first, where the first line that begins a list item is,
# or at the end when the list is empty. The line reaches awk through
# the environment, which keeps every byte.
mkdir -p "$logs"
VERSION="$version" sh release/changelog.sh > "$log" || { rm -f "$log"; rmdir "$logs" 2>/dev/null; fail "could not write $log"; }
line="- [$version]($log) - $(sed -n '1s/^## \[.*\] - //p' "$log")"
if grep -q '^- \[' "$index"; then
    LINE="$line" awk '!done && /^- \[/ { print ENVIRON["LINE"]; done = 1 } { print }' "$index" > "$index.new"
else
    { cat "$index"; printf '%s\n' "$line"; } > "$index.new"
fi
grep -q "^- \[$version\]" "$index.new" || { rm -f "$index.new" "$log"; fail "could not write the line into $index"; }

sed -i.bak "s/^\tCurrent = \"[^\"]*\"/\tCurrent = \"$version\"/" "$versions" && rm "$versions.bak"
grep -q "^\tCurrent = \"$version\"" "$versions" || { rm -f "$index.new" "$log"; fail "could not write $version into $versions"; }
mv "$index.new" "$index"

echo "release/prepare.sh: $version written into $versions, its changelog into $log and its line into $index"
echo "next: read $log and keep what reads well; open the pull request, chore(release): prepare $version;"
echo "after its merge: git tag -a v$version -m 'snello $version' && git push origin v$version"
