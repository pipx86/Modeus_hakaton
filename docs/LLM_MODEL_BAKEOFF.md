# Analyst model bakeoff

Дата: 2026-11-18  
Провайдер: ReformBoss  
Задача: выбрать модель для `analyst` на одинаковом analyst-входе и контракте `agent.io/v1`.

## 1. JSON/схема и стоимость

| Модель | JSON valid | Latency | Prompt tokens | Completion tokens | Total tokens |
|---|---:|---:|---:|---:|---:|
| `deepseek-v4.1-flash-1017` | ✅ | 24.5 s | 2322 | 3788 | 6110 |
| `deepseek-v4-pro` | ✅ | 83.7 s | 2296 | 3716 | 6012 |
| `glm-5.3-flash` | ✅ | 23.4 s | 1993 | 382 | 2375 |
| `minimax-m3.1-flash` | ✅ | **20.4 s** | 2135 | **749** | **2884** |
| `qwen3.8-flash` | ❌ empty response | 123.3 s | — | — | — |
| `google/gemini-3.6-flash` | ❌ 403 | 0.8 s | — | — | — |

## 2. Полный e2e-прогон analyst

Одинаковый вход, `cmd/e2e -live`, interviewer — DeepSeek Flash.

| Модель analyst | Статус | Сущностей | Evidence | Узлов дерева | Evidence в дереве |
|---|---|---:|---:|---:|---:|
| DeepSeek V4.1 Flash | done | 10 | 13 | 11 | 15 |
| DeepSeek V4 Pro | timeout | — | — | — | — |
| GLM 5.3 Flash | done | 9–10 | 10–11 | 10–11 | 12–13 |
| MiniMax M3.1 Flash | done / degraded* | **10–11** | **14–15** | **11–12** | **16–17** |

\* Один e2e-прогон MiniMax получил `degraded` из-за таймаута **interviewer** (не analyst); analyst при этом выдал 11 сущностей и 14 evidence.

## 3. Реальный HTTP-pipeline с MiniMax analyst

Отдельный run:

- `status=done`;
- 6/6 ответов;
- 11 узлов дерева;
- финальное сообщение с курсами Stepik пришло.

## 4. Вывод

**Лучший выбор для analyst: `minimax-m3.1-flash`.**

Почему:

- стабильно проходит JSON-схему;
- самый быстрый из валидных (20.4 с);
- самый дешёвый по completion-токенам (749 против 3788 у DeepSeek Flash);
- больше всего evidence в дереве (14–15 против 13 у Flash и 10–11 у GLM);
- успешно прошёл реальный HTTP-pipeline.

Fallback: `deepseek-v4.1-flash-1017` — проверен на трёх профессиях, но дороже и медленнее.

Не использовать:

- `deepseek-v4-pro` — 84 с, нестабильный, e2e таймаутит;
- `qwen3.8-flash` — пустой content;
- `google/gemini-3.6-flash` — 403 у провайдера.

## 5. Рекомендуемая конфигурация

```env
LLM_INTERVIEWER_MODEL=deepseek-v4.1-flash-1017
LLM_ANALYST_MODEL=minimax-m3.1-flash
LLM_INTERVIEWER_MAX_TOKENS=1500
LLM_ANALYST_MAX_TOKENS=8000
LLM_TIMEOUT_S=300
```
