# Nemotron / OpenRouter: три агента и параллельный запуск

Все три продукт-агента используют один OpenAI-совместимый endpoint OpenRouter
с моделью **Nemotron 3 Ultra (free)**:

```
nvidia/nemotron-3-ultra-550b-a55b:free
```

## Как устроена «параллельность»

Полный конвейер **последовательный по смыслу**: пока интервьюер не задал вопрос
и не получен ответ, аналитику нечего разбирать; синтезатору нужно дождаться
анализа. Поэтому «запустить все три агента одновременно на один run» нельзя —
получится мусор.

Что сделано вместо этого:

1. **У каждого агента свой клиент и свои параметры** — модель, температура,
   max tokens, timeout, reasoning effort. Они не делят одно соединение и не
   мешают друг другу.
2. **Общий параллельный шлюз** (`internal/llm/agents`): не больше
   `LLM_MAX_CONCURRENCY` одновременных запросов и не больше
   `LLM_REQUESTS_PER_MINUTE` в минуту на провайдера. Это защищает бесплатный
   тариф от 429.
3. **Повторные попытки** на 429/5xx/timeout с экспоненциальной задержкой
   (`LLM_RETRY_MAX`), плюс circuit breaker и guardrails на каждого агента.
4. **Независимые вызовы можно запускать параллельно**: `cmd/nemotron-smoke`
   отправляет по одному запросу от интервьюера, аналитика и синтезатора
   одновременно и показывает, что ключ/модель/лимиты работают.

## Ключ: не в git и не в чат

Положи ключ в файл, который игнорируется git:

```powershell
cd C:\MyPrograms\CompetencyGapAnalyzer\.worktrees\integration
New-Item -ItemType Directory -Force .secrets | Out-Null
Set-Content .secrets\openrouter.key "sk-or-..." -NoNewline
```

`OPENROUTER_API_KEY_FILE=.secrets/openrouter.key` уже прописан в
`.env.example`; переменная окружения `OPENROUTER_API_KEY` имеет приоритет.
`.secrets/` и `*.key` добавлены в `.gitignore`.

## Конфигурация

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `OPENROUTER_API_KEY` / `OPENROUTER_API_KEY_FILE` | — | ключ |
| `OPENROUTER_BASE_URL` | `https://openrouter.ai/api/v1` | endpoint |
| `OPENROUTER_MODEL` | `nvidia/nemotron-3-ultra-550b-a55b:free` | модель для всех |
| `LLM_TIMEOUT_S` | `240` | общий таймаут запроса |
| `LLM_MAX_CONCURRENCY` | `3` | одновременных запросов |
| `LLM_REQUESTS_PER_MINUTE` | `20` | лимит запросов |
| `LLM_RETRY_MAX` | `3` | повторов на 429/5xx/timeout |
| `LLM_INTERVIEWER_MODEL` | общая | модель интервьюера |
| `LLM_INTERVIEWER_TEMPERATURE` | `0.4` | креативность вопросов |
| `LLM_INTERVIEWER_MAX_TOKENS` | `1500` | ответ + reasoning |
| `LLM_ANALYST_MODEL` | общая | модель аналитика |
| `LLM_ANALYST_TEMPERATURE` | `0.2` | точность извлечения |
| `LLM_ANALYST_MAX_TOKENS` | `5000` | длинный JSON |
| `LLM_SYNTHESIZER_*` | `0.1/4000` | резерв под LLM-нормализацию |

## Проверка ключа и параллельного запуска

```powershell
make nemotron-smoke
# или: go run ./cmd/nemotron-smoke -parallel
```

Ожидаемо: три строки `ok` (interviewer/analyst/synthesizer), время каждого
запроса и `failures: 0`. Free-модель медленная: 30–120 секунд на ответ —
это нормально, поэтому таймаут 180 с.

## Живой e2e

```powershell
make e2e-live
# или: go run ./cmd/e2e -live -out e2e-output.json
```

В отличие от `make e2e` (фейковая модель), здесь реальные вызовы Nemotron:
интервьюер задаёт вопросы, аналитик разбирает стенограмму. Может занять
5–10 минут из-за скорости free-модели.

## Ограничения и риски

- Free Nemotron имеет суточные/минутные лимиты; при 429 шлюз повторит запрос,
  но лучше снизить `LLM_MAX_CONCURRENCY` до 2 или перейти на платный вариант.
- Модель может вернуть невалидный JSON — сработает repair (до 2 попыток), затем
  явная ошибка; run не зависает.
- Модель плохо знает русский в официальном списке языков; в чате она отвечала
  по-русски нормально, но качество надо проверять на реальных диалогах.
- Полного параллельного запуска трёх агентов на один run нет по построению:
  см. раздел выше.
- Ключ, попавший в чат/лог/скриншот, считается скомпрометированным — ротируй.
