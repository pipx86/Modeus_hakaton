$ErrorActionPreference = 'Stop'

$repo = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$psql = 'D:\PostgreSQL\16\bin\psql.exe'
$goose = 'C:\Users\REDMI\go\bin\goose.exe'
$dbHost = '127.0.0.1'
$dbPort = '5432'
$dbUser = 'postgres'
$dbName = 'modeus_project'
$dataDrive = 'D:\'

if (-not (Test-Path -LiteralPath $psql)) { throw 'Не найден psql в D:\PostgreSQL\16\bin.' }
if (-not (Test-Path -LiteralPath $goose)) { throw 'Не найден Goose в C:\Users\REDMI\go\bin.' }
if (-not (Test-Path -LiteralPath (Join-Path $repo 'migrations/optional\vector'))) {
    throw 'Не найдена папка необязательных миграций pgvector.'
}
if (-not (Test-Path -LiteralPath 'D:\PostgreSQL\16\lib\vector.dll') -or -not (Test-Path -LiteralPath 'D:\PostgreSQL\16\share\extension\vector.control')) {
    throw 'Расширение pgvector не установлено в PostgreSQL на диске D.'
}

$securePassword = Read-Host 'Введите пароль PostgreSQL для локальной базы (ввод скрыт)' -AsSecureString
$passwordPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($securePassword)
$previousPassword = $env:PGPASSWORD
try {
    $env:PGPASSWORD = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($passwordPointer)
    $preflightSql = "SELECT current_database(), current_setting('data_directory'), EXISTS(SELECT 1 FROM pg_available_extensions WHERE name='vector'), EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='sources');"
    $preflight = & $psql -w -h $dbHost -p $dbPort -U $dbUser -d $dbName -A -t -F '|' -v ON_ERROR_STOP=1 -c $preflightSql
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось проверить локальную базу PostgreSQL.' }
    $fields = ($preflight | Out-String).Trim().Split('|')
    if ($fields.Count -ne 4 -or $fields[0] -ne $dbName) { throw 'Подключение открыло не ожидаемую базу проекта.' }
    $dataPath = $fields[1].Replace('/', '\')
    if (-not $dataPath.StartsWith($dataDrive, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Каталог данных PostgreSQL находится вне D-диска: $dataPath"
    }
    if ($fields[2] -ne 't') { throw 'PostgreSQL не видит установленное расширение pgvector.' }
    if ($fields[3] -ne 't') { throw 'В выбранной базе нет основной схемы проекта; сначала примените основные миграции.' }

    $dsn = "postgres://$dbUser@$dbHost`:$dbPort/$dbName`?sslmode=disable"
    & $goose -table goose_vector_versions -dir (Join-Path $repo 'migrations/optional\vector') postgres $dsn up
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось применить миграции поиска.' }

    $verifySql = "SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='vector') AND to_regclass('public.source_fragments') IS NOT NULL AND to_regclass('public.fragment_embeddings') IS NOT NULL;"
    $verified = & $psql -w -h $dbHost -p $dbPort -U $dbUser -d $dbName -A -t -v ON_ERROR_STOP=1 -c $verifySql
    if ($LASTEXITCODE -ne 0 -or ($verified | Out-String).Trim() -ne 't') {
        throw 'После миграции не найдены таблицы векторного поиска.'
    }
    Write-Output "Необязательные миграции поиска применены к $dbName; каталог PostgreSQL находится на D:."
}
finally {
	if ($null -eq $previousPassword) {
		Remove-Item Env:PGPASSWORD -ErrorAction SilentlyContinue
	} else {
		$env:PGPASSWORD = $previousPassword
	}
    [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($passwordPointer)
    $securePassword.Dispose()
}
