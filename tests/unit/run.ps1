$ErrorActionPreference = 'Stop'

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Set-Location $repoRoot

if (-not (Test-Path -LiteralPath 'go.mod')) {
    throw 'В корне backend-репозитория не найден go.mod.'
}

$namesToClear = @(
    'INGEST_TEST_DATABASE_URL',
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
    $output = & go test -count=1 ./... 2>&1
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
