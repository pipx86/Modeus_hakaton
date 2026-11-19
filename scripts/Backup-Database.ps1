param(
    [Parameter(Mandatory)] [string]$Output,
    [string]$PgBin
)

$ErrorActionPreference = 'Stop'
foreach ($name in 'PGHOST', 'PGPORT', 'PGUSER', 'PGDATABASE') {
    if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($name))) {
        throw "Задайте $name для подключения к базе."
    }
}

if ($PgBin) {
    $dump = Join-Path $PgBin 'pg_dump.exe'
    $restore = Join-Path $PgBin 'pg_restore.exe'
} else {
    $dump = (Get-Command pg_dump -CommandType Application).Source
    $restore = (Get-Command pg_restore -CommandType Application).Source
}

$destination = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Output)
if ([IO.File]::Exists($destination) -or [IO.Directory]::Exists($destination)) {
    throw "Путь назначения уже занят: $destination"
}
if (-not [IO.Directory]::Exists([IO.Path]::GetDirectoryName($destination))) {
    throw 'Сначала создайте каталог для резервной копии.'
}

$partial = $destination + '.partial-' + [Guid]::NewGuid().ToString('N')
try {
    # libpq читает пароль из PGPASSFILE или защищённого окружения.
    & $dump --no-password --format=custom "--file=$partial"
    if ($LASTEXITCODE -ne 0) { throw "pg_dump завершился с кодом $LASTEXITCODE." }
    & $restore --list $partial | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось прочитать оглавление резервной копии.' }
    [IO.File]::Move($partial, $destination)
    Get-Item -LiteralPath $destination | Select-Object FullName, Length
} finally {
    if ([IO.File]::Exists($partial)) {
        Remove-Item -LiteralPath $partial
    }
}
