$ErrorActionPreference = 'Stop'

Set-Location (Join-Path $PSScriptRoot '..')

$gitDir = (git rev-parse --git-dir) 2>$null
if (-not $gitDir) {
    Write-Error 'Not a git repository.'
    exit 1
}

$hooksDir = Join-Path $gitDir 'hooks'
$hookPath = Join-Path $hooksDir 'pre-commit'

$hookScript = @(
    '#!/bin/sh',
    'REPO_ROOT="$(git rev-parse --show-toplevel)"',
    'HOOK_SCRIPT="$REPO_ROOT/scripts/hooks/gofmt-pre-commit.sh"',
    'exec "$HOOK_SCRIPT"'
) -join "`n"

New-Item -ItemType Directory -Force -Path $hooksDir | Out-Null
[System.IO.File]::WriteAllText($hookPath, $hookScript, [System.Text.Encoding]::ASCII)

Write-Output "Pre-commit hook installed successfully at $hookPath"
