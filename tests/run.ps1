param(
    [switch]$Coverage,
    [switch]$Short
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    Write-Host '== go test ./... ==' -ForegroundColor Cyan
    if ($Short) {
        go test -short ./...
    } else {
        go test ./...
    }
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    Write-Host '== go vet ./... ==' -ForegroundColor Cyan
    go vet ./...
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    if ($Coverage) {
        Write-Host '== coverage ==' -ForegroundColor Cyan
        go test -count=1 -coverprofile coverage.out ./...
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        go tool cover -func coverage.out | Select-Object -Last 1
    }

    Write-Host '== offline e2e ==' -ForegroundColor Cyan
    go run ./cmd/e2e -out e2e-output.json
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
} finally {
    Pop-Location
}
