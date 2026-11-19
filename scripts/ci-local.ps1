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
  Step "git diff --check" { git diff --check HEAD }
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

  $frontend = Join-Path $root "frontend"
  if (-not $SkipFrontend -and (Test-Path (Join-Path $frontend "package.json"))) {
    $npm = Get-Command -Name npm.cmd,npm -ErrorAction SilentlyContinue | Select-Object -First 1
    $node = Get-Command -Name node -ErrorAction SilentlyContinue
    if ($npm -and $node -and ((& $node.Source --version) -match '^v24\.')) {
      Push-Location $frontend
      try {
        Step "frontend install (Node 24)" { & $npm.Source ci --no-audit --no-fund }
        if ($LASTEXITCODE -eq 0) {
          Step "frontend lint" { & $npm.Source run lint }
          Step "frontend typecheck" { & $npm.Source run typecheck }
          Step "frontend tests" { & $npm.Source test }
          Step "frontend build" { & $npm.Source run build }
        }
      } finally { Pop-Location }
    } elseif (-not $SkipDocker -and (Get-Command docker -ErrorAction SilentlyContinue)) {
      Step "frontend install, lint, typecheck, tests and build (Docker Node 24)" {
        docker run --rm --mount "type=bind,source=$frontend,target=/source,readonly" -e CI=true node:24-alpine sh -ec '
          node --version
          mkdir /app
          tar -C /source --exclude="./node_modules" --exclude="./dist" --exclude="./coverage" --exclude="./.env*" --exclude="./.npmrc" -cf /tmp/frontend.tar .
          tar -C /app -xf /tmp/frontend.tar
          cd /app
          npm ci --no-audit --no-fund
          npm run lint
          npm run typecheck
          npm test
          npm run build
        '
      }
    } else {
      Step "frontend prerequisites" { throw "Для проверки фронтенда нужен Node.js 24 с npm или запущенный Docker" }
    }
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
