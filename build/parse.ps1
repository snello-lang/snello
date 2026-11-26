# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Parses every PowerShell script the repository tracks, with the parser
# of the PowerShell that runs it, and runs none of them: under
# powershell it holds the scripts to Windows PowerShell 5.1's grammar,
# which the one-line install promises, and under pwsh to PowerShell
# 7's. Each error is printed as a GitHub annotation, at its file and
# line, and any error exits 1.

$failed = $false
foreach ($file in (git ls-files '*.ps1')) {
    $errors = $null
    [System.Management.Automation.Language.Parser]::ParseFile((Resolve-Path $file).Path, [ref]$null, [ref]$errors) | Out-Null
    foreach ($e in $errors) {
        $message = $e.Message -replace '%', '%25' -replace "`r", '%0D' -replace "`n", '%0A' # the escapes an annotation's message needs
        Write-Output "::error file=$file,line=$($e.Extent.StartLineNumber)::$message"
        $failed = $true
    }
}

if ($failed) { exit 1 }
