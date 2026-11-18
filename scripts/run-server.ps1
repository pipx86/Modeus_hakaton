<#
.SYNOPSIS
  Starts cmd/server with local env loaded from .env and .secrets/llm.env.

.DESCRIPTION
  Agents (interviewer / analyst / synthesizer) are in-process libraries of
  cmd/server, not separate containers. This script only merges local env files
  and starts the backend; secrets stay out of git.

.PARAMETER Port
  HTTP port, default 8080.

.PARAMETER EnvFiles
  Additional dotenv files to load after .env and .secrets/llm.env.
#>
param(
  [int]$Port = 8080,
  [string[]]$EnvFiles = @()
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$files = @((Join-Path $root ".env"), (Join-Path $root ".secrets\llm.env")) + $EnvFiles

foreach ($file in $files) {
  if (-not (Test-Path -LiteralPath $file)) { continue }
  foreach ($line in Get-Content -LiteralPath $file) {
    if ($line -match '^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$') {
      $name = $matches[1]
      $value = $matches[2].Trim()
      if ($value.Length -ge 2 -and (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'")))) {
        $value = $value.Substring(1, $value.Length - 2)
      }
      Set-Item -Path "Env:$name" -Value $value
    }
  }
}

if (-not $env:DATABASE_URL) {
  Write-Error "DATABASE_URL is not set. Put it into .env or .secrets/llm.env."
}
if (-not $env:JWT_SECRET) {
  Write-Error "JWT_SECRET is not set. Put it into .env or .secrets/llm.env."
}
$env:HTTP_ADDR = "127.0.0.1:$Port"
Write-Host "Starting cmd/server on http://127.0.0.1:$Port (agents in-process)"
Push-Location $root
try {
  go run ./cmd/server
} finally {
  Pop-Location
}
