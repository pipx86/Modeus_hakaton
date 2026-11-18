#Requires -Version 5.1
<#
.SYNOPSIS
    Issues demo JWTs for the admin and guest users via cmd/test-users.
.DESCRIPTION
    Requires JWT_SECRET (the same signing secret as the API). Prints ready-to-use
    VITE_API_TOKEN lines for PowerShell and bash/zsh. The guest role is read-only.
    The script is ASCII-only so Windows PowerShell 5.1 reads it without a BOM.
.EXAMPLE
    .\scripts\Test-Users.ps1
.EXAMPLE
    .\scripts\Test-Users.ps1 -Role admin
.EXAMPLE
    .\scripts\Test-Users.ps1 -Json
#>
param(
    [ValidateSet('admin', 'guest')]
    [string]$Role = 'guest',
    [switch]$Json
)

$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($env:JWT_SECRET)) {
    throw 'JWT_SECRET is not set: use the same signing secret as the API.'
}

$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot
try {
    $raw = & go run ./cmd/test-users -json
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
    $data = $raw | ConvertFrom-Json
    if ($Json) {
        $raw
        return
    }

    $user = $data.users | Where-Object { $_.role -eq $Role } | Select-Object -First 1
    if (-not $user) {
        throw "No demo user with role '$Role' in the command output."
    }
    $note = ''
    if ($user.read_only) {
        $note = ' (read-only)'
    }
    Write-Host "# subject=$($user.subject) role=$($user.role) ttl=$($data.ttl)$note"
    Write-Output "`$env:VITE_API_TOKEN = '$($user.token)'"
    Write-Output "export VITE_API_TOKEN='$($user.token)'"
}
finally {
    Pop-Location
}
