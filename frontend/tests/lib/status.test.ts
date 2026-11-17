import { describe, expect, it } from "vitest"
import {
  NODE_TYPE_LABEL,
  RUN_STATUS_LABEL,
  RUN_STATUS_VARIANT,
  SOURCE_TYPE_LABEL,
} from "@/lib/status"

describe("справочник статусов исследования", () => {
  it("содержит подпись для каждого статуса", () => {
    expect(RUN_STATUS_LABEL).toEqual({
      pending: "В очереди",
      running: "Выполняется",
      done: "Завершён",
      failed: "Ошибка",
      degraded: "С ограничениями",
    })
  })

  it("сопоставляет статусу вариант бейджа", () => {
    expect(RUN_STATUS_VARIANT).toEqual({
      pending: "outline",
      running: "default",
      done: "secondary",
      failed: "destructive",
      degraded: "outline",
    })
  })
})

describe("справочник типов узлов", () => {
  it("содержит все четыре уровня дерева", () => {
    expect(NODE_TYPE_LABEL).toEqual({
      role: "Роль",
      function: "Функция",
      task: "Задача",
      action: "Действие",
    })
  })
})

describe("справочник типов источников", () => {
  it("содержит все четыре типа контракта", () => {
    expect(SOURCE_TYPE_LABEL).toEqual({
      vacancy: "Вакансия",
      interview: "Интервью",
      standard: "Профстандарт",
      document: "Документ",
    })
  })
})
