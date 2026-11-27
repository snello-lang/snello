#!/bin/sh
# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Writes a release's changelog, the file changelogs/VERSION.md, in the
# Keep a Changelog form, from the Conventional Commits since the last release, or since
# the last tag of any kind for a pre-release, or since the tag given,
# as Rust's release notes are drafted from its merged pull requests
# and Go's from its release-note fragments: the version from
# compiler/version/version.go, or VERSION when set, and today's date;
# then the breaking changes first, a commit whose type carries ! or
# whose body has the footer BREAKING CHANGE, hyphenated or not; then
# Added from feat, Fixed from fix, Changed from perf and Removed from
# revert, each commit as its subject without its
# type, its scope in bold before it, and its pull request's number
# kept, which GitHub links in the notes. docs, test, ci, chore and
# refactor are left out, since they change nothing a developer sees.
# A pull request merged with a commit of its own of the same subject is
# listed once, by the merge, which carries the pull request's number.
# One git log and one awk: a few hundred commits take no time.
# prepare.sh writes it into the release's file and the maintainer
# reads it and keeps what reads well; the release workflow refuses a
# tag whose version has no file.
#
#	sh release/changelog.sh > changelogs/0.2.0.md
#	sh release/changelog.sh v0.1.0 # the commits since v0.1.0
set -eu

version=${VERSION:-$(sed -n 's/^\tCurrent = "\([^"]*\)".*/\1/p' compiler/version/version.go)}
[ -n "$version" ] || { echo "release/changelog.sh: no Current in compiler/version/version.go" >&2; exit 1; }
# A release's changelog covers everything since the last release, the
# pre-releases between them included; a pre-release's covers what came
# since the last tag of any kind.
case $version in
    *-*) last=$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null || true) ;;
    *) last=$(git tag -l 'v*' | grep -v -- - | sort -V | tail -1) ;;
esac
since=${1:-$last}
[ "$(git rev-list --count "${since:+$since..}HEAD")" != 0 ] || echo "release/changelog.sh: no commit since ${since:-the start}: the changelog is empty" >&2
unit=$(printf '\037')   # parts a commit's hash, subject and body
record=$(printf '\036') # ends a commit

git log --format="%h$unit%s$unit%b$record" "${since:+$since..}HEAD" |
    awk -v version="$version" -v date="$(date -u +%Y-%m-%d)" -v RS="$record" -F"$unit" '
    function add(heading, line) { sections[heading] = sections[heading] line "\n" }
    NF >= 2 {
        subject = $2; body = $3
        type = subject; sub(/[(!:].*/, "", type)
        scope = ""
        if (match(subject, /^[a-z]+\([^)]*\)/)) { scope = substr(subject, RSTART, RLENGTH); sub(/^[a-z]+\(/, "", scope); sub(/\)$/, "", scope) }
        text = subject; sub(/^[a-z]+(\([^)]*\))?!?: /, "", text)
        key = subject; sub(/ \(#[0-9]+\)$/, "", key)
        if (key in seen) next # a pull request merged with its commit of the same subject: the newer, the merge with its number, stands for both
        seen[key] = 1
        first = text; sub(/ .*/, "", first)
        if (first ~ /^[a-z]+(-[a-z]+)*[,:;]?$/) text = toupper(substr(text, 1, 1)) substr(text, 2) # a word starts the line with a capital; a name, install.ps1 or go.mod, stays as written
        line = "- " (scope != "" ? "**" scope "**: " : "") text
        if (subject ~ /^[a-z]+(\([^)]*\))?!: / || body ~ /(^|\n)BREAKING[ -]CHANGE: /) add("Breaking changes", line)
        else if (type == "feat") add("Added", line)
        else if (type == "fix") add("Fixed", line)
        else if (type == "perf") add("Changed", line)
        else if (type == "revert") add("Removed", line)
    }
    END {
        printf "## [%s] - %s\n", version, date
        n = split("Breaking changes,Added,Fixed,Changed,Removed", order, ",")
        for (i = 1; i <= n; i++) if (order[i] in sections) printf "\n### %s\n\n%s", order[i], sections[order[i]]
    }'
