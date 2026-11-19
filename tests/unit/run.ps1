$ErrorActionPreference = 'Stop'

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Set-Location $repoRoot

if (-not (Test-Path -LiteralPath 'go.mod')) {
    throw 'В корне backend-репозитория не найден go.mod.'
}

$namesToClear = @(
    'DATABASE_URL',
    'TEST_DATABASE_URL',
    'STORE_TEST_DATABASE_URL',
    'VECTOR_TEST_DATABASE_URL',
    'OUTBOX_TEST_DATABASE_URL',
    'INGEST_TEST_DATABASE_URL',
    'DATABASE_LIVE_EVAL_TEST',
    'HH_ACCESS_TOKEN',
    'HH_CLIENT_ID',
    'HH_CLIENT_SECRET',
    'LLM_API_KEY',
    'OPENROUTER_API_KEY',
    'OLLAMA_URL',
    'OTEL_EXPORTER_OTLP_ENDPOINT',
    'OLLAMA_TEST_URL',
    'OLLAMA_EMBED_MODEL',
    'MODEUS_ACCEPTANCE_EXCLUSIVE'
)
$savedValues = @{}
foreach ($name in $namesToClear) {
    $savedValues[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
    [Environment]::SetEnvironmentVariable($name, $null, 'Process')
}

try {
    $packages = & go list ./...
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось определить пакеты Go.' }
    $unitPackages = @($packages | Where-Object { $_ -ne 'activity-descriptor/tests/database' })
    $output = & go test -count=1 -skip '^TestOfflineE2E$' $unitPackages 2>&1
    $exitCode = $LASTEXITCODE
}
finally {
    foreach ($name in $namesToClear) {
        [Environment]::SetEnvironmentVariable($name, $savedValues[$name], 'Process')
    }
}

$output
if ($exitCode -ne 0) {
    exit $exitCode
}
Write-Output 'Все unit-тесты backend завершились успешно. PostgreSQL и Ollama не подключались.'
