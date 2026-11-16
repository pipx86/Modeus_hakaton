# Домен ошибок LLM

Все ошибки слоя LLM сводятся к стабильным категориям. Они не содержат
промптов, PII и тела ответа провайдера, поэтому их безопасно логировать и
отдавать в API.

## Категории

| Категория | Когда возникает | Retryable |
|---|---|---|
| `auth` | 401, 403, неверный ключ | нет |
| `rate_limit` | 429 | да |
| `timeout` | таймаут запроса, deadline exceeded | да |
| `unavailable` | 5xx, сеть, circuit open | да, кроме circuit open |
| `invalid_response` | невалидный JSON, пустой ответ, 4xx запроса | нет |
| `guardrail` | сработал injection/PII/jailbreak guard | нет |
| `canceled` | отменили контекст | нет |
| `configuration` | нет ключа, модели или неверный конфиг | нет |
| `internal` | неизвестная ошибка | нет |

## API

```go
kind := llm.Classify(err)
code := llm.Code(err)
retry := llm.IsRetryable(err)
text := llm.SafeMessage(err)
```

- `Classify` понимает `llm.DomainError`, `Classified`-ошибки провайдеров,
  `context.DeadlineExceeded`, `context.Canceled` и guard-ошибки.
- `Code` возвращает короткий код для логов и API.
- `SafeMessage` возвращает сообщение без внутренних деталей.
- `IsRetryable` используется общим runner-ом агентов.

## Пример

```go
if llm.IsRetryable(err) {
    // повторить с backoff
}
log.Warn("llm_call_failed", "code", llm.Code(err), "message", llm.SafeMessage(err))
```

## Провайдеры

`openai.UpstreamError` и `ollama.UpstreamError` реализуют `llm.Classified`:
категория и retryability выводятся из HTTP-статуса. Timeout, canceled,
decode и network-ошибки оборачиваются в `llm.DomainError` с сохранением
старых sentinel-ошибок через `Unwrap`.
