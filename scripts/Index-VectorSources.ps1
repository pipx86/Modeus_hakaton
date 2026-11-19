<#
.SYNOPSIS
  Backfills the semantic index (source_fragments + fragment_embeddings) with
  real Ollama embeddings for sources that do not have vectors yet.

.DESCRIPTION
  Loads local .env and .secrets/llm.env without printing any values, applies the
  same Ollama defaults as cmd/server, then runs `go run ./cmd/index-sources`.
  The command talks to Ollama /api/embed only; no chat LLM is called.

  Required: DATABASE_URL (from env files or the current environment), a running
  Ollama with the embedding model pulled, and the optional
  migrations_optional/vector package applied. See docs/EMBEDDINGS.md.

.PARAMETER EnvFiles
  Additional dotenv files to load after .env and .secrets/llm.env.

.PARAMETER Arguments
  Flags forwarded to cmd/index-sources, for example -Limit 10, -DryRun, -All,
  -Source <uuid>, -Timeout 5m, -AllowFailures.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File .\scripts\Index-VectorSources.ps1 -Limit 10

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File .\scripts\Index-VectorSources.ps1 -DryRun
#>
param(
  [string[]]$EnvFiles = @(),
  [Parameter(ValueFromRemainingArguments = $true)]
  [string[]]$Arguments = @()
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$files = @((Join-Path $root '.env'), (Join-Path $root '.secrets\llm.env')) + $EnvFiles

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
  Write-Error 'DATABASE_URL is not set. Put it into .env or .secrets/llm.env.'
}
if (-not $env:OLLAMA_URL) { $env:OLLAMA_URL = 'http://127.0.0.1:11434' }
if (-not $env:OLLAMA_EMBED_MODEL) { $env:OLLAMA_EMBED_MODEL = 'nomic-embed-text' }

Write-Host "Backfilling semantic index: ollama=$env:OLLAMA_URL model=$env:OLLAMA_EMBED_MODEL"
Push-Location $root
try {
  go run ./cmd/index-sources @Arguments
  $code = $LASTEXITCODE
} finally {
  Pop-Location
}
if ($code -ne 0) {
  Write-Error "cmd/index-sources failed with exit code $code"
}
