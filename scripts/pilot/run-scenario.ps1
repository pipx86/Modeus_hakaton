#requires -Version 5.1
<#
.SYNOPSIS
    Прогон пилота: создаёт исследование по профессии и программе, отправляет
    ответы интервью (если передан транскрипт), ждёт финализации и проверяет
    tree / conflicts / review summary / program coverage.

.DESCRIPTION
    Скрипт выполняет API-сценарий карточки 28 и печатает чек-лист PASS/FAIL.
    Токен берётся из параметра -Token или из переменной окружения MODEUS_TOKEN
    и никогда не записывается в файл.

    -DryRun печатает план и завершается с кодом 0, не обращаясь к сети.

.PARAMETER BaseUrl
    Базовый адрес backend, по умолчанию http://127.0.0.1:8080.

.PARAMETER Token
    Bearer-токен. Если не задан, используется $env:MODEUS_TOKEN.

.PARAMETER Profession
    Профессия из decision record. Обязательна вне -DryRun.

.PARAMETER ProgramId
    UUID образовательной программы. Обязателен вне -DryRun.

.PARAMETER TranscriptPath
    Необязательный файл ответов интервью: одна строка — один ответ, UTF-8.
    Пустые строки и строки с # игнорируются; префиксы И:/Q: пропускаются,
    Р:/Респондент: срезаются.

.PARAMETER DryRun
    Напечатать план без сетевых вызовов и выйти с кодом 0.

.EXAMPLE
    pwsh -NoProfile -File scripts/pilot/run-scenario.ps1 -DryRun `
      -Profession "Техник-механик" -ProgramId "00000000-0000-0000-0000-000000000000"

.EXAMPLE
    $env:MODEUS_TOKEN = "<admin-token>"
    pwsh -NoProfile -File scripts/pilot/run-scenario.ps1 `
      -BaseUrl "http://127.0.0.1:8080" `
      -Profession "Техник-механик" `
      -ProgramId "<program_id>" `
      -TranscriptPath ".\pilot\interview-R-01.txt"

.NOTES
    Коды выхода: 0 — все проверки PASS; 1 — есть FAIL; 2 — ошибка параметров.
#>
[CmdletBinding()]
param(
    [string]$BaseUrl = "http://127.0.0.1:8080",
    [string]$Token = $env:MODEUS_TOKEN,
    [string]$Profession,
    [string]$ProgramId,
    [string]$TranscriptPath,
    [switch]$DryRun
)

$ErrorActionPreference = "Stop"

try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }
if ($PSVersionTable.PSVersion.Major -lt 6) {
    try { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 } catch { }
}

if ([string]::IsNullOrWhiteSpace($Token)) { $Token = $env:MODEUS_TOKEN }

$script:BaseUrl = $BaseUrl.Trim().TrimEnd("/")
$script:Token = $Token
$script:Profession = $Profession
$script:ProgramId = $ProgramId
$script:TranscriptPath = $TranscriptPath
$script:RunId = $null
$script:TreeNodes = @()
$script:Checks = New-Object System.Collections.ArrayList

function Get-Prop {
    param($Object, [string]$Name)
    if ($null -eq $Object) { return $null }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { return $null }
    return $property.Value
}

function Get-Array {
    param($Object, [string]$Name)
    $value = Get-Prop -Object $Object -Name $Name
    if ($null -eq $value) { return @() }
    return @($value)
}

function Format-ApiError {
    param($Result)
    $parts = New-Object System.Collections.ArrayList
    if ($null -ne $Result) {
        if ($Result.StatusCode -gt 0) { [void]$parts.Add("HTTP $($Result.StatusCode)") }
        if (-not [string]::IsNullOrWhiteSpace([string]$Result.Code)) { [void]$parts.Add([string]$Result.Code) }
        if (-not [string]::IsNullOrWhiteSpace([string]$Result.Message)) { [void]$parts.Add([string]$Result.Message) }
    }
    if ($parts.Count -eq 0) { return "нет ответа API" }
    return ($parts -join " · ")
}

function Invoke-Api {
    param(
        [Parameter(Mandatory = $true)][string]$Method,
        [Parameter(Mandatory = $true)][string]$Path,
        [object]$Body = $null
    )

    $uri = $script:BaseUrl + $Path
    $request = @{
        Method           = $Method
        Uri              = $uri
        Headers          = @{
            Authorization = "Bearer $script:Token"
            Accept        = "application/json"
        }
        UseBasicParsing  = $true
        TimeoutSec       = 120
    }

    if ($null -ne $Body) {
        $json = $Body | ConvertTo-Json -Depth 10 -Compress
        $request["ContentType"] = "application/json; charset=utf-8"
        $request["Body"] = [System.Text.Encoding]::UTF8.GetBytes($json)
    }

    try {
        $response = Invoke-WebRequest @request
        $data = $null
        if (-not [string]::IsNullOrWhiteSpace([string]$response.Content)) {
            try { $data = $response.Content | ConvertFrom-Json }
            catch { $data = $response.Content }
        }
        return [pscustomobject]@{
            Ok         = $true
            StatusCode = [int]$response.StatusCode
            Code       = ""
            Message    = ""
            Data       = $data
        }
    }
    catch {
        $status = 0
        $code = ""
        $message = $_.Exception.Message
        $raw = ""

        if ($null -ne $_.Exception.Response) {
            try { $status = [int]$_.Exception.Response.StatusCode } catch { $status = 0 }
        }
        if ($null -ne $_.ErrorDetails -and -not [string]::IsNullOrWhiteSpace([string]$_.ErrorDetails.Message)) {
            $raw = [string]$_.ErrorDetails.Message
        }
        if ([string]::IsNullOrWhiteSpace($raw) -and $null -ne $_.Exception.Response) {
            try {
                $stream = $_.Exception.Response.GetResponseStream()
                if ($null -ne $stream) {
                    $reader = New-Object System.IO.StreamReader -ArgumentList $stream
                    $raw = $reader.ReadToEnd()
                    $reader.Close()
                }
            }
            catch { }
        }
        if (-not [string]::IsNullOrWhiteSpace($raw)) {
            try {
                $parsed = $raw | ConvertFrom-Json
                $parsedCode = [string](Get-Prop -Object $parsed -Name "code")
                $parsedMessage = [string](Get-Prop -Object $parsed -Name "message")
                if (-not [string]::IsNullOrWhiteSpace($parsedCode)) { $code = $parsedCode }
                if (-not [string]::IsNullOrWhiteSpace($parsedMessage)) { $message = $parsedMessage }
            }
            catch { $message = $raw.Trim() }
        }

        return [pscustomobject]@{
            Ok         = $false
            StatusCode = $status
            Code       = $code
            Message    = $message
            Data       = $null
        }
    }
}

function Write-Step {
    param([string]$Text)
    Write-Host ""
    Write-Host ("--- " + $Text) -ForegroundColor Cyan
}

function Write-Info {
    param([string]$Text)
    Write-Host ("    " + $Text) -ForegroundColor Gray
}

function Write-Warn {
    param([string]$Text)
    Write-Host ("!   " + $Text) -ForegroundColor Yellow
}

function Add-Check {
    param(
        [Parameter(Mandatory = $true)][string]$Id,
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][bool]$Passed,
        [string]$Detail = ""
    )

    $status = "FAIL"
    $color = "Red"
    if ($Passed) {
        $status = "PASS"
        $color = "Green"
    }

    Write-Host ("[{0}] {1} {2}" -f $status, $Id, $Name) -ForegroundColor $color
    if (-not [string]::IsNullOrWhiteSpace($Detail)) {
        Write-Host ("       " + $Detail) -ForegroundColor DarkGray
    }

    [void]$script:Checks.Add([pscustomobject]@{
        Id     = $Id
        Name   = $Name
        Status = $status
        Detail = $Detail
    })
}

function Write-Summary {
    $failed = @($script:Checks | Where-Object { $_.Status -eq "FAIL" })
    $passed = @($script:Checks | Where-Object { $_.Status -eq "PASS" })

    Write-Host ""
    Write-Host "=== ИТОГ ===" -ForegroundColor Cyan
    Write-Host ("PASS: {0}   FAIL: {1}" -f $passed.Count, $failed.Count)
    if (-not [string]::IsNullOrWhiteSpace([string]$script:RunId)) {
        Write-Host ("run_id: " + $script:RunId)
    }

    if ($failed.Count -gt 0) {
        Write-Host "Проваленные проверки:" -ForegroundColor Red
        foreach ($item in $failed) {
            Write-Host (" - " + $item.Id + " " + $item.Name + ": " + $item.Detail) -ForegroundColor Red
        }
        return 1
    }

    Write-Host "Все проверки пройдены." -ForegroundColor Green
    return 0
}

function Stop-WithSummary {
    $code = Write-Summary
    exit $code
}

function Read-TranscriptAnswers {
    param([Parameter(Mandatory = $true)][string]$Path)

    $raw = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    $answers = New-Object System.Collections.ArrayList
    if ($null -eq $raw) { return @() }

    foreach ($line in ($raw -split '\r?\n')) {
        $text = $line.Trim()
        if ($text.Length -eq 0) { continue }
        if ($text.StartsWith("#")) { continue }
        # Строки интервьюера не отправляются как ответы.
        if ($text -match '^(интервьюер|interviewer|и|q)\s*[:\-—–]\s*') { continue }
        $text = $text -replace '^(респондент|respondent|р|r)\s*[:\-—–]\s*', ''
        $text = $text.Trim()
        if ($text.Length -gt 0) { [void]$answers.Add($text) }
    }

    return @($answers)
}

function Send-Answers {
    param([Parameter(Mandatory = $true)][string[]]$Answers)

    $sent = 0
    $finished = $false
    $errorMessage = ""
    $perAnswerTimeoutMinutes = 6

    for ($i = 0; $i -lt $Answers.Count; $i++) {
        if ($finished) { break }

        $answer = $Answers[$i]
        $deadline = (Get-Date).AddMinutes($perAnswerTimeoutMinutes)
        $posted = $false

        while ((Get-Date) -lt $deadline) {
            $response = Invoke-Api -Method POST -Path ("/api/v1/runs/" + $script:RunId + "/messages") -Body @{ content = $answer }

            if ($response.Ok) {
                $sent++
                $posted = $true
                Write-Info ("ответ {0}/{1} отправлен" -f ($i + 1), $Answers.Count)
                break
            }

            if ($response.StatusCode -eq 409) {
                if ($response.Code -eq "SESSION_FINISHED") {
                    $finished = $true
                    break
                }
                if ($response.Code -eq "SESSION_INTERRUPTED") {
                    $errorMessage = Format-ApiError -Result $response
                    break
                }
                # QUESTION_PENDING / CHAT_CONFLICT: интервьюер ещё готовит вопрос.
                Start-Sleep -Seconds 3
                continue
            }

            if ($response.StatusCode -eq 0) {
                # Сетевой сбой: повторяем, пока не истечёт окно ответа.
                Start-Sleep -Seconds 5
                continue
            }

            $errorMessage = Format-ApiError -Result $response
            break
        }

        if ($finished) { break }
        if (-not $posted -and [string]::IsNullOrWhiteSpace($errorMessage)) {
            $errorMessage = "ответ $($i + 1) не отправлен за $perAnswerTimeoutMinutes мин"
        }
        if (-not $posted) { break }

        Start-Sleep -Seconds 1
    }

    return [pscustomobject]@{
        Sent     = $sent
        Total    = $Answers.Count
        Finished = $finished
        Error    = $errorMessage
    }
}

function Wait-Finalization {
    param(
        [int]$TimeoutMinutes = 90,
        [int]$PollSeconds = 5
    )

    $started = Get-Date
    $lastLine = ""

    while ($true) {
        $response = Invoke-Api -Method GET -Path ("/api/v1/runs/" + $script:RunId)

        if (-not $response.Ok) {
            if ($response.StatusCode -eq 404) {
                return [pscustomobject]@{
                    Ok      = $false
                    Status  = ""
                    Stage   = ""
                    Timeout = $false
                    Error   = Format-ApiError -Result $response
                }
            }
            Write-Warn ("не удалось прочитать статус: " + (Format-ApiError -Result $response) + "; повтор")
            Start-Sleep -Seconds $PollSeconds
            continue
        }

        $status = [string](Get-Prop -Object $response.Data -Name "status")
        $stage = [string](Get-Prop -Object $response.Data -Name "stage")
        $line = "status=$status stage=$stage"
        if ($line -ne $lastLine) {
            Write-Info $line
            $lastLine = $line
        }

        if ($status -eq "done" -or $status -eq "degraded" -or $status -eq "failed") {
            return [pscustomobject]@{
                Ok      = $true
                Status  = $status
                Stage   = $stage
                Timeout = $false
                Error   = ""
            }
        }

        if (((Get-Date) - $started).TotalMinutes -ge $TimeoutMinutes) {
            return [pscustomobject]@{
                Ok      = $false
                Status  = $status
                Stage   = $stage
                Timeout = $true
                Error   = "таймаут $TimeoutMinutes мин"
            }
        }

        Start-Sleep -Seconds $PollSeconds
    }
}

function Show-DryRunPlan {
    Write-Host ""
    Write-Host "=== ПЛАН ПРОГОНА (DryRun) ===" -ForegroundColor Cyan
    Write-Host "Сеть не используется; API-вызовы не выполняются."
    Write-Host ("BaseUrl:       " + $script:BaseUrl)
    if ([string]::IsNullOrWhiteSpace($script:Profession)) {
        Write-Host "Profession:    <не задана; обязательна для реального прогона>"
    }
    else {
        Write-Host ("Profession:    " + $script:Profession)
    }
    if ([string]::IsNullOrWhiteSpace($script:ProgramId)) {
        Write-Host "ProgramId:     <не задан; обязателен для реального прогона>"
    }
    else {
        Write-Host ("ProgramId:     " + $script:ProgramId)
    }
    if ($script:Token) { Write-Host "Token:         задан (значение не печатается)" }
    else { Write-Host "Token:         не задан (нужен -Token или `$env:MODEUS_TOKEN)" }

    if (-not [string]::IsNullOrWhiteSpace($script:TranscriptPath)) {
        Write-Host ("Transcript:    " + $script:TranscriptPath)
        if (Test-Path -LiteralPath $script:TranscriptPath) {
            try {
                $preview = @(Read-TranscriptAnswers -Path $script:TranscriptPath)
                Write-Host ("Ответов в файле: " + $preview.Count)
                if ($preview.Count -eq 0) {
                    Write-Warn "В транскрипте не найдено ни одного ответа — реальный прогон остановится."
                }
            }
            catch {
                Write-Warn ("Не удалось прочитать транскрипт: " + $_.Exception.Message)
            }
        }
        else {
            Write-Warn "Файл транскрипта не найден — реальный прогон остановится на проверке параметров."
        }
    }
    else {
        Write-Host "Transcript:    не передан (живое интервью в UI)"
    }

    Write-Host ""
    Write-Host "Шаги:"
    $steps = @(
        "1.  GET  /api/v1/health                                  -> 200, status=ok",
        "2.  GET  /api/v1/ready                                   -> 200, problems=[]",
        "3.  GET  /api/v1/academic/programs/{ProgramId}           -> 200, >=3 компетенции, >=5 KSA",
        "4.  POST /api/v1/runs                                    -> 201, result_kind=program, stage=interview",
        "5.  POST /api/v1/runs/{id}/messages (если есть транскрипт) -> 201 на каждый ответ",
        "6.  GET  /api/v1/runs/{id} (поллинг)                     -> done",
        "7.  GET  /api/v1/runs/{id}/tree                          -> 200, nodes, evidence",
        "8.  GET  /api/v1/runs/{id}/conflicts                     -> 200, winner+reason (admin)",
        "9.  GET  /api/v1/runs/{id}/review/summary                -> 200, счётчики сходятся",
        "10. POST /api/v1/runs/{id}/program-links/generate        -> 200 (если связей 0)",
        "11. GET  /api/v1/runs/{id}/program-coverage              -> 200, program_id, counts"
    )
    foreach ($step in $steps) { Write-Host ("  " + $step) }
    Write-Host ""
    Write-Host "Проверки чек-листа: health, ready, program data, create run, answers,"
    Write-Host "finalization, tree, tree levels, conflicts, review summary, program coverage."
    Write-Host "При FAIL реального прогона скрипт завершится с кодом 1."
    Write-Host ""
}

if ($DryRun) {
    Show-DryRunPlan
    exit 0
}

# --------------------------------------------------------------------------
# Проверка параметров реального прогона
# --------------------------------------------------------------------------

$parameterError = ""
if ([string]::IsNullOrWhiteSpace($script:BaseUrl) -or $script:BaseUrl -notmatch '^https?://') {
    $parameterError = "BaseUrl должен начинаться с http:// или https://"
}
elseif ([string]::IsNullOrWhiteSpace($script:Profession)) {
    $parameterError = "не задан -Profession (профессия из decision record)"
}
elseif ([string]::IsNullOrWhiteSpace($script:ProgramId)) {
    $parameterError = "не задан -ProgramId (UUID образовательной программы)"
}
elseif ([string]::IsNullOrWhiteSpace($script:Token)) {
    $parameterError = "не задан токен: используйте -Token или `$env:MODEUS_TOKEN"
}

if (-not [string]::IsNullOrWhiteSpace($script:TranscriptPath)) {
    if (-not (Test-Path -LiteralPath $script:TranscriptPath)) {
        $parameterError = "файл транскрипта не найден: " + $script:TranscriptPath
    }
    else {
        $script:TranscriptPath = (Resolve-Path -LiteralPath $script:TranscriptPath).Path
    }
}

if (-not [string]::IsNullOrWhiteSpace($parameterError)) {
    Write-Host ("ОШИБКА ПАРАМЕТРОВ: " + $parameterError) -ForegroundColor Red
    Write-Host "Подсказка: запустите с -DryRun, чтобы увидеть план без параметров." -ForegroundColor Yellow
    exit 2
}

# --------------------------------------------------------------------------
# Прогон
# --------------------------------------------------------------------------

Write-Host ""
Write-Host "=== ПРОГОН ПИЛОТА ===" -ForegroundColor Cyan
Write-Host ("BaseUrl:    " + $script:BaseUrl)
Write-Host ("Profession: " + $script:Profession)
Write-Host ("ProgramId:  " + $script:ProgramId)

# 1. Health
Write-Step "1/10 Проверка health"
$health = Invoke-Api -Method GET -Path "/api/v1/health"
$healthOk = $health.Ok -and ([string](Get-Prop -Object $health.Data -Name "status") -eq "ok")
if ($healthOk) {
    Add-Check -Id "01" -Name "health" -Passed $true -Detail "GET /api/v1/health -> 200, status=ok"
}
else {
    Add-Check -Id "01" -Name "health" -Passed $false -Detail (Format-ApiError -Result $health)
    Stop-WithSummary
}

# 2. Ready
Write-Step "2/10 Проверка readiness (PostgreSQL и схема)"
$ready = Invoke-Api -Method GET -Path "/api/v1/ready"
$readyOk = $ready.Ok -and ([string](Get-Prop -Object $ready.Data -Name "status") -eq "ok")
if ($readyOk) {
    Add-Check -Id "02" -Name "ready" -Passed $true -Detail "GET /api/v1/ready -> 200, problems=[]"
}
else {
    $detail = Format-ApiError -Result $ready
    $problems = @(Get-Array -Object $ready.Data -Name "problems")
    if ($problems.Count -gt 0) { $detail = $detail + "; problems=" + ($problems -join ",") }
    $detail = $detail + " (для пилота нужен cmd/server + PostgreSQL, не демо-API)"
    Add-Check -Id "02" -Name "ready" -Passed $false -Detail $detail
    Stop-WithSummary
}

# 3. Program data
Write-Step "3/10 Проверка данных программы"
$program = Invoke-Api -Method GET -Path ("/api/v1/academic/programs/" + $script:ProgramId)
$competencies = @()
$ksaCount = 0
if ($program.Ok) {
    $competencies = @(Get-Array -Object $program.Data -Name "competencies")
    foreach ($competency in $competencies) {
        $ksaCount += @(Get-Array -Object $competency -Name "ksa").Count
    }
}
$programOk = $program.Ok -and ($competencies.Count -ge 3) -and ($ksaCount -ge 5)
if ($programOk) {
    $programTitle = [string](Get-Prop -Object $program.Data -Name "title")
    Add-Check -Id "03" -Name "program data" -Passed $true -Detail ("program='" + $programTitle + "'; компетенций=" + $competencies.Count + " (>=3); KSA=" + $ksaCount + " (>=5)")
}
else {
    if ($program.Ok) {
        $detail = "компетенций=" + $competencies.Count + " (нужно >=3); KSA=" + $ksaCount + " (нужно >=5); дополните программу или зафиксируйте ограничение в карточке 2"
    }
    else {
        $detail = Format-ApiError -Result $program
    }
    Add-Check -Id "03" -Name "program data" -Passed $false -Detail $detail
    Stop-WithSummary
}

# 4. Create run
Write-Step "4/10 Создание исследования"
$createBody = @{
    profession  = $script:Profession
    type        = "chat"
    result_kind = "program"
    program_id  = $script:ProgramId
}
$created = Invoke-Api -Method POST -Path "/api/v1/runs" -Body $createBody
$script:RunId = [string](Get-Prop -Object $created.Data -Name "id")
$runStatus = [string](Get-Prop -Object $created.Data -Name "status")
$runStage = [string](Get-Prop -Object $created.Data -Name "stage")
$resultKind = [string](Get-Prop -Object $created.Data -Name "result_kind")
$programIdOut = [string](Get-Prop -Object $created.Data -Name "program_id")

$createOk = $created.Ok -and ($created.StatusCode -eq 201) -and
    (-not [string]::IsNullOrWhiteSpace($script:RunId)) -and
    ($resultKind -eq "program") -and
    ($programIdOut -eq $script:ProgramId) -and
    ($runStage -eq "interview")

if ($createOk) {
    Add-Check -Id "04" -Name "create run" -Passed $true -Detail ("run_id=" + $script:RunId + "; status=" + $runStatus + "; stage=" + $runStage + "; result_kind=" + $resultKind)
}
else {
    if ($created.Ok) {
        $detail = "run_id=" + $script:RunId + "; status=" + $runStatus + "; stage=" + $runStage + "; result_kind=" + $resultKind + "; program_id=" + $programIdOut
    }
    else {
        $detail = Format-ApiError -Result $created
        if ($created.StatusCode -eq 401) { $detail = $detail + " (проверьте -Token / `$env:MODEUS_TOKEN)" }
        if ($created.StatusCode -eq 400 -and $created.Message -match "chat") { $detail = $detail + " (не настроен чат/LLM)" }
    }
    Add-Check -Id "04" -Name "create run" -Passed $false -Detail $detail
    Stop-WithSummary
}

# 5. Answers
Write-Step "5/10 Ответы интервью"
$hasTranscript = -not [string]::IsNullOrWhiteSpace($script:TranscriptPath)
if ($hasTranscript) {
    $answers = @(Read-TranscriptAnswers -Path $script:TranscriptPath)
    Write-Info ("ответов в транскрипте: " + $answers.Count)
    if ($answers.Count -eq 0) {
        Add-Check -Id "05" -Name "interview answers" -Passed $false -Detail "в транскрипте не найдено ответов; формат: одна строка — один ответ"
        Stop-WithSummary
    }

    $sendResult = Send-Answers -Answers $answers
    $answersOk = ($sendResult.Sent -gt 0) -and [string]::IsNullOrWhiteSpace($sendResult.Error)
    $detail = "отправлено " + $sendResult.Sent + " из " + $sendResult.Total
    if ($sendResult.Finished) { $detail = $detail + "; интервью завершилось раньше, лишние ответы не отправлены" }
    if (-not [string]::IsNullOrWhiteSpace($sendResult.Error)) { $detail = $detail + "; ошибка: " + $sendResult.Error }
    Add-Check -Id "05" -Name "interview answers" -Passed $answersOk -Detail $detail
    if (-not $answersOk) { Stop-WithSummary }
}
else {
    Add-Check -Id "05" -Name "interview answers" -Passed $true -Detail "N/A: -TranscriptPath не передан; интервью проводит человек в UI"
    Write-Warn ("Скрипт ждёт финализации. run_id=" + $script:RunId + "; проведите интервью в интерфейсе исследования.")
}

# 6. Finalization
Write-Step "6/10 Финализация (поллинг GET /runs/{id})"
$final = Wait-Finalization -TimeoutMinutes 90 -PollSeconds 5
if (-not $final.Ok) {
    Add-Check -Id "06" -Name "finalization" -Passed $false -Detail ("status=" + $final.Status + "; stage=" + $final.Stage + "; " + $final.Error)
    Stop-WithSummary
}
$finalOk = ($final.Status -eq "done")
Add-Check -Id "06" -Name "finalization" -Passed $finalOk -Detail ("status=" + $final.Status + "; stage=" + $final.Stage)
if ($final.Status -eq "failed" -or $final.Status -eq "degraded") {
    Write-Warn "Результат неполный: не передавайте пакет как успешный; зафиксируйте замечание."
}

# 7. Tree
Write-Step "7/10 Дерево деятельности"
$tree = Invoke-Api -Method GET -Path ("/api/v1/runs/" + $script:RunId + "/tree")
$script:TreeNodes = @()
if ($tree.Ok) {
    $script:TreeNodes = @(Get-Array -Object $tree.Data -Name "nodes")
}
$roleCount = 0
$functionCount = 0
$taskCount = 0
$actionCount = 0
$evidenceCount = 0
$withoutEvidence = 0
foreach ($node in $script:TreeNodes) {
    $nodeType = [string](Get-Prop -Object $node -Name "type")
    switch ($nodeType) {
        "role" { $roleCount++ }
        "function" { $functionCount++ }
        "task" { $taskCount++ }
        "action" { $actionCount++ }
    }
    $nodeEvidence = @(Get-Array -Object $node -Name "evidence")
    $evidenceCount += $nodeEvidence.Count
    if ($nodeEvidence.Count -eq 0) { $withoutEvidence++ }
}

$treeOk = $tree.Ok -and ($script:TreeNodes.Count -gt 0) -and ($roleCount -eq 1) -and ($withoutEvidence -eq 0)
$treeDetail = "nodes=" + $script:TreeNodes.Count + "; role=" + $roleCount + "; function=" + $functionCount + "; task=" + $taskCount + "; action=" + $actionCount + "; evidence=" + $evidenceCount + "; без обоснований=" + $withoutEvidence
if (-not $tree.Ok) { $treeDetail = Format-ApiError -Result $tree }
Add-Check -Id "07" -Name "tree" -Passed $treeOk -Detail $treeDetail

$levelsOk = $treeOk -and ($functionCount -ge 2) -and ($taskCount -ge 4) -and ($actionCount -ge 8)
$levelsDetail = "function=" + $functionCount + " (>=2); task=" + $taskCount + " (>=4); action=" + $actionCount + " (>=8)"
Add-Check -Id "08" -Name "tree levels" -Passed $levelsOk -Detail $levelsDetail

# 8. Conflicts
Write-Step "8/10 Конфликты источников"
$conflicts = Invoke-Api -Method GET -Path ("/api/v1/runs/" + $script:RunId + "/conflicts")
$conflictItems = @()
if ($conflicts.Ok -and $null -ne $conflicts.Data) { $conflictItems = @($conflicts.Data) }
$badConflicts = 0
foreach ($conflict in $conflictItems) {
    $winner = Get-Prop -Object $conflict -Name "winner"
    $reason = [string](Get-Prop -Object $conflict -Name "reason")
    if ($null -eq $winner -or [string]::IsNullOrWhiteSpace($reason)) { $badConflicts++ }
}
$conflictsOk = $conflicts.Ok -and ($badConflicts -eq 0)
if ($conflicts.Ok) {
    $conflictsDetail = "конфликтов=" + $conflictItems.Count + "; без winner/reason=" + $badConflicts
}
else {
    $conflictsDetail = Format-ApiError -Result $conflicts
    if ($conflicts.StatusCode -eq 403) { $conflictsDetail = $conflictsDetail + " (нужен admin-токен)" }
    if ($conflicts.StatusCode -eq 501) { $conflictsDetail = $conflictsDetail + " (нужен PostgreSQL)" }
}
Add-Check -Id "09" -Name "conflicts" -Passed $conflictsOk -Detail $conflictsDetail

# 9. Review summary
Write-Step "9/10 Сводка проверки"
$summary = Invoke-Api -Method GET -Path ("/api/v1/runs/" + $script:RunId + "/review/summary")
$summaryNodes = Get-Prop -Object $summary.Data -Name "nodes"
$summaryEvidence = Get-Prop -Object $summary.Data -Name "evidence"
$summaryLinks = Get-Prop -Object $summary.Data -Name "links"
$nodesTotal = Get-Prop -Object $summaryNodes -Name "total"
$evidenceTotal = Get-Prop -Object $summaryEvidence -Name "total"
$linksTotal = Get-Prop -Object $summaryLinks -Name "total"

$summaryOk = $summary.Ok -and ($null -ne $nodesTotal) -and
    ([int]$nodesTotal -eq $script:TreeNodes.Count) -and
    ([int]$evidenceTotal -eq $evidenceCount)
if ($summary.Ok) {
    $summaryDetail = "nodes.total=" + $nodesTotal + " (дерево=" + $script:TreeNodes.Count + "); evidence.total=" + $evidenceTotal + " (дерево=" + $evidenceCount + "); links.total=" + $linksTotal
}
else {
    $summaryDetail = Format-ApiError -Result $summary
}
Add-Check -Id "10" -Name "review summary" -Passed $summaryOk -Detail $summaryDetail

# 10. Program links + coverage
Write-Step "10/10 Сопоставление и покрытие программы"
$coverage = Invoke-Api -Method GET -Path ("/api/v1/runs/" + $script:RunId + "/program-coverage")
$generated = $false
$generateError = ""

if ($coverage.Ok) {
    $initialCounts = Get-Prop -Object $coverage.Data -Name "counts"
    $initialLinked = [int](Get-Prop -Object $initialCounts -Name "confirmed") + [int](Get-Prop -Object $initialCounts -Name "proposed")
    if ($initialLinked -eq 0) {
        Write-Info "связей нет — вызываю POST /runs/{id}/program-links/generate"
        $generatedResult = Invoke-Api -Method POST -Path ("/api/v1/runs/" + $script:RunId + "/program-links/generate")
        if ($generatedResult.Ok) {
            $generated = $true
            $coverage = Invoke-Api -Method GET -Path ("/api/v1/runs/" + $script:RunId + "/program-coverage")
        }
        else {
            $generateError = Format-ApiError -Result $generatedResult
        }
    }
}

$counts = Get-Prop -Object $coverage.Data -Name "counts"
$confirmed = [int](Get-Prop -Object $counts -Name "confirmed")
$proposed = [int](Get-Prop -Object $counts -Name "proposed")
$none = [int](Get-Prop -Object $counts -Name "none")
$total = [int](Get-Prop -Object $counts -Name "total")
$coverageProgramId = [string](Get-Prop -Object $coverage.Data -Name "program_id")

$coverageOk = $coverage.Ok -and
    ($coverageProgramId -eq $script:ProgramId) -and
    ($total -eq $script:TreeNodes.Count) -and
    (($confirmed + $proposed + $none) -eq $total) -and
    (($confirmed + $proposed) -ge 1)

$generationText = "не требовалась"
if ($generated) { $generationText = "да" }

if ($coverage.Ok) {
    $coverageDetail = "program_id=" + $coverageProgramId + "; confirmed=" + $confirmed + "; proposed=" + $proposed + "; none=" + $none + "; total=" + $total + " (дерево=" + $script:TreeNodes.Count + "); генерация=" + $generationText
}
else {
    $coverageDetail = Format-ApiError -Result $coverage
}
if (-not [string]::IsNullOrWhiteSpace($generateError)) {
    $coverageDetail = $coverageDetail + "; generate: " + $generateError
}
Add-Check -Id "11" -Name "program coverage" -Passed $coverageOk -Detail $coverageDetail

exit (Write-Summary)
