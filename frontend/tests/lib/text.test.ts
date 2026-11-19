import { describe, expect, it } from "vitest"
import { normalizeDashes, normalizeText } from "@/lib/text"

describe("нормализация тире", () => {
  it("заменяет длинное и среднее тире на дефис", () => {
    expect(normalizeText("шаг - результат - готово")).toBe("шаг - результат - готово")
  })

  it("рекурсивно чистит строки в ответах API", () => {
    const data = {
      title: "Роль - функция",
      nodes: [{ summary: "Задача - действие" }],
      count: 1,
    }
    expect(normalizeDashes(data)).toEqual({
      title: "Роль - функция",
      nodes: [{ summary: "Задача - действие" }],
      count: 1,
    })
  })
})
