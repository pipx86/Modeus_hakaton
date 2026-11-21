# OCR сканированных PDF и изображений

Как импорт программ ФГОС распознаёт сканы, какие инструменты нужны на Windows,
как включить OCR и какие ограничения у этого способа.

## Зачем

Вузовские программы и ФГОС приходят не только в XLSX/CSV, но и в PDF. Часть PDF —
это сканы: текстового слоя в них нет, поэтому `internal/fgos/pages.go` не может
отдать страницы модели. Раньше такой документ возвращал ошибку «нет текстового
слоя, нужен OCR». Теперь страницы можно распознать tesseract'ом и отправить в
обычный LLM-пайплайн ФГОС: модель извлечёт компетенции, KSA и цитаты, а
методист проверит черновик.

Изображения PNG/JPEG распознаются тем же движком. DOCX не сканируется: это
текстовый формат, OCR для него не нужен.

## Как это работает

```
PDF ──► текстовый слой (ledongthuc/pdf)
          │
          ├─ все страницы содержат >= 20 символов ──► OCR не нужен
          │
          └─ есть пустые/почти пустые страницы
                   │
                   ▼
        pdftoppm -r 200 -png -f N -l M  ──► PNG во временном каталоге
                   │
                   ▼
        tesseract <png> stdout -l rus+eng
                   │
                   ▼
        страницы + предупреждение «страница N распознана OCR (точность ниже)»
                   │
                   ▼
        LLM-адаптер ФГОС (internal/fgos) ──► черновик программы
```

1. **Текстовый слой.** PDF читается постранично существующей логикой
   `internal/fgos`. Страница короче `fgos.MinPageTextRunes` (20 символов)
   считается пустой: в сканах колонтитулы иногда дают 1–2 символа текста.
2. **Смешанный документ.** Если текстовый слой есть не на всех страницах,
   OCR-ятся только страницы короче порога; страницы с нормальным текстом
   остаются как есть.
3. **Скан целиком.** Если текстового слоя нет ни на одной странице, OCR-ятся все
   страницы.
4. **Рендеринг.** `pdftoppm` из poppler рендерит нужные страницы в PNG с
   разрешением `OCR_DPI` (по умолчанию 200). Соседние страницы рендерятся одним
   вызовом на диапазон.
5. **Распознавание.** `tesseract <image> stdout -l <lang>` вызывается на каждую
   страницу отдельно, с таймаутом `OCR_PAGE_TIMEOUT_S`.
6. **Сборка.** Распознанный текст заменяет пустую страницу и помечается
   `Page.OCR = true`; `fgos.Extract` добавляет в `warnings` строку
   «страница N распознана OCR (точность ниже)». Временные PNG и PDF удаляются.
7. **Ограничения.** Число страниц, которые разрешено OCR-ить, ограничено
   `OCR_MAX_PAGES` (20), общее время — `OCR_TIMEOUT_S` (300 секунд). При
   превышении возвращается понятная ошибка, а не бесконечный импорт.

Если OCR выключен (`OCR_ENABLED=false`) или tesseract/poppler не найдены,
импорт не падает: PDF с текстовым слоем разбирается как раньше, а скан
получает 422/503 с инструкцией по установке.

## Установка на Windows

Инструменты ставятся через winget. В PATH рабочего процесса их может не быть:
winget-пакеты часто лежат в пользовательском каталоге. Поэтому путь к каждому
бинарю настраивается отдельно.

```powershell
winget install --id UB-Mannheim.TesseractOCR -e
winget install --id oschwartz10612.Poppler -e
```

Проверенные пути на машине разработки (версии могут отличаться):

| Инструмент | Типичный путь |
|---|---|
| tesseract | `C:\Program Files\Tesseract-OCR\tesseract.exe` |
| pdftoppm | `%LOCALAPPDATA%\Microsoft\WinGet\Packages\oschwartz10612.Poppler_Microsoft.Winget.Source_8wekyb3d8bbwe\poppler-26.09.0\Library\bin\pdftoppm.exe` |

`pdftoppm` удобно искать по шаблону (номер версии меняется):

```powershell
$pdftoppm = Get-ChildItem `
  "$env:LOCALAPPDATA\Microsoft\WinGet\Packages\oschwartz10612.Poppler_*\poppler-*\Library\bin\pdftoppm.exe" |
  Select-Object -First 1 -ExpandProperty FullName
```

### Языки tesseract

По умолчанию используется `rus+eng`. Установщик UB-Mannheim не всегда ставит
русский язык: проверьте список.

```powershell
& "C:\Program Files\Tesseract-OCR\tesseract.exe" --list-langs
```

Если в списке только `eng` и `osd`, добавьте `rus.traineddata` в
`C:\Program Files\Tesseract-OCR\tessdata` (например, из
<https://github.com/tesseract-ocr/tessdata>) или переустановите tesseract с
отмеченным русским языком. Ошибка tesseract `Failed loading language 'rus'`
означает именно отсутствие языковых данных; адаптер дописывает в ошибку
подсказку про tessdata.

Проверка распознавания вручную:

```powershell
& "C:\Program Files\Tesseract-OCR\tesseract.exe" scan.png stdout -l rus+eng
```

### Переменные окружения

| Переменная | По умолчанию | Что задаёт |
|---|---|---|
| `OCR_ENABLED` | `false` | Включает OCR в `cmd/server` и приём PNG/JPEG в импорте |
| `OCR_TESSERACT` | `tesseract` | Путь или имя бинаря tesseract |
| `OCR_PDFTOPPM` | `pdftoppm` | Путь или имя бинаря pdftoppm |
| `OCR_LANG` | `rus+eng` | Языки tesseract через `+` |
| `OCR_DPI` | `200` | Разрешение рендеринга PDF; 300 — для мелкого текста |
| `OCR_PAGE_TIMEOUT_S` | `60` | Таймаут распознавания одной страницы, секунды |
| `OCR_MAX_PAGES` | `20` | Максимум страниц, которые разрешено OCR-ить за документ |
| `OCR_TIMEOUT_S` | `300` | Общий таймаут OCR документа, секунды |
| `PROGRAM_IMPORT_TIMEOUT_S` | `900` | Бюджет всего синхронного импорта PDF/DOCX/скана (OCR + LLM), секунды; в `cmd/program-watcher` не используется |

Пример для текущего процесса PowerShell:

```powershell
$env:OCR_ENABLED = "true"
$env:OCR_TESSERACT = "C:\Program Files\Tesseract-OCR\tesseract.exe"
$env:OCR_PDFTOPPM = (Get-ChildItem `
  "$env:LOCALAPPDATA\Microsoft\WinGet\Packages\oschwartz10612.Poppler_*\poppler-*\Library\bin\pdftoppm.exe" |
  Select-Object -First 1 -ExpandProperty FullName)
$env:OCR_LANG = "rus+eng"
$env:OCR_DPI = "200"
```

Чтобы переменные пережили перезапуск терминала, используйте `setx` (или
системные переменные) и заново откройте терминал/службу:

```powershell
setx OCR_ENABLED true
setx OCR_TESSERACT "C:\Program Files\Tesseract-OCR\tesseract.exe"
setx OCR_PDFTOPPM "$env:OCR_PDFTOPPM"
```

Альтернатива — добавить каталоги tesseract и poppler в `PATH`. Если провайдер
запускает сервер в контейнере, tesseract и poppler должны быть установлены
внутри образа, а не только на хосте.

## Как включить

### cmd/server и API импорта

```powershell
$env:OCR_ENABLED = "true"
$env:OCR_TESSERACT = "C:\Program Files\Tesseract-OCR\tesseract.exe"
$env:OCR_PDFTOPPM = "...\pdftoppm.exe"
# опционально: бюджет импорта OCR+LLM, по умолчанию 900 секунд (15 минут)
$env:PROGRAM_IMPORT_TIMEOUT_S = "900"
go run ./cmd/server
```

В логе появляются `program_import_ocr_enabled` (и
`program_import_ocr_unavailable`, если инструменты не найдены — сервер при этом
продолжает работать). После включения:

- PDF-сканы и PNG/JPEG принимаются эндпоинтом
  `POST /api/v1/admin/programs/import`;
- бюджет импорта по умолчанию 15 минут (`PROGRAM_IMPORT_TIMEOUT_S=900`);
  OCR и LLM работают в одном бюджете, поэтому скан редко упирается в прежние
  5 минут;
- предупреждения OCR попадают в `report.warnings` черновика.

```powershell
curl -s -X POST http://127.0.0.1:8080/api/v1/admin/programs/import `
  -H "Authorization: Bearer $TOKEN" `
  -F "key=mirea/09.03.01/2024/full-time" `
  -F "file=@scan.pdf"
```

Если OCR выключен, PNG/JPEG получают 422 `PROGRAM_IMPORT_OCR_DISABLED`, а
PDF-скан — 422 `PROGRAM_IMPORT_INVALID` с подсказкой включить OCR. Если
tesseract/poppler не установлены — 503 `PROGRAM_IMPORT_OCR_UNAVAILABLE`, если
не уложились в таймаут — 503 `PROGRAM_IMPORT_OCR_TIMEOUT`.

### cmd/program-watcher

```powershell
go run ./cmd/program-watcher -once -pdf-llm -ocr `
  -ocr-lang=rus+eng -ocr-dpi=200
```

- `-ocr` включает OCR и подразумевает `-pdf-llm`: распознанный текст всё равно
  должен уйти в LLM, чтобы получить компетенции.
- `-ocr-lang` переопределяет `OCR_LANG`.
- `-ocr-dpi` переопределяет `OCR_DPI` (0 — значение из env).
- Пути к бинарям берутся из `OCR_TESSERACT`/`OCR_PDFTOPPM`.
- Если инструменты не найдены, воркер пишет `fgos_ocr_unavailable` и продолжает
  цикл: документ получит статус `error` с инструкцией, остальные источники
  проверяются как раньше.

## Лимиты и точность

- **Лимит страниц.** `OCR_MAX_PAGES=20` считает страницы, которые нужно
  распознать, а не весь документ. Скан на 25 страниц отклоняется: разделите
  документ или увеличьте лимит осознанно.
- **Таймауты.** `OCR_PAGE_TIMEOUT_S=60` — на страницу,
  `OCR_TIMEOUT_S=300` — на весь OCR. Импорт в API дополнительно ограничен
  `PROGRAM_IMPORT_TIMEOUT_S` (по умолчанию 900 секунд): этот бюджет общий для
  OCR и запроса к модели.
- **Разрешение.** 200 DPI подходит для обычного машинописного текста; для
  мелкого шрифта и таблиц поднимите `OCR_DPI` до 300. Это замедляет рендеринг и
  распознавание.
- **Точность.** Tesseract ошибается на колонках, таблицах, штампах, рукописном
  тексте и плохих ксерокопиях. Ошибки распознавания могут попасть в `title`,
  KSA и цитаты. Дословная проверка цитат работает по распознанному тексту,
  поэтому цитата, совпавшая с OCR-версией, всё равно требует сверки с
  оригиналом.
- **Предупреждения.** Для каждой распознанной страницы в черновике есть строка
  «страница N распознана OCR (точность ниже)». Если предупреждений нет, значит,
  документ разобран по текстовому слою.

## Что делать со сканами

1. **Сначала ищите PDF с текстовым слоем.** Откройте документ и попробуйте
   выделить текст: если выделяется — OCR не нужен, качество будет выше.
2. **Готовьте скан.** Ровные страницы без теней и перекосов, разрешение
   оригинала не ниже 200 DPI, формат PNG/JPEG или PDF. Не фотографируйте экран.
3. **Проверяйте язык.** Для русских документов нужен `rus` в tessdata;
   `OCR_LANG=rus+eng` помогает с английскими аббревиатурами и названиями.
4. **Дробите большие документы.** Скан больше 20 страниц не пройдёт лимит:
   загрузите частями или поднимите `OCR_MAX_PAGES`, помня про таймаут.
5. **Проверяйте черновик.** Сверьте хотя бы коды компетенций, названия и
   несколько цитат с оригиналом; при систематических ошибках поднимите DPI или
   распознайте страницы вручную.
6. **Отдельные картинки.** PNG/JPEG принимаются импортом только при
   `OCR_ENABLED=true`; без OCR они отклоняются с 422.

## Как проверить без установленных бинарей

Тесты не вызывают настоящие tesseract и pdftoppm:

```bash
go test -count=1 ./internal/ocr/ ./internal/fgos/
```

- `internal/ocr` использует фейковые `Engine` и `Renderer`, тестовый бинарь
  (helper process) для проверки пустого вывода, таймаута и ошибки запуска;
- `internal/fgos` использует фейковый `OCRExtractor` и проверяет, что скан
  уходит в OCR, а текстовый PDF — нет;
- проверяются: страница без текста, смешанный документ, лимит страниц,
  таймаут, отсутствие бинаря, выключенный OCR и предупреждения.

## Файлы

| Файл | Что делает |
|---|---|
| `internal/ocr/tesseract.go` | `Engine`, `Tesseract`, вызов `tesseract ... stdout -l` |
| `internal/ocr/renderer.go` | `Renderer`, `PDFRenderer`, вызов `pdftoppm` и уборка PNG |
| `internal/ocr/document.go` | `Document.PagesFromPDF` и `Document.RecognizeImage`, лимиты, таймауты, слияние страниц |
| `internal/ocr/ocr.go` | конфигурация `OCR_*`, ошибки и подсказки по установке |
| `internal/ocr/extractor.go` | обёртка `FGOSExtractor`: OCR + LLM-адаптер |
| `internal/fgos/pages.go` | `ExtractPagesWithOCR`, `ExtractPDFPageTexts`, порог 20 символов |
| `internal/fgos/ocr.go` | интерфейс `OCRExtractor` и `ContextDocumentExtractor` |
| `cmd/server/main.go` | включение OCR по `OCR_ENABLED=true` |
| `cmd/program-watcher/main.go` | флаги `-ocr`, `-ocr-lang`, `-ocr-dpi` |
| `internal/api/program_import.go` | приём сканов, 422/503 и бюджет `PROGRAM_IMPORT_TIMEOUT_S` (15 минут) |

## Ограничения

1. **Точность OCR ниже текстового слоя.** Ошибки распознавания попадают в
   компетенции и цитаты; методист обязан сверить черновик с оригиналом.
2. **Лимиты синхронного импорта.** OCR-ится не больше 20 страниц и не дольше
   5 минут OCR; весь HTTP-запрос ограничен `PROGRAM_IMPORT_TIMEOUT_S`
   (по умолчанию 15 минут), большие сканы нужно делить.
3. **Инструменты ставятся на каждом хосте.** По умолчанию `OCR_ENABLED=false`;
   без tesseract и poppler (и русского `traineddata`) сканы возвращают 422/503,
   а не разбираются. Таблицы и колонки OCR может перепутать.
