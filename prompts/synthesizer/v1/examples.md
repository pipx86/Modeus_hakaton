# Примеры ответов синтезатора

## Пример 1: конфликт названия между интервью и вакансией

Интервью: задача `tax-task-sql` «Писать SQL-запросы».
Вакансия: та же задача `tax-task-sql` «Готовить выгрузки».

Побеждает интервью (приоритет 300 против 100); обе цитаты остаются у узла,
конфликт попадает в `conflicts` и в `messages`.

```json
{
  "tree": [
    {
      "id": "tax-task-sql",
      "type": "task",
      "title": "Писать SQL-запросы",
      "parent_id": "tax-fn-reporting",
      "evidence": [
        {"id": "ev-i1", "source_id": "src-interview-1", "ref_id": "msg-4", "quote": "каждый день пишу SQL-запросы"},
        {"id": "ev-v1", "source_id": "src-vacancy-1", "ref_id": "req-2", "quote": "подготовка выгрузок"}
      ]
    }
  ],
  "conflicts": [
    {
      "id": "c-1",
      "node_id": "tax-task-sql",
      "field": "title",
      "values": [
        {"source_id": "src-interview-1", "source_type": "interview", "value": "Писать SQL-запросы", "priority": 300},
        {"source_id": "src-vacancy-1", "source_type": "vacancy", "value": "Готовить выгрузки", "priority": 100}
      ],
      "winner": {"source_id": "src-interview-1", "source_type": "interview", "value": "Писать SQL-запросы", "priority": 300},
      "reason": "higher source priority wins: interview (300)"
    }
  ],
  "messages": [
    {"id": "msg-1", "kind": "conflict", "text": "Конфликт title узла tax-task-sql", "payload": {"conflict_id": "c-1"}}
  ]
}
```

## Пример 2: сгенерированный узел-родитель

Если из интервью пришла только задача, а функции нет, синтезатор создаёт
функцию и агрегирует evidence из дочерней задачи. Узел без evidence
недопустим.
