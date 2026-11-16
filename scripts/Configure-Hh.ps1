param([string]$TokenFile)
$ErrorActionPreference = 'Stop'
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw 'Этот способ защищённого хранения предназначен для Windows.' }
$token = $env:HH_ACCESS_TOKEN
if (-not $token -and $TokenFile) { $token = [IO.File]::ReadAllText((Resolve-Path -LiteralPath $TokenFile)).Trim() }
if (-not $token) { throw 'Нужен существующий HH_ACCESS_TOKEN в окружении или -TokenFile с путём к файлу действующего токена. Новый токен не запрашивается.' }
$agent = $env:HH_USER_AGENT
if (-not $agent) { $agent = 'ModeusResearch/0.1 (https://github.com/pipx86/Modeus_hakaton)' }
# Сохраняется только действующий токен; запросов выдачи и отзыва токенов здесь нет.
$directory = Join-Path $env:LOCALAPPDATA 'ModeusProject'
New-Item -ItemType Directory -Force -Path $directory | Out-Null
$path = Join-Path $directory 'hh-auth.xml'
[pscustomobject]@{Token=(ConvertTo-SecureString -String $token -AsPlainText -Force);UserAgent=$agent} | Export-Clixml -LiteralPath $path
$token = $null
Write-Output "Действующий токен сохранён в зашифрованных настройках: $path"
