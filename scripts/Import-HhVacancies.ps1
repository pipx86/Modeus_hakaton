param([Parameter(Mandatory=$true)][string]$Query,[ValidateRange(1,20)][int]$Pages=1)
$ErrorActionPreference = 'Stop'
$path = Join-Path $env:LOCALAPPDATA 'ModeusProject\hh-auth.xml'
if (-not (Test-Path -LiteralPath $path)) { throw 'Сначала настройте токен через Configure-Hh.ps1.' }
$config = Import-Clixml -LiteralPath $path
if (-not ($config.Token -is [Security.SecureString])) { throw 'Неверный формат защищённых настроек.' }
$previousToken = $env:HH_ACCESS_TOKEN
$previousAgent = $env:HH_USER_AGENT
try {
    # Токен передаётся дочернему процессу через окружение, а не через аргументы командной строки.
    $env:HH_ACCESS_TOKEN = [Net.NetworkCredential]::new('', $config.Token).Password
    $env:HH_USER_AGENT = $config.UserAgent
    Push-Location (Split-Path -Parent $PSScriptRoot)
    try { & go run ./cmd/ingest vacancies -query $Query -pages $Pages; if ($LASTEXITCODE -ne 0) { throw "Импорт завершился с кодом $LASTEXITCODE" } }
    finally { Pop-Location }
} finally {
    $env:HH_ACCESS_TOKEN = $previousToken
    $env:HH_USER_AGENT = $previousAgent
}
