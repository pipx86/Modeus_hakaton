param([string]$DatabaseRepository = (Join-Path $PSScriptRoot '..\..\Modeus_hakaton'))
$ErrorActionPreference = 'Stop'
$backend = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$database = (Resolve-Path -LiteralPath $DatabaseRepository).Path
if (-not (Test-Path -LiteralPath (Join-Path $database 'migrations\20261112000001_evidence_reference.sql'))) { throw 'Нужна актуальная рабочая папка ветки БД с миграцией ссылки на подтверждение.' }
Get-Command goose -ErrorAction Stop | Out-Null
$compose = Join-Path $PSScriptRoot 'acceptance.compose.yaml'
$artifacts = Join-Path $backend ('data\acceptance\' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
New-Item -ItemType Directory -Force -Path $artifacts | Out-Null
$project = 'modeus-check-' + [Guid]::NewGuid().ToString('N').Substring(0,10)
$previousDb=$env:MODEUS_DB_PATH; $previousBackend=$env:MODEUS_BACKEND_PATH; $previousArtifacts=$env:MODEUS_ARTIFACTS_PATH
$env:MODEUS_DB_PATH=$database; $env:MODEUS_BACKEND_PATH=$backend; $env:MODEUS_ARTIFACTS_PATH=$artifacts
$watch=[Diagnostics.Stopwatch]::StartNew()
$passed=$false
try {
    & docker compose -f $compose -p $project up -d --wait database
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось запустить отдельную тестовую БД.' }
    $address = (& docker compose -f $compose -p $project port database 5432).Trim()
    if ($LASTEXITCODE -ne 0 -or $address -notmatch '^127\.0\.0\.1:(\d+)$') { throw 'Не удалось определить порт тестовой БД.' }
    $dsn = 'postgres://postgres:acceptance-only@127.0.0.1:' + $Matches[1] + '/acceptance?sslmode=disable'
    & goose -dir (Join-Path $database 'migrations') postgres $dsn up
    if ($LASTEXITCODE -ne 0) { throw 'Ошибка основных миграций.' }
    & goose -table goose_vector_versions -dir (Join-Path $database 'migrations_optional\vector') postgres $dsn up
    if ($LASTEXITCODE -ne 0) { throw 'Ошибка поисковых миграций.' }
    & docker compose -f $compose -p $project run --rm checks
    if ($LASTEXITCODE -ne 0) { throw 'Приёмочные проверки завершились ошибкой; смотрите сохранённые логи.' }
    $passed=$true
} finally {
    $watch.Stop()
    [pscustomobject]@{Passed=$passed;Seconds=[Math]::Round($watch.Elapsed.TotalSeconds,2);Project=$project;SyntheticAgents=$true;RealHH=$false;RealOllama=$false} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $artifacts 'summary.json') -Encoding utf8
    # Удаляется только созданный этим запуском тестовый проект Compose.
    & docker compose -f $compose -p $project down --volumes
    $env:MODEUS_DB_PATH=$previousDb; $env:MODEUS_BACKEND_PATH=$previousBackend; $env:MODEUS_ARTIFACTS_PATH=$previousArtifacts
    Write-Output "Результаты проверки: $artifacts"
}
