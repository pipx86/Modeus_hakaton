# Зависимости проекта

Единый обзор всех экосистем репозитория. Приложение — Go-бэкенд + React-фронтенд,
Python используется только для вспомогательных скриптов проверки.

## Установка всего одной командой

```bash
make deps
```

Цель выполняет:

1. `go mod download` — модули Go из `go.mod`/`go.sum`;
2. `cd frontend && npm ci` — пакеты фронтенда из `frontend/package-lock.json`;
3. `python3 -m pip install -r requirements-dev.txt` — Python-инструменты.

Без `make` (например, в pwsh на Windows):

```powershell
go mod download
cd frontend; npm ci; cd ..
python -m pip install -r requirements-dev.txt
```

В Makefile интерпретатор по умолчанию — `python3` (как в CI). На Windows, где
команда называется `python`, цель запускается так: `make deps PYTHON=python`.

## Языки и манифесты

| Язык | Версия | Где зависимости | Файлов |
|---|---|---|---|
| Go | 1.26.10 (`go.mod`) | `go.mod`, `go.sum` | 241 `.go` |
| TypeScript/JavaScript | Node 24 (CI), TS 5.9.3 | `frontend/package.json`, `frontend/package-lock.json` | 38 `.tsx`, 21 `.ts`, 12 `.js` |
| Python | 3.x | `requirements.txt`, `requirements-dev.txt` | 1 `.py` |
| SQL | PostgreSQL 16 + pgvector | `migrations/*.sql` | 24 |

## Go-бэкенд

Прямые зависимости:

| Модуль | Версия | Назначение |
|---|---|---|
| `github.com/golang-jwt/jwt/v5` | 5.3.1 | JWT-аутентификация |
| `github.com/jackc/pgx/v5` | 5.7.5 | PostgreSQL-драйвер |
| `github.com/ledongthuc/pdf` | v0.0.0-20261020125853 | извлечение текста из PDF |
| `github.com/pashagolub/pgxmock/v4` | 4.9.0 | мок БД в тестах |
| `github.com/santhosh-tekuri/jsonschema/v6` | 6.0.3 | валидация JSON-схем агентов |
| `go.opentelemetry.io/otel` (+ `sdk`, `trace`) | 1.26.0 | трейсинг |
| `gopkg.in/natefinch/lumberjack.v2` | 2.2.1 | ротация логов |

Косвенные (10): `go-logr/logr`, `go-logr/stdr`, `jackc/pgpassfile`,
`jackc/pgservicefile`, `jackc/puddle/v2`, `otel/metric`, `x/crypto`, `x/sync`,
`x/sys`, `x/text`.

Инструменты: `golangci-lint v1.62.2`, `goose v3.29.0`.

## Фронтенд

Runtime: React 19.3.1, react-dom, react-router-dom 7.18.5, `radix-ui` 1.8.1 и
`@radix-ui/react-slot`, `shadcn` 4.23.0, `lucide-react` 1.70.0, `sonner` 2.0.8,
`next-themes` 0.4.6, `openapi-fetch` 0.17.0, `clsx` 2.1.1, `tailwind-merge` 3.7.0,
`tw-animate-css` 1.4.0, `@fontsource-variable/geist` 5.3.0, `cn` 0.5.2.

Dev: Vite 8.3.6, Vitest 5.1.0, `@vitest/coverage-v8`, jsdom 31, Testing Library
(react/dom/jest-dom/user-event), TypeScript 5.9.3, ESLint 10.14 +
react-hooks/react-refresh/typescript-eslint, Tailwind CSS 3.4.19 + autoprefixer,
postcss, cssnano, tailwindcss-animate, `openapi-typescript` 7.13.0,
`@vitejs/plugin-react` 6.1.2, типы `@types/*`.

## Python-скрипты

| Файл | Зависимости |
|---|---|
| `scripts/check_deploy.py` | стандартная библиотека + **PyYAML** |

`PyYAML` нужен только для разбора YAML-ассетов: `docker-compose.yml`,
`.golangci.yml`, `deploy/prometheus/prometheus.yml`, двух provisioning-файлов
Grafana и двух GitHub Actions workflow. Вызывается через `make check-deploy` и
в CI (`python3 scripts/check_deploy.py`). Рантайм приложения от него не зависит.

`requirements-dev.txt` дополнительно ставит `pytest` и `ruff` — на случай
появления тестов и линтинга Python-кода.

## Сервисы docker-compose

| Сервис | Образ |
|---|---|
| API | сборка из `Dockerfile` (`golang:1.26.10-bookworm` → `distroless/static-debian12:nonroot`) |
| Web | `nginx:1.27-alpine` |
| PostgreSQL | `pgvector/pgvector:0.8.0-pg16` |
| Prometheus | `prom/prometheus:v2.54.1` |
| Grafana | `grafana/grafana:11.2.0` |
| n8n | `n8nio/n8n:1.63.4` |
| n8n PostgreSQL | `postgres:16-alpine` |

## Проверки

```bash
make deps          # установить всё
make test          # Go-тесты
make lint          # golangci-lint
make check-deploy  # Python-валидация deploy-ассетов (нужен PyYAML)
cd frontend && npm test
```
