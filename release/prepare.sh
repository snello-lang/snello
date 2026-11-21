#!/bin/sh
# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Prepares a release in the working tree, for the pull request a
# maintainer opens: writes the version given into
# compiler/version/version.go, where snello version and the module's
# check read it, and the release's section into CHANGELOG.md, at the
# top under the preamble, from the commits since the last tag through
# changelog.sh, under the version given and today's date, the day the
# release was prepared. It refuses a version that is not Semantic
# Versioning's MAJOR.MINOR.PATCH, with a pre-release after a hyphen
# allowed, 0.1.0-alpha.1, one that is tagged already, here or
# at origin, and one the changelog has a section for; it commits
# nothing and pushes nothing, writes nothing when a step fails, and ends by
# saying what the maintainer does next: read the section, open the
# pull request, and after its merge push the tag.
#
#	sh release/prepare.sh 0.2.0
set -eu

changelog=CHANGELOG.md
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
if grep -q "^## \[$version\]" "$changelog"; then
    fail "$changelog has a section for $version already"
fi

# The section is written first, into a file beside the changelog, so
# that a failure leaves the tree as it was; it goes after the preamble,
# before the first section, or at the end when there is none yet. The
# text reaches awk through the environment, which keeps every byte;
# a value given with -v would have its backslashes read as escapes.
section=$(VERSION="$version" sh release/changelog.sh)
if grep -q '^## ' "$changelog"; then
    SECTION="$section" awk '!done && /^## / { print ENVIRON["SECTION"]; print ""; done = 1 } { print }' "$changelog" > "$changelog.new"
else
    { cat "$changelog"; echo; printf '%s\n' "$section"; } > "$changelog.new"
fi
grep -q "^## \[$version\]" "$changelog.new" || { rm -f "$changelog.new"; fail "could not write the section into $changelog"; }

sed -i.bak "s/^\tCurrent = \"[^\"]*\"/\tCurrent = \"$version\"/" "$versions" && rm "$versions.bak"
grep -q "^\tCurrent = \"$version\"" "$versions" || { rm -f "$changelog.new"; fail "could not write $version into $versions"; }
mv "$changelog.new" "$changelog"

echo "release/prepare.sh: $version written into $versions and its section into $changelog"
echo "next: read the section and keep what reads well; open the pull request, chore(release): prepare $version;"
echo "after its merge: git tag -a v$version -m 'snello $version' && git push origin v$version"
