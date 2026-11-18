# Логи фронтенда

Фронтенд собирает собственные ошибки в файл — **без элементов интерфейса**.
В браузере ничего не отображается и не скачивается: записи складываются на диск
рядом с проектом.

## Где лежит файл

```
frontend/logs/frontend.log
```

Формат — по одной JSON-записи на строку:

```json
{"time":"2026-11-18T11:45:00.000Z","level":"error","message":"API 5xx","context":{"method":"GET","url":"http://localhost:8080/api/v1/runs","status":500},"url":"http://localhost:5173/"}
```

При превышении 5 МБ файл переименовывается в `frontend.log.prev`, новый пишется с нуля.

## Что попадает в лог

| Источник | Уровень | Пример сообщения |
|---|---|---|
| Ответы API 5xx | `error` | `API 5xx` + метод, URL, статус |
| Ответы API 4xx | `warn` | `API 4xx` + метод, URL, статус |
| Сетевой сбой запроса | `error` | `API request failed` + текст ошибки |
| Ошибка рендера React | `error` | `render error` + стек и компонентный стек |
| Необработанное исключение окна | `error` | `uncaught error` + файл, строка, колонка |
| Необработанный промис | `error` | `unhandled rejection` + причина |
| Старт приложения | `info` | `frontend started` + URL |

## Как это работает

1. `frontend/src/lib/frontend-log.ts` — модуль сбора: `frontendLog.error(...)` и
   глобальные обработчики `error`/`unhandledrejection`.
2. В dev-режиме запись уходит `POST /__frontend-log` на Vite dev-сервер.
3. Плагин `frontend-log-collector` в `frontend/vite.config.ts` дописывает строку
   в `frontend/logs/frontend.log` и отвечает `204`.

В production (`import.meta.env.PROD`) отправка отключена — боевая сборка ничего
никуда не пишет и не требует дополнительного сервера.

## Просмотр

```powershell
Get-Content frontend/logs/frontend.log -Tail 50
```

Только ошибки:

```powershell
Get-Content frontend/logs/frontend.log | Select-String '"level":"error"'
```

Файл и каталог `logs/` исключены из git (правило `logs/` в корневом `.gitignore`).
