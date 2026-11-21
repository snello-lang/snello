# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Installs Snello on Windows from a release, in one line, in PowerShell
# 5.1 or later:
#
#	irm https://raw.githubusercontent.com/snello-lang/snello/main/release/install.ps1 | iex
#
# It finds the processor, downloads the archive of the latest release,
# or of the version SNELLO_VERSION names, or of the newest pre-release
# while no release exists yet, and SHA256SUMS beside it, refuses an
# archive the list does not vouch for, checks the archive's provenance
# when the GitHub CLI is installed and signed in and says so when it is
# not, puts snello.exe into %LOCALAPPDATA%\Programs\snello, or into
# SNELLO_INSTALL, adds that directory to the user's Path when it is not
# there, and ends by running snello doctor, which says what a build
# still needs and how to install it. Run again, it upgrades; removing
# the directory uninstalls.
# The preferences are the session's under iex, so they are set for the
# script and put back at its end.
$preferences = @{ ErrorAction = $ErrorActionPreference; Progress = $ProgressPreference }
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue' # the progress bar makes a download crawl on PowerShell 5.1

$repo = 'snello-lang/snello'
$api = "https://api.github.com/repos/$repo/releases"
$download = "https://github.com/$repo/releases/download"
$dir = if ($env:SNELLO_INSTALL) { $env:SNELLO_INSTALL } else { Join-Path $env:LOCALAPPDATA 'Programs\snello' }
$version = $env:SNELLO_VERSION

function Say($text) { Write-Host "install.ps1: $text" }
# Fail ends the script with a terminating error, never with exit, which
# under iex would close the terminal the script was pasted into.
function Fail($text) { throw "install.ps1: $text" }

# A 32-bit PowerShell on a 64-bit Windows names the real processor in
# PROCESSOR_ARCHITEW6432.
$processor = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
$arch = switch ($processor) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { Fail "$processor is not a processor a release is built for: x86-64 and arm64" }
}
if ($PSVersionTable.PSVersion.Major -lt 6) {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
}

# The version: the one named, else the latest release, else the newest
# pre-release while there is no release yet, since GitHub's latest
# release is never a pre-release.
if ($version) {
    $tag = 'v' + $version.TrimStart('v')
} else {
    $tag = $null
    try { $tag = (Invoke-RestMethod "$api/latest").tag_name } catch { $tag = $null }
    if (-not $tag) {
        $newest = @(Invoke-RestMethod "${api}?per_page=1") # ? is a character of a variable's name, so $api is braced
        if ($newest.Count -eq 0) { Fail "no release found at https://github.com/$repo/releases" }
        $tag = $newest[0].tag_name
        Say "no release yet: installing the newest pre-release, $($tag.TrimStart('v'))"
    }
}
$version = $tag.TrimStart('v')
$archive = "snello-$version-windows-$arch.zip"

$work = Join-Path ([IO.Path]::GetTempPath()) ('snello-install-' + [IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $work | Out-Null
try {
    Say "downloading $archive from $download/$tag"
    try {
        Invoke-WebRequest "$download/$tag/$archive" -OutFile (Join-Path $work $archive) -UseBasicParsing
        Invoke-WebRequest "$download/$tag/SHA256SUMS" -OutFile (Join-Path $work 'SHA256SUMS') -UseBasicParsing
    } catch {
        Fail "release $tag has no $archive or no SHA256SUMS: https://github.com/$repo/releases/tag/$tag"
    }

    $listed = Get-Content (Join-Path $work 'SHA256SUMS') | Where-Object { $_ -match "\s$([regex]::Escape($archive))$" }
    $expected = if ($listed) { ($listed -split '\s+')[0].ToLower() } else { '' }
    $actual = (Get-FileHash (Join-Path $work $archive) -Algorithm SHA256).Hash.ToLower()
    if (-not $expected -or $actual -ne $expected) {
        Fail "$archive does not match the release's SHA256SUMS: it is not the file the release published"
    }
    Say 'checksum verified against SHA256SUMS'

    # gh writes to its standard error even when it succeeds, which
    # PowerShell before 7.2 turns into a terminating error under Stop,
    # so the two calls run under Continue and are judged by their exit
    # codes.
    $gh = Get-Command gh -ErrorAction SilentlyContinue
    $signedIn = $false
    $ErrorActionPreference = 'Continue'
    if ($gh) {
        & gh auth status *> $null
        $signedIn = ($LASTEXITCODE -eq 0)
    }
    $verified = $false
    if ($signedIn) {
        & gh attestation verify (Join-Path $work $archive) --repo $repo *> $null
        $verified = ($LASTEXITCODE -eq 0)
    }
    $ErrorActionPreference = 'Stop'
    if ($signedIn -and -not $verified) { Fail "$archive's provenance does not verify: it was not built by $repo's release workflow" }
    if ($verified) {
        Say "provenance verified: built by $repo's release workflow"
    } else {
        Say 'provenance not checked: the GitHub CLI, gh, signed in, checks it with gh attestation verify'
    }

    # The binary is copied beside its place and renamed into it, the one
    # there renamed aside first, since Windows lets a running program be
    # renamed but not written over; a copy that fails leaves the old one.
    Expand-Archive (Join-Path $work $archive) -DestinationPath $work -Force
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
    $binary = Join-Path $dir 'snello.exe'
    Copy-Item (Join-Path $work "snello-$version-windows-$arch\snello.exe") "$binary.new" -Force
    if (Test-Path $binary) { Move-Item $binary "$binary.old" -Force }
    Move-Item "$binary.new" $binary -Force
    Remove-Item "$binary.old" -Force -ErrorAction SilentlyContinue
    Say "installed snello $version into $dir"

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (($userPath -split ';') -notcontains $dir) {
        [Environment]::SetEnvironmentVariable('Path', "$dir;$userPath", 'User')
        Say "added $dir to your Path: open a new terminal to run snello by name"
    }
    $env:Path = "$dir;$env:Path"

    & $binary version
    & $binary doctor
    if ($LASTEXITCODE -ne 0) { Fail 'snello is installed; snello doctor says what a build still needs, above' }
} finally {
    Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
    $ErrorActionPreference = $preferences.ErrorAction
    $ProgressPreference = $preferences.Progress
}
