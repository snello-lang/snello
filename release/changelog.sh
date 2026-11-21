#!/bin/sh
# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Writes a release's section of CHANGELOG.md, in the Keep a Changelog
# form, from the Conventional Commits since the last tag, or since the
# tag given, as Rust's release notes are drafted from its merged pull
# requests and Go's from its release-note fragments: the version from
# compiler/version/version.go, or VERSION when set, and today's date;
# then the breaking changes first, a commit whose type carries ! or
# whose body has the footer BREAKING CHANGE, hyphenated or not; then
# Added from feat, Fixed from fix, Changed from perf and Removed from
# revert, each commit as its subject without its
# type, its scope in bold before it, and its pull request's number
# kept, which GitHub links in the notes. docs, test, ci, chore and
# refactor are left out, since they change nothing a developer sees.
# One git log and one awk: a few hundred commits take no time. The
# maintainer preparing a release pastes the section at the top of
# CHANGELOG.md under the preamble, reads it, and keeps what reads
# well; the release workflow refuses a changelog without the version's
# section.
#
#	sh release/changelog.sh > section.md
#	sh release/changelog.sh v0.1.0 # the commits since v0.1.0
set -eu

since=${1:-$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null || true)}
version=${VERSION:-$(sed -n 's/^\tCurrent = "\([^"]*\)".*/\1/p' compiler/version/version.go)}
[ -n "$version" ] || { echo "release/changelog.sh: no Current in compiler/version/version.go" >&2; exit 1; }
[ "$(git rev-list --count "${since:+$since..}HEAD")" != 0 ] || echo "release/changelog.sh: no commit since ${since:-the start}: the section is empty" >&2
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
        line = "- " (scope != "" ? "**" scope "**: " : "") toupper(substr(text, 1, 1)) substr(text, 2)
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
