$ErrorActionPreference = 'Stop'

if (-not (Test-Path -LiteralPath 'go.mod') -or -not (Test-Path -LiteralPath 'tests/unit/store')) {
    throw 'Запусти скрипт из корневой папки репозитория.'
}

$profilePath = 'coverage-db-unit.out'
$coveragePackage = 'activity-descriptor/internal/store'
$testPackages = @('./internal/store', './tests/unit/store')
$testArgs = @('-count=1', "-coverpkg=$coveragePackage", "-coverprofile=$profilePath") + $testPackages
$testOutput = & go test @testArgs 2>&1
if ($LASTEXITCODE -ne 0) {
    $testOutput
    exit $LASTEXITCODE
}
Write-Output 'Unit-тесты базы завершились успешно.'

$summary = & go tool cover "-func=$profilePath"
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}
$summary

$totalLine = $summary | Where-Object { $_ -match '^total:' } | Select-Object -Last 1
$match = [regex]::Match($totalLine, '([0-9]+(?:\.[0-9]+)?)%')
if (-not $match.Success) {
    throw 'Не удалось прочитать общий процент unit-покрытия.'
}
$coverage = [double]::Parse($match.Groups[1].Value, [Globalization.CultureInfo]::InvariantCulture)
if ($coverage -lt 70) {
    throw "Unit-покрытие логики хранения составляет $coverage%; требуется не менее 70%."
}
Write-Output "Unit-покрытие логики хранения: $coverage%."
