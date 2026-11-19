<#
.SYNOPSIS
  Runs the CI-equivalent checks locally when GitHub Actions is unavailable.

.DESCRIPTION
  Mirrors the important gates from .github/workflows/ci.yml:
  go build/vet/test, frontend lint/test/build, deploy config validation and
  compose configuration. Docker image build is opt-in because it is slow.

.PARAMETER SkipFrontend
  Skip frontend lint/test/build.

.PARAMETER SkipDocker
  Skip docker compose config validation.

.PARAMETER BuildDocker
  Also build the default API image and the full-stack server targets.

.EXAMPLE
  pwsh -ExecutionPolicy Bypass -File scripts/ci-local.ps1
#>
param(
  [switch]$SkipFrontend,
  [switch]$SkipDocker,
  [switch]$BuildDocker
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$failures = [System.Collections.Generic.List[string]]::new()
$results = [System.Collections.Generic.List[object]]::new()

function Step([string]$name, [scriptblock]$action) {
  $started = Get-Date
  Write-Host ""
  Write-Host "==> $name" -ForegroundColor Cyan
  try {
    & $action
    if ($LASTEXITCODE -ne 0) { throw "exit code $LASTEXITCODE" }
    $elapsed = (Get-Date) - $started
    $results.Add([pscustomobject]@{ Step=$name; Result="PASS"; Seconds=[math]::Round($elapsed.TotalSeconds,1) })
    Write-Host "PASS $name" -ForegroundColor Green
  } catch {
    $elapsed = (Get-Date) - $started
    $failures.Add("$name`: $($_.Exception.Message)")
    $results.Add([pscustomobject]@{ Step=$name; Result="FAIL"; Seconds=[math]::Round($elapsed.TotalSeconds,1) })
    Write-Host "FAIL $name`: $($_.Exception.Message)" -ForegroundColor Red
  }
}

Push-Location $root
try {
  if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw "go not found" }
  if (-not (Get-Command python -ErrorAction SilentlyContinue)) { throw "python not found" }

  Step "go build ./..." { go build ./... }
  Step "go vet ./..." { go vet ./... }
  Step "go test ./..." { go test ./... }
  Step "git diff --check" { git diff --check }
  Step "deploy config validation" { python scripts\check_deploy.py }

  if (-not $SkipDocker -and (Get-Command docker -ErrorAction SilentlyContinue)) {
    Step "docker compose config (default + profiles)" {
      docker compose config --quiet
      if ($LASTEXITCODE -ne 0) { return }
      docker compose --profile server config --quiet
      if ($LASTEXITCODE -ne 0) { return }
      docker compose --profile monitoring --profile automation config --quiet
    }
  } else {
    Write-Host "SKIP docker compose config (docker unavailable or -SkipDocker)" -ForegroundColor Yellow
  }

  if (-not $SkipFrontend -and (Test-Path "$root\frontend\package.json")) {
    if (-not (Get-Command npm.cmd -ErrorAction SilentlyContinue)) { throw "npm.cmd not found" }
    Push-Location "$root\frontend"
    try {
      Step "frontend lint" { npm.cmd run lint }
      Step "frontend tests" { npm.cmd test -- --run }
      Step "frontend build" { npm.cmd run build }
    } finally { Pop-Location }
  } else {
    Write-Host "SKIP frontend (missing package.json or -SkipFrontend)" -ForegroundColor Yellow
  }

  if ($BuildDocker -and (Get-Command docker -ErrorAction SilentlyContinue)) {
    Step "docker build default api image" { docker build -t activity-descriptor-api:local . }
    Step "docker build server target" { docker build --target server -t activity-descriptor-server:local . }
    Step "docker build web target" { docker build --target web -t activity-descriptor-web:local . }
  }

  Write-Host ""
  Write-Host "SUMMARY" -ForegroundColor Cyan
  $results | Format-Table -AutoSize | Out-String | Write-Host
  if ($failures.Count -gt 0) {
    Write-Host "LOCAL CI: FAIL ($($failures.Count) step(s))" -ForegroundColor Red
    $failures | ForEach-Object { Write-Host " - $_" -ForegroundColor Red }
    exit 1
  }
  Write-Host "LOCAL CI: PASS" -ForegroundColor Green
  exit 0
} finally {
  Pop-Location
}
