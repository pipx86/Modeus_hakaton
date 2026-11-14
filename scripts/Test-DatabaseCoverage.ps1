$ErrorActionPreference = 'Stop'

if (-not (Test-Path -LiteralPath 'go.mod') -or -not (Test-Path -LiteralPath 'tests/database')) {
    throw 'Запустите скрипт из корневой папки репозитория.'
}

foreach ($name in @('STORE_TEST_DATABASE_URL', 'OUTBOX_TEST_DATABASE_URL', 'VECTOR_TEST_DATABASE_URL')) {
    if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($name))) {
        throw "Перед запуском задайте переменную $name для отдельной тестовой БД."
    }
}

$profilePath = 'coverage-db.out'
$coveragePackages = './internal/store,./internal/domain,./internal/demofill,./internal/outbox,./internal/webhook'
$testPackages = @(
    './internal/store/...',
    './internal/domain/...',
    './internal/demofill/...',
    './internal/outbox/...',
    './internal/webhook/...',
    './cmd/data-audit/...',
    './tests/database/...'
)

$testArgs = @('-count=1', "-coverprofile=$profilePath", "-coverpkg=$coveragePackages") + $testPackages
$testOutput = & go test @testArgs 2>&1
if ($LASTEXITCODE -ne 0) {
	$testOutput
    exit $LASTEXITCODE
}
Write-Output 'Все тесты завершились успешно.'

$summary = & go tool cover "-func=$profilePath"
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}
$summary

$totalLine = $summary | Where-Object { $_ -match '^total:' } | Select-Object -Last 1
$match = [regex]::Match($totalLine, '([0-9]+(?:\.[0-9]+)?)%')
if (-not $match.Success) {
    throw 'Не удалось прочитать общий процент покрытия.'
}
$coverage = [double]::Parse($match.Groups[1].Value, [Globalization.CultureInfo]::InvariantCulture)
if ($coverage -lt 70) {
    throw "Покрытие логики БД составляет $coverage%; требуется не менее 70%."
}
